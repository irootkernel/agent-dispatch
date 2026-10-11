package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
)

var (
	ErrManagementNotFound     = errors.New("management handle not found")
	ErrManagementExpired      = errors.New("management draft expired")
	ErrManagementUnauthorized = errors.New("management policy or binding is not current")
)

type ManagementRequest struct {
	RequestID string
	syncmanagement.Binding
	Fingerprint, State                            string
	Revision                                      int64
	CreatedAt, ExpiresAt                          string
	AcceptedAt, ResolvedAt                        *string
	Attempts                                      int
	Fence                                         int64
	ClaimOwner, ClaimExpiresAt, ExecutionDeadline string
	AssociationReserved                           int
	Outcome                                       *syncmanagement.Outcome
	Jobs                                          []ManagementJob
}

type ManagementJob struct {
	JobID, Kind, AssociationReason string
	TargetCommit                   *string
	State                          string
	Attempts                       int
}

const managementColumns = `request_id,group_id,instance_id,state_incarnation_id,action,target_request_id,
 policy_revision,config_revision,expected_control_revision,fingerprint,state,revision,created_at,expires_at,
 accepted_at,resolved_at,attempts,fence,COALESCE(claim_owner,''),claim_expires_at,
 execution_deadline,association_reserved,outcome_json`

type managementQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func managementTime(now time.Time) string { return now.UTC().Format("2006-01-02T15:04:05.000000000Z") }

func loadManagement(ctx context.Context, q managementQuerier, group, id string) (ManagementRequest, error) {
	var r ManagementRequest
	var outcome, claimExpiresAt, executionDeadline sql.NullString
	err := q.QueryRowContext(ctx, `SELECT `+managementColumns+` FROM sync_management_requests WHERE group_id=? AND request_id=?`, group, id).Scan(
		&r.RequestID, &r.GroupID, &r.InstanceID, &r.StateIncarnationID, &r.Action, &r.TargetRequestID,
		&r.PolicyRevision, &r.ConfigRevision, &r.ExpectedControlRevision, &r.Fingerprint, &r.State, &r.Revision, &r.CreatedAt, &r.ExpiresAt,
		&r.AcceptedAt, &r.ResolvedAt, &r.Attempts, &r.Fence, &r.ClaimOwner, &claimExpiresAt, &executionDeadline, &r.AssociationReserved, &outcome)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrManagementNotFound
	}
	if err != nil {
		return r, err
	}
	r.ClaimExpiresAt, r.ExecutionDeadline = claimExpiresAt.String, executionDeadline.String
	times := []string{r.CreatedAt, r.ExpiresAt}
	for _, value := range []sql.NullString{claimExpiresAt, executionDeadline} {
		if value.Valid {
			times = append(times, value.String)
		} else if r.State == "running" {
			return r, fmt.Errorf("missing stored management claim time")
		}
	}
	for _, value := range []*string{r.AcceptedAt, r.ResolvedAt} {
		if value != nil {
			times = append(times, *value)
		}
	}
	for _, value := range times {
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			return r, fmt.Errorf("invalid stored management time: %w", err)
		}
	}
	if outcome.Valid {
		var stored struct {
			Schema  string                 `json:"schema_version"`
			Outcome syncmanagement.Outcome `json:"outcome"`
		}
		if err := json.Unmarshal([]byte(outcome.String), &stored); err != nil {
			return r, err
		}
		if stored.Schema != syncmanagement.Schema {
			return r, fmt.Errorf("unsupported stored management outcome")
		}
		if err := stored.Outcome.Validate(r.Action); err != nil {
			return r, err
		}
		r.Outcome = &stored.Outcome
	}
	return r, nil
}

// beginManagementTx reserves the SQLite writer before reading capacity or
// identity. A deferred read-to-write upgrade must not lose a concurrent submit.
func (s *Store) beginManagementTx(ctx context.Context, id string) (*managementTx, error) {
	conn, err := s.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var busyMS int64
	if err = conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyMS); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, `PRAGMA busy_timeout=0`); err != nil {
		conn.Close()
		return nil, err
	}
	release := func() {
		conn.ExecContext(context.Background(), fmt.Sprintf("PRAGMA busy_timeout=%d", busyMS))
		conn.Close()
	}
	for {
		tx, err := conn.BeginTx(ctx, nil)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE sync_management_requests SET state=state WHERE request_id=?`, id)
			if err == nil {
				return &managementTx{Tx: tx, release: release}, nil
			}
			tx.Rollback()
		}
		if !isBusy(err) {
			release()
			return nil, err
		}
		select {
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// The reserved connection keeps busy-handler changes private to this
// transaction. Returning it restores ordinary callers' connection settings.
type managementTx struct {
	*sql.Tx
	release func()
}

func (tx *managementTx) finish() {
	if tx.release != nil {
		tx.release()
		tx.release = nil
	}
}

func (tx *managementTx) Commit() error {
	err := tx.Tx.Commit()
	tx.finish()
	return err
}

func (tx *managementTx) Rollback() error {
	err := tx.Tx.Rollback()
	tx.finish()
	return err
}

// LoadManagementRequest is an exact bounded snapshot. Draft expiry is derived;
// lookup never writes, starts recovery, or restamps obsolete bindings.
func (s *Store) LoadManagementRequest(ctx context.Context, group, id string, now time.Time) (ManagementRequest, error) {
	ctx, cancel := context.WithTimeout(ctx, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	tx, err := s.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ManagementRequest{}, err
	}
	defer tx.Rollback()
	r, err := loadManagement(ctx, tx, group, id)
	if err != nil {
		return r, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.job_id,a.kind,a.association_reason,a.target_commit,j.state,j.attempts
	 FROM sync_management_jobs a JOIN sync_jobs j ON j.job_id=a.job_id WHERE a.request_id=? ORDER BY a.job_id LIMIT 1001`, id)
	if err != nil {
		return r, err
	}
	r.Jobs = []ManagementJob{}
	for rows.Next() {
		var j ManagementJob
		if err := rows.Scan(&j.JobID, &j.Kind, &j.AssociationReason, &j.TargetCommit, &j.State, &j.Attempts); err != nil {
			rows.Close()
			return r, err
		}
		r.Jobs = append(r.Jobs, j)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return r, err
	}
	if err := rows.Close(); err != nil {
		return r, err
	}
	if len(r.Jobs) > syncmanagement.AssociationLimit {
		return r, ErrSyncQueueFull
	}
	if r.State == "prepared" && !now.Before(mustManagementTime(r.ExpiresAt)) {
		r.State = "expired"
	}
	return r, tx.Commit()
}

// All management times are written by these typed primitives, never supplied
// as unvalidated timestamp strings. Corrupt persisted time fails closed.
func mustManagementTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

// sameManagementAuthority compares the current operator context. Action, retry
// target and expected control revision come from the immutable stored request;
// this check never replaces those parameters with a caller's current binding.
func sameManagementAuthority(a, b syncmanagement.Binding) bool {
	return a.GroupID == b.GroupID && a.InstanceID == b.InstanceID && a.StateIncarnationID == b.StateIncarnationID && a.PolicyRevision == b.PolicyRevision && a.ConfigRevision == b.ConfigRevision
}

// PrepareManagementRequest persists only an immutable, expiring draft. Retained
// expired drafts count toward the same bound until eligible maintenance prunes
// them, preventing lost responses from creating unbounded draft storage.
func (s *Store) PrepareManagementRequest(ctx context.Context, id string, b syncmanagement.Binding, now time.Time) (ManagementRequest, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	if !syncmanagement.ValidHandle(id) || now.IsZero() {
		return ManagementRequest{}, false, fmt.Errorf("invalid management preparation")
	}
	fp, err := b.Fingerprint()
	if err != nil {
		return ManagementRequest{}, false, err
	}
	tx, err := s.beginManagementTx(ctx, id)
	if err != nil {
		return ManagementRequest{}, false, err
	}
	defer tx.Rollback()
	r, err := loadManagement(ctx, tx, b.GroupID, id)
	if err == nil {
		if r.Fingerprint != fp {
			return r, false, ErrSyncAdmissionConflict
		}
		return r, true, tx.Commit()
	}
	if !errors.Is(err, ErrManagementNotFound) {
		return r, false, err
	}
	if b.TargetRequestID != nil {
		prev, err := loadManagement(ctx, tx, b.GroupID, *b.TargetRequestID)
		if err != nil {
			return r, false, err
		}
		if prev.AcceptedAt == nil || !sameManagementAuthority(prev.Binding, b) {
			return r, false, ErrManagementUnauthorized
		}
	}
	class := syncmanagement.IsControl(b.Action)
	limit := syncmanagement.DataDraftLimit
	if class {
		limit = syncmanagement.ControlDraftLimit
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_management_requests WHERE group_id=? AND (action IN ('pause','resume'))=? AND accepted_at IS NULL`, b.GroupID, class).Scan(&n); err != nil {
		return r, false, err
	}
	if n >= limit {
		return r, false, ErrSyncQueueFull
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sync_management_requests(request_id,group_id,instance_id,state_incarnation_id,action,target_request_id,policy_revision,config_revision,expected_control_revision,fingerprint,state,created_at,expires_at)
	 VALUES(?,?,?,?,?,?,?,?,?,?,'prepared',?,?)`, id, b.GroupID, b.InstanceID, b.StateIncarnationID, b.Action, b.TargetRequestID, b.PolicyRevision, b.ConfigRevision, b.ExpectedControlRevision, fp, managementTime(now), managementTime(now.Add(syncmanagement.DraftTTLSeconds*time.Second)))
	if err != nil {
		return r, false, err
	}
	r, err = loadManagement(ctx, tx, b.GroupID, id)
	if err != nil {
		return r, false, err
	}
	return r, false, tx.Commit()
}

type ManagementAdmission struct {
	GroupID, RequestID string
	CurrentBinding     syncmanagement.Binding
	Authorized         bool
	QueueLimit         int
	// This is the existing SyncRevision, not the full management config digest.
	ControlSyncRevision string
	// Clock is sampled after acquiring the writer; nil uses time.Now.
	Clock func() time.Time
}

// AcceptManagementRequest returns accepted duplicates before policy/capacity
// checks. New async work becomes queued only at commit. Controls use the same
// transaction helper as the operator CLI, with acceptance and result atomic.
// The application owns bounded resume safety checks and its resource guard.
func (s *Store) AcceptManagementRequest(ctx context.Context, in ManagementAdmission) (ManagementRequest, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	tx, err := s.beginManagementTx(ctx, in.RequestID)
	if err != nil {
		return ManagementRequest{}, false, err
	}
	defer tx.Rollback()
	r, err := loadManagement(ctx, tx, in.GroupID, in.RequestID)
	if err != nil {
		return r, false, err
	}
	if r.AcceptedAt != nil {
		return r, true, tx.Commit()
	}
	clock := in.Clock
	if clock == nil {
		clock = time.Now
	}
	now := clock()
	if now.IsZero() || r.State != "prepared" || !now.Before(mustManagementTime(r.ExpiresAt)) {
		return r, false, ErrManagementExpired
	}
	if !in.Authorized || !sameManagementAuthority(r.Binding, in.CurrentBinding) {
		return r, false, ErrManagementUnauthorized
	}
	if in.QueueLimit < syncmanagement.DataQueueMin || in.QueueLimit > syncmanagement.DataQueueMax {
		return r, false, fmt.Errorf("management queue limit must be 2..1000")
	}
	class := syncmanagement.IsControl(r.Action)
	queue, terminal := in.QueueLimit, syncmanagement.DataTerminalLimit
	if class {
		queue, terminal = syncmanagement.ControlQueueLimit, syncmanagement.ControlTerminalLimit
	}
	var unresolved, finished int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(state IN ('queued','running','blocked','uncertain')),0),COALESCE(SUM(state='finished'),0)
	 FROM sync_management_requests WHERE group_id=? AND (action IN ('pause','resume'))=?`, r.GroupID, class).Scan(&unresolved, &finished); err != nil {
		return r, false, err
	}
	if unresolved >= queue || unresolved+finished >= terminal {
		return r, false, ErrSyncQueueFull
	}
	at := managementTime(now)
	state := "queued"
	var outcomeJSON any
	var resolved any
	if class {
		target := "paused"
		if r.Action == "resume" {
			target = "active"
		}
		if _, err := ensureSyncControlTx(ctx, tx.Tx, r.GroupID, in.ControlSyncRevision, at); err != nil {
			return r, false, err
		}
		control, err := setSyncControlTx(ctx, tx.Tx, r.GroupID, *r.ExpectedControlRevision, target, in.ControlSyncRevision, at)
		if err != nil {
			return r, false, err
		}
		if control.State == "blocked" {
			return r, false, ErrSyncControlHeld
		}
		q := "unknown"
		o := syncmanagement.Outcome{Status: "intent_committed", ControlRevision: &control.Revision, Quiescence: &q, Reason: "local control intent committed"}
		outcomeJSON, err = managementOutcomeJSON(r.Action, &o)
		if err != nil {
			return r, false, err
		}
		state = "finished"
		resolved = at
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_management_acceptances(request_id,accepted_at,fingerprint) VALUES(?,?,?)`, r.RequestID, at, r.Fingerprint); err != nil {
		return r, false, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_management_requests SET state=?,revision=revision+1,accepted_at=?,resolved_at=?,outcome_json=? WHERE request_id=? AND state='prepared' AND revision=?`, state, at, resolved, outcomeJSON, r.RequestID, r.Revision)
	if err != nil {
		return r, false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return r, false, ErrSyncPrecondition
	}
	r, err = loadManagement(ctx, tx, r.GroupID, r.RequestID)
	if err != nil {
		return r, false, err
	}
	return r, false, tx.Commit()
}

func managementOutcomeJSON(action string, o *syncmanagement.Outcome) (any, error) {
	if o == nil {
		return nil, nil
	}
	if err := o.Validate(action); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(struct {
		Schema  string                  `json:"schema_version"`
		Outcome *syncmanagement.Outcome `json:"outcome"`
	}{syncmanagement.Schema, o})
	return string(raw), err
}

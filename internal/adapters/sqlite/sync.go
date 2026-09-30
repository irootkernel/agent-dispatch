package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

var (
	// ErrSyncAdmissionConflict means a logical key was already admitted with
	// different immutable request bytes.
	ErrSyncAdmissionConflict = errors.New("sync admission fingerprint conflict")
	// ErrSyncPrecondition means a revision, owner, or fencing generation no
	// longer matches the durable row.
	ErrSyncPrecondition = errors.New("sync precondition failed")
	// ErrSyncControlHeld means protected work cannot start while the group is
	// paused or blocked.
	ErrSyncControlHeld = errors.New("sync control prevents protected work")
	// ErrSyncQueueFull keeps an exhausted obligation visible instead of
	// silently dropping or truncating it.
	ErrSyncQueueFull = errors.New("sync queue bound exhausted")
)

// SyncControlRow is the durable group control record.
type SyncControlRow struct {
	GroupID        string `json:"group_id"`
	Revision       int64  `json:"revision"`
	State          string `json:"state"`
	Reason         string `json:"reason"`
	MembershipMode string `json:"membership_mode"`
	ConfigRevision string `json:"config_revision"`
	UpdatedAt      string `json:"updated_at"`
}

// SyncJobInput is one immutable logical admission.
type SyncJobInput struct {
	JobID                  string
	GroupID                string
	Kind                   string
	LogicalKey             string
	InitialState           string
	PayloadJSON            string
	ConfigRevision         string
	QueueLimit             int
	Now                    string
	PublicationResourceID  string
	ExpectedSourceRevision int64
}

// SyncJobRow is the queryable durable job head. Recovery evidence lives in
// append-only SyncJournalEntry rows and is never overwritten with the head.
type SyncJobRow struct {
	JobID               string `json:"job_id"`
	GroupID             string `json:"group_id"`
	Kind                string `json:"kind"`
	LogicalKey          string `json:"logical_key"`
	RequestFingerprint  string `json:"request_fingerprint"`
	State               string `json:"state"`
	PayloadJSON         string `json:"payload_json"`
	Attempts            int    `json:"attempts"`
	ClaimOwner          string `json:"claim_owner,omitempty"`
	ClaimExpiresAt      string `json:"claim_expires_at,omitempty"`
	Fence               int64  `json:"fence"`
	RetainUntilResolved bool   `json:"retain_until_resolved"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
	ResolvedAt          string `json:"resolved_at,omitempty"`
}

// SyncJournalEntry is immutable recovery evidence for one fenced attempt.
type SyncJournalEntry struct {
	Sequence     int64  `json:"sequence"`
	JournalID    string `json:"journal_id"`
	JobID        string `json:"job_id"`
	Fence        int64  `json:"fence"`
	Phase        string `json:"phase"`
	Outcome      string `json:"outcome"`
	EvidenceJSON string `json:"evidence_json"`
	RecordedAt   string `json:"recorded_at"`
}

// SyncJobDisposition makes retention an explicit terminal decision rather
// than an unlabelled boolean at the storage boundary.
type SyncJobDisposition uint8

const (
	SyncJobKeepUnresolved SyncJobDisposition = iota
	SyncJobResolve
)

// PublicationEligibility is one transactionally observed maintenance barrier.
type PublicationEligibility struct {
	SourceRevision int64
	ReceiptIDs     []string
	PathDigests    map[string]string
}

// ResourceWritersIdle proves that no maintained route for the resource owns
// an active dispatch slot. The resource file guard separately serializes
// Watchman planning, publication snapshotting, and import application.
func (s *Store) ResourceWritersIdle(ctx context.Context, resourceID string) (bool, error) {
	var active int
	err := s.QueryRowContext(ctx, `SELECT COUNT(*) FROM routes r
		LEFT JOIN route_runtime_state rs ON rs.route_id=r.route_id
		LEFT JOIN destination_lane_state dl ON dl.route_id=r.route_id
		WHERE r.resource_id=? AND (rs.active_dispatch_id IS NOT NULL OR dl.active_dispatch_id IS NOT NULL)`, resourceID).Scan(&active)
	return active == 0, err
}

type ImportEffectRow struct {
	JobID, ResourceID, Path, Before, After, AppliedAt string
	Fence                                             int64
}

// LoadPublicationEligibility requires every enabled route governing the
// resource to be idle and backed by a valid completed receipt for its current
// route revision. The resource observation revision and receipt set are read
// from the same SQLite snapshot.
func (s *Store) LoadPublicationEligibility(ctx context.Context, resourceID string) (PublicationEligibility, error) {
	tx, err := s.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return PublicationEligibility{}, err
	}
	defer tx.Rollback()
	var out PublicationEligibility
	out.PathDigests = map[string]string{}
	if err := tx.QueryRowContext(ctx, `SELECT observation_revision FROM resources WHERE resource_id = ?`, resourceID).Scan(&out.SourceRevision); err != nil {
		return out, err
	}
	if out.SourceRevision < 1 {
		return out, fmt.Errorf("resource has no maintained observation revision: %w", ErrSyncPrecondition)
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.route_id, r.revision, COALESCE(rrs.activation_state,''), COALESCE(rrs.route_state,''), COALESCE(rrs.pending_reconcile,1)
		FROM routes r LEFT JOIN route_runtime_state rrs ON rrs.route_id=r.route_id WHERE r.resource_id=? ORDER BY r.route_id`, resourceID)
	if err != nil {
		return out, err
	}
	type route struct {
		id, revision, activation, state string
		pending                         int
	}
	var routes []route
	for rows.Next() {
		var r route
		if err := rows.Scan(&r.id, &r.revision, &r.activation, &r.state, &r.pending); err != nil {
			rows.Close()
			return out, err
		}
		routes = append(routes, r)
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	enabled := 0
	for _, r := range routes {
		if r.activation == "disabled" {
			continue
		}
		if r.activation != "enabled" || r.state != "IDLE" || r.pending != 0 {
			return out, fmt.Errorf("route %s is not idle and maintained: %w", r.id, ErrSyncPrecondition)
		}
		enabled++
		var receipt string
		err := tx.QueryRowContext(ctx, `SELECT w.receipt_id FROM work_receipts w JOIN dispatch_intents i ON i.dispatch_id=w.dispatch_id
			WHERE i.route_id=? AND w.resource_id=? AND w.route_revision=? AND w.status='completed' AND w.validation_state='valid'
			ORDER BY w.submitted_at DESC,w.receipt_id DESC LIMIT 1`, r.id, resourceID, r.revision).Scan(&receipt)
		if errors.Is(err, sql.ErrNoRows) {
			return out, fmt.Errorf("route %s has no current valid completed receipt: %w", r.id, ErrSyncPrecondition)
		}
		if err != nil {
			return out, err
		}
		out.ReceiptIDs = append(out.ReceiptIDs, receipt)
	}
	if enabled == 0 {
		return out, fmt.Errorf("resource has no enabled maintained route: %w", ErrSyncPrecondition)
	}
	if len(out.ReceiptIDs) > 100 {
		return out, fmt.Errorf("publication receipt evidence exceeds 100 entries: %w", ErrSyncPrecondition)
	}
	sort.Strings(out.ReceiptIDs)
	facts, err := tx.QueryContext(ctx, `SELECT path,digest FROM path_facts WHERE resource_id=? AND "exists"=1 ORDER BY path`, resourceID)
	if err != nil {
		return out, err
	}
	for facts.Next() {
		var path string
		var digest sql.NullString
		if err := facts.Scan(&path, &digest); err != nil {
			facts.Close()
			return out, err
		}
		if digest.Valid && digest.String != "" {
			out.PathDigests[path] = digest.String
		}
	}
	if err := facts.Close(); err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

// EnsureSyncControl materializes a group's initial active control without
// changing an existing operator or safety decision.
func (s *Store) EnsureSyncControl(ctx context.Context, groupID, configRevision, now string) (SyncControlRow, error) {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return SyncControlRow{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO sync_controls
		(group_id, revision, state, reason, config_revision, updated_at)
		VALUES (?, 1, 'active', 'none', ?, ?)
		ON CONFLICT(group_id) DO NOTHING`, groupID, configRevision, now)
	if err != nil {
		return SyncControlRow{}, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		contextJSON, _ := json.Marshal(map[string]any{"reason": "none", "config_revision": configRevision, "revision": 1})
		if _, err := tx.ExecContext(ctx, `INSERT INTO state_transitions
			(transition_id, entity_type, entity_id, from_state, to_state, recorded_at, context_json)
			VALUES (?,?,?,?,?,?,?)`, "sync-control:"+groupID+":1", "sync_control", groupID, nil, "active", now, string(contextJSON)); err != nil {
			return SyncControlRow{}, err
		}
	}
	var row SyncControlRow
	if err := tx.QueryRowContext(ctx, `SELECT group_id, revision, state, reason, membership_mode, config_revision, updated_at
		FROM sync_controls WHERE group_id = ?`, groupID).Scan(
		&row.GroupID, &row.Revision, &row.State, &row.Reason, &row.MembershipMode, &row.ConfigRevision, &row.UpdatedAt); err != nil {
		return SyncControlRow{}, err
	}
	if err := tx.Commit(); err != nil {
		return SyncControlRow{}, err
	}
	return row, nil
}

// LoadSyncControl returns one group control record.
func (s *Store) LoadSyncControl(ctx context.Context, groupID string) (SyncControlRow, error) {
	var row SyncControlRow
	err := s.QueryRowContext(ctx, `SELECT group_id, revision, state, reason, membership_mode, config_revision, updated_at
		FROM sync_controls WHERE group_id = ?`, groupID).Scan(
		&row.GroupID, &row.Revision, &row.State, &row.Reason, &row.MembershipMode, &row.ConfigRevision, &row.UpdatedAt)
	return row, err
}

// SetSyncControl performs the operator active/paused transition under an exact
// revision. It never rewrites a blocked safety state.
func (s *Store) SetSyncControl(ctx context.Context, groupID string, expectedRevision int64, targetState, configRevision, now string) (SyncControlRow, error) {
	if targetState != "active" && targetState != "paused" {
		return SyncControlRow{}, fmt.Errorf("unsupported sync control target %q", targetState)
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return SyncControlRow{}, err
	}
	defer tx.Rollback()
	var current SyncControlRow
	if err := tx.QueryRowContext(ctx, `SELECT group_id, revision, state, reason, membership_mode, config_revision, updated_at
		FROM sync_controls WHERE group_id = ?`, groupID).Scan(
		&current.GroupID, &current.Revision, &current.State, &current.Reason, &current.MembershipMode, &current.ConfigRevision, &current.UpdatedAt); err != nil {
		return SyncControlRow{}, err
	}
	if current.Revision != expectedRevision {
		return SyncControlRow{}, fmt.Errorf("control revision is %d, expected %d: %w", current.Revision, expectedRevision, ErrSyncPrecondition)
	}
	if current.State == "blocked" {
		return SyncControlRow{}, fmt.Errorf("control is blocked for %s and requires its recovery evidence: %w", current.Reason, ErrSyncControlHeld)
	}
	if targetState == "active" && current.MembershipMode == "blocked_emergency" {
		targetState = "blocked"
	}
	if current.State == targetState && (targetState != "active" || current.ConfigRevision == configRevision) {
		return current, nil
	}
	if targetState == "active" && current.State != "paused" && current.State != "active" {
		return SyncControlRow{}, fmt.Errorf("control cannot resume from %s: %w", current.State, ErrSyncPrecondition)
	}
	reason := "none"
	if targetState == "paused" {
		reason = "operator_pause"
	} else if targetState == "blocked" {
		reason = "membership_emergency"
	}
	boundConfigRevision := configRevision
	if targetState == "paused" {
		boundConfigRevision = current.ConfigRevision
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_controls
		SET revision = revision + 1, state = ?, reason = ?, config_revision = ?, updated_at = ?
		WHERE group_id = ? AND revision = ? AND state != 'blocked'`, targetState, reason, boundConfigRevision, now, groupID, expectedRevision)
	if err != nil {
		return SyncControlRow{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return SyncControlRow{}, ErrSyncPrecondition
	}
	contextJSON, _ := json.Marshal(map[string]any{"reason": reason, "membership_mode": current.MembershipMode, "config_revision": boundConfigRevision, "revision": expectedRevision + 1})
	if _, err := tx.ExecContext(ctx, `INSERT INTO state_transitions
		(transition_id, entity_type, entity_id, from_state, to_state, recorded_at, context_json)
		VALUES (?,?,?,?,?,?,?)`, fmt.Sprintf("sync-control:%s:%d", groupID, expectedRevision+1), "sync_control", groupID, current.State, targetState, now, string(contextJSON)); err != nil {
		return SyncControlRow{}, err
	}
	current.Revision++
	current.State, current.Reason = targetState, reason
	current.ConfigRevision, current.UpdatedAt = boundConfigRevision, now
	if err := tx.Commit(); err != nil {
		return SyncControlRow{}, err
	}
	return current, nil
}

// HoldSyncControl records a safety block even when an operator pause is
// active. Repeating the same block is idempotent; ordinary resume cannot clear
// it.
func (s *Store) HoldSyncControl(ctx context.Context, groupID, reason, configRevision, now string) (SyncControlRow, error) {
	if reason != "conflict" && reason != "trust_failure" && reason != "recovery_required" {
		return SyncControlRow{}, fmt.Errorf("unsupported sync safety hold %q", reason)
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return SyncControlRow{}, err
	}
	defer tx.Rollback()
	var row SyncControlRow
	if err := tx.QueryRowContext(ctx, `SELECT group_id,revision,state,reason,membership_mode,config_revision,updated_at FROM sync_controls WHERE group_id=?`, groupID).Scan(&row.GroupID, &row.Revision, &row.State, &row.Reason, &row.MembershipMode, &row.ConfigRevision, &row.UpdatedAt); err != nil {
		return SyncControlRow{}, err
	}
	if row.State == "blocked" && row.Reason == reason && row.ConfigRevision == configRevision {
		return row, nil
	}
	// Preserve the strongest established safety reason. New evidence may
	// escalate conflict -> recovery -> trust, but a later secondary symptom
	// must not hide the reason that already closed the gate.
	if row.State == "blocked" && syncHoldPriority(reason) < syncHoldPriority(row.Reason) {
		reason = row.Reason
	}
	previous := row.State
	row.Revision++
	row.State, row.Reason, row.ConfigRevision, row.UpdatedAt = "blocked", reason, configRevision, now
	if _, err := tx.ExecContext(ctx, `UPDATE sync_controls SET revision=?,state='blocked',reason=?,config_revision=?,updated_at=? WHERE group_id=?`, row.Revision, reason, configRevision, now, groupID); err != nil {
		return SyncControlRow{}, err
	}
	contextJSON, _ := json.Marshal(map[string]any{"reason": reason, "config_revision": configRevision, "revision": row.Revision})
	if _, err := tx.ExecContext(ctx, `INSERT INTO state_transitions(transition_id,entity_type,entity_id,from_state,to_state,recorded_at,context_json) VALUES (?,?,?,?,?,?,?)`, fmt.Sprintf("sync-control:%s:%d", groupID, row.Revision), "sync_control", groupID, previous, "blocked", now, string(contextJSON)); err != nil {
		return SyncControlRow{}, err
	}
	if err := tx.Commit(); err != nil {
		return SyncControlRow{}, err
	}
	return row, nil
}

func syncHoldPriority(reason string) int {
	switch reason {
	case "trust_failure":
		return 3
	case "recovery_required":
		return 2
	case "conflict":
		return 1
	default:
		return 0
	}
}

// ReconcileAdoptedMembership applies the protected-effect posture of a
// verified membership revision. Emergency mode is armed before the caller
// moves its local membership ref; normal mode clears only an earlier
// membership emergency after that ref has been adopted.
func (s *Store) ReconcileAdoptedMembership(ctx context.Context, groupID, mode, membershipRevision, configRevision, now string) (SyncControlRow, error) {
	return s.reconcileAdoptedMembership(ctx, groupID, mode, membershipRevision, configRevision, now, true)
}

// RefreshAdoptedMembership repairs posture for an already-equal ref without
// claiming that any blocked membership job was replaced by this invocation.
func (s *Store) RefreshAdoptedMembership(ctx context.Context, groupID, mode, membershipRevision, configRevision, now string) (SyncControlRow, error) {
	return s.reconcileAdoptedMembership(ctx, groupID, mode, membershipRevision, configRevision, now, false)
}

func (s *Store) reconcileAdoptedMembership(ctx context.Context, groupID, mode, membershipRevision, configRevision, now string, resolveReplacement bool) (SyncControlRow, error) {
	if (mode != "normal" && mode != "blocked_emergency") || membershipRevision == "" {
		return SyncControlRow{}, ErrSyncPrecondition
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return SyncControlRow{}, err
	}
	defer tx.Rollback()
	var control SyncControlRow
	if err := tx.QueryRowContext(ctx, `SELECT group_id,revision,state,reason,membership_mode,config_revision,updated_at FROM sync_controls WHERE group_id=?`, groupID).Scan(&control.GroupID, &control.Revision, &control.State, &control.Reason, &control.MembershipMode, &control.ConfigRevision, &control.UpdatedAt); err != nil {
		return SyncControlRow{}, err
	}
	if err := applyMembershipPosture(ctx, tx, &control, mode, "membership_revision", membershipRevision, configRevision, now); err != nil {
		return SyncControlRow{}, err
	}
	if resolveReplacement && mode == "normal" {
		if err := resolveBlockedSyncJobs(ctx, tx, groupID, "membership_replaced", membershipRevision, now); err != nil {
			return SyncControlRow{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return SyncControlRow{}, err
	}
	return control, nil
}

// ReconcileSyncControlCheckpoint clears only a conflict/trust/recovery hold
// after the caller has verified the exact administrator checkpoint at the
// local and approved remote head.
func (s *Store) ReconcileSyncControlCheckpoint(ctx context.Context, groupID string, expectedRevision int64, configRevision, checkpointCommit, now string) (SyncControlRow, error) {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return SyncControlRow{}, err
	}
	defer tx.Rollback()
	var row SyncControlRow
	if err := tx.QueryRowContext(ctx, `SELECT group_id,revision,state,reason,membership_mode,config_revision,updated_at FROM sync_controls WHERE group_id=?`, groupID).Scan(&row.GroupID, &row.Revision, &row.State, &row.Reason, &row.MembershipMode, &row.ConfigRevision, &row.UpdatedAt); err != nil {
		return SyncControlRow{}, err
	}
	if row.Revision != expectedRevision || row.State != "blocked" || (row.Reason != "conflict" && row.Reason != "trust_failure" && row.Reason != "recovery_required") || checkpointCommit == "" {
		return SyncControlRow{}, ErrSyncPrecondition
	}
	previous := row.State
	targetState, targetReason := "active", "none"
	if row.MembershipMode == "blocked_emergency" {
		targetState, targetReason = "blocked", "membership_emergency"
	}
	row.Revision++
	row.State, row.Reason, row.ConfigRevision, row.UpdatedAt = targetState, targetReason, configRevision, now
	res, err := tx.ExecContext(ctx, `UPDATE sync_controls SET revision=?,state=?,reason=?,config_revision=?,updated_at=? WHERE group_id=? AND revision=? AND state='blocked'`, row.Revision, targetState, targetReason, configRevision, now, groupID, expectedRevision)
	if err != nil {
		return SyncControlRow{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return SyncControlRow{}, ErrSyncPrecondition
	}
	contextJSON, _ := json.Marshal(map[string]any{"reason": "checkpoint_reconciled", "checkpoint_commit": checkpointCommit, "membership_mode": row.MembershipMode, "result_reason": targetReason, "config_revision": configRevision, "revision": row.Revision})
	if _, err := tx.ExecContext(ctx, `INSERT INTO state_transitions(transition_id,entity_type,entity_id,from_state,to_state,recorded_at,context_json) VALUES (?,?,?,?,?,?,?)`, fmt.Sprintf("sync-control:%s:%d", groupID, row.Revision), "sync_control", groupID, previous, targetState, now, string(contextJSON)); err != nil {
		return SyncControlRow{}, err
	}
	if err := resolveBlockedSyncJobs(ctx, tx, groupID, "checkpoint_reconciled", checkpointCommit, now); err != nil {
		return SyncControlRow{}, err
	}
	if err := tx.Commit(); err != nil {
		return SyncControlRow{}, err
	}
	return row, nil
}

// AdmitSyncJob persists intent before an external effect. Repeating the same
// logical key and fingerprint returns the existing identity; changed immutable
// input is a conflict. Queue exhaustion leaves all existing rows intact.
func (s *Store) AdmitSyncJob(ctx context.Context, in SyncJobInput) (SyncJobRow, bool, error) {
	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		row, reused, err := s.admitSyncJobOnce(ctx, in)
		if err == nil || !isBusy(err) {
			return row, reused, err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return SyncJobRow{}, false, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return SyncJobRow{}, false, &syncBusyError{err: lastErr}
}

// syncBusyError preserves the retryable SQLite cause after the bounded
// cross-process admission convergence window is exhausted.
type syncBusyError struct{ err error }

func (e *syncBusyError) Error() string { return fmt.Sprintf("sync admission remained busy: %v", e.err) }
func (e *syncBusyError) Unwrap() error { return e.err }

func (s *Store) admitSyncJobOnce(ctx context.Context, in SyncJobInput) (SyncJobRow, bool, error) {
	if in.QueueLimit < 1 || in.QueueLimit > 1000 {
		return SyncJobRow{}, false, fmt.Errorf("sync queue limit must be 1..1000")
	}
	if !validSyncState(in.Kind, in.InitialState) {
		return SyncJobRow{}, false, fmt.Errorf("state %q is invalid for sync job kind %q", in.InitialState, in.Kind)
	}
	fingerprint, canonicalPayload, err := syncRequestFingerprint(in.Kind, []byte(in.PayloadJSON))
	if err != nil {
		return SyncJobRow{}, false, err
	}
	in.PayloadJSON = canonicalPayload
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return SyncJobRow{}, false, err
	}
	defer tx.Rollback()
	row, err := loadSyncJobByKey(ctx, tx, in.GroupID, in.Kind, in.LogicalKey)
	if err == nil {
		if row.RequestFingerprint != fingerprint {
			return SyncJobRow{}, false, fmt.Errorf("logical key %q already has a different request fingerprint: %w", in.LogicalKey, ErrSyncAdmissionConflict)
		}
		return row, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SyncJobRow{}, false, err
	}
	if in.Kind == "publication" && in.PublicationResourceID != "" {
		var revision int64
		if err := tx.QueryRowContext(ctx, `SELECT observation_revision FROM resources WHERE resource_id=?`, in.PublicationResourceID).Scan(&revision); err != nil {
			return SyncJobRow{}, false, err
		}
		if revision != in.ExpectedSourceRevision {
			return SyncJobRow{}, false, fmt.Errorf("publication source revision changed: %w", ErrSyncPrecondition)
		}
	}
	var state, membershipMode, controlConfigRevision string
	if err := tx.QueryRowContext(ctx, `SELECT state, membership_mode, config_revision FROM sync_controls WHERE group_id = ?`, in.GroupID).Scan(&state, &membershipMode, &controlConfigRevision); err != nil {
		return SyncJobRow{}, false, err
	}
	if in.ConfigRevision != "" && controlConfigRevision != in.ConfigRevision {
		return SyncJobRow{}, false, fmt.Errorf("control configuration revision changed: %w", ErrSyncPrecondition)
	}
	if in.Kind == "publication" && in.PublicationResourceID != "" && (state != "active" || membershipMode != "normal") {
		return SyncJobRow{}, false, fmt.Errorf("sync group %s is %s: %w", in.GroupID, state, ErrSyncControlHeld)
	}
	pending, err := countUnresolvedSyncObligations(ctx, tx, in.GroupID)
	if err != nil {
		return SyncJobRow{}, false, err
	}
	if pending >= in.QueueLimit {
		return SyncJobRow{}, false, fmt.Errorf("group %s has %d unresolved obligations (limit %d): %w", in.GroupID, pending, in.QueueLimit, ErrSyncQueueFull)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_jobs
		(job_id, group_id, kind, logical_key, request_fingerprint, state, payload_json, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, in.JobID, in.GroupID, in.Kind, in.LogicalKey, fingerprint, in.InitialState, in.PayloadJSON, in.Now, in.Now); err != nil {
		_ = tx.Rollback()
		existing, loadErr := loadSyncJobByKey(ctx, s.DB, in.GroupID, in.Kind, in.LogicalKey)
		if loadErr == nil {
			if existing.RequestFingerprint == fingerprint {
				return existing, true, nil
			}
			return SyncJobRow{}, false, fmt.Errorf("logical key %q already has a different request fingerprint: %w", in.LogicalKey, ErrSyncAdmissionConflict)
		}
		return SyncJobRow{}, false, err
	}
	row, err = loadSyncJobByKey(ctx, tx, in.GroupID, in.Kind, in.LogicalKey)
	if err != nil {
		return SyncJobRow{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return SyncJobRow{}, false, err
	}
	return row, false, nil
}

// countUnresolvedSyncObligations shares one group queue between jobs and peer
// nudges. Admission reads this count in the same transaction as its insert.
func countUnresolvedSyncObligations(ctx context.Context, tx *sql.Tx, groupID string) (int, error) {
	var pending int
	err := tx.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM sync_jobs WHERE group_id=? AND resolved_at IS NULL) +
		(SELECT COUNT(*) FROM sync_peer_nudges WHERE group_id=? AND processed_at IS NULL)`,
		groupID, groupID).Scan(&pending)
	return pending, err
}

// ClaimSyncJob obtains a new fencing generation. An expired lease permits a
// new claim only through this increment; it never proves the old effect safe.
func (s *Store) ClaimSyncJob(ctx context.Context, jobID, owner, expectedConfigRevision, now, expiresAt string) (SyncJobRow, error) {
	return s.claimSyncJob(ctx, jobID, owner, expectedConfigRevision, now, expiresAt, false)
}

// ClaimSyncAdministrationJob permits the administration matrix implemented by
// administrationRepairAllowed: emergency membership replacement, publication
// recovery through conflict, and checkpoint or import work through conflict,
// trust, or recovery holds. Publication and import recovery callers prove their
// exact candidate before claiming; checkpoint apply creates its signed
// candidate after claiming. Delivery is never admitted here.
func (s *Store) ClaimSyncAdministrationJob(ctx context.Context, jobID, owner, expectedConfigRevision, now, expiresAt string) (SyncJobRow, error) {
	return s.claimSyncJob(ctx, jobID, owner, expectedConfigRevision, now, expiresAt, true)
}

func (s *Store) claimSyncJob(ctx context.Context, jobID, owner, expectedConfigRevision, now, expiresAt string, allowMembershipRepair bool) (SyncJobRow, error) {
	if owner == "" || expiresAt == "" {
		return SyncJobRow{}, fmt.Errorf("claim owner and expiry are required")
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return SyncJobRow{}, err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return SyncJobRow{}, err
	}
	var controlState, controlReason, controlMembershipMode, controlConfigRevision string
	if err := tx.QueryRowContext(ctx, `SELECT state, reason, membership_mode, config_revision FROM sync_controls WHERE group_id = ?`, row.GroupID).Scan(&controlState, &controlReason, &controlMembershipMode, &controlConfigRevision); err != nil {
		return SyncJobRow{}, err
	}
	controllerRepair := false
	if allowMembershipRepair && row.Kind == "import" {
		if record, decodeErr := syncrecords.DecodeImport([]byte(row.PayloadJSON)); decodeErr == nil {
			controllerRepair = record.ControllerOnly
		}
	}
	allowedRepair := allowMembershipRepair && administrationRepairAllowed(row.Kind, controllerRepair, controlState, controlReason, controlMembershipMode)
	if controlState != "active" && !allowedRepair {
		return SyncJobRow{}, fmt.Errorf("sync group %s is %s: %w", row.GroupID, controlState, ErrSyncControlHeld)
	}
	if expectedConfigRevision == "" || controlConfigRevision != expectedConfigRevision {
		return SyncJobRow{}, fmt.Errorf("sync control configuration binding is stale: %w", ErrSyncControlHeld)
	}
	if row.ResolvedAt != "" || !claimableSyncState(row.Kind, row.State) {
		return SyncJobRow{}, fmt.Errorf("job %s in state %s is not claimable: %w", jobID, row.State, ErrSyncPrecondition)
	}
	if row.Attempts >= 20 {
		return SyncJobRow{}, fmt.Errorf("job %s exhausted 20 attempts: %w", jobID, ErrSyncQueueFull)
	}
	if row.ClaimOwner != "" {
		if timeBefore(row.ClaimExpiresAt, now) {
			return SyncJobRow{}, fmt.Errorf("job %s has an expired claim at fence %d; record recovery evidence before takeover: %w", jobID, row.Fence, ErrSyncPrecondition)
		}
		return SyncJobRow{}, fmt.Errorf("job %s is claimed by %s through %s: %w", jobID, row.ClaimOwner, row.ClaimExpiresAt, ErrSyncPrecondition)
	}
	claims, err := tx.QueryContext(ctx, `SELECT claim_expires_at FROM sync_jobs WHERE group_id=? AND job_id<>? AND claim_owner IS NOT NULL`, row.GroupID, row.JobID)
	if err != nil {
		return SyncJobRow{}, err
	}
	for claims.Next() {
		var claimExpiresAt string
		if err := claims.Scan(&claimExpiresAt); err != nil {
			claims.Close()
			return SyncJobRow{}, err
		}
		if !timeBefore(claimExpiresAt, now) {
			claims.Close()
			return SyncJobRow{}, fmt.Errorf("sync group %s already has a protected effect claim: %w", row.GroupID, ErrSyncPrecondition)
		}
	}
	if err := claims.Close(); err != nil {
		return SyncJobRow{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET claim_owner = ?, claim_expires_at = ?,
		fence = fence + 1, attempts = attempts + 1, updated_at = ?
		WHERE job_id = ? AND fence = ?`, owner, expiresAt, now, jobID, row.Fence)
	if err != nil {
		return SyncJobRow{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return SyncJobRow{}, ErrSyncPrecondition
	}
	row.ClaimOwner, row.ClaimExpiresAt, row.UpdatedAt = owner, expiresAt, now
	row.Fence++
	row.Attempts++
	if err := tx.Commit(); err != nil {
		return SyncJobRow{}, err
	}
	return row, nil
}

// FinishMembershipJob atomically records the confirmed remote membership,
// resolves its job, and applies or clears the emergency protected-effect hold.
// A normal membership clears only membership_emergency; it cannot clear an
// operator pause, conflict, trust failure, or generic recovery block.
func (s *Store) FinishMembershipJob(ctx context.Context, jobID, owner string, fence int64, mode string, journal SyncJournalEntry, configRevision, now string) (SyncControlRow, error) {
	if mode != "normal" && mode != "blocked_emergency" {
		return SyncControlRow{}, fmt.Errorf("unsupported membership mode %q", mode)
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return SyncControlRow{}, err
	}
	defer tx.Rollback()
	job, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return SyncControlRow{}, err
	}
	if job.Kind != "membership" || job.ClaimOwner != owner || job.Fence != fence || timeBefore(job.ClaimExpiresAt, now) {
		return SyncControlRow{}, ErrSyncPrecondition
	}
	if err := requireSyncTransition(job.Kind, job.State, "applied"); err != nil {
		return SyncControlRow{}, err
	}
	if journal.JobID != jobID || journal.Fence != fence || journal.JournalID == "" || journal.RecordedAt != now || journal.Phase != "membership" || journal.Outcome != "applied" {
		return SyncControlRow{}, fmt.Errorf("membership terminal journal does not bind the confirmed apply")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries
		(journal_id, job_id, fence, phase, outcome, evidence_json, recorded_at)
		VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return SyncControlRow{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state = 'applied', claim_owner = NULL,
		claim_expires_at = NULL, retain_until_resolved = 0, resolved_at = ?, updated_at = ?
		WHERE job_id = ? AND claim_owner = ? AND fence = ?`, now, now, jobID, owner, fence)
	if err != nil {
		return SyncControlRow{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return SyncControlRow{}, ErrSyncPrecondition
	}
	control, err := applyMembershipControl(ctx, tx, job.GroupID, jobID, mode, configRevision, now)
	if err != nil {
		return SyncControlRow{}, err
	}
	if err := tx.Commit(); err != nil {
		return SyncControlRow{}, err
	}
	return control, nil
}

// FinishRecoveredMembershipJob settles an exact remote-confirmed membership
// after a lost or terminal local attempt. The exceptional convergence is kept
// outside the generic transition graph and requires an unowned exact fence.
func (s *Store) FinishRecoveredMembershipJob(ctx context.Context, jobID string, expectedFence int64, mode string, journal SyncJournalEntry, configRevision, now string) (SyncControlRow, error) {
	if mode != "normal" && mode != "blocked_emergency" {
		return SyncControlRow{}, ErrSyncPrecondition
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return SyncControlRow{}, err
	}
	defer tx.Rollback()
	job, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return SyncControlRow{}, err
	}
	if job.Kind != "membership" || job.Fence != expectedFence || (job.State != "planned" && job.State != "uncertain" && job.State != "blocked") || (job.ClaimOwner != "" && !timeBefore(job.ClaimExpiresAt, now)) {
		return SyncControlRow{}, ErrSyncPrecondition
	}
	if journal.JobID != jobID || journal.Fence != expectedFence || journal.Phase != "membership" || journal.Outcome != "applied" || journal.RecordedAt != now || journal.JournalID == "" {
		return SyncControlRow{}, fmt.Errorf("recovered membership journal does not bind confirmation")
	}
	if err := requireSyncRecoveryTransition(job.Kind, job.State, "applied"); err != nil {
		return SyncControlRow{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return SyncControlRow{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='applied',claim_owner=NULL,claim_expires_at=NULL,retain_until_resolved=0,resolved_at=?,updated_at=? WHERE job_id=? AND fence=?`, now, now, jobID, expectedFence)
	if err != nil {
		return SyncControlRow{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return SyncControlRow{}, ErrSyncPrecondition
	}
	control, err := applyMembershipControl(ctx, tx, job.GroupID, jobID, mode, configRevision, now)
	if err != nil {
		return SyncControlRow{}, err
	}
	if err := tx.Commit(); err != nil {
		return SyncControlRow{}, err
	}
	return control, nil
}

func applyMembershipControl(ctx context.Context, tx *sql.Tx, groupID, resolverID, mode, configRevision, now string) (SyncControlRow, error) {
	var control SyncControlRow
	if err := tx.QueryRowContext(ctx, `SELECT group_id, revision, state, reason, membership_mode, config_revision, updated_at FROM sync_controls WHERE group_id = ?`, groupID).Scan(&control.GroupID, &control.Revision, &control.State, &control.Reason, &control.MembershipMode, &control.ConfigRevision, &control.UpdatedAt); err != nil {
		return SyncControlRow{}, err
	}
	if err := applyMembershipPosture(ctx, tx, &control, mode, "membership_job_id", resolverID, configRevision, now); err != nil {
		return SyncControlRow{}, err
	}
	if mode == "normal" {
		if err := resolveBlockedSyncJobs(ctx, tx, groupID, "membership_replaced", resolverID, now); err != nil {
			return SyncControlRow{}, err
		}
	}
	return control, nil
}

func applyMembershipPosture(ctx context.Context, tx *sql.Tx, control *SyncControlRow, mode, resolverKey, resolverID, configRevision, now string) error {
	targetState, targetReason := control.State, control.Reason
	if mode == "blocked_emergency" {
		if control.State == "active" {
			targetState, targetReason = "blocked", "membership_emergency"
		}
	} else if control.State == "blocked" && control.Reason == "membership_emergency" {
		targetState, targetReason = "active", "none"
	}
	if targetState != control.State || targetReason != control.Reason || control.MembershipMode != mode || (targetState == "active" && control.ConfigRevision != configRevision) {
		previous := control.State
		control.Revision++
		previousRevision := control.Revision - 1
		boundConfigRevision := configRevision
		if targetState == "paused" {
			boundConfigRevision = control.ConfigRevision
		}
		control.State, control.Reason, control.MembershipMode, control.ConfigRevision, control.UpdatedAt = targetState, targetReason, mode, boundConfigRevision, now
		res, err := tx.ExecContext(ctx, `UPDATE sync_controls SET revision=?,state=?,reason=?,membership_mode=?,config_revision=?,updated_at=? WHERE group_id=? AND revision=?`, control.Revision, control.State, control.Reason, control.MembershipMode, control.ConfigRevision, control.UpdatedAt, control.GroupID, previousRevision)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrSyncPrecondition
		}
		context := map[string]any{"reason": control.Reason, "membership_mode": mode, "config_revision": control.ConfigRevision, "revision": control.Revision, resolverKey: resolverID}
		contextJSON, _ := json.Marshal(context)
		if _, err := tx.ExecContext(ctx, `INSERT INTO state_transitions(transition_id,entity_type,entity_id,from_state,to_state,recorded_at,context_json) VALUES (?,?,?,?,?,?,?)`, fmt.Sprintf("sync-control:%s:%d", control.GroupID, control.Revision), "sync_control", control.GroupID, previous, control.State, now, string(contextJSON)); err != nil {
			return err
		}
	}
	return nil
}

// AppendSyncJournal records evidence only for the live owner and fence.
func (s *Store) AppendSyncJournal(ctx context.Context, entry SyncJournalEntry, owner string) error {
	if !validSyncJournal(entry.Phase, entry.Outcome) {
		return fmt.Errorf("invalid sync journal phase or outcome")
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, entry.JobID)
	if err != nil {
		return err
	}
	if row.ClaimOwner != owner || row.Fence != entry.Fence || timeBefore(row.ClaimExpiresAt, entry.RecordedAt) {
		return fmt.Errorf("job %s owner or fence is stale: %w", entry.JobID, ErrSyncPrecondition)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries
		(journal_id, job_id, fence, phase, outcome, evidence_json, recorded_at)
		VALUES (?,?,?,?,?,?,?)`, entry.JournalID, entry.JobID, entry.Fence, entry.Phase, entry.Outcome, entry.EvidenceJSON, entry.RecordedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// AdvanceSyncJob atomically appends recovery evidence and advances a claimed
// nonterminal job head without releasing its fence.
func (s *Store) AdvanceSyncJob(ctx context.Context, jobID, owner string, fence int64, state string, journal SyncJournalEntry, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if !validSyncTransition(row.Kind, row.State, state) || !claimableSyncState(row.Kind, state) || row.ClaimOwner != owner || row.Fence != fence || timeBefore(row.ClaimExpiresAt, now) {
		return ErrSyncPrecondition
	}
	if journal.JobID != jobID || journal.Fence != fence || journal.JournalID == "" || journal.RecordedAt != now || !validSyncJournal(journal.Phase, journal.Outcome) {
		return fmt.Errorf("advance journal does not bind the exact job")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state=?,updated_at=? WHERE job_id=? AND claim_owner=? AND fence=?`, state, now, jobID, owner, fence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

// BeginImportApply persists the complete effect set and the pre-apply journal
// before the first live-tree write. Re-entry with the same immutable set is
// idempotent; a changed set is a fencing failure.
func (s *Store) BeginImportApply(ctx context.Context, jobID, owner, resourceID string, fence int64, effects []syncrecords.ImportPath, journal SyncJournalEntry, now string) error {
	if len(effects) == 0 || len(effects) > 1000 {
		return fmt.Errorf("import effect count must be 1..1000")
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if row.Kind != "import" || row.ClaimOwner != owner || row.Fence != fence || timeBefore(row.ClaimExpiresAt, now) || (row.State != "validated" && row.State != "recovering" && row.State != "applying") {
		return ErrSyncPrecondition
	}
	if err := requireSyncTransition(row.Kind, row.State, "applying"); err != nil {
		return err
	}
	if journal.JobID != jobID || journal.Fence != fence || journal.Phase != "import" || journal.Outcome != "started" || journal.RecordedAt != now || journal.JournalID == "" {
		return fmt.Errorf("import pre-apply journal does not bind the exact job")
	}
	for _, effect := range effects {
		res, err := tx.ExecContext(ctx, `INSERT INTO sync_import_effects
			(job_id,fence,resource_id,path,before_value,after_value) VALUES (?,?,?,?,?,?)
			ON CONFLICT(job_id,fence,path) DO NOTHING`, jobID, fence, resourceID, effect.Path, effect.Before, effect.After)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var stored ImportEffectRow
			if err := tx.QueryRowContext(ctx, `SELECT job_id,fence,resource_id,path,before_value,after_value,COALESCE(applied_at,'') FROM sync_import_effects WHERE job_id=? AND fence=? AND path=?`, jobID, fence, effect.Path).Scan(&stored.JobID, &stored.Fence, &stored.ResourceID, &stored.Path, &stored.Before, &stored.After, &stored.AppliedAt); err != nil {
				return err
			}
			if stored.Fence != fence || stored.ResourceID != resourceID || stored.Before != effect.Before || stored.After != effect.After {
				return ErrSyncPrecondition
			}
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_import_effects WHERE job_id=? AND fence=?`, jobID, fence).Scan(&count); err != nil || count != len(effects) {
		if err != nil {
			return err
		}
		return ErrSyncPrecondition
	}
	if row.State != "applying" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='applying',updated_at=? WHERE job_id=? AND claim_owner=? AND fence=?`, now, jobID, owner, fence); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BeginControllerImportApply records the durable pre-apply boundary for an
// import that advances only controller-owned paths, the index, and the content
// ref. Its immutable record deliberately contains no live-tree path effects.
func (s *Store) BeginControllerImportApply(ctx context.Context, jobID, owner string, fence int64, journal SyncJournalEntry, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	record, err := syncrecords.DecodeImport([]byte(row.PayloadJSON))
	if err != nil || !record.ControllerOnly || len(record.Paths) != 0 || row.Kind != "import" || row.ClaimOwner != owner || row.Fence != fence || timeBefore(row.ClaimExpiresAt, now) || (row.State != "validated" && row.State != "recovering" && row.State != "applying") {
		return ErrSyncPrecondition
	}
	if err := requireSyncTransition(row.Kind, row.State, "applying"); err != nil {
		return err
	}
	if journal.JobID != jobID || journal.Fence != fence || journal.Phase != "import" || journal.Outcome != "started" || journal.RecordedAt != now || journal.JournalID == "" {
		return fmt.Errorf("controller import pre-apply journal does not bind the exact job")
	}
	if row.State != "applying" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='applying',updated_at=? WHERE job_id=? AND claim_owner=? AND fence=?`, now, jobID, owner, fence)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrSyncPrecondition
		}
	}
	return tx.Commit()
}

// FinishRecoveredControllerImportJob settles a controller-only advance after
// exact Git inspection proves the content ref reached its immutable target.
// A live, unexpired owner remains authoritative and cannot be bypassed.
func (s *Store) FinishRecoveredControllerImportJob(ctx context.Context, jobID string, expectedFence int64, journal SyncJournalEntry, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	record, err := syncrecords.DecodeImport([]byte(row.PayloadJSON))
	if err != nil || !record.ControllerOnly || len(record.Paths) != 0 || row.Kind != "import" || row.Fence != expectedFence || (row.State != "applying" && row.State != "recovering" && row.State != "uncertain") || (row.ClaimOwner != "" && !timeBefore(row.ClaimExpiresAt, now)) {
		return ErrSyncPrecondition
	}
	if err := requireSyncTransition(row.Kind, row.State, "applied"); err != nil {
		return err
	}
	if journal.JobID != jobID || journal.Fence != expectedFence || journal.Phase != "import" || journal.Outcome != "applied" || journal.RecordedAt != now || journal.JournalID == "" {
		return fmt.Errorf("recovered controller import journal does not bind confirmation")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='applied',claim_owner=NULL,claim_expires_at=NULL,retain_until_resolved=0,resolved_at=?,updated_at=? WHERE job_id=? AND fence=?`, now, now, jobID, expectedFence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

// PrepareControllerImportRecovery records exact controller-only inspection
// after process loss. A clean original state is retryable; every other
// non-target state remains explicit uncertainty.
func (s *Store) PrepareControllerImportRecovery(ctx context.Context, jobID string, expectedFence int64, state string, journal SyncJournalEntry, now string) error {
	if state != "validated" && state != "uncertain" {
		return fmt.Errorf("unsupported controller import recovery state %q", state)
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	record, err := syncrecords.DecodeImport([]byte(row.PayloadJSON))
	if err != nil || !record.ControllerOnly || row.Kind != "import" || row.Fence != expectedFence || (row.State != "applying" && row.State != "recovering" && row.State != "uncertain") || (row.ClaimOwner != "" && !timeBefore(row.ClaimExpiresAt, now)) {
		return ErrSyncPrecondition
	}
	if err := requireSyncTransition(row.Kind, row.State, state); err != nil {
		return err
	}
	wantOutcome := "effect_unknown"
	if state == "validated" {
		wantOutcome = "effect_not_started"
	}
	if journal.JobID != jobID || journal.Fence != expectedFence || journal.Phase != "claim_recovery" || journal.Outcome != wantOutcome || journal.RecordedAt != now || journal.JournalID == "" {
		return fmt.Errorf("controller import recovery journal does not bind inspection")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state=?,claim_owner=NULL,claim_expires_at=NULL,updated_at=? WHERE job_id=? AND fence=?`, state, now, jobID, expectedFence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

// ResolveImportDeferral keeps the durable disposition inspectable without
// leaking a terminal, unclaimable obligation into the bounded active queue.
func (s *Store) ResolveImportDeferral(ctx context.Context, jobID, now string) error {
	res, err := s.ExecContext(ctx, `UPDATE sync_jobs SET retain_until_resolved=0,resolved_at=?,updated_at=?
		WHERE job_id=? AND kind='import' AND state='deferred' AND claim_owner IS NULL AND resolved_at IS NULL`, now, now, jobID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return nil
}

// ReopenDeferredImport revives a validated plan that was deferred only after
// it had been claimed. Admission-time deferrals use a different immutable
// logical identity and are never reopened by this transition.
func (s *Store) ReopenDeferredImport(ctx context.Context, jobID, expectedConfigRevision string, journal SyncJournalEntry, now string) (SyncJobRow, error) {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return SyncJobRow{}, err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return SyncJobRow{}, err
	}
	var controlState, controlReason, controlMembershipMode, controlRevision string
	if err := tx.QueryRowContext(ctx, `SELECT state,reason,membership_mode,config_revision FROM sync_controls WHERE group_id=?`, row.GroupID).Scan(&controlState, &controlReason, &controlMembershipMode, &controlRevision); err != nil {
		return SyncJobRow{}, err
	}
	admitted, err := syncrecords.DecodeImport([]byte(row.PayloadJSON))
	if err != nil || admitted.State != "validated" || admitted.Reason != "none" {
		return SyncJobRow{}, ErrSyncPrecondition
	}
	controllerRepair := administrationRepairAllowed(row.Kind, admitted.ControllerOnly, controlState, controlReason, controlMembershipMode)
	if row.Kind != "import" || row.State != "deferred" || row.ResolvedAt == "" || row.ClaimOwner != "" || (controlState != "active" && !controllerRepair) || controlRevision != expectedConfigRevision {
		return SyncJobRow{}, ErrSyncPrecondition
	}
	if err := requireSyncTransition(row.Kind, row.State, "validated"); err != nil {
		return SyncJobRow{}, err
	}
	if journal.JobID != jobID || journal.Fence != row.Fence || journal.Phase != "claim_recovery" || journal.Outcome != "effect_not_started" || journal.RecordedAt != now || journal.JournalID == "" {
		return SyncJobRow{}, fmt.Errorf("deferred import reopen journal does not bind the job")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries(journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES(?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return SyncJobRow{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='validated',retain_until_resolved=1,resolved_at=NULL,updated_at=? WHERE job_id=? AND state='deferred' AND resolved_at IS NOT NULL AND claim_owner IS NULL`, now, jobID)
	if err != nil {
		return SyncJobRow{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return SyncJobRow{}, ErrSyncPrecondition
	}
	row, err = loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return SyncJobRow{}, err
	}
	if err := tx.Commit(); err != nil {
		return SyncJobRow{}, err
	}
	return row, nil
}

func administrationRepairAllowed(kind string, _ bool, state, reason, membershipMode string) bool {
	if kind == "membership" {
		return membershipMode == "blocked_emergency" && (state == "blocked" || state == "paused")
	}
	if state != "blocked" {
		return false
	}
	strongerHold := reason == "conflict" || reason == "trust_failure" || reason == "recovery_required"
	if kind == "publication" {
		return reason == "conflict"
	}
	return strongerHold && (kind == "checkpoint" || kind == "import")
}

// BlockSyncJobAfterRemoteMove records a measured predecessor loss on re-entry.
// It preserves the signed candidate and refuses to steal an unexpired claim.
func (s *Store) BlockSyncJobAfterRemoteMove(ctx context.Context, jobID string, expectedFence int64, journal SyncJournalEntry, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	allowed := (row.Kind == "publication" && (row.State == "signed" || row.State == "push_pending" || row.State == "uncertain")) ||
		(row.Kind == "checkpoint" && (row.State == "planned" || row.State == "applying" || row.State == "uncertain")) ||
		((row.Kind == "publication" || row.Kind == "checkpoint") && row.State == "blocked")
	if !allowed || row.Fence != expectedFence || row.ResolvedAt != "" || (row.ClaimOwner != "" && !timeBefore(row.ClaimExpiresAt, now)) {
		return ErrSyncPrecondition
	}
	if journal.JobID != jobID || journal.Fence != expectedFence || journal.Phase != "claim_recovery" || journal.Outcome != "blocked" || journal.RecordedAt != now || journal.JournalID == "" {
		return fmt.Errorf("remote-move recovery evidence does not bind the job")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='blocked',claim_owner=NULL,claim_expires_at=NULL,retain_until_resolved=1,resolved_at=NULL,updated_at=? WHERE job_id=? AND fence=? AND resolved_at IS NULL`, now, jobID, expectedFence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

// FinishImportJob atomically marks exact effects applied, advances path facts
// under their observation fence, and resolves the durable import job. An exact
// Watchman observation may already have consumed an applying effect after
// process loss; otherwise attribution remains available after completion.
func (s *Store) FinishImportJob(ctx context.Context, jobID, owner, resourceID string, fence, expectedObservationRevision int64, journal SyncJournalEntry, now string) error {
	return s.finishImportJob(ctx, jobID, owner, resourceID, fence, expectedObservationRevision, journal, now, false)
}

// FinishRecoveredImportJob completes a proven all-after recovery even when
// observations of unrelated paths advanced the resource revision while the
// original process was absent. The caller must first prove every import path
// and the content ref are at the recorded target.
func (s *Store) FinishRecoveredImportJob(ctx context.Context, jobID, owner, resourceID string, fence, expectedObservationRevision int64, journal SyncJournalEntry, now string) error {
	return s.finishImportJob(ctx, jobID, owner, resourceID, fence, expectedObservationRevision, journal, now, true)
}

func (s *Store) finishImportJob(ctx context.Context, jobID, owner, resourceID string, fence, expectedObservationRevision int64, journal SyncJournalEntry, now string, allowAdvancedObservation bool) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if row.Kind != "import" || row.State != "applying" || row.ClaimOwner != owner || row.Fence != fence || timeBefore(row.ClaimExpiresAt, now) {
		return ErrSyncPrecondition
	}
	if err := requireSyncTransition(row.Kind, row.State, "applied"); err != nil {
		return err
	}
	if journal.JobID != jobID || journal.Fence != fence || journal.Phase != "import" || journal.Outcome != "applied" || journal.RecordedAt != now || journal.JournalID == "" {
		return fmt.Errorf("import terminal journal does not bind the exact job")
	}
	res, err := tx.ExecContext(ctx, `UPDATE resources SET observation_revision=observation_revision+1 WHERE resource_id=? AND observation_revision=?`, resourceID, expectedObservationRevision)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		if !allowAdvancedObservation {
			return ErrSyncPrecondition
		}
		var current int64
		if err := tx.QueryRowContext(ctx, `SELECT observation_revision FROM resources WHERE resource_id=?`, resourceID).Scan(&current); err != nil || current < expectedObservationRevision {
			if err != nil {
				return err
			}
			return ErrSyncPrecondition
		}
		res, err = tx.ExecContext(ctx, `UPDATE resources SET observation_revision=observation_revision+1 WHERE resource_id=? AND observation_revision=?`, resourceID, current)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrSyncPrecondition
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT path,after_value FROM sync_import_effects WHERE job_id=? AND fence=? ORDER BY path`, jobID, fence)
	if err != nil {
		return err
	}
	type effect struct{ path, after string }
	var effects []effect
	for rows.Next() {
		var e effect
		if err := rows.Scan(&e.path, &e.after); err != nil {
			rows.Close()
			return err
		}
		effects = append(effects, e)
	}
	if err := rows.Close(); err != nil || len(effects) == 0 {
		if err != nil {
			return err
		}
		return ErrSyncPrecondition
	}
	for _, effect := range effects {
		exists, digest := effect.after != "absent", effect.after
		if !exists {
			digest = ""
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO path_facts(resource_id,path,digest,"exists",observed_at) VALUES (?,?,?,?,?)
			ON CONFLICT(resource_id,path) DO UPDATE SET digest=excluded.digest,"exists"=excluded."exists",observed_at=excluded.observed_at`, resourceID, effect.path, nullString(digest), exists, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sync_import_effects SET applied_at=? WHERE job_id=? AND fence=? AND applied_at IS NULL`, now, jobID, fence); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE sync_jobs SET state='applied',claim_owner=NULL,claim_expires_at=NULL,retain_until_resolved=0,resolved_at=?,updated_at=? WHERE job_id=? AND claim_owner=? AND fence=?`, now, now, jobID, owner, fence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

// LoadPendingImportEffects returns only each path's newest active-or-applied,
// unattributed effect. Active effects keep Watchman from echoing exact bytes
// after process loss; older provenance never resurfaces after a later import.
func (s *Store) LoadPendingImportEffects(ctx context.Context, resourceID string) ([]ImportEffectRow, error) {
	rows, err := s.QueryContext(ctx, `SELECT e.job_id,e.fence,e.resource_id,e.path,e.before_value,e.after_value,COALESCE(e.applied_at,'')
		FROM sync_import_effects e JOIN sync_jobs j ON j.job_id=e.job_id JOIN sync_job_sequences js ON js.job_id=j.job_id
		WHERE e.resource_id=? AND (e.applied_at IS NOT NULL OR (j.state IN ('applying','recovering','uncertain') AND j.resolved_at IS NULL))
		AND NOT EXISTS (SELECT 1 FROM sync_import_attributions a WHERE a.job_id=e.job_id AND a.fence=e.fence AND a.path=e.path)
		AND NOT EXISTS (SELECT 1 FROM sync_import_effects newer JOIN sync_jobs newer_job ON newer_job.job_id=newer.job_id JOIN sync_job_sequences newer_js ON newer_js.job_id=newer_job.job_id
			WHERE newer.resource_id=e.resource_id AND newer.path=e.path
			AND (newer.applied_at IS NOT NULL OR (newer_job.state IN ('applying','recovering','uncertain') AND newer_job.resolved_at IS NULL))
			AND (newer_js.sequence>js.sequence OR (newer_js.sequence=js.sequence AND newer.fence>e.fence)))
		ORDER BY e.path`, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImportEffectRow
	for rows.Next() {
		var row ImportEffectRow
		if err := rows.Scan(&row.JobID, &row.Fence, &row.ResourceID, &row.Path, &row.Before, &row.After, &row.AppliedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// LoadSyncJournals returns immutable evidence in deterministic order.
func (s *Store) LoadSyncJournals(ctx context.Context, jobID string) ([]SyncJournalEntry, error) {
	rows, err := s.QueryContext(ctx, `SELECT sequence,journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at FROM sync_journal_entries WHERE job_id=? ORDER BY sequence`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SyncJournalEntry
	for rows.Next() {
		var e SyncJournalEntry
		if err := rows.Scan(&e.Sequence, &e.JournalID, &e.JobID, &e.Fence, &e.Phase, &e.Outcome, &e.EvidenceJSON, &e.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LoadUnresolvedSyncJobs returns retained obligations for explicit recovery.
func (s *Store) LoadUnresolvedSyncJobs(ctx context.Context, groupID, kind string) ([]SyncJobRow, error) {
	if !validSyncKind(kind) {
		return nil, fmt.Errorf("unsupported sync job kind %q", kind)
	}
	rows, err := s.QueryContext(ctx, `SELECT job_id,group_id,kind,logical_key,request_fingerprint,state,payload_json,attempts,COALESCE(claim_owner,''),COALESCE(claim_expires_at,''),fence,retain_until_resolved,created_at,updated_at,COALESCE(resolved_at,'') FROM sync_jobs WHERE group_id=? AND kind=? AND resolved_at IS NULL ORDER BY created_at,job_id`, groupID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SyncJobRow
	for rows.Next() {
		var row SyncJobRow
		var retain int
		if err := rows.Scan(&row.JobID, &row.GroupID, &row.Kind, &row.LogicalKey, &row.RequestFingerprint, &row.State, &row.PayloadJSON, &row.Attempts, &row.ClaimOwner, &row.ClaimExpiresAt, &row.Fence, &retain, &row.CreatedAt, &row.UpdatedAt, &row.ResolvedAt); err != nil {
			return nil, err
		}
		row.RetainUntilResolved = retain != 0
		out = append(out, row)
	}
	return out, rows.Err()
}

// LoadLatestSyncJob returns the newest durable disposition for operator status,
// including resolved deferrals that no longer occupy the active queue.
func (s *Store) LoadLatestSyncJob(ctx context.Context, groupID, kind string) (SyncJobRow, error) {
	if !validSyncKind(kind) {
		return SyncJobRow{}, fmt.Errorf("unsupported sync job kind %q", kind)
	}
	var row SyncJobRow
	// RFC3339Nano omits trailing zeroes, so its raw strings do not sort by time.
	err := s.QueryRowContext(ctx, `SELECT job_id,group_id,kind,logical_key,request_fingerprint,state,payload_json,attempts,COALESCE(claim_owner,''),COALESCE(claim_expires_at,''),fence,retain_until_resolved,created_at,updated_at,COALESCE(resolved_at,'')
		FROM sync_jobs WHERE group_id=? AND kind=?
		ORDER BY CAST(strftime('%s',updated_at) AS INTEGER) DESC,
		         CAST(substr(updated_at,20) AS REAL) DESC,job_id DESC LIMIT 1`, groupID, kind).Scan(
		&row.JobID, &row.GroupID, &row.Kind, &row.LogicalKey, &row.RequestFingerprint, &row.State, &row.PayloadJSON,
		&row.Attempts, &row.ClaimOwner, &row.ClaimExpiresAt, &row.Fence, &row.RetainUntilResolved,
		&row.CreatedAt, &row.UpdatedAt, &row.ResolvedAt)
	return row, err
}

// FindSyncJob returns the durable identity for an exact logical request,
// including resolved rows needed for idempotent explicit re-entry.
func (s *Store) FindSyncJob(ctx context.Context, groupID, kind, logicalKey string) (SyncJobRow, bool, error) {
	if !validSyncKind(kind) {
		return SyncJobRow{}, false, fmt.Errorf("unsupported sync job kind %q", kind)
	}
	row, err := loadSyncJobByKey(ctx, s.DB, groupID, kind, logicalKey)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncJobRow{}, false, nil
	}
	return row, err == nil, err
}

// FinishPublicationJob commits the confirmed publication and its one peer
// delivery obligation atomically. A crash can therefore expose neither half.
func (s *Store) FinishPublicationJob(ctx context.Context, jobID, owner string, fence int64, journal SyncJournalEntry, delivery SyncJobInput, now string) (SyncJobRow, error) {
	fingerprint, payload, err := syncRequestFingerprint("delivery", []byte(delivery.PayloadJSON))
	if err != nil {
		return SyncJobRow{}, err
	}
	delivery.PayloadJSON = payload
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return SyncJobRow{}, err
	}
	defer tx.Rollback()
	job, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return SyncJobRow{}, err
	}
	if job.Kind != "publication" || job.ClaimOwner != owner || job.Fence != fence || timeBefore(job.ClaimExpiresAt, now) {
		return SyncJobRow{}, ErrSyncPrecondition
	}
	if err := requireSyncTransition(job.Kind, job.State, "published"); err != nil {
		return SyncJobRow{}, err
	}
	if journal.JobID != jobID || journal.Fence != fence || journal.Phase != "publication" || journal.Outcome != "published" || journal.RecordedAt != now || journal.JournalID == "" {
		return SyncJobRow{}, fmt.Errorf("publication terminal journal does not bind confirmation")
	}
	if delivery.GroupID != job.GroupID || delivery.Kind != "delivery" || delivery.InitialState != "pending" || delivery.JobID == "" || delivery.LogicalKey == "" {
		return SyncJobRow{}, fmt.Errorf("invalid publication delivery obligation")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return SyncJobRow{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='published',claim_owner=NULL,claim_expires_at=NULL,retain_until_resolved=0,resolved_at=?,updated_at=? WHERE job_id=? AND claim_owner=? AND fence=?`, now, now, jobID, owner, fence)
	if err != nil {
		return SyncJobRow{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return SyncJobRow{}, ErrSyncPrecondition
	}
	existing, loadErr := loadSyncJobByKey(ctx, tx, delivery.GroupID, "delivery", delivery.LogicalKey)
	if loadErr == nil {
		if existing.RequestFingerprint != fingerprint {
			return SyncJobRow{}, ErrSyncAdmissionConflict
		}
		if err := tx.Commit(); err != nil {
			return SyncJobRow{}, err
		}
		return existing, nil
	}
	if !errors.Is(loadErr, sql.ErrNoRows) {
		return SyncJobRow{}, loadErr
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_jobs WHERE group_id=? AND resolved_at IS NULL`, delivery.GroupID).Scan(&pending); err != nil {
		return SyncJobRow{}, err
	}
	if pending >= delivery.QueueLimit {
		return SyncJobRow{}, ErrSyncQueueFull
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sync_jobs (job_id,group_id,kind,logical_key,request_fingerprint,state,payload_json,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?)`, delivery.JobID, delivery.GroupID, "delivery", delivery.LogicalKey, fingerprint, "pending", delivery.PayloadJSON, now, now); err != nil {
		return SyncJobRow{}, err
	}
	created, err := loadSyncJobByKey(ctx, tx, delivery.GroupID, "delivery", delivery.LogicalKey)
	if err != nil {
		return SyncJobRow{}, err
	}
	if err := tx.Commit(); err != nil {
		return SyncJobRow{}, err
	}
	return created, nil
}

// FinishRecoveredPublicationJob settles a measured remote-confirmed candidate
// after the original process lost its claim. It refuses an unexpired owner and
// still commits the peer obligation in the same transaction.
func (s *Store) FinishRecoveredPublicationJob(ctx context.Context, jobID string, expectedFence int64, journal SyncJournalEntry, delivery SyncJobInput, now string) (SyncJobRow, error) {
	fingerprint, payload, err := syncRequestFingerprint("delivery", []byte(delivery.PayloadJSON))
	if err != nil {
		return SyncJobRow{}, err
	}
	delivery.PayloadJSON = payload
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return SyncJobRow{}, err
	}
	defer tx.Rollback()
	job, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return SyncJobRow{}, err
	}
	if job.Kind != "publication" || job.Fence != expectedFence || (job.State != "signed" && job.State != "push_pending" && job.State != "uncertain" && job.State != "blocked") || (job.ClaimOwner != "" && !timeBefore(job.ClaimExpiresAt, now)) {
		return SyncJobRow{}, ErrSyncPrecondition
	}
	if journal.JobID != jobID || journal.Fence != expectedFence || journal.Phase != "publication" || journal.Outcome != "published" || journal.RecordedAt != now || journal.JournalID == "" {
		return SyncJobRow{}, fmt.Errorf("recovered publication journal does not bind confirmation")
	}
	if err := requireSyncRecoveryTransition(job.Kind, job.State, "published"); err != nil {
		return SyncJobRow{}, err
	}
	if delivery.GroupID != job.GroupID || delivery.Kind != "delivery" || delivery.InitialState != "pending" || delivery.JobID == "" || delivery.LogicalKey == "" || delivery.QueueLimit < 1 || delivery.QueueLimit > 1000 {
		return SyncJobRow{}, fmt.Errorf("invalid recovered publication delivery obligation")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return SyncJobRow{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='published',claim_owner=NULL,claim_expires_at=NULL,retain_until_resolved=0,resolved_at=?,updated_at=? WHERE job_id=? AND fence=?`, now, now, jobID, expectedFence)
	if err != nil {
		return SyncJobRow{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return SyncJobRow{}, ErrSyncPrecondition
	}
	existing, loadErr := loadSyncJobByKey(ctx, tx, delivery.GroupID, "delivery", delivery.LogicalKey)
	if loadErr == nil {
		if existing.RequestFingerprint != fingerprint {
			return SyncJobRow{}, ErrSyncAdmissionConflict
		}
		if err := tx.Commit(); err != nil {
			return SyncJobRow{}, err
		}
		return existing, nil
	}
	if !errors.Is(loadErr, sql.ErrNoRows) {
		return SyncJobRow{}, loadErr
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_jobs WHERE group_id=? AND resolved_at IS NULL`, delivery.GroupID).Scan(&pending); err != nil {
		return SyncJobRow{}, err
	}
	if pending >= delivery.QueueLimit {
		return SyncJobRow{}, ErrSyncQueueFull
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sync_jobs (job_id,group_id,kind,logical_key,request_fingerprint,state,payload_json,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?)`, delivery.JobID, delivery.GroupID, "delivery", delivery.LogicalKey, fingerprint, "pending", delivery.PayloadJSON, now, now); err != nil {
		return SyncJobRow{}, err
	}
	created, err := loadSyncJobByKey(ctx, tx, delivery.GroupID, "delivery", delivery.LogicalKey)
	if err != nil {
		return SyncJobRow{}, err
	}
	if err := tx.Commit(); err != nil {
		return SyncJobRow{}, err
	}
	return created, nil
}

// FinishRecoveredCheckpointJob settles a measured remote-confirmed checkpoint
// after the originating process lost its claim. It never creates or signs a
// replacement candidate and refuses takeover while the original lease is live.
func (s *Store) FinishRecoveredCheckpointJob(ctx context.Context, jobID string, expectedFence int64, journal SyncJournalEntry, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if job.Kind != "checkpoint" || job.Fence != expectedFence || (job.State != "applying" && job.State != "uncertain" && job.State != "blocked") || (job.ClaimOwner != "" && !timeBefore(job.ClaimExpiresAt, now)) {
		return ErrSyncPrecondition
	}
	if journal.JobID != jobID || journal.Fence != expectedFence || journal.Phase != "checkpoint" || journal.Outcome != "applied" || journal.RecordedAt != now || journal.JournalID == "" {
		return fmt.Errorf("recovered checkpoint journal does not bind confirmation")
	}
	if err := requireSyncRecoveryTransition(job.Kind, job.State, "applied"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='applied',claim_owner=NULL,claim_expires_at=NULL,retain_until_resolved=0,resolved_at=?,updated_at=? WHERE job_id=? AND fence=?`, now, now, jobID, expectedFence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

// ReopenRejectedCheckpoint records an exact predecessor probe and makes the
// already-signed checkpoint candidate claimable again without opening a
// generic blocked transition.
func (s *Store) ReopenRejectedCheckpoint(ctx context.Context, jobID string, expectedFence int64, journal SyncJournalEntry, now string) error {
	return s.reopenRejectedAdministrationJob(ctx, jobID, expectedFence, "checkpoint", "applying", journal, now)
}

// ReopenRejectedPublication records an exact predecessor probe and makes the
// preserved signed candidate claimable through its existing conflict hold.
func (s *Store) ReopenRejectedPublication(ctx context.Context, jobID string, expectedFence int64, journal SyncJournalEntry, now string) error {
	return s.reopenRejectedAdministrationJob(ctx, jobID, expectedFence, "publication", "signed", journal, now)
}

// ReopenRejectedMembership records an exact predecessor probe and makes the
// already-signed membership candidate claimable again.
func (s *Store) ReopenRejectedMembership(ctx context.Context, jobID string, expectedFence int64, journal SyncJournalEntry, now string) error {
	return s.reopenRejectedAdministrationJob(ctx, jobID, expectedFence, "membership", "planned", journal, now)
}

func (s *Store) reopenRejectedAdministrationJob(ctx context.Context, jobID string, expectedFence int64, kind, targetState string, journal SyncJournalEntry, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if job.Kind != kind || job.Fence != expectedFence || job.ResolvedAt != "" || (job.State != "blocked" && job.State != "uncertain") || (job.ClaimOwner != "" && !timeBefore(job.ClaimExpiresAt, now)) {
		return ErrSyncPrecondition
	}
	if journal.JobID != jobID || journal.Fence != expectedFence || journal.Phase != "claim_recovery" || journal.Outcome != "effect_not_started" || journal.RecordedAt != now || journal.JournalID == "" {
		return fmt.Errorf("rejected administration recovery evidence does not bind the job")
	}
	if err := requireSyncRecoveryTransition(job.Kind, job.State, targetState); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries(journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES(?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state=?,claim_owner=NULL,claim_expires_at=NULL,retain_until_resolved=1,resolved_at=NULL,updated_at=? WHERE job_id=? AND fence=?`, targetState, now, jobID, expectedFence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

// FinishSyncJob records the measured head outcome under the live fencing
// generation and releases ownership. Journal rows are retained separately.
func (s *Store) FinishSyncJob(ctx context.Context, jobID, owner string, fence int64, state string, disposition SyncJobDisposition, journal SyncJournalEntry, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if !validSyncTransition(row.Kind, row.State, state) {
		return fmt.Errorf("state transition %q -> %q is invalid for sync job kind %q", row.State, state, row.Kind)
	}
	if disposition != SyncJobKeepUnresolved && disposition != SyncJobResolve {
		return fmt.Errorf("invalid sync job disposition")
	}
	resolved := disposition == SyncJobResolve
	if resolved != resolvedSyncState(row.Kind, state) {
		return fmt.Errorf("sync job kind %q state %q has contradictory resolution disposition", row.Kind, state)
	}
	if journal.JobID != jobID || journal.Fence != fence || journal.JournalID == "" || journal.RecordedAt != now {
		return fmt.Errorf("terminal journal does not bind the exact job, fence, and timestamp")
	}
	if !validSyncJournal(journal.Phase, journal.Outcome) {
		return fmt.Errorf("invalid terminal sync journal phase or outcome")
	}
	if row.ClaimOwner != owner || row.Fence != fence || timeBefore(row.ClaimExpiresAt, now) {
		return ErrSyncPrecondition
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries
		(journal_id, job_id, fence, phase, outcome, evidence_json, recorded_at)
		VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	resolvedAt := any(nil)
	retain := 1
	if resolved {
		resolvedAt = now
		retain = 0
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state = ?, claim_owner = NULL,
		claim_expires_at = NULL, retain_until_resolved = ?, resolved_at = ?, updated_at = ?
		WHERE job_id = ? AND claim_owner = ? AND fence = ?`,
		state, retain, resolvedAt, now, jobID, owner, fence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

func resolvedSyncState(kind, state string) bool {
	resolved := map[string]map[string]bool{
		"publication":  {"published": true},
		"delivery":     {"accepted": true, "refused": true},
		"import":       {"applied": true, "deferred": true},
		"verification": {"complete": true, "incomplete": true, "target_changed": true, "expired": true},
		"membership":   {"applied": true},
		"checkpoint":   {"applied": true},
	}
	return resolved[kind][state]
}

// ReconcileExpiredSyncClaim converts process loss into explicit recovery
// evidence. Only a proven no-effect result makes the job claimable again;
// an ambiguous result becomes uncertain and stays held for a later exact
// reconciliation.
func (s *Store) ReconcileExpiredSyncClaim(ctx context.Context, jobID string, expectedFence int64, disposition string, journal SyncJournalEntry, now string) error {
	if disposition != "effect_not_started" && disposition != "effect_unknown" {
		return fmt.Errorf("unsupported expired-claim disposition %q", disposition)
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if row.Fence != expectedFence || row.ClaimOwner == "" || !timeBefore(row.ClaimExpiresAt, now) {
		return ErrSyncPrecondition
	}
	targetState := row.State
	if disposition == "effect_unknown" {
		targetState = unknownSyncState(row.Kind)
	}
	if err := requireSyncTransition(row.Kind, row.State, targetState); err != nil {
		return err
	}
	if journal.JobID != jobID || journal.Fence != expectedFence || journal.JournalID == "" || journal.RecordedAt != now || journal.Phase != "claim_recovery" || journal.Outcome != disposition {
		return fmt.Errorf("recovery journal does not bind the expired claim")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries
		(journal_id, job_id, fence, phase, outcome, evidence_json, recorded_at)
		VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	if disposition == "effect_unknown" {
		_, err = tx.ExecContext(ctx, `UPDATE sync_jobs SET state = ?, claim_owner = NULL,
			claim_expires_at = NULL, updated_at = ? WHERE job_id = ? AND fence = ?`, unknownSyncState(row.Kind), now, jobID, expectedFence)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE sync_jobs SET claim_owner = NULL,
			claim_expires_at = NULL, updated_at = ? WHERE job_id = ? AND fence = ?`, now, jobID, expectedFence)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// PrepareUnknownDeliveryRetry records why replaying an ambiguous delivery is
// safe: the immutable logical key and request fingerprint are reused, and the
// receiver's inbox is idempotent on those same values. This exceptional edge
// does not turn an unknown attempt into accepted evidence.
func (s *Store) PrepareUnknownDeliveryRetry(ctx context.Context, jobID string, expectedFence int64, journal SyncJournalEntry, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if row.Kind != "delivery" || row.State != "unknown" || row.Fence != expectedFence || row.ClaimOwner != "" || row.ResolvedAt != "" || row.RequestFingerprint == "" {
		return ErrSyncPrecondition
	}
	if journal.JobID != jobID || journal.Fence != expectedFence || journal.JournalID == "" || journal.RecordedAt != now || journal.Phase != "claim_recovery" || journal.Outcome != "effect_unknown" {
		return fmt.Errorf("delivery replay journal does not bind the unknown attempt")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries
		(journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`,
		journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='retryable',updated_at=? WHERE job_id=? AND fence=? AND state='unknown' AND claim_owner IS NULL AND resolved_at IS NULL`, now, jobID, expectedFence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

// ResolveObsoleteImport retires a validated import that is unclaimed, or whose
// expired claim is proven not to have started, after the approved plan moved.
func (s *Store) ResolveObsoleteImport(ctx context.Context, jobID string, expectedFence int64, journal SyncJournalEntry, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if row.Kind != "import" || row.State != "validated" || row.Fence != expectedFence || (row.ClaimOwner != "" && !timeBefore(row.ClaimExpiresAt, now)) {
		return ErrSyncPrecondition
	}
	recoveryFence := expectedFence
	if recoveryFence < 1 {
		recoveryFence = 1
	}
	if journal.JobID != jobID || journal.Fence != recoveryFence || journal.JournalID == "" || journal.RecordedAt != now || journal.Phase != "claim_recovery" || journal.Outcome != "effect_not_started" {
		return fmt.Errorf("obsolete import recovery journal does not bind the expired claim")
	}
	if err := requireSyncTransition(row.Kind, row.State, "deferred"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries
		(journal_id, job_id, fence, phase, outcome, evidence_json, recorded_at)
		VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='deferred',fence=?,claim_owner=NULL,
		claim_expires_at=NULL,retain_until_resolved=0,resolved_at=?,updated_at=?
		WHERE job_id=? AND fence=?`, recoveryFence, now, now, jobID, expectedFence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

// PrepareImportRecovery classifies an expired applying attempt from measured
// file/index/ref evidence. Recoverable before/after combinations retain the old
// immutable effect rows and become claimable under a new fence; unexplained
// bytes become terminal uncertainty.
func (s *Store) PrepareImportRecovery(ctx context.Context, jobID string, expectedFence int64, state string, journal SyncJournalEntry, now string) error {
	if state != "validated" && state != "recovering" && state != "uncertain" {
		return fmt.Errorf("unsupported import recovery state %q", state)
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if row.Kind != "import" || (row.State != "applying" && row.State != "recovering") || row.Fence != expectedFence || row.ClaimOwner == "" || !timeBefore(row.ClaimExpiresAt, now) {
		return ErrSyncPrecondition
	}
	if err := requireSyncTransition(row.Kind, row.State, state); err != nil {
		return err
	}
	expectedOutcome := "effect_unknown"
	if state == "validated" {
		expectedOutcome = "effect_not_started"
	}
	if journal.JobID != jobID || journal.Fence != expectedFence || journal.Phase != "claim_recovery" || journal.Outcome != expectedOutcome || journal.RecordedAt != now || journal.JournalID == "" {
		return fmt.Errorf("import recovery journal does not bind the expired attempt")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries(job_id,journal_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JobID, journal.JournalID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	resolved, retain := any(nil), 1
	if state == "uncertain" {
		retain = 1
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state=?,claim_owner=NULL,claim_expires_at=NULL,retain_until_resolved=?,resolved_at=?,updated_at=? WHERE job_id=? AND fence=?`, state, retain, resolved, now, jobID, expectedFence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

// PrepareUncertainImportRecovery records new inspection that either proves an
// uncertain import never started or makes its coherent partial effects
// claimable for exact-plan recovery. Unexplained bytes remain uncertain.
func (s *Store) PrepareUncertainImportRecovery(ctx context.Context, jobID string, expectedFence int64, state string, journal SyncJournalEntry, now string) error {
	if state != "validated" && state != "recovering" {
		return fmt.Errorf("unsupported uncertain import recovery state %q", state)
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if row.Kind != "import" || row.State != "uncertain" || row.Fence != expectedFence || row.ClaimOwner != "" {
		return ErrSyncPrecondition
	}
	if err := requireSyncRecoveryTransition(row.Kind, row.State, state); err != nil {
		return err
	}
	wantOutcome := "effect_unknown"
	if state == "validated" {
		wantOutcome = "effect_not_started"
	}
	if journal.JobID != jobID || journal.Fence != expectedFence || journal.Phase != "claim_recovery" || journal.Outcome != wantOutcome || journal.RecordedAt != now || journal.JournalID == "" {
		return fmt.Errorf("uncertain import recovery journal does not bind inspection")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries(job_id,journal_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JobID, journal.JournalID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state=?,claim_owner=NULL,claim_expires_at=NULL,updated_at=? WHERE job_id=? AND fence=?`, state, now, jobID, expectedFence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

// ResolveSupersededImport retires a no-effect in-progress import after a fully
// verified administrator checkpoint has advanced the approved remote beyond
// the stored target. The caller binds that checkpoint in the recovery journal.
func (s *Store) ResolveSupersededImport(ctx context.Context, jobID string, expectedFence int64, journal SyncJournalEntry, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if row.Kind != "import" || row.Fence != expectedFence || (row.State != "applying" && row.State != "recovering" && row.State != "uncertain") || (row.ClaimOwner != "" && !timeBefore(row.ClaimExpiresAt, now)) {
		return ErrSyncPrecondition
	}
	if err := requireSyncRecoveryTransition(row.Kind, row.State, "deferred"); err != nil {
		return err
	}
	if journal.JobID != jobID || journal.Fence != expectedFence || journal.Phase != "claim_recovery" || journal.Outcome != "effect_not_started" || journal.RecordedAt != now || journal.JournalID == "" {
		return fmt.Errorf("superseded import recovery journal does not bind checkpoint evidence")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries(job_id,journal_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JobID, journal.JournalID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='deferred',claim_owner=NULL,claim_expires_at=NULL,retain_until_resolved=0,resolved_at=?,updated_at=? WHERE job_id=? AND fence=?`, now, now, jobID, expectedFence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

// ReconcileUncertainPublication records an exact remote probe proving that an
// ambiguous publication candidate did not move the content ref, then makes the
// same signed candidate claimable again.
func (s *Store) ReconcileUncertainPublication(ctx context.Context, jobID string, expectedFence int64, journal SyncJournalEntry, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if row.Kind != "publication" || row.State != "uncertain" || row.Fence != expectedFence || row.ClaimOwner != "" {
		return ErrSyncPrecondition
	}
	if err := requireSyncTransition(row.Kind, row.State, "signed"); err != nil {
		return err
	}
	if journal.JobID != jobID || journal.Fence != expectedFence || journal.Phase != "claim_recovery" || journal.Outcome != "effect_not_started" || journal.RecordedAt != now || journal.JournalID == "" {
		return fmt.Errorf("uncertain publication recovery evidence does not bind the job")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='signed',updated_at=? WHERE job_id=? AND fence=? AND state='uncertain' AND claim_owner IS NULL`, now, jobID, expectedFence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

// ReconcileUncertainCheckpoint records a remote probe proving that the signed
// checkpoint candidate did not move the content ref, then reopens that exact
// candidate for the next explicit apply invocation.
func (s *Store) ReconcileUncertainCheckpoint(ctx context.Context, jobID string, expectedFence int64, journal SyncJournalEntry, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if row.Kind != "checkpoint" || row.State != "uncertain" || row.Fence != expectedFence || row.ClaimOwner != "" {
		return ErrSyncPrecondition
	}
	if err := requireSyncTransition(row.Kind, row.State, "applying"); err != nil {
		return err
	}
	if journal.JobID != jobID || journal.Fence != expectedFence || journal.Phase != "claim_recovery" || journal.Outcome != "effect_not_started" || journal.RecordedAt != now || journal.JournalID == "" {
		return fmt.Errorf("uncertain checkpoint recovery evidence does not bind the job")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`, journal.JournalID, journal.JobID, journal.Fence, journal.Phase, journal.Outcome, journal.EvidenceJSON, journal.RecordedAt); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='applying',updated_at=? WHERE job_id=? AND fence=? AND state='uncertain' AND claim_owner IS NULL`, now, jobID, expectedFence)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

func loadSyncJobByKey(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, groupID, kind, logicalKey string) (SyncJobRow, error) {
	return scanSyncJob(q.QueryRowContext(ctx, `SELECT job_id, group_id, kind, logical_key, request_fingerprint,
		state, payload_json, attempts, COALESCE(claim_owner,''), COALESCE(claim_expires_at,''), fence,
		retain_until_resolved, created_at, updated_at, COALESCE(resolved_at,'')
		FROM sync_jobs WHERE group_id = ? AND kind = ? AND logical_key = ?`, groupID, kind, logicalKey))
}

func loadSyncJob(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, jobID string) (SyncJobRow, error) {
	return scanSyncJob(q.QueryRowContext(ctx, `SELECT job_id, group_id, kind, logical_key, request_fingerprint,
		state, payload_json, attempts, COALESCE(claim_owner,''), COALESCE(claim_expires_at,''), fence,
		retain_until_resolved, created_at, updated_at, COALESCE(resolved_at,'')
		FROM sync_jobs WHERE job_id = ?`, jobID))
}

func scanSyncJob(row *sql.Row) (SyncJobRow, error) {
	var out SyncJobRow
	var retain int
	err := row.Scan(&out.JobID, &out.GroupID, &out.Kind, &out.LogicalKey, &out.RequestFingerprint,
		&out.State, &out.PayloadJSON, &out.Attempts, &out.ClaimOwner, &out.ClaimExpiresAt, &out.Fence,
		&retain, &out.CreatedAt, &out.UpdatedAt, &out.ResolvedAt)
	out.RetainUntilResolved = retain != 0
	return out, err
}

func timeBefore(left, right string) bool {
	a, aerr := time.Parse(time.RFC3339Nano, left)
	b, berr := time.Parse(time.RFC3339Nano, right)
	if aerr != nil || berr != nil {
		return left < right
	}
	return a.Before(b)
}

// SyncClaimLiveAt is the shared lease predicate used by publication recovery
// and the status projection. A malformed expiry is not a live claim.
func SyncClaimLiveAt(owner, expiry string, now time.Time) bool {
	if owner == "" {
		return false
	}
	until, err := time.Parse(time.RFC3339Nano, expiry)
	return err == nil && now.Before(until)
}

// Publication states before a signed candidate exists. Keep these beside the
// job validator and transitions; recovery status uses the same vocabulary.
const (
	preSignatureEligible = "eligible"
	preSignaturePrepared = "prepared"
)

func validSyncState(kind, state string) bool {
	allowed := map[string]map[string]bool{
		"publication":  {preSignatureEligible: true, preSignaturePrepared: true, "signed": true, "push_pending": true, "published": true, "blocked": true, "uncertain": true},
		"delivery":     {"pending": true, "attempted": true, "accepted": true, "retryable": true, "unknown": true, "refused": true},
		"import":       {"requested": true, "fetched": true, "validated": true, "applying": true, "applied": true, "deferred": true, "blocked": true, "recovering": true, "uncertain": true},
		"verification": {"planned": true, "collecting": true, "finished": true, "complete": true, "incomplete": true, "target_changed": true, "blocked": true, "expired": true},
		"membership":   {"planned": true, "applying": true, "applied": true, "blocked": true, "uncertain": true},
		"checkpoint":   {"planned": true, "applying": true, "applied": true, "blocked": true, "uncertain": true},
	}
	return allowed[kind][state]
}

// validSyncTransition is the ordinary durable edge table for sync job heads.
// Evidence-qualified recovery edges are declared alongside it in
// validSyncRecoveryTransition and may only be used after the caller validates
// the journal that proves the exceptional transition.
func validSyncTransition(kind, from, to string) bool {
	edges := map[string]map[string]map[string]bool{
		"publication": {
			preSignatureEligible: {preSignatureEligible: true, preSignaturePrepared: true, "blocked": true, "uncertain": true},
			preSignaturePrepared: {preSignaturePrepared: true, "signed": true, "blocked": true, "uncertain": true},
			"signed":             {"signed": true, "push_pending": true, "published": true, "blocked": true, "uncertain": true},
			"push_pending":       {"push_pending": true, "published": true, "blocked": true, "uncertain": true},
			"uncertain":          {"uncertain": true, "signed": true, "published": true},
		},
		"delivery": {
			"pending":   {"pending": true, "attempted": true, "accepted": true, "retryable": true, "unknown": true, "refused": true},
			"attempted": {"attempted": true, "accepted": true, "retryable": true, "unknown": true, "refused": true},
			"retryable": {"attempted": true, "accepted": true, "retryable": true, "unknown": true, "refused": true},
		},
		"import": {
			"requested":  {"requested": true, "fetched": true, "deferred": true, "blocked": true, "uncertain": true},
			"fetched":    {"fetched": true, "validated": true, "deferred": true, "blocked": true, "uncertain": true},
			"validated":  {"validated": true, "applying": true, "deferred": true, "blocked": true, "uncertain": true},
			"applying":   {"applying": true, "validated": true, "applied": true, "deferred": true, "recovering": true, "uncertain": true},
			"recovering": {"recovering": true, "validated": true, "applying": true, "applied": true, "uncertain": true},
			"deferred":   {"validated": true},
			"uncertain":  {"uncertain": true, "validated": true, "applied": true},
		},
		"verification": {
			"planned":    {"planned": true, "collecting": true, "blocked": true, "expired": true},
			"collecting": {"collecting": true, "finished": true, "blocked": true, "expired": true},
			"finished":   {"finished": true, "complete": true, "incomplete": true, "target_changed": true, "blocked": true, "expired": true},
		},
		"membership": {
			"planned":  {"planned": true, "applying": true, "applied": true, "blocked": true, "uncertain": true},
			"applying": {"applying": true, "applied": true, "blocked": true, "uncertain": true},
		},
		"checkpoint": {
			"planned":   {"planned": true, "applying": true, "applied": true, "blocked": true, "uncertain": true},
			"applying":  {"applying": true, "applied": true, "blocked": true, "uncertain": true},
			"uncertain": {"uncertain": true, "applying": true, "applied": true},
		},
	}
	return edges[kind][from][to]
}

func requireSyncTransition(kind, from, to string) error {
	if !validSyncTransition(kind, from, to) {
		return fmt.Errorf("state transition %q -> %q is invalid for sync job kind %q", from, to, kind)
	}
	return nil
}

// validSyncRecoveryTransition is the single list of exceptional edges that
// require binding recovery evidence. Keeping these edges explicit prevents a
// blocked job from becoming generally claimable merely because one recovery
// path is allowed to settle or reopen it.
func validSyncRecoveryTransition(kind, from, to string) bool {
	edges := map[string]map[string]map[string]bool{
		"import": {
			"applying":   {"deferred": true},
			"recovering": {"deferred": true},
			"uncertain":  {"validated": true, "recovering": true, "deferred": true},
		},
		"publication": {
			"signed":       {"published": true},
			"push_pending": {"published": true},
			"uncertain":    {"published": true},
			"blocked":      {"published": true, "signed": true},
		},
		"membership": {
			"planned":   {"applied": true},
			"applying":  {"applied": true},
			"uncertain": {"applied": true, "planned": true},
			"blocked":   {"applied": true, "planned": true},
		},
		"checkpoint": {
			"applying":  {"applied": true},
			"uncertain": {"applied": true, "applying": true},
			"blocked":   {"applied": true, "applying": true},
		},
	}
	return edges[kind][from][to]
}

func requireSyncRecoveryTransition(kind, from, to string) error {
	if !validSyncRecoveryTransition(kind, from, to) {
		return fmt.Errorf("recovery state transition %q -> %q is invalid for sync job kind %q", from, to, kind)
	}
	return nil
}

func resolveBlockedSyncJobs(ctx context.Context, tx *sql.Tx, groupID, resolution, resolverID, now string) error {
	var kinds map[string]bool
	switch resolution {
	case "membership_replaced":
		kinds = map[string]bool{"membership": true}
	case "checkpoint_reconciled":
		kinds = map[string]bool{"publication": true, "checkpoint": true, "import": true}
	default:
		return fmt.Errorf("unsupported blocked sync resolution %q", resolution)
	}
	rows, err := tx.QueryContext(ctx, `SELECT job_id,fence,kind FROM sync_jobs
		WHERE group_id=? AND state='blocked' AND resolved_at IS NULL ORDER BY job_id`, groupID)
	if err != nil {
		return err
	}
	type blockedJob struct {
		id    string
		fence int64
		kind  string
	}
	var jobs []blockedJob
	for rows.Next() {
		var job blockedJob
		if err := rows.Scan(&job.id, &job.fence, &job.kind); err != nil {
			rows.Close()
			return err
		}
		if kinds[job.kind] {
			jobs = append(jobs, job)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, job := range jobs {
		evidence, _ := json.Marshal(map[string]any{"resolution": resolution, "resolver_id": resolverID})
		digest := sha256.Sum256([]byte(resolution + "\x00" + resolverID + "\x00" + job.id))
		journalID := fmt.Sprintf("sync-resolution-%x", digest[:16])
		if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries
			(journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES(?,?,?,?,?,?,?)`,
			journalID, job.id, job.fence, "claim_recovery", "ok", string(evidence), now); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET retain_until_resolved=0,resolved_at=?
			WHERE job_id=? AND state='blocked' AND resolved_at IS NULL`, now, job.id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrSyncPrecondition
		}
	}
	return nil
}

func claimableSyncState(kind, state string) bool {
	terminal := map[string]map[string]bool{
		"publication":  {"published": true, "blocked": true, "uncertain": true},
		"delivery":     {"accepted": true, "refused": true, "unknown": true},
		"import":       {"applied": true, "deferred": true, "blocked": true, "uncertain": true},
		"verification": {"complete": true, "incomplete": true, "target_changed": true, "blocked": true, "expired": true},
		"membership":   {"applied": true, "blocked": true, "uncertain": true},
		"checkpoint":   {"applied": true, "blocked": true, "uncertain": true},
	}
	return validSyncState(kind, state) && !terminal[kind][state]
}

func unknownSyncState(kind string) string {
	switch kind {
	case "delivery":
		return "unknown"
	case "verification":
		return "blocked"
	default:
		return "uncertain"
	}
}

func syncRequestFingerprint(kind string, payload []byte) (string, string, error) {
	if !validSyncKind(kind) {
		return "", "", fmt.Errorf("unsupported sync job kind %q", kind)
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return "", "", fmt.Errorf("sync job payload: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", "", fmt.Errorf("sync job payload must contain one JSON value")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", "", fmt.Errorf("canonical sync job payload: %w", err)
	}
	h := sha256.New()
	_, _ = h.Write([]byte("agent-dispatch.sync-job/" + kind + "/v1"))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(canonical)
	_, _ = h.Write([]byte{'\n'})
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), string(canonical), nil
}

func validSyncKind(kind string) bool {
	switch kind {
	case "publication", "delivery", "import", "verification", "membership", "checkpoint":
		return true
	default:
		return false
	}
}

func validSyncJournal(phase, outcome string) bool {
	phases := map[string]bool{"claim_recovery": true, "publication": true, "delivery": true, "import": true, "verification": true, "membership": true, "checkpoint": true}
	outcomes := map[string]bool{"started": true, "prepared": true, "signed": true, "push_pending": true, "published": true, "accepted": true, "applied": true, "finished": true, "effect_not_started": true, "effect_unknown": true, "blocked": true, "deferred": true, "retryable": true, "refused": true, "failed": true, "ok": true}
	return phases[phase] && outcomes[outcome]
}

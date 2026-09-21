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
	"time"
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
	ConfigRevision string `json:"config_revision"`
	UpdatedAt      string `json:"updated_at"`
}

// SyncJobInput is one immutable logical admission.
type SyncJobInput struct {
	JobID          string
	GroupID        string
	Kind           string
	LogicalKey     string
	InitialState   string
	PayloadJSON    string
	ConfigRevision string
	QueueLimit     int
	Now            string
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
	JournalID    string `json:"journal_id"`
	JobID        string `json:"job_id"`
	Fence        int64  `json:"fence"`
	Phase        string `json:"phase"`
	Outcome      string `json:"outcome"`
	EvidenceJSON string `json:"evidence_json"`
	RecordedAt   string `json:"recorded_at"`
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
	if err := tx.QueryRowContext(ctx, `SELECT group_id, revision, state, reason, config_revision, updated_at
		FROM sync_controls WHERE group_id = ?`, groupID).Scan(
		&row.GroupID, &row.Revision, &row.State, &row.Reason, &row.ConfigRevision, &row.UpdatedAt); err != nil {
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
	err := s.QueryRowContext(ctx, `SELECT group_id, revision, state, reason, config_revision, updated_at
		FROM sync_controls WHERE group_id = ?`, groupID).Scan(
		&row.GroupID, &row.Revision, &row.State, &row.Reason, &row.ConfigRevision, &row.UpdatedAt)
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
	if err := tx.QueryRowContext(ctx, `SELECT group_id, revision, state, reason, config_revision, updated_at
		FROM sync_controls WHERE group_id = ?`, groupID).Scan(
		&current.GroupID, &current.Revision, &current.State, &current.Reason, &current.ConfigRevision, &current.UpdatedAt); err != nil {
		return SyncControlRow{}, err
	}
	if current.Revision != expectedRevision {
		return SyncControlRow{}, fmt.Errorf("control revision is %d, expected %d: %w", current.Revision, expectedRevision, ErrSyncPrecondition)
	}
	if current.State == "blocked" {
		return SyncControlRow{}, fmt.Errorf("control is blocked for %s and requires its recovery evidence: %w", current.Reason, ErrSyncControlHeld)
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
	contextJSON, _ := json.Marshal(map[string]any{"reason": reason, "config_revision": boundConfigRevision, "revision": expectedRevision + 1})
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
	var state, controlConfigRevision string
	if err := tx.QueryRowContext(ctx, `SELECT state, config_revision FROM sync_controls WHERE group_id = ?`, in.GroupID).Scan(&state, &controlConfigRevision); err != nil {
		return SyncJobRow{}, false, err
	}
	if in.ConfigRevision != "" && controlConfigRevision != in.ConfigRevision {
		return SyncJobRow{}, false, fmt.Errorf("control configuration revision changed: %w", ErrSyncPrecondition)
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_jobs WHERE group_id = ? AND resolved_at IS NULL`, in.GroupID).Scan(&pending); err != nil {
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

// ClaimSyncJob obtains a new fencing generation. An expired lease permits a
// new claim only through this increment; it never proves the old effect safe.
func (s *Store) ClaimSyncJob(ctx context.Context, jobID, owner, expectedConfigRevision, now, expiresAt string) (SyncJobRow, error) {
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
	var controlState, controlConfigRevision string
	if err := tx.QueryRowContext(ctx, `SELECT state, config_revision FROM sync_controls WHERE group_id = ?`, row.GroupID).Scan(&controlState, &controlConfigRevision); err != nil {
		return SyncJobRow{}, err
	}
	if controlState != "active" {
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

// FinishSyncJob records the measured head outcome under the live fencing
// generation and releases ownership. Journal rows are retained separately.
func (s *Store) FinishSyncJob(ctx context.Context, jobID, owner string, fence int64, state string, resolved bool, journal SyncJournalEntry, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := loadSyncJob(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if !validSyncState(row.Kind, state) {
		return fmt.Errorf("state %q is invalid for sync job kind %q", state, row.Kind)
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
		WHERE job_id = ? AND claim_owner = ? AND fence = ? AND claim_expires_at >= ?`,
		state, retain, resolvedAt, now, jobID, owner, fence, now)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
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

func validSyncState(kind, state string) bool {
	allowed := map[string]map[string]bool{
		"publication":  {"eligible": true, "prepared": true, "signed": true, "push_pending": true, "published": true, "blocked": true, "uncertain": true},
		"delivery":     {"pending": true, "attempted": true, "accepted": true, "retryable": true, "unknown": true, "refused": true},
		"import":       {"requested": true, "fetched": true, "validated": true, "applying": true, "applied": true, "deferred": true, "blocked": true, "recovering": true, "uncertain": true},
		"verification": {"planned": true, "collecting": true, "finished": true, "complete": true, "incomplete": true, "target_changed": true, "blocked": true, "expired": true},
		"membership":   {"planned": true, "applying": true, "applied": true, "blocked": true, "uncertain": true},
		"checkpoint":   {"planned": true, "applying": true, "applied": true, "blocked": true, "uncertain": true},
	}
	return allowed[kind][state]
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

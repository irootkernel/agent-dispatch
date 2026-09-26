package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	syncRecoveryInterval                  = 5 * time.Minute
	syncRecoveryBaseWait                  = 30 * time.Second
	SyncRecoveryReasonNone                = "none"
	SyncRecoveryReasonReconcileFailed     = "reconcile_failed"
	SyncRecoveryReasonInboxUnavailable    = "inbox_unavailable"
	SyncRecoveryReasonLocalRefUnavailable = "local_ref_unavailable"
	SyncRecoveryReasonInboxUnsettled      = "inbox_unsettled"
)

// SyncRecoverySchedule is the durable retry posture of the configured-ref
// worker. An empty attempt ID means no attempt is currently reserved.
type SyncRecoverySchedule struct {
	GroupID             string `json:"group_id"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	NextDueAt           string `json:"next_due_at"`
	LastAttemptAt       string `json:"last_attempt_at"`
	LastSuccessAt       string `json:"last_success_at"`
	LastReason          string `json:"last_reason"`
	AttemptID           string `json:"-"`
}

// PreSignaturePublications counts obligations that only an explicit publisher
// re-entry can advance. It does not infer that a newer publication retires one.
func (s *Store) PreSignaturePublications(ctx context.Context, groupID string, now time.Time) (int, string, error) {
	rows, err := s.QueryContext(ctx, `SELECT created_at,COALESCE(claim_owner,''),COALESCE(claim_expires_at,'') FROM sync_jobs WHERE group_id=? AND kind='publication' AND resolved_at IS NULL AND state IN (?,?)`, groupID, preSignatureEligible, preSignaturePrepared)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	count, oldest := 0, ""
	for rows.Next() {
		var created, owner, expiry string
		if err := rows.Scan(&created, &owner, &expiry); err != nil {
			return 0, "", err
		}
		if SyncClaimLiveAt(owner, expiry, now) {
			continue // an active publisher still owns this work
		}
		count++
		if oldest == "" || timeBefore(created, oldest) {
			oldest = created
		}
	}
	return count, oldest, rows.Err()
}

func scanSyncRecovery(row *sql.Row) (SyncRecoverySchedule, error) {
	var state SyncRecoverySchedule
	err := row.Scan(&state.GroupID, &state.ConsecutiveFailures, &state.NextDueAt,
		&state.LastAttemptAt, &state.LastSuccessAt, &state.LastReason, &state.AttemptID)
	return state, err
}

// LoadSyncRecoverySchedule reports the state when one has been created. The
// first service start creates it through ReserveSyncRecovery.
func (s *Store) LoadSyncRecoverySchedule(ctx context.Context, groupID string) (SyncRecoverySchedule, bool, error) {
	state, err := scanSyncRecovery(s.QueryRowContext(ctx, `SELECT group_id,consecutive_failures,next_due_at,last_attempt_at,last_success_at,last_reason,attempt_id FROM sync_recovery_schedule WHERE group_id=?`, groupID))
	if errors.Is(err, sql.ErrNoRows) {
		return SyncRecoverySchedule{}, false, nil
	}
	return state, err == nil, err
}

// ReserveSyncRecovery persists a crash-safe minimum delay before doing Git or
// filesystem work. A wake can advance a healthy periodic timer, but cannot
// bypass failure backoff. The owner-only service socket provides one worker;
// the attempt token also fences a late completion after a restart.
func (s *Store) ReserveSyncRecovery(ctx context.Context, groupID, attemptID string, now time.Time, wake bool) (bool, error) {
	if groupID == "" || attemptID == "" || len(attemptID) > 128 {
		return false, fmt.Errorf("invalid sync recovery identity")
	}
	now = now.UTC()
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO sync_recovery_schedule(group_id,next_due_at) VALUES (?,?)`, groupID, now.Format(time.RFC3339Nano)); err != nil {
		return false, err
	}
	var dueAt, currentAttempt string
	var failures int
	if err := tx.QueryRowContext(ctx, `SELECT next_due_at,consecutive_failures,attempt_id FROM sync_recovery_schedule WHERE group_id=?`, groupID).Scan(&dueAt, &failures, &currentAttempt); err != nil {
		return false, err
	}
	due, err := time.Parse(time.RFC3339Nano, dueAt)
	if err != nil {
		return false, err
	}
	if now.Before(due) && (!wake || failures != 0) {
		return false, tx.Commit()
	}
	// An in-flight owner may still be active. A reservation expires at its
	// recorded due time, so a dead owner never suppresses recovery forever.
	if currentAttempt != "" && now.Before(due) {
		return false, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `UPDATE sync_recovery_schedule SET attempt_id=?,last_attempt_at=?,next_due_at=? WHERE group_id=?`,
		attemptID, now.Format(time.RFC3339Nano), now.Add(syncRecoveryBaseWait).Format(time.RFC3339Nano), groupID)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// CompleteSyncRecovery advances the periodic timer or exponential retry from
// the exact reservation. Failed work remains inspectable and is never erased.
func (s *Store) CompleteSyncRecovery(ctx context.Context, groupID, attemptID string, now time.Time, settled bool, reason string) error {
	if !settled && reason != SyncRecoveryReasonReconcileFailed && reason != SyncRecoveryReasonInboxUnavailable && reason != SyncRecoveryReasonLocalRefUnavailable && reason != SyncRecoveryReasonInboxUnsettled {
		return fmt.Errorf("invalid recovery failure reason")
	}
	if settled {
		reason = SyncRecoveryReasonNone
	}
	now = now.UTC()
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var failures int
	if err := tx.QueryRowContext(ctx, `SELECT consecutive_failures FROM sync_recovery_schedule WHERE group_id=? AND attempt_id=?`, groupID, attemptID).Scan(&failures); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSyncPrecondition
		}
		return err
	}
	next := now.Add(syncRecoveryInterval)
	successAt := now.Format(time.RFC3339Nano)
	if !settled {
		if failures < 16 {
			failures++
		}
		wait := syncRecoveryBaseWait
		for i := 1; i < failures && wait < syncRecoveryInterval; i++ {
			wait *= 2
		}
		if wait > syncRecoveryInterval {
			wait = syncRecoveryInterval
		}
		next = now.Add(wait)
		successAt = ""
	} else {
		failures = 0
	}
	_, err = tx.ExecContext(ctx, `UPDATE sync_recovery_schedule SET consecutive_failures=?,next_due_at=?,last_reason=?,attempt_id='',last_success_at=CASE WHEN ?='' THEN last_success_at ELSE ? END WHERE group_id=? AND attempt_id=?`,
		failures, next.Format(time.RFC3339Nano), reason, successAt, successAt, groupID, attemptID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

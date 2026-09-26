package sqlite

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"
)

// InterruptedVerificationExpiryAge exceeds the verifier's live deadline.
const InterruptedVerificationExpiryAge = 5 * time.Minute

// FinishSyncVerification commits collection and its final disposition as one
// transaction. A crash before this transaction leaves the admitted planned
// job visible rather than manufacturing a successful observation.
func (s *Store) FinishSyncVerification(ctx context.Context, jobID, result, evidence, now string) error {
	if result != "complete" && result != "incomplete" && result != "target_changed" && result != "blocked" {
		return fmt.Errorf("invalid verification result %q", result)
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
	if row.Kind != "verification" || row.State != "planned" || row.Fence != 0 || row.ClaimOwner != "" || row.ResolvedAt != "" {
		return ErrSyncPrecondition
	}
	for _, step := range []struct{ state, outcome, payload string }{
		{"collecting", "started", `{"phase":"collecting"}`},
		{"finished", "finished", `{"phase":"finished"}`},
		{result, "ok", evidence},
	} {
		if err := requireSyncTransition("verification", row.State, step.state); err != nil {
			return err
		}
		if !validSyncJournal("verification", step.outcome) {
			return fmt.Errorf("invalid verification journal outcome %q", step.outcome)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,?,?,?,?,?)`,
			randomVerificationJournalID(), jobID, 1, "verification", step.outcome, step.payload, now); err != nil {
			return err
		}
		updated, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state=? WHERE job_id=? AND state=? AND fence=0`, step.state, jobID, row.State)
		if err != nil {
			return err
		}
		if count, _ := updated.RowsAffected(); count != 1 {
			return ErrSyncPrecondition
		}
		row.State = step.state
	}
	resolved := resolvedSyncState("verification", result)
	retain, resolvedAt := 1, any(nil)
	if resolved {
		resolvedAt = now
	}
	if result == "complete" {
		retain = 0
	}
	// A newer finished observation supersedes earlier retained failures.
	// Keep the latest incomplete result visible until that happens.
	if _, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET retain_until_resolved=0 WHERE group_id=? AND kind='verification' AND job_id<>? AND retain_until_resolved=1 AND resolved_at IS NOT NULL`, row.GroupID, jobID); err != nil {
		return err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET attempts=1, fence=1, retain_until_resolved=?, resolved_at=?, updated_at=? WHERE job_id=? AND state=? AND fence=0`, retain, resolvedAt, now, jobID, result)
	if err != nil {
		return err
	}
	if count, _ := updated.RowsAffected(); count != 1 {
		return ErrSyncPrecondition
	}
	return tx.Commit()
}

// ExpireInterruptedSyncVerifications resolves only abandoned planned attempts.
// The verifier's four-minute command deadline precedes this five-minute age.
func (s *Store) ExpireInterruptedSyncVerifications(ctx context.Context, groupID string, now time.Time) error {
	cutoff := now.Add(-InterruptedVerificationExpiryAge)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT job_id,created_at FROM sync_jobs WHERE group_id=? AND kind='verification' AND state='planned' AND resolved_at IS NULL AND claim_owner IS NULL ORDER BY created_at,job_id`, groupID)
	if err != nil {
		return err
	}
	var expired []string
	for rows.Next() {
		var id, created string
		if err := rows.Scan(&id, &created); err != nil {
			rows.Close()
			return err
		}
		at, err := time.Parse(time.RFC3339Nano, created)
		if err == nil && at.Before(cutoff) {
			expired = append(expired, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	stamp := now.UTC().Format(time.RFC3339Nano)
	for _, id := range expired {
		if err := requireSyncTransition("verification", "planned", "expired"); err != nil {
			return err
		}
		if !validSyncJournal("verification", "failed") {
			return fmt.Errorf("invalid verification expiry journal outcome")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sync_journal_entries (journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES (?,?,1,'verification','failed','{"reason":"interrupted_observation_expired"}',?)`, randomVerificationJournalID(), id, stamp); err != nil {
			return err
		}
		updated, err := tx.ExecContext(ctx, `UPDATE sync_jobs SET state='expired',attempts=1,fence=1,retain_until_resolved=0,resolved_at=?,updated_at=? WHERE job_id=? AND state='planned' AND fence=0`, stamp, stamp, id)
		if err != nil {
			return err
		}
		if count, _ := updated.RowsAffected(); count != 1 {
			return ErrSyncPrecondition
		}
	}
	return tx.Commit()
}

func randomVerificationJournalID() string {
	return "verification-journal-" + rand.Text()
}

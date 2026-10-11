package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
)

// ClaimManagementRequest never steals an expired running claim. Explicit
// journal/effect inspection must settle it through RecoverManagementClaim.
func (s *Store) ClaimManagementRequest(ctx context.Context, group, id, owner string, current syncmanagement.Binding, authorized bool, now time.Time) (ManagementRequest, error) {
	ctx, cancel := context.WithTimeout(ctx, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	if !syncmanagement.ValidID(owner) || now.IsZero() {
		return ManagementRequest{}, fmt.Errorf("invalid management claim")
	}
	tx, err := s.beginManagementTx(ctx, id)
	if err != nil {
		return ManagementRequest{}, err
	}
	defer tx.Rollback()
	r, err := loadManagement(ctx, tx, group, id)
	if err != nil {
		return r, err
	}
	if !authorized || !sameManagementAuthority(r.Binding, current) {
		return r, ErrManagementUnauthorized
	}
	if r.State != "queued" || syncmanagement.IsControl(r.Action) {
		return r, ErrSyncPrecondition
	}
	if r.Attempts >= syncmanagement.AttemptLimit {
		return r, ErrSyncQueueFull
	}
	// Retry branches share the original request's execution budget. Counting
	// retained attempts in its FK-protected family prevents repeated retries of
	// an early predecessor from manufacturing another twenty executions.
	lineageAttempts, err := managementLineageAttempts(ctx, tx, r.RequestID)
	if err != nil {
		return r, err
	}
	if lineageAttempts >= syncmanagement.AttemptLimit {
		return r, ErrSyncQueueFull
	}
	var state, membership string
	if err := tx.QueryRowContext(ctx, `SELECT state,membership_mode FROM sync_controls WHERE group_id=?`, group).Scan(&state, &membership); err != nil {
		return r, err
	}
	if state != "active" || membership != "normal" {
		return r, ErrSyncControlHeld
	}
	lease := managementTime(now.Add(syncmanagement.LeaseSeconds * time.Second))
	deadline := managementTime(now.Add(syncmanagement.ExecutionSeconds * time.Second))
	res, err := tx.ExecContext(ctx, `UPDATE sync_management_requests SET state='running',revision=revision+1,attempts=attempts+1,fence=fence+1,claim_owner=?,claim_expires_at=?,execution_deadline=?,outcome_json=NULL WHERE request_id=? AND revision=? AND state='queued'`, owner, lease, deadline, id, r.Revision)
	if err != nil {
		return r, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return r, ErrSyncPrecondition
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_management_executions(request_id,fence,owner,started_at,deadline) VALUES(?,?,?,?,?)`, id, r.Fence+1, owner, managementTime(now), deadline); err != nil {
		return r, err
	}
	r, err = loadManagement(ctx, tx, group, id)
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}

func managementHeadMatches(r, expected ManagementRequest) bool {
	return r.RequestID == expected.RequestID && r.GroupID == expected.GroupID && r.State == expected.State && r.Revision == expected.Revision && r.Fence == expected.Fence && r.ClaimOwner == expected.ClaimOwner
}

func managementClaimLive(r ManagementRequest, now time.Time) bool {
	return r.State == "running" && !now.IsZero() && now.Before(mustManagementTime(r.ClaimExpiresAt)) && now.Before(mustManagementTime(r.ExecutionDeadline))
}

// RenewManagementClaim retains owner, fence, attempt count and original deadline.
func (s *Store) RenewManagementClaim(ctx context.Context, expected ManagementRequest, now time.Time) (ManagementRequest, error) {
	ctx, cancel := context.WithTimeout(ctx, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	tx, err := s.beginManagementTx(ctx, expected.RequestID)
	if err != nil {
		return ManagementRequest{}, err
	}
	defer tx.Rollback()
	r, err := loadManagement(ctx, tx, expected.GroupID, expected.RequestID)
	if err != nil {
		return r, err
	}
	if !managementHeadMatches(r, expected) || !managementClaimLive(r, now) {
		return r, ErrSyncPrecondition
	}
	lease := now.Add(syncmanagement.LeaseSeconds * time.Second)
	deadline := mustManagementTime(r.ExecutionDeadline)
	if lease.After(deadline) {
		lease = deadline
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sync_management_requests SET claim_expires_at=?,revision=revision+1 WHERE request_id=? AND revision=?`, managementTime(lease), r.RequestID, r.Revision); err != nil {
		return r, err
	}
	r, err = loadManagement(ctx, tx, r.GroupID, r.RequestID)
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}

// TransitionManagementRequest persists the result before releasing a live claim.
// Queued/held transitions require the exact revision too, preventing an ABA write.
func (s *Store) TransitionManagementRequest(ctx context.Context, expected ManagementRequest, state string, outcome *syncmanagement.Outcome, evidenceReference string, now time.Time) (ManagementRequest, error) {
	return s.transitionManagement(ctx, expected, state, outcome, evidenceReference, now, false)
}

// RecoverManagementClaim requires an explicit caller inspection reference; lease
// age only makes inspection eligible. It never chooses a state from age alone.
func (s *Store) RecoverManagementClaim(ctx context.Context, expected ManagementRequest, state string, outcome *syncmanagement.Outcome, inspectionReference string, now time.Time) (ManagementRequest, error) {
	return s.transitionManagement(ctx, expected, state, outcome, inspectionReference, now, true)
}

func (s *Store) transitionManagement(ctx context.Context, expected ManagementRequest, state string, outcome *syncmanagement.Outcome, evidence string, now time.Time, recovery bool) (ManagementRequest, error) {
	ctx, cancel := context.WithTimeout(ctx, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	if !syncmanagement.ValidID(evidence) || now.IsZero() {
		return ManagementRequest{}, fmt.Errorf("management transition requires bounded inspected evidence")
	}
	tx, err := s.beginManagementTx(ctx, expected.RequestID)
	if err != nil {
		return ManagementRequest{}, err
	}
	defer tx.Rollback()
	r, err := loadManagement(ctx, tx, expected.GroupID, expected.RequestID)
	if err != nil {
		return r, err
	}
	if !managementHeadMatches(r, expected) || !syncmanagement.CanTransition(r.State, state) || r.AcceptedAt == nil {
		return r, ErrSyncPrecondition
	}
	if recovery {
		if r.State != "running" || managementClaimLive(r, now) {
			return r, ErrSyncPrecondition
		}
	} else if r.State == "running" && !managementClaimLive(r, now) {
		return r, ErrSyncPrecondition
	}
	if state == "running" || state == "prepared" || state == "expired" {
		return r, ErrSyncPrecondition
	}
	if state == "finished" && outcome == nil || state == "queued" && outcome != nil {
		return r, fmt.Errorf("management transition outcome does not match state")
	}
	if state == "finished" && r.Action == "verify-all" {
		switch outcome.Status {
		case "complete", "incomplete", "target_changed", "expired", "failed":
		default:
			return r, fmt.Errorf("invalid finished verification outcome")
		}
	}
	if state == "blocked" && (outcome == nil || outcome.Status != "blocked") || state == "uncertain" && (outcome == nil || outcome.Status != "uncertain") {
		return r, fmt.Errorf("held management transition lacks its result")
	}
	outcomeJSON, err := managementOutcomeJSON(r.Action, outcome)
	if err != nil {
		return r, err
	}
	at := managementTime(now)
	var resolved any
	if state == "finished" {
		resolved = at
	}
	if r.State == "running" {
		res, err := tx.ExecContext(ctx, `UPDATE sync_management_executions SET ended_at=?,disposition=?,evidence_reference=?,outcome_json=? WHERE request_id=? AND fence=? AND owner=? AND ended_at IS NULL`, at, state, evidence, outcomeJSON, r.RequestID, r.Fence, r.ClaimOwner)
		if err != nil {
			return r, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return r, ErrSyncPrecondition
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE sync_management_requests SET state=?,revision=revision+1,resolved_at=?,outcome_json=?,claim_owner=NULL,claim_expires_at=NULL,execution_deadline=NULL,association_reserved=0 WHERE request_id=? AND revision=?`, state, resolved, outcomeJSON, r.RequestID, r.Revision)
	if err != nil {
		return r, err
	}
	r, err = loadManagement(ctx, tx, r.GroupID, r.RequestID)
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}

// NextQueuedManagementRequest returns only one exact identity. The existing
// worker owns dispatch fairness; storage introduces no scheduler or executor.
func (s *Store) NextQueuedManagementRequest(ctx context.Context, group string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	var id string
	err := s.QueryRowContext(ctx, `SELECT request_id FROM sync_management_requests WHERE group_id=? AND state='queued' ORDER BY attempts,accepted_at,request_id LIMIT 1`, group).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// ReserveManagementAssociations must precede effects. A reservation belongs to
// the current fenced attempt, is consumed by immutable association insertion,
// and is released when that attempt is settled.
func (s *Store) ReserveManagementAssociations(ctx context.Context, expected ManagementRequest, count int, now time.Time) (ManagementRequest, error) {
	ctx, cancel := context.WithTimeout(ctx, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	if count < 1 || count > syncmanagement.AssociationLimit {
		return ManagementRequest{}, ErrSyncQueueFull
	}
	tx, err := s.beginManagementTx(ctx, expected.RequestID)
	if err != nil {
		return ManagementRequest{}, err
	}
	defer tx.Rollback()
	r, err := loadManagement(ctx, tx, expected.GroupID, expected.RequestID)
	if err != nil {
		return r, err
	}
	if !managementHeadMatches(r, expected) || !managementClaimLive(r, now) {
		return r, ErrSyncPrecondition
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_management_jobs WHERE request_id=?`, r.RequestID).Scan(&n); err != nil {
		return r, err
	}
	if n+r.AssociationReserved+count > syncmanagement.AssociationLimit {
		return r, ErrSyncQueueFull
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sync_management_requests SET association_reserved=association_reserved+?,revision=revision+1 WHERE request_id=?`, count, r.RequestID); err != nil {
		return r, err
	}
	r, err = loadManagement(ctx, tx, r.GroupID, r.RequestID)
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}

func (s *Store) AssociateManagementJob(ctx context.Context, expected ManagementRequest, job ManagementJob, now time.Time) (ManagementRequest, error) {
	ctx, cancel := context.WithTimeout(ctx, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	if !syncmanagement.ValidID(job.JobID) || job.TargetCommit != nil && !syncmanagement.ValidCommit(*job.TargetCommit) {
		return ManagementRequest{}, fmt.Errorf("invalid management job association")
	}
	tx, err := s.beginManagementTx(ctx, expected.RequestID)
	if err != nil {
		return ManagementRequest{}, err
	}
	defer tx.Rollback()
	r, err := loadManagement(ctx, tx, expected.GroupID, expected.RequestID)
	if err != nil {
		return r, err
	}
	if !managementHeadMatches(r, expected) || !managementClaimLive(r, now) {
		return r, ErrSyncPrecondition
	}
	var kind, group string
	if err := tx.QueryRowContext(ctx, `SELECT kind,group_id FROM sync_jobs WHERE job_id=?`, job.JobID).Scan(&kind, &group); err != nil {
		return r, err
	}
	if kind != job.Kind || group != r.GroupID {
		return r, ErrSyncAdmissionConflict
	}
	var old ManagementJob
	err = tx.QueryRowContext(ctx, `SELECT kind,target_commit,association_reason FROM sync_management_jobs WHERE request_id=? AND job_id=?`, r.RequestID, job.JobID).Scan(&old.Kind, &old.TargetCommit, &old.AssociationReason)
	if err == nil {
		sameTarget := old.TargetCommit == nil && job.TargetCommit == nil || old.TargetCommit != nil && job.TargetCommit != nil && *old.TargetCommit == *job.TargetCommit
		if old.Kind != job.Kind || old.AssociationReason != job.AssociationReason || !sameTarget {
			return r, ErrSyncAdmissionConflict
		}
		return r, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return r, err
	}
	if r.AssociationReserved < 1 {
		return r, ErrSyncQueueFull
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_management_jobs(request_id,job_id,kind,target_commit,association_reason) VALUES(?,?,?,?,?)`, r.RequestID, job.JobID, job.Kind, job.TargetCommit, job.AssociationReason); err != nil {
		return r, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sync_management_requests SET association_reserved=association_reserved-1,revision=revision+1 WHERE request_id=?`, r.RequestID); err != nil {
		return r, err
	}
	r, err = loadManagement(ctx, tx, r.GroupID, r.RequestID)
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}

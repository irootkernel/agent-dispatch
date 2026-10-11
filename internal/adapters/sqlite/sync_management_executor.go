package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

// This is an additive migration; the installed request-ledger migration is immutable.
const schemaV29SyncManagementExecutor = `
CREATE TABLE sync_management_executor (
 group_id TEXT PRIMARY KEY,
 instance_id TEXT NOT NULL,
 state_incarnation_id TEXT NOT NULL,
 policy_revision TEXT NOT NULL,
 config_revision TEXT NOT NULL,
 owner TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('available','stopped')),
 observed_at TEXT NOT NULL
);
`

// StartManagementExecutor records the owner of the already-started local worker.
// It neither installs nor starts any service.
func (s *Store) StartManagementExecutor(ctx context.Context, b syncmanagement.Binding, owner string, now time.Time) error {
	if !syncmanagement.ValidID(owner) || now.IsZero() {
		return fmt.Errorf("invalid executor evidence")
	}
	_, err := s.ExecContext(ctx, `INSERT INTO sync_management_executor(group_id,instance_id,state_incarnation_id,policy_revision,config_revision,owner,state,observed_at)
 VALUES(?,?,?,?,?,?,'available',?) ON CONFLICT(group_id) DO UPDATE SET instance_id=excluded.instance_id,state_incarnation_id=excluded.state_incarnation_id,policy_revision=excluded.policy_revision,config_revision=excluded.config_revision,owner=excluded.owner,state='available',observed_at=excluded.observed_at`, b.GroupID, b.InstanceID, b.StateIncarnationID, b.PolicyRevision, b.ConfigRevision, owner, managementTime(now))
	return err
}

// RecordManagementExecutor cannot let a previous owner refresh or stop a successor.
func (s *Store) RecordManagementExecutor(ctx context.Context, b syncmanagement.Binding, owner, state string, now time.Time) error {
	if state != "available" && state != "stopped" || now.IsZero() {
		return fmt.Errorf("invalid executor state")
	}
	res, err := s.ExecContext(ctx, `UPDATE sync_management_executor SET state=?,observed_at=? WHERE group_id=? AND instance_id=? AND state_incarnation_id=? AND policy_revision=? AND config_revision=? AND owner=?`, state, managementTime(now), b.GroupID, b.InstanceID, b.StateIncarnationID, b.PolicyRevision, b.ConfigRevision, owner)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSyncPrecondition
	}
	return nil
}

// CheckManagementCapabilitiesSchema verifies immutable existing history without
// requiring the request ledger. Capabilities also supports pre-management state.
func (s *Store) CheckManagementCapabilitiesSchema(ctx context.Context) error {
	applied, err := s.appliedMigrationsContext(ctx)
	if err != nil {
		return err
	}
	newest := 0
	for version := range applied {
		newest = max(newest, version)
	}
	if newest > Migrations[len(Migrations)-1].Version {
		return &ErrSchemaTooNewType{Detail: "state schema is newer than this binary"}
	}
	for _, m := range Migrations {
		if m.Version <= newest && applied[m.Version] != m.checksum() {
			return fmt.Errorf("migration ledger does not match immutable history")
		}
	}
	return nil
}

// ManagementExecutorAvailability is an exact local read. Stale or absent evidence
// is unknown, including schema28 databases where request lookup remains supported.
func (s *Store) ManagementExecutorAvailability(ctx context.Context, b syncmanagement.Binding, now time.Time) (string, error) {
	var version int
	if err := s.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		return "unknown", err
	}
	if version < 29 {
		return "unknown", nil
	}
	var observed, state string
	err := s.QueryRowContext(ctx, `SELECT state,observed_at FROM sync_management_executor WHERE group_id=? AND instance_id=? AND state_incarnation_id=? AND policy_revision=? AND config_revision=?`, b.GroupID, b.InstanceID, b.StateIncarnationID, b.PolicyRevision, b.ConfigRevision).Scan(&state, &observed)
	if errors.Is(err, sql.ErrNoRows) {
		return "unknown", nil
	}
	if err != nil {
		return "unknown", err
	}
	at, err := time.Parse(time.RFC3339Nano, observed)
	if err != nil {
		return "unknown", err
	}
	if now.Before(at) {
		return "unknown", nil
	}
	if state == "stopped" {
		return "stopped", nil
	}
	if now.Sub(at) > 60*time.Second {
		return "unknown", nil
	}
	return "available", nil
}

// ManagementContentPending excludes only exact live verification bookkeeping.
// Queued/running sync, retry and held uncertainty remain content-affecting.
func (s *Store) ManagementContentPending(ctx context.Context, group, verificationRequest string) (bool, error) {
	var count int
	err := s.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_management_requests WHERE group_id=? AND state IN ('queued','running','blocked','uncertain') AND NOT (request_id=? AND action='verify-all' AND state='running')`, group, verificationRequest).Scan(&count)
	return count != 0, err
}

// FinishManagementPublicationJob records the confirmed publication, its delivery
// obligation and the reserved request association in one transaction. Current
// control gates new Git effects; this evidence-only commit retains a live exact
// management owner/fence even when pause has committed after those effects.
func (s *Store) FinishManagementPublicationJob(ctx context.Context, expected ManagementRequest, jobID, owner string, fence int64, journal SyncJournalEntry, delivery SyncJobInput, target string, now time.Time) (ManagementRequest, error) {
	ctx, cancel := context.WithTimeout(ctx, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	nudge, err := syncrecords.DecodeNudge([]byte(delivery.PayloadJSON))
	if err != nil || nudge.TargetCommit != target || !syncmanagement.ValidCommit(target) {
		return ManagementRequest{}, ErrSyncAdmissionConflict
	}
	var associated ManagementRequest
	associate := func(tx *sql.Tx, job SyncJobRow) error {
		r, err := loadManagement(ctx, tx, expected.GroupID, expected.RequestID)
		if err != nil {
			return err
		}
		if !managementHeadMatches(r, expected) || !managementClaimLive(r, time.Now()) {
			return ErrSyncPrecondition
		}
		if (r.Action != "sync-now" && r.Action != "retry") || job.GroupID != r.GroupID || job.Kind != "delivery" {
			return ErrSyncAdmissionConflict
		}
		reason := "already_signed_recovery"
		if r.Action == "retry" {
			reason = "retry_obligation"
		}
		var publicationTarget, publicationReason string
		if err := tx.QueryRowContext(ctx, `SELECT target_commit,association_reason FROM sync_management_jobs WHERE request_id=? AND job_id=? AND kind='publication'`, r.RequestID, jobID).Scan(&publicationTarget, &publicationReason); err != nil {
			return err
		}
		if publicationTarget != target || publicationReason != reason {
			return ErrSyncAdmissionConflict
		}
		var oldTarget, oldReason string
		err = tx.QueryRowContext(ctx, `SELECT target_commit,association_reason FROM sync_management_jobs WHERE request_id=? AND job_id=?`, r.RequestID, job.JobID).Scan(&oldTarget, &oldReason)
		if err == nil {
			if oldTarget != target || oldReason != reason {
				return ErrSyncAdmissionConflict
			}
		} else {
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if r.AssociationReserved < 1 {
				return ErrSyncQueueFull
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO sync_management_jobs(request_id,job_id,kind,target_commit,association_reason) VALUES(?,?,'delivery',?,?)`, r.RequestID, job.JobID, target, reason); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE sync_management_requests SET association_reserved=association_reserved-1,revision=revision+1 WHERE request_id=?`, r.RequestID); err != nil {
				return err
			}
		}
		associated, err = loadManagement(ctx, tx, r.GroupID, r.RequestID)
		return err
	}
	nowText := now.UTC().Format(time.RFC3339Nano)
	if owner == "" {
		_, err = s.finishRecoveredPublicationJob(ctx, jobID, fence, journal, delivery, nowText, associate)
	} else {
		_, err = s.finishPublicationJob(ctx, jobID, owner, fence, journal, delivery, nowText, associate)
	}
	if err != nil {
		return ManagementRequest{}, err
	}
	return associated, nil
}

// AdmitManagementSyncJob commits a child intent and its exact reserved request
// association together. This metadata grants no subsequent Git/network effect;
// callers must recheck current control before performing such an effect.
func (s *Store) AdmitManagementSyncJob(ctx context.Context, expected ManagementRequest, in SyncJobInput, target, reason string) (SyncJobRow, bool, ManagementRequest, error) {
	ctx, cancel := context.WithTimeout(ctx, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	if in.GroupID != expected.GroupID || !syncmanagement.ValidCommit(target) {
		return SyncJobRow{}, false, ManagementRequest{}, ErrSyncAdmissionConflict
	}
	switch in.Kind {
	case "import":
		record, err := syncrecords.DecodeImport([]byte(in.PayloadJSON))
		if err != nil || expected.Action != "sync-now" || reason != "observed_reconciliation" || record.GroupID != in.GroupID || record.ImportID != in.LogicalKey || record.TargetCommit != target {
			return SyncJobRow{}, false, ManagementRequest{}, ErrSyncAdmissionConflict
		}
	case "verification":
		var planned struct {
			ID     string `json:"verification_id"`
			Group  string `json:"group_id"`
			Target string `json:"target_commit"`
			Phase  string `json:"phase"`
		}
		if json.Unmarshal([]byte(in.PayloadJSON), &planned) != nil || expected.Action != "verify-all" || reason != "verification_target" || planned.ID != in.JobID || planned.ID != in.LogicalKey || planned.Group != in.GroupID || planned.Target != target || planned.Phase != "planned" {
			return SyncJobRow{}, false, ManagementRequest{}, ErrSyncAdmissionConflict
		}
	default:
		return SyncJobRow{}, false, ManagementRequest{}, ErrSyncAdmissionConflict
	}
	var associated ManagementRequest
	associate := func(tx *sql.Tx, job SyncJobRow) error {
		r, err := loadManagement(ctx, tx, expected.GroupID, expected.RequestID)
		if err != nil {
			return err
		}
		if !managementHeadMatches(r, expected) || !managementClaimLive(r, time.Now()) {
			return ErrSyncPrecondition
		}
		var oldKind, oldTarget, oldReason string
		err = tx.QueryRowContext(ctx, `SELECT kind,target_commit,association_reason FROM sync_management_jobs WHERE request_id=? AND job_id=?`, r.RequestID, job.JobID).Scan(&oldKind, &oldTarget, &oldReason)
		if err == nil {
			if oldKind != job.Kind || oldTarget != target || oldReason != reason {
				return ErrSyncAdmissionConflict
			}
		} else {
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if r.AssociationReserved < 1 {
				return ErrSyncQueueFull
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO sync_management_jobs(request_id,job_id,kind,target_commit,association_reason) VALUES(?,?,?,?,?)`, r.RequestID, job.JobID, job.Kind, target, reason); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE sync_management_requests SET association_reserved=association_reserved-1,revision=revision+1 WHERE request_id=?`, r.RequestID); err != nil {
				return err
			}
		}
		associated, err = loadManagement(ctx, tx, r.GroupID, r.RequestID)
		return err
	}
	job, reused, err := s.admitSyncJob(ctx, in, expected.RequestID, associate)
	if err != nil {
		return SyncJobRow{}, false, ManagementRequest{}, err
	}
	return job, reused, associated, nil
}

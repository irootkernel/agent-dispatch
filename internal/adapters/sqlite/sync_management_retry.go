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

// ManagementObligation describes only a recorded immutable association. Retry
// admission and recovery inspect these heads rather than discovering new jobs.
type ManagementObligation struct {
	ManagementJob
	Settled, Eligible bool
	payloadJSON       string
	Verification      *syncrecords.Verification
}

func managementObligations(ctx context.Context, q managementQuerier, r ManagementRequest, now time.Time) ([]ManagementObligation, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.job_id,a.kind,a.association_reason,a.target_commit,j.state,j.attempts,j.payload_json,COALESCE(j.resolved_at,''),COALESCE(j.claim_owner,''),COALESCE(j.claim_expires_at,'')
	 FROM sync_management_jobs a JOIN sync_jobs j ON j.job_id=a.job_id AND j.group_id=? AND j.kind=a.kind WHERE a.request_id=? ORDER BY a.job_id LIMIT 1001`, r.GroupID, r.RequestID)
	if err != nil {
		return nil, err
	}
	var out []ManagementObligation
	for rows.Next() {
		var o ManagementObligation
		var resolved, owner, expiry string
		if err := rows.Scan(&o.JobID, &o.Kind, &o.AssociationReason, &o.TargetCommit, &o.State, &o.Attempts, &o.payloadJSON, &resolved, &owner, &expiry); err != nil {
			rows.Close()
			return nil, err
		}
		o.Settled = resolved != ""
		if owner != "" {
			if _, err := time.Parse(time.RFC3339Nano, expiry); err != nil {
				rows.Close()
				return nil, fmt.Errorf("invalid recorded obligation claim")
			}
		}
		if o.Settled {
			if _, err := time.Parse(time.RFC3339Nano, resolved); err != nil || !resolvedSyncState(o.Kind, o.State) {
				rows.Close()
				return nil, fmt.Errorf("invalid recorded obligation resolution")
			}
		}
		// Ordinary sync resolves a deferral to release active-queue capacity.
		// Its protected effect is still held, so management cannot infer an
		// applied obligation or reopen it through explicit retry.
		if o.Kind == "import" && o.State == "deferred" {
			o.Settled = false
		}
		o.Eligible = !o.Settled && o.Attempts < syncmanagement.AttemptLimit && !SyncClaimLiveAt(owner, expiry, now) && o.TargetCommit != nil && syncmanagement.ValidCommit(*o.TargetCommit)
		if o.Eligible {
			switch o.Kind {
			case "import":
				p, err := syncrecords.DecodeImport([]byte(o.payloadJSON))
				o.Eligible = err == nil && !p.ControllerOnly && p.GroupID == r.GroupID && p.TargetCommit == *o.TargetCommit && (o.State == "validated" || o.State == "applying" || o.State == "recovering" || o.State == "uncertain")
			case "publication":
				p, err := syncrecords.DecodePublication([]byte(o.payloadJSON))
				o.Eligible = err == nil && p.GroupID == r.GroupID && p.Publisher == r.InstanceID && p.StateIncarnationID == r.StateIncarnationID && (o.State == "signed" || o.State == "uncertain")
			case "delivery":
				p, err := syncrecords.DecodeNudge([]byte(o.payloadJSON))
				o.Eligible = err == nil && p.GroupID == r.GroupID && p.TargetCommit == *o.TargetCommit && (o.State == "pending" || o.State == "retryable" || o.State == "unknown")
			default:
				o.Eligible = false
			}
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(out) > syncmanagement.AssociationLimit {
		return nil, ErrSyncQueueFull
	}
	var associations int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_management_jobs WHERE request_id=?`, r.RequestID).Scan(&associations); err != nil {
		return nil, err
	}
	if associations != len(out) {
		return nil, ErrSyncAdmissionConflict
	}
	// A signed head alone is insufficient: the durable journal must name the
	// exact candidate that was associated before publication effects.
	for i := range out {
		o := &out[i]
		if o.Settled && o.Kind == "verification" && o.State != "expired" {
			var evidence string
			if err := q.QueryRowContext(ctx, `SELECT evidence_json FROM sync_journal_entries WHERE job_id=? AND phase='verification' AND outcome='ok' ORDER BY sequence DESC LIMIT 1`, o.JobID).Scan(&evidence); err != nil {
				return nil, err
			}
			var v struct {
				Verification syncrecords.Verification `json:"verification"`
			}
			if json.Unmarshal([]byte(evidence), &v) != nil || v.Verification.Validate() != nil || v.Verification.Phase != "finished" || v.Verification.VerificationID != o.JobID || v.Verification.GroupID != r.GroupID || o.TargetCommit == nil || v.Verification.TargetCommit != *o.TargetCommit || v.Verification.Result != o.State {
				return nil, fmt.Errorf("invalid recorded verification evidence")
			}
			o.Verification = &v.Verification
		}
		if !o.Eligible || o.Kind != "publication" {
			continue
		}
		var evidence string
		err := q.QueryRowContext(ctx, `SELECT evidence_json FROM sync_journal_entries WHERE job_id=? AND json_extract(evidence_json,'$.candidate') IS NOT NULL ORDER BY sequence DESC LIMIT 1`, o.JobID).Scan(&evidence)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		var v struct {
			Candidate string `json:"candidate"`
		}
		o.Eligible = err == nil && json.Unmarshal([]byte(evidence), &v) == nil && v.Candidate == *o.TargetCommit
	}
	return out, nil
}

func retryObligations(ctx context.Context, q managementQuerier, b syncmanagement.Binding, now time.Time) (ManagementRequest, []ManagementObligation, error) {
	prev, err := loadManagement(ctx, q, b.GroupID, *b.TargetRequestID)
	if err != nil {
		return prev, nil, err
	}
	if prev.InstanceID != b.InstanceID || prev.StateIncarnationID != b.StateIncarnationID {
		return prev, nil, ErrManagementObsolete
	}
	if prev.AcceptedAt == nil || !sameManagementAuthority(prev.Binding, b) {
		return prev, nil, ErrManagementUnauthorized
	}
	if syncmanagement.IsControl(prev.Action) {
		return prev, nil, ErrManagementNotRetryable
	}
	jobs, err := managementObligations(ctx, q, prev, now)
	if err != nil {
		return prev, nil, err
	}
	settled, eligible := len(jobs) > 0, false
	for _, j := range jobs {
		settled = settled && j.Settled
		eligible = eligible || j.Eligible
	}
	if !settled && (!eligible || prev.Attempts >= syncmanagement.AttemptLimit) {
		return prev, nil, ErrManagementNotRetryable
	}
	if !settled {
		n, err := managementLineageAttempts(ctx, q, prev.RequestID)
		if err != nil {
			return prev, nil, err
		}
		if n >= syncmanagement.AttemptLimit {
			return prev, nil, ErrManagementNotRetryable
		}
	}
	return prev, jobs, nil
}

func managementLineageAttempts(ctx context.Context, q managementQuerier, id string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `WITH RECURSIVE ancestors(request_id,target_request_id) AS (
	 SELECT request_id,target_request_id FROM sync_management_requests WHERE request_id=?
	 UNION SELECT p.request_id,p.target_request_id FROM sync_management_requests p JOIN ancestors a ON p.request_id=a.target_request_id
	), family(request_id) AS (
	 SELECT request_id FROM ancestors WHERE target_request_id IS NULL
	 UNION SELECT c.request_id FROM sync_management_requests c JOIN family f ON c.target_request_id=f.request_id
	) SELECT COUNT(*) FROM sync_management_executions e JOIN family f ON f.request_id=e.request_id`, id).Scan(&n)
	return n, err
}

// InspectManagementObligations is a bounded read-only snapshot. The caller
// still fences any resulting ledger transition and rechecks effect authority.
func (s *Store) InspectManagementObligations(ctx context.Context, r ManagementRequest, now time.Time) ([]ManagementObligation, error) {
	ctx, cancel := context.WithTimeout(ctx, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	tx, err := s.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	jobs, err := managementObligations(ctx, tx, r, now)
	if err != nil {
		return nil, err
	}
	return jobs, tx.Commit()
}

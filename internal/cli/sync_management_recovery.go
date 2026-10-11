package cli

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/app/maintenance"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
)

// Each existing recovery pass inspects one interrupted request after periodic
// recovery. A round-robin cursor lets retained holds yield to other requests.
// This inspection settles evidence even after management authority is revoked.
func (s *peerService) recoverManagement(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, syncmanagement.CallSeconds*time.Second)
	defer cancel()
	now := time.Now()
	// Planned verification cannot regain the old observation window. Expire
	// its durable admission through the existing verifier's recovery primitive.
	if err := s.store.ExpireInterruptedSyncVerifications(ctx, s.cfg.Sync.GroupID, now); err != nil {
		s.warn("interrupted verification expiry remains pending")
		return
	}
	var id string
	err := s.store.QueryRowContext(ctx, `SELECT request_id FROM sync_management_requests WHERE group_id=? AND
	 (state IN ('blocked','uncertain') OR (state='running' AND (claim_expires_at<=? OR execution_deadline<=?)))
	 ORDER BY (request_id>?) DESC,request_id LIMIT 1`, s.cfg.Sync.GroupID, now.UTC().Format("2006-01-02T15:04:05.000000000Z"), now.UTC().Format("2006-01-02T15:04:05.000000000Z"), s.managementRecoveryAfter).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		s.warn("management recovery lookup unavailable")
		return
	}
	s.managementRecoveryAfter = id
	r, err := s.store.LoadManagementRequest(ctx, s.cfg.Sync.GroupID, id, now)
	if err != nil {
		return
	}
	jobs, err := s.store.InspectManagementObligations(ctx, r, now)
	if err != nil {
		return
	}
	state := "uncertain"
	outcome := &syncmanagement.Outcome{Status: "uncertain", Reason: "recorded obligations require durable effect recovery"}
	if r.State != "running" {
		state = r.State
		outcome = r.Outcome
	}
	settled := len(jobs) > 0
	for _, j := range jobs {
		settled = settled && j.Settled
	}
	if settled {
		state = "finished"
		outcome = &syncmanagement.Outcome{Status: "already_settled", Reason: "periodic recovery durably settled recorded obligations"}
		for _, j := range jobs {
			if len(jobs) == 1 {
				outcome.TargetCommit = j.TargetCommit
			}
		}
		if r.Action == "verify-all" {
			if len(jobs) != 1 || jobs[0].Kind != "verification" {
				return
			}
			v := jobs[0].Verification
			if v == nil {
				if jobs[0].State != "expired" {
					return
				}
				outcome = &syncmanagement.Outcome{Status: "expired", Reason: "interrupted verification expired without a finished observation"}
			} else {
				outcome = &syncmanagement.Outcome{Status: v.Result, TargetCommit: &v.TargetCommit, VerificationID: &v.VerificationID, Reason: "exact interrupted verification result recovered"}
			}
		}
	} else if len(jobs) == 0 && (r.State == "running" || r.State == "uncertain") {
		// Every protected content effect and verification observation first
		// commits an association. Empty evidence under an expired exact claim
		// proves no such effect was admitted, independently of claim age.
		e := &managementExecution{request: r, service: s}
		// Inspect current authority without reusing the expired effect claim.
		e.request.State = "queued"
		if err := e.authorized(ctx); err != nil || r.Attempts >= syncmanagement.AttemptLimit {
			state = "blocked"
			outcome = &syncmanagement.Outcome{Status: "blocked", Reason: "no effect admitted; current authority or attempt budget refuses another execution"}
		} else {
			state = "queued"
			outcome = nil
		}
	}
	if r.State == "running" {
		_, err = s.store.RecoverManagementClaim(ctx, r, state, outcome, "management-recorded-obligation-inspection", now)
	} else if state == "finished" || state != r.State {
		_, err = s.store.TransitionManagementRequest(ctx, r, state, outcome, "management-recorded-obligation-inspection", now)
	}
	if err != nil {
		s.warn("management recovery result remains unresolved")
	}
}

func (s *peerService) pruneManagement(ctx context.Context) {
	policy, err := maintenance.EffectivePolicy(s.cfg.Retention)
	if err != nil {
		return
	}
	if _, err := s.store.PruneManagementRequests(ctx, time.Now(), policy.CompletedReceipts); err != nil {
		s.warn("management retention remains pending")
	}
}

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

type managementExecutionKey struct{}

// The provenance stays inside local orchestration. Peer status requests contain
// no caller-controlled management identity or pending-work exemption.
type managementExecution struct {
	mu              sync.Mutex
	request         sqlite.ManagementRequest
	service         *peerService
	admissionHold   string
	admissionTarget string
	retryJobs       map[string]sqlite.ManagementObligation
}

func managementBindingForConfig(cfg *config.Config) (syncmanagement.Binding, error) {
	policy, revision, err := config.ManagementRevisions(cfg)
	if err != nil {
		return syncmanagement.Binding{}, err
	}
	return syncmanagement.Binding{GroupID: cfg.Sync.GroupID, InstanceID: cfg.Instance.ID, StateIncarnationID: localSyncIncarnation(cfg), PolicyRevision: policy, ConfigRevision: revision}, nil
}

func (e *managementExecution) authorized(ctx context.Context) error {
	cfg, err := config.LoadContext(ctx, e.service.configPath)
	if err != nil || cfg.Sync == nil || !cfg.Sync.Enabled || cfg.Sync.Management == nil || !cfg.Sync.Management.Enabled || !slices.Contains(cfg.Sync.Management.Actions, e.request.Action) {
		return sqlite.ErrManagementUnauthorized
	}
	b, err := managementBindingForConfig(cfg)
	if err != nil {
		return err
	}
	r := e.request
	if r.State == "running" {
		current, err := e.service.store.LoadManagementRequest(ctx, r.GroupID, r.RequestID, time.Now())
		if err != nil {
			return err
		}
		if current.State != "running" || current.Revision != r.Revision || current.Fence != r.Fence || current.ClaimOwner != r.ClaimOwner {
			return sqlite.ErrSyncPrecondition
		}
		now := time.Now()
		lease, leaseErr := time.Parse(time.RFC3339Nano, r.ClaimExpiresAt)
		deadline, deadlineErr := time.Parse(time.RFC3339Nano, r.ExecutionDeadline)
		if leaseErr != nil || deadlineErr != nil || !now.Before(lease) || !now.Before(deadline) {
			return sqlite.ErrSyncPrecondition
		}
	}
	if b.GroupID != r.GroupID || b.InstanceID != r.InstanceID || b.StateIncarnationID != r.StateIncarnationID || b.PolicyRevision != r.PolicyRevision || b.ConfigRevision != r.ConfigRevision {
		return sqlite.ErrManagementUnauthorized
	}
	if err := verifyPeerSignerIsolation(cfg.Sync); err != nil {
		return err
	}
	control, err := e.service.store.LoadSyncControl(ctx, r.GroupID)
	revision, _ := config.SyncRevision(cfg)
	if err != nil {
		return err
	}
	if control.State != "active" || control.MembershipMode != "normal" || control.ConfigRevision != revision {
		return sqlite.ErrSyncControlHeld
	}
	return nil
}

func managementEffectBoundary(ctx context.Context) error {
	e, _ := ctx.Value(managementExecutionKey{}).(*managementExecution)
	if e == nil {
		return ctx.Err()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.authorized(ctx)
}

func associateManagementExecution(ctx context.Context, store *sqlite.Store, job sqlite.SyncJobRow, target, reason string) error {
	e, _ := ctx.Value(managementExecutionKey{}).(*managementExecution)
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.authorized(ctx); err != nil {
		return err
	}
	if e.request.Action == "retry" {
		j, ok := e.retryJobs[job.JobID]
		if !ok || !j.Eligible || j.Kind != job.Kind || j.TargetCommit == nil || *j.TargetCommit != target {
			return sqlite.ErrManagementNotRetryable
		}
		reason = "retry_obligation"
	}
	// Existing immutable associations need no new reservation, including a
	// periodic pass that settled a previously observed request obligation.
	var count int
	if err := store.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_management_jobs WHERE request_id=? AND job_id=?`, e.request.RequestID, job.JobID).Scan(&count); err != nil {
		return err
	}
	if count == 0 && e.request.AssociationReserved == 0 {
		reserved, reserveErr := store.ReserveManagementAssociations(ctx, e.request, 1, time.Now())
		if reserveErr != nil {
			if errors.Is(reserveErr, sqlite.ErrSyncQueueFull) {
				e.admissionHold = "request association capacity exhausted before another effect"
			}
			return reserveErr
		}
		e.request = reserved
	}
	associated, err := store.AssociateManagementJob(ctx, e.request, sqlite.ManagementJob{JobID: job.JobID, Kind: job.Kind, TargetCommit: &target, AssociationReason: reason}, time.Now())
	if err == nil {
		e.request = associated
	}
	return err
}

func admitManagementExecutionJob(ctx context.Context, store *sqlite.Store, in sqlite.SyncJobInput, target, reason string) (sqlite.SyncJobRow, bool, error) {
	e, _ := ctx.Value(managementExecutionKey{}).(*managementExecution)
	if e == nil {
		return store.AdmitSyncJob(ctx, in)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.request.Action == "retry" {
		return sqlite.SyncJobRow{}, false, sqlite.ErrManagementNotRetryable
	}
	if err := e.authorized(ctx); err != nil {
		return sqlite.SyncJobRow{}, false, err
	}
	var linked int
	if err := store.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_management_jobs a JOIN sync_jobs j ON j.job_id=a.job_id WHERE a.request_id=? AND j.group_id=? AND j.kind=? AND j.logical_key=?`, e.request.RequestID, in.GroupID, in.Kind, in.LogicalKey).Scan(&linked); err != nil {
		return sqlite.SyncJobRow{}, false, err
	}
	if linked == 0 && e.request.AssociationReserved == 0 {
		reserved, err := store.ReserveManagementAssociations(ctx, e.request, 1, time.Now())
		if err != nil {
			if errors.Is(err, sqlite.ErrSyncQueueFull) {
				recordManagementImportAdmissionHold(ctx, store, e, in, target, "request association capacity exhausted before import effects")
			}
			return sqlite.SyncJobRow{}, false, err
		}
		e.request = reserved
	}
	job, reused, associated, err := store.AdmitManagementSyncJob(ctx, e.request, in, target, reason)
	if err == nil {
		e.request = associated
	} else if errors.Is(err, sqlite.ErrSyncQueueFull) {
		recordManagementImportAdmissionHold(ctx, store, e, in, target, "sync job capacity exhausted before import effects")
	}
	return job, reused, err
}

// The caller holds e.mu. A failed import admission proves only this attempted
// child had no effect. Earlier linked recovery work must retain uncertainty.
func recordManagementImportAdmissionHold(ctx context.Context, store *sqlite.Store, e *managementExecution, in sqlite.SyncJobInput, target, reason string) {
	if e.request.Action != "sync-now" || in.Kind != "import" {
		return
	}
	var prior int
	if err := store.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_management_jobs WHERE request_id=?`, e.request.RequestID).Scan(&prior); err != nil || prior != 0 {
		return
	}
	e.admissionHold = reason
	e.admissionTarget = target
}

func reserveManagementExecution(ctx context.Context, store *sqlite.Store) error {
	e, _ := ctx.Value(managementExecutionKey{}).(*managementExecution)
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.authorized(ctx); err != nil {
		return err
	}
	reserved, err := store.ReserveManagementAssociations(ctx, e.request, 1, time.Now())
	if errors.Is(err, sqlite.ErrSyncQueueFull) {
		e.admissionHold = "request association capacity exhausted before another effect"
	}
	if err == nil {
		e.request = reserved
	}
	return err
}

// Only a demonstrated hold before verification observations reaches this disposition.
func managementVerificationAdmissionHold(ctx context.Context, err error, target string) {
	e, _ := ctx.Value(managementExecutionKey{}).(*managementExecution)
	if e == nil || !errors.Is(err, sqlite.ErrSyncQueueFull) && !errors.Is(err, sqlite.ErrSyncControlHeld) {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if errors.Is(err, sqlite.ErrSyncControlHeld) {
		e.admissionHold = "local control held before verification observations"
	} else if e.admissionHold == "" {
		e.admissionHold = "sync job capacity exhausted before verification observations"
	}
	e.admissionTarget = target
}

func finishManagementPublication(ctx context.Context, store *sqlite.Store, job sqlite.SyncJobRow, owner string, journal sqlite.SyncJournalEntry, delivery sqlite.SyncJobInput, target string, now time.Time) error {
	e, _ := ctx.Value(managementExecutionKey{}).(*managementExecution)
	if e == nil {
		nowText := now.UTC().Format(time.RFC3339Nano)
		if owner == "" {
			_, err := store.FinishRecoveredPublicationJob(ctx, job.JobID, job.Fence, journal, delivery, nowText)
			return err
		}
		_, err := store.FinishPublicationJob(ctx, job.JobID, owner, job.Fence, journal, delivery, nowText)
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// No new protected effect occurs here. The transaction itself validates the
	// durable claim and consumes its previously reserved evidence capacity.
	associated, err := store.FinishManagementPublicationJob(ctx, e.request, job.JobID, owner, job.Fence, journal, delivery, target, now)
	if err == nil {
		e.request = associated
	}
	return err
}

func managementVerificationRequest(ctx context.Context) string {
	e, _ := ctx.Value(managementExecutionKey{}).(*managementExecution)
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.request.Action != "verify-all" {
		return ""
	}
	return e.request.RequestID
}

// Each existing periodic/peer recovery pass precedes at most one management
// dispatch. Control requests are already settled at acceptance, outside this loop.
func (s *peerService) dispatchManagement(ctx context.Context) {
	s.dispatchManagementWithHTTP(ctx, nil)
}

func (s *peerService) dispatchManagementWithHTTP(ctx context.Context, httpClient *http.Client) {
	if ctx.Err() != nil {
		return
	}
	id, err := s.store.NextQueuedManagementRequest(ctx, s.cfg.Sync.GroupID)
	if err != nil || id == "" {
		return
	}
	pending, err := s.store.LoadManagementRequest(ctx, s.cfg.Sync.GroupID, id, time.Now())
	if err != nil || (pending.Action != "sync-now" && pending.Action != "verify-all" && pending.Action != "retry") {
		return
	}
	binding, err := managementBindingForConfig(s.cfg)
	if err != nil {
		return
	}
	e := &managementExecution{request: pending, service: s}
	if err := e.authorized(ctx); err != nil {
		if ctx.Err() == nil {
			_, transitionErr := s.store.TransitionManagementRequest(ctx, pending, "blocked", &syncmanagement.Outcome{Status: "blocked", Reason: "current policy, binding or control refuses execution"}, "management-authority-inspection", time.Now())
			if transitionErr != nil {
				s.warn("management authority result remains unresolved")
			}
		}
		return
	}
	claimed, err := s.store.ClaimManagementRequest(ctx, pending.GroupID, id, randomSyncID("management-owner"), binding, true, time.Now())
	if err != nil {
		if errors.Is(err, sqlite.ErrSyncQueueFull) || errors.Is(err, sqlite.ErrManagementUnauthorized) || errors.Is(err, sqlite.ErrSyncControlHeld) {
			reason := "worker binding or current control refuses execution"
			if errors.Is(err, sqlite.ErrSyncQueueFull) {
				reason = "request lineage exhausted its execution budget"
			}
			if _, err := s.store.TransitionManagementRequest(ctx, pending, "blocked", &syncmanagement.Outcome{Status: "blocked", Reason: reason}, "management-claim-refusal-inspection", time.Now()); err != nil {
				s.warn("management claim refusal remains unresolved")
			}
		}
		return
	}
	e.request = claimed
	if pending.Action == "retry" {
		jobs, err := s.store.InspectManagementObligations(ctx, claimed, time.Now())
		if err != nil {
			return
		}
		e.retryJobs = make(map[string]sqlite.ManagementObligation, len(jobs))
		for _, j := range jobs {
			e.retryJobs[j.JobID] = j
		}
	}
	executionCtx, cancel := context.WithTimeout(ctx, syncmanagement.ExecutionSeconds*time.Second)
	executionCtx = context.WithValue(executionCtx, managementExecutionKey{}, e)
	defer cancel()
	renewalDone := make(chan struct{})
	go func() {
		defer close(renewalDone)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-executionCtx.Done():
				return
			case <-ticker.C:
				e.mu.Lock()
				err := e.authorized(executionCtx)
				if err == nil {
					var renewed sqlite.ManagementRequest
					renewed, err = s.store.RenewManagementClaim(executionCtx, e.request, time.Now())
					if err == nil {
						e.request = renewed
					}
				}
				e.mu.Unlock()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	out := boundedReconcileOutput{limit: syncmanagement.ResultBytes}
	diagnostics := boundedReconcileOutput{limit: syncmanagement.ResultBytes}
	code := 0
	if pending.Action == "retry" {
		code = s.retryManagement(executionCtx, e, httpClient, &out)
	} else if pending.Action == "verify-all" {
		if httpClient == nil {
			httpClient = peerHTTPClient()
			defer httpClient.CloseIdleConnections()
		}
		code = verifyConfiguredSync(executionCtx, s.cfg, s.store, httpClient, &out, &diagnostics)
	} else {
		code = reconcileConfiguredSync(executionCtx, s.cfg, s.store, &out, &diagnostics)
	}
	wasCancelled := executionCtx.Err() != nil
	cancel()
	<-renewalDone
	// Persist observed results using an independent bounded cleanup context.
	// An expired claim remains running for T5 journal inspection; never steal it.
	cleanup, finish := context.WithTimeout(context.Background(), 5*time.Second)
	defer finish()
	e.mu.Lock()
	defer e.mu.Unlock()
	state := "uncertain"
	outcome := syncmanagement.Outcome{Status: "uncertain", Reason: "execution requires exact journal inspection"}
	if !wasCancelled && !out.overflow && !diagnostics.overflow {
		result, target := parseReconcileResult(out.buffer.Bytes())
		reason := managementReconcileReason(out.buffer.Bytes())
		if pending.Action == "sync-now" && result == "blocked" {
			state = "blocked"
			outcome = syncmanagement.Outcome{Status: "blocked", Reason: "shared reconciliation retained an operator or safety hold"}
			if reason != "" {
				outcome.Reason = reason
			}
			if syncmanagement.ValidCommit(target) {
				outcome.TargetCommit = &target
			}
		}
		// Protected effects are associated before admission. A completed typed
		// refusal plus an empty journal distinguishes a known pre-effect result
		// from interrupted, malformed or already-associated execution.
		if pending.Action != "retry" && state == "uncertain" {
			refusalState, refusalReason := managementSharedRefusal(pending.Action, code, diagnostics.buffer.Bytes())
			if pending.Action == "sync-now" && result == "deferred" && reason != "" && (code == 0 || code == 10) {
				refusalState, refusalReason = "blocked", reason
			}
			if refusalState != "" {
				jobs, inspectErr := s.store.InspectManagementObligations(cleanup, e.request, time.Now())
				if inspectErr == nil && len(jobs) == 0 {
					state = refusalState
					status := "blocked"
					if state == "finished" {
						status = "failed"
					}
					outcome = syncmanagement.Outcome{Status: status, Reason: refusalReason}
					if pending.Action == "sync-now" && reason != "" && syncmanagement.ValidCommit(target) {
						outcome.TargetCommit = &target
					}
				}
			}
		}
	}
	if !wasCancelled && e.admissionHold != "" {
		state = "blocked"
		outcome = syncmanagement.Outcome{Status: "blocked", Reason: e.admissionHold}
		if syncmanagement.ValidCommit(e.admissionTarget) {
			outcome.TargetCommit = &e.admissionTarget
		}
	}
	if code == 0 && !wasCancelled && !out.overflow && !diagnostics.overflow {
		if pending.Action == "retry" {
			jobs, inspectErr := s.store.InspectManagementObligations(cleanup, e.request, time.Now())
			if inspectErr == nil && len(jobs) > 0 {
				state = "finished"
				outcome = syncmanagement.Outcome{Status: "already_settled", Reason: "exact recorded obligations are durably settled"}
				for _, j := range jobs {
					if !j.Settled {
						outcome.Status = "incomplete"
						outcome.Reason = "recorded obligations remain held, exhausted or unresolved"
					}
					if len(jobs) == 1 {
						outcome.TargetCommit = j.TargetCommit
					}
				}
			}
		} else if pending.Action == "verify-all" {
			var result struct {
				Result syncrecords.Verification `json:"result"`
			}
			if json.Unmarshal(out.buffer.Bytes(), &result) == nil && result.Result.Validate() == nil && result.Result.Phase == "finished" {
				state = "finished"
				outcome = syncmanagement.Outcome{Status: result.Result.Result, TargetCommit: &result.Result.TargetCommit, VerificationID: &result.Result.VerificationID, Reason: "exact verification result recorded"}
			}
		} else {
			result, target := parseReconcileResult(out.buffer.Bytes())
			if settled, pinned := settledReconcileResult(result, target); settled && syncmanagement.ValidCommit(pinned) {
				state = "finished"
				outcome = syncmanagement.Outcome{Status: "no_op", TargetCommit: &pinned, Reason: "guarded reconciliation observed the configured target"}
				var total, unresolved int
				err := s.store.QueryRowContext(cleanup, `SELECT COUNT(*),COALESCE(SUM(j.resolved_at IS NULL),0) FROM sync_management_jobs a JOIN sync_jobs j ON j.job_id=a.job_id WHERE a.request_id=?`, e.request.RequestID).Scan(&total, &unresolved)
				if err != nil {
					state = "uncertain"
					outcome = syncmanagement.Outcome{Status: "uncertain", Reason: "associated job outcomes could not be inspected"}
				} else if unresolved != 0 {
					outcome.Status = "incomplete"
					outcome.Reason = "guarded reconciliation reached the target; linked obligations remain unresolved"
				} else if total != 0 {
					outcome.Status = "already_settled"
				}
			} else if result == "deferred" {
				state = "blocked"
				outcome = syncmanagement.Outcome{Status: "blocked", Reason: "guarded reconciliation deferred; inspect exact obligations"}
				if reason := managementReconcileReason(out.buffer.Bytes()); reason != "" {
					outcome.Reason = reason
				}
				if syncmanagement.ValidCommit(target) {
					outcome.TargetCommit = &target
				}
			}
		}
	}
	_, err = s.store.TransitionManagementRequest(cleanup, e.request, state, &outcome, "management-execution-observation", time.Now())
	if err != nil && !errors.Is(err, sqlite.ErrSyncPrecondition) {
		s.warn("management result remains unresolved")
	}
}

// Copy only closed machine codes, never remote diagnostics or local paths.
func managementReconcileReason(raw []byte) string {
	var v struct {
		Command string `json:"command"`
		OK      bool   `json:"ok"`
		Result  struct {
			Schema string `json:"schema_version"`
			Reason string `json:"reason"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &v) != nil || !v.OK || v.Command != "sync reconcile" || v.Result.Schema != "agent-dispatch.sync-reconcile-result/v1" {
		return ""
	}
	switch v.Result.Reason {
	case "resource_busy", "git_unstable", "operator_pause", "observation_unavailable", "acknowledgement_stale", "local_overlap", "untracked_collision", "membership_stale", "history_uncovered", "history_diverged", "trust_failed", "bound_exhausted", "membership_adoption_requires_operator", "checkpoint_adoption_requires_operator", "membership_emergency", "trust_failure", "conflict", "recovery_required", "recorded_obligations_only", "publication_recovery_pending":
		return v.Result.Reason
	}
	return ""
}

func managementSharedRefusal(action string, exit int, raw []byte) (string, string) {
	var v struct {
		Command string                          `json:"command"`
		OK      bool                            `json:"ok"`
		Error   struct{ Code, Category string } `json:"error"`
	}
	command := "sync reconcile"
	if action == "verify-all" {
		command = "sync verify"
	}
	if json.Unmarshal(raw, &v) != nil || v.OK || v.Command != command {
		return "", ""
	}
	switch {
	case exit == 30 && v.Error.Code == "sync_trust_failed" && v.Error.Category == "security":
		return "finished", v.Error.Code
	case exit == 3 && v.Error.Code == "config_invalid" && v.Error.Category == "configuration":
		return "finished", v.Error.Code
	case exit == 10 && v.Error.Code == "sync_retryable" && v.Error.Category == "transient_local":
		return "blocked", v.Error.Code
	case exit == 14 && v.Error.Code == "sync_precondition_failed" && v.Error.Category == "conflict":
		return "blocked", v.Error.Code
	case exit == 20 && v.Error.Code == "sqlite_write_failed" && v.Error.Category == "storage":
		return "blocked", v.Error.Code
	}
	return "", ""
}

// Heartbeats belong to the existing worker lifecycle, including a paused worker.
// A crash leaves stale evidence; only observed worker termination records stopped.
func (s *peerService) managementHeartbeat(ctx context.Context) func() {
	if s.cfg.Sync.Management == nil {
		return func() {}
	}
	binding, err := managementBindingForConfig(s.cfg)
	if err != nil {
		return func() {}
	}
	owner := randomSyncID("worker")
	if err := s.store.StartManagementExecutor(ctx, binding, owner, time.Now()); err != nil {
		s.warn("management executor evidence unavailable")
		return func() {}
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				update, finish := context.WithTimeout(heartbeatCtx, 5*time.Second)
				err := s.store.RecordManagementExecutor(update, binding, owner, "available", time.Now())
				finish()
				if err != nil {
					s.warn("management executor heartbeat unavailable")
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
		stopped, finish := context.WithTimeout(context.Background(), 5*time.Second)
		defer finish()
		if err := s.store.RecordManagementExecutor(stopped, binding, owner, "stopped", time.Now()); err != nil {
			s.warn("management executor stop evidence unavailable")
		}
	}
}

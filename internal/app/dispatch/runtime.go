package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/observability"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Runtime drives the durable submit flow (E3-T2/E3-T3): commit the
// observation-to-intent lineage, acquire one transactional attempt lease,
// invoke the sink, classify the result, and complete the attempt with a
// domain-validated transition. The intent is committed and leased before
// the sink invocation and no store transaction is open while the sink
// runs (DUR-001, DUR-002, ADR-0005).
type Runtime struct {
	Store ports.DispatchStore
	Sink  ports.Sink
	// Now is the wall clock; tests inject a deterministic one.
	Now func() time.Time
	// LeaseTTL bounds how long one attempt owner may hold the submitting
	// state before recovery may take it over.
	LeaseTTL time.Duration
	// Backoff is the persisted submission retry policy (DUR-007); a zero
	// value disables automatic backoff scheduling (used by tests that
	// drive eligibility directly).
	Backoff Backoff
	// JitterUnit supplies the deterministic jitter fraction in [0, 1).
	JitterUnit func() float64
	// Actor labels audit records from this runtime.
	Actor string
	// Log is the optional structured operational log (OPS-001): when
	// set, the submit flow emits the dispatch lifecycle events with
	// causal correlation; a nil log emits nothing.
	Log *observability.Logger
	// TraceID correlates this runtime's log events.
	TraceID string
	// StalenessCheck reports whether a loaded intent was planned under a
	// route revision or target identity that is no longer active
	// (POL-008/SEC-010, E7-T3/H-1). Nil disables the check (tests).
	StalenessCheck func(ctx context.Context, snap ports.IntentSnapshot) (stale bool, detail string, err error)
	// StaleRebuilder replaces one stale intent under the active
	// configuration and returns the replacement dispatch id. Without a
	// rebuilder a stale intent fails closed with ErrStaleRouteRevision.
	StaleRebuilder func(ctx context.Context, dispatchID string) (string, error)
}

// SubmitReport summarizes one completed submit attempt.
type SubmitReport struct {
	DispatchID     string
	AttemptID      string
	From           records.IntentState
	To             records.IntentState
	Reason         state.IntentReason
	Classification ports.SubmitClassification
	// NextAttemptAt is the persisted backoff deadline for retryable
	// outcomes, empty otherwise.
	NextAttemptAt string
}

// RouteCoordinationView is the optional store surface the runtime uses
// for route-slot enforcement and follow-up promotion. The durable store
// implements it; test fakes may not, in which case the slot guard is
// skipped (E7-T2: the production store always provides it).
type RouteCoordinationView interface {
	LoadRouteState(ctx context.Context, routeID string) (state.RouteSnapshot, error)
	ActivateFollowup(ctx context.Context, dispatchID, actor, now string) error
}

// SubmitOnce runs the flow for one durable intent: it refuses terminal or
// leased intents, commits the lease before invoking the sink, classifies
// the adapter result, and records the outcome through the domain state
// machine. A sink error is an ambiguous outcome and completes as unknown,
// never failed (DUR-005), and no automatic target fallback exists
// (DUR-008).
func (r *Runtime) SubmitOnce(ctx context.Context, dispatchID, owner string) (SubmitReport, error) {
	return r.submitOnce(ctx, dispatchID, owner, 0)
}

// submitOnce bounds the stale-rebuild recursion (E7-T3 round-1
// remediation): at most one supersede-and-rebuild per submission, so a
// rebuilder that cannot produce a fresh intent fails closed instead of
// looping.
func (r *Runtime) submitOnce(ctx context.Context, dispatchID, owner string, rebuildDepth int) (SubmitReport, error) {
	var report SubmitReport
	snap, err := r.Store.LoadIntent(ctx, dispatchID)
	if err != nil {
		return report, err
	}
	switch snap.State {
	case records.IntentReady, records.IntentRetryWait:
	case records.IntentSubmitting:
		return report, fmt.Errorf("%w: dispatch %s is submitting under owner %q", ports.ErrLeaseHeld, dispatchID, snap.LeaseOwner)
	default:
		return report, fmt.Errorf("dispatch %s is %s, not submittable", dispatchID, snap.State)
	}
	// POL-008/SEC-010 (E7-T3/H-1): the stored plan is revalidated against
	// the active configuration immediately before any side effect. A
	// stale intent is superseded and rebuilt under the current revision
	// and target; without a rebuilder it fails closed.
	if r.StalenessCheck != nil {
		stale, detail, err := r.StalenessCheck(ctx, snap)
		if err != nil {
			return report, err
		}
		if stale {
			if rebuildDepth >= 1 {
				return report, fmt.Errorf("%w: dispatch %s was planned under %s and its replacement is still stale", ports.ErrStaleRouteRevision, dispatchID, detail)
			}
			if r.StaleRebuilder == nil {
				return report, fmt.Errorf("%w: dispatch %s was planned under %s", ports.ErrStaleRouteRevision, dispatchID, detail)
			}
			replacement, err := r.StaleRebuilder(ctx, dispatchID)
			if err != nil {
				return report, fmt.Errorf("%w: rebuilding stale dispatch %s: %v", ports.ErrStaleRouteRevision, dispatchID, err)
			}
			if replacement == "" || replacement == dispatchID {
				return report, fmt.Errorf("%w: dispatch %s was planned under %s and no replacement was built", ports.ErrStaleRouteRevision, dispatchID, detail)
			}
			r.logEvent(observability.LevelInfo, observability.EventDispatchRetryScheduled, dispatchID, "", snap.RouteID, snap.TargetID, "stale intent superseded and rebuilt under the active configuration", map[string]any{"replacement": replacement, "stale_detail": detail})
			return r.submitOnce(ctx, replacement, owner, rebuildDepth+1)
		}
	}
	// The route's active slot must be empty or held by exactly this
	// dispatch before the lease commits (CON-001: a submission never runs
	// beside another authoritative task; E7-T2). The lease transaction
	// re-checks the same predicate, so this guard fails fast with the
	// actionable error while the store remains the authority.
	if view, ok := r.Store.(RouteCoordinationView); ok {
		rs, err := view.LoadRouteState(ctx, snap.RouteID)
		if err != nil {
			return report, err
		}
		if !slotAdmissible(rs, dispatchID) {
			return report, fmt.Errorf("%w: route %s is %s with active dispatch %q, not submittable for %s", ports.ErrStateNotEligible, snap.RouteID, rs.State, rs.ActiveDispatchID, dispatchID)
		}
		// The acknowledged-revision gate (E8-T3, H-2): a behavior-sensitive
		// configuration change since the operator's enable acknowledgement
		// pauses the route — nothing submits under a revoked plan until
		// `route enable` re-acknowledges the computed revision.
		if rs.ActivationState == "enabled" && rs.AcknowledgedRevision != snap.RouteRevision {
			return report, fmt.Errorf("%w: route %s acknowledged revision %s but dispatch %s was planned under %s; re-acknowledge with route enable",
				ports.ErrStaleRouteRevision, snap.RouteID, rs.AcknowledgedRevision, dispatchID, snap.RouteRevision)
		}
	}
	now := r.Now()
	report.DispatchID = dispatchID
	report.From = snap.State

	// The lease and its audit entry commit in one transaction that closes
	// before the sink call; from here until CompleteAttempt the runtime
	// holds no store transaction (DUR-001).
	attemptID := fmt.Sprintf("%s-%d", dispatchID, now.UnixNano())
	acquired, err := r.Store.AcquireAttempt(ctx, ports.AcquireAttempt{
		DispatchID:     dispatchID,
		AttemptID:      attemptID,
		Owner:          owner,
		Now:            Timestamp(now),
		LeaseExpiresAt: Timestamp(now.Add(r.LeaseTTL)),
	})
	if err != nil {
		return report, err
	}
	report.AttemptID = acquired
	r.logEvent(observability.LevelInfo, observability.EventDispatchAttemptStarted, dispatchID, acquired, snap.RouteID, snap.TargetID, "attempt leased", nil)

	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(snap.RequestJSON), &req); err != nil {
		// A malformed stored request can never be submitted, but the
		// lease is already held: complete the attempt as a definite
		// pre-invocation failure so the intent lands in retry_wait with
		// bounded budget (eventually dead-lettering through the normal
		// machinery) instead of stranding in submitting until the lease
		// expires (E4 audit remediation for the E3-T2 reopen).
		report.Classification = ports.SubmitDefiniteNotSubmitted
		completion := ports.AttemptResult{
			AttemptID:   acquired,
			DispatchID:  dispatchID,
			Outcome:     "transport_failure",
			ErrorCode:   "stored_request_invalid",
			CompletedAt: Timestamp(r.Now()),
			Diagnostic:  "stored request is not the task contract shape",
			Transition: ports.AttemptTransition{
				To:           records.IntentRetryWait,
				Reason:       state.ReasonTransientFailure,
				TransitionID: acquired + ":" + string(state.ReasonTransientFailure),
			},
		}
		if r.Backoff != (Backoff{}) {
			delay, derr := r.Backoff.Delay(snap.AttemptCount+1, 0)
			if derr == nil {
				completion.NextAttemptAt = Timestamp(r.Now().Add(delay))
			}
		}
		if err := r.Store.CompleteAttempt(ctx, completion); err != nil {
			return report, err
		}
		report.To = completion.Transition.To
		report.Reason = completion.Transition.Reason
		return report, fmt.Errorf("stored request is not the task contract shape: %w", err)
	}
	res, sinkErr := r.Sink.Submit(ctx, req)
	classified := ClassifyResult(res, sinkErr)
	if sinkErr != nil {
		// Adapter error text is untrusted for persistence (the sink
		// contract redacts diagnostics at the source and classifies
		// through the result, so a returned error keeps only the
		// bounded class here).
		res.Diagnostic = fmt.Sprintf("sink error (%T); message redacted", sinkErr)
	}
	report.Classification = res.Classification
	completion := ports.AttemptResult{
		AttemptID:   acquired,
		DispatchID:  dispatchID,
		Outcome:     classified.AttemptOutcome,
		ErrorCode:   classified.ErrorCode,
		CompletedAt: Timestamp(r.Now()),
		Diagnostic:  boundedDiagnostic(res.Diagnostic),
		Transition: ports.AttemptTransition{
			To:           classified.To,
			Reason:       classified.Reason,
			TransitionID: acquired + ":" + string(classified.Reason),
		},
	}
	if classified.ReceiptWorthy {
		acceptance := records.AcceptanceAccepted
		if classified.To == records.IntentRejected {
			acceptance = records.AcceptanceRejected
		}
		completion.Receipt = r.receipt(acquired, res, acceptance)
		completion.Transition.Evidence.ReceiptRef = completion.Receipt.ReceiptID
	}
	// A retryable outcome schedules the persisted backoff deadline the
	// lease predicate enforces (DUR-007).
	if classified.To == records.IntentRetryWait && r.Backoff != (Backoff{}) {
		attempts := snap.AttemptCount + 1
		unit := 0.0
		if r.JitterUnit != nil {
			unit = r.JitterUnit()
		}
		delay, derr := r.Backoff.Delay(attempts, unit)
		if derr != nil {
			return report, derr
		}
		deadline := Timestamp(r.Now().Add(delay))
		completion.NextAttemptAt = deadline
		report.NextAttemptAt = deadline
	}
	if err := r.Store.CompleteAttempt(ctx, completion); err != nil {
		return report, err
	}
	report.To = classified.To
	report.Reason = classified.Reason
	r.logSubmitOutcome(dispatchID, acquired, snap.RouteID, snap.TargetID, classified, res)
	if classified.To == records.IntentRejected {
		// A definite rejection dead-letters through the declared edge so
		// the dispatch cannot hold the route slot as a terminal rejected
		// with no operator exit (E8-T2, M-3). The follow-on closure is
		// the existing discard/rerun surface.
		if err := r.Store.DeadLetterRejected(ctx, dispatchID, r.Actor, Timestamp(r.Now())); err != nil {
			r.logEvent(observability.LevelWarn, observability.EventDispatchRejected, dispatchID, acquired, snap.RouteID, snap.TargetID,
				"definite rejection did not dead-letter", map[string]any{"error": boundedDiagnostic(err.Error())})
		}
	}
	if classified.To == records.IntentAccepted {
		// An accepted follow-up becomes the route's active task at
		// acceptance (FOLLOWUP_READY -> ACTIVE_CLEAN, E7-T2/B-3): the
		// work-receipt surface only admits active routes, so without this
		// promotion a second generation could never begin work.
		r.promoteFollowup(ctx, snap.RouteID, dispatchID)
	}
	return report, nil
}

// promoteFollowup activates one just-accepted dispatch when its route is
// the pending follow-up shape. Every other route state is skipped
// silently: the store's own activation guard is the authority, and a
// normal first-generation dispatch already activated its route at arrival.
func (r *Runtime) promoteFollowup(ctx context.Context, routeID, dispatchID string) {
	view, ok := r.Store.(RouteCoordinationView)
	if !ok {
		return
	}
	rs, err := view.LoadRouteState(ctx, routeID)
	if err != nil {
		r.logEvent(observability.LevelWarn, observability.EventDispatchUnknown, dispatchID, "", routeID, "", "follow-up promotion skipped: route state unreadable", map[string]any{"error": err.Error()})
		return
	}
	if rs.State != state.RouteFollowupReady {
		return
	}
	if rs.ActiveDispatchID != "" && rs.ActiveDispatchID != dispatchID {
		r.logEvent(observability.LevelWarn, observability.EventDispatchUnknown, dispatchID, "", routeID, "", "follow-up promotion skipped: slot held by another dispatch", map[string]any{"active": rs.ActiveDispatchID})
		return
	}
	if err := view.ActivateFollowup(ctx, dispatchID, r.Actor, Timestamp(r.Now())); err != nil {
		// The acceptance stands; a promotion failure (including a crash
		// between the two transactions) is healed by the next drain's
		// accepted-follow-up sweep and stays visible in the operational
		// log (promoteAcceptedFollowup, E7-T2 round-1 review).
		r.logEvent(observability.LevelWarn, observability.EventDispatchUnknown, dispatchID, "", routeID, "", "follow-up promotion failed after acceptance; the next drain promotes it", map[string]any{"error": err.Error()})
		return
	}
	r.logEvent(observability.LevelInfo, observability.EventDispatchAccepted, dispatchID, "", routeID, "", "accepted follow-up promoted to the active slot", nil)
}

// DrainReport summarizes one bounded drain run (CLI dispatches drain).
type DrainReport struct {
	Processed int
	Skipped   int
	Reports   []SubmitReport
	// Warnings carries per-dispatch conditions that did not abort the
	// drain (a stale intent the rebuilder could not resolve stays
	// operator-visible instead of blocking the route).
	Warnings []string
}

// DrainLister supplies the drain loop's reads: the intent listing plus
// the route runtime snapshot for slot enforcement (E7-T2).
type DrainLister interface {
	ports.InspectionStore
	RouteCoordinationView
}

// Drain submits up to max due ready/retry_wait intents for one route.
// The attempt budget stops automatic processing (DUR-007); an ambiguity
// stops the drain for operator reconciliation rather than falling over
// (DUR-008). Only the dispatch holding the route's active slot (or a
// route with an empty slot) is submitted, and an uncertain or quarantined
// route submits nothing: reconciliation or the operator resolves first
// (CON-001, E7-T2).
func (r *Runtime) Drain(ctx context.Context, routeID string, max int, lister DrainLister) (DrainReport, error) {
	var out DrainReport
	if max < 1 {
		return out, fmt.Errorf("drain max must be >= 1")
	}
	intents, err := lister.ListIntents(ctx, ports.IntentFilter{RouteID: routeID, Limit: 1000})
	if err != nil {
		return out, err
	}
	now := Timestamp(r.Now())
	for _, sum := range intents {
		if out.Processed >= max {
			break
		}
		switch sum.State {
		case records.IntentReady, records.IntentRetryWait:
		default:
			continue
		}
		rs, err := lister.LoadRouteState(ctx, routeID)
		if err != nil {
			return out, fmt.Errorf("drain stopped at %s: route state unreadable: %w", sum.DispatchID, err)
		}
		if !slotAdmissible(rs, sum.DispatchID) {
			// The slot belongs to another authoritative dispatch (or the
			// route must be reconciled first); this intent waits or was
			// superseded and must never run beside it (CON-001, E7-T2/B-2).
			out.Skipped++
			continue
		}
		if sum.NextAttemptAt != "" && sum.NextAttemptAt > now {
			out.Skipped++
			continue
		}
		if r.Backoff != (Backoff{}) && r.Backoff.Exhausted(sum.AttemptCount) {
			out.Skipped++
			continue
		}
		report, err := r.SubmitOnce(ctx, sum.DispatchID, "drain")
		if err != nil {
			if errors.Is(err, ports.ErrStaleRouteRevision) {
				// A stale intent the rebuilder could not resolve (for
				// example its route or target left the configuration)
				// never blocks the route's other work; it stays visible
				// as a drain warning for the operator.
				out.Skipped++
				out.Warnings = append(out.Warnings, fmt.Sprintf("dispatch %s skipped: %v", sum.DispatchID, err))
				continue
			}
			return out, fmt.Errorf("drain stopped at %s: %w", sum.DispatchID, err)
		}
		out.Processed++
		out.Reports = append(out.Reports, report)
	}
	// Self-healing close of the promotion crash window (round-1 review):
	// a process that died between an accepted follow-up's receipt commit
	// and its route promotion leaves FOLLOWUP_READY with an accepted
	// follow-up that no submit path revisits — the drain promotes it here
	// without manual edits (DUR-010 posture).
	r.promoteAcceptedFollowup(ctx, routeID, lister)
	return out, nil
}

// promoteAcceptedFollowup promotes an accepted follow-up whose route was
// left in FOLLOWUP_READY (a crash or transient failure between the
// acceptance commit and the promotion). Every other shape is skipped:
// the store's activation guard stays the authority.
func (r *Runtime) promoteAcceptedFollowup(ctx context.Context, routeID string, lister DrainLister) {
	rs, err := lister.LoadRouteState(ctx, routeID)
	if err != nil || rs.State != state.RouteFollowupReady {
		return
	}
	intents, err := lister.ListIntents(ctx, ports.IntentFilter{RouteID: routeID, State: records.IntentAccepted, Limit: 10})
	if err != nil {
		return
	}
	for _, sum := range intents {
		if rs.ActiveDispatchID != "" && rs.ActiveDispatchID != sum.DispatchID {
			continue
		}
		if err := lister.ActivateFollowup(ctx, sum.DispatchID, r.Actor, Timestamp(r.Now())); err != nil {
			r.logEvent(observability.LevelWarn, observability.EventDispatchUnknown, sum.DispatchID, "", routeID, "", "drain-time follow-up promotion failed", map[string]any{"error": err.Error()})
			continue
		}
		r.logEvent(observability.LevelInfo, observability.EventDispatchAccepted, sum.DispatchID, "", routeID, "", "accepted follow-up promoted to the active slot by the drain", nil)
		return
	}
}

// slotAdmissible is the one route-slot admission rule shared by every
// submit-path site (E7-T2/B-2): an uncertain or quarantined route
// submits nothing, and a dispatch may only run when the active slot is
// empty or its own.
func slotAdmissible(rs state.RouteSnapshot, dispatchID string) bool {
	if rs.State == state.RouteUncertain || rs.State == state.RouteQuarantined {
		return false
	}
	// The automatic-write gate (TST-008, E7-T6/M-1): a route whose
	// activation state is not enabled never submits automatically.
	if rs.ActivationState != "enabled" {
		return false
	}
	return rs.ActiveDispatchID == "" || rs.ActiveDispatchID == dispatchID
}

// Recover takes over every submitting intent on one route (or the whole
// store when routeID is empty) whose attempt lease expired, defaulting
// it to unknown with audit evidence (persistence §5).
func (r *Runtime) Recover(ctx context.Context, routeID string) ([]ports.RecoveredLease, error) {
	return r.Store.RecoverExpiredSubmitting(ctx, routeID, Timestamp(r.Now()))
}

// receipt builds the durable acceptance evidence for one submit result.
func (r *Runtime) receipt(attemptID string, res ports.SubmitResult, acceptance records.AcceptanceState) *ports.ReceiptInput {
	// The receipt carries the payload version of the contract that was
	// actually submitted (DAT-009, E7-T8/M-8): never null.
	version := res.PayloadVersion
	if version == "" {
		version = ports.TaskRequestContractVersion
	}
	return &ports.ReceiptInput{
		ReceiptID:        "rcpt-" + attemptID,
		Acceptance:       acceptance,
		Durable:          res.Durable == ports.DurableTrue,
		ExternalRef:      res.ExternalRef,
		TargetObservedAt: res.TargetObservedAt,
		ReceivedAt:       Timestamp(r.Now()),
		PayloadVersion:   version,
		BoundedPayload:   boundedPayload(res.StructuredPayload),
	}
}

// Timestamp renders the canonical UTC RFC 3339 second-precision form the
// store's TEXT comparisons rely on.
func Timestamp(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

// boundedDiagnostic keeps the recorded diagnostic bounded.
func boundedDiagnostic(d string) string {
	const max = 2000
	if len(d) > max {
		return d[:max] + "...(truncated)"
	}
	return d
}

// boundedPayload keeps the recorded structured evidence bounded; digests
// and refs are stored as real columns. An oversized payload is replaced
// by an explicit marker document so the persisted evidence is always
// valid JSON, never a mid-document truncation.
func boundedPayload(p []byte) string {
	const max = 4096
	if len(p) > max {
		return fmt.Sprintf(`{"truncated":true,"bytes":%d}`, len(p))
	}
	if len(p) == 0 {
		return "{}"
	}
	return string(p)
}

// logSubmitOutcome emits the classified submit outcome event with its
// causal correlation (OPS-001; levels per observability §4).
func (r *Runtime) logSubmitOutcome(dispatchID, attemptID, routeID, targetID string, classified Classified, res ports.SubmitResult) {
	data := map[string]any{"classification": string(res.Classification), "outcome": classified.AttemptOutcome}
	switch classified.To {
	case records.IntentAccepted:
		r.logEvent(observability.LevelInfo, observability.EventDispatchAccepted, dispatchID, attemptID, routeID, targetID, "dispatch accepted", data)
	case records.IntentRejected:
		r.logEvent(observability.LevelWarn, observability.EventDispatchRejected, dispatchID, attemptID, routeID, targetID, "dispatch rejected by the target", data)
	case records.IntentUnknown:
		// Recoverable uncertainty pending operator resolution: WARN per
		// observability §4, not ERROR.
		r.logEvent(observability.LevelWarn, observability.EventDispatchUnknown, dispatchID, attemptID, routeID, targetID, "delivery outcome unknown", data)
	case records.IntentRetryWait:
		r.logEvent(observability.LevelInfo, observability.EventDispatchRetryScheduled, dispatchID, attemptID, routeID, targetID, "transport failure scheduled for retry", data)
	}
}

// corr builds the causal correlation for one dispatch event.
func (r *Runtime) corr(dispatchID, attemptID, routeID, targetID string) observability.Correlation {
	return observability.Correlation{
		TraceID:    r.TraceID,
		DispatchID: dispatchID,
		AttemptID:  attemptID,
		RouteID:    routeID,
		TargetID:   targetID,
	}
}

// logEvent emits one structured event when the logger is present.
func (r *Runtime) logEvent(level observability.Level, event, dispatchID, attemptID, routeID, targetID, message string, data map[string]any) {
	if r.Log == nil {
		return
	}
	r.Log.Log(level, event, r.corr(dispatchID, attemptID, routeID, targetID), message, data)
}

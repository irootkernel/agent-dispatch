package state

import "fmt"

// RouteState is the route coordination state machine
// (persistence-and-state-machines §6). Values match the
// route_runtime_state CHECK constraint exactly; the lockstep test fails
// when either side drifts.
type RouteState string

const (
	RouteIdle          RouteState = "IDLE"
	RouteActiveClean   RouteState = "ACTIVE_CLEAN"
	RouteActiveDirty   RouteState = "ACTIVE_DIRTY"
	RouteFollowupReady RouteState = "FOLLOWUP_READY"
	RouteUncertain     RouteState = "UNCERTAIN"
	RouteQuarantined   RouteState = "QUARANTINED"
)

// ParseRouteState fails closed on unknown values (DAT-009 posture).
func ParseRouteState(s string) (RouteState, error) {
	switch RouteState(s) {
	case RouteIdle, RouteActiveClean, RouteActiveDirty, RouteFollowupReady, RouteUncertain, RouteQuarantined:
		return RouteState(s), nil
	}
	return "", fmt.Errorf("unknown route state %q", s)
}

// AllRouteStates returns every declared route state in schema order.
func AllRouteStates() []RouteState {
	return []RouteState{
		RouteIdle,
		RouteActiveClean,
		RouteActiveDirty,
		RouteFollowupReady,
		RouteUncertain,
		RouteQuarantined,
	}
}

// IsActive reports whether the route currently holds an active task with
// an unresolved authoritative dispatch.
func (r RouteState) IsActive() bool {
	return r == RouteActiveClean || r == RouteActiveDirty
}

// RouteReason is the typed cause recorded with one route transition.
type RouteReason string

const (
	ReasonDispatchAccepted        RouteReason = "dispatch_accepted"
	ReasonLaterRelevantChange     RouteReason = "later_relevant_change"
	ReasonMoreChangesMerged       RouteReason = "more_changes_merged"
	ReasonWorkCompletedClean      RouteReason = "work_completed_no_dirty_generation"
	ReasonWorkCompletedDirty      RouteReason = "work_completed_dirty_generation"
	ReasonWorkSuppressed          RouteReason = "work_completed_exact_suppression"
	ReasonWorkRetryBudgetRemains  RouteReason = "work_retry_budget_remains"
	ReasonRetryBudgetExhausted    RouteReason = "retry_budget_exhausted"
	ReasonExecutionEvidenceStale  RouteReason = "execution_evidence_stale_or_missing"
	ReasonFollowupAccepted        RouteReason = "followup_accepted"
	ReasonFollowupDropped         RouteReason = "followup_dropped_after_reconciliation"
	ReasonTargetLookupFindsActive RouteReason = "target_lookup_finds_active"
	ReasonReconciliationResolved  RouteReason = "reconciliation_resolved"
	ReasonOperatorActionRequired  RouteReason = "operator_action_required"
	ReasonOperatorResolved        RouteReason = "operator_release_or_discard"
)

// AllRouteReasons returns every declared route reason in stable order.
func AllRouteReasons() []RouteReason {
	return []RouteReason{
		ReasonDispatchAccepted,
		ReasonLaterRelevantChange,
		ReasonMoreChangesMerged,
		ReasonWorkCompletedClean,
		ReasonWorkCompletedDirty,
		ReasonWorkSuppressed,
		ReasonWorkRetryBudgetRemains,
		ReasonRetryBudgetExhausted,
		ReasonExecutionEvidenceStale,
		ReasonFollowupAccepted,
		ReasonFollowupDropped,
		ReasonTargetLookupFindsActive,
		ReasonReconciliationResolved,
		ReasonOperatorActionRequired,
		ReasonOperatorResolved,
	}
}

// routeEdge identifies one directed edge of the route state machine.
type routeEdge struct {
	from, to RouteState
}

// routeTable is the route state machine (persistence-and-state-machines
// §6): exactly the documented edges, each with its documented reasons.
var routeTable = map[routeEdge][]RouteReason{
	{RouteIdle, RouteActiveClean}:          {ReasonDispatchAccepted},
	{RouteActiveClean, RouteActiveDirty}:   {ReasonLaterRelevantChange},
	{RouteActiveDirty, RouteActiveDirty}:   {ReasonMoreChangesMerged},
	{RouteActiveClean, RouteIdle}:          {ReasonWorkCompletedClean},
	{RouteActiveDirty, RouteIdle}:          {ReasonWorkSuppressed},
	{RouteActiveDirty, RouteFollowupReady}: {ReasonWorkCompletedDirty, ReasonWorkRetryBudgetRemains},
	{RouteActiveClean, RouteFollowupReady}: {ReasonWorkCompletedDirty, ReasonWorkRetryBudgetRemains},
	{RouteActiveClean, RouteUncertain}:     {ReasonRetryBudgetExhausted, ReasonExecutionEvidenceStale},
	{RouteActiveDirty, RouteUncertain}:     {ReasonRetryBudgetExhausted, ReasonExecutionEvidenceStale},
	{RouteFollowupReady, RouteActiveClean}: {ReasonFollowupAccepted},
	{RouteFollowupReady, RouteIdle}:        {ReasonFollowupDropped},
	{RouteUncertain, RouteActiveClean}:     {ReasonTargetLookupFindsActive},
	{RouteUncertain, RouteFollowupReady}:   {ReasonReconciliationResolved},
	{RouteUncertain, RouteQuarantined}:     {ReasonOperatorActionRequired},
	{RouteQuarantined, RouteIdle}:          {ReasonOperatorResolved},
}

// RouteSnapshot is the minimal durable route runtime fact set the guards
// read (the route_runtime_state projection).
type RouteSnapshot struct {
	RouteID          string
	ActivationState  string // disabled | enabled | paused
	State            RouteState
	ActiveDispatchID string
	DirtyGeneration  int
	PendingReconcile bool
	// FailureBudget is the route's remaining consecutive-failure budget.
	FailureBudget int
}

// RouteEvidence carries the typed proof a guarded route edge demands. The
// zero value satisfies only unguarded edges; guards fail closed.
type RouteEvidence struct {
	// Actor is the operator or process asserting the transition.
	Actor string
	// ActivatingDispatchID is the dispatch whose acceptance activates the
	// route (normal dispatch or follow-up).
	ActivatingDispatchID string
	// DirtyGenerationAfter is the durable dirty count after this burst
	// merged; dirtying edges must strictly increase it (CON-002).
	DirtyGenerationAfter int
	// ReceiptRef references the completion or projection evidence.
	ReceiptRef string
	// OperatorAction marks an operator-resolved quarantine release.
	OperatorAction bool
	// ReconciledNoWork marks reconciliation proving no work remains.
	ReconciledNoWork bool
}

func routeRejected(from, to RouteState, reason RouteReason, detail string) error {
	return &TransitionError{
		Entity: "route",
		From:   string(from),
		To:     string(to),
		Reason: string(reason),
		Detail: detail,
	}
}

// CanTransitionRoute reports whether the route state machine declares the
// edge, ignoring guards.
func CanTransitionRoute(from, to RouteState) bool {
	_, ok := routeTable[routeEdge{from, to}]
	return ok
}

// RouteReasons returns the documented reasons for one declared edge.
func RouteReasons(from, to RouteState) ([]RouteReason, bool) {
	reasons, ok := routeTable[routeEdge{from, to}]
	return reasons, ok
}

// ValidateRouteTransition applies the route table and every guard: the
// edge must exist, the reason must be documented for it, and the snapshot
// plus typed evidence must satisfy the edge's guards. A cooperative
// failure or cancellation never erases dirty state, activation is refused
// while another dispatch holds the active slot, and dirtying edges must
// durably increment the dirty generation.
func ValidateRouteTransition(snap RouteSnapshot, to RouteState, reason RouteReason, ev RouteEvidence) error {
	from := snap.State
	reasons, ok := routeTable[routeEdge{from, to}]
	if !ok {
		return routeRejected(from, to, reason, "edge is not part of the route state machine")
	}
	documented := false
	for _, r := range reasons {
		if r == reason {
			documented = true
			break
		}
	}
	if !documented {
		return routeRejected(from, to, reason, fmt.Sprintf("reason is not documented for this edge (allowed: %v)", reasons))
	}
	switch edge := (routeEdge{from, to}); edge {
	case (routeEdge{RouteIdle, RouteActiveClean}), (routeEdge{RouteFollowupReady, RouteActiveClean}):
		if snap.ActivationState != "enabled" {
			return routeRejected(from, to, reason, fmt.Sprintf("route activation state is %q, not enabled", snap.ActivationState))
		}
		if ev.ActivatingDispatchID == "" {
			return routeRejected(from, to, reason, "activation requires the dispatch being accepted")
		}
		// A dispatch's own pre-commit slot reservation is not a second
		// active dispatch: activation may consume exactly that reservation.
		if snap.ActiveDispatchID != "" && snap.ActiveDispatchID != ev.ActivatingDispatchID {
			return routeRejected(from, to, reason, fmt.Sprintf("route already holds active dispatch %s (invariant 5)", snap.ActiveDispatchID))
		}
	case (routeEdge{RouteActiveClean, RouteActiveDirty}), (routeEdge{RouteActiveDirty, RouteActiveDirty}):
		if ev.DirtyGenerationAfter <= snap.DirtyGeneration {
			return routeRejected(from, to, reason, fmt.Sprintf("dirty generation must increase (current %d, after %d)", snap.DirtyGeneration, ev.DirtyGenerationAfter))
		}
	case (routeEdge{RouteActiveClean, RouteIdle}):
		if snap.DirtyGeneration != 0 {
			return routeRejected(from, to, reason, fmt.Sprintf("dirty generation %d must not be silently dropped", snap.DirtyGeneration))
		}
		if snap.PendingReconcile {
			return routeRejected(from, to, reason, "pending reconciliation must not be silently dropped")
		}
		if ev.ReceiptRef == "" {
			return routeRejected(from, to, reason, "clean completion requires completion evidence")
		}
	case (routeEdge{RouteActiveDirty, RouteIdle}):
		// Clearing a dirty generation by completion requires the exact
		// suppression decision's receipt evidence (E5-T3, FBK-002); a
		// pending reconciliation still forces a follow-up instead.
		if ev.ReceiptRef == "" {
			return routeRejected(from, to, reason, "exact suppression requires the completion receipt evidence")
		}
		if snap.PendingReconcile {
			return routeRejected(from, to, reason, "pending reconciliation must not be silently dropped")
		}
	case (routeEdge{RouteActiveDirty, RouteFollowupReady}):
		if reason == ReasonWorkCompletedDirty && snap.DirtyGeneration == 0 && !snap.PendingReconcile {
			return routeRejected(from, to, reason, "follow-up after completion requires a dirty generation or pending reconciliation")
		}
		if reason == ReasonWorkRetryBudgetRemains && snap.FailureBudget <= 0 {
			return routeRejected(from, to, reason, "failure follow-up requires remaining failure budget")
		}
	case (routeEdge{RouteActiveClean, RouteFollowupReady}):
		if reason == ReasonWorkRetryBudgetRemains && snap.FailureBudget <= 0 {
			return routeRejected(from, to, reason, "failure follow-up requires remaining failure budget")
		}
		if reason == ReasonWorkCompletedDirty && !snap.PendingReconcile {
			return routeRejected(from, to, reason, "follow-up after completion requires a pending reconciliation")
		}
	case (routeEdge{RouteActiveClean, RouteUncertain}), (routeEdge{RouteActiveDirty, RouteUncertain}):
		if reason == ReasonRetryBudgetExhausted && snap.FailureBudget > 0 {
			return routeRejected(from, to, reason, "budget exhaustion requires an exhausted failure budget")
		}
	case (routeEdge{RouteFollowupReady, RouteIdle}):
		if !ev.ReconciledNoWork {
			return routeRejected(from, to, reason, "dropping a follow-up requires reconciliation proving no work remains")
		}
	case (routeEdge{RouteUncertain, RouteActiveClean}):
		if snap.ActiveDispatchID == "" {
			return routeRejected(from, to, reason, "lookup activation requires the uncertain dispatch still holding the active slot")
		}
		if ev.Actor == "" {
			return routeRejected(from, to, reason, "lookup activation requires an actor")
		}
	case (routeEdge{RouteUncertain, RouteFollowupReady}), (routeEdge{RouteUncertain, RouteQuarantined}):
		if ev.Actor == "" {
			return routeRejected(from, to, reason, "resolving uncertainty requires an actor")
		}
	case (routeEdge{RouteQuarantined, RouteIdle}):
		if !ev.OperatorAction || ev.Actor == "" {
			return routeRejected(from, to, reason, "leaving quarantine requires an explicit operator resolution with an actor")
		}
	}
	return nil
}

// CanActivateNormalDispatch reports whether a route may accept a new
// normal dispatch (CON-001: at most one unresolved authoritative task; a
// route cannot hold two active dispatch IDs). Only an enabled idle route
// with an empty active slot qualifies; active, uncertain, quarantined, and
// follow-up-pending routes must reconcile or merge instead.
func CanActivateNormalDispatch(snap RouteSnapshot) error {
	if snap.ActivationState != "enabled" {
		return &TransitionError{
			Entity: "route",
			From:   string(snap.State),
			Detail: fmt.Sprintf("route activation state is %q, not enabled", snap.ActivationState),
		}
	}
	switch snap.State {
	case RouteIdle:
	case RouteFollowupReady:
		return &TransitionError{
			Entity: "route",
			From:   string(snap.State),
			Detail: "route has a pending follow-up; later changes must merge into the dirty generation",
		}
	case RouteUncertain:
		return &TransitionError{
			Entity: "route",
			From:   string(snap.State),
			Detail: "route is uncertain; reconciliation must resolve the previous dispatch first",
		}
	case RouteQuarantined:
		return &TransitionError{
			Entity: "route",
			From:   string(snap.State),
			Detail: "route is quarantined; an operator must resolve it first",
		}
	default:
		return &TransitionError{
			Entity: "route",
			From:   string(snap.State),
			Detail: "route already has an active task",
		}
	}
	if snap.ActiveDispatchID != "" {
		return &TransitionError{
			Entity: "route",
			From:   string(snap.State),
			Detail: fmt.Sprintf("route already holds active dispatch %s (invariant 5)", snap.ActiveDispatchID),
		}
	}
	return nil
}

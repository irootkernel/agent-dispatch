package ports

import (
	"context"

	"github.com/rootkernel/jjukkumi/internal/domain/state"
)

// Route coordination surface (E3-T4, ADR-0009): one active task per
// route, durable dirty generations, and at most one follow-up.

// RouteCoordinationStore is the durable surface the coordinator drives.
type RouteCoordinationStore interface {
	// LoadRouteState returns the route's runtime snapshot.
	LoadRouteState(ctx context.Context, routeID string) (state.RouteSnapshot, error)
	// CommitMergePending persists one arriving lineage whose decision is
	// merge_pending and durably increments the route's dirty generation
	// in the same transaction (CON-002, FBK-001): no intent is created
	// while another dispatch holds the active slot.
	CommitMergePending(ctx context.Context, lin Lineage, actor, now string) (int, error)
	// CompleteActive applies one work-completion transaction: the
	// E3-T1-validated route transition, active-slot handling, and, when a
	// dirty generation or pending reconciliation remains, exactly one
	// follow-up decision and intent for latest state (CON-003, CON-004).
	CompleteActive(ctx context.Context, req ActiveCompletion) (FollowupCreated, error)
	// ActivateDispatch applies the acceptance transition of one normal
	// dispatch: IDLE -> ACTIVE_CLEAN consuming the dispatch's own slot
	// reservation.
	ActivateDispatch(ctx context.Context, dispatchID, actor, now string) error
	// ActivateFollowup moves a route from FOLLOWUP_READY to ACTIVE_CLEAN
	// with the follow-up dispatch taking the active slot (E3-T1
	// activation guard).
	ActivateFollowup(ctx context.Context, dispatchID, actor, now string) error
}

// ActiveCompletion is one terminal outcome of the active dispatch.
type ActiveCompletion struct {
	RouteID    string
	DispatchID string
	// Failed reports a cooperative failure or verified terminal failure
	// (the failure budget decides follow-up versus uncertain).
	Failed bool
	// FailureBudgetRemaining is the route's remaining budget at
	// completion time.
	FailureBudgetRemaining int
	// ReceiptRef references the completion evidence (work receipt or
	// terminal projection).
	ReceiptRef string
	Actor      string
	// FollowupRequest carries the fully built latest-state intent when
	// the caller prepared one; the store creates it only when the route
	// actually needs a follow-up.
	FollowupRequest *IntentInput
	// DirtyLineageJSON records the dirty generation lineage reference.
	DirtyLineageJSON string
	// DirtySuppressed reports that every change of the dirty generation
	// was verified self-generated through an exact receipt match, so the
	// completion may clear the route instead of scheduling a follow-up
	// (E5-T3; a pending reconciliation still forces one).
	DirtySuppressed bool
	Now             string
}

// FollowupCreated reports the completion outcome.
type FollowupCreated struct {
	// RouteTo is the resulting route state.
	RouteTo state.RouteState
	// FollowupDispatchID is set exactly when a follow-up was created.
	FollowupDispatchID string
	// DirtyGeneration is the dirty count before the collapse (evidence).
	DirtyGeneration int
}

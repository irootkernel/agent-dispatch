package ports

import (
	"context"
	"errors"

	"github.com/irootkernel/agent-dispatch/internal/domain/state"
)

// Route coordination surface (E3-T4, ADR-0009): one active task per
// route, durable dirty generations, and at most one follow-up.

// LegacyDestinationLaneID is the synthetic coordination lane pre-cutover
// work belongs to (ADR-0016, E12-T2): an intent with no child_dispatches
// row predates the destinations[] contract and coordinates on this lane
// so historical paths keep working. New work never selects it. This is
// the one declaration; the sqlite adapter's LegacyLaneID is its SQL
// binding and the app layer references this constant directly.
const LegacyDestinationLaneID = "__legacy__"

// RouteCoordinationStore is the durable surface the coordinator drives.
type RouteCoordinationStore interface {
	// LoadRouteState returns the route's aggregated coordination snapshot
	// (E12-T2: the route envelope plus the lanes' aggregation, with a
	// route-level QUARANTINED/UNCERTAIN hold overriding every lane).
	LoadRouteState(ctx context.Context, routeID string) (state.RouteSnapshot, error)
	// LoadLaneState returns one destination lane's coordination snapshot
	// merged with the route envelope (E12-T2, CON-007): a missing lane
	// reads as IDLE with an empty slot; a route-level hold overrides the
	// lane's own state.
	LoadLaneState(ctx context.Context, routeID, destinationID string) (state.RouteSnapshot, error)
	// LoadIntentLane returns the coordination snapshot of the lane one
	// dispatch belongs to (E12-T2): the child row's destination, else the
	// synthetic legacy lane of pre-cutover work.
	LoadIntentLane(ctx context.Context, dispatchID string) (state.RouteSnapshot, error)
	// CommitMergePending persists one arriving lineage whose decision is
	// merge_pending and durably increments the dirty generation of exactly
	// the mergeDestinations lanes (CON-002, CON-008, FBK-001): no intent is
	// created while another dispatch holds a merging lane's slot. The
	// batch's selection evidence becomes the occurrence's FULL
	// selectedDestinations — UNIONed with any selection the lineage's batch
	// already carried, canonically sorted, idempotent (E12 epic
	// whole-review round 2: evidence is written once and never narrowed, so
	// a conditioned multi-lane burst keeps every selected lane's follow-up
	// whole). Empty lists merge the synthetic legacy lane and record no
	// evidence.
	CommitMergePending(ctx context.Context, lin Lineage, mergeDestinations, selectedDestinations []string, actor, now string) (int, error)
	// MergeSelectedLanes durably increments the dirty generation of exactly
	// the mergeDestinations lanes under the merge guards (CON-008, E12-T2)
	// without re-persisting the occurrence's lineage: a fan-out sibling
	// that lost its lane's slot race merges beside the winner's
	// already-committed prefix. The occurrence's FULL selectedDestinations
	// union onto the named batch's selection evidence the same way (an
	// empty batchID skips the evidence write — the merge target owns no
	// batch).
	MergeSelectedLanes(ctx context.Context, routeID, batchID string, mergeDestinations, selectedDestinations []string, actor, now string) (int, error)
	// CommitFanoutChild persists one additional child intent of an
	// occurrence whose shared observation/batch/decision lineage a sibling
	// already committed (FAN-003): the intent, its child-dispatch record
	// and aggregate references, and its lane-slot reservation commit in
	// one transaction. ErrRouteSlotHeld reports the lane's held slot.
	CommitFanoutChild(ctx context.Context, intent IntentInput) error
	// CompleteActive applies one work-completion transaction: the
	// E3-T1-validated lane transition of the completed dispatch's lane,
	// lane-slot handling, and, when a dirty generation or pending
	// reconciliation remains, exactly one follow-up decision and intent for
	// latest state (CON-003, CON-004).
	CompleteActive(ctx context.Context, req ActiveCompletion) (FollowupCreated, error)
	// ActivateDispatch applies the acceptance transition of one normal
	// dispatch: the dispatch's lane IDLE -> ACTIVE_CLEAN consuming the
	// dispatch's own slot reservation.
	ActivateDispatch(ctx context.Context, dispatchID, actor, now string) error
	// ActivateFollowup moves a lane from FOLLOWUP_READY to ACTIVE_CLEAN
	// (no dirty generation) or ACTIVE_DIRTY (a dirty generation remained
	// when the follow-up was submitted, E8-T1) with the follow-up dispatch
	// taking the lane's active slot (E3-T1 activation guard).
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
	// PolicyRevision is the independent policy digest of the live route
	// (config.PolicyRevision): the follow-up decision records it instead
	// of echoing the route revision, so an audit can attribute the
	// decision to policy content (E9-T3, L-18). Callers without a live
	// route leave it empty and the store falls back to the route
	// revision.
	PolicyRevision string
	// FollowupGeneration is the generation the follow-up prepared by this
	// completion would carry (the completing dispatch's generation plus
	// one); a generation beyond state.MaxConsecutiveFollowups moves the
	// route to UNCERTAIN instead of scheduling another generation
	// (FBK-008 bound, E8-T1).
	FollowupGeneration int64
	// DirtyLineageJSON records the dirty generation lineage reference.
	DirtyLineageJSON string
	// DirtySuppressed reports that every change of the dirty generation
	// was verified self-generated through an exact receipt match, so the
	// completion may clear the route instead of scheduling a follow-up
	// (E5-T3; a pending reconciliation still forces one).
	DirtySuppressed bool
	// FenceGeneration opts this completion into the dirty-generation
	// fence; ExpectedDirtyGeneration is the generation the attribution
	// decision was derived from (including zero): when the
	// in-transaction generation differs, the completion refuses instead
	// of clearing a generation the receipt never matched (E5 audit).
	FenceGeneration         bool
	ExpectedDirtyGeneration int
	// RemainingWork marks a partially_completed outcome (E12-T3,
	// FBK-010): when the lane's dirty generation is zero it is advanced
	// through the documented dirtying edge, so the follow-up edge carries
	// exactly one same-lane follow-up for the remaining scope (the
	// follow-up itself is the caller's FollowupRequest).
	RemainingWork bool
	Now           string
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

// StoreError wraps one durable-store failure so CLI boundaries
// classify store surfaces as storage instead of relabeling service
// defects (the typed boundary both app services share).
type StoreError struct{ Err error }

func (e *StoreError) Error() string { return "durable store: " + e.Err.Error() }
func (e *StoreError) Unwrap() error { return e.Err }

// ErrGenerationConflict reports a conditional durable update that no
// longer matched the state it was prepared against (the shared
// optimistic-concurrency sentinel; the sqlite adapter's
// ErrOptimisticConcurrency aliases it).
var ErrGenerationConflict = errors.New("conditional update matched no row")

// WrapStore wraps one store failure (nil passes through).
func WrapStore(err error) error {
	if err == nil {
		return nil
	}
	return &StoreError{Err: err}
}

package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/domain/state"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// Coordinator owns route-level work coordination (E3-T4, ADR-0009): at
// most one unresolved authoritative dispatch per route (CON-001, local
// serialization regardless of any target mutex capability, CON-006),
// every later relevant burst durably merged into the dirty generation
// (CON-002, FBK-001), and at most one latest-state follow-up after
// completion or reconciliation (CON-003, CON-004).
type Coordinator struct {
	Store CoordinatorStore
	// Now renders the canonical audit timestamp.
	Now func() string
	// Actor labels audit records.
	Actor string
}

// CoordinatorStore is the durable surface coordination needs.
type CoordinatorStore interface {
	ports.RouteCoordinationStore
	ports.DispatchStore
}

// Arrival routes one persisted incoming lineage: an idle enabled route
// activates a normal dispatch; any route that already holds unresolved
// work merges the burst into the durable dirty generation instead
// (never a second active dispatch).
func (c *Coordinator) Arrival(ctx context.Context, lin ports.Lineage) (merged bool, err error) {
	snap, err := c.Store.LoadRouteState(ctx, lin.Decision.RouteID)
	if err != nil {
		return false, err
	}
	if err := state.CanActivateNormalDispatch(snap); err == nil {
		err := c.Store.CommitLineage(ctx, lin)
		if err == nil {
			// The slot reservation commits with the intent (persistence
			// §7); for coordination the reserved dispatch activates the
			// route. The acceptance refinement arrives with the E4/E5
			// receipt projections.
			if err := c.Store.ActivateDispatch(ctx, lin.Intent.DispatchID, c.Actor, c.Now()); err != nil {
				return false, err
			}
			return false, nil
		}
		if !errors.Is(err, ports.ErrRouteSlotHeld) {
			return false, err
		}
		// A concurrent arrival won the slot: this burst merges (AC-204
		// posture — the loser observes existing ownership).
	}
	dirty, err := c.Store.CommitMergePending(ctx, lin, c.Actor, c.Now())
	if err != nil {
		return false, err
	}
	_ = dirty
	return true, nil
}

// Completion applies one terminal outcome of the active dispatch and,
// when dirty work or pending reconciliation remains, creates exactly one
// latest-state follow-up whose generation and manifest derive from the
// caller's projection of the current state.
func (c *Coordinator) Completion(ctx context.Context, req ports.ActiveCompletion) (ports.FollowupCreated, error) {
	if req.FollowupRequest == nil {
		snap, err := c.Store.LoadRouteState(ctx, req.RouteID)
		if err != nil {
			return ports.FollowupCreated{}, ports.WrapStore(err)
		}
		if snap.DirtyGeneration > 0 || snap.PendingReconcile {
			return ports.FollowupCreated{}, fmt.Errorf("%w: completion of %s needs a follow-up request: dirty generation %d, pending reconcile %v",
				ports.ErrStateNotEligible, req.RouteID, snap.DirtyGeneration, snap.PendingReconcile)
		}
	}
	req.Actor = c.Actor
	if req.Now == "" {
		req.Now = c.Now()
	}
	return c.Store.CompleteActive(ctx, req)
}

// Activate promotes one accepted follow-up to the route's active task.
func (c *Coordinator) Activate(ctx context.Context, dispatchID string) error {
	return c.Store.ActivateFollowup(ctx, dispatchID, c.Actor, c.Now())
}

// BuildFollowupRequest constructs the latest-state follow-up intent
// input for one completed dispatch: a new dispatch ID, the next
// generation, and the same immutable request shape with the
// latest-state instruction retained (CON-004, CON-005 — evidence, not
// snapshots).
func BuildFollowupRequest(original ports.IntentSnapshot, manifest []records.ChangeItem, flags []string) (ports.IntentInput, error) {
	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(original.RequestJSON), &req); err != nil {
		return ports.IntentInput{}, fmt.Errorf("stored request is not the task contract shape: %w", err)
	}
	id := fmt.Sprintf("%s-followup-%d", original.DispatchID, original.Generation+1)
	next, key, err := BuildRequest(RequestInput{
		DispatchID:         id,
		Route:              req.Route,
		Resource:           req.Resource,
		TargetID:           original.TargetID,
		Generation:         original.Generation + 1,
		Fingerprint:        records.Digest(req.Activation.ContentFingerprint),
		Changes:            manifest,
		Flags:              flags,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Assignment:         req.Assignment,
		ExecutionHints:     req.ExecutionHints,
	})
	if err != nil {
		return ports.IntentInput{}, err
	}
	requestJSON, err := MarshalRequest(next)
	if err != nil {
		return ports.IntentInput{}, err
	}
	return ports.IntentInput{
		DispatchID: id, RouteID: original.RouteID, RouteRevision: req.Route.Revision,
		TargetID: original.TargetID, TargetType: original.TargetType, TargetScope: original.TargetScope, ResourceID: req.Resource.ID,
		Generation: original.Generation + 1, IdempotencyKey: key,
		ContentFingerprint: next.Activation.ContentFingerprint,
		ManifestDigest:     ManifestDigest(manifest),
		RequestVersion:     RequestContractVersion, RequestJSON: requestJSON,
	}, nil
}

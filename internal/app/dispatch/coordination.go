package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/fingerprint"
	"github.com/irootkernel/agent-dispatch/internal/domain/ids"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
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

// SingleLaneFanout builds the one-lane fanout block every derived child
// carries (E12-T1, E12 epic validation): the aggregate identity, origin,
// the child's destination lane, its single-lane selection summary with
// the closed reason, and the durable destination-revision records the
// selection references. ONE constructor for the five creation sites —
// arrival and reconcile in the CLI, follow-up, rerun, and rebuild in the
// app services — so the assembly can never drift between them.
func SingleLaneFanout(aggregateID string, origin records.AggregateOrigin, lane ports.TaskDestinationRef, reason string, revisions []ports.DestinationRevisionInput) *ports.FanoutInput {
	return &ports.FanoutInput{
		AggregateID: aggregateID, Origin: string(origin),
		DestinationID: lane.ID, DestinationRevision: lane.Revision, Workstream: lane.Workstream,
		Selections: []records.DestinationSelection{{
			DestinationID: lane.ID, DestinationRevision: lane.Revision,
			Workstream: lane.Workstream, Reason: reason,
		}},
		Revisions: revisions,
	}
}

// LegacyDestinationLaneID is the ports-side declaration of the synthetic
// legacy lane (ADR-0016): pre-cutover work without a child row coordinates
// on this lane. The app layer references the shared constant so the value
// has exactly one live encoding.
const LegacyDestinationLaneID = ports.LegacyDestinationLaneID

// laneOfLineage resolves the destination lane one arriving lineage's
// intent belongs to (E12-T2): the fanout destination, else the synthetic
// legacy lane of pre-cutover work.
func laneOfLineage(lin ports.Lineage) string {
	if lin.Intent.Fanout != nil && lin.Intent.Fanout.DestinationID != "" {
		return lin.Intent.Fanout.DestinationID
	}
	return LegacyDestinationLaneID
}

// Arrival routes one persisted incoming lineage: a lane that may accept
// work activates a normal dispatch; any lane that already holds unresolved
// work merges the burst into that lane's durable dirty generation instead
// (never a second active dispatch on the lane, CON-001/CON-007).
func (c *Coordinator) Arrival(ctx context.Context, lin ports.Lineage) (merged bool, err error) {
	result, err := c.arrivalOne(ctx, lin)
	return result.DispatchID == "", err
}

// FanoutOutcome reports the per-destination outcome of one fan-out arrival
// (E12-T2, FAN-002/FAN-003): which lanes activated their child, which
// merged into their lane's dirty generation, and which failed — one
// sibling's failure never blocks the others (CON-007), so failures ride
// along beside the successes instead of aborting the occurrence.
type FanoutOutcome struct {
	// Activated carries the per-destination dispatch IDs that activated
	// their lane (one child per selected destination).
	Activated []FanoutLaneResult
	// Merged carries the per-destination merges into a held lane slot.
	Merged []FanoutLaneResult
	// Failed carries the per-destination failures with bounded, redacted
	// error text; a failed activation keeps its durable dispatch ID so an
	// operator can resolve the reserved slot (activate, drain, discard).
	Failed []FanoutLaneFailure
}

// FanoutLaneResult is one destination's arrival outcome.
type FanoutLaneResult struct {
	DestinationID   string
	DispatchID      string
	DirtyGeneration int
}

// FanoutLaneFailure is one destination's failed arrival outcome.
type FanoutLaneFailure struct {
	DestinationID string
	// DispatchID is set when the child is durable (a failed activation
	// leaves a reserved slot behind) and empty when the commit itself
	// refused.
	DispatchID string
	// Error is the bounded, redacted failure text (BoundLaneError).
	Error string
	// Err is the LIVE failure (round 3): the bounded text above is
	// presentation; this member preserves the typed error so callers can
	// classify non-lane-isolated classes (an invalid fanout record is a
	// configuration failure for EVERY lane, never a per-lane warning).
	Err error
}

// operatorTextBound caps one lane failure's or resolver diagnostic's
// reported operator text (OPS-009 posture: the envelope stays bounded
// even when the underlying error is not; ONE bound for the package's
// operator-facing surfaces, E12 epic whole-review round 3).
const operatorTextBound = 300

// boundTruncate is the ONE bounded-text truncator of this package (E12
// epic whole-review round 1): the cut lands ON A RUNE BOUNDARY with an
// explicit marker — a byte cut could split a multi-byte character and
// emit invalid UTF-8 into a JSON envelope or stored diagnostic.
func boundTruncate(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "...(truncated)"
}

// BoundLaneError renders one lane failure's bounded, redacted text:
// control characters collapse to spaces (envelope-safe) and an oversized
// tail is cut through the shared rune-bound truncator.
func BoundLaneError(err string) string {
	var b strings.Builder
	for _, r := range err {
		if r == '\n' || r == '\r' || r == '\t' || r < 0x20 || r == 0x7f {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	return boundTruncate(b.String(), operatorTextBound)
}

// arrivalOne applies the single-lane arrival decision: the lane's snapshot
// gates activation (CON-001 per lane, CON-007); a held slot merges into
// exactly this lane's dirty generation (CON-008). The occurrence's
// selection is this one lane — the store still unions it with any
// selection the batch already carried.
func (c *Coordinator) arrivalOne(ctx context.Context, lin ports.Lineage) (FanoutLaneResult, error) {
	lane := laneOfLineage(lin)
	snap, err := c.Store.LoadLaneState(ctx, lin.Decision.RouteID, lane)
	if err != nil {
		return FanoutLaneResult{DestinationID: lane}, err
	}
	if err := state.CanActivateNormalDispatch(snap); err == nil {
		err := c.Store.CommitLineage(ctx, lin)
		if err == nil {
			// The lane slot reservation commits with the intent
			// (persistence §7); for coordination the reserved dispatch
			// activates the lane. The acceptance refinement arrives with
			// the E4/E5 receipt projections.
			if err := c.Store.ActivateDispatch(ctx, lin.Intent.DispatchID, c.Actor, c.Now()); err != nil {
				return FanoutLaneResult{DestinationID: lane}, err
			}
			return FanoutLaneResult{DestinationID: lane, DispatchID: lin.Intent.DispatchID}, nil
		}
		if !errors.Is(err, ports.ErrRouteSlotHeld) {
			return FanoutLaneResult{DestinationID: lane}, err
		}
		// A concurrent arrival won the lane's slot: this burst merges (AC-204
		// posture — the loser observes existing ownership).
	}
	dirty, err := c.Store.CommitMergePending(ctx, lin, []string{lane}, []string{lane}, c.Actor, c.Now())
	if err != nil {
		return FanoutLaneResult{DestinationID: lane}, err
	}
	return FanoutLaneResult{DestinationID: lane, DirtyGeneration: dirty}, nil
}

// ArrivalFanout routes one occurrence's per-destination lineages under
// their shared aggregate (E12-T2, FAN-002/FAN-003): each selected
// destination's lineage activates or merges on ITS lane independently —
// one sibling's failure never blocks the others (CON-007) — and the call
// fails only when every lane failed. The shared observation/batch/decision
// prefix persists exactly once: the first lane to commit carries it, and
// the remaining children commit beside it.
func (c *Coordinator) ArrivalFanout(ctx context.Context, lins []ports.Lineage) (FanoutOutcome, error) {
	var out FanoutOutcome
	if len(lins) == 0 {
		return out, fmt.Errorf("%w: a fan-out arrival needs at least one destination lineage", ports.ErrStateNotEligible)
	}
	// The occurrence's FULL selection (E12 epic whole-review round 2): a
	// lane's merge records the WHOLE occurrence's selected lanes as batch
	// evidence — never just the merging lane — so every selected lane's
	// follow-up keeps the whole burst (FAN-005/CON-008; the merging lane
	// set and the selection evidence are different facts and are passed
	// separately to the store).
	selection := make([]string, 0, len(lins))
	for _, lin := range lins {
		selection = append(selection, laneOfLineage(lin))
	}
	var lastErr error
	persisted := false
	// recordFailure keeps one lane's failure visible beside its siblings'
	// successes (CON-007): the bounded text rides in the outcome, the live
	// typed error rides beside it for classification (round 3), and the
	// error stays live for the all-failed return.
	recordFailure := func(lane, dispatchID string, err error) {
		lastErr = err
		out.Failed = append(out.Failed, FanoutLaneFailure{
			DestinationID: lane, DispatchID: dispatchID,
			Error: BoundLaneError(err.Error()), Err: err,
		})
	}
	for _, lin := range lins {
		lane := laneOfLineage(lin)
		snap, err := c.Store.LoadLaneState(ctx, lin.Decision.RouteID, lane)
		if err != nil {
			recordFailure(lane, "", err)
			continue
		}
		result := FanoutLaneResult{DestinationID: lane}
		if state.CanActivateNormalDispatch(snap) == nil {
			var commitErr error
			if !persisted {
				commitErr = c.Store.CommitLineage(ctx, lin)
			} else {
				commitErr = c.Store.CommitFanoutChild(ctx, lin.Intent)
			}
			if commitErr == nil {
				persisted = true
				// The lane slot reservation commits with the intent
				// (persistence §7); for coordination the reserved dispatch
				// activates the lane. A failed activation leaves the child
				// durable with a reserved slot: record it as a failed
				// activation so the operator sees the dispatch to resolve.
				if err := c.Store.ActivateDispatch(ctx, lin.Intent.DispatchID, c.Actor, c.Now()); err != nil {
					recordFailure(lane, lin.Intent.DispatchID, err)
					continue
				}
				result.DispatchID = lin.Intent.DispatchID
				out.Activated = append(out.Activated, result)
				continue
			}
			if !errors.Is(commitErr, ports.ErrRouteSlotHeld) {
				recordFailure(lane, "", commitErr)
				continue
			}
			// A concurrent arrival won the lane's slot: this burst merges
			// (AC-204 posture — the loser observes existing ownership).
		}
		var dirty int
		var mergeErr error
		if !persisted {
			dirty, mergeErr = c.Store.CommitMergePending(ctx, lin, []string{lane}, selection, c.Actor, c.Now())
		} else {
			dirty, mergeErr = c.Store.MergeSelectedLanes(ctx, lin.Decision.RouteID, lin.Batch.BatchID, []string{lane}, selection, c.Actor, c.Now())
		}
		if mergeErr != nil {
			recordFailure(lane, "", mergeErr)
			continue
		}
		persisted = true
		result.DirtyGeneration = dirty
		out.Merged = append(out.Merged, result)
	}
	if len(out.Activated)+len(out.Merged) == 0 {
		// Every lane failed: the occurrence did not happen; surface the
		// last error (the per-lane detail stays in out.Failed).
		return out, lastErr
	}
	return out, nil
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

// DestinationLaneResolver resolves the live destination lane of one route
// (E12-T1): the lane identity plus the canonical projection bytes its
// revision digests, so a derived child that references the lane can also
// persist the destination-revision record it names (DAT-010: every
// referenced revision has its projection row). ok is false when the route
// or its certified destination cannot be resolved; detail then carries
// the bounded underlying reason (route missing, multi-destination bound,
// projection failure) so the fail-closed error names WHY (E12 epic
// validation).
type DestinationLaneResolver func(routeID string) (lane ports.TaskDestinationRef, projectionJSON string, ok bool, detail string)

// boundedResolverDetail keeps a resolver's diagnostic bounded (E12 epic
// validation): the underlying configuration cause is operator-facing
// text inside a typed error, never unbounded prose. The cut is the
// package's shared rune-bound truncator at the shared operator-text
// bound (E12 epic whole-review round 3).
func boundedResolverDetail(detail string) string {
	return boundTruncate(detail, operatorTextBound)
}

// ResolveDestinationLane resolves the destination lane for derived work
// (E12-T1, DAT-013 precedence): the stored request's destination block
// first, then the parent snapshot's child linkage, then the live
// configured lane — never silently submitting route-scoped legacy
// identity under the destinations contract. The returned projection bytes
// are non-empty exactly when the lane was resolved live, because only
// then can no earlier arrival have persisted the revision record.
func ResolveDestinationLane(routeID string, stored *ports.TaskDestinationRef, snapshotLane ports.TaskDestinationRef, resolver DestinationLaneResolver) (ports.TaskDestinationRef, string, error) {
	if stored != nil {
		return *stored, "", nil
	}
	if snapshotLane.ID != "" && snapshotLane.Revision != "" && snapshotLane.Workstream != "" {
		return snapshotLane, "", nil
	}
	if resolver != nil {
		if lane, projection, ok, detail := resolver(routeID); ok {
			return lane, projection, nil
		} else if detail != "" {
			return ports.TaskDestinationRef{}, "", fmt.Errorf("%w: the work predates the destinations contract and no live destination lane resolves for route %s (%s); regenerate the configuration before rerunning legacy work", ports.ErrStateNotEligible, routeID, boundedResolverDetail(detail))
		}
	}
	return ports.TaskDestinationRef{}, "", fmt.Errorf("%w: the work predates the destinations contract and no live destination lane resolves for route %s; regenerate the configuration before rerunning legacy work", ports.ErrStateNotEligible, routeID)
}

// BuildFollowupRequest constructs the latest-state follow-up intent
// input for one completed dispatch: a fresh UUIDv7 dispatch ID (the
// follow-up chain never grows derived-ID suffixes, E8-T1/H-1.2), the
// next generation, and the same immutable request shape with the
// latest-state instruction retained (CON-004, CON-005 — evidence, not
// snapshots). The content fingerprint is recomputed over the delivered
// manifest so the follow-up key reflects the work it covers, not the
// parent's generation (M-10). Parent lineage stays recorded with the
// decision (generation_lineage_json) and the creation audit row
// (supersedes_dispatch). The follow-up keeps the parent's destination
// lane (E12-T1): its child idempotency key derives from the same
// destination identity at the new generation, and it lands beneath its
// own follow-up aggregate event. A parent whose lane resolves only
// through laneResolver also persists the referenced destination-revision
// record (DAT-010).
func BuildFollowupRequest(original ports.IntentSnapshot, manifest []records.ChangeItem, flags []string, laneResolver DestinationLaneResolver) (ports.IntentInput, error) {
	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(original.RequestJSON), &req); err != nil {
		return ports.IntentInput{}, fmt.Errorf("stored request is not the task contract shape: %w", err)
	}
	snapshotLane := ports.TaskDestinationRef{ID: original.DestinationID, Revision: original.DestinationRevision, Workstream: original.Workstream}
	destination, projection, err := ResolveDestinationLane(original.RouteID, req.Destination, snapshotLane, laneResolver)
	if err != nil {
		return ports.IntentInput{}, fmt.Errorf("follow-up of dispatch %s: %w", original.DispatchID, err)
	}
	gen := ids.NewUUIDv7(time.Now)
	followupID, err := gen.NewID()
	if err != nil {
		return ports.IntentInput{}, fmt.Errorf("follow-up identity: %w", err)
	}
	aggregateID, err := gen.NewID()
	if err != nil {
		return ports.IntentInput{}, fmt.Errorf("follow-up aggregate identity: %w", err)
	}
	id := string(followupID)
	fp, err := fingerprint.Content(records.ContentFingerprintInput{
		Changes:    followupFingerprintChanges(manifest),
		ResourceID: req.Resource.ID,
	})
	if err != nil {
		return ports.IntentInput{}, fmt.Errorf("follow-up fingerprint: %w", err)
	}
	next, key, err := BuildRequest(RequestInput{
		DispatchID:         id,
		Route:              req.Route,
		Resource:           req.Resource,
		TargetID:           original.TargetID,
		TargetScope:        original.TargetScope,
		Destination:        destination,
		Generation:         original.Generation + 1,
		Fingerprint:        fp,
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
	var revisions []ports.DestinationRevisionInput
	if projection != "" {
		revisions = []ports.DestinationRevisionInput{{DestinationID: destination.ID, Revision: destination.Revision, ProjectionJSON: projection}}
	}
	return ports.IntentInput{
		DispatchID: id, RouteID: original.RouteID, RouteRevision: req.Route.Revision,
		TargetID: original.TargetID, TargetType: original.TargetType, TargetScope: original.TargetScope, ResourceID: req.Resource.ID,
		Generation: original.Generation + 1, IdempotencyKey: key,
		ContentFingerprint: next.Activation.ContentFingerprint,
		ManifestDigest:     ManifestDigest(manifest),
		RequestVersion:     RequestContractVersion, RequestJSON: requestJSON,
		Fanout: SingleLaneFanout(string(aggregateID), records.OriginFollowup, destination,
			"followup:"+original.DispatchID, revisions),
	}, nil
}

// followupFingerprintChanges projects the follow-up manifest into the
// content-fingerprint change projection (the same projection ingest
// uses, minus the source flags a follow-up never carries).
func followupFingerprintChanges(manifest []records.ChangeItem) []records.FingerprintChange {
	out := make([]records.FingerprintChange, 0, len(manifest))
	for _, m := range manifest {
		out = append(out, records.FingerprintChange{
			Path: m.Path, Operation: string(m.Operation),
			BeforeDigest: string(m.BeforeDigest), AfterDigest: string(m.AfterDigest),
			ExistsAfter: m.Operation != records.OpDelete,
		})
	}
	return out
}

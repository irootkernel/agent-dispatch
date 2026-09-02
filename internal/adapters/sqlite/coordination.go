package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Route and destination-lane coordination (E3-T4, E12-T2): merge-pending,
// work completion with follow-up collapse, and follow-up activation. Every
// method is one transaction and applies the E3-T1 guards. Since E12-T2 the
// single-active slot, the dirty generation, and the follow-up chain are
// keyed on the dispatch's destination lane (CON-007/CON-008); the route row
// keeps the envelope and the route-level QUARANTINED/UNCERTAIN holds.

// RouteRegistered reports whether the route's trusted registration row
// exists (materialized by the first reconciliation or the disabled
// baseline): durable arrival rows reference it, so a never-registered
// route must refuse before any write instead of failing a foreign key
// mid-transaction (the E17-T2 real-Hermes cold validation finding).
func (s *Store) RouteRegistered(ctx context.Context, routeID string) (bool, error) {
	var one int
	err := s.QueryRowContext(ctx, `SELECT 1 FROM routes WHERE route_id = ?`, routeID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// LoadRouteState returns the route's aggregated coordination snapshot
// (E12-T2): the route envelope plus the route-level UNCERTAIN/QUARANTINED
// hold when set, otherwise the aggregation over the route's lanes — a
// route is as busy as its busiest lane (any ACTIVE_DIRTY, else any
// ACTIVE_CLEAN, else any FOLLOWUP_READY, else IDLE), the reported active
// dispatch is the first lane holder in destination order, and the dirty
// generation is the lanes' maximum.
func (s *Store) LoadRouteState(ctx context.Context, routeID string) (state.RouteSnapshot, error) {
	rec, err := s.LoadRouteRuntimeState(routeID)
	if err != nil {
		return state.RouteSnapshot{}, fmt.Errorf("route %s: %v", routeID, err)
	}
	parsed, err := state.ParseRouteState(rec.RouteState)
	if err != nil {
		return state.RouteSnapshot{}, err
	}
	snap := state.RouteSnapshot{
		RouteID:               rec.RouteID,
		ActivationState:       rec.ActivationState,
		State:                 parsed,
		ActiveDispatchID:      rec.ActiveDispatchID,
		DirtyGeneration:       rec.DirtyGeneration,
		PendingReconcile:      rec.PendingReconcile,
		AcknowledgedRevision:  rec.AcknowledgedRevision,
		CapabilityFingerprint: rec.CapabilityFingerprint,
	}
	if parsed == state.RouteUncertain || parsed == state.RouteQuarantined {
		// The route-level hold is authoritative and carries its own fence
		// values (the uncertainty/resolution flows stay route-keyed).
		return snap, nil
	}
	return s.aggregateLaneState(s.DB, snap)
}

// rowsQueryer is the read surface the lane aggregation needs: one
// multi-row query beside the single-row queryer.
type rowsQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// aggregateLaneState folds the route's lanes into one route snapshot
// inside the caller's read. A lane row in a hold state surfaces as the
// route state (the defensive arm: holds are route-level by construction).
func (s *Store) aggregateLaneState(q rowsQueryer, snap state.RouteSnapshot) (state.RouteSnapshot, error) {
	rows, err := q.Query(`SELECT lane_state, COALESCE(active_dispatch_id, ''), dirty_generation
		FROM destination_lane_state WHERE route_id = ? ORDER BY destination_id`, snap.RouteID)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	snap.State = state.RouteIdle
	snap.ActiveDispatchID = ""
	snap.DirtyGeneration = 0
	for rows.Next() {
		var laneState string
		var active string
		var dirty int
		if err := rows.Scan(&laneState, &active, &dirty); err != nil {
			return snap, err
		}
		parsed, perr := state.ParseRouteState(laneState)
		if perr != nil {
			return snap, perr
		}
		switch parsed {
		case state.RouteQuarantined, state.RouteUncertain:
			snap.State = parsed
		case state.RouteActiveDirty:
			if snap.State != state.RouteQuarantined && snap.State != state.RouteUncertain {
				snap.State = parsed
			}
		case state.RouteActiveClean:
			if snap.State == state.RouteIdle || snap.State == state.RouteFollowupReady {
				snap.State = parsed
			}
		case state.RouteFollowupReady:
			if snap.State == state.RouteIdle {
				snap.State = parsed
			}
		}
		if active != "" && snap.ActiveDispatchID == "" {
			snap.ActiveDispatchID = active
		}
		if dirty > snap.DirtyGeneration {
			snap.DirtyGeneration = dirty
		}
	}
	return snap, rows.Err()
}

// unionSelections merges destination-selection sets into one canonically
// sorted, de-duplicated slice (E12 epic whole-review round 2): selection
// evidence is written ONCE per occurrence and never narrowed — a later
// merging lane's evidence can only widen the recorded set. The canonical
// form itself is the ONE shared derivation in records
// (records.CanonicalDestinations, E12 epic whole-review round 3).
func unionSelections(sets ...[]string) []string {
	out := []string{}
	for _, set := range sets {
		for _, dest := range set {
			if dest != "" {
				out = append(out, dest)
			}
		}
	}
	return records.CanonicalDestinations(out)
}

// CommitMergePending persists one arriving lineage as merge_pending and
// durably increments the dirty generation of exactly the mergeDestinations
// lanes (CON-002, CON-008, FBK-001). An empty merge list merges the
// synthetic legacy lane (pre-cutover work, ADR-0016). The batch's
// selection evidence becomes the occurrence's FULL selectedDestinations
// UNION any selection the lineage's batch already carried (E12 epic
// whole-review round 2: the merging lanes are NOT necessarily the whole
// occurrence — a conditioned multi-lane burst whose first committing lane
// merges keeps every selected lane's follow-up whole). It returns the
// highest new dirty count.
func (s *Store) CommitMergePending(ctx context.Context, lin ports.Lineage, mergeDestinations, selectedDestinations []string, actor, now string) (int, error) {
	now = normalizeTimestamp(now)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := s.SaveObservation(tx, portsObservation(lin.Observation)); err != nil {
		return 0, err
	}
	// The merged batch records the occurrence's FULL destination selection
	// as durable evidence (E12 epic validation, migration v15): the
	// follow-up filter reads occurrence-level FAN-005 semantics from it
	// instead of re-evaluating conditions per change. The union keeps the
	// evidence monotonic — a pre-stamped multi-lane selection (the arrival
	// path stamps the occurrence's set on the shared batch) is never
	// narrowed to the merging lane (E12 epic whole-review round 2).
	batch := lin.Batch
	batch.SelectedDestinations = unionSelections(batch.SelectedDestinations, selectedDestinations)
	if err := s.SaveBatch(tx, BatchRecord{
		BatchID: batch.BatchID, RouteID: batch.RouteID, RouteRevision: batch.RouteRevision,
		ResourceID: batch.ResourceID, CreatedAt: batch.CreatedAt, ContentFingerprint: batch.ContentFingerprint,
		ObservationIDs: batch.ObservationIDs, SelectedDestinations: batch.SelectedDestinations,
	}); err != nil {
		return 0, err
	}
	// The merge is the outcome this transaction persists: the decision
	// records merge_pending, never the planner's optimistic dispatch
	// disposition (POL-006, E7-T6/M-18). The local copy keeps the caller's
	// lineage untouched (E12 epic whole-review round 2).
	merged := lin.Decision
	if merged.Disposition == "dispatch" {
		merged.Disposition = "merge_pending"
	}
	if err := s.SaveDecision(tx, portsDecision(merged)); err != nil {
		return 0, err
	}
	routeSnap, err := s.routeSnapshotInTx(tx, lin.Decision.RouteID)
	if err != nil {
		return 0, err
	}
	if routeSnap.State == state.RouteQuarantined {
		return 0, fmt.Errorf("%w: route %s is %s, arriving work cannot merge", ports.ErrStateNotEligible, lin.Decision.RouteID, routeSnap.State)
	}
	maxDirty, _, err := s.mergeLanesTx(tx, lin.Decision.RouteID, mergeDestinations, actor, now, lin.Batch.BatchID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return maxDirty, nil
}

// MergeSelectedLanes durably increments the dirty generation of exactly
// the mergeDestinations lanes under the merge guards (CON-008, E12-T2)
// without re-persisting the occurrence's lineage: a fan-out sibling that
// lost its lane's slot race merges beside the winner's already-committed
// prefix. An empty list merges the synthetic legacy lane. The occurrence's
// FULL selectedDestinations union onto the named batch's selection
// evidence (E12 epic whole-review round 2): the first committer recorded
// the shared batch, and every later lane's merge widens — never narrows —
// the recorded selection so each selected lane's follow-up keeps the whole
// burst. An empty batchID skips the evidence write.
func (s *Store) MergeSelectedLanes(ctx context.Context, routeID, batchID string, mergeDestinations, selectedDestinations []string, actor, now string) (int, error) {
	now = normalizeTimestamp(now)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	routeSnap, err := s.routeSnapshotInTx(tx, routeID)
	if err != nil {
		return 0, err
	}
	if routeSnap.State == state.RouteQuarantined {
		return 0, fmt.Errorf("%w: route %s is %s, arriving work cannot merge", ports.ErrStateNotEligible, routeID, routeSnap.State)
	}
	if err := s.unionBatchSelectionTx(tx, batchID, selectedDestinations); err != nil {
		return 0, err
	}
	maxDirty, _, err := s.mergeLanesTx(tx, routeID, mergeDestinations, actor, now, batchID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return maxDirty, nil
}

// unionBatchSelectionTx unions selectedDestinations onto one persisted
// batch's selection evidence inside the caller's transaction (E12 epic
// whole-review round 2). An empty batchID or empty selection is a no-op.
// A named batch that does not exist fails closed: the evidence is
// load-bearing (FAN-005 occurrence-level filtering) — silently skipping
// the write would recreate the silent-work-loss class this evidence
// exists to prevent.
func (s *Store) unionBatchSelectionTx(tx *sql.Tx, batchID string, selectedDestinations []string) error {
	if batchID == "" || len(selectedDestinations) == 0 {
		return nil
	}
	var existing string
	err := txOrDB(tx, s.DB).QueryRow(`SELECT COALESCE(selected_destinations_json, '') FROM change_batches WHERE batch_id = ?`, batchID).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("batch %s carries no row to record the occurrence's selection evidence: %w", batchID, ports.ErrInvalidFanoutRecord)
	}
	if err != nil {
		return err
	}
	var recorded []string
	if existing != "" {
		if err := json.Unmarshal([]byte(existing), &recorded); err != nil {
			return fmt.Errorf("batch %s selection evidence is not valid JSON: %w", batchID, err)
		}
	}
	merged := unionSelections(recorded, selectedDestinations)
	raw, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	if _, err := execOn(tx, s.DB, `UPDATE change_batches SET selected_destinations_json = ? WHERE batch_id = ?`, string(raw), batchID); err != nil {
		return err
	}
	return nil
}

// mergeLanesTx bumps the named lanes' dirty generations with the E3-T1
// merge guards inside the caller's transaction (CON-008): an IDLE lane
// with an empty slot records the owed work as the route's pending
// reconciliation instead (the route-keyed collapse), a reserved-but-not-
// activated slot consumes its reservation first, active lanes take the
// ACTIVE_* -> ACTIVE_DIRTY edge, and FOLLOWUP_READY or route-level
// UNCERTAIN lanes keep their state with the dirty count still recorded.
// It returns the highest new dirty count and whether any lane recorded a
// pending reconciliation instead.
func (s *Store) mergeLanesTx(tx *sql.Tx, routeID string, selectedDestinations []string, actor, now, batchID string) (int, bool, error) {
	destinations := selectedDestinations
	if len(destinations) == 0 {
		destinations = []string{LegacyLaneID}
	}
	maxDirty := 0
	idleEmpty := false
	for _, dest := range destinations {
		if err := s.materializeLaneTx(tx, routeID, dest); err != nil {
			return 0, false, err
		}
		snap, err := s.laneSnapshotInTx(tx, routeID, dest)
		if err != nil {
			return 0, false, err
		}
		// The lane's OWN dirty count drives the arithmetic: under the
		// route-level UNCERTAIN hold the merged snapshot carries the route
		// row's fence copy (another lane's value), and writing a computed
		// count from it could lower this lane's durable generation. The
		// write itself is monotonic (MAX) so no interleaving can decrement
		// any lane (review round 1, logic finding).
		ownDirty, err := s.laneOwnDirtyTx(tx, routeID, dest)
		if err != nil {
			return 0, false, err
		}
		// A lane-level IDLE merge with an empty slot has no dispatch that
		// could own the burst's dirty generation (a disabled or paused
		// lane, or the slot winner completing inside the race window):
		// recording dirty here would wedge the next clean completion
		// against a count no later receipt can clear. The owed work is
		// recorded as the route's pending reconciliation instead; the next
		// arrival or the scheduled reconcile delivers it as latest-state
		// work (E8-T1 round-1 F001, route-keyed collapse).
		if snap.State == state.RouteIdle && snap.ActiveDispatchID == "" {
			idleEmpty = true
			continue
		}
		// Merging onto IDLE only happens when this arrival lost the lane's
		// slot race: the winner's completion collapses the recorded
		// generation (the loser reached here through ErrRouteSlotHeld).
		dirtyAfter := ownDirty + 1
		if snap.State == state.RouteIdle && snap.ActiveDispatchID != "" {
			// A reserved-but-not-yet-activated dispatch still implies lane
			// activity for coordination: consume its own reservation
			// first — UNLESS the lane's serialization group is occupied
			// (E15-T3, CON-012): the reservation's real activation is
			// group-gated, and consuming it here would run a second group
			// child. The dirty generation still records the burst and the
			// reserved dispatch activates through its own gated path.
			groupFree := true
			if groupID, ok, gerr := s.groupOfLaneTx(tx, routeID, dest); gerr != nil {
				return 0, false, gerr
			} else if ok {
				var gState string
				var gRoute, gDest sql.NullString
				if err := tx.QueryRow(`SELECT state, holder_route_id, holder_destination_id FROM serialization_groups WHERE group_id = ?`, groupID).
					Scan(&gState, &gRoute, &gDest); err != nil {
					return 0, false, err
				}
				groupFree = gState == GroupStateOpen || (nullText(gRoute) == routeID && nullText(gDest) == dest)
			}
			if groupFree {
				if err := s.applyLaneTransition(tx, routeID, dest, snap, state.RouteActiveClean, state.ReasonDispatchAccepted,
					state.RouteEvidence{Actor: actor, ActivatingDispatchID: snap.ActiveDispatchID}, now,
					auditJSON("reason", state.ReasonDispatchAccepted, "dispatch_id", snap.ActiveDispatchID, "note", "reservation activation before merge", "destination_id", dest)); err != nil {
					return 0, false, err
				}
				snap.State = state.RouteActiveClean
				if err := s.acquireGroupSlotTx(tx, routeID, dest, snap.ActiveDispatchID, actor, now); err != nil {
					return 0, false, err
				}
			}
		}
		if snap.State.IsActive() {
			// Active work exists on this lane: a validated ACTIVE_* ->
			// ACTIVE_DIRTY transition marks the durable generation.
			reason := state.ReasonLaterRelevantChange
			if snap.State == state.RouteActiveDirty {
				reason = state.ReasonMoreChangesMerged
			}
			if err := s.applyLaneTransition(tx, routeID, dest, snap, state.RouteActiveDirty, reason, state.RouteEvidence{
				Actor: actor, DirtyGenerationAfter: dirtyAfter,
			}, now, auditJSON("reason", reason, "batch_id", batchID, "dirty_generation_after", dirtyAfter, "destination_id", dest)); err != nil {
				return 0, false, err
			}
		}
		// FOLLOWUP_READY and the route-level UNCERTAIN hold keep their
		// state: the latest-state follow-up (or the operator resolution)
		// absorbs the retained changes (CON-004, CON-005); the dirty count
		// still records them. MAX keeps the write monotonic: a stale
		// snapshot can never lower the lane's durable generation.
		if _, err := tx.Exec(`UPDATE destination_lane_state SET dirty_generation = MAX(dirty_generation, ?), dirty_since = COALESCE(dirty_since, ?) WHERE route_id = ? AND destination_id = ?`,
			dirtyAfter, now, routeID, dest); err != nil {
			return 0, false, err
		}
		if dirtyAfter > maxDirty {
			maxDirty = dirtyAfter
		}
	}
	if idleEmpty {
		// The pending-generation appearance is reportable: the intent
		// joins this transaction before the flag is set (E13-T1,
		// OPS-013), firing only when the route was not already pending.
		if err := s.notifyPendingReconcileTx(tx, routeID, "merge:"+batchID,
			map[string]string{"batch_id": batchID, "actor": actor, "origin": "merge"}, now); err != nil {
			return 0, false, err
		}
		if _, err := tx.Exec(`UPDATE route_runtime_state SET pending_reconcile = 1 WHERE route_id = ?`, routeID); err != nil {
			return 0, false, err
		}
	}
	// While the route holds uncertainty the resolution fence compares the
	// ROUTE row's dirty generation, so a merge under the hold keeps the
	// route-level copy fresh (the hold's flows stay route-keyed).
	if routeSnapState, rerr := s.routeStateOfTx(tx, routeID); rerr == nil && routeSnapState == state.RouteUncertain {
		if _, err := tx.Exec(`UPDATE route_runtime_state SET dirty_generation = dirty_generation + 1, dirty_since = COALESCE(dirty_since, ?) WHERE route_id = ?`,
			now, routeID); err != nil {
			return 0, false, err
		}
	}
	return maxDirty, idleEmpty, nil
}

// laneOwnDirtyTx reads one lane row's own dirty generation inside the
// caller's transaction (the merged snapshot may carry the route-level
// hold's fence copy instead, mergeLanesTx's monotonic arithmetic reads
// this value).
func (s *Store) laneOwnDirtyTx(tx *sql.Tx, routeID, destinationID string) (int, error) {
	var dirty int
	if err := tx.QueryRow(`SELECT dirty_generation FROM destination_lane_state WHERE route_id = ? AND destination_id = ?`,
		routeID, destinationID).Scan(&dirty); err != nil {
		return 0, err
	}
	return dirty, nil
}

// routeStateOfTx reads only the route row's coordination state inside a
// transaction.
func (s *Store) routeStateOfTx(tx *sql.Tx, routeID string) (state.RouteState, error) {
	var routeState string
	if err := tx.QueryRow(`SELECT route_state FROM route_runtime_state WHERE route_id = ?`, routeID).Scan(&routeState); err != nil {
		return "", err
	}
	return state.ParseRouteState(routeState)
}

// CommitFanoutChild persists one additional child intent of an occurrence
// whose shared observation/batch/decision lineage a sibling already
// committed (E12-T2, FAN-003): the intent, its child-dispatch record and
// aggregate references, and its lane-slot reservation commit in one
// transaction. ErrRouteSlotHeld reports the lane's held slot; every other
// failure wraps as a typed store error so callers classify genuine
// storage faults as storage, not internal defects (E12 epic whole-review
// round 2). The creation audit names the child's OWN origin and
// destination from its fanout block (E12 epic whole-review round 2) — a
// reconcile sibling is audited as reconcile work on its lane, never as
// an arrival.
func (s *Store) CommitFanoutChild(ctx context.Context, intent ports.IntentInput) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return ports.WrapStore(err)
	}
	defer tx.Rollback()
	if err := s.SaveIntent(tx, portsIntent(intent)); err != nil {
		return ports.WrapStore(mapIntentConstraint(err))
	}
	// The creation audit carries the child's own lineage: the fanout
	// block's origin (arrival, reconcile, ...) and destination lane when
	// the block exists, the legacy arrival shape otherwise.
	origin, destination := "arrival", ""
	if intent.Fanout != nil {
		origin = intent.Fanout.Origin
		destination = intent.Fanout.DestinationID
	}
	contextKeys := []any{"reason", origin, "origin", origin, "route_id", intent.RouteID,
		"route_revision", intent.RouteRevision, "generation", intent.Generation, "fanout_child", true}
	if destination != "" {
		contextKeys = append(contextKeys, "destination_id", destination)
	}
	if err := s.AppendTransition(tx, intent.DispatchID+":created", "dispatch_intent", intent.DispatchID, "", "ready", intent.CreatedAt,
		auditJSON(contextKeys...)); err != nil {
		return ports.WrapStore(err)
	}
	if err := tx.Commit(); err != nil {
		return ports.WrapStore(err)
	}
	return nil
}

// CompleteActive applies one work-completion transaction (persistence
// §7, E12-T2 lane-keyed): the E3-T1-validated lane transition of the
// completed dispatch's lane and, when dirty work or pending
// reconciliation remains, exactly one follow-up decision and intent for
// latest state. The pending-reconciliation collapse stays on the route row
// (the shared generation every lane's completion may absorb).
func (s *Store) CompleteActive(ctx context.Context, req ports.ActiveCompletion) (ports.FollowupCreated, error) {
	now := normalizeTimestamp(req.Now)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return ports.FollowupCreated{}, err
	}
	defer tx.Rollback()
	req.Now = now
	out, err := s.completeActiveTx(ctx, tx, req)
	if err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

// completeActiveTx applies the completion transaction inside the
// caller's transaction so receipt persistence can join it atomically.
func (s *Store) completeActiveTx(ctx context.Context, tx *sql.Tx, req ports.ActiveCompletion) (ports.FollowupCreated, error) {
	var out ports.FollowupCreated
	now := req.Now
	routeID, lane, laneErr := s.laneOfDispatch(tx, req.DispatchID)
	if laneErr != nil {
		return out, laneErr
	}
	if routeID == "" {
		return out, fmt.Errorf("%w: %s", ports.ErrIntentNotFound, req.DispatchID)
	}
	if err := s.materializeLaneTx(tx, routeID, lane); err != nil {
		return out, err
	}
	snap, err := s.laneSnapshotInTx(tx, routeID, lane)
	if err != nil {
		return out, err
	}
	if !snap.State.IsActive() {
		return out, fmt.Errorf("%w: lane %s/%s is %s, not active", ports.ErrStateNotEligible, routeID, lane, snap.State)
	}
	if snap.ActiveDispatchID != req.DispatchID {
		return out, fmt.Errorf("%w: lane %s/%s active dispatch is %s, not %s", ports.ErrStateNotEligible, routeID, lane, snap.ActiveDispatchID, req.DispatchID)
	}
	// The failure budget is configuration-owned (configuration-spec §9):
	// the caller's remaining count is the authoritative value the E3-T1
	// guards evaluate.
	snap.FailureBudget = req.FailureBudgetRemaining
	out.DirtyGeneration = snap.DirtyGeneration
	// The attribution decision was derived from an earlier read: if the
	// generation moved since, the receipt matched a different
	// generation than the one being completed — refuse (E5 audit). The
	// fence is explicit so generation zero is fenced like any other.
	if req.FenceGeneration && req.ExpectedDirtyGeneration != snap.DirtyGeneration {
		return out, fmt.Errorf("%w: dirty generation moved to %d while the receipt was being evaluated (expected %d)", ErrOptimisticConcurrency, snap.DirtyGeneration, req.ExpectedDirtyGeneration)
	}
	// A partially_completed outcome owes the remaining scope (E12-T3,
	// FBK-010): when the lane's dirty generation is zero the remaining
	// work is recorded through the documented dirtying edge — the same
	// guarded, audited transition a merged burst takes — so the follow-up
	// edge below carries exactly one same-lane follow-up.
	if req.RemainingWork && snap.DirtyGeneration == 0 && snap.State.IsActive() {
		if err := s.applyLaneTransition(tx, routeID, lane, snap, state.RouteActiveDirty, state.ReasonLaterRelevantChange,
			state.RouteEvidence{Actor: req.Actor, DirtyGenerationAfter: 1}, now,
			auditJSON("reason", state.ReasonLaterRelevantChange, "dispatch_id", req.DispatchID, "dirty_generation_after", 1,
				"destination_id", lane, "note", "partially completed remaining scope")); err != nil {
			return out, err
		}
		snap.State = state.RouteActiveDirty
		snap.DirtyGeneration = 1
	}
	needsFollowup := (snap.DirtyGeneration > 0 && !req.DirtySuppressed) || snap.PendingReconcile
	// The caller built the follow-up from an earlier read. If the
	// transaction now sees work the caller did not (a reconciliation
	// arrival or release flipping pending_reconcile without moving the
	// fenced dirty generation), refusing beats silently dropping the
	// signal into a follow-up-less FOLLOWUP_READY. A completion over the
	// follow-up budget is the deliberate exception: it deliberately
	// schedules no follow-up and resolves through UNCERTAIN instead
	// (E8-T1, FBK-008).
	overFollowupBudget := needsFollowup && req.FollowupGeneration > state.MaxConsecutiveFollowups
	if needsFollowup && !overFollowupBudget && req.FollowupRequest == nil {
		return out, fmt.Errorf("%w: lane %s/%s needs a follow-up (dirty %d, pending reconciliation %v) but none was prepared", ErrOptimisticConcurrency, routeID, lane, snap.DirtyGeneration, snap.PendingReconcile)
	}
	var to state.RouteState
	var reason state.RouteReason
	if needsFollowup {
		if req.Failed && req.FailureBudgetRemaining <= 0 {
			to, reason = state.RouteUncertain, state.ReasonRetryBudgetExhausted
		} else if overFollowupBudget {
			// The consecutive follow-up chain exceeded its bound: the
			// route resolves through operator reconciliation instead of
			// scheduling another generation (E8-T1, H-1.1). The active
			// slot and dirty generation stay recorded for the resolution.
			to, reason = state.RouteUncertain, state.ReasonFollowupBudgetExhausted
		} else if req.Failed {
			to, reason = state.RouteFollowupReady, state.ReasonWorkRetryBudgetRemains
		} else {
			to, reason = state.RouteFollowupReady, state.ReasonWorkCompletedDirty
		}
	} else if req.Failed {
		if req.FailureBudgetRemaining <= 0 {
			to, reason = state.RouteUncertain, state.ReasonRetryBudgetExhausted
		} else {
			to, reason = state.RouteFollowupReady, state.ReasonWorkRetryBudgetRemains
		}
	} else {
		to, reason = state.RouteIdle, state.ReasonWorkCompletedClean
		if req.DirtySuppressed && snap.DirtyGeneration > 0 {
			reason = state.ReasonWorkSuppressed
		}
	}
	evidence := state.RouteEvidence{Actor: req.Actor, ReceiptRef: req.ReceiptRef, FollowupGeneration: req.FollowupGeneration}
	transitionCtx := auditJSON("reason", reason, "dispatch_id", req.DispatchID, "dirty_generation", snap.DirtyGeneration, "failed", req.Failed, "destination_id", lane)
	// The completion's lane transition is reportable: its notification
	// intent joins this transaction (DUR-016, E13-T1, ADR-0019) with the
	// completing dispatch as the transition occurrence, so a replay or
	// rerun collapses onto the same notification identity (AC-902).
	notifyWork := func() error {
		_, err := s.enqueueNotificationTx(tx, routeID, notificationEventOfWork(to, req.Failed),
			"dispatch:"+req.DispatchID+":"+string(to), lane,
			map[string]string{
				"dispatch_id":    req.DispatchID,
				"destination_id": lane,
				"reason":         string(reason),
				"route_state":    string(to),
				"receipt_ref":    req.ReceiptRef,
				"failed":         fmt.Sprintf("%v", req.Failed),
				"followup":       fmt.Sprintf("%v", out.FollowupDispatchID != ""),
			}, now)
		return err
	}
	if to == state.RouteUncertain {
		// Uncertainty is a route-level hold (E12-T2): it blocks every lane
		// and its resolution stays route-keyed. The held lane takes its own
		// audited UNCERTAIN transition (retaining its slot and dirty
		// generation) and the route row carries the fence copy.
		if err := s.routeUncertainHoldTx(tx, snap, lane, reason, evidence, now, transitionCtx); err != nil {
			return out, err
		}
		if err := notifyWork(); err != nil {
			return out, err
		}
		out.RouteTo = to
		return out, nil
	}
	if err := s.applyLaneTransition(tx, routeID, lane, snap, to, reason, evidence, now, transitionCtx); err != nil {
		return out, err
	}
	if to == state.RouteIdle {
		// Clean completion clears the lane's active slot (invariant 5
		// freed per lane); an exact-suppressed dirty generation clears
		// with it.
		if _, err := tx.Exec(`UPDATE destination_lane_state SET active_dispatch_id = NULL, active_generation = 0,
			dirty_generation = CASE WHEN ? THEN 0 ELSE dirty_generation END,
			dirty_since = CASE WHEN ? THEN NULL ELSE dirty_since END
			WHERE route_id = ? AND destination_id = ? AND active_dispatch_id = ?`, req.DirtySuppressed, req.DirtySuppressed, routeID, lane, req.DispatchID); err != nil {
			return out, err
		}
		// E15-T3 (CON-012): the completing dispatch releases the group
		// slot in the same transaction; the oldest first-dirty waiting
		// lane of the group receives the next activation as a
		// reservation, or the group opens.
		if err := s.releaseGroupSlotTx(tx, routeID, lane, req.DispatchID, req.Actor, now); err != nil {
			return out, err
		}
	} else if to == state.RouteFollowupReady {
		// The completed dispatch no longer holds the lane's slot; the
		// follow-up takes it at activation.
		// The pending reconciliation generation collapses into this one
		// follow-up (repeated reconciliations never stack generations);
		// the flag stays route-keyed as the shared collapse.
		if _, err := tx.Exec(`UPDATE destination_lane_state SET active_dispatch_id = NULL, dirty_generation = 0, dirty_since = NULL WHERE route_id = ? AND destination_id = ? AND active_dispatch_id = ?`, routeID, lane, req.DispatchID); err != nil {
			return out, err
		}
		// E15-T3 (CON-012): the completed dispatch no longer holds the
		// group slot even though its lane stays FOLLOWUP_READY — the
		// follow-up re-acquires at activation, and an older waiting
		// lane may be promoted ahead of it.
		if err := s.releaseGroupSlotTx(tx, routeID, lane, req.DispatchID, req.Actor, now); err != nil {
			return out, err
		}
		if _, err := tx.Exec(`UPDATE route_runtime_state SET pending_reconcile = 0 WHERE route_id = ?`, routeID); err != nil {
			return out, err
		}
		if req.FollowupRequest != nil {
			decisionID := "dec-" + req.FollowupRequest.DispatchID
			// The follow-up decision records the independent policy
			// digest of the live route (E9-T3, L-18); a caller without a
			// live route leaves it empty and the route revision keeps the
			// pre-L-18 value so the column never regresses to blank.
			policyRevision := req.PolicyRevision
			if policyRevision == "" {
				policyRevision = req.FollowupRequest.RouteRevision
			}
			reasonCodes, _ := json.Marshal([]string{fmt.Sprintf("followup:%s", reason)})
			if err := s.SaveDecision(tx, DecisionRecord{
				DecisionID: decisionID, RouteID: req.RouteID, RouteRevision: req.FollowupRequest.RouteRevision,
				PolicyRevision: policyRevision, GenerationLineageJSON: lineageOr(req.DirtyLineageJSON),
				Disposition: "dispatch", Classification: "normal",
				ReasonCodesJSON: string(reasonCodes), CreatedAt: now, Actor: req.Actor,
			}); err != nil {
				return out, err
			}
			req.FollowupRequest.DecisionID = decisionID
			req.FollowupRequest.CreatedAt = now
			if err := s.SaveIntent(tx, portsIntent(*req.FollowupRequest)); err != nil {
				// The lane's slot was just freed by this transaction, so the
				// reservation succeeds; a failure here is a constraint
				// violation the caller must see.
				return out, mapIntentConstraint(err)
			}
			if err := s.AppendTransition(tx, req.FollowupRequest.DispatchID+":created", "dispatch_intent", req.FollowupRequest.DispatchID, "", "ready", now,
				auditJSON("reason", "followup", "dirty_generation", out.DirtyGeneration, "supersedes_dispatch", req.DispatchID, "latest_state", true, "destination_id", lane)); err != nil {
				return out, err
			}
			out.FollowupDispatchID = req.FollowupRequest.DispatchID
		}
	}
	if err := notifyWork(); err != nil {
		return out, err
	}
	out.RouteTo = to
	return out, nil
}

// ActivateDispatch applies the acceptance transition of one normal
// dispatch: the dispatch's lane IDLE -> ACTIVE_CLEAN consuming the
// dispatch's own slot reservation (persistence §6 "dispatch accepted",
// lane-keyed since E12-T2).
func (s *Store) ActivateDispatch(ctx context.Context, dispatchID, actor, now string) error {
	now = normalizeTimestamp(now)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	routeID, lane, laneErr := s.laneOfDispatch(tx, dispatchID)
	if laneErr != nil {
		return laneErr
	}
	if routeID == "" {
		return fmt.Errorf("%w: %s", ports.ErrIntentNotFound, dispatchID)
	}
	snap, err := s.laneSnapshotInTx(tx, routeID, lane)
	if err != nil {
		return err
	}
	if snap.State.IsActive() {
		// A concurrent merge already consumed this dispatch's
		// reservation: activation is idempotent for the holding
		// dispatch and a conflict for any other.
		if snap.ActiveDispatchID == dispatchID {
			return tx.Rollback()
		}
		return fmt.Errorf("%w: lane %s/%s is active with %s", ports.ErrStateNotEligible, routeID, lane, snap.ActiveDispatchID)
	}
	if err := s.applyLaneTransition(tx, routeID, lane, snap, state.RouteActiveClean, state.ReasonDispatchAccepted,
		state.RouteEvidence{Actor: actor, ActivatingDispatchID: dispatchID}, now,
		auditJSON("reason", state.ReasonDispatchAccepted, "dispatch_id", dispatchID, "destination_id", lane)); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE destination_lane_state SET active_dispatch_id = ? WHERE route_id = ? AND destination_id = ? AND (active_dispatch_id IS NULL OR active_dispatch_id = ?)`,
		dispatchID, routeID, lane, dispatchID); err != nil {
		return err
	}
	// E15-T3 (CON-011): the activation takes the group slot in the SAME
	// transaction — a group held by another lane rolls the lane
	// activation back and reports ErrGroupSlotHeld so the caller merges
	// the arrival; a preserved conflict refuses outright.
	if err := s.acquireGroupSlotTx(tx, routeID, lane, dispatchID, actor, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ActivateFollowup moves the dispatch's lane from FOLLOWUP_READY to
// ACTIVE_CLEAN (no dirty generation) or ACTIVE_DIRTY (a later burst merged
// while the follow-up waited, E8-T1/B-1) with the follow-up dispatch
// taking the lane's active slot (E3-T1 activation guard).
func (s *Store) ActivateFollowup(ctx context.Context, dispatchID, actor, now string) error {
	now = normalizeTimestamp(now)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var generation int64
	if err := tx.QueryRow(`SELECT generation FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ports.ErrIntentNotFound, dispatchID)
		}
		return err
	}
	routeID, lane, laneErr := s.laneOfDispatch(tx, dispatchID)
	if laneErr != nil {
		return laneErr
	}
	if routeID == "" {
		return fmt.Errorf("%w: %s", ports.ErrIntentNotFound, dispatchID)
	}
	if err := s.materializeLaneTx(tx, routeID, lane); err != nil {
		return err
	}
	snap, err := s.laneSnapshotInTx(tx, routeID, lane)
	if err != nil {
		return err
	}
	// A burst that arrived between completion and follow-up submission
	// left a dirty generation behind: activating into ACTIVE_CLEAN would
	// wedge the follow-up's own completion (no documented clean edge with
	// a dirty generation), so the dirty generation activates with it.
	to := state.RouteActiveClean
	reason := state.ReasonFollowupAccepted
	if snap.DirtyGeneration > 0 {
		to, reason = state.RouteActiveDirty, state.ReasonFollowupAcceptedDirty
	}
	if err := s.applyLaneTransition(tx, routeID, lane, snap, to, reason,
		state.RouteEvidence{Actor: actor, ActivatingDispatchID: dispatchID}, now,
		auditJSON("reason", reason, "dispatch_id", dispatchID, "dirty_generation", snap.DirtyGeneration, "destination_id", lane)); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE destination_lane_state SET active_dispatch_id = ?, active_generation = ? WHERE route_id = ? AND destination_id = ?`,
		dispatchID, int(generation), routeID, lane); err != nil {
		return err
	}
	// E15-T3 (CON-012): a follow-up activation takes the group slot the
	// same way a normal dispatch does; a promotion reserved for THIS
	// lane passes, another lane's hold (or reservation) refuses, and the
	// follow-up stays FOLLOWUP_READY until its turn.
	if err := s.acquireGroupSlotTx(tx, routeID, lane, dispatchID, actor, now); err != nil {
		return err
	}
	return tx.Commit()
}

// AgeState distinguishes the three active-dispatch age outcomes
// (E9-T2/T4-F006, T4-F004): no active dispatch, an unreadable
// timestamp, or a measured age.
type AgeState int

const (
	AgeNone AgeState = iota
	AgeUnreadable
	AgeMeasured
)

// ActiveDispatchAge reports the route's oldest lane-held active-dispatch
// age with its tri-state: AgeNone (no lane holds a slot), AgeUnreadable
// (the row exists but the timestamp does not parse), or AgeMeasured with
// the nanoseconds since creation. Since E12-T2 the slot lives on the
// lanes, so the age is measured over every lane of the route. The stored
// second-precision timestamp truncates downward, so a sub-second bound
// only applies once the dispatch crosses a full second.
func (s *Store) ActiveDispatchAge(ctx context.Context, routeID string) (AgeState, int64, error) {
	// MIN over no rows is NULL, not a value: scan nullable so a route with
	// no lane-held slot reads AgeNone instead of a scan failure.
	var createdAt sql.NullString
	err := s.QueryRowContext(ctx, `SELECT MIN(i.created_at) FROM dispatch_intents i
		JOIN destination_lane_state l ON l.route_id = i.route_id AND l.active_dispatch_id = i.dispatch_id
		WHERE i.route_id = ?`, routeID).Scan(&createdAt)
	if err != nil {
		return AgeNone, 0, err
	}
	if !createdAt.Valid || createdAt.String == "" {
		return AgeNone, 0, nil
	}
	created, perr := time.Parse(time.RFC3339, normalizeTimestamp(createdAt.String))
	if perr != nil {
		return AgeUnreadable, 0, nil
	}
	return AgeMeasured, int64(time.Since(created)), nil
}

// EligibleForStale is the store-level route-stale eligibility rule
// (E9-T2/T4-F006): staling live work requires an active lane dispatch to
// have held its slot at least as long as the configured bound. An
// unreadable age refuses (fail closed).
func (s *Store) EligibleForStale(ctx context.Context, routeID string, bound time.Duration) (bool, string, error) {
	ageState, age, err := s.ActiveDispatchAge(ctx, routeID)
	if err != nil {
		return false, "", err
	}
	switch ageState {
	case AgeNone:
		return false, "no active dispatch holds any lane slot", nil
	case AgeUnreadable:
		return false, "the active dispatch's creation timestamp is unreadable", nil
	default:
		if age < int64(bound) {
			var id string
			// Name the dispatch the age was measured against: the OLDEST
			// lane holder (created_at first, destination order only as the
			// deterministic tiebreak), never a younger sibling that merely
			// sorts first in destination order.
			_ = s.QueryRowContext(ctx, `SELECT l.active_dispatch_id FROM destination_lane_state l
				JOIN dispatch_intents i ON i.route_id = l.route_id AND i.dispatch_id = l.active_dispatch_id
				WHERE l.route_id = ? AND l.active_dispatch_id IS NOT NULL
				ORDER BY i.created_at, l.destination_id LIMIT 1`, routeID).Scan(&id)
			return false, fmt.Sprintf("active dispatch %s is inside the active_stale_after bound; live work is not stale", id), nil
		}
		return true, "", nil
	}
}

// routeSnapshotInTx reads the raw route runtime row inside a transaction.
// Since E12-T2 the route row's coordination columns are the frozen v12-era
// history plus the route-level hold values; only the route-keyed
// uncertainty/resolution flows read it directly. Coordination readers use
// laneSnapshotInTx or the aggregating LoadRouteState.
func (s *Store) routeSnapshotInTx(tx *sql.Tx, routeID string) (state.RouteSnapshot, error) {
	var activation, routeState string
	var ack, active sql.NullString
	var activeGen, dirtyGen int
	var pending bool
	err := tx.QueryRow(`SELECT activation_state, acknowledged_revision, route_state, active_dispatch_id, active_generation, dirty_generation, pending_reconcile
		FROM route_runtime_state WHERE route_id = ?`, routeID).Scan(&activation, &ack, &routeState, &active, &activeGen, &dirtyGen, &pending)
	if errors.Is(err, sql.ErrNoRows) {
		return state.RouteSnapshot{}, fmt.Errorf("route %s has no runtime state: %w", routeID, ErrOptimisticConcurrency)
	}
	if err != nil {
		return state.RouteSnapshot{}, err
	}
	parsed, err := state.ParseRouteState(routeState)
	if err != nil {
		return state.RouteSnapshot{}, err
	}
	return state.RouteSnapshot{
		RouteID: routeID, ActivationState: activation, State: parsed,
		ActiveDispatchID: nullText(active), DirtyGeneration: dirtyGen, PendingReconcile: pending,
		AcknowledgedRevision: nullText(ack),
	}, nil
}

// applyRouteTransition validates the E3-T1 guards and applies one route
// state transition inside the caller's transaction, bumping the version;
// dirty-count changes stay with the callers' explicit updates. Since
// E12-T2 this is the route-level hold machinery (QUARANTINED/UNCERTAIN
// resolution edges); lane coordination uses applyLaneTransition.
func (s *Store) applyRouteTransition(tx *sql.Tx, snap state.RouteSnapshot, to state.RouteState, reason state.RouteReason, evidence state.RouteEvidence, now, contextJSON string) error {
	if err := state.ValidateRouteTransition(snap, to, reason, evidence); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE route_runtime_state SET route_state = ?, version = version + 1
		WHERE route_id = ? AND route_state = ?`,
		string(to), snap.RouteID, string(snap.State))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("route %s left %s concurrently: %w", snap.RouteID, snap.State, ErrOptimisticConcurrency)
	}
	// Every route transition lands in the audit history inside the same
	// transaction (DUR-011, E7-T6/M-3): the route timeline is fully
	// reconstructable from state_transitions alone.
	var version int64
	if err := tx.QueryRow(`SELECT version FROM route_runtime_state WHERE route_id = ?`, snap.RouteID).Scan(&version); err != nil {
		return err
	}
	transitionID := fmt.Sprintf("%s:%s:v%d", snap.RouteID, reason, version)
	return s.AppendTransition(tx, transitionID, "route", snap.RouteID, string(snap.State), string(to), now, contextJSON)
}

func lineageOr(json string) string {
	if json == "" {
		return `{"generations":[]}`
	}
	return json
}

// MarkRouteStale moves a route with a stale active lane to the route-level
// UNCERTAIN hold through the declared execution-evidence-stale edge
// (E7-T7/M-6, lane-aware since E12-T2): the operator exit for a route
// whose lane-held active dispatch is older than active_stale_after. The
// uncertain route is then resolved through the documented reconciliation
// or lookup exits.
func (s *Store) MarkRouteStaleWithReason(ctx context.Context, routeID, actor, reason, now string) error {
	now = normalizeTimestamp(now)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The lane holding the OLDEST active dispatch is the stale evidence
	// (created_at first; destination order is only the deterministic
	// tiebreak), so the audited dispatch is the one whose age made the
	// route stale.
	rows, err := tx.Query(`SELECT l.destination_id FROM destination_lane_state l
		JOIN dispatch_intents i ON i.route_id = l.route_id AND i.dispatch_id = l.active_dispatch_id
		WHERE l.route_id = ? AND l.lane_state IN ('ACTIVE_CLEAN','ACTIVE_DIRTY') AND l.active_dispatch_id IS NOT NULL
		ORDER BY i.created_at, l.destination_id LIMIT 1`, routeID)
	if err != nil {
		return err
	}
	var lane string
	if rows.Next() {
		if err := rows.Scan(&lane); err != nil {
			rows.Close()
			return err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if lane == "" {
		snap, serr := s.routeSnapshotInTx(tx, routeID)
		if serr != nil {
			return serr
		}
		return fmt.Errorf("%w: route %s is %s with no active lane, not staled", ports.ErrStateNotEligible, routeID, snap.State)
	}
	snap, err := s.laneSnapshotInTx(tx, routeID, lane)
	if err != nil {
		return err
	}
	if err := s.routeUncertainHoldTx(tx, snap, lane, state.ReasonExecutionEvidenceStale,
		state.RouteEvidence{Actor: actor}, now,
		auditJSON("reason", state.ReasonExecutionEvidenceStale, "actor", actor, "active_dispatch_id", snap.ActiveDispatchID,
			"destination_id", lane, "operator_stale", true, "operator_reason", reason)); err != nil {
		return err
	}
	return tx.Commit()
}

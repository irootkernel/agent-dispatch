package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Route coordination (E3-T4): merge-pending, work completion with
// follow-up collapse, and follow-up activation. Every method is one
// transaction and applies the E3-T1 route guards.

// LoadRouteState returns the route's runtime snapshot.
func (s *Store) LoadRouteState(ctx context.Context, routeID string) (state.RouteSnapshot, error) {
	rec, err := s.LoadRouteRuntimeState(routeID)
	if err != nil {
		return state.RouteSnapshot{}, fmt.Errorf("route %s: %v", routeID, err)
	}
	parsed, err := state.ParseRouteState(rec.RouteState)
	if err != nil {
		return state.RouteSnapshot{}, err
	}
	return state.RouteSnapshot{
		RouteID:          rec.RouteID,
		ActivationState:  rec.ActivationState,
		State:            parsed,
		ActiveDispatchID: rec.ActiveDispatchID,
		DirtyGeneration:  rec.DirtyGeneration,
		PendingReconcile: rec.PendingReconcile,
	}, nil
}

// CommitMergePending persists one arriving lineage as merge_pending and
// durably increments the dirty generation in the same transaction
// (CON-002, FBK-001). It returns the new dirty count.
func (s *Store) CommitMergePending(ctx context.Context, lin ports.Lineage, actor, now string) (int, error) {
	now = normalizeTimestamp(now)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := s.SaveObservation(tx, portsObservation(lin.Observation)); err != nil {
		return 0, err
	}
	if err := s.SaveBatch(tx, lin.Batch.BatchID, lin.Batch.RouteID, lin.Batch.RouteRevision,
		lin.Batch.ResourceID, lin.Batch.CreatedAt, lin.Batch.ContentFingerprint, lin.Batch.ObservationIDs); err != nil {
		return 0, err
	}
	if err := s.SaveDecision(tx, portsDecision(lin.Decision)); err != nil {
		return 0, err
	}
	snap, err := s.routeSnapshotInTx(tx, lin.Decision.RouteID)
	if err != nil {
		return 0, err
	}
	if !snap.State.IsActive() && snap.State != state.RouteFollowupReady && snap.State != state.RouteUncertain && snap.State != state.RouteIdle {
		return 0, fmt.Errorf("%w: route %s is %s, arriving work cannot merge", ports.ErrStateNotEligible, lin.Decision.RouteID, snap.State)
	}
	// Merging onto IDLE only happens when this arrival lost a slot race:
	// the winner's completion collapses the recorded generation (the
	// loser reached here through ErrRouteSlotHeld).
	dirtyAfter := snap.DirtyGeneration + 1
	if snap.State == state.RouteIdle && snap.ActiveDispatchID != "" {
		// A reserved-but-not-yet-activated dispatch still implies route
		// activity for coordination: consume its own reservation first.
		if err := applyRouteTransition(tx, snap, state.RouteActiveClean, state.ReasonDispatchAccepted,
			state.RouteEvidence{Actor: actor, ActivatingDispatchID: snap.ActiveDispatchID}, now,
			fmt.Sprintf(`{"reason":%q,"dispatch_id":%q,"note":"reservation activation before merge"}`, state.ReasonDispatchAccepted, snap.ActiveDispatchID)); err != nil {
			return 0, err
		}
		snap.State = state.RouteActiveClean
	}
	if snap.State.IsActive() {
		// Active work exists: a validated ACTIVE_* -> ACTIVE_DIRTY
		// transition marks the durable generation.
		reason := state.ReasonLaterRelevantChange
		if snap.State == state.RouteActiveDirty {
			reason = state.ReasonMoreChangesMerged
		}
		if err := applyRouteTransition(tx, snap, state.RouteActiveDirty, reason, state.RouteEvidence{
			Actor: actor, DirtyGenerationAfter: dirtyAfter,
		}, now, fmt.Sprintf(`{"reason":%q,"batch_id":%q,"dirty_generation_after":%d}`, reason, lin.Batch.BatchID, dirtyAfter)); err != nil {
			return 0, err
		}
	}
	// FOLLOWUP_READY and UNCERTAIN keep their state: the latest-state
	// follow-up (or the operator resolution) absorbs the retained
	// changes (CON-004, CON-005); the dirty count still records them.
	if _, err := tx.Exec(`UPDATE route_runtime_state SET dirty_generation = ?, dirty_since = COALESCE(dirty_since, ?) WHERE route_id = ?`, dirtyAfter, now, lin.Decision.RouteID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return dirtyAfter, nil
}

// CompleteActive applies one work-completion transaction (persistence
// §7): the E3-T1-validated route transition and, when dirty work or
// pending reconciliation remains, exactly one follow-up decision and
// intent for latest state.
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
	snap, err := s.routeSnapshotInTx(tx, req.RouteID)
	if err != nil {
		return out, err
	}
	if !snap.State.IsActive() {
		return out, fmt.Errorf("%w: route %s is %s, not active", ports.ErrStateNotEligible, req.RouteID, snap.State)
	}
	if snap.ActiveDispatchID != req.DispatchID {
		return out, fmt.Errorf("%w: active dispatch is %s, not %s", ports.ErrStateNotEligible, snap.ActiveDispatchID, req.DispatchID)
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
	needsFollowup := (snap.DirtyGeneration > 0 && !req.DirtySuppressed) || snap.PendingReconcile
	// The caller built the follow-up from an earlier read. If the
	// transaction now sees work the caller did not (a reconciliation
	// arrival or release flipped pending_reconcile without moving the
	// fenced dirty generation), refusing beats silently dropping the
	// signal into a follow-up-less FOLLOWUP_READY.
	if needsFollowup && req.FollowupRequest == nil {
		return out, fmt.Errorf("%w: route %s needs a follow-up (dirty %d, pending reconciliation %v) but none was prepared", ErrOptimisticConcurrency, req.RouteID, snap.DirtyGeneration, snap.PendingReconcile)
	}
	var to state.RouteState
	var reason state.RouteReason
	if needsFollowup {
		to = state.RouteFollowupReady
		if req.Failed {
			if req.FailureBudgetRemaining <= 0 {
				to, reason = state.RouteUncertain, state.ReasonRetryBudgetExhausted
			} else {
				reason = state.ReasonWorkRetryBudgetRemains
			}
		} else {
			reason = state.ReasonWorkCompletedDirty
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
	evidence := state.RouteEvidence{Actor: req.Actor, ReceiptRef: req.ReceiptRef}
	if err := applyRouteTransition(tx, snap, to, reason, evidence, now,
		fmt.Sprintf(`{"reason":%q,"dispatch_id":%q,"dirty_generation":%d,"failed":%v}`, reason, req.DispatchID, snap.DirtyGeneration, req.Failed)); err != nil {
		return out, err
	}
	if to == state.RouteIdle {
		// Clean completion clears the active slot (invariant 5 freed);
		// an exact-suppressed dirty generation clears with it.
		if _, err := tx.Exec(`UPDATE route_runtime_state SET active_dispatch_id = NULL, active_generation = 0,
			dirty_generation = CASE WHEN ? THEN 0 ELSE dirty_generation END,
			dirty_since = CASE WHEN ? THEN NULL ELSE dirty_since END
			WHERE route_id = ? AND active_dispatch_id = ?`, req.DirtySuppressed, req.DirtySuppressed, req.RouteID, req.DispatchID); err != nil {
			return out, err
		}
	} else if to == state.RouteFollowupReady {
		// The completed dispatch no longer holds the slot; the follow-up
		// takes it at activation.
		// The pending reconciliation generation collapses into this one
		// follow-up (repeated reconciliations never stack generations).
		if _, err := tx.Exec(`UPDATE route_runtime_state SET active_dispatch_id = NULL, dirty_generation = 0, dirty_since = NULL, pending_reconcile = 0 WHERE route_id = ? AND active_dispatch_id = ?`, req.RouteID, req.DispatchID); err != nil {
			return out, err
		}
		if req.FollowupRequest != nil {
			decisionID := "dec-" + req.FollowupRequest.DispatchID
			// The follow-up inherits the revisions the completed dispatch
			// was planned under; the policy derives from route
			// configuration, so its revision is the route revision (the
			// same convention plan, reprocess, and reconcile record).
			reasonCodes, _ := json.Marshal([]string{fmt.Sprintf("followup:%s", reason)})
			if err := s.SaveDecision(tx, DecisionRecord{
				DecisionID: decisionID, RouteID: req.RouteID, RouteRevision: req.FollowupRequest.RouteRevision,
				PolicyRevision: req.FollowupRequest.RouteRevision, GenerationLineageJSON: lineageOr(req.DirtyLineageJSON),
				Disposition: "dispatch", Classification: "normal",
				ReasonCodesJSON: string(reasonCodes), CreatedAt: now, Actor: req.Actor,
			}); err != nil {
				return out, err
			}
			req.FollowupRequest.DecisionID = decisionID
			req.FollowupRequest.CreatedAt = now
			if err := s.SaveIntent(tx, portsIntent(*req.FollowupRequest)); err != nil {
				// The slot was just freed by this transaction, so the
				// reservation succeeds; a failure here is a constraint
				// violation the caller must see.
				return out, mapIntentConstraint(err)
			}
			if err := s.AppendTransition(tx, req.FollowupRequest.DispatchID+":created", "dispatch_intent", req.FollowupRequest.DispatchID, "", "ready", now,
				fmt.Sprintf(`{"reason":"followup","dirty_generation":%d,"supersedes_dispatch":%q,"latest_state":true}`, out.DirtyGeneration, req.DispatchID)); err != nil {
				return out, err
			}
			out.FollowupDispatchID = req.FollowupRequest.DispatchID
		}
	}
	out.RouteTo = to
	return out, nil
}

// ActivateDispatch applies the acceptance transition of one normal
// dispatch: IDLE -> ACTIVE_CLEAN consuming the dispatch's own slot
// reservation (persistence §6 "dispatch accepted").
func (s *Store) ActivateDispatch(ctx context.Context, dispatchID, actor, now string) error {
	now = normalizeTimestamp(now)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var routeID string
	if err := tx.QueryRow(`SELECT route_id FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&routeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ports.ErrIntentNotFound, dispatchID)
		}
		return err
	}
	snap, err := s.routeSnapshotInTx(tx, routeID)
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
		return fmt.Errorf("%w: route %s is active with %s", ports.ErrStateNotEligible, routeID, snap.ActiveDispatchID)
	}
	if err := applyRouteTransition(tx, snap, state.RouteActiveClean, state.ReasonDispatchAccepted,
		state.RouteEvidence{Actor: actor, ActivatingDispatchID: dispatchID}, now,
		fmt.Sprintf(`{"reason":%q,"dispatch_id":%q}`, state.ReasonDispatchAccepted, dispatchID)); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE route_runtime_state SET active_dispatch_id = ? WHERE route_id = ? AND (active_dispatch_id IS NULL OR active_dispatch_id = ?)`, dispatchID, routeID, dispatchID); err != nil {
		return err
	}
	return tx.Commit()
}

// ActivateFollowup moves FOLLOWUP_READY to ACTIVE_CLEAN with the
// follow-up dispatch taking the active slot (E3-T1 activation guard).
func (s *Store) ActivateFollowup(ctx context.Context, dispatchID, actor, now string) error {
	now = normalizeTimestamp(now)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var routeID string
	var generation int64
	if err := tx.QueryRow(`SELECT route_id, generation FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&routeID, &generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ports.ErrIntentNotFound, dispatchID)
		}
		return err
	}
	snap, err := s.routeSnapshotInTx(tx, routeID)
	if err != nil {
		return err
	}
	if err := applyRouteTransition(tx, snap, state.RouteActiveClean, state.ReasonFollowupAccepted,
		state.RouteEvidence{Actor: actor, ActivatingDispatchID: dispatchID}, now,
		fmt.Sprintf(`{"reason":%q,"dispatch_id":%q}`, state.ReasonFollowupAccepted, dispatchID)); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE route_runtime_state SET active_dispatch_id = ?, active_generation = ? WHERE route_id = ?`, dispatchID, int(generation), routeID); err != nil {
		return err
	}
	return tx.Commit()
}

// routeSnapshotInTx reads the route runtime snapshot inside a
// transaction.
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
	}, nil
}

// applyRouteTransition validates the E3-T1 guards and applies one route
// state transition inside the caller's transaction, bumping the version;
// dirty-count changes stay with the callers' explicit updates.
func applyRouteTransition(tx *sql.Tx, snap state.RouteSnapshot, to state.RouteState, reason state.RouteReason, evidence state.RouteEvidence, now, contextJSON string) error {
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
	return nil
}

func lineageOr(json string) string {
	if json == "" {
		return `{"generations":[]}`
	}
	return json
}

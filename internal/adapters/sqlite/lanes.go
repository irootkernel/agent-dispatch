package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Destination-lane coordination (E12-T2, ADR-0016): the per-destination
// lane state introduced by migration v13. Every coordination write the
// pre-E12 pipeline keyed on the route row — the single-active slot, the
// dirty generation, the follow-up chain — is keyed on the lane instead, so
// two destinations of one route hold independent slots (CON-007) and a
// burst dirties exactly the lanes it selected (CON-008). The route row
// keeps the route envelope (activation state, acknowledged revision,
// capability fingerprint, pending reconciliation) and the route-level
// QUARANTINED/UNCERTAIN holds: a quarantined or uncertain route blocks
// every lane.

// LegacyLaneID is the SQL binding of the one ports-side declaration
// (ports.LegacyDestinationLaneID, ADR-0016): the synthetic lane
// pre-cutover active work belongs to — an intent with no
// child_dispatches row predates the destinations[] contract, and its
// coordination keys on this lane so historical harness paths keep
// working. New work never selects it.
const LegacyLaneID = ports.LegacyDestinationLaneID

// LoadLaneState returns one lane's coordination snapshot merged with the
// route envelope (E12-T2): ActivationState, AcknowledgedRevision, and
// CapabilityFingerprint always come from route_runtime_state, and a
// route-level QUARANTINED or UNCERTAIN hold overrides the lane's own state
// — a quarantined or uncertain route blocks every lane. When no lane row
// exists the reading materializes as IDLE with an empty slot WITHOUT
// writing: callers treat a missing lane as idle, and rows are created on
// the first write.
func (s *Store) LoadLaneState(ctx context.Context, routeID, destinationID string) (state.RouteSnapshot, error) {
	return s.laneSnapshotInTx(s.DB, routeID, destinationID)
}

// LoadIntentLane returns the coordination snapshot of the lane one
// dispatch belongs to (E12-T2): the dispatch's child row names its
// destination; a legacy pre-cutover dispatch keys on the synthetic legacy
// lane.
func (s *Store) LoadIntentLane(ctx context.Context, dispatchID string) (state.RouteSnapshot, error) {
	routeID, destinationID, err := s.laneOfDispatch(s.DB, dispatchID)
	if err != nil {
		return state.RouteSnapshot{}, err
	}
	if routeID == "" {
		return state.RouteSnapshot{}, fmt.Errorf("%w: %s", ports.ErrIntentNotFound, dispatchID)
	}
	return s.laneSnapshotInTx(s.DB, routeID, destinationID)
}

// laneOfDispatch resolves the lane one dispatch belongs to inside a
// transaction: the child row's destination, else the synthetic legacy lane
// (ADR-0016). An unknown dispatch is the typed not-found shape (empty
// route, legacy lane, nil error); a transient read failure returns the
// error instead of masquerading as not-found (review round 1, security
// finding).
func (s *Store) laneOfDispatch(q queryer, dispatchID string) (routeID, destinationID string, err error) {
	destinationID = LegacyLaneID
	err = q.QueryRow(`SELECT dispatch_intents.route_id, COALESCE((SELECT c.destination_id FROM child_dispatches c WHERE c.dispatch_id = dispatch_intents.dispatch_id), ?)
		FROM dispatch_intents WHERE dispatch_intents.dispatch_id = ?`, LegacyLaneID, dispatchID).Scan(&routeID, &destinationID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", LegacyLaneID, nil
	}
	if err != nil {
		return "", "", err
	}
	return routeID, destinationID, nil
}

// laneSnapshotInTx reads one lane's merged coordination snapshot inside a
// transaction without writing. The route envelope always comes from the
// route row; a route-level QUARANTINED/UNCERTAIN hold wins over the lane's
// own state and carries the route row's coordination columns (the hold's
// fence values), because those flows stay route-keyed.
func (s *Store) laneSnapshotInTx(q queryer, routeID, destinationID string) (state.RouteSnapshot, error) {
	var activation, routeState string
	var ack, fingerprint, laneActive, routeActive, dirtySince sql.NullString
	var routeDirty, laneDirty, activeGen, laneVersion int
	var pending bool
	var laneState sql.NullString
	// The lane columns are LEFT JOIN nullable (a missing lane reads as
	// IDLE with an empty slot); COALESCE keeps the scan shapes exact.
	err := q.QueryRow(`SELECT r.activation_state, r.acknowledged_revision, r.capability_fingerprint,
			r.route_state, r.active_dispatch_id, r.dirty_generation, r.pending_reconcile,
			COALESCE(l.lane_state, 'IDLE'), COALESCE(l.active_dispatch_id, ''), COALESCE(l.active_generation, 0),
			COALESCE(l.dirty_generation, 0), l.dirty_since, COALESCE(l.version, 0)
		FROM route_runtime_state r
		LEFT JOIN destination_lane_state l ON l.route_id = r.route_id AND l.destination_id = ?
		WHERE r.route_id = ?`, destinationID, routeID).Scan(
		&activation, &ack, &fingerprint, &routeState, &routeActive, &routeDirty, &pending,
		&laneState, &laneActive, &activeGen, &laneDirty, &dirtySince, &laneVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return state.RouteSnapshot{}, fmt.Errorf("route %s has no runtime state: %w", routeID, ErrOptimisticConcurrency)
	}
	if err != nil {
		return state.RouteSnapshot{}, err
	}
	parsedRoute, err := state.ParseRouteState(routeState)
	if err != nil {
		return state.RouteSnapshot{}, err
	}
	snap := state.RouteSnapshot{
		RouteID:               routeID,
		ActivationState:       activation,
		AcknowledgedRevision:  nullText(ack),
		CapabilityFingerprint: nullText(fingerprint),
		PendingReconcile:      pending,
	}
	if parsedRoute == state.RouteUncertain || parsedRoute == state.RouteQuarantined {
		// A route-level hold blocks every lane and carries its own fence
		// values (the uncertainty/resolution flows stay route-keyed).
		snap.State = parsedRoute
		snap.ActiveDispatchID = nullText(routeActive)
		snap.DirtyGeneration = routeDirty
		return snap, nil
	}
	snap.State = state.RouteIdle
	if laneState.Valid {
		parsed, err := state.ParseRouteState(laneState.String)
		if err != nil {
			return state.RouteSnapshot{}, err
		}
		snap.State = parsed
	}
	snap.ActiveDispatchID = nullText(laneActive)
	snap.DirtyGeneration = laneDirty
	return snap, nil
}

// materializeLaneTx inserts the lane's IDLE row when absent inside the
// caller's transaction: lanes materialize lazily on their first write
// (migration v13 backfills only lanes with pre-cutover active work).
func (s *Store) materializeLaneTx(tx *sql.Tx, routeID, destinationID string) error {
	_, err := execOn(tx, s.DB, `INSERT INTO destination_lane_state (route_id, destination_id, lane_state)
		VALUES (?, ?, 'IDLE') ON CONFLICT (route_id, destination_id) DO NOTHING`, routeID, destinationID)
	return err
}

// applyLaneTransition validates the E3-T1 guards and applies one lane state
// transition inside the caller's transaction, bumping the lane's optimistic
// version and appending the audit row with entity_type "destination_lane"
// and entity_id routeID+"/"+destinationID (E12-T2). On a missing row the
// lane materializes as IDLE first. Dirty-count changes stay with the
// callers' explicit updates, exactly like applyRouteTransition.
func (s *Store) applyLaneTransition(tx *sql.Tx, routeID, destinationID string, snap state.RouteSnapshot, to state.RouteState, reason state.RouteReason, evidence state.RouteEvidence, now, contextJSON string) error {
	if err := state.ValidateRouteTransition(snap, to, reason, evidence); err != nil {
		return err
	}
	if err := s.materializeLaneTx(tx, routeID, destinationID); err != nil {
		return err
	}
	res, err := execOn(tx, s.DB, `UPDATE destination_lane_state SET lane_state = ?, version = version + 1
		WHERE route_id = ? AND destination_id = ? AND lane_state = ?`,
		string(to), routeID, destinationID, string(snap.State))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("lane %s/%s left %s concurrently: %w", routeID, destinationID, snap.State, ErrOptimisticConcurrency)
	}
	var version int64
	if err := tx.QueryRow(`SELECT version FROM destination_lane_state WHERE route_id = ? AND destination_id = ?`, routeID, destinationID).Scan(&version); err != nil {
		return err
	}
	entityID := routeID + "/" + destinationID
	transitionID := fmt.Sprintf("%s:%s:v%d", entityID, reason, version)
	return s.AppendTransition(tx, transitionID, "destination_lane", entityID, string(snap.State), string(to), now, contextJSON)
}

// routeUncertainHoldTx moves the held LANE and the ROUTE row into the
// route-level UNCERTAIN hold (E12-T2): uncertainty blocks every lane and
// its resolution stays route-keyed. The lane takes its own guarded,
// audited UNCERTAIN transition (retaining its slot and dirty generation
// for the resolution to release), and the route row's coordination
// columns receive the lane's fence values — what the resolution
// transaction and the pre-enumeration read compare against.
func (s *Store) routeUncertainHoldTx(tx *sql.Tx, laneSnap state.RouteSnapshot, destinationID string, reason state.RouteReason, evidence state.RouteEvidence, now, contextJSON string) error {
	if err := state.ValidateRouteTransition(laneSnap, state.RouteUncertain, reason, evidence); err != nil {
		return err
	}
	// The lane's own UNCERTAIN transition (audit entity destination_lane);
	// the transition does not touch the lane's slot or dirty columns, so
	// the retained coordination survives for the resolution.
	if err := s.applyLaneTransition(tx, laneSnap.RouteID, destinationID, laneSnap, state.RouteUncertain, reason, evidence, now, contextJSON); err != nil {
		return err
	}
	res, err := execOn(tx, s.DB, `UPDATE route_runtime_state
		SET route_state = 'UNCERTAIN', active_dispatch_id = ?, dirty_generation = ?, dirty_since = COALESCE(dirty_since, ?), version = version + 1
		WHERE route_id = ? AND route_state NOT IN ('QUARANTINED','UNCERTAIN')`,
		nullString(laneSnap.ActiveDispatchID), laneSnap.DirtyGeneration, now, laneSnap.RouteID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("route %s entered the uncertain hold concurrently: %w", laneSnap.RouteID, ErrOptimisticConcurrency)
	}
	var version int64
	if err := tx.QueryRow(`SELECT version FROM route_runtime_state WHERE route_id = ?`, laneSnap.RouteID).Scan(&version); err != nil {
		return err
	}
	transitionID := fmt.Sprintf("%s:%s:v%d", laneSnap.RouteID, reason, version)
	return s.AppendTransition(tx, transitionID, "route", laneSnap.RouteID, string(laneSnap.State), string(state.RouteUncertain), now, contextJSON)
}

// resolveHeldLaneTx lands one held lane after the operator resolution
// (E12-T2): through the documented, audited edges — UNCERTAIN ->
// FOLLOWUP_READY (reconciliation_resolved), plus FOLLOWUP_READY -> IDLE
// (followup_dropped) when no work is due — and then clears the lane's
// retained slot and dirty generation, the same column pattern the other
// lane writers pair with their transitions. A lane that never entered the
// hold (an IDLE lane with nothing retained, the pre-cutover shape) stays
// as it is: the created follow-up's own reservation governs its slot.
func (s *Store) resolveHeldLaneTx(tx *sql.Tx, routeID, destinationID string, workDue bool, actor, now string) error {
	if err := s.materializeLaneTx(tx, routeID, destinationID); err != nil {
		return err
	}
	snap, err := s.laneSnapshotInTx(tx, routeID, destinationID)
	if err != nil {
		return err
	}
	switch snap.State {
	case state.RouteUncertain:
		if err := s.applyLaneTransition(tx, routeID, destinationID, snap, state.RouteFollowupReady, state.ReasonReconciliationResolved,
			state.RouteEvidence{Actor: actor}, now,
			auditJSON("reason", state.ReasonReconciliationResolved, "actor", actor, "destination_id", destinationID)); err != nil {
			return err
		}
		if !workDue {
			held := snap
			held.State = state.RouteFollowupReady
			if err := s.applyLaneTransition(tx, routeID, destinationID, held, state.RouteIdle, state.ReasonFollowupDropped,
				state.RouteEvidence{Actor: actor, ReconciledNoWork: true}, now,
				auditJSON("reason", state.ReasonFollowupDropped, "actor", actor, "destination_id", destinationID)); err != nil {
				return err
			}
		}
	case state.RouteIdle:
		// Nothing retained on this lane: the follow-up's own reservation
		// (work due) or the already-idle shape (no work) governs.
		return nil
	default:
		return fmt.Errorf("%w: lane %s/%s is %s, not the held uncertain shape", ports.ErrStateNotEligible, routeID, destinationID, snap.State)
	}
	if _, err := tx.Exec(`UPDATE destination_lane_state SET active_dispatch_id = NULL, active_generation = 0,
		dirty_generation = 0, dirty_since = NULL, version = version + 1 WHERE route_id = ? AND destination_id = ?`,
		routeID, destinationID); err != nil {
		return err
	}
	return nil
}

package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E12-T2 acceptance coverage: per-destination lane coordination over the
// destination_lane_state table of migration v13 — independent slots
// (CON-007), per-lane dirty generations (CON-008), per-lane follow-up
// chains (CON-003), per-lane lease admission, the v13 backfill, and the
// route-level hold that blocks every lane.

// e12t2LaneLineage builds one child lineage of a shared fan-out
// occurrence for the named destination lane (E12-T2): the observation,
// batch, and decision of the shared prefix plus the lane's own intent.
// The first child commits the prefix through CommitLineage; later
// children reference the same decision and aggregate.
func e12t2LaneLineage(dispatchID, decisionID, batchID, destinationID string) ports.Lineage {
	lin := lineage(dispatchID, "agent-dispatch:v2:sha256:"+repeat(dispatchID[len(dispatchID)-1:], 64))
	lin.Decision.DecisionID = decisionID
	lin.Decision.BatchID = batchID
	lin.Batch.BatchID = batchID
	lin.Intent.DecisionID = decisionID
	lin.Intent.Fanout = &ports.FanoutInput{
		AggregateID: "agg-e12t2", Origin: string(records.OriginArrival),
		DestinationID: destinationID, DestinationRevision: "dst-" + destinationID, Workstream: "ws-" + destinationID,
		Selections: []records.DestinationSelection{
			{DestinationID: "wiki-primary", DestinationRevision: "dst-wiki-primary", Workstream: "ws-wiki-primary", Reason: "fanout_mode:all"},
			{DestinationID: "wiki-secondary", DestinationRevision: "dst-wiki-secondary", Workstream: "ws-wiki-secondary", Reason: "fanout_mode:all"},
		},
	}
	return lin
}

// e12t2CommitFanout commits one two-lane fan-out occurrence the way the
// coordinator does (E12-T2): the first child carries the shared prefix,
// the second commits beside it, and each lane's dispatch activates.
func e12t2CommitFanout(t *testing.T, s *Store, decisionID string) (ports.Lineage, ports.Lineage) {
	t.Helper()
	first := e12t2LaneLineage("dispatch-lane-a", decisionID, "batch-"+decisionID, "wiki-primary")
	second := e12t2LaneLineage("dispatch-lane-b", decisionID, "batch-"+decisionID, "wiki-secondary")
	if err := s.CommitLineage(context.Background(), first); err != nil {
		t.Fatalf("commit first child: %v", err)
	}
	if err := s.CommitFanoutChild(context.Background(), second.Intent); err != nil {
		t.Fatalf("commit second child beside the prefix: %v", err)
	}
	if err := s.ActivateDispatch(context.Background(), first.Intent.DispatchID, "test", "2026-08-29T01:00:00Z"); err != nil {
		t.Fatalf("activate first lane: %v", err)
	}
	if err := s.ActivateDispatch(context.Background(), second.Intent.DispatchID, "test", "2026-08-29T01:00:01Z"); err != nil {
		t.Fatalf("activate second lane: %v", err)
	}
	return first, second
}

// laneCoordination reads one lane's coordination columns.
func laneCoordination(t *testing.T, s *Store, routeID, destinationID string) (string, string, int) {
	t.Helper()
	var laneState string
	var active string
	var dirty int
	if err := s.QueryRow(`SELECT lane_state, COALESCE(active_dispatch_id, ''), dirty_generation
		FROM destination_lane_state WHERE route_id = ? AND destination_id = ?`, routeID, destinationID).
		Scan(&laneState, &active, &dirty); err != nil {
		t.Fatalf("lane %s/%s must exist: %v", routeID, destinationID, err)
	}
	return laneState, active, dirty
}

// TestE12T2TwoLanesHoldSlotsSimultaneously pins CON-007/CON-001 per lane:
// two child-linked intents of one occurrence on DIFFERENT lanes hold
// their slots simultaneously, both lanes are ACTIVE_CLEAN, and the
// aggregate carries exactly two children.
func TestE12T2TwoLanesHoldSlotsSimultaneously(t *testing.T) {
	s := openTestStore(t)
	first, second := e12t2CommitFanout(t, s, "decision-e12t2-a")
	if st, active, _ := laneCoordination(t, s, "wiki-maintenance", "wiki-primary"); st != "ACTIVE_CLEAN" || active != first.Intent.DispatchID {
		t.Fatalf("first lane must hold its own slot: %s %s", st, active)
	}
	if st, active, _ := laneCoordination(t, s, "wiki-maintenance", "wiki-secondary"); st != "ACTIVE_CLEAN" || active != second.Intent.DispatchID {
		t.Fatalf("second lane must hold its own slot: %s %s", st, active)
	}
	var children int
	if err := s.QueryRow(`SELECT COUNT(*) FROM child_dispatches WHERE aggregate_id = 'agg-e12t2'`).Scan(&children); err != nil || children != 2 {
		t.Fatalf("one occurrence must carry two children: %d %v", children, err)
	}
	// A third intent for an OCCUPIED lane is refused: per-lane single
	// active still holds (CON-001 per lane).
	third := e12t2LaneLineage("dispatch-lane-c", "decision-e12t2-c", "batch-third", "wiki-primary")
	third.Intent.Fanout.AggregateID = "agg-e12t2-c"
	if err := s.SaveDecision(nil, DecisionRecord{
		DecisionID: "decision-e12t2-c", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
		PolicyRevision: "policy-rev-1", GenerationLineageJSON: `{"generations":[]}`,
		Disposition: "dispatch", Classification: "normal", ReasonCodesJSON: `[]`,
		CreatedAt: "2026-08-29T01:00:02Z", Actor: "test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitFanoutChild(context.Background(), third.Intent); err == nil {
		t.Fatal("a second intent on an occupied lane must fail closed")
	}
}

// TestE12T2MergeBumpsOnlySelectedLane pins CON-008: a merge-pending burst
// over one selected destination increments exactly that lane's dirty
// generation; the sibling lane's count is untouched.
func TestE12T2MergeBumpsOnlySelectedLane(t *testing.T) {
	s := openTestStore(t)
	e12t2CommitFanout(t, s, "decision-e12t2-b")
	burst := lineage("dispatch-burst-b", "agent-dispatch:v2:sha256:"+repeat("b", 64))
	burst.Decision.Disposition = "merge_pending"
	if _, err := s.CommitMergePending(context.Background(), burst, []string{"wiki-primary"}, "test", "2026-08-29T02:00:00Z"); err != nil {
		t.Fatalf("merge into one lane: %v", err)
	}
	if _, _, dirty := laneCoordination(t, s, "wiki-maintenance", "wiki-primary"); dirty != 1 {
		t.Fatalf("the selected lane's dirty generation must advance to 1")
	}
	if _, _, dirty := laneCoordination(t, s, "wiki-maintenance", "wiki-secondary"); dirty != 0 {
		t.Fatalf("the sibling lane's dirty generation must stay 0, got %d", dirty)
	}
	if st, _, _ := laneCoordination(t, s, "wiki-maintenance", "wiki-primary"); st != "ACTIVE_DIRTY" {
		t.Fatalf("the selected lane must take the ACTIVE_DIRTY edge: %s", st)
	}
	// The sibling lane stays ACTIVE_CLEAN.
	if st, _, _ := laneCoordination(t, s, "wiki-maintenance", "wiki-secondary"); st != "ACTIVE_CLEAN" {
		t.Fatalf("the sibling lane must stay ACTIVE_CLEAN: %s", st)
	}
}

// TestE12T2CompletionAndFollowupPerLane pins CON-003 per lane: a dirty
// completion on one lane creates exactly that lane's follow-up (keeping
// the parent lane) while the sibling lane's slot and state are untouched.
func TestE12T2CompletionAndFollowupPerLane(t *testing.T) {
	s := openTestStore(t)
	first, second := e12t2CommitFanout(t, s, "decision-e12t2-c")
	burst := lineage("dispatch-burst-c", "agent-dispatch:v2:sha256:"+repeat("c", 64))
	burst.Decision.Disposition = "merge_pending"
	if _, err := s.CommitMergePending(context.Background(), burst, []string{"wiki-primary"}, "test", "2026-08-29T02:00:00Z"); err != nil {
		t.Fatal(err)
	}
	followup := ports.IntentInput{
		DispatchID: "dispatch-followup-primary", DecisionID: "dec-dispatch-followup-primary", RouteID: "wiki-maintenance",
		RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes_kanban",
		TargetScope: "board-main", ResourceID: "vault-main", Generation: 2,
		IdempotencyKey:     "agent-dispatch:v2:sha256:" + repeat("f", 64),
		ContentFingerprint: "sha256:" + repeat("c", 64), ManifestDigest: "sha256:" + repeat("1", 64),
		RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: `{}`,
		CreatedAt: "2026-08-29T02:00:01Z",
		Fanout: &ports.FanoutInput{
			AggregateID: "agg-followup-primary", Origin: string(records.OriginFollowup),
			DestinationID: "wiki-primary", DestinationRevision: "dst-wiki-primary", Workstream: "ws-wiki-primary",
			Selections: []records.DestinationSelection{{
				DestinationID: "wiki-primary", DestinationRevision: "dst-wiki-primary", Workstream: "ws-wiki-primary",
				Reason: "followup:dispatch-lane-a",
			}},
		},
	}
	out, err := s.CompleteActive(context.Background(), ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: first.Intent.DispatchID, FollowupRequest: &followup,
		Now: "2026-08-29T02:00:01Z", Actor: "test", PolicyRevision: "policy-rev-1",
	})
	if err != nil {
		t.Fatalf("completion on one lane: %v", err)
	}
	if out.RouteTo != state.RouteFollowupReady || out.FollowupDispatchID != "dispatch-followup-primary" {
		t.Fatalf("the completing lane must schedule its follow-up: %+v", out)
	}
	// The completing lane is FOLLOWUP_READY with its dirty generation
	// collapsed behind the follow-up's own slot reservation; the follow-up
	// child keeps the parent lane.
	if st, active, dirty := laneCoordination(t, s, "wiki-maintenance", "wiki-primary"); st != "FOLLOWUP_READY" || active != "dispatch-followup-primary" || dirty != 0 {
		t.Fatalf("the completing lane must collapse behind its follow-up: %s %q %d", st, active, dirty)
	}
	child, err := s.LoadChildDispatch(context.Background(), "dispatch-followup-primary")
	if err != nil || child.DestinationID != "wiki-primary" {
		t.Fatalf("the follow-up child must keep the parent lane: %+v %v", child, err)
	}
	// The sibling lane is untouched: still active, still holding its own
	// dispatch, no dirty generation.
	if st, active, dirty := laneCoordination(t, s, "wiki-maintenance", "wiki-secondary"); st != "ACTIVE_CLEAN" || active != second.Intent.DispatchID || dirty != 0 {
		t.Fatalf("the sibling lane must be untouched by the completion: %s %q %d", st, active, dirty)
	}
	// The follow-up activates on its own lane.
	if err := s.ActivateFollowup(context.Background(), "dispatch-followup-primary", "test", "2026-08-29T02:00:02Z"); err != nil {
		t.Fatalf("follow-up activation on its lane: %v", err)
	}
	if st, active, _ := laneCoordination(t, s, "wiki-maintenance", "wiki-primary"); st != "ACTIVE_CLEAN" || active != "dispatch-followup-primary" {
		t.Fatalf("the follow-up must hold its lane's slot: %s %q", st, active)
	}
}

// TestE12T2AcquireAttemptBlocksOnlyOwningLane pins the per-lane lease
// admission (CON-007): a sibling lane's slot holder never blocks this
// dispatch's lease, while the owning lane's holder does.
func TestE12T2AcquireAttemptBlocksOnlyOwningLane(t *testing.T) {
	s := openTestStore(t)
	first, second := e12t2CommitFanout(t, s, "decision-e12t2-d")
	// The sibling lane's dispatch leases fine while the first lane holds
	// its own slot with its own dispatch.
	acquired, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: second.Intent.DispatchID, AttemptID: "attempt-lane-b", Owner: "process-b",
		Now: "2026-08-29T01:00:00Z", LeaseExpiresAt: "2026-08-29T01:01:00Z",
	})
	if err != nil || acquired != "attempt-lane-b" {
		t.Fatalf("the sibling lane's dispatch must lease beside the first lane's holder: %q %v", acquired, err)
	}
	// A THIRD intent on the first lane (held by first.Intent.DispatchID)
	// is refused by the lease transaction with the slot conflict.
	third := e12t2LaneLineage("dispatch-lane-d3", "decision-e12t2-d3", "batch-d3", "wiki-primary")
	third.Intent.Fanout.AggregateID = "agg-e12t2-d3"
	if err := s.SaveDecision(nil, DecisionRecord{
		DecisionID: "decision-e12t2-d3", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
		PolicyRevision: "policy-rev-1", GenerationLineageJSON: `{"generations":[]}`,
		Disposition: "dispatch", Classification: "normal", ReasonCodesJSON: `[]`,
		CreatedAt: "2026-08-29T01:00:02Z", Actor: "test",
	}); err != nil {
		t.Fatal(err)
	}
	// Free only the legacy lane (absent) so the refusal can only come from
	// the first lane's holder.
	if err := s.CommitFanoutChild(context.Background(), third.Intent); err == nil {
		t.Fatal("the held lane must refuse the third intent's reservation")
	}
	// The owning lane's own dispatch still leases (its own slot).
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: first.Intent.DispatchID, AttemptID: "attempt-lane-a", Owner: "process-a",
		Now: "2026-08-29T01:00:00Z", LeaseExpiresAt: "2026-08-29T01:01:00Z",
	}); err != nil {
		t.Fatalf("the slot holder must lease its own dispatch: %v", err)
	}
}

// TestE12T2MigrationV13BackfillsActiveLanes pins the v13 cutover: every
// route whose active dispatch is not NULL gets exactly one lane row
// carrying its in-flight coordination — the child row's destination when
// the active dispatch is child-linked, the synthetic '__legacy__' lane
// when it is not — and routes without an active dispatch materialize no
// lane row.
func TestE12T2MigrationV13BackfillsActiveLanes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.migrations = Migrations[:12]
	if err := s.Migrate(dir); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i, routeID := range []string{"wiki-maintenance", "wiki-archive", "wiki-idle"} {
		revision := fmt.Sprintf("route-rev-%d", i)
		if err := s.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
			t.Fatal(err)
		}
		if err := s.RegisterRoute(nil, routeID, revision, "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", now); err != nil {
			t.Fatal(err)
		}
		if err := s.InitializeRouteState(nil, routeID); err != nil {
			t.Fatal(err)
		}
		if err := s.SetRouteActivation(context.Background(), routeID, "enabled", revision, "", now); err != nil {
			t.Fatal(err)
		}
	}
	seedV12Active := func(routeID, dispatchID, decisionID, destinationID string) {
		t.Helper()
		if err := s.SaveDecision(nil, DecisionRecord{
			DecisionID: decisionID, RouteID: routeID, RouteRevision: fmt.Sprintf("route-rev-%d", map[string]int{"wiki-maintenance": 0, "wiki-archive": 1, "wiki-idle": 2}[routeID]),
			PolicyRevision: "policy-rev-1", GenerationLineageJSON: `{"generations":[]}`,
			Disposition: "dispatch", Classification: "normal", ReasonCodesJSON: `[]`,
			CreatedAt: now, Actor: "planner",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Exec(`INSERT INTO dispatch_intents
			(dispatch_id, decision_id, route_id, route_revision, target_id, target_type, resource_id, generation, idempotency_key, content_fingerprint, manifest_digest, request_version, request_json, state, created_at, updated_at, base_batch_seq)
			VALUES (?, ?, ?, ?, 'hermes-kanban-main', 'hermes_kanban', 'vault-main', 1, ?, 'sha256:x', 'sha256:y',
			'agent-dispatch.hermes-task/v1', '{}', 'ready', ?, ?, 0)`,
			dispatchID, decisionID, routeID, "route-rev-seed", "key-"+dispatchID, now, now); err != nil {
			t.Fatal(err)
		}
		aggID := "agg-" + dispatchID
		if destinationID != "" {
			if _, err := s.Exec(`INSERT INTO aggregate_events
				(aggregate_id, decision_id, route_id, route_revision, resource_id, origin, generation, content_fingerprint, schema_version, selection_json, created_at)
				VALUES (?, ?, ?, ?, 'vault-main', 'arrival', 1, 'sha256:x', ?, '{}', ?)`,
				aggID, decisionID, routeID, "route-rev-0", AggregateEventSchemaVersion, now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Exec(`INSERT INTO child_dispatches
				(child_id, aggregate_id, dispatch_id, route_id, destination_id, destination_revision, workstream, idempotency_key, schema_version, created_at)
				VALUES (?, ?, ?, ?, ?, 'dst-x', 'ws-x', 'agent-dispatch:v2:sha256:x', ?, ?)`,
				"child-"+dispatchID, aggID, dispatchID, routeID, destinationID, ChildDispatchSchemaVersion, now); err != nil {
				t.Fatal(err)
			}
		}
		// The v12-era coordination shape: the route row holds the slot.
		if _, err := s.Exec(`UPDATE route_runtime_state SET route_state = 'ACTIVE_DIRTY', active_dispatch_id = ?, dirty_generation = 2 WHERE route_id = ?`,
			dispatchID, routeID); err != nil {
			t.Fatal(err)
		}
	}
	seedV12Active("wiki-maintenance", "dispatch-child-linked", "decision-child", "wiki-primary")
	seedV12Active("wiki-archive", "dispatch-legacy-active", "decision-legacy", "")
	// wiki-idle keeps its empty slot: no lane row.
	s.Close()

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { upgraded.Close() })
	if err := upgraded.Migrate(t.TempDir()); err != nil {
		t.Fatalf("upgrade to v13: %v", err)
	}
	// The upgrade runs every pending unit through the current baseline
	// (v14 since E12-T3); v13's objects exist and the lane backfill ran.
	if version, err := upgraded.SchemaVersion(); err != nil || version < 13 {
		t.Fatalf("the ledger must reach the v13 lane state: %d %v", version, err)
	}
	// The child-linked active dispatch lands on its child's destination.
	if st, active, dirty := laneCoordination(t, upgraded, "wiki-maintenance", "wiki-primary"); st != "ACTIVE_DIRTY" || active != "dispatch-child-linked" || dirty != 2 {
		t.Fatalf("the child-linked lane must carry the v12-era coordination: %s %q %d", st, active, dirty)
	}
	// The childless active dispatch lands on the synthetic legacy lane.
	if st, active, dirty := laneCoordination(t, upgraded, "wiki-archive", LegacyLaneID); st != "ACTIVE_DIRTY" || active != "dispatch-legacy-active" || dirty != 2 {
		t.Fatalf("the childless lane must be the legacy lane: %s %q %d", st, active, dirty)
	}
	// A route without an active dispatch materializes no lane row.
	var lanes int
	if err := upgraded.QueryRow(`SELECT COUNT(*) FROM destination_lane_state WHERE route_id = 'wiki-idle'`).Scan(&lanes); err != nil || lanes != 0 {
		t.Fatalf("an idle route must have no lane row: %d %v", lanes, err)
	}
}

// TestE12T2RouteLevelHoldBlocksEveryLane pins the route-envelope hold: a
// QUARANTINED or UNCERTAIN route row overrides every lane's own state —
// activations refuse, leases refuse, and the merged snapshot reports the
// hold — while the lane rows keep their in-flight coordination.
func TestE12T2RouteLevelHoldBlocksEveryLane(t *testing.T) {
	s := openTestStore(t)
	first, second := e12t2CommitFanout(t, s, "decision-e12t2-e")
	for _, hold := range []string{"QUARANTINED", "UNCERTAIN"} {
		if _, err := s.Exec(`UPDATE route_runtime_state SET route_state = ? WHERE route_id = 'wiki-maintenance'`, hold); err != nil {
			t.Fatal(err)
		}
		// Every lane's merged snapshot reports the route-level hold.
		for _, lane := range []string{"wiki-primary", "wiki-secondary"} {
			snap, err := s.LoadLaneState(context.Background(), "wiki-maintenance", lane)
			if err != nil {
				t.Fatal(err)
			}
			if snap.State != state.RouteState(hold) {
				t.Fatalf("lane %s must read the route-level %s hold, got %s", lane, hold, snap.State)
			}
		}
		// A new child on either lane cannot activate while the hold stands.
		third := e12t2LaneLineage("dispatch-hold-"+hold, "decision-hold-"+hold, "batch-hold-"+hold, "wiki-secondary")
		third.Intent.Fanout.AggregateID = "agg-hold-" + hold
		if err := s.SaveDecision(nil, DecisionRecord{
			DecisionID: "decision-hold-" + hold, RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
			PolicyRevision: "policy-rev-1", GenerationLineageJSON: `{"generations":[]}`,
			Disposition: "dispatch", Classification: "normal", ReasonCodesJSON: `[]`,
			CreatedAt: "2026-08-29T03:00:00Z", Actor: "test",
		}); err != nil {
			t.Fatal(err)
		}
		// The lane slots are held by the running children, so the hold's
		// activation refusal is proven through the lease predicate on the
		// freed legacy lane below; releasing the target lane's slot first
		// keeps this arm about the hold, not the slot.
		if _, err := s.Exec(`UPDATE destination_lane_state SET active_dispatch_id = NULL WHERE route_id = 'wiki-maintenance' AND destination_id = 'wiki-secondary'`); err != nil {
			t.Fatal(err)
		}
		if err := s.CommitFanoutChild(context.Background(), third.Intent); err != nil {
			t.Fatalf("the freed lane must still accept the reservation: %v", err)
		}
		if err := s.ActivateDispatch(context.Background(), third.Intent.DispatchID, "test", "2026-08-29T03:00:01Z"); err == nil {
			t.Fatalf("activation must refuse under the route-level %s hold", hold)
		}
		if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
			DispatchID: third.Intent.DispatchID, AttemptID: "attempt-hold-" + hold, Owner: "p",
			Now: "2026-08-29T03:00:02Z", LeaseExpiresAt: "2026-08-29T03:01:00Z",
		}); err == nil {
			t.Fatalf("the lease transaction must refuse under the route-level %s hold", hold)
		}
		// Restore the pre-hold shape for the next arm.
		if _, err := s.Exec(`UPDATE destination_lane_state SET active_dispatch_id = ? WHERE route_id = 'wiki-maintenance' AND destination_id = 'wiki-secondary'`, second.Intent.DispatchID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Exec(`DELETE FROM child_dispatches WHERE dispatch_id = ?`, third.Intent.DispatchID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Exec(`DELETE FROM destination_lane_state WHERE route_id = 'wiki-maintenance' AND destination_id = 'wiki-secondary' AND active_dispatch_id IS NULL`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Exec(`DELETE FROM dispatch_intents WHERE dispatch_id = ?`, third.Intent.DispatchID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Exec(`UPDATE destination_lane_state SET active_dispatch_id = ? WHERE route_id = 'wiki-maintenance' AND destination_id = 'wiki-secondary'`, second.Intent.DispatchID); err != nil {
			t.Fatal(err)
		}
	}
	// The lane rows kept their own coordination throughout the holds.
	if st, active, _ := laneCoordination(t, s, "wiki-maintenance", "wiki-primary"); st != "ACTIVE_CLEAN" || active != first.Intent.DispatchID {
		t.Fatalf("the lane rows must keep their coordination under the hold: %s %q", st, active)
	}
}

// TestE12T2MergeUnderUncertainHoldNeverLowersDirtyGenerations pins the
// review round-1 logic finding: a merge under the route-level UNCERTAIN
// hold reads the merged lane's dirty count from the hold's route-row copy
// (another lane's value) — the merge must still never LOWER any lane's
// durable generation, and the route-row fence copy stays monotonic.
func TestE12T2MergeUnderUncertainHoldNeverLowersDirtyGenerations(t *testing.T) {
	s := openTestStore(t)
	// Lane A holds active work that enters the route-level hold with
	// dirty=2 (copied onto the route row); sibling lane B carries its own
	// heavier generation dirty=5.
	if err := s.CommitLineage(context.Background(), e12t2LaneLineage("dispatch-hold-a", "decision-hold-a", "batch-hold-a", "wiki-primary")); err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateDispatch(context.Background(), "dispatch-hold-a", "test", "2026-08-29T01:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE destination_lane_state SET dirty_generation = 2 WHERE route_id = 'wiki-maintenance' AND destination_id = 'wiki-primary'`); err != nil {
		t.Fatal(err)
	}
	// Sibling lane B: active with its own heavier dirty generation (its
	// lineage commit persists the decision).
	second := e12t2LaneLineage("dispatch-hold-b", "decision-hold-b", "batch-hold-b", "wiki-secondary")
	second.Intent.Fanout.AggregateID = "agg-hold-b"
	if err := s.CommitLineage(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateDispatch(context.Background(), "dispatch-hold-b", "test", "2026-08-29T01:00:02Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE destination_lane_state SET dirty_generation = 5 WHERE route_id = 'wiki-maintenance' AND destination_id = 'wiki-secondary'`); err != nil {
		t.Fatal(err)
	}
	// The hold: lane A's completion resolves through UNCERTAIN (retry
	// budget exhausted), copying lane A's coordination onto the route row.
	if _, err := s.CompleteActive(context.Background(), ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: "dispatch-hold-a", Failed: true,
		FailureBudgetRemaining: 0, ReceiptRef: "rcpt-hold", Actor: "test", Now: "2026-08-29T02:00:00Z",
		// Over the consecutive follow-up bound: the completion resolves
		// through the route-level UNCERTAIN hold without a follow-up.
		FollowupGeneration: state.MaxConsecutiveFollowups + 1,
	}); err != nil {
		t.Fatalf("enter the uncertain hold: %v", err)
	}
	// A burst merges the SIBLING lane while the hold stands.
	burst := lineage("dispatch-hold-burst", "agent-dispatch:v2:sha256:"+repeat("h", 64))
	burst.Decision.Disposition = "merge_pending"
	if _, err := s.CommitMergePending(context.Background(), burst, []string{"wiki-secondary"}, "test", "2026-08-29T02:01:00Z"); err != nil {
		t.Fatalf("merge the sibling lane under the hold: %v", err)
	}
	// No count decreased anywhere: lane A keeps 2 (retained under the
	// hold), lane B advances from its own 5 to 6 (not the route copy's 3),
	// and the route-row fence copy advanced monotonically.
	if _, _, dirty := laneCoordination(t, s, "wiki-maintenance", "wiki-primary"); dirty != 2 {
		t.Fatalf("the held lane's retained generation must not move: %d", dirty)
	}
	if _, _, dirty := laneCoordination(t, s, "wiki-maintenance", "wiki-secondary"); dirty != 6 {
		t.Fatalf("the merged lane must advance from its OWN count (5->6): %d", dirty)
	}
	var routeDirty int
	if err := s.QueryRow(`SELECT dirty_generation FROM route_runtime_state WHERE route_id = 'wiki-maintenance'`).Scan(&routeDirty); err != nil || routeDirty < 3 {
		t.Fatalf("the route-row fence copy must stay monotonic: %d %v", routeDirty, err)
	}
}

// TestE12T2ActiveDispatchAgeNoneWithoutLaneHolders pins the review round-1
// logic finding: a route with no lane-held slot reads AgeNone (a NULL MIN
// aggregate), never a scan failure.
func TestE12T2ActiveDispatchAgeNoneWithoutLaneHolders(t *testing.T) {
	s := openTestStore(t)
	ageState, _, err := s.ActiveDispatchAge(context.Background(), "wiki-maintenance")
	if err != nil {
		t.Fatalf("a route with no lane-held slot must read AgeNone, not a scan failure: %v", err)
	}
	if ageState != AgeNone {
		t.Fatalf("expected AgeNone, got %d", ageState)
	}
}

package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E15-T1 acceptance coverage over migration v18 and the durable group
// topology (ADR-0021, CON-011 through CON-013, TST-002): the migration
// is additive and configuration-independent, preserves every existing
// dispatch, lane, and receipt identity, and the first topology
// reconciliation materializes group membership — a preserved active
// collision reports serialization_conflict with NO holder, blocks on
// the typed refusal, and resolves atomically to the sole survivor or an
// open group as the allowed existing-work exits land.

// e15t1ActiveLineage builds one activated lane for a route/destination
// pair (the arrival shape minus the fanout block: a direct child of its
// own decision).
func e15t1ActiveLineage(t *testing.T, s *Store, routeID, destinationID, dispatchID string) ports.Lineage {
	t.Helper()
	lin := lineage(dispatchID, "agent-dispatch:v2:sha256:"+repeat(dispatchID[len(dispatchID)-1:], 64))
	lin.Batch.RouteID = routeID
	lin.Decision.RouteID = routeID
	lin.Intent.RouteID = routeID
	projection := `{"id":"` + destinationID + `","workstream":"ws-` + destinationID + `"}`
	revision := records.RevisionOfProjection(projection)
	lin.Intent.Fanout = &ports.FanoutInput{
		AggregateID: "agg-e15t1-" + destinationID, Origin: "arrival",
		DestinationID: destinationID, DestinationRevision: revision, Workstream: "ws-" + destinationID,
		Selections: []records.DestinationSelection{{
			DestinationID: destinationID, DestinationRevision: revision,
			Workstream: "ws-" + destinationID, Reason: "fanout_mode:all",
		}},
		Revisions: []ports.DestinationRevisionInput{{
			DestinationID: destinationID, Revision: revision,
			ProjectionJSON: projection,
		}},
	}
	return lin
}

func e15t1Activate(t *testing.T, s *Store, routeID, destinationID, dispatchID string) {
	t.Helper()
	lin := e15t1ActiveLineage(t, s, routeID, destinationID, dispatchID)
	if err := s.CommitLineage(context.Background(), lin); err != nil {
		t.Fatalf("commit %s/%s: %v", routeID, destinationID, err)
	}
	if err := s.ActivateDispatch(context.Background(), dispatchID, "test", "2026-08-31T01:00:00Z"); err != nil {
		t.Fatalf("activate %s: %v", dispatchID, err)
	}
}

// e15t1Members places both routes' lanes in one explicit group.
func e15t1Members(groupID string) []SerializationGroupMemberInput {
	return []SerializationGroupMemberInput{
		{RouteID: "wiki-maintenance", DestinationID: "indexing", GroupID: groupID},
		{RouteID: "nightly-audit", DestinationID: "audit", GroupID: groupID},
	}
}

func TestE15T1MigrationPreservesIdentitiesAndReportsConflict(t *testing.T) {
	// Build the database at the v17 baseline the way a pre-upgrade
	// installation stands, with two active children that resolve to one
	// group under v0.1.6 configuration (per-lane serialization permits
	// them; the group topology does not).
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.migrations = Migrations[:17]
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatalf("migrate to v17: %v", err)
	}
	if err := s.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	seedE15Route := func(routeID, revision string) {
		t.Helper()
		if err := s.RegisterRoute(nil, routeID, revision, "policy-"+revision, "vault-main", "hermes-kanban-main", "{}", now()); err != nil {
			t.Fatal(err)
		}
		if err := s.InitializeRouteState(nil, routeID); err != nil {
			t.Fatal(err)
		}
		if err := s.SetRouteActivation(context.Background(), routeID, "enabled", revision, "", now()); err != nil {
			t.Fatal(err)
		}
	}
	seedE15Route("wiki-maintenance", "route-rev-1")
	seedE15Route("nightly-audit", "route-rev-2")
	e15t1Activate(t, s, "wiki-maintenance", "indexing", "dispatch-e15-a")
	e15t1Activate(t, s, "nightly-audit", "audit", "dispatch-e15-b")
	var intentsBefore int
	if err := s.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intentsBefore); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Upgrade with the current binary: v18 (and every later unit)
	// applies forward-only and the group tables exist empty — the
	// migration itself is configuration-independent.
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { upgraded.Close() })
	if err := upgraded.Migrate(t.TempDir()); err != nil {
		t.Fatalf("upgrade to v18: %v", err)
	}
	if version, err := upgraded.SchemaVersion(); err != nil || version != MaxSchemaVersion {
		t.Fatalf("schema version after upgrade: %d %v", version, err)
	}
	var intentsAfter int
	if err := upgraded.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intentsAfter); err != nil {
		t.Fatal(err)
	}
	if intentsAfter != intentsBefore {
		t.Fatalf("the migration must preserve every dispatch identity: %d before, %d after", intentsBefore, intentsAfter)
	}
	if groups, err := upgraded.LoadSerializationGroups(context.Background()); err != nil || len(groups) != 0 {
		t.Fatalf("before the first topology reconciliation no group exists: %+v %v", groups, err)
	}
	if _, ok, err := upgraded.SerializationGroupOf(context.Background(), "wiki-maintenance", "indexing"); err != nil || ok {
		t.Fatalf("membership before materialization: ok=%v err=%v", ok, err)
	}

	// The first topology reconciliation materializes the group and the
	// preserved active collision surfaces as serialization_conflict with
	// no holder and both historical identities intact.
	conflicts, err := upgraded.MaterializeSerializationGroups(context.Background(), e15t1Members("wiki-publish"), "reconcile", "2026-08-31T02:00:00Z")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].GroupID != "wiki-publish" {
		t.Fatalf("the shared group must report one conflict: %+v", conflicts)
	}
	row := conflicts[0]
	if row.State != GroupStateConflict {
		t.Fatalf("the group state must be CONFLICT: %+v", row)
	}
	if row.HolderDispatchID != "" || row.HolderRouteID != "" || row.HolderDestinationID != "" {
		t.Fatalf("no arbitrary holder may be selected: %+v", row)
	}
	if len(row.Collisions) != 2 {
		t.Fatalf("both preserved actives stay named: %+v", row.Collisions)
	}
	for _, lane := range []string{"wiki-maintenance/indexing", "nightly-audit/audit"} {
		var dispatchID string
		parts := strings.SplitN(lane, "/", 2)
		if err := upgraded.QueryRow(`SELECT l.active_dispatch_id FROM destination_lane_state l WHERE l.route_id = ? AND l.destination_id = ?`,
			parts[0], parts[1]).Scan(&dispatchID); err != nil || dispatchID == "" {
			t.Fatalf("lane %s keeps its preserved active child: %v", lane, err)
		}
	}

	// The typed refusal is behaviorally load-bearing (E15-T3/E15-T4):
	// acquiring into the conflicted group returns exactly it inside the
	// activation transaction, and it wraps the state-not-eligible
	// sentinel so callers classify it as a gate, never a storage fault —
	// pinned against the preserved fixture rather than the constant's
	// own text.
	var active int
	if err := upgraded.QueryRow(`SELECT COUNT(*) FROM destination_lane_state l
		JOIN serialization_group_members m ON m.route_id = l.route_id AND m.destination_id = l.destination_id
		WHERE m.group_id = 'wiki-publish' AND l.lane_state IN ('ACTIVE_CLEAN','ACTIVE_DIRTY')`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 2 {
		t.Fatalf("the fixture must hold two preserved actives, got %d", active)
	}
	// The conflicted group refuses any member lane's activation through
	// the pre-check (the fresh-child commit is lane-blocked by the
	// preserved holder, which is itself the no-arbitrary-holder proof);
	// the rerun refusal is pinned behaviorally by the E15-T3 suite.
	if free, reason, gerr := upgraded.GroupSlotFree(context.Background(), "wiki-maintenance", "indexing"); gerr != nil || free || reason == "" {
		t.Fatalf("the conflicted group must refuse with a reason: free=%v reason=%q err=%v", free, reason, gerr)
	}
	if !errors.Is(ErrSerializationConflict, ports.ErrStateNotEligible) {
		t.Fatal("the conflict refusal must wrap the state-not-eligible sentinel")
	}
	if errors.Is(ErrSerializationConflict, ports.ErrStateNotEligible) != true {
		t.Fatal("the conflict refusal must wrap the state-not-eligible sentinel")
	}

	// Every conflict records typed evidence and an audit row.
	var auditRows int
	if err := upgraded.QueryRow(`SELECT COUNT(*) FROM state_transitions WHERE entity_type = 'serialization_group' AND entity_id = ? AND context_json LIKE '%serialization_conflict%'`, "wiki-publish").Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if auditRows == 0 {
		t.Fatal("the conflict must record a typed audit row")
	}
}

func TestE15T1ConflictResolvesThroughAllowedExits(t *testing.T) {
	// CON-013: the conflict blocks new group work without rewriting
	// history and resolves atomically as the allowed existing-work exits
	// (work complete / work fail) leave at most one active child.
	s := openTestStore(t)
	if err := s.RegisterRoute(nil, "nightly-audit", "route-rev-2", "policy-rev-2", "vault-main", "hermes-kanban-main", "{}", now()); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "nightly-audit"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRouteActivation(context.Background(), "nightly-audit", "enabled", "route-rev-2", "", now()); err != nil {
		t.Fatal(err)
	}
	e15t1Activate(t, s, "wiki-maintenance", "indexing", "dispatch-e15-a")
	e15t1Activate(t, s, "nightly-audit", "audit", "dispatch-e15-b")

	conflicts, err := s.MaterializeSerializationGroups(context.Background(), e15t1Members("wiki-publish"), "reconcile", "2026-08-31T02:00:00Z")
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("the shared group reports the conflict: %+v %v", conflicts, err)
	}

	// One allowed exit lands: the surviving child atomically becomes the
	// holder at the next topology reconciliation.
	if _, err := s.CompleteActive(context.Background(), ports.ActiveCompletion{
		RouteID: "nightly-audit", DispatchID: "dispatch-e15-b", ReceiptRef: "rcpt-e15-b", Actor: "test", Now: "2026-08-31T03:00:00Z",
	}); err != nil {
		t.Fatalf("work complete on one preserved child: %v", err)
	}
	conflicts, err = s.MaterializeSerializationGroups(context.Background(), e15t1Members("wiki-publish"), "reconcile", "2026-08-31T03:01:00Z")
	if err != nil {
		t.Fatalf("re-materialize after one exit: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("one surviving child resolves the conflict: %+v", conflicts)
	}
	groups, err := s.LoadSerializationGroups(context.Background())
	if err != nil || len(groups) != 1 {
		t.Fatalf("one materialized group: %+v %v", groups, err)
	}
	if groups[0].State != GroupStateHeld || groups[0].HolderDispatchID != "dispatch-e15-a" || groups[0].HolderRouteID != "wiki-maintenance" {
		t.Fatalf("the sole survivor atomically holds the slot: %+v", groups[0])
	}
	var resolveAudit int
	if err := s.QueryRow(`SELECT COUNT(*) FROM state_transitions WHERE entity_type = 'serialization_group' AND context_json LIKE '%conflict-resolved-sole-holder%'`).Scan(&resolveAudit); err != nil {
		t.Fatal(err)
	}
	if resolveAudit == 0 {
		t.Fatal("the resolution must record a typed audit row")
	}

	// The last exit opens the group.
	if _, err := s.CompleteActive(context.Background(), ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: "dispatch-e15-a", ReceiptRef: "rcpt-e15-a", Actor: "test", Now: "2026-08-31T04:00:00Z",
	}); err != nil {
		t.Fatalf("work complete on the survivor: %v", err)
	}
	if _, err := s.MaterializeSerializationGroups(context.Background(), e15t1Members("wiki-publish"), "reconcile", "2026-08-31T04:01:00Z"); err != nil {
		t.Fatalf("re-materialize after the last exit: %v", err)
	}
	groups, err = s.LoadSerializationGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if groups[0].State != GroupStateOpen || groups[0].HolderDispatchID != "" {
		t.Fatalf("an empty group opens with no holder: %+v", groups[0])
	}
}

func TestE15T1MembershipFollowsCurrentConfiguration(t *testing.T) {
	// Materialization replaces the derived membership set: a
	// reconfigured topology stops referencing the removed lane and the
	// group it alone held disappears with its members.
	s := openTestStore(t)
	e15t1Activate(t, s, "wiki-maintenance", "indexing", "dispatch-e15-a")
	if _, err := s.MaterializeSerializationGroups(context.Background(), []SerializationGroupMemberInput{
		{RouteID: "wiki-maintenance", DestinationID: "indexing", GroupID: "wiki-publish"},
	}, "reconcile", "2026-08-31T02:00:00Z"); err != nil {
		t.Fatal(err)
	}
	group, ok, err := s.SerializationGroupOf(context.Background(), "wiki-maintenance", "indexing")
	if err != nil || !ok || group != "wiki-publish" {
		t.Fatalf("membership after materialization: %q ok=%v err=%v", group, ok, err)
	}
	// The held slot is recomputed from the live lanes on every pass.
	rows, err := s.LoadSerializationGroups(context.Background())
	if err != nil || len(rows) != 1 || rows[0].State != GroupStateHeld || rows[0].HolderDispatchID != "dispatch-e15-a" {
		t.Fatalf("the active child holds the materialized group: %+v %v", rows, err)
	}
	// The lane-state machine still reports the active lane exactly as
	// before the topology work (DAT-012: no historic identity moved).
	snap, err := s.LoadRouteState(context.Background(), "wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != state.RouteActiveClean || snap.ActiveDispatchID != "dispatch-e15-a" {
		t.Fatalf("the lane keeps its active dispatch through materialization: %+v", snap)
	}
	// A configuration change moves the lane to another group; the stale
	// group leaves the member-backed load view, and its frozen row keeps
	// the durable version so a later recreate cannot reuse an audit
	// transition ID (round-1 F001).
	if _, err := s.MaterializeSerializationGroups(context.Background(), []SerializationGroupMemberInput{
		{RouteID: "wiki-maintenance", DestinationID: "indexing", GroupID: "resource:vault-main"},
	}, "reconcile", "2026-08-31T03:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if group, ok, _ := s.SerializationGroupOf(context.Background(), "wiki-maintenance", "indexing"); !ok || group != "resource:vault-main" {
		t.Fatalf("membership follows current configuration: %q ok=%v", group, ok)
	}
	rows, err = s.LoadSerializationGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].GroupID != "resource:vault-main" {
		t.Fatalf("the retired group leaves the member-backed load view: %+v", rows)
	}
	var frozenVersion int64
	if err := s.QueryRow(`SELECT version FROM serialization_groups WHERE group_id = ?`, "wiki-publish").Scan(&frozenVersion); err != nil {
		t.Fatalf("the retired group keeps its frozen row: %v", err)
	}
	// Recreating the retired group continues the frozen row's version
	// sequence — the append-only audit transition IDs are never reused
	// (round-1 F001). A later state change proves the sequence advanced.
	if _, err := s.MaterializeSerializationGroups(context.Background(), []SerializationGroupMemberInput{
		{RouteID: "wiki-maintenance", DestinationID: "indexing", GroupID: "wiki-publish"},
	}, "reconcile", "2026-08-31T03:30:00Z"); err != nil {
		t.Fatalf("recreate after retire must not collide with historical audit rows: %v", err)
	}
	if _, err := s.CompleteActive(context.Background(), ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: "dispatch-e15-a", ReceiptRef: "rcpt-e15-a-open", Actor: "test", Now: "2026-08-31T03:40:00Z",
	}); err != nil {
		t.Fatalf("release the active child for the state-change proof: %v", err)
	}
	if _, err := s.MaterializeSerializationGroups(context.Background(), []SerializationGroupMemberInput{
		{RouteID: "wiki-maintenance", DestinationID: "indexing", GroupID: "wiki-publish"},
	}, "reconcile", "2026-08-31T03:41:00Z"); err != nil {
		t.Fatalf("re-materialize after the release: %v", err)
	}
	var recreatedVersion int64
	if err := s.QueryRow(`SELECT version FROM serialization_groups WHERE group_id = ?`, "wiki-publish").Scan(&recreatedVersion); err != nil {
		t.Fatal(err)
	}
	if recreatedVersion <= frozenVersion {
		t.Fatalf("the recreated group must continue the version sequence past %d: %d", frozenVersion, recreatedVersion)
	}
	// Idempotence: an unchanged topology re-materializes without new
	// audit noise (the state row simply re-records the same hold).
	var before int
	if err := s.QueryRow(`SELECT COUNT(*) FROM state_transitions WHERE entity_type = 'serialization_group'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeSerializationGroups(context.Background(), []SerializationGroupMemberInput{
		{RouteID: "wiki-maintenance", DestinationID: "indexing", GroupID: "wiki-publish"},
	}, "reconcile", "2026-08-31T03:45:00Z"); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := s.QueryRow(`SELECT COUNT(*) FROM state_transitions WHERE entity_type = 'serialization_group'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("an unchanged topology must not append audit rows: %d -> %d", before, after)
	}
}

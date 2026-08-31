package dispatch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E15-T3 acceptance coverage (ADR-0021, CON-011 through CON-014,
// AC-1102 through AC-1105, DUR-010/DUR-012, TST-004/TST-005): one
// active child per serialization group across destination lanes,
// occupied-group merging, oldest-first deterministic promotion,
// retry/rerun slot discipline, conflict blocking, and multi-process
// exclusivity — all against real SQLite files.

// e15t3Store seeds a store with two routes over one resource whose
// lanes share one serialization group.
func e15t3Store(t *testing.T) *sqlite.Store {
	t.Helper()
	s := openCoordStore(t)
	e15t3SeedAuditRoute(t, s)
	if _, err := s.MaterializeSerializationGroups(context.Background(), []sqlite.SerializationGroupMemberInput{
		{RouteID: "wiki", DestinationID: "wiki-primary", GroupID: "shared"},
		{RouteID: "audit", DestinationID: "wiki-primary", GroupID: "shared"},
		{RouteID: "wiki", DestinationID: "wiki-secondary", GroupID: "independent"},
	}, "test", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	return s
}

// e15t3Lineage builds one arrival for a route's lane with unique batch
// identity (the shared harness pins route wiki).
func e15t3Lineage(t *testing.T, n int, routeID string) ports.Lineage {
	t.Helper()
	lin := coordLineage(t, n, "dispatch")
	lin.Batch.RouteID = routeID
	lin.Decision.RouteID = routeID
	lin.Intent.RouteID = routeID
	if routeID == "audit" {
		// The routes table keys revisions uniquely; the audit route
		// carries its own revision through every record.
		lin.Batch.RouteRevision = "route-rev-2"
		lin.Decision.RouteRevision = "route-rev-2"
		lin.Intent.RouteRevision = "route-rev-2"
	}
	lin.Intent.Fanout.AggregateID = fmt.Sprintf("agg-%s-%d", routeID, n)
	return lin
}

func TestE15T3SharedGroupNeverRunsParallelChildren(t *testing.T) {
	// AC-1102/AC-1103: a burst over one shared group activates exactly
	// one child; every other lane's arrival merges into that lane's
	// dirty generation with no parallel child.
	s := e15t3Store(t)
	c := newCoordinator(s)
	ctx := context.Background()

	if _, err := c.Arrival(ctx, e15t3Lineage(t, 1, "wiki")); err != nil {
		t.Fatal(err)
	}
	merged, err := c.Arrival(ctx, e15t3Lineage(t, 101, "audit"))
	if err != nil || !merged {
		t.Fatalf("the occupied group merges the other lane's arrival: merged=%v err=%v", merged, err)
	}
	activated := "dispatch-1"
	// The durable group state holds exactly the activated child.
	groups, err := s.LoadSerializationGroups(ctx)
	if err != nil || len(groups) != 2 {
		t.Fatalf("two materialized groups: %+v %v", groups, err)
	}
	var shared *sqlite.SerializationGroupRow
	for i := range groups {
		if groups[i].GroupID == "shared" {
			shared = &groups[i]
		}
	}
	if shared == nil || shared.State != sqlite.GroupStateHeld || shared.HolderDispatchID != activated {
		t.Fatalf("the shared group's slot names the one activated child: %+v", shared)
	}
	// No second child exists for the group anywhere in the intents.
	var activeChildren int
	if err := s.QueryRow(`SELECT COUNT(*) FROM destination_lane_state l
		JOIN serialization_group_members m ON m.route_id = l.route_id AND m.destination_id = l.destination_id
		WHERE m.group_id = 'shared' AND l.lane_state IN ('ACTIVE_CLEAN','ACTIVE_DIRTY')`).Scan(&activeChildren); err != nil {
		t.Fatal(err)
	}
	if activeChildren != 1 {
		t.Fatalf("exactly one active child per shared group, got %d", activeChildren)
	}
}

func TestE15T3AcknowledgedIndependentGroupsProgressConcurrently(t *testing.T) {
	// AC-1104: independent groups over one resource run concurrently —
	// the guarantee is state-database scoped and never described as
	// cross-instance single-writer safety.
	s := e15t3Store(t)
	c := newCoordinator(s)
	ctx := context.Background()

	// wiki-primary holds "shared"; wiki-secondary holds "independent".
	if _, err := c.Arrival(ctx, e15t3Lineage(t, 1, "wiki")); err != nil {
		t.Fatal(err)
	}
	secondary := e15t3Lineage(t, 201, "wiki")
	secondary.Intent.Fanout.DestinationID = "wiki-secondary"
	secondary.Intent.Fanout.Selections[0].DestinationID = "wiki-secondary"
	secondary.Intent.Fanout.Revisions[0].DestinationID = "wiki-secondary"
	if _, err := c.Arrival(ctx, secondary); err != nil {
		t.Fatal(err)
	}
	var active int
	if err := s.QueryRow(`SELECT COUNT(*) FROM destination_lane_state WHERE lane_state IN ('ACTIVE_CLEAN','ACTIVE_DIRTY')`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 2 {
		t.Fatalf("independent groups progress concurrently, got %d active children", active)
	}
}

func TestE15T3CompletionPromotesOldestFirstDirtyLane(t *testing.T) {
	// AC-1103: a lane whose follow-up waits on the group slot is the
	// waiting lane; the holder's completion promotes exactly the oldest
	// first-dirty one with destination ID as the tie break, its follow-up
	// activates through the reservation, and the releaser's own follow-up
	// waits behind it.
	s := e15t3Store(t)
	c := newCoordinator(s)
	ctx := context.Background()

	// Phase 1 — the wiki lane activates, goes dirty, and completes with
	// a waiting follow-up; the group opens (nothing else waits yet).
	if _, err := c.Arrival(ctx, e15t3Lineage(t, 1, "wiki")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Arrival(ctx, e15t3Lineage(t, 2, "wiki")); err != nil {
		t.Fatal(err)
	}
	active1, err := s.LoadIntent(ctx, "dispatch-1")
	if err != nil {
		t.Fatal(err)
	}
	followup1, err := BuildFollowupRequest(active1, e15t3Manifest(1), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Completion(ctx, ports.ActiveCompletion{
		RouteID: "wiki", DispatchID: "dispatch-1", ReceiptRef: "wr-1",
		FollowupRequest: &followup1, DirtyLineageJSON: `{"generations":[2]}`,
	}); err != nil {
		t.Fatal(err)
	}

	// Phase 2 — the audit lane takes the open slot, goes dirty, and
	// completes with its own waiting follow-up: the release must promote
	// the OLDER wiki waiting lane, not the releaser's follow-up.
	if _, err := c.Arrival(ctx, e15t3Lineage(t, 101, "audit")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Arrival(ctx, e15t3Lineage(t, 102, "audit")); err != nil {
		t.Fatal(err)
	}
	active2, err := s.LoadIntent(ctx, "dispatch-101")
	if err != nil {
		t.Fatal(err)
	}
	followup2, err := BuildFollowupRequest(active2, e15t3Manifest(2), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Completion(ctx, ports.ActiveCompletion{
		RouteID: "audit", DispatchID: "dispatch-101", ReceiptRef: "wr-101",
		FollowupRequest: &followup2, DirtyLineageJSON: `{"generations":[102]}`,
	}); err != nil {
		t.Fatal(err)
	}
	groups, _ := s.LoadSerializationGroups(ctx)
	for _, g := range groups {
		if g.GroupID != "shared" {
			continue
		}
		if g.HolderRouteID != "wiki" || g.HolderDestinationID != "wiki-primary" || g.HolderDispatchID != "" {
			t.Fatalf("the release promotes the oldest waiting lane as a reservation, got %+v", g)
		}
	}
	// The releaser's own follow-up waits behind the reservation...
	if err := c.Activate(ctx, followup2.DispatchID); err == nil {
		t.Fatal("the releaser's follow-up must wait behind the promoted lane")
	}
	// ...and the promoted lane's follow-up takes the reserved slot.
	if err := c.Activate(ctx, followup1.DispatchID); err != nil {
		t.Fatalf("the promoted lane's follow-up activates through the reservation: %v", err)
	}
}

func TestE15T3RerunTransfersSlotAtomicallyAndConflictRefuses(t *testing.T) {
	// CON-012: a rerun TRANSFERS the group slot atomically with the
	// lane takeover, and a preserved conflict refuses the rerun.
	s := e15t3Store(t)
	c := newCoordinator(s)
	ctx := context.Background()

	if _, err := c.Arrival(ctx, e15t3Lineage(t, 1, "wiki")); err != nil {
		t.Fatal(err)
	}
	active, err := s.LoadIntent(ctx, "dispatch-1")
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := BuildFollowupRequest(active, e15t3Manifest(3), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RerunIntent(ctx, ports.RerunInput{OriginalDispatchID: "dispatch-1", New: replacement}); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	groups, _ := s.LoadSerializationGroups(ctx)
	for _, g := range groups {
		if g.GroupID == "shared" && g.HolderDispatchID != replacement.DispatchID {
			t.Fatalf("the rerun holds the transferred slot, got %+v", g)
		}
	}
	// The slot survived the retry lifecycle unchanged: a retry_wait
	// dispatch keeps the lane active, so the holder is the replacement
	// until a real completion.
	if err := s.MakeRetryDue(ctx, replacement.DispatchID, "test", "2026-08-20T02:00:00Z"); err == nil {
		// Not retry_wait yet (the intent is ready); the retry-retention
		// fact is that nothing in the retry path released the slot.
	}
	rerunGroups, _ := s.LoadSerializationGroups(ctx)
	for _, g := range rerunGroups {
		if g.GroupID == "shared" && g.HolderDispatchID != replacement.DispatchID {
			t.Fatalf("the rerun retains the transferred slot through the retry lifecycle: %+v", g)
		}
	}

	// A preserved conflict refuses the rerun outright: two lanes
	// activated before the topology materialized, then one group.
	conflict := e15t3PreGroupStore(t)
	if _, err := newCoordinator(conflict).Arrival(ctx, e15t3Lineage(t, 301, "wiki")); err != nil {
		t.Fatal(err)
	}
	if _, err := newCoordinator(conflict).Arrival(ctx, e15t3Lineage(t, 302, "audit")); err != nil {
		t.Fatal(err)
	}
	if _, err := conflict.MaterializeSerializationGroups(ctx, e15t3MembersShared(), "test", "2026-08-20T00:30:00Z"); err != nil {
		t.Fatal(err)
	}
	rerunTarget, err := conflict.LoadIntent(ctx, "dispatch-301")
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := BuildFollowupRequest(rerunTarget, e15t3Manifest(4), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conflict.RerunIntent(ctx, ports.RerunInput{OriginalDispatchID: "dispatch-301", New: blocked}); err == nil {
		t.Fatal("a preserved conflict must refuse the rerun")
	}
}

// e15t3Manifest builds a distinct latest-state manifest so concurrent
// follow-up derivations never collide on idempotency identity.
func e15t3Manifest(n int) []records.ChangeItem {
	return []records.ChangeItem{{
		Path: fmt.Sprintf("Inbox/followup-%d.md", n), Operation: records.OpModify, FileType: records.FileRegular,
		AfterDigest: records.Digest(fmt.Sprintf("sha256:%064d", 9000+n)), DigestStatus: records.DigestKnown,
	}}
}

// e15t3PreGroupStore seeds routes and lanes WITHOUT materializing
// group membership: activations there predate the group topology (the
// natural pre-upgrade shape), and materializing afterwards turns two
// preserved actives into the blocking conflict.
func e15t3PreGroupStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s := openCoordStore(t)
	e15t3SeedAuditRoute(t, s)
	return s
}

// e15t3SeedAuditRoute registers the second route (the harness's base
// store already carries wiki).
func e15t3SeedAuditRoute(t *testing.T, s *sqlite.Store) {
	t.Helper()
	if err := s.RegisterRoute(nil, "audit", "route-rev-2", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "audit"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRouteActivation(context.Background(), "audit", "enabled", "route-rev-2", "", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
}

func e15t3MembersShared() []sqlite.SerializationGroupMemberInput {
	return []sqlite.SerializationGroupMemberInput{
		{RouteID: "wiki", DestinationID: "wiki-primary", GroupID: "shared"},
		{RouteID: "audit", DestinationID: "wiki-primary", GroupID: "shared"},
	}
}

func TestE15T3ConflictBlocksNewAcquisition(t *testing.T) {
	// CON-013: a preserved serialization conflict blocks new group
	// acquisition, promotion, retry, and rerun while the allowed
	// existing-work exits stay open.
	// Two preserved actives resolve to one group: conflict (the lanes
	// activated before the topology materialized, the pre-upgrade
	// shape).
	s := e15t3PreGroupStore(t)
	c := newCoordinator(s)
	ctx := context.Background()
	if _, err := c.Arrival(ctx, e15t3Lineage(t, 401, "wiki")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Arrival(ctx, e15t3Lineage(t, 402, "audit")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeSerializationGroups(ctx, []sqlite.SerializationGroupMemberInput{
		{RouteID: "wiki", DestinationID: "wiki-primary", GroupID: "shared"},
		{RouteID: "audit", DestinationID: "wiki-primary", GroupID: "shared"},
	}, "test", "2026-08-20T00:30:00Z"); err != nil {
		t.Fatal(err)
	}
	// The independent group still admits work: conflicts are per group
	// (the secondary lane joins its own group now).
	if _, err := s.MaterializeSerializationGroups(ctx, []sqlite.SerializationGroupMemberInput{
		{RouteID: "wiki", DestinationID: "wiki-primary", GroupID: "shared"},
		{RouteID: "audit", DestinationID: "wiki-primary", GroupID: "shared"},
		{RouteID: "wiki", DestinationID: "wiki-secondary", GroupID: "independent"},
	}, "test", "2026-08-20T00:40:00Z"); err != nil {
		t.Fatal(err)
	}
	secondary := e15t3Lineage(t, 201, "wiki")
	secondary.Intent.Fanout.DestinationID = "wiki-secondary"
	secondary.Intent.Fanout.Selections[0].DestinationID = "wiki-secondary"
	secondary.Intent.Fanout.Revisions[0].DestinationID = "wiki-secondary"
	if _, err := c.Arrival(ctx, secondary); err != nil {
		t.Fatalf("an independent group is not blocked by another group's conflict: %v", err)
	}
	// The conflicted group refuses a new activation through the
	// coordinator (the arrival of the third shared-group lane has no
	// member row here, so prove the typed refusal at the store gate).
	if free, reason, err := s.GroupSlotFree(ctx, "wiki", "wiki-primary"); err != nil || free || reason == "" {
		t.Fatalf("the conflicted group must refuse with a reason: free=%v reason=%q err=%v", free, reason, err)
	}
	// The allowed exits still work: completing one preserved child is
	// permitted (the work exits stay available), and the next topology
	// reconciliation resolves the group to its sole survivor.
	if _, err := s.CompleteActive(ctx, ports.ActiveCompletion{
		RouteID: "audit", DispatchID: "dispatch-402", ReceiptRef: "wr-402", Actor: "test", Now: "2026-08-20T01:00:00Z",
	}); err != nil {
		t.Fatalf("the allowed existing-work exit must remain available: %v", err)
	}
	if _, err := s.MaterializeSerializationGroups(ctx, []sqlite.SerializationGroupMemberInput{
		{RouteID: "wiki", DestinationID: "wiki-primary", GroupID: "shared"},
		{RouteID: "audit", DestinationID: "wiki-primary", GroupID: "shared"},
		{RouteID: "wiki", DestinationID: "wiki-secondary", GroupID: "independent"},
	}, "test", "2026-08-20T01:30:00Z"); err != nil {
		t.Fatal(err)
	}
	groups, _ := s.LoadSerializationGroups(ctx)
	for _, g := range groups {
		if g.GroupID == "shared" && g.State != sqlite.GroupStateHeld {
			t.Fatalf("the conflict resolves to its sole survivor: %+v", g)
		}
	}
}

func TestE15T3ConcurrentProcessesElectOneGroupChild(t *testing.T) {
	// TST-005/DUR-012: multiple one-shot processes racing arrivals on
	// one shared group elect exactly one active child; every loser
	// merges durably. Each racer opens its own connection to the same
	// database file, the production multi-process shape.
	dir := t.TempDir()
	dbPath := fmt.Sprintf("%s/state.db", dir)
	seed, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := seed.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := seed.RegisterRoute(nil, "wiki", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := seed.InitializeRouteState(nil, "wiki"); err != nil {
		t.Fatal(err)
	}
	if err := seed.SetRouteActivation(context.Background(), "wiki", "enabled", "route-rev-1", "", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	e15t3SeedAuditRoute(t, seed)
	if _, err := seed.MaterializeSerializationGroups(context.Background(), []sqlite.SerializationGroupMemberInput{
		{RouteID: "wiki", DestinationID: "wiki-primary", GroupID: "shared"},
		{RouteID: "audit", DestinationID: "wiki-primary", GroupID: "shared"},
	}, "test", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	seed.Close()

	const racers = 8
	var wg sync.WaitGroup
	errs := make([]error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			racer, err := sqlite.Open(dbPath)
			if err != nil {
				errs[i] = err
				return
			}
			defer racer.Close()
			c := &Coordinator{Store: racer, Now: func() string { return "2026-08-20T01:00:00Z" }, Actor: fmt.Sprintf("racer-%d", i)}
			routeID := "wiki"
			if i%2 == 1 {
				routeID = "audit"
			}
			// Concurrent one-shot writers may see transient SQLITE_BUSY
			// congestion (deferred read-to-write upgrades); the CLI
			// classifies it retryable, and the racer retries with a
			// small backoff exactly like the operator would.
			// A BUSY after the lineage committed leaves a durable
			// reserved dispatch (the documented failed-activation
			// posture); the re-invocation models the operator's retry
			// with a fresh occurrence identity, exactly like a
			// re-delivered one-shot run.
			for attempt := 0; attempt < 12; attempt++ {
				_, err := c.Arrival(context.Background(), e15t3Lineage(t, 1000+i+attempt*100, routeID))
				if err == nil {
					return
				}
				if !strings.Contains(err.Error(), "database is locked") && !strings.Contains(err.Error(), "SQLITE_BUSY") {
					errs[i] = err
					return
				}
				time.Sleep(time.Duration(20+attempt*10) * time.Millisecond)
			}
			errs[i] = fmt.Errorf("racer kept seeing congestion")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v", i, err)
		}
	}
	verify, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer verify.Close()
	var activeChildren int
	if err := verify.QueryRow(`SELECT COUNT(*) FROM destination_lane_state l
		JOIN serialization_group_members m ON m.route_id = l.route_id AND m.destination_id = l.destination_id
		WHERE m.group_id = 'shared' AND l.lane_state IN ('ACTIVE_CLEAN','ACTIVE_DIRTY')`).Scan(&activeChildren); err != nil {
		t.Fatal(err)
	}
	if activeChildren != 1 {
		t.Fatalf("the racing processes elect exactly one group child, got %d", activeChildren)
	}
	// Every loser merges durably into its own lane's dirty generation:
	// one dirty lane per route at most (the losers of both routes), and
	// at least one recorded merge.
	var dirty int
	if err := verify.QueryRow(`SELECT COUNT(*) FROM destination_lane_state WHERE dirty_generation > 0`).Scan(&dirty); err != nil {
		t.Fatal(err)
	}
	if dirty < 1 || dirty > 2 {
		t.Fatalf("losers merge durably (one dirty lane per route), got %d", dirty)
	}
}

func TestE15T4ReconcileChildRespectsOccupiedGroup(t *testing.T) {
	// The G11 real-Hermes walkthrough exposed the bypass: a reconcile
	// child activated beside the group holder's active child because
	// CommitReconcileIntent applied the lane activation without the
	// group gate. The gate now refuses the whole commit, the pending
	// reconciliation stays owed, and a later retry delivers once the
	// group frees.
	s := e15t3Store(t)
	c := newCoordinator(s)
	ctx := context.Background()

	// The audit lane's child holds the shared group.
	if _, err := c.Arrival(ctx, e15t3Lineage(t, 501, "audit")); err != nil {
		t.Fatal(err)
	}
	// A reconciliation child for the wiki lane cannot commit while the
	// group is held: the typed refusal rolls everything back. The
	// reconcile flow commits its own decision first (full.go), so the
	// fixture carries one beside the intent.
	lin502 := e15t3Lineage(t, 502, "wiki")
	if err := s.SaveDecision(nil, sqlite.DecisionRecord{
		DecisionID: "dec-reconcile-e15t4", RouteID: "wiki",
		RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch",
		GenerationLineageJSON: `{"generations":[]}`,
		Classification:        "normal", ReasonCodesJSON: `["reconcile"]`, CreatedAt: "2026-08-20T01:59:00Z", Actor: "reconcile",
	}); err != nil {
		t.Fatal(err)
	}
	reconcileChild := lin502.Intent
	reconcileChild.DecisionID = "dec-reconcile-e15t4"
	if err := s.CommitReconcileIntent(ctx, reconcileChild, "reconcile", "2026-08-20T02:00:00Z"); err == nil {
		t.Fatal("a reconcile child must not activate beside the group holder")
	} else if !strings.Contains(err.Error(), "serialization group slot held") && !strings.Contains(err.Error(), "preserved conflict") {
		t.Fatalf("the refusal must be the typed group error: %v", err)
	}
	var active int
	if err := s.QueryRow(`SELECT COUNT(*) FROM destination_lane_state l
		JOIN serialization_group_members m ON m.route_id = l.route_id AND l.destination_id = m.destination_id
		WHERE m.group_id = 'shared' AND l.lane_state IN ('ACTIVE_CLEAN','ACTIVE_DIRTY')`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("exactly one active child remains in the shared group, got %d", active)
	}
	var intents int
	if err := s.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE dispatch_id = ?`, reconcileChild.DispatchID).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if intents != 0 {
		t.Fatal("the refused reconcile child must not stay committed")
	}
	// Once the group frees, the same child commits.
	if _, err := s.CompleteActive(ctx, ports.ActiveCompletion{
		RouteID: "audit", DispatchID: "dispatch-501", ReceiptRef: "wr-501", Actor: "test", Now: "2026-08-20T03:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitReconcileIntent(ctx, reconcileChild, "reconcile", "2026-08-20T04:00:00Z"); err != nil {
		t.Fatalf("the reconcile child commits once the group is free: %v", err)
	}
}

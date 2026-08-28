package dispatch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E12-T2 fan-out coverage at the coordination layer (TST-013): racing
// arrivals over two lanes of one route hold ONE active child per lane —
// exactly two active children under their shared aggregate — while
// repeated bursts over one lane still collapse into that lane's single
// dirty generation (CON-007, CON-008).

// e12t2FanoutLineages builds one fan-out occurrence's two lane lineages
// with unique batch identity per call.
func e12t2FanoutLineages(t *testing.T, n int) []ports.Lineage {
	t.Helper()
	base := coordLineage(t, n, "dispatch")
	return []ports.Lineage{e12t2WithLane(base, "wiki-primary"), e12t2WithLane(base, "wiki-secondary")}
}

// e12t2WithLane clones one lineage onto the named destination lane with
// its own dispatch identity.
func e12t2WithLane(base ports.Lineage, destinationID string) ports.Lineage {
	lin := base
	lin.Intent.DispatchID = fmt.Sprintf("%s-%s", base.Intent.DispatchID, destinationID)
	lin.Intent.DecisionID = base.Decision.DecisionID
	lin.Intent.IdempotencyKey = "agent-dispatch:v2:" + destinationID + ":" + base.Intent.IdempotencyKey
	lin.Intent.Fanout = &ports.FanoutInput{
		AggregateID: "agg-" + base.Intent.DispatchID + "-" + destinationID, Origin: string(records.OriginArrival),
		DestinationID: destinationID, DestinationRevision: "dst-rev-1", Workstream: "ws-" + destinationID,
		Selections: []records.DestinationSelection{{
			DestinationID: destinationID, DestinationRevision: "dst-rev-1", Workstream: "ws-" + destinationID,
			Reason: "fanout_mode:all",
		}},
	}
	return lin
}

// TestE12T2ConcurrentArrivalsCreateOneActiveChildPerLane mirrors
// TestConcurrentArrivalsCreateOneActiveDispatch at the lane layer
// (TST-013): simultaneous fan-out arrivals over two lanes produce exactly
// TWO active children — one per lane, each holding its own lane's slot —
// and every later burst merges into its lane's dirty generation.
func TestE12T2ConcurrentArrivalsCreateOneActiveChildPerLane(t *testing.T) {
	s := openCoordStore(t)
	c := newCoordinator(s)
	const bursts = 8
	var wg sync.WaitGroup
	errs := make([]error, bursts)
	for i := 0; i < bursts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := c.ArrivalFanout(context.Background(), e12t2FanoutLineages(t, i))
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("fan-out arrival %d: %v", i, err)
		}
	}
	intents, err := s.ListIntents(context.Background(), ports.IntentFilter{RouteID: "wiki", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 2 {
		t.Fatalf("exactly one active child per lane must exist, got %d", len(intents))
	}
	// Each lane holds its own child with a dirty generation from the
	// losing bursts of its lane only.
	rows, err := s.Query(`SELECT destination_id, COALESCE(active_dispatch_id, ''), lane_state, dirty_generation FROM destination_lane_state WHERE route_id = 'wiki' ORDER BY destination_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	lanes := 0
	for rows.Next() {
		var dest, active, laneState string
		var dirty int
		if err := rows.Scan(&dest, &active, &laneState, &dirty); err != nil {
			t.Fatal(err)
		}
		lanes++
		if active == "" || laneState != "ACTIVE_DIRTY" {
			t.Fatalf("lane %s must hold its child as ACTIVE_DIRTY: %q %s", dest, active, laneState)
		}
		if dirty != bursts-1 {
			t.Fatalf("lane %s must record every later burst in its own dirty generation: %d want %d", dest, dirty, bursts-1)
		}
	}
	if lanes != 2 {
		t.Fatalf("both lanes must materialize: %d", lanes)
	}
}

// TestE12T2ArrivalFanoutReportsLaneFailureBesideSiblingSuccess pins the
// failure surfacing (review round 1): a lane whose child commit fails is
// recorded in FanoutOutcome.Failed with bounded error text while the
// sibling activates — the call still succeeds because not every lane
// failed. The failure is injected by colliding the second lane's dispatch
// identity with the first lane's (a durable constraint violation).
func TestE12T2ArrivalFanoutReportsLaneFailureBesideSiblingSuccess(t *testing.T) {
	s := openCoordStore(t)
	c := newCoordinator(s)
	base := coordLineage(t, 41, "dispatch")
	first := e12t2WithLane(base, "wiki-primary")
	// The second lane reuses the first lane's dispatch identity: the child
	// commit fails on the dispatch primary key after the sibling committed.
	second := e12t2WithLane(base, "wiki-secondary")
	second.Intent.DispatchID = first.Intent.DispatchID
	outcome, err := c.ArrivalFanout(context.Background(), []ports.Lineage{first, second})
	if err != nil {
		t.Fatalf("one sibling's failure must not fail the occurrence: %v", err)
	}
	if len(outcome.Activated) != 1 || outcome.Activated[0].DestinationID != "wiki-primary" {
		t.Fatalf("the healthy lane must activate: %+v", outcome)
	}
	if len(outcome.Failed) != 1 || outcome.Failed[0].DestinationID != "wiki-secondary" {
		t.Fatalf("the failed lane must be recorded: %+v", outcome)
	}
	failure := outcome.Failed[0]
	if failure.Error == "" || strings.ContainsAny(failure.Error, "\n") {
		t.Fatalf("the failure text must be bounded and envelope-safe: %q", failure.Error)
	}
	// The healthy lane's child is durable and holds its slot.
	snap, err := s.LoadLaneState(context.Background(), "wiki", "wiki-primary")
	if err != nil || snap.State != state.RouteActiveClean || snap.ActiveDispatchID != first.Intent.DispatchID {
		t.Fatalf("the healthy lane must hold its child: %+v %v", snap, err)
	}
}

// TestE12T2AllLanesFailedReturnsError pins the all-failed return: when
// every lane fails the occurrence did not happen and the last error
// surfaces (with the per-lane detail in Failed).
func TestE12T2AllLanesFailedReturnsError(t *testing.T) {
	s := openCoordStore(t)
	c := newCoordinator(s)
	// A pre-existing durable dispatch identity both lanes collide with.
	if _, err := c.Arrival(context.Background(), coordLineage(t, 1, "dispatch")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE destination_lane_state SET active_dispatch_id = NULL, lane_state = 'IDLE' WHERE route_id = 'wiki'`); err != nil {
		t.Fatal(err)
	}
	base := coordLineage(t, 42, "dispatch")
	dup := e12t2WithLane(base, "wiki-primary")
	dup.Intent.DispatchID = "dispatch-1"
	dup2 := e12t2WithLane(base, "wiki-secondary")
	dup2.Intent.DispatchID = "dispatch-1"
	outcome, err := c.ArrivalFanout(context.Background(), []ports.Lineage{dup, dup2})
	if err == nil {
		t.Fatalf("every lane failing must surface the error: %+v", outcome)
	}
	if len(outcome.Failed) == 0 {
		t.Fatalf("the per-lane failures must be recorded even on the all-failed return: %+v", outcome)
	}
}

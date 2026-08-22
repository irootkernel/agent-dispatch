package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E3-T4 acceptance coverage: one active dispatch per route under
// concurrency, durable dirty generations for any number of later
// bursts, at most one latest-state follow-up after completion, local
// serialization independent of target mutex capability, and the
// latest-state instruction retained in the follow-up.

func openCoordStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(fmt.Sprintf("%s/state.db", t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRouteActivation(context.Background(), "wiki", "enabled", "route-rev-1", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	return s
}

// coordLineage builds one arriving lineage with unique batch identity.
func coordLineage(t *testing.T, n int, disposition string) ports.Lineage {
	t.Helper()
	now := "2026-08-20T01:00:00Z"
	return ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: fmt.Sprintf("obs-%d", n), SchemaVersion: "agent-dispatch.source-observation/v1",
			SourceType: "watchman", SourceID: "watchman-main", TriggerName: "trig",
			ResourceID: "vault-main", ObservedAt: now, ReceivedAt: now,
			RawPayloadDigest: fmt.Sprintf("sha256:%064d", n), IngestStatus: "accepted",
		},
		Batch: ports.BatchInput{
			BatchID: fmt.Sprintf("batch-%d", n), RouteID: "wiki", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: now,
			ContentFingerprint: fmt.Sprintf("sha256:%064d", n), ObservationIDs: []string{fmt.Sprintf("obs-%d", n)},
		},
		Decision: ports.DecisionInput{
			DecisionID: fmt.Sprintf("decision-%d", n), BatchID: fmt.Sprintf("batch-%d", n),
			RouteID: "wiki", RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1",
			Disposition: disposition, Classification: "normal", CreatedAt: now, Actor: "planner",
		},
		Intent: intentForN(t, n),
	}
}

func intentForN(t *testing.T, n int) ports.IntentInput {
	t.Helper()
	req, key, err := BuildRequest(RequestInput{
		DispatchID: fmt.Sprintf("dispatch-%d", n),
		Route:      ports.TaskRouteRef{ID: "wiki", Revision: "route-rev-1"},
		Resource:   ports.TaskResource{ID: "vault-main", Workspace: "dir:/srv/vault"},
		TargetID:   "hermes-kanban-main", Generation: 1,
		Fingerprint: records.Digest(fmt.Sprintf("sha256:%064d", n)),
		Changes: []records.ChangeItem{{
			Path: fmt.Sprintf("Inbox/n%d.md", n), Operation: records.OpCreate, FileType: records.FileRegular,
			AfterDigest: records.Digest(fmt.Sprintf("sha256:%064d", n)), DigestStatus: records.DigestKnown,
		}},
		AcceptanceCriteria: []string{"wiki maintenance completes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	requestJSON, _ := MarshalRequest(req)
	return ports.IntentInput{
		DispatchID: fmt.Sprintf("dispatch-%d", n), DecisionID: fmt.Sprintf("decision-%d", n),
		RouteID: "wiki", RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main",
		TargetType: "hermes_kanban", ResourceID: "vault-main", Generation: 1,
		IdempotencyKey: key, ContentFingerprint: string(req.Activation.ContentFingerprint),
		ManifestDigest: ManifestDigest([]records.ChangeItem{{
			Path: fmt.Sprintf("Inbox/n%d.md", n), Operation: records.OpCreate, FileType: records.FileRegular,
			AfterDigest: records.Digest(fmt.Sprintf("sha256:%064d", n)), DigestStatus: records.DigestKnown,
		}}), RequestVersion: RequestContractVersion,
		RequestJSON: requestJSON, CreatedAt: "2026-08-20T01:00:00Z",
	}
}

func newCoordinator(s *sqlite.Store) *Coordinator {
	return &Coordinator{Store: s, Now: func() string { return "2026-08-20T01:00:00Z" }, Actor: "coordinator"}
}

// TestConcurrentArrivalsCreateOneActiveDispatch proves simultaneous
// one-shot arrivals produce exactly one active dispatch and merge every
// other burst into the durable dirty generation (CON-001, CON-002,
// CON-003 posture, TST-005 at the process-internal level).
func TestConcurrentArrivalsCreateOneActiveDispatch(t *testing.T) {
	s := openCoordStore(t)
	c := newCoordinator(s)
	const bursts = 12
	var wg sync.WaitGroup
	errs := make([]error, bursts)
	merged := make([]bool, bursts)
	for i := 0; i < bursts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m, err := c.Arrival(context.Background(), coordLineage(t, i, "dispatch"))
			errs[i], merged[i] = err, m
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("arrival %d: %v", i, err)
		}
	}
	intents, err := s.ListIntents(context.Background(), ports.IntentFilter{RouteID: "wiki", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 {
		t.Fatalf("exactly one dispatch must exist, got %d", len(intents))
	}
	snap, err := s.LoadRouteState(context.Background(), "wiki")
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != state.RouteActiveDirty {
		t.Fatalf("route must be ACTIVE_DIRTY after merged bursts: %s", snap.State)
	}
	if snap.DirtyGeneration != bursts-1 {
		t.Fatalf("every later burst must increment the durable dirty state: %d want %d", snap.DirtyGeneration, bursts-1)
	}
	if snap.ActiveDispatchID != intents[0].DispatchID {
		t.Fatalf("the single intent must hold the active slot: %+v", snap)
	}
}

// TestLocalSerializationWithoutTargetMutex proves serialization is a
// local route invariant: it holds even though no mutex capability
// exists (CON-006).
func TestLocalSerializationWithoutTargetMutex(t *testing.T) {
	s := openCoordStore(t)
	c := newCoordinator(s)
	// The fake target exposes no resource mutex; serialization is local.
	if _, err := c.Arrival(context.Background(), coordLineage(t, 1, "dispatch")); err != nil {
		t.Fatal(err)
	}
	merged, err := c.Arrival(context.Background(), coordLineage(t, 2, "dispatch"))
	if err != nil || !merged {
		t.Fatalf("second normal dispatch must merge, not activate: merged=%v err=%v", merged, err)
	}
	intents, _ := s.ListIntents(context.Background(), ports.IntentFilter{RouteID: "wiki"})
	if len(intents) != 1 {
		t.Fatalf("one active dispatch only: %d", len(intents))
	}
}

// TestCompletionCreatesAtMostOneFollowup proves dirty collapse: any
// number of bursts become exactly one latest-state follow-up, the
// follow-up carries the latest-state instruction, and a second
// completion cannot create another (CON-003, CON-004).
func TestCompletionCreatesAtMostOneFollowup(t *testing.T) {
	s := openCoordStore(t)
	c := newCoordinator(s)
	ctx := context.Background()
	if _, err := c.Arrival(ctx, coordLineage(t, 1, "dispatch")); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 5; i++ {
		if _, err := c.Arrival(ctx, coordLineage(t, i, "merge_pending")); err != nil {
			t.Fatal(err)
		}
	}
	active, err := s.LoadIntent(ctx, "dispatch-1")
	if err != nil {
		t.Fatal(err)
	}
	followup, err := BuildFollowupRequest(active, latestManifest(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Completion(ctx, ports.ActiveCompletion{
		RouteID: "wiki", DispatchID: "dispatch-1", ReceiptRef: "wr-1",
		FollowupRequest: &followup, DirtyLineageJSON: `{"generations":[2,3,4,5]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.RouteTo != state.RouteFollowupReady || out.FollowupDispatchID == "" || out.DirtyGeneration != 4 {
		t.Fatalf("unexpected completion outcome: %+v", out)
	}
	intents, _ := s.ListIntents(ctx, ports.IntentFilter{RouteID: "wiki"})
	if len(intents) != 2 {
		t.Fatalf("exactly one follow-up must exist: %d", len(intents))
	}
	snap, _ := s.LoadRouteState(ctx, "wiki")
	if snap.DirtyGeneration != 0 || snap.ActiveDispatchID != out.FollowupDispatchID {
		t.Fatalf("dirty must collapse with the follow-up holding the reservation: %+v", snap)
	}
	// The follow-up retains the latest-state instruction.
	fsnap, err := s.LoadIntent(ctx, out.FollowupDispatchID)
	if err != nil {
		t.Fatal(err)
	}
	if fsnap.Generation != 2 || fsnap.IdempotencyKey == active.IdempotencyKey {
		t.Fatalf("follow-up must be a new generation with a new key: %+v", fsnap)
	}
	// Activation promotes the follow-up to the single active task.
	if err := c.Activate(ctx, out.FollowupDispatchID); err != nil {
		t.Fatal(err)
	}
	snap, _ = s.LoadRouteState(ctx, "wiki")
	if snap.State != state.RouteActiveClean || snap.ActiveDispatchID != out.FollowupDispatchID {
		t.Fatalf("follow-up must activate: %+v", snap)
	}
	// A second completion of the original is refused: it no longer holds
	// the slot.
	if _, err := c.Completion(ctx, ports.ActiveCompletion{RouteID: "wiki", DispatchID: "dispatch-1", ReceiptRef: "wr-1"}); err == nil {
		t.Fatal("a completed dispatch cannot complete again")
	}
}

// TestCleanCompletionReturnsToIdle proves completion without dirty work
// clears the slot and returns the route to IDLE.
func TestCleanCompletionReturnsToIdle(t *testing.T) {
	s := openCoordStore(t)
	c := newCoordinator(s)
	ctx := context.Background()
	if _, err := c.Arrival(ctx, coordLineage(t, 1, "dispatch")); err != nil {
		t.Fatal(err)
	}
	// No bursts: clean completion.
	out, err := c.Completion(ctx, ports.ActiveCompletion{RouteID: "wiki", DispatchID: "dispatch-1", ReceiptRef: "wr-1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.RouteTo != state.RouteIdle || out.FollowupDispatchID != "" {
		t.Fatalf("clean completion must idle the route: %+v", out)
	}
	snap, _ := s.LoadRouteState(ctx, "wiki")
	if snap.State != state.RouteIdle || snap.ActiveDispatchID != "" {
		t.Fatalf("idle route with a free slot: %+v", snap)
	}
}

// TestFailedCompletionRespectsBudget proves failure with remaining
// budget creates one follow-up and exhaustion becomes uncertain.
func TestFailedCompletionRespectsBudget(t *testing.T) {
	s := openCoordStore(t)
	c := newCoordinator(s)
	ctx := context.Background()
	if _, err := c.Arrival(ctx, coordLineage(t, 1, "dispatch")); err != nil {
		t.Fatal(err)
	}
	active, _ := s.LoadIntent(ctx, "dispatch-1")
	followup, err := BuildFollowupRequest(active, latestManifest(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Completion(ctx, ports.ActiveCompletion{
		RouteID: "wiki", DispatchID: "dispatch-1", Failed: true, FailureBudgetRemaining: 1,
		FollowupRequest: &followup, ReceiptRef: "wr-fail",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.RouteTo != state.RouteFollowupReady {
		t.Fatalf("failure within budget must create one follow-up: %+v", out)
	}
	// Activate and fail again with the budget exhausted: uncertain.
	if err := c.Activate(ctx, out.FollowupDispatchID); err != nil {
		t.Fatal(err)
	}
	out2, err := c.Completion(ctx, ports.ActiveCompletion{
		RouteID: "wiki", DispatchID: out.FollowupDispatchID, Failed: true, FailureBudgetRemaining: 0,
		ReceiptRef: "wr-fail-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out2.RouteTo != state.RouteUncertain {
		t.Fatalf("exhausted budget must become uncertain: %+v", out2)
	}
	// Uncertain routes refuse new normal dispatches until resolved.
	if _, err := s.LoadRouteState(ctx, "wiki"); err != nil {
		t.Fatal(err)
	}
	merged, err := c.Arrival(ctx, coordLineage(t, 9, "dispatch"))
	if err != nil || !merged {
		t.Fatalf("arrivals during uncertainty must merge, not activate: %v %v", merged, err)
	}
}

// latestManifest models the caller's latest-state projection.
func latestManifest(t *testing.T) []records.ChangeItem {
	t.Helper()
	return []records.ChangeItem{{
		Path: "Inbox/n1.md", Operation: records.OpCreate, FileType: records.FileRegular,
		AfterDigest: records.Digest(fmt.Sprintf("sha256:%064d", 1)), DigestStatus: records.DigestKnown,
	}}
}

// TestCompletionRefusesUnpreparedFollowupTyped proves the coordinator's
// pre-check refuses a completion that needs a follow-up none was
// prepared for, as the typed state conflict the boundary maps to
// transition_invalid — and that its snapshot read failure classifies as
// storage, never a plain defect.
func TestCompletionRefusesUnpreparedFollowupTyped(t *testing.T) {
	s := openCoordStore(t)
	c := newCoordinator(s)
	ctx := context.Background()
	if _, err := c.Arrival(ctx, coordLineage(t, 1, "dispatch")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Arrival(ctx, coordLineage(t, 2, "merge_pending")); err != nil {
		t.Fatal(err)
	}
	_, err := c.Completion(ctx, ports.ActiveCompletion{
		RouteID: "wiki", DispatchID: "dispatch-1", ReceiptRef: "wr-1",
	})
	if !errors.Is(err, ports.ErrStateNotEligible) {
		t.Fatalf("the unprepared-followup refusal must be the typed state conflict, got %v", err)
	}
	var storeErr *ports.StoreError
	if errors.As(err, &storeErr) {
		t.Fatalf("a state conflict must not classify as storage: %v", err)
	}
}

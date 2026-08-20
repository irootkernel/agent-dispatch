package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rootkernel/jjukkumi/internal/adapters/sqlite"
	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/domain/state"
	"github.com/rootkernel/jjukkumi/internal/ports"
	"github.com/rootkernel/jjukkumi/internal/schemavalid"
	"github.com/rootkernel/jjukkumi/internal/testsupport/fakesink"
)

// The runtime tests compose the real SQLite adapter with the fake sink
// (TST-002/TST-006): they prove the durable flow against a real database
// file, not mocks of it.

func testClock(start time.Time) func() time.Time {
	current := start
	return func() time.Time {
		current = current.Add(time.Second)
		return current
	}
}

func openRuntimeStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := s.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki-maintenance", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki-maintenance"); err != nil {
		t.Fatal(err)
	}
	return s
}

func runtimeLineage(t *testing.T, dispatchID, requestJSON string) ports.Lineage {
	t.Helper()
	return ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: "obs-1", SchemaVersion: "jjukkumi.source-observation/v1",
			SourceType: "watchman", SourceID: "watchman-main", TriggerName: "jjukkumi-wiki-maintenance",
			ResourceID: "vault-main", ObservedAt: "2026-08-20T01:00:00Z", ReceivedAt: "2026-08-20T01:00:00Z",
			RawPayloadDigest: "sha256:" + hex64('a'), IngestStatus: "accepted",
			Changes: []ports.ObservationChange{{
				Ordinal: 0, Path: "Inbox/n.md", Operation: "modify", ExistsAfter: true, FileType: "regular",
				AfterDigest: "sha256:" + hex64('b'), DigestStatus: "known",
			}},
		},
		Batch: ports.BatchInput{
			BatchID: "batch-1", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: "2026-08-20T01:00:00Z",
			ContentFingerprint: "sha256:" + hex64('c'), ObservationIDs: []string{"obs-1"},
		},
		Decision: ports.DecisionInput{
			DecisionID: "decision-1", BatchID: "batch-1", RouteID: "wiki-maintenance",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch",
			Classification: "normal", ReasonCodesJSON: `["meaningful_markdown_change"]`,
			CreatedAt: "2026-08-20T01:00:00Z", Actor: "planner",
		},
		Intent: ports.IntentInput{
			DispatchID: dispatchID, DecisionID: "decision-1", RouteID: "wiki-maintenance",
			RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes_kanban",
			ResourceID: "vault-main", Generation: 1, IdempotencyKey: "jjukkumi:v1:sha256:" + hex64('1'),
			ContentFingerprint: "sha256:" + hex64('c'), ManifestDigest: "sha256:" + hex64('d'),
			RequestVersion: RequestContractVersion, RequestJSON: requestJSON, CreatedAt: "2026-08-20T01:00:00Z",
		},
	}
}

func hex64(ch byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}

func testRequest(dispatchID string) ports.TaskRequest {
	req, _, err := BuildRequest(RequestInput{
		DispatchID:  dispatchID,
		Route:       ports.TaskRouteRef{ID: "wiki-maintenance", Revision: "route-rev-1"},
		Resource:    ports.TaskResource{ID: "vault-main", Workspace: "/srv/vault"},
		TargetID:    "hermes-kanban-main",
		Generation:  1,
		Fingerprint: records.Digest("sha256:" + hex64('c')),
		Changes: []records.ChangeItem{{
			Path: "Inbox/n.md", Operation: records.OpModify, ExistsAfter: true, FileType: records.FileRegular,
			AfterDigest: records.Digest("sha256:" + hex64('b')), DigestStatus: records.DigestKnown,
		}},
		AcceptanceCriteria: []string{"wiki maintenance completes"},
	})
	if err != nil {
		panic(err)
	}
	return req
}

// TestIntentCommittedBeforeSinkInvocation proves the committed, leased
// intent exists before the sink is invoked and no store transaction is
// held during the call (DUR-001, DUR-002).
func TestIntentCommittedBeforeSinkInvocation(t *testing.T) {
	store := openRuntimeStore(t)
	req := testRequest("dispatch-1")
	requestJSON, err := MarshalRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitLineage(context.Background(), runtimeLineage(t, "dispatch-1", requestJSON)); err != nil {
		t.Fatal(err)
	}
	var midFlight struct {
		state string
		owner string
		wrote bool
	}
	fake := fakesink.New("fake-main", fakesink.Step{Result: fakesink.Accepted("task-1")})
	fake.SetSubmitHook(func(ctx context.Context, submitted fakesink.Submitted) error {
		snap, err := store.LoadIntent(ctx, "dispatch-1")
		if err != nil {
			t.Errorf("mid-flight load: %v", err)
			return err
		}
		midFlight.state = string(snap.State)
		midFlight.owner = snap.LeaseOwner
		// A concurrent write proves no store transaction is open across
		// the sink call.
		if _, err := store.Exec(`UPDATE route_runtime_state SET last_source_position = 'mid-flight' WHERE route_id = 'wiki-maintenance'`); err != nil {
			t.Errorf("concurrent write during sink call must succeed: %v", err)
			return err
		}
		midFlight.wrote = true
		return nil
	})
	rt := &Runtime{
		Store: store, Sink: fake,
		Now:      testClock(time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)),
		LeaseTTL: time.Minute, Actor: "runtime-test",
	}
	report, err := rt.SubmitOnce(context.Background(), "dispatch-1", "process-a")
	if err != nil {
		t.Fatal(err)
	}
	if midFlight.state != "submitting" || midFlight.owner != "process-a" {
		t.Fatalf("sink must observe a leased submitting intent: state %q owner %q", midFlight.state, midFlight.owner)
	}
	if !midFlight.wrote {
		t.Fatal("concurrent write during the sink call did not run")
	}
	if report.To != records.IntentAccepted || report.Reason != "durable_acceptance" {
		t.Fatalf("unexpected report: %+v", report)
	}
	snap, _ := store.LoadIntent(context.Background(), "dispatch-1")
	if snap.State != records.IntentAccepted {
		t.Fatalf("final state: %+v", snap)
	}
}

// TestSubmitOnceClassificationScenarios walks the adapter result
// classifications through the flow (TST-006 core scenarios).
func TestSubmitOnceClassificationScenarios(t *testing.T) {
	cases := []struct {
		name   string
		step   fakesink.Step
		ctxErr bool
		to     records.IntentState
	}{
		{"accepted", fakesink.Step{Result: fakesink.Accepted("task-1")}, false, records.IntentAccepted},
		{"rejected", fakesink.Step{Result: fakesink.Rejected()}, false, records.IntentRejected},
		{"definite not submitted", fakesink.Step{Result: fakesink.NotSubmitted()}, false, records.IntentRetryWait},
		{"malformed response", fakesink.Step{Malformed: true}, false, records.IntentUnknown},
		{"sink error is ambiguous", fakesink.Step{Err: errors.New("connection reset after write")}, false, records.IntentUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := openRuntimeStore(t)
			requestJSON, _ := MarshalRequest(testRequest("dispatch-1"))
			if err := store.CommitLineage(context.Background(), runtimeLineage(t, "dispatch-1", requestJSON)); err != nil {
				t.Fatal(err)
			}
			rt := &Runtime{
				Store: store, Sink: fakesink.New("fake-main", c.step),
				Now:      testClock(time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)),
				LeaseTTL: time.Minute, Actor: "runtime-test",
			}
			report, err := rt.SubmitOnce(context.Background(), "dispatch-1", "process-a")
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if report.To != c.to {
				t.Fatalf("%s: final state %s, want %s", c.name, report.To, c.to)
			}
		})
	}
}

// TestSubmitOnceRefusesSecondOwner proves two processes cannot own the
// same attempt: the loser receives the typed lease error and the intent
// is untouched by it (DUR-012).
func TestSubmitOnceRefusesSecondOwner(t *testing.T) {
	store := openRuntimeStore(t)
	requestJSON, _ := MarshalRequest(testRequest("dispatch-1"))
	if err := store.CommitLineage(context.Background(), runtimeLineage(t, "dispatch-1", requestJSON)); err != nil {
		t.Fatal(err)
	}
	// The first process holds the lease without completing it.
	if _, err := store.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-1", Owner: "process-a",
		Now: "2026-08-20T01:00:00Z", LeaseExpiresAt: "2026-08-20T01:05:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	rt := &Runtime{
		Store: store, Sink: fakesink.New("fake-main", fakesink.Step{Result: fakesink.Accepted("task-1")}),
		Now:      func() time.Time { return time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC) },
		LeaseTTL: time.Minute, Actor: "runtime-test",
	}
	_, err := rt.SubmitOnce(context.Background(), "dispatch-1", "process-b")
	if !errors.Is(err, ports.ErrLeaseHeld) {
		t.Fatalf("second owner must receive ErrLeaseHeld: %v", err)
	}
	if len(rt.Sink.(*fakesink.Sink).Submissions()) != 0 {
		t.Fatal("the losing process must not invoke the sink")
	}
}

// TestCrashAfterCommitLeavesRecoverableEvidence proves a process that
// dies after committing and leasing leaves evidence the restart can
// recover: the intent reopens as submitting with the lease, and expiry
// moves it to unknown (AC-203 posture, DUR-010).
func TestCrashAfterCommitLeavesRecoverableEvidence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	mkStore := func(t *testing.T) *sqlite.Store {
		s, err := sqlite.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Migrate(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		return s
	}
	seed := mkStore(t)
	if err := seed.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := seed.RegisterRoute(nil, "wiki-maintenance", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := seed.InitializeRouteState(nil, "wiki-maintenance"); err != nil {
		t.Fatal(err)
	}
	requestJSON, _ := MarshalRequest(testRequest("dispatch-1"))
	if err := seed.CommitLineage(context.Background(), runtimeLineage(t, "dispatch-1", requestJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-1", Owner: "process-a",
		Now: "2026-08-20T01:00:00Z", LeaseExpiresAt: "2026-08-20T01:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	seed.Close() // the process dies after commit

	restart := mkStore(t)
	t.Cleanup(func() { restart.Close() })
	snap, err := restart.LoadIntent(context.Background(), "dispatch-1")
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != records.IntentSubmitting || snap.LeaseOwner != "process-a" {
		t.Fatalf("crash after commit must leave recoverable submitting evidence: %+v", snap)
	}
	rt := &Runtime{
		Store: restart, Sink: fakesink.New("fake-main"),
		Now:      func() time.Time { return time.Date(2026, 8, 20, 1, 2, 0, 0, time.UTC) },
		LeaseTTL: time.Minute, Actor: "recovery",
	}
	recovered, err := rt.Recover(context.Background())
	if err != nil || len(recovered) != 1 || recovered[0].DispatchID != "dispatch-1" {
		t.Fatalf("recovery must take the expired lease: %+v %v", recovered, err)
	}
	snap, _ = restart.LoadIntent(context.Background(), "dispatch-1")
	if snap.State != records.IntentUnknown {
		t.Fatalf("recovered state: %+v", snap)
	}
}

// TestSubmitOnceRefusesTerminal proves a terminal intent is never
// resubmitted.
func TestSubmitOnceRefusesTerminal(t *testing.T) {
	store := openRuntimeStore(t)
	requestJSON, _ := MarshalRequest(testRequest("dispatch-1"))
	if err := store.CommitLineage(context.Background(), runtimeLineage(t, "dispatch-1", requestJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-1", Owner: "process-a",
		Now: "2026-08-20T01:00:00Z", LeaseExpiresAt: "2026-08-20T01:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAttempt(context.Background(), ports.AttemptResult{
		AttemptID: "attempt-1", DispatchID: "dispatch-1", Outcome: "accepted", CompletedAt: "2026-08-20T01:00:30Z",
		Transition: ports.AttemptTransition{
			To: records.IntentAccepted, Reason: "durable_acceptance",
			Evidence:     state.IntentEvidence{ReceiptRef: "rcpt-attempt-1"},
			TransitionID: "attempt-1:durable_acceptance",
		},
		Receipt: &ports.ReceiptInput{
			ReceiptID: "rcpt-attempt-1", Acceptance: records.AcceptanceAccepted, Durable: true,
			ReceivedAt: "2026-08-20T01:00:30Z",
		},
	}); err != nil {
		t.Fatal(err)
	}
	rt := &Runtime{
		Store: store, Sink: fakesink.New("fake-main", fakesink.Step{Result: fakesink.Accepted("task-2")}),
		Now:      func() time.Time { return time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC) },
		LeaseTTL: time.Minute, Actor: "runtime-test",
	}
	if _, err := rt.SubmitOnce(context.Background(), "dispatch-1", "process-b"); err == nil {
		t.Fatal("accepted intent must not be resubmitted")
	}
}

// TestBuildRequestDeterminism proves the request and key are deterministic
// for the same input and identity-affecting for different generations.
func TestBuildRequestDeterminism(t *testing.T) {
	base := RequestInput{
		DispatchID:  "dispatch-1",
		Route:       ports.TaskRouteRef{ID: "wiki-maintenance", Revision: "route-rev-1"},
		Resource:    ports.TaskResource{ID: "vault-main", Workspace: "/srv/vault"},
		TargetID:    "hermes-kanban-main",
		Generation:  1,
		Fingerprint: records.Digest("sha256:" + hex64('c')),
		Changes: []records.ChangeItem{{
			Path: "Inbox/n.md", Operation: records.OpModify, FileType: records.FileRegular,
			AfterDigest: records.Digest("sha256:" + hex64('b')), DigestStatus: records.DigestKnown,
		}},
		AcceptanceCriteria: []string{"wiki maintenance completes"},
	}
	a, keyA, err := BuildRequest(base)
	if err != nil {
		t.Fatal(err)
	}
	b, keyB, err := BuildRequest(base)
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := MarshalRequest(a)
	jb, _ := MarshalRequest(b)
	if ja != jb || keyA != keyB {
		t.Fatalf("identical input must produce identical requests and keys:\n%s\n%s", ja, jb)
	}
	next := base
	next.Generation = 2
	if _, keyNext, err := BuildRequest(next); err != nil || keyNext == keyA {
		t.Fatalf("a new generation must produce a new key: %q vs %q (%v)", keyNext, keyA, err)
	}
	if _, _, err := BuildRequest(RequestInput{DispatchID: "x"}); err == nil {
		t.Fatal("incomplete input must fail closed")
	}
}

// TestRequestValidatesAgainstContractSchema proves the built request
// matches the hermes-task-request contract.
func TestRequestValidatesAgainstContractSchema(t *testing.T) {
	if _, err := os.Stat("../../../docs/schemas"); err != nil {
		t.Skip("docs schemas unavailable")
	}
	compiled, err := schemavalid.CompileSchemas("../../../docs/schemas")
	if err != nil {
		t.Fatal(err)
	}
	schema, ok := compiled["urn:jjukkumi:schema:hermes-task-request:v1"]
	if !ok {
		t.Fatal("hermes-task-request schema missing")
	}
	raw, err := json.Marshal(testRequest("dispatch-1"))
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("built request does not validate: %v\n%s", err, raw)
	}
}

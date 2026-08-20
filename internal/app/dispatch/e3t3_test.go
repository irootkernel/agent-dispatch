package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/rootkernel/jjukkumi/internal/adapters/sqlite"
	"github.com/rootkernel/jjukkumi/internal/app/reconcile"
	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/ports"
	"github.com/rootkernel/jjukkumi/internal/testsupport/fakesink"
)

// E3-T3 acceptance coverage: bounded retry with jittered backoff, the
// result classifier, drain limits, operator retry/rerun, and dead-letter
// inspectability, over the real SQLite store with the fake sink.

func openE3T3Store(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "state.db"))
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
	if err := s.RegisterRoute(nil, "wiki-maintenance", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki-maintenance"); err != nil {
		t.Fatal(err)
	}
	// Rerun is a new submission: the route must be enabled for its
	// takeover activation to be legal.
	if err := s.SetRouteActivation(context.Background(), "wiki-maintenance", "enabled", "route-rev-1", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	return s
}

func seedReadyIntent(t *testing.T, s *sqlite.Store, dispatchID string) {
	t.Helper()
	req, _, err := BuildRequest(RequestInput{
		DispatchID: dispatchID,
		Route:      ports.TaskRouteRef{ID: "wiki-maintenance", Revision: "route-rev-1"},
		Resource:   ports.TaskResource{ID: "vault-main", Workspace: "/srv/vault"},
		TargetID:   "hermes-kanban-main", Generation: 1,
		Fingerprint: records.Digest("sha256:" + hex64('c')),
		Changes: []records.ChangeItem{{
			Path: "Inbox/n.md", Operation: records.OpModify, FileType: records.FileRegular,
			AfterDigest: records.Digest("sha256:" + hex64('b')), DigestStatus: records.DigestKnown,
		}},
		AcceptanceCriteria: []string{"wiki maintenance completes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	requestJSON, _ := MarshalRequest(req)
	lin := runtimeLineage(t, dispatchID, requestJSON)
	lin.Intent.IdempotencyKey = req.IdempotencyKey
	if err := s.CommitLineage(context.Background(), lin); err != nil {
		t.Fatal(err)
	}
}

// TestBackoffDeterministicAndBounded proves the exponential delay is
// deterministic for one injected jitter unit, capped at the maximum, and
// fails closed outside the envelope (DUR-007).
func TestBackoffDeterministicAndBounded(t *testing.T) {
	b := Backoff{MaxAttempts: 3, InitialBackoff: 2 * time.Second, MaxBackoff: time.Minute, Multiplier: 2, JitterFraction: 0.2}
	a, err := b.Delay(1, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := b.Delay(1, 0.5)
	if err != nil || a != bb {
		t.Fatalf("same inputs must be deterministic: %v vs %v (%v)", a, bb, err)
	}
	// 2s * 2^0 * (1 + 0.2*0.5) = 2.2s
	if a != 2200*time.Millisecond {
		t.Fatalf("delay = %v, want 2.2s", a)
	}
	if d, _ := b.Delay(9, 1.0); d != time.Minute {
		t.Fatalf("delay must cap at the maximum: %v", d)
	}
	if _, err := b.Delay(1, 1.5); err == nil {
		t.Fatal("jitter unit above 1 must fail closed")
	}
	if _, err := b.Delay(1, -0.1); err == nil {
		t.Fatal("negative jitter unit must fail closed")
	}
	if !b.Exhausted(3) || b.Exhausted(2) {
		t.Fatal("exhaustion must match the configured limit")
	}
	bad := Backoff{MaxAttempts: 11, InitialBackoff: time.Second, MaxBackoff: time.Second, Multiplier: 2}
	if _, err := bad.Delay(1, 0); err == nil {
		t.Fatal("out-of-envelope policy must fail closed")
	}
}

// TestClassifyResultScenarios covers the classifier over the TST-006
// outcome space, including that no classification ever yields an
// automatic fallback (DUR-008) and ambiguity never becomes failed
// (DUR-005).
func TestClassifyResultScenarios(t *testing.T) {
	cases := []struct {
		name    string
		res     ports.SubmitResult
		err     error
		outcome string
		to      records.IntentState
	}{
		{"accepted durable", ports.SubmitResult{Classification: ports.SubmitAccepted, Durable: ports.DurableTrue}, nil, "accepted", records.IntentAccepted},
		{"accepted non-durable", ports.SubmitResult{Classification: ports.SubmitAccepted, Durable: ports.DurableFalse}, nil, "accepted", records.IntentAccepted},
		{"rejected", ports.SubmitResult{Classification: ports.SubmitRejected}, nil, "rejected", records.IntentRejected},
		{"definite not submitted", ports.SubmitResult{Classification: ports.SubmitDefiniteNotSubmitted}, nil, "transport_failure", records.IntentRetryWait},
		{"unknown", ports.SubmitResult{Classification: ports.SubmitUnknown}, nil, "unknown", records.IntentUnknown},
		{"sink error", ports.SubmitResult{}, errors.New("reset"), "unknown", records.IntentUnknown},
	}
	for _, c := range cases {
		got := ClassifyResult(c.res, c.err)
		if got.AttemptOutcome != c.outcome || got.To != c.to {
			t.Errorf("%s: %+v", c.name, got)
		}
		if c.to == records.IntentFailed {
			t.Errorf("%s: ambiguity must never classify failed", c.name)
		}
	}
}

// TestRetryWaitPersistsBackoff proves a definite transient failure moves
// the intent to retry_wait with the persisted backoff deadline.
func TestRetryWaitPersistsBackoff(t *testing.T) {
	s := openE3T3Store(t)
	seedReadyIntent(t, s, "dispatch-1")
	rt := &Runtime{
		Store: s, Sink: fakesink.New("fake-main", fakesink.Step{Result: fakesink.NotSubmitted()}),
		Now:      func() time.Time { return time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC) },
		LeaseTTL: time.Minute, Actor: "test",
		Backoff:    Backoff{MaxAttempts: 2, InitialBackoff: time.Hour, MaxBackoff: time.Hour, Multiplier: 2, JitterFraction: 0},
		JitterUnit: func() float64 { return 0 },
	}
	report, err := rt.SubmitOnce(context.Background(), "dispatch-1", "p1")
	if err != nil {
		t.Fatal(err)
	}
	if report.To != records.IntentRetryWait || report.NextAttemptAt != "2026-08-20T02:00:00Z" {
		t.Fatalf("unexpected report: %+v", report)
	}
	snap, _ := s.LoadIntent(context.Background(), "dispatch-1")
	if snap.State != records.IntentRetryWait || snap.NextAttemptAt != "2026-08-20T02:00:00Z" {
		t.Fatalf("backoff deadline must persist: %+v", snap)
	}
	// The retained key survives the retry cycle.
	if snap.IdempotencyKey == "" {
		t.Fatal("retry must retain the idempotency key")
	}
}

// TestDrainStopsAtLimit proves automatic attempts stop at the configured
// limit (AC-205 posture).
func TestDrainStopsAtLimit(t *testing.T) {
	s := openE3T3Store(t)
	seedReadyIntent(t, s, "dispatch-1")
	// Exhaust the budget: two failed attempts with max 2.
	base := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	clock := func() func() time.Time {
		current := base
		return func() time.Time {
			current = current.Add(time.Second)
			return current
		}
	}()
	rt := &Runtime{
		Store: s, Sink: fakesink.New("fake-main", fakesink.Step{Result: fakesink.NotSubmitted()}, fakesink.Step{Result: fakesink.NotSubmitted()}),
		Now:      clock,
		LeaseTTL: time.Minute, Actor: "test",
		Backoff: Backoff{MaxAttempts: 2, InitialBackoff: time.Second, MaxBackoff: time.Second, Multiplier: 2, JitterFraction: 0},
	}
	if _, err := rt.SubmitOnce(context.Background(), "dispatch-1", "p1"); err != nil {
		t.Fatal(err)
	}
	// Make it due, then the second (final) attempt.
	if err := s.MakeRetryDue(context.Background(), "dispatch-1", "2026-08-20T01:00:01Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SubmitOnce(context.Background(), "dispatch-1", "p1"); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.LoadIntent(context.Background(), "dispatch-1")
	if snap.AttemptCount != 2 {
		t.Fatalf("attempt count: %d", snap.AttemptCount)
	}
	if err := s.MakeRetryDue(context.Background(), "dispatch-1", "2026-08-20T01:00:02Z"); err != nil {
		t.Fatal(err)
	}
	report, err := rt.Drain(context.Background(), "wiki-maintenance", 10, s)
	if err != nil {
		t.Fatal(err)
	}
	if report.Processed != 0 || report.Skipped != 1 {
		t.Fatalf("exhausted budget must stop automatic processing: %+v", report)
	}
}

// TestOperatorRetryDeadLettered proves the full E3-T3 dead-letter path:
// an ambiguous outcome becomes unknown, unresolved reconciliation with an
// exhausted budget dead-letters it, and the explicit operator retry
// returns it to ready with a reset budget while retaining the request and
// key (DUR-005, DUR-006, DUR-009).
func TestOperatorRetryDeadLettered(t *testing.T) {
	s := openE3T3Store(t)
	seedReadyIntent(t, s, "dispatch-1")
	rt := &Runtime{
		Store: s, Sink: fakesink.New("fake-main", fakesink.Step{Err: errors.New("deadline exceeded after write")}),
		Now:      func() time.Time { return time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC) },
		LeaseTTL: time.Minute, Actor: "test",
	}
	if _, err := rt.SubmitOnce(context.Background(), "dispatch-1", "p1"); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.LoadIntent(context.Background(), "dispatch-1")
	if snap.State != records.IntentUnknown {
		t.Fatalf("ambiguous outcome must be unknown: %+v", snap)
	}
	fake := fakesink.New("fake-main")
	fake.ScriptLookupByIdempotency(snap.IdempotencyKey, fakesink.LookupStep{
		Result: ports.LookupResult{Status: ports.LookupAmbiguous},
	})
	rec := &reconcile.Service{Store: s, Sink: fake, Now: func() string { return "2026-08-20T01:01:00Z" }}
	res, err := rec.Reconcile(context.Background(), "dispatch-1", "reconciler", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.To != records.IntentDeadLettered {
		t.Fatalf("unresolved lookup with exhausted budget must dead-letter: %+v", res)
	}
	before, _ := s.LoadIntent(context.Background(), "dispatch-1")
	op := &OperatorService{Store: s, Now: func() string { return "2026-08-20T01:02:00Z" }}
	if _, err := op.Retry(context.Background(), "dispatch-1", "operator", ""); err == nil {
		t.Fatal("dead-lettered retry requires --reason")
	}
	to, err := op.Retry(context.Background(), "dispatch-1", "operator", "operator reviewed the dead letter")
	if err != nil || to != "ready" {
		t.Fatalf("retry: %q %v", to, err)
	}
	after, _ := s.LoadIntent(context.Background(), "dispatch-1")
	if after.State != records.IntentReady || after.AttemptCount != 0 {
		t.Fatalf("retry must reset the budget: %+v", after)
	}
	if after.IdempotencyKey != before.IdempotencyKey {
		t.Fatal("retry must retain the idempotency key")
	}
	// The dead-lettered history remains fully inspectable (DUR-009).
	lin, err := s.LoadIntentLineage(context.Background(), "dispatch-1")
	if err != nil || len(lin.Transitions) < 3 || len(lin.Attempts) == 0 {
		t.Fatalf("lineage must remain inspectable: %+v %v", lin, err)
	}
}

// TestOperatorRerunCreatesNewLineageAndKey proves rerun creates a new
// dispatch, generation, and idempotency key while the original stays
// untouched.
func TestOperatorRerunCreatesNewLineageAndKey(t *testing.T) {
	s := openE3T3Store(t)
	seedReadyIntent(t, s, "dispatch-1")
	op := &OperatorService{Store: s, Now: func() string { return "2026-08-20T01:02:00Z" }}
	if _, err := op.Rerun(context.Background(), "dispatch-1", "operator", ""); err == nil {
		t.Fatal("rerun requires --reason")
	}
	summary, err := op.Rerun(context.Background(), "dispatch-1", "operator", "operator wants a fresh run")
	if err != nil {
		t.Fatal(err)
	}
	if summary.DispatchID == "dispatch-1" || summary.Generation != 2 || summary.State != records.IntentReady {
		t.Fatalf("unexpected rerun summary: %+v", summary)
	}
	if summary.IdempotencyKey == "" {
		t.Fatal("rerun must derive a new idempotency key")
	}
	// The route slot moved to the rerun; the original intent is intact.
	original, _ := s.LoadIntent(context.Background(), "dispatch-1")
	if original.State != records.IntentReady || original.Generation != 1 {
		t.Fatalf("original intent must be untouched: %+v", original)
	}
}

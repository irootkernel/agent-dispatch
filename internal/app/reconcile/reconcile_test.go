package reconcile

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/fakesink"
)

func openStore(t *testing.T) *sqlite.Store {
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
	if err := s.SetRouteActivation(context.Background(), "wiki-maintenance", "enabled", "route-rev-1", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	return s
}

// seedUnknown commits an intent and leaves it in unknown (the recovered
// abandoned-submitting shape).
func seedUnknown(t *testing.T, s *sqlite.Store, dispatchID string) string {
	t.Helper()
	lin := ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: "obs-1", SchemaVersion: "agent-dispatch.source-observation/v1", SourceType: "watchman",
			SourceID: "watchman-main", TriggerName: "trig", ResourceID: "vault-main",
			ObservedAt: "2026-08-20T01:00:00Z", ReceivedAt: "2026-08-20T01:00:00Z",
			RawPayloadDigest: "sha256:" + rep('a'), IngestStatus: "accepted",
		},
		Batch: ports.BatchInput{
			BatchID: "batch-1", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: "2026-08-20T01:00:00Z",
			ContentFingerprint: "sha256:" + rep('c'), ObservationIDs: []string{"obs-1"},
		},
		Decision: ports.DecisionInput{
			DecisionID: "decision-1", BatchID: "batch-1", RouteID: "wiki-maintenance",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch",
			Classification: "normal", CreatedAt: "2026-08-20T01:00:00Z", Actor: "planner",
		},
		Intent: ports.IntentInput{
			DispatchID: dispatchID, DecisionID: "decision-1", RouteID: "wiki-maintenance",
			RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes_kanban",
			ResourceID: "vault-main", Generation: 1, IdempotencyKey: "agent-dispatch:v1:sha256:" + rep('1'),
			ContentFingerprint: "sha256:" + rep('c'), ManifestDigest: "sha256:" + rep('d'),
			RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: "{}", CreatedAt: "2026-08-20T01:00:00Z",
		},
	}
	if err := s.CommitLineage(context.Background(), lin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: dispatchID, AttemptID: "attempt-1", Owner: "p1",
		Now: "2026-08-20T01:00:00Z", LeaseExpiresAt: "2026-08-20T01:00:30Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecoverExpiredSubmitting(context.Background(), "", "2026-08-20T01:01:00Z"); err != nil {
		t.Fatal(err)
	}
	return lin.Intent.IdempotencyKey
}

func rep(ch byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}

// TestReconcileScenarios walks the lookup outcomes: found-accepted
// resolves to accepted, proven non-acceptance retries while budget
// remains, and an ambiguous lookup with an exhausted budget dead-letters
// (DUR-006, DUR-009).
func TestReconcileScenarios(t *testing.T) {
	cases := []struct {
		name      string
		lookup    ports.LookupResult
		exhausted bool
		to        records.IntentState
	}{
		{"found accepted", ports.LookupResult{Status: ports.LookupFound, Acceptance: records.AcceptanceAccepted, ExternalRef: "task-9"}, false, records.IntentAccepted},
		{"found rejected", ports.LookupResult{Status: ports.LookupFound, Acceptance: records.AcceptanceRejected}, false, records.IntentRetryWait},
		{"absent with budget", ports.LookupResult{Status: ports.LookupAbsent}, false, records.IntentRetryWait},
		{"absent exhausted", ports.LookupResult{Status: ports.LookupAbsent}, true, records.IntentDeadLettered},
		{"ambiguous exhausted", ports.LookupResult{Status: ports.LookupAmbiguous}, true, records.IntentDeadLettered},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := openStore(t)
			key := seedUnknown(t, s, "dispatch-1")
			fake := fakesink.New("fake-main")
			fake.ScriptLookupByIdempotency(key, fakesink.LookupStep{Result: c.lookup})
			svc := &Service{Store: s, Sink: fake, Now: func() string { return "2026-08-20T01:02:00Z" }}
			res, err := svc.Reconcile(context.Background(), "dispatch-1", "reconciler", c.exhausted)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if res.To != c.to {
				t.Fatalf("%s: resolved %s, want %s", c.name, res.To, c.to)
			}
		})
	}
}

// TestReconcileUnavailableLookupDeadLetters proves an unavailable lookup
// never retries blindly: it proves nothing and dead-letters for the
// operator when the budget is exhausted (DUR-008 posture).
func TestReconcileUnavailableLookupDeadLetters(t *testing.T) {
	s := openStore(t)
	key := seedUnknown(t, s, "dispatch-1")
	fake := fakesink.New("fake-main")
	fake.ScriptLookupByIdempotency(key, fakesink.LookupStep{Err: errors.New("target lookup unavailable")})
	svc := &Service{Store: s, Sink: fake, Now: func() string { return "2026-08-20T01:02:00Z" }}
	res, err := svc.Reconcile(context.Background(), "dispatch-1", "reconciler", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.To != records.IntentDeadLettered {
		t.Fatalf("unavailable lookup with exhausted budget must dead-letter: %+v", res)
	}
}

// TestReconcileRefusesNonUnknown proves only unknown work enters the
// workflow.
func TestReconcileRefusesNonUnknown(t *testing.T) {
	s := openStore(t)
	seedUnknown(t, s, "dispatch-1")
	// Resolve it once (the unscripted lookup reports absent, proving
	// non-acceptance and moving the dispatch to retry_wait).
	fake := fakesink.New("fake-main")
	svc := &Service{Store: s, Sink: fake, Now: func() string { return dispatchTS() }}
	if _, err := svc.Reconcile(context.Background(), "dispatch-1", "reconciler", false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Reconcile(context.Background(), "dispatch-1", "reconciler", false); err == nil {
		t.Fatal("non-unknown work must not re-enter reconciliation")
	}
}

func dispatchTS() string {
	return time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
}

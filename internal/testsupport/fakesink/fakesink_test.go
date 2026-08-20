package fakesink

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

func baseRequest(key string) ports.TaskRequest {
	return ports.TaskRequest{
		ContractVersion:    "jjukkumi.hermes-task/v1",
		DispatchID:         "dispatch-1",
		IdempotencyKey:     key,
		AcceptanceCriteria: []string{"wiki maintenance completes"},
	}
}

// TestSimulatesAcceptedDurable is the accepted-durable scenario.
func TestSimulatesAcceptedDurable(t *testing.T) {
	s := New("fake-main", Step{Result: Accepted("task-1")})
	res, err := s.Submit(context.Background(), baseRequest("k1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Classification != ports.SubmitAccepted || res.Durable != ports.DurableTrue || res.ExternalRef != "task-1" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(s.Submissions()) != 1 {
		t.Fatalf("submit must be recorded: %d", len(s.Submissions()))
	}
}

// TestSimulatesRejected is the definite rejection scenario.
func TestSimulatesRejected(t *testing.T) {
	s := New("fake-main", Step{Result: Rejected()})
	res, err := s.Submit(context.Background(), baseRequest("k1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Classification != ports.SubmitRejected {
		t.Fatalf("unexpected result: %+v", res)
	}
}

// TestSimulatesDefiniteNotSubmitted is the definite pre-submit failure
// scenario.
func TestSimulatesDefiniteNotSubmitted(t *testing.T) {
	s := New("fake-main", Step{Result: NotSubmitted()})
	res, err := s.Submit(context.Background(), baseRequest("k1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Classification != ports.SubmitDefiniteNotSubmitted {
		t.Fatalf("unexpected result: %+v", res)
	}
}

// TestSimulatesTimeoutBeforeAccept proves a deadline hit before the
// write is recorded leaves no recorded submission (nothing reached the
// target).
func TestSimulatesTimeoutBeforeAccept(t *testing.T) {
	s := New("fake-main", Step{DelayBeforeRecord: 10 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := s.Submit(ctx, baseRequest("k1"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
	if len(s.Submissions()) != 0 {
		t.Fatalf("timeout before accept must not record a write: %d", len(s.Submissions()))
	}
}

// TestSimulatesTimeoutAfterAccept proves a deadline hit after the write
// is recorded still counts as possibly submitted.
func TestSimulatesTimeoutAfterAccept(t *testing.T) {
	s := New("fake-main", Step{Result: Accepted("task-1"), DelayAfterRecord: 10 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := s.Submit(ctx, baseRequest("k1"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
	if len(s.Submissions()) != 1 {
		t.Fatalf("timeout after accept must record the write: %d", len(s.Submissions()))
	}
}

// TestSimulatesMalformedResponse is the invalid-output scenario: the
// classification is unknown, never failed (DUR-005).
func TestSimulatesMalformedResponse(t *testing.T) {
	s := New("fake-main", Step{Malformed: true})
	res, err := s.Submit(context.Background(), baseRequest("k1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Classification != ports.SubmitUnknown {
		t.Fatalf("malformed output must classify unknown: %+v", res)
	}
	if len(res.StructuredPayload) == 0 {
		t.Fatal("malformed payload evidence must be preserved")
	}
}

// TestSimulatesDuplicateIdempotency proves a second submit with the same
// key replays the first result instead of creating a second target task.
func TestSimulatesDuplicateIdempotency(t *testing.T) {
	s := New("fake-main", Step{Result: Accepted("task-1")}, Step{Result: Rejected()})
	first, err := s.Submit(context.Background(), baseRequest("k1"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Submit(context.Background(), baseRequest("k1"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Classification != second.Classification || second.ExternalRef != "task-1" {
		t.Fatalf("duplicate key must replay the first result: %+v then %+v", first, second)
	}
	if len(s.Submissions()) != 2 {
		t.Fatalf("both invocations are recorded evidence: %d", len(s.Submissions()))
	}
}

// TestSimulatesUnavailableLookup is the lookup-unavailable scenario.
func TestSimulatesUnavailableLookup(t *testing.T) {
	s := New("fake-main")
	unavailable := errors.New("target lookup temporarily unavailable")
	s.ScriptLookupByIdempotency("k1", LookupStep{Err: unavailable})
	if _, err := s.LookupByIdempotencyKey(context.Background(), "k1"); !errors.Is(err, unavailable) {
		t.Fatalf("expected the unavailable lookup error, got %v", err)
	}
}

// TestSimulatesLookupFoundAbsent proves recorded keys look up found and
// unknown keys look up absent.
func TestSimulatesLookupFoundAbsent(t *testing.T) {
	s := New("fake-main", Step{Result: Accepted("task-1")})
	if _, err := s.Submit(context.Background(), baseRequest("k1")); err != nil {
		t.Fatal(err)
	}
	got, err := s.LookupByIdempotencyKey(context.Background(), "k1")
	if err != nil || got.Status != ports.LookupFound {
		t.Fatalf("recorded key must look up found: %+v %v", got, err)
	}
	got, err = s.LookupByIdempotencyKey(context.Background(), "k-other")
	if err != nil || got.Status != ports.LookupAbsent {
		t.Fatalf("unknown key must look up absent: %+v %v", got, err)
	}
	s.ScriptLookupByIdempotency("k-amb", LookupStep{Result: ports.LookupResult{Status: ports.LookupAmbiguous}})
	got, err = s.LookupByIdempotencyKey(context.Background(), "k-amb")
	if err != nil || got.Status != ports.LookupAmbiguous {
		t.Fatalf("scripted ambiguous lookup: %+v %v", got, err)
	}
}

// TestSimulatesStatusProgression drains the scripted execution stages in
// order.
func TestSimulatesStatusProgression(t *testing.T) {
	s := New("fake-main")
	s.ScriptExecution("task-1",
		ExecutionStep{Projection: ports.ExecutionProjection{State: records.ExecQueued, ExternalRef: "task-1"}},
		ExecutionStep{Projection: ports.ExecutionProjection{State: records.ExecRunning, ExternalRef: "task-1"}},
		ExecutionStep{Projection: ports.ExecutionProjection{State: records.ExecSucceeded, ExternalRef: "task-1"}},
	)
	for _, want := range []records.ExecutionState{records.ExecQueued, records.ExecRunning, records.ExecSucceeded} {
		got, err := s.GetExecution(context.Background(), "task-1")
		if err != nil {
			t.Fatal(err)
		}
		if got.State != want {
			t.Fatalf("progression: got %s, want %s", got.State, want)
		}
	}
	if _, err := s.GetExecution(context.Background(), "task-1"); err == nil {
		t.Fatal("drained progression must report unsupported")
	}
}

// TestSubmitHookObservesMidFlight proves the hook runs while the write
// is recorded, so runtime tests can assert durable pre-call state.
func TestSubmitHookObservesMidFlight(t *testing.T) {
	var observed int
	s := New("fake-main", Step{Result: Accepted("task-1")})
	s.SetSubmitHook(func(ctx context.Context, submitted Submitted) error {
		observed++
		if submitted.Request.DispatchID != "dispatch-1" {
			t.Errorf("hook must see the request: %+v", submitted)
		}
		return nil
	})
	if _, err := s.Submit(context.Background(), baseRequest("k1")); err != nil {
		t.Fatal(err)
	}
	if observed != 1 {
		t.Fatalf("hook calls: %d", observed)
	}
}

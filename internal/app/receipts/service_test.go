package receipts

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// TestStaleActiveBoundaries proves the stale detection window: older
// than the window is stale, exactly at it is not, and unparsable times
// never warn.
func TestStaleActiveBoundaries(t *testing.T) {
	created := "2026-08-20T10:00:00Z"
	at := func(offset time.Duration) string {
		return time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC).Add(offset).Format(time.RFC3339)
	}
	if !StaleActive(created, at(2*time.Hour), time.Hour) {
		t.Fatal("older than the window must be stale")
	}
	if StaleActive(created, at(time.Hour), time.Hour) {
		t.Fatal("exactly at the window must not be stale")
	}
	if StaleActive(created, at(30*time.Minute), time.Hour) {
		t.Fatal("inside the window must not be stale")
	}
	if StaleActive("not-a-time", at(time.Hour), time.Hour) {
		t.Fatal("unparsable times must never warn")
	}
}

// failingSink simulates sink failures with and without a projection,
// per the persist-vs-fail convention on ports.Sink.GetExecution.
type failingSink struct {
	projection ports.ExecutionProjection
	err        error
}

func (f *failingSink) ID() string           { return "failing" }
func (f *failingSink) Type() ports.SinkType { return ports.SinkFake }
func (f *failingSink) Probe(ctx context.Context) (ports.Capabilities, error) {
	return ports.Capabilities{}, nil
}
func (f *failingSink) Submit(ctx context.Context, req ports.TaskRequest) (ports.SubmitResult, error) {
	return ports.SubmitResult{}, nil
}
func (f *failingSink) LookupByIdempotencyKey(ctx context.Context, key string) (ports.LookupResult, error) {
	return ports.LookupResult{}, nil
}
func (f *failingSink) LookupByExternalRef(ctx context.Context, ref string) (ports.LookupResult, error) {
	return ports.LookupResult{}, nil
}
func (f *failingSink) GetExecution(ctx context.Context, ref string) (ports.ExecutionProjection, error) {
	return f.projection, f.err
}

// TestRefreshTransportFailurePersistsNothing proves a sink failure
// carrying no projection persists nothing (the convention documented
// on the port); an unavailable projection with its error is persisted
// with the reason, never as success.
func TestRefreshTransportFailurePersistsNothing(t *testing.T) {
	sink := &failingSink{err: errors.New("connection reset after request transmission")}
	service := &Service{Store: storeWithAcceptedDispatch(t), Sink: sink, Now: time.Now}
	_, err := service.Refresh(context.Background(), acceptedDispatchID)
	if err == nil {
		t.Fatal("transport failure must surface the error")
	}
	receipts, err := service.Store.ListReceipts(context.Background(), ports.ReceiptFilter{DispatchID: acceptedDispatchID})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range receipts {
		if r.ReceiptKind == "execution_projection" {
			t.Fatal("no projection receipt may be persisted without a projection")
		}
	}

	// An unavailable projection with its reason is persisted.
	sink2 := &failingSink{
		projection: ports.ExecutionProjection{State: records.ExecUnavailable, ExternalRef: "t_6253023d"},
		err:        errors.New("task status is outside the frozen public enum"),
	}
	service2 := &Service{Store: storeWithAcceptedDispatch(t), Sink: sink2, Now: time.Now}
	result, err := service2.Refresh(context.Background(), acceptedDispatchID)
	if err != nil {
		t.Fatalf("unavailable projection must be recorded, not failed: %v", err)
	}
	if result.Projection.State != records.ExecUnavailable || result.UnavailableReason == "" {
		t.Fatalf("unavailable projection wrong: %+v", result)
	}
	persisted, err := service2.Store.ListReceipts(context.Background(), ports.ReceiptFilter{DispatchID: acceptedDispatchID, Kind: "execution_projection"})
	if err != nil || len(persisted) != 1 || persisted[0].ExecutionState != records.ExecUnavailable {
		t.Fatalf("persisted unavailable projection wrong: %+v err=%v", persisted, err)
	}
}

const acceptedDispatchID = "disp-accepted-1"

// storeWithAcceptedDispatch builds a real store with one accepted
// dispatch carrying an external reference.
func storeWithAcceptedDispatch(t *testing.T) Store {
	t.Helper()
	dir := t.TempDir()
	s, err := sqlite.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki"); err != nil {
		t.Fatal(err)
	}
	now := "2026-08-20T00:00:00Z"
	if err := s.CommitLineage(ctx, ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: "obs-1", SchemaVersion: "agent-dispatch.source-observation/v1",
			SourceType: "watchman", SourceID: "watchman-main", TriggerName: "trig",
			ResourceID: "vault-main", ObservedAt: now, ReceivedAt: now,
			RawPayloadDigest: "sha256:aaa", IngestStatus: "accepted",
		},
		Batch: ports.BatchInput{
			BatchID: "batch-1", RouteID: "wiki", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: now,
			ContentFingerprint: "sha256:aaa", ObservationIDs: []string{"obs-1"},
		},
		Decision: ports.DecisionInput{
			DecisionID: "decision-1", BatchID: "batch-1", RouteID: "wiki",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1",
			Disposition: "dispatch", Classification: "normal", CreatedAt: now, Actor: "planner",
		},
		Intent: ports.IntentInput{
			DispatchID: acceptedDispatchID, DecisionID: "decision-1", RouteID: "wiki", RouteRevision: "route-rev-1",
			TargetID: "hermes-kanban-main", TargetType: "hermes-kanban", ResourceID: "vault-main",
			Generation: 1, IdempotencyKey: "agent-dispatch:v1:sha256:abc", ContentFingerprint: "sha256:aaa",
			ManifestDigest: "sha256:ddd", RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: "{}", CreatedAt: now,
		},
	}); err != nil {
		t.Fatal(err)
	}
	acquired, err := s.AcquireAttempt(ctx, ports.AcquireAttempt{DispatchID: acceptedDispatchID, Owner: "test", Now: now, LeaseExpiresAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAttempt(ctx, ports.AttemptResult{
		AttemptID: acquired, DispatchID: acceptedDispatchID, Outcome: "accepted", CompletedAt: now,
		Transition: ports.AttemptTransition{To: records.IntentAccepted, Reason: state.ReasonDurableAcceptance, TransitionID: acquired + ":durable_acceptance",
			Evidence: state.IntentEvidence{ReceiptRef: "rcpt-" + acceptedDispatchID}},
		Receipt: &ports.ReceiptInput{
			ReceiptID: "rcpt-" + acceptedDispatchID, Acceptance: records.AcceptanceAccepted, Durable: true,
			ExternalRef: "t_6253023d", ReceivedAt: now,
		},
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

// queuedSink always projects queued execution.
type queuedSink struct{}

func (queuedSink) ID() string           { return "queued" }
func (queuedSink) Type() ports.SinkType { return ports.SinkFake }
func (queuedSink) Probe(ctx context.Context) (ports.Capabilities, error) {
	return ports.Capabilities{}, nil
}
func (queuedSink) Submit(ctx context.Context, req ports.TaskRequest) (ports.SubmitResult, error) {
	return ports.SubmitResult{}, nil
}
func (queuedSink) LookupByIdempotencyKey(ctx context.Context, key string) (ports.LookupResult, error) {
	return ports.LookupResult{}, nil
}
func (queuedSink) LookupByExternalRef(ctx context.Context, ref string) (ports.LookupResult, error) {
	return ports.LookupResult{}, nil
}
func (queuedSink) GetExecution(ctx context.Context, ref string) (ports.ExecutionProjection, error) {
	return ports.ExecutionProjection{State: records.ExecQueued, ExternalRef: ref, TargetObservedAt: "2026-08-20T00:00:01Z"}, nil
}

// TestRefreshAppendsHistory proves every refresh appends a new
// projection receipt: the acceptance evidence plus one row per refresh,
// never overwritten history.
func TestRefreshAppendsHistory(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 1, 0, 0, time.UTC)
	current := base
	service := &Service{Store: storeWithAcceptedDispatch(t), Sink: queuedSink{}, Now: func() time.Time { return current }}
	for i := 0; i < 3; i++ {
		if _, err := service.Refresh(context.Background(), acceptedDispatchID); err != nil {
			t.Fatalf("refresh %d: %v", i, err)
		}
		current = current.Add(time.Duration(i+1) * time.Minute)
	}
	all, err := service.Store.ListReceipts(context.Background(), ports.ReceiptFilter{DispatchID: acceptedDispatchID, Kind: "execution_projection"})
	if err != nil || len(all) != 3 {
		t.Fatalf("three refreshes must append three projection receipts, got %d (err=%v)", len(all), err)
	}
	// Newest first.
	if all[0].ReceivedAt < all[1].ReceivedAt || all[1].ReceivedAt < all[2].ReceivedAt {
		t.Fatalf("projection history must list newest first: %+v", all)
	}
	// The acceptance receipt is untouched and still present.
	acceptance, err := service.Store.ListReceipts(context.Background(), ports.ReceiptFilter{DispatchID: acceptedDispatchID, Kind: "acceptance"})
	if err != nil || len(acceptance) != 1 || acceptance[0].ExternalRef != "t_6253023d" {
		t.Fatalf("acceptance evidence must remain: %+v err=%v", acceptance, err)
	}
}

// TestWorkReceiptsUnionAndKind proves the receipts repository covers
// work receipts: the work kind filter and the empty-kind union read
// the work-receipt table with the status mapped onto the execution
// axis, and show returns the work detail.
func TestWorkReceiptsUnionAndKind(t *testing.T) {
	store := storeWithAcceptedDispatch(t)
	ctx := context.Background()
	if err := store.(interface {
		SaveWorkReceipt(tx *sql.Tx, w sqlite.WorkReceiptRecord) error
	}).SaveWorkReceipt(nil, sqlite.WorkReceiptRecord{
		ReceiptID: "wr-1", DispatchID: acceptedDispatchID, RunID: "run-1", ResourceID: "vault-main",
		Status: "completed", ExternalTaskID: "t_6253023d", SubmittedAt: "2026-08-20T00:02:00Z",
		ValidationState: "valid",
	}); err != nil {
		t.Fatal(err)
	}
	kind, err := store.ListReceipts(ctx, ports.ReceiptFilter{DispatchID: acceptedDispatchID, Kind: "work"})
	if err != nil || len(kind) != 1 {
		t.Fatalf("work kind filter: %+v err=%v", kind, err)
	}
	if kind[0].ReceiptKind != "work" || kind[0].ExecutionState != records.ExecSucceeded || kind[0].ExternalRef != "t_6253023d" {
		t.Fatalf("work receipt mapping wrong: %+v", kind[0])
	}

	// Empty kind unions dispatch and work receipts, newest first, and
	// honors the caller's limit.
	union, err := store.ListReceipts(ctx, ports.ReceiptFilter{DispatchID: acceptedDispatchID})
	if err != nil || len(union) != 2 {
		t.Fatalf("union must include acceptance and work receipts: %+v err=%v", union, err)
	}
	limited, err := store.ListReceipts(ctx, ports.ReceiptFilter{DispatchID: acceptedDispatchID, Limit: 1})
	if err != nil || len(limited) != 1 {
		t.Fatalf("union limit must be honored: %+v err=%v", limited, err)
	}

	detail, err := store.LoadReceipt(ctx, "wr-1")
	if err != nil || detail.ReceiptKind != "work" || detail.RunID != "run-1" || detail.ExecutionState != records.ExecSucceeded {
		t.Fatalf("work receipt detail wrong: %+v err=%v", detail, err)
	}

	// Same-second work receipts order newest-first through the
	// descending id tiebreak.
	if err := store.(interface {
		SaveWorkReceipt(tx *sql.Tx, w sqlite.WorkReceiptRecord) error
	}).SaveWorkReceipt(nil, sqlite.WorkReceiptRecord{
		ReceiptID: "wr-2", DispatchID: acceptedDispatchID, RunID: "run-2", ResourceID: "vault-main",
		Status: "failed", FailureCode: "agent_error", SubmittedAt: "2026-08-20T00:02:00Z",
		ValidationState: "valid",
	}); err != nil {
		t.Fatal(err)
	}
	sameSecond, err := store.ListReceipts(ctx, ports.ReceiptFilter{DispatchID: acceptedDispatchID, Kind: "work"})
	if err != nil || len(sameSecond) != 2 || sameSecond[0].ReceiptID != "wr-2" || sameSecond[1].ExecutionState != records.ExecSucceeded {
		t.Fatalf("same-second work receipts must order newest-first with statuses mapped: %+v err=%v", sameSecond, err)
	}
	if _, err := store.LoadReceipt(ctx, "wr-absent"); err == nil || !errors.Is(err, ports.ErrReceiptNotFound) {
		t.Fatalf("unknown receipt must report ErrReceiptNotFound, got %v", err)
	}
}

// TestRefreshSameSecondUniqueReceipts proves same-second refreshes stay
// unique and order newest-first through the nanosecond id.
func TestRefreshSameSecondUniqueReceipts(t *testing.T) {
	base := time.Date(2026, 8, 20, 0, 1, 0, 0, time.UTC)
	current := base
	service := &Service{Store: storeWithAcceptedDispatch(t), Sink: queuedSink{}, Now: func() time.Time { return current }}
	for i := 0; i < 3; i++ {
		current = current.Add(time.Duration(i+1) * 100 * time.Millisecond)
		if _, err := service.Refresh(context.Background(), acceptedDispatchID); err != nil {
			t.Fatalf("same-second refresh %d: %v", i, err)
		}
	}
	projected, err := service.Store.ListReceipts(context.Background(), ports.ReceiptFilter{DispatchID: acceptedDispatchID, Kind: "execution_projection"})
	if err != nil || len(projected) != 3 {
		t.Fatalf("same-second refreshes must append uniquely, got %d (err=%v)", len(projected), err)
	}
	if projected[0].ReceiptID <= projected[1].ReceiptID || projected[1].ReceiptID <= projected[2].ReceiptID {
		t.Fatalf("projection history must order newest first: %+v", projected)
	}
}

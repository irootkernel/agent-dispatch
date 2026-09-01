package notifications

import (
	"context"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// stubSink scripts one outcome per delivery.
type stubSink struct {
	outcomes []records.NotificationAttemptOutcome
	calls    int
	delay    time.Duration
}

func (s *stubSink) Deliver(ctx context.Context, in ports.NotificationDelivery) ports.NotificationAttemptInput {
	i := s.calls
	if i >= len(s.outcomes) {
		i = len(s.outcomes) - 1
	}
	s.calls++
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return ports.NotificationAttemptInput{Outcome: records.NotificationAmbiguousOutcome}
		}
	}
	return ports.NotificationAttemptInput{Outcome: s.outcomes[i]}
}

// fakeDrainStore is an in-memory NotificationDrainStore scripted per
// test: claims come from a due pool, fenced records resolve or
// schedule backoff, releases return claims to the pool.
type fakeDrainStore struct {
	pool     []ports.NotificationClaim
	next     map[string]int64 // notification -> next token
	released []ports.NotificationClaim
	recorded []ports.NotificationAttemptInput
	lost     map[string]bool // notification -> simulate lease loss
}

func newFakeDrainStore(ids ...string) *fakeDrainStore {
	f := &fakeDrainStore{next: map[string]int64{}, lost: map[string]bool{}}
	for _, id := range ids {
		f.pool = append(f.pool, ports.NotificationClaim{NotificationEventRecord: ports.NotificationEventRecord{NotificationID: id, RouteID: "wiki", SinkID: "ops-log", SinkType: "log"}, LeaseToken: 1})
		f.next[id] = 1
	}
	return f
}

func (f *fakeDrainStore) ClaimDueNotifications(ctx context.Context, filter ports.NotificationClaimFilter) ([]ports.NotificationClaim, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	out := f.pool
	if len(out) > limit {
		out = out[:limit]
	}
	f.pool = f.pool[len(out):]
	return out, nil
}

func (f *fakeDrainStore) RecordNotificationAttemptFenced(ctx context.Context, in ports.NotificationAttemptInput, claim ports.NotificationClaim, backoff ports.NotificationBackoff) (ports.NotificationAttemptRecord, error) {
	if f.lost[in.NotificationID] {
		return ports.NotificationAttemptRecord{}, ports.ErrNotificationLeaseLost
	}
	f.recorded = append(f.recorded, in)
	return ports.NotificationAttemptRecord{NotificationID: in.NotificationID, Outcome: in.Outcome}, nil
}

func (f *fakeDrainStore) ReleaseNotificationClaims(ctx context.Context, owner string, claims []ports.NotificationClaim) error {
	f.released = append(f.released, claims...)
	return nil
}

// TestE16T2BudgetExpiryReleasesUnstartedClaims pins the ten-second
// automatic budget posture: at context expiry the pass stops, unstarted
// claims are released, and the budget-expired flag is reported.
func TestE16T2BudgetExpiryReleasesUnstartedClaims(t *testing.T) {
	store := newFakeDrainStore("ntf-1", "ntf-2", "ntf-3")
	sink := &stubSink{outcomes: []records.NotificationAttemptOutcome{records.NotificationDeliveredOutcome}, delay: 60 * time.Millisecond}
	svc := &DrainService{Store: store, Resolver: func(string, ports.NotificationSinkRef) (ports.NotificationSink, error) { return sink, nil }, Limit: 3}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	report, err := svc.DrainDue(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !report.BudgetExpired {
		t.Fatalf("the pass must report budget expiry: %+v", report)
	}
	if len(store.released) == 0 {
		t.Fatal("unstarted claims must be released at expiry")
	}
	if report.Delivered+report.Retryable == 0 {
		t.Fatalf("the pass must have delivered started work: %+v", report)
	}
}

// TestE16T2LeaseLossIsNotAnError pins the recovery posture: a claim
// recovered by another drainer mid-pass is skipped, never surfaced as a
// pass failure.
func TestE16T2LeaseLossIsNotAnError(t *testing.T) {
	store := newFakeDrainStore("ntf-1")
	store.lost["ntf-1"] = true
	sink := &stubSink{outcomes: []records.NotificationAttemptOutcome{records.NotificationDeliveredOutcome}}
	svc := &DrainService{Store: store, Resolver: func(string, ports.NotificationSinkRef) (ports.NotificationSink, error) { return sink, nil }}
	report, err := svc.DrainDue(context.Background(), "")
	if err != nil {
		t.Fatalf("a recovered claim must not fail the pass: %v", err)
	}
	if report.Delivered != 0 || report.Ambiguous != 0 || report.Retryable != 0 || report.Refused != 0 {
		t.Fatalf("a recovered claim counts no outcome here: %+v", report)
	}
}

// TestE16T2UnresolvableSinkStaysLocal pins sink isolation: a defective
// declaration records one retryable attempt and the pass completes.
func TestE16T2UnresolvableSinkStaysLocal(t *testing.T) {
	store := newFakeDrainStore("ntf-1")
	svc := &DrainService{Store: store, Resolver: func(string, ports.NotificationSinkRef) (ports.NotificationSink, error) {
		return nil, context.DeadlineExceeded // any construction failure
	}}
	report, err := svc.DrainDue(context.Background(), "")
	if err != nil {
		t.Fatalf("a defective declaration is sink-local: %v", err)
	}
	if report.Retryable != 1 || len(store.recorded) != 1 || store.recorded[0].ErrorCode != "sink_unresolvable" {
		t.Fatalf("the unresolvable sink must record one retryable attempt: %+v %+v", report, store.recorded)
	}
}

// TestE16T2RetryDefaults pins the documented fallback envelope: a zero
// Retry resolves to 30s/15m/2.0 so every surface observes the same
// defaults the configuration layer documents.
func TestE16T2RetryDefaults(t *testing.T) {
	svc := &DrainService{}
	b := svc.retry()
	if b.Initial != 30*time.Second || b.Max != 15*time.Minute || b.Multiplier != 2.0 {
		t.Fatalf("defaults: %+v", b)
	}
	if svc.limit() != DefaultMaxPerRun {
		t.Fatalf("limit default: %d", svc.limit())
	}
	if svc.owner() == "" {
		t.Fatal("the owner must derive uniquely")
	}
}

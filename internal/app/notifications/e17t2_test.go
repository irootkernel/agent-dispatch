// E17-T2 cold-validation remediations at the service level: the
// mid-pass durable-store failure must release claimed-but-unstarted
// work (round-4 F015), and the per-route backoff resolver must drive
// each claim's persisted envelope (round-4 F002).
package notifications

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// recordingDrainStore extends the e16t2 fake with the envelopes its
// fenced records received and an optional scripted failure after the
// first record.
type recordingDrainStore struct {
	*fakeDrainStore
	backoffs   []ports.NotificationBackoff
	failOnNth  int // 1-based fenced-record index that fails
	recordSeen int
}

func (f *recordingDrainStore) RecordNotificationAttemptFenced(ctx context.Context, in ports.NotificationAttemptInput, claim ports.NotificationClaim, backoff ports.NotificationBackoff) (ports.NotificationAttemptRecord, error) {
	f.recordSeen++
	if f.failOnNth > 0 && f.recordSeen == f.failOnNth {
		return ports.NotificationAttemptRecord{}, errors.New("durable store failed mid-pass")
	}
	f.backoffs = append(f.backoffs, backoff)
	return f.fakeDrainStore.RecordNotificationAttemptFenced(ctx, in, claim, backoff)
}

// TestE17T2MidPassStoreErrorReleasesUnstartedClaims pins round-4 F015:
// a durable-store failure on the first claim's fenced record releases
// the remainder this pass never started — nothing strands until lease
// expiry. The failing claim itself keeps its fence (lease-recoverable),
// and the release carries the SAME owner that claimed.
func TestE17T2MidPassStoreErrorReleasesUnstartedClaims(t *testing.T) {
	inner := newFakeDrainStore("ntf-a", "ntf-b", "ntf-c")
	store := &recordingDrainStore{fakeDrainStore: inner, failOnNth: 1}
	sink := &stubSink{outcomes: []records.NotificationAttemptOutcome{records.NotificationDeliveredOutcome}}
	svc := &DrainService{Store: store, Resolver: func(string, ports.NotificationSinkRef) (ports.NotificationSink, error) { return sink, nil }}
	if _, err := svc.DrainDue(context.Background(), ""); err == nil {
		t.Fatal("the mid-pass store failure must surface as the pass's error")
	}
	// The two unstarted claims returned to the pool under the claiming
	// owner; the failing first claim stays leased (its fence owns the
	// recovery).
	if len(store.released) != 2 {
		t.Fatalf("the unstarted claims must be released, got %d", len(store.released))
	}
	if store.owner["ntf-a"] == "" {
		t.Fatal("the failing claim keeps its fence for lease recovery")
	}
	if store.owner["ntf-b"] != "" || store.owner["ntf-c"] != "" {
		t.Fatalf("released claims must be unowned: %v", store.owner)
	}
}

// TestE17T2PerRouteBackoffDrivesEachClaim pins round-4 F002: when a
// pass spans routes and carries a per-route resolver, each claim's
// fenced record persists ITS OWN route envelope — a custom route keeps
// its custom initial delay and jitter while the unresolved route keeps
// the documented defaults (jitter included).
func TestE17T2PerRouteBackoffDrivesEachClaim(t *testing.T) {
	store := &recordingDrainStore{fakeDrainStore: newFakeDrainStore("ntf-custom", "ntf-default")}
	// The fake seeds every claim on route "wiki"; point one record at a
	// second route through the pool directly.
	store.pool[1].RouteID = "other"
	sink := &stubSink{outcomes: []records.NotificationAttemptOutcome{records.NotificationAmbiguousOutcome, records.NotificationAmbiguousOutcome}}
	svc := &DrainService{
		Store: store, Resolver: func(string, ports.NotificationSinkRef) (ports.NotificationSink, error) { return sink, nil },
		BackoffFor: func(routeID string) ports.NotificationBackoff {
			if routeID == "other" {
				return ports.NotificationBackoff{Initial: 5 * time.Minute, Max: 30 * time.Minute, Multiplier: 3, JitterFraction: 0.4}
			}
			return ports.NotificationBackoff{}
		},
	}
	if _, err := svc.DrainDue(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(store.backoffs) != 2 {
		t.Fatalf("both claims record fenced outcomes: %d", len(store.backoffs))
	}
	custom, fallback := store.backoffs[1], store.backoffs[0]
	if custom.Initial != 5*time.Minute || custom.JitterFraction != 0.4 || custom.Multiplier != 3 {
		t.Fatalf("the custom route's envelope must persist: %+v", custom)
	}
	if fallback.Initial != 30*time.Second || fallback.Max != 15*time.Minute || fallback.Multiplier != 2.0 || fallback.JitterFraction != 0.2 {
		t.Fatalf("the unresolved route keeps the documented defaults (jitter included): %+v", fallback)
	}
}

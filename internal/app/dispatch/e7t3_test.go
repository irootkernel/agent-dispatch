package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/ports"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/fakesink"
)

// E7-T3 round-1 remediation coverage: the stale-rebuild recursion bound
// and the drain's stale-intent warning path.

func alwaysStale(context.Context, ports.IntentSnapshot) (bool, string, error) {
	return true, "test staleness", nil
}

// TestStaleRecursionBounded proves the rebuild recursion is bounded: a
// replacement that is itself stale fails closed instead of looping.
func TestStaleRecursionBounded(t *testing.T) {
	s := openE3T3Store(t)
	seedReadyIntent(t, s, "dispatch-1")
	rt := &Runtime{
		Store: s, Sink: fakesink.New("fake-main"), StalenessCheck: alwaysStale,
		StaleRebuilder: func(context.Context, string) (string, error) { return "dispatch-replacement", nil },
	}
	_, err := rt.submitOnce(context.Background(), "dispatch-1", "p1", 1)
	if !errors.Is(err, ports.ErrStaleRouteRevision) {
		t.Fatalf("the bounded recursion must fail closed with the staleness error: %v", err)
	}
}

// TestDrainWarnsOnUnresolvableStaleIntent proves the drain converts a
// staleness refusal into an operator-visible warning instead of
// aborting the route's work.
func TestDrainWarnsOnUnresolvableStaleIntent(t *testing.T) {
	s := openE3T3Store(t)
	seedReadyIntent(t, s, "dispatch-1")
	rt := &Runtime{Store: s, Sink: fakesink.New("fake-main"), Now: time.Now, LeaseTTL: time.Minute, StalenessCheck: alwaysStale}
	report, err := rt.Drain(context.Background(), "wiki-maintenance", 5, s)
	if err != nil {
		t.Fatalf("the drain must not abort on an unresolvable stale intent: %v", err)
	}
	if report.Skipped != 1 || len(report.Warnings) != 1 {
		t.Fatalf("the stale intent must be skipped with one warning: %+v", report)
	}
	if report.Processed != 0 {
		t.Fatalf("nothing may be submitted: %+v", report)
	}
}

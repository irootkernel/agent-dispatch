package dispatch

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// fakeSiblingStore stages per-destination CommitFanoutChild outcomes for
// the reconcile sibling-commit policy test (E12 epic whole-review round
// 1): held lanes refuse with the typed slot error, failing lanes refuse
// with their staged error, everything else commits.
type fakeSiblingStore struct {
	held      map[string]bool
	failures  map[string]error
	committed []string
}

func (f *fakeSiblingStore) CommitFanoutChild(ctx context.Context, intent ports.IntentInput) error {
	dest := intent.Fanout.DestinationID
	if err := f.failures[dest]; err != nil {
		return err
	}
	if f.held[dest] {
		return fmt.Errorf("route wiki lane %s: %w", dest, ports.ErrRouteSlotHeld)
	}
	f.committed = append(f.committed, dest)
	return nil
}

func siblingIntent(dispatchID, destinationID string, withFanout bool) ports.IntentInput {
	intent := ports.IntentInput{DispatchID: dispatchID, RouteID: "wiki"}
	if withFanout {
		intent.Fanout = SingleLaneFanout("agg-1", records.OriginReconcile,
			ports.TaskDestinationRef{ID: destinationID, Revision: "dst-x", Workstream: "ws"},
			"reconcile:test", nil)
	}
	return intent
}

// TestCommitReconcileSiblingsPolicyIsolatesLanes pins the app-layer
// sibling-commit policy (E12 epic whole-review round 1, FAN-002/CON-007):
// one lane's outcome never blocks the others — a healthy lane commits, a
// slot-held lane skips with its note (warning class), a lane whose
// identical child already exists skips the same way (the retry shape's
// duplicate idempotency key, round 2), a lane failing for any other
// reason records its live error (failure class), and a sibling without a
// fanout block fails closed instead of panicking.
func TestCommitReconcileSiblingsPolicyIsolatesLanes(t *testing.T) {
	store := &fakeSiblingStore{
		held: map[string]bool{"beta": true},
		failures: map[string]error{
			"gamma": errors.New("disk I/O error"),
			"delta": fmt.Errorf("route wiki lane delta: %w", ports.ErrIdempotencyConflict),
		},
	}
	out := CommitReconcileSiblings(context.Background(), store, []ports.IntentInput{
		siblingIntent("disp-alpha", "alpha", true),
		siblingIntent("disp-beta", "beta", true),
		siblingIntent("disp-gamma", "gamma", true),
		siblingIntent("disp-delta", "delta", true),
		siblingIntent("disp-legacy", "", false),
	})
	if len(out) != 5 || len(store.committed) != 1 || store.committed[0] != "alpha" {
		t.Fatalf("exactly the healthy lane commits: %+v committed=%v", out, store.committed)
	}
	if !out[0].Committed || out[0].Note != "" || out[0].Err != nil || out[0].DestinationID != "alpha" || out[0].DispatchID != "disp-alpha" {
		t.Fatalf("the healthy sibling commits cleanly: %+v", out[0])
	}
	if out[1].Committed || out[1].Err != nil || out[1].Note == "" {
		t.Fatalf("a slot-held sibling skips with its note, never an error: %+v", out[1])
	}
	if out[2].Committed || out[2].Err == nil || out[2].Note != "" {
		t.Fatalf("a failed sibling keeps its live error for the exit policy: %+v", out[2])
	}
	if out[3].Committed || out[3].Err != nil || out[3].Note == "" {
		t.Fatalf("a duplicate-key sibling skips with its note (the retry shape), never an error: %+v", out[3])
	}
	if out[4].Committed || out[4].Err == nil {
		t.Fatalf("a sibling without a fanout block fails closed: %+v", out[4])
	}
}

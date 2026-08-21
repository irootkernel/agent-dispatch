package reconcile

import (
	"errors"
	"fmt"
	"testing"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/domain/state"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// TestClassifyResolutionError pins the two resolution-failure classes:
// the typed eligibility refusal (a lost race) stays a conflict-class
// error, everything durable-store wraps as storage.
func TestClassifyResolutionError(t *testing.T) {
	refusal := fmt.Errorf("%w: route wiki is ACTIVE_CLEAN, not uncertain", ports.ErrStateNotEligible)
	err := classifyResolutionError(refusal)
	if !errors.Is(err, ports.ErrStateNotEligible) {
		t.Fatalf("the eligibility refusal must pass through, got %v", err)
	}
	var storeErr *ports.StoreError
	if errors.As(err, &storeErr) {
		t.Fatalf("a lost resolution race must not classify as storage: %v", err)
	}

	fence := classifyResolutionError(fmt.Errorf("%w: route wiki changed during reconciliation", ports.ErrGenerationConflict))
	if !errors.Is(fence, ports.ErrGenerationConflict) || errors.As(fence, &storeErr) {
		t.Fatalf("the generation fence conflict must pass through unwrapped, got %v", fence)
	}

	wrapped := classifyResolutionError(errors.New("disk I/O error"))
	if !errors.As(wrapped, &storeErr) {
		t.Fatalf("a store failure must wrap as the typed store error, got %v", wrapped)
	}
}

// TestIntentBuilderRequired pins the uniform builder contract: due work
// on every eligible route state fails loudly without a builder.
func TestIntentBuilderRequired(t *testing.T) {
	for _, snapState := range []state.RouteState{state.RouteIdle, state.RouteUncertain} {
		if err := intentBuilderRequired("wiki", snapState, nil); err == nil {
			t.Fatalf("due work on %s without a builder must fail loudly", snapState)
		}
	}
	if err := intentBuilderRequired("wiki", state.RouteIdle, func(routeID, reason, decisionID string, changes []records.ChangeItem) (ports.IntentInput, error) {
		return ports.IntentInput{}, nil
	}); err != nil {
		t.Fatalf("a wired builder must pass: %v", err)
	}
}

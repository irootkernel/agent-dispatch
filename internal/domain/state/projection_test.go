package state

import (
	"errors"
	"testing"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
)

// TestAcceptanceTransitionAxis verifies every acceptance outcome maps to
// exactly its documented transition and never infers execution.
func TestAcceptanceTransitionAxis(t *testing.T) {
	cases := []struct {
		in     records.AcceptanceState
		to     records.IntentState
		reason IntentReason
	}{
		{records.AcceptanceAccepted, records.IntentAccepted, ReasonDurableAcceptance},
		{records.AcceptanceRejected, records.IntentRejected, ReasonDefiniteRejection},
		{records.AcceptanceUnknown, records.IntentUnknown, ReasonAmbiguousOutcome},
	}
	for _, c := range cases {
		to, reason, err := AcceptanceTransition(c.in)
		if err != nil {
			t.Fatalf("AcceptanceTransition(%s): %v", c.in, err)
		}
		if to != c.to || reason != c.reason {
			t.Errorf("AcceptanceTransition(%s) = %s/%s, want %s/%s", c.in, to, reason, c.to, c.reason)
		}
	}
	if _, _, err := AcceptanceTransition(records.AcceptanceState("maybe")); err == nil {
		t.Error("unknown acceptance values must fail closed")
	}
}

// TestExecutionTransitionAxis verifies terminal projections map to their
// documented transitions and non-terminal projections prove nothing:
// accepted does not imply succeeded (persistence §4).
func TestExecutionTransitionAxis(t *testing.T) {
	terminal := []struct {
		in     records.ExecutionState
		to     records.IntentState
		reason IntentReason
	}{
		{records.ExecSucceeded, records.IntentCompleted, ReasonExecutionSucceeded},
		{records.ExecFailed, records.IntentFailed, ReasonExecutionFailed},
		{records.ExecCanceled, records.IntentCanceled, ReasonTargetCanceled},
	}
	for _, c := range terminal {
		to, reason, err := ExecutionTransition(c.in)
		if err != nil {
			t.Fatalf("ExecutionTransition(%s): %v", c.in, err)
		}
		if to != c.to || reason != c.reason {
			t.Errorf("ExecutionTransition(%s) = %s/%s, want %s/%s", c.in, to, reason, c.to, c.reason)
		}
	}
	for _, in := range []records.ExecutionState{records.ExecUnavailable, records.ExecQueued, records.ExecRunning} {
		_, _, err := ExecutionTransition(in)
		if !errors.Is(err, ErrExecutionNotTerminal) {
			t.Errorf("ExecutionTransition(%s) must report a non-terminal projection, got %v", in, err)
		}
	}
	if _, _, err := ExecutionTransition(records.ExecutionState("finished")); err == nil {
		t.Error("unknown execution values must fail closed")
	}
}

// TestProjectionAxesStaySeparate walks the full acceptance x execution
// grid and verifies the separation invariants: an accepted dispatch with a
// non-terminal or absent execution projection never reports an execution
// outcome, and no combination ever infers acceptance from execution.
func TestProjectionAxesStaySeparate(t *testing.T) {
	acceptanceToIntent := map[records.AcceptanceState]records.IntentState{
		records.AcceptanceAccepted: records.IntentAccepted,
		records.AcceptanceRejected: records.IntentRejected,
		records.AcceptanceUnknown:  records.IntentUnknown,
	}
	for _, a := range []records.AcceptanceState{records.AcceptanceAccepted, records.AcceptanceRejected, records.AcceptanceUnknown} {
		for _, e := range []records.ExecutionState{records.ExecUnavailable, records.ExecQueued, records.ExecRunning} {
			p := Projection{Acceptance: a, Execution: e}
			// The acceptance axis is authoritative for the dispatch state;
			// a non-terminal projection must not change it.
			if _, _, err := ExecutionTransition(p.Execution); !errors.Is(err, ErrExecutionNotTerminal) {
				t.Errorf("projection %s/%s: non-terminal execution must not justify a transition", a, e)
			}
			to, reason, err := AcceptanceTransition(p.Acceptance)
			if err != nil {
				t.Fatalf("AcceptanceTransition(%s): %v", a, err)
			}
			if to != acceptanceToIntent[a] {
				t.Errorf("projection %s/%s: acceptance axis changed the state to %s", a, e, to)
			}
			if reason == ReasonExecutionSucceeded || reason == ReasonExecutionFailed || reason == ReasonTargetCanceled {
				t.Errorf("projection %s/%s: acceptance derived an execution reason %s", a, e, reason)
			}
		}
	}
	// Accepted plus a terminal projection is the only execution path.
	to, reason, err := ExecutionTransition(records.ExecSucceeded)
	if err != nil || to != records.IntentCompleted || reason != ReasonExecutionSucceeded {
		t.Errorf("accepted work projected succeeded must complete: %v %s %v", to, reason, err)
	}
}

package state

import (
	"fmt"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
)

// Projection is the paired target projection axes of one dispatch
// (persistence-and-state-machines §4): a dispatch can be accepted while
// execution is queued, running, or unavailable, so acceptance never
// implies execution success and execution never proves acceptance. An
// empty axis means the receipt did not carry it; each axis is parsed
// fail-closed before it reaches this type.
type Projection struct {
	Acceptance records.AcceptanceState
	Execution  records.ExecutionState
}

// ErrExecutionNotTerminal reports an execution projection that proves
// nothing terminal: unavailable, queued, and running leave the dispatch
// accepted (accepted does not imply succeeded).
var ErrExecutionNotTerminal = fmt.Errorf("execution projection is not terminal; acceptance does not imply execution success")

// AcceptanceTransition returns the intent transition a durable acceptance
// receipt justifies from submitting or reconciling. Every outcome maps to
// exactly its documented state and reason; acceptance is proved by a
// receipt, never by silence, and never by any execution stage.
func AcceptanceTransition(a records.AcceptanceState) (records.IntentState, IntentReason, error) {
	switch a {
	case records.AcceptanceAccepted:
		return records.IntentAccepted, ReasonDurableAcceptance, nil
	case records.AcceptanceRejected:
		return records.IntentRejected, ReasonDefiniteRejection, nil
	case records.AcceptanceUnknown:
		return records.IntentUnknown, ReasonAmbiguousOutcome, nil
	}
	return records.IntentReady, "", fmt.Errorf("unknown acceptance state %q", string(a))
}

// ExecutionTransition returns the intent transition an execution
// projection justifies from accepted. Succeeded, failed, and canceled are
// terminal projections; unavailable, queued, and running prove nothing and
// return ErrExecutionNotTerminal so callers record the projection without
// transitioning the dispatch state. Execution evidence alone never proves
// the dispatch was accepted.
func ExecutionTransition(e records.ExecutionState) (records.IntentState, IntentReason, error) {
	switch e {
	case records.ExecSucceeded:
		return records.IntentCompleted, ReasonExecutionSucceeded, nil
	case records.ExecFailed:
		return records.IntentFailed, ReasonExecutionFailed, nil
	case records.ExecCanceled:
		return records.IntentCanceled, ReasonTargetCanceled, nil
	case records.ExecUnavailable, records.ExecQueued, records.ExecRunning:
		return records.IntentAccepted, "", ErrExecutionNotTerminal
	}
	return records.IntentAccepted, "", fmt.Errorf("unknown execution state %q", string(e))
}

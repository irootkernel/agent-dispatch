package state

import (
	"fmt"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// IntentReason is the typed cause recorded with one dispatch-intent
// transition. Reasons are part of the durable audit context (DUR-011) and
// carry the operator-facing explanation the dispatches commands report.
type IntentReason string

const (
	ReasonLeaseAcquired       IntentReason = "lease_acquired"
	ReasonDurableAcceptance   IntentReason = "durable_acceptance"
	ReasonDefiniteRejection   IntentReason = "definite_rejection"
	ReasonAmbiguousOutcome    IntentReason = "ambiguous_outcome"
	ReasonTransientFailure    IntentReason = "transient_failure"
	ReasonRetryDue            IntentReason = "retry_due"
	ReasonLookupStarted       IntentReason = "lookup_started"
	ReasonLookupFoundAccepted IntentReason = "lookup_found_accepted"
	ReasonLookupNotAccepted   IntentReason = "lookup_proven_not_accepted"
	ReasonUnresolvedOrLimit   IntentReason = "unresolved_or_limit_reached"
	ReasonTerminalPolicy      IntentReason = "terminal_policy"
	ReasonRouteInvalidated    IntentReason = "route_revision_invalidated"
	ReasonExecutionSucceeded  IntentReason = "execution_succeeded"
	ReasonExecutionFailed     IntentReason = "execution_failed"
	ReasonTargetCanceled      IntentReason = "target_canceled"
	ReasonExplicitRetry       IntentReason = "explicit_retry"
	ReasonReprocessOrDiscard  IntentReason = "reprocess_or_discard"
)

// AllIntentReasons returns every declared intent reason in stable order.
func AllIntentReasons() []IntentReason {
	return []IntentReason{
		ReasonLeaseAcquired,
		ReasonDurableAcceptance,
		ReasonDefiniteRejection,
		ReasonAmbiguousOutcome,
		ReasonTransientFailure,
		ReasonRetryDue,
		ReasonLookupStarted,
		ReasonLookupFoundAccepted,
		ReasonLookupNotAccepted,
		ReasonUnresolvedOrLimit,
		ReasonTerminalPolicy,
		ReasonRouteInvalidated,
		ReasonExecutionSucceeded,
		ReasonExecutionFailed,
		ReasonTargetCanceled,
		ReasonExplicitRetry,
		ReasonReprocessOrDiscard,
	}
}

// intentEdge identifies one directed edge of the dispatch state machine.
type intentEdge struct {
	from, to records.IntentState
}

// intentTable is the dispatch state machine (persistence-and-state-machines
// §3): exactly the documented edges, each with its documented reasons.
var intentTable = map[intentEdge][]IntentReason{
	{records.IntentReady, records.IntentSubmitting}:         {ReasonLeaseAcquired},
	{records.IntentSubmitting, records.IntentAccepted}:      {ReasonDurableAcceptance},
	{records.IntentSubmitting, records.IntentRejected}:      {ReasonDefiniteRejection},
	{records.IntentSubmitting, records.IntentUnknown}:       {ReasonAmbiguousOutcome},
	{records.IntentSubmitting, records.IntentRetryWait}:     {ReasonTransientFailure},
	{records.IntentRetryWait, records.IntentSubmitting}:     {ReasonRetryDue},
	{records.IntentUnknown, records.IntentReconciling}:      {ReasonLookupStarted},
	{records.IntentReconciling, records.IntentAccepted}:     {ReasonLookupFoundAccepted},
	{records.IntentReconciling, records.IntentRetryWait}:    {ReasonLookupNotAccepted},
	{records.IntentReconciling, records.IntentDeadLettered}: {ReasonUnresolvedOrLimit},
	{records.IntentRejected, records.IntentDeadLettered}:    {ReasonTerminalPolicy},
	{records.IntentReady, records.IntentSuperseded}:         {ReasonRouteInvalidated},
	{records.IntentAccepted, records.IntentCompleted}:       {ReasonExecutionSucceeded},
	{records.IntentAccepted, records.IntentFailed}:          {ReasonExecutionFailed},
	{records.IntentAccepted, records.IntentCanceled}:        {ReasonTargetCanceled},
	{records.IntentDeadLettered, records.IntentReady}:       {ReasonExplicitRetry},
	{records.IntentDeadLettered, records.IntentSuperseded}:  {ReasonReprocessOrDiscard},
}

// AllIntentStates returns every dispatch-intent state in schema order.
// The set matches the dispatch-intent contract's state enum; the lockstep
// test fails when either side drifts.
func AllIntentStates() []records.IntentState {
	return []records.IntentState{
		records.IntentReady,
		records.IntentSubmitting,
		records.IntentAccepted,
		records.IntentRejected,
		records.IntentUnknown,
		records.IntentRetryWait,
		records.IntentReconciling,
		records.IntentDeadLettered,
		records.IntentSuperseded,
		records.IntentCompleted,
		records.IntentFailed,
		records.IntentCanceled,
	}
}

// CanTransitionIntent reports whether the dispatch state machine declares
// the edge, ignoring guards. This is the single authority the SQLite
// adapter delegates to for transactional validation (DUR-011).
func CanTransitionIntent(from, to records.IntentState) bool {
	_, ok := intentTable[intentEdge{from, to}]
	return ok
}

// IntentReasons returns the documented reasons for one declared edge.
func IntentReasons(from, to records.IntentState) ([]IntentReason, bool) {
	reasons, ok := intentTable[intentEdge{from, to}]
	return reasons, ok
}

// LeaseEvidence proves one process owns an unexpired attempt lease
// (persistence-and-state-machines §5).
type LeaseEvidence struct {
	Owner     string
	ExpiresAt string
}

// ReconciliationEvidence records what a target lookup proved about an
// unknown submission (feedback-loop-and-reconciliation §9).
type ReconciliationEvidence struct {
	// Result is the lookup outcome: accepted proves the task exists,
	// rejected proves it was never accepted, unknown proves nothing.
	Result records.AcceptanceState
	// ExternalRef identifies the looked-up task when Result is accepted.
	ExternalRef string
	// AttemptsExhausted marks that the persisted retry limit was reached,
	// the second dead-letter justification alongside an unresolved lookup.
	AttemptsExhausted bool
}

// IntentEvidence carries the typed proof a guarded edge demands. The zero
// value satisfies only unguarded edges; guards fail closed.
type IntentEvidence struct {
	// Actor is the operator or process asserting the transition.
	Actor string
	// Lease proves submission ownership for edges entering submitting.
	Lease *LeaseEvidence
	// Reconciliation proves what a lookup found for edges leaving
	// reconciling.
	Reconciliation *ReconciliationEvidence
	// ExplicitRetry marks an operator-authorized retry of dead-lettered
	// work (DEAD_LETTERED -> READY).
	ExplicitRetry bool
	// ReceiptRef references the durable receipt that proves an
	// acceptance, rejection, or execution projection claim.
	ReceiptRef string
}

// TransitionError reports one rejected transition with its guard detail.
// The CLI error model maps it to transition_invalid (conflict, exit 14).
type TransitionError struct {
	Entity string // "dispatch_intent" or "route"
	From   string
	To     string
	Reason string
	Detail string
}

func (e *TransitionError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("%s transition %s -> %s (%s) rejected: %s", e.Entity, e.From, e.To, e.Reason, e.Detail)
	}
	return fmt.Sprintf("%s transition %s -> %s rejected: %s", e.Entity, e.From, e.To, e.Detail)
}

func intentRejected(from, to records.IntentState, reason IntentReason, detail string) error {
	return &TransitionError{
		Entity: "dispatch_intent",
		From:   string(from),
		To:     string(to),
		Reason: string(reason),
		Detail: detail,
	}
}

// ValidateIntentTransition applies the dispatch table and every guard: the
// edge must exist, the reason must be documented for that edge, and the
// typed evidence must satisfy the edge's guards. A nil error is the
// validated transition the audit history records (DUR-011). In particular
// `unknown` can never become `ready`: the machine has no such edge, and
// leaving `unknown` always requires reconciliation evidence.
func ValidateIntentTransition(from, to records.IntentState, reason IntentReason, ev IntentEvidence) error {
	reasons, ok := intentTable[intentEdge{from, to}]
	if !ok {
		return intentRejected(from, to, reason, "edge is not part of the dispatch state machine")
	}
	documented := false
	for _, r := range reasons {
		if r == reason {
			documented = true
			break
		}
	}
	if !documented {
		return intentRejected(from, to, reason, fmt.Sprintf("reason is not documented for this edge (allowed: %v)", reasons))
	}
	switch edge := (intentEdge{from, to}); edge {
	case (intentEdge{records.IntentReady, records.IntentSubmitting}),
		(intentEdge{records.IntentRetryWait, records.IntentSubmitting}):
		if ev.Lease == nil || ev.Lease.Owner == "" || ev.Lease.ExpiresAt == "" {
			return intentRejected(from, to, reason, "entering submitting requires attempt-lease evidence with owner and expiry")
		}
	case (intentEdge{records.IntentSubmitting, records.IntentAccepted}):
		if ev.ReceiptRef == "" {
			return intentRejected(from, to, reason, "durable acceptance requires a receipt reference")
		}
	case (intentEdge{records.IntentSubmitting, records.IntentRejected}):
		if ev.ReceiptRef == "" {
			return intentRejected(from, to, reason, "definite rejection requires a receipt reference")
		}
	case (intentEdge{records.IntentUnknown, records.IntentReconciling}):
		if ev.Actor == "" {
			return intentRejected(from, to, reason, "starting reconciliation requires an actor")
		}
	case (intentEdge{records.IntentReconciling, records.IntentAccepted}):
		if ev.Reconciliation == nil || ev.Reconciliation.Result != records.AcceptanceAccepted {
			return intentRejected(from, to, reason, "only a lookup that found the task accepted can prove acceptance")
		}
	case (intentEdge{records.IntentReconciling, records.IntentRetryWait}):
		if ev.Reconciliation == nil || ev.Reconciliation.Result != records.AcceptanceRejected {
			return intentRejected(from, to, reason, "retrying after reconciliation requires proof the target did not accept")
		}
	case (intentEdge{records.IntentReconciling, records.IntentDeadLettered}):
		unresolved := ev.Reconciliation != nil && ev.Reconciliation.Result == records.AcceptanceUnknown
		exhausted := ev.Reconciliation != nil && ev.Reconciliation.AttemptsExhausted
		if !unresolved && !exhausted {
			return intentRejected(from, to, reason, "dead lettering requires an unresolved lookup or an exhausted retry limit")
		}
	case (intentEdge{records.IntentAccepted, records.IntentCompleted}),
		(intentEdge{records.IntentAccepted, records.IntentFailed}),
		(intentEdge{records.IntentAccepted, records.IntentCanceled}):
		if ev.ReceiptRef == "" {
			return intentRejected(from, to, reason, "execution projection requires a receipt reference")
		}
	case (intentEdge{records.IntentDeadLettered, records.IntentReady}):
		if !ev.ExplicitRetry || ev.Actor == "" {
			return intentRejected(from, to, reason, "re-entering ready requires an explicit operator retry with an actor")
		}
	}
	return nil
}

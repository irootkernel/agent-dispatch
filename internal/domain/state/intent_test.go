package state

import (
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// documentIntentTable restates the dispatch state machine independently
// from persistence-and-state-machines §3 so the exhaustive matrix test
// catches drift between the SOT and the package table.
var documentIntentTable = map[string][]string{
	"ready":         {"submitting", "superseded"},
	"submitting":    {"accepted", "rejected", "unknown", "retry_wait"},
	"retry_wait":    {"submitting"},
	"unknown":       {"reconciling"},
	"reconciling":   {"accepted", "retry_wait", "dead_lettered"},
	"rejected":      {"dead_lettered"},
	"accepted":      {"completed", "failed", "canceled"},
	"dead_lettered": {"ready", "superseded"},
}

// TestIntentTransitionMatrixExhaustive walks every state pair: exactly the
// documented edges are allowed, every other pair (including unknown ->
// ready) is forbidden, and terminal states have no outgoing edge (TST-001).
func TestIntentTransitionMatrixExhaustive(t *testing.T) {
	terminal := map[records.IntentState]bool{
		records.IntentSuperseded: true,
		records.IntentCompleted:  true,
		records.IntentFailed:     true,
		records.IntentCanceled:   true,
	}
	for _, from := range AllIntentStates() {
		documented, inSOT := documentIntentTable[string(from)]
		if terminal[from] && inSOT {
			t.Errorf("terminal state %s must not appear as a source in the SOT table", from)
		}
		if !terminal[from] && !inSOT {
			t.Errorf("non-terminal state %s must have outgoing transitions in the SOT table", from)
		}
		for _, to := range AllIntentStates() {
			want := false
			for _, d := range documented {
				if d == string(to) {
					want = true
				}
			}
			if got := CanTransitionIntent(from, to); got != want {
				t.Errorf("CanTransitionIntent(%s, %s) = %v, want %v (SOT §3)", from, to, got, want)
			}
			if terminal[from] && CanTransitionIntent(from, to) {
				t.Errorf("terminal state %s must have no outgoing transitions", from)
			}
		}
	}
	// The table alphabet plus the initial state covers every contract state.
	seen := map[records.IntentState]bool{records.IntentReady: true}
	for edge := range intentTable {
		seen[edge.to] = true
		seen[edge.from] = true
	}
	for _, s := range AllIntentStates() {
		if !seen[s] {
			t.Errorf("state %s is declared but unreachable in the transition table", s)
		}
	}
}

// TestIntentReasonsMatchEdges verifies every allowed edge accepts only its
// documented reasons and rejects every other declared reason.
func TestIntentReasonsMatchEdges(t *testing.T) {
	satisfying := map[intentEdge]IntentEvidence{
		{records.IntentReady, records.IntentSubmitting}:         {Lease: &LeaseEvidence{Owner: "p1", ExpiresAt: "2026-08-20T00:01:00Z"}},
		{records.IntentSubmitting, records.IntentAccepted}:      {ReceiptRef: "receipt-1"},
		{records.IntentSubmitting, records.IntentRejected}:      {ReceiptRef: "receipt-1"},
		{records.IntentSubmitting, records.IntentUnknown}:       {},
		{records.IntentSubmitting, records.IntentRetryWait}:     {},
		{records.IntentRetryWait, records.IntentSubmitting}:     {Lease: &LeaseEvidence{Owner: "p1", ExpiresAt: "2026-08-20T00:01:00Z"}},
		{records.IntentUnknown, records.IntentReconciling}:      {Actor: "reconciler"},
		{records.IntentReconciling, records.IntentAccepted}:     {Reconciliation: &ReconciliationEvidence{Result: records.AcceptanceAccepted}},
		{records.IntentReconciling, records.IntentRetryWait}:    {Reconciliation: &ReconciliationEvidence{Result: records.AcceptanceRejected}},
		{records.IntentReconciling, records.IntentDeadLettered}: {Reconciliation: &ReconciliationEvidence{Result: records.AcceptanceUnknown}},
		{records.IntentRejected, records.IntentDeadLettered}:    {},
		{records.IntentReady, records.IntentSuperseded}:         {},
		{records.IntentAccepted, records.IntentCompleted}:       {ReceiptRef: "receipt-2"},
		{records.IntentAccepted, records.IntentFailed}:          {ReceiptRef: "receipt-2"},
		{records.IntentAccepted, records.IntentCanceled}:        {ReceiptRef: "receipt-2"},
		{records.IntentDeadLettered, records.IntentReady}:       {ExplicitRetry: true, Actor: "operator"},
		{records.IntentDeadLettered, records.IntentSuperseded}:  {},
	}
	for edge, documented := range intentTable {
		ev := satisfying[edge]
		for _, r := range documented {
			if err := ValidateIntentTransition(edge.from, edge.to, r, ev); err != nil {
				t.Errorf("ValidateIntentTransition(%s -> %s, %s) with satisfying evidence: %v", edge.from, edge.to, r, err)
			}
		}
		for _, r := range AllIntentReasons() {
			allowed := false
			for _, d := range documented {
				if d == r {
					allowed = true
				}
			}
			err := ValidateIntentTransition(edge.from, edge.to, r, ev)
			if allowed && err != nil {
				t.Errorf("documented reason %s rejected on %s -> %s: %v", r, edge.from, edge.to, err)
			}
			if !allowed && err == nil {
				t.Errorf("reason %s must be rejected on edge %s -> %s", r, edge.from, edge.to)
			}
		}
	}
}

// TestIntentGuardUnknownCannotBecomeReady proves the acceptance rule
// structurally: no reason and no evidence combination moves unknown to
// ready; leaving unknown always requires reconciliation evidence.
func TestIntentGuardUnknownCannotBecomeReady(t *testing.T) {
	if CanTransitionIntent(records.IntentUnknown, records.IntentReady) {
		t.Fatal("unknown -> ready must not be a declared edge")
	}
	fullEvidence := IntentEvidence{
		Actor: "operator",
		Lease: &LeaseEvidence{Owner: "p1", ExpiresAt: "2026-08-20T00:01:00Z"},
		Reconciliation: &ReconciliationEvidence{
			Result:            records.AcceptanceAccepted,
			ExternalRef:       "task-1",
			AttemptsExhausted: true,
		},
		ExplicitRetry: true,
		ReceiptRef:    "receipt-1",
	}
	for _, r := range AllIntentReasons() {
		if err := ValidateIntentTransition(records.IntentUnknown, records.IntentReady, r, fullEvidence); err == nil {
			t.Fatalf("unknown -> ready must fail even with full evidence and reason %s", r)
		}
	}
	// The only way back to ready is an explicit operator retry of
	// dead-lettered work; reconciliation evidence alone does not unlock it.
	if err := ValidateIntentTransition(records.IntentDeadLettered, records.IntentReady, ReasonExplicitRetry, IntentEvidence{
		Reconciliation: &ReconciliationEvidence{Result: records.AcceptanceUnknown},
	}); err == nil {
		t.Fatal("dead_lettered -> ready without an explicit retry must fail")
	}
}

// TestIntentGuardLeaseEvidence verifies both edges entering submitting
// require attempt-lease evidence under their documented reasons.
func TestIntentGuardLeaseEvidence(t *testing.T) {
	cases := []struct {
		from   records.IntentState
		reason IntentReason
	}{
		{records.IntentReady, ReasonLeaseAcquired},
		{records.IntentRetryWait, ReasonRetryDue},
	}
	for _, c := range cases {
		if err := ValidateIntentTransition(c.from, records.IntentSubmitting, c.reason, IntentEvidence{}); err == nil {
			t.Fatalf("%s -> submitting without lease evidence must fail", c.from)
		}
		if err := ValidateIntentTransition(c.from, records.IntentSubmitting, c.reason, IntentEvidence{Lease: &LeaseEvidence{Owner: "p1"}}); err == nil {
			t.Fatalf("%s -> submitting with an expiry-less lease must fail", c.from)
		}
		if err := ValidateIntentTransition(c.from, records.IntentSubmitting, c.reason, IntentEvidence{Lease: &LeaseEvidence{Owner: "p1", ExpiresAt: "2026-08-20T00:01:00Z"}}); err != nil {
			t.Fatalf("%s -> submitting with lease evidence: %v", c.from, err)
		}
	}
}

// TestIntentGuardReconciliationEvidence verifies every edge leaving
// reconciling demands lookup proof matching its destination.
func TestIntentGuardReconciliationEvidence(t *testing.T) {
	cases := []struct {
		to       records.IntentState
		reason   IntentReason
		ev       IntentEvidence
		ok       bool
		fragment string
	}{
		{records.IntentAccepted, ReasonLookupFoundAccepted, IntentEvidence{Reconciliation: &ReconciliationEvidence{Result: records.AcceptanceRejected}}, false, "found the task accepted"},
		{records.IntentAccepted, ReasonLookupFoundAccepted, IntentEvidence{Reconciliation: &ReconciliationEvidence{Result: records.AcceptanceAccepted}}, true, ""},
		{records.IntentRetryWait, ReasonLookupNotAccepted, IntentEvidence{Reconciliation: &ReconciliationEvidence{Result: records.AcceptanceAccepted}}, false, "did not accept"},
		{records.IntentRetryWait, ReasonLookupNotAccepted, IntentEvidence{Reconciliation: &ReconciliationEvidence{Result: records.AcceptanceRejected}}, true, ""},
		{records.IntentDeadLettered, ReasonUnresolvedOrLimit, IntentEvidence{}, false, "unresolved lookup"},
		{records.IntentDeadLettered, ReasonUnresolvedOrLimit, IntentEvidence{Reconciliation: &ReconciliationEvidence{Result: records.AcceptanceUnknown}}, true, ""},
		{records.IntentDeadLettered, ReasonUnresolvedOrLimit, IntentEvidence{Reconciliation: &ReconciliationEvidence{AttemptsExhausted: true}}, true, ""},
	}
	for _, c := range cases {
		err := ValidateIntentTransition(records.IntentReconciling, c.to, c.reason, c.ev)
		if c.ok && err != nil {
			t.Errorf("reconciling -> %s with matching evidence: %v", c.to, err)
		}
		if !c.ok {
			if err == nil {
				t.Errorf("reconciling -> %s with mismatched evidence must fail", c.to)
			} else if !strings.Contains(err.Error(), c.fragment) {
				t.Errorf("reconciling -> %s error should mention %q: %v", c.to, c.fragment, err)
			}
		}
	}
}

// TestIntentGuardReceiptReferences verifies acceptance, rejection, and
// execution projections require durable receipt references.
func TestIntentGuardReceiptReferences(t *testing.T) {
	cases := []struct {
		from, to records.IntentState
		reason   IntentReason
	}{
		{records.IntentSubmitting, records.IntentAccepted, ReasonDurableAcceptance},
		{records.IntentSubmitting, records.IntentRejected, ReasonDefiniteRejection},
		{records.IntentAccepted, records.IntentCompleted, ReasonExecutionSucceeded},
		{records.IntentAccepted, records.IntentFailed, ReasonExecutionFailed},
		{records.IntentAccepted, records.IntentCanceled, ReasonTargetCanceled},
	}
	for _, c := range cases {
		if err := ValidateIntentTransition(c.from, c.to, c.reason, IntentEvidence{}); err == nil {
			t.Errorf("%s -> %s without a receipt reference must fail", c.from, c.to)
		}
		if err := ValidateIntentTransition(c.from, c.to, c.reason, IntentEvidence{ReceiptRef: "receipt-1"}); err != nil {
			t.Errorf("%s -> %s with a receipt reference: %v", c.from, c.to, err)
		}
	}
}

// TestIntentTransitionErrorShape verifies the typed error carries the
// entity, edge, and reason the audit and CLI layers report.
func TestIntentTransitionErrorShape(t *testing.T) {
	err := ValidateIntentTransition(records.IntentReady, records.IntentCompleted, ReasonExecutionSucceeded, IntentEvidence{})
	te, ok := err.(*TransitionError)
	if !ok {
		t.Fatalf("expected *TransitionError, got %T", err)
	}
	if te.Entity != "dispatch_intent" || te.From != "ready" || te.To != "completed" {
		t.Fatalf("unexpected error identity: %+v", te)
	}
	if !strings.Contains(err.Error(), "dispatch_intent transition ready -> completed") {
		t.Fatalf("unexpected message: %v", err)
	}
}

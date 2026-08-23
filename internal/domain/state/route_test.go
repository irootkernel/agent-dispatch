package state

import (
	"strings"
	"testing"
)

// documentRouteTable restates the route state machine independently from
// persistence-and-state-machines §6 so the exhaustive matrix test catches
// drift between the SOT and the package table.
var documentRouteTable = map[RouteState][]RouteState{
	RouteIdle:          {RouteActiveClean},
	RouteActiveClean:   {RouteActiveDirty, RouteIdle, RouteFollowupReady, RouteUncertain},
	RouteActiveDirty:   {RouteActiveDirty, RouteFollowupReady, RouteUncertain, RouteIdle},
	RouteFollowupReady: {RouteActiveClean, RouteActiveDirty, RouteIdle},
	RouteUncertain:     {RouteActiveClean, RouteFollowupReady, RouteQuarantined},
	RouteQuarantined:   {RouteIdle},
}

// TestRouteTransitionMatrixExhaustive walks every route state pair: exactly
// the documented edges are allowed and every other pair is forbidden
// (TST-001).
func TestRouteTransitionMatrixExhaustive(t *testing.T) {
	for _, from := range AllRouteStates() {
		documented := documentRouteTable[from]
		for _, to := range AllRouteStates() {
			want := false
			for _, d := range documented {
				if d == to {
					want = true
				}
			}
			if got := CanTransitionRoute(from, to); got != want {
				t.Errorf("CanTransitionRoute(%s, %s) = %v, want %v (SOT §6)", from, to, got, want)
			}
		}
	}
	// Every non-initial state is reachable from IDLE through the table.
	seen := map[RouteState]bool{RouteIdle: true}
	for edge := range routeTable {
		seen[edge.to] = true
	}
	for _, s := range AllRouteStates() {
		if !seen[s] {
			t.Errorf("route state %s is declared but unreachable from IDLE", s)
		}
	}
}

// TestParseRouteStateFailsClosed verifies unknown values are errors, never
// silent defaults (DAT-009 posture).
func TestParseRouteStateFailsClosed(t *testing.T) {
	for _, s := range AllRouteStates() {
		got, err := ParseRouteState(string(s))
		if err != nil || got != s {
			t.Errorf("ParseRouteState(%s) = %v, %v", s, got, err)
		}
	}
	for _, bad := range []string{"", "idle", "SLEEPING", "ACTIVE", "active_clean"} {
		if _, err := ParseRouteState(bad); err == nil {
			t.Errorf("ParseRouteState(%q) must fail closed", bad)
		}
	}
}

// TestRouteIsActive verifies only the two active states report an active
// task; follow-up-pending, uncertain, and quarantined routes coordinate
// differently.
func TestRouteIsActive(t *testing.T) {
	active := map[RouteState]bool{RouteActiveClean: true, RouteActiveDirty: true}
	for _, s := range AllRouteStates() {
		if got := s.IsActive(); got != active[s] {
			t.Errorf("%s.IsActive() = %v, want %v", s, got, active[s])
		}
	}
}

// routeSnap builds a snapshot with every guard input controllable.
func routeSnap(mut func(*RouteSnapshot)) RouteSnapshot {
	snap := RouteSnapshot{
		RouteID:          "wiki-maintenance",
		ActivationState:  "enabled",
		State:            RouteIdle,
		ActiveDispatchID: "",
		DirtyGeneration:  0,
		FailureBudget:    3,
	}
	if mut != nil {
		mut(&snap)
	}
	return snap
}

// TestRouteReasonsMatchEdges verifies every allowed edge accepts only its
// documented reasons with satisfying evidence.
func TestRouteReasonsMatchEdges(t *testing.T) {
	type edgeCase struct {
		snap RouteSnapshot
		to   RouteState
		ev   RouteEvidence
	}
	// One satisfying case per (edge, documented reason) pair, because
	// reasons on one edge can demand different snapshots (for example the
	// exhausted-budget variant of ACTIVE_* -> UNCERTAIN).
	satisfying := map[struct {
		from, to RouteState
		reason   RouteReason
	}]edgeCase{
		{RouteIdle, RouteActiveClean, ReasonDispatchAccepted}: {routeSnap(nil), RouteActiveClean, RouteEvidence{ActivatingDispatchID: "d-1"}},
		{RouteActiveClean, RouteActiveDirty, ReasonLaterRelevantChange}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteActiveClean
			s.ActiveDispatchID = "d-1"
		}), RouteActiveDirty, RouteEvidence{DirtyGenerationAfter: 1}},
		{RouteActiveDirty, RouteActiveDirty, ReasonMoreChangesMerged}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteActiveDirty
			s.ActiveDispatchID = "d-1"
			s.DirtyGeneration = 2
		}), RouteActiveDirty, RouteEvidence{DirtyGenerationAfter: 3}},
		{RouteActiveClean, RouteIdle, ReasonWorkCompletedClean}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteActiveClean
			s.ActiveDispatchID = "d-1"
		}), RouteIdle, RouteEvidence{ReceiptRef: "wr-1"}},
		{RouteActiveDirty, RouteFollowupReady, ReasonWorkCompletedDirty}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteActiveDirty
			s.ActiveDispatchID = "d-1"
			s.DirtyGeneration = 1
		}), RouteFollowupReady, RouteEvidence{ReceiptRef: "wr-1"}},
		{RouteActiveDirty, RouteFollowupReady, ReasonWorkRetryBudgetRemains}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteActiveDirty
			s.ActiveDispatchID = "d-1"
			s.DirtyGeneration = 1
		}), RouteFollowupReady, RouteEvidence{ReceiptRef: "wr-1"}},
		{RouteActiveClean, RouteFollowupReady, ReasonWorkCompletedDirty}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteActiveClean
			s.ActiveDispatchID = "d-1"
			s.PendingReconcile = true
		}), RouteFollowupReady, RouteEvidence{ReceiptRef: "wr-1"}},
		{RouteActiveClean, RouteFollowupReady, ReasonWorkRetryBudgetRemains}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteActiveClean
			s.ActiveDispatchID = "d-1"
		}), RouteFollowupReady, RouteEvidence{ReceiptRef: "wr-1"}},
		{RouteActiveClean, RouteUncertain, ReasonRetryBudgetExhausted}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteActiveClean
			s.ActiveDispatchID = "d-1"
			s.FailureBudget = 0
		}), RouteUncertain, RouteEvidence{}},
		{RouteActiveClean, RouteUncertain, ReasonFollowupBudgetExhausted}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteActiveClean
			s.ActiveDispatchID = "d-1"
		}), RouteUncertain, RouteEvidence{FollowupGeneration: MaxConsecutiveFollowups + 1}},
		{RouteActiveClean, RouteUncertain, ReasonExecutionEvidenceStale}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteActiveClean
			s.ActiveDispatchID = "d-1"
		}), RouteUncertain, RouteEvidence{}},
		{RouteActiveDirty, RouteIdle, ReasonWorkSuppressed}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteActiveDirty
			s.ActiveDispatchID = "d-1"
			s.DirtyGeneration = 2
		}), RouteIdle, RouteEvidence{ReceiptRef: "rcpt-work-1"}},
		{RouteActiveDirty, RouteUncertain, ReasonRetryBudgetExhausted}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteActiveDirty
			s.ActiveDispatchID = "d-1"
			s.DirtyGeneration = 1
			s.FailureBudget = 0
		}), RouteUncertain, RouteEvidence{}},
		{RouteActiveDirty, RouteUncertain, ReasonFollowupBudgetExhausted}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteActiveDirty
			s.ActiveDispatchID = "d-1"
			s.DirtyGeneration = 1
		}), RouteUncertain, RouteEvidence{FollowupGeneration: MaxConsecutiveFollowups + 1}},
		{RouteActiveDirty, RouteUncertain, ReasonExecutionEvidenceStale}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteActiveDirty
			s.ActiveDispatchID = "d-1"
			s.DirtyGeneration = 1
		}), RouteUncertain, RouteEvidence{}},
		{RouteFollowupReady, RouteActiveClean, ReasonFollowupAccepted}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteFollowupReady
		}), RouteActiveClean, RouteEvidence{ActivatingDispatchID: "d-2"}},
		{RouteFollowupReady, RouteActiveDirty, ReasonFollowupAcceptedDirty}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteFollowupReady
			s.DirtyGeneration = 1
		}), RouteActiveDirty, RouteEvidence{ActivatingDispatchID: "d-2"}},
		{RouteFollowupReady, RouteIdle, ReasonFollowupDropped}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteFollowupReady
		}), RouteIdle, RouteEvidence{ReconciledNoWork: true}},
		{RouteUncertain, RouteActiveClean, ReasonTargetLookupFindsActive}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteUncertain
			s.ActiveDispatchID = "d-1"
		}), RouteActiveClean, RouteEvidence{Actor: "reconciler"}},
		{RouteUncertain, RouteFollowupReady, ReasonReconciliationResolved}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteUncertain
			s.ActiveDispatchID = "d-1"
			s.DirtyGeneration = 1
		}), RouteFollowupReady, RouteEvidence{Actor: "reconciler"}},
		{RouteUncertain, RouteQuarantined, ReasonOperatorActionRequired}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteUncertain
			s.ActiveDispatchID = "d-1"
		}), RouteQuarantined, RouteEvidence{Actor: "doctor"}},
		{RouteQuarantined, RouteIdle, ReasonOperatorResolved}: {routeSnap(func(s *RouteSnapshot) {
			s.State = RouteQuarantined
		}), RouteIdle, RouteEvidence{OperatorAction: true, Actor: "operator"}},
	}
	for edge, documented := range routeTable {
		for _, r := range documented {
			key := struct {
				from, to RouteState
				reason   RouteReason
			}{edge.from, edge.to, r}
			c, ok := satisfying[key]
			if !ok {
				t.Fatalf("test is missing a satisfying case for %s -> %s (%s)", edge.from, edge.to, r)
			}
			if err := ValidateRouteTransition(c.snap, edge.to, r, c.ev); err != nil {
				t.Errorf("ValidateRouteTransition(%s -> %s, %s) with satisfying evidence: %v", edge.from, edge.to, r, err)
			}
		}
		for _, r := range AllRouteReasons() {
			allowed := false
			for _, d := range documented {
				if d == r {
					allowed = true
				}
			}
			if !allowed {
				var zero RouteSnapshot
				zero.State = edge.from
				if err := ValidateRouteTransition(zero, edge.to, r, RouteEvidence{}); err == nil {
					t.Errorf("reason %s must be rejected on edge %s -> %s", r, edge.from, edge.to)
				}
			}
		}
	}
}

// TestRouteGuardE8T1Edges verifies the two E8-T1 guard negations: the
// dirty-aware follow-up activation refuses a clean generation, and the
// followup-budget exhaustion reason refuses a generation inside the
// budget.
func TestRouteGuardE8T1Edges(t *testing.T) {
	cleanFollowup := routeSnap(func(s *RouteSnapshot) {
		s.State = RouteFollowupReady
		s.DirtyGeneration = 0
	})
	err := ValidateRouteTransition(cleanFollowup, RouteActiveDirty, ReasonFollowupAcceptedDirty, RouteEvidence{ActivatingDispatchID: "d-2"})
	if err == nil {
		t.Fatal("dirty-aware follow-up activation must refuse a clean generation")
	}
	inBudget := routeSnap(func(s *RouteSnapshot) {
		s.State = RouteActiveDirty
		s.ActiveDispatchID = "d-1"
		s.DirtyGeneration = 1
	})
	err = ValidateRouteTransition(inBudget, RouteUncertain, ReasonFollowupBudgetExhausted, RouteEvidence{FollowupGeneration: MaxConsecutiveFollowups})
	if err == nil {
		t.Fatal("follow-up budget exhaustion must refuse a generation inside the budget")
	}
}

// TestRouteGuardActivation verifies activation requires an enabled route,
// the accepted dispatch, and an empty active slot (one route cannot hold
// two active dispatch IDs).
func TestRouteGuardActivation(t *testing.T) {
	base := func(mut func(*RouteSnapshot)) RouteSnapshot {
		return routeSnap(mut)
	}
	ev := RouteEvidence{ActivatingDispatchID: "d-2"}

	// Disabled and paused routes refuse activation.
	for _, activation := range []string{"disabled", "paused"} {
		snap := base(func(s *RouteSnapshot) { s.ActivationState = activation })
		err := ValidateRouteTransition(snap, RouteActiveClean, ReasonDispatchAccepted, ev)
		if err == nil || !strings.Contains(err.Error(), "not enabled") {
			t.Errorf("activation state %q must refuse activation: %v", activation, err)
		}
	}
	// Missing dispatch evidence refuses activation.
	if err := ValidateRouteTransition(base(nil), RouteActiveClean, ReasonDispatchAccepted, RouteEvidence{}); err == nil {
		t.Error("activation without the accepted dispatch must fail")
	}
	// An occupied active slot refuses activation.
	snap := base(func(s *RouteSnapshot) { s.State = RouteFollowupReady; s.ActiveDispatchID = "d-1" })
	err := ValidateRouteTransition(snap, RouteActiveClean, ReasonFollowupAccepted, ev)
	if err == nil || !strings.Contains(err.Error(), "already holds active dispatch") {
		t.Errorf("activation over an occupied slot must fail with the invariant: %v", err)
	}
	// A clean activation passes.
	if err := ValidateRouteTransition(base(nil), RouteActiveClean, ReasonDispatchAccepted, ev); err != nil {
		t.Fatalf("clean activation: %v", err)
	}
}

// TestRouteGuardDirtyGeneration verifies dirtying edges must durably
// increment the dirty generation (CON-002: never silently dropped) and any
// number of bursts can merge.
func TestRouteGuardDirtyGeneration(t *testing.T) {
	dirtySnap := routeSnap(func(s *RouteSnapshot) {
		s.State = RouteActiveDirty
		s.ActiveDispatchID = "d-1"
		s.DirtyGeneration = 4
	})
	if err := ValidateRouteTransition(dirtySnap, RouteActiveDirty, ReasonMoreChangesMerged, RouteEvidence{DirtyGenerationAfter: 4}); err == nil {
		t.Error("a non-increasing dirty generation must fail")
	}
	if err := ValidateRouteTransition(dirtySnap, RouteActiveDirty, ReasonMoreChangesMerged, RouteEvidence{DirtyGenerationAfter: 3}); err == nil {
		t.Error("a decreasing dirty generation must fail")
	}
	for after := 5; after < 12; after++ {
		if err := ValidateRouteTransition(dirtySnap, RouteActiveDirty, ReasonMoreChangesMerged, RouteEvidence{DirtyGenerationAfter: after}); err != nil {
			t.Fatalf("burst merging to generation %d must pass: %v", after, err)
		}
	}
	cleanSnap := routeSnap(func(s *RouteSnapshot) {
		s.State = RouteActiveClean
		s.ActiveDispatchID = "d-1"
	})
	if err := ValidateRouteTransition(cleanSnap, RouteActiveDirty, ReasonLaterRelevantChange, RouteEvidence{DirtyGenerationAfter: 1}); err != nil {
		t.Fatalf("first dirtying of a clean active route: %v", err)
	}
}

// TestRouteGuardDirtyNeverErasedByFailure verifies completion to IDLE is
// refused while dirty state or pending reconciliation remains, and failure
// edges preserve it via FOLLOWUP_READY.
func TestRouteGuardDirtyNeverErasedByFailure(t *testing.T) {
	dirtySnap := routeSnap(func(s *RouteSnapshot) {
		s.State = RouteActiveClean
		s.ActiveDispatchID = "d-1"
		s.DirtyGeneration = 2
	})
	err := ValidateRouteTransition(dirtySnap, RouteIdle, ReasonWorkCompletedClean, RouteEvidence{ReceiptRef: "wr-1"})
	if err == nil || !strings.Contains(err.Error(), "must not be silently dropped") {
		t.Errorf("completing to IDLE with dirty state must fail: %v", err)
	}
	pendingSnap := routeSnap(func(s *RouteSnapshot) {
		s.State = RouteActiveClean
		s.ActiveDispatchID = "d-1"
		s.PendingReconcile = true
	})
	if err := ValidateRouteTransition(pendingSnap, RouteIdle, ReasonWorkCompletedClean, RouteEvidence{ReceiptRef: "wr-1"}); err == nil {
		t.Error("completing to IDLE with pending reconciliation must fail")
	}
	// Dirty completion routes to FOLLOWUP_READY, never to IDLE.
	dirtyActive := routeSnap(func(s *RouteSnapshot) {
		s.State = RouteActiveDirty
		s.ActiveDispatchID = "d-1"
		s.DirtyGeneration = 2
	})
	// Negative branches for the E5 edges: a pending reconciliation
	// completion without the flag, and a pending flag dropped by a clean
	// completion, are both refused.
	pendingCompletion := routeSnap(func(s *RouteSnapshot) {
		s.State = RouteActiveClean
		s.ActiveDispatchID = "d-1"
	})
	if err := ValidateRouteTransition(pendingCompletion, RouteFollowupReady, ReasonWorkCompletedDirty, RouteEvidence{ReceiptRef: "wr-1"}); err == nil {
		t.Fatal("a completion follow-up without a pending reconciliation must be rejected")
	}
	cleanDrop := routeSnap(func(s *RouteSnapshot) {
		s.State = RouteActiveClean
		s.ActiveDispatchID = "d-1"
		s.PendingReconcile = true
	})
	if err := ValidateRouteTransition(cleanDrop, RouteIdle, ReasonWorkCompletedClean, RouteEvidence{ReceiptRef: "wr-1"}); err == nil {
		t.Fatal("a clean completion must not silently drop a pending reconciliation")
	}
	suppressPending := routeSnap(func(s *RouteSnapshot) {
		s.State = RouteActiveDirty
		s.ActiveDispatchID = "d-1"
		s.DirtyGeneration = 1
		s.PendingReconcile = true
	})
	if err := ValidateRouteTransition(suppressPending, RouteIdle, ReasonWorkSuppressed, RouteEvidence{ReceiptRef: "wr-1"}); err == nil {
		t.Fatal("exact suppression must not silently drop a pending reconciliation")
	}

	// Only an exact-suppression decision with receipt evidence may clear
	// a dirty generation to IDLE (E5-T3); plain dirty completion still
	// routes to FOLLOWUP_READY and evidence-less suppression is refused.
	if !CanTransitionRoute(RouteActiveDirty, RouteIdle) {
		t.Fatal("SOT §6 must declare ACTIVE_DIRTY -> IDLE for exact suppression")
	}
	if err := ValidateRouteTransition(dirtyActive, RouteIdle, ReasonWorkSuppressed, RouteEvidence{}); err == nil {
		t.Fatal("exact suppression without receipt evidence must be rejected")
	}
	if err := ValidateRouteTransition(dirtyActive, RouteFollowupReady, ReasonWorkCompletedDirty, RouteEvidence{ReceiptRef: "wr-1"}); err != nil {
		t.Fatalf("dirty completion must create a follow-up: %v", err)
	}
}

// TestRouteGuardFailureBudget verifies the failure-budget edges: remaining
// budget creates one follow-up, exhaustion becomes uncertain.
func TestRouteGuardFailureBudget(t *testing.T) {
	withBudget := routeSnap(func(s *RouteSnapshot) {
		s.State = RouteActiveClean
		s.ActiveDispatchID = "d-1"
		s.FailureBudget = 1
	})
	if err := ValidateRouteTransition(withBudget, RouteFollowupReady, ReasonWorkRetryBudgetRemains, RouteEvidence{ReceiptRef: "wr-1"}); err != nil {
		t.Fatalf("failure with remaining budget must create a follow-up: %v", err)
	}
	if err := ValidateRouteTransition(withBudget, RouteUncertain, ReasonRetryBudgetExhausted, RouteEvidence{}); err == nil {
		t.Error("uncertainty requires an exhausted budget when claimed as exhaustion")
	}
	exhausted := routeSnap(func(s *RouteSnapshot) {
		s.State = RouteActiveClean
		s.ActiveDispatchID = "d-1"
		s.FailureBudget = 0
	})
	if err := ValidateRouteTransition(exhausted, RouteFollowupReady, ReasonWorkRetryBudgetRemains, RouteEvidence{ReceiptRef: "wr-1"}); err == nil {
		t.Error("exhausted budget must not create another automatic follow-up")
	}
	if err := ValidateRouteTransition(exhausted, RouteUncertain, ReasonRetryBudgetExhausted, RouteEvidence{}); err != nil {
		t.Fatalf("budget exhaustion must become uncertain: %v", err)
	}
}

// TestRouteGuardUncertainAndQuarantine verifies uncertainty resolution and
// the operator-only quarantine exit.
func TestRouteGuardUncertainAndQuarantine(t *testing.T) {
	uncertain := routeSnap(func(s *RouteSnapshot) {
		s.State = RouteUncertain
		s.ActiveDispatchID = "d-1"
	})
	// Lookup activation requires the uncertain dispatch to still hold the slot.
	lost := uncertain
	lost.ActiveDispatchID = ""
	if err := ValidateRouteTransition(lost, RouteActiveClean, ReasonTargetLookupFindsActive, RouteEvidence{Actor: "reconciler"}); err == nil {
		t.Error("lookup activation without the dispatch holding the slot must fail")
	}
	if err := ValidateRouteTransition(uncertain, RouteActiveClean, ReasonTargetLookupFindsActive, RouteEvidence{Actor: "reconciler"}); err != nil {
		t.Fatalf("lookup activation: %v", err)
	}
	if err := ValidateRouteTransition(uncertain, RouteQuarantined, ReasonOperatorActionRequired, RouteEvidence{}); err == nil {
		t.Error("escalating to quarantine requires an actor")
	}
	quarantined := routeSnap(func(s *RouteSnapshot) { s.State = RouteQuarantined })
	if err := ValidateRouteTransition(quarantined, RouteIdle, ReasonOperatorResolved, RouteEvidence{Actor: "operator"}); err == nil {
		t.Error("leaving quarantine requires an explicit operator action")
	}
	if err := ValidateRouteTransition(quarantined, RouteIdle, ReasonOperatorResolved, RouteEvidence{OperatorAction: true, Actor: "operator"}); err != nil {
		t.Fatalf("operator release: %v", err)
	}
}

// TestCanActivateNormalDispatch verifies the CON-001 gate: only an enabled
// idle route with an empty active slot accepts a new normal dispatch.
func TestCanActivateNormalDispatch(t *testing.T) {
	if err := CanActivateNormalDispatch(routeSnap(nil)); err != nil {
		t.Fatalf("enabled idle route must accept: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*RouteSnapshot)
		want string
	}{
		{"disabled", func(s *RouteSnapshot) { s.ActivationState = "disabled" }, "not enabled"},
		{"paused", func(s *RouteSnapshot) { s.ActivationState = "paused" }, "not enabled"},
		{"active slot held", func(s *RouteSnapshot) { s.ActiveDispatchID = "d-1" }, "already holds active dispatch"},
		{"active clean", func(s *RouteSnapshot) { s.State = RouteActiveClean; s.ActiveDispatchID = "d-1" }, "active task"},
		{"active dirty", func(s *RouteSnapshot) { s.State = RouteActiveDirty; s.ActiveDispatchID = "d-1" }, "active task"},
		{"followup pending", func(s *RouteSnapshot) { s.State = RouteFollowupReady }, "pending follow-up"},
		{"uncertain", func(s *RouteSnapshot) { s.State = RouteUncertain; s.ActiveDispatchID = "d-1" }, "uncertain"},
		{"quarantined", func(s *RouteSnapshot) { s.State = RouteQuarantined }, "quarantined"},
	}
	for _, c := range cases {
		err := CanActivateNormalDispatch(routeSnap(c.mut))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: expected %q failure, got %v", c.name, c.want, err)
		}
	}
}

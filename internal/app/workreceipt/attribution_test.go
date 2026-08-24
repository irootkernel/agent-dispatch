package workreceipt

import (
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

func dirty(path, digest, observedAt string) ports.DirtyChange {
	return ports.DirtyChange{Path: path, AfterDigest: digest, DigestStatus: "known", ObservedAt: observedAt}
}

func ptr(s string) *string { return &s }

// TestMatchOutcomes is the unit table for the exact-suppression matcher:
// every outcome class, the empty-window refusal, and the full/partial
// decisions.
func TestMatchOutcomes(t *testing.T) {
	digestA := "sha256:" + repeatHex(1)
	digestB := "sha256:" + repeatHex(2)
	receipt := ReceiptEvidence{
		ReceiptID: "r1", DispatchID: "d1", RunID: "run1",
		BegunAt: "2026-08-21T01:00:00Z", CompletedAt: "2026-08-21T02:00:00Z",
		Changes: []changeEntry{
			{Path: "match.md", AfterDigest: ptr(digestA)},
			{Path: "wrong.md", AfterDigest: ptr(digestB)},
			{Path: "null.md"},
		},
	}
	observed := []ports.DirtyChange{
		dirty("match.md", digestA, "2026-08-21T01:30:00Z"),
		dirty("wrong.md", digestA, "2026-08-21T01:30:00Z"),
		dirty("null.md", digestA, "2026-08-21T01:30:00Z"),
		dirty("missing.md", digestA, "2026-08-21T01:30:00Z"),
		dirty("early.md", digestA, "2026-08-21T00:30:00Z"),
	}
	receipt.Changes = append(receipt.Changes, changeEntry{Path: "early.md", AfterDigest: ptr(digestA)})
	decision := Match(receipt, observed)
	outcomes := map[string]string{}
	for _, u := range decision.Unresolved {
		outcomes[u.Path] = u.Outcome
	}
	if outcomes["wrong.md"] != OutcomeMismatch {
		t.Fatalf("wrong digest must mismatch: %v", outcomes)
	}
	if outcomes["null.md"] != OutcomeNoDigest {
		t.Fatalf("a null receipt digest must be unverified: %v", outcomes)
	}
	if outcomes["missing.md"] != OutcomeMissing {
		t.Fatalf("a missing receipt path must be unresolved: %v", outcomes)
	}
	if outcomes["early.md"] != OutcomeBeforeRun {
		t.Fatalf("a pre-begin observation must demote: %v", outcomes)
	}
	if decision.FullySuppressed || len(decision.SuppressedPaths) != 1 {
		t.Fatalf("exactly the exact match suppresses: %+v", decision)
	}

	// An empty observed window is never proof of clearing.
	if empty := Match(receipt, nil); empty.FullySuppressed {
		t.Fatal("an empty observed window must refuse suppression")
	}

	// An unverified observed digest (unknown status or absent) never
	// suppresses even with a matching receipt value.
	unverified := ReceiptEvidence{
		ReceiptID: "r3", DispatchID: "d1", RunID: "run3",
		BegunAt: "2026-08-21T01:00:00Z", CompletedAt: "2026-08-21T02:00:00Z",
		Changes: []changeEntry{{Path: "u.md", AfterDigest: ptr(digestA)}},
	}
	unknownStatus := dirty("u.md", digestA, "2026-08-21T01:30:00Z")
	unknownStatus.DigestStatus = "unavailable"
	for _, obs := range [][]ports.DirtyChange{
		{unknownStatus},
		{{Path: "u.md", DigestStatus: "known", ObservedAt: "2026-08-21T01:30:00Z"}},
	} {
		if d := Match(unverified, obs); d.FullySuppressed {
			t.Fatalf("an unverified observed digest must never suppress: %+v", d.Unresolved)
		}
	}

	// A single exact match with nothing unresolved fully suppresses.
	only := ReceiptEvidence{
		ReceiptID: "r2", DispatchID: "d1", RunID: "run2",
		BegunAt: "2026-08-21T01:00:00Z", CompletedAt: "2026-08-21T02:00:00Z",
		Changes: []changeEntry{{Path: "match.md", AfterDigest: ptr(digestA)}},
	}
	if d := Match(only, []ports.DirtyChange{dirty("match.md", digestA, "2026-08-21T01:30:00Z")}); !d.FullySuppressed {
		t.Fatalf("the exact single match must fully suppress: %+v", d)
	}

	// A receipt claiming a path the generation never observed is extra
	// provenance: it blocks full suppression even when every observed
	// path verifies (AC-404).
	extra := ReceiptEvidence{
		ReceiptID: "r4", DispatchID: "d1", RunID: "run4",
		BegunAt: "2026-08-21T01:00:00Z", CompletedAt: "2026-08-21T02:00:00Z",
		Changes: []changeEntry{
			{Path: "match.md", AfterDigest: ptr(digestA)},
			{Path: "claimed-but-unobserved.md", AfterDigest: ptr(digestB)},
		},
	}
	d := Match(extra, []ports.DirtyChange{dirty("match.md", digestA, "2026-08-21T01:30:00Z")})
	if d.FullySuppressed {
		t.Fatal("an extra receipt path must block full suppression")
	}
	extraOutcome := ""
	for _, u := range d.Unresolved {
		if u.Path == "claimed-but-unobserved.md" {
			extraOutcome = u.Outcome
		}
	}
	if extraOutcome != OutcomeExtra {
		t.Fatalf("the extra receipt path must be recorded as %q, got %q (unresolved %+v)", OutcomeExtra, extraOutcome, d.Unresolved)
	}
	if len(d.SuppressedPaths) != 1 {
		t.Fatalf("the verified observed path still suppresses individually: %+v", d)
	}
}

func repeatHex(seed byte) string {
	buf := make([]byte, 64)
	for i := range buf {
		buf[i] = "0123456789abcdef"[(int(seed)+i)%16]
	}
	return string(buf)
}

// TestMatchDecisionDocumentDeterministic proves the audited decision
// document is identical for permuted input orders: map iteration must
// never leak into the evidence (repeated matches produce identical
// attribution audits).
func TestMatchDecisionDocumentDeterministic(t *testing.T) {
	digestA := "sha256:" + repeatHex(1)
	digestB := "sha256:" + repeatHex(2)
	build := func(order int) (ReceiptEvidence, []ports.DirtyChange) {
		changes := []changeEntry{
			{Path: "match.md", AfterDigest: ptr(digestA)},
			{Path: "extra.md", AfterDigest: ptr(digestB)},
		}
		observed := []ports.DirtyChange{
			dirty("match.md", digestA, "2026-08-21T01:30:00Z"),
			dirty("other.md", digestA, "2026-08-21T01:40:00Z"),
		}
		if order == 1 {
			changes = []changeEntry{{Path: "extra.md", AfterDigest: ptr(digestB)}, {Path: "match.md", AfterDigest: ptr(digestA)}}
			observed = []ports.DirtyChange{
				dirty("other.md", digestA, "2026-08-21T01:40:00Z"),
				dirty("match.md", digestA, "2026-08-21T01:30:00Z"),
			}
		}
		return ReceiptEvidence{
			ReceiptID: "r1", DispatchID: "d1", RunID: "run1",
			BegunAt: "2026-08-21T01:00:00Z", CompletedAt: "2026-08-21T02:00:00Z",
			Changes: changes,
		}, observed
	}
	firstReceipt, firstObserved := build(0)
	secondReceipt, secondObserved := build(1)
	if a, b := Match(firstReceipt, firstObserved).ContextJSON(), Match(secondReceipt, secondObserved).ContextJSON(); a != b {
		t.Fatalf("permuted inputs must produce identical decision documents:\n%s\n%s", a, b)
	}
}

// TestInScopeUnobservedReceiptPathStaysExtra pins the receipt-scope
// rule's conservative half: an unobserved receipt path the predicate
// calls in-scope stays unresolved extra provenance. (Honest rename of
// the former TestE8AuditClassifyErrorStaysMaterial — the bool
// predicate carries no error channel to exercise here; the
// error-capable arm of the scope predicate is its construction, pinned
// at the CLI boundary by TestE9T4ScopePredicateErrorFailsClosed.)
func TestInScopeUnobservedReceiptPathStaysExtra(t *testing.T) {
	evidence := ReceiptEvidence{
		ReceiptID: "rcpt-audit", Changes: []changeEntry{{Path: "Notes/x.md"}},
		OutsideScope: func(path string) bool {
			return false
		},
	}
	decision := Match(evidence, []ports.DirtyChange{
		{Path: "Notes/other.md", Operation: "modify", AfterDigest: "sha256:" + strings.Repeat("a", 64), DigestStatus: "known", ObservedAt: "2026-08-23T10:00:00Z"},
	})
	// Notes/x.md was never observed and the predicate says in-scope:
	// it must stay unresolved extra provenance.
	found := false
	for _, d := range decision.Unresolved {
		if d.Path == "Notes/x.md" && d.Outcome == OutcomeExtra {
			found = true
		}
	}
	if !found {
		t.Fatalf("an in-scope unobserved receipt path must stay receipt_extra_path: %+v", decision.Unresolved)
	}
	_ = evidence
}

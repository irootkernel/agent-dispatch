package workreceipt

import (
	"testing"

	"github.com/rootkernel/jjukkumi/internal/ports"
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
	decision := Match(receipt, observed, "2026-08-21T02:00:00Z")
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
	if empty := Match(receipt, nil, "2026-08-21T02:00:00Z"); empty.FullySuppressed {
		t.Fatal("an empty observed window must refuse suppression")
	}

	// A single exact match with nothing unresolved fully suppresses.
	only := ReceiptEvidence{
		ReceiptID: "r2", DispatchID: "d1", RunID: "run2",
		BegunAt: "2026-08-21T01:00:00Z", CompletedAt: "2026-08-21T02:00:00Z",
		Changes: []changeEntry{{Path: "match.md", AfterDigest: ptr(digestA)}},
	}
	if d := Match(only, []ports.DirtyChange{dirty("match.md", digestA, "2026-08-21T01:30:00Z")}, "now"); !d.FullySuppressed {
		t.Fatalf("the exact single match must fully suppress: %+v", d)
	}
}

func repeatHex(seed byte) string {
	buf := make([]byte, 64)
	for i := range buf {
		buf[i] = "0123456789abcdef"[(int(seed)+i)%16]
	}
	return string(buf)
}

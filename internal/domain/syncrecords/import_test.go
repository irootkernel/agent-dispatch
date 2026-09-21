package syncrecords

import (
	"bytes"
	"strings"
	"testing"
)

func TestImportCanonicalIdentityAndAliasSafety(t *testing.T) {
	oidA, oidB := strings.Repeat("a", 40), strings.Repeat("b", 40)
	p, err := NewImport(ImportBinding{
		GroupID: "wiki-pair", FromCommit: oidA, TargetCommit: oidB,
		MembershipRevision: strings.Repeat("c", 40), AcknowledgementID: "acknowledgement-1",
		ResourceObservationRevision: 7, ExpectedGitStateDigest: "sha256:" + strings.Repeat("d", 64),
		HistoryEvidenceID: "history-evidence-1", CaseMode: "insensitive", State: "validated", Reason: "none",
	}, []ImportPath{{Path: "Notes/b.md", Before: "absent", After: "sha256:" + strings.Repeat("e", 64)}, {Path: "Notes/a.md", Before: "sha256:" + strings.Repeat("f", 64), After: "absent"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := CanonicalImport(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeImport(raw)
	if err != nil || decoded.ImportID != p.ImportID || decoded.Paths[0].Path != "Notes/a.md" {
		t.Fatalf("round trip: %+v %v", decoded, err)
	}
	_, err = NewImport(ImportBinding{
		GroupID: "wiki-pair", FromCommit: oidA, TargetCommit: oidB,
		MembershipRevision: strings.Repeat("c", 40), AcknowledgementID: "acknowledgement-1",
		ResourceObservationRevision: 7, ExpectedGitStateDigest: "sha256:" + strings.Repeat("d", 64),
		HistoryEvidenceID: "history-evidence-2", CaseMode: "sensitive", State: "validated", Reason: "none",
	}, []ImportPath{{Path: "Notes/A.md", Before: "absent", After: "sha256:" + strings.Repeat("e", 64)}, {Path: "Notes/a.md", Before: "absent", After: "sha256:" + strings.Repeat("f", 64)}})
	if err == nil {
		t.Fatal("case-fold alias must fail closed even in sensitive mode")
	}
}

func TestControllerOnlyImportRequiresNoPathEffects(t *testing.T) {
	binding := ImportBinding{
		GroupID: "wiki-pair", FromCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40),
		MembershipRevision: strings.Repeat("c", 40), AcknowledgementID: "acknowledgement-1",
		ResourceObservationRevision: 7, ExpectedGitStateDigest: "sha256:" + strings.Repeat("d", 64),
		HistoryEvidenceID: "history-evidence-controller", CaseMode: "sensitive", ControllerOnly: true, State: "validated", Reason: "none",
	}
	p, err := NewImport(binding, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := CanonicalImport(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"paths":[]`)) {
		t.Fatalf("controller-only import must encode an explicit empty path array: %s", raw)
	}
	decoded, err := DecodeImport(raw)
	if err != nil || !decoded.ControllerOnly || len(decoded.Paths) != 0 {
		t.Fatalf("controller-only round trip: %+v %v", decoded, err)
	}
	binding.ControllerOnly = false
	if _, err := NewImport(binding, nil); err == nil {
		t.Fatal("an empty ordinary import must fail closed")
	}
}

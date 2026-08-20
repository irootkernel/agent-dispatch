package records

import "testing"

func TestEnumsFailClosed(t *testing.T) {
	bad := map[string]func(string) error{
		"operation":      func(s string) error { _, err := ParseOperation(s); return err },
		"file type":      func(s string) error { _, err := ParseFileType(s); return err },
		"digest status":  func(s string) error { _, err := ParseDigestStatus(s); return err },
		"classification": func(s string) error { _, err := ParseClassification(s); return err },
		"disposition":    func(s string) error { _, err := ParseDisposition(s); return err },
		"generation":     func(s string) error { _, err := ParseGenerationAction(s); return err },
		"intent state":   func(s string) error { _, err := ParseIntentState(s); return err },
		"receipt kind":   func(s string) error { _, err := ParseReceiptKind(s); return err },
		"acceptance":     func(s string) error { _, err := ParseAcceptanceState(s); return err },
		"execution":      func(s string) error { _, err := ParseExecutionState(s); return err },
		"work status":    func(s string) error { _, err := ParseWorkStatus(s); return err },
		"failure code":   func(s string) error { _, err := ParseFailureCode(s); return err },
	}
	good := map[string]string{
		"operation": "create", "file type": "regular", "digest status": "known",
		"classification": "normal", "disposition": "dispatch", "generation": "none",
		"intent state": "ready", "receipt kind": "acceptance", "acceptance": "accepted",
		"execution": "succeeded", "work status": "begun", "failure code": "timeout",
	}
	for name, check := range bad {
		if err := check("definitely-unknown"); err == nil {
			t.Errorf("%s: unknown value must fail closed", name)
		}
		if err := check(""); err == nil {
			t.Errorf("%s: empty value must fail closed", name)
		}
		if err := check(good[name]); err != nil {
			t.Errorf("%s: known value %q rejected: %v", name, good[name], err)
		}
	}
}

func TestParseDigestFailsClosed(t *testing.T) {
	bad := []string{
		"",
		"sha256:",
		"sha256:deadbeef",
		"sha256:DEADBEEF000000000000000000000000000000000000000000000000000000",
		"sha256:deadbeef0000000000000000000000000000000000000000000000000000000", // 65
		"md5:deadbeef00000000000000000000000000000000000000000000000000000000",
		"sha512:deadbeef00000000000000000000000000000000000000000000000000000000",
	}
	for _, s := range bad {
		if _, err := ParseDigest(s); err == nil {
			t.Errorf("%q must fail", s)
		}
	}
	if _, err := ParseDigest("sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"); err != nil {
		t.Errorf("valid digest rejected: %v", err)
	}
}

func TestSumDigest(t *testing.T) {
	if got := SumDigest(nil); got.String() != "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("empty sha256 mismatch: %s", got)
	}
}

func TestNormalizePath(t *testing.T) {
	good := map[string]string{
		"notes/a.md":    "notes/a.md",
		"한글/폴더/note.md": "한글/폴더/note.md",
		"a/b/c.txt":     "a/b/c.txt",
	}
	for in, want := range good {
		if got, err := NormalizePath(in); err != nil || got != want {
			t.Errorf("NormalizePath(%q) = %q, %v", in, got, err)
		}
	}
	bad := []string{"", "/abs/path", "a//b", "./a", "../a", "a/../b", "a\\b.md", "a\x00b"}
	for _, in := range bad {
		if _, err := NormalizePath(in); err == nil {
			t.Errorf("NormalizePath(%q) must fail", in)
		}
	}
}

func TestSortChangesCanonicalOrder(t *testing.T) {
	changes := []ChangeItem{
		{Path: "b/2.md", Ordinal: 1, Operation: OpModify},
		{Path: "a/x.md", Ordinal: 5, Operation: OpModify},
		{Path: "a/x.md", Ordinal: 2, Operation: OpCreate},
		{Path: "a/x.md", Ordinal: 3, Operation: OpDelete},
		{Path: "a/a.md", Ordinal: 4, Operation: OpCreate},
	}
	SortChanges(changes)
	got := make([]string, 0, len(changes))
	for _, c := range changes {
		got = append(got, c.Path+":"+string(c.Operation))
	}
	want := []string{
		"a/a.md:create",
		"a/x.md:delete",
		"a/x.md:create",
		"a/x.md:modify",
		"b/2.md:modify",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestChangeValidateFailsClosed(t *testing.T) {
	base := ChangeItem{Path: "a.md", Ordinal: 1, Operation: OpCreate, ExistsAfter: true, FileType: FileRegular, DigestStatus: DigestKnown}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid change rejected: %v", err)
	}
	bad := []func(*ChangeItem){
		func(c *ChangeItem) { c.Operation = Operation("upsert") },
		func(c *ChangeItem) { c.FileType = FileType("socket") },
		func(c *ChangeItem) { c.DigestStatus = DigestStatus("maybe") },
		func(c *ChangeItem) { c.Path = "/abs" },
		func(c *ChangeItem) { c.BeforeDigest = Digest("sha256:nope") },
		func(c *ChangeItem) { c.AfterDigest = Digest("sha256:nope") },
	}
	for i, mutate := range bad {
		c := base
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("case %d must fail closed", i)
		}
	}
}

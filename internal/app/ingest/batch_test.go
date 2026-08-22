package ingest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/localfs"
	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
	"github.com/irootkernel/agent-dispatch/internal/domain/policy"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

func setup(t *testing.T) (*localfs.Resolver, *policy.Engine, string) {
	t.Helper()
	root := t.TempDir()
	r, err := localfs.NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := policy.NewEngine([]string{"**/*"}, nil, []string{"raw/**"}, nil, policy.CaseSensitive)
	if err != nil {
		t.Fatal(err)
	}
	return r, engine, root
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func entry(name string, op records.Operation) watchman.Entry {
	return watchman.Entry{Name: name, Op: op, Exists: op != records.OpDelete, Type: records.FileRegular}
}

func build(t *testing.T, r *localfs.Resolver, e *policy.Engine, facts PathFacts, entries ...watchman.Entry) *Result {
	t.Helper()
	res, err := BuildBatch(entries, e, r, facts, records.SourceFlags{}, "res-1", Options{MaxHashBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func digestOf(t *testing.T, root, rel string) records.Digest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return records.SumDigest(raw)
}

func TestCoalescingRules(t *testing.T) {
	r, e, root := setup(t)
	write(t, root, "mm.md", "final")
	write(t, root, "cm.md", "final")
	write(t, root, "dc.md", "reborn")
	// md.md: modify then delete — file absent.
	// cd.md: create then delete, no prior — absent.

	t.Run("modify modify", func(t *testing.T) {
		res := build(t, r, e, NoFacts{}, entry("mm.md", records.OpModify), entry("mm.md", records.OpModify))
		if len(res.Changes) != 1 || res.Changes[0].Operation != records.OpModify {
			t.Fatalf("want one modify, got %+v", res.Changes)
		}
		if res.Changes[0].AfterDigest != digestOf(t, root, "mm.md") {
			t.Fatal("coalesced modify must carry the final digest")
		}
	})
	t.Run("create modify", func(t *testing.T) {
		res := build(t, r, e, NoFacts{}, entry("cm.md", records.OpCreate), entry("cm.md", records.OpModify))
		if len(res.Changes) != 1 || res.Changes[0].Operation != records.OpCreate {
			t.Fatalf("want one create, got %+v", res.Changes)
		}
	})
	t.Run("modify delete", func(t *testing.T) {
		res := build(t, r, e, NoFacts{}, entry("missing-after.md", records.OpModify), entry("missing-after.md", records.OpDelete))
		if len(res.Changes) != 1 || res.Changes[0].Operation != records.OpDelete || res.Changes[0].ExistsAfter {
			t.Fatalf("want one delete, got %+v", res.Changes)
		}
	})
	t.Run("delete create is replacement", func(t *testing.T) {
		res := build(t, r, e, NoFacts{}, entry("dc.md", records.OpDelete), entry("dc.md", records.OpCreate))
		if len(res.Changes) != 1 || res.Changes[0].Operation != records.OpCreate {
			t.Fatalf("want one create, got %+v", res.Changes)
		}
		if !res.Replacements["dc.md"] {
			t.Fatal("delete+create must be marked as replacement evidence")
		}
	})
	t.Run("create delete never existed drops", func(t *testing.T) {
		res := build(t, r, e, NoFacts{}, entry("cd.md", records.OpCreate), entry("cd.md", records.OpDelete))
		if len(res.Changes) != 0 {
			t.Fatalf("create+delete with no prior must drop, got %+v", res.Changes)
		}
		if len(res.Dropped) != 1 || res.Dropped[0].Reason != ReasonCreateDeleteNever {
			t.Fatalf("drop reason missing: %+v", res.Dropped)
		}
	})
	t.Run("create delete over prior keeps uncertain delete", func(t *testing.T) {
		facts := MapFacts{"cd2.md": digestOf(t, root, "mm.md")}
		res := build(t, r, e, facts, entry("cd2.md", records.OpCreate), entry("cd2.md", records.OpDelete))
		if len(res.Changes) != 1 || res.Changes[0].Operation != records.OpDelete {
			t.Fatalf("prior-existing create+delete must stay a delete, got %+v", res.Changes)
		}
		if res.Changes[0].BeforeDigest == "" {
			t.Fatal("uncertain delete should carry the prior digest as evidence")
		}
	})
	t.Run("delete create but file gone falls back to delete", func(t *testing.T) {
		res := build(t, r, e, NoFacts{}, entry("gone.md", records.OpDelete), entry("gone.md", records.OpCreate))
		if len(res.Changes) != 1 || res.Changes[0].Operation != records.OpDelete {
			t.Fatalf("want delete fallback, got %+v", res.Changes)
		}
		if res.Replacements["gone.md"] {
			t.Fatal("no replacement evidence when the file is absent")
		}
	})
}

func TestUnchangedModifySuppression(t *testing.T) {
	r, e, root := setup(t)
	write(t, root, "note.md", "stable")
	d := digestOf(t, root, "note.md")

	// Same digest: suppressed.
	res := build(t, r, e, MapFacts{"note.md": d}, entry("note.md", records.OpModify))
	if len(res.Changes) != 0 || len(res.Dropped) != 1 || res.Dropped[0].Reason != ReasonUnchangedModify {
		t.Fatalf("unchanged modify must drop with reason, got %+v / %+v", res.Changes, res.Dropped)
	}
	// No prior digest: meaningful.
	res = build(t, r, e, NoFacts{}, entry("note.md", records.OpModify))
	if len(res.Changes) != 1 {
		t.Fatal("modify without prior digest is meaningful")
	}
	// Different digest: meaningful.
	other := records.SumDigest([]byte("different"))
	res = build(t, r, e, MapFacts{"note.md": other}, entry("note.md", records.OpModify))
	if len(res.Changes) != 1 {
		t.Fatal("changed modify is meaningful")
	}
}

func TestDeleteNeverOpensFile(t *testing.T) {
	r, e, _ := setup(t)
	// The path does not exist on disk at all; a delete must not attempt
	// to open it and must not fail.
	res := build(t, r, e, NoFacts{}, entry("vanish.md", records.OpDelete))
	if len(res.Changes) != 1 || res.Changes[0].Operation != records.OpDelete {
		t.Fatalf("delete of a missing file must stand: %+v", res.Changes)
	}
	if res.Changes[0].DigestStatus != records.DigestNotApplicable {
		t.Fatal("delete digest is not applicable")
	}
}

func TestOversizeStaysStructurallyUnknown(t *testing.T) {
	r, e, root := setup(t)
	write(t, root, "big.md", "0123456789")
	res, err := BuildBatch([]watchman.Entry{entry("big.md", records.OpModify)}, e, r, MapFacts{"big.md": records.SumDigest([]byte("0123456789"))}, records.SourceFlags{}, "res-1", Options{MaxHashBytes: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 1 {
		t.Fatal("oversize modify must not be suppressed as unchanged")
	}
	if res.Changes[0].DigestStatus != records.DigestUnavailable || len(res.HashUnknown) != 1 {
		t.Fatalf("oversize digest must be structurally unknown: %+v", res.Changes[0])
	}
}

func TestProtectedNotHashed(t *testing.T) {
	r, e, root := setup(t)
	// raw/** is protected in the engine; the file does not even exist,
	// proving no read occurs during classification.
	res := build(t, r, e, NoFacts{}, entry("raw/protected.md", records.OpModify))
	if len(res.Changes) != 1 {
		t.Fatal("protected path must stay in the batch for quarantine")
	}
	if res.Changes[0].DigestStatus != records.DigestUnavailable || res.Changes[0].AfterDigest != "" {
		t.Fatal("protected path must not be hashed")
	}
	_ = root
}

func TestExcludedDropped(t *testing.T) {
	r, _, _ := setup(t)
	engine, err := policy.NewEngine([]string{"**/*.md"}, []string{"tmp/**"}, nil, nil, policy.CaseSensitive)
	if err != nil {
		t.Fatal(err)
	}
	res, err := BuildBatch([]watchman.Entry{entry("tmp/x.md", records.OpCreate), entry("keep.md", records.OpCreate)}, engine, r, NoFacts{}, records.SourceFlags{}, "res-1", Options{MaxHashBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	// keep.md was never written; create without file: hash unknown but kept.
	if len(res.Changes) != 1 || res.Changes[0].Path != "keep.md" {
		t.Fatalf("excluded path must be dropped: %+v", res.Changes)
	}
	if len(res.Dropped) != 1 || res.Dropped[0].Reason != ReasonExcluded {
		t.Fatalf("exclusion drop missing: %+v", res.Dropped)
	}
}

func TestDeterministicOrderAndFingerprint(t *testing.T) {
	r, e, root := setup(t)
	write(t, root, "a.md", "alpha")
	write(t, root, "b/n.md", "beta")
	write(t, root, "c.md", "gamma")

	forward := []watchman.Entry{entry("a.md", records.OpCreate), entry("b/n.md", records.OpCreate), entry("c.md", records.OpCreate)}
	shuffled := []watchman.Entry{entry("c.md", records.OpCreate), entry("b/n.md", records.OpCreate), entry("a.md", records.OpCreate)}

	r1 := build(t, r, e, NoFacts{}, forward...)
	r2 := build(t, r, e, NoFacts{}, shuffled...)
	if r1.Fingerprint != r2.Fingerprint {
		t.Fatalf("fingerprint must be order-independent: %s vs %s", r1.Fingerprint, r2.Fingerprint)
	}
	want := []string{"a.md", "b/n.md", "c.md"}
	for i, c := range r2.Changes {
		if c.Path != want[i] {
			t.Fatalf("canonical order wrong at %d: %+v", i, c.Path)
		}
	}
	// Content change alters the fingerprint.
	write(t, root, "a.md", "alpha2")
	r3 := build(t, r, e, NoFacts{}, forward...)
	if r3.Fingerprint == r1.Fingerprint {
		t.Fatal("content change must change the fingerprint")
	}
}

func TestEmptyBatchFingerprints(t *testing.T) {
	r, e, _ := setup(t)
	res := build(t, r, e, NoFacts{})
	if len(res.Changes) != 0 || res.Fingerprint == "" {
		t.Fatalf("empty batch must still carry a deterministic fingerprint: %+v", res)
	}
	res2 := build(t, r, e, NoFacts{})
	if res.Fingerprint != res2.Fingerprint {
		t.Fatal("empty batch fingerprint must be stable")
	}
}

// TestAtomicSaveAndReplacementFixtures replays the frozen E0-T5 corpus
// shapes through the batch builder: an atomic save is one modify of the
// final path, and a replacement is a delete plus a modify of two
// different paths in one batch.
func TestAtomicSaveAndReplacementFixtures(t *testing.T) {
	r, e, root := setup(t)
	write(t, root, "trigger-create.md", "atomic content")

	atomicPayload := `[{"size":19,"type":"f","new":false,"exists":true,"name":"trigger-create.md"}]`
	entries, _, err := watchman.ParsePayload([]byte(atomicPayload))
	if err != nil {
		t.Fatal(err)
	}
	res := build(t, r, e, NoFacts{}, entries...)
	if len(res.Changes) != 1 || res.Changes[0].Operation != records.OpModify || res.Changes[0].Path != "trigger-create.md" {
		t.Fatalf("atomic save must be one final-path modify: %+v", res.Changes)
	}
	if res.Changes[0].DigestStatus != records.DigestKnown {
		t.Fatal("atomic save digest must be hashed")
	}

	write(t, root, "trigger-repeated.md", "replacement content")
	replPayload := `[{"size":20,"type":"f","new":false,"exists":false,"name":"repl-src.md"},{"size":20,"type":"f","new":false,"exists":true,"name":"trigger-repeated.md"}]`
	entries, _, err = watchman.ParsePayload([]byte(replPayload))
	if err != nil {
		t.Fatal(err)
	}
	res = build(t, r, e, NoFacts{}, entries...)
	if len(res.Changes) != 2 {
		t.Fatalf("replacement batch should keep both paths: %+v", res.Changes)
	}
	ops := map[string]records.Operation{}
	for _, c := range res.Changes {
		ops[c.Path] = c.Operation
	}
	if ops["repl-src.md"] != records.OpDelete || ops["trigger-repeated.md"] != records.OpModify {
		t.Fatalf("replacement ops wrong: %v", ops)
	}
}

func TestRepeatedSaveResolvesToOneFinalChange(t *testing.T) {
	r, e, root := setup(t)
	write(t, root, "trigger-repeated.md", "v1")
	build(t, r, e, NoFacts{}, entry("trigger-repeated.md", records.OpCreate))
	write(t, root, "trigger-repeated.md", "v2")
	build(t, r, e, NoFacts{}, entry("trigger-repeated.md", records.OpModify))
	write(t, root, "trigger-repeated.md", "v3")
	res := build(t, r, e, NoFacts{}, entry("trigger-repeated.md", records.OpModify))
	if len(res.Changes) != 1 {
		t.Fatalf("three saves resolve to one final change, got %+v", res.Changes)
	}
	if res.Changes[0].AfterDigest != digestOf(t, root, "trigger-repeated.md") {
		t.Fatal("final change must carry the final digest")
	}
}

func TestCreateOverPriorDigestIsConservativeModify(t *testing.T) {
	r, e, root := setup(t)
	write(t, root, "p.md", "content")
	d := digestOf(t, root, "p.md")

	// Same digest: create+modify over a prior path suppresses like a modify.
	res := build(t, r, e, MapFacts{"p.md": d}, entry("p.md", records.OpCreate), entry("p.md", records.OpModify))
	if len(res.Changes) != 0 || len(res.Dropped) != 1 || res.Dropped[0].Reason != ReasonUnchangedModify {
		t.Fatalf("create+modify over equal prior digest must drop: %+v %+v", res.Changes, res.Dropped)
	}
	// Different digest: kept as a modify.
	other := records.SumDigest([]byte("other"))
	res = build(t, r, e, MapFacts{"p.md": other}, entry("p.md", records.OpCreate), entry("p.md", records.OpModify))
	if len(res.Changes) != 1 || res.Changes[0].Operation != records.OpModify {
		t.Fatalf("create over prior path is conservatively a modify: %+v", res.Changes)
	}
	// Bare create over a prior path: also a modify.
	res = build(t, r, e, MapFacts{"p.md": other}, entry("p.md", records.OpCreate))
	if len(res.Changes) != 1 || res.Changes[0].Operation != records.OpModify {
		t.Fatalf("bare create over prior path is conservatively a modify: %+v", res.Changes)
	}
}

func TestHashGuardClasses(t *testing.T) {
	r, e, root := setup(t)
	if err := os.MkdirAll(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Directory modify: structurally unknown.
	res := build(t, r, e, NoFacts{}, watchman.Entry{Name: "dir", Op: records.OpModify, Exists: true, Type: records.FileDirectory})
	if len(res.Changes) != 1 || res.Changes[0].DigestStatus != records.DigestUnavailable || !containsPath(res.HashUnknown, "dir") {
		t.Fatalf("directory digest must be unknown: %+v", res.Changes)
	}
	// Vanished file modify: structurally unknown.
	res = build(t, r, e, NoFacts{}, entry("vanished.md", records.OpModify))
	if len(res.Changes) != 1 || res.Changes[0].DigestStatus != records.DigestUnavailable || !containsPath(res.HashUnknown, "vanished.md") {
		t.Fatalf("vanished digest must be unknown: %+v", res.Changes)
	}
	// At-limit file: known.
	write(t, root, "at.md", "12345")
	res = build(t, r, e, NoFacts{}, entry("at.md", records.OpModify))
	if len(res.Changes) != 1 || res.Changes[0].DigestStatus != records.DigestKnown {
		t.Fatalf("at-limit digest must be known: %+v", res.Changes)
	}
	// Non-regular replacement (delete then create of a directory).
	res = build(t, r, e, NoFacts{}, entry("dir", records.OpDelete), watchman.Entry{Name: "dir", Op: records.OpCreate, Exists: true, Type: records.FileDirectory})
	if len(res.Changes) != 1 || res.Changes[0].Operation != records.OpCreate || res.Changes[0].DigestStatus != records.DigestUnavailable {
		t.Fatalf("non-regular replacement keeps an unknown create: %+v", res.Changes)
	}
	if !res.Replacements["dir"] {
		t.Fatal("non-regular replacement keeps replacement evidence")
	}
}

func TestFingerprintSensitiveToFlagsAndResource(t *testing.T) {
	r, e, root := setup(t)
	write(t, root, "a.md", "x")
	mk := func(flags records.SourceFlags, resID string) records.Digest {
		res, err := BuildBatch([]watchman.Entry{entry("a.md", records.OpCreate)}, e, r, NoFacts{}, flags, resID, Options{MaxHashBytes: 1 << 20})
		if err != nil {
			t.Fatal(err)
		}
		return res.Fingerprint
	}
	base := mk(records.SourceFlags{}, "res-1")
	variants := []records.SourceFlags{
		{Overflow: true},
		{FreshInstance: true},
		{HasRelative: true, RelativeRoot: "/vault/Notes"},
	}
	for i, f := range variants {
		if mk(f, "res-1") == base {
			t.Fatalf("variant %d must change the fingerprint", i)
		}
	}
	if mk(records.SourceFlags{}, "res-2") == base {
		t.Fatal("resource ID must change the fingerprint")
	}
}

func TestBuildBatchErrorTable(t *testing.T) {
	r, e, _ := setup(t)
	if _, err := BuildBatch(nil, nil, r, NoFacts{}, records.SourceFlags{}, "res", Options{MaxHashBytes: 1}); err == nil {
		t.Fatal("nil engine must fail")
	}
	if _, err := BuildBatch(nil, e, nil, NoFacts{}, records.SourceFlags{}, "res", Options{MaxHashBytes: 1}); err == nil {
		t.Fatal("nil resolver must fail")
	}
	if _, err := BuildBatch(nil, e, r, NoFacts{}, records.SourceFlags{}, "res", Options{}); err == nil {
		t.Fatal("zero MaxHashBytes must fail")
	}
	failing := failingFacts{}
	if _, err := BuildBatch([]watchman.Entry{entry("a.md", records.OpModify)}, e, r, failing, records.SourceFlags{}, "res", Options{MaxHashBytes: 1}); err == nil {
		t.Fatal("path-fact lookup failure must fail the build")
	}
}

type failingFacts struct{}

func (failingFacts) PriorDigest(string) (records.Digest, bool, error) {
	return "", false, errors.New("store down")
}

func TestNoGitIsInert(t *testing.T) {
	facts, err := NoGit{}.Enrich([]string{"a.md"})
	if err != nil || facts != nil {
		t.Fatalf("NoGit must be inert: %v %v", facts, err)
	}
}

// TestCreateDeleteSurvivingFileKeepsDelete proves the create,delete drop
// requires planning-time absence, not just a missing prior digest
// (processing-pipeline §4: "did not exist before AND does not exist after").
func TestCreateDeleteSurvivingFileKeepsDelete(t *testing.T) {
	r, e, root := setup(t)
	write(t, root, "survivor.md", "content")
	res := build(t, r, e, NoFacts{}, entry("survivor.md", records.OpCreate), entry("survivor.md", records.OpDelete))
	if len(res.Changes) != 1 || res.Changes[0].Operation != records.OpDelete {
		t.Fatalf("surviving file after create+delete must keep an uncertain delete, got %+v", res.Changes)
	}
	if len(res.Dropped) != 0 {
		t.Fatalf("surviving file must not be dropped: %+v", res.Dropped)
	}
}

// TestLongReplacementSequenceKeepsEvidenceAndNeverSuppresses proves a
// delete followed by later life is replacement semantics regardless of
// event splitting, and identical content is not suppressed.
func TestLongReplacementSequenceKeepsEvidenceAndNeverSuppresses(t *testing.T) {
	r, e, root := setup(t)
	write(t, root, "cycle.md", "identical")
	d := digestOf(t, root, "cycle.md")
	facts := MapFacts{"cycle.md": d}

	res := build(t, r, e, facts, entry("cycle.md", records.OpDelete), entry("cycle.md", records.OpCreate), entry("cycle.md", records.OpModify))
	if len(res.Changes) != 1 || res.Changes[0].Operation != records.OpCreate {
		t.Fatalf("delete,create,modify must coalesce to a create: %+v", res.Changes)
	}
	if !res.Replacements["cycle.md"] {
		t.Fatal("replacement evidence must survive a trailing modify")
	}
	if len(res.Dropped) != 0 {
		t.Fatalf("identical-content replacement must never be suppressed: %+v", res.Dropped)
	}

	// The physical two-op delivery stays consistent.
	res2 := build(t, r, e, facts, entry("cycle.md", records.OpDelete), entry("cycle.md", records.OpCreate))
	if len(res2.Changes) != 1 || res2.Changes[0].Operation != records.OpCreate || !res2.Replacements["cycle.md"] {
		t.Fatalf("two-op replacement diverged: %+v", res2.Changes)
	}
}

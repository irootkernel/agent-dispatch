package syncimport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func TestApplyPreservesDisjointFilesAndDetectsLateEdit(t *testing.T) {
	root := t.TempDir()
	before := map[string][]byte{"a.md": []byte("old"), "gone.md": []byte("gone")}
	after := map[string][]byte{"a.md": []byte("new"), "new.md": []byte("created")}
	for path, raw := range before {
		if err := os.WriteFile(filepath.Join(root, path), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "local.txt"), []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	effects, writes, _ := Diff(before, after)
	if state, err := Inspect(root, effects); err != nil || state != AllBefore {
		t.Fatalf("before state=%s err=%v", state, err)
	}
	if err := Apply(root, effects, writes); err != nil {
		t.Fatal(err)
	}
	if state, err := Inspect(root, effects); err != nil || state != AllAfter {
		t.Fatalf("after state=%s err=%v", state, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(root, "local.txt")); string(raw) != "untouched" {
		t.Fatalf("disjoint file changed: %q", raw)
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("late"), 0o600); err != nil {
		t.Fatal(err)
	}
	if state, err := Inspect(root, effects); err != nil || state != Unexpected {
		t.Fatalf("late edit state=%s err=%v", state, err)
	}
	if err := Apply(root, effects, writes); err == nil {
		t.Fatal("apply must refuse an independent late edit")
	}
	if raw, _ := os.ReadFile(filepath.Join(root, "a.md")); string(raw) != "late" {
		t.Fatalf("refused recovery overwrote the independent edit: %q", raw)
	}
}

func TestApplyRefusesInRootSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "protected.md")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("protected.md", filepath.Join(root, "note.md")); err != nil {
		t.Fatal(err)
	}
	effects := []syncrecords.ImportPath{{Path: "note.md", Before: Digest([]byte("old")), After: Digest([]byte("new"))}}
	if err := Apply(root, effects, map[string][]byte{"note.md": []byte("new")}); err == nil {
		t.Fatal("import followed an in-root symlink")
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "old" {
		t.Fatalf("symlink target changed: raw=%q err=%v", raw, err)
	}
}

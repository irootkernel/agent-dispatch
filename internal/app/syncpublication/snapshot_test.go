package syncpublication

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func TestCaptureFreezesOnlyGovernedMarkdown(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "a.md"), []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "attachment.png"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "private.md"), []byte("excluded"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := snapshotConfig(root)
	snap, err := Capture(cfg, "vault-main")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Files) != 1 || string(snap.Files["notes/a.md"]) != "first\n" {
		t.Fatalf("snapshot=%v", snap.Files)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "a.md"), []byte("late\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if string(snap.Files["notes/a.md"]) != "first\n" {
		t.Fatal("late edit changed frozen bytes")
	}
}

func TestMatchesObservedFactsRejectsMissingGovernedPath(t *testing.T) {
	cfg := snapshotConfig(t.TempDir())
	records := []syncrecords.SnapshotFile{{Path: "notes/a.md", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	facts := map[string]string{
		"notes/a.md":       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"notes/deleted.md": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"private.md":       "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	matched, err := MatchesObservedFacts(cfg, "vault-main", records, facts)
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("snapshot missing an observed governed path was accepted")
	}
	delete(facts, "notes/deleted.md")
	matched, err = MatchesObservedFacts(cfg, "vault-main", records, facts)
	if err != nil || !matched {
		t.Fatalf("exact governed facts did not match: matched=%v err=%v", matched, err)
	}
}

func TestCaptureRejectsGovernedSymlinkAndProtectedPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.md", filepath.Join(root, "linked.md")); err != nil {
		t.Fatal(err)
	}
	cfg := snapshotConfig(root)
	if _, err := Capture(cfg, "vault-main"); err == nil {
		t.Fatal("governed symlink accepted")
	}
	if err := os.Remove(filepath.Join(root, "linked.md")); err != nil {
		t.Fatal(err)
	}
	route := cfg.Routes["route-main"]
	route.Policy.Protected = []string{"target.md"}
	cfg.Routes["route-main"] = route
	if _, err := Capture(cfg, "vault-main"); err == nil {
		t.Fatal("protected Markdown accepted")
	}
}

func snapshotConfig(root string) *config.Config {
	return &config.Config{Resources: map[string]config.Resource{"vault-main": {Type: "directory", Root: root, FileScope: "markdown"}}, Routes: map[string]config.Route{"route-main": {Source: config.Source{Resource: "vault-main", Include: []string{"**/*.md", "*.md"}, Exclude: []string{"private.md"}}, Policy: config.Policy{Protected: []string{}, Immutable: []string{}}}}}
}

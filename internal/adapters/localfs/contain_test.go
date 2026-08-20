package localfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupRoot(t *testing.T) (root, outside string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "vault")
	outside = filepath.Join(base, "outside")
	writeFile(t, filepath.Join(root, "Inbox", "note.md"), "hello")
	writeFile(t, filepath.Join(outside, "secret.md"), "secret")
	return root, outside
}

func TestOpenRegularContainedFile(t *testing.T) {
	root, _ := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	f, info, err := r.OpenRegular("Inbox/note.md", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !info.Mode().IsRegular() || info.Size() != 5 {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestLexicalEscapeRejectedBeforeAccess(t *testing.T) {
	root, _ := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"../outside/secret.md",
		"Inbox/../../outside/secret.md",
		"/etc/passwd",
		"a\x00b",
		`a\b`,
		"",
		".",
		"..",
	} {
		if _, _, err := r.OpenRegular(rel, 1<<20); !errors.Is(err, ErrEscape) {
			t.Fatalf("path %q must fail with ErrEscape before any access, got %v", rel, err)
		}
		if _, err := r.Resolve(rel); !errors.Is(err, ErrEscape) {
			t.Fatalf("resolve %q must fail with ErrEscape, got %v", rel, err)
		}
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	root, outside := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	// File symlink pointing outside the root.
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "escape.md")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.OpenRegular("escape.md", 1<<20); err == nil {
		t.Fatal("symlinked file outside the root must not open")
	}
	if _, err := r.Resolve("escape.md"); !errors.Is(err, ErrEscape) {
		t.Fatalf("resolve must detect symlink escape, got %v", err)
	}

	// Directory symlink: a contained-looking path that leaves the root.
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve("linkdir/secret.md"); !errors.Is(err, ErrEscape) {
		t.Fatalf("symlinked directory escape must fail, got %v", err)
	}

	// Symlink to a file inside the root resolves fine (contained).
	if err := os.Symlink("Inbox/note.md", filepath.Join(root, "inside.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve("inside.md"); err != nil {
		t.Fatalf("contained symlink must resolve: %v", err)
	}
	// A contained symlink opens to its contained target's content; only
	// escapes are refused, in-scope indirection is safe.
	f, _, err := r.OpenRegular("inside.md", 1<<20)
	if err != nil {
		t.Fatalf("contained symlink must open its contained target: %v", err)
	}
	f.Close()
}

func TestRootItselfSymlinkResolved(t *testing.T) {
	root, _ := setupRoot(t)
	base := filepath.Dir(root)
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	r, err := NewResolver(alias)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if r.Root() != resolved {
		t.Fatalf("root must resolve symlinks once: %q vs %q", r.Root(), root)
	}
	if _, _, err := r.OpenRegular("Inbox/note.md", 1<<20); err != nil {
		t.Fatal(err)
	}
}

func TestFileTypeGuard(t *testing.T) {
	root, _ := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.OpenRegular("Folder", 1<<20); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("directory must fail as not-regular, got %v", err)
	}
	if _, err := r.StatContained("Folder"); err != nil {
		t.Fatalf("directory lstat should succeed: %v", err)
	}
}

func TestSizeGuard(t *testing.T) {
	root, _ := setupRoot(t)
	writeFile(t, filepath.Join(root, "big.md"), strings.Repeat("x", 100))
	r, err := NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.OpenRegular("big.md", 99); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize file must fail structurally, got %v", err)
	}
	// Exactly at the limit is allowed.
	f, _, err := r.OpenRegular("big.md", 100)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestPathLengthGuard(t *testing.T) {
	root, _ := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	r.SetLimits(10)
	if _, err := r.Resolve("Inbox/note.md"); !errors.Is(err, ErrPathTooLong) {
		t.Fatalf("long path must fail, got %v", err)
	}
	r.SetLimits(0) // back to default
	if _, err := r.Resolve("Inbox/note.md"); err != nil {
		t.Fatal(err)
	}
}

// TestDeletedPathNeverOpened: resolving a missing path returns the
// lexically checked join (for delete evidence) without requiring the
// file to exist; a dangling symlink ancestor is still an escape.
func TestDeletedPathNeverOpened(t *testing.T) {
	root, outside := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Resolve("Inbox/deleted-just-now.md")
	if err != nil {
		t.Fatalf("deleted path must resolve without existing: %v", err)
	}
	if !filepath.IsAbs(p) || filepath.Base(p) != "deleted-just-now.md" {
		t.Fatalf("unexpected resolved path %q", p)
	}
	// Dangling symlink directory positioned outside.
	if err := os.Symlink(filepath.Join(outside, "missing"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve("dangling/target.md"); !errors.Is(err, ErrEscape) {
		t.Fatalf("dangling symlink ancestor must be an escape, got %v", err)
	}
}

func TestNewResolverRejectsBadRoots(t *testing.T) {
	root, _ := setupRoot(t)
	if _, err := NewResolver("relative/path"); err == nil {
		t.Fatal("relative root must fail")
	}
	if _, err := NewResolver(filepath.Join(root, "Inbox", "note.md")); err == nil {
		t.Fatal("file root must fail")
	}
	if _, err := NewResolver(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing root must fail")
	}
}

// TestAncestorEscapeWithMissingTarget covers the deleted-path branch
// where the full path does not exist but an intermediate ancestor is a
// live symlink outside the root and directories below it do exist.
func TestAncestorEscapeWithMissingTarget(t *testing.T) {
	root, outside := setupRoot(t)
	if err := os.MkdirAll(filepath.Join(outside, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "esc")); err != nil {
		t.Fatal(err)
	}
	r, err := NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve("esc/sub/brand-new.md"); !errors.Is(err, ErrEscape) {
		t.Fatalf("missing target under escaping symlink ancestor must fail, got %v", err)
	}
	// A contained intermediate symlink followed by an ordinary missing
	// directory is fine.
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve("link/new.md"); err != nil {
		t.Fatalf("contained symlink ancestor with missing file must resolve: %v", err)
	}
}

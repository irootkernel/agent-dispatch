package watchman

import (
	"os"
	"path/filepath"
	"testing"
)

// TestE10T2RelativeRootBetween pins SRC-011's pure computation: equal
// roots yield the trivial relative root, an ancestor yields the
// configured-root-relative path, and a configured root outside the
// actual root fails closed.
func TestE10T2RelativeRootBetween(t *testing.T) {
	base := t.TempDir()
	vault := filepath.Join(base, "workspace", "vault")
	if err := mkdirAll(vault); err != nil {
		t.Fatal(err)
	}
	rel, err := RelativeRootBetween(vault, vault)
	if err != nil || rel != "." {
		t.Fatalf("equal roots must yield '.', got %q %v", rel, err)
	}
	rel, err = RelativeRootBetween(base, vault)
	if err != nil || rel != "workspace/vault" {
		t.Fatalf("ancestor root must yield the configured-relative path, got %q %v", rel, err)
	}
	other := filepath.Join(base, "sibling")
	if err := mkdirAll(other); err != nil {
		t.Fatal(err)
	}
	if _, err := RelativeRootBetween(other, vault); err == nil {
		t.Fatal("a configured root outside the actual root must fail closed")
	}
	if _, err := RelativeRootBetween(filepath.Join(base, "workspace"), filepath.Join(other, "vault")); err == nil {
		t.Fatal("a divergent configured root must fail closed")
	}
	// A symlinked configured root resolves against the actual root
	// before the relationship is decided.
	alias := filepath.Join(base, "alias-vault")
	if err := symlink(vault, alias); err != nil {
		t.Fatal(err)
	}
	rel, err = RelativeRootBetween(base, alias)
	if err != nil || rel != "workspace/vault" {
		t.Fatalf("a symlinked configured root must resolve to the same relative path, got %q %v", rel, err)
	}
}

// TestE10T2ManagedTriggerSubtreeConstraint pins SRC-011: the managed
// definition carries relative_root exactly when the actual watch root is
// an ancestor, and the constraint participates in definition equality.
func TestE10T2ManagedTriggerSubtreeConstraint(t *testing.T) {
	trivial := ManagedTrigger("trig", []string{"/bin/true"}, ".")
	if trivial.RelativeRoot != "" {
		t.Fatalf("the trivial relative root must stay implicit: %+v", trivial)
	}
	subtree := ManagedTrigger("trig", []string{"/bin/true"}, "workspace/vault")
	if subtree.RelativeRoot != "workspace/vault" {
		t.Fatalf("an ancestral watch root must constrain the trigger: %+v", subtree)
	}
	if subtree.Equal(trivial) {
		t.Fatal("the subtree constraint must participate in definition equality")
	}
}

// TestE10T2ValidateBindingAncestorFailsClosed pins the SRC-011 defense:
// an ancestor environment binds only through the exact persisted
// binding; a forged ancestor, a drifted actual root, a mismatched
// relative root, and a relative environment with no persisted binding
// all fail closed.
func TestE10T2ValidateBindingAncestorFailsClosed(t *testing.T) {
	base := t.TempDir()
	vault := filepath.Join(base, "workspace", "vault")
	if err := mkdirAll(vault); err != nil {
		t.Fatal(err)
	}
	stored := &Binding{
		RouteID: "wiki", ConfiguredRoot: vault, ActualRoot: base,
		RelativeRoot: "workspace/vault", TriggerName: "trig",
	}
	// The frozen-evidence absolute form (trigger-invocation-environment):
	// real Watchman sets WATCHMAN_RELATIVE_ROOT to the subdirectory's
	// absolute path while WATCHMAN_ROOT stays the watch root.
	ok := Env{Trigger: "trig", Root: base, RelativeRoot: vault, HasRelative: true}
	if err := ValidateBinding(ok, "trig", vault, stored); err != nil {
		t.Fatalf("the exact persisted ancestor binding must validate in the absolute form: %v", err)
	}
	// The relative form binds only when it is exactly the persisted one.
	okRel := Env{Trigger: "trig", Root: base, RelativeRoot: "workspace/vault", HasRelative: true}
	if err := ValidateBinding(okRel, "trig", vault, stored); err != nil {
		t.Fatalf("the persisted relative form must validate: %v", err)
	}
	forged := Env{Trigger: "trig", Root: base, RelativeRoot: filepath.Join(base, "sibling", "vault"), HasRelative: true}
	if err := ValidateBinding(forged, "trig", vault, stored); err == nil {
		t.Fatal("an absolute relative root that is not the configured root must fail closed")
	}
	forgedRel := Env{Trigger: "trig", Root: base, RelativeRoot: "sibling/vault", HasRelative: true}
	if err := ValidateBinding(forgedRel, "trig", vault, stored); err == nil {
		t.Fatal("a relative root that is not the persisted one must fail closed")
	}
	drifted := Env{Trigger: "trig", Root: filepath.Join(base, "other"), RelativeRoot: vault, HasRelative: true}
	if err := ValidateBinding(drifted, "trig", vault, stored); err == nil {
		t.Fatal("an environment root that is not the persisted actual root must fail closed")
	}
	if err := ValidateBinding(ok, "trig", vault, nil); err == nil {
		t.Fatal("a relative environment without a persisted binding must fail closed (plan/dry-run posture)")
	}
	wrongTrigger := Env{Trigger: "other", Root: base, RelativeRoot: vault, HasRelative: true}
	if err := ValidateBinding(wrongTrigger, "trig", vault, stored); err == nil {
		t.Fatal("a trigger identity mismatch must fail closed")
	}
	// A whole-root trigger without a relative root keeps the exact-root
	// contract and needs no persisted binding.
	exact := Env{Trigger: "trig", Root: vault}
	if err := ValidateBinding(exact, "trig", vault, nil); err != nil {
		t.Fatalf("the exact-root contract must hold without a binding: %v", err)
	}
}

func mkdirAll(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

func symlink(target, link string) error {
	return os.Symlink(target, link)
}

// TestE10T2CoversRoot pins the ancestor-coverage boundary: a nested root
// is covered, a sibling is not, and a filesystem-root watch covers every
// absolute path (round-1 review).
func TestE10T2CoversRoot(t *testing.T) {
	if !coversRoot("/srv", "/srv/vault/x") {
		t.Fatal("a nested path is covered by its ancestor watch")
	}
	if coversRoot("/srv/vault", "/srv/other") {
		t.Fatal("a sibling path is not covered")
	}
	if coversRoot("/srv/vault", "/srv/vaultx") {
		t.Fatal("coverage is segment-bound, not a string prefix")
	}
	if !coversRoot("/", "/any/absolute/path") {
		t.Fatal("a filesystem-root watch covers every absolute path")
	}
	if coversRoot("/srv", "relative/path") {
		t.Fatal("a relative path is never covered by an absolute watch")
	}
}

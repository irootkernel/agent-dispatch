package watchman

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestE18T1ManagedTriggerNeverCarriesRelativeRoot pins SRC-011 (D-028):
// the managed definition never carries relative_root, and a stale
// ancestor-era definition that does must not compare equal, so the
// install conflict gate demands --replace instead of a silent no-op.
func TestE18T1ManagedTriggerNeverCarriesRelativeRoot(t *testing.T) {
	def := ManagedTrigger("trig", []string{"/bin/true"})
	if def.RelativeRoot != "" {
		t.Fatalf("the managed definition must never carry relative_root: %+v", def)
	}
	stale := def
	stale.RelativeRoot = "workspace/vault"
	if def.Equal(stale) {
		t.Fatal("a stale relative-root definition must not compare equal to the managed form")
	}
}

// TestE18T1ValidateBindingExactRootOnly pins SRC-013 (D-028): the
// canonicalized WATCHMAN_ROOT must be the configured resource root
// itself, no persisted binding participates, and a present
// WATCHMAN_RELATIVE_ROOT fails closed as a stale relative-root trigger.
func TestE18T1ValidateBindingExactRootOnly(t *testing.T) {
	base := t.TempDir()
	vault := filepath.Join(base, "workspace", "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	exact := Env{Trigger: "trig", Root: vault}
	if err := ValidateBinding(exact, "trig", vault); err != nil {
		t.Fatalf("the exact configured root must validate with no persisted binding: %v", err)
	}
	// A configured root expressed through a symlink resolves to the same
	// directory from either side.
	alias := filepath.Join(base, "alias-vault")
	if err := os.Symlink(vault, alias); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBinding(Env{Trigger: "trig", Root: vault}, "trig", alias); err != nil {
		t.Fatalf("a symlinked configured root must bind: %v", err)
	}
	if err := ValidateBinding(Env{Trigger: "trig", Root: alias}, "trig", vault); err != nil {
		t.Fatalf("a symlinked environment root must bind: %v", err)
	}
	// An ancestor watch root is never accepted.
	if err := ValidateBinding(Env{Trigger: "trig", Root: base}, "trig", vault); err == nil {
		t.Fatal("an ancestor environment root must fail closed")
	}
	// Any relative-root environment is the signature of a stale
	// relative-root trigger and fails closed, even when it names the
	// configured root itself: there is no second binding axis.
	for _, env := range []Env{
		{Trigger: "trig", Root: base, RelativeRoot: vault, HasRelative: true},
		{Trigger: "trig", Root: base, RelativeRoot: "workspace/vault", HasRelative: true},
		{Trigger: "trig", Root: vault, RelativeRoot: vault, HasRelative: true},
	} {
		if err := ValidateBinding(env, "trig", vault); err == nil {
			t.Fatalf("a present WATCHMAN_RELATIVE_ROOT must fail closed as stale: %+v", env)
		}
	}
	if err := ValidateBinding(Env{Trigger: "other", Root: vault}, "trig", vault); err == nil {
		t.Fatal("a trigger identity mismatch must fail closed")
	}
	// A case-divergent spelling of the same directory — the D-028
	// incident's failure class on a case-insensitive volume — is refused
	// with the spelling guidance, never accepted.
	cased := filepath.Join(base, "Workspace", "Vault")
	err := ValidateBinding(Env{Trigger: "trig", Root: cased}, "trig", vault)
	if err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("a case-divergent spelling must fail closed with the spelling guidance: %v", err)
	}
}

// fakeWatchmanBinary writes one script-backed watchman binary that
// answers every request with the given canned JSON response, so the
// fail-closed arms of EnsureWatch are provable without a server.
func fakeWatchmanBinary(t *testing.T, response string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "watchman-fake")
	script := "#!/bin/sh\ncat <<'JSON'\n" + response + "\nJSON\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestE18T1EnsureWatchFailsClosedOnAncestorReuse pins SRC-011's
// fail-closed arm (D-028): a response that reuses an ancestor watch (a
// non-empty relative_path or a different reported root) and a server
// refusal both return an actionable LifecycleError carrying unwatch
// guidance — never an ancestor binding — while the exact root is
// accepted.
func TestE18T1EnsureWatchFailsClosedOnAncestorReuse(t *testing.T) {
	vault := "/srv/workspace/vault"

	// The ancestor-reuse shape watch-project used to return: the watch
	// root is the ancestor and relative_path names the subtree.
	bin := fakeWatchmanBinary(t, `{"version":"2026.07.27.00","watcher":"fsevents","watch":"/srv","relative_path":"workspace/vault"}`)
	_, err := NewClient(bin).EnsureWatch(context.Background(), vault)
	var le *LifecycleError
	if err == nil || !errors.As(err, &le) || le.Command != "watch" || !strings.Contains(le.Message, "watch-del") {
		t.Fatalf("an ancestor-reusing response must fail closed with unwatch guidance: %+v", err)
	}

	// A reported root that is not the requested one fails closed even
	// without relative_path.
	bin = fakeWatchmanBinary(t, `{"version":"2026.07.27.00","watcher":"fsevents","watch":"/srv"}`)
	_, err = NewClient(bin).EnsureWatch(context.Background(), vault)
	if err == nil || !errors.As(err, &le) || !strings.Contains(le.Message, "watch-del") {
		t.Fatalf("a different reported root must fail closed with unwatch guidance: %+v", err)
	}

	// A server refusal (the typical blocked cause) carries the guidance.
	bin = fakeWatchmanBinary(t, `{"version":"2026.07.27.00","error":"cannot watch nested path"}`)
	_, err = NewClient(bin).EnsureWatch(context.Background(), vault)
	if err == nil || !errors.As(err, &le) || !strings.Contains(le.Message, "watch-del") {
		t.Fatalf("a server refusal must fail closed with unwatch guidance: %+v", err)
	}

	// The exact root is accepted through the same harness.
	bin = fakeWatchmanBinary(t, `{"version":"2026.07.27.00","watcher":"fsevents","watch":"/srv/workspace/vault"}`)
	got, err := NewClient(bin).EnsureWatch(context.Background(), vault)
	if err != nil || got != vault {
		t.Fatalf("the exact root must be accepted: %q %v", got, err)
	}

	// A server-canonicalized spelling that differs only by case gets the
	// spelling guidance (the D-028 incident's failure class), never
	// acceptance and never the generic ancestor-reuse refusal.
	bin = fakeWatchmanBinary(t, `{"version":"2026.07.27.00","watcher":"fsevents","watch":"/srv/Workspace/Vault"}`)
	_, err = NewClient(bin).EnsureWatch(context.Background(), vault)
	if err == nil || !errors.As(err, &le) || !strings.Contains(le.Message, "canonical spelling") {
		t.Fatalf("a case-divergent canonical spelling must fail closed with the spelling guidance: %+v", err)
	}
}

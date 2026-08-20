package watchman

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// hasWatchman reports whether a real Watchman is usable; lifecycle
// integration tests skip when it is not (TST-003 environment-dependent
// evidence gap is recorded in the roadmap).
func hasWatchman(t *testing.T) *Client {
	t.Helper()
	if _, err := exec.LookPath("watchman"); err != nil {
		t.Skip("watchman binary not available")
	}
	client := NewClient("")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := client.Version(ctx); err != nil {
		t.Skipf("watchman server not usable: %v", err)
	}
	return client
}

func tempRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLifecycleVersionSupported(t *testing.T) {
	client := hasWatchman(t)
	ctx := context.Background()
	version, err := client.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckVersionSupported(version); err != nil {
		t.Fatalf("installed watchman %s reported unsupported: %v", version, err)
	}
	if err := CheckVersionSupported("2025.01.01.00"); err == nil {
		t.Fatal("older version must be rejected")
	}
}

func TestLifecycleInstallNoOpReplaceAndRemove(t *testing.T) {
	client := hasWatchman(t)
	ctx := context.Background()
	root := tempRoot(t)
	def := ManagedTrigger("jjukkumi-test-lifecycle", []string{"/bin/true"})

	watchRoot, err := client.EnsureWatch(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if watchRoot == "" {
		t.Fatal("no canonical watch root")
	}
	t.Cleanup(func() {
		_, _ = client.TriggerDelete(ctx, watchRoot, def.Name)
	})

	// Fresh install creates.
	disp, err := client.TriggerInstall(ctx, watchRoot, def)
	if err != nil || disp != "created" {
		t.Fatalf("install: %v %s", err, disp)
	}
	// Identical reinstall is a true no-op with already_defined.
	disp, err = client.TriggerInstall(ctx, watchRoot, def)
	if err != nil || disp != "already_defined" {
		t.Fatalf("identical reinstall must be already_defined: %v %s", err, disp)
	}
	// The installed normalized definition equals the managed one.
	installed, err := client.TriggerList(ctx, watchRoot)
	if err != nil {
		t.Fatal(err)
	}
	current, ok := FindTrigger(installed, def.Name)
	if !ok {
		t.Fatalf("trigger missing after install: %+v", installed)
	}
	if !current.Equal(def) {
		t.Fatalf("normalized definition diverged:\ninstalled: %+v\nmanaged:   %+v", current, def)
	}
	// A changed definition replaces.
	changed := def
	changed.Expression = []any{"allof", []any{"type", "f"}, []any{"match", "*.md", "wholename"}}
	disp, err = client.TriggerInstall(ctx, watchRoot, changed)
	if err != nil || disp != "replaced" {
		t.Fatalf("changed definition must replace: %v %s", err, disp)
	}
	// Remove is exact and idempotent.
	deleted, err := client.TriggerDelete(ctx, watchRoot, def.Name)
	if err != nil || !deleted {
		t.Fatalf("remove: %v %v", err, deleted)
	}
	deleted, err = client.TriggerDelete(ctx, watchRoot, def.Name)
	if err != nil || deleted {
		t.Fatalf("second remove must be a no-op: %v %v", err, deleted)
	}
}

func TestTriggerDefinitionComparison(t *testing.T) {
	a := ManagedTrigger("n", []string{"/bin/true"})
	b := ManagedTrigger("n", []string{"/bin/true"})
	if !a.Equal(b) {
		t.Fatal("identical definitions must compare equal")
	}
	c := ManagedTrigger("other", []string{"/bin/true"})
	if a.Equal(c) {
		t.Fatal("different names must compare unequal")
	}
	b.Expression = []any{"match", "*.md"}
	if a.Equal(b) {
		t.Fatal("different expressions must compare unequal")
	}
}

// newStubWatchman writes a fake watchman binary that emits canned
// behavior keyed by a marker file, so the client's error paths are
// testable without a server.
func newStubWatchman(t *testing.T, script string) *Client {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "watchman")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return NewClient(bin)
}

func TestClientServerReportedError(t *testing.T) {
	client := newStubWatchman(t, `echo '{"error":"root_dir failed: yo"}'; exit 0`)
	_, err := client.Version(context.Background())
	var le *LifecycleError
	if err == nil || !errors.As(err, &le) {
		t.Fatalf("server error member must surface as LifecycleError: %v", err)
	}
}

func TestClientUnparseableResponse(t *testing.T) {
	client := newStubWatchman(t, `echo 'not json at all'; exit 0`)
	_, err := client.Version(context.Background())
	var le *LifecycleError
	if err == nil || !errors.As(err, &le) {
		t.Fatalf("garbage must surface as LifecycleError: %v", err)
	}
}

func TestClientNonZeroExit(t *testing.T) {
	client := newStubWatchman(t, `echo boom >&2; exit 3`)
	_, err := client.Version(context.Background())
	var ue *UnavailableError
	if err == nil || !errors.As(err, &ue) {
		t.Fatalf("non-zero exit must surface as UnavailableError: %v", err)
	}
}

func TestClientTimeout(t *testing.T) {
	client := newStubWatchman(t, `sleep 5`)
	client.SetTimeoutForTest(200 * time.Millisecond)
	_, err := client.Version(context.Background())
	var ue *UnavailableError
	if err == nil || !errors.As(err, &ue) {
		t.Fatalf("timeout must surface as UnavailableError: %v", err)
	}
}

func TestVersionComparisonTable(t *testing.T) {
	cases := []struct {
		version string
		wantErr bool
	}{
		{"2025.01.01.00", true},
		{"2026.07.26.99", true},
		{"2026.07.27.00", false}, // exact baseline
		{"2026.08.01.00", false},
		{"2027.01.01.00", false},
		{"", true},
		{"nonsense", true},
		{"1.2.3", true},
		{"2026.07.27", true},
		{"2026.07.27.00-beta", true},
		{"12345678901234.1.1.1", true}, // absurd component fails closed
	}
	for _, c := range cases {
		err := CheckVersionSupported(c.version)
		if (err != nil) != c.wantErr {
			t.Fatalf("version %q: err=%v wantErr=%v", c.version, err, c.wantErr)
		}
	}
}

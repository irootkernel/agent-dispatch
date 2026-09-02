// Package hermesenv guards environment-dependent tests that need the
// real installed Hermes inside the runtime-verified support set.
package hermesenv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Sandbox is one test's isolated operating-system and Hermes state root.
// Binary is resolved before HOME changes so the installed executable can
// be exercised without exposing the operator's profiles or shared boards.
type Sandbox struct {
	Binary     string
	Home       string
	HermesHome string
}

// EnvironmentAllowlist returns the variables an Agent Dispatch target must
// inherit to remain inside this sandbox. Production defaults are deliberately
// unchanged; this wider set belongs only to real-Hermes tests.
func (s *Sandbox) EnvironmentAllowlist() []string {
	return []string{"PATH", "HOME", "HERMES_HOME", "HERMES_KANBAN_HOME"}
}

// CommandContext builds a real-Hermes command. NewSandbox has already made
// the process environment test-local, so direct commands and Agent Dispatch
// child commands resolve the same profile and Kanban roots.
func (s *Sandbox) CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, s.Binary, args...)
}

// NewSandbox resolves the real `hermes` binary on PATH and skips the test as
// a TST-007 environment-dependent evidence gap when it is absent,
// unprobeable, or rejected by the caller's supported-set predicate over the
// first `hermes --version` line. It then redirects every Hermes state-routing
// variable into a fresh per-test root. The version judgment stays with the
// owning adapter package; widening the verified set is a fresh E0-T4 probe,
// not a test override.
func NewSandbox(t testing.TB, supported func(firstVersionLine string) bool) *Sandbox {
	t.Helper()
	bin, err := exec.LookPath("hermes")
	if err != nil {
		t.Skip("hermes binary not available")
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	hermesHome := filepath.Join(root, "hermes")
	for _, dir := range []string{home, hermesHome} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create isolated Hermes test directory: %v", err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("HERMES_HOME", hermesHome)
	t.Setenv("HERMES_KANBAN_HOME", hermesHome)
	// These higher-precedence selectors must never escape the isolated root.
	// Hermes treats an empty value as unset.
	for _, key := range []string{
		"HERMES_KANBAN_DB",
		"HERMES_KANBAN_BOARD",
		"HERMES_KANBAN_WORKSPACES_ROOT",
	} {
		t.Setenv(key, "")
	}

	verOut, verr := exec.Command(bin, "--version").Output()
	if verr != nil {
		t.Skipf("hermes --version unavailable: %v", verr)
	}
	firstLine := strings.SplitN(strings.TrimSpace(string(verOut)), "\n", 2)[0]
	if !supported(firstLine) {
		t.Skipf("installed hermes (%s) is outside the verified support set (environment-dependent evidence gap, TST-007)", firstLine)
	}

	return &Sandbox{Binary: bin, Home: home, HermesHome: hermesHome}
}

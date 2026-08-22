// Package crashbinbuild builds the crash-injection harness binary for
// tests. A broken checkout is a hard failure, not a skippable gap: the
// harness backs the crash-boundary evidence (E7-T4).
package crashbinbuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Build compiles the crashbin harness once per test binary invocation
// and returns its path; the binary is removed when the test ends.
func Build(t testing.TB) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatalf("repository root unavailable (crash-boundary evidence requires the harness): go.mod not found from %s", root)
		}
		root = parent
	}
	out, err := os.CreateTemp("", "agent-dispatch-crashbin-*")
	if err != nil {
		t.Fatal(err)
	}
	name := out.Name()
	out.Close()
	t.Cleanup(func() { os.Remove(name) })
	cmd := exec.Command("go", "build", "-o", name, "./internal/testsupport/crashbin")
	cmd.Dir = root
	if build, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build crashbin: %v: %s", err, build)
	}
	return name
}

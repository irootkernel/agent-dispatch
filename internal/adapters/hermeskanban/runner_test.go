package hermeskanban

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newStubHermes writes a fake hermes executable with the given shell
// body, so the transport's controlled-execution behavior is testable
// without a real installation (watchman lifecycle-test pattern).
func newStubHermes(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "hermes")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestRunnerEnvironmentAllowlisted proves the child sees only the
// allowlisted environment (SEC-004): a poisoned variable outside the
// allowlist never reaches Hermes.
func TestRunnerEnvironmentAllowlisted(t *testing.T) {
	t.Setenv("JJUKKUMI_SECRET_SHOULD_NOT_LEAK", "sesame")
	bin := newStubHermes(t, `env | sort`)
	r := &runner{executable: bin, limits: ProcessLimits{
		SubmitTimeout: 5 * time.Second, LookupTimeout: 5 * time.Second,
		MaxOutputBytes: 4096,
	}.withDefaults()}
	res, err := r.run(context.Background(), time.Second, []string{"kanban", "list", "--json"})
	if err != nil {
		t.Fatalf("stub run: %v", err)
	}
	out := string(res.Stdout)
	if strings.Contains(out, "SESAME") || strings.Contains(out, "sesame") {
		t.Fatalf("non-allowlisted environment leaked to the child: %s", out)
	}
	for _, k := range []string{"PATH", "HOME"} {
		if !strings.Contains(out, k+"=") {
			t.Fatalf("required variable %s missing from child environment: %s", k, out)
		}
	}
}

// TestRunnerControlledWorkingDirectory proves the invocation runs in the
// controlled cwd, never the vault root (SEC-004).
func TestRunnerControlledWorkingDirectory(t *testing.T) {
	vault := t.TempDir()
	if err := os.WriteFile(filepath.Join(vault, "note.md"), []byte("vault"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := newStubHermes(t, `pwd; ls`)
	controlled := t.TempDir()
	r := &runner{executable: bin, limits: ProcessLimits{
		WorkingDirectory: controlled,
		SubmitTimeout:    time.Second, LookupTimeout: time.Second, MaxOutputBytes: 4096,
	}.withDefaults()}
	res, err := r.run(context.Background(), time.Second, []string{"kanban"})
	if err != nil {
		t.Fatalf("stub run: %v", err)
	}
	got := strings.TrimSpace(string(res.Stdout))
	resolved, rerr := filepath.EvalSymlinks(controlled)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if got != controlled && got != resolved {
		t.Fatalf("child ran in %q, want controlled dir %q", got, controlled)
	}
	if strings.Contains(got, "note.md") {
		t.Fatal("child working directory exposed vault content")
	}
}

// TestRunnerOutputBounded proves the bound is enforced at the write
// side: an excessive stream fails the invocation as excessive output
// (an ambiguous outcome, DUR-005), never an unbounded capture
// (SEC-004/SEC-009; conformance "excessive output").
func TestRunnerOutputBounded(t *testing.T) {
	bin := newStubHermes(t, `yes '0123456789' | head -c 100000`)
	r := &runner{executable: bin, limits: ProcessLimits{
		SubmitTimeout: 5 * time.Second, LookupTimeout: 5 * time.Second,
		MaxOutputBytes: 2048,
	}.withDefaults()}
	res, err := r.run(context.Background(), 5*time.Second, []string{"kanban", "list", "--json"})
	if !errors.Is(err, errExcessiveOutput) {
		t.Fatalf("excessive output must fail the run with the bound sentinel, got %v", err)
	}
	if len(res.Stdout) > 2048 {
		t.Fatalf("captured stdout %d bytes exceeds the bound", len(res.Stdout))
	}
}

// TestRunnerOutputWithinBound proves a stream inside the bound completes
// normally and is read back fully.
func TestRunnerOutputWithinBound(t *testing.T) {
	bin := newStubHermes(t, `head -c 1024 /dev/zero | tr '\0' 'x'`)
	r := &runner{executable: bin, limits: ProcessLimits{
		SubmitTimeout: 5 * time.Second, LookupTimeout: 5 * time.Second,
		MaxOutputBytes: 4096,
	}.withDefaults()}
	res, err := r.run(context.Background(), 5*time.Second, []string{"kanban", "list", "--json"})
	if err != nil {
		t.Fatalf("bounded stream inside the limit must succeed: %v", err)
	}
	if len(res.Stdout) != 1024 {
		t.Fatalf("captured %d bytes, want the full 1024", len(res.Stdout))
	}
}

// TestRunnerAllowlistPassThrough proves an operator-added allowlist
// entry reaches the child (the include direction of the allowlist, not
// just the exclusion direction).
func TestRunnerAllowlistPassThrough(t *testing.T) {
	t.Setenv("HERMES_TEST_MARKER", "reaches-child")
	bin := newStubHermes(t, `env | grep -c '^HERMES_TEST_MARKER=' || true`)
	r := &runner{executable: bin, limits: ProcessLimits{
		SubmitTimeout: time.Second, LookupTimeout: time.Second, MaxOutputBytes: 4096,
		EnvironmentAllowlist: []string{"HERMES_TEST_MARKER"},
	}.withDefaults()}
	res, err := r.run(context.Background(), time.Second, []string{"kanban"})
	if err != nil {
		t.Fatalf("stub run: %v", err)
	}
	if strings.TrimSpace(string(res.Stdout)) != "1" {
		t.Fatalf("allowlisted marker must reach the child exactly once, saw %q", res.Stdout)
	}
}

// TestRunnerTimeoutKillsProcessGroup proves the deadline kills the whole
// child process group (conformance "cancellation of timed-out child
// process"; sink-adapter-contract §6) and surfaces the timeout error.
func TestRunnerTimeoutKillsProcessGroup(t *testing.T) {
	bin := newStubHermes(t, `sh -c 'sleep 30' & sleep 30`)
	r := &runner{executable: bin, limits: ProcessLimits{
		SubmitTimeout: 5 * time.Second, LookupTimeout: 5 * time.Second,
		MaxOutputBytes: 1024,
	}.withDefaults()}
	start := time.Now()
	_, err := r.run(context.Background(), 300*time.Millisecond, []string{"kanban", "create", "x", "--json"})
	if !errors.Is(err, errDeadline) {
		t.Fatalf("deadline run must surface the timeout sentinel, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("child process group was not cleaned up promptly")
	}
}

// TestRunnerStdinClosed proves the child inherits no open stdin
// descriptor (SEC-004): reading stdin yields EOF immediately.
func TestRunnerStdinClosed(t *testing.T) {
	bin := newStubHermes(t, `head -c 16 /dev/stdin | wc -c | tr -d ' '`)
	r := &runner{executable: bin, limits: ProcessLimits{
		SubmitTimeout: time.Second, LookupTimeout: time.Second, MaxOutputBytes: 1024,
	}.withDefaults()}
	res, err := r.run(context.Background(), time.Second, []string{"kanban"})
	if err != nil {
		t.Fatalf("stub run: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(res.Stdout), []byte("0")) {
		t.Fatalf("child stdin must be closed, read %q", res.Stdout)
	}
}

// TestRunnerExecutableMissing is the definite pre-submit failure.
func TestRunnerExecutableMissing(t *testing.T) {
	r := &runner{executable: filepath.Join(t.TempDir(), "absent-hermes"), limits: ProcessLimits{}.withDefaults()}
	_, err := r.run(context.Background(), time.Second, []string{"--version"})
	var missing *ExecutableMissingError
	if !errors.As(err, &missing) || missing.Remediation() == "" {
		t.Fatalf("missing executable must surface ExecutableMissingError with remediation, got %v", err)
	}
}

// TestClassifyFailurePreservesCompletedResults proves the deadline-race
// property deterministically: a completed zero exit keeps its result
// even when the context deadline is already expired at classification
// time, while every Wait failure coinciding with a done context is the
// ambiguous deadline outcome and failures without it stay definite.
func TestClassifyFailurePreservesCompletedResults(t *testing.T) {
	exit1 := fmt.Errorf("exit status 1")
	signal := fmt.Errorf("signal: killed")
	if err := classifyFailure(nil, false, false, true); err != nil {
		t.Fatalf("zero exit with expired context must be preserved, got %v", err)
	}
	if err := classifyFailure(nil, false, false, false); err != nil {
		t.Fatalf("zero exit must be preserved, got %v", err)
	}
	if err := classifyFailure(exit1, false, false, false); !errors.Is(err, exit1) {
		t.Fatalf("definite exit without deadline must stay definite, got %v", err)
	}
	for _, runErr := range []error{signal, exit1} {
		if err := classifyFailure(runErr, false, false, true); !errors.Is(err, errDeadline) {
			t.Fatalf("failure with done context must classify as deadline (ambiguous), got %v", err)
		}
	}
	if err := classifyFailure(exit1, true, false, false); !errors.Is(err, errExcessiveOutput) {
		t.Fatalf("exceeded bound must classify as excessive output, got %v", err)
	}
	if err := classifyFailure(fmt.Errorf("wrap: %w", errDeadline), false, false, false); !errors.Is(err, errDeadline) {
		t.Fatalf("deadline sentinel must survive wrapping, got %v", err)
	}
}

package hermeskanban

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ProcessLimits are the controlled-execution bounds for every Hermes
// invocation (SEC-004/SEC-005): argument arrays with no shell, an
// allowlisted environment, a controlled working directory, no inherited
// descriptors, bounded captured output, and an execution deadline with
// process-group cleanup.
type ProcessLimits struct {
	// SubmitTimeout bounds task-creating invocations (config
	// submit_timeout; default DefaultSubmitTimeout).
	SubmitTimeout time.Duration
	// LookupTimeout bounds read-only invocations (config lookup_timeout;
	// default DefaultLookupTimeout).
	LookupTimeout time.Duration
	// MaxOutputBytes bounds stdout and stderr per stream at the write
	// side: a stream that exceeds it fails the invocation as excessive
	// output (ambiguous outcome), never an unbounded capture.
	MaxOutputBytes int64
	// EnvironmentAllowlist names the only inherited environment
	// variables. PATH and HOME are always required to resolve and
	// operate the CLI; the operator may not remove them.
	EnvironmentAllowlist []string
	// WorkingDirectory is the controlled cwd for every invocation. Empty
	// means the system temporary directory; a vault root must never be
	// the cwd of a target invocation.
	WorkingDirectory string
}

// Default process limits (configuration-spec §5 defaults; SEC-009).
const (
	DefaultSubmitTimeout  = 30 * time.Second
	DefaultLookupTimeout  = 15 * time.Second
	DefaultMaxOutputBytes = 1 << 20
)

// withDefaults fills unset limits.
func (l ProcessLimits) withDefaults() ProcessLimits {
	if l.SubmitTimeout <= 0 {
		l.SubmitTimeout = DefaultSubmitTimeout
	}
	if l.LookupTimeout <= 0 {
		l.LookupTimeout = DefaultLookupTimeout
	}
	if l.MaxOutputBytes <= 0 {
		l.MaxOutputBytes = DefaultMaxOutputBytes
	}
	if len(l.EnvironmentAllowlist) == 0 {
		l.EnvironmentAllowlist = []string{"PATH", "HOME"}
	} else {
		seen := map[string]bool{"PATH": true, "HOME": true}
		out := make([]string, 0, len(l.EnvironmentAllowlist)+2)
		for _, k := range l.EnvironmentAllowlist {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
		l.EnvironmentAllowlist = append([]string{"PATH", "HOME"}, out...)
	}
	return l
}

// errExcessiveOutput is the sentinel for a stream that exceeded the
// write-side bound (output truncation is an ambiguous outcome, DUR-005).
var errExcessiveOutput = errors.New("hermes output exceeded the configured bound")

// runResult is one bounded invocation outcome. Stdout and Stderr are
// already truncated to MaxOutputBytes; Err is nil only for a zero exit
// with both streams inside the bound.
type runResult struct {
	Stdout  []byte
	Stderr  []byte
	ExitErr error
}

// errDeadline is the sentinel deadline error for one run.
var errDeadline = fmt.Errorf("hermes invocation deadline exceeded")

// runner executes the verified public CLI under the process limits.
type runner struct {
	executable string
	limits     ProcessLimits
}

// run executes one argv array under the given timeout. It never uses a
// shell and never interpolates event data into the command (HER-003,
// SEC-005): argv elements are passed verbatim as arguments.
func (r *runner) run(ctx context.Context, timeout time.Duration, argv []string) (runResult, error) {
	if len(argv) == 0 {
		return runResult{}, fmt.Errorf("empty argv")
	}
	if _, err := exec.LookPath(r.executable); err != nil {
		return runResult{}, &ExecutableMissingError{Executable: r.executable, Detail: "not found on PATH"}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.executable, argv...)
	cmd.Env = r.environment()
	cmd.Dir = r.workingDirectory()
	// Stdin nil means /dev/null: the child inherits no open descriptor
	// and can never block reading agent-dispatch state (SEC-004).
	cmd.Stdin = nil
	// Run the child in its own process group so a timeout can clean up
	// the whole group, not just the leader (sink-adapter-contract §6).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Negative pid signals the group; error means it already exited.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return errDeadline
	}
	cmd.WaitDelay = 5 * time.Second

	// Capture through real files with a write-side bound: the child
	// writes into a capped sink (no unbounded temp-file growth while a
	// deadline window runs) and the later read is bounded again as a
	// belt (SEC-004/SEC-009).
	outFile, err := os.CreateTemp("", "agent-dispatch-hermes-*.out")
	if err != nil {
		return runResult{}, fmt.Errorf("creating capture file: %w", err)
	}
	defer os.Remove(outFile.Name())
	defer outFile.Close()
	errFile, err := os.CreateTemp("", "agent-dispatch-hermes-*.err")
	if err != nil {
		return runResult{}, fmt.Errorf("creating capture file: %w", err)
	}
	defer os.Remove(errFile.Name())
	defer errFile.Close()
	stdoutSink := &boundedSink{w: outFile, max: r.limits.MaxOutputBytes}
	stderrSink := &boundedSink{w: errFile, max: r.limits.MaxOutputBytes}
	cmd.Stdout = stdoutSink
	cmd.Stderr = stderrSink

	runErr := cmd.Start()
	if runErr == nil {
		runErr = cmd.Wait()
	}
	stdout := readBounded(outFile, r.limits.MaxOutputBytes)
	stderr := readBounded(errFile, r.limits.MaxOutputBytes)
	result := runResult{Stdout: stdout, Stderr: stderr, ExitErr: runErr}
	return result, classifyFailure(runErr, stdoutSink.exceeded, stderrSink.exceeded, ctx.Err() != nil)
}

// environment renders only the allowlisted variables; values are never
// embedded in errors or diagnostics.
func (r *runner) environment() []string {
	var out []string
	for _, k := range r.limits.EnvironmentAllowlist {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// redactSecrets removes the values of allowlisted environment variables
// from text about to be embedded in a diagnostic, so a child that echoes
// its environment cannot leak operator allowlisted values into operator
// output (error-model §6).
func (r *runner) redactSecrets(s string) string {
	for _, k := range r.limits.EnvironmentAllowlist {
		if v, ok := os.LookupEnv(k); ok && len(v) >= 4 {
			s = strings.ReplaceAll(s, v, "<redacted:"+k+">")
		}
	}
	return s
}

func (r *runner) workingDirectory() string {
	if r.limits.WorkingDirectory != "" {
		return r.limits.WorkingDirectory
	}
	return os.TempDir()
}

func readBounded(f *os.File, max int64) []byte {
	if _, err := f.Seek(0, 0); err != nil {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(f, max))
	return bytes.TrimSpace(raw)
}

// classifyFailure decides the run error from the actual Wait failure,
// the write-side bound flags, and whether the run context was done.
// A completed zero exit (nil runErr) always returns nil: a deadline
// that elapsed only after process exit must not discard a valid result.
// Any Wait failure coinciding with a done context — the Cancel hook's
// sentinel, a group-kill signal, or a genuine exit code that lost the
// race with the deadline — is the ambiguous deadline outcome, never a
// definite failure; a failure without a done context keeps its exit
// code as a definite result.
func classifyFailure(runErr error, stdoutExceeded, stderrExceeded bool, ctxDone bool) error {
	if runErr == nil {
		return nil
	}
	if stdoutExceeded || stderrExceeded {
		return errExcessiveOutput
	}
	if ctxDone || errors.Is(runErr, errDeadline) {
		return errDeadline
	}
	return runErr
}

// boundedSink caps one capture stream at the write side: writes beyond
// max fail, which the exec copier turns into a Wait error and the run
// classifies as excessive output.
type boundedSink struct {
	w        *os.File
	max      int64
	written  int64
	exceeded bool
}

func (b *boundedSink) Write(p []byte) (int, error) {
	if b.written+int64(len(p)) > b.max {
		b.exceeded = true
		return 0, errExcessiveOutput
	}
	n, err := b.w.Write(p)
	b.written += int64(n)
	return n, err
}

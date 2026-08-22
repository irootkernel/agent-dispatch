package secretresolver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rootkernel/jjukkumi/internal/config"
)

// ref parses one reference or fails the test.
func ref(t *testing.T, text string) *config.SecretRef {
	t.Helper()
	r, err := config.ParseSecretRef(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return r
}

// TestResolveEnv proves the env form resolves the live environment and
// fails definitively when absent or empty (SEC-006).
func TestResolveEnv(t *testing.T) {
	t.Setenv("JJUKKUMI_TEST_SECRET", "value-1")
	value, err := Resolve(context.Background(), ref(t, "env:JJUKKUMI_TEST_SECRET"))
	if err != nil || value != "value-1" {
		t.Fatalf("resolve = %q, %v", value, err)
	}
	if _, err := Resolve(context.Background(), ref(t, "env:JJUKKUMI_ABSENT_SECRET")); err == nil {
		t.Fatalf("absent variable must fail")
	} else {
		var unresolved *UnresolvedError
		if !errors.As(err, &unresolved) {
			t.Fatalf("err %v is not UnresolvedError", err)
		}
		if strings.Contains(err.Error(), "value-1") {
			t.Fatalf("error leaks a value: %s", err)
		}
	}
	t.Setenv("JJUKKUMI_EMPTY_SECRET", "")
	if _, err := Resolve(context.Background(), ref(t, "env:JJUKKUMI_EMPTY_SECRET")); err == nil {
		t.Fatalf("empty variable must fail")
	}
}

// TestResolveFile proves the file form reads one owner-readable file and
// trims the trailing newline.
func TestResolveFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("file-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := Resolve(context.Background(), ref(t, "file:"+path))
	if err != nil || value != "file-value" {
		t.Fatalf("resolve = %q, %v", value, err)
	}
	if _, err := Resolve(context.Background(), ref(t, "file:"+filepath.Join(t.TempDir(), "absent"))); err == nil {
		t.Fatalf("absent file must fail")
	}
}

// TestResolveFD proves the descriptor branch resolves through a real
// open descriptor and fails definitively on an unopened one.
func TestResolveFD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fdtoken")
	if err := os.WriteFile(path, []byte("fd-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	value, err := Resolve(context.Background(), ref(t, "fd:"+strconv.Itoa(int(f.Fd()))))
	if err != nil || value != "fd-value" {
		t.Fatalf("resolve = %q, %v", value, err)
	}
	if _, err := Resolve(context.Background(), ref(t, "fd:999")); err == nil {
		t.Fatalf("unopened descriptor must fail")
	}
}

// TestResolveKeychainStubbed proves the keychain branch runs the
// controlled lookup and surfaces its failures without values.
func TestResolveKeychainStubbed(t *testing.T) {
	original := keychainCommand
	defer func() { keychainCommand = original }()
	keychainCommand = func(ctx context.Context, name string) ([]byte, error) {
		if name != "jjukkumi-test-item" {
			t.Fatalf("keychain item name = %q", name)
		}
		return []byte("keychain-value\n"), nil
	}
	value, err := Resolve(context.Background(), ref(t, "keychain:jjukkumi-test-item"))
	if err != nil || value != "keychain-value" {
		t.Fatalf("resolve = %q, %v", value, err)
	}
	keychainCommand = func(ctx context.Context, name string) ([]byte, error) {
		return nil, errors.New("keychain lookup failed: item not found")
	}
	_, err = Resolve(context.Background(), ref(t, "keychain:jjukkumi-test-item"))
	var unresolved *UnresolvedError
	if !errors.As(err, &unresolved) || strings.Contains(err.Error(), "keychain-value") {
		t.Fatalf("err = %v, want unresolved without value leak", err)
	}
}

// TestResolveNilRef proves the degenerate input fails closed.
func TestResolveNilRef(t *testing.T) {
	if _, err := Resolve(context.Background(), nil); err == nil {
		t.Fatalf("nil reference must fail")
	}
}

// TestResolveFDRereadsOnRetry proves descriptor references survive
// repeated resolution in one process (WHK-005 retry posture): the read
// rewinds a seekable descriptor instead of consuming it once.
func TestResolveFDRereadsOnRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fdtoken")
	if err := os.WriteFile(path, []byte("fd-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	refText := "fd:" + strconv.Itoa(int(f.Fd()))
	for i := 0; i < 2; i++ {
		value, err := Resolve(context.Background(), ref(t, refText))
		if err != nil || value != "fd-value" {
			t.Fatalf("resolution %d = %q, %v", i, value, err)
		}
	}
}

// TestRunSecurityControls pins the controlled keychain subprocess
// (SEC-004, SEC-005): argv-only invocation, an empty environment, no
// stdin, and bounded failure detail. A stub script stands in for
// /usr/bin/security and records what it observed.
func TestRunSecurityControls(t *testing.T) {
	t.Setenv("LEAKED_MARKER", "must-not-inherit")
	dir := t.TempDir()
	observed := filepath.Join(dir, "observed")
	stub := filepath.Join(dir, "stub.sh")
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + observed + `.argv"
env > "` + observed + `.env"
if [ -f "` + observed + `.fail" ]; then
  printf '%s' "$(head -c 400 /dev/zero | tr '\0' 'x')" >&2
  exit 1
fi
printf 'keychain-value\n'
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	originalExec := execCommand
	defer func() { execCommand = originalExec }()
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, stub, args...)
	}

	out, err := runSecurity(context.Background(), "jjukkumi-item")
	if err != nil || string(out) != "keychain-value\n" {
		t.Fatalf("runSecurity = %q, %v", out, err)
	}
	argv, err := os.ReadFile(observed + ".argv")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(strings.Fields(string(argv)), " "); got != "find-generic-password -w -s jjukkumi-item" {
		t.Fatalf("argv = %q", got)
	}
	envDump, err := os.ReadFile(observed + ".env")
	if err != nil {
		t.Fatal(err)
	}
	// /bin/sh synthesizes PWD, SHLVL, and _ for itself, so emptiness is
	// not observable; the contract is that nothing is inherited.
	for _, leaked := range []string{"PATH=", "HOME=", "LEAKED_MARKER="} {
		if strings.Contains(string(envDump), leaked) {
			t.Fatalf("subprocess inherited %s: %q", leaked, envDump)
		}
	}

	if err := os.WriteFile(observed+".fail", []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = runSecurity(context.Background(), "jjukkumi-item")
	if err == nil {
		t.Fatalf("failing lookup must error")
	}
	if len(err.Error()) > 400 { // "keychain lookup failed: " prefix plus the 200-byte bound
		t.Fatalf("failure detail must be bounded, got %d bytes: %s", len(err.Error()), err.Error())
	}
}

// TestResolveFDSurvivesGC proves the descriptor cache keeps the
// launching process's descriptor open across garbage collection: a
// transient os.NewFile wrapper would have been finalized closed,
// breaking every later resolution (the round-2 defect).
func TestResolveFDSurvivesGC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fdtoken")
	if err := os.WriteFile(path, []byte("fd-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	refText := "fd:" + strconv.Itoa(int(f.Fd()))
	value, err := Resolve(context.Background(), ref(t, refText))
	if err != nil || value != "fd-value" {
		t.Fatalf("first resolution = %q, %v", value, err)
	}
	// Drop every local wrapper reference the resolver created and force
	// finalization; the cached wrapper must keep the descriptor open.
	runtime.GC()
	runtime.GC()
	value, err = Resolve(context.Background(), ref(t, refText))
	if err != nil || value != "fd-value" {
		t.Fatalf("post-GC resolution = %q, %v (descriptor was finalized closed)", value, err)
	}
}

// TestResolveBoundsEveryKind proves the 64 KiB bound on the env, file,
// fd, and keychain kinds: an over-bound reference is a configuration
// defect, never a credential.
func TestResolveBoundsEveryKind(t *testing.T) {
	oversize := strings.Repeat("x", maxSecretBytes+1)

	t.Setenv("JJUKKUMI_OVERSIZE", oversize)
	if _, err := Resolve(context.Background(), ref(t, "env:JJUKKUMI_OVERSIZE")); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("env oversize error = %v", err)
	}

	bigPath := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(bigPath, []byte(oversize), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(context.Background(), ref(t, "file:"+bigPath)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("file oversize error = %v", err)
	}

	bigFDPath := filepath.Join(t.TempDir(), "bigfd")
	if err := os.WriteFile(bigFDPath, []byte(oversize), 0o600); err != nil {
		t.Fatal(err)
	}
	bf, err := os.Open(bigFDPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bf.Close()
	if _, err := Resolve(context.Background(), ref(t, "fd:"+strconv.Itoa(int(bf.Fd())))); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("fd oversize error = %v", err)
	}

	original := keychainCommand
	defer func() { keychainCommand = original }()
	keychainCommand = func(ctx context.Context, name string) ([]byte, error) {
		return []byte(oversize), nil
	}
	if _, err := Resolve(context.Background(), ref(t, "keychain:oversize")); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("keychain oversize error = %v", err)
	}
}

// TestRunSecuritySeparatesStderr proves stderr never contaminates the
// resolved credential: a succeeding lookup that also writes keychain
// notices to stderr yields exactly the stdout bytes.
func TestRunSecuritySeparatesStderr(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "stub.sh")
	script := `#!/bin/sh
printf 'keychain notice: something benign\n' >&2
printf 'the-real-token\n'
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	originalExec := execCommand
	defer func() { execCommand = originalExec }()
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, stub, args...)
	}
	out, err := runSecurity(context.Background(), "item")
	if err != nil {
		t.Fatalf("runSecurity: %v", err)
	}
	if string(out) != "the-real-token\n" {
		t.Fatalf("credential contaminated by stderr: %q", out)
	}
}

// TestResolveFDConcurrentFirstResolution proves the serialized cache
// under the round-3 audit fix: two goroutines resolving the same
// first-time descriptor leave exactly one wrapper (the loser's wrapper
// would have finalized the shared descriptor closed).
func TestResolveFDConcurrentFirstResolution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fdtoken")
	if err := os.WriteFile(path, []byte("fd-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	refText := "fd:" + strconv.Itoa(int(f.Fd()))
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := Resolve(context.Background(), ref(t, refText))
			if err != nil || value != "fd-value" {
				errs <- fmt.Errorf("concurrent resolution = %q, %v", value, err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.GC()
	value, err := Resolve(context.Background(), ref(t, refText))
	if err != nil || value != "fd-value" {
		t.Fatalf("post-GC resolution = %q, %v (the race loser's finalizer closed the descriptor)", value, err)
	}
}

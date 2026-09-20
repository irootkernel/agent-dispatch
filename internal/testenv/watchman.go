// Package testenv owns opt-in guards for tests that use installed external
// tools. Repository Make targets establish the corresponding isolated runtime;
// direct go test invocations must not reach an operator's live services.
package testenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const realWatchmanEnv = "AGENT_DISPATCH_REAL_WATCHMAN_TESTS"

// RealWatchmanEnabled reports whether the repository test harness provided an
// isolated Watchman daemon for this test process.
func RealWatchmanEnabled() bool {
	_, err := isolatedWatchmanWrapper()
	return err == nil
}

// RequireRealWatchman skips outside the repository harness and fails if an
// opted-in harness does not expose its Watchman wrapper through PATH.
func RequireRealWatchman(t testing.TB) {
	t.Helper()
	if os.Getenv(realWatchmanEnv) == "" {
		t.Skip("real Watchman test requires make test or make verify (isolated daemon not enabled)")
	}
	if _, err := isolatedWatchmanWrapper(); err != nil {
		t.Fatalf("isolated Watchman wrapper identity invalid: %v", err)
	}
}

func isolatedWatchmanWrapper() (string, error) {
	want := os.Getenv(realWatchmanEnv)
	if want == "" {
		return "", fmt.Errorf("%s is unset", realWatchmanEnv)
	}
	if !filepath.IsAbs(want) {
		return "", fmt.Errorf("%s must name an absolute wrapper path", realWatchmanEnv)
	}
	got, err := exec.LookPath("watchman")
	if err != nil {
		return "", fmt.Errorf("watchman is missing from PATH: %w", err)
	}
	if filepath.Clean(got) != filepath.Clean(want) {
		return "", fmt.Errorf("PATH resolved watchman to %q, want Make-owned wrapper %q", got, want)
	}
	return got, nil
}

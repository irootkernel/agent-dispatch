package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRealWatchmanEnabledRequiresExactOptIn(t *testing.T) {
	t.Setenv(realWatchmanEnv, "")
	if RealWatchmanEnabled() {
		t.Fatal("empty opt-in must stay disabled")
	}
	t.Setenv(realWatchmanEnv, "1")
	if RealWatchmanEnabled() {
		t.Fatal("boolean opt-in without the Make-owned wrapper identity must stay disabled")
	}
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "watchman")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv(realWatchmanEnv, filepath.Join(t.TempDir(), "watchman"))
	if RealWatchmanEnabled() {
		t.Fatal("a wrapper identity that differs from PATH must stay disabled")
	}
	t.Setenv(realWatchmanEnv, wrapper)
	if !RealWatchmanEnabled() {
		t.Fatal("the exact Make-owned wrapper identity must enable isolated Watchman tests")
	}
}

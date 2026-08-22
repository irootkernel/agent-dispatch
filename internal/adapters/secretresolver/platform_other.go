//go:build !darwin

package secretresolver

import (
	"context"
	"os/exec"
)

// keychainSupported reports whether the macOS Keychain lookup is
// available; on non-darwin builds keychain references resolve to a typed
// unsupported failure instead of a platform panic.
func keychainSupported() bool { return false }

// execCommand builds the controlled subprocess (argv-only, no shell).
// It is a variable so tests can substitute a stub binary.
var execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

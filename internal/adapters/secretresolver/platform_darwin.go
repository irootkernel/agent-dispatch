package secretresolver

import (
	"context"
	"os/exec"
)

// keychainSupported reports whether the macOS Keychain lookup is
// available (provider-specific keychain references, configuration-spec
// §11; darwin only in v0.1).
func keychainSupported() bool { return true }

// execCommand builds the controlled subprocess (argv-only, no shell).
// It is a variable so tests can substitute a stub binary.
var execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

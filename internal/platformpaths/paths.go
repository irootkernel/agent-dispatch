// Package platformpaths resolves jjukkumi's platform-specific default
// locations (configuration-spec §1, §3): the default configuration path,
// the default state directory, and the JJUKKUMI_STATE_DIR override. Filesystem
// locality policy (SCP-007) is enforced later by the storage adapters; this
// package only resolves paths.
package platformpaths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// AppName is the directory name used for application state and config.
const AppName = "jjukkumi"

// DefaultConfigPath returns the platform default configuration file path:
// $HOME/.config/jjukkumi/config.yaml on both supported platforms. On Linux
// an absolute XDG_CONFIG_HOME replaces the $HOME/.config prefix; relative
// values are ignored per the XDG base-directory specification.
func DefaultConfigPath() string {
	if runtime.GOOS == "linux" {
		// XDG base-directory specification: relative values are ignored.
		if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
			return filepath.Join(xdg, AppName, "config.yaml")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Home resolution failing leaves no sane per-user location; use an
		// absolute temporary fallback rather than a CWD-relative path.
		return filepath.Join(tempFallbackDir(), "config.yaml")
	}
	return filepath.Join(home, ".config", AppName, "config.yaml")
}

// DefaultStateDir returns the platform default state directory:
// ~/Library/Application Support/JJUKKUMI on macOS and
// $XDG_STATE_HOME/jjukkumi (or ~/.local/state/jjukkumi) on Linux.
func DefaultStateDir() string {
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return tempFallbackDir()
		}
		return filepath.Join(home, "Library", "Application Support", "JJUKKUMI")
	}
	if runtime.GOOS == "linux" {
		if xdg := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(xdg) {
			return filepath.Join(xdg, AppName)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return tempFallbackDir()
	}
	return filepath.Join(home, ".local", "state", AppName)
}

// ResolveStateDir applies the state-directory precedence: an explicit
// override wins, then JJUKKUMI_STATE_DIR, then the platform default.
func ResolveStateDir(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("JJUKKUMI_STATE_DIR"); env != "" {
		return env
	}
	return DefaultStateDir()
}

// DefaultCapabilityReportPath returns the conventional location of the
// frozen Hermes capability report referenced by default configurations.
func DefaultCapabilityReportPath() string {
	if runtime.GOOS == "linux" {
		if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
			return filepath.Join(xdg, AppName, "hermes-capabilities.json")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(tempFallbackDir(), "hermes-capabilities.json")
	}
	return filepath.Join(home, ".config", AppName, "hermes-capabilities.json")
}

// tempFallbackDir is the last-resort per-user location when home
// resolution fails: a per-uid directory under the system temp dir, so a
// predictable shared world-writable path is never used.
func tempFallbackDir() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", AppName, os.Getuid()))
}

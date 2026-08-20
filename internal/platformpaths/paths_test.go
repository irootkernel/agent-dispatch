package platformpaths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultConfigPathShape(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	p := DefaultConfigPath()
	if filepath.Base(p) != "config.yaml" {
		t.Errorf("config file name = %q", filepath.Base(p))
	}
	home, _ := os.UserHomeDir()
	if want := filepath.Join(home, ".config", "jjukkumi", "config.yaml"); p != want {
		t.Errorf("default config path = %q, want %q", p, want)
	}
}

func TestXDGConfigHomeGating(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative/x")
	if p := DefaultConfigPath(); strings.Contains(p, "relative") || filepath.Base(filepath.Dir(p)) != "jjukkumi" {
		t.Errorf("relative XDG_CONFIG_HOME must be ignored, got %q", p)
	}
	t.Setenv("XDG_CONFIG_HOME", "/abs/cfg")
	p := DefaultConfigPath()
	if runtime.GOOS == "linux" {
		if p != "/abs/cfg/jjukkumi/config.yaml" {
			t.Errorf("absolute XDG_CONFIG_HOME not honored on linux: %q", p)
		}
	} else if p == "/abs/cfg/jjukkumi/config.yaml" {
		t.Errorf("XDG_CONFIG_HOME must not override the platform default on %s", runtime.GOOS)
	}
}

func TestDefaultCapabilityReportPathGating(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/abs/cfg")
	p := DefaultCapabilityReportPath()
	if runtime.GOOS == "linux" {
		if p != "/abs/cfg/jjukkumi/hermes-capabilities.json" {
			t.Errorf("XDG_CONFIG_HOME not honored on linux: %q", p)
		}
	} else if p == "/abs/cfg/jjukkumi/hermes-capabilities.json" {
		t.Errorf("XDG_CONFIG_HOME must not override the default on %s", runtime.GOOS)
	}
}

func TestDefaultStateDirShape(t *testing.T) {
	p := DefaultStateDir()
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		want := filepath.Join(home, "Library", "Application Support", "JJUKKUMI")
		if p != want {
			t.Errorf("darwin state dir = %q, want %q", p, want)
		}
	default:
		if filepath.IsAbs(p) == false {
			t.Errorf("state dir should be absolute: %q", p)
		}
	}
}

func TestResolveStateDirPrecedence(t *testing.T) {
	t.Setenv("JJUKKUMI_STATE_DIR", "/tmp/from-env")
	if got := ResolveStateDir(""); got != "/tmp/from-env" {
		t.Errorf("env override ignored: %q", got)
	}
	if got := ResolveStateDir("/explicit"); got != "/explicit" {
		t.Errorf("explicit override ignored: %q", got)
	}
}

func TestXDGStateHomeGatedToLinux(t *testing.T) {
	// A relative value must be ignored everywhere; an absolute value only
	// applies on Linux.
	t.Setenv("XDG_STATE_HOME", "relative/path")
	if p := DefaultStateDir(); p == filepath.Join("relative/path", "jjukkumi") {
		t.Errorf("relative XDG_STATE_HOME must be ignored, got %q", p)
	}
	if runtime.GOOS != "linux" {
		t.Setenv("XDG_STATE_HOME", "/abs/xdg")
		if p := DefaultStateDir(); p == "/abs/xdg/jjukkumi" {
			t.Errorf("XDG_STATE_HOME must not override the platform default on %s", runtime.GOOS)
		}
	}
}

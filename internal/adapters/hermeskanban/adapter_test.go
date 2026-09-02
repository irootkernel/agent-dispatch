package hermeskanban

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/testsupport/hermesenv"
)

// stubVersionHermes emits the frozen version first line for --version.
func stubVersionHermes(t *testing.T, versionLine string) string {
	t.Helper()
	return newStubHermes(t, `if [ "$1" = "--version" ]; then printf '%s\nInstall directory: ~/.hermes/hermes-agent\n' '`+versionLine+`'; exit 0; fi; exit 3`)
}

// TestProbeEligibleFloor proves the read-only eligibility gate: the
// 0.20.5 support floor passes, every later version passes with no
// maximum (HER-011, ADR-0021), and only a below-floor version fails.
func TestProbeEligibleFloor(t *testing.T) {
	t.Run("floor version", func(t *testing.T) {
		bin := stubVersionHermes(t, "Hermes Agent v0.20.5 (2026.8.19)")
		adapter, err := New("hermes-kanban-main", bin, "", ProcessLimits{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adapter.Probe(context.Background()); err != nil {
			t.Fatalf("the 0.20.5 floor interface must be eligible: %v", err)
		}
	})
	t.Run("later version with no maximum", func(t *testing.T) {
		bin := stubVersionHermes(t, "Hermes Agent v0.21.0 (2026.9.10)")
		adapter, err := New("t", bin, "", ProcessLimits{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adapter.Probe(context.Background()); err != nil {
			t.Fatalf("a version above the floor must be eligible without a source allowlist edit: %v", err)
		}
	})
	t.Run("below floor", func(t *testing.T) {
		bin := stubVersionHermes(t, "Hermes Agent v0.20.4 (2026.8.18)")
		adapter, err := New("t", bin, "", ProcessLimits{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = adapter.Probe(context.Background())
		var gate *VersionUnsupportedError
		if !errors.As(err, &gate) {
			t.Fatalf("a below-floor version must fail the gate, got %v", err)
		}
	})
	t.Run("declared higher floor", func(t *testing.T) {
		bin := stubVersionHermes(t, "Hermes Agent v0.20.5 (2026.8.19)")
		adapter, err := New("t", bin, "0.21.0", ProcessLimits{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = adapter.Probe(context.Background())
		var gate *VersionUnsupportedError
		if !errors.As(err, &gate) {
			t.Fatalf("a version below the declared floor must fail, got %v", err)
		}
	})
	t.Run("adapter identity", func(t *testing.T) {
		bin := stubVersionHermes(t, "Hermes Agent v0.20.5 (2026.8.19)")
		adapter, err := New("hermes-kanban-main", bin, "", ProcessLimits{})
		if err != nil {
			t.Fatal(err)
		}
		if adapter.Type() != "hermes_kanban" || adapter.ID() != "hermes-kanban-main" {
			t.Fatalf("adapter identity %q/%q", adapter.ID(), adapter.Type())
		}
	})
}

// TestProbeFailsClosedOnUnparsableVersion proves an unparsable version
// output never passes (E0-T4 §2: version discovery must fail closed).
func TestProbeFailsClosedOnUnparsableVersion(t *testing.T) {
	bin := newStubHermes(t, `printf 'weird\n'`)
	adapter, err := New("t", bin, "", ProcessLimits{})
	if err != nil {
		t.Fatal(err)
	}
	_, perr := adapter.Probe(context.Background())
	var malformed *MalformedOutputError
	if !errors.As(perr, &malformed) {
		t.Fatalf("unparsable version must be a malformed-output failure, got %v", perr)
	}
}

// TestParseMinimumVersion proves the strict configured-floor parser:
// empty means the default, a canonical triple parses, and every other
// shape fails closed.
func TestParseMinimumVersion(t *testing.T) {
	if v, err := ParseMinimumVersion(""); err != nil || v != MinimumEligibleVersion {
		t.Fatalf("empty floor must mean the default 0.20.5, got %v err=%v", v, err)
	}
	if v, err := ParseMinimumVersion("1.2.3"); err != nil || v.String() != "1.2.3" {
		t.Fatalf("canonical triple must parse, got %v err=%v", v, err)
	}
	for _, bad := range []string{"0.20", "0.20.5.2", "v0.20.5", "0.20.5-beta", "01.2.3", "0.20.x", "0.20.9999999999"} {
		if _, err := ParseMinimumVersion(bad); err == nil {
			t.Fatalf("floor %q must fail closed", bad)
		}
	}
}

// TestProbeVerboseStates proves the validation-surface classification:
// unavailable and version_unsupported are distinct operator-visible
// states; the capability classes return with the E11-T2 probe.
func TestProbeVerboseStates(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) {
		adapter, err := New("t", filepath.Join(t.TempDir(), "absent"), "", ProcessLimits{})
		if err != nil {
			t.Fatal(err)
		}
		summary, _, err := adapter.ProbeVerbose(context.Background())
		if err != nil || summary.State != "unavailable" {
			t.Fatalf("absent executable: state=%q err=%v", summary.State, err)
		}
	})
	t.Run("version_unsupported", func(t *testing.T) {
		bin := stubVersionHermes(t, "Hermes Agent v0.18.5 (2026.6.01)")
		adapter, err := New("t", bin, "", ProcessLimits{})
		if err != nil {
			t.Fatal(err)
		}
		summary, _, err := adapter.ProbeVerbose(context.Background())
		if err != nil || summary.State != "version_unsupported" || summary.Version != "0.18.5" {
			t.Fatalf("state=%q version=%q err=%v", summary.State, summary.Version, err)
		}
	})
	t.Run("available", func(t *testing.T) {
		bin := stubVersionHermes(t, "Hermes Agent v0.20.5 (2026.8.19)")
		adapter, err := New("t", bin, "", ProcessLimits{})
		if err != nil {
			t.Fatal(err)
		}
		summary, _, err := adapter.ProbeVerbose(context.Background())
		if err != nil || summary.State != "available" || summary.Version != "0.20.5" {
			t.Fatalf("state=%q err=%v", summary.State, err)
		}
	})
}

// TestRealHermesProbeIfAvailable probes the real installed Hermes when
// present (TST-007 posture; skipped as an environment-dependent evidence
// gap otherwise).
func TestRealHermesProbeIfAvailable(t *testing.T) {
	// An installed Hermes below the eligibility floor skips as the same
	// environment-dependent evidence gap as an absent binary (TST-007);
	// the environment probe is shared through testsupport/hermesenv and
	// the eligibility judgment stays here.
	sandbox := hermesenv.NewSandbox(t, func(firstLine string) bool {
		ver, perr := ParseVersionOutput(firstLine)
		return perr == nil && ver.Eligible(MinimumEligibleVersion)
	})
	adapter, err := New("hermes-local", sandbox.Binary, "", ProcessLimits{
		LookupTimeout:        10 * time.Second,
		SubmitTimeout:        20 * time.Second,
		EnvironmentAllowlist: sandbox.EnvironmentAllowlist(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Probe(context.Background()); err != nil {
		t.Fatalf("real hermes probe: %v", err)
	}
}

// TestProbeDiscoversVersionOnce proves the single-discovery property:
// one Probe and one ProbeVerbose each invoke `hermes --version` exactly
// once, so the gated version and the reported version cannot diverge.
func TestProbeDiscoversVersionOnce(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "count")
	bin := newStubHermes(t, `if [ "$1" = "--version" ]; then printf x >> "`+counter+`"; printf 'Hermes Agent v0.20.5 (2026.8.19)\n'; exit 0; fi; exit 3`)
	readCount := func() string {
		raw, err := os.ReadFile(counter)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	adapter, err := New("t", bin, "", ProcessLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Probe(context.Background()); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if got := readCount(); got != "x" {
		t.Fatalf("Probe must discover the version exactly once, saw %q", got)
	}
	if _, _, err := adapter.ProbeVerbose(context.Background()); err != nil {
		t.Fatalf("probe verbose: %v", err)
	}
	if got := readCount(); got != "xx" {
		t.Fatalf("ProbeVerbose must add exactly one discovery, saw %q", got)
	}
}

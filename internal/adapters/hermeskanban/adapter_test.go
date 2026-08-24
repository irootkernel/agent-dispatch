package hermeskanban

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubVersionHermes emits the frozen version first line for --version.
func stubVersionHermes(t *testing.T, versionLine string) string {
	t.Helper()
	return newStubHermes(t, `if [ "$1" = "--version" ]; then printf '%s\nInstall directory: ~/.hermes/hermes-agent\n' '`+versionLine+`'; exit 0; fi; exit 3`)
}

// TestProbeHappyPath proves the read-only probe path: discover version
// from the public CLI, gate it, load the frozen report, require the
// report to match the installed version, and validate the route's
// required capabilities (HER-002/HER-004/HER-005).
func TestProbeHappyPath(t *testing.T) {
	bin := stubVersionHermes(t, "Hermes Agent v0.19.1 (2026.7.30)")
	adapter := New("hermes-kanban-main", bin, machineReport, []string{
		"durable_acceptance", "submit_idempotency_key", "lookup_by_external_ref",
	}, ProcessLimits{})
	caps, err := adapter.Probe(context.Background())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !caps.DurableAcceptance || !caps.SubmitIdempotencyKey || !caps.LookupByExternalRef {
		t.Fatalf("frozen capabilities must surface: %+v", caps)
	}
	if caps.LookupByIdempotencyKey {
		t.Fatalf("the public CLI has no read-only key lookup; the honest report records false (E8-T3): %+v", caps)
	}
	if adapter.Type() != "hermes_kanban" || adapter.ID() != "hermes-kanban-main" {
		t.Fatalf("adapter identity %q/%q", adapter.ID(), adapter.Type())
	}
}

// TestProbeRejectsUnsupportedVersion proves an unsupported Hermes version
// fails the gate before anything is submitted (HER-002, AC-306 posture).
func TestProbeRejectsUnsupportedVersion(t *testing.T) {
	bin := stubVersionHermes(t, "Hermes Agent v0.20.0 (2026.8.10)")
	adapter := New("t", bin, machineReport, nil, ProcessLimits{})
	_, err := adapter.Probe(context.Background())
	var gate *VersionUnsupportedError
	if !errors.As(err, &gate) {
		t.Fatalf("unsupported version must fail the gate, got %v", err)
	}
}

// TestProbeFailsClosedOnUnparsableVersion proves an unparsable version
// output never passes (E0-T4 §2: version discovery must fail closed).
func TestProbeFailsClosedOnUnparsableVersion(t *testing.T) {
	bin := newStubHermes(t, `printf 'weird\n'`)
	adapter := New("t", bin, machineReport, nil, ProcessLimits{})
	_, err := adapter.Probe(context.Background())
	var malformed *MalformedOutputError
	if !errors.As(err, &malformed) {
		t.Fatalf("unparsable version must be a malformed-output failure, got %v", err)
	}
}

// limitedReport writes a report identical to the frozen one except
// resource_mutex is the given value, for mismatch tests.
func limitedReport(t *testing.T, resourceMutex bool) string {
	t.Helper()
	mutex := "false"
	if resourceMutex {
		mutex = "true"
	}
	body := `{
	  "schema_version": "agent-dispatch.hermes-capabilities/v1",
	  "probed_at": "2026-08-19T21:25:24+09:00",
	  "hermes_version": "0.19.1 (2026.7.30)",
	  "interface": "public_cli",
	  "capabilities": {
	    "durable_acceptance": true,
	    "submit_idempotency_key": true,
	    "lookup_by_idempotency_key": true,
	    "lookup_by_external_ref": true,
	    "resource_mutex": ` + mutex + `,
	    "execution_status": true,
	    "cancellation": true,
	    "result_receipt": true
	  },
	  "limits": {"maximum_request_bytes": null},
	  "evidence": []
	}`
	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestProbeCapabilityMismatch proves a required capability the verified
// target does not provide fails validation with the typed error and is
// never silently emulated (HER-005).
func TestProbeCapabilityMismatch(t *testing.T) {
	bin := stubVersionHermes(t, "Hermes Agent v0.19.1 (2026.7.30)")
	adapter := New("wiki", bin, limitedReport(t, false), []string{"durable_acceptance", "resource_mutex"}, ProcessLimits{})
	caps, err := adapter.Probe(context.Background())
	var missing *CapabilityError
	if !errors.As(err, &missing) || len(missing.Missing) != 1 || missing.Missing[0] != "resource_mutex" {
		t.Fatalf("missing capability must fail validation, got %v", err)
	}
	if caps.ResourceMutex {
		t.Fatal("the absent capability must not be emulated as present")
	}
}

// TestProbeReportFreshness proves a report probed against a different
// Hermes version is rejected for this installation.
func TestProbeReportFreshness(t *testing.T) {
	bin := stubVersionHermes(t, "Hermes Agent v0.19.1 (2026.7.30)")
	other := filepath.Join(t.TempDir(), "report.json")
	body := `{
	  "schema_version": "agent-dispatch.hermes-capabilities/v1",
	  "probed_at": "2026-08-19T21:25:24+09:00",
	  "hermes_version": "0.18.0 (2026.6.01)",
	  "interface": "public_cli",
	  "capabilities": {"durable_acceptance": true},
	  "evidence": []
	}`
	if err := os.WriteFile(other, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := New("t", bin, other, nil, ProcessLimits{})
	_, err := adapter.Probe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "re-run the E0-T4 probe") {
		t.Fatalf("stale report must be rejected, got %v", err)
	}
}

// TestProbeVerboseStates proves the validation-surface classification:
// unavailable, version_unsupported, and capability_mismatch are distinct
// operator-visible states.
func TestProbeVerboseStates(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) {
		adapter := New("t", filepath.Join(t.TempDir(), "absent"), machineReport, nil, ProcessLimits{})
		summary, _, err := adapter.ProbeVerbose(context.Background())
		if err != nil || summary.State != "unavailable" {
			t.Fatalf("absent executable: state=%q err=%v", summary.State, err)
		}
	})
	t.Run("version_unsupported", func(t *testing.T) {
		bin := stubVersionHermes(t, "Hermes Agent v0.21.0 (2026.9.01)")
		adapter := New("t", bin, machineReport, nil, ProcessLimits{})
		summary, _, err := adapter.ProbeVerbose(context.Background())
		if err != nil || summary.State != "version_unsupported" || summary.Version != "0.21.0" {
			t.Fatalf("state=%q version=%q err=%v", summary.State, summary.Version, err)
		}
	})
	t.Run("capability_mismatch", func(t *testing.T) {
		bin := stubVersionHermes(t, "Hermes Agent v0.19.1 (2026.7.30)")
		limited := limitedReport(t, false)
		adapter := New("t", bin, limited, []string{"durable_acceptance", "resource_mutex"}, ProcessLimits{})
		summary, _, err := adapter.ProbeVerbose(context.Background())
		var missing *CapabilityError
		if !errors.As(err, &missing) || summary.State != "capability_mismatch" {
			t.Fatalf("state=%q err=%v", summary.State, err)
		}
	})
	t.Run("available", func(t *testing.T) {
		bin := stubVersionHermes(t, "Hermes Agent v0.19.1 (2026.7.30)")
		// The honest report no longer carries the port-level key lookup
		// (E8-T3), so requiring every capability name would mismatch.
		available := []string{"durable_acceptance", "submit_idempotency_key", "lookup_by_external_ref", "resource_mutex", "execution_status", "cancellation", "result_receipt"}
		adapter := New("t", bin, machineReport, available, ProcessLimits{})
		summary, caps, err := adapter.ProbeVerbose(context.Background())
		if err != nil || summary.State != "available" || summary.Version != "0.19.1" {
			t.Fatalf("state=%q err=%v", summary.State, err)
		}
		if !caps.DurableAcceptance {
			t.Fatal("available probe must surface capabilities")
		}
	})
}

// TestRealHermesProbeIfAvailable probes the real installed Hermes when
// present (TST-007 posture; skipped as an environment-dependent evidence
// gap otherwise).
func TestRealHermesProbeIfAvailable(t *testing.T) {
	bin, err := exec.LookPath("hermes")
	if err != nil {
		t.Skip("hermes binary not available")
	}
	// An installed Hermes outside the verified support set is the same
	// environment-dependent evidence gap as an absent binary (TST-007):
	// the probe's guarantees are recorded only for the verified set, and
	// widening it is a fresh E0-T4 probe, not a test assertion.
	if verOut, verr := exec.Command(bin, "--version").Output(); verr == nil {
		firstLine := strings.SplitN(strings.TrimSpace(string(verOut)), "\n", 2)[0]
		if ver, perr := ParseVersionOutput(firstLine); perr == nil && !ver.Supported() {
			t.Skipf("installed hermes %s is outside the verified support set %s (environment-dependent evidence gap, TST-007)", ver.String(), SupportedRangeText())
		}
	}
	adapter := New("hermes-local", bin, machineReport, []string{
		"durable_acceptance", "submit_idempotency_key", "lookup_by_external_ref",
	}, ProcessLimits{LookupTimeout: 10 * time.Second, SubmitTimeout: 20 * time.Second})
	caps, err := adapter.Probe(context.Background())
	if err != nil {
		t.Fatalf("real hermes probe: %v", err)
	}
	if !caps.DurableAcceptance {
		t.Fatal("real installation must satisfy the durable contract per the E0-T4 report")
	}
}

// TestProbeDiscoversVersionOnce proves the single-discovery property:
// one Probe and one ProbeVerbose each invoke `hermes --version` exactly
// once, so the gated version and the reported version cannot diverge.
func TestProbeDiscoversVersionOnce(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "count")
	bin := newStubHermes(t, `if [ "$1" = "--version" ]; then printf x >> "`+counter+`"; printf 'Hermes Agent v0.19.1 (2026.7.30)\n'; exit 0; fi; exit 3`)
	readCount := func() string {
		raw, err := os.ReadFile(counter)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	adapter := New("t", bin, machineReport, nil, ProcessLimits{})
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

// TestProbeVerboseConfigErrorState covers the config_error classification
// at the adapter level: an unknown required-capability name is a
// configuration defect, not target unavailability.
func TestProbeVerboseConfigErrorState(t *testing.T) {
	bin := stubVersionHermes(t, "Hermes Agent v0.19.1 (2026.7.30)")
	adapter := New("t", bin, machineReport, []string{"durable_acceptance", "teleportaion"}, ProcessLimits{})
	summary, _, err := adapter.ProbeVerbose(context.Background())
	if err == nil || summary.State != "config_error" {
		t.Fatalf("unknown capability name must classify as config_error with the error returned, got state=%q err=%v", summary.State, err)
	}
}

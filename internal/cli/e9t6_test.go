package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
)

// E9-T6 regression evidence (D-023 F1, F2): the capability report is
// mandatory enable evidence in every executable-availability state, and
// a delivery-evidence change pauses the acknowledged route.

// TestE9T6EnableGateRequiresReportWithoutExecutable proves F2: with the
// executable absent (probe "unavailable"), a missing or unreadable
// capability report refuses the enable at exit 3 — the recorded defect
// let the route reach production-enabled with neither executable nor
// report — while an honest report still enables with the liveness
// warning preserved.
func TestE9T6EnableGateLivenessWarningKeepsEligibilityDeferred(t *testing.T) {
	configPath, _ := e4t3Fixture(t)
	cfgDir := filepath.Dir(configPath)
	enable := func() (int, string, string) {
		cfg, err := config.Load(configPath)
		if err != nil {
			t.Fatal(err)
		}
		rev, ok := config.RouteRevision(cfg, "wiki")
		if !ok {
			t.Fatal("revision unavailable")
		}
		var out, errb bytes.Buffer
		code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", rev, "--yes"}, &out, &errb)
		return code, errb.String(), out.String()
	}

	// An absent executable keeps the enable a warning, never a refusal:
	// re-acknowledging a paused production route is not hostage to the
	// target being up, and eligibility re-rides the probe at submit time.
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	idx := strings.Index(string(raw), "executable:")
	if idx < 0 {
		t.Fatal("fixture config carries no executable")
	}
	rest := string(raw)[idx:]
	lineEnd := strings.IndexByte(rest, '\n')
	e5t4Rewrite(t, configPath, rest[:lineEnd], "executable: "+filepath.Join(cfgDir, "hermes-absent"))
	code, stderr, stdout := enable()
	if code != 0 {
		t.Fatalf("an unavailable target must enable with a warning, got %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "warning: hermes_targets.hermes-main probes") || !strings.Contains(stdout, `"enabled"`) {
		t.Fatalf("the unavailable-target warning and the enabled envelope must both appear: stderr=%q stdout=%q", stderr, stdout)
	}

	// A live below-floor executable: refused at exit 3 even though the
	// route was previously acknowledged (the eligibility gate owns the
	// refusal; the E11-T2 capability probe tightens it further).
	bin := filepath.Join(cfgDir, "hermes-below")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf 'Hermes Agent v0.18.3 (2026.6.1)\\n'; exit 0; fi\nexit 3\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	idx = strings.Index(string(raw), "executable:")
	rest = string(raw)[idx:]
	lineEnd = strings.IndexByte(rest, '\n')
	e5t4Rewrite(t, configPath, rest[:lineEnd], "executable: "+bin)
	if code, stderr, _ := enable(); code != 3 || !strings.Contains(stderr, "version_unsupported") {
		t.Fatalf("a below-floor executable must refuse at exit 3, got %d: %s", code, stderr)
	}
}

// TestE9T6DeliveryEvidenceChangePausesUntilReacknowledged proves F1 end
// to end: after enabling at revision A, changing only the lookup bound —
// a delivery-evidence field the pre-E9-T6 revision ignored — moves the
// computed revision to B, the drain refuses with the re-acknowledge
// guidance, and the route submits again only after acknowledging B.
func TestE9T6DeliveryEvidenceChangePausesUntilReacknowledged(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	dispatchID, _ := decodeEnvelope(t, &out)["dispatch_id"].(string)
	if dispatchID == "" {
		t.Fatal("no-submit dispatch produced no dispatch id")
	}
	if code := enableRouteAck(t, configPath, "wiki"); code != 0 {
		t.Fatalf("initial acknowledgement: %d", code)
	}

	// The delivery-evidence change: only the lookup bound moves.
	e5t4Rewrite(t, configPath, "lookup_timeout: 30s", "lookup_timeout: 45s")

	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain under a paused route: %s", errb.String())
	}
	body := out.String()
	if strings.Contains(body, `"accepted"`) || !strings.Contains(body, "re-acknowledge") {
		t.Fatalf("the delivery-evidence change must pause submission until re-acknowledgement: %s", body)
	}
	if code := enableRouteAck(t, configPath, "wiki"); code != 0 {
		t.Fatalf("re-acknowledgement: %d", code)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain after re-ack: %s", errb.String())
	}
	if !strings.Contains(out.String(), `"accepted"`) {
		t.Fatalf("the intent must submit after re-acknowledgement: %s", out.String())
	}
}

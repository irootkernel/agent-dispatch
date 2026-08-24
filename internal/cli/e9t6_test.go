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
func TestE9T6EnableGateRequiresReportWithoutExecutable(t *testing.T) {
	configPath, _ := e4t3Fixture(t)
	cfgDir := filepath.Dir(configPath)
	reportPath := filepath.Join(cfgDir, "cap-e9t6.json")

	// Point the fixture's target at an executable that does not exist
	// and at this test's report copy.
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
	pointAtReport := func() {
		raw, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		idx := strings.Index(string(raw), "capability_report:")
		if idx < 0 {
			t.Fatal("fixture config carries no capability_report")
		}
		rest := string(raw)[idx:]
		lineEnd := strings.IndexByte(rest, '\n')
		e5t4Rewrite(t, configPath, rest[:lineEnd], "capability_report: "+reportPath)
	}
	writeReport := func(version string) {
		body := `{"schema_version":"agent-dispatch.hermes-capabilities/v1","probed_at":"2026-08-19T21:25:24+09:00","hermes_version":"` + version + `","interface":"public_cli","capabilities":{"durable_acceptance":true,"submit_idempotency_key":true,"lookup_by_idempotency_key":false,"lookup_by_external_ref":true,"resource_mutex":true,"execution_status":true,"cancellation":true,"result_receipt":true},"limits":{"maximum_request_bytes":null},"evidence":[]}`
		if err := os.WriteFile(reportPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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

	// Neither executable nor report: the enable must refuse at exit 3.
	pointAtReport()
	if code, stderr, _ := enable(); code != 3 || !strings.Contains(stderr, "config_invalid") {
		t.Fatalf("a missing report with an absent executable must refuse at exit 3, got %d: %s", code, stderr)
	}
	// An unreadable report: refused the same way.
	if err := os.WriteFile(reportPath, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stderr, _ := enable(); code != 3 {
		t.Fatalf("an unreadable report with an absent executable must refuse at exit 3, got %d: %s", code, stderr)
	}
	// A structurally valid report recording an unsupported Hermes
	// version: refused without a live target, because the supported set
	// is build-time evidence (the round-1 review observation).
	writeReport("0.18.3 (2026.6.1)")
	if code, stderr, _ := enable(); code != 3 || !strings.Contains(stderr, "outside the runtime-verified set") {
		t.Fatalf("an unsupported-version report with an absent executable must refuse at exit 3, got %d: %s", code, stderr)
	}
	// The honest report enables with the liveness warning preserved.
	writeReport("0.19.1 (2026.7.30)")
	code, stderr, stdout := enable()
	if code != 0 {
		t.Fatalf("the honest report with an absent executable must enable: %d %s", code, stderr)
	}
	if !strings.Contains(stderr, "warning: target hermes-main probes") || !strings.Contains(stdout, `"enabled"`) {
		t.Fatalf("the unavailable-target warning and the enabled envelope must both appear: stderr=%q stdout=%q", stderr, stdout)
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

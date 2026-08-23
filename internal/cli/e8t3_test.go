package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
)

// E8-T3 regression evidence (H-2, H-7): the acknowledged-revision pause
// and the production enable gate.

// TestE8T3BehaviorChangePausesUntilReacknowledged proves H-2's pause:
// after enabling at revision A, a behavior-affecting include-pattern
// change leaves the stored intent planned under A while the computed
// revision moves to B — the drain refuses with the re-acknowledge
// guidance, and the route submits again only after `route enable`
// acknowledges B.
func TestE8T3BehaviorChangePausesUntilReacknowledged(t *testing.T) {
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

	// The behavior-affecting change: a new include pattern moves the
	// computed revision away from the acknowledged one.
	e5t4Rewrite(t, configPath, "include: [\"**/*.md\"]", "include: [\"**/*.md\", \"Inbox/special.md\"]")

	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain under a paused route: %s", errb.String())
	}
	body := out.String()
	if strings.Contains(body, `"accepted"`) || !strings.Contains(body, "re-acknowledge") {
		t.Fatalf("the behavior change must pause submission until re-acknowledgement: %s", body)
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

// TestE8T3EnableGateRefusesStaleReportAndWeakGuarantees proves H-7: the
// production enable refuses a report recorded against a different
// Hermes version and a report without the unconditional
// durable_acceptance and submit_idempotency_key guarantees.
func TestE8T3EnableGateRefusesStaleReportAndWeakGuarantees(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	_ = vault
	cfgDir := filepath.Dir(configPath)
	reportPath := filepath.Join(cfgDir, "cap-gate.json")
	writeReport := func(version string, durable bool) {
		d := "true"
		if !durable {
			d = "false"
		}
		body := `{"schema_version":"agent-dispatch.hermes-capabilities/v1","probed_at":"2026-08-19T21:25:24+09:00","hermes_version":"` + version + `","interface":"public_cli","capabilities":{"durable_acceptance":` + d + `,"submit_idempotency_key":true,"lookup_by_idempotency_key":false,"lookup_by_external_ref":true,"resource_mutex":true,"execution_status":true,"cancellation":true,"result_receipt":true},"limits":{"maximum_request_bytes":null},"evidence":[]}`
		if err := os.WriteFile(reportPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	revisionOf := func() string {
		cfg, err := config.Load(configPath)
		if err != nil {
			t.Fatal(err)
		}
		rev, ok := config.RouteRevision(cfg, "wiki")
		if !ok {
			t.Fatal("revision unavailable")
		}
		return rev
	}
	enable := func() (int, string) {
		var out, errb bytes.Buffer
		code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", revisionOf(), "--yes"}, &out, &errb)
		return code, errb.String()
	}
	pointAtReport := func() {
		// Point the fixture's target at the gate's report copy.
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

	// A report recorded against a different version: the enable must
	// refuse (the pre-E8-T3 defect let it pass).
	pointAtReport()
	writeReport("0.19.0 (2026.7.30)", true)
	if code, stderr := enable(); code != 3 || !strings.Contains(stderr, "config_capability_missing") {
		t.Fatalf("a stale report must refuse the enable at exit 3, got %d: %s", code, stderr)
	}
	// A report without the durable guarantee: refused even though the
	// operator's required list never named it.
	writeReport("0.19.1 (2026.7.30)", false)
	if code, stderr := enable(); code != 3 || !strings.Contains(stderr, "durable_acceptance") {
		t.Fatalf("a non-durable report must refuse the production enable, got %d: %s", code, stderr)
	}
	// The honest report enables cleanly.
	writeReport("0.19.1 (2026.7.30)", true)
	if code, stderr := enable(); code != 0 {
		t.Fatalf("the honest report must enable: %d %s", code, stderr)
	}
}

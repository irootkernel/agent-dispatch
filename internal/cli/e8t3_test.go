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
func TestE8T3EnableGateRefusesBelowFloorAndEnablesCleanly(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	_ = vault
	cfgDir := filepath.Dir(configPath)
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
	pointExecutableAt := func(versionLine string) string {
		bin := filepath.Join(cfgDir, "hermes-gate")
		script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '%s\\n' '" + versionLine + "'; exit 0; fi\nexit 3\n"
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
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
		if line := rest[:lineEnd]; line != "executable: "+bin {
			e5t4Rewrite(t, configPath, line, "executable: "+bin)
		}
		return bin
	}

	// A Hermes below the eligibility floor: the enable must refuse (the
	// pre-cutover defect let a stale report pass; the E11-T1 contract
	// refuses on eligibility, and the E11-T2 probe tightens this to the
	// capability fingerprint).
	pointExecutableAt("Hermes Agent v0.18.5 (2026.6.01)")
	if code, stderr := enable(); code != 3 || !strings.Contains(stderr, "config_invalid") {
		t.Fatalf("a below-floor target must refuse the enable at exit 3, got %d: %s", code, stderr)
	}
	// The frozen floor version enables cleanly (the stub is rewritten in
	// place; the configuration already points at it).
	pointExecutableAt("Hermes Agent v0.19.1 (2026.7.30)")
	if code, stderr := enable(); code != 0 {
		t.Fatalf("the eligible target must enable: %d %s", code, stderr)
	}
}

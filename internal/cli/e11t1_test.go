package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
)

// e11t1MarkLegacy rewrites one accepted dispatch as dead-lettered work
// created under a foreign (pre-cutover) route revision, exactly as a
// pre-upgrade database would hold it, so the route carries unresolved
// legacy work for the DAT-013 enable gate.
func e11t1MarkLegacy(t *testing.T, configPath, dispatchID string) {
	t.Helper()
	store, err := openStateStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Exec(`UPDATE dispatch_intents
		SET state = 'dead_lettered', route_revision = 'legacy-cutover-rev', lease_owner = NULL, lease_expires_at = NULL
		WHERE dispatch_id = ?`, dispatchID); err != nil {
		t.Fatalf("mark legacy intent: %v", err)
	}
}

// TestE11T1EnableBlockedByUnresolvedLegacyWork proves DAT-013 end to
// end: enabling a route under the destinations contract refuses while
// unresolved legacy work from a different route revision exists, names
// the resolution exits, and enables cleanly once the work is resolved
// through the documented operator exit (dead-letter discard).
func TestE11T1EnableBlockedByUnresolvedLegacyWork(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	legacyDispatch := e4t4Accepted(t, configPath, vault)
	e11t1MarkLegacy(t, configPath, legacyDispatch)

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	rev, ok := config.RouteRevision(cfg, "wiki")
	if !ok {
		t.Fatal("revision unavailable")
	}
	enable := func() (int, string) {
		var out, errb bytes.Buffer
		code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", rev, "--yes"}, &out, &errb)
		return code, errb.String()
	}
	code, stderr := enable()
	if code != 14 {
		t.Fatalf("unresolved legacy work must refuse the enable at exit 14, got %d: %s", code, stderr)
	}
	for _, want := range []string{"unresolved legacy work", "DAT-013"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal must mention %q: %s", want, stderr)
		}
	}

	// Resolve the dead letter through the documented operator exit.
	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "discard", "--config", configPath, legacyDispatch, "--reason", "cutover resolution"}, &out, &errb); code != 0 {
		t.Fatalf("discard the dead-lettered legacy dispatch: %s", errb.String())
	}
	if code, stderr := enable(); code != 0 {
		t.Fatalf("enable must succeed after the resolution, got %d: %s", code, stderr)
	}
}

// TestE11T1MultiDestinationRouteFailsClosed proves the pre-E12 bound at
// the operator surface: a valid two-destination route parses, but
// dispatching it fails closed naming the E12 lanes, and enabling names
// the same bound.
func TestE11T1MultiDestinationRouteFailsClosed(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := string(raw)
	start := strings.Index(updated, "    fanout_mode: all\n    destinations:\n")
	end := strings.Index(updated, "\n    submission_retry:")
	if start < 0 || end <= start {
		t.Fatal("fixture no longer carries the single-destination block")
	}
	dest := "    fanout_mode: all\n    destinations:\n" +
		"      - id: alpha\n        target: hermes-main\n        profile: wiki-maintainer\n        skills: [llm-wiki]\n        workstream: indexing\n        mutex_key: wiki-publish\n        execution_hints:\n          max_runtime: 30m\n          max_attempts: 2\n" +
		"      - id: beta\n        target: hermes-main\n        profile: wiki-maintainer\n        skills: [llm-wiki]\n        workstream: review\n        execution_hints:\n          max_runtime: 30m\n          max_attempts: 2"
	updated = updated[:start] + dest + updated[end:]
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("a two-destination route must parse and validate: %v", err)
	}
	rev, _ := config.RouteRevision(cfg, "wiki")

	var out, errb bytes.Buffer
	code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", rev, "--yes"}, &out, &errb)
	if code != 3 || !strings.Contains(errb.String(), "E12") {
		t.Fatalf("enable must refuse the multi-destination route with the E12 bound, got %d: %s", code, errb.String())
	}

	out.Reset()
	errb.Reset()
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		code = Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if code != 3 || !strings.Contains(errb.String(), "E12") {
		t.Fatalf("dispatch must refuse the multi-destination route with the E12 bound, got %d: %s", code, errb.String())
	}
}

// TestE11T1NormalizedShowsDestinations proves the normalized display
// covers the cutover surface: sorted destinations, hermes targets, and
// the declared notification policy with the sink's secret reference
// (never a value).
func TestE11T1NormalizedShowsDestinations(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "examples", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := cfg.Normalized()
	if err != nil {
		t.Fatal(err)
	}
	text := string(normalized)
	for _, want := range []string{
		`"fanout_mode":"all"`,
		`"hermes_targets"`,
		`"minimum_version":"0.19.1"`,
		`"compatibility":"capability_probe"`,
		`"destinations":[{"id":"indexing"`,
		`"notifications"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("normalized output must contain %s: %s", want, text)
		}
	}
	if strings.Contains(text, "capability_report") || strings.Contains(text, `"dispatch":`) {
		t.Errorf("normalized output must not carry the retired surface: %s", text)
	}
}

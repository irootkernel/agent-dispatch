package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
)

// stubExeOf extracts the stub hermes executable path from the fixture
// configuration so a second target can share it.
func stubExeOf(t *testing.T, configPath string) string {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "executable: "); ok {
			return v
		}
	}
	t.Fatal("fixture config carries no stub executable")
	return ""
}

// E7-T3 regression suite: POL-008/SEC-010 submit-path revalidation (H-1)
// and durable path facts (H-2), proven through the CLI product path.

// TestStaleIntentRebuiltUnderActiveRevision proves H-1's revision half:
// a configuration change between planning and submission never submits
// the stored plan; the stale intent is superseded through the declared
// edge and the replacement carries the active revision and is submitted.
func TestStaleIntentRebuiltUnderActiveRevision(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	original, _ := decodeEnvelope(t, &out)["dispatch_id"].(string)
	if original == "" {
		t.Fatal("no-submit dispatch produced no dispatch id")
	}

	// The operator changes behavior-affecting route configuration: the
	// stored plan's revision is revoked while the intent waits.
	e5t4Rewrite(t, configPath, "automatic_threshold: 25", "automatic_threshold: 40")

	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	body := out.String()
	if !strings.Contains(body, `"accepted"`) {
		t.Fatalf("the rebuilt replacement must be submitted: %s", body)
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var originalState string
	if err := store.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, original).Scan(&originalState); err != nil || originalState != "superseded" {
		t.Fatalf("the stale original must be superseded, not submitted: %q %v", originalState, err)
	}
	var rebuiltRevision string
	if err := store.QueryRow(`SELECT route_revision FROM dispatch_intents WHERE state = 'accepted'`).Scan(&rebuiltRevision); err != nil {
		t.Fatal(err)
	}
	acceptedRevision, ok := routeRevisionOf(t, configPath)
	if !ok || rebuiltRevision != acceptedRevision {
		t.Fatalf("the replacement must carry the active revision %q, got %q", acceptedRevision, rebuiltRevision)
	}
	// The invalidation is audited on both the intent transition and the
	// superseding decision.
	var invalidated int
	if err := store.QueryRow(`SELECT COUNT(*) FROM state_transitions WHERE entity_id = ? AND to_state = 'superseded' AND context_json LIKE '%route_revision_invalidated%'`, original).Scan(&invalidated); err != nil || invalidated < 1 {
		t.Fatalf("the supersede must carry the declared reason: %d %v", invalidated, err)
	}
	var decisionCodes int
	if err := store.QueryRow(`SELECT COUNT(*) FROM policy_decisions WHERE reason_codes_json LIKE '%route_revision_invalidated%' AND supersedes_decision_id IS NOT NULL`).Scan(&decisionCodes); err != nil || decisionCodes < 1 {
		t.Fatalf("the replacement decision must record the invalidation: %d %v", decisionCodes, err)
	}
}

// TestStaleTargetRepointNeverSubmitsFalseLineage proves H-1's target
// half: when the route's target is re-pointed between planning and
// submission, the stored intent is never submitted as-is; the
// replacement records the target it is actually submitted against.
func TestStaleTargetRepointNeverSubmitsFalseLineage(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	original, _ := decodeEnvelope(t, &out)["dispatch_id"].(string)

	// Re-point the route's target at a second stub board: the stored
	// intent's target identity is now false.
	e5t4Rewrite(t, configPath, "targets:\n  hermes-main:", "targets:\n  hermes-backup:\n    type: hermes-kanban\n    board: agent-dispatch-backup\n    executable: "+stubExeOf(t, configPath)+"\n    capability_report: ../../docs/integrations/hermes-capability-report.json\n    required_capabilities: [durable_acceptance, submit_idempotency_key, lookup_by_external_ref]\n    submit_timeout: 30s\n    lookup_timeout: 30s\n    environment_allowlist: [PATH, HOME]\n  hermes-main:")
	e5t4Rewrite(t, configPath, "target: hermes-main", "target: hermes-backup")

	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var originalState string
	if err := store.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, original).Scan(&originalState); err != nil || originalState != "superseded" {
		t.Fatalf("the re-pointed original must be superseded: %q %v", originalState, err)
	}
	var scope string
	if err := store.QueryRow(`SELECT target_scope FROM dispatch_intents WHERE state = 'accepted'`).Scan(&scope); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(scope, "agent-dispatch-backup") {
		t.Fatalf("the replacement must record the current target scope, got %q", scope)
	}
}

// TestUnchangedModifySuppressedDurably proves H-2: after one durable
// dispatch records its path facts, a byte-identical modify arrival is
// suppressed with the unchanged_content reason visible in the persisted
// decision, and nothing is dispatched again.
func TestUnchangedModifySuppressedDurably(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)

	// First arrival: the note exists on disk, is created and dispatched;
	// the ingestion transaction records the path fact.
	os.MkdirAll(filepath.Join(vault, "Inbox"), 0o755)
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "same.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/same.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("first dispatch: %s", errb.String())
	}
	firstDispatch, _ := decodeEnvelope(t, &out)["dispatch_id"].(string)
	if firstDispatch == "" {
		t.Fatal("the first dispatch produced no dispatch id")
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var facts int
	if err := store.QueryRow(`SELECT COUNT(*) FROM path_facts WHERE path = 'Inbox/same.md' AND "exists" = 1`).Scan(&facts); err != nil || facts != 1 {
		t.Fatalf("the ingestion transaction must record the path fact: %d %v", facts, err)
	}

	// Second arrival: the same bytes. The durable facts suppress the
	// modify with the unchanged_content reason (AC-102).
	out.Reset()
	errb.Reset()
	withStdin(t, `[{"name":"Inbox/same.md","exists":true,"new":false,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("unchanged arrival: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["disposition"] != "drop" {
		t.Fatalf("a byte-identical modify must drop, got %v", res["disposition"])
	}
	var suppressed int
	if err := store.QueryRow(`SELECT COUNT(*) FROM policy_decisions WHERE reason_codes_json LIKE '%unchanged_content%'`).Scan(&suppressed); err != nil || suppressed < 1 {
		t.Fatalf("the unchanged_content reason must reach the decision: %d %v", suppressed, err)
	}
	// The create_delete_never_existed propagation is covered by the
	// planner unit tests (it needs a coalesced create+delete pair that a
	// single Watchman payload cannot express).

	var intents int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents); err != nil || intents != 1 {
		t.Fatalf("the suppressed arrival must not create a second intent: %d %v", intents, err)
	}
}

// TestRerunCarriesActiveRevision proves the round-1 remediation: a rerun
// of work planned under an older revision persists the replacement and
// its superseding decision under the active revision (H-1).
func TestRerunCarriesActiveRevision(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	original, _ := decodeEnvelope(t, &out)["dispatch_id"].(string)
	e5t4Rewrite(t, configPath, "automatic_threshold: 25", "automatic_threshold: 40")
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "rerun", "--config", configPath, "--yes", "--reason", "redo under new config", original}, &out, &errb); code != 0 {
		t.Fatalf("rerun: %s", errb.String())
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	active, _ := routeRevisionOf(t, configPath)
	var rerunRevision string
	if err := store.QueryRow(`SELECT route_revision FROM dispatch_intents WHERE state = 'ready' AND dispatch_id != ?`, original).Scan(&rerunRevision); err != nil {
		t.Fatal(err)
	}
	if rerunRevision != active {
		t.Fatalf("the rerun must carry the active revision %q, got %q", active, rerunRevision)
	}
}

// routeRevisionOf computes the current route revision from the test
// configuration.
func routeRevisionOf(t *testing.T, configPath string) (string, bool) {
	t.Helper()
	cfg, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		t.Fatal(err)
	}
	return config.RouteRevision(cfg, "wiki")
}

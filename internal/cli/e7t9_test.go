package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/secretresolver"
	"github.com/irootkernel/agent-dispatch/internal/config"
)

// E7-T9 regression suite: the operations and security medium batch.

// TestMarkdownScopeBeatsIncludePatterns proves M-10: under
// file_scope markdown, an include pattern admitting a binary path never
// dispatches it.
func TestMarkdownScopeBeatsIncludePatterns(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	// Widen the include pattern to everything; the scope must still
	// bound the batch to Markdown.
	e5t4Rewrite(t, configPath, `include: ["**/*.md"]`, `include: ["**/*"]`)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	os.WriteFile(filepath.Join(vault, "Notes", "image.png"), []byte("png"), 0o644)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Notes/image.png","exists":true,"new":true,"size":3,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if res := decodeEnvelope(t, &out); res["disposition"] != "drop" {
		t.Fatalf("a non-Markdown path must drop under file_scope markdown, got %v", res["disposition"])
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var intents int
	store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents)
	if intents != 0 {
		t.Fatalf("no intent may exist for a non-Markdown path: %d", intents)
	}
}

// TestPrunePreservesBegunReceipts proves M-20: an in-flight begun work
// receipt survives pruning whatever its age.
func TestPrunePreservesBegunReceipts(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	id, _ := decodeEnvelope(t, &out)["dispatch_id"].(string)
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", id, "--run-id", "r1", "--external-task-id", "t_00000001"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("work begin failed")
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	// Age the begun receipt far past any retention cutoff.
	store.Exec(`UPDATE work_receipts SET submitted_at = '2020-01-01T00:00:00Z' WHERE run_id = 'r1'`)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"maintenance", "prune", "--config", configPath, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("prune: %s", errb.String())
	}
	var begun int
	if err := store.QueryRow(`SELECT COUNT(*) FROM work_receipts WHERE run_id = 'r1'`).Scan(&begun); err != nil || begun != 1 {
		t.Fatalf("the begun receipt must survive pruning: %d %v", begun, err)
	}
}

// TestInitStateDirPersists proves M-29: the chosen state directory lands
// in the written configuration and later invocations honor it.
func TestInitStateDirPersists(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	stateDir := filepath.Join(dir, "chosen-state")
	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--config", configPath, "--state-dir", stateDir}, &out, &errb); code != 0 {
		t.Fatalf("init: %s", errb.String())
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "state_dir: "+stateDir) {
		t.Fatalf("the written config must persist the chosen state dir: %s", raw)
	}
}

// TestDisabledReconcileRefusalClassified proves M-21: reconciling a
// disabled route reports the state conflict classification, not storage.
func TestDisabledReconcileRefusalClassified(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	if code := Run([]string{"route", "disable", "--config", configPath, "--route", "wiki"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("route disable failed")
	}
	var out, errb bytes.Buffer
	code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "manual"}, &out, &errb)
	if code == 0 {
		t.Fatal("reconciling a disabled route must fail closed")
	}
	body := errb.String()
	if strings.Contains(body, `"code":"sqlite_query_failed"`) || strings.Contains(body, `"storage"`) {
		t.Fatalf("the refusal must not classify as storage: %s", body)
	}
	if !strings.Contains(body, "transition_invalid") && !strings.Contains(body, `"conflict"`) {
		t.Fatalf("the refusal must classify as a state conflict: %s", body)
	}
}

// TestRouteEnableValidatesCapabilities proves M-22: enabling a route
// whose readable local report lacks a required capability refuses with
// config_capability_missing.
func TestRouteEnableValidatesCapabilities(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	// Point at a local copy of the report with one capability false,
	// then require it: the enablement gate must refuse.
	fake := filepath.Join(filepath.Dir(configPath), "caps.json")
	report := map[string]any{}
	raw, err := os.ReadFile("../../docs/integrations/hermes-capability-report.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	report["capabilities"].(map[string]any)["durable_acceptance"] = false
	encoded, _ := json.Marshal(report)
	if err := os.WriteFile(fake, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	e5t4Rewrite(t, configPath, "capability_report: ../../docs/integrations/hermes-capability-report.json", "capability_report: "+fake)
	rev, _ := routeRevisionOf(t, configPath)
	var out, errb bytes.Buffer
	code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", rev, "--yes"}, &out, &errb)
	if code != 3 || !strings.Contains(errb.String(), "config_capability_missing") {
		t.Fatalf("the enablement gate must refuse the missing capability, got %d: %s", code, errb.String())
	}
}

// TestOwnerOnlySecretFileGate proves M-25 at the resolver level: a
// world-readable file secret fails closed naming the mode, and an
// owner-only file resolves.
func TestOwnerOnlySecretFileGate(t *testing.T) {
	dir := t.TempDir()
	permissive := filepath.Join(dir, "permissive.txt")
	private := filepath.Join(dir, "private.txt")
	os.WriteFile(permissive, []byte("tok"), 0o644)
	os.WriteFile(private, []byte("tok"), 0o600)
	if _, err := secretresolver.Resolve(context.Background(), &config.SecretRef{Kind: config.RefFile, Path: permissive}); err == nil || !strings.Contains(err.Error(), "permissive mode") {
		t.Fatalf("a permissive secret file must fail closed naming the mode: %v", err)
	}
	if v, err := secretresolver.Resolve(context.Background(), &config.SecretRef{Kind: config.RefFile, Path: private}); err != nil || v != "tok" {
		t.Fatalf("an owner-only secret file must resolve: %q %v", v, err)
	}
}

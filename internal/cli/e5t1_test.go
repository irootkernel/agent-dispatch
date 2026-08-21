package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootkernel/jjukkumi/internal/adapters/sqlite"
	"github.com/rootkernel/jjukkumi/internal/config"
	"github.com/rootkernel/jjukkumi/internal/platformpaths"
)

// e5t1Store opens the fixture's durable store directly for assertions.
func e5t1Store(t *testing.T, configPath string) *sqlite.Store {
	t.Helper()
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := platformpaths.ResolveStateDir(cfg.Instance.StateDir)
	store, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// e5t1CountTransitions counts work-receipt audit transitions for one
// dispatch (the invalid-receipt audit evidence, FBK-003).
func e5t1CountTransitions(t *testing.T, store *sqlite.Store, dispatchID, toState string) int {
	t.Helper()
	var n int
	if err := store.QueryRow(`SELECT COUNT(*) FROM state_transitions WHERE entity_type = 'work_receipt' AND entity_id = ? AND to_state = ?`, dispatchID, toState).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// e5t1RouteState reads the route's current state machine value.
func e5t1RouteState(t *testing.T, store *sqlite.Store) string {
	t.Helper()
	var routeState string
	if err := store.QueryRow(`SELECT route_state FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&routeState); err != nil {
		t.Fatal(err)
	}
	return routeState
}

const e5t1GoodDigest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// TestWorkBeginValidRecordsRun proves a validated begin receipt is
// persisted with valid state and is inspectable through receipts list
// (FBK-005, OPS-002).
func TestWorkBeginValidRecordsRun(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	var out, errb bytes.Buffer
	code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb)
	if code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	begun := decodeEnvelope(t, &out)
	if begun["status"] != "begun" || begun["run_id"] != "run-1" || begun["receipt_id"] == "" {
		t.Fatalf("begin result wrong: %v", begun)
	}

	// The receipt row is inspectable through the receipts surface.
	out.Reset()
	errb.Reset()
	code = Run([]string{"receipts", "list", "--config", configPath, "--dispatch", dispatchID, "--kind", "work"}, &out, &errb)
	if code != 0 {
		t.Fatalf("receipts list: %s", errb.String())
	}
	listing := decodeEnvelope(t, &out)
	if listing["count"].(float64) != 1 {
		t.Fatalf("work receipt must be listed: %v", listing)
	}

	// The audit transition recorded the begun receipt.
	store := e5t1Store(t, configPath)
	if e5t1CountTransitions(t, store, dispatchID, "begun") != 1 {
		t.Fatal("begun receipt must append one audit transition")
	}
}

// TestWorkBeginRejectsForgedReceipts proves the lineage checks: a forged
// external task id, an unknown dispatch, and a replayed run are each
// rejected with work_receipt_invalid (exit 4) and audited, with no valid
// row persisted (E5-T1 acceptance, FBK-003).
func TestWorkBeginRejectsForgedReceipts(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	cases := []struct {
		name       string
		args       []string
		wantCode   int
		wantPhrase string
	}{
		{"forged external task", []string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-x", "--external-task-id", "t_99999999"}, 4, "does not match the accepted task"},
		{"unknown dispatch", []string{"work", "begin", "--config", configPath, "--dispatch-id", "disp-absent", "--run-id", "run-x"}, 4, "dispatch_not_found"},
		{"missing run", []string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID}, 2, "requires --dispatch-id and --run-id"},
	}
	for _, tc := range cases {
		var out, errb bytes.Buffer
		if code := Run(tc.args, &out, &errb); code != tc.wantCode || !strings.Contains(errb.String(), tc.wantPhrase) {
			t.Fatalf("%s: want exit %d with %q, got %d: %s", tc.name, tc.wantCode, tc.wantPhrase, code, errb.String())
		}
		if out.Len() != 0 {
			t.Fatalf("%s: stdout must stay empty on failure", tc.name)
		}
	}

	// A rejected receipt is audited (the forged one), never persisted.
	store := e5t1Store(t, configPath)
	if e5t1CountTransitions(t, store, dispatchID, "invalid") != 1 {
		t.Fatal("the forged receipt must leave one invalid audit transition")
	}
	var rows int
	if err := store.QueryRow(`SELECT COUNT(*) FROM work_receipts WHERE dispatch_id = ?`, dispatchID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("no receipt row may persist for rejected submissions: %d %v", rows, err)
	}

	// A replayed run is rejected after the first begin succeeds.
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("first begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1"}, &out, &errb); code != 4 || !strings.Contains(errb.String(), "already recorded") {
		t.Fatalf("replayed run must be rejected exit 4, got %d: %s", code, errb.String())
	}
}

// TestWorkCompleteValidatesManifest proves the manifest rules: contained
// relative paths only, well-formed digests, bounded change counts, and
// no note bodies (E5-T1 acceptance, SEC-002, SEC-009).
func TestWorkCompleteValidatesManifest(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	manifests := map[string]string{
		"absolute path":           `[{"path":"/etc/passwd"}]`,
		"traversal":               `[{"path":"../escape.md"}]`,
		"non-canonical":           `[{"path":"./Inbox/new.md"}]`,
		"bad digest":              `[{"path":"Inbox/new.md","after_digest":"md5:zz"}]`,
		"note body":               `[{"path":"Inbox/new.md","note":"agent thoughts"}]`,
		"symlink escape":          `[{"path":"link-out.md"}]`,
		"over change limit":       `[` + strings.TrimRight(strings.Repeat(`{"path":"Inbox/new.md"},`, 1001), ",") + `]`,
		"full doc wrong run":      `{"schema_version":"jjukkumi.work-receipt/v1","dispatch_id":"` + dispatchID + `","run_id":"other-run","status":"completed","changes":[]}`,
		"full doc bad schema":     `{"schema_version":"jjukkumi.work-receipt/v2","dispatch_id":"` + dispatchID + `","run_id":"run-1","status":"completed","changes":[]}`,
		"full doc wrong resource": `{"schema_version":"jjukkumi.work-receipt/v1","dispatch_id":"` + dispatchID + `","run_id":"run-1","resource_id":"wrong-vault","status":"completed","changes":[]}`,
	}
	// A symlink inside the vault pointing outside exercises the SEC-002
	// containment defense through the configured resource root.
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(vault, "link-out.md")); err != nil {
		t.Fatal(err)
	}
	for name, manifest := range manifests {
		var out, errb bytes.Buffer
		code := 0
		if code = Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-" + name, "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
			// Only the first case sequence uses run-<name>; begin must
			// succeed for each distinct run before its completion is
			// attempted.
			t.Fatalf("begin for %s: %s", name, errb.String())
		}
		out.Reset()
		errb.Reset()
		withStdin(t, manifest, func() {
			code = Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-" + name, "--manifest", "-"}, &out, &errb)
		})
		if code != 4 || !strings.Contains(errb.String(), "work_receipt_invalid") {
			t.Fatalf("%s must be rejected exit 4 work_receipt_invalid, got %d: %s", name, code, errb.String())
		}
	}
	// Every rejection was audited and no completion row was written.
	store := e5t1Store(t, configPath)
	if e5t1CountTransitions(t, store, dispatchID, "invalid") < len(manifests) {
		t.Fatalf("each rejected completion must be audited, got %d", e5t1CountTransitions(t, store, dispatchID, "invalid"))
	}
}

// TestWorkCompleteCleanSchedulesNothing proves a clean completion (no
// dirty generation) returns the route to IDLE with no follow-up and the
// terminal receipt updated in place.
func TestWorkCompleteCleanSchedulesNothing(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	manifest := `[{"path":"Inbox/new.md","after_digest":"` + e5t1GoodDigest + `"}]`
	out.Reset()
	errb.Reset()
	withStdin(t, manifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete: %s", errb.String())
	}
	completed := decodeEnvelope(t, &out)
	cleanFollowup, _ := completed["followup_dispatch_id"].(string)
	if completed["status"] != "completed" || completed["route_state"] != "IDLE" || cleanFollowup != "" {
		t.Fatalf("clean completion result wrong: %v", completed)
	}

	// The begun row became the completed receipt (one row per run),
	// and the begin timestamp survived the terminal update (v4).
	store := e5t1Store(t, configPath)
	var status, begunAt, submittedAt string
	if err := store.QueryRow(`SELECT status, begun_at, submitted_at FROM work_receipts WHERE dispatch_id = ? AND run_id = 'run-1'`, dispatchID).Scan(&status, &begunAt, &submittedAt); err != nil || status != "completed" {
		t.Fatalf("receipt row must be updated in place: %q %v", status, err)
	}
	if begunAt == "" || begunAt > submittedAt {
		t.Fatalf("the begin window must persist across the terminal update: begun %q submitted %q", begunAt, submittedAt)
	}
	if e5t1RouteState(t, store) != "IDLE" {
		t.Fatal("route must return to IDLE after a clean completion")
	}
}

// TestWorkCompleteDirtySchedulesOneFollowup proves the atomic completion
// transaction: changes that arrived while the dispatch was active keep
// the route dirty, and the completion schedules exactly one latest-state
// follow-up (CON-003, FBK-001 posture).
func TestWorkCompleteDirtySchedulesOneFollowup(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	// A second burst while the dispatch is active merges into the dirty
	// generation instead of creating a second dispatch.
	setPlanEnv(t, vault, false)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/later.md","exists":true,"new":true,"size":3,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("second burst: %s", errb.String())
	}

	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	manifest := `[{"path":"Inbox/new.md","after_digest":"` + e5t1GoodDigest + `"}]`
	withStdin(t, manifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete: %s", errb.String())
	}
	completed := decodeEnvelope(t, &out)
	followup, _ := completed["followup_dispatch_id"].(string)
	if completed["route_state"] != "FOLLOWUP_READY" || followup == "" {
		t.Fatalf("dirty completion must schedule one follow-up: %v", completed)
	}

	// Exactly one follow-up intent exists and the dirty generation
	// collapsed.
	store := e5t1Store(t, configPath)
	var ready int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE state = 'ready'`).Scan(&ready); err != nil || ready != 1 {
		t.Fatalf("exactly one ready follow-up must exist: %d %v", ready, err)
	}
	var dirty int
	if err := store.QueryRow(`SELECT dirty_generation FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&dirty); err != nil || dirty != 0 {
		t.Fatalf("dirty generation must collapse: %d %v", dirty, err)
	}

	// A completion without a begin receipt is rejected (no completion-only
	// mode in v0.1).
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-absent", "--manifest", "-"}, &out, &errb); code != 4 {
		t.Fatalf("completion without begin must be rejected exit 4, got %d: %s", code, errb.String())
	}
}

// TestWorkFailNeverErasesDirtyState proves a cooperative failure keeps
// the dirty evidence and, while the failure budget remains, creates one
// bounded follow-up; exhausting the budget requires operator resolution
// (UNCERTAIN, feedback-loop §7).
func TestWorkFailNeverErasesDirtyState(t *testing.T) {
	configPath, vault := e5t1FixtureBudget(t, 1)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	// Dirty the route with a second burst.
	setPlanEnv(t, vault, false)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/later.md","exists":true,"new":true,"size":3,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})

	// A bogus failure code is rejected with the closed set.
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--failure-code", "gave-up"}, &out, &errb); code != 4 {
		t.Fatalf("bogus failure code must be exit 4, got %d: %s", code, errb.String())
	}

	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--failure-code", "agent_error", "--detail", "bounded reason"}, &out, &errb); code != 0 {
		t.Fatalf("fail: %s", errb.String())
	}
	failed := decodeEnvelope(t, &out)
	followup, _ := failed["followup_dispatch_id"].(string)
	if failed["status"] != "failed" || followup == "" || failed["route_state"] != "FOLLOWUP_READY" {
		t.Fatalf("failure with budget must create one follow-up: %v", failed)
	}

	// The dirty generation collapsed into the follow-up (the evidence is
	// the follow-up intent, never a deletion).
	store := e5t1Store(t, configPath)
	var dirty int
	if err := store.QueryRow(`SELECT dirty_generation FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&dirty); err != nil || dirty != 0 {
		t.Fatalf("failure collapses the generation into the follow-up: %d %v", dirty, err)
	}

	// Exhausting the budget (budget 1, one failure recorded) moves the
	// route to UNCERTAIN on the next failure: activate the follow-up and
	// fail it too.
	if err := store.ActivateFollowup(context.Background(), followup, "test", "2026-08-21T00:00:00Z"); err != nil {
		t.Fatalf("activate follow-up: %v", err)
	}
	var activeDispatch string
	if err := store.QueryRow(`SELECT active_dispatch_id FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&activeDispatch); err != nil || activeDispatch != followup {
		t.Fatalf("follow-up must hold the slot: %q %v", activeDispatch, err)
	}
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", followup, "--run-id", "run-2"}, &out, &errb); code != 0 {
		t.Fatalf("begin follow-up: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", followup, "--run-id", "run-2", "--failure-code", "timeout"}, &out, &errb); code != 0 {
		t.Fatalf("second fail: %s", errb.String())
	}
	exhausted := decodeEnvelope(t, &out)
	exhaustedFollowup, _ := exhausted["followup_dispatch_id"].(string)
	if exhausted["route_state"] != "UNCERTAIN" || exhaustedFollowup != "" {
		t.Fatalf("budget exhaustion must require operator resolution: %v", exhausted)
	}
}

// TestWorkCompleteFullDocumentReceipt proves the full work-receipt
// document form validates its identity fields against the lineage
// (dispatch, run, resource) and records the declared result revision.
func TestWorkCompleteFullDocumentReceipt(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--base-revision", "git:abc"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	doc := map[string]any{
		"schema_version":  "jjukkumi.work-receipt/v1",
		"dispatch_id":     dispatchID,
		"run_id":          "run-1",
		"resource_id":     "vault-main",
		"status":          "completed",
		"result_revision": "git:def456",
		"changes":         []map[string]any{{"path": "Inbox/new.md", "after_digest": e5t1GoodDigest}},
	}
	raw, _ := json.Marshal(doc)
	dir := t.TempDir()
	manifest := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifest, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", manifest}, &out, &errb); code != 0 {
		t.Fatalf("complete: %s", errb.String())
	}
	completed := decodeEnvelope(t, &out)
	if completed["status"] != "completed" {
		t.Fatalf("full-document completion wrong: %v", completed)
	}
	store := e5t1Store(t, configPath)
	var resultRevision string
	if err := store.QueryRow(`SELECT result_revision FROM work_receipts WHERE dispatch_id = ? AND run_id = 'run-1'`, dispatchID).Scan(&resultRevision); err != nil || resultRevision != "git:def456" {
		t.Fatalf("result revision must persist: %q %v", resultRevision, err)
	}
}

// e5t1FixtureBudget builds the e4t3 fixture with an explicit failure
// budget so budget exhaustion is reachable in one test.
func e5t1FixtureBudget(t *testing.T, budget int) (string, string) {
	t.Helper()
	configPath, vault := e4t3Fixture(t)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(raw), "failure_budget: 2", "failure_budget: "+fmt.Sprintf("%d", budget), 1)
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath, vault
}

// TestWorkFailDetailBound proves the --detail byte bound (E5 audit
// round 11, F008).
func TestWorkFailDetailBound(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	long := strings.Repeat("x", 300)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--failure-code", "agent_error", "--detail", long}, &out, &errb); code != 4 || !strings.Contains(errb.String(), "work_receipt_invalid") {
		t.Fatalf("an over-bound detail must be rejected exit 4, got %d: %s", code, errb.String())
	}
	// A bounded detail is accepted.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--failure-code", "agent_error", "--detail", "bounded"}, &out, &errb); code != 0 {
		t.Fatalf("a bounded detail must pass: %s", errb.String())
	}
	_ = vault
}

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// e5t4Rewrite rewrites one line of the fixture configuration.
func e5t4Rewrite(t *testing.T, configPath, from, to string) {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(raw), from, to, 1)
	if updated == string(raw) {
		t.Fatalf("config rewrite %q -> %q did not apply", from, to)
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestProtectedPathQuarantined proves a protected-path change is held
// durably, never enters a task manifest, and is operator-visible with
// release lineage (PTH-008, AC-407, CLI-006).
func TestProtectedPathQuarantined(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e5t4Rewrite(t, configPath, "      protected: []", "      protected: [\"Secrets/**\"]")
	if err := os.MkdirAll(filepath.Join(vault, "Secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "Secrets", "keep.md"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	e4t3RegisterRoute(t, configPath)
	setPlanEnv(t, vault, false)

	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Secrets/keep.md","exists":true,"new":true,"size":6,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("protected dispatch: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["disposition"] != "quarantine" || res["quarantine_id"] == "" {
		t.Fatalf("protected path must be quarantined: %v", res)
	}
	quarantineID, _ := res["quarantine_id"].(string)

	// No task manifest exists: the protected change never dispatched.
	store := e5t1Store(t, configPath)
	var intents int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents); err != nil || intents != 0 {
		t.Fatalf("protected path must not create an intent: %d %v", intents, err)
	}
	var decisionDisposition string
	if err := store.QueryRow(`SELECT disposition FROM policy_decisions ORDER BY created_at DESC LIMIT 1`).Scan(&decisionDisposition); err != nil || decisionDisposition != "quarantine" {
		t.Fatalf("quarantine decision must be persisted: %q %v", decisionDisposition, err)
	}

	// The hold is operator-visible.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"quarantine", "list", "--config", configPath, "--state", "held"}, &out, &errb); code != 0 {
		t.Fatalf("quarantine list: %s", errb.String())
	}
	listing := decodeEnvelope(t, &out)
	if listing["count"].(float64) != 1 {
		t.Fatalf("one held item expected: %v", listing)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"quarantine", "show", "--config", configPath, quarantineID}, &out, &errb); code != 0 {
		t.Fatalf("quarantine show: %s", errb.String())
	}
	if shown := decodeEnvelope(t, &out); shown["state"] != "held" {
		t.Fatalf("held item detail wrong: %v", shown)
	}

	// Release requires --reason and --yes, records actor and reason, and
	// creates the replacement decision lineage (CLI-006).
	out.Reset()
	errb.Reset()
	if code := Run([]string{"quarantine", "release", "--config", configPath, quarantineID}, &out, &errb); code != 2 {
		t.Fatalf("release without --yes/--reason must be a usage error, got %d", code)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"quarantine", "release", "--config", configPath, "--yes", "--reason", "operator reviewed the hold", quarantineID}, &out, &errb); code != 0 {
		t.Fatalf("release: %s", errb.String())
	}
	released := decodeEnvelope(t, &out)
	if released["state"] != "released" || released["resolved_by"] != "operator" || released["replacement_decision_id"] == "" {
		t.Fatalf("release lineage wrong: %v", released)
	}
	replacement, _ := released["replacement_decision_id"].(string)
	var disposition string
	if err := store.QueryRow(`SELECT disposition FROM policy_decisions WHERE decision_id = ?`, replacement).Scan(&disposition); err != nil || disposition != "reconcile" {
		t.Fatalf("release must create a reconciliation decision: %q %v", disposition, err)
	}
	var pending int
	if err := store.QueryRow(`SELECT pending_reconcile FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("release must mark the pending reconciliation generation: %d %v", pending, err)
	}
	// A second release of the resolved hold is a conflict, never a
	// second decision, under its registered code.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"quarantine", "release", "--config", configPath, "--yes", "--reason", "again", quarantineID}, &out, &errb); code != 14 || !strings.Contains(errb.String(), "quarantine_release_denied") {
		t.Fatalf("re-release must report quarantine_release_denied exit 14, got %d: %s", code, errb.String())
	}
	// Discarding the resolved hold is the same conflict class under the
	// transition code.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"quarantine", "discard", "--config", configPath, "--yes", "--reason", "again", quarantineID}, &out, &errb); code != 14 || !strings.Contains(errb.String(), "transition_invalid") {
		t.Fatalf("re-discard must report transition_invalid exit 14, got %d: %s", code, errb.String())
	}
}

// TestOverflowNeverDispatchesPartialWork proves a fresh-instance signal
// marks the single pending reconciliation generation and never dispatches
// the partial ordinary batch (SRC-005, AC-408 posture); repeated signals
// collapse into the same one generation.
func TestOverflowNeverDispatchesPartialWork(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)

	// One normal change activates the route.
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	// A fresh-instance signal while the route is active: the partial
	// batch (one file) must never dispatch; one pending reconciliation
	// generation is marked instead.
	setPlanEnv(t, vault, true)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/later.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("fresh-instance dispatch: %s", errb.String())
	}
	fresh := decodeEnvelope(t, &out)
	if fresh["disposition"] != "reconcile" || fresh["pending_reconcile"] != true {
		t.Fatalf("fresh instance must reconcile, not dispatch: %v", fresh)
	}
	store := e5t1Store(t, configPath)
	var intents, pending int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents); err != nil || intents != 1 {
		t.Fatalf("no second intent may exist: %d %v", intents, err)
	}
	if err := store.QueryRow(`SELECT pending_reconcile FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("pending generation must be marked: %d %v", pending, err)
	}

	// A repeated fresh-instance signal collapses into the same single
	// generation (never a stack).
	out.Reset()
	errb.Reset()
	withStdin(t, `[{"name":"Inbox/more.md","exists":true,"new":true,"size":4,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("second fresh-instance dispatch: %s", errb.String())
	}
	if err := store.QueryRow(`SELECT pending_reconcile FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("repeated reconciliation must stay one generation: %d %v", pending, err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents); err != nil || intents != 1 {
		t.Fatalf("repeated reconciliation must not dispatch: %d %v", intents, err)
	}

	// Completing the active work collapses the pending generation into
	// exactly one follow-up, clearing the flag (AC-408).
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete: %s", errb.String())
	}
	completed := decodeEnvelope(t, &out)
	if completed["route_state"] != "FOLLOWUP_READY" {
		t.Fatalf("pending reconciliation must collapse into one follow-up: %v", completed)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE state = 'ready'`).Scan(&intents); err != nil || intents != 1 {
		t.Fatalf("exactly one follow-up: %d %v", intents, err)
	}
	if err := store.QueryRow(`SELECT pending_reconcile FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("the generation must clear into the follow-up: %d %v", pending, err)
	}
}

// TestBulkOverThresholdQuarantined proves a batch above the automatic
// threshold is quarantined per route policy (POL-005) and discard
// resolves it without task creation.
func TestBulkOverThresholdQuarantined(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e5t4Rewrite(t, configPath, "      automatic_threshold: 25", "      automatic_threshold: 1")
	e4t3RegisterRoute(t, configPath)
	setPlanEnv(t, vault, false)

	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/a.md","exists":true,"new":true,"size":1,"type":"f"},{"name":"Inbox/b.md","exists":true,"new":true,"size":1,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("bulk dispatch: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["disposition"] != "quarantine" {
		t.Fatalf("bulk batch must quarantine per policy: %v", res)
	}
	quarantineID, _ := res["quarantine_id"].(string)
	store := e5t1Store(t, configPath)
	var intents int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents); err != nil || intents != 0 {
		t.Fatalf("bulk batch must not dispatch: %d %v", intents, err)
	}

	// Discard resolves the hold without creating any task.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"quarantine", "discard", "--config", configPath, "--yes", "--reason", "stale bulk case", quarantineID}, &out, &errb); code != 0 {
		t.Fatalf("discard: %s", errb.String())
	}
	discarded := decodeEnvelope(t, &out)
	discardReplacement, _ := discarded["replacement_decision_id"].(string)
	if discarded["state"] != "discarded" || discardReplacement != "" {
		t.Fatalf("discard must resolve without task creation: %v", discarded)
	}
	var decisions int
	if err := store.QueryRow(`SELECT COUNT(*) FROM policy_decisions`).Scan(&decisions); err != nil || decisions != 1 {
		t.Fatalf("discard must not create a replacement decision: %d %v", decisions, err)
	}
}

// TestFullReconcileEnumeratesAndCompares proves the full-scope
// reconciliation: complete enumeration under containment and scope
// filters, path-fact comparison, one latest-state intent on an idle
// route, and collapse on repeat (OPS-006, SRC-005).
func TestFullReconcileEnumeratesAndCompares(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	// Extra scope content: one more Markdown file, one non-Markdown
	// file (out of scope), and one symlink pointing outside (reported,
	// never followed).
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "second.md"), []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "image.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(vault, "Inbox", "link.md")); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial"}, &out, &errb); code != 0 {
		t.Fatalf("reconcile: %s", errb.String())
	}
	first := decodeEnvelope(t, &out)
	// Scope: Inbox/new.md, Inbox/second.md (markdown, in scope). The
	// escaping symlink is skipped with a warning, never enumerated as a
	// fact (E9-T2/M-24 corrects the old reported-fact posture: a
	// symlink to outside never projects as an existing regular file);
	// the PNG stays out of the markdown scope.
	if first["enumerated"].(float64) != 2 {
		t.Fatalf("enumeration must cover exactly the markdown scope: %v", first)
	}
	added, _ := first["added"].([]any)
	if len(added) != 2 {
		t.Fatalf("first reconciliation reports every in-scope path as added: %v", first)
	}
	skipped, _ := first["skipped"].([]any)
	foundSkippedLink := false
	for _, s := range skipped {
		if strings.Contains(fmt.Sprint(s), "link.md") {
			foundSkippedLink = true
		}
	}
	if !foundSkippedLink {
		t.Fatalf("the escaping symlink must be reported in the skipped list: %v", first)
	}
	if first["reconcile_dispatch_id"] == "" {
		t.Fatalf("an idle route with due work must schedule one intent: %v", first)
	}
	store := e5t1Store(t, configPath)
	var intents int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents); err != nil || intents != 1 {
		t.Fatalf("exactly one reconciliation intent: %d %v", intents, err)
	}
	var pending int
	if err := store.QueryRow(`SELECT pending_reconcile FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("pending generation marked: %d %v", pending, err)
	}

	// A repeated reconciliation with no changes schedules nothing new
	// and collapses into the same pending generation.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "scheduled"}, &out, &errb); code != 0 {
		t.Fatalf("second reconcile: %s", errb.String())
	}
	second := decodeEnvelope(t, &out)
	if len(second["added"].([]any)) != 0 || len(second["changed"].([]any)) != 0 || len(second["removed"].([]any)) != 0 {
		t.Fatalf("unchanged scope must produce an empty diff: %v", second)
	}
	secondDispatch, _ := second["reconcile_dispatch_id"].(string)
	if secondDispatch != "" {
		t.Fatalf("no due work must not schedule an intent: %v", second)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents); err != nil || intents != 1 {
		t.Fatalf("repeated reconciliation must not stack intents: %d %v", intents, err)
	}

	// An unknown reason is a usage error.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "bogus"}, &out, &errb); code != 2 {
		t.Fatalf("unknown reason must be a usage error, got %d", code)
	}
}

// TestIdleNoDiffReconcileClearsPending proves a full reconciliation on
// an idle route resolves a pending generation it did not need (the E5
// audit remediation: the flag no longer waits indefinitely for a
// dispatch completion that may never come).
func TestIdleNoDiffReconcileClearsPending(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	// Store the snapshot so the comparison finds no diff, then mark a
	// pending generation directly (the quarantine-release aftermath).
	var initOut, initErr bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial"}, &initOut, &initErr); code != 0 {
		t.Fatalf("initial reconcile failed (exit %d): %s", code, initErr.String())
	}
	// Complete the created reconciliation intent through the receipt
	// loop so the route returns to idle with the snapshot current: the
	// pending generation collapses into its follow-up, and the
	// follow-up completes clean.
	store := e5t1Store(t, configPath)
	var dispatchID string
	if err := store.QueryRow(`SELECT dispatch_id FROM dispatch_intents ORDER BY created_at DESC LIMIT 1`).Scan(&dispatchID); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "r1"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "r1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete: %s", errb.String())
	}
	first := decodeEnvelope(t, &out)
	followup, _ := first["followup_dispatch_id"].(string)
	if followup == "" {
		t.Fatalf("the pending generation must collapse into a follow-up: %v", first)
	}
	submitFollowupProductPath(t, configPath, followup)
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", followup, "--run-id", "r2"}, &out, &errb); code != 0 {
		t.Fatalf("follow-up begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", followup, "--run-id", "r2", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("follow-up complete: %s", errb.String())
	}
	// Mark a pending generation (quarantine-release aftermath) and
	// reconcile the unchanged scope: the idle route resolves it.
	if err := store.MarkPendingReconcile(context.Background(), "wiki", "", "2026-08-21T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "manual"}, &out, &errb); code != 0 {
		t.Fatalf("no-diff reconcile: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["pending_reconcile"] == true {
		t.Fatalf("an idle no-diff reconciliation must resolve the pending generation: %v", res)
	}
	var pending int
	if err := store.QueryRow(`SELECT pending_reconcile FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending generation must be cleared: %d %v", pending, err)
	}
	_ = vault
}

// TestDropDispositionPersistsEvidence proves a batch with no meaningful
// changes persists its drop decision without any intent or hold (E5
// audit F012).
func TestDropDispositionPersistsEvidence(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	setPlanEnv(t, vault, false)
	// The fixture excludes .obsidian/workspace.json: the burst carries
	// no meaningful change, so the plan drops it.
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":".obsidian/workspace.json","exists":true,"new":true,"size":9,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("drop dispatch: %s", errb.String())
	}
	dropped := decodeEnvelope(t, &out)
	if dropped["disposition"] != "drop" {
		t.Fatalf("an excluded-only burst must drop: %v", dropped)
	}
	store := e5t1Store(t, configPath)
	var intents, holds int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents); err != nil || intents != 0 {
		t.Fatalf("a drop must not create an intent: %d %v", intents, err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM quarantine_items`).Scan(&holds); err != nil || holds != 0 {
		t.Fatalf("a drop must not hold: %d %v", holds, err)
	}
	var disposition string
	if err := store.QueryRow(`SELECT disposition FROM policy_decisions ORDER BY created_at DESC LIMIT 1`).Scan(&disposition); err != nil || disposition != "drop" {
		t.Fatalf("the drop decision must be persisted as evidence: %q %v", disposition, err)
	}
}

// TestReconcileRefusalOnDisabledRoute proves M-12 (E8-T4): a route
// that is not enabled fails closed with transition_invalid/14
// unconditionally — in every route state — instead of exiting 0 with a
// warning only when work happens to be due.
func TestReconcileRefusalOnDisabledRoute(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	var initOut, initErr bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial"}, &initOut, &initErr); code != 0 {
		t.Fatalf("initial reconcile failed (exit %d): %s", code, initErr.String())
	}
	if code := Run([]string{"route", "disable", "--config", configPath, "--route", "wiki"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("route disable failed")
	}
	var out2, errb2 bytes.Buffer
	code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "scheduled", "--submit"}, &out2, &errb2)
	if code != 14 || !strings.Contains(errb2.String(), "transition_invalid") || !strings.Contains(errb2.String(), "not enabled") {
		t.Fatalf("a disabled route must fail closed at 14 with the activation reason, got %d: %s", code, errb2.String())
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var submitted int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE state IN ('submitting','accepted')`).Scan(&submitted); err != nil || submitted != 0 {
		t.Fatalf("a disabled route must not submit through --submit: %d %v", submitted, err)
	}
	_ = vault
}

// TestQuarantineListFiltersAndBounds proves the state filter, the limit
// bound, and the usage rejection of an out-of-range limit (E5 audit
// round 10, F005).
func TestQuarantineListFiltersAndBounds(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e5t4Rewrite(t, configPath, `      protected: []`, `      protected: ["Secrets/**"]`)
	os.MkdirAll(filepath.Join(vault, "Secrets"), 0o755)
	os.WriteFile(filepath.Join(vault, "Secrets", "a.md"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(vault, "Secrets", "b.md"), []byte("b"), 0o644)
	e4t3RegisterRoute(t, configPath)
	setPlanEnv(t, vault, false)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Secrets/a.md","exists":true,"new":true,"size":1,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("first hold: %s", errb.String())
	}
	// A second hold on the other protected file.
	os.WriteFile(filepath.Join(vault, "Secrets", "b.md"), []byte("bb"), 0o644)
	out.Reset()
	errb.Reset()
	withStdin(t, `[{"name":"Secrets/b.md","exists":true,"new":true,"size":2,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("second hold: %s", errb.String())
	}
	// The state filter and the route filter narrow, the limit bounds.
	var o, e bytes.Buffer
	if code := Run([]string{"quarantine", "list", "--config", configPath, "--state", "held", "--limit", "1"}, &o, &e); code != 0 {
		t.Fatalf("bounded list: %s", e.String())
	}
	listing := decodeEnvelope(t, &o)
	if listing["count"].(float64) != 1 {
		t.Fatalf("the limit must bound the listing: %v", listing)
	}
	o.Reset()
	e.Reset()
	if code := Run([]string{"quarantine", "list", "--config", configPath, "--route", "absent"}, &o, &e); code != 0 {
		t.Fatalf("route filter: %s", e.String())
	}
	if decodeEnvelope(t, &o)["count"].(float64) != 0 {
		t.Fatal("an absent route must list nothing")
	}
	o.Reset()
	e.Reset()
	if code := Run([]string{"quarantine", "list", "--config", configPath, "--limit", "0"}, &o, &e); code != 2 {
		t.Fatalf("limit 0 must be a usage error, got %d", code)
	}
}

// TestReconcileSubmitReachesAccepted proves the --submit success path
// against the gated stub target: the reconciliation intent is submitted
// and reaches acceptance (E5 audit round 11, F002).
func TestReconcileSubmitReachesAccepted(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	os.WriteFile(filepath.Join(vault, "Inbox", "extra.md"), []byte("extra"), 0o644)
	var out, errb bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial", "--submit"}, &out, &errb); code != 0 {
		t.Fatalf("reconcile --submit: %s", errb.String())
	}
	submitted := decodeEnvelope(t, &out)
	if submitted["submitted"] != true || submitted["submitted_state"] != "accepted" {
		t.Fatalf("the reconciliation intent must reach acceptance: %v", submitted)
	}
}

// TestReconcileRemovedDiffAndDeleteItems proves a removal enters the
// diff and the intent's evidence as delete items (E5 audit round 12,
// F003), and quarantine show of an unknown id is the registered
// not-found code (F004).
func TestReconcileRemovedDiffAndQuarantineNotFound(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	// Baseline snapshot; complete the reconciliation generation's
	// intents so the route returns to idle before the removal.
	var initOut, initErr bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial"}, &initOut, &initErr); code != 0 {
		t.Fatalf("initial reconcile failed (exit %d): %s", code, initErr.String())
	}
	store := e5t1Store(t, configPath)
	var dispatchID string
	if err := store.QueryRow(`SELECT dispatch_id FROM dispatch_intents ORDER BY created_at DESC LIMIT 1`).Scan(&dispatchID); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "r1"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "r1", "--manifest", "-"}, &out, &errb)
	})
	first := decodeEnvelope(t, &out)
	followup, _ := first["followup_dispatch_id"].(string)
	if followup == "" {
		t.Fatalf("the pending generation must collapse into a follow-up: %v", first)
	}
	submitFollowupProductPath(t, configPath, followup)
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", followup, "--run-id", "r2"}, &out, &errb); code != 0 {
		t.Fatalf("follow-up begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", followup, "--run-id", "r2", "--manifest", "-"}, &out, &errb)
	})
	if err := os.Remove(filepath.Join(vault, "Inbox", "new.md")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "manual"}, &out, &errb); code != 0 {
		t.Fatalf("reconcile after removal: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	removed, _ := res["removed"].([]any)
	if len(removed) != 1 || removed[0] != "Inbox/new.md" {
		t.Fatalf("the removal must enter the diff: %v", res)
	}
	// The intent's evidence carries the deletion.
	var requestJSON string
	// Select the reconcile intent by its derived identity: second-precision
	// created_at values can tie across the follow-up and reconcile intents
	// in the same second (E8-T1 removed the timestamp dodge here).
	if err := store.QueryRow(`SELECT request_json FROM dispatch_intents WHERE dispatch_id LIKE 'disp-reconcile-%' ORDER BY created_at DESC, rowid DESC LIMIT 1`).Scan(&requestJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(requestJSON, `"operation":"delete"`) || !strings.Contains(requestJSON, "Inbox/new.md") {
		t.Fatalf("the deletion must be the intent's bounded evidence: %s", requestJSON)
	}
	// An unknown quarantine id reports the registered code.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"quarantine", "show", "--config", configPath, "q-absent"}, &out, &errb); code != 4 || !strings.Contains(errb.String(), "quarantine_not_found") {
		t.Fatalf("unknown quarantine must report quarantine_not_found exit 4, got %d: %s", code, errb.String())
	}
}

func TestReconcileReasonCodesSorted(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("reconcile failed")
	}
	store := e5t1Store(t, configPath)
	var codesJSON string
	if err := store.QueryRow(`SELECT reason_codes_json FROM policy_decisions WHERE disposition = 'reconcile' ORDER BY created_at DESC LIMIT 1`).Scan(&codesJSON); err != nil {
		t.Fatal(err)
	}
	var codes []string
	if err := json.Unmarshal([]byte(codesJSON), &codes); err != nil {
		t.Fatalf("not JSON: %s", codesJSON)
	}
	for i := 1; i < len(codes); i++ {
		if codes[i-1] > codes[i] {
			t.Fatalf("reason codes must be sorted: %v", codes)
		}
	}
	_ = vault
}

// TestUncertainResolvedByReconcile proves the operator exit from
// UNCERTAIN: full reconciliation resolves the uncertain route, collapses
// the retained generation, schedules exactly one latest-state intent,
// and the loop closes back to IDLE (feedback-loop §10, E5-T1's
// operator-required UNCERTAIN resolution).
func TestUncertainResolvedByReconcile(t *testing.T) {
	configPath, vault := e5t1FixtureBudget(t, 1)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--failure-code", "agent_error"}, &out, &errb); code != 0 {
		t.Fatalf("first fail: %s", errb.String())
	}
	failed := decodeEnvelope(t, &out)
	followup, _ := failed["followup_dispatch_id"].(string)
	if followup == "" {
		t.Fatalf("failure with budget must create one follow-up: %v", failed)
	}
	submitFollowupProductPath(t, configPath, followup)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", followup, "--run-id", "run-2"}, &out, &errb); code != 0 {
		t.Fatalf("begin follow-up: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", followup, "--run-id", "run-2", "--failure-code", "timeout"}, &out, &errb); code != 0 {
		t.Fatalf("second fail: %s", errb.String())
	}
	if exhausted := decodeEnvelope(t, &out); exhausted["route_state"] != "UNCERTAIN" {
		t.Fatalf("budget exhaustion must reach UNCERTAIN: %v", exhausted)
	}

	// The operator resolution: one reconciliation resolves the uncertain
	// route with due work and schedules exactly one latest-state intent.
	// The durable path facts now track every observed arrival (E7-T3),
	// so due work requires a real file change after the failure.
	stale := filepath.Join(vault, "Indexes", "stale-index.md")
	os.MkdirAll(filepath.Dir(stale), 0o755)
	os.WriteFile(stale, []byte("stale index content"), 0o644)
	setPlanEnv(t, vault, false)
	withStdin(t, `[{"name":"Indexes/stale-index.md","exists":true,"new":true,"size":19,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &bytes.Buffer{}, &bytes.Buffer{})
	})
	out.Reset()
	errb.Reset()
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "manual"}, &out, &errb); code != 0 {
		t.Fatalf("reconcile the uncertain route: %s", errb.String())
	}
	resolvedRun := decodeEnvelope(t, &out)
	resolvedDispatch, _ := resolvedRun["reconcile_dispatch_id"].(string)
	if resolvedRun["pending_reconcile"] != true || resolvedDispatch == "" {
		t.Fatalf("the uncertain resolution must schedule one intent: %v", resolvedRun)
	}
	store := e5t1Store(t, configPath)
	var routeState, activeDispatch string
	var dirty int
	if err := store.QueryRow(`SELECT lane_state, COALESCE(active_dispatch_id, ''), dirty_generation FROM destination_lane_state WHERE route_id = 'wiki'`).Scan(&routeState, &activeDispatch, &dirty); err != nil ||
		routeState != "FOLLOWUP_READY" || activeDispatch != resolvedDispatch || dirty != 0 {
		t.Fatalf("resolution must leave FOLLOWUP_READY with the reserved intent and no retained generation: %q %q %d %v", routeState, activeDispatch, dirty, err)
	}
	var resolved int
	if err := store.QueryRow(`SELECT COUNT(*) FROM state_transitions WHERE entity_type = 'route' AND to_state = 'FOLLOWUP_READY' AND context_json LIKE '%reconciliation_resolved%'`).Scan(&resolved); err != nil || resolved != 1 {
		t.Fatalf("the resolution must be audited: %d %v", resolved, err)
	}

	// The loop closes: the resolved dispatch completes (the pending
	// generation forces its one follow-up), and that follow-up completes
	// clean back to IDLE — the route never re-enters UNCERTAIN.
	submitFollowupProductPath(t, configPath, resolvedDispatch)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", resolvedDispatch, "--run-id", "run-r"}, &out, &errb); code != 0 {
		t.Fatalf("begin resolved dispatch: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", resolvedDispatch, "--run-id", "run-r", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete resolved dispatch: %s", errb.String())
	}
	resolvedDone := decodeEnvelope(t, &out)
	secondFollowup, _ := resolvedDone["followup_dispatch_id"].(string)
	if secondFollowup == "" {
		t.Fatalf("the pending generation must force one follow-up: %v", resolvedDone)
	}
	submitFollowupProductPath(t, configPath, secondFollowup)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", secondFollowup, "--run-id", "run-r2"}, &out, &errb); code != 0 {
		t.Fatalf("begin second follow-up: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", secondFollowup, "--run-id", "run-r2", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete second follow-up: %s", errb.String())
	}
	if err := store.QueryRow(`SELECT lane_state FROM destination_lane_state WHERE route_id = 'wiki'`).Scan(&routeState); err != nil || routeState != "IDLE" {
		t.Fatalf("the resolved loop must close back to IDLE: %q %v", routeState, err)
	}
}

// TestReconcileUnreadableSubtreeKeepsStoredFacts is the round-20
// regression: a path under an unreadable subtree keeps its stored path
// fact — an access failure is never reported as a removal (which would
// build a data-destroying reconcile intent).
func TestReconcileUnreadableSubtreeKeepsStoredFacts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based unreadability is not available on windows")
	}
	configPath, vault := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	priv := filepath.Join(vault, "Priv")
	if err := os.MkdirAll(priv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(priv, "a.md"), []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial"}, &out, &errb); code != 0 {
		t.Fatalf("first reconcile: %s", errb.String())
	}
	first := decodeEnvelope(t, &out)
	firstDispatch, _ := first["reconcile_dispatch_id"].(string)
	if firstDispatch == "" {
		t.Fatalf("the idle route with due work must schedule its intent: %v", first)
	}
	// The snapshot now covers the private subtree.
	store := e5t1Store(t, configPath)
	var facts int
	if err := store.QueryRow(`SELECT COUNT(*) FROM path_facts WHERE path = 'Priv/a.md'`).Scan(&facts); err != nil || facts != 1 {
		t.Fatalf("the private file must be snapshotted: %d %v", facts, err)
	}
	if err := os.Chmod(priv, 0o000); err != nil {
		t.Skipf("cannot make the subtree unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(priv, 0o755) })
	// Prove the subtree is genuinely unreadable for this process.
	if _, err := os.ReadDir(priv); err == nil {
		t.Skip("the subtree stayed readable; permission semantics unavailable")
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "manual"}, &out, &errb); code != 0 {
		t.Fatalf("reconcile over the unreadable subtree: %s", errb.String())
	}
	third := decodeEnvelope(t, &out)
	if removed, _ := third["removed"].([]any); len(removed) != 0 {
		t.Fatalf("an unreadable subtree must never surface as removals: %v", third["removed"])
	}
	if changed, _ := third["changed"].([]any); len(changed) != 0 {
		t.Fatalf("an unreadable subtree must never surface as changes: %v", third["changed"])
	}
	// The stored fact stands so a later readable reconciliation compares
	// against the truth.
	if err := store.QueryRow(`SELECT COUNT(*) FROM path_facts WHERE path = 'Priv/a.md'`).Scan(&facts); err != nil || facts != 1 {
		t.Fatalf("the stored fact must stand: %d %v", facts, err)
	}

	// Bring the route back to IDLE (complete the first intent's bounded
	// chain), add one readable change, and reconcile again: the scheduled
	// intent must carry the verified change and must not assert deletes
	// under the unreadable subtree (an access failure is not a removal an
	// intent may claim either).
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", firstDispatch, "--run-id", "run-1"}, &out, &errb); code != 0 {
		t.Fatalf("begin first intent: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", firstDispatch, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete first intent: %s", errb.String())
	}
	secondDispatch, _ := decodeEnvelope(t, &out)["followup_dispatch_id"].(string)
	if secondDispatch == "" {
		t.Fatal("the pending generation must force one follow-up")
	}
	submitFollowupProductPath(t, configPath, secondDispatch)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", secondDispatch, "--run-id", "run-2"}, &out, &errb); code != 0 {
		t.Fatalf("begin follow-up: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", secondDispatch, "--run-id", "run-2", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete follow-up: %s", errb.String())
	}
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "added.md"), []byte("added"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "manual"}, &out, &errb); code != 0 {
		t.Fatalf("reconcile with due work: %s", errb.String())
	}
	fourth := decodeEnvelope(t, &out)
	newDispatch, _ := fourth["reconcile_dispatch_id"].(string)
	if newDispatch == "" {
		t.Fatalf("the idle route with due work must schedule an intent: %v", fourth)
	}
	var requestJSON string
	if err := store.QueryRow(`SELECT request_json FROM dispatch_intents WHERE dispatch_id = ?`, newDispatch).Scan(&requestJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(requestJSON, "Inbox/added.md") {
		t.Fatalf("the verified change must be the intent's evidence: %s", requestJSON)
	}
	if strings.Contains(requestJSON, "Priv/a.md") {
		t.Fatalf("an unverifiable path under an unreadable subtree must never enter an intent manifest: %s", requestJSON)
	}
	if removed, _ := fourth["removed"].([]any); len(removed) != 0 {
		t.Fatalf("the unreadable subtree still must not surface as removals: %v", fourth["removed"])
	}
}

// TestUncertainNoWorkResolutionLandsIdle proves the no-work arm of the
// operator exit: an uncertain route whose full reconciliation proves no
// remaining work lands IDLE with the pending generation cleared and
// nothing scheduled (the store-level nil-intent edge driven through the
// real CLI surface and Run's flag logic).
func TestUncertainNoWorkResolutionLandsIdle(t *testing.T) {
	configPath, _ := e5t1FixtureBudget(t, 1)
	e4t3RegisterRoute(t, configPath)

	var out, errb bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial"}, &out, &errb); code != 0 {
		t.Fatalf("first reconcile: %s", errb.String())
	}
	first := decodeEnvelope(t, &out)
	d1, _ := first["reconcile_dispatch_id"].(string)
	if d1 == "" {
		t.Fatalf("the idle route with due work must schedule its intent: %v", first)
	}
	store := e5t1Store(t, configPath)

	// Two consecutive failures (budget 1) exhaust the route into
	// UNCERTAIN without touching the vault: the stored snapshot still
	// matches the scope.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", d1, "--run-id", "run-1"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", d1, "--run-id", "run-1", "--failure-code", "agent_error"}, &out, &errb); code != 0 {
		t.Fatalf("first fail: %s", errb.String())
	}
	failed := decodeEnvelope(t, &out)
	d2, _ := failed["followup_dispatch_id"].(string)
	if d2 == "" {
		t.Fatalf("the budgeted failure must schedule its follow-up: %v", failed)
	}
	submitFollowupProductPath(t, configPath, d2)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", d2, "--run-id", "run-2"}, &out, &errb); code != 0 {
		t.Fatalf("begin follow-up: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", d2, "--run-id", "run-2", "--failure-code", "timeout"}, &out, &errb); code != 0 {
		t.Fatalf("second fail: %s", errb.String())
	}
	if exhausted := decodeEnvelope(t, &out); exhausted["route_state"] != "UNCERTAIN" {
		t.Fatalf("budget exhaustion must reach UNCERTAIN: %v", exhausted)
	}

	// No vault change since the stored snapshot: the resolution lands
	// IDLE with the pending generation cleared and nothing scheduled.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "manual"}, &out, &errb); code != 0 {
		t.Fatalf("no-work resolution: %s", errb.String())
	}
	resolved := decodeEnvelope(t, &out)
	if resolved["pending_reconcile"] != false {
		t.Fatalf("a no-work resolution must clear the pending generation: %v", resolved)
	}
	if dispatch, _ := resolved["reconcile_dispatch_id"].(string); dispatch != "" {
		t.Fatalf("no due work must schedule nothing: %v", resolved)
	}
	var routeState string
	var dirty, pending int
	if err := store.QueryRow(`SELECT route_state, dirty_generation, pending_reconcile FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&routeState, &dirty, &pending); err != nil ||
		routeState != "IDLE" || dirty != 0 || pending != 0 {
		t.Fatalf("the uncertain route must land idle and clean: %q %d %d %v", routeState, dirty, pending, err)
	}
}

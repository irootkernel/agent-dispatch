package cli

import (
	"bytes"
	"os"
	"path/filepath"
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
	// second decision.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"quarantine", "release", "--config", configPath, "--yes", "--reason", "again", quarantineID}, &out, &errb); code != 14 {
		t.Fatalf("re-release must conflict, got %d", code)
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
	// Scope: Inbox/new.md, Inbox/second.md (markdown, in scope) plus the
	// reported symlink; the PNG stays out of the markdown scope.
	if first["enumerated"].(float64) != 3 {
		t.Fatalf("enumeration must cover the markdown scope plus the reported symlink: %v", first)
	}
	added, _ := first["added"].([]any)
	if len(added) != 3 {
		t.Fatalf("first reconciliation reports every in-scope path as added: %v", first)
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

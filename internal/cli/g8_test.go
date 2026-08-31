package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeskanban"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
	"github.com/irootkernel/agent-dispatch/internal/ports"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/fakesink"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/hermesenv"
)

// Gate G8 (E12-T4): multi-destination fan-out and the four-outcome
// completion contract are closed end to end (AC-801 through AC-806,
// FAN-*, CON-001, CON-007 through CON-010, FBK-009 through FBK-012).
// Every criterion drives the real CLI surface over the deterministic
// stub Hermes (the frozen 0.19.1 interface, TST-012); the migration,
// crash, and race evidence is the existing suite cited per criterion,
// not duplicated here; and the isolated real-Hermes walkthrough is the
// skip-guarded TST-007 leg at the bottom of this file.

// g8TwoLanes writes the two-destination fixture through the ONE shared
// twoDestinationFixture helper (E12-T4 review round 1). distinctProfiles
// picks the AC-801 shape (different profiles: the fixture's
// wiki-maintainer and the board's other on-disk profile, default) versus
// the AC-802 shape (same profile, different workstreams).
func g8TwoLanes(t *testing.T, distinctProfiles bool) (string, string) {
	t.Helper()
	configPath, vault := e4t3Fixture(t)
	secondProfile := "wiki-maintainer"
	if distinctProfiles {
		secondProfile = "default"
	}
	twoDestinationFixture(t, configPath, secondProfile)
	return configPath, vault
}

// g8Enable enables the fixture route under its computed revision.
func g8Enable(t *testing.T, configPath string) string {
	t.Helper()
	e12t2Enable(t, configPath)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	rev, _ := config.RouteRevision(cfg, "wiki")
	return rev
}

// g8DispatchOccurrence submits one arrival over both lanes and returns
// the per-lane child dispatch IDs (main, review).
func g8DispatchOccurrence(t *testing.T, configPath, vault, content, file string) (string, string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(vault, "Inbox", file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, vault, false)
	var out, errb bytes.Buffer
	withStdin(t, fmt.Sprintf(`[{"name":"Inbox/%s","exists":true,"new":true,"size":%d,"type":"f"}]`, file, len(content)), func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb); code != 0 {
			t.Fatalf("dispatch: %s", errb.String())
		}
	})
	// The envelope lists this occurrence's children per destination in
	// destination order (E12-T2): main first, review second.
	res := decodeEnvelope(t, &out)
	fanout, _ := res["fanout"].([]any)
	if len(fanout) != 2 {
		t.Fatalf("the occurrence must fan out to both lanes: %v", res)
	}
	children := make([]string, 0, 2)
	for _, raw := range fanout {
		entry, _ := raw.(map[string]any)
		id, _ := entry["dispatch_id"].(string)
		if id == "" {
			t.Fatalf("every lane's child must be listed: %v", res)
		}
		children = append(children, id)
	}
	return children[0], children[1]
}

// g8Store opens the fixture's store for direct assertions.
func g8Store(t *testing.T, configPath string) *sqlite.Store {
	t.Helper()
	store := e5t1Store(t, configPath)
	t.Cleanup(func() { store.Close() })
	return store
}

// g8StubTasks reads the stub's recorded task documents (id, assignee)
// from its state directory beside the executable.
func g8StubTasks(t *testing.T, configPath string) map[string]string {
	t.Helper()
	exe := stubExeOf(t, configPath)
	dir := filepath.Join(filepath.Dir(exe), "state")
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "id-") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var task struct {
			ID       string `json:"id"`
			Assignee string `json:"assignee"`
		}
		if err := json.Unmarshal(raw, &task); err != nil {
			t.Fatal(err)
		}
		out[task.ID] = task.Assignee
	}
	return out
}

// g8Drain runs one bounded drain and returns its decoded envelope.
func g8Drain(t *testing.T, configPath string) map[string]any {
	t.Helper()
	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "drain", "--config", configPath, "--route", "wiki"}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	return decodeEnvelope(t, &out)
}

// g8WorkComplete runs the receipt outcome for one dispatch.
func g8WorkComplete(t *testing.T, configPath, dispatchID, runID string, args ...string) map[string]any {
	t.Helper()
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", runID}, &out, &errb); code != 0 {
		t.Fatalf("work begin %s: %s", runID, errb.String())
	}
	out.Reset()
	errb.Reset()
	argv := []string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", runID}
	argv = append(argv, args...)
	if code := Run(argv, &out, &errb); code != 0 {
		t.Fatalf("work complete %s (%v): %s", runID, args, errb.String())
	}
	return decodeEnvelope(t, &out)
}

// TestG8AC801TwoProfilesTwoTasksBeneathOneAggregate proves AC-801: one
// event over two eligible destinations with DIFFERENT profiles creates
// two independent children and two Hermes tasks — distinct assignees on
// the stub — beneath ONE aggregate event, and events show lists both
// children with the selection carrying both lanes.
func TestG8AC801TwoProfilesTwoTasksBeneathOneAggregate(t *testing.T) {
	configPath, vault := g8TwoLanes(t, true)
	g8Enable(t, configPath)
	g8DispatchOccurrence(t, configPath, vault, "two profiles", "profiles.md")
	// The drain submits the sibling the dispatch command left ready: both
	// lanes' tasks exist on the stub.
	g8Drain(t, configPath)

	store := g8Store(t, configPath)
	var aggregates int
	if err := store.QueryRow(`SELECT COUNT(DISTINCT aggregate_id) FROM child_dispatches`).Scan(&aggregates); err != nil || aggregates != 1 {
		t.Fatalf("both children must sit beneath one aggregate event: %d %v", aggregates, err)
	}
	tasks := g8StubTasks(t, configPath)
	if len(tasks) != 2 {
		t.Fatalf("two Hermes tasks must exist (one per lane), got %v", tasks)
	}
	assignees := map[string]bool{}
	for _, assignee := range tasks {
		assignees[assignee] = true
	}
	if len(assignees) != 2 {
		t.Fatalf("the two tasks must carry the two distinct profiles, got %v", tasks)
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"events", "show", "--config", configPath, e12t3AggregateOf(t, configPath)}, &out, &errb); code != 0 {
		t.Fatalf("events show: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["count"] != float64(2) {
		t.Fatalf("events show must list both children: %v", res)
	}
	selections := fmt.Sprintf("%v", res["selections"])
	if !strings.Contains(selections, "main") || !strings.Contains(selections, "review") {
		t.Fatalf("the aggregate selection must carry both lanes: %s", selections)
	}
}

// TestG8AC802SameProfileDistinctIdentities proves AC-802: two
// destinations with the SAME profile but different workstreams keep
// distinct destination identities and idempotency keys while sharing the
// one aggregate event.
func TestG8AC802SameProfileDistinctIdentities(t *testing.T) {
	configPath, vault := g8TwoLanes(t, false)
	g8Enable(t, configPath)
	g8DispatchOccurrence(t, configPath, vault, "same profile", "same-profile.md")
	store := g8Store(t, configPath)
	rows, err := store.Query(`SELECT c.destination_id, c.workstream, c.idempotency_key, c.aggregate_id
		FROM child_dispatches c ORDER BY c.destination_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type child struct{ destination, workstream, key, aggregate string }
	var children []child
	for rows.Next() {
		var c child
		if err := rows.Scan(&c.destination, &c.workstream, &c.key, &c.aggregate); err != nil {
			t.Fatal(err)
		}
		children = append(children, c)
	}
	if len(children) != 2 {
		t.Fatalf("two children beneath one aggregate: %d", len(children))
	}
	if children[0].destination == children[1].destination || children[0].workstream == children[1].workstream {
		t.Fatalf("the lanes must carry distinct identities: %+v", children)
	}
	if children[0].key == children[1].key {
		t.Fatalf("the DAT-014 child keys must differ per destination: %+v", children)
	}
	if children[0].aggregate != children[1].aggregate {
		t.Fatalf("both children share the one aggregate event: %+v", children)
	}
}

// TestG8AC803FailedLaneRetryReusesCompletedSibling proves AC-803: one
// child fails and is retried while its sibling completes; reprocessing
// the aggregate refuses the duplicate generation (the idempotency
// constraint), the retried lane keeps its key, and the completed lane is
// reused — still exactly one Hermes task per completed lane. The
// dead-letter shape is scripted at the intent row (the stub cannot fail
// one lane's transport selectively); the DRIVEN failure classification
// this scripting stands in for is the fakesink G2 suite's coverage —
// internal/app/dispatch TestG2AC205 (bounded transient failures with
// persisted backoff and one retained idempotency key),
// TestG2AC206 (definite rejection dead-letters through the declared
// edge), and TestE8T2DeadLetterRejectedGuards — plus the CLI-level retry
// flow in TestE12T2RetryIsolationForFanoutChildren, so the gate's
// evidence chain for "one child fails" is explicit.
func TestG8AC803FailedLaneRetryReusesCompletedSibling(t *testing.T) {
	configPath, vault := g8TwoLanes(t, false)
	g8Enable(t, configPath)
	mainChild, reviewChild := g8DispatchOccurrence(t, configPath, vault, "retry isolation", "retry.md")
	g8Drain(t, configPath) // both lanes accepted: two stub tasks, one per lane
	store := g8Store(t, configPath)
	var mainKey, reviewKey string
	if err := store.QueryRow(`SELECT idempotency_key FROM child_dispatches WHERE dispatch_id = ?`, mainChild).Scan(&mainKey); err != nil {
		t.Fatal(err)
	}
	if err := store.QueryRow(`SELECT idempotency_key FROM child_dispatches WHERE dispatch_id = ?`, reviewChild).Scan(&reviewKey); err != nil {
		t.Fatal(err)
	}
	tasksBefore := len(g8StubTasks(t, configPath))

	// The review lane's child fails definitively (dead letter) and is
	// retried through the operator exit: CON-009 keeps its key.
	if _, err := store.Exec(`UPDATE dispatch_intents SET state = 'dead_lettered' WHERE dispatch_id = ?`, reviewChild); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "retry", "--config", configPath, reviewChild, "--reason", "lane-local failure exit"}, &out, &errb); code != 0 {
		t.Fatalf("retry the failed lane: %s", errb.String())
	}
	var retriedKey string
	if err := store.QueryRow(`SELECT idempotency_key FROM child_dispatches WHERE dispatch_id = ?`, reviewChild).Scan(&retriedKey); err != nil || retriedKey != reviewKey {
		t.Fatalf("the retried lane must keep its key: %q want %q (%v)", retriedKey, reviewKey, err)
	}
	g8Drain(t, configPath)
	var reviewState string
	if err := store.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, reviewChild).Scan(&reviewState); err != nil || reviewState != "accepted" {
		t.Fatalf("the retried lane must reach acceptance: %q %v", reviewState, err)
	}
	// The completed sibling is reused, never duplicated: the stub still
	// holds exactly one task per lane (the retry deduplicated by key).
	if tasks := g8StubTasks(t, configPath); len(tasks) != tasksBefore {
		t.Fatalf("no duplicate Hermes task may appear after the retry: %v", tasks)
	}

	// Reprocessing the aggregate: after both lanes complete, the same
	// unchanged content re-observes as no new work — the completed
	// sibling is reused, never duplicated (no third child or task).
	g8WorkComplete(t, configPath, mainChild, "run-main", "--manifest", g8Manifest(t, `[]`))
	g8WorkComplete(t, configPath, reviewChild, "run-review", "--manifest", g8Manifest(t, `[]`))
	setPlanEnv(t, vault, false)
	t.Setenv("WATCHMAN_CLOCK", "c:1:2:9:9")
	var dupOut, dupErr bytes.Buffer
	withStdin(t, `[{"name":"Inbox/retry.md","exists":true,"new":true,"size":14,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &dupOut, &dupErr); code != 0 {
			t.Fatalf("the unchanged re-observation must drop cleanly: %s", dupErr.String())
		}
	})
	if res := decodeEnvelope(t, &dupOut); res["disposition"] != "drop" {
		t.Fatalf("unchanged content must not create new work: %v", res)
	}
	// With the durable suppression cleared, the SAME change re-observes
	// and the re-derived generation-1 child keys hit the idempotency
	// constraint — the duplicate generation refuses (FAN-003 posture).
	if _, err := store.Exec(`DELETE FROM path_facts WHERE resource_id = 'vault-main' AND path = 'Inbox/retry.md'`); err != nil {
		t.Fatal(err)
	}
	dupOut.Reset()
	dupErr.Reset()
	t.Setenv("WATCHMAN_CLOCK", "c:1:2:9:8")
	withStdin(t, `[{"name":"Inbox/retry.md","exists":true,"new":true,"size":14,"type":"f"}]`, func() {
		code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &dupOut, &dupErr)
		if code != 14 {
			t.Fatalf("reprocessing the same generation must refuse at 14, got %d: out=%s err=%s", code, dupOut.String(), dupErr.String())
		}
	})
	if !strings.Contains(dupErr.String(), "dispatch_duplicate") {
		t.Fatalf("the refusal must be the duplicate-generation constraint: %s", dupErr.String())
	}
	if tasks := g8StubTasks(t, configPath); len(tasks) != tasksBefore {
		t.Fatalf("the refusal must not create a duplicate Hermes task: %v", tasks)
	}
}

// g8Manifest writes one manifest document and returns its path.
func g8Manifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestG8AC804DestinationEditRequiresReacknowledgement proves AC-804: a
// behavior-affecting destination edit produces a new destination
// revision and child idempotency identity, moves the route revision, and
// submission pauses until the production acknowledgement re-enables the
// route.
func TestG8AC804DestinationEditRequiresReacknowledgement(t *testing.T) {
	configPath, vault := g8TwoLanes(t, false)
	revBefore := g8Enable(t, configPath)
	mainChild, reviewChild := g8DispatchOccurrence(t, configPath, vault, "before edit", "edit.md")
	g8Drain(t, configPath) // the sibling submits: no unresolved old-revision work
	store := g8Store(t, configPath)
	var keyBefore, dstRevBefore string
	if err := store.QueryRow(`SELECT c.idempotency_key, c.destination_revision FROM child_dispatches c WHERE c.dispatch_id = ?`, mainChild).Scan(&keyBefore, &dstRevBefore); err != nil {
		t.Fatal(err)
	}
	g8WorkComplete(t, configPath, mainChild, "run-edit", "--manifest", g8Manifest(t, `[]`))
	g8WorkComplete(t, configPath, reviewChild, "run-edit-sibling", "--manifest", g8Manifest(t, `[]`))

	// The behavior edit: the main lane's workstream changes.
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), "        workstream: main\n", "        workstream: curated\n", 1)
	if edited == string(raw) {
		t.Fatal("fixture no longer carries the main workstream line")
	}
	if err := os.WriteFile(configPath, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(configPath)
	revAfter, _ := config.RouteRevision(cfg, "wiki")
	route := cfg.Routes["wiki"]
	mainDest, _ := route.DestinationByID("main")
	dstRevAfter := config.DestinationRevision(cfg, route, mainDest)
	if revAfter == revBefore || dstRevAfter == dstRevBefore {
		t.Fatalf("the edit must move both revisions: route %s→%s destination %s→%s", revBefore, revAfter, dstRevBefore, dstRevAfter)
	}

	// New work under the edited revision persists and pauses on the
	// acknowledged-revision gate (the production acknowledgement, E8-T3).
	var out, errb bytes.Buffer
	setPlanEnv(t, vault, false)
	os.WriteFile(filepath.Join(vault, "Inbox", "after-edit.md"), []byte("edited"), 0o644)
	withStdin(t, `[{"name":"Inbox/after-edit.md","exists":true,"new":true,"size":6,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb); code != 14 {
			t.Fatalf("submission under the edited revision must pause at 14, got %d: %s", code, errb.String())
		}
	})
	var newKey string
	if err := store.QueryRow(`SELECT c.idempotency_key FROM child_dispatches c JOIN dispatch_intents d ON d.dispatch_id = c.dispatch_id
		WHERE c.destination_id = 'main' AND d.route_revision = ?`, revAfter).Scan(&newKey); err != nil {
		t.Fatal(err)
	}
	if newKey == keyBefore {
		t.Fatal("the edited revision must produce a new child idempotency identity")
	}

	// Re-acknowledgement lifts the pause and the paused children submit
	// (both lanes: the drain is bounded per pass).
	out.Reset()
	errb.Reset()
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", revAfter, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("re-acknowledge: %s", errb.String())
	}
	var drainOut, drainErr bytes.Buffer
	if code := Run([]string{"dispatches", "drain", "--config", configPath, "--route", "wiki", "--max", "10"}, &drainOut, &drainErr); code != 0 {
		t.Fatalf("drain after re-acknowledgement: %s", drainErr.String())
	}
	if code := Run([]string{"dispatches", "drain", "--config", configPath, "--route", "wiki", "--max", "10"}, &drainOut, &drainErr); code != 0 {
		t.Fatalf("second drain pass: %s", drainErr.String())
	}
	// BOTH paused children submit after the re-acknowledgement — the
	// main lane's edited child and the sibling lane's child planned under
	// the same edited revision (review round 1: the sibling's outcome is
	// asserted, not assumed).
	var editedChildren int
	rows, rerr := store.Query(`SELECT d.state FROM child_dispatches c JOIN dispatch_intents d ON d.dispatch_id = c.dispatch_id
		WHERE d.route_revision = ?`, revAfter)
	if rerr != nil {
		t.Fatal(rerr)
	}
	states := map[string]string{}
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		states[state] = state
		editedChildren++
	}
	rows.Close()
	if editedChildren != 2 {
		t.Fatalf("both lanes' paused children must exist under the edited revision: %d", editedChildren)
	}
	for state := range states {
		if state != "accepted" {
			t.Fatalf("every paused child must submit after re-acknowledgement: %v", states)
		}
	}
	var editedState string
	if err := store.QueryRow(`SELECT state FROM dispatch_intents WHERE idempotency_key = ?`, newKey).Scan(&editedState); err != nil || editedState != "accepted" {
		t.Fatalf("the edited child must submit after re-acknowledgement: %q %v", editedState, err)
	}
}

// TestG8AC805FourReceiptOutcomes proves AC-805: completed closes the
// lane; partially_completed creates exactly one bounded same-lane
// follow-up; blocked requires manual intervention (a drain submits
// nothing for the blocked lane); failed follows its budget exactly as
// the semantics document it — with the fixture budget at one, the FIRST
// cooperative failure still has budget remaining and schedules one
// follow-up, and the SECOND failure (on that follow-up) exhausts the
// budget and resolves the route through the UNCERT operator hold
// (feedback-loop §7), never a third automatic generation.
func TestG8AC805FourReceiptOutcomes(t *testing.T) {
	configPath, vault := g8TwoLanes(t, false)
	// Budget one: the FIRST failure still has budget remaining and
	// schedules one follow-up; the SECOND failure (on that follow-up)
	// exhausts it into the UNCERT operator hold — the corrected budget
	// semantics the docstring above states (the fixture ships two; the
	// edit is the test's own fixture).
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(strings.Replace(string(raw), "    failure_budget: 2", "    failure_budget: 1", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	g8Enable(t, configPath)
	mainChild, reviewChild := g8DispatchOccurrence(t, configPath, vault, "four outcomes", "outcomes.md")
	g8Drain(t, configPath)
	store := g8Store(t, configPath)

	// partially_completed: one same-lane follow-up for the remaining scope.
	partial := g8WorkComplete(t, configPath, mainChild, "run-partial",
		"--status", "partially_completed", "--manifest", g8Manifest(t, `[]`), "--remaining-manifest", g8Manifest(t, `[{"path":"Indexes/remaining.md"}]`))
	followup, _ := partial["followup_dispatch_id"].(string)
	if followup == "" {
		t.Fatalf("the partial outcome must schedule its follow-up: %v", partial)
	}
	g8Drain(t, configPath) // the follow-up submits
	// completed: closes the lane.
	completed := g8WorkComplete(t, configPath, followup, "run-completed", "--manifest", g8Manifest(t, `[]`))
	if completed["route_state"] != "FOLLOWUP_READY" && completed["route_state"] != "IDLE" {
		t.Fatalf("the completed outcome must close the lane: %v", completed)
	}
	var followups int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE generation = 2 AND route_id = 'wiki'`).Scan(&followups); err != nil || followups != 1 {
		t.Fatalf("exactly one bounded follow-up generation: %d %v", followups, err)
	}

	// blocked: the lane stays active, nothing auto-runs — the drain
	// submits nothing for it.
	blocked := g8WorkComplete(t, configPath, reviewChild, "run-blocked", "--status", "blocked", "--manual-reason", "policy forbids the rewrite")
	if blocked["status"] != "blocked" || blocked["manual_intervention"] != true {
		t.Fatalf("the blocked outcome must surface manual intervention: %v", blocked)
	}
	drained := g8Drain(t, configPath)
	if processed, _ := drained["processed"].(float64); processed != 0 {
		t.Fatalf("a drain must submit nothing while the lane is blocked: %v", drained)
	}
	var laneState string
	if err := store.QueryRow(`SELECT lane_state FROM destination_lane_state WHERE route_id = 'wiki' AND destination_id = 'review'`).Scan(&laneState); err != nil || laneState != "ACTIVE_CLEAN" {
		t.Fatalf("the blocked lane stays active with its child: %q %v", laneState, err)
	}
	// Resolution completes the blocked lane (the operator exit).
	g8WorkComplete(t, configPath, reviewChild, "run-blocked-resolution", "--manifest", g8Manifest(t, `[]`))

	// failed: with the budget at one, the first cooperative failure
	// exhausts it and the lane resolves through the UNCERT operator hold
	// (operator reconciliation), never a second automatic generation. A
	// fresh occurrence supplies the active child both lanes freed.
	failedChild, _ := g8DispatchOccurrence(t, configPath, vault, "failed leg", "failed.md")
	// The budget is one: the FIRST failure still has budget remaining and
	// schedules one follow-up (the documented semantics); the SECOND
	// failure — on that follow-up — exhausts it and resolves through the
	// UNCERT operator hold (feedback-loop §7).
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", failedChild, "--run-id", "run-fail-1"}, &out, &errb); code != 0 {
		t.Fatalf("work begin (first failure): %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", failedChild, "--run-id", "run-fail-1", "--failure-code", "agent_error"}, &out, &errb); code != 0 {
		t.Fatalf("work fail (first): %s", errb.String())
	}
	firstFail := decodeEnvelope(t, &out)
	failureFollowup, _ := firstFail["followup_dispatch_id"].(string)
	if firstFail["status"] != "failed" || failureFollowup == "" {
		t.Fatalf("the first failure within budget must schedule its follow-up: %v", firstFail)
	}
	g8Drain(t, configPath) // the FIRST failure's follow-up submits (budget remained)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", failureFollowup, "--run-id", "run-fail-2"}, &out, &errb); code != 0 {
		t.Fatalf("work begin (second failure): %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", failureFollowup, "--run-id", "run-fail-2", "--failure-code", "agent_error"}, &out, &errb); code != 0 {
		t.Fatalf("work fail (second): %s", errb.String())
	}
	secondFail := decodeEnvelope(t, &out)
	if secondFail["status"] != "failed" || secondFail["route_state"] != "UNCERTAIN" {
		t.Fatalf("budget exhaustion must resolve through the UNCERT operator hold: %v", secondFail)
	}
	drained = g8Drain(t, configPath)
	if processed, _ := drained["processed"].(float64); processed != 0 {
		t.Fatalf("an uncertain route submits nothing automatically: %v", drained)
	}
}

// TestG8AC806AcceptanceWithoutReceiptIsActionable proves AC-806: Hermes
// acceptance without a valid attributable work receipt is never reported
// as completed — events show renders the per-child gap with the
// actionable next step and the aggregate status names the evidence gap.
// The invalid-receipt half of the criterion is pinned by
// TestE12T3InvalidLatestReceiptKeepsEvidenceGap beside this suite.
func TestG8AC806AcceptanceWithoutReceiptIsActionable(t *testing.T) {
	configPath, vault := g8TwoLanes(t, false)
	g8Enable(t, configPath)
	mainChild, _ := g8DispatchOccurrence(t, configPath, vault, "evidence gap", "gap.md")
	g8Drain(t, configPath)
	store := g8Store(t, configPath)
	var accepted string
	if err := store.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, mainChild).Scan(&accepted); err != nil || accepted != "accepted" {
		t.Fatalf("the submitted child must be accepted: %q %v", accepted, err)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"events", "show", "--config", configPath, e12t3AggregateOf(t, configPath)}, &out, &errb); code != 0 {
		t.Fatalf("events show: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["aggregate_status"] != "evidence-gap" {
		t.Fatalf("the aggregate must name the evidence gap: %v", res["aggregate_status"])
	}
	// The per-child assertions are indexed by lane and fail when an
	// expected child is absent (review round 1, testing finding): the loop
	// can never pass vacuously on an empty or short children slice.
	children, _ := res["children"].([]any)
	if len(children) != 2 {
		t.Fatalf("both lanes' children must render (non-vacuous gap proof): %v", res)
	}
	byLane := map[string]map[string]any{}
	for _, raw := range children {
		child, _ := raw.(map[string]any)
		dest, _ := child["destination"].(map[string]any)
		lane, _ := dest["id"].(string)
		if lane == "" {
			t.Fatalf("every child must name its destination lane: %v", child)
		}
		byLane[lane] = child
	}
	if len(byLane) != 2 {
		t.Fatalf("both lanes must be present, indexed by destination: %v", byLane)
	}
	gapChild, mainPresent := byLane["main"]
	if !mainPresent || gapChild["dispatch_id"] != mainChild {
		t.Fatalf("the main lane's accepted child must be found: %v", byLane)
	}
	if gapChild["completion_evidence"] != "missing" {
		t.Fatalf("the accepted child without a receipt must render the gap: %v", gapChild)
	}
	next, _ := gapChild["completion_evidence_next_step"].(string)
	if !strings.Contains(next, "work") || !strings.Contains(next, mainChild) {
		t.Fatalf("the gap must be actionable (next step naming the dispatch): %q", next)
	}
	// The sibling is accepted too (the drain submitted it): BOTH accepted
	// children without receipts are actionable gaps — the criterion names
	// acceptance, not one specific lane.
	sibling, reviewPresent := byLane["review"]
	if !reviewPresent {
		t.Fatalf("the review lane's child must be found: %v", byLane)
	}
	if sibling["completion_evidence"] != "missing" {
		t.Fatalf("the accepted sibling without a receipt must also render the gap: %v", sibling)
	}
	if _, ok := sibling["completion_evidence_next_step"].(string); !ok {
		t.Fatalf("the sibling's gap must be actionable too: %v", sibling)
	}
}

// TestG8StressConcurrentArrivalsPerLane is the TST-005/TST-013 posture
// at the gate (CON-001 per lane, CON-007, FAN-003): concurrent fan-out
// arrivals from many goroutines over a two-destination route leave
// exactly ONE active child per lane, no cross-lane blocking (both lanes
// end ACTIVE_DIRTY with every loser merged into its own lane's dirty
// generation), exactly one child per (occurrence, destination), and the
// fake sink observes no duplicate external submission per lane key. The
// cross-process leg is NOT duplicated here: the crashbin-based
// multi-process coverage (TestG2MultiProcessOneActiveRouteDispatch and
// the AC-207 interrupted-upgrade loop over every migration unit) already
// proves the process-level serialization this criterion needs; the
// in-process race suite plus that g2 coverage is the recorded evidence.
func TestG8StressConcurrentArrivalsPerLane(t *testing.T) {
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki", "route-rev-1", "policy-rev-1", "vault-main", "fake-main", "{}", "2026-08-29T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRouteActivation(context.Background(), "wiki", "enabled", "route-rev-1", "", "2026-08-29T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	coordinator := &dispatch.Coordinator{Store: s, Now: func() string { return dispatch.Timestamp(time.Now()) }, Actor: "g8-stress"}
	const bursts = 10
	var wg sync.WaitGroup
	errs := make([]error, bursts)
	for i := 0; i < bursts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lins := g8StressLineages(t, s, i)
			if _, err := coordinator.ArrivalFanout(context.Background(), lins); err != nil {
				errs[i] = err
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("fan-out arrival %d: %v", i, err)
		}
	}

	// Exactly one child per lane, both lanes active, every loser merged
	// into its own lane's dirty generation.
	rows, err := s.Query(`SELECT destination_id, lane_state, COALESCE(active_dispatch_id, ''), dirty_generation FROM destination_lane_state WHERE route_id = 'wiki' ORDER BY destination_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	lanes := 0
	for rows.Next() {
		var dest, state, active string
		var dirty int
		if err := rows.Scan(&dest, &state, &active, &dirty); err != nil {
			t.Fatal(err)
		}
		lanes++
		if active == "" || state != "ACTIVE_DIRTY" {
			t.Fatalf("lane %s must hold its winner as ACTIVE_DIRTY: %q %q", dest, state, active)
		}
		if dirty != bursts-1 {
			t.Fatalf("lane %s must record every loser in its OWN dirty generation: %d want %d", dest, dirty, bursts-1)
		}
	}
	if lanes != 2 {
		t.Fatalf("both lanes must end active (no cross-lane blocking): %d", lanes)
	}

	// Submit both winners through the fake sink: each lane's key reaches
	// the target exactly once, and a second submission attempt of either
	// winner never reaches the sink at all (the accepted intent refuses
	// before the invocation — no duplicate per-key submission exists).
	fake := fakesink.New("fake-main",
		fakesink.Step{Result: fakesink.Accepted("t_00000001")},
		fakesink.Step{Result: fakesink.Accepted("t_00000002")})
	rt := &dispatch.Runtime{Store: s, Sink: fake, Now: time.Now, LeaseTTL: time.Minute, Actor: "g8-stress"}
	winnerRows, err := s.Query(`SELECT active_dispatch_id FROM destination_lane_state WHERE route_id = 'wiki' ORDER BY destination_id`)
	if err != nil {
		t.Fatal(err)
	}
	var winners []string
	for winnerRows.Next() {
		var id string
		if err := winnerRows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		winners = append(winners, id)
	}
	winnerRows.Close()
	if len(winners) != 2 {
		t.Fatalf("one winner per lane: %v", winners)
	}
	for _, id := range winners {
		report, err := rt.SubmitOnce(context.Background(), id, "g8-stress")
		if err != nil {
			t.Fatalf("winner %s must submit: %v", id, err)
		}
		if report.To != records.IntentAccepted {
			t.Fatalf("winner %s must be accepted, got %s", id, report.To)
		}
		// The re-submission proof: the accepted intent refuses before any
		// sink invocation, so the per-key submission count cannot grow.
		if _, err := rt.SubmitOnce(context.Background(), id, "g8-stress-again"); err == nil {
			t.Fatalf("an accepted winner must refuse re-submission: %s", id)
		}
	}
	if len(fake.Submissions()) != len(winners) {
		t.Fatalf("exactly one external submission per lane key, got %d for %d lanes", len(fake.Submissions()), len(winners))
	}
	// The stress fixtures persist their keys on the intent rows (the
	// minimal request document carries none), so per-lane submission
	// uniqueness is the count — exactly one external submission per lane
	// winner — with the store's child keys checked distinct at the source.
	var childKeys int
	if err := s.QueryRow(`SELECT COUNT(DISTINCT idempotency_key) FROM child_dispatches`).Scan(&childKeys); err != nil || childKeys != len(winners) {
		t.Fatalf("each lane's child key must be distinct: %d winners %v (%v)", childKeys, winners, err)
	}

	// Exactly one child per (occurrence, destination), asserted against
	// the EXPECTED total rather than a self-comparison (review round 1):
	// exactly one winner child per lane means exactly 2 child rows and
	// exactly 2 distinct (aggregate, destination) pairs — the bursts-1
	// losers per lane merged, never duplicated into children.
	var childRows, distinctPairs int
	if err := s.QueryRow(`SELECT COUNT(*) FROM child_dispatches`).Scan(&childRows); err != nil {
		t.Fatal(err)
	}
	if err := s.QueryRow(`SELECT COUNT(*) FROM (SELECT DISTINCT aggregate_id, destination_id FROM child_dispatches)`).Scan(&distinctPairs); err != nil {
		t.Fatal(err)
	}
	if childRows != len(winners) || distinctPairs != len(winners) || childRows != distinctPairs {
		t.Fatalf("exactly one child per (occurrence, destination): rows=%d pairs=%d winners=%d", childRows, distinctPairs, len(winners))
	}
	// Belt-and-braces guard: no pair may ever carry more than one child
	// even if a future fixture change grows the expected count.
	var duplicated int
	if err := s.QueryRow(`SELECT COUNT(*) FROM (SELECT aggregate_id, destination_id, COUNT(*) AS n FROM child_dispatches GROUP BY aggregate_id, destination_id HAVING n > 1)`).Scan(&duplicated); err != nil || duplicated != 0 {
		t.Fatalf("no (occurrence, destination) pair may carry two children: %d %v", duplicated, err)
	}
}

// g8StressLaneProjection/g8StressLaneRevision render one stress lane's
// real content-addressed pair (E12 epic validation: the store verifies
// persisted revisions are the content address of their bytes and child
// references have durable rows; the derivation is the canonical
// records.RevisionOfProjection).
func g8StressLaneProjection(destinationID string) string {
	return `{"id":"` + destinationID + `","stress":true}`
}

func g8StressLaneRevision(destinationID string) string {
	return records.RevisionOfProjection(g8StressLaneProjection(destinationID))
}

// g8StressLineages builds one fan-out occurrence's two lane lineages
// with unique batch identities per burst. It intentionally mirrors the
// app/dispatch concurrency fixture shape
// (internal/app/dispatch/e12t2_test.go's e12t2WithLane) rather than
// sharing it through testsupport: exporting a lineage builder would
// force the app package's test contract into a public surface for one
// caller, and the two fixtures differ in their request documents (the
// gate's stress submits through the real runtime, so it keeps its own
// minimal shape; review round 1, maintainability finding — recorded as
// a settled duplication, not an accident).
func g8StressLineages(t *testing.T, s *sqlite.Store, n int) []ports.Lineage {
	t.Helper()
	now := "2026-08-29T01:00:00Z"
	decisionID := fmt.Sprintf("decision-g8-%d", n)
	batchID := fmt.Sprintf("batch-g8-%d", n)
	base := ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: fmt.Sprintf("obs-g8-%d", n), SchemaVersion: "agent-dispatch.source-observation/v1",
			SourceType: "watchman", SourceID: "watchman-main", TriggerName: "trig",
			ResourceID: "vault-main", ObservedAt: now, ReceivedAt: now,
			RawPayloadDigest: fmt.Sprintf("sha256:%064d", n), IngestStatus: "accepted",
		},
		Batch: ports.BatchInput{
			BatchID: batchID, RouteID: "wiki", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: now,
			ContentFingerprint: fmt.Sprintf("sha256:%064d", n), ObservationIDs: []string{fmt.Sprintf("obs-g8-%d", n)},
		},
		Decision: ports.DecisionInput{
			DecisionID: decisionID, BatchID: batchID, RouteID: "wiki",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch",
			Classification: "normal", ReasonCodesJSON: `["normal_batch"]`, CreatedAt: now, Actor: "planner",
		},
	}
	out := make([]ports.Lineage, 0, 2)
	for _, lane := range []struct{ id, workstream string }{{"wiki-primary", "maintenance"}, {"wiki-secondary", "review"}} {
		lin := base
		lin.Intent = ports.IntentInput{
			DispatchID: fmt.Sprintf("dispatch-g8-%d-%s", n, lane.id), DecisionID: decisionID, RouteID: "wiki",
			RouteRevision: "route-rev-1", TargetID: "fake-main", TargetType: "hermes_kanban",
			TargetScope: "board-main", ResourceID: "vault-main", Generation: 1,
			IdempotencyKey:     "agent-dispatch:v2:sha256:" + strings.Repeat(fmt.Sprintf("%d", n%10), 64) + "-" + lane.id,
			ContentFingerprint: fmt.Sprintf("sha256:%064d", n), ManifestDigest: "sha256:" + strings.Repeat("d", 64),
			RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: `{}`, CreatedAt: now,
			Fanout: &ports.FanoutInput{
				AggregateID: fmt.Sprintf("agg-g8-%d-%s", n, lane.id), Origin: "arrival",
				DestinationID: lane.id, DestinationRevision: g8StressLaneRevision(lane.id), Workstream: lane.workstream,
				Selections: []records.DestinationSelection{{
					DestinationID: lane.id, DestinationRevision: g8StressLaneRevision(lane.id),
					Workstream: lane.workstream, Reason: "fanout_mode:all",
				}},
				Revisions: []ports.DestinationRevisionInput{{
					DestinationID: lane.id, Revision: g8StressLaneRevision(lane.id), ProjectionJSON: g8StressLaneProjection(lane.id),
				}},
			},
		}
		out = append(out, lin)
	}
	return out
}

// TestG8RealHermesTwoDestinationWalkthrough is the TST-007 isolated
// public-Hermes leg of the gate: one two-destination dispatch against a
// REAL installed Hermes creates two tasks under the two profiles/
// workstreams on a disposable board, records both receipts, and renders
// the complete aggregate through events show; the board is hard-deleted
// afterwards exactly as the E0-T4 probe did. Skipped as an explicit
// environment-dependent evidence gap when no supported Hermes is
// installed (the gate's pass/fail never depends on this leg — every
// AC-801 through AC-806 criterion is proven deterministically above).
func TestG8RealHermesTwoDestinationWalkthrough(t *testing.T) {
	hermesenv.SkipUnlessSupportedHermes(t, func(firstLine string) bool {
		ver, perr := hermeskanban.ParseVersionOutput(firstLine)
		return perr == nil && ver.Eligible(hermeskanban.MinimumEligibleVersion)
	})
	bin, _ := exec.LookPath("hermes")
	// The HOME redirect below happens after the board exists; the cleanup
	// defer may therefore run under the REDIRECTED HOME (E12 epic
	// validation, review posture): capture the real HOME now so the
	// deletion command runs in the operator's environment, and the board
	// name is recorded in the test log for manual removal if the cleanup
	// still fails (the disposable-board guarantee stays honest).
	realHome := os.Getenv("HOME")
	cleanupEnv := func(argv ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, argv...)
		cmd.Env = append(os.Environ(), "HOME="+realHome)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	// The frozen 0.19.1 create surface: a drifted surface is the E11-T2
	// capability probe's detection, not this walkthrough's (same posture
	// as the adapter's realboard test).
	if help, herr := g3Hermes(t, "kanban", "create", "-h"); herr != nil || !strings.Contains(help, "--mutex-key") {
		t.Skipf("installed hermes create surface drifted from the frozen 0.19.1 flags; the capability probe owns shape detection: %s", help)
	}
	// The disposable board is uniquely generated (nanosecond tag) and the
	// cleanup deletes exactly that one board by name — never a wildcard or
	// the user's active selection. It is best-effort: the HOME redirect
	// below happens AFTER the board exists, and a cleanup failure under a
	// redirected environment must not mask the walkthrough's result, so it
	// logs the orphan honestly instead of failing the test (review round 1,
	// security finding).
	board := fmt.Sprintf("agent-dispatch-g8-%d", time.Now().UnixNano())
	if out, err := cleanupEnv("kanban", "boards", "create", board); err != nil {
		t.Skipf("boards create unavailable (%v): %s", err, out)
	}
	t.Logf("disposable walkthrough board: %s", board)
	defer func() {
		out, err := cleanupEnv("kanban", "boards", "rm", board, "--delete")
		if err != nil {
			t.Logf("best-effort cleanup of the disposable board %s failed (%v): %s — remove it manually (TST-007 posture)", board, err, out)
		}
	}()

	// The walkthrough fixture: the two-destination shape bound to the real
	// executable and the disposable board, isolated per test.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AGENT_DISPATCH_CONFIG", "")
	t.Setenv("AGENT_DISPATCH_STATE_DIR", filepath.Join(home, "state"))
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	if err := os.MkdirAll(filepath.Join(vault, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "g8.md"), []byte("real walkthrough"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	raw := fmt.Sprintf(`version: 1
instance:
  id: g8-real
  state_dir: %s
resources:
  vault-main:
    type: directory
    root: %s
    file_scope: markdown
hermes_targets:
  hermes-main:
    board: %s
    minimum_version: 0.20.5
    compatibility: capability_probe
    executable: %s
    submit_timeout: 30s
    lookup_timeout: 30s
    environment_allowlist: [PATH, HOME]
routes:
  wiki:
    enabled: true
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: agent-dispatch.wiki.g8
      include: ["**/*.md"]
      exclude: [".obsidian/workspace*.json"]
    batching:
      automatic_threshold: 25
      hard_limit: 100
      max_manifest_bytes: 262144
    policy:
      protected: []
      immutable: []
      bulk_action: quarantine
      overflow_action: reconcile
      fresh_instance_action: reconcile
      unsafe_path_action: quarantine
    fanout_mode: all
    destinations:
      - id: main
        target: hermes-main
        profile: default
        skills: [llm-wiki]
        workstream: main
        execution_hints:
          max_runtime: 30m
          max_attempts: 2
      - id: review
        target: hermes-main
        profile: default
        skills: [llm-wiki]
        workstream: review
        execution_hints:
          max_runtime: 30m
          max_attempts: 2
    submission_retry:
      max_attempts: 3
      initial_backoff: 1s
      max_backoff: 2s
      multiplier: 2.0
      jitter_fraction: 0.0
    latest_state: true
    failure_budget: 2
    active_stale_after: 2h
    reconciliation:
      initial: true
      daily_expected: true
`, filepath.Join(home, "state"), vault, board, bin)
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	setPlanEnv(t, vault, false)
	var out, errb bytes.Buffer
	rev := func() string {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		r, _ := config.RouteRevision(cfg, "wiki")
		return r
	}()
	if code := Run([]string{"route", "enable", "--config", cfgPath, "--route", "wiki", "--acknowledge-production-gate", rev, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("real enable: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[{"name":"Inbox/g8.md","exists":true,"new":true,"size":16,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", cfgPath, "--input", "watchman"}, &out, &errb); code != 0 {
			t.Fatalf("real dispatch: %s", errb.String())
		}
	})
	res := decodeEnvelope(t, &out)
	if res["submitted"] != true {
		t.Fatalf("the first lane must submit against the real Hermes: %v", res)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--config", cfgPath, "--route", "wiki", "--max", "10"}, &out, &errb); code != 0 {
		t.Fatalf("real drain: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"events", "show", "--config", cfgPath, e12t3AggregateOf(t, cfgPath)}, &out, &errb); code != 0 {
		t.Fatalf("real events show: %s", errb.String())
	}
	shown := decodeEnvelope(t, &out)
	children, _ := shown["children"].([]any)
	if shown["count"] != float64(2) || len(children) != 2 {
		t.Fatalf("the real aggregate must list both children: %v", shown)
	}
	for _, rawChild := range children {
		child, _ := rawChild.(map[string]any)
		if child["acceptance"] != "accepted" {
			t.Fatalf("both children must be accepted by the real Hermes: %v", child)
		}
		if ref, _ := child["external_ref"].(string); !strings.HasPrefix(ref, "t_") {
			t.Fatalf("each child must name its real Hermes task: %v", child)
		}
	}
	// The receipts complete both lanes (the walkthrough closes its work).
	store := func() *sqlite.Store {
		s, err := sqlite.Open(filepath.Join(platformpaths.ResolveStateDir(filepath.Join(home, "state")), StateDBName))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}()
	defer store.Close()
	for _, lane := range []string{"main", "review"} {
		var id string
		if err := store.QueryRow(`SELECT dispatch_id FROM child_dispatches WHERE destination_id = ?`, lane).Scan(&id); err != nil {
			t.Fatal(err)
		}
		manifest := filepath.Join(t.TempDir(), lane+".json")
		os.WriteFile(manifest, []byte(`[]`), 0o600)
		var wOut, wErr bytes.Buffer
		if code := Run([]string{"work", "begin", "--config", cfgPath, "--dispatch-id", id, "--run-id", "run-" + lane}, &wOut, &wErr); code != 0 {
			t.Fatalf("real work begin %s: %s", lane, wErr.String())
		}
		wOut.Reset()
		wErr.Reset()
		if code := Run([]string{"work", "complete", "--config", cfgPath, "--dispatch-id", id, "--run-id", "run-" + lane, "--manifest", manifest}, &wOut, &wErr); code != 0 {
			t.Fatalf("real work complete %s: %s", lane, wErr.String())
		}
	}
}

// TestEpicValidationReconcileFansOutBothLanes pins the E12 epic
// validation residual: the reconcile path fans the reconciliation intent
// out per selected destination — one shared aggregate, one child per
// lane, origin reconcile — instead of committing one child on the
// canonically-first lane only.
func TestEpicValidationReconcileFansOutBothLanes(t *testing.T) {
	configPath, vault := g8TwoLanes(t, false)
	g8Enable(t, configPath)
	// Due work exists on both lanes' scope: a fresh file the initial
	// reconciliation will diff.
	os.WriteFile(filepath.Join(vault, "Inbox", "reconcile-both.md"), []byte("due work"), 0o644)
	var out, errb bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial", "--submit"}, &out, &errb); code != 0 {
		t.Fatalf("reconcile --submit: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["submitted"] != true {
		t.Fatalf("the reconcile intent must submit: %v", res)
	}
	lanes, _ := res["reconcile_lanes"].([]any)
	if len(lanes) != 1 {
		t.Fatalf("the sibling lane's child must be listed beside the result: %v", res)
	}
	store := g8Store(t, configPath)
	rows, err := store.Query(`SELECT c.destination_id, c.dispatch_id, a.origin FROM child_dispatches c JOIN aggregate_events a ON a.aggregate_id = c.aggregate_id WHERE a.origin = 'reconcile'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	byLane := map[string]string{}
	aggregateIDs := map[string]bool{}
	for rows.Next() {
		var dest, dispatch, origin string
		if err := rows.Scan(&dest, &dispatch, &origin); err != nil {
			t.Fatal(err)
		}
		byLane[dest] = dispatch
	}
	if len(byLane) != 2 {
		t.Fatalf("the reconcile fan-out must create one child per lane: %v", byLane)
	}
	for _, lane := range []string{"main", "review"} {
		if byLane[lane] == "" {
			t.Fatalf("lane %s must carry a reconcile child: %v", lane, byLane)
		}
		// Both children sit beneath ONE shared aggregate.
		var aggregate string
		if err := store.QueryRow(`SELECT aggregate_id FROM child_dispatches WHERE dispatch_id = ?`, byLane[lane]).Scan(&aggregate); err != nil {
			t.Fatal(err)
		}
		aggregateIDs[aggregate] = true
	}
	if len(aggregateIDs) != 1 {
		t.Fatalf("both reconcile children must share one aggregate: %v", aggregateIDs)
	}
	// Durable-first order (E12 epic whole-review round 1): the sibling
	// children commit BEFORE the first lane's reconciliation transaction,
	// so the shared aggregate's creation audit names the SIBLING that
	// created it — the crash window between the two leaves ready siblings
	// the drain submits, never an unmarked first lane.
	var aggregateID string
	for id := range aggregateIDs {
		aggregateID = id
	}
	var createdContext string
	if err := store.QueryRow(`SELECT context_json FROM state_transitions
		WHERE entity_type = 'aggregate_event' AND entity_id = ?`, aggregateID).Scan(&createdContext); err != nil {
		t.Fatalf("the aggregate's creation audit must persist: %v", err)
	}
	if !strings.Contains(createdContext, `"review"`) || !strings.Contains(createdContext, `"origin":"reconcile"`) {
		t.Fatalf("the aggregate must be created by the durable-first sibling commit, not the first lane: %s", createdContext)
	}
	// Each sibling child's OWN creation audit names origin reconcile and
	// its own destination (E12 epic whole-review round 2): the sibling is
	// reconcile work on its lane, never misattributed arrival work.
	var reviewChild string
	if err := store.QueryRow(`SELECT dispatch_id FROM child_dispatches WHERE destination_id = 'review'`).Scan(&reviewChild); err != nil {
		t.Fatal(err)
	}
	var siblingContext string
	if err := store.QueryRow(`SELECT context_json FROM state_transitions
		WHERE entity_type = 'dispatch_intent' AND entity_id = ? AND transition_id = ?`, reviewChild, reviewChild+":created").Scan(&siblingContext); err != nil {
		t.Fatalf("the sibling's creation audit must persist: %v", err)
	}
	if !strings.Contains(siblingContext, `"origin":"reconcile"`) || !strings.Contains(siblingContext, `"destination_id":"review"`) {
		t.Fatalf("the sibling creation audit must name origin reconcile and its own destination: %s", siblingContext)
	}
}

// TestEpicValidationReconcileSiblingFailureExitsNonzero pins the exit-code
// policy of the reconcile fan-out (E12 epic whole-review round 1): a
// sibling child whose commit fails for a non-slot-held reason is never
// silent and never zeroes the exit — the reconcile still delivers its
// first lane, the envelope lists the failed lane beside the result, and
// the error rides stderr with a mapped non-zero exit.
func TestEpicValidationReconcileSiblingFailureExitsNonzero(t *testing.T) {
	configPath, vault := g8TwoLanes(t, false)
	g8Enable(t, configPath)
	os.WriteFile(filepath.Join(vault, "Inbox", "sibling-failure.md"), []byte("due work"), 0o644)
	store := g8Store(t, configPath)
	defer store.Close()
	// Stage a store-layer fault on exactly the sibling lane's child insert
	// (the durable-first order commits the sibling BEFORE the first lane's
	// transaction, and the trigger's WHEN clause leaves the main lane's
	// child insert untouched): a non-slot-held failure by construction.
	if _, err := store.Exec(`CREATE TRIGGER stage_sibling_commit_failure
		BEFORE INSERT ON child_dispatches WHEN NEW.destination_id = 'review'
		BEGIN SELECT RAISE(ABORT, 'staged sibling commit failure'); END`); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial"}, &out, &errb)
	if code == 0 {
		t.Fatalf("a failed sibling commit must exit non-zero: %s", out.String())
	}
	// The stdout envelope keeps the delivered reconcile result with the
	// failed lane listed (ok:false rides the stderr error envelope).
	res := decodeEnvelope(t, &out)
	lanes, _ := res["reconcile_lanes"].([]any)
	if len(lanes) != 1 {
		t.Fatalf("the failed sibling lane must be listed beside the result: %v", res)
	}
	entry, _ := lanes[0].(map[string]any)
	if entry["destination_id"] != "review" || entry["committed"] != false || entry["error"] == nil {
		t.Fatalf("the failed sibling entry carries its bounded error: %v", entry)
	}
	if !strings.Contains(errb.String(), "review") {
		t.Fatalf("the sibling failure must surface on stderr: %s", errb.String())
	}
	// The first lane's child still committed through the reconciliation
	// transaction (CON-007 isolation): the occurrence delivered its main
	// lane despite the sibling's failure.
	var mainChildren int
	if err := store.QueryRow(`SELECT COUNT(*) FROM child_dispatches c
		JOIN aggregate_events a ON a.aggregate_id = c.aggregate_id
		WHERE a.origin = 'reconcile' AND c.destination_id = 'main'`).Scan(&mainChildren); err != nil || mainChildren != 1 {
		t.Fatalf("the first lane must commit despite the sibling failure: %d %v", mainChildren, err)
	}
	// The storage fault classifies as STORAGE (20), never internal: the
	// sibling commit wraps its store failures as typed store errors (E12
	// epic whole-review round 2).
	if code != 20 {
		t.Fatalf("a store-fault sibling failure must exit 20 (storage), got %d", code)
	}
}

// TestEpicValidationReconcileFirstLaneFailureSurfacesSiblings pins the
// failure-path visibility (E12 epic whole-review round 2): the
// durable-first order commits the siblings BEFORE the first lane's
// transaction, so when the FIRST lane's commit fails the already-committed
// siblings still reach the operator — the error envelope's result slot
// carries the lane listing and stderr names each committed sibling.
func TestEpicValidationReconcileFirstLaneFailureSurfacesSiblings(t *testing.T) {
	configPath, vault := g8TwoLanes(t, false)
	g8Enable(t, configPath)
	os.WriteFile(filepath.Join(vault, "Inbox", "first-lane-failure.md"), []byte("due work"), 0o644)
	store := g8Store(t, configPath)
	defer store.Close()
	// The staged fault hits exactly the FIRST lane's child insert; the
	// sibling lane's child commits before it (durable-first).
	if _, err := store.Exec(`CREATE TRIGGER stage_first_lane_failure
		BEFORE INSERT ON child_dispatches WHEN NEW.destination_id = 'main'
		BEGIN SELECT RAISE(ABORT, 'staged first lane failure'); END`); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial"}, &out, &errb)
	if code == 0 {
		t.Fatalf("the failed first-lane commit must exit non-zero: %s", out.String())
	}
	// The sibling outcomes ride the stderr error envelope's result slot
	// (writeErrorWithResult), committed siblings included. The envelope is
	// the JSON line on stderr beside the plain-text notes.
	var errEnvelope map[string]any
	var envelopeLine string
	for _, line := range strings.Split(errb.String(), "\n") {
		if strings.HasPrefix(line, "{") {
			envelopeLine = line
		}
	}
	if err := json.Unmarshal([]byte(envelopeLine), &errEnvelope); err != nil {
		t.Fatalf("the failure must write the error envelope: %s", errb.String())
	}
	result, _ := errEnvelope["result"].(map[string]any)
	lanes, _ := result["reconcile_lanes"].([]any)
	if len(lanes) != 1 {
		t.Fatalf("the committed sibling must ride the failure envelope: %v", errEnvelope)
	}
	entry, _ := lanes[0].(map[string]any)
	if entry["destination_id"] != "review" || entry["committed"] != true {
		t.Fatalf("the committed sibling must be visible beside the error: %v", entry)
	}
	// And on stderr as a plain note, so the operator sees it without
	// parsing the envelope.
	if !strings.Contains(errb.String(), "committed its child") || !strings.Contains(errb.String(), "review") {
		t.Fatalf("the committed sibling must be named on stderr: %s", errb.String())
	}
	// The sibling really is durable: its child row exists beneath the
	// reconcile aggregate while the first lane has none.
	var reviewChildren, mainChildren int
	store.QueryRow(`SELECT COUNT(*) FROM child_dispatches WHERE destination_id = 'review'`).Scan(&reviewChildren)
	store.QueryRow(`SELECT COUNT(*) FROM child_dispatches WHERE destination_id = 'main'`).Scan(&mainChildren)
	if reviewChildren != 1 || mainChildren != 0 {
		t.Fatalf("exactly the sibling committed: review=%d main=%d", reviewChildren, mainChildren)
	}
}

// TestEpicValidationReconcileRetryAfterPartialFailure pins the truthful
// retry shape (E12 epic whole-review round 2): a retry after a partial
// failure is a FRESH occurrence whose already-committed lane refuses with
// the slot-held skip — nothing new commits for that lane, no duplicate
// child appears, and the retry reports the lane as skipped with its note.
func TestEpicValidationReconcileRetryAfterPartialFailure(t *testing.T) {
	configPath, vault := g8TwoLanes(t, false)
	g8Enable(t, configPath)
	os.WriteFile(filepath.Join(vault, "Inbox", "retry-partial.md"), []byte("due work"), 0o644)
	store := g8Store(t, configPath)
	defer store.Close()
	// Run 1: the first lane fails, the sibling commits (durable-first).
	if _, err := store.Exec(`CREATE TRIGGER stage_first_lane_failure
		BEFORE INSERT ON child_dispatches WHEN NEW.destination_id = 'main'
		BEGIN SELECT RAISE(ABORT, 'staged first lane failure'); END`); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial"}, &out, &errb); code == 0 {
		t.Fatalf("run 1 must fail on the staged first-lane fault: %s", out.String())
	}
	if _, err := store.Exec(`DROP TRIGGER stage_first_lane_failure`); err != nil {
		t.Fatal(err)
	}
	// Run 2 (the retry): the first lane's work is still due, the sibling's
	// lane already holds its committed child.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "initial"}, &out, &errb); code != 0 {
		t.Fatalf("the retry must succeed once the fault clears: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	lanes, _ := res["reconcile_lanes"].([]any)
	if len(lanes) != 1 {
		t.Fatalf("the retry must report the already-committed sibling lane: %v", res)
	}
	entry, _ := lanes[0].(map[string]any)
	note, _ := entry["note"].(string)
	if entry["destination_id"] != "review" || entry["committed"] != false || note == "" {
		t.Fatalf("the retry reports the committed lane as the slot-held skip: %v", entry)
	}
	// The skip warning rides the envelope's warnings member too (E12 cold
	// validation round 1): a regression that silently drops it must fail.
	var outer struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(out.Bytes(), &outer); err != nil {
		t.Fatalf("decode outer envelope: %v", err)
	}
	warned := false
	for _, text := range outer.Warnings {
		if strings.Contains(text, "reconcile sibling lane review skipped") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("the slot-held skip must warn on the envelope: %v", outer.Warnings)
	}
	// Nothing new committed for the retrying lane and no duplicate exists:
	// the earlier child IS that lane's reconciliation.
	var reviewChildren, mainChildren int
	store.QueryRow(`SELECT COUNT(*) FROM child_dispatches WHERE destination_id = 'review'`).Scan(&reviewChildren)
	store.QueryRow(`SELECT COUNT(*) FROM child_dispatches WHERE destination_id = 'main'`).Scan(&mainChildren)
	if reviewChildren != 1 || mainChildren != 1 {
		t.Fatalf("the retry commits nothing new for the held lane and delivers the first lane: review=%d main=%d", reviewChildren, mainChildren)
	}
}

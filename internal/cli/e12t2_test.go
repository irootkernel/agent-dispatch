package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/fingerprint"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E12-T2 acceptance coverage at the CLI boundary: one arrival over a
// two-destination route fans out to two children under one aggregate with
// distinct child keys (AC-802 shape), a sibling lane's dead letter never
// blocks the other lane (CON-007), and a destination behavior edit pauses
// submission until re-acknowledgement (CON-010).

// e12t2TwoDestinationFixture writes the e4t3 fixture with a second
// destination sharing the target and returns the config path and vault.
func e12t2TwoDestinationFixture(t *testing.T) (string, string) {
	t.Helper()
	configPath, vault := e4t3Fixture(t)
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
	// Two lanes over the one shared target (FAN-011): the alpha lane keeps
	// the fixture's behavior, the beta lane feeds the review workstream.
	dest := "    fanout_mode: all\n    destinations:\n" +
		"      - id: main\n        target: hermes-main\n        profile: wiki-maintainer\n        skills: [llm-wiki]\n        mutex_key: wiki-publish\n        workstream: main\n        execution_hints:\n          max_runtime: 30m\n          max_attempts: 2\n" +
		"      - id: review\n        target: hermes-main\n        profile: wiki-maintainer\n        skills: [llm-wiki]\n        workstream: review\n        execution_hints:\n          max_runtime: 30m\n          max_attempts: 2"
	updated = updated[:start] + dest + updated[end:]
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath, vault
}

// TestE12T2OneArrivalFansOutTwoChildrenUnderOneAggregate pins the AC-802
// shape end to end (E12-T2, FAN-002/FAN-003): ONE arrival on a
// two-destination route creates two child dispatches beneath ONE shared
// aggregate event and ONE shared decision, each child carrying its own
// destination identity and a distinct DAT-014 child key, and each lane
// holding its own ACTIVE_CLEAN slot (CON-007).
func TestE12T2OneArrivalFansOutTwoChildrenUnderOneAggregate(t *testing.T) {
	configPath, vault := e12t2TwoDestinationFixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	rev, _ := config.RouteRevision(cfg, "wiki")
	var out, errb bytes.Buffer
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", rev, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("enable: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb); code != 0 {
			t.Fatalf("dispatch: %s", errb.String())
		}
	})
	// The envelope lists every selected destination's child.
	res := decodeEnvelope(t, &out)
	fanout, _ := res["fanout"].([]any)
	if len(fanout) != 2 {
		t.Fatalf("the dispatch envelope must list both lanes' children: %v", res)
	}

	store := e5t1Store(t, configPath)
	defer store.Close()
	ctx := context.Background()
	var aggregateID string
	var children int
	if err := store.QueryRow(`SELECT COUNT(*) FROM child_dispatches`).Scan(&children); err != nil || children != 2 {
		t.Fatalf("one arrival must create two children: %d %v", children, err)
	}
	if err := store.QueryRow(`SELECT COUNT(DISTINCT aggregate_id) FROM child_dispatches`).Scan(&aggregateID); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Query(`SELECT c.dispatch_id, c.destination_id, c.workstream, c.idempotency_key, c.aggregate_id, d.decision_id
		FROM child_dispatches c JOIN dispatch_intents d ON d.dispatch_id = c.dispatch_id ORDER BY c.destination_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type childRow struct{ dispatch, destination, workstream, key, aggregate, decision string }
	var got []childRow
	for rows.Next() {
		var c childRow
		if err := rows.Scan(&c.dispatch, &c.destination, &c.workstream, &c.key, &c.aggregate, &c.decision); err != nil {
			t.Fatal(err)
		}
		got = append(got, c)
	}
	if len(got) != 2 {
		t.Fatalf("two child rows: %d", len(got))
	}
	if got[0].aggregate != got[1].aggregate || got[0].aggregate == "" {
		t.Fatalf("both children must share one aggregate: %+v", got)
	}
	if got[0].decision != got[1].decision || got[0].decision == "" {
		t.Fatalf("both children must share one decision: %+v", got)
	}
	if got[0].destination == got[1].destination || got[0].key == got[1].key {
		t.Fatalf("sibling lanes must carry distinct destinations and child keys: %+v", got)
	}
	if got[0].destination != "main" || got[1].destination != "review" {
		t.Fatalf("both configured lanes must be selected (fanout_mode all): %+v", got)
	}
	// Both lanes hold their own ACTIVE_CLEAN slot (CON-007).
	lanes, err := store.Query(`SELECT destination_id, lane_state, COALESCE(active_dispatch_id, '') FROM destination_lane_state WHERE route_id = 'wiki' ORDER BY destination_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer lanes.Close()
	seen := map[string][2]string{}
	for lanes.Next() {
		var dest, state, active string
		if err := lanes.Scan(&dest, &state, &active); err != nil {
			t.Fatal(err)
		}
		seen[dest] = [2]string{state, active}
	}
	for _, child := range got {
		if pair, ok := seen[child.destination]; !ok || pair[0] != "ACTIVE_CLEAN" || pair[1] != child.dispatch {
			t.Fatalf("lane %s must hold its own child's slot: %v", child.destination, seen)
		}
	}
	// The shared aggregate's selection summary records both lanes.
	agg, err := store.LoadAggregateEvent(ctx, got[0].aggregate)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(agg.SelectionJSON, `"main"`) || !strings.Contains(agg.SelectionJSON, `"review"`) {
		t.Fatalf("the aggregate summary must record both selections: %s", agg.SelectionJSON)
	}
	// Both referenced destination revisions persisted beside it (DAT-010).
	for _, child := range got {
		var n int
		if err := store.QueryRow(`SELECT COUNT(*) FROM destination_revisions WHERE route_id = 'wiki' AND destination_id = ?`, child.destination).Scan(&n); err != nil || n != 1 {
			t.Fatalf("destination %s revision must persist: %d %v", child.destination, n, err)
		}
	}
}

// TestE12T2SiblingDeadLetterDoesNotBlockOtherLane pins CON-007's operator
// surface: closing one lane's dead letter releases only that lane's slot
// and never blocks the sibling's dispatch, which still leases and submits.
func TestE12T2SiblingDeadLetterDoesNotBlockOtherLane(t *testing.T) {
	configPath, vault := e12t2TwoDestinationFixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	cfg, _ := config.Load(configPath)
	rev, _ := config.RouteRevision(cfg, "wiki")
	var out, errb bytes.Buffer
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", rev, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("enable: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[{"name":"Inbox/dead.md","exists":true,"new":true,"size":6,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb); code != 0 {
			t.Fatalf("dispatch: %s", errb.String())
		}
	})
	store := e5t1Store(t, configPath)
	defer store.Close()
	// Force the alpha lane's child into the dead-lettered shape the
	// discard exit owns, then close it through the operator surface.
	var alpha string
	if err := store.QueryRow(`SELECT dispatch_id FROM child_dispatches WHERE destination_id = 'main'`).Scan(&alpha); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`UPDATE dispatch_intents SET state = 'dead_lettered' WHERE dispatch_id = ?`, alpha); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "discard", "--config", configPath, alpha, "--reason", "sibling lane resolved"}, &out, &errb); code != 0 {
		t.Fatalf("discard the sibling's dead letter: %s", errb.String())
	}
	// The closed lane released its slot and went IDLE; the sibling lane
	// keeps its slot and active child.
	var alphaState, alphaActive string
	if err := store.QueryRow(`SELECT lane_state, COALESCE(active_dispatch_id, '') FROM destination_lane_state WHERE route_id = 'wiki' AND destination_id = 'main'`).Scan(&alphaState, &alphaActive); err != nil || alphaState != "IDLE" || alphaActive != "" {
		t.Fatalf("the closed lane must be idle with a free slot: %q %q %v", alphaState, alphaActive, err)
	}
	var reviewState, reviewActive string
	if err := store.QueryRow(`SELECT lane_state, COALESCE(active_dispatch_id, '') FROM destination_lane_state WHERE route_id = 'wiki' AND destination_id = 'review'`).Scan(&reviewState, &reviewActive); err != nil || reviewState != "ACTIVE_CLEAN" || reviewActive == "" || reviewActive == alpha {
		t.Fatalf("the sibling lane must keep its own active child: %q %q %v", reviewState, reviewActive, err)
	}
	// The sibling's dispatch leases beside the closed letter (the lane
	// predicate admits it), proving the sibling lane never blocked.
	if _, err := store.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: reviewActive, AttemptID: "attempt-e12t2-sibling", Owner: "sibling-lane",
		Now: "2026-08-29T01:00:00Z", LeaseExpiresAt: "2026-08-29T01:01:00Z",
	}); err != nil {
		t.Fatalf("the sibling lane's dispatch must lease: %v", err)
	}
}

// TestE12T2DestinationEditPausesSubmissionUntilReacknowledged pins
// CON-010 at the acknowledged-revision gate: editing a destination's
// behavior (its workstream) changes its DestinationRevision and — because
// the sorted destination set joins the route revision projection
// (E11-T1) — the route revision, so the new arrival's child key differs
// and the submission pauses with the acknowledged-revision conflict until
// `route enable` re-acknowledges the computed revision.
func TestE12T2DestinationEditPausesSubmissionUntilReacknowledged(t *testing.T) {
	configPath, vault := e12t2TwoDestinationFixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	cfg, _ := config.Load(configPath)
	revBefore, _ := config.RouteRevision(cfg, "wiki")
	route := cfg.Routes["wiki"]
	dest, _ := route.DestinationByID("main")
	dstBefore := config.DestinationRevision(cfg, route, dest)

	var out, errb bytes.Buffer
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", revBefore, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("enable: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[{"name":"Inbox/before.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb); code != 0 {
			t.Fatalf("dispatch under the acknowledged revision: %s", errb.String())
		}
	})
	// Submit the second lane's child (the dispatch command submits the
	// canonically-first child; the sibling stays ready for the drain), so
	// no unresolved work remains under the pre-edit revision.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--config", configPath, "--route", "wiki"}, &out, &errb); code != 0 {
		t.Fatalf("drain the sibling child: %s", errb.String())
	}
	// Complete both lanes' children so the edited arrival can activate:
	// per-lane coordination (CON-007) completes each child independently.
	firstStore := e5t1Store(t, configPath)
	firstRows, err := firstStore.Query(`SELECT d.dispatch_id FROM dispatch_intents d JOIN child_dispatches c ON c.dispatch_id = d.dispatch_id ORDER BY c.destination_id`)
	if err != nil {
		t.Fatal(err)
	}
	var firstDispatches []string
	for firstRows.Next() {
		var id string
		if err := firstRows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		firstDispatches = append(firstDispatches, id)
	}
	firstRows.Close()
	firstStore.Close()
	for _, id := range firstDispatches {
		out.Reset()
		errb.Reset()
		if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", id, "--run-id", "run-e12t2"}, &out, &errb); code != 0 {
			t.Fatalf("work begin %s: %s", id, errb.String())
		}
		out.Reset()
		errb.Reset()
		withStdin(t, `[]`, func() {
			if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", id, "--run-id", "run-e12t2", "--manifest", "-"}, &out, &errb); code != 0 {
				t.Fatalf("work complete %s: %s", id, errb.String())
			}
		})
	}

	// The behavior edit: the main lane's workstream changes.
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), "        workstream: main\n", "        workstream: curated\n", 1)
	if edited == string(raw) {
		t.Fatal("the fixture no longer carries the main workstream line")
	}
	if err := os.WriteFile(configPath, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	editedCfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	revAfter, _ := config.RouteRevision(editedCfg, "wiki")
	editedRoute := editedCfg.Routes["wiki"]
	editedDest, _ := editedRoute.DestinationByID("main")
	dstAfter := config.DestinationRevision(editedCfg, editedRoute, editedDest)
	if revAfter == revBefore || dstAfter == dstBefore {
		t.Fatalf("a destination behavior edit must move both revisions: route %s→%s destination %s→%s", revBefore, revAfter, dstBefore, dstAfter)
	}
	// The DAT-014 child key changes with the destination revision.
	keyBefore, err := fingerprint.ChildIdempotency(records.ChildIdempotencyKeyInput{
		RouteID: "wiki", RouteRevision: revBefore, DestinationID: "main", DestinationRevision: dstBefore,
		Workstream: "main", TargetScope: "agent-dispatch", Generation: 1,
		ContentFingerprint: "sha256:" + strings.Repeat("a", 64), RequestVersion: "agent-dispatch.hermes-task/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	keyAfter, err := fingerprint.ChildIdempotency(records.ChildIdempotencyKeyInput{
		RouteID: "wiki", RouteRevision: revAfter, DestinationID: "main", DestinationRevision: dstAfter,
		Workstream: "curated", TargetScope: "agent-dispatch", Generation: 1,
		ContentFingerprint: "sha256:" + strings.Repeat("a", 64), RequestVersion: "agent-dispatch.hermes-task/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if keyBefore == keyAfter {
		t.Fatal("an edited destination revision must change the child key (CON-010)")
	}

	// The arrival under the edited revision persists under the new
	// revision's child keys, and its submission pauses on the
	// acknowledged-revision gate (E8-T3, H-2): the operator must
	// re-acknowledge the computed revision.
	out.Reset()
	errb.Reset()
	setPlanEnv(t, vault, false)
	os.WriteFile(filepath.Join(vault, "Inbox", "after.md"), []byte("edited behavior"), 0o644)
	withStdin(t, `[{"name":"Inbox/after.md","exists":true,"new":true,"size":8,"type":"f"}]`, func() {
		code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
		if code != 14 {
			t.Fatalf("submission under the edited revision must pause at 14, got %d: %s", code, errb.String())
		}
	})
	if !strings.Contains(errb.String(), "re-acknowledge") && !strings.Contains(errb.String(), "acknowledged revision") {
		t.Fatalf("the pause must name the acknowledged-revision gate: %s", errb.String())
	}
	editedStore := e5t1Store(t, configPath)
	var editedChildren int
	if err := editedStore.QueryRow(`SELECT COUNT(*) FROM dispatch_intents d JOIN child_dispatches c ON c.dispatch_id = d.dispatch_id
		WHERE d.route_revision = ? AND c.destination_id = 'main'`, revAfter).Scan(&editedChildren); err != nil || editedChildren != 1 {
		editedStore.Close()
		t.Fatalf("the edited arrival must plan its children under the edited route revision: %d %v", editedChildren, err)
	}
	editedStore.Close()
	// Re-acknowledgement lifts the pause: the paused children submit.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", revAfter, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("re-acknowledge the edited revision: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--config", configPath, "--route", "wiki"}, &out, &errb); code != 0 {
		t.Fatalf("drain after re-acknowledgement: %s", errb.String())
	}
}

// e12t2ConditionedFixture writes the e4t3 fixture with two conditioned
// destinations on the shared target: the alpha lane selects alpha/**
// paths, the beta lane beta/** paths (FAN-004/FAN-005 at the dispatch
// surface).
func e12t2ConditionedFixture(t *testing.T) (string, string) {
	t.Helper()
	configPath, vault := e4t3Fixture(t)
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
		"      - id: alpha\n        target: hermes-main\n        profile: wiki-maintainer\n        skills: [llm-wiki]\n        workstream: indexing\n        execution_hints:\n          max_runtime: 30m\n          max_attempts: 2\n        conditions:\n          path_include: [\"alpha/**\"]\n" +
		"      - id: beta\n        target: hermes-main\n        profile: wiki-maintainer\n        skills: [llm-wiki]\n        workstream: review\n        execution_hints:\n          max_runtime: 30m\n          max_attempts: 2\n        conditions:\n          path_include: [\"beta/**\"]"
	updated = updated[:start] + dest + updated[end:]
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath, vault
}

// e12t2Enable enables the fixture route under its computed revision.
func e12t2Enable(t *testing.T, configPath string) {
	t.Helper()
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	rev, _ := config.RouteRevision(cfg, "wiki")
	var out, errb bytes.Buffer
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", rev, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("enable: %s", errb.String())
	}
}

// TestE12T2ConditionedArrivalSelectsOneLane pins the condition wiring at
// the dispatch surface (E12-T2, FAN-004/FAN-005): an occurrence touching
// only alpha/** selects exactly the alpha lane — one child, the evaluated
// selection reason recorded on the aggregate's summary — and the beta lane
// is never materialized.
func TestE12T2ConditionedArrivalSelectsOneLane(t *testing.T) {
	configPath, vault := e12t2ConditionedFixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	e12t2Enable(t, configPath)
	os.MkdirAll(filepath.Join(vault, "alpha"), 0o755)
	os.WriteFile(filepath.Join(vault, "alpha", "only.md"), []byte("alpha work"), 0o644)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"alpha/only.md","exists":true,"new":true,"size":10,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb); code != 0 {
			t.Fatalf("dispatch: %s", errb.String())
		}
	})
	res := decodeEnvelope(t, &out)
	fanout, _ := res["fanout"].([]any)
	if len(fanout) != 1 {
		t.Fatalf("only the alpha lane may be selected: %v", res)
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var children, lanes int
	if err := store.QueryRow(`SELECT COUNT(*) FROM child_dispatches`).Scan(&children); err != nil || children != 1 {
		t.Fatalf("exactly one child: %d %v", children, err)
	}
	var destination, workstream string
	if err := store.QueryRow(`SELECT destination_id, workstream FROM child_dispatches`).Scan(&destination, &workstream); err != nil || destination != "alpha" {
		t.Fatalf("the child must belong to the alpha lane: %q %v", destination, err)
	}
	agg, err := store.LoadAggregateEvent(context.Background(), func() string {
		var id string
		if err := store.QueryRow(`SELECT aggregate_id FROM child_dispatches`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(agg.SelectionJSON, `"alpha"`) || strings.Contains(agg.SelectionJSON, `"beta"`) {
		t.Fatalf("the selection summary must record only the alpha lane: %s", agg.SelectionJSON)
	}
	if !strings.Contains(agg.SelectionJSON, "conditions:") {
		t.Fatalf("the selection reason must record the evaluated conditions outcome: %s", agg.SelectionJSON)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM destination_lane_state WHERE route_id = 'wiki' AND destination_id = 'beta'`).Scan(&lanes); err != nil || lanes != 0 {
		t.Fatalf("the unselected beta lane must not materialize: %d %v", lanes, err)
	}
}

// TestE12T2NoDestinationSelectedFailsClosed pins the fail-closed
// no-selection path (FAN-005): an occurrence every destination's
// conditions refuse creates NO aggregate, NO child, and NO intent — the
// arrival fails closed with the configuration class naming the rule.
func TestE12T2NoDestinationSelectedFailsClosed(t *testing.T) {
	configPath, vault := e12t2ConditionedFixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	e12t2Enable(t, configPath)
	os.MkdirAll(filepath.Join(vault, "other"), 0o755)
	os.WriteFile(filepath.Join(vault, "other", "unselected.md"), []byte("no lane"), 0o644)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"other/unselected.md","exists":true,"new":true,"size":7,"type":"f"}]`, func() {
		code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
		if code != 3 {
			t.Fatalf("an occurrence no destination selects must fail closed at 3, got %d: %s", code, errb.String())
		}
	})
	if !strings.Contains(errb.String(), "selected no destination") {
		t.Fatalf("the refusal must name the no-selection rule: %s", errb.String())
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	for _, table := range []string{"aggregate_events", "child_dispatches", "dispatch_intents"} {
		var n int
		if err := store.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s must stay empty after the refusal: %d %v", table, n, err)
		}
	}
}

// TestE12T2DifferingTargetsRefused pins FAN-011's fail-closed bound: a
// route whose destinations reference DIFFERENT targets refuses at the
// configuration class naming both targets — one shared Hermes Kanban
// submission surface per route.
func TestE12T2DifferingTargetsRefused(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := string(raw)
	// A second hermes target beside the fixture's own.
	marker := "routes:\n"
	secondTarget := "  hermes-secondary:\n    board: agent-dispatch-test\n    minimum_version: 0.19.1\n    compatibility: capability_probe\n    executable: " + stubExeOf(t, configPath) + "\n    submit_timeout: 30s\n    lookup_timeout: 30s\n    environment_allowlist: [PATH, HOME]\n"
	if i := strings.Index(updated, marker); i < 0 {
		t.Fatal("fixture no longer carries the routes block")
	} else {
		updated = updated[:i] + secondTarget + updated[i:]
	}
	start := strings.Index(updated, "    fanout_mode: all\n    destinations:\n")
	end := strings.Index(updated, "\n    submission_retry:")
	if start < 0 || end <= start {
		t.Fatal("fixture no longer carries the single-destination block")
	}
	dest := "    fanout_mode: all\n    destinations:\n" +
		"      - id: main\n        target: hermes-main\n        profile: wiki-maintainer\n        skills: [llm-wiki]\n        workstream: main\n        execution_hints:\n          max_runtime: 30m\n          max_attempts: 2\n" +
		"      - id: review\n        target: hermes-secondary\n        profile: wiki-maintainer\n        skills: [llm-wiki]\n        workstream: review\n        execution_hints:\n          max_runtime: 30m\n          max_attempts: 2"
	updated = updated[:start] + dest + updated[end:]
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, vault, false)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/split.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
		if code != 3 {
			t.Fatalf("differing destination targets must refuse at 3, got %d: %s", code, errb.String())
		}
	})
	if !strings.Contains(errb.String(), "hermes-main") || !strings.Contains(errb.String(), "hermes-secondary") {
		t.Fatalf("the refusal must name both targets: %s", errb.String())
	}
}

// TestE12T2LaneScopedFollowupManifest pins CON-008 at the follow-up
// boundary: while both conditioned lanes are active, an alpha-only burst
// merges only the alpha lane (the beta lane's dirty generation is
// untouched), and the alpha lane's completion projects a follow-up whose
// manifest carries ONLY the paths its destination selects — the beta
// lane's conditioned-out work never rides along — with the beta lane's
// own later follow-up carrying exactly the beta paths.
func TestE12T2LaneScopedFollowupManifest(t *testing.T) {
	configPath, vault := e12t2ConditionedFixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	e12t2Enable(t, configPath)
	for _, dir := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(vault, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Arrival 1 touches both lanes: both children activate and submit.
	os.WriteFile(filepath.Join(vault, "alpha", "a.md"), []byte("alpha"), 0o644)
	os.WriteFile(filepath.Join(vault, "beta", "b.md"), []byte("beta"), 0o644)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"alpha/a.md","exists":true,"new":true,"size":5,"type":"f"},{"name":"beta/b.md","exists":true,"new":true,"size":4,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb); code != 0 {
			t.Fatalf("dispatch: %s", errb.String())
		}
	})
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--config", configPath, "--route", "wiki"}, &out, &errb); code != 0 {
		t.Fatalf("drain both children: %s", errb.String())
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	childOf := func(lane string) string {
		var id string
		if err := store.QueryRow(`SELECT dispatch_id FROM child_dispatches WHERE destination_id = ?`, lane).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// Bursts while both lanes are active: an alpha-only change (merges only
	// the alpha lane) followed by a beta-only change (merges only beta).
	os.WriteFile(filepath.Join(vault, "alpha", "x.md"), []byte("alpha later"), 0o644)
	withStdin(t, `[{"name":"alpha/x.md","exists":true,"new":true,"size":11,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &bytes.Buffer{}, &bytes.Buffer{})
	})
	os.WriteFile(filepath.Join(vault, "beta", "y.md"), []byte("beta later"), 0o644)
	withStdin(t, `[{"name":"beta/y.md","exists":true,"new":true,"size":10,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &bytes.Buffer{}, &bytes.Buffer{})
	})
	laneDirty := func(lane string) int {
		var dirty int
		if err := store.QueryRow(`SELECT dirty_generation FROM destination_lane_state WHERE route_id = 'wiki' AND destination_id = ?`, lane).Scan(&dirty); err != nil {
			t.Fatal(err)
		}
		return dirty
	}
	if laneDirty("alpha") != 1 || laneDirty("beta") != 1 {
		t.Fatalf("each lane must hold exactly its own burst: alpha=%d beta=%d", laneDirty("alpha"), laneDirty("beta"))
	}

	// The alpha lane's completion: its follow-up manifest must carry ONLY
	// the alpha path (the route-scoped window holds beta/y.md too).
	completeLane := func(lane string) string {
		id := childOf(lane)
		out.Reset()
		errb.Reset()
		if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", id, "--run-id", "run-" + lane}, &out, &errb); code != 0 {
			t.Fatalf("work begin %s: %s", lane, errb.String())
		}
		out.Reset()
		errb.Reset()
		withStdin(t, `[]`, func() {
			if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", id, "--run-id", "run-" + lane, "--manifest", "-"}, &out, &errb); code != 0 {
				t.Fatalf("work complete %s: %s", lane, errb.String())
			}
		})
		res := decodeEnvelope(t, &out)
		followup, _ := res["followup_dispatch_id"].(string)
		if followup == "" {
			t.Fatalf("the dirty %s lane must schedule a follow-up: %v", lane, res)
		}
		return followup
	}
	manifestPaths := func(dispatchID string) []string {
		var requestJSON string
		if err := store.QueryRow(`SELECT request_json FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&requestJSON); err != nil {
			t.Fatal(err)
		}
		var req ports.TaskRequest
		if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
			t.Fatal(err)
		}
		paths := make([]string, 0, len(req.Activation.Manifest))
		for _, m := range req.Activation.Manifest {
			paths = append(paths, m.Path)
		}
		return paths
	}
	alphaFollowup := completeLane("alpha")
	if paths := manifestPaths(alphaFollowup); len(paths) != 1 || !strings.HasPrefix(paths[0], "alpha/") {
		t.Fatalf("the alpha follow-up must carry only the alpha lane's work, got %v", paths)
	}
	// The beta lane's dirty generation is untouched by the alpha completion.
	if laneDirty("beta") != 1 {
		t.Fatalf("the beta lane's dirty generation must be untouched: %d", laneDirty("beta"))
	}
	betaFollowup := completeLane("beta")
	if paths := manifestPaths(betaFollowup); len(paths) != 1 || !strings.HasPrefix(paths[0], "beta/") {
		t.Fatalf("the beta follow-up must carry only the beta lane's work, got %v", paths)
	}
}

// TestE12T2FanoutFailureReportedWhileSiblingActivates pins the failure
// surfacing (E12-T2, review round 1 product finding): a lane whose child
// commit fails beside a successful sibling never blocks it — the envelope
// reports the failed lane with bounded error text while the sibling's
// child activated. The failure is injected deterministically: the review
// lane's DAT-014 child key for the occurrence is computed with the
// product's own derivation and pre-seeded on a blocking intent row, so
// only that lane's child commit collides.
func TestE12T2FanoutFailureReportedWhileSiblingActivates(t *testing.T) {
	configPath, vault := e12t2TwoDestinationFixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	e12t2Enable(t, configPath)
	os.WriteFile(filepath.Join(vault, "Inbox", "dup.md"), []byte("hello"), 0o644)

	// Compute the review lane's child key exactly as the arrival will
	// (DAT-014 over the known occurrence facts).
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	route := cfg.Routes["wiki"]
	routeRevision, _ := config.RouteRevision(cfg, "wiki")
	reviewDest, _ := route.DestinationByID("review")
	sum := sha256.Sum256([]byte("hello"))
	contentDigest := "sha256:" + hex.EncodeToString(sum[:])
	occurrence, err := fingerprint.Content(records.ContentFingerprintInput{
		Changes:    []records.FingerprintChange{{Path: "Inbox/dup.md", Operation: "create", ExistsAfter: true, AfterDigest: contentDigest}},
		ResourceID: "vault-main",
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewKey, err := fingerprint.ChildIdempotency(records.ChildIdempotencyKeyInput{
		RouteID: "wiki", RouteRevision: routeRevision,
		DestinationID: "review", DestinationRevision: config.DestinationRevision(cfg, route, reviewDest),
		Workstream: reviewDest.Workstream, TargetScope: "agent-dispatch-test",
		Generation: 1, ContentFingerprint: string(occurrence),
		RequestVersion: ports.TaskRequestContractVersion,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Pre-seed the blocking intent: the review lane's child commit collides
	// on (target, idempotency key) while the main lane's key is free.
	store := e5t1Store(t, configPath)
	defer store.Close()
	if _, err := store.Exec(`INSERT INTO policy_decisions
		(decision_id, route_id, route_revision, policy_revision, generation_lineage_json, disposition, classification, reason_codes_json, created_at, actor)
		VALUES ('decision-blocking', 'wiki', ?, 'policy-x', '{"generations":[]}', 'dispatch', 'normal', '[]', '2026-08-29T00:00:00Z', 'test')`, routeRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`INSERT INTO dispatch_intents
		(dispatch_id, decision_id, route_id, route_revision, target_id, target_type, resource_id, generation, idempotency_key, content_fingerprint, manifest_digest, request_version, request_json, state, created_at, updated_at, base_batch_seq)
		VALUES ('dispatch-blocking', 'decision-blocking', 'wiki', ?, 'hermes-main', 'hermes_kanban', 'vault-main', 9, ?, 'sha256:x', 'sha256:y',
		'agent-dispatch.hermes-task/v1', '{}', 'superseded', '2026-08-29T00:00:00Z', '2026-08-29T00:00:00Z', 0)`, routeRevision, reviewKey); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/dup.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
		if code != 0 {
			t.Fatalf("the occurrence must succeed beside the failed lane, got %d: %s", code, errb.String())
		}
	})
	res := decodeEnvelope(t, &out)
	fanout, _ := res["fanout"].([]any)
	if len(fanout) != 1 {
		t.Fatalf("the main sibling must activate: %v", res)
	}
	failed, _ := res["failed_lanes"].([]any)
	if len(failed) != 1 {
		t.Fatalf("the review lane's failure must be reported: %v", res)
	}
	failure, _ := failed[0].(map[string]any)
	if failure["destination_id"] != "review" || failure["error"] == "" {
		t.Fatalf("the failure entry must name the lane and its bounded error: %v", failure)
	}
	if errText, _ := failure["error"].(string); strings.ContainsAny(errText, "\n\r") || len(errText) > 400 {
		t.Fatalf("the failure text must be bounded and envelope-safe: %q", errText)
	}
	// The main child exists and holds its lane; the review lane gained no
	// child beside the blocking row.
	var children int
	if err := store.QueryRow(`SELECT COUNT(*) FROM child_dispatches WHERE destination_id = 'main'`).Scan(&children); err != nil || children != 1 {
		t.Fatalf("exactly one main child: %d %v", children, err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM child_dispatches WHERE destination_id = 'review'`).Scan(&children); err != nil || children != 0 {
		t.Fatalf("the failed review lane must create no child: %d %v", children, err)
	}
	var laneState, laneActive string
	if err := store.QueryRow(`SELECT lane_state, COALESCE(active_dispatch_id, '') FROM destination_lane_state WHERE route_id = 'wiki' AND destination_id = 'main'`).Scan(&laneState, &laneActive); err != nil || laneState != "ACTIVE_CLEAN" || laneActive == "" {
		t.Fatalf("the main lane must hold its activated child: %q %q %v", laneState, laneActive, err)
	}
}

// TestE12T2RetryIsolationForFanoutChildren pins CON-009 at the fan-out
// boundary: one lane's dead-lettered child retries with its idempotency
// key unchanged while the sibling stays accepted — exactly one child per
// lane and exactly one Hermes task per lane (the stub deduplicates the
// retry by key).
func TestE12T2RetryIsolationForFanoutChildren(t *testing.T) {
	configPath, vault := e12t2TwoDestinationFixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	e12t2Enable(t, configPath)
	os.WriteFile(filepath.Join(vault, "Inbox", "retry.md"), []byte("retry me"), 0o644)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/retry.md","exists":true,"new":true,"size":7,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb); code != 0 {
			t.Fatalf("dispatch: %s", errb.String())
		}
	})
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--config", configPath, "--route", "wiki"}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	stubTaskCount := func() int {
		exe := stubExeOf(t, configPath)
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "state", "count"))
		if err != nil {
			return 0
		}
		n, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		return n
	}
	if n := stubTaskCount(); n != 2 {
		t.Fatalf("one Hermes task per lane before the retry: %d", n)
	}
	var reviewChild, reviewKey string
	if err := store.QueryRow(`SELECT dispatch_id, idempotency_key FROM child_dispatches WHERE destination_id = 'review'`).Scan(&reviewChild, &reviewKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`UPDATE dispatch_intents SET state = 'dead_lettered' WHERE dispatch_id = ?`, reviewChild); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "retry", "--config", configPath, reviewChild, "--reason", "lane-local resolution"}, &out, &errb); code != 0 {
		t.Fatalf("retry the review lane's dead letter: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--config", configPath, "--route", "wiki"}, &out, &errb); code != 0 {
		t.Fatalf("drain after the retry: %s", errb.String())
	}
	// The retried child keeps exactly its key; no sibling duplication.
	var afterKey string
	if err := store.QueryRow(`SELECT idempotency_key FROM child_dispatches WHERE dispatch_id = ?`, reviewChild).Scan(&afterKey); err != nil || afterKey != reviewKey {
		t.Fatalf("the retry must preserve the child key: %q want %q (%v)", afterKey, reviewKey, err)
	}
	for _, lane := range []string{"main", "review"} {
		var n int
		if err := store.QueryRow(`SELECT COUNT(*) FROM child_dispatches WHERE destination_id = ?`, lane).Scan(&n); err != nil || n != 1 {
			t.Fatalf("exactly one child on lane %s: %d %v", lane, n, err)
		}
	}
	var reviewState, mainState string
	if err := store.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, reviewChild).Scan(&reviewState); err != nil {
		t.Fatal(err)
	}
	if err := store.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = (SELECT dispatch_id FROM child_dispatches WHERE destination_id = 'main')`).Scan(&mainState); err != nil {
		t.Fatal(err)
	}
	if reviewState != "accepted" || mainState != "accepted" {
		t.Fatalf("both lanes' children must be accepted after the retry: review=%s main=%s", reviewState, mainState)
	}
	// The stub deduplicated the retry by key: still exactly one task per
	// lane (CON-009).
	if n := stubTaskCount(); n != 2 {
		t.Fatalf("the retry must not create a second Hermes task: %d", n)
	}
}

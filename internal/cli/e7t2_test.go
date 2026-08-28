package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E7-T2 regression suite: the compliance review's three Blockers proven
// through the CLI product path — expired-submitting recovery inside the
// drain command (B-1), rerun supersession and the single authoritative
// task (B-2), and the follow-up lifecycle closed by acceptance-time
// activation (B-3, exercised end to end by the rewritten G4/E5 suites).

// e7t2NoSubmitDispatch persists one arrival without submitting it and
// returns the ready dispatch id.
func e7t2NoSubmitDispatch(t *testing.T, configPath, vault string) string {
	t.Helper()
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("no-submit dispatch: %s", errb.String())
	}
	id, _ := decodeEnvelope(t, &out)["dispatch_id"].(string)
	if id == "" {
		t.Fatal("no-submit dispatch produced no dispatch id")
	}
	return id
}

// TestDrainRecoversExpiredSubmittingWithoutManualEdits proves B-1 at the
// command level: a submitter that died after committing its attempt
// lease (here: an already-expired lease in the submitting state) is
// recovered and reconciled by `dispatches drain` alone — no manual
// database edit, and the previously wedged route heals.
func TestDrainRecoversExpiredSubmittingWithoutManualEdits(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	dispatchID := e7t2NoSubmitDispatch(t, configPath, vault)

	// The dead submitter: the lease committed long ago and expired; the
	// intent is wedged in submitting exactly as a mid-submit process
	// death leaves it.
	store := e5t1Store(t, configPath)
	if _, err := store.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: dispatchID, AttemptID: "attempt-victim", Owner: "victim",
		Now: "2026-08-20T00:00:00Z", LeaseExpiresAt: "2026-08-20T00:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	recovered, _ := res["recovered"].([]any)
	if len(recovered) != 1 {
		t.Fatalf("the drain must report the recovered expired lease: %v", res)
	}
	healed := e5t1Store(t, configPath)
	defer healed.Close()
	var state string
	if err := healed.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&state); err != nil || state == "submitting" {
		t.Fatalf("the wedged intent must leave submitting through the drain alone: %q %v", state, err)
	}
}

// TestRerunSupersedesReadyLeavingOneAuthoritativeTask proves B-2 at the
// command level: rerunning ready work supersedes the original, the route
// ends with exactly one submittable dispatch, and drain submits exactly
// one task — never two authoritative Hermes tasks per route.
func TestRerunSupersedesReadyLeavingOneAuthoritativeTask(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	dispatchID := e7t2NoSubmitDispatch(t, configPath, vault)

	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "rerun", "--config", configPath, "--yes", "--reason", "operator redo", dispatchID}, &out, &errb); code != 0 {
		t.Fatalf("rerun: %s", errb.String())
	}
	rerun := decodeEnvelope(t, &out)
	newID, _ := rerun["dispatch_id"].(string)
	if newID == "" || newID == dispatchID {
		t.Fatalf("rerun must create a new dispatch: %v", rerun)
	}
	// The original moved to superseded; exactly one live dispatch remains.
	store := e5t1Store(t, configPath)
	defer store.Close()
	var original string
	if err := store.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&original); err != nil || original != "superseded" {
		t.Fatalf("the rerun original must be superseded: %q %v", original, err)
	}
	// Drain submits exactly one task: the slot-holding rerun.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath, "--max", "5"}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	drained := decodeEnvelope(t, &out)
	if n, _ := drained["processed"].(float64); n != 1 {
		t.Fatalf("exactly one authoritative task may be submitted: %v", drained)
	}
	if !strings.Contains(out.String(), `"accepted"`) {
		t.Fatalf("the rerun must reach acceptance: %s", out.String())
	}
}

// TestScheduledReconcileSubmitsDueFollowup proves B-3's liveness half on
// the scheduled path: a pending follow-up generation reaches the target
// through `reconcile --reason scheduled --submit` alone — the operator
// never needs a manual drain between generations (CON-003).
func TestScheduledReconcileSubmitsDueFollowup(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	first := e7t2NoSubmitDispatch(t, configPath, vault)

	// Submit, begin, burst, and complete without receipt coverage: the
	// route ends in FOLLOWUP_READY with exactly one due follow-up.
	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", first, "--run-id", "r1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	setPlanEnv(t, vault, false)
	withStdin(t, `[{"name":"Inbox/during.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", first, "--run-id", "r1", "--manifest", "-"}, &out, &errb)
	})
	var followup string
	store := e5t1Store(t, configPath)
	if err := store.QueryRow(`SELECT dispatch_id FROM dispatch_intents WHERE state = 'ready'`).Scan(&followup); err != nil || followup == "" {
		t.Fatalf("the completion must leave one due follow-up: %q %v", followup, err)
	}
	store.Close()

	// The scheduled path alone delivers and activates it.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "scheduled", "--submit"}, &out, &errb); code != 0 {
		t.Fatalf("scheduled reconcile --submit: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	drained, _ := res["drained"].(map[string]any)
	if n, _ := drained["processed"].(float64); n != 1 {
		t.Fatalf("the scheduled path must submit the due follow-up: %v", res)
	}
	healed := e5t1Store(t, configPath)
	defer healed.Close()
	var state, routeState string
	if err := healed.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, followup).Scan(&state); err != nil || state != "accepted" {
		t.Fatalf("the follow-up must be accepted by the scheduled path: %q %v", state, err)
	}
	if err := healed.QueryRow(`SELECT lane_state FROM destination_lane_state WHERE route_id = 'wiki'`).Scan(&routeState); err != nil || routeState != "ACTIVE_CLEAN" {
		t.Fatalf("the accepted follow-up must be active (B-3): %q %v", routeState, err)
	}
}

package cli

import (
	"bytes"
	"strings"
	"testing"
)

// E7-T6 regression suite: the write gates, audit rows, and decision
// records (M-1, M-2, M-3, M-18, M-19).

// TestDisabledRouteNeverAutoSubmits proves M-1/M-2: with the store
// activation disabled (or the YAML key off), drain submits nothing and
// the two-key gate refuses enablement while the YAML key is off.
func TestDisabledRouteNeverAutoSubmits(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	// Close the YAML key: the two-key gate must hold.
	e5t4Rewrite(t, configPath, "enabled: true", "enabled: false")

	// The YAML key alone must not enable: route enable refuses while
	// enabled: false stands, so the store key cannot be turned.
	rev, _ := routeRevisionOf(t, configPath)
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", rev, "--yes"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 3 {
		t.Fatalf("route enable must refuse while the YAML key is disabled, got %d", code)
	}

	// A ready intent on the disabled route: drain refuses to submit it
	// (the recovery sweep still runs and the intent stays inspectable).
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	id, _ := decodeEnvelope(t, &out)["dispatch_id"].(string)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain on a disabled route must succeed without submitting: %s", errb.String())
	}
	if strings.Contains(out.String(), `"submitted"`) || strings.Contains(out.String(), `"accepted"`) {
		t.Fatalf("a disabled route must never submit automatically: %s", out.String())
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var state string
	if err := store.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, id).Scan(&state); err != nil || state != "ready" {
		t.Fatalf("the ready intent must stay ready on the disabled route: %q %v", state, err)
	}
}

// TestRouteTransitionsFullyAudited proves M-3: the route timeline is
// reconstructable from state_transitions alone.
func TestRouteTransitionsFullyAudited(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	id := e7t5AcceptedDispatch(t, configPath, vault)
	store := e5t1Store(t, configPath)
	defer store.Close()
	// The intent's creation row and the route's IDLE -> ACTIVE_CLEAN
	// acceptance row both exist with context.
	var created int
	if err := store.QueryRow(`SELECT COUNT(*) FROM state_transitions WHERE entity_type = 'dispatch_intent' AND entity_id = ? AND to_state = 'ready' AND context_json LIKE '%arrival%'`, id).Scan(&created); err != nil || created != 1 {
		t.Fatalf("the intent creation must be audited: %d %v", created, err)
	}
	var routeAccepted int
	if err := store.QueryRow(`SELECT COUNT(*) FROM state_transitions WHERE entity_type = 'route' AND entity_id = 'wiki' AND to_state = 'ACTIVE_CLEAN' AND context_json LIKE '%dispatch_accepted%'`).Scan(&routeAccepted); err != nil || routeAccepted < 1 {
		t.Fatalf("the route acceptance transition must be audited: %d %v", routeAccepted, err)
	}
}

// TestMergePendingPersistedOnDecision proves M-18: a burst merged onto
// an active dispatch records merge_pending on its durable decision.
func TestMergePendingPersistedOnDecision(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/first.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	// A second burst while the route is active merges.
	out.Reset()
	errb.Reset()
	withStdin(t, `[{"name":"Inbox/second.md","exists":true,"new":true,"size":6,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if res := decodeEnvelope(t, &out); res["disposition"] != "merge_pending" {
		t.Fatalf("the second burst must merge, got %v", res["disposition"])
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var merged int
	if err := store.QueryRow(`SELECT COUNT(*) FROM policy_decisions WHERE disposition = 'merge_pending'`).Scan(&merged); err != nil || merged < 1 {
		t.Fatalf("the merged burst must persist merge_pending on its decision: %d %v", merged, err)
	}
}

// TestStoreActivationGateBlocksDrain proves the store-key half of the
// write gate (M-1): with the YAML key on but the store activation
// disabled, drain never submits.
func TestStoreActivationGateBlocksDrain(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	id, _ := decodeEnvelope(t, &out)["dispatch_id"].(string)
	if id == "" {
		t.Fatal("no-submit dispatch produced no id")
	}
	if code := Run([]string{"route", "disable", "--config", configPath, "--route", "wiki"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("route disable failed")
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	if res := decodeEnvelope(t, &out); res["processed"] != float64(0) {
		t.Fatalf("a store-disabled route must not submit: %v", res["processed"])
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var state string
	if err := store.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, id).Scan(&state); err != nil || state != "ready" {
		t.Fatalf("the intent must stay ready: %q %v", state, err)
	}
}

// TestReprocessMatchesPlannerDisposition proves the reprocess parity
// (M-19): a retained batch reprocessed under the active policy records
// the disposition the planner itself would produce, including the
// hard-limit bulk action.
func TestReprocessMatchesPlannerDisposition(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	// One retained batch from a no-submit arrival.
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/a.md","exists":true,"new":true,"size":5,"type":"f"},{"name":"Inbox/b.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	store := e5t1Store(t, configPath)
	var batchID string
	if err := store.QueryRow(`SELECT batch_id FROM change_batches ORDER BY created_at DESC LIMIT 1`).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	store.Close()
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "reprocess", "--config", configPath, batchID}, &out, &errb); code != 0 {
		t.Fatalf("reprocess: %s", errb.String())
	}
	if res := decodeEnvelope(t, &out); res["disposition"] != "dispatch" {
		t.Fatalf("a normal batch must reprocess to dispatch: %v", res["disposition"])
	}
	// Shrink the hard limit so the same batch crosses it: the planner's
	// bulk action (quarantine) must be what reprocess records.
	e5t4Rewrite(t, configPath, "hard_limit: 100", "hard_limit: 1")
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "reprocess", "--config", configPath, batchID}, &out, &errb); code != 0 {
		t.Fatalf("reprocess over limit: %s", errb.String())
	}
	if res := decodeEnvelope(t, &out); res["disposition"] != "quarantine" {
		t.Fatalf("an over-limit batch must record the planner bulk action (quarantine): %v", res["disposition"])
	}
}

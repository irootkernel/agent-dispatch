package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// TestE9T4LeaseTTLWiredAtEverySubmitSite pins the T2-F003/F004 wiring:
// the one shared submit-runtime constructor derives the attempt lease
// TTL from the target's submit timeout for every submit surface, so no
// site can drift from the E8-T2/M-1 derivation.
func TestE9T4LeaseTTLWiredAtEverySubmitSite(t *testing.T) {
	for _, actor := range []string{"dispatch", "drain", "reconcile"} {
		rt := newSubmitRuntime(nil, nil, &config.Config{}, config.Target{SubmitTimeout: "45s"}, dispatch.Backoff{}, actor, io.Discard)
		if rt.LeaseTTL != 75*time.Second {
			t.Fatalf("%s: a 45s submit timeout must lease for 75s, got %v", actor, rt.LeaseTTL)
		}
		if rt.Actor != actor {
			t.Fatalf("%s: the actor must ride through the shared constructor, got %q", actor, rt.Actor)
		}
	}
	for _, tc := range []struct {
		submitTimeout string
		want          time.Duration
	}{
		{"", 60 * time.Second},
		{"30s", 60 * time.Second},
		{"45s", 75 * time.Second},
		{"not-a-duration", 60 * time.Second},
	} {
		if got := leaseTTLFor(tc.submitTimeout); got != tc.want {
			t.Fatalf("leaseTTLFor(%q) = %v, want %v", tc.submitTimeout, got, tc.want)
		}
	}
}

// TestE9T4UngatedRecoveryOnDisabledRoute pins the T2-F005 posture: the
// drain's recovery half is not gated by the configuration key — an
// expired submitting lease on a route whose YAML key is off still
// heals, while nothing is submitted and the operator sees why.
func TestE9T4UngatedRecoveryOnDisabledRoute(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	dispatchID := e7t2NoSubmitDispatch(t, configPath, vault)

	store := e5t1Store(t, configPath)
	if _, err := store.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: dispatchID, AttemptID: "attempt-victim", Owner: "victim",
		Now: "2026-08-20T00:00:00Z", LeaseExpiresAt: "2026-08-20T00:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	// The configuration key half turns off; the store half stays
	// acknowledged exactly as the two-key gate describes.
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, bytes.Replace(body, []byte("enabled: true"), []byte("enabled: false"), 1), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain on a disabled route: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	recovered, _ := res["recovered"].([]any)
	if len(recovered) != 1 {
		t.Fatalf("recovery must run ungated on the disabled route: %v", res)
	}
	if !strings.Contains(out.String(), "disabled in configuration") {
		t.Fatalf("the operator must see why nothing was submitted: %s", out.String())
	}
	healed := e5t1Store(t, configPath)
	defer healed.Close()
	var state string
	if err := healed.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&state); err != nil || state != "unknown" {
		// Recovery moves the expired submitting lease to unknown;
		// the disabled route runs no reconciliation beside it.
		t.Fatalf("the wedged intent must heal to unknown without the configuration key: %q %v", state, err)
	}
}

// TestE9T4ReconcileSubmitSkipsDisabledYAMLKey pins the YAML-key half of
// the two-key gate on the scheduled path — the exact posture the
// shipped recipes create (security round-1 observation): an
// acknowledged route whose configuration key is off persists its
// reconciliation decision and heals expired leases through
// `reconcile --submit`, while the submit leg is skipped with the
// operator warning and nothing reaches the target.
func TestE9T4ReconcileSubmitSkipsDisabledYAMLKey(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	// One in-scope change so the reconciliation has real work to
	// evaluate and persist a decision over.
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "sched.md"), []byte("sched"), 0o644); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, bytes.Replace(body, []byte("enabled: true"), []byte("enabled: false"), 1), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "scheduled", "--submit"}, &out, &errb); code != 0 {
		t.Fatalf("reconcile --submit on the YAML-disabled route: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if !strings.Contains(out.String(), "--submit skipped") || !strings.Contains(out.String(), "disabled in configuration") {
		t.Fatalf("the operator must see the skipped submit leg and why: %s", out.String())
	}
	// The reconciliation itself ran and persisted its lineage: the
	// envelope carries the enumeration result beside the warning.
	if _, ok := res["enumerated"]; !ok {
		t.Fatalf("the reconciliation result must ride the skipped-submit envelope: %v", res)
	}
	healed := e5t1Store(t, configPath)
	defer healed.Close()
	var accepted int
	if err := healed.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE state = 'accepted'`).Scan(&accepted); err != nil || accepted != 0 {
		t.Fatalf("the YAML-disabled route must submit nothing: %d %v", accepted, err)
	}
	var decisions int
	if err := healed.QueryRow(`SELECT COUNT(*) FROM policy_decisions WHERE disposition = 'reconcile'`).Scan(&decisions); err != nil || decisions == 0 {
		t.Fatalf("the reconciliation decision must persist without the YAML key: %d %v", decisions, err)
	}
}

// TestE9T4ScopePredicateErrorFailsClosed pins the error-capable arm of
// the receipt-scope predicate (the E8-T1 classify-error observation):
// its construction is the only error channel, and a route whose
// patterns no longer compile makes the work commands fail closed at
// exit 3 before any receipt is read.
func TestE9T4ScopePredicateErrorFailsClosed(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	dispatchID := e7t2NoSubmitDispatch(t, configPath, vault)

	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// `**` as a non-whole segment does not compile; schema shape rules
	// do not reject it, so the configuration still loads.
	broken := bytes.Replace(body, []byte(`include: ["**/*.md"]`), []byte(`include: ["Inbox**/*.md"]`), 1)
	if bytes.Equal(broken, body) {
		t.Fatal("fixture config must carry the expected include pattern")
	}
	if err := os.WriteFile(configPath, broken, 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "r1", "--external-task-id", "t_00000001"}, &out, &errb)
	if code != 3 {
		t.Fatalf("an uncompilable scope predicate must fail closed at exit 3, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "pattern sets do not compile") && !strings.Contains(errb.String(), "config_invalid") {
		t.Fatalf("the refusal must classify as configuration, got: %s", errb.String())
	}
}

// TestE9T4PathFactLoadFailureDegradesConservative pins T1-F002: when
// the durable path-fact surface fails to load, the completion still
// succeeds and the attribution decision records the degradation
// (facts_unavailable), so the audit trail can explain the
// conservative outcome.
func TestE9T4PathFactLoadFailureDegradesConservative(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	if dispatchID == "" {
		t.Fatalf("no dispatch id: %v", res)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	// Break the path-fact surface after the begin: the completion's
	// attribution must degrade conservatively, never fail.
	store := e5t1Store(t, configPath)
	if _, err := store.Exec(`DROP TABLE path_facts`); err != nil {
		t.Fatal(err)
	}
	store.Close()

	out.Reset()
	errb.Reset()
	manifest := `[{"path":"Inbox/new.md","after_digest":"` + e5t1GoodDigest + `"}]`
	withStdin(t, manifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete over a failed path-fact surface: %s", errb.String())
	}
	healed := e5t1Store(t, configPath)
	defer healed.Close()
	var doc string
	if err := healed.QueryRow(`SELECT context_json FROM state_transitions WHERE entity_type = 'attribution' AND entity_id = ? ORDER BY recorded_at DESC LIMIT 1`, dispatchID).Scan(&doc); err != nil {
		t.Fatalf("the attribution decision must be recorded: %v", err)
	}
	if !strings.Contains(doc, `"facts_unavailable":true`) {
		t.Fatalf("the decision must record the path-fact degradation: %s", doc)
	}
}

// TestE9T4PendingReconcileDeliveredThroughFollowup pins T1-F005's
// delivery half: a pending reconciliation recorded on an IDLE route is
// delivered by the next completion — even a clean one — as exactly one
// latest-state follow-up, and the pending flag is consumed by the
// follow-up taking the route.
func TestE9T4PendingReconcileDeliveredThroughFollowup(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	if dispatchID == "" {
		t.Fatalf("no dispatch id: %v", res)
	}
	// The recorded-but-undelivered pending generation (the quarantine
	// release or reconcile-arrival shape).
	store := e5t1Store(t, configPath)
	if _, err := store.Exec(`UPDATE route_runtime_state SET pending_reconcile = 1 WHERE route_id = 'wiki'`); err != nil {
		t.Fatal(err)
	}
	store.Close()

	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[{"path":"Inbox/new.md","after_digest":"`+e5t1GoodDigest+`"}]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	completed := decodeEnvelope(t, &out)
	followup, _ := completed["followup_dispatch_id"].(string)
	if completed["route_state"] != "FOLLOWUP_READY" || followup == "" {
		t.Fatalf("the pending reconciliation must be delivered as one follow-up: %v", completed)
	}
	healed := e5t1Store(t, configPath)
	defer healed.Close()
	var pending int
	var active string
	if err := healed.QueryRow(`SELECT pending_reconcile, COALESCE(active_dispatch_id, '') FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&pending, &active); err != nil || pending != 0 {
		t.Fatalf("the follow-up must consume the pending generation: %d %v", pending, err)
	}
	var state string
	if err := healed.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, followup).Scan(&state); err != nil || state != "ready" {
		t.Fatalf("the delivered follow-up must be ready: %q %v", state, err)
	}
}

// TestE9T4FailureBudgetMirrorDrivesUncertain pins T1-F006 end to end:
// the service-level budget mirror and the store's guard agree — with a
// one-shot budget, a single cooperative failure exhausts it and the
// completion resolves through UNCERTAIN with the retry budget reason.
func TestE9T4FailureBudgetMirrorDrivesUncertain(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, bytes.Replace(body, []byte("failure_budget: 2"), []byte("failure_budget: 1"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	if dispatchID == "" {
		t.Fatalf("no dispatch id: %v", res)
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--failure-code", "agent_error"}, &out, &errb); code != 0 {
		t.Fatalf("work fail: %s", errb.String())
	}
	// The one-shot budget still permits this first failure: the mirror
	// schedules the retry follow-up instead of resolving.
	first := decodeEnvelope(t, &out)
	followup, _ := first["followup_dispatch_id"].(string)
	if first["route_state"] != "FOLLOWUP_READY" || followup == "" {
		t.Fatalf("the first failure with budget remaining must schedule the retry follow-up: %v", first)
	}
	// The follow-up reaches the target and takes the active slot.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain submits the follow-up: %s", errb.String())
	}
	var followupState string
	fs := e5t1Store(t, configPath)
	if err := fs.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, followup).Scan(&followupState); err != nil || followupState != "accepted" {
		fs.Close()
		t.Fatalf("the follow-up must be accepted before its failure: %q %v", followupState, err)
	}
	fs.Close()

	// The second failure arrives with the budget exhausted: the mirror
	// and the store guard must agree on UNCERTAIN with no follow-up.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", followup, "--run-id", "run-2", "--external-task-id", "t_00000002"}, &out, &errb); code != 0 {
		t.Fatalf("follow-up work begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", followup, "--run-id", "run-2", "--failure-code", "agent_error"}, &out, &errb); code != 0 {
		t.Fatalf("follow-up work fail: %s", errb.String())
	}
	failed := decodeEnvelope(t, &out)
	if failed["route_state"] != "UNCERTAIN" {
		t.Fatalf("the exhausted budget must resolve through UNCERTAIN, got %v", failed)
	}
	healed := e5t1Store(t, configPath)
	defer healed.Close()
	var state string
	if err := healed.QueryRow(`SELECT route_state FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&state); err != nil || state != "UNCERTAIN" {
		t.Fatalf("the store must agree with the service mirror: %q %v", state, err)
	}
	var dispatches int
	if err := healed.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&dispatches); err != nil || dispatches != 2 {
		t.Fatalf("the exhausted budget schedules no follow-up beyond the one retry: %d %v", dispatches, err)
	}
}

// TestE9T4ScheduledRecipesCarrySubmit pins T2-F001: the shipped
// scheduling recipes carry the --submit leg the runbook's
// automatic-recovery claim relies on.
func TestE9T4ScheduledRecipesCarrySubmit(t *testing.T) {
	for _, name := range []string{
		"agent-dispatch-reconcile.launchd.plist.example",
		"agent-dispatch-reconcile.service.example",
	} {
		body, err := os.ReadFile(filepath.Join("..", "..", "docs", "examples", "scripts", name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(string(body), "--submit") {
			t.Fatalf("%s: the scheduled recipe must carry --submit", name)
		}
		if strings.Contains(string(body), "recipe omits") {
			t.Fatalf("%s: the stale omit---submit guidance must be gone", name)
		}
	}
}

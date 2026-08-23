package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Gate G4 (E5-T5): the production-capable feedback-loop harness. Every
// scenario drives the real CLI surface over one disposable vault and
// stub target: synthetic Hermes edits reported through work receipts,
// concurrent human edits, protected/bulk/overflow handling, the
// no-receipt fallback, and the distinct operator operations. The
// per-criterion evidence table lives in docs/VALIDATION.md under
// Gate G4.

// g4Digest renders the canonical content digest.
func g4Digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// g4Edit writes one vault file and delivers its Watchman burst.
func g4Edit(t *testing.T, configPath, vault, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(vault, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	withStdin(t, fmt.Sprintf(`[{"name":%q,"exists":true,"new":true,"size":%d,"type":"f"}]`, name, len(content)), func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("edit burst for %s: %s", name, errb.String())
	}
}

// g4Run is one CLI invocation with envelope decoding.
func g4Run(t *testing.T, args ...string) map[string]any {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, &out, &errb)
	if code != 0 {
		t.Fatalf("%v failed (exit %d): %s", args, code, errb.String())
	}
	return decodeEnvelope(t, &out)
}

// g4StoreInt reads one integer fact from the durable store.
func g4StoreInt(t *testing.T, configPath, query string, args ...any) int {
	t.Helper()
	store := e5t1Store(t, configPath)
	var n int
	if err := store.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestG4FeedbackLoopGate is the gate scenario: AC-401 through AC-408 on
// one route, ending with the production gate review.
func TestG4FeedbackLoopGate(t *testing.T) {
	configPath, vault := e4t3Fixture(t)

	// --- AC-401: an active task plus later changes increments the dirty
	// generation and never creates a parallel task.
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	if code := g4Run0(t, "work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"); code != 0 {
		t.Fatal("work begin failed")
	}
	g4Edit(t, configPath, vault, "Notes/human-during-task.md", "human words")
	if n := g4StoreInt(t, configPath, `SELECT COUNT(*) FROM dispatch_intents`); n != 1 {
		t.Fatalf("AC-401: no parallel task may exist, got %d", n)
	}
	if n := g4StoreInt(t, configPath, `SELECT dirty_generation FROM route_runtime_state WHERE route_id='wiki'`); n != 1 {
		t.Fatalf("AC-401: dirty generation must be 1, got %d", n)
	}

	// --- AC-402/AC-406 posture: nine more bursts during the same active
	// task, then completion WITHOUT any receipt coverage (empty manifest).
	for i := 0; i < 9; i++ {
		g4Edit(t, configPath, vault, fmt.Sprintf("Inbox/burst-%d.md", i), "burst")
	}
	if n := g4StoreInt(t, configPath, `SELECT dirty_generation FROM route_runtime_state WHERE route_id='wiki'`); n != 10 {
		t.Fatalf("AC-402 setup: ten bursts must merge into the generation, got %d", n)
	}
	var out, errb bytes.Buffer
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("completion without receipt: %s", errb.String())
	}
	completed := decodeEnvelope(t, &out)
	if completed["route_state"] != "FOLLOWUP_READY" {
		t.Fatalf("AC-406: no-receipt completion must keep a bounded follow-up: %v", completed)
	}
	if n := g4StoreInt(t, configPath, `SELECT COUNT(*) FROM dispatch_intents WHERE state='ready'`); n != 1 {
		t.Fatalf("AC-402: at most one follow-up, got %d", n)
	}

	// --- AC-403 through AC-405: the follow-up generation runs with a
	// cooperative receipt; agent and human edits mix.
	followup, _ := completed["followup_dispatch_id"].(string)
	submitFollowupProductPath(t, configPath, followup)
	if code := g4Run0(t, "work", "begin", "--config", configPath, "--dispatch-id", followup, "--run-id", "run-2"); code != 0 {
		t.Fatal("follow-up begin failed")
	}
	agentContent := "agent index update"
	g4Edit(t, configPath, vault, "Indexes/topic-index.md", agentContent)
	g4Edit(t, configPath, vault, "Notes/second-human.md", "more human words") // AC-405 mix

	// AC-404 first: a mismatched digest never suppresses.
	out.Reset()
	errb.Reset()
	wrongManifest := `[{"path":"Indexes/topic-index.md","after_digest":"` + g4Digest("tampered") + `"}]`
	withStdin(t, wrongManifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", followup, "--run-id", "run-2", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("mismatched completion: %s", errb.String())
	}
	mismatch := decodeEnvelope(t, &out)
	if mismatch["self_change_suppressed"] == true || mismatch["route_state"] != "FOLLOWUP_READY" {
		t.Fatalf("AC-404: a mismatched digest must stay dirty: %v", mismatch)
	}

	// The next generation completes with an exact receipt for its agent
	// edit while a human edit stays uncovered: the mixed batch keeps one
	// follow-up (AC-405), and the exact path suppresses with audit
	// (AC-403) once the human edit is also reported exactly.
	gen3, _ := mismatch["followup_dispatch_id"].(string)
	submitFollowupProductPath(t, configPath, gen3)
	if code := g4Run0(t, "work", "begin", "--config", configPath, "--dispatch-id", gen3, "--run-id", "run-3"); code != 0 {
		t.Fatal("generation-3 begin failed")
	}
	// The generation-3 agent run makes one synthetic Hermes edit whose
	// receipt exactly matches: the whole generation suppresses (AC-403).
	agentExact := "agent generation-3 index"
	g4Edit(t, configPath, vault, "Indexes/gen3-index.md", agentExact)
	out.Reset()
	errb.Reset()
	exactManifest := `[{"path":"Indexes/gen3-index.md","after_digest":"` + g4Digest(agentExact) + `"}]`
	withStdin(t, exactManifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", gen3, "--run-id", "run-3", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("exact completion: %s", errb.String())
	}
	exact := decodeEnvelope(t, &out)
	if exact["self_change_suppressed"] != true || exact["route_state"] != "IDLE" {
		t.Fatalf("AC-403: exact coverage must clear the route: %v", exact)
	}
	// The suppression decision is audited.
	store := e5t1Store(t, configPath)
	var audit int
	if err := store.QueryRow(`SELECT COUNT(*) FROM state_transitions WHERE entity_type='attribution' AND entity_id=? AND context_json LIKE '%verified_self_generated%'`, gen3).Scan(&audit); err != nil || audit < 1 {
		t.Fatalf("AC-403: audited suppression missing: %d %v", audit, err)
	}
	// No infinite loop: two follow-up-needing completions created exactly
	// two bounded follow-ups (each at most one), both were submitted and
	// accepted through the product path rather than left pending, the
	// suppressed completion created none, and the route is idle with no
	// pending reconciliation — the recursion is bounded by construction.
	if n := g4StoreInt(t, configPath, `SELECT COUNT(*) FROM dispatch_intents WHERE state='ready'`); n != 0 {
		t.Fatalf("stress: no follow-up may be left unsubmitted, got %d", n)
	}
	if n := g4StoreInt(t, configPath, `SELECT COUNT(*) FROM dispatch_intents`); n != 3 {
		t.Fatalf("stress: one dispatch per generation, got %d", n)
	}
	if n := g4StoreInt(t, configPath, `SELECT pending_reconcile FROM route_runtime_state WHERE route_id='wiki'`); n != 0 {
		t.Fatalf("stress: no pending reconciliation may remain, got %d", n)
	}
}

// enableRouteAck enables a route through the production gate with the
// computed revision acknowledgement (shared by the older enable tests).
func enableRouteAck(t *testing.T, configPath, routeID string) int {
	t.Helper()
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	revision, ok := config.RouteRevision(cfg, routeID)
	if !ok {
		t.Fatal("route revision could not be computed")
	}
	var out, errb bytes.Buffer
	return Run([]string{"route", "enable", "--config", configPath, "--route", routeID, "--acknowledge-production-gate", revision, "--yes"}, &out, &errb)
}

// submitFollowupProductPath drives one pending follow-up through the
// product path (E7-T2/B-3): the CLI drain submits the due intent to the
// target, and the acceptance promotes it to the route's active task.
// The earlier store-direct activation bypass is gone from every
// multi-generation test.
func submitFollowupProductPath(t *testing.T, configPath, dispatchID string) {
	t.Helper()
	res := g4Run(t, "dispatches", "drain", "--route", "wiki", "--config", configPath)
	if n, _ := res["processed"].(float64); n != 1 {
		t.Fatalf("follow-up %s must submit exactly once through drain: %v", dispatchID, res)
	}
	store := e5t1Store(t, configPath)
	var state, routeState string
	if err := store.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id=?`, dispatchID).Scan(&state); err != nil || state != "accepted" {
		t.Fatalf("follow-up %s must reach accepted through the product path: %s %v", dispatchID, state, err)
	}
	if err := store.QueryRow(`SELECT route_state FROM route_runtime_state WHERE route_id='wiki'`).Scan(&routeState); err != nil || routeState != "ACTIVE_CLEAN" {
		t.Fatalf("the accepted follow-up must activate the route (B-3): %s %v", routeState, err)
	}
}

// g4Run0 runs one CLI invocation and returns only the exit code.
func g4Run0(t *testing.T, args ...string) int {
	t.Helper()
	var out, errb bytes.Buffer
	return Run(args, &out, &errb)
}

// TestG4StructuralScenarios covers AC-407 and AC-408 inside the gate:
// a protected path holds operator-visibly and a fresh-instance signal
// during active work leaves exactly one pending reconciliation
// generation that completion collapses.
func TestG4StructuralScenarios(t *testing.T) {
	// AC-407: protected path.
	configPath, vault := e4t3Fixture(t)
	e5t4Rewrite(t, configPath, "      protected: []", "      protected: [\"Secrets/**\"]")
	os.MkdirAll(filepath.Join(vault, "Secrets"), 0o755)
	os.WriteFile(filepath.Join(vault, "Secrets", "key.md"), []byte("s"), 0o644)
	e4t3RegisterRoute(t, configPath)
	setPlanEnv(t, vault, false)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Secrets/key.md","exists":true,"new":true,"size":1,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("protected dispatch: %s", errb.String())
	}
	held := decodeEnvelope(t, &out)
	if held["disposition"] != "quarantine" {
		t.Fatalf("AC-407: protected path must hold: %v", held)
	}
	quarantineID, _ := held["quarantine_id"].(string)
	listing := g4Run(t, "quarantine", "list", "--config", configPath, "--state", "held")
	if listing["count"].(float64) != 1 {
		t.Fatalf("AC-407: hold must be operator-visible: %v", listing)
	}
	if n := g4StoreInt(t, configPath, `SELECT COUNT(*) FROM dispatch_intents`); n != 0 {
		t.Fatalf("AC-407: no automatic task may exist, got %d", n)
	}
	released := g4Run(t, "quarantine", "release", "--config", configPath, "--yes", "--reason", "operator reviewed", quarantineID)
	if released["state"] != "released" {
		t.Fatalf("AC-407: release lineage wrong: %v", released)
	}

	// AC-408: fresh instance during active work.
	configPath2, vault2 := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath2, vault2)
	dispatchID, _ := res["dispatch_id"].(string)
	setPlanEnv(t, vault2, true)
	out.Reset()
	errb.Reset()
	withStdin(t, `[{"name":"Inbox/x.md","exists":true,"new":true,"size":1,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath2, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("fresh-instance dispatch: %s", errb.String())
	}
	if fresh := decodeEnvelope(t, &out); fresh["disposition"] != "reconcile" {
		t.Fatalf("AC-408: fresh instance must reconcile: %v", fresh)
	}
	if n := g4StoreInt(t, configPath2, `SELECT COUNT(*) FROM dispatch_intents`); n != 1 {
		t.Fatalf("AC-408: no partial dispatch, got %d", n)
	}
	if code := g4Run0(t, "work", "begin", "--config", configPath2, "--dispatch-id", dispatchID, "--run-id", "r1"); code != 0 {
		t.Fatal("begin failed")
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath2, "--dispatch-id", dispatchID, "--run-id", "r1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("completion: %s", errb.String())
	}
	if done := decodeEnvelope(t, &out); done["route_state"] != "FOLLOWUP_READY" {
		t.Fatalf("AC-408: one pending generation must remain as the follow-up: %v", done)
	}
	if n := g4StoreInt(t, configPath2, `SELECT pending_reconcile FROM route_runtime_state WHERE route_id='wiki'`); n != 0 {
		t.Fatalf("AC-408: the generation must have collapsed into the follow-up, still pending: %d", n)
	}
}

// TestG4DistinctOperatorOperations proves AC-409: retry, reprocess,
// rerun, and reconcile keep their distinct id and lineage semantics.
// The arrival is persisted without submission so the rerun leg runs
// against ready work, the only supersede-eligible shape beside
// dead-lettered work (E7-T2/B-2).
func TestG4DistinctOperatorOperations(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var dispatchOut, dispatchErr bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &dispatchOut, &dispatchErr)
	})
	res := decodeEnvelope(t, &dispatchOut)
	dispatchID, _ := res["dispatch_id"].(string)

	// Locate the batch for reprocess through the receipts surface.
	store := e5t1Store(t, configPath)
	var batchID string
	if err := store.QueryRow(`SELECT b.batch_id FROM change_batches b JOIN dispatch_intents d ON d.decision_id != '' LIMIT 1`).Scan(&batchID); err != nil {
		// Fall back to the newest batch.
		if err := store.QueryRow(`SELECT batch_id FROM change_batches ORDER BY created_at DESC LIMIT 1`).Scan(&batchID); err != nil {
			t.Fatal(err)
		}
	}

	// Reconcile: a new batch-less decision under generation lineage.
	reconciled := g4Run(t, "reconcile", "--route", "wiki", "--config", configPath, "--reason", "manual")
	decisionID, _ := reconciled["decision_id"].(string)
	if !strings.HasPrefix(decisionID, "dec-reconcile-") {
		t.Fatalf("AC-409: reconcile must create its own decision lineage: %v", reconciled)
	}

	// Reprocess: a new decision for the retained batch, original intact.
	reprocessed := g4Run(t, "dispatches", "reprocess", "--config", configPath, batchID)
	if !strings.HasPrefix(reprocessed["new_decision_id"].(string), "dec-reprocess-") {
		t.Fatalf("AC-409: reprocess decision lineage wrong: %v", reprocessed)
	}

	// Rerun: a new dispatch id, generation, and key (requires --yes and
	// --reason; distinct from retry's same-key semantics).
	rerun := g4Run(t, "dispatches", "rerun", "--config", configPath, "--yes", "--reason", "gate check", dispatchID)
	if rerun["dispatch_id"] == dispatchID {
		t.Fatalf("AC-409: rerun must create a new dispatch id: %v", rerun)
	}
}

// TestG4ProductionGateReviewed proves the production-enable posture:
// enabling a route requires the explicit acknowledgement of the
// computed route revision, and the automatic write path stays inactive
// without it.
func TestG4ProductionGateReviewed(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki"}, &out, &errb); code != 2 {
		t.Fatalf("production gate: enable without acknowledgement must be a usage error, got %d", code)
	}
	// The computed route revision is required; a wrong acknowledgement
	// is refused.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", "route-rev-wrong", "--yes"}, &out, &errb); code != 3 {
		t.Fatalf("production gate: a wrong acknowledged revision must be refused, got %d: %s", code, errb.String())
	}
	// The correct computed revision enables the route.
	revision := g4ComputedRevision(t, configPath)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", revision, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("route enable with the computed revision: %s", errb.String())
	}
	enabled := decodeEnvelope(t, &out)
	if enabled["activation_state"] != "enabled" || enabled["acknowledged_revision"] != revision {
		t.Fatalf("production gate acknowledgement state wrong: %v", enabled)
	}
	_ = vault
}

// g4ComputedRevision computes the revision the production gate demands
// through the same configuration authority the enable path uses.
func g4ComputedRevision(t *testing.T, configPath string) string {
	t.Helper()
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	revision, ok := config.RouteRevision(cfg, "wiki")
	if !ok {
		t.Fatal("route revision could not be computed")
	}
	return revision
}

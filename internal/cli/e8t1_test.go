package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/state"
)

// E8-T1 regression evidence (B-1, H-1, M-10): the follow-up loop under
// the burst-between-completion-and-submission window, the bounded
// follow-up chain, and the same-second generation boundary keyed on the
// batch-sequence watermark instead of second-truncated timestamps.

// e8t1CompleteEmpty runs `work complete` with an empty manifest and
// returns the decoded envelope.
func e8t1CompleteEmpty(t *testing.T, configPath, dispatchID, runID string) map[string]any {
	t.Helper()
	var out, errb bytes.Buffer
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", runID, "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("completion of %s: %s", dispatchID, errb.String())
	}
	return decodeEnvelope(t, &out)
}

// e8t1RouteState reads the route's durable lane state (E12-T2: the
// coordination columns live on the destination lane).
func e8t1RouteState(t *testing.T, configPath string) (string, int) {
	t.Helper()
	store := e5t1Store(t, configPath)
	defer store.Close()
	var routeState string
	var dirty int
	if err := store.QueryRow(`SELECT lane_state, dirty_generation FROM destination_lane_state WHERE route_id='wiki'`).Scan(&routeState, &dirty); err != nil {
		t.Fatal(err)
	}
	return routeState, dirty
}

// e8t1DrainFollowup submits the pending follow-up through the product
// drain path and proves the expected dispatch reached accepted.
func e8t1DrainFollowup(t *testing.T, configPath, expected string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain failed (exit %d): %s", code, errb.String())
	}
	env := decodeEnvelope(t, &out)
	processed, _ := env["processed"].(float64)
	if processed != 1 {
		t.Fatalf("drain must submit exactly the pending follow-up, got %v", env)
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var intentState string
	if err := store.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, expected).Scan(&intentState); err != nil {
		t.Fatalf("the scheduled follow-up %s must exist: %v", expected, err)
	}
	if intentState != "accepted" {
		t.Fatalf("the scheduled follow-up %s must reach accepted, got %s", expected, intentState)
	}
}

// TestE8T1EditBetweenCompletionAndFollowupSubmission is the review's
// B-1 reproduction: a vault edit arriving between completion and
// follow-up submission must not wedge the follow-up's own completion.
// The route activates into ACTIVE_DIRTY and completes through the
// documented ACTIVE_DIRTY -> FOLLOWUP_READY edge.
func TestE8T1EditBetweenCompletionAndFollowupSubmission(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	// First generation: one human edit during the active task.
	if code := g4Run0(t, "work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"); code != 0 {
		t.Fatal("work begin failed")
	}
	g4Edit(t, configPath, vault, "Notes/during-first.md", "first human words")
	completed := e8t1CompleteEmpty(t, configPath, dispatchID, "run-1")
	if completed["route_state"] != "FOLLOWUP_READY" {
		t.Fatalf("dirty completion must schedule a follow-up: %v", completed)
	}

	// The B-1 window: one more vault edit before the next drain. The
	// route stays FOLLOWUP_READY with a dirty generation recorded.
	g4Edit(t, configPath, vault, "Notes/before-drain.md", "second human words")
	if routeState, dirty := e8t1RouteState(t, configPath); routeState != "FOLLOWUP_READY" || dirty != 1 {
		t.Fatalf("the burst must keep FOLLOWUP_READY with dirty=1, got %s dirty=%d", routeState, dirty)
	}

	// The drain accepts the follow-up; the retained dirty generation
	// activates with it (the E8-T1 edge) instead of wedging ACTIVE_CLEAN.
	followup, _ := completed["followup_dispatch_id"].(string)
	if followup == "" {
		t.Fatal("completion reported no follow-up id")
	}
	// M-10: the follow-up manifest carries exactly the unresolved paths of
	// the dirty generation (not the parent's whole manifest), and its
	// content fingerprint is recomputed rather than inherited.
	store := e5t1Store(t, configPath)
	var requestJSON, fingerprint, parentFingerprint string
	if err := store.QueryRow(`SELECT request_json FROM dispatch_intents WHERE dispatch_id = ?`, followup).Scan(&requestJSON); err != nil {
		t.Fatal(err)
	}
	if err := store.QueryRow(`SELECT content_fingerprint FROM dispatch_intents WHERE dispatch_id = ?`, followup).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := store.QueryRow(`SELECT content_fingerprint FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&parentFingerprint); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if !strings.Contains(requestJSON, "Notes/during-first.md") || strings.Contains(requestJSON, "Inbox/new.md") {
		t.Fatalf("the follow-up manifest must carry the unresolved path only: %s", requestJSON)
	}
	if fingerprint == parentFingerprint {
		t.Fatal("the follow-up fingerprint must be recomputed over its own manifest")
	}
	e8t1DrainFollowup(t, configPath, followup)
	if routeState, dirty := e8t1RouteState(t, configPath); routeState != "ACTIVE_DIRTY" || dirty != 1 {
		t.Fatalf("the dirty follow-up must activate into ACTIVE_DIRTY with its dirty generation, got %s dirty=%d", routeState, dirty)
	}

	// The follow-up's own completion exits 0: the product path the Blocker
	// closed (previously exit 40 internal_unclassified).
	if code := g4Run0(t, "work", "begin", "--config", configPath, "--dispatch-id", followup, "--run-id", "run-2"); code != 0 {
		t.Fatal("follow-up begin failed")
	}
	second := e8t1CompleteEmpty(t, configPath, followup, "run-2")
	if second["route_state"] != "FOLLOWUP_READY" {
		t.Fatalf("the dirty follow-up's completion must schedule exactly one follow-up: %v", second)
	}
	// The chain stays live: the third generation drains and completes.
	gen3, _ := second["followup_dispatch_id"].(string)
	if gen3 == "" {
		t.Fatal("second completion reported no follow-up id")
	}
	e8t1DrainFollowup(t, configPath, gen3)
	if routeState, _ := e8t1RouteState(t, configPath); routeState != "ACTIVE_CLEAN" {
		t.Fatalf("generation 3 must activate cleanly, got %s", routeState)
	}
	if code := g4Run0(t, "work", "begin", "--config", configPath, "--dispatch-id", gen3, "--run-id", "run-3"); code != 0 {
		t.Fatal("generation-3 begin failed")
	}
	clean := e8t1CompleteEmpty(t, configPath, gen3, "run-3")
	if clean["route_state"] != "IDLE" {
		t.Fatalf("a clean generation-3 completion must return the route to IDLE: %v", clean)
	}
}

// TestE8T1TwentyFiveGenerationChain proves the chain bound (H-1.1/H-1.2)
// without a single timestamp dodge: every cycle edits during the active
// generation, completes without receipt coverage, and drains the
// follow-up through the product path. The dispatch IDs stay bounded
// (UUIDv7, never cumulative -followup-N suffixes) and the route stays
// live through generation 25, well inside state.MaxConsecutiveFollowups.
func TestE8T1TwentyFiveGenerationChain(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	const generations = 25
	current := dispatchID
	for i := 1; i <= generations; i++ {
		runID := fmt.Sprintf("run-%d", i)
		if code := g4Run0(t, "work", "begin", "--config", configPath, "--dispatch-id", current, "--run-id", runID); code != 0 {
			t.Fatalf("generation %d begin failed", i)
		}
		g4Edit(t, configPath, vault, fmt.Sprintf("Inbox/gen-%d.md", i), fmt.Sprintf("generation %d edit", i))
		completed := e8t1CompleteEmpty(t, configPath, current, runID)
		if completed["route_state"] != "FOLLOWUP_READY" {
			t.Fatalf("generation %d completion must schedule a follow-up: %v", i, completed)
		}
		next, _ := completed["followup_dispatch_id"].(string)
		if next == "" {
			t.Fatalf("generation %d completion reported no follow-up", i)
		}
		if len(next) > 36 || strings.Contains(next, "-followup-") {
			t.Fatalf("follow-up ids must stay bounded UUIDv7 values, got %q", next)
		}
		// The batch-sequence watermark keeps the same-second cycle honest
		// with no sleeps: the edit batch always belongs to the generation
		// that observed it and never re-imports into the next one.
		e8t1DrainFollowup(t, configPath, next)
		current = next
		if routeState, _ := e8t1RouteState(t, configPath); routeState != "ACTIVE_CLEAN" {
			t.Fatalf("generation %d must activate cleanly (its edit is the next cycle's dirty work), got %s", i+1, routeState)
		}
	}
	// The final generation completes clean and returns the route to IDLE.
	if code := g4Run0(t, "work", "begin", "--config", configPath, "--dispatch-id", current, "--run-id", "run-final"); code != 0 {
		t.Fatal("final begin failed")
	}
	final := e8t1CompleteEmpty(t, configPath, current, "run-final")
	if final["route_state"] != "IDLE" {
		t.Fatalf("the final clean completion must return the route to IDLE: %v", final)
	}
	if n := g4StoreInt(t, configPath, `SELECT COUNT(*) FROM dispatch_intents`); n != generations+1 {
		t.Fatalf("the chain must carry exactly one intent per generation plus the original, got %d", n)
	}
	// The bound itself: a chain past the budget resolves through UNCERTAIN
	// (the unit-level proof is TestCompleteActiveFollowupBudgetExhausted).
	if state.MaxConsecutiveFollowups < generations {
		t.Fatalf("the follow-up budget %d must bound chains at or beyond the tested %d generations", state.MaxConsecutiveFollowups, generations)
	}
}

// TestE8T1SameSecondCompletionDoesNotReimport proves the generation
// boundary defect (H-1.3) without the retired 1100 ms dodges: a batch
// merged in the same second as a completion belongs to the completed
// generation only — the follow-up's window is keyed on the batch
// sequence watermark, so the follow-up completes clean and the chain
// ends instead of re-importing the batch as uncovered work.
func TestE8T1SameSecondCompletionDoesNotReimport(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	if code := g4Run0(t, "work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1"); code != 0 {
		t.Fatal("work begin failed")
	}
	g4Edit(t, configPath, vault, "Notes/same-second.md", "same second content")
	completed := e8t1CompleteEmpty(t, configPath, dispatchID, "run-1")
	if completed["route_state"] != "FOLLOWUP_READY" {
		t.Fatalf("uncovered completion must schedule a follow-up: %v", completed)
	}
	followup, _ := completed["followup_dispatch_id"].(string)
	e8t1DrainFollowup(t, configPath, followup)
	// No sleep: the same-second batch must not re-import into this
	// generation's window.
	if code := g4Run0(t, "work", "begin", "--config", configPath, "--dispatch-id", followup, "--run-id", "run-2"); code != 0 {
		t.Fatal("follow-up begin failed")
	}
	second := e8t1CompleteEmpty(t, configPath, followup, "run-2")
	if second["route_state"] != "IDLE" {
		t.Fatalf("the follow-up covers no unresolved work and must complete the route to IDLE: %v", second)
	}
	if next, _ := second["followup_dispatch_id"].(string); next != "" {
		t.Fatalf("a re-imported same-second batch would have forced another follow-up: %v", second)
	}
	if n := g4StoreInt(t, configPath, `SELECT COUNT(*) FROM dispatch_intents`); n != 2 {
		t.Fatalf("the chain must end at two intents, got %d", n)
	}
}

// TestE8T1ReceiptScopeAndIdenticalRewriteSuppress proves the receipt
// scope intersection (H-1.1): an honest receipt that also lists an
// out-of-scope path and a byte-identical rewrite (whose reported digest
// equals the durable path fact) fully suppresses the covered generation
// instead of breeding a follow-up per cycle.
func TestE8T1ReceiptScopeAndIdenticalRewriteSuppress(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	stable := "stable note body"

	// Generation one establishes the durable path fact for the stable
	// note through an exact receipt, returning the route to IDLE.
	res, _ := e4t3Dispatch(t, configPath, vault)
	first, _ := res["dispatch_id"].(string)
	if code := g4Run0(t, "work", "begin", "--config", configPath, "--dispatch-id", first, "--run-id", "run-1"); code != 0 {
		t.Fatal("first begin failed")
	}
	g4Edit(t, configPath, vault, "Notes/stable.md", stable)
	var out, errb bytes.Buffer
	firstManifest := `[{"path":"Notes/stable.md","after_digest":"` + g4Digest(stable) + `"}]`
	withStdin(t, firstManifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", first, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("first completion: %s", errb.String())
	}
	if firstDone := decodeEnvelope(t, &out); firstDone["route_state"] != "IDLE" {
		t.Fatalf("generation one must suppress exactly: %v", firstDone)
	}

	// Generation two: one real agent edit (covered exactly), one
	// byte-identical rewrite of the stable note (no observation; the
	// reported digest equals the durable path fact), and one out-of-scope
	// path (never observed; outside the markdown file scope).
	var out2, err2 bytes.Buffer
	withStdin(t, `[{"name":"Inbox/second-run.md","exists":true,"new":true,"size":6,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out2, &err2)
	})
	if err2.Len() != 0 {
		t.Fatalf("second dispatch: %s", err2.String())
	}
	env2 := decodeEnvelope(t, &out2)
	second, _ := env2["dispatch_id"].(string)
	if second == "" {
		t.Fatalf("second dispatch id missing: %v", env2)
	}
	if code := g4Run0(t, "work", "begin", "--config", configPath, "--dispatch-id", second, "--run-id", "run-2"); code != 0 {
		t.Fatal("second begin failed")
	}
	g4Edit(t, configPath, vault, "Indexes/agent-edit.md", "agent index update")
	// The byte-identical rewrite: a distinct delivery event whose ingested
	// digest equals the stored fact, so the modify drops as unchanged.
	var rewriteOut, rewriteErr bytes.Buffer
	withStdin(t, fmt.Sprintf(`[{"name":"Notes/stable.md","exists":true,"new":false,"size":%d,"type":"f"}]`, len(stable)+1), func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &rewriteOut, &rewriteErr)
	})
	if rewriteErr.Len() != 0 {
		t.Fatalf("identical rewrite burst: %s", rewriteErr.String())
	}
	out.Reset()
	errb.Reset()
	manifest := `[{"path":"Indexes/agent-edit.md","after_digest":"` + g4Digest("agent index update") + `"},` +
		`{"path":"Notes/stable.md","after_digest":"` + g4Digest(stable) + `"},` +
		`{"path":".obsidian/workspace.json","after_digest":"` + g4Digest("ignored") + `"}]`
	withStdin(t, manifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", second, "--run-id", "run-2", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("scoped completion: %s", errb.String())
	}
	completed := decodeEnvelope(t, &out)
	if completed["route_state"] != "IDLE" || completed["self_change_suppressed"] != true {
		t.Fatalf("immaterial receipt paths must not block exact suppression: %v", completed)
	}
	if n := g4StoreInt(t, configPath, `SELECT COUNT(*) FROM dispatch_intents`); n != 2 {
		t.Fatalf("a fully suppressed generation schedules no follow-up, got %d intents", n)
	}
}

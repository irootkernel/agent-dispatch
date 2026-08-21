package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rootkernel/jjukkumi/internal/adapters/sqlite"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// e5t3Digest renders the canonical content digest of a byte string.
func e5t3Digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// e5t3Merge delivers one Watchman burst for one vault file while the
// route is active (a dirty-generation merge, CON-002).
func e5t3Merge(t *testing.T, configPath, vault, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(vault, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"`+name+`","exists":true,"new":true,"size":`+strconv.Itoa(len(content))+`,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("merge burst for %s: %s", name, errb.String())
	}
}

// e5t3Attribution reads the persisted attribution decision for one
// dispatch.
func e5t3Attribution(t *testing.T, configPath, dispatchID string) string {
	t.Helper()
	store := e5t1Store(t, configPath)
	var contextJSON string
	if err := store.QueryRow(`SELECT context_json FROM state_transitions WHERE entity_type = 'attribution' AND entity_id = ? ORDER BY recorded_at DESC, transition_id DESC LIMIT 1`, dispatchID).Scan(&contextJSON); err != nil {
		t.Fatalf("attribution decision missing: %v", err)
	}
	return contextJSON
}

// TestExactMatchSuppressesSelfChange proves the exact-suppression path
// (AC-403, FBK-002): a dirty change whose path and after-digest exactly
// match the completion receipt is verified self-generated, the decision
// is audited, and the route clears without a follow-up.
func TestExactMatchSuppressesSelfChange(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	// The agent's edit arrives as a dirty merge during the run.
	e5t3Merge(t, configPath, vault, "Indexes/inbox-index.md", "index update v2")
	manifest := `[{"path":"Indexes/inbox-index.md","after_digest":"` + e5t3Digest("index update v2") + `"}]`
	out.Reset()
	errb.Reset()
	withStdin(t, manifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete: %s", errb.String())
	}
	completed := decodeEnvelope(t, &out)
	if completed["self_change_suppressed"] != true || completed["route_state"] != "IDLE" {
		t.Fatalf("exact match must clear the route: %v", completed)
	}
	if suppressed, _ := completed["suppressed_paths"].([]any); len(suppressed) != 1 {
		t.Fatalf("one suppressed path expected: %v", completed["suppressed_paths"])
	}
	followup, _ := completed["followup_dispatch_id"].(string)
	if followup != "" {
		t.Fatalf("a fully suppressed generation must not schedule a follow-up: %v", completed)
	}

	// The decision is audited and the dirty generation collapsed to zero.
	audit := e5t3Attribution(t, configPath, dispatchID)
	if !strings.Contains(audit, "verified_self_generated") || !strings.Contains(audit, `"fully_suppressed":true`) {
		t.Fatalf("attribution audit wrong: %s", audit)
	}
	store := e5t1Store(t, configPath)
	var dirty int
	if err := store.QueryRow(`SELECT dirty_generation FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&dirty); err != nil || dirty != 0 {
		t.Fatalf("dirty generation must be collapsed: %d %v", dirty, err)
	}
	var ready int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE state = 'ready'`).Scan(&ready); err != nil || ready != 0 {
		t.Fatalf("no follow-up intent may exist: %d %v", ready, err)
	}
}

// TestMismatchStaysDirty proves any digest mismatch refuses suppression
// (AC-404, FBK-003): the change stays unresolved and the completion
// schedules exactly one follow-up.
func TestMismatchStaysDirty(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	e5t3Merge(t, configPath, vault, "Indexes/inbox-index.md", "index update v2")
	wrongDigest := e5t3Digest("different content entirely")
	manifest := `[{"path":"Indexes/inbox-index.md","after_digest":"` + wrongDigest + `"}]`
	out.Reset()
	errb.Reset()
	withStdin(t, manifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete: %s", errb.String())
	}
	completed := decodeEnvelope(t, &out)
	if completed["self_change_suppressed"] == true || completed["route_state"] != "FOLLOWUP_READY" {
		t.Fatalf("a mismatched digest must stay dirty: %v", completed)
	}
	if followup, _ := completed["followup_dispatch_id"].(string); followup == "" {
		t.Fatal("one follow-up must be scheduled for the unresolved change")
	}
	if audit := e5t3Attribution(t, configPath, dispatchID); !strings.Contains(audit, "digest_mismatch") {
		t.Fatalf("mismatch must be audited: %s", audit)
	}

	// A receipt path never observed and a missing receipt path both stay
	// unresolved: the follow-up generation covers them.
	store := e5t1Store(t, configPath)
	var ready int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE state = 'ready'`).Scan(&ready); err != nil || ready != 1 {
		t.Fatalf("exactly one follow-up: %d %v", ready, err)
	}
}

// TestMixedBatchNotFullySuppressed proves a mixed human/agent batch never
// fully suppresses (FBK-004, AC-405): one verified path plus one unknown
// path keeps the route dirty with one bounded follow-up.
func TestMixedBatchNotFullySuppressed(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	// Agent edit plus a concurrent human edit in the same window.
	e5t3Merge(t, configPath, vault, "Indexes/inbox-index.md", "index update v2")
	e5t3Merge(t, configPath, vault, "Notes/human-thought.md", "human words")
	manifest := `[{"path":"Indexes/inbox-index.md","after_digest":"` + e5t3Digest("index update v2") + `"}]`
	out.Reset()
	errb.Reset()
	withStdin(t, manifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete: %s", errb.String())
	}
	completed := decodeEnvelope(t, &out)
	if completed["self_change_suppressed"] == true || completed["route_state"] != "FOLLOWUP_READY" {
		t.Fatalf("a mixed batch must not fully suppress: %v", completed)
	}
	audit := e5t3Attribution(t, configPath, dispatchID)
	if !strings.Contains(audit, "verified_self_generated") || !strings.Contains(audit, "receipt_missing_path") {
		t.Fatalf("mixed attribution must record both outcomes: %s", audit)
	}
}

// TestTenBurstsCollapseIntoOneFollowup proves the collapse bound (AC-402,
// CON-003, FBK-008): ten dirty bursts during one active run without any
// receipt produce exactly one follow-up and never infinite recursion.
func TestTenBurstsCollapseIntoOneFollowup(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	for i := 0; i < 10; i++ {
		e5t3Merge(t, configPath, vault, "Inbox/"+string(rune('a'+i))+".md", "burst")
	}
	// No receipt coverage at all (empty manifest): nothing may suppress.
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
		t.Fatalf("conservative completion must schedule the follow-up generation: %v", completed)
	}
	store := e5t1Store(t, configPath)
	var ready int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE state = 'ready'`).Scan(&ready); err != nil || ready != 1 {
		t.Fatalf("ten bursts must collapse into exactly one follow-up, got %d (%v)", ready, err)
	}
	var dirty int
	if err := store.QueryRow(`SELECT dirty_generation FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&dirty); err != nil || dirty != 0 {
		t.Fatalf("the generation must collapse into the follow-up: %d %v", dirty, err)
	}
	// A receipt with a null after-digest never verifies (unverified
	// digests stay unresolved).
	audit := e5t3Attribution(t, configPath, dispatchID)
	if !strings.Contains(audit, "receipt_missing_path") {
		t.Fatalf("uncovered bursts must be recorded unresolved: %s", audit)
	}
}

// TestTemporalWindowAndLatestObservation proves the matcher's temporal
// and collapse rules (E5 audit F003/F004): a change observed before the
// run began is never the run's output, and a later divergent write to
// the same path is never suppressed through an earlier matching digest.
func TestTemporalWindowAndLatestObservation(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	// A pre-begin change (the generation exists before the run starts).
	e5t3Merge(t, configPath, vault, "Indexes/pre-begin.md", "pre-begin content")
	time.Sleep(1100 * time.Millisecond)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	// The run then writes the same path twice; only the latest digest
	// can verify (F004), and the pre-begin path can never verify (F003).
	e5t3Merge(t, configPath, vault, "Indexes/pre-begin.md", "agent revision")
	e5t3Merge(t, configPath, vault, "Indexes/pre-begin.md", "agent revision 2")
	manifest := `[{"path":"Indexes/pre-begin.md","after_digest":"` + e5t3Digest("pre-begin content") + `"}]`
	// Separate the follow-up generation's creation window from the
	// pre-begin and mid-run batches (second-precision timestamps).
	time.Sleep(1100 * time.Millisecond)
	out.Reset()
	errb.Reset()
	withStdin(t, manifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete: %s", errb.String())
	}
	// The receipt's digest matches the FIRST observation only: the
	// latest observation differs, so nothing suppresses and the
	// conservative follow-up path runs.
	completed := decodeEnvelope(t, &out)
	if completed["self_change_suppressed"] == true || completed["route_state"] != "FOLLOWUP_READY" {
		t.Fatalf("a stale digest must never suppress: %v", completed)
	}
	audit := e5t3Attribution(t, configPath, dispatchID)
	if !strings.Contains(audit, "digest_mismatch") {
		t.Fatalf("the latest observation must decide: %s", audit)
	}

	// A receipt matching the LATEST digest on a fresh generation
	// verifies; the same path's earlier observations are irrelevant.
	followup, _ := completed["followup_dispatch_id"].(string)
	store := e5t1Store(t, configPath)
	if err := store.ActivateFollowup(context.Background(), followup, "test", "2026-08-21T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", followup, "--run-id", "run-2"}, &out, &errb); code != 0 {
		t.Fatalf("begin 2: %s", errb.String())
	}
	e5t3Merge(t, configPath, vault, "Indexes/fresh.md", "fresh agent edit")
	out.Reset()
	errb.Reset()
	manifest2 := `[{"path":"Indexes/fresh.md","after_digest":"` + e5t3Digest("fresh agent edit") + `"}]`
	withStdin(t, manifest2, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", followup, "--run-id", "run-2", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete 2: %s", errb.String())
	}
	second := decodeEnvelope(t, &out)
	if second["self_change_suppressed"] != true || second["route_state"] != "IDLE" {
		t.Fatalf("the latest exact digest must verify: %v", second)
	}
}

// TestSuppressedDirtyWithPendingReconcile proves a fully suppressed
// dirty generation still schedules the follow-up when a reconciliation
// is pending (E5 audit F008): suppression never drops a pending
// generation.
func TestSuppressedDirtyWithPendingReconcile(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	e5t3Merge(t, configPath, vault, "Indexes/suppressed.md", "agent edit")
	// A pending reconciliation generation coexists with the dirty one.
	store := e5t1Store(t, configPath)
	if err := store.MarkPendingReconcile(context.Background(), "wiki", "", "2026-08-21T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	manifest := `[{"path":"Indexes/suppressed.md","after_digest":"` + e5t3Digest("agent edit") + `"}]`
	withStdin(t, manifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete: %s", errb.String())
	}
	completed := decodeEnvelope(t, &out)
	if completed["route_state"] != "FOLLOWUP_READY" {
		t.Fatalf("a pending reconciliation must force the follow-up even when fully suppressed: %v", completed)
	}
	if followup, _ := completed["followup_dispatch_id"].(string); followup == "" {
		t.Fatal("the pending generation must collapse into the follow-up")
	}
	var pending int
	if err := store.QueryRow(`SELECT pending_reconcile FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("the pending generation must clear into the follow-up: %d %v", pending, err)
	}
}

// TestOversizedManifestRefused proves the manifest byte bound is
// enforced before parsing (E5 audit F010).
func TestOversizedManifestRefused(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	oversized := "[" + strings.Repeat(`{"path":"Inbox/x.md"},`, 60000) + `{"path":"Inbox/y.md"}]`
	var dErrb bytes.Buffer
	code := 0
	withStdin(t, oversized, func() {
		code = Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &dErrb)
	})
	if code != 4 {
		t.Fatalf("an oversized manifest must be rejected exit 4, got %d: %s", code, dErrb.String())
	}
	if !strings.Contains(dErrb.String(), "manifest exceeds") {
		t.Fatalf("the bound must be named: %s", dErrb.String())
	}
}

// TestConcurrentCompletionSingleWinner proves the atomic completion
// under concurrency: two concurrent completions of the same run resolve
// to exactly one winner (the begun-row guard), never two route
// transitions (E5 audit F002).
func TestConcurrentCompletionSingleWinner(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	// Each goroutine uses its own manifest file: the shared stdin
	// helper is not concurrency-safe (and neither is process stdin).
	manifestFiles := make([]string, 2)
	for i := range manifestFiles {
		p := filepath.Join(t.TempDir(), fmt.Sprintf("m%d.json", i))
		if err := os.WriteFile(p, []byte(`[]`), 0o600); err != nil {
			t.Fatal(err)
		}
		manifestFiles[i] = p
	}
	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func(manifest string) {
			var o, e bytes.Buffer
			results <- Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", manifest}, &o, &e)
		}(manifestFiles[i])
	}
	wins, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		switch code := <-results; code {
		case 0:
			wins++
		case 4, 14, 20:
			conflicts++
		default:
			t.Fatalf("unexpected exit code %d", code)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("exactly one completion may win, got %d wins / %d conflicts", wins, conflicts)
	}
	store := e5t1Store(t, configPath)
	var completed int
	if err := store.QueryRow(`SELECT COUNT(*) FROM work_receipts WHERE dispatch_id = ? AND run_id = 'run-1' AND status = 'completed'`, dispatchID).Scan(&completed); err != nil || completed != 1 {
		t.Fatalf("one terminal receipt row: %d %v", completed, err)
	}
}

// TestCompletionGenerationFence proves the expected-dirty-generation
// fence: a completion whose receipt matched a different generation than
// the one being completed refuses with optimistic concurrency (E5 audit
// round 5, F002).
func TestCompletionGenerationFence(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	// A stale completion request (the generation moved after its
	// attribution was computed) is refused by the store fence.
	store := e5t1Store(t, configPath)
	_, err := store.CompleteWork(context.Background(), ports.WorkReceiptInput{
		ReceiptID: "rcpt-work-fence", DispatchID: dispatchID, RunID: "run-1",
		Status: "completed", ChangesJSON: "[]", SubmittedAt: "2026-08-21T00:00:00Z", ValidationState: "valid",
	}, ports.ActiveCompletion{
		RouteID: "wiki", DispatchID: dispatchID, ReceiptRef: "rcpt-work-fence", Actor: "test",
		ExpectedDirtyGeneration: 42, Now: "2026-08-21T00:00:00Z",
	})
	if err == nil || !strings.Contains(err.Error(), "dirty generation moved") {
		t.Fatalf("a stale generation fence must refuse: %v", err)
	}
	// The route and the begun receipt are unchanged by the refusal.
	var status string
	if err := store.QueryRow(`SELECT status FROM work_receipts WHERE dispatch_id = ? AND run_id = 'run-1'`, dispatchID).Scan(&status); err != nil || status != "begun" {
		t.Fatalf("the refused completion must leave the begun receipt intact: %q %v", status, err)
	}
}

// TestFailureBudgetResetsAfterValidCompletion proves the streak resets
// on a valid completion: a later failure gets a fresh budget (E5 audit
// round 5, F005).
func TestFailureBudgetResetsAfterValidCompletion(t *testing.T) {
	configPath, vault := e5t1FixtureBudget(t, 1)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "r1"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "r1", "--failure-code", "timeout"}, &out, &errb); code != 0 {
		t.Fatalf("fail: %s", errb.String())
	}
	failed := decodeEnvelope(t, &out)
	followup, _ := failed["followup_dispatch_id"].(string)
	if followup == "" {
		t.Fatalf("a budgeted failure must create its one follow-up: %v", failed)
	}
	// The follow-up completes VALIDLY, resetting the streak...
	store := e5t1Store(t, configPath)
	if err := store.ActivateFollowup(context.Background(), followup, "test", "2026-08-21T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", followup, "--run-id", "r2"}, &out, &errb); code != 0 {
		t.Fatalf("begin 2: %s", errb.String())
	}
	time.Sleep(1100 * time.Millisecond)
	out.Reset()
	errb.Reset()
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", followup, "--run-id", "r2", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete: %s", errb.String())
	}
	// The route is idle again; a fresh generation's failure gets the
	// reset budget (FOLLOWUP_READY, not UNCERTAIN). A distinct burst
	// avoids the source retransmission key.
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "fresh-generation.md"), []byte("fresh"), 0o644); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, vault, false)
	var nextOut, nextErrb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/fresh-generation.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &nextOut, &nextErrb)
	})
	if nextErrb.Len() != 0 {
		t.Fatalf("fresh dispatch: %s", nextErrb.String())
	}
	nextEnv := decodeEnvelope(t, &nextOut)
	next, _ := nextEnv["dispatch_id"].(string)
	_ = next
	nextID := next
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", nextID, "--run-id", "r3"}, &out, &errb); code != 0 {
		t.Fatalf("begin 3: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", nextID, "--run-id", "r3", "--failure-code", "agent_error"}, &out, &errb); code != 0 {
		t.Fatalf("fail after reset: %s", errb.String())
	}
	after := decodeEnvelope(t, &out)
	if after["route_state"] != "FOLLOWUP_READY" {
		t.Fatalf("a valid completion must reset the failure budget: %v", after)
	}
}

// TestObservedBeforeRunNeverSuppresses proves a change whose latest
// observation strictly predates the run's begin is never attributed to
// the run, even with an exact digest (E5 audit round 10, F003).
func TestObservedBeforeRunNeverSuppresses(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	// A pre-begin change; the receipt will match its digest exactly.
	e5t3Merge(t, configPath, vault, "Indexes/early.md", "early content")
	time.Sleep(1100 * time.Millisecond)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	manifest := `[{"path":"Indexes/early.md","after_digest":"` + e5t3Digest("early content") + `"}]`
	withStdin(t, manifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete: %s", errb.String())
	}
	completed := decodeEnvelope(t, &out)
	if completed["self_change_suppressed"] == true || completed["route_state"] != "FOLLOWUP_READY" {
		t.Fatalf("a pre-begin change must never suppress: %v", completed)
	}
	if audit := e5t3Attribution(t, configPath, dispatchID); !strings.Contains(audit, "observed_before_run") {
		t.Fatalf("the temporal demotion must be audited: %s", audit)
	}
}

// TestSameSecondWindowIsInclusive documents and pins the inclusive
// second-precision boundary: a change merged in the same second as the
// run's begin stays attributable (E5 audit round 12, F001).
func TestSameSecondWindowIsInclusive(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	// No sleep: the merge and the begin share the second deliberately.
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	e5t3Merge(t, configPath, vault, "Indexes/same-second.md", "same second content")
	out.Reset()
	errb.Reset()
	manifest := `[{"path":"Indexes/same-second.md","after_digest":"` + e5t3Digest("same second content") + `"}]`
	withStdin(t, manifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("complete: %s", errb.String())
	}
	completed := decodeEnvelope(t, &out)
	if completed["self_change_suppressed"] != true || completed["route_state"] != "IDLE" {
		t.Fatalf("the same-second boundary must be inclusive: %v", completed)
	}
	// The dirty-generation window is inclusive on the same second too:
	// the merge created in the dispatch's second entered the generation.
	if n := g4StoreInt(t, configPath, `SELECT dirty_generation FROM route_runtime_state WHERE route_id='wiki'`); n != 0 {
		t.Fatalf("the suppressed generation must have collapsed: %d", n)
	}
	store := e5t1Store(t, configPath)
	var dirtyBatches int
	if err := store.QueryRow(`SELECT COUNT(*) FROM change_batches cb
		JOIN dispatch_intents d ON d.dispatch_id = ?
		WHERE cb.created_at >= d.created_at AND cb.batch_id != (SELECT COALESCE(batch_id,'') FROM policy_decisions WHERE decision_id = d.decision_id)`, dispatchID).Scan(&dirtyBatches); err != nil || dirtyBatches < 1 {
		t.Fatalf("the same-second merge must be inside the window: %d %v", dirtyBatches, err)
	}
}

// TestGenerationFenceMapsToConflict pins the fence's CLI class: the
// stale-generation refusal is a conflict (exit 14), not storage (E5
// audit round 12, F002).
func TestGenerationFenceMapsToConflict(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1"}, &out, &errb); code != 0 {
		t.Fatalf("begin: %s", errb.String())
	}
	store := e5t1Store(t, configPath)
	_, err := store.CompleteWork(context.Background(), ports.WorkReceiptInput{
		ReceiptID: "rcpt-fence", DispatchID: dispatchID, RunID: "run-1",
		Status: "completed", ChangesJSON: "[]", SubmittedAt: "2026-08-21T00:00:00Z", ValidationState: "valid",
	}, ports.ActiveCompletion{
		RouteID: "wiki", DispatchID: dispatchID, ReceiptRef: "rcpt-fence", Actor: "test",
		ExpectedDirtyGeneration: 7, Now: "2026-08-21T00:00:00Z",
	})
	if err == nil || !errors.Is(err, sqlite.ErrOptimisticConcurrency) {
		t.Fatalf("the fence must carry the optimistic-concurrency sentinel: %v", err)
	}
	_ = vault
}

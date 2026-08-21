package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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

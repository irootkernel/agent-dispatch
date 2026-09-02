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

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
	"github.com/irootkernel/agent-dispatch/internal/ports"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/hermesenv"
)

// newGateSink builds the gate's sink through the production
// resolveSink path (no re-implementation).
func newGateSink(h *g3) (ports.Sink, error) {
	cfg, err := config.Load(h.configPath)
	if err != nil {
		return nil, err
	}
	return resolveSink(cfg, "wiki", nil)
}

// Gate G3 (E4-T5): real end-to-end evidence for the Hermes Kanban
// delivery. The harness uses only real components — the built agent-dispatch
// binary as separate one-shot processes, the installed Watchman with a
// real trigger on a disposable vault, and the installed Hermes through
// a disposable board created and hard-deleted through the public CLI
// (TST-007; E0-T4 boundary). The user's active board selection and the
// production vault are never touched, and no production vault automatic
// write is enabled anywhere in the evidence.

var (
	g3BinaryOnce sync.Once
	g3BinaryPath string
	g3BinaryErr  error
)

// g3Binary builds the real agent-dispatch binary once for the gate; every
// gate CLI interaction runs as a separate OS process through it,
// proving process-restart durability rather than in-process reuse.
func g3Binary(t *testing.T) string {
	t.Helper()
	g3BinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "agent-dispatch-g3-bin")
		if err != nil {
			g3BinaryErr = err
			return
		}
		bin := filepath.Join(dir, "agent-dispatch")
		out, err := exec.Command("go", "build", "-o", bin, "github.com/irootkernel/agent-dispatch/cmd/agent-dispatch").CombinedOutput()
		if err != nil {
			g3BinaryErr = fmt.Errorf("go build: %v: %s", err, out)
			return
		}
		g3BinaryPath = bin
	})
	if g3BinaryErr != nil {
		t.Fatalf("build gate binary: %v", g3BinaryErr)
	}
	// The shared binary intentionally outlives individual tests; the
	// OS temp directory reclaims it.
	return g3BinaryPath
}

// g3Run executes the real binary as one separate process.
func g3Run(t *testing.T, bin string, args ...string) (map[string]any, string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	code := 0
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	var result map[string]any
	if out.Len() > 0 {
		var env struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(out.Bytes(), &env); err == nil && len(env.Result) > 0 {
			json.Unmarshal(env.Result, &result)
		}
	}
	return result, out.String(), errb.String(), code
}

// g3Hermes runs one public hermes administrative command.
func g3Hermes(t *testing.T, bin string, argv ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, argv...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// g3RouteRevision computes the revision the production gate demands.
func g3RouteRevision(t *testing.T, configPath string) string {
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

// g3 is the gate harness state.
type g3 struct {
	bin, hermes, configPath, vault, board, stateDir string
}

// g3Setup guards on the real Watchman and a verified Hermes, then
// builds the isolated vault, config, and disposable board.
func g3Setup(t *testing.T) *g3 {
	t.Helper()
	if _, err := exec.LookPath("watchman"); err != nil {
		t.Skip("watchman binary not available (environment-dependent evidence gap)")
	}
	sandbox := hermesenv.NewSandbox(t, func(firstLine string) bool {
		return strings.Contains(firstLine, "v0.20.5")
	})

	dir := t.TempDir()
	h := &g3{
		bin:      g3Binary(t),
		hermes:   sandbox.Binary,
		vault:    filepath.Join(dir, "vault"),
		stateDir: filepath.Join(dir, "state"),
		board:    fmt.Sprintf("agent-dispatch-e4t5-g3-%d", time.Now().UnixNano()),
	}
	if err := os.MkdirAll(filepath.Join(h.vault, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The gate runs against the real installed Hermes with a disposable
	// profile prepared through the public CLI (E15-T4): the wiki-maintainer
	// destination profile is created and board-registered inside a
	// throwaway HOME, so the gate never depends on the operator's own
	// profiles and leaves the real ~/.hermes untouched.
	for _, argv := range [][]string{
		{"profile", "create", "wiki-maintainer"},
		{"profile", "use", "wiki-maintainer"},
		{"config", "set", "default_model", "gpt-5.2", "--force"},
	} {
		if out, err := g3Hermes(t, h.hermes, argv...); err != nil {
			t.Fatalf("disposable profile setup %v: %v: %s", argv, err, out)
		}
	}
	if out, err := g3Hermes(t, h.hermes, "kanban", "boards", "create", h.board); err != nil {
		t.Fatalf("boards create: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := g3Hermes(t, h.hermes, "kanban", "boards", "rm", h.board, "--delete"); err != nil {
			t.Errorf("cleanup boards rm --delete failed: %v: %s", err, out)
		}
	})
	h.configPath = filepath.Join(dir, "config.yaml")
	cfg := fmt.Sprintf(`version: 1
instance:
  id: g3-gate
  state_dir: %s
resources:
  vault-main:
    type: directory
    root: %s
    file_scope: markdown
    git:
      mode: disabled
hermes_targets:
  hermes-main:
    board: %s
    minimum_version: 0.20.5
    compatibility: capability_probe
    executable: %s
    submit_timeout: 30s
    lookup_timeout: 15s
    environment_allowlist: [PATH, HOME, HERMES_HOME, HERMES_KANBAN_HOME]
routes:
  wiki:
    enabled: true
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: agent-dispatch.wiki.g3
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
        profile: wiki-maintainer
        skills: [llm-wiki]
        mutex_key: wiki-publish
        workstream: main
        execution_hints:
          max_runtime: 30m
          max_attempts: 2
    submission_retry:
      max_attempts: 3
      initial_backoff: 1s
      max_backoff: 4s
      multiplier: 2.0
      jitter_fraction: 0.0
    latest_state: true
    failure_budget: 2
    active_stale_after: 2h
    reconciliation:
      initial: true
      daily_expected: true
`, h.stateDir, h.vault, h.board, h.hermes)
	if err := os.WriteFile(h.configPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return h
}

// g3Register materializes the route registration through the
// production first-use entry point — `route enable` with the
// production-gate flags — so the gate exercises the remediated
// operator flow rather than direct store seeding.
func (h *g3) g3Register(t *testing.T) {
	t.Helper()
	g3Revision := g3RouteRevision(t, h.configPath)
	_, _, errb, code := g3Run(t, h.bin, "route", "enable", "--route", "wiki", "--config", h.configPath, "--acknowledge-production-gate", g3Revision, "--yes")
	if code != 0 {
		t.Fatalf("production route enable: %s", errb)
	}
}

// g3WaitAccepted polls the dispatches list until one dispatch reaches
// the wanted state or the gate deadline passes.
func (h *g3) g3WaitAccepted(t *testing.T, state string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		res, _, _, code := g3Run(t, h.bin, "dispatches", "list", "--config", h.configPath, "--route", "wiki")
		if code == 0 {
			if rows, ok := res["dispatches"].([]any); ok {
				for _, raw := range rows {
					if row, ok := raw.(map[string]any); ok && row["state"] == state {
						return row
					}
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("no dispatch reached state %s within the gate deadline", state)
	return nil
}

// TestG3AC301And305RealTriggerEndToEnd proves AC-301 and AC-305: a real
// Watchman-triggered change in the isolated vault creates exactly one
// durably accepted task on the real disposable board with the stored
// external task id, and the generated task carries the trusted/untrusted
// separation, the configured resource, latest-state semantics, and
// receipt instructions with no note body.
func TestG3AC301And305RealTriggerEndToEnd(t *testing.T) {
	h := g3Setup(t)
	h.g3Register(t)

	// Install a real Watchman trigger whose command is the real binary
	// with the gate configuration (absolute paths; state dir comes from
	// the configuration so the trigger environment needs nothing).
	client := watchman.NewClient("")
	ctx := context.Background()
	watchRoot, err := client.EnsureWatch(ctx, h.vault)
	if err != nil {
		t.Fatalf("ensure watch: %v", err)
	}
	trigger := watchman.ManagedTrigger("agent-dispatch.wiki.g3", []string{h.bin, "dispatch", "--route", "wiki", "--config", h.configPath, "--input", "watchman"}, "")
	if _, err := client.TriggerInstall(ctx, watchRoot, trigger); err != nil {
		t.Fatalf("trigger install: %v", err)
	}
	t.Cleanup(func() {
		_, _ = client.TriggerDelete(ctx, watchRoot, trigger.Name)
		_ = client.WatchDelete(context.Background(), watchRoot)
	})

	// The vault change: one new note whose content must never reach the
	// task.
	noteBody := "g3-secret-note-body-0123456789"
	note := filepath.Join(h.vault, "Inbox", "gate-note.md")
	if err := os.WriteFile(note, []byte(noteBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// The first delivery after trigger installation carries a fresh
	// instance (empty since): per SRC-005 it reconciles instead of
	// dispatching, so the gate drives the reconciliation path and then
	// waits for the accepted task.
	res, _, _, code := g3Run(t, h.bin, "reconcile", "--route", "wiki", "--config", h.configPath, "--reason", "fresh-instance", "--submit")
	if code != 0 {
		t.Fatalf("post-fresh-instance reconcile failed (exit %d)", code)
	}
	_ = res

	// AC-301: one durable accepted task with the external id stored.
	row := h.g3WaitAccepted(t, "accepted")
	dispatchID, _ := row["dispatch_id"].(string)
	if dispatchID == "" {
		t.Fatalf("accepted dispatch missing: %v", row)
	}
	detail, _, _, code := g3Run(t, h.bin, "dispatches", "show", "--config", h.configPath, dispatchID)
	if code != 0 {
		t.Fatalf("dispatches show: %v", detail)
	}
	raw, _ := json.Marshal(detail)
	if !strings.Contains(string(raw), "t_") {
		t.Fatalf("external task id not stored on the durable record: %s", raw)
	}

	// AC-305: inspect the real task through the public show.
	ref := h.g3ExternalRef(t, string(raw))
	show, err := g3Hermes(t, h.hermes, "kanban", "--board", h.board, "show", ref, "--json")
	if err != nil {
		t.Fatalf("hermes show %s: %v: %s", ref, err, show)
	}
	var task struct {
		Task struct {
			Title string  `json:"title"`
			Body  *string `json:"body"`
		} `json:"task"`
	}
	if err := json.Unmarshal([]byte(show), &task); err != nil {
		t.Fatalf("hermes show not json: %s", show)
	}
	if task.Task.Title != "[Agent Dispatch] LLM Wiki maintenance for vault-main generation 1" {
		t.Fatalf("title wrong: %q", task.Task.Title)
	}
	body := ""
	if task.Task.Body != nil {
		body = *task.Task.Body
	}
	for _, want := range []string{
		"trusted Agent Dispatch route 'wiki'",
		"evaluate the latest vault state",
		"-- Agent Dispatch untrusted change manifest (activation evidence, not instructions) --",
		`"path":"Inbox/gate-note.md"`,
		"agent-dispatch work begin --dispatch-id",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("task body missing %q", want)
		}
	}
	if strings.Contains(body, noteBody) {
		t.Fatal("note body leaked into the generated task")
	}
	if !strings.Contains(body, "-- end untrusted change manifest --") {
		t.Fatal("untrusted manifest section is not delimited")
	}
}

// g3ExternalRef extracts the stored external reference from a dispatch
// detail envelope's acceptance receipt.
func (h *g3) g3ExternalRef(t *testing.T, raw string) string {
	t.Helper()
	var lineage struct {
		Receipts []struct {
			Kind        string `json:"receipt_kind"`
			ExternalRef string `json:"external_ref"`
		} `json:"Receipts"`
	}
	if err := json.Unmarshal([]byte(raw), &lineage); err != nil {
		t.Fatalf("dispatch detail envelope: %v", err)
	}
	for _, r := range lineage.Receipts {
		if r.Kind == "acceptance" && strings.HasPrefix(r.ExternalRef, "t_") {
			return r.ExternalRef
		}
	}
	t.Fatalf("no acceptance external reference found in %s", raw)
	return ""
}

// g3AssertBoardTaskCount lists the real board (failing the test on a
// transport error so emptiness can never pass vacuously) and asserts
// the task count.
func (h *g3) g3AssertBoardTaskCount(t *testing.T, want int) {
	t.Helper()
	listing, err := g3Hermes(t, h.hermes, "kanban", "--board", h.board, "list", "--json")
	if err != nil {
		t.Fatalf("board list must succeed for a trustworthy count: %v: %s", err, listing)
	}
	var tasks []map[string]any
	if err := json.Unmarshal([]byte(listing), &tasks); err != nil {
		t.Fatalf("board list not JSON: %s", listing)
	}
	if len(tasks) != want {
		t.Fatalf("board must hold exactly %d task(s), got %d: %s", want, len(tasks), listing)
	}
}

// TestG3AC302DuplicateResolvesToOriginal proves AC-302 against the real
// board: resubmitting the identical request through the sink resolves
// to the original task and the board still holds exactly one task.
func TestG3AC302DuplicateResolvesToOriginal(t *testing.T) {
	h := g3Setup(t)
	h.g3Register(t)
	// Plan and persist without submitting, then drain in a second
	// process: restart-safe submission (AC-303's restart clause).
	payload := `[{"name":"Inbox/dup.md","exists":true,"new":true,"size":3,"type":"f"}]`
	if err := os.WriteFile(filepath.Join(h.vault, "Inbox", "dup.md"), []byte("dup"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.g3DispatchPayload(t, payload, "--no-submit")
	_, _, errb, code := g3Run(t, h.bin, "dispatches", "drain", "--route", "wiki", "--config", h.configPath)
	if code != 0 {
		t.Fatalf("drain: %s", errb)
	}
	row := h.g3WaitAccepted(t, "accepted")
	dispatchID, _ := row["dispatch_id"].(string)

	// Load the durable intent and resubmit the identical stored request
	// through the same sink construction the CLI uses.
	intent := h.g3LoadIntent(t, dispatchID)
	sink, err := newGateSink(h)
	if err != nil {
		t.Fatal(err)
	}
	first, err := sink.Submit(context.Background(), intent)
	if err != nil || first.Classification != "accepted" {
		t.Fatalf("resubmit: %+v err=%v", first, err)
	}
	second, err := sink.Submit(context.Background(), intent)
	if err != nil || second.ExternalRef != first.ExternalRef {
		t.Fatalf("duplicate must resolve to the original %s, got %+v err=%v", first.ExternalRef, second, err)
	}
	h.g3AssertBoardTaskCount(t, 1)
}

// TestG3AC303DowntimeAndRestart proves AC-303: with Hermes unavailable,
// the committed intent stays retryable and the event is not lost; after
// recovery a separate process submits it successfully.
func TestG3AC303DowntimeAndRestart(t *testing.T) {
	h := g3Setup(t)
	h.g3Register(t)

	// Downtime: a present but failing Hermes (a server-down stand-in
	// that answers the version gate and then rejects every command).
	down := filepath.Join(h.stateDir, "hermes-down")
	downScript := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf 'Hermes Agent v0.20.5 (2026.8.19)\\n'; exit 0; fi\necho 'hermes server is down' >&2; exit 7\n"
	if err := os.WriteFile(down, []byte(downScript), 0o755); err != nil {
		t.Fatal(err)
	}
	h.g3SetExecutable(t, down)
	// The executable swap moves the route's behavior digest (E9-T3/
	// T3-F006), so the operator re-acknowledges under the stand-in
	// before dispatching — the pause the revision guard exists to force.
	if _, _, errb, code := g3Run(t, h.bin, "route", "enable", "--route", "wiki", "--config", h.configPath, "--acknowledge-production-gate", g3RouteRevision(t, h.configPath), "--yes"); code != 0 {
		t.Fatalf("re-acknowledge under the downtime stand-in: %s", errb)
	}
	if err := os.WriteFile(filepath.Join(h.vault, "Inbox", "down.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _, errOut := h.g3DispatchPayload(t, `[{"name":"Inbox/down.md","exists":true,"new":true,"size":1,"type":"f"}]`)
	if !strings.Contains(errOut, "|exit=0") {
		t.Fatalf("downtime dispatch failed: %s", errOut)
	}
	if res["submitted"] != true || res["state"] != "unknown" {
		t.Fatalf("downtime result must record the ambiguous unknown, got %v", res)
	}
	dispatchID, _ := res["dispatch_id"].(string)
	// The committed intent exists throughout the outage: no event lost.
	if _, _, _, c := g3Run(t, h.bin, "dispatches", "show", "--config", h.configPath, dispatchID); c != 0 {
		t.Fatal("the committed intent must remain inspectable during downtime")
	}

	// With the target down, the drain reconciles the unknown dispatch
	// and dead-letters it for the operator: the work stays durable and
	// inspectable, never lost, never duplicated.
	if _, _, errb, code := g3Run(t, h.bin, "dispatches", "drain", "--route", "wiki", "--config", h.configPath); code != 0 {
		t.Fatalf("downtime drain: %s", errb)
	}
	// The downtime reconciliation created no duplicate: the board is
	// still empty until recovery.
	h.g3AssertBoardTaskCount(t, 0)
	if _, _, _, c := g3Run(t, h.bin, "dispatches", "show", "--config", h.configPath, dispatchID); c != 0 {
		t.Fatal("the dead-lettered intent must remain inspectable after the downtime drain")
	}

	// Recovery: restore the executable and drain in fresh processes
	// until the persisted backoff deadline passes and the work submits.
	h.g3SetExecutable(t, h.hermes)
	// The restore moves the behavior digest back, so the operator
	// re-acknowledges the route under the restored executable before the
	// drain (E9-T3/T3-F006: every executable swap is a re-acknowledge
	// boundary, both directions).
	if _, _, errb, code := g3Run(t, h.bin, "route", "enable", "--route", "wiki", "--config", h.configPath, "--acknowledge-production-gate", g3RouteRevision(t, h.configPath), "--yes"); code != 0 {
		t.Fatalf("re-acknowledge under the restored executable: %s", errb)
	}
	// The operator resolves the downtime explicitly: retry returns the
	// dead-lettered work to ready, and the drain supersedes the stored
	// plan — planned under the stand-in's revision — with a rebuilt
	// request under the active configuration before submitting.
	if _, _, errb, code := g3Run(t, h.bin, "dispatches", "retry", "--config", h.configPath, dispatchID, "--reason", "hermes downtime resolved"); code != 0 {
		t.Fatalf("operator retry: %s", errb)
	}
	var row map[string]any
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, _, errb, code := g3Run(t, h.bin, "dispatches", "drain", "--route", "wiki", "--config", h.configPath)
		if code != 0 {
			t.Fatalf("recovery drain: %s", errb)
		}
		res, _, _, code := g3Run(t, h.bin, "dispatches", "list", "--config", h.configPath, "--route", "wiki")
		if code == 0 {
			if rows, ok := res["dispatches"].([]any); ok {
				for _, raw := range rows {
					if r, ok := raw.(map[string]any); ok && r["state"] == "accepted" {
						row = r
						break
					}
				}
			}
		}
		if row != nil {
			break
		}
		time.Sleep(time.Second)
	}
	if row == nil {
		t.Fatal("the downed dispatch never recovered after the target returned")
	}
	// The recovered work is the stale-rebuilt replacement, not the
	// original identity: the stored plan was superseded under the active
	// configuration (E7-T3 staleness) because the executable swap moved
	// the behavior digest between planning and recovery (E9-T3/T3-F006).
	// The invariants that matter: the work was never lost, never
	// duplicated, and the original is explicitly superseded — not
	// abandoned beside an unrelated accepted dispatch.
	h.g3AssertBoardTaskCount(t, 1)
	store := e5t1Store(t, h.configPath)
	defer store.Close()
	var originalState string
	if err := store.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&originalState); err != nil || originalState != "superseded" {
		t.Fatalf("the downed original must be superseded by the rebuilt replacement, got %q %v", originalState, err)
	}
	// The accepted work is the recorded rebuild of exactly this
	// original: the -rebuilt- identity tie plus the superseding
	// decision linkage (RerunIntent records supersedes_decision_id).
	if !strings.HasPrefix(row["dispatch_id"].(string), dispatchID+"-rebuilt-") {
		t.Fatalf("the accepted work must be the original's rebuild: %v", row["dispatch_id"])
	}
	var supersedes string
	if err := store.QueryRow(`SELECT p.supersedes_decision_id FROM policy_decisions p
		JOIN dispatch_intents i ON i.decision_id = p.decision_id
		WHERE i.dispatch_id = ?`, row["dispatch_id"]).Scan(&supersedes); err != nil || supersedes == "" {
		t.Fatalf("the replacement's decision must supersede the original's: %q %v", supersedes, err)
	}
	var originalDecision string
	if err := store.QueryRow(`SELECT decision_id FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&originalDecision); err != nil || supersedes != originalDecision {
		t.Fatalf("the superseded decision must be the original's: %q vs %q %v", supersedes, originalDecision, err)
	}
}

// TestG3AC304AmbiguousUnknownNoFallback proves AC-304: an ambiguous CLI
// result records unknown locally, reconciles without any webhook or
// target fallback, and dead-letters for the operator.
func TestG3AC304AmbiguousUnknownNoFallback(t *testing.T) {
	h := g3Setup(t)
	h.g3Register(t)

	// Ambiguity: a hermes look-alike that answers the version gate and
	// then never answers the create.
	stub := filepath.Join(h.stateDir, "hermes-ambiguous")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf 'Hermes Agent v0.20.5 (2026.8.19)\\n'; exit 0; fi\nsleep 60\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	h.g3SetExecutable(t, stub)
	// Swapping the target executable changes the route's behavior digest
	// (E9-T3/T3-F006: a route acknowledged under one binary never
	// submits through another), so the operator re-acknowledges the
	// route under the stub before dispatching — exactly the pause the
	// revision guard exists to force.
	if _, _, errb, code := g3Run(t, h.bin, "route", "enable", "--route", "wiki", "--config", h.configPath, "--acknowledge-production-gate", g3RouteRevision(t, h.configPath), "--yes"); code != 0 {
		t.Fatalf("re-acknowledge under the stub executable: %s", errb)
	}
	if err := os.WriteFile(filepath.Join(h.vault, "Inbox", "amb.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _, errOut := h.g3DispatchPayload(t, `[{"name":"Inbox/amb.md","exists":true,"new":true,"size":1,"type":"f"}]`)
	if !strings.Contains(errOut, "|exit=0") {
		t.Fatalf("ambiguous dispatch failed hard: %s", errOut)
	}
	if res["state"] != "unknown" {
		t.Fatalf("ambiguous result must record unknown, got %v", res["state"])
	}
	// Drain reconciles; no reference is known and no fallback target
	// exists in the configuration, so the work dead-letters.
	drain, _, errb, code := g3Run(t, h.bin, "dispatches", "drain", "--route", "wiki", "--config", h.configPath)
	if code != 0 {
		t.Fatalf("reconciling drain: %s", errb)
	}
	reconciled, _ := drain["reconciled"].([]any)
	if len(reconciled) != 1 {
		t.Fatalf("one unknown dispatch must be reconciled: %v", drain)
	}
	entry, _ := reconciled[0].(map[string]any)
	if entry["state"] != "dead_lettered" {
		t.Fatalf("ambiguous unknown with no reference must dead-letter, got %v", entry)
	}
	// The board was never written (the stub never answered); the
	// listing must succeed so the emptiness assertion cannot pass
	// vacuously on a transport error.
	h.g3AssertBoardTaskCount(t, 0)
}

// TestG3AC306CapabilityGateBlocks proves AC-306: a production route
// requiring a capability the verified target does not provide fails
// validation before any submission.
func TestG3AC306CapabilityGateBlocks(t *testing.T) {
	h := g3Setup(t)
	h.g3Register(t)

	// A Hermes below the eligibility floor, re-pointed through the
	// configuration (the SOT-named AC-306 gate posture under the E11-T1
	// contract: the capability-shape probe that restores
	// per-capability refusal lands with E11-T2).
	belowFloor := e4t1StubHermes(t, h.stateDir, "Hermes Agent v0.18.0 (2026.5.1)")
	raw, err := os.ReadFile(h.configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(raw), "hermes", belowFloor, 1)
	if err := os.WriteFile(h.configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(h.vault, "Inbox", "gate.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, errOut := h.g3DispatchPayload(t, `[{"name":"Inbox/gate.md","exists":true,"new":true,"size":1,"type":"f"}]`)
	if !strings.Contains(errOut, "|exit=3") {
		t.Fatalf("the below-floor target must block validation with exit 3: %s", errOut)
	}
	// Nothing was submitted; the listing must succeed so the
	// emptiness assertion cannot pass vacuously.
	h.g3AssertBoardTaskCount(t, 0)
}

// g3DispatchPayload drives one real dispatch process with the given
// watchman payload on stdin.
func (h *g3) g3DispatchPayload(t *testing.T, payload string, extra ...string) (map[string]any, string, string) {
	t.Helper()
	args := append([]string{"dispatch", "--route", "wiki", "--config", h.configPath, "--input", "watchman"}, extra...)
	cmd := exec.Command(h.bin, args...)
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = g3TriggerEnv(t, h)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	code := 0
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatalf("dispatch: %v", err)
		}
	}
	var result map[string]any
	if out.Len() > 0 {
		var env struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(out.Bytes(), &env); err == nil && len(env.Result) > 0 {
			json.Unmarshal(env.Result, &result)
		}
	}
	return result, out.String(), errb.String() + fmt.Sprintf("|exit=%d", code)
}

// g3TriggerEnv renders the Watchman trigger environment the dispatch
// input expects.
func g3TriggerEnv(t *testing.T, h *g3) []string {
	t.Helper()
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"HERMES_HOME=" + os.Getenv("HERMES_HOME"),
		"HERMES_KANBAN_HOME=" + os.Getenv("HERMES_KANBAN_HOME"),
		"WATCHMAN_TRIGGER=agent-dispatch.wiki.g3",
		"WATCHMAN_ROOT=" + h.vault,
		"WATCHMAN_CLOCK=c:1:2:3:4",
		"WATCHMAN_SOCK=/tmp/sock",
		"WATCHMAN_SINCE=c:1:2:3:3",
	}
}

// g3SetExecutable swaps the configured hermes executable.
func (h *g3) g3SetExecutable(t *testing.T, executable string) {
	t.Helper()
	raw, err := os.ReadFile(h.configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := string(raw)
	for _, line := range strings.Split(updated, "\n") {
		if strings.HasPrefix(line, "    executable: ") {
			updated = strings.Replace(updated, line, "    executable: "+executable, 1)
			break
		}
	}
	if err := os.WriteFile(h.configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

// g3LoadIntent reads the durable intent's stored request directly from
// the gate state store (the list surface intentionally omits request
// bodies).
func (h *g3) g3LoadIntent(t *testing.T, dispatchID string) (request portsTaskRequest) {
	t.Helper()
	cfg, err := config.Load(h.configPath)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := platformpaths.ResolveStateDir(cfg.Instance.StateDir)
	store, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snap, err := store.LoadIntent(context.Background(), dispatchID)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(snap.RequestJSON), &request); err != nil {
		t.Fatalf("stored request: %v", err)
	}
	return request
}

// portsTaskRequest mirrors the logical request for gate-side
// resubmission; the sink package owns the real type.
type portsTaskRequest = ports.TaskRequest

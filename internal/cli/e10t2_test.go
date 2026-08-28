package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
	"github.com/irootkernel/agent-dispatch/internal/config"
)

// e10t2Fixture writes one enabled watchman route whose configured vault
// root is nested under a disposable ancestor directory, and returns the
// config path, the ancestor, and the nested vault root.
func e10t2Fixture(t *testing.T) (configPath, ancestor, vault string) {
	t.Helper()
	ancestor = t.TempDir()
	vault = filepath.Join(ancestor, "workspace", "vault")
	if err := os.MkdirAll(filepath.Join(vault, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "a.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(vault, "Secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "Secrets", "keep.md"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath = filepath.Join(ancestor, "config.yaml")
	cfg := `version: 1
instance:
  id: e10t2-test
resources:
  vault-main:
    type: directory
    root: ` + vault + `
    file_scope: markdown
hermes_targets:
  hermes-main:
    board: agent-dispatch
    minimum_version: 0.19.1
    compatibility: capability_probe
    executable: ` + filepath.Join(ancestor, "hermes-stub") + `
routes:
  wiki:
    enabled: true
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: agent-dispatch.wiki.e10t2
      include: ["**/*.md"]
      exclude: ["Secrets", "Inbox/noise-*.md"]
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
        mutex_key: wiki-e10t2
        workstream: main
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
`
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_DISPATCH_STATE_DIR", filepath.Join(ancestor, "state"))
	return configPath, ancestor, vault
}

// bindingOf decodes the binding member of a lifecycle envelope.
func bindingOf(t *testing.T, res map[string]any) effectiveBinding {
	t.Helper()
	raw, err := json.Marshal(res["binding"])
	if err != nil {
		t.Fatal(err)
	}
	var b effectiveBinding
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("binding member wrong: %v (%s)", err, raw)
	}
	return b
}

// TestE10T2AncestorRootBindingLifecycle pins AC-601 and TST-010 against
// the real Watchman server over a disposable nested tree: watching the
// ancestor first makes the actual watch root an ancestor of the
// configured vault; install then reports and persists the four-part
// binding, installs the subtree-constrained trigger, status resolves the
// same binding, and remove proves the managed trigger absent on every
// applicable root.
func TestE10T2AncestorRootBindingLifecycle(t *testing.T) {
	if _, err := exec.LookPath("watchman"); err != nil {
		t.Skip("watchman binary not available")
	}
	configPath, ancestor, vault := e10t2Fixture(t)
	client := watchman.NewClient("")
	ctx := context.Background()

	// Watch the ancestor BEFORE install: watch-project reuses the
	// closest existing watch, so the configured nested root binds to the
	// ancestral actual root.
	actual, err := client.EnsureWatch(ctx, ancestor)
	if err != nil {
		t.Fatalf("watch ancestor: %v", err)
	}
	t.Cleanup(func() {
		_, _, errb, code := runWatchmanArgs(t, configPath, "remove", "--yes")
		if code != 0 {
			t.Logf("cleanup remove: %s", errb.String())
		}
		// The disposable tree's watch is dropped so repeated runs do not
		// accumulate FSEvent streams (the managed remove never touches
		// watch roots by design).
		if err := client.WatchDelete(context.Background(), actual); err != nil {
			t.Logf("cleanup watch-del %s: %v", actual, err)
		}
	})

	res, _, errb, code := runWatchmanArgs(t, configPath, "install")
	if code != 0 {
		t.Fatalf("install under an ancestor root failed: %s", errb.String())
	}
	if res["action"] != "installed" {
		t.Fatalf("install action wrong: %v", res)
	}
	binding := bindingOf(t, res)
	if filepath.Clean(binding.ActualRoot) != filepath.Clean(actual) {
		t.Fatalf("the actual root must be the watched ancestor: %+v (want %s)", binding, actual)
	}
	if binding.RelativeRoot != "workspace/vault" || binding.ConfiguredRoot != vault || binding.TriggerName != "agent-dispatch.wiki.e10t2" {
		t.Fatalf("the four binding values must be reported distinctly: %+v", binding)
	}
	// The installed definition carries the subtree constraint (SRC-011).
	defs, err := client.TriggerList(ctx, actual)
	if err != nil {
		t.Fatal(err)
	}
	installed, ok := watchman.FindTrigger(defs, "agent-dispatch.wiki.e10t2")
	if !ok || installed.RelativeRoot != "workspace/vault" {
		t.Fatalf("the trigger must be subtree-constrained: %+v", installed)
	}
	// The binding is durable (SRC-009): the persisted record round-trips.
	_, store, exit := openOperatorStore("watchman status", configPath, &bytes.Buffer{})
	if exit != 0 {
		t.Fatal("store open failed")
	}
	stored, err := store.LoadWatchBinding(ctx, "wiki")
	if err != nil || stored.ActualRoot != filepath.Clean(actual) || stored.RelativeRoot != "workspace/vault" {
		t.Fatalf("the persisted binding must hold the resolved values: %+v %v", stored, err)
	}
	store.Close()

	// Status resolves the identical effective binding (SRC-010).
	st, _, errb, code := runWatchmanArgs(t, configPath, "status")
	if code != 0 {
		t.Fatalf("status failed: %s", errb.String())
	}
	if st["state"] != "installed" {
		t.Fatalf("status state wrong: %v", st)
	}
	statusBinding := bindingOf(t, st)
	if statusBinding != binding {
		t.Fatalf("status must report the same binding install reported: %+v vs %+v", statusBinding, binding)
	}
	if _, has := st["include"]; !has {
		t.Fatalf("status must expose the effective patterns (OPS-010): %v", st)
	}

	// watchman test resolves the same binding from its logical root with
	// no server contact (SRC-010): the persisted record is reported
	// verbatim.
	te, _, errb, code := runWatchmanArgs(t, configPath, "test", "--route", "wiki", "--config", configPath)
	if code != 0 {
		t.Fatalf("watchman test --route failed: %s", errb.String())
	}
	testBinding := bindingOf(t, te)
	if testBinding != binding {
		t.Fatalf("test must report the persisted binding: %+v vs %+v", testBinding, binding)
	}

	// Remove proves absence on every applicable root (AC-603): the
	// managed trigger is additionally planted on a second watched root —
	// exactly the changed-topology leftover the proof exists for — and
	// the removal must find and delete it there too.
	// A second watched root carrying the same managed trigger name: the
	// removal proof must cover it (SRC-012's changed-topology arm).
	second := t.TempDir()
	if err := os.WriteFile(filepath.Join(second, "park.md"), []byte("park"), 0o644); err != nil {
		t.Fatal(err)
	}
	secondRoot, err := client.EnsureWatch(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.WatchDelete(context.Background(), secondRoot); err != nil {
			t.Logf("cleanup second watch-del %s: %v", secondRoot, err)
		}
	})
	stray := watchman.ManagedTrigger("agent-dispatch.wiki.e10t2", []string{"/bin/true"}, "")
	if _, err := client.TriggerInstall(ctx, secondRoot, stray); err != nil {
		t.Fatalf("plant stray trigger: %v", err)
	}
	proof, _, errb, code := runWatchmanArgs(t, configPath, "remove", "--yes")
	if code != 0 {
		t.Fatalf("remove failed: %s", errb.String())
	}
	roots := map[string]bool{}
	list, _ := proof["proof"].([]any)
	for _, entry := range list {
		if m, ok := entry.(map[string]any); ok {
			if m["present"] != false {
				t.Fatalf("the proof must show absence everywhere: %v", entry)
			}
			roots[m["watch_root"].(string)] = true
		}
	}
	if !roots[filepath.Clean(actual)] {
		t.Fatalf("the proof must cover the recorded ancestor root %s: %v", actual, roots)
	}
	if !roots[filepath.Clean(secondRoot)] {
		t.Fatalf("the proof must cover every watched root including %s: %v", secondRoot, roots)
	}
	defs2, err := client.TriggerList(ctx, secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := watchman.FindTrigger(defs2, "agent-dispatch.wiki.e10t2"); present {
		t.Fatal("the stray managed trigger must be removed from the second root")
	}
	defs, err = client.TriggerList(ctx, actual)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := watchman.FindTrigger(defs, "agent-dispatch.wiki.e10t2"); present {
		t.Fatal("the managed trigger must be gone from the ancestor root")
	}
}

// runWatchmanArgs drives one watchman subcommand through the CLI.
func runWatchmanArgs(t *testing.T, configPath, sub string, extra ...string) (map[string]any, *bytes.Buffer, *bytes.Buffer, int) {
	t.Helper()
	var out, errb bytes.Buffer
	args := append([]string{"watchman", sub, "--route", "wiki", "--config", configPath}, extra...)
	code := Run(args, &out, &errb)
	if code == 0 && out.Len() == 0 {
		t.Fatalf("watchman %s produced no envelope: %s", sub, errb.String())
	}
	var decoded map[string]any
	if code == 0 {
		decoded = decodeEnvelope(t, &out)
	}
	return decoded, &out, &errb, code
}

// TestE10T2DriftDetection pins OPS-010's drifted state: a persisted
// binding that no longer matches the live watch topology (the actual
// root moved) surfaces as drifted, independent of the trigger
// definition comparison.
func TestE10T2DriftDetection(t *testing.T) {
	if _, err := exec.LookPath("watchman"); err != nil {
		t.Skip("watchman binary not available")
	}
	configPath, _, vault := e10t2Fixture(t)
	if _, _, errb, code := runWatchmanArgs(t, configPath, "install"); code != 0 {
		t.Fatalf("install failed: %s", errb.String())
	}
	t.Cleanup(func() {
		_, _, _, _ = runWatchmanArgs(t, configPath, "remove", "--yes")
		if client := watchman.NewClient(""); client != nil {
			_ = client.WatchDelete(context.Background(), vault)
		}
	})
	// Simulate topology drift exactly as a re-watched ancestor would:
	// the persisted actual root no longer matches the live one.
	_, store, exit := openOperatorStore("watchman status", configPath, &bytes.Buffer{})
	if exit != 0 {
		t.Fatal("store open failed")
	}
	binding, err := store.LoadWatchBinding(context.Background(), "wiki")
	if err != nil {
		t.Fatal(err)
	}
	binding.ActualRoot = filepath.Join(filepath.Dir(vault), "gone-ancestor")
	binding.RelativeRoot = "vault"
	if err := store.SaveWatchBinding(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	store.Close()
	st, _, errb, code := runWatchmanArgs(t, configPath, "status")
	if code != 0 {
		t.Fatalf("status failed: %s", errb.String())
	}
	if st["state"] != "drifted" {
		t.Fatalf("a moved actual root must surface as drifted: %v", st)
	}
	_ = errb
}

// TestE10T2ExcludedAndOutOfRootChangesCreateNoRecords pins AC-602 at the
// dispatch layer: an out-of-root path and every exclusion form produce
// no event record, no child task, and no hash — the batch is dropped
// before any read (PTH-009).
func TestE10T2ExcludedAndOutOfRootChangesCreateNoRecords(t *testing.T) {
	configPath, _, vault := e10t2Fixture(t)
	t.Setenv("WATCHMAN_TRIGGER", "agent-dispatch.wiki.e10t2")
	t.Setenv("WATCHMAN_ROOT", vault)
	t.Setenv("WATCHMAN_SINCE", "c:1:2:3:3")
	t.Setenv("WATCHMAN_CLOCK", "c:1:2:3:4")
	// The exclusion forms: the exact directory and the file glob, both
	// configured-root-relative (the relative-root environment makes the
	// payload names configured-root-relative exactly as a subtree trigger
	// delivers them).
	payload := `[` +
		`{"name":"Secrets/keep.md","exists":true,"new":true,"size":6,"type":"f"},` +
		`{"name":"Inbox/noise-1.md","exists":true,"new":true,"size":4,"type":"f"}` + `]`
	// The durable dispatch path needs the route registration install
	// would have materialized (the observation and drop lineage reference
	// it).
	_, regStore, regExit := openOperatorStore("dispatch", configPath, &bytes.Buffer{})
	if regExit != 0 {
		t.Fatal("store open failed")
	}
	if cfg, err := config.Load(configPath); err != nil {
		t.Fatal(err)
	} else if err := registerRouteState(requestCtx(), regStore, cfg, "wiki"); err != nil {
		t.Fatal(err)
	}
	regStore.Close()
	var out, errb bytes.Buffer
	withStdin(t, payload, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("excluded-only dispatch must not fail: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["disposition"] != "drop" {
		t.Fatalf("an excluded-only burst must drop: %v", res)
	}
	if changes, _ := res["changes"].([]any); len(changes) != 0 {
		t.Fatalf("no change may survive the exclusions: %v", changes)
	}
	_, store, exit := openOperatorStore("dispatch", configPath, &bytes.Buffer{})
	if exit != 0 {
		t.Fatal("store open failed")
	}
	defer store.Close()
	var observations, intents int
	if err := store.QueryRow(`SELECT COUNT(*) FROM source_observations`).Scan(&observations); err != nil || observations != 1 {
		t.Fatalf("exactly the drop-observation may exist: %d %v", observations, err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents); err != nil || intents != 0 {
		t.Fatalf("an excluded burst must create no task: %d %v", intents, err)
	}
	var hashed int
	if err := store.QueryRow(`SELECT COUNT(*) FROM observation_changes WHERE after_digest IS NOT NULL`).Scan(&hashed); err != nil || hashed != 0 {
		t.Fatalf("an excluded path must never be hashed: %d %v", hashed, err)
	}
}

// TestE10T2DispatchValidatesAncestorBinding pins the dispatch-side
// defense (SRC-011): a trigger environment from the ancestral root
// binds only through the exact persisted record; without it the
// dispatch fails closed, and with it the configured-root-relative
// payload flows.
func TestE10T2DispatchValidatesAncestorBinding(t *testing.T) {
	configPath, ancestor, vault := e10t2Fixture(t)
	payload := `[{"name":"Inbox/a.md","exists":true,"new":true,"size":5,"type":"f"}]`
	rel := "workspace/vault"
	// The frozen-evidence absolute form a real subtree trigger delivers.
	t.Setenv("WATCHMAN_ROOT", ancestor)
	t.Setenv("WATCHMAN_RELATIVE_ROOT", vault)
	t.Setenv("WATCHMAN_TRIGGER", "agent-dispatch.wiki.e10t2")
	t.Setenv("WATCHMAN_SINCE", "c:1:2:3:3")
	t.Setenv("WATCHMAN_CLOCK", "c:1:2:3:4")

	// Without the persisted binding the ancestor environment fails
	// closed — even though the join would reach the configured root.
	var out, errb bytes.Buffer
	withStdin(t, payload, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if !strings.Contains(errb.String(), "source_binding_mismatch") {
		t.Fatalf("an ancestor environment without the persisted binding must fail closed: %s", errb.String())
	}

	// Persisting the exact binding opens the ancestral dispatch path;
	// the binding references the route registration install would have
	// materialized, so it is registered first.
	_, store, exit := openOperatorStore("dispatch", configPath, &bytes.Buffer{})
	if exit != 0 {
		t.Fatal("store open failed")
	}
	cfgRoute, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerRouteState(requestCtx(), store, cfgRoute, "wiki"); err != nil {
		t.Fatal(err)
	}
	// The enable gate the operator acknowledges in production (E8-T3).
	rev, ok := config.RouteRevision(cfgRoute, "wiki")
	if !ok {
		t.Fatal("route wiki revision could not be computed")
	}
	if err := store.SetRouteActivation(context.Background(), "wiki", "enabled", rev, "2026-08-26T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWatchBinding(context.Background(), watchman.Binding{
		RouteID: "wiki", ResourceID: "vault-main",
		ConfiguredRoot: vault, ActualRoot: ancestor,
		RelativeRoot: rel, TriggerName: "agent-dispatch.wiki.e10t2",
		UpdatedAt: "2026-08-26T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	out.Reset()
	errb.Reset()
	withStdin(t, payload, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("the persisted ancestor binding must validate: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["dispatch_id"] == nil || res["state"] != "ready" {
		t.Fatalf("the configured-root-relative payload must flow: %v", res)
	}
	// A drifted binding (someone re-watched a different ancestor) fails
	// closed again.
	_, store, _ = openOperatorStore("dispatch", configPath, &bytes.Buffer{})
	drifted, _ := store.LoadWatchBinding(context.Background(), "wiki")
	drifted.RelativeRoot = "other/vault"
	if err := store.SaveWatchBinding(context.Background(), drifted); err != nil {
		t.Fatal(err)
	}
	store.Close()
	out.Reset()
	errb.Reset()
	withStdin(t, payload, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if !strings.Contains(errb.String(), "source_binding_mismatch") {
		t.Fatalf("a drifted relative root must fail closed: %s", errb.String())
	}
	_ = sqlite.ErrWatchBindingNotFound
}

// TestMain guards the real-trigger lifecycle tests: a managed Watchman
// trigger installed by a test fires this test binary as its command
// (managedCommand pins os.Executable()), and a Go test binary invoked
// directly runs the whole suite — recursively, with every nested run
// installing and firing more triggers. Serving the trigger-shaped
// invocation as a documented no-op keeps the lifecycle evidence honest
// without the cascade (E10-T3; the managed command's production argv is
// served by the installed binary, which a test binary cannot be).
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "dispatch" {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

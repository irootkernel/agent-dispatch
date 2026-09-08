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

	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
	"github.com/irootkernel/agent-dispatch/internal/config"
)

// watchmanFixture writes one enabled watchman route whose configured vault
// root is nested under a disposable ancestor directory, and returns the
// config path, the ancestor, and the nested vault root.
func watchmanFixture(t *testing.T) (configPath, ancestor, vault string) {
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
  id: e18t1-test
resources:
  vault-main:
    type: directory
    root: ` + vault + `
    file_scope: markdown
hermes_targets:
  hermes-main:
    board: agent-dispatch
    minimum_version: 0.20.5
    compatibility: capability_probe
    executable: ` + filepath.Join(ancestor, "hermes-stub") + `
routes:
  wiki:
    enabled: true
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: agent-dispatch.wiki.e18t1
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
        mutex_key: wiki-e18t1
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

// TestE18T1ExactRootBindingLifecycle pins AC-601 (amended by D-028) and
// TST-010 against the real Watchman server over a disposable tree with
// the live failure's topology: the ancestor is watched FIRST, and the
// managed watch command must still establish the configured absolute
// root as its own watch root — install reports and persists the
// exact-root binding with the schema-vestigial ".", the installed
// trigger carries no relative_root, status resolves the same binding,
// and remove proves the managed trigger absent.
func TestE18T1ExactRootBindingLifecycle(t *testing.T) {
	if _, err := exec.LookPath("watchman"); err != nil {
		t.Skip("watchman binary not available")
	}
	configPath, ancestor, vault := watchmanFixture(t)
	client := watchman.NewClient("")
	ctx := context.Background()

	// Watch the ancestor BEFORE install — exactly the operator-host
	// topology that produced the live source_binding_mismatch.
	if _, err := client.EnsureWatch(ctx, ancestor); err != nil {
		t.Fatalf("watch ancestor: %v", err)
	}
	t.Cleanup(func() {
		_, _, errb, code := runWatchmanArgs(t, configPath, "remove", "--yes")
		if code != 0 {
			t.Logf("cleanup remove: %s", errb.String())
		}
		// The disposable trees' watches are dropped so repeated runs do
		// not accumulate FSEvent streams (the managed remove never
		// touches watch roots by design).
		for _, root := range []string{vault, ancestor} {
			if err := client.WatchDelete(context.Background(), root); err != nil {
				t.Logf("cleanup watch-del %s: %v", root, err)
			}
		}
	})

	res, _, errb, code := runWatchmanArgs(t, configPath, "install")
	if code != 0 {
		t.Fatalf("install under a watched ancestor failed: %s", errb.String())
	}
	if res["action"] != "installed" {
		t.Fatalf("install action wrong: %v", res)
	}
	watchRoot, _ := res["watch_root"].(string)
	if watchRoot == "" {
		t.Fatalf("install must report the watch root: %v", res)
	}
	binding := bindingOf(t, res)
	if watchman.CanonicalRoot(binding.ActualRoot) != watchman.CanonicalRoot(vault) ||
		binding.RelativeRoot != "." || binding.ConfiguredRoot != vault || binding.TriggerName != "agent-dispatch.wiki.e18t1" {
		t.Fatalf("the binding must be exact-root with the vestigial '.': %+v", binding)
	}
	// The installed definition carries no relative_root (SRC-011,
	// amended by D-028).
	defs, err := client.TriggerList(ctx, watchRoot)
	if err != nil {
		t.Fatal(err)
	}
	installed, ok := watchman.FindTrigger(defs, "agent-dispatch.wiki.e18t1")
	if !ok || installed.RelativeRoot != "" {
		t.Fatalf("the trigger must never be subtree-constrained: %+v", installed)
	}
	// The binding is durable (SRC-009): the persisted record round-trips
	// the exact root and the vestigial relative root.
	_, store, exit := openOperatorStore("watchman status", configPath, &bytes.Buffer{})
	if exit != 0 {
		t.Fatal("store open failed")
	}
	stored, err := store.LoadWatchBinding(ctx, "wiki")
	if err != nil || watchman.CanonicalRoot(stored.ActualRoot) != watchman.CanonicalRoot(vault) || stored.RelativeRoot != "." {
		t.Fatalf("the persisted binding must hold the exact-root values: %+v %v", stored, err)
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
	if statusBinding := bindingOf(t, st); statusBinding != binding {
		t.Fatalf("status must report the same binding install reported: %+v vs %+v", statusBinding, binding)
	}
	if _, has := st["include"]; !has {
		t.Fatalf("status must expose the effective patterns (OPS-010): %v", st)
	}

	// watchman test resolves the same binding from the persisted record
	// with no server contact (SRC-010).
	te, _, errb, code := runWatchmanArgs(t, configPath, "test", "--route", "wiki", "--config", configPath)
	if code != 0 {
		t.Fatalf("watchman test --route failed: %s", errb.String())
	}
	if testBinding := bindingOf(t, te); testBinding != binding {
		t.Fatalf("test must report the persisted binding: %+v vs %+v", testBinding, binding)
	}

	// Remove proves absence on the exact watch root (SRC-012's baseline).
	proof, _, errb, code := runWatchmanArgs(t, configPath, "remove", "--yes")
	if code != 0 {
		t.Fatalf("remove failed: %s", errb.String())
	}
	list, _ := proof["proof"].([]any)
	for _, entry := range list {
		if m, ok := entry.(map[string]any); ok && m["present"] != false {
			t.Fatalf("the proof must show absence everywhere: %v", entry)
		}
	}
	defs2, err := client.TriggerList(ctx, watchRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := watchman.FindTrigger(defs2, "agent-dispatch.wiki.e18t1"); present {
		t.Fatal("the managed trigger must be gone from the exact watch root")
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

// TestE18T1DriftDetection pins OPS-010's drifted state: a persisted
// binding whose actual root no longer matches the live exact root (the
// pre-E18 ancestor binding is exactly this shape) surfaces as drifted,
// independent of the trigger definition comparison.
func TestE18T1DriftDetection(t *testing.T) {
	if _, err := exec.LookPath("watchman"); err != nil {
		t.Skip("watchman binary not available")
	}
	configPath, _, vault := watchmanFixture(t)
	if _, _, errb, code := runWatchmanArgs(t, configPath, "install"); code != 0 {
		t.Fatalf("install failed: %s", errb.String())
	}
	t.Cleanup(func() {
		_, _, _, _ = runWatchmanArgs(t, configPath, "remove", "--yes")
		if client := watchman.NewClient(""); client != nil {
			_ = client.WatchDelete(context.Background(), vault)
		}
	})
	// Simulate the pre-E18 persisted ancestor binding: the stored actual
	// root is not the live exact root.
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
		t.Fatalf("a stale ancestor binding must surface as drifted: %v", st)
	}
	_ = errb
}

// TestE18T1ExcludedAndOutOfRootChangesCreateNoRecords pins AC-602 at the
// dispatch layer: an out-of-root path and every exclusion form produce
// no event record, no child task, and no hash — the batch is dropped
// before any read (PTH-009).
func TestE18T1ExcludedAndOutOfRootChangesCreateNoRecords(t *testing.T) {
	configPath, _, vault := watchmanFixture(t)
	t.Setenv("WATCHMAN_TRIGGER", "agent-dispatch.wiki.e18t1")
	t.Setenv("WATCHMAN_ROOT", vault)
	t.Setenv("WATCHMAN_SINCE", "c:1:2:3:3")
	t.Setenv("WATCHMAN_CLOCK", "c:1:2:3:4")
	// The exclusion forms: the exact directory and the file glob, both
	// configured-root-relative exactly as an exact-root trigger delivers
	// them.
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

// TestE18T1DispatchValidatesExactRootBinding pins the dispatch-side
// defense (SRC-013): only a WATCHMAN_ROOT that canonicalizes to the
// configured resource root is accepted — with no persisted-binding
// second axis — and both an ancestor root and a present
// WATCHMAN_RELATIVE_ROOT fail closed even when the ancestor binding is
// exactly what is persisted.
func TestE18T1DispatchValidatesExactRootBinding(t *testing.T) {
	configPath, ancestor, vault := watchmanFixture(t)
	payload := `[{"name":"Inbox/a.md","exists":true,"new":true,"size":5,"type":"f"}]`

	// The durable dispatch path needs the route registration install
	// would have materialized; the enable gate the operator acknowledges
	// in production (E8-T3).
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
	rev, ok := config.RouteRevision(cfgRoute, "wiki")
	if !ok {
		t.Fatal("route wiki revision could not be computed")
	}
	if err := store.SetRouteActivation(context.Background(), "wiki", "enabled", rev, "", "2026-08-26T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	// The pre-E18 ancestor binding is persisted exactly as the old
	// contract stored it; it must not open any ancestor dispatch path.
	if err := store.SaveWatchBinding(context.Background(), watchman.Binding{
		RouteID: "wiki", ResourceID: "vault-main",
		ConfiguredRoot: vault, ActualRoot: ancestor,
		RelativeRoot: "workspace/vault", TriggerName: "agent-dispatch.wiki.e18t1",
		UpdatedAt: "2026-08-26T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	t.Setenv("WATCHMAN_TRIGGER", "agent-dispatch.wiki.e18t1")
	t.Setenv("WATCHMAN_SINCE", "c:1:2:3:3")
	t.Setenv("WATCHMAN_CLOCK", "c:1:2:3:4")

	// An ancestor environment — with or without the frozen-evidence
	// relative root — fails closed against the persisted ancestor
	// binding: the configuration is the only trust anchor.
	for _, env := range []map[string]string{
		{"WATCHMAN_ROOT": ancestor},
		{"WATCHMAN_ROOT": ancestor, "WATCHMAN_RELATIVE_ROOT": vault},
	} {
		for k, v := range env {
			t.Setenv(k, v)
		}
		var out, errb bytes.Buffer
		withStdin(t, payload, func() {
			Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
		})
		if !strings.Contains(errb.String(), "source_binding_mismatch") {
			t.Fatalf("an ancestor environment must fail closed despite the persisted binding: %s (env %v)", errb.String(), env)
		}
	}

	// A present WATCHMAN_RELATIVE_ROOT fails closed even with the exact
	// root: it is the signature of a stale relative-root trigger.
	t.Setenv("WATCHMAN_ROOT", vault)
	t.Setenv("WATCHMAN_RELATIVE_ROOT", vault)
	var out, errb bytes.Buffer
	withStdin(t, payload, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if !strings.Contains(errb.String(), "source_binding_mismatch") {
		t.Fatalf("a present relative root must fail closed as stale: %s", errb.String())
	}

	// The exact root flows without consulting any persisted binding.
	os.Unsetenv("WATCHMAN_RELATIVE_ROOT")
	out.Reset()
	errb.Reset()
	withStdin(t, payload, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("the exact-root environment must flow: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["dispatch_id"] == nil || res["state"] != "ready" {
		t.Fatalf("the configured-root-relative payload must flow: %v", res)
	}
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

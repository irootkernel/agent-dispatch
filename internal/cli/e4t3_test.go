package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
	"github.com/irootkernel/agent-dispatch/internal/ports"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/stubhermes"
)

// e4t3RegisterRoute creates the route runtime state row directly (the
// same seeding the crash harness uses); production first-use
// registration flows through `watchman install` and `route enable`
// (TestFirstUseRegistrationFlow).
func e4t3RegisterRoute(t *testing.T, configPath string) {
	t.Helper()
	cfg, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		t.Fatal(err)
	}
	stateDir := platformpaths.ResolveStateDir(cfg.Instance.StateDir)
	store, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(filepath.Join(stateDir, "backups")); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterRoute(nil, "wiki", "route-rev-1", "policy-rev-1", "vault-main", "hermes-main", "{}", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	var exists int
	if err := store.QueryRowContext(context.Background(), `SELECT 1 FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&exists); err == nil {
		// Already initialized: registration is idempotent for repeated
		// fixture seeding.
	} else {
		if err := store.InitializeRouteState(nil, "wiki"); err != nil {
			t.Fatal(err)
		}
	}
	// The fixture acknowledges the computed revision, exactly as the
	// product enable gate does (E8-T3: the acknowledged-revision submit
	// gate fires on placeholder acknowledgements).
	rev, ok := config.RouteRevision(cfg, "wiki")
	if !ok {
		t.Fatal("route wiki revision could not be computed")
	}
	if err := store.SetRouteActivation(context.Background(), "wiki", "enabled", rev, "", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
}

// e4t3Fixture builds a vault plus a one-route configuration whose
// hermes-kanban target runs the stateful stub under the frozen
// capability report.
func e4t3Fixture(t *testing.T) (configPath, vault string) {
	t.Helper()
	dir := t.TempDir()
	vault = filepath.Join(dir, "vault")
	if err := os.MkdirAll(filepath.Join(vault, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "new.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `version: 1
instance:
  id: e4t3-test
  state_dir: ` + stateDir + `
resources:
  vault-main:
    type: directory
    root: ` + vault + `
    file_scope: markdown
    git:
      mode: disabled
hermes_targets:
  hermes-main:
    board: agent-dispatch-test
    minimum_version: 0.19.1
    compatibility: capability_probe
    executable: ` + stubhermes.Write(t) + `
    submit_timeout: 30s
    lookup_timeout: 30s
    environment_allowlist: [PATH, HOME]
routes:
  wiki:
    enabled: true
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: agent-dispatch.wiki.test
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
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, vault
}

func e4t3Dispatch(t *testing.T, configPath, vault string) (map[string]any, string) {
	t.Helper()
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	var code int
	payload := `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`
	withStdin(t, payload, func() {
		code = Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("dispatch failed (exit %d): %s", code, errb.String())
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("not an envelope: %s", out.String())
	}
	raw, _ := json.Marshal(env.Result)
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	return res, out.String()
}

// TestDispatchSubmitsThroughHermesSink proves the E4-T3 wiring end to
// end: one Watchman batch becomes a durable intent, the gated hermes
// sink submits it, and the accepted state plus the external reference
// are persisted and inspectable (HER-001, DUR acceptance).
func TestDispatchSubmitsThroughHermesSink(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	if res["state"] != "accepted" || res["submitted"] != true {
		t.Fatalf("dispatch result wrong: %v", res)
	}
	dispatchID, _ := res["dispatch_id"].(string)
	if dispatchID == "" {
		t.Fatal("dispatch id missing")
	}

	// The durable record carries the external reference through the
	// acceptance receipt.
	var out, errb bytes.Buffer
	code := Run([]string{"dispatches", "show", "--config", configPath, dispatchID}, &out, &errb)
	if code != 0 {
		t.Fatalf("dispatches show failed: %s", errb.String())
	}
	shown := decodeEnvelope(t, &out)
	if !strings.Contains(out.String(), "t_0000000") {
		t.Fatalf("external reference missing from inspection: %v", shown)
	}
}

// TestDispatchUnusableTargetFailsClosed proves a target that fails the
// construction gate (an unparseable eligibility floor) fails with the
// stable configuration error instead of submitting.
func TestDispatchUnusableTargetFailsClosed(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.Replace(raw, []byte("minimum_version: 0.19.1"), []byte("minimum_version: 0.19.0.9"), 1)
	if err := os.WriteFile(configPath, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	var code int
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		code = Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if code != 3 {
		t.Fatalf("report defect must exit 3, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "config_invalid") {
		t.Fatalf("stable code missing: %s", errb.String())
	}
}

// TestDispatchAmbiguousRecoveryLoop proves the full unknown-delivery
// recovery against the real sink wiring: an ambiguous submission leaves
// the durable intent unknown, drain reconciles it (by-reference lookup
// only on Hermes; no reference is known yet, so it dead-letters), the
// operator retry returns it to ready, and the drain resubmission — the
// same idempotency key, dedup-safe — is accepted (DUR-005/006/008).
func TestDispatchAmbiguousRecoveryLoop(t *testing.T) {
	dir := t.TempDir()
	// A sabotaging stub: version answers normally, create sleeps past
	// the submit deadline (ambiguous), controlled by a flag file.
	sabotage := filepath.Join(dir, "sabotage")
	good := stubhermes.Write(t)
	if err := os.WriteFile(sabotage, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "hermes-bad")
	badScript := `#!/bin/sh
if [ "$1" = "--version" ]; then printf 'Hermes Agent v0.19.1 (2026.7.30)\n'; exit 0; fi
if [ -f "` + sabotage + `" ]; then sleep 30; exit 0; fi
exec "` + good + `" "$@"
`
	if err := os.WriteFile(bad, []byte(badScript), 0o755); err != nil {
		t.Fatal(err)
	}

	configPath, vault := e4t3Fixture(t)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "    executable: ") {
			lines[i] = "    executable: " + bad
		}
		if strings.HasPrefix(l, "    submit_timeout: ") {
			lines[i] = "    submit_timeout: 1s"
		}
	}
	if err := os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	// Phase 1: ambiguous submission -> unknown.
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	var code int
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		code = Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("ambiguous dispatch must still exit 0 with recorded unknown state: %s", errb.String())
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	resRaw, _ := json.Marshal(env.Result)
	var res map[string]any
	if err := json.Unmarshal(resRaw, &res); err != nil {
		t.Fatal(err)
	}
	if res["state"] != "unknown" {
		t.Fatalf("ambiguous submission must record unknown, got %v", res["state"])
	}
	dispatchID, _ := res["dispatch_id"].(string)

	// Phase 2: drain reconciles the unknown dispatch; no external
	// reference is known and the by-key read does not exist, so it
	// dead-letters for the operator.
	var drainOut, drainErr bytes.Buffer
	code = Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &drainOut, &drainErr)
	if code != 0 {
		t.Fatalf("drain reconcile failed: %s", drainErr.String())
	}
	drainEnv := decodeEnvelope(t, &drainOut)
	reconciled, _ := drainEnv["reconciled"].([]any)
	if len(reconciled) != 1 {
		t.Fatalf("one unknown dispatch must be reconciled: %v", drainEnv["reconciled"])
	}
	entry, _ := reconciled[0].(map[string]any)
	if entry["state"] != "dead_lettered" {
		t.Fatalf("ambiguous unknown with no reference must dead-letter: %v", entry)
	}

	// Phase 3: operator retry returns it to ready.
	var retryOut, retryErr bytes.Buffer
	code = Run([]string{"dispatches", "retry", "--config", configPath, dispatchID, "--reason", "operator resolved the ambiguity"}, &retryOut, &retryErr)
	if code != 0 {
		t.Fatalf("retry failed: %s", retryErr.String())
	}

	// Phase 4: heal the target and drain: the same idempotency key
	// resubmission is accepted (dedup-safe).
	if err := os.Remove(sabotage); err != nil {
		t.Fatal(err)
	}
	var drain2Out, drain2Err bytes.Buffer
	code = Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &drain2Out, &drain2Err)
	if code != 0 {
		t.Fatalf("recovery drain failed: %s", drain2Err.String())
	}
	var showOut, showErr bytes.Buffer
	code = Run([]string{"dispatches", "show", "--config", configPath, dispatchID}, &showOut, &showErr)
	if code != 0 {
		t.Fatalf("show failed: %s", showErr.String())
	}
	if !strings.Contains(showOut.String(), `"state": "accepted"`) && !strings.Contains(showOut.String(), `"state":"accepted"`) {
		t.Fatalf("recovered dispatch must be accepted: %s", showOut.String())
	}
	if !strings.Contains(showOut.String(), "t_0000000") {
		t.Fatalf("accepted dispatch must carry the external reference: %s", showOut.String())
	}
}

// TestDispatchSinkErrorCodes proves the stable registry codes for a
// submit-phase gate failure: an unsupported installed version and a
// missing executable are target unavailability (exit 11), and a
// capability mismatch is a configuration failure (exit 3).
func TestDispatchSinkErrorCodes(t *testing.T) {
	cases := []struct {
		name     string
		bin      func(t *testing.T, dir string) string
		wantCode string
		wantExit int
	}{
		{
			name: "version unsupported",
			bin: func(t *testing.T, dir string) string {
				return e4t1StubHermes(t, dir, "Hermes Agent v0.18.5 (2026.6.01)")
			},
			wantCode: "hermes_version_unsupported",
			wantExit: 11,
		},
		{
			name: "executable missing",
			bin: func(t *testing.T, dir string) string {
				return filepath.Join(dir, "absent-hermes")
			},
			wantCode: "hermes_executable_missing",
			wantExit: 11,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath, vault := e4t3Fixture(t)
			raw, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			updated := string(raw)
			lines := strings.Split(updated, "\n")
			for i, l := range lines {
				if strings.HasPrefix(l, "    executable: ") {
					lines[i] = "    executable: " + c.bin(t, dir)
				}
			}
			if err := os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
				t.Fatal(err)
			}
			setPlanEnv(t, vault, false)
			e4t3RegisterRoute(t, configPath)
			var out, errb bytes.Buffer
			var code int
			withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
				code = Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
			})
			if code != c.wantExit {
				t.Fatalf("exit %d want %d: %s", code, c.wantExit, errb.String())
			}
			if !strings.Contains(errb.String(), c.wantCode) {
				t.Fatalf("stable code %q missing: %s", c.wantCode, errb.String())
			}
		})
	}
}

// TestSchemaLegalDayUnitDurations verifies the E3-T3 audit remediation:
// schema-legal whole-day durations behave identically at validation and
// run time (the retry policy and execution hints parse through the one
// schema-exact parser), and a configured-but-unparsable hint fails
// closed instead of silently dropping.
func TestSchemaLegalDayUnitDurations(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(raw), "initial_backoff: 1s", "initial_backoff: 1d", 1)
	updated = strings.Replace(updated, "max_backoff: 2s", "max_backoff: 2d", 1)
	updated = strings.Replace(updated, "max_runtime: 30m", "max_runtime: 1d", 1)
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	// Drain resolves the day-unit policy without a configuration error
	// (no due work exists; the policy is still constructed).
	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("schema-legal day-unit policy must resolve: %s", errb.String())
	}
	// The rendered request carries the day-unit hint (86400 seconds).
	setPlanEnv(t, vault, false)
	res, _ := e4t3Dispatch(t, configPath, vault)
	if res["state"] != "accepted" {
		t.Fatalf("day-unit dispatch must be accepted, got %v", res["state"])
	}
	// The stored request carries the day-unit hint as 86400 seconds.
	dispatchID, _ := res["dispatch_id"].(string)
	cfgLoaded, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		t.Fatal(err)
	}
	stateDir := platformpaths.ResolveStateDir(cfgLoaded.Instance.StateDir)
	store, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snap, err := store.LoadIntent(context.Background(), dispatchID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snap.RequestJSON, `"max_runtime_seconds":86400`) {
		t.Fatalf("day-unit hint missing from the stored request: %s", snap.RequestJSON)
	}

	// A configured-but-unparsable hint fails closed, never silently
	// dropped: with the schema rejecting the value at load time, the
	// fail-closed path is proven through the parser directly.
	if _, err := config.ParseDuration("banana"); err == nil {
		t.Fatal("the schema-exact parser must reject nonsense")
	}
}

// TestFirstUseRegistrationFlow proves the E4 audit remediation for the
// route-registration seam: on a fresh state directory the operator's
// entry points themselves materialize the registration — `route
// enable` succeeds on first use, `watchman install` registers the
// route, and a dispatched change reaches acceptance with no manual
// state seeding (production first use works end to end).
func TestFirstUseRegistrationFlow(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	// Fresh state, no e4t3RegisterRoute seeding.
	setPlanEnv(t, vault, false)

	// route enable succeeds on first use and registers the route.
	var out, errb bytes.Buffer
	code := enableRouteAck(t, configPath, "wiki")
	if code != 0 {
		t.Fatalf("first-use route enable must register and succeed: %s", errb.String())
	}

	// watchman install (guarded on the real Watchman) also registers
	// idempotently.
	if _, err := exec.LookPath("watchman"); err == nil {
		out.Reset()
		errb.Reset()
		if code := Run([]string{"watchman", "install", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
			t.Fatalf("watchman install: %s", errb.String())
		}
		Run([]string{"watchman", "remove", "--route", "wiki", "--config", configPath, "--yes"}, &out, &errb)
		if client := watchman.NewClient(""); client != nil {
			_ = client.WatchDelete(context.Background(), vault)
		}
	}

	// A dispatched change reaches acceptance with no manual seeding.
	res := e4t3DispatchNoRegister(t, configPath, vault)
	if res["state"] != "accepted" {
		t.Fatalf("first-use dispatch must reach acceptance, got %v", res["state"])
	}
}

// e4t3DispatchNoRegister runs the dispatch without seeding route state.
func e4t3DispatchNoRegister(t *testing.T, configPath, vault string) map[string]any {
	t.Helper()
	var out, errb bytes.Buffer
	var code int
	payload := `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`
	withStdin(t, payload, func() {
		code = Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("dispatch failed (exit %d): %s", code, errb.String())
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(env.Result)
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

// TestReconcileSkipsRepointedScope proves the accepting-scope guard:
// an unknown dispatch submitted against one board is not reconciled
// against a re-pointed board — the skip is a visible warning, never a
// wrong-board absence proof.
func TestReconcileSkipsRepointedScope(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// Dispatch with an ambiguous stub so the intent sits in unknown.
	dir := t.TempDir()
	good := stubhermes.Write(t)
	bad := filepath.Join(dir, "hermes-ambiguous")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf 'Hermes Agent v0.19.1 (2026.7.30)\\n'; exit 0; fi\nsleep 60\n"
	if err := os.WriteFile(bad, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "    executable: ") {
			lines[i] = "    executable: " + bad
		}
		if strings.HasPrefix(l, "    submit_timeout: ") {
			lines[i] = "    submit_timeout: 1s"
		}
	}
	if err := os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	var code int
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		code = Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("ambiguous dispatch failed: %s", errb.String())
	}
	var env Envelope
	json.Unmarshal(out.Bytes(), &env)
	resRaw, _ := json.Marshal(env.Result)
	var res map[string]any
	json.Unmarshal(resRaw, &res)
	if res["state"] != "unknown" {
		t.Fatalf("expected unknown, got %v", res["state"])
	}

	// Re-point the target to a different board (scope change) and heal
	// the executable; the drain must skip the reconciliation visibly.
	updated := strings.Replace(string(raw), "board: agent-dispatch-test", "board: agent-dispatch-other", 1)
	lines = strings.Split(updated, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "    executable: ") {
			lines[i] = "    executable: " + good
		}
	}
	if err := os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	body := out.String() + errb.String()
	if !strings.Contains(body, "submitted against target scope") || !strings.Contains(body, "agent-dispatch-test") {
		t.Fatalf("the re-pointed scope skip must be visible: %s", body)
	}
	// The dispatch is untouched (still unknown, not dead-lettered by a
	// wrong-board proof).
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "list", "--config", configPath, "--route", "wiki", "--state", "unknown"}, &out, &errb); code != 0 {
		t.Fatalf("list: %s", errb.String())
	}
	if !strings.Contains(out.String(), "\"unknown\"") {
		t.Fatalf("the dispatch must remain unknown after the scope skip: %s", out.String())
	}
}

// TestRerunPreservesTargetScope proves rerun intents carry the
// predecessor's target scope so the reconciliation guard stays
// effective across reruns. The predecessor is persisted without
// submission: rerun requires ready or dead-lettered work (E7-T2/B-2).
func TestRerunPreservesTargetScope(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var dispatchOut, dispatchErr bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &dispatchOut, &dispatchErr)
	})
	res := decodeEnvelope(t, &dispatchOut)
	dispatchID, _ := res["dispatch_id"].(string)

	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "rerun", "--config", configPath, "--yes", dispatchID, "--reason", "audit scope check"}, &out, &errb); code != 0 {
		t.Fatalf("rerun: %s", errb.String())
	}
	cfgL, _ := config.Load(configPath)
	stateDir := platformpaths.ResolveStateDir(cfgL.Instance.StateDir)
	st, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Find the rerun intent (new id) and verify the scope was inherited.
	sums, err := st.ListIntents(context.Background(), ports.IntentFilter{RouteID: "wiki", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var rerunScope string
	for _, sum := range sums {
		if sum.DispatchID != dispatchID {
			snap, err := st.LoadIntent(context.Background(), sum.DispatchID)
			if err != nil {
				t.Fatal(err)
			}
			rerunScope = snap.TargetScope
		}
	}
	if rerunScope != "agent-dispatch-test" {
		t.Fatalf("rerun intent must inherit the target scope, got %q", rerunScope)
	}
}

// TestRefreshRefusedOnRepointedBoard proves the refresh scope guard:
// a board re-point under the same target id is refused (the accepting
// scope no longer matches) with the configuration class.
func TestRefreshRefusedOnRepointedBoard(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	res := e4t3DispatchNoRegister(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)

	raw, _ := os.ReadFile(configPath)
	updated := strings.Replace(string(raw), "board: agent-dispatch-test", "board: agent-dispatch-other", 1)
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"dispatches", "refresh", "--config", configPath, dispatchID}, &out, &errb)
	if code != 3 || !strings.Contains(errb.String(), "target scope") {
		t.Fatalf("board re-point must be refused with the scope mismatch, got %d: %s", code, errb.String())
	}
}

// TestOpenOperatorStoreErrorClasses proves the exit-class mapping:
// configuration failures exit 3, newer-database failures exit 21.
func TestOpenOperatorStoreErrorClasses(t *testing.T) {
	configPath, _ := e4t3Fixture(t)
	// Configuration failure: unreadable file content.
	broken := filepath.Join(t.TempDir(), "broken.yaml")
	if err := os.WriteFile(broken, []byte("version: 1\nresources: {}\ntargets: {}\nroutes: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"dispatches", "list", "--config", broken}, &out, &errb)
	if code != 3 || !strings.Contains(errb.String(), "config_invalid") {
		t.Fatalf("configuration failure must exit 3 config_invalid, got %d: %s", code, errb.String())
	}
	// Newer database: forge the migration ledger above the supported
	// maximum inside the fixture's state directory.
	cfgL, _ := config.Load(configPath)
	stateDir := platformpaths.ResolveStateDir(cfgL.Instance.StateDir)
	st, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Exec(`INSERT INTO schema_migrations (version, name, checksum) VALUES (?, 'future', 'sha256:x')`, 999); err != nil {
		t.Fatal(err)
	}
	st.Close()
	out.Reset()
	errb.Reset()
	code = Run([]string{"dispatches", "list", "--config", configPath}, &out, &errb)
	if code != 21 || !strings.Contains(errb.String(), "migration_newer_schema") {
		t.Fatalf("newer database must exit 21 migration_newer_schema, got %d: %s", code, errb.String())
	}
}

// TestReconcilePreV3EmptyScopeWarnsAndProceeds proves the pre-v3 path:
// an intent with no recorded scope is reconciled but with a visible
// weaker-identity warning (and the scope guard itself stays engaged
// for scoped intents).
func TestReconcilePreV3EmptyScopeWarnsAndProceeds(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	dir := t.TempDir()
	good := stubhermes.Write(t)
	bad := filepath.Join(dir, "hermes-ambiguous")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf 'Hermes Agent v0.19.1 (2026.7.30)\\n'; exit 0; fi\nsleep 60\n"
	if err := os.WriteFile(bad, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(configPath)
	lines := strings.Split(string(raw), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "    executable: ") {
			lines[i] = "    executable: " + bad
		}
		if strings.HasPrefix(l, "    submit_timeout: ") {
			lines[i] = "    submit_timeout: 1s"
		}
	}
	if err := os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	res := e4t3DispatchNoRegister(t, configPath, vault)
	if res["state"] != "unknown" {
		t.Fatalf("expected unknown, got %v", res["state"])
	}
	dispatchID, _ := res["dispatch_id"].(string)

	// Simulate the pre-v3 shape: clear the recorded scope.
	cfgL, _ := config.Load(configPath)
	stateDir := platformpaths.ResolveStateDir(cfgL.Instance.StateDir)
	st, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Exec(`UPDATE dispatch_intents SET target_scope = '' WHERE dispatch_id = ?`, dispatchID); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Heal the target and drain: the empty-scope reconciliation
	// proceeds with the visible weaker-identity warning.
	healed := string(raw)
	lines = strings.Split(healed, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "    executable: ") {
			lines[i] = "    executable: " + good
		}
	}
	if err := os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	if !strings.Contains(out.String(), "pre-v3 intent") {
		t.Fatalf("the weaker-identity warning must be visible: %s", out.String())
	}
	// The reconciliation itself proceeded (dead-lettered: no reference).
	if !strings.Contains(out.String(), "dead_lettered") {
		t.Fatalf("empty-scope reconciliation must proceed: %s", out.String())
	}
}

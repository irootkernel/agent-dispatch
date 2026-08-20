package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootkernel/jjukkumi/internal/adapters/sqlite"
	"github.com/rootkernel/jjukkumi/internal/config"
	"github.com/rootkernel/jjukkumi/internal/platformpaths"
	"github.com/rootkernel/jjukkumi/internal/testsupport/stubhermes"
)

// e4t3RegisterRoute creates the route runtime state row the coordinator
// requires (the same registration the crash harness performs; a
// production registration surface is tracked for the epic audit).
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
	if err := store.InitializeRouteState(nil, "wiki"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRouteActivation(context.Background(), "wiki", "enabled", "route-rev-1", "2026-08-20T00:00:00Z"); err != nil {
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
	cfg := `version: 1
instance:
  id: e4t3-test
resources:
  vault-main:
    type: directory
    root: ` + vault + `
    file_scope: markdown
    git:
      mode: disabled
targets:
  hermes-main:
    type: hermes-kanban
    board: jjukkumi-test
    executable: ` + stubhermes.Write(t) + `
    capability_report: ../../docs/integrations/hermes-capability-report.json
    required_capabilities: [durable_acceptance, submit_idempotency_key, lookup_by_external_ref]
    submit_timeout: 10s
    lookup_timeout: 5s
    environment_allowlist: [PATH, HOME]
routes:
  wiki:
    enabled: true
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: jjukkumi.wiki.test
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
    dispatch:
      target: hermes-main
      profile: wiki-maintainer
      skills: [llm-wiki]
      mutex_key: wiki-publish
      latest_state: true
      submission_retry:
        max_attempts: 3
        initial_backoff: 1s
        max_backoff: 2s
        multiplier: 2.0
        jitter_fraction: 0.0
      execution_hints:
        max_runtime: 30m
        max_attempts: 2
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
// construction gate (bad capability report) fails with the stable
// configuration error instead of submitting.
func TestDispatchUnusableTargetFailsClosed(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.Replace(raw, []byte("hermes-capability-report.json"), []byte("absent-report.json"), 1)
	if err := os.WriteFile(configPath, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
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
		report   func(t *testing.T, dir string) string
		required string
		wantCode string
		wantExit int
	}{
		{
			name: "version unsupported",
			bin: func(t *testing.T, dir string) string {
				return e4t1StubHermes(t, dir, "Hermes Agent v0.21.0 (2026.9.01)")
			},
			report: func(t *testing.T, dir string) string {
				return "../../docs/integrations/hermes-capability-report.json"
			},
			required: "durable_acceptance",
			wantCode: "hermes_version_unsupported",
			wantExit: 11,
		},
		{
			name: "executable missing",
			bin: func(t *testing.T, dir string) string {
				return filepath.Join(dir, "absent-hermes")
			},
			report: func(t *testing.T, dir string) string {
				return "../../docs/integrations/hermes-capability-report.json"
			},
			required: "durable_acceptance",
			wantCode: "hermes_executable_missing",
			wantExit: 11,
		},
		{
			name: "capability mismatch",
			bin: func(t *testing.T, dir string) string {
				return e4t1StubHermes(t, dir, "Hermes Agent v0.19.1 (2026.7.30)")
			},
			report:   limitedReportPath,
			required: "durable_acceptance, resource_mutex",
			wantCode: "config_capability_missing",
			wantExit: 3,
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
			updated = strings.Replace(updated, "required_capabilities: [durable_acceptance, submit_idempotency_key, lookup_by_external_ref]", "required_capabilities: ["+c.required+"]", 1)
			updated = strings.Replace(updated, "capability_report: ../../docs/integrations/hermes-capability-report.json", "capability_report: "+c.report(t, dir), 1)
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

// limitedReportPath writes a report without resource_mutex for the
// capability-mismatch case.
func limitedReportPath(t *testing.T, dir string) string {
	t.Helper()
	body := `{"schema_version":"jjukkumi.hermes-capabilities/v1","probed_at":"2026-08-19T21:25:24+09:00","hermes_version":"0.19.1 (2026.7.30)","interface":"public_cli","capabilities":{"durable_acceptance":true,"submit_idempotency_key":true,"lookup_by_idempotency_key":true,"lookup_by_external_ref":true,"resource_mutex":false,"execution_status":true,"cancellation":true,"result_receipt":true},"limits":{"maximum_request_bytes":null},"evidence":[]}`
	path := filepath.Join(dir, "limited-report.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
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
	e4t3RegisterRoute(t, configPath)
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

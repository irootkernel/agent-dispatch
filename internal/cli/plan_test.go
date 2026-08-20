package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// planFixture writes a minimal valid configuration with one enabled
// watchman route over a temporary vault and returns the config path and
// vault root.
func planFixture(t *testing.T) (configPath, vault string) {
	t.Helper()
	dir := t.TempDir()
	vault = filepath.Join(dir, "vault")
	if err := os.MkdirAll(filepath.Join(vault, "Notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "Notes", "a.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := `version: 1
instance:
  id: plan-test
limits:
  max_stdin_bytes: 65536
  max_hash_file_bytes: 1048576
resources:
  vault-main:
    type: directory
    root: ` + vault + `
    file_scope: markdown
targets:
  hermes-main:
    type: hermes-kanban
    board: jjukkumi
    executable: hermes
    capability_report: "` + filepath.Join(dir, "cap.json") + `"
    required_capabilities: [durable_acceptance, submit_idempotency_key]
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
      protected: ["raw/**"]
      immutable: []
      bulk_action: quarantine
      overflow_action: reconcile
      fresh_instance_action: reconcile
      unsafe_path_action: quarantine
    dispatch:
      target: hermes-main
      profile: maintainer
      skills: [llm-wiki]
      mutex_key: wiki
      latest_state: true
      submission_retry:
        max_attempts: 3
        initial_backoff: 2s
        max_backoff: 2m
        multiplier: 2.0
        jitter_fraction: 0.2
      execution_hints:
        max_runtime: 30m
        max_attempts: 2
      failure_budget: 2
      active_stale_after: 2h
    reconciliation:
      initial: true
      daily_expected: true
`
	// capability_report is only referenced, not read during planning.
	configPath = filepath.Join(dir, "jjukkumi.yaml")
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath, vault
}

func setPlanEnv(t *testing.T, vault string, fresh bool) {
	t.Helper()
	t.Setenv("WATCHMAN_TRIGGER", "jjukkumi.wiki.test")
	t.Setenv("WATCHMAN_ROOT", vault)
	t.Setenv("WATCHMAN_CLOCK", "c:1:2:3:4")
	t.Setenv("WATCHMAN_SOCK", "/tmp/sock")
	if fresh {
		os.Unsetenv("WATCHMAN_SINCE")
	} else {
		t.Setenv("WATCHMAN_SINCE", "c:1:2:3:3")
	}
	t.Setenv("JJUKKUMI_STATE_DIR", t.TempDir())
}

func withStdin(t *testing.T, payload string, fn func()) {
	t.Helper()
	old := os.Stdin
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	os.Stdin = f
	defer func() { os.Stdin = old; f.Close() }()
	fn()
}

func TestRoutePlanNormalBatch(t *testing.T) {
	configPath, vault := planFixture(t)
	setPlanEnv(t, vault, false)
	payload := `[{"name":"Notes/a.md","exists":true,"new":true,"size":5,"type":"f"}]`
	withStdin(t, payload, func() {
		var out, errb bytes.Buffer
		code := Run([]string{"route", "plan", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
		if code != 0 {
			t.Fatalf("exit %d, stderr: %s", code, errb.String())
		}
		var env Envelope
		if err := json.Unmarshal(out.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if !env.OK || env.Command != "route plan" {
			t.Fatalf("envelope wrong: %+v", env)
		}
		raw, _ := json.Marshal(env.Result)
		var plan map[string]any
		if err := json.Unmarshal(raw, &plan); err != nil {
			t.Fatal(err)
		}
		if plan["disposition"] != "dispatch" {
			t.Fatalf("normal batch must dispatch: %s", raw)
		}
		if plan["schema_version"] != "jjukkumi.dispatch-plan/v1" {
			t.Fatalf("schema version wrong: %s", raw)
		}
		route := plan["route"].(map[string]any)
		if route["id"] != "wiki" || route["revision"] == "" {
			t.Fatalf("route identity missing: %s", raw)
		}
	})
}

func TestDispatchDryRunFreshInstanceReconciles(t *testing.T) {
	configPath, vault := planFixture(t)
	setPlanEnv(t, vault, true) // no WATCHMAN_SINCE
	payload := `[{"name":"Notes/a.md","exists":true,"new":true,"size":5,"type":"f"}]`
	withStdin(t, payload, func() {
		var out, errb bytes.Buffer
		code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--dry-run"}, &out, &errb)
		if code != 0 {
			t.Fatalf("exit %d, stderr: %s", code, errb.String())
		}
		var env Envelope
		if err := json.Unmarshal(out.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(env.Result)
		var plan map[string]any
		if err := json.Unmarshal(raw, &plan); err != nil {
			t.Fatal(err)
		}
		if plan["disposition"] != "reconcile" {
			t.Fatalf("fresh instance must never yield partial dispatch: %s", raw)
		}
	})
}

func TestRoutePlanRejectsBindingMismatch(t *testing.T) {
	configPath, vault := planFixture(t)
	setPlanEnv(t, vault, false)
	t.Setenv("WATCHMAN_TRIGGER", "other-trigger")
	payload := `[{"name":"Notes/a.md","exists":true,"new":true,"size":5,"type":"f"}]`
	withStdin(t, payload, func() {
		var out, errb bytes.Buffer
		code := Run([]string{"route", "plan", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
		if code != 4 || out.Len() != 0 {
			t.Fatalf("binding mismatch must fail with empty stdout, exit %d", code)
		}
		var env ErrorEnvelope
		if err := json.Unmarshal(errb.Bytes(), &env); err != nil || env.Error.Code != "source_binding_mismatch" {
			t.Fatalf("wrong envelope: %s", errb.String())
		}
	})
}

func TestRoutePlanMalformedStdinRejectedBeforeAccess(t *testing.T) {
	configPath, vault := planFixture(t)
	setPlanEnv(t, vault, false)
	withStdin(t, `{not json`, func() {
		var out, errb bytes.Buffer
		code := Run([]string{"route", "plan", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
		if code != 4 || out.Len() != 0 {
			t.Fatalf("malformed stdin must fail closed, exit %d", code)
		}
		var env ErrorEnvelope
		if err := json.Unmarshal(errb.Bytes(), &env); err != nil || env.Error.Code != "source_malformed_json" {
			t.Fatalf("wrong envelope: %s", errb.String())
		}
	})
}

func TestRoutePlanUnknownRoute(t *testing.T) {
	configPath, _ := planFixture(t)
	var out, errb bytes.Buffer
	code := Run([]string{"route", "plan", "--route", "missing", "--config", configPath, "--input", "watchman"}, &out, &errb)
	if code != 3 {
		t.Fatalf("unknown route must fail, exit %d", code)
	}
	var env ErrorEnvelope
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil || env.Error.Code != "config_route_not_found" {
		t.Fatalf("wrong envelope: %s", errb.String())
	}
}

func TestDispatchWithoutDryRunExecutesDurablePath(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"dispatch", "--route", "wiki"}, &out, &errb)
	if code == 2 {
		t.Fatalf("non-dry-run dispatch is executable since E3, got command_not_implemented: %s", errb.String())
	}
	var env ErrorEnvelope
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
		t.Fatalf("stderr is not the error envelope: %s", errb.String())
	}
	// Without a real configuration the durable path stops at the
	// documented configuration error, never at command_not_implemented.
	if env.Error.Code == "command_not_implemented" || env.Error.Code == "command_unknown" {
		t.Fatalf("durable dispatch must be a known executable command: %s", errb.String())
	}
	if code != 3 {
		t.Fatalf("exit %d with envelope %s", code, errb.String())
	}
}

func TestRoutePlanUnsafePathFailsClosed(t *testing.T) {
	configPath, vault := planFixture(t)
	// A lexically valid name that escapes through a symlink: rejected
	// by the containment defense, not the parser.
	outside := filepath.Join(filepath.Dir(vault), "outside-secret.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(vault, "escape.md")); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, vault, false)
	payload := `[{"name":"escape.md","exists":true,"new":true,"size":6,"type":"f"}]`
	withStdin(t, payload, func() {
		var out, errb bytes.Buffer
		code := Run([]string{"route", "plan", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
		if code != 30 || out.Len() != 0 {
			t.Fatalf("unsafe path must fail with exit 30 and empty stdout, exit %d", code)
		}
		var env ErrorEnvelope
		if err := json.Unmarshal(errb.Bytes(), &env); err != nil || env.Error.Code != "source_unsafe_path" {
			t.Fatalf("wrong envelope: %s", errb.String())
		}
	})
}

func TestRoutePlanAcceptsSpacedOutputFlag(t *testing.T) {
	configPath, vault := planFixture(t)
	setPlanEnv(t, vault, false)
	payload := `[{"name":"Notes/a.md","exists":true,"new":true,"size":5,"type":"f"}]`
	withStdin(t, payload, func() {
		var out, errb bytes.Buffer
		code := Run([]string{"route", "plan", "--route", "wiki", "--config", configPath, "--input", "watchman", "--output", "json"}, &out, &errb)
		if code != 0 || errb.Len() != 0 {
			t.Fatalf("spaced --output json must be accepted: %s", errb.String())
		}
	})
}

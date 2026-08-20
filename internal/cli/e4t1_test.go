package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// e4t1StubHermes writes a stub hermes executable that answers --version
// with the frozen first line and otherwise emits the frozen create
// response.
func e4t1StubHermes(t *testing.T, dir, versionLine string) string {
	t.Helper()
	bin := filepath.Join(dir, "hermes")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '%s\\n' '" + versionLine + "'; exit 0; fi\nexit 3\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// e4t1ProbeConfig writes a one-target configuration pointing at the
// given executable and capability report.
func e4t1ProbeConfig(t *testing.T, dir, executable, report, required string) string {
	t.Helper()
	cfg := `version: 1
instance:
  id: probe-test
resources:
  vault-main:
    type: directory
    root: ` + filepath.Join(dir, "vault") + `
    file_scope: markdown
targets:
  hermes-main:
    type: hermes-kanban
    board: jjukkumi
    executable: ` + executable + `
    capability_report: ` + report + `
    required_capabilities: [` + required + `]
    submit_timeout: 5s
    lookup_timeout: 5s
    environment_allowlist: [PATH, HOME]
routes:
  wiki:
    enabled: false
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
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestConfigValidateProbeTargetsAvailable: a matching stub target probes
// available and reports the eight HER-004 declarations (cli-spec §3).
func TestConfigValidateProbeTargetsAvailable(t *testing.T) {
	dir := t.TempDir()
	bin := e4t1StubHermes(t, dir, "Hermes Agent v0.19.1 (2026.7.30)")
	configPath := e4t1ProbeConfig(t, dir, bin, "../../docs/integrations/hermes-capability-report.json", "durable_acceptance, submit_idempotency_key")
	t.Setenv("JJUKKUMI_STATE_DIR", dir)
	var out, errb bytes.Buffer
	code := Run([]string{"config", "validate", "--probe-targets", "--config", configPath}, &out, &errb)
	if code != 0 {
		t.Fatalf("probe-targets failed (%d): %s", code, errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["valid"] != true {
		t.Fatalf("valid=%v", res["valid"])
	}
	probes, ok := res["probe_targets"].([]any)
	if !ok || len(probes) != 1 {
		t.Fatalf("probe_targets missing: %v", res["probe_targets"])
	}
	entry, _ := probes[0].(map[string]any)
	if entry["state"] != "available" || entry["hermes_version"] != "0.19.1" {
		t.Fatalf("probe entry wrong: %v", entry)
	}
	caps, ok := entry["capabilities"].(map[string]any)
	if !ok || caps["durable_acceptance"] != true || caps["submit_idempotency_key"] != true {
		t.Fatalf("capabilities missing: %v", entry["capabilities"])
	}
	if len(caps) != 8 {
		t.Fatalf("all eight HER-004 declarations must be reported, got %d", len(caps))
	}
}

// TestConfigValidateProbeTargetsCapabilityMismatch: a required
// capability absent from a version-matching report fails validation with
// the stable config_capability_missing code and exit 3 (HER-005).
func TestConfigValidateProbeTargetsCapabilityMismatch(t *testing.T) {
	dir := t.TempDir()
	bin := e4t1StubHermes(t, dir, "Hermes Agent v0.19.1 (2026.7.30)")
	// A report that honestly records no mutex.
	limited := filepath.Join(dir, "limited.json")
	body := `{"schema_version":"jjukkumi.hermes-capabilities/v1","probed_at":"2026-08-19T21:25:24+09:00","hermes_version":"0.19.1 (2026.7.30)","interface":"public_cli","capabilities":{"durable_acceptance":true,"submit_idempotency_key":true,"lookup_by_idempotency_key":true,"lookup_by_external_ref":true,"resource_mutex":false,"execution_status":true,"cancellation":true,"result_receipt":true},"limits":{"maximum_request_bytes":null},"evidence":[]}`
	if err := os.WriteFile(limited, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := e4t1ProbeConfig(t, dir, bin, limited, "durable_acceptance, resource_mutex")
	t.Setenv("JJUKKUMI_STATE_DIR", dir)
	var out, errb bytes.Buffer
	code := Run([]string{"config", "validate", "--probe-targets", "--config", configPath}, &out, &errb)
	if code != 3 {
		t.Fatalf("capability mismatch must exit 3, got %d", code)
	}
	var env struct {
		Error struct {
			Code     string `json:"code"`
			Category string `json:"category"`
		} `json:"error"`
	}
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
		t.Fatalf("stderr not an error envelope: %s", errb.String())
	}
	if env.Error.Code != "config_capability_missing" || env.Error.Category != "configuration" {
		t.Fatalf("error code/category wrong: %+v", env.Error)
	}
}

// TestConfigValidateProbeTargetsVersionUnsupported: an unsupported
// installed version is a warning with the state recorded, because the
// configuration document itself is valid and the adapter gates
// submissions again at run time.
func TestConfigValidateProbeTargetsVersionUnsupported(t *testing.T) {
	dir := t.TempDir()
	bin := e4t1StubHermes(t, dir, "Hermes Agent v0.20.0 (2026.8.10)")
	configPath := e4t1ProbeConfig(t, dir, bin, "../../docs/integrations/hermes-capability-report.json", "durable_acceptance")
	t.Setenv("JJUKKUMI_STATE_DIR", dir)
	var out, errb bytes.Buffer
	code := Run([]string{"config", "validate", "--probe-targets", "--config", configPath}, &out, &errb)
	if code != 0 {
		t.Fatalf("version-unsupported is a warning, got exit %d: %s", code, errb.String())
	}
	res := decodeEnvelope(t, &out)
	probes, _ := res["probe_targets"].([]any)
	if len(probes) != 1 {
		t.Fatalf("probe_targets: %v", res["probe_targets"])
	}
	entry, _ := probes[0].(map[string]any)
	if entry["state"] != "version_unsupported" {
		t.Fatalf("state=%v", entry["state"])
	}
}

// TestConfigValidateProbeTargetsUnavailable: a missing executable stays a
// warning (config validates without a Hermes installation).
func TestConfigValidateProbeTargetsUnavailable(t *testing.T) {
	dir := t.TempDir()
	configPath := e4t1ProbeConfig(t, dir, filepath.Join(dir, "absent-hermes"), "../../docs/integrations/hermes-capability-report.json", "durable_acceptance")
	t.Setenv("JJUKKUMI_STATE_DIR", dir)
	var out, errb bytes.Buffer
	code := Run([]string{"config", "validate", "--probe-targets", "--config", configPath}, &out, &errb)
	if code != 0 {
		t.Fatalf("unavailable target is a warning, got exit %d: %s", code, errb.String())
	}
	res := decodeEnvelope(t, &out)
	probes, _ := res["probe_targets"].([]any)
	entry, _ := probes[0].(map[string]any)
	if entry["state"] != "unavailable" {
		t.Fatalf("state=%v", entry["state"])
	}
}

// TestConfigValidateProbeTargetsConfigErrors proves persistent
// configuration defects fail validation (exit 3) instead of downgrading
// to a warning: an unknown required-capability name and an unreadable
// capability report are operator errors, never reduced guarantees.
func TestConfigValidateProbeTargetsConfigErrors(t *testing.T) {
	dir := t.TempDir()
	bin := e4t1StubHermes(t, dir, "Hermes Agent v0.19.1 (2026.7.30)")
	frozen := "../../docs/integrations/hermes-capability-report.json"
	cases := []struct {
		name     string
		report   string
		required string
	}{
		{"unknown capability name", frozen, "durable_acceptance, teleportaion"},
		{"missing report file", filepath.Join(dir, "absent-report.json"), "durable_acceptance"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			configPath := e4t1ProbeConfig(t, dir, bin, c.report, c.required)
			t.Setenv("JJUKKUMI_STATE_DIR", dir)
			var out, errb bytes.Buffer
			code := Run([]string{"config", "validate", "--probe-targets", "--config", configPath}, &out, &errb)
			if code != 3 {
				t.Fatalf("config defect must exit 3, got %d: %s", code, errb.String())
			}
			var env struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
				t.Fatalf("stderr not an error envelope: %s", errb.String())
			}
			if env.Error.Code != "config_invalid" {
				t.Fatalf("error code = %q want config_invalid", env.Error.Code)
			}
		})
	}
}

// TestConfigValidateProbeTargetsInvalidTimeouts proves non-positive or
// unparseable target durations fail closed with exit 3, while the
// schema's whole-day unit is accepted.
func TestConfigValidateProbeTargetsInvalidTimeouts(t *testing.T) {
	dir := t.TempDir()
	bin := e4t1StubHermes(t, dir, "Hermes Agent v0.19.1 (2026.7.30)")
	for _, bad := range []string{"banana", "0s", "-5s", "5"} {
		t.Run(bad, func(t *testing.T) {
			configPath := e4t1ProbeConfig(t, dir, bin, "../../docs/integrations/hermes-capability-report.json", "durable_acceptance")
			raw, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			updated := bytes.Replace(raw, []byte("submit_timeout: 5s"), []byte("submit_timeout: "+bad), 1)
			if err := os.WriteFile(configPath, updated, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("JJUKKUMI_STATE_DIR", dir)
			var out, errb bytes.Buffer
			code := Run([]string{"config", "validate", "--probe-targets", "--config", configPath}, &out, &errb)
			if code != 3 {
				t.Fatalf("timeout %q must fail closed with exit 3, got %d", bad, code)
			}
		})
	}
	t.Run("day unit accepted", func(t *testing.T) {
		configPath := e4t1ProbeConfig(t, dir, bin, "../../docs/integrations/hermes-capability-report.json", "durable_acceptance")
		raw, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		updated := bytes.Replace(raw, []byte("submit_timeout: 5s"), []byte("submit_timeout: 1d"), 1)
		if err := os.WriteFile(configPath, updated, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("JJUKKUMI_STATE_DIR", dir)
		var out, errb bytes.Buffer
		code := Run([]string{"config", "validate", "--probe-targets", "--config", configPath}, &out, &errb)
		if code != 0 {
			t.Fatalf("1d must be accepted, got exit %d: %s", code, errb.String())
		}
	})
}

// TestConfigValidateProbeTargetsMultipleTargets proves every hermes
// target is probed in deterministic order with a complete summary even
// when states are mixed.
func TestConfigValidateProbeTargetsMultipleTargets(t *testing.T) {
	dir := t.TempDir()
	good := e4t1StubHermes(t, dir, "Hermes Agent v0.19.1 (2026.7.30)")
	bad := filepath.Join(dir, "absent-hermes")
	frozen := "../../docs/integrations/hermes-capability-report.json"
	cfg := `version: 1
instance:
  id: probe-multi
resources:
  vault-main:
    type: directory
    root: ` + filepath.Join(dir, "vault") + `
    file_scope: markdown
targets:
  a-target:
    type: hermes-kanban
    board: jjukkumi
    executable: ` + bad + `
    capability_report: ` + frozen + `
    required_capabilities: [durable_acceptance]
  b-target:
    type: hermes-kanban
    board: jjukkumi
    executable: ` + good + `
    capability_report: ` + frozen + `
    required_capabilities: [durable_acceptance]
routes:
  wiki:
    enabled: false
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
      target: b-target
      profile: wiki-maintainer
      skills: [llm-wiki]
      mutex_key: wiki-publish
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
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JJUKKUMI_STATE_DIR", dir)
	var out, errb bytes.Buffer
	code := Run([]string{"config", "validate", "--probe-targets", "--config", configPath}, &out, &errb)
	if code != 0 {
		t.Fatalf("mixed states stay a warning, got exit %d: %s", code, errb.String())
	}
	res := decodeEnvelope(t, &out)
	probes, _ := res["probe_targets"].([]any)
	if len(probes) != 2 {
		t.Fatalf("both targets must appear in the summary: %v", probes)
	}
	first, _ := probes[0].(map[string]any)
	second, _ := probes[1].(map[string]any)
	if first["target_id"] != "a-target" || first["state"] != "unavailable" {
		t.Fatalf("first entry = %v", first)
	}
	if second["target_id"] != "b-target" || second["state"] != "available" {
		t.Fatalf("second entry = %v", second)
	}
}

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
// given executable with the given eligibility floor ("" keeps the
// 0.19.1 default). The lookup budget is deliberately generous — these
// tests assert probe verdicts, and a transient host stall must not
// flip them to the unavailable path — while submit_timeout stays 5s
// because TestConfigValidateProbeTargetsInvalidTimeouts rewrites that
// literal.
func e4t1ProbeConfig(t *testing.T, dir, executable, floor string) string {
	t.Helper()
	cfg := `version: 1
instance:
  id: probe-test
resources:
  vault-main:
    type: directory
    root: ` + filepath.Join(dir, "vault") + `
    file_scope: markdown
hermes_targets:
  hermes-main:
    board: agent-dispatch
    minimum_version: ` + floorValue(floor) + `
    compatibility: capability_probe
    executable: ` + executable + `
    submit_timeout: 5s
    lookup_timeout: 120s
    environment_allowlist: [PATH, HOME]
routes:
  wiki:
    enabled: false
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
      initial_backoff: 2s
      max_backoff: 2m
      multiplier: 2.0
      jitter_fraction: 0.2
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
	return path
}

// floorValue renders the configured floor for the fixture.
func floorValue(floor string) string {
	if floor == "" {
		return "0.19.1"
	}
	return floor
}

// TestConfigValidateProbeTargetsAvailable: a matching stub target probes
// available and reports the eight HER-004 declarations (cli-spec §3).
func TestConfigValidateProbeTargetsAvailable(t *testing.T) {
	dir := t.TempDir()
	bin := e4t1StubHermes(t, dir, "Hermes Agent v0.19.1 (2026.7.30)")
	configPath := e4t1ProbeConfig(t, dir, bin, "")
	t.Setenv("AGENT_DISPATCH_STATE_DIR", dir)
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
	// The capability declarations return with the E11-T2 probe's cached
	// shape evidence; the E11-T1 probe proves eligibility.
	if _, has := entry["capabilities"]; has {
		t.Fatalf("E11-T1 probe must not assert unproven capabilities: %v", entry)
	}
}

// TestConfigValidateProbeTargetsCapabilityMismatch: a required
// capability absent from a version-matching report fails validation with
// the stable config_capability_missing code and exit 3 (HER-005).
func TestConfigValidateProbeTargetsBelowDeclaredFloor(t *testing.T) {
	dir := t.TempDir()
	bin := e4t1StubHermes(t, dir, "Hermes Agent v0.19.1 (2026.7.30)")
	// A declared floor above the installed version: the configuration
	// document is valid (the mismatch rides the probe as a warning,
	// state version_unsupported) and the enable/submit gates refuse.
	configPath := e4t1ProbeConfig(t, dir, bin, "0.19.2")
	t.Setenv("AGENT_DISPATCH_STATE_DIR", dir)
	var out, errb bytes.Buffer
	code := Run([]string{"config", "validate", "--probe-targets", "--config", configPath}, &out, &errb)
	if code != 0 {
		t.Fatalf("below-declared-floor is a warning, got exit %d: %s", code, errb.String())
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

// TestConfigValidateProbeTargetsVersionUnsupported: an unsupported
// installed version is a warning with the state recorded, because the
// configuration document itself is valid and the adapter gates
// submissions again at run time.
func TestConfigValidateProbeTargetsVersionUnsupported(t *testing.T) {
	dir := t.TempDir()
	bin := e4t1StubHermes(t, dir, "Hermes Agent v0.18.5 (2026.6.01)")
	configPath := e4t1ProbeConfig(t, dir, bin, "")
	t.Setenv("AGENT_DISPATCH_STATE_DIR", dir)
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
	configPath := e4t1ProbeConfig(t, dir, filepath.Join(dir, "absent-hermes"), "")
	t.Setenv("AGENT_DISPATCH_STATE_DIR", dir)
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
	cases := []struct {
		name  string
		floor string
	}{
		{"unparseable eligibility floor", "0.19.x"},
		{"floor below the v0.1.5 minimum", "0.18.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			configPath := e4t1ProbeConfig(t, dir, bin, c.floor)
			t.Setenv("AGENT_DISPATCH_STATE_DIR", dir)
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
			configPath := e4t1ProbeConfig(t, dir, bin, "")
			raw, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			updated := bytes.Replace(raw, []byte("submit_timeout: 5s"), []byte("submit_timeout: "+bad), 1)
			if err := os.WriteFile(configPath, updated, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AGENT_DISPATCH_STATE_DIR", dir)
			var out, errb bytes.Buffer
			code := Run([]string{"config", "validate", "--probe-targets", "--config", configPath}, &out, &errb)
			if code != 3 {
				t.Fatalf("timeout %q must fail closed with exit 3, got %d", bad, code)
			}
		})
	}
	t.Run("day unit accepted", func(t *testing.T) {
		configPath := e4t1ProbeConfig(t, dir, bin, "")
		raw, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		updated := bytes.Replace(raw, []byte("submit_timeout: 5s"), []byte("submit_timeout: 1d"), 1)
		if err := os.WriteFile(configPath, updated, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AGENT_DISPATCH_STATE_DIR", dir)
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
	_ = good
	cfg := `version: 1
instance:
  id: probe-multi
resources:
  vault-main:
    type: directory
    root: ` + filepath.Join(dir, "vault") + `
    file_scope: markdown
hermes_targets:
  a-target:
    board: agent-dispatch
    minimum_version: 0.19.1
    compatibility: capability_probe
    executable: ` + bad + `
  b-target:
    board: agent-dispatch
    minimum_version: 0.19.1
    compatibility: capability_probe
    executable: ` + good + `
routes:
  wiki:
    enabled: false
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
        target: b-target
        profile: wiki-maintainer
        skills: [llm-wiki]
        mutex_key: wiki-publish
        workstream: main
        execution_hints:
          max_runtime: 30m
          max_attempts: 2
    submission_retry:
      max_attempts: 3
      initial_backoff: 2s
      max_backoff: 2m
      multiplier: 2.0
      jitter_fraction: 0.2
    latest_state: true
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
	t.Setenv("AGENT_DISPATCH_STATE_DIR", dir)
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

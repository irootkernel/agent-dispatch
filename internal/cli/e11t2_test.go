package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeskanban"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/stubhermes"
)

// e11t2HermesConfig writes a one-hermes-target destinations
// configuration bound to the given executable under a disposable HOME
// (so the capability cache lands in the test tree, not the operator's).
func e11t2HermesConfig(t *testing.T, bin string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AGENT_DISPATCH_CONFIG", "")
	t.Setenv("AGENT_DISPATCH_STATE_DIR", filepath.Join(home, "state"))
	dir := t.TempDir()
	cfg := `version: 1
instance:
  id: e11t2-cli
resources:
  vault-main:
    type: directory
    root: ` + filepath.Join(dir, "vault") + `
    file_scope: markdown
hermes_targets:
  hermes-main:
    board: agent-dispatch-test
    minimum_version: 0.19.1
    compatibility: capability_probe
    executable: ` + bin + `
    submit_timeout: 30s
    lookup_timeout: 30s
    environment_allowlist: [PATH, HOME]
routes:
  wiki:
    enabled: false
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: agent-dispatch.wiki.e11t2
      include: ["**/*.md"]
      exclude: [".git/**"]
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
        workstream: main
        mutex_key: wiki-publish
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

// TestE11T2HermesProbeWritesCache proves CLI-010/HER-012: `hermes probe`
// runs every shape probe, writes the owner-only cache, and reports the
// fingerprint through the envelope.
func TestE11T2HermesProbeWritesCache(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	var out, errb bytes.Buffer
	code := Run([]string{"hermes", "probe", "--config", configPath}, &out, &errb)
	if code != 0 {
		t.Fatalf("hermes probe failed: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["all_passed"] != true || res["target_id"] != "hermes-main" {
		t.Fatalf("probe result wrong: %v", res)
	}
	fingerprint, _ := res["fingerprint"].(string)
	if !strings.HasPrefix(fingerprint, "cap:") {
		t.Fatalf("fingerprint missing: %v", res)
	}
	home, _ := os.UserHomeDir()
	cache := filepath.Join(home, ".config", "agent-dispatch", "hermes-capability-hermes-main.json")
	if info, err := os.Stat(cache); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the cache must exist owner-only: %v %v", info, err)
	}
	record, err := hermeskanban.LoadCapabilityRecord(cache)
	if err != nil {
		t.Fatal(err)
	}
	if record.Fingerprint != fingerprint || !record.AllRequiredPassed() {
		t.Fatalf("cache diverges from the probe result: %+v", record)
	}
}

// TestE11T2HermesCapabilitiesCacheAndRefresh proves HER-012/HER-013 at
// the CLI surface: `hermes capabilities` reads the cache without a
// probe, refuses incomplete evidence with the named failures, and
// `--refresh` re-probes a stale record.
func TestE11T2HermesCapabilitiesCacheAndRefresh(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	// Seed the cache through the probe command.
	var out, errb bytes.Buffer
	if code := Run([]string{"hermes", "probe", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("hermes probe: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	// capabilities reads the cache: one --version child at most, and the
	// capability set surfaces with the fingerprint.
	if code := Run([]string{"hermes", "capabilities", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("hermes capabilities: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	caps, _ := res["capabilities"].(map[string]any)
	if caps["durable_acceptance"] != true || caps["submit_idempotency_key"] != true {
		t.Fatalf("capabilities wrong: %v", res)
	}
	if _, has := res["fingerprint"]; !has {
		t.Fatalf("fingerprint missing: %v", res)
	}

	// Stale the cache by swapping the executable bytes; capabilities
	// re-probes transparently (HER-013 invalidation) and succeeds.
	raw, _ := os.ReadFile(bin)
	if err := os.WriteFile(bin, append(raw, []byte("\n# refreshed\n")...), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"hermes", "capabilities", "--config", configPath, "--refresh"}, &out, &errb); code != 0 {
		t.Fatalf("hermes capabilities --refresh: %s", errb.String())
	}
	refreshed := decodeEnvelope(t, &out)
	if refreshed["all_passed"] != true {
		t.Fatalf("refresh must re-probe the live executable: %v", refreshed)
	}
}

// TestE11T2HermesCapabilitiesRefusesIncompleteEvidence proves the
// fail-closed surface: drifted shape evidence refuses with
// config_capability_missing naming the probe remediation.
func TestE11T2HermesCapabilitiesRefusesIncompleteEvidence(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "hermes-drifted")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then printf 'Hermes Agent v0.20.9 (2026.9.9)\n'; exit 0; fi
if [ "$5" = "-h" ]; then printf 'usage: hermes kanban create [-h] [--body BODY] [--json] title\n'; exit 0; fi
if [ "$4" = "assignees" ]; then printf '[]'; exit 0; fi
if [ "$4" = "list" ]; then printf '[]'; exit 0; fi
if [ "$1" = "skills" ]; then printf '┏━━━┳━━━┓\n┃ Name ┃ Status ┃\n┡━━━╇━━━┩\n│ x ┃ enabled │\n└───┴───┘\n'; exit 0; fi
exit 3
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := e11t2HermesConfig(t, bin)
	var out, errb bytes.Buffer
	code := Run([]string{"hermes", "capabilities", "--config", configPath}, &out, &errb)
	if code != 3 {
		t.Fatalf("drifted evidence must refuse at exit 3, got %d: %s", code, errb.String())
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "config_capability_missing" || !strings.Contains(env.Error.Message, "--idempotency-key") {
		t.Fatalf("the refusal must name the missing capability: %+v", env.Error)
	}
}

// TestE11T2DispatchBlockedAfterExecutableSwap proves the full chain
// (HER-018, AC-703): enable through the production gate binds the
// fingerprint; swapping the executable bytes and dispatching through
// the real CLI yields definite_not_submitted with the probe
// remediation and no Hermes side effect (no task created).
func TestE11T2DispatchBlockedAfterExecutableSwap(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, bytes.Replace(raw, []byte("enabled: false"), []byte("enabled: true"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// setPlanEnv isolates the state directory (the e4t3 pattern), so the
	// registration, enable, and dispatch must all run inside it.
	setPlanEnv(t, cfg.Resources["vault-main"].Root, false)
	t.Setenv("WATCHMAN_TRIGGER", "agent-dispatch.wiki.e11t2")
	e4t3RegisterRoute(t, configPath)
	// A fresh enable through the production gate binds the capability
	// fingerprint on top of the helper's acknowledgement.
	revision, _ := config.RouteRevision(cfg, "wiki")
	var out, errb bytes.Buffer
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", revision, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("route enable: %s", errb.String())
	}
	// The plan path needs the claimed change on disk.
	if err := os.MkdirAll(filepath.Join(cfg.Resources["vault-main"].Root, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Resources["vault-main"].Root, "Inbox", "new.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Swap the executable bytes in place: same path, new digest.
	stubRaw, _ := os.ReadFile(bin)
	if err := os.WriteFile(bin, append(stubRaw, []byte("\n# swapped\n")...), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	var code int
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		code = Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("dispatch exit %d: %s", code, errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["state"] != "retry_wait" {
		t.Fatalf("the blocked submission must land in retry_wait, got %v", res)
	}
	// The stub's state directory holds no task: nothing reached Hermes.
	stamp := filepath.Join(filepath.Dir(bin), "state", "count")
	if _, err := os.Stat(stamp); !os.IsNotExist(err) {
		t.Fatalf("no Hermes side effect may occur: %v", err)
	}
}

// TestE11T2ProfileScopeInvalidatesCache proves the HER-013 scope rule:
// a cache probed unscoped (or under another profile) is stale for a
// profile-scoped read and is re-probed under the requested scope.
func TestE11T2ProfileScopeInvalidatesCache(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	var out, errb bytes.Buffer
	if code := Run([]string{"hermes", "probe", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("unscoped probe: %s", errb.String())
	}
	home, _ := os.UserHomeDir()
	cache := filepath.Join(home, ".config", "agent-dispatch", "hermes-capability-hermes-main.json")
	before, err := hermeskanban.LoadCapabilityRecord(cache)
	if err != nil {
		t.Fatal(err)
	}
	if before.Profile != "" {
		t.Fatalf("the unscoped probe must record an empty scope, got %q", before.Profile)
	}
	// A scoped read must not serve the unscoped record.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"hermes", "capabilities", "--config", configPath, "--profile", "wiki-maintainer"}, &out, &errb); code != 0 {
		t.Fatalf("scoped capabilities: %s", errb.String())
	}
	after, err := hermeskanban.LoadCapabilityRecord(cache)
	if err != nil {
		t.Fatal(err)
	}
	if after.Profile != "wiki-maintainer" {
		t.Fatalf("the scoped read must re-probe under the requested scope: before=%q after=%q", before.Profile, after.Profile)
	}
}

// TestE11T2ActivationRecordsFingerprint proves HER-018 end to end: the
// acknowledged activation stores the capability fingerprint beside the
// route revision, and the stored value equals the probe record's.
func TestE11T2ActivationRecordsFingerprint(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	// Enable through the production gate with the full probe.
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, bytes.Replace(raw, []byte("enabled: false"), []byte("enabled: true"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	revision, ok := config.RouteRevision(cfg, "wiki")
	if !ok {
		t.Fatal("revision unavailable")
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", revision, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("route enable: %s", errb.String())
	}
	store, err := openStateStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snap, err := store.LoadRouteState(requestCtx(), "wiki")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(snap.CapabilityFingerprint, "cap:") {
		t.Fatalf("activation must bind the capability fingerprint, got %q", snap.CapabilityFingerprint)
	}
	home, _ := os.UserHomeDir()
	record, err := hermeskanban.LoadCapabilityRecord(filepath.Join(home, ".config", "agent-dispatch", "hermes-capability-hermes-main.json"))
	if err != nil {
		t.Fatal(err)
	}
	if snap.CapabilityFingerprint != record.Fingerprint {
		t.Fatalf("stored fingerprint %q must equal the probe record's %q", snap.CapabilityFingerprint, record.Fingerprint)
	}
}

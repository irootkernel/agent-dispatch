package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeskanban"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/stubhermes"
)

// E15-T2 CLI coverage (HER-011, HER-020, CLI-019, AC-1101/AC-1107): the
// 0.20.5 floor fails closed on omitted and below-floor settings, the
// operator surfaces report the certified serialization mode, and the
// atomic target-floor helper updates exactly one target while pausing
// every affected route revision.

// e15t2FloorConfig writes a one-target configuration with a
// configurable declared floor.
func e15t2FloorConfig(t *testing.T, bin, floor string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AGENT_DISPATCH_CONFIG", "")
	t.Setenv("AGENT_DISPATCH_STATE_DIR", filepath.Join(home, "state"))
	dir := t.TempDir()
	cfg := `version: 1
instance:
  id: e15t2-cli
resources:
  vault-main:
    type: directory
    root: ` + filepath.Join(dir, "vault") + `
    file_scope: markdown
hermes_targets:
  hermes-main:
    board: agent-dispatch-test
    minimum_version: ` + floor + `
    compatibility: capability_probe
    executable: ` + bin + `
    submit_timeout: 30s
    lookup_timeout: 30s
    environment_allowlist: [PATH, HOME]
  hermes-other:
    board: agent-dispatch-other
    minimum_version: 0.21.0
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
      trigger_name: agent-dispatch.wiki.e15t2
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

func TestE15T2OmittedFloorFailsClosed(t *testing.T) {
	// AC-1107: an omitted floor fails validation without rewriting.
	bin := stubhermes.Write(t)
	configPath := e15t2FloorConfig(t, bin, "0.20.5")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	omitted := strings.Replace(string(raw), "    minimum_version: 0.20.5\n", "", 1)
	if omitted == string(raw) {
		t.Fatal("the fixture does not carry the floor line to omit")
	}
	if err := os.WriteFile(configPath, []byte(omitted), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"config", "validate", "--config", configPath}, &out, &errb)
	if code == 0 {
		t.Fatal("an omitted floor must fail closed")
	}
	if !strings.Contains(errb.String(), "minimum_version") {
		t.Fatalf("the refusal must name the omitted floor field: %s", errb.String())
	}
	// The document was never rewritten.
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "minimum_version: 0.20.5") {
		t.Fatal("the loader must never rewrite an omitted floor into the file")
	}
}

func TestE15T2BelowFloorSettingFailsClosed(t *testing.T) {
	// AC-1107: a configured floor below 0.20.5 fails validation.
	bin := stubhermes.Write(t)
	configPath := e15t2FloorConfig(t, bin, "0.20.4")
	var out, errb bytes.Buffer
	code := Run([]string{"config", "validate", "--config", configPath}, &out, &errb)
	if code == 0 {
		t.Fatal("a below-floor setting must fail validation")
	}
	if !strings.Contains(errb.String(), "below the support floor 0.20.5") {
		t.Fatalf("the refusal must name the support floor: %s", errb.String())
	}
}

func TestE15T2ProbeAndCapabilitiesReportTheMode(t *testing.T) {
	// HER-020: the probe and capabilities surfaces agree on the
	// certified mode; the stub carries --mutex-key so the complementary
	// mode is certified.
	bin := stubhermes.Write(t)
	configPath := e15t2FloorConfig(t, bin, "0.20.5")
	var out, errb bytes.Buffer
	if code := Run([]string{"hermes", "probe", "--config", configPath, "--target", "hermes-main"}, &out, &errb); code != 0 {
		t.Fatalf("probe: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["serialization_mode"] != hermeskanban.SerializationModeGroupPlusTargetMutex {
		t.Fatalf("the stub certifies group-plus-target-mutex: %v", res["serialization_mode"])
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"hermes", "capabilities", "--config", configPath, "--target", "hermes-main"}, &out, &errb); code != 0 {
		t.Fatalf("capabilities: %s", errb.String())
	}
	res = decodeEnvelope(t, &out)
	if res["serialization_mode"] != hermeskanban.SerializationModeGroupPlusTargetMutex {
		t.Fatalf("capabilities must report the same certified mode: %v", res["serialization_mode"])
	}
}

func TestE15T2SetMinimumVersionRefusals(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e15t2FloorConfig(t, bin, "0.20.5")
	var out, errb bytes.Buffer
	code := Run([]string{"hermes", "set-minimum-version", "--config", configPath, "hermes-main", "0.20.4"}, &out, &errb)
	if code == 0 || !strings.Contains(errb.String(), "below the support floor 0.20.5") {
		t.Fatalf("a floor below 0.20.5 must be refused: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	code = Run([]string{"hermes", "set-minimum-version", "--config", configPath, "nope", "0.21.0"}, &out, &errb)
	if code == 0 || !strings.Contains(errb.String(), "not declared under hermes_targets") {
		t.Fatalf("an unknown target must be refused: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	code = Run([]string{"hermes", "set-minimum-version", "--config", configPath, "hermes-main", "0.20.5"}, &out, &errb)
	if code == 0 || !strings.Contains(errb.String(), "already 0.20.5") {
		t.Fatalf("an unchanged floor must be refused as a no-op: %s", errb.String())
	}
}

func TestE15T2SetMinimumVersionAtomicUpdatePausesAffectedRoutes(t *testing.T) {
	// CLI-019: only the selected target's floor changes, unrelated
	// targets and routes keep their bytes, and every route binding the
	// target owes fresh probe, preflight, and re-acknowledgement through
	// its changed revision.
	bin := stubhermes.Write(t)
	configPath := e15t2FloorConfig(t, bin, "0.20.5")
	before, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	wikiBefore, _ := config.RouteRevision(before, "wiki")

	var out, errb bytes.Buffer
	code := Run([]string{"hermes", "set-minimum-version", "--config", configPath, "hermes-main", "0.21.0"}, &out, &errb)
	if code != 0 {
		t.Fatalf("set-minimum-version: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["minimum_version"] != "0.21.0" || res["previous_version"] != "0.20.5" {
		t.Fatalf("the envelope names the transition: %v", res)
	}
	affected, _ := res["affected_routes"].([]any)
	if len(affected) != 1 {
		t.Fatalf("exactly the wiki route binds hermes-main: %v", affected)
	}
	entry, _ := affected[0].(map[string]any)
	if entry["route_id"] != "wiki" || entry["acknowledgement"] != "stale" || entry["revision_after"] == entry["revision_before"] {
		t.Fatalf("the affected route reports the revision pause: %v", entry)
	}

	updated, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.HermesTargets["hermes-main"].MinimumVersion; got != "0.21.0" {
		t.Fatalf("the selected target's floor changed: %q", got)
	}
	if got := updated.HermesTargets["hermes-other"].MinimumVersion; got != "0.21.0" || updated.HermesTargets["hermes-other"].Board != "agent-dispatch-other" {
		t.Fatalf("the unrelated target is preserved: %+v", updated.HermesTargets["hermes-other"])
	}
	if len(updated.Routes) != len(before.Routes) || len(updated.Resources) != len(before.Resources) {
		t.Fatal("unrelated configuration must be preserved")
	}
	wikiAfter, _ := config.RouteRevision(updated, "wiki")
	if wikiAfter == wikiBefore {
		t.Fatal("the floor update must change the binding route's revision (the acknowledgement is stale)")
	}
}

func TestE15T2SetMinimumVersionRejectedCandidateLeavesFileUntouched(t *testing.T) {
	// A candidate the semantic layer would reject leaves the original
	// document untouched (CLI-015 atomicity through MutateHermesTarget).
	bin := stubhermes.Write(t)
	configPath := e15t2FloorConfig(t, bin, "0.20.5")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	// An unparseable floor is refused before any write.
	code := Run([]string{"hermes", "set-minimum-version", "--config", configPath, "hermes-main", "0.21"}, &out, &errb)
	if code == 0 || !strings.Contains(errb.String(), "minimum_version") {
		t.Fatalf("an unparseable floor must be refused: %s", errb.String())
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a refused candidate must leave the configuration untouched")
	}
}

func TestE15T2SingleProductFloorDefinition(t *testing.T) {
	// Round-1 F002: the config literal and the adapter's floor struct
	// are two representations of ONE floor; drift between them fails
	// verification instead of being bridged by an ignored parse.
	if config.MinimumEligibleHermesVersion != hermeskanban.MinimumEligibleVersion.String() {
		t.Fatalf("the product floor is defined twice and drifted: config %q vs adapter %q", config.MinimumEligibleHermesVersion, hermeskanban.MinimumEligibleVersion.String())
	}
}

func TestE15T2SetMinimumVersionRepairsLegacyFloor(t *testing.T) {
	// Round-1 F005: a configuration whose floor predates the product
	// floor (a below-floor value, or the field omitted) fails the
	// ordinary load — the floor helper is the documented remediation, so
	// it must repair such a document instead of refusing it, and a
	// candidate that would still be invalid leaves the file untouched.
	bin := stubhermes.Write(t)

	configPath := e15t2FloorConfig(t, bin, "0.20.5")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(string(raw), "minimum_version: 0.20.5", "minimum_version: 0.19.9", 1)
	if err := os.WriteFile(configPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"hermes", "set-minimum-version", "--config", configPath, "hermes-main", "0.20.5"}, &out, &errb)
	if code != 0 {
		t.Fatalf("the helper must remediate a legacy below-floor document: %s", errb.String())
	}
	fixed, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("the repaired document must pass every gate: %v", err)
	}
	if got := fixed.HermesTargets["hermes-main"].MinimumVersion; got != "0.20.5" {
		t.Fatalf("the repaired floor: %q", got)
	}

	// The omitted-field posture repairs the same way.
	omitted := strings.Replace(string(raw), "    minimum_version: 0.20.5\n", "", 1)
	if err := os.WriteFile(configPath, []byte(omitted), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	code = Run([]string{"hermes", "set-minimum-version", "--config", configPath, "hermes-main", "0.20.5"}, &out, &errb)
	if code != 0 {
		t.Fatalf("the helper must remediate an omitted floor: %s", errb.String())
	}
	if _, err := config.Load(configPath); err != nil {
		t.Fatalf("the repaired document must pass every gate: %v", err)
	}

	// A repair whose candidate would still be invalid leaves the file
	// untouched: an unparseable floor is refused before any write, even
	// over an invalid document.
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	code = Run([]string{"hermes", "set-minimum-version", "--config", configPath, "hermes-main", "0.20"}, &out, &errb)
	if code == 0 {
		t.Fatal("an unparseable floor must be refused even in repair")
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a refused repair must leave the invalid original untouched")
	}
}

func TestE15T2SubmitIgnoresOverwrittenCapabilityCacheAndHeals(t *testing.T) {
	// Regression for handoff 0fcef551-1e00-4ec8-94d4-aa06f1782081
	// (HER-019, HER-020, HER-021): when a target executable lacks
	// --mutex-key (such as Hermes v0.22.2, which rejects --mutex-key at
	// exit 2) and the capability cache file on disk was overwritten by a
	// foreign fixture's agent-dispatch-group-plus-target-mutex record,
	// the submit path must never use the overwritten record as evidence
	// for the live executable, must suppress --mutex-key, and must
	// refresh the cache back to agent-dispatch-group-enforced.
	bin := stubhermes.WriteVersioned(t, "Hermes Agent v0.22.2 (2026.9.24)")
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Replace(string(raw), " [--mutex-key KEY]", "", 1)
	if script == string(raw) {
		t.Fatal("the stub's create help does not carry --mutex-key to strip")
	}
	rejectMutex := "  create)\n" +
		"    printf '%s\\n' \"$*\" >> \"$DIR/argv-create\"\n" +
		"    for arg in \"$@\"; do\n" +
		"      if [ \"$arg\" = \"--mutex-key\" ]; then\n" +
		"        echo 'hermes: error: unrecognized arguments: --mutex-key' >&2\n" +
		"        exit 2\n" +
		"      fi\n" +
		"    done\n"
	script = strings.Replace(script, "  create)\n", rejectMutex, 1)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	configPath := e15t2FloorConfig(t, bin, "0.20.5")
	cfgRaw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, bytes.Replace(cfgRaw, []byte("enabled: false"), []byte("enabled: true"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, cfg.Resources["vault-main"].Root, false)
	t.Setenv("WATCHMAN_TRIGGER", "agent-dispatch.wiki.e15t2")
	e4t3RegisterRoute(t, configPath)
	revision, _ := config.RouteRevision(cfg, "wiki")
	var out, errb bytes.Buffer
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", revision, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("route enable: %s", errb.String())
	}

	// Pollute the capability cache with a foreign executable's
	// group-plus-target-mutex record.
	foreignBin := stubhermes.Write(t)
	foreignProber, err := hermeskanban.NewProber("hermes-main", foreignBin, "0.20.5", "agent-dispatch-test", "wiki-maintainer", hermeskanban.ProcessLimits{SubmitTimeout: 30e9, LookupTimeout: 30e9})
	if err != nil {
		t.Fatal(err)
	}
	foreignRecord, err := foreignProber.Probe(requestCtx())
	if err != nil {
		t.Fatal(err)
	}
	if foreignRecord.EffectiveSerializationMode() != hermeskanban.SerializationModeGroupPlusTargetMutex {
		t.Fatalf("foreign fixture must certify group-plus-target-mutex, got %s", foreignRecord.EffectiveSerializationMode())
	}
	cachePath := capabilityCachePath("hermes-main")
	if err := hermeskanban.WriteCapabilityRecord(foreignRecord, cachePath); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(cfg.Resources["vault-main"].Root, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Resources["vault-main"].Root, "Inbox", "new.md"), []byte("x"), 0o644); err != nil {
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
	if res["state"] != "accepted" || res["submitted"] != true {
		t.Fatalf("dispatch must be accepted without sending --mutex-key, got %v (stderr: %s)", res, errb.String())
	}
	argvLog, err := os.ReadFile(filepath.Join(filepath.Dir(bin), "state", "argv-create"))
	if err != nil {
		t.Fatalf("create argv log missing: %v", err)
	}
	if strings.Contains(string(argvLog), "--mutex-key") {
		t.Fatalf("renderer must never send --mutex-key to a target without it, got: %s", argvLog)
	}
	healed, err := hermeskanban.LoadCapabilityRecord(cachePath)
	if err != nil {
		t.Fatalf("healed capability record: %v", err)
	}
	if healed.ExecutablePath != bin || healed.EffectiveSerializationMode() != hermeskanban.SerializationModeGroupEnforced {
		t.Fatalf("cache must be healed to the live executable's group-enforced record, got path=%q mode=%q", healed.ExecutablePath, healed.EffectiveSerializationMode())
	}
}

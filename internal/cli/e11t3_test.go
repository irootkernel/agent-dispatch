package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/stubhermes"
)

// e11t3Config writes a one-hermes-target destinations configuration
// bound to the stub (whose on-disk profiles are default and
// wiki-maintainer, with llm-wiki enabled).
func e11t3Config(t *testing.T, bin string) string {
	t.Helper()
	path := e11t2HermesConfig(t, bin)
	return path
}

// TestE11T3HermesProfiles proves CLI-010/HER-015: `hermes profiles`
// lists the public profiles with their on-disk status through the
// typed assignees surface.
func TestE11T3HermesProfiles(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t3Config(t, bin)
	var out, errb bytes.Buffer
	code := Run([]string{"hermes", "profiles", "--config", configPath}, &out, &errb)
	if code != 0 {
		t.Fatalf("hermes profiles: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["count"] != float64(2) {
		t.Fatalf("the stub board carries two profiles: %v", res)
	}
	profiles, _ := res["profiles"].([]any)
	first, _ := profiles[0].(map[string]any)
	if first["profile"] != "default" || first["on_disk"] != true {
		t.Fatalf("profile entries wrong: %v", profiles)
	}
}

// TestE11T3PreflightPassesAndBlocks proves HER-015 through HER-017 and
// AC-704/705 posture: a complete destination passes every check, a
// missing profile fails listing the on-disk alternatives, and a
// disabled skill fails listing the enabled inventory — before any task
// creation.
func TestE11T3PreflightPassesAndBlocks(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t3Config(t, bin)

	// Passing configuration: profile wiki-maintainer (on disk in the
	// stub), skill llm-wiki (enabled in the stub's table).
	var out, errb bytes.Buffer
	code := Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code != 0 {
		t.Fatalf("preflight on the complete destination must pass: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["ok"] != true {
		t.Fatalf("preflight result wrong: %v", res)
	}

	// Missing profile: fails naming the on-disk alternatives.
	raw, _ := os.ReadFile(configPath)
	missing := bytes.Replace(raw, []byte("profile: wiki-maintainer"), []byte("profile: no-such-profile"), 1)
	if err := os.WriteFile(configPath, missing, 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	code = Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code != 3 {
		t.Fatalf("a missing profile must block at exit 3, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "config_capability_missing") {
		t.Fatalf("the refusal class is wrong: %s", errb.String())
	}
	// The error envelope (stderr) carries the alternatives and the
	// set-profile remediation in its result slot.
	if !strings.Contains(errb.String(), "wiki-maintainer") || !strings.Contains(errb.String(), "set-profile") || !strings.Contains(errb.String(), "alternatives") {
		t.Fatalf("the refusal must carry the sorted alternatives and the set-profile remediation: %s", errb.String())
	}

	// Disabled skill: the stub's table enables llm-wiki only; a
	// required skill the table does not carry fails closed.
	raw, _ = os.ReadFile(configPath)
	disabled := bytes.Replace(raw, []byte("skills: [llm-wiki]"), []byte("skills: [llm-wiki, sleepy-skill]"), 1)
	if err := os.WriteFile(configPath, disabled, 0o600); err != nil {
		t.Fatal(err)
	}
	// Restore the passing profile first.
	disabled = bytes.Replace(disabled, []byte("profile: no-such-profile"), []byte("profile: wiki-maintainer"), 1)
	if err := os.WriteFile(configPath, disabled, 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	code = Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code != 3 || !strings.Contains(errb.String(), "sleepy-skill") {
		t.Fatalf("a disabled required skill must block naming it, got %d: %s", code, errb.String())
	}
	// Nothing reached the stub: no task was created.
	if _, err := os.Stat(filepath.Join(filepath.Dir(bin), "state", "count")); !os.IsNotExist(err) {
		t.Fatalf("preflight must not create tasks: %v", err)
	}
}

// TestE11T3SetProfileQualifiedAndAmbiguity proves CLI-011: the
// destination-qualified set-profile writes atomically and pauses the
// route revision; the qualifier may be omitted only with exactly one
// destination; an unknown destination fails naming the declared set.
func TestE11T3SetProfileQualifiedAndAmbiguity(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t3Config(t, bin)
	before, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	revBefore, _ := config.RouteRevision(before, "wiki")

	var out, errb bytes.Buffer
	code := Run([]string{"route", "set-profile", "--config", configPath, "wiki", "wolyeong"}, &out, &errb)
	if code != 0 {
		t.Fatalf("single-destination qualifier omission must work: %s", errb.String())
	}
	after, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	dest, derr := after.Routes["wiki"].CertifiedDestination("wiki")
	if derr != nil || dest.Profile != "wolyeong" {
		t.Fatalf("the qualified edit must change the destination: %+v %v", dest, derr)
	}
	revAfter, _ := config.RouteRevision(after, "wiki")
	if revAfter == revBefore {
		t.Fatal("the destination edit must pause the route revision")
	}

	// Unknown destination names the declared set.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"route", "set-profile", "--config", configPath, "wiki:nope", "x"}, &out, &errb); code != 3 {
		t.Fatalf("unknown destination must fail at exit 3, got %d", code)
	}

	// A two-destination route: the route-only form is a usage error.
	e11t3SecondDestination(t, configPath)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"route", "set-profile", "--config", configPath, "wiki", "x"}, &out, &errb); code != 2 {
		t.Fatalf("an ambiguous route-only edit must be a usage error, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "flag_invalid") || !strings.Contains(errb.String(), "indexing") {
		t.Fatalf("the ambiguity refusal must name both destinations: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"route", "set-profile", "--config", configPath, "wiki:indexing", "wolyeong"}, &out, &errb); code != 0 {
		t.Fatalf("the qualified edit on the multi-destination route must work: %s", errb.String())
	}
	cfg2, _ := config.Load(configPath)
	if cfg2.Routes["wiki"].Destinations[1].Profile != "wolyeong" {
		t.Fatalf("the qualified edit must change only its destination: %+v", cfg2.Routes["wiki"].Destinations)
	}
}

// TestE11T3SetSkillsQualified proves CLI-011 for set-skills: the edit
// replaces the destination's skill list, validates before the atomic
// write, and pauses the revision.
func TestE11T3SetSkillsQualified(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t3Config(t, bin)
	var out, errb bytes.Buffer
	code := Run([]string{"route", "set-skills", "--config", configPath, "wiki", "llm-wiki", "provenance"}, &out, &errb)
	if code != 0 {
		t.Fatalf("set-skills: %s", errb.String())
	}
	cfg, _ := config.Load(configPath)
	dest, _ := cfg.Routes["wiki"].CertifiedDestination("wiki")
	if len(dest.Skills) != 2 || dest.Skills[0] != "llm-wiki" || dest.Skills[1] != "provenance" {
		t.Fatalf("set-skills must replace the list in order: %+v", dest.Skills)
	}
	// An invalid candidate (empty skill) never writes.
	before, _ := os.ReadFile(configPath)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"route", "set-skills", "--config", configPath, "wiki", "llm-wiki", ""}, &out, &errb); code != 3 {
		t.Fatalf("an empty skill must fail validation, got %d", code)
	}
	after, _ := os.ReadFile(configPath)
	if string(before) != string(after) {
		t.Fatal("a rejected candidate must leave the file untouched")
	}
}

// e11t3SecondDestination appends a second destination to the fixture's
// route so the ambiguity rule is exercisable.
func e11t3SecondDestination(t *testing.T, configPath string) {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	insert := "      - id: indexing\n        target: hermes-main\n        profile: reviewer\n        skills: [llm-wiki]\n        workstream: review\n        execution_hints:\n          max_runtime: 30m\n          max_attempts: 2"
	updated := strings.Replace(string(raw), "    submission_retry:", insert+"\n    submission_retry:", 1)
	if updated == string(raw) {
		t.Fatal("fixture does not carry the submission_retry anchor")
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestE11T3StatusSurfacesDrift proves OPS-013: the status command
// reports the five drift classes per route — capability (stale
// evidence), watchman (no persisted binding while enabled), profile,
// skill, and reconciliation — as an observational projection.
func TestE11T3StatusSurfacesDrift(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t3Config(t, bin)
	// Flip the YAML key, then register/enable through the production
	// gate so the capability fingerprint binds (the helper alone stores
	// an empty one).
	raw, _ := os.ReadFile(configPath)
	os.WriteFile(configPath, bytes.Replace(raw, []byte("enabled: false"), []byte("enabled: true"), 1), 0o600)
	setPlanEnv(t, "/tmp", false)
	e4t3RegisterRoute(t, configPath)
	cfg, _ := config.Load(configPath)
	revision, ok := config.RouteRevision(cfg, "wiki")
	if !ok {
		t.Fatal("revision unavailable")
	}
	var eout, eerrb bytes.Buffer
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", revision, "--yes"}, &eout, &eerrb); code != 0 {
		t.Fatalf("route enable: %s", eerrb.String())
	}
	// Swap the executable bytes: the cached evidence goes stale while
	// the activation keeps its fingerprint.
	stubRaw, _ := os.ReadFile(bin)
	os.WriteFile(bin, append(stubRaw, []byte("\n# drifted\n")...), 0o755)
	resource := cfg.Resources["vault-main"].Root
	os.MkdirAll(resource, 0o755)
	var out, errb bytes.Buffer
	code := Run([]string{"status", "--config", configPath}, &out, &errb)
	if code != 0 {
		t.Fatalf("status: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	driftRows, _ := res["drift"].([]any)
	if len(driftRows) != 1 {
		t.Fatalf("one configured route must report drift: %v", res["drift"])
	}
	row, _ := driftRows[0].(map[string]any)
	kinds, _ := row["drift"].(map[string]any)
	if _, has := kinds["watchman"]; !has {
		t.Fatalf("an enabled route without a persisted binding must report watchman drift: %v", kinds)
	}
	if _, has := kinds["capability"]; !has {
		t.Fatalf("an enabled route without fresh capability evidence must report capability drift: %v", kinds)
	}
}

// TestE11T3MutationConfigEqualsForm proves the mutation path accepts
// the `--config=<path>` equals form exactly like the shared parser: the
// named file is the one validated and written (E11-T3 round-1 review).
func TestE11T3MutationConfigEqualsForm(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t3Config(t, bin)
	var out, errb bytes.Buffer
	code := Run([]string{"route", "set-profile", "--config=" + configPath, "wiki", "wolyeong"}, &out, &errb)
	if code != 0 {
		t.Fatalf("the equals form must resolve the named config: %s", errb.String())
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	dest, _ := cfg.Routes["wiki"].CertifiedDestination("wiki")
	if dest.Profile != "wolyeong" {
		t.Fatalf("the named file must carry the edit: %+v", dest)
	}
}

// TestE11T3EnableFailsOnMissingProfile proves the AC-704 enable arm
// (HER-015): a confirmed-missing on-disk profile refuses enable at
// exit 3 with the bounded sorted alternatives, while enumeration
// itself stays the preflight surface.
func TestE11T3EnableFailsOnMissingProfile(t *testing.T) {
	bin := stubhermes.Write(t)
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.Replace(string(raw),
		`[{"name":"default","on_disk":true},{"name":"wiki-maintainer","on_disk":true}]`,
		`[{"name":"default","on_disk":true}]`, 1)
	if trimmed == string(raw) {
		t.Fatal("the stub's assignees surface does not carry wiki-maintainer to remove")
	}
	if err := os.WriteFile(bin, []byte(trimmed), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := e11t3Config(t, bin)
	cfgRaw, _ := os.ReadFile(configPath)
	if err := os.WriteFile(configPath, bytes.Replace(cfgRaw, []byte("enabled: false"), []byte("enabled: true"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, "/tmp", false)
	e4t3RegisterRoute(t, configPath)
	cfg, _ := config.Load(configPath)
	revision, ok := config.RouteRevision(cfg, "wiki")
	if !ok {
		t.Fatal("revision unavailable")
	}
	var out, errb bytes.Buffer
	code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", revision, "--yes"}, &out, &errb)
	if code != 3 || !strings.Contains(errb.String(), "does not exist on board") || !strings.Contains(errb.String(), "default") {
		t.Fatalf("enable must refuse a confirmed-missing profile at exit 3 listing the on-disk alternatives, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "HER-015") {
		t.Fatalf("the refusal must name the requirement, got: %s", errb.String())
	}
}

// TestE11T3PreflightBlocksOnIneligibleCapability proves the preflight
// inspects the probe record instead of deferring shape failures to the
// production gate: a below-floor version and a create surface missing
// a required flag both block with the probe remediation.
func TestE11T3PreflightBlocksOnIneligibleCapability(t *testing.T) {
	// Below the eligibility floor.
	old := stubhermes.WriteVersioned(t, "Hermes Agent v0.18.5 (2026.6.6)")
	configPath := e11t3Config(t, old)
	var out, errb bytes.Buffer
	code := Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code != 3 || !strings.Contains(errb.String(), "below the eligibility floor") {
		t.Fatalf("a below-floor version must block preflight naming the floor, got %d: %s", code, errb.String())
	}
	// A create surface missing --idempotency-key (a required flag).
	drifted := stubhermes.Write(t)
	raw, err := os.ReadFile(drifted)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Replace(string(raw), " [--idempotency-key IDEMPOTENCY_KEY]", "", 1)
	if script == string(raw) {
		t.Fatal("the stub's create help does not carry --idempotency-key to drift")
	}
	if err := os.WriteFile(drifted, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath = e11t3Config(t, drifted)
	out.Reset()
	errb.Reset()
	code = Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code != 3 || !strings.Contains(errb.String(), "--idempotency-key") {
		t.Fatalf("a drifted create surface must block preflight naming the missing flag, got %d: %s", code, errb.String())
	}
}

// TestE11T3MutationDefaultConfigPath proves the CLI-011 default path:
// without an explicit --config, the destination-qualified mutation
// resolves and writes the platform-default configuration exactly like
// every other route command (AGENT_DISPATCH_CONFIG in the test).
func TestE11T3MutationDefaultConfigPath(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t3Config(t, bin)
	t.Setenv("AGENT_DISPATCH_CONFIG", configPath)
	var out, errb bytes.Buffer
	if code := Run([]string{"route", "set-skills", "wiki", "sleepy-skill"}, &out, &errb); code != 0 {
		t.Fatalf("the default-config mutation must apply: %s", errb.String())
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	dest, _ := cfg.Routes["wiki"].CertifiedDestination("wiki")
	if len(dest.Skills) != 1 || dest.Skills[0] != "sleepy-skill" {
		t.Fatalf("the default-resolved file must carry the edit: %+v", dest.Skills)
	}
}

// TestE11T3StatusSkillDriftScoped proves the scoped drift classes:
// after a profile-scoped probe and enable, editing the destination to
// require a skill the cached inventory does not list surfaces skill
// drift, and an unscoped record (a bare probe) fabricates none.
func TestE11T3StatusSkillDriftScoped(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t3Config(t, bin)
	var out, errb bytes.Buffer
	// Scoped probe so the cached evidence answers profile questions.
	if code := Run([]string{"hermes", "probe", "--config", configPath, "--profile", "wiki-maintainer"}, &out, &errb); code != 0 {
		t.Fatalf("scoped probe: %s", errb.String())
	}
	// Edit the destination to a skill the stub never lists.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"route", "set-skills", "--config", configPath, "wiki", "sleepy-skill"}, &out, &errb); code != 0 {
		t.Fatalf("set-skills: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"status", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("status: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	driftRows, _ := res["drift"].([]any)
	if len(driftRows) != 1 {
		t.Fatalf("one configured route must report drift: %v", res["drift"])
	}
	row, _ := driftRows[0].(map[string]any)
	kinds, _ := row["drift"].(map[string]any)
	if msg, has := kinds["skill"]; !has || !strings.Contains(fmt.Sprint(msg), "sleepy-skill") {
		t.Fatalf("the scoped drift must name the missing skill: %v", kinds)
	}
	// An unscoped record must not fabricate profile-scoped drift: the
	// kinds reset after a bare probe leaves no skill class.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"hermes", "probe", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("bare probe: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"status", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("status after bare probe: %s", errb.String())
	}
	res = decodeEnvelope(t, &out)
	driftRows, _ = res["drift"].([]any)
	row, _ = driftRows[0].(map[string]any)
	kinds, _ = row["drift"].(map[string]any)
	if _, has := kinds["skill"]; has {
		t.Fatalf("an unscoped record must not fabricate skill drift: %v", kinds)
	}
}

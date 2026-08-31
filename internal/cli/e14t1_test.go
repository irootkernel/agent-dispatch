package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/stubhermes"
)

// E14-T1 (CLI-016, AC-1001, AC-1002): the guided walkthrough selects
// exactly one route explicitly — --route, a single-route configuration's
// only choice, or an interactive selection — and carries that identity
// through every route-scoped step and instruction. The former
// sorted-first fallback is gone.

// e14t1TwoRouteConfig writes a disabled two-route configuration. The
// sorted-first route is "journal" and every selection test drives
// "wiki", so an assertion on "wiki" proves the explicit selection and
// not the old sorted-order fallback.
func e14t1TwoRouteConfig(t *testing.T, bin string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AGENT_DISPATCH_CONFIG", "")
	t.Setenv("AGENT_DISPATCH_STATE_DIR", filepath.Join(home, "state"))
	dir := t.TempDir()
	route := func(id, sourceID, trigger string) string {
		return `
  ` + id + `:
    enabled: false
    source:
      type: watchman-trigger
      source_id: ` + sourceID + `
      resource: vault-main
      trigger_name: ` + trigger + `
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
	}
	cfg := `version: 1
instance:
  id: e14t1-cli
resources:
  vault-main:
    type: directory
    root: ` + filepath.Join(dir, "vault") + `
    file_scope: markdown
hermes_targets:
  hermes-main:
    board: agent-dispatch-test
    minimum_version: 0.20.5
    compatibility: capability_probe
    executable: ` + bin + `
    submit_timeout: 30s
    lookup_timeout: 30s
    environment_allowlist: [PATH, HOME]
routes:` + route("journal", "vault-main-watchman-journal", "agent-dispatch.journal.e14t1") + route("wiki", "vault-main-watchman-wiki", "agent-dispatch.wiki.e14t1")
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// e14t1PrepareVault creates the resource root the reconciliation step
// enumerates, mirroring the G7 walkthrough fixture.
func e14t1PrepareVault(t *testing.T, configPath string) {
	t.Helper()
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Resources["vault-main"].Root, 0o755); err != nil {
		t.Fatal(err)
	}
}

// e14t1AssertWalkthroughNamesRoute proves AC-1001 for the deterministic
// surfaces: the printed enable command, the Watchman install/test
// guidance, and the baseline step's envelope name the selected route,
// and no instruction points at the unselected route.
func e14t1AssertWalkthroughNamesRoute(t *testing.T, configPath, stdout, stderr, route string) {
	t.Helper()
	if !strings.Contains(stdout, "route enable --route "+route+" --config "+configPath+" ") {
		t.Fatalf("the enable guidance must name route %s: %s", route, stdout)
	}
	if !strings.Contains(stderr, "watchman install --route "+route+" ") {
		t.Fatalf("the Watchman install guidance must name route %s: %s", route, stderr)
	}
	if !strings.Contains(stderr, "watchman test --route "+route+" ") {
		t.Fatalf("the Watchman test guidance must name route %s: %s", route, stderr)
	}
	// The baseline step's result envelope carries the driven route (the
	// E14-T3 walkthrough baselines instead of the old advisory dry run).
	if !strings.Contains(stdout, "\"route_id\":\""+route+"\"") {
		t.Fatalf("the baseline step envelope must name route %s: %s", route, stdout)
	}
	for _, wrong := range []string{"journal"} {
		if wrong == route {
			continue
		}
		if strings.Contains(stdout, "route enable --route "+wrong) ||
			strings.Contains(stdout, "\"route_id\":\""+wrong+"\"") ||
			strings.Contains(stderr, "watchman install --route "+wrong) ||
			strings.Contains(stderr, "watchman test --route "+wrong) {
			t.Fatalf("the walkthrough must not drive or point at the unselected route %s", wrong)
		}
	}
}

// TestE14T1NonInteractiveMultiRouteRefusesInsteadOfSortedFirst proves
// AC-1002: with multiple routes and no selection, non-interactive
// execution fails without choosing the lexicographically first route.
func TestE14T1NonInteractiveMultiRouteRefusesInsteadOfSortedFirst(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e14t1TwoRouteConfig(t, bin)
	e14t1PrepareVault(t, configPath)
	setPlanEnv(t, "/tmp", false)
	var out, errb bytes.Buffer
	var code int
	withStdin(t, "", func() {
		code = Run([]string{"setup", "wiki", "--config", configPath}, &out, &errb)
	})
	if code != 2 {
		t.Fatalf("a multi-route walkthrough without a selection must fail with a usage error: %d %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "an explicit route selection is required") {
		t.Fatalf("the refusal must explain the missing selection: %s", errb.String())
	}
	if !strings.Contains(errb.String(), "journal, wiki") {
		t.Fatalf("the refusal must name the declared routes: %s", errb.String())
	}
	if strings.Contains(out.String(), "setup complete") || strings.Contains(out.String(), "route enable") {
		t.Fatalf("a refused walkthrough must not reach the gate summary: %s", out.String())
	}
}

// TestE14T1InteractiveMultiRouteSelectionHonored proves AC-1002: the
// numbered interactive choice is rendered and honored, and the
// walkthrough then drives exactly the selected (non-sorted-first)
// route.
func TestE14T1InteractiveMultiRouteSelectionHonored(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e14t1TwoRouteConfig(t, bin)
	e14t1PrepareVault(t, configPath)
	setPlanEnv(t, "/tmp", false)
	var out, errb bytes.Buffer
	var code int
	withStdin(t, "2\n", func() {
		code = Run([]string{"setup", "wiki", "--config", configPath}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("the interactive walkthrough must complete: %d %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "1) journal") || !strings.Contains(errb.String(), "2) wiki") {
		t.Fatalf("the route choice must render every route: %s", errb.String())
	}
	if !strings.Contains(errb.String(), `the walkthrough drives the selected route "wiki"`) {
		t.Fatalf("the selection must be confirmed: %s", errb.String())
	}
	e14t1AssertWalkthroughNamesRoute(t, configPath, out.String(), errb.String(), "wiki")
}

// TestE14T1InteractiveRouteIDAnswerAndGarbageRefused proves the
// interactive reader accepts the exact route ID and refuses an answer
// that names no declared route.
func TestE14T1InteractiveRouteIDAnswerAndGarbageRefused(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e14t1TwoRouteConfig(t, bin)
	e14t1PrepareVault(t, configPath)
	setPlanEnv(t, "/tmp", false)
	var out, errb bytes.Buffer
	var code int
	withStdin(t, "wiki\n", func() {
		code = Run([]string{"setup", "wiki", "--config", configPath}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("answering with the route ID must select it: %d %s", code, errb.String())
	}
	e14t1AssertWalkthroughNamesRoute(t, configPath, out.String(), errb.String(), "wiki")

	out.Reset()
	errb.Reset()
	withStdin(t, "archive\n", func() {
		code = Run([]string{"setup", "wiki", "--config", configPath}, &out, &errb)
	})
	if code != 2 || !strings.Contains(errb.String(), "an explicit route selection is required") {
		t.Fatalf("an answer naming no declared route must be refused: %d %s", code, errb.String())
	}
}

// TestE14T1ExplicitRouteFlagDrivesNamedRoute proves AC-1001 through the
// flag: --route drives the named (non-sorted-first) route end to end
// with no interactive input.
func TestE14T1ExplicitRouteFlagDrivesNamedRoute(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e14t1TwoRouteConfig(t, bin)
	e14t1PrepareVault(t, configPath)
	setPlanEnv(t, "/tmp", false)
	var out, errb bytes.Buffer
	var code int
	withStdin(t, "", func() {
		code = Run([]string{"setup", "wiki", "--config", configPath, "--route", "wiki"}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("the explicit selection must complete without any prompt: %d %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), `the walkthrough drives the explicitly selected route "wiki"`) {
		t.Fatalf("the explicit selection must be reported: %s", errb.String())
	}
	e14t1AssertWalkthroughNamesRoute(t, configPath, out.String(), errb.String(), "wiki")
}

// TestE14T1UnknownRouteRefused proves --route validation: an unknown
// route ID is a usage error naming the declared routes.
func TestE14T1UnknownRouteRefused(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e14t1TwoRouteConfig(t, bin)
	setPlanEnv(t, "/tmp", false)
	var out, errb bytes.Buffer
	code := Run([]string{"setup", "wiki", "--config", configPath, "--route", "archive"}, &out, &errb)
	if code != 2 {
		t.Fatalf("an unknown route must be a usage error: %d", code)
	}
	if !strings.Contains(errb.String(), "does not declare route archive") || !strings.Contains(errb.String(), "journal, wiki") {
		t.Fatalf("the refusal must name the route and the declared set: %s", errb.String())
	}
}

// TestE14T1RouteSpellingsCovered proves both --route spellings and the
// valueless trailing form: `--route=<id>` selects the route, and a
// trailing `--route` with no value leaves the selection unset so a
// multi-route configuration is refused.
func TestE14T1RouteSpellingsCovered(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e14t1TwoRouteConfig(t, bin)
	e14t1PrepareVault(t, configPath)
	setPlanEnv(t, "/tmp", false)
	var out, errb bytes.Buffer
	var code int
	withStdin(t, "", func() {
		code = Run([]string{"setup", "wiki", "--config", configPath, "--route=wiki"}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("the --route=<id> spelling must select the route: %d %s", code, errb.String())
	}
	e14t1AssertWalkthroughNamesRoute(t, configPath, out.String(), errb.String(), "wiki")

	out.Reset()
	errb.Reset()
	withStdin(t, "", func() {
		code = Run([]string{"setup", "wiki", "--config", configPath, "--route"}, &out, &errb)
	})
	if code != 2 || !strings.Contains(errb.String(), "--route requires a value") {
		t.Fatalf("a valueless trailing --route must be a usage error like every other route-flag command: %d %s", code, errb.String())
	}
	if strings.Contains(out.String(), "setup complete") {
		t.Fatalf("the malformed flag must not reach the gate summary: %s", out.String())
	}
}

// TestE14T1SingleRouteAutoSelects proves the single-route posture: the
// only route is selected automatically with the informational step, and
// the walkthrough still reaches the disabled gate summary.
func TestE14T1SingleRouteAutoSelects(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := g7Config(t, bin)
	e14t1PrepareVault(t, configPath)
	setPlanEnv(t, "/tmp", false)
	var out, errb bytes.Buffer
	var code int
	withStdin(t, "", func() {
		code = Run([]string{"setup", "wiki", "--config", configPath}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("a single-route walkthrough must complete without a prompt: %d %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), `the configuration declares one route; the walkthrough uses "wiki"`) {
		t.Fatalf("the automatic selection must be reported: %s", errb.String())
	}
	e14t1AssertWalkthroughNamesRoute(t, configPath, out.String(), errb.String(), "wiki")
}

// TestE14EpicAuditSetupFlagValuesStrict proves the epic-audit flag
// contract: a valueless --config, an empty =value, and a value that is
// itself the next flag are usage errors, never silent fallbacks.
func TestE14EpicAuditSetupFlagValuesStrict(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	setPlanEnv(t, "/tmp", false)
	for _, argv := range [][]string{
		{"setup", "wiki", "--config"},
		{"setup", "wiki", "--config="},
		{"setup", "wiki", "--config", "--route", "wiki"},
		{"setup", "wiki", "--route="},
		{"setup", "wiki", "--config", configPath, "--route", "--config"},
	} {
		var out, errb bytes.Buffer
		var code int
		withStdin(t, "", func() {
			code = Run(argv, &out, &errb)
		})
		if code != 2 {
			t.Fatalf("%v must be a usage error: %d", argv, code)
		}
		if !strings.Contains(errb.String(), "requires a value") {
			t.Fatalf("%v must name the valueless flag: %s", argv, errb.String())
		}
		if strings.Contains(out.String(), "setup complete") {
			t.Fatalf("%v must not reach the gate summary: %s", argv, out.String())
		}
	}
}

package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/crashbinbuild"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/stubhermes"
)

// G10 acceptance (E14-T3, TST-015, AC-1001 through AC-1005): the guided
// walkthrough baselines while the route stays disabled and reaches an
// honest five-state production-gate summary in every rerun posture —
// clean host, Watchman installed, materialized runtime row, unchanged
// rerun, and interrupted rerun — with the exact enable command printed
// and never executed.

// g10Setup runs one walkthrough and returns its streams. The caller
// owns setPlanEnv so every rerun posture in one test shares a single
// state store (the rerun semantics under proof).
func g10Setup(t *testing.T, configPath string, extra ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	var code int
	argv := append([]string{"setup", "wiki", "--config", configPath}, extra...)
	withStdin(t, "", func() {
		code = Run(argv, &out, &errb)
	})
	return code, out.String(), errb.String()
}

// g10AssertGateSummary proves the five distinct states and the
// print-only enable command (OPS-016, AC-1005).
func g10AssertGateSummary(t *testing.T, configPath, out string) {
	t.Helper()
	for _, want := range []string{
		"setup complete; the route is DISABLED and nothing was submitted",
		"production-gate summary:",
		"configuration enabled:      disabled",
		"runtime activation:         ",
		"Watchman binding:           ",
		"initial baseline:           established (",
		"production acknowledgement: ",
		"route enable --route wiki --config " + configPath + " --acknowledge-production-gate ",
		"setup never enables the route or accepts production approval implicitly",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the gate summary must contain %q:\n%s", want, out)
		}
	}
}

// g10AssertControlsOff proves both enable controls remain off and no
// production row exists after the walkthrough (AC-1003's controls-off
// clause, AC-1004's zero-row clause).
func g10AssertControlsOff(t *testing.T, configPath string) {
	t.Helper()
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if route := cfg.Routes["wiki"]; route.Enabled {
		t.Fatal("setup must leave the configuration key off")
	}
	store, err := openUnmigratedStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if present, _ := store.RouteRuntimeStatePresent(ctx, "wiki"); present {
		if snap, lerr := store.LoadRouteState(ctx, "wiki"); lerr == nil {
			if snap.ActivationState != "disabled" {
				t.Fatalf("setup must leave the runtime half disabled, got %q", snap.ActivationState)
			}
			if snap.AcknowledgedRevision != "" {
				t.Fatal("setup must never record a production acknowledgement")
			}
		}
	}
	for _, table := range []string{"policy_decisions", "dispatch_intents", "notification_events"} {
		var n int
		if err := store.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("setup must create zero rows in %s: %d", table, n)
		}
	}
}

// TestG10AC1003CleanHostReachesDisabledGate proves the clean-host
// posture: nothing registered, nothing installed — the walkthrough
// baselines and reaches the five-state summary.
func TestG10AC1003CleanHostReachesDisabledGate(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.Resources["vault-main"].Root, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Resources["vault-main"].Root, "Inbox", "a.md"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, "/tmp", false)
	code, out, errb := g10Setup(t, configPath)
	if code != 0 {
		t.Fatalf("the clean-host walkthrough must complete: %d %s", code, errb)
	}
	g10AssertGateSummary(t, configPath, out)
	if !strings.Contains(out, "runtime activation:         disabled (no runtime state") {
		t.Fatalf("a clean host must report the absent runtime state honestly:\n%s", out)
	}
	if !strings.Contains(out, "Watchman binding:           not installed") {
		t.Fatalf("a clean host must report the binding as not installed:\n%s", out)
	}
	if !strings.Contains(out, "production acknowledgement: not acknowledged") {
		t.Fatalf("a clean host must report no acknowledgement:\n%s", out)
	}
	g10AssertControlsOff(t, configPath)
}

// g10RegisterDisabledRoute materializes the route rows in their
// disabled posture (no acknowledgement, no activation): the honest
// Watchman-installed-but-not-enabled rerun state.
func g10RegisterDisabledRoute(t *testing.T, configPath string) {
	t.Helper()
	cfg, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		t.Fatal(err)
	}
	route := cfg.Routes["wiki"]
	stateDir := platformpaths.ResolveStateDir(cfg.Instance.StateDir)
	store, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(filepath.Join(stateDir, "backups")); err != nil {
		t.Fatal(err)
	}
	root := cfg.Resources[route.Source.Resource].Root
	if err := store.RegisterResource(nil, route.Source.Resource, "res-rev-g10", root, root, cfg.Resources[route.Source.Resource].FileScope, "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterRoute(nil, "wiki", "route-rev-g10", "policy-rev-g10", route.Source.Resource, "hermes-main", "{}", "2026-08-31T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if present, err := store.RouteRuntimeStatePresent(context.Background(), "wiki"); err != nil {
		t.Fatal(err)
	} else if !present {
		if err := store.InitializeRouteState(nil, "wiki"); err != nil {
			t.Fatal(err)
		}
	}
}

// TestG10AC1003WatchmanInstalledRerun proves the Watchman-installed
// posture: with the trigger installed (a registered route plus a stored
// binding), the rerun still reaches the gate summary with the binding
// state reported.
func TestG10AC1003WatchmanInstalledRerun(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Resources["vault-main"].Root, 0o755); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, "/tmp", false)
	// The Watchman-installed posture on a still-disabled route: the
	// materialized rows, a stored binding, and no acknowledgement.
	g10RegisterDisabledRoute(t, configPath)
	root := cfg.Resources["vault-main"].Root
	store, err := openUnmigratedStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWatchBinding(context.Background(), watchman.Binding{
		RouteID: "wiki", ResourceID: "vault-main",
		ConfiguredRoot: root, ActualRoot: root, RelativeRoot: ".",
		TriggerName: "agent-dispatch.wiki.g10", UpdatedAt: "2026-08-31T00:00:00Z",
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	store.Close()
	code, out, errb := g10Setup(t, configPath)
	if code != 0 {
		t.Fatalf("the Watchman-installed rerun must complete: %d %s", code, errb)
	}
	g10AssertGateSummary(t, configPath, out)
	if !strings.Contains(out, "Watchman binding:           installed (trigger agent-dispatch.wiki.g10)") {
		t.Fatalf("the installed binding must be reported:\n%s", out)
	}
	if !strings.Contains(out, "runtime activation:         disabled") {
		t.Fatalf("the materialized disabled row must be reported:\n%s", out)
	}
	g10AssertControlsOff(t, configPath)
}

// TestG10AC1003UnchangedRerunIsIdempotent proves the unchanged-rerun
// posture: a second walkthrough with no changes converges on the same
// summary, replaces (never duplicates) the baseline, and stays off.
func TestG10AC1003UnchangedRerunIsIdempotent(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Resources["vault-main"].Root, 0o755); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, "/tmp", false)
	code, out, errb := g10Setup(t, configPath)
	if code != 0 {
		t.Fatalf("the first walkthrough: %d %s", code, errb)
	}
	g10AssertGateSummary(t, configPath, out)
	code2, out2, errb2 := g10Setup(t, configPath)
	if code2 != 0 {
		t.Fatalf("the unchanged rerun must complete: %d %s", code2, errb2)
	}
	g10AssertGateSummary(t, configPath, out2)
	store, err := openUnmigratedStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var rows int
	if err := store.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM route_baselines").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("one route keeps exactly one baseline row across reruns: %d", rows)
	}
	g10AssertControlsOff(t, configPath)
}

// TestG10AC1003InterruptedRerunConverges proves the interrupted-rerun
// posture: a baseline process dying after its enumeration leaves the
// previous baseline intact, and the walkthrough rerun converges on the
// complete new baseline while staying disabled.
func TestG10AC1003InterruptedRerunConverges(t *testing.T) {
	// Build the harness before the fixture redirects HOME: the build's
	// module cache must not land inside the test's temporary tree.
	crashbin := crashbinbuild.Build(t)
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	root := cfg.Resources["vault-main"].Root
	if err := os.MkdirAll(filepath.Join(root, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Inbox", "a.md"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errb := g10Setup(t, configPath)
	if code != 0 {
		t.Fatalf("the first walkthrough: %d %s", code, errb)
	}
	g10AssertGateSummary(t, configPath, out)
	// The vault changes and a baseline attempt dies after its enumeration,
	// before the fenced transaction (the interrupted posture).
	if err := os.WriteFile(filepath.Join(root, "Inbox", "b.md"), []byte("beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := os.Getenv("AGENT_DISPATCH_STATE_DIR")
	crashed := exec.Command(crashbin, "baseline", "--db", filepath.Join(stateDir, StateDBName),
		"--vault", root, "--route", "wiki", "--resource", "vault-main", "--window", "die-before-write")
	if cout, cerr := crashed.CombinedOutput(); cerr == nil || !strings.Contains(string(cout), "dying before the baseline transaction") {
		t.Fatalf("the crash window must die hard: %v %s", cerr, cout)
	}
	code2, out2, errb2 := g10Setup(t, configPath)
	if code2 != 0 {
		t.Fatalf("the interrupted rerun must converge: %d %s", code2, errb2)
	}
	g10AssertGateSummary(t, configPath, out2)
	if !strings.Contains(out2, "initial baseline:           established (2 facts") {
		t.Fatalf("the converged baseline must carry both facts:\n%s", out2)
	}
	g10AssertControlsOff(t, configPath)
}

// TestG10AC1005EnableCommandRenderedNotExecuted proves the exact enable
// command is the reviewed one and no execution path exists: the
// acknowledgement stays absent and no dispatch row appears.
func TestG10AC1005EnableCommandRenderedNotExecuted(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Resources["vault-main"].Root, 0o755); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, "/tmp", false)
	code, out, errb := g10Setup(t, configPath)
	if code != 0 {
		t.Fatalf("the walkthrough: %d %s", code, errb)
	}
	revision, ok := config.RouteRevision(cfg, "wiki")
	if !ok {
		t.Fatal("the route revision must compute")
	}
	if !strings.Contains(out, "--acknowledge-production-gate "+revision+" --yes") {
		t.Fatalf("the enable command must render the exact computed revision %s:\n%s", revision, out)
	}
	g10AssertControlsOff(t, configPath)
}

// TestG10FreshGenerateWithoutVaultRootStopsAtBaseline pins the first-use
// posture the walkthrough's own generate path invites: a host with no
// configuration names a vault root that does not exist yet. Setup never
// creates the operator's vault, so the missing root is a genuine finding
// and the walkthrough stops at the baseline step — exit 3 with the
// re-run guidance — before the five-state summary, leaving the generated
// disabled configuration in place so the rerun after the vault exists
// converges (E14-T3; cold-validation round-1 F001).
func TestG10FreshGenerateWithoutVaultRootStopsAtBaseline(t *testing.T) {
	bin := stubhermes.Write(t)
	// The generated example resolves its Hermes through PATH as the
	// bare `hermes` command.
	stubDir := t.TempDir()
	if err := os.Symlink(bin, filepath.Join(stubDir, "hermes")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	setPlanEnv(t, "/tmp", false)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent-dispatch.yaml")
	missingRoot := filepath.Join(dir, "vault-that-does-not-exist")
	var out, errb bytes.Buffer
	var code int
	withStdin(t, missingRoot+"\n", func() {
		code = Run([]string{"setup", "wiki", "--config", configPath}, &out, &errb)
	})
	if code != 3 {
		t.Fatalf("the missing-root walkthrough must stop at the baseline step with exit 3: %d %s", code, errb.String())
	}
	combined := out.String() + errb.String()
	if strings.Contains(combined, "production-gate summary:") {
		t.Fatalf("the walkthrough must not reach the gate summary:\n%s", combined)
	}
	if !strings.Contains(errb.String(), "the initial baseline did not complete") {
		t.Fatalf("the stop must carry the re-run guidance:\n%s", errb.String())
	}
	// The generated configuration is written disabled and stays for the
	// rerun once the vault exists.
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routes) != 1 {
		t.Fatalf("the generated example must carry its single route: %d", len(cfg.Routes))
	}
	for id, route := range cfg.Routes {
		if route.Enabled {
			t.Fatalf("the generated route %s must stay disabled", id)
		}
	}
}

// TestG10GateSummaryDegradedPathsLabelTheirCause proves the honesty
// contract's degraded half (OPS-016, AC-1005): when a fact cannot be
// read, its summary line names the failed read — not the store open,
// and never an invented state — so a load failure after a successful
// open is labeled by its own cause (cold-validation round-2 F002).
func TestG10GateSummaryDegradedPathsLabelTheirCause(t *testing.T) {
	dir := t.TempDir()
	routeID := "wiki-maintenance"
	// degraded builds one config whose state directory holds a migrated
	// store with the named tables dropped, so the store opens while the
	// reads fail deterministically.
	degraded := func(name string, drop ...string) string {
		t.Helper()
		stateDir := filepath.Join(dir, name)
		if err := os.MkdirAll(stateDir, 0o700); err != nil {
			t.Fatal(err)
		}
		dbPath := filepath.Join(stateDir, StateDBName)
		store, err := sqlite.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Migrate(filepath.Join(stateDir, "backups")); err != nil {
			t.Fatal(err)
		}
		for _, table := range drop {
			if _, err := store.Exec(`DROP TABLE ` + table); err != nil {
				t.Fatal(err)
			}
		}
		store.Close()
		cfg := config.Example("g10-degraded", filepath.Join(dir, name+"-vault"))
		cfg.Instance.StateDir = stateDir
		configPath := filepath.Join(dir, name+".yaml")
		if err := config.WriteExample(cfg, configPath); err != nil {
			t.Fatal(err)
		}
		loaded, err := config.Load(configPath)
		if err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		setupGateSummary(&setupUI{stdout: &out, stderr: &errb}, loaded, configPath, routeID)
		return out.String()
	}

	// Both runtime reads fail: each line names its own failed read.
	both := degraded("both-missing", "route_runtime_state", "route_baselines")
	for _, want := range []string{
		"runtime activation:         unreadable (runtime state could not be loaded)",
		"production acknowledgement: unreadable (acknowledgement could not be loaded)",
		"initial baseline:           unreadable (baseline could not be loaded)",
	} {
		if !strings.Contains(both, want) {
			t.Fatalf("the degraded summary must label its own failed read %q:\n%s", want, both)
		}
	}

	// Only the baseline read fails: the runtime lines stay honest
	// positives while the baseline line names its cause.
	baselineOnly := degraded("baseline-missing", "route_baselines")
	for _, want := range []string{
		"runtime activation:         disabled (no runtime state; nothing dispatched and no trigger installed)",
		"production acknowledgement: not acknowledged",
		"initial baseline:           unreadable (baseline could not be loaded)",
	} {
		if !strings.Contains(baselineOnly, want) {
			t.Fatalf("the partially degraded summary must keep its readable facts and label only the failed one %q:\n%s", want, baselineOnly)
		}
	}
}

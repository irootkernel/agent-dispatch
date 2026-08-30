package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
)

// The E11-T4 guided setup (CLI-012): `setup wiki` walks a new operator
// from nothing to the edge of the production gate — disabled
// configuration, dependency probes, the destination preflight, the
// Watchman test, and the initial reconciliation — and stops there,
// printing the exact production-gate action. It never accepts
// production approval implicitly: enablement stays the operator's
// explicit two-key command.
//
// E14-T1 (CLI-016): the walkthrough drives exactly one explicitly
// selected route — through --route, a single-route configuration's only
// choice, or an interactive selection — and never a silent sorted-first
// fallback.

// runSetup implements the `setup` group; only `wiki` exists in v0.1.5.
func runSetup(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "wiki" {
		return usageError(stderr, "setup", "setup requires the workflow name: setup wiki")
	}
	command := "setup wiki"
	// The walkthrough is a TTY flow; a non-interactive invocation runs
	// the same steps against explicit flags so automation can drive it
	// (the prompts read from stdin either way).
	ui := newSetupUI(stdout, stderr)
	return runSetupWiki(command, args[1:], ui)
}

// setupUI is the bounded interaction surface: prompts on stderr,
// results on stdout, input from the reader. The vault-root prompt
// degrades to its documented default on a nil reader (EOF) or an empty
// answer; the route selection is the deliberate exception (E14-T1) —
// it has no default, and an empty or unreadable answer refuses the
// walkthrough instead of choosing a route silently.
type setupUI struct {
	stdout, stderr io.Writer
	reader         *bufio.Reader
}

func newSetupUI(stdout, stderr io.Writer) *setupUI {
	return &setupUI{stdout: stdout, stderr: stderr, reader: bufio.NewReader(os.Stdin)}
}

func (u *setupUI) prompt(label, def string) string {
	fmt.Fprintf(u.stderr, "%s [%s]: ", label, def)
	line, err := u.reader.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(u.stderr, def)
		return def
	}
	answer := strings.TrimSpace(line)
	if answer == "" {
		return def
	}
	return answer
}

func (u *setupUI) step(format string, args ...any) {
	fmt.Fprintf(u.stderr, "\n== "+format+"\n", args...)
}

// runSetupWiki drives the walkthrough: write disabled config, probe
// Hermes, preflight the destination, test the Watchman binding, run
// the initial reconciliation, print the gate action.
func runSetupWiki(command string, args []string, ui *setupUI) int {
	home, _ := os.UserHomeDir()
	defaultRoot := filepath.Join(home, "Documents", "Obsidian", "MainVault")
	// An explicit --config path names the walkthrough's target file;
	// the platform default is used otherwise. An explicit --route names
	// the one route the walkthrough drives (E14-T1, CLI-016).
	explicitConfig := ""
	explicitRoute := ""
	for i, a := range args {
		if a == "--config" && i+1 < len(args) {
			explicitConfig = args[i+1]
		}
		if strings.HasPrefix(a, "--config=") {
			explicitConfig = strings.TrimPrefix(a, "--config=")
		}
		if a == "--route" && i+1 < len(args) {
			explicitRoute = args[i+1]
		}
		if strings.HasPrefix(a, "--route=") {
			explicitRoute = strings.TrimPrefix(a, "--route=")
		}
	}

	basePath := platformpaths.DefaultConfigPath()
	if explicitConfig != "" {
		basePath = explicitConfig
	}

	ui.step("Step 1/6 — vault root and configuration")
	resourceRoot := defaultRoot
	_, statErr := os.Stat(basePath)
	baseExists := statErr == nil
	if !baseExists {
		resourceRoot = ui.prompt("Vault root", defaultRoot)
		if !filepath.IsAbs(resourceRoot) {
			abs, aerr := filepath.Abs(resourceRoot)
			if aerr != nil {
				writeError(ui.stderr, command, "config_invalid", "configuration", aerr.Error())
				return 3
			}
			resourceRoot = abs
		}
	}
	if !filepath.IsAbs(resourceRoot) {
		abs, err := filepath.Abs(resourceRoot)
		if err != nil {
			writeError(ui.stderr, command, "config_invalid", "configuration", err.Error())
			return 3
		}
		resourceRoot = abs
	}
	// An explicitly named configuration is the walkthrough's base: a
	// disabled base is used in place; an enabled base (or the platform
	// default, generated fresh) becomes a disabled draft beside it so
	// setup never weakens an existing gate.
	cfg := config.Example("workstation-main", resourceRoot)
	configPath := basePath
	if _, statErr := os.Stat(basePath); statErr == nil {
		loaded, lerr := config.Load(basePath)
		if lerr != nil {
			writeError(ui.stderr, command, "config_invalid", "configuration", lerr.Error())
			return 3
		}
		if !anyRouteEnabled(loaded) {
			cfg = loaded
		} else {
			ui.step("the named configuration has an enabled route; drafting a disabled copy")
			for id, route := range loaded.Routes {
				route.Enabled = false
				loaded.Routes[id] = route
			}
			cfg = loaded
			configPath = filepath.Join(filepath.Dir(basePath), setupDraftName)
			// The draft name is stable so the walkthrough is re-runnable,
			// and the generated-by marker is the provenance proof: a
			// previous run's own draft is replaced, while any other file
			// carrying this name is refused — setup never deletes a file
			// it cannot prove it created.
			if existing, rerr := os.ReadFile(configPath); rerr == nil && !strings.HasPrefix(string(existing), setupDraftMarker+"\n") {
				writeError(ui.stderr, command, "config_invalid", "configuration",
					configPath+" exists but is not a setup-generated draft (it lacks the generated-by marker); move or rename it, then re-run setup wiki")
				return 3
			}
			if werr := writeSetupDraft(cfg, configPath); werr != nil {
				writeError(ui.stderr, command, "config_invalid", "configuration", werr.Error())
				return 3
			}
			fmt.Fprintf(ui.stdout, "wrote disabled draft: %s (the enabled base %s is untouched)\n", configPath, basePath)
		}
	} else if werr := config.WriteExample(cfg, configPath); werr != nil {
		writeError(ui.stderr, command, "config_invalid", "configuration", werr.Error())
		return 3
	} else {
		fmt.Fprintf(ui.stdout, "wrote disabled configuration: %s\n", configPath)
	}

	// The operator's global options ride every nested step: Run resets
	// the parsed globals per invocation (the nested steps would inherit
	// nothing), so the exact spellings scanned from this invocation are
	// re-passed to every nested Run. --config is threaded explicitly per
	// step and is not forwarded.
	var forwarded []string
	if globalRawStateDir != "" {
		forwarded = append(forwarded, "--state-dir", globalRawStateDir)
	}
	if globalRawLogLevel != "" {
		forwarded = append(forwarded, "--log-level", globalRawLogLevel)
	}
	if globalRawTraceID != "" {
		forwarded = append(forwarded, "--trace-id", globalRawTraceID)
	}
	if globalRawTimeout != "" {
		forwarded = append(forwarded, "--timeout", globalRawTimeout)
	}
	step := func(argv []string) int {
		return Run(append(argv, forwarded...), ui.stdout, ui.stderr)
	}

	ui.step("Step 2/6 — configuration validation")
	if code := step([]string{"config", "validate", "--config", configPath}); code != 0 {
		writeError(ui.stderr, command, "config_invalid", "configuration", "validation failed; resolve the reported findings and re-run setup wiki")
		return 3
	}

	// The selected-route contract (E14-T1): one route identity is
	// resolved before any route-scoped step runs, and the preflight,
	// Watchman status and guidance, baseline reconciliation, and the
	// final enable command all name exactly that route.
	routeID, routeExit := selectSetupRoute(command, cfg, explicitRoute, ui)
	if routeExit != 0 {
		return routeExit
	}

	ui.step("Step 3/6 — Hermes dependency probes")
	probeCode := step([]string{"hermes", "probe", "--config", configPath})
	if probeCode != 0 {
		writeError(ui.stderr, command, "config_capability_missing", "configuration",
			"the Hermes probes failed; install a Hermes at or above 0.19.1, create the board, and re-run setup wiki")
		return 3
	}
	preflightCode := step([]string{"route", "preflight", "--config", configPath, "--route", routeID})
	if preflightCode != 0 {
		writeError(ui.stderr, command, "config_capability_missing", "configuration",
			"the destination preflight failed; create the profile and enable the skills in Hermes, or adjust the destination with route set-profile/set-skills, then re-run setup wiki")
		return 3
	}

	ui.step("Step 4/6 — Watchman binding check")
	step([]string{"watchman", "status", "--config", configPath, "--route", routeID})
	fmt.Fprintln(ui.stderr, "the Watchman test (watchman test) drives the real trigger surface; install it explicitly after setup and run the test:")
	fmt.Fprintln(ui.stderr, "  agent-dispatch watchman install --route "+routeID+" --config "+configPath)
	fmt.Fprintln(ui.stderr, "  agent-dispatch watchman test --route "+routeID+" --config "+configPath)

	ui.step("Step 5/6 — initial baseline (disabled, no production work)")
	// The baseline is the public disabled-route operation of ADR-0020
	// (E14-T3): it enumerates and records the initial path facts while
	// the route stays disabled — on a clean host or a materialized
	// disabled row alike — and a rerun converges, so an interrupted
	// walkthrough is safely repeatable (AC-1003/AC-1004). It never
	// submits and never creates production work; a genuine refusal (an
	// enabled route, an unresolved hold) is a real finding and stops the
	// walkthrough before the gate summary.
	baselineCode := step([]string{"reconcile", "--route", routeID, "--reason", "initial", "--baseline-only", "--config", configPath})
	if baselineCode != 0 {
		fmt.Fprintf(ui.stderr, "the initial baseline did not complete; resolve the reported findings and re-run setup wiki, or the baseline alone with:\n  agent-dispatch reconcile --route %s --reason initial --baseline-only --config %s\n", routeID, configPath)
		writeError(ui.stderr, command, "config_invalid", "configuration", "the initial baseline failed; setup stops before the gate summary")
		return 3
	}

	ui.step("Step 6/6 — the production gate (NOT performed by setup)")
	setupGateSummary(ui, cfg, configPath, routeID)
	return 0
}

// setupGateSummary prints the honest five-state production-gate summary
// (OPS-016, AC-1005): configuration enabled state, runtime activation,
// Watchman binding, initial baseline, and production acknowledgement as
// distinct states, followed by the exact reviewed enable command —
// printed, never executed. Every state is read from current durable
// facts (the configuration, the route runtime row, the stored Watchman
// binding, and the baseline record the walkthrough just committed); an
// unreadable fact degrades to an explicit "unreadable" instead of an
// invented state.
func setupGateSummary(ui *setupUI, cfg *config.Config, configPath, routeID string) {
	revision, ok := config.RouteRevision(cfg, routeID)
	if !ok {
		revision = "<run 'agent-dispatch route show --route " + routeID + "' for the computed revision>"
	}
	configState := "disabled"
	if route, ok := cfg.Routes[routeID]; ok && route.Enabled {
		configState = "enabled"
	}
	runtimeState := "unreadable (state store could not be opened)"
	baselineState := "unreadable (state store could not be opened)"
	ackState := "unreadable (state store could not be opened)"
	if store, serr := openUnmigratedStore(configPath); serr == nil {
		runtimeState = "disabled (no runtime state; nothing dispatched and no trigger installed)"
		ackState = "not acknowledged"
		if present, Perr := store.RouteRuntimeStatePresent(requestCtx(), routeID); Perr == nil && present {
			if snap, lerr := store.LoadRouteState(requestCtx(), routeID); lerr == nil {
				runtimeState = string(snap.ActivationState)
				if snap.AcknowledgedRevision != "" {
					ackState = "acknowledged for revision " + snap.AcknowledgedRevision
				}
			} else {
				runtimeState = "unreadable (runtime state could not be loaded)"
			}
		} else if Perr != nil {
			runtimeState = "unreadable (runtime state could not be loaded)"
		}
		if baseline, berr := store.LoadRouteBaseline(requestCtx(), routeID); berr == nil {
			if baseline != nil {
				baselineState = fmt.Sprintf("established (%d facts, observation revision %d, at %s)",
					baseline.FactCount, baseline.ObservationRevision, baseline.EstablishedAt)
			} else {
				baselineState = "not established"
			}
		}
		store.Close()
	}
	bindingState := "not installed"
	if stored, hasStored, err := storedBindingFor(configPath, routeID); err == nil && hasStored {
		bindingState = "installed (trigger " + stored.TriggerName + ")"
	} else if err != nil {
		bindingState = "unreadable (stored binding could not be loaded)"
	}
	fmt.Fprintln(ui.stdout, "setup complete; the route is DISABLED and nothing was submitted")
	fmt.Fprintln(ui.stdout, "production-gate summary:")
	fmt.Fprintf(ui.stdout, "  configuration enabled:      %s\n", configState)
	fmt.Fprintf(ui.stdout, "  runtime activation:         %s\n", runtimeState)
	fmt.Fprintf(ui.stdout, "  Watchman binding:           %s\n", bindingState)
	fmt.Fprintf(ui.stdout, "  initial baseline:           %s\n", baselineState)
	fmt.Fprintf(ui.stdout, "  production acknowledgement: %s\n", ackState)
	fmt.Fprintln(ui.stdout, "review the configuration, then enable explicitly with:")
	fmt.Fprintf(ui.stdout, "  agent-dispatch route enable --route %s --config %s --acknowledge-production-gate %s --yes\n", routeID, configPath, revision)
	fmt.Fprintln(ui.stdout, "setup never enables the route or accepts production approval implicitly")
}

// anyRouteEnabled reports whether any route carries the YAML key half
// of the two-key gate.
func anyRouteEnabled(cfg *config.Config) bool {
	for _, route := range cfg.Routes {
		if route.Enabled {
			return true
		}
	}
	return false
}

// selectSetupRoute resolves the one route the walkthrough drives
// (E14-T1, CLI-016): an explicit --route that names a declared route,
// the only route of a single-route configuration, or an explicit
// interactive choice. A multi-route configuration without a flag or a
// usable answer is refused — the walkthrough never falls back to the
// first route in sorted order.
func selectSetupRoute(command string, cfg *config.Config, explicit string, ui *setupUI) (string, int) {
	ids := cfg.SortedRouteIDs()
	if explicit != "" {
		if _, ok := cfg.Routes[explicit]; !ok {
			writeError(ui.stderr, command, "flag_invalid", "usage",
				"the configuration does not declare route "+explicit+"; it declares: "+strings.Join(ids, ", "))
			return "", 2
		}
		ui.step("the walkthrough drives the explicitly selected route %q", explicit)
		return explicit, 0
	}
	switch len(ids) {
	case 0:
		writeError(ui.stderr, command, "flag_invalid", "usage",
			"the configuration declares no routes; nothing to walk through — add a route before re-running setup wiki")
		return "", 2
	case 1:
		ui.step("the configuration declares one route; the walkthrough uses %q", ids[0])
		return ids[0], 0
	}
	return ui.promptRoute(command, ids)
}

// promptRoute renders the numbered route choice and reads one explicit
// selection (AC-1002). There is deliberately no default: an empty or
// unreadable answer fails the walkthrough rather than choosing the first
// route in sorted order, so non-interactive execution must pass --route.
func (u *setupUI) promptRoute(command string, ids []string) (string, int) {
	u.step("the configuration declares %d routes; select one to walk through", len(ids))
	for i, id := range ids {
		fmt.Fprintf(u.stderr, "  %d) %s\n", i+1, id)
	}
	fmt.Fprintf(u.stderr, "Route [1-%d or the route ID]: ", len(ids))
	line, err := u.reader.ReadString('\n')
	if (err != nil && line == "") || strings.TrimSpace(line) == "" {
		return u.routeRefused(command, ids)
	}
	answer := strings.TrimSpace(line)
	for i, id := range ids {
		if answer == strconv.Itoa(i+1) || answer == id {
			u.step("the walkthrough drives the selected route %q", id)
			return id, 0
		}
	}
	return u.routeRefused(command, ids)
}

func (u *setupUI) routeRefused(command string, ids []string) (string, int) {
	writeError(u.stderr, command, "flag_invalid", "usage",
		"an explicit route selection is required when the configuration declares multiple routes; re-run with --route <id> (declared: "+strings.Join(ids, ", ")+")")
	return "", 2
}

// setupDraftName is the stable draft file the walkthrough writes beside
// an enabled base, and setupDraftMarker is its provenance proof: the
// first line of every draft this flow creates.
const (
	setupDraftName   = "config-setup-draft.yaml"
	setupDraftMarker = "# generated-by: agent-dispatch setup wiki; safe to replace on re-run"
)

// writeSetupDraft writes the disabled draft carrying the generated-by
// marker through a private temporary file and an atomic rename, so a
// failed write never leaves a partial draft behind.
func writeSetupDraft(cfg *config.Config, configPath string) error {
	data, err := config.MarshalYAML(cfg)
	if err != nil {
		return err
	}
	out := append([]byte(setupDraftMarker+"\n"), data...)
	dir := filepath.Dir(configPath)
	tmp, err := os.CreateTemp(dir, ".agent-dispatch-setup-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, configPath)
}

// Run0 is a test seam returning only the exit code of one in-process
// CLI invocation.
func Run0(argv []string) (int, error) {
	return Run(argv, io.Discard, io.Discard), nil
}

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
	"github.com/irootkernel/agent-dispatch/internal/config"
)

// runWatchman implements the managed Watchman trigger lifecycle (cli-spec
// §4, SRC-005..007, OPS-006): install is idempotent for the identical
// definition, replacement requires --replace, remove touches only the
// exact managed trigger, and test replays a fixture through the
// side-effect-free pipeline.
func runWatchman(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "watchman", "watchman requires a subcommand: install, status, remove, test")
	}
	switch args[0] {
	case "install":
		return runWatchmanInstall(args[1:], stdout, stderr)
	case "status":
		return runWatchmanStatus(args[1:], stdout, stderr)
	case "remove":
		return runWatchmanRemove(args[1:], stdout, stderr)
	case "test":
		return runWatchmanTest(args[1:], stdout, stderr)
	default:
		return usageError(stderr, "watchman", fmt.Sprintf("unknown watchman subcommand %q", args[0]))
	}
}

type watchmanOptions struct {
	routeID    string
	configPath string
	replace    bool
	yes        bool
	fixture    string
}

func parseWatchmanFlags(command string, args []string, stderr io.Writer) (*watchmanOptions, int) {
	opts := &watchmanOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--route":
			if i+1 >= len(args) {
				return nil, usageError(stderr, command, "--route requires an id")
			}
			i++
			opts.routeID = args[i]
		case "--config":
			if i+1 >= len(args) {
				return nil, usageError(stderr, command, "--config requires a path")
			}
			i++
			opts.configPath = args[i]
		case "--replace":
			opts.replace = true
		case "--yes":
			opts.yes = true
		case "--fixture":
			if i+1 >= len(args) {
				return nil, usageError(stderr, command, "--fixture requires a path")
			}
			i++
			opts.fixture = args[i]
		default:
			return nil, usageError(stderr, command, fmt.Sprintf("unknown argument %q", args[i]))
		}
	}
	if opts.routeID == "" {
		return nil, usageError(stderr, command, "--route is required")
	}
	return opts, 0
}

// loadRoute resolves the config path, route, and resource for a
// lifecycle command.
func loadRoute(configPath, routeID, command string, stderr io.Writer) (*config.Config, config.Route, config.Resource, int) {
	configPath = resolveConfigPath(configPath)
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, config.Route{}, config.Resource{}, planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	route, ok := cfg.Routes[routeID]
	if !ok {
		return nil, config.Route{}, config.Resource{}, planErr(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q is not defined", routeID), 3)
	}
	resource, ok := cfg.Resources[route.Source.Resource]
	if !ok {
		return nil, config.Route{}, config.Resource{}, planErr(stderr, command, "config_invalid", "configuration", fmt.Sprintf("resource %q is not defined", route.Source.Resource), 3)
	}
	return cfg, route, resource, 0
}

// newLifecycleClient builds the client and performs the availability and
// version checks with actionable output.
func newLifecycleClient(command string, stderr io.Writer) (*watchman.Client, context.Context, string, int) {
	client := watchman.NewClient("")
	ctx := context.Background()
	version, err := client.Version(ctx)
	if err != nil {
		var unavailable *watchman.UnavailableError
		if errors.As(err, &unavailable) {
			return nil, nil, "", planErr(stderr, command, "watchman_unavailable", "target_unavailable", unavailable.Error()+"; "+unavailable.Remediation(), 11)
		}
		var protocol *watchman.LifecycleError
		if errors.As(err, &protocol) {
			return nil, nil, "", planErr(stderr, command, "target_response_invalid", "acceptance_unknown", protocol.Error(), 13)
		}
		return nil, nil, "", planErr(stderr, command, "watchman_unavailable", "target_unavailable", err.Error(), 11)
	}
	if err := watchman.CheckVersionSupported(version); err != nil {
		return nil, nil, "", planErr(stderr, command, "watchman_version_unsupported", "target_unavailable", err.Error(), 11)
	}
	return client, ctx, version, 0
}

// lifecycleErr maps a client failure to the registered code: absence or
// an unusable server is target_unavailable (11); a server-reported
// protocol error is target_response_invalid (13).
func lifecycleErr(stderr io.Writer, command string, err error) int {
	var unavailable *watchman.UnavailableError
	if errors.As(err, &unavailable) {
		return planErr(stderr, command, "watchman_unavailable", "target_unavailable", unavailable.Error()+"; "+unavailable.Remediation(), 11)
	}
	var protocol *watchman.LifecycleError
	if errors.As(err, &protocol) {
		return planErr(stderr, command, "target_response_invalid", "acceptance_unknown", protocol.Error(), 13)
	}
	return planErr(stderr, command, "watchman_unavailable", "target_unavailable", err.Error(), 11)
}

// managedCommand builds the trigger command that invokes this binary's
// one-shot dispatch path for the route. The durable dispatch
// implementation arrives with E3; the definition this task installs is
// the final managed form.
func managedCommand(routeID, configPath string) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// The trigger pins --config (and the documented output form) so a
	// configuration installed outside the default location survives fire
	// time: the un-pinned form loaded the default config whenever the
	// service environment differed from the installing shell's (E8-T5,
	// M-9; watchman-integration section 2).
	argv := []string{exe, "dispatch", "--route", routeID, "--config", configPath, "--input", "watchman", "--output", "json"}
	return argv, nil
}

func runWatchmanInstall(args []string, stdout, stderr io.Writer) int {
	command := "watchman install"
	opts, code := parseWatchmanFlags(command, args, stderr)
	if code != 0 {
		return code
	}
	_, route, resource, code := loadRoute(opts.configPath, opts.routeID, command, stderr)
	if code != 0 {
		return code
	}
	client, ctx, _, code := newLifecycleClient(command, stderr)
	if code != 0 {
		return code
	}
	watchRoot, err := client.EnsureWatch(ctx, resource.Root)
	if err != nil {
		return lifecycleErr(stderr, command, err)
	}
	cmdArgv, err := managedCommand(opts.routeID, resolveConfigPath(opts.configPath))
	if err != nil {
		return planErr(stderr, command, "internal_unclassified", "internal", err.Error(), 40)
	}
	expected := watchman.ManagedTrigger(route.Source.TriggerName, cmdArgv)

	// The install is the operator's first-use entry point: it also
	// materializes the route's durable registration (resource, route
	// revision, runtime state) from the configuration, idempotently,
	// so the first dispatched change never meets an unregistered route
	// (E4 audit remediation for the E2-T5/E3 seam).
	if err := registerRouteFromConfig(command, opts.configPath, opts.routeID, stderr); err != 0 {
		return err
	}
	installed, err := client.TriggerList(ctx, watchRoot)
	if err != nil {
		return lifecycleErr(stderr, command, err)
	}
	if current, exists := watchman.FindTrigger(installed, expected.Name); exists {
		if current.Equal(expected) {
			// Identical reinstall is a true no-op that preserves the
			// incremental position (E0-T5 lifecycle evidence).
			return writeEnvelope(stdout, command, map[string]any{
				"route":                          opts.routeID,
				"watch_root":                     watchRoot,
				"trigger":                        expected.Name,
				"action":                         "noop",
				"disposition":                    "already_defined",
				"initial_reconciliation_pending": route.Reconciliation.Initial,
			})
		}
		if !opts.replace {
			return planErr(stderr, command, "watchman_trigger_conflict", "conflict",
				fmt.Sprintf("trigger %q exists with a different definition; pass --replace to replace it (replacement resets the incremental position)", expected.Name), 14)
		}
	}
	disposition, err := client.TriggerInstall(ctx, watchRoot, expected)
	if err != nil {
		return lifecycleErr(stderr, command, err)
	}
	// The conflict gate above is advisory against same-user concurrency:
	// if the server replaced a definition we believed equal or absent,
	// surface it instead of silently claiming a clean install.
	if disposition == "replaced" && !opts.replace {
		return writeEnvelopeWithWarnings(stdout, command, map[string]any{
			"route":                          opts.routeID,
			"watch_root":                     watchRoot,
			"trigger":                        expected.Name,
			"action":                         "installed",
			"disposition":                    disposition,
			"initial_reconciliation_pending": route.Reconciliation.Initial,
		}, []string{"the trigger definition changed between the pre-check and the install; the managed definition was reinstalled and the incremental position reset"})
	}
	// A created or replaced trigger has no usable position: the first
	// invocation delivers the full matching list without WATCHMAN_SINCE,
	// which the E2-T4 planner converts to exactly one initial
	// reconciliation (SRC-005, architecture watchman-integration §7).
	return writeEnvelope(stdout, command, map[string]any{
		"route":                          opts.routeID,
		"watch_root":                     watchRoot,
		"trigger":                        expected.Name,
		"action":                         "installed",
		"disposition":                    disposition,
		"initial_reconciliation_pending": route.Reconciliation.Initial,
	})
}

// registerRouteFromConfig loads the configuration and materializes the
// route registration through the shared store.
func registerRouteFromConfig(command, configPath, routeID string, stderr io.Writer) int {
	cfg, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	_, closer, exit := openOperatorStore(command, configPath, stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	if err := registerRouteState(requestCtx(), closer, cfg, routeID); err != nil {
		writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
		return 20
	}
	return 0
}

func runWatchmanStatus(args []string, stdout, stderr io.Writer) int {
	command := "watchman status"
	opts, code := parseWatchmanFlags(command, args, stderr)
	if code != 0 {
		return code
	}
	_, route, resource, code := loadRoute(opts.configPath, opts.routeID, command, stderr)
	if code != 0 {
		return code
	}
	client, ctx, version, code := newLifecycleClient(command, stderr)
	if code != 0 {
		return code
	}
	// Status never creates a watch: an unwatched root reports missing
	// rather than establishing one.
	watched, err := client.IsWatched(ctx, resource.Root)
	if err != nil {
		return lifecycleErr(stderr, command, err)
	}
	if !watched {
		return writeEnvelope(stdout, command, map[string]any{
			"route":            opts.routeID,
			"watch_root":       resource.Root,
			"watch_root_state": "not_watched",
			"watchman_version": version,
			"trigger":          route.Source.TriggerName,
			"state":            "missing",
		})
	}
	watchRoot, err := client.EnsureWatch(ctx, resource.Root)
	if err != nil {
		return lifecycleErr(stderr, command, err)
	}
	installed, err := client.TriggerList(ctx, watchRoot)
	if err != nil {
		return lifecycleErr(stderr, command, err)
	}
	cmdArgv, err := managedCommand(opts.routeID, resolveConfigPath(opts.configPath))
	if err != nil {
		return planErr(stderr, command, "internal_unclassified", "internal", err.Error(), 40)
	}
	expected := watchman.ManagedTrigger(route.Source.TriggerName, cmdArgv)
	state := "missing"
	current, exists := watchman.FindTrigger(installed, expected.Name)
	if exists {
		if current.Equal(expected) {
			state = "installed"
		} else {
			state = "diverged"
		}
	}
	return writeEnvelope(stdout, command, map[string]any{
		"route":            opts.routeID,
		"watch_root":       watchRoot,
		"watchman_version": version,
		"trigger":          expected.Name,
		"state":            state,
		"expected":         expected,
		"installed":        current,
		"other_triggers":   otherNames(installed, expected.Name),
	})
}

// otherNames lists installed trigger names other than the managed one.
func otherNames(defs []watchman.TriggerDefinition, own string) []string {
	var out []string
	for _, n := range watchman.SortedNames(defs) {
		if n != own {
			out = append(out, n)
		}
	}
	return out
}

func runWatchmanRemove(args []string, stdout, stderr io.Writer) int {
	command := "watchman remove"
	opts, code := parseWatchmanFlags(command, args, stderr)
	if code != 0 {
		return code
	}
	if !opts.yes {
		return usageError(stderr, command, "remove requires --yes; it never removes the watch root itself")
	}
	_, route, resource, code := loadRoute(opts.configPath, opts.routeID, command, stderr)
	if code != 0 {
		return code
	}
	client, ctx, _, code := newLifecycleClient(command, stderr)
	if code != 0 {
		return code
	}
	// Remove never creates a watch either: an unwatched root is already
	// free of the managed trigger.
	watched, err := client.IsWatched(ctx, resource.Root)
	if err != nil {
		return lifecycleErr(stderr, command, err)
	}
	if !watched {
		return writeEnvelope(stdout, command, map[string]any{
			"route":      opts.routeID,
			"watch_root": resource.Root,
			"trigger":    route.Source.TriggerName,
			"action":     "noop",
		})
	}
	watchRoot, err := client.EnsureWatch(ctx, resource.Root)
	if err != nil {
		return lifecycleErr(stderr, command, err)
	}
	// Removes only the exact managed trigger; deleting an absent trigger
	// is idempotent. The watch root is never removed.
	deleted, err := client.TriggerDelete(ctx, watchRoot, route.Source.TriggerName)
	if err != nil {
		return lifecycleErr(stderr, command, err)
	}
	action := "removed"
	if !deleted {
		action = "noop"
	}
	return writeEnvelope(stdout, command, map[string]any{
		"route":      opts.routeID,
		"watch_root": watchRoot,
		"trigger":    route.Source.TriggerName,
		"action":     action,
	})
}

// runWatchmanTest parses a supplied or built-in fixture payload with a
// synthetic environment and prints the normalized source input DTO —
// entries, operation mapping, raw digest, and flags — with no Watchman
// contact, no configuration load, and no Hermes side effects (SRC-008).
func runWatchmanTest(args []string, stdout, stderr io.Writer) int {
	command := "watchman test"
	fixture := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--fixture":
			if i+1 >= len(args) {
				return usageError(stderr, command, "--fixture requires a path")
			}
			i++
			fixture = args[i]
		default:
			return usageError(stderr, command, fmt.Sprintf("unknown argument %q", args[i]))
		}
	}
	payload := []byte(`[{"name":"Inbox/new-note.md","exists":true,"new":true,"size":24,"type":"f"}]`)
	if fixture != "" {
		raw, err := os.ReadFile(fixture)
		if err != nil {
			return planErr(stderr, command, "flag_invalid", "usage", err.Error(), 2)
		}
		payload = raw
	}
	env := watchman.Env{
		Trigger: "agent-dispatch.test",
		Root:    "/synthetic-test-root",
		Clock:   "c:0:0:0:1",
		// No Since: the synthetic first-position shape, the verified
		// overflow-class signal.
	}
	input, err := watchman.ReadInput(bytes.NewReader(payload), env, watchman.DefaultMaxStdinBytes)
	if err != nil {
		return planErr(stderr, command, "source_malformed_json", "input_rejected", err.Error(), 4)
	}
	// The envelope maps the entries into snake_case wire entries:
	// marshaling the domain structs directly leaked Go field names
	// (L-10, E9-T1).
	wire := make([]map[string]any, 0, len(input.Entries))
	for _, e := range input.Entries {
		entry := map[string]any{
			"name": e.Name, "exists": e.Exists, "type": string(e.Type), "op": string(e.Op),
		}
		if e.Size != nil {
			entry["size"] = *e.Size
		}
		wire = append(wire, entry)
	}
	return writeEnvelope(stdout, command, map[string]any{
		"raw_payload_digest": input.RawDigest,
		"source_event_key":   input.SourceEventKey("test-source"),
		"flags":              env.Flags(),
		"changes":            wire,
	})
}

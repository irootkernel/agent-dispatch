package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/ids"
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

// effectiveBinding is one resolved managed Watchman binding (E10-T2,
// SRC-009/SRC-010): the identical four values install, status, test, and
// remove resolve and report. D-028: the actual root is the configured
// resource root itself and relative_root is the schema-vestigial ".".
type effectiveBinding struct {
	ConfiguredRoot string `json:"configured_root"`
	ActualRoot     string `json:"actual_root"`
	RelativeRoot   string `json:"relative_root"`
	TriggerName    string `json:"trigger_name"`
}

// resolveServerBinding resolves the effective binding against the live
// Watchman server: the configured absolute root is established as its
// own watch root (D-028, SRC-011) — EnsureWatch fails closed with
// unwatch guidance when the server cannot watch exactly that root, so
// no ancestor binding can be resolved here. This is the one resolver
// every server-contacting lifecycle command shares.
func resolveServerBinding(ctx context.Context, client *watchman.Client, resource config.Resource, triggerName string) (string, effectiveBinding, error) {
	actual, err := client.EnsureWatch(ctx, resource.Root)
	if err != nil {
		return "", effectiveBinding{}, err
	}
	return actual, effectiveBinding{
		ConfiguredRoot: resource.Root,
		ActualRoot:     actual,
		RelativeRoot:   ".",
		TriggerName:    triggerName,
	}, nil
}

// persistBinding stores the resolved binding on the route (SRC-009):
// installation is the durable record the other lifecycle commands
// read — dispatch validates against the configuration alone (D-028,
// SRC-013) and never consults it.
func persistBinding(configPath, routeID, resourceID string, binding effectiveBinding) error {
	store, err := openStateStore(resolveConfigPath(configPath))
	if err != nil {
		return err
	}
	defer store.Close()
	return store.SaveWatchBinding(context.Background(), watchman.Binding{
		RouteID: routeID, ResourceID: resourceID,
		ConfiguredRoot: binding.ConfiguredRoot, ActualRoot: binding.ActualRoot,
		RelativeRoot: binding.RelativeRoot, TriggerName: binding.TriggerName,
		UpdatedAt: ids.CanonicalTimestamp(time.Now()),
	})
}

// storedBindingFor loads the persisted binding for the lifecycle
// surfaces that read it — status drift, remove's proof set, and test's
// logical root (round-1 review: one loader, one absent contract).
// Absent is reported as hasStored=false, never an error; dispatch never
// reads it (D-028, SRC-013).
func storedBindingFor(configPath, routeID string) (watchman.Binding, bool, error) {
	binding, err := loadStoredBinding(resolveConfigPath(configPath), routeID)
	if err != nil {
		return watchman.Binding{}, false, err
	}
	if binding == nil {
		return watchman.Binding{}, false, nil
	}
	return *binding, true, nil
}

// loadStoredBinding is the single storage reader for the managed
// binding; a nil result means no binding is persisted.
func loadStoredBinding(configPath, routeID string) (*watchman.Binding, error) {
	store, err := openStateStore(configPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	binding, err := store.LoadWatchBinding(context.Background(), routeID)
	if err != nil {
		if errors.Is(err, sqlite.ErrWatchBindingNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &binding, nil
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
	// The effective binding resolves against the live server before
	// anything is installed (E10-T2, SRC-009/SRC-011): the configured
	// absolute root must be its own watch root — a server that cannot
	// watch it exactly fails closed with unwatch guidance (D-028).
	watchRoot, binding, err := resolveServerBinding(ctx, client, resource, route.Source.TriggerName)
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
	// Topology self-heal (round-1 review): a reinstall after the watch
	// moved (the stored actual root differs from the resolved one)
	// removes the stale managed trigger from the previous actual root —
	// deleting an absent trigger is idempotent — so the old root cannot
	// keep firing the managed command.
	if stored, hasStored, err := storedBindingFor(opts.configPath, opts.routeID); err != nil {
		writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
		return 20
	} else if hasStored && watchman.CanonicalRoot(stored.ActualRoot) != watchman.CanonicalRoot(watchRoot) {
		if _, err := client.TriggerDelete(ctx, stored.ActualRoot, route.Source.TriggerName); err != nil {
			var protocol *watchman.LifecycleError
			if !errors.As(err, &protocol) {
				return lifecycleErr(stderr, command, err)
			}
			// An unwatchable previous root is structurally free of the
			// trigger; the server refusal is reported, not hidden.
			fmt.Fprintf(stderr, "note: the previous actual root %q is no longer watchable; its managed trigger is gone with the watch\n", stored.ActualRoot)
		}
	}
	noop := false
	if current, exists := watchman.FindTrigger(installed, expected.Name); exists {
		if current.Equal(expected) {
			// Identical reinstall is a true no-op that preserves the
			// incremental position (E0-T5 lifecycle evidence).
			noop = true
		} else if !opts.replace {
			return planErr(stderr, command, "watchman_trigger_conflict", "conflict",
				fmt.Sprintf("trigger %q exists with a different definition; pass --replace to replace it (replacement resets the incremental position)", expected.Name), 14)
		}
	}
	disposition := "already_defined"
	if !noop {
		var installErr error
		disposition, installErr = client.TriggerInstall(ctx, watchRoot, expected)
		if installErr != nil {
			return lifecycleErr(stderr, command, installErr)
		}
	}
	// The resolved binding is durable from a successful install (and an
	// identical no-op): every other lifecycle command reads this record
	// (SRC-009); dispatch validates against the configuration alone
	// (D-028, SRC-013).
	if err := persistBinding(opts.configPath, opts.routeID, route.Source.Resource, binding); err != nil {
		writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
		return 20
	}
	action := "installed"
	if noop {
		action = "noop"
	} else if disposition == "replaced" && !opts.replace {
		// The conflict gate above is advisory against same-user
		// concurrency: if the server replaced a definition we believed
		// equal or absent, surface it instead of silently claiming a
		// clean install.
		return writeEnvelopeWithWarnings(stdout, command, map[string]any{
			"route": opts.routeID, "watch_root": watchRoot, "binding": binding,
			"trigger": expected.Name, "action": action, "disposition": disposition,
			"initial_reconciliation_pending": route.Reconciliation.Initial,
		}, []string{"the trigger definition changed between the pre-check and the install; the managed definition was reinstalled and the incremental position reset"})
	}
	// A created or replaced trigger has no usable position: the first
	// invocation delivers the full matching list without WATCHMAN_SINCE,
	// which the E2-T4 planner converts to exactly one initial
	// reconciliation (SRC-005, architecture watchman-integration §7).
	return writeEnvelope(stdout, command, map[string]any{
		"route": opts.routeID, "watch_root": watchRoot, "binding": binding,
		"trigger": expected.Name, "action": action, "disposition": disposition,
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
	stored, hasStored, err := storedBindingFor(opts.configPath, opts.routeID)
	if err != nil {
		writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
		return 20
	}
	watched, err := client.IsWatched(ctx, resource.Root)
	if err != nil {
		return lifecycleErr(stderr, command, err)
	}
	if !watched {
		state := "missing"
		var binding any
		if hasStored {
			// The persisted record is reported exactly when it is stale
			// (round-1 review): nothing watches the configured root, so
			// the stored topology cannot be current.
			state = "drifted"
			binding = effectiveBinding{
				ConfiguredRoot: stored.ConfiguredRoot, ActualRoot: stored.ActualRoot,
				RelativeRoot: stored.RelativeRoot, TriggerName: stored.TriggerName,
			}
		}
		return writeEnvelope(stdout, command, map[string]any{
			"route":            opts.routeID,
			"watch_root":       resource.Root,
			"watch_root_state": "not_watched",
			"binding":          binding,
			"watchman_version": version,
			"trigger":          route.Source.TriggerName,
			"include":          route.Source.Include,
			"exclude":          route.Source.Exclude,
			"state":            state,
		})
	}
	// The same server resolver install uses resolves the effective
	// binding (SRC-010): configured root, actual watch root (the
	// configured root itself, D-028), relative root, and trigger
	// identity.
	watchRoot, binding, err := resolveServerBinding(ctx, client, resource, route.Source.TriggerName)
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
	// Drift (OPS-010): the persisted binding no longer matches the live
	// watch topology — the actual root moved or the configured root or
	// trigger identity changed — independent of the trigger definition
	// comparison. The relative root is not an axis: it is the
	// schema-vestigial "." on both sides (D-028).
	drifted := hasStored && (watchman.CanonicalRoot(stored.ActualRoot) != watchman.CanonicalRoot(binding.ActualRoot) ||
		stored.TriggerName != binding.TriggerName ||
		stored.ConfiguredRoot != binding.ConfiguredRoot)
	state := "missing"
	current, exists := watchman.FindTrigger(installed, expected.Name)
	if exists {
		if current.Equal(expected) {
			state = "installed"
		} else {
			state = "diverged"
		}
	}
	if drifted {
		// Binding drift outranks the trigger comparison (round-1
		// review): even with the trigger absent, a stale persisted
		// binding is the operator-visible defect — ancestor dispatches
		// stay refused until install re-persists the record.
		state = "drifted"
	}
	return writeEnvelope(stdout, command, map[string]any{
		"route":            opts.routeID,
		"watch_root":       watchRoot,
		"binding":          binding,
		"watchman_version": version,
		"trigger":          expected.Name,
		"include":          route.Source.Include,
		"exclude":          route.Source.Exclude,
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
	// Remove never creates a watch: an unwatched configured root is
	// already free of the managed trigger there — but the proof still
	// searches every applicable root (SRC-012): the persisted binding's
	// actual root and every root the server currently watches, because a
	// re-watched ancestor may still carry the managed trigger.
	stored, hasStored, err := storedBindingFor(opts.configPath, opts.routeID)
	if err != nil {
		writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
		return 20
	}
	// A trigger can only exist on a root the server actually watches:
	// the proof set is the current watch-list, plus the stored actual
	// root while it stays watched (a trigger-list on an unwatched path
	// is a server error, and absence there is structural).
	watchRoots, err := client.WatchList(ctx)
	if err != nil {
		return lifecycleErr(stderr, command, err)
	}
	candidateRoots := append([]string{}, watchRoots...)
	if hasStored {
		watched, err := client.IsWatched(ctx, stored.ActualRoot)
		if err != nil {
			return lifecycleErr(stderr, command, err)
		}
		if watched && !containsRoot(watchRoots, stored.ActualRoot) {
			candidateRoots = append(candidateRoots, stored.ActualRoot)
		}
	}
	// Deduplicated in canonical order so the proof is deterministic.
	seen := map[string]bool{}
	var roots []string
	for _, root := range candidateRoots {
		if root == "" {
			continue
		}
		key := watchman.CanonicalRoot(root)
		if seen[key] {
			continue
		}
		seen[key] = true
		roots = append(roots, key)
	}
	sort.Strings(roots)
	primaryRoot := watchman.CanonicalRoot(resource.Root)
	if hasStored {
		primaryRoot = watchman.CanonicalRoot(stored.ActualRoot)
	}

	// Removes only the exact managed trigger; deleting an absent trigger
	// is idempotent. The watch roots themselves are never removed.
	action := "noop"
	removedFrom := map[string]bool{}
	for _, root := range roots {
		defs, err := client.TriggerList(ctx, root)
		if err != nil {
			return lifecycleErr(stderr, command, err)
		}
		if _, exists := watchman.FindTrigger(defs, route.Source.TriggerName); !exists {
			continue
		}
		deleted, err := client.TriggerDelete(ctx, root, route.Source.TriggerName)
		if err != nil {
			return lifecycleErr(stderr, command, err)
		}
		if deleted {
			action = "removed"
			removedFrom[root] = true
		}
	}
	// The removal proof (SRC-012): the command succeeds only after the
	// managed trigger is absent on every applicable root; each root's
	// post-delete listing is re-read rather than assumed.
	proof := make([]map[string]any, 0, len(roots))
	for _, root := range roots {
		defs, err := client.TriggerList(ctx, root)
		if err != nil {
			return lifecycleErr(stderr, command, err)
		}
		if _, present := watchman.FindTrigger(defs, route.Source.TriggerName); present {
			return planErr(stderr, command, "watchman_trigger_conflict", "conflict",
				fmt.Sprintf("the managed trigger %q is still present on watch root %q after removal", route.Source.TriggerName, root), 14)
		}
		proof = append(proof, map[string]any{"watch_root": root, "removed_from": removedFrom[root], "present": false})
	}
	binding := effectiveBinding{
		ConfiguredRoot: resource.Root, ActualRoot: primaryRoot,
		RelativeRoot: ".", TriggerName: route.Source.TriggerName,
	}
	if hasStored {
		binding = effectiveBinding{
			ConfiguredRoot: stored.ConfiguredRoot, ActualRoot: stored.ActualRoot,
			RelativeRoot: stored.RelativeRoot, TriggerName: stored.TriggerName,
		}
	}
	return writeEnvelope(stdout, command, map[string]any{
		"route":      opts.routeID,
		"watch_root": primaryRoot,
		"binding":    binding,
		"trigger":    route.Source.TriggerName,
		"action":     action,
		"proof":      proof,
	})
}

// containsRoot reports exact membership after canonicalization.
func containsRoot(roots []string, root string) bool {
	want := watchman.CanonicalRoot(root)
	for _, r := range roots {
		if watchman.CanonicalRoot(r) == want {
			return true
		}
	}
	return false
}

// runWatchmanTest parses a supplied or built-in fixture payload with a
// synthetic environment and prints the normalized source input DTO —
// entries, operation mapping, raw digest, and flags — with no Watchman
// contact, no Hermes side effects, and no mutation (SRC-008). With
// --route it also resolves the same effective binding the other
// lifecycle commands report, from its logical root — the persisted
// binding when one exists, the configured root otherwise (E10-T2,
// SRC-010: test never contacts the server).
func runWatchmanTest(args []string, stdout, stderr io.Writer) int {
	command := "watchman test"
	fixture := ""
	routeID := ""
	configPath := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--fixture":
			if i+1 >= len(args) {
				return usageError(stderr, command, "--fixture requires a path")
			}
			i++
			fixture = args[i]
		case "--route":
			if i+1 >= len(args) {
				return usageError(stderr, command, "--route requires an id")
			}
			i++
			routeID = args[i]
		case "--config":
			if i+1 >= len(args) {
				return usageError(stderr, command, "--config requires a path")
			}
			i++
			configPath = args[i]
		default:
			return usageError(stderr, command, fmt.Sprintf("unknown argument %q", args[i]))
		}
	}
	var binding any
	if routeID != "" {
		_, route, resource, code := loadRoute(configPath, routeID, command, stderr)
		if code != 0 {
			return code
		}
		stored, hasStored, err := storedBindingFor(configPath, routeID)
		if err != nil {
			writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
			return 20
		}
		if hasStored {
			binding = effectiveBinding{
				ConfiguredRoot: stored.ConfiguredRoot, ActualRoot: stored.ActualRoot,
				RelativeRoot: stored.RelativeRoot, TriggerName: stored.TriggerName,
			}
		} else {
			// No persisted binding yet: the logical root is the
			// configured root with a trivial relative root.
			binding = effectiveBinding{
				ConfiguredRoot: resource.Root, ActualRoot: resource.Root,
				RelativeRoot: ".", TriggerName: route.Source.TriggerName,
			}
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
		"binding":            binding,
	})
}

package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeskanban"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
	"github.com/irootkernel/agent-dispatch/internal/config"
)

// The E11-T3 preflight surface (CLI-010, CLI-011, HER-015 through
// HER-017, OPS-013): `hermes profiles` lists the public profiles,
// `route preflight` proves every configured destination executable by
// its on-disk profile with all required skills before enablement, and
// the destination-qualified `route set-profile`/`set-skills` edit
// exactly one destination through the atomic CLI-015 mutation path.

// runHermesProfiles implements `hermes profiles` (CLI-010, HER-015):
// the typed assignees surface with each profile's on-disk status —
// no Hermes state is touched. --refresh belongs to `hermes
// capabilities` only and is refused at the documented usage exit.
func runHermesProfiles(command string, args []string, stdout, stderr io.Writer) int {
	cfg, targetID, _, refresh, code := hermesTargetSelection(command, args, stderr)
	if code != 0 {
		return code
	}
	if refresh {
		return usageError(stderr, command, "--refresh belongs to 'hermes capabilities'; profiles enumerates the live public surface")
	}
	target := cfg.HermesTargets[targetID]
	limits, err := hermesTargetProcessLimits(cfg, target)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", fmt.Sprintf("hermes_targets.%s: %v", targetID, err))
		return 3
	}
	client := hermeskanban.NewClient(target.Executable, limits)
	profiles, err := client.Assignees(requestCtx(), target.Board)
	if err != nil {
		writeError(stderr, command, "target_definite_unavailable", "target_unavailable", err.Error())
		return 11
	}
	entries := make([]map[string]any, 0, len(profiles))
	for _, p := range profiles {
		entries = append(entries, map[string]any{
			"profile": p.Name,
			"on_disk": p.OnDisk,
		})
	}
	return writeEnvelope(stdout, command, map[string]any{
		"target_id": targetID,
		"board":     target.Board,
		"profiles":  entries,
		"count":     len(entries),
	})
}

// runRoutePreflight implements `route preflight --route <id>` (CLI-010,
// HER-015 through HER-017, OPS-013): every configured destination is
// proven executable by its selected on-disk Hermes profile with all
// required skills, over the target, board, workspace, mutex, hints,
// notification sinks, and the effective Watchman binding. Every gap
// names bounded sorted alternatives and the concrete remediation; a
// missing profile or skill blocks before any task creation.
func runRoutePreflight(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	routeID := flags.val("--route")
	if routeID == "" && flags.positional != "" {
		routeID = flags.positional
	}
	if routeID == "" {
		return usageError(stderr, command, "route preflight requires --route <id>")
	}
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return 3
	}
	route, ok := cfg.Routes[routeID]
	if !ok {
		writeError(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q is not defined", routeID))
		return 3
	}
	checks := []map[string]any{}
	blocked := false
	block := func(check, detail, remediation string, alternatives []string) {
		blocked = true
		checks = append(checks, map[string]any{
			"check": check, "state": "fail", "detail": detail,
			"alternatives": alternatives, "remediation": remediation,
		})
	}
	pass := func(check, detail string) {
		checks = append(checks, map[string]any{"check": check, "state": "pass", "detail": detail})
	}

	for _, dest := range route.SortedDestinations() {
		resolved, ok := cfg.ResolveTarget(dest.Target)
		if !ok {
			block("target", fmt.Sprintf("destination %q references unknown target %q", dest.ID, dest.Target),
				"declare the target under hermes_targets (or targets for a webhook destination)", nil)
			continue
		}
		if resolved.Webhook != nil {
			pass("target", fmt.Sprintf("destination %q targets the webhook %q; the static declaration was validated at load", dest.ID, dest.Target))
			continue
		}
		t := resolved.Hermes
		limits, lerr := hermesTargetProcessLimits(cfg, *t)
		if lerr != nil {
			block("target", fmt.Sprintf("hermes_targets.%s: %v", dest.Target, lerr), "fix the target's execution bounds", nil)
			continue
		}
		pass("target", fmt.Sprintf("destination %q targets hermes %q (board %q, floor %s)", dest.ID, dest.Target, t.Board, orDefault(t.MinimumVersion, config.MinimumEligibleHermesVersion)))
		client := hermeskanban.NewClient(t.Executable, limits)

		// Profile existence through the public assignees surface
		// (HER-015) with bounded sorted alternatives (HER-017).
		profiles, perr := client.Assignees(requestCtx(), t.Board)
		if perr != nil {
			block("profile", fmt.Sprintf("destination %q: the profile surface is unreachable: %v", dest.ID, perr),
				"run 'agent-dispatch hermes probe --target "+dest.Target+"' and resolve the named failures", nil)
			continue
		}
		var onDisk []string
		found := false
		for _, p := range profiles {
			if p.OnDisk {
				onDisk = append(onDisk, p.Name)
			}
			if p.Name == dest.Profile {
				found = p.OnDisk
			}
		}
		sort.Strings(onDisk)
		if !found {
			if containsStringSorted(onDisk, dest.Profile) {
				block("profile", fmt.Sprintf("destination %q: profile %q exists but is not on disk", dest.ID, dest.Profile),
					"create the profile in Hermes or select an on-disk profile with 'agent-dispatch route set-profile "+routeID+":"+dest.ID+" <profile>'", onDisk)
			} else {
				block("profile", fmt.Sprintf("destination %q: profile %q does not exist on board %q", dest.ID, dest.Profile, t.Board),
					"create the profile in Hermes or select one of the on-disk profiles with 'agent-dispatch route set-profile "+routeID+":"+dest.ID+" <profile>'", onDisk)
			}
			continue
		}
		pass("profile", fmt.Sprintf("destination %q: profile %q is on disk", dest.ID, dest.Profile))

		// Enabled-skill inventory through the fixed-environment public
		// skill table (HER-016, SEC-014) with bounded sorted
		// alternatives (HER-017).
		prober, prerr := hermeskanban.NewProber(dest.Target, t.Executable, t.MinimumVersion, t.Board, dest.Profile, limits)
		if prerr != nil {
			block("skills", fmt.Sprintf("destination %q: %v", dest.ID, prerr), "fix the target's eligibility floor", nil)
			continue
		}
		record, rerr := prober.Probe(requestCtx())
		if rerr != nil {
			block("skills", fmt.Sprintf("destination %q: the probe failed: %v", dest.ID, rerr),
				"run 'agent-dispatch hermes probe --target "+dest.Target+"'", nil)
			continue
		}
		enabled := record.EnabledSkillNames()
		if enabled == nil {
			block("skills", fmt.Sprintf("destination %q: the skill table for profile %q did not parse; the preflight fails closed", dest.ID, dest.Profile),
				"run 'agent-dispatch hermes probe --target "+dest.Target+" --profile "+dest.Profile+"' and inspect the skill-table shape", nil)
			continue
		}
		var missing []string
		for _, want := range dest.Skills {
			if !containsString(enabled, want) {
				missing = append(missing, want)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			sorted := append([]string(nil), enabled...)
			sort.Strings(sorted)
			block("skills", fmt.Sprintf("destination %q: required skills missing or disabled for profile %q: %s", dest.ID, dest.Profile, strings.Join(missing, ", ")),
				"enable each skill for the profile in Hermes or update the destination with 'agent-dispatch route set-skills "+routeID+":"+dest.ID+" <skill>...'", sorted)
			continue
		}
		pass("skills", fmt.Sprintf("destination %q: all %d required skills are enabled for profile %q", dest.ID, len(dest.Skills), dest.Profile))

		// Workspace, mutex, and hints are validated at configuration
		// load; the preflight restates them as the reviewed envelope.
		pass("workspace", fmt.Sprintf("destination %q: workspace %q", dest.ID, workspaceOf(dest, cfg.Resources[route.Source.Resource].Root)))
		if dest.MutexKey != "" {
			if !record.CapabilitiesIncludeMutex() {
				checks = append(checks, map[string]any{
					"check": "mutex", "state": "warn",
					"detail":      fmt.Sprintf("destination %q: the create surface lacks --mutex-key; the mutex key will be suppressed at submit", dest.ID),
					"remediation": "upgrade Hermes to a release carrying --mutex-key or clear mutex_key for this destination",
				})
			} else {
				pass("mutex", fmt.Sprintf("destination %q: mutex key honored by the create surface", dest.ID))
			}
		}
		pass("hints", fmt.Sprintf("destination %q: execution hints max_runtime=%s max_attempts=%d", dest.ID, dest.ExecutionHints.MaxRuntime, dest.ExecutionHints.MaxAttempts))
	}

	// Notification sinks: the declared policy must validate and name
	// reachable sinks (the delivery itself arrives with E13).
	if route.Notifications != nil {
		if len(route.Notifications.Sinks) == 0 {
			pass("notifications", "notifications are disabled (no sinks configured)")
		} else {
			var sinkIDs []string
			for _, sink := range route.Notifications.Sinks {
				sinkIDs = append(sinkIDs, sink.ID)
			}
			sort.Strings(sinkIDs)
			pass("notifications", fmt.Sprintf("%d sink(s) declared and validated: %s", len(sinkIDs), strings.Join(sinkIDs, ", ")))
		}
	} else {
		pass("notifications", "no notification policy declared (disabled)")
	}

	// The effective Watchman binding (SRC-009): the persisted managed
	// record for the route's resource, when a store exists.
	bindingState := "no database yet (first use); the binding is validated at install and dispatch time"
	if store, serr := openUnmigratedStore(resolveConfigPath(flags.val("--config"))); serr == nil {
		if binding, berr := store.LoadWatchBinding(requestCtx(), routeID); berr == nil {
			bindingState = fmt.Sprintf("configured root %s, actual watch root %s, trigger %s", binding.ConfiguredRoot, binding.ActualRoot, binding.TriggerName)
		} else {
			bindingState = "no persisted binding yet; run 'agent-dispatch watchman install --route " + routeID + "'"
		}
		store.Close()
	}
	pass("watchman", bindingState)

	if blocked {
		// The failing checks ride the error envelope so the operator
		// sees every alternative and remediation in one document.
		writeErrorWithResult(stderr, command, "config_capability_missing", "configuration",
			"route preflight failed: resolve each failed check before enabling; nothing was submitted and no task was created",
			map[string]any{"route_id": routeID, "ok": false, "checks": checks})
		return 3
	}
	return writeEnvelope(stdout, command, map[string]any{
		"route_id": routeID, "ok": true, "checks": checks,
	})
}

// runRouteSetProfile implements the destination-qualified
// `route set-profile <route>[:<destination>] <profile>` (CLI-011).
func runRouteSetProfile(command string, args []string, stdout, stderr io.Writer) int {
	selector, rest, code := splitSelectorArgs(command, args, stderr)
	if code != 0 {
		return code
	}
	if len(rest) != 1 {
		return usageError(stderr, command, "route set-profile requires <route>:<destination> <profile>")
	}
	profile := rest[0]
	routeID, destID, code := resolveDestinationSelector(command, selector, args, stderr)
	if code != 0 {
		return code
	}
	configPath := selectorConfigPath(args)
	if err := config.MutateDestination(configPath, routeID, destID, func(d *config.Destination) error {
		d.Profile = profile
		return nil
	}); err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return 3
	}
	return mutationResult(stdout, stderr, command, configPath, routeID, "profile", profile)
}

// runRouteSetSkills implements the destination-qualified
// `route set-skills <route>[:<destination>] <skill>...` (CLI-011).
func runRouteSetSkills(command string, args []string, stdout, stderr io.Writer) int {
	selector, rest, code := splitSelectorArgs(command, args, stderr)
	if code != 0 {
		return code
	}
	if len(rest) < 1 {
		return usageError(stderr, command, "route set-skills requires <route>:<destination> <skill>...")
	}
	skills := rest
	routeID, destID, code := resolveDestinationSelector(command, selector, args, stderr)
	if code != 0 {
		return code
	}
	configPath := selectorConfigPath(args)
	if err := config.MutateDestination(configPath, routeID, destID, func(d *config.Destination) error {
		d.Skills = append([]string(nil), skills...)
		return nil
	}); err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return 3
	}
	return mutationResult(stdout, stderr, command, configPath, routeID, "skills", strings.Join(skills, ","))
}

// resolveDestinationSelector splits `<route>` or `<route>:<destination>`
// and enforces the CLI-011 ambiguity rule: the qualifier may be omitted
// only when the route has exactly one destination.
func resolveDestinationSelector(command, selector string, args []string, stderr io.Writer) (string, string, int) {
	routeID, destID := selector, ""
	if i := strings.IndexByte(selector, ':'); i >= 0 {
		routeID, destID = selector[:i], selector[i+1:]
	}
	cfg, err := config.Load(resolveConfigPath(selectorConfigPath(args)))
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return "", "", 3
	}
	route, ok := cfg.Routes[routeID]
	if !ok {
		writeError(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q is not defined", routeID))
		return "", "", 3
	}
	if destID == "" {
		if len(route.Destinations) != 1 {
			writeError(stderr, command, "flag_invalid", "usage",
				fmt.Sprintf("route %q declares %d destinations; qualify the edit as <route>:<destination> (destinations: %s)",
					routeID, len(route.Destinations), destinationIDs(route)))
			return "", "", 2
		}
		destID = route.Destinations[0].ID
	} else if _, ok := route.DestinationByID(destID); !ok {
		writeError(stderr, command, "config_invalid", "configuration",
			fmt.Sprintf("destination %q is not defined in route %q (destinations: %s)", destID, routeID, destinationIDs(route)))
		return "", "", 3
	}
	return routeID, destID, 0
}

func destinationIDs(route config.Route) string {
	ids := make([]string, 0, len(route.Destinations))
	for _, d := range route.SortedDestinations() {
		ids = append(ids, d.ID)
	}
	return strings.Join(ids, ", ")
}

// splitSelectorArgs separates the destination selector from the value
// arguments, leaving any --flag value pairs out of the positional
// stream (the mutation commands accept flags in any position).
func splitSelectorArgs(command string, args []string, stderr io.Writer) (string, []string, int) {
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--") {
			// Skip the flag's value for the known valued flags, in both
			// the space and = forms (the shared parser's convention).
			switch {
			case arg == "--config" || arg == "--route":
				i++
			case strings.HasPrefix(arg, "--config=") || strings.HasPrefix(arg, "--route="):
			default:
				return "", nil, usageError(stderr, command, fmt.Sprintf("unknown argument %q", arg))
			}
			continue
		}
		positional = append(positional, arg)
	}
	if len(positional) < 1 {
		return "", nil, usageError(stderr, command, "the destination selector <route>:<destination> is required")
	}
	return positional[0], positional[1:], 0
}

// selectorConfigPath extracts --config from the raw argv for the
// mutation path, accepting both the space and = forms.
func selectorConfigPath(args []string) string {
	for i, a := range args {
		if a == "--config" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, "--config=") {
			return strings.TrimPrefix(a, "--config=")
		}
	}
	return ""
}

// mutationResult reports the mutation with the before/after revisions
// so the operator sees the revision pause (CLI-015: the destination
// edit is behavior-affecting and pauses the acknowledged route).
func mutationResult(stdout, stderr io.Writer, command, configPath, routeID, field, value string) int {
	cfg, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return 3
	}
	revision, _ := config.RouteRevision(cfg, routeID)
	return writeEnvelope(stdout, command, map[string]any{
		"route_id": routeID, "field": field, "value": value,
		"route_revision": revision,
		"note":           "the destination edit changed the route revision; an acknowledged route pauses until re-acknowledged",
	})
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func containsStringSorted(sorted []string, want string) bool {
	i := sort.SearchStrings(sorted, want)
	return i < len(sorted) && sorted[i] == want
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// Compile-time assertions keeping the preflight imports honest.
var (
	_ = watchman.ValidateBinding
	_ = sqlite.ErrOptimisticConcurrency
	_ = context.Background
)

// routeDriftSummary reports the five OPS-013 drift classes per route —
// capability, profile, skill, watchman, and reconciliation — as an
// observational projection for the status command. Every check is
// bounded and read-only; nothing here heals, submits, or blocks.
func routeDriftSummary(ctx context.Context, cfg *config.Config, store *sqlite.Store) []map[string]any {
	out := []map[string]any{}
	for _, routeID := range cfg.SortedRouteIDs() {
		route := cfg.Routes[routeID]
		entry := map[string]any{"route_id": routeID, "drift": map[string]any{}}
		drift := entry["drift"].(map[string]any)

		// Reconciliation drift: a pending generation or a stale
		// last-reconciled window.
		if snap, err := store.LoadRouteState(ctx, routeID); err == nil {
			if snap.PendingReconcile {
				drift["reconciliation"] = "a reconciliation generation is pending"
			}
		}

		// Watchman drift: the persisted managed binding's trigger
		// differs from the configured one, or no binding exists while
		// the route is enabled.
		if binding, err := store.LoadWatchBinding(ctx, routeID); err == nil {
			if binding.TriggerName != route.Source.TriggerName {
				drift["watchman"] = fmt.Sprintf("persisted trigger %q differs from the configured %q", binding.TriggerName, route.Source.TriggerName)
			}
		} else if route.Enabled {
			drift["watchman"] = "no persisted Watchman binding; run 'agent-dispatch watchman install --route " + routeID + "'"
		}

		// Capability, profile, and skill drift against the cached
		// probe evidence: observational, bounded, offline where the
		// evidence is absent.
		for _, dest := range route.SortedDestinations() {
			resolved, ok := cfg.ResolveTarget(dest.Target)
			if !ok || resolved.Hermes == nil {
				continue
			}
			t := resolved.Hermes
			if snap, err := store.LoadRouteState(ctx, routeID); err == nil && snap.CapabilityFingerprint != "" {
				digest, derr := hermeskanban.ExecutableDigest(t.Executable)
				if derr != nil {
					drift["capability"] = "the executable identity could not be verified: " + derr.Error()
				} else {
					record, rerr := hermeskanban.LoadCapabilityRecord(capabilityCachePath(dest.Target))
					if rerr != nil {
						drift["capability"] = "no capability evidence is cached; run 'agent-dispatch hermes probe --target " + dest.Target + "'"
					} else if reason := record.StaleReasonForProfile(t.Executable, digest, "", dest.Profile); reason != "" {
						drift["capability"] = reason
					}
				}
			}
			record, rerr := hermeskanban.LoadCapabilityRecord(capabilityCachePath(dest.Target))
			if rerr == nil && record.EnabledSkillNames() != nil {
				if !containsString(record.EnabledSkillNames(), "") {
					// Profile drift: the destination's profile is not
					// among the probed evidence's scope or the board's
					// on-disk set is unknown offline; the definitive
					// check lives in preflight, so status reports only
					// the cached scope mismatch.
					if record.Profile != "" && record.Profile != dest.Profile {
						drift["profile"] = fmt.Sprintf("cached evidence covers profile %q, destination %q uses %q", record.Profile, dest.ID, dest.Profile)
					}
					for _, want := range dest.Skills {
						if !containsString(record.EnabledSkillNames(), want) {
							drift["skill"] = fmt.Sprintf("destination %q requires skill %q that the cached profile evidence does not list as enabled", dest.ID, want)
							break
						}
					}
				}
			}
			break // one certified destination pre-E12; E12 widens this
		}
		out = append(out, entry)
	}
	return out
}

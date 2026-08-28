package cli

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeskanban"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/app/receipts"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Route runtime-management subcommands (E3-T3, CLI-004): list, show,
// enable, and disable. `route plan` stays in plan.go.
func runRoute(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "route", "route requires a subcommand: plan, list, show, enable, disable, stale, preflight, set-profile, or set-skills")
	}
	switch args[0] {
	case "plan":
		return runPlan("route plan", args[1:], stdout, stderr)
	case "list":
		return runRouteList("route list", args[1:], stdout, stderr)
	case "show":
		return runRouteShow("route show", args[1:], stdout, stderr)
	case "enable":
		return runRouteEnable("route enable", args[1:], stdout, stderr)
	case "disable":
		return runRouteDisable("route disable", args[1:], stdout, stderr)
	case "stale":
		return runRouteStale("route stale", args[1:], stdout, stderr)
	case "preflight":
		return runRoutePreflight("route preflight", args[1:], stdout, stderr)
	case "set-profile":
		return runRouteSetProfile("route set-profile", args[1:], stdout, stderr)
	case "set-skills":
		return runRouteSetSkills("route set-skills", args[1:], stdout, stderr)
	default:
		return usageError(stderr, "route", fmt.Sprintf("unknown route subcommand %q", args[0]))
	}
}

func runRouteList(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	rows, err := store.ListRoutes(requestCtx())
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	return writeEnvelope(stdout, command, map[string]any{"routes": rows, "count": len(rows)})
}

func runRouteShow(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	if flags.positional == "" && flags.val("--route") == "" {
		return usageError(stderr, command, "route show requires a route ID")
	}
	routeID := flags.positional
	if routeID == "" {
		routeID = flags.val("--route")
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	rows, err := store.ListRoutes(requestCtx())
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	for _, row := range rows {
		if row.RouteID != routeID {
			continue
		}
		// Route status projection (E4-T4): the active dispatch's
		// acceptance and latest persisted execution projection, with
		// stale-active detection that warns and never auto-fails.
		result := map[string]any{
			"route_id": row.RouteID, "revision": row.Revision, "resource_id": row.ResourceID,
			"target_id": row.TargetID, "activation_state": row.ActivationState,
			"acknowledged_revision": row.AcknowledgedRevision, "route_state": row.RouteState,
			"active_dispatch_id": row.ActiveDispatchID, "dirty_generation": row.DirtyGeneration,
			"pending_reconcile":    row.PendingReconcile == 1,
			"last_source_position": row.LastSourcePosition, "last_reconciled_at": row.LastReconciledAt,
		}
		var warnings []string
		if row.PendingReconcile == 1 {
			warnings = append(warnings, "a reconciliation generation is pending; run 'agent-dispatch reconcile --route "+row.RouteID+"' or complete the active work to collapse it")
		}
		if row.ActiveDispatchID != "" {
			intent, err := store.LoadIntent(requestCtx(), row.ActiveDispatchID)
			if err != nil {
				writeError(stderr, command, "sqlite_query_failed", "storage", fmt.Sprintf("loading active dispatch %s: %v", row.ActiveDispatchID, err))
				return 20
			}
			result["active_dispatch_state"] = string(intent.State)
			result["active_dispatch_external_ref"] = intent.ExternalRef
			projected, err := store.ListReceipts(requestCtx(), ports.ReceiptFilter{DispatchID: row.ActiveDispatchID, Kind: "execution_projection", Limit: 1})
			if err != nil {
				writeError(stderr, command, "sqlite_query_failed", "storage", fmt.Sprintf("loading execution projection: %v", err))
				return 20
			}
			if len(projected) == 1 {
				result["execution_projection"] = string(projected[0].ExecutionState)
				result["execution_observed_at"] = projected[0].TargetObservedAt
			}
			staleAfter, staleErr := routeActiveStaleAfter(flags.val("--config"), routeID)
			if staleErr != nil {
				warnings = append(warnings, "stale-active window unreadable: "+staleErr.Error())
			} else if staleAfter > 0 {
				summaries, err := store.ListIntents(requestCtx(), ports.IntentFilter{DispatchID: row.ActiveDispatchID, Limit: 1})
				if err != nil {
					writeError(stderr, command, "sqlite_query_failed", "storage", fmt.Sprintf("loading active dispatch summary: %v", err))
					return 20
				}
				if len(summaries) == 1 && receipts.StaleActive(summaries[0].CreatedAt, dispatch.Timestamp(time.Now()), staleAfter) {
					warnings = append(warnings, fmt.Sprintf("active dispatch %s is older than the configured active_stale_after %s; it is warned, not auto-failed — inspect and reconcile explicitly", row.ActiveDispatchID, staleAfter))
				}
			}
		}
		return writeEnvelopeWithWarnings(stdout, command, result, warnings)
	}
	return planErr(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q has no materialized state; run the route once or check the configuration", routeID), 3)
}

// routeActiveStaleAfter resolves the configured stale window for one
// route; zero disables the check and a configuration failure is
// surfaced instead of silently skipped.
func routeActiveStaleAfter(configPath, routeID string) (time.Duration, error) {
	cfg, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		return 0, fmt.Errorf("configuration: %v", err)
	}
	route, ok := cfg.Routes[routeID]
	if !ok {
		return 0, fmt.Errorf("route %q is not defined", routeID)
	}
	if route.ActiveStaleAfter == "" {
		return 0, nil
	}
	d, err := config.ParseDuration(route.ActiveStaleAfter)
	if err != nil {
		return 0, fmt.Errorf("active_stale_after: %v", err)
	}
	return time.Duration(d.Nanos), nil
}

func runRouteEnable(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	if flags.val("--acknowledge-production-gate") == "" || flags.val("--yes") == "" {
		return usageError(stderr, command, "route enable requires --acknowledge-production-gate and --yes")
	}
	routeID := flags.val("--route")
	if routeID == "" {
		return usageError(stderr, command, "route enable requires --route")
	}
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err == nil {
		if _, defined := cfg.Routes[routeID]; !defined {
			return planErr(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q is not defined", routeID), 3)
		}
		if !cfg.Routes[routeID].Enabled {
			return planErr(stderr, command, "config_invalid", "configuration",
				fmt.Sprintf("route %q is disabled in configuration (routes.%s.enabled: false); the two-key gate requires both keys", routeID, routeID), 3)
		}
	}
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	if _, ok := cfg.Routes[routeID]; !ok {
		return planErr(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q is not defined", routeID), 3)
	}
	revision, ok := config.RouteRevision(cfg, routeID)
	if !ok {
		return planErr(stderr, command, "internal_unclassified", "internal", "route revision could not be computed", 40)
	}
	// The production gate is explicit: the operator must acknowledge the
	// exact computed route revision, never an assumed one (E5-T5).
	if flags.val("--acknowledge-production-gate") != revision {
		return planErr(stderr, command, "config_invalid", "configuration",
			fmt.Sprintf("acknowledged revision %q does not match the computed route revision %q; review the route and acknowledge the computed value", flags.val("--acknowledge-production-gate"), revision), 3)
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	capabilityFingerprint, code := routeEnableGate(command, cfg, routeID, revision, closer, stderr)
	if code != 0 {
		return code
	}
	if err := store.SetRouteActivation(requestCtx(), routeID, "enabled", revision, capabilityFingerprint, dispatch.Timestamp(time.Now())); err != nil {
		if errors.Is(err, sqlite.ErrOptimisticConcurrency) {
			// First use: materialize the registration from the
			// configuration and retry the activation once.
			if regErr := registerRouteState(requestCtx(), closer, cfg, routeID); regErr != nil {
				return planErr(stderr, command, "route_not_registered", "conflict", regErr.Error(), 14)
			}
			if capabilityFingerprint, code = routeEnableGate(command, cfg, routeID, revision, closer, stderr); code != 0 {
				return code
			}
			if err := store.SetRouteActivation(requestCtx(), routeID, "enabled", revision, capabilityFingerprint, dispatch.Timestamp(time.Now())); err != nil {
				return planErr(stderr, command, "transition_invalid", "conflict", err.Error(), 14)
			}
		} else {
			return planErr(stderr, command, "route_not_registered", "conflict", err.Error(), 14)
		}
	}
	return writeEnvelope(stdout, command, map[string]any{"route_id": routeID, "activation_state": "enabled", "acknowledged_revision": revision})
}

// routeEnableGate is the production enable precondition (E8-T3, E11-T1,
// E12-T2 FAN-011): a route may be enabled when (1) every destination
// binds the same target — one Hermes Kanban submission surface, refused
// otherwise by resolveRouteTarget — and every configured destination
// profile exists on the target's board, (2) a hermes destination's
// installed Hermes meets the declared minimum-version eligibility floor
// (HER-011; the E11-T2 capability probe replaces this with the
// fingerprint-bound shape proof), where an unreachable executable warns
// and defers to the submit path's run-time gate, and (3) the route
// carries no unresolved legacy work created under a different route
// revision (DAT-013: the operator resolves it through the documented
// exits first; nothing is silently submitted under the new destination
// contract).
func routeEnableGate(command string, cfg *config.Config, routeID, revision string, store *sqlite.Store, stderr io.Writer) (string, int) {
	if _, ok := cfg.Routes[routeID]; !ok {
		return "", 0
	}
	_, resolved, rerr := resolveRouteTarget(cfg, routeID)
	if rerr != nil {
		return "", planErr(stderr, command, "config_invalid", "configuration", rerr.Error(), 3)
	}
	if count, detail, qerr := store.UnresolvedLegacyWork(requestCtx(), routeID, revision); qerr != nil {
		return "", planErr(stderr, command, "sqlite_query_failed", "storage", qerr.Error(), 20)
	} else if count > 0 {
		return "", planErr(stderr, command, "transition_invalid", "conflict",
			fmt.Sprintf("route %q carries unresolved legacy work created under a different route revision (%s); resolve it before enabling under the destinations contract — 'agent-dispatch dispatches drain --route %s' recovers crashed submits and reconciles unknowns, 'agent-dispatch dispatches retry/discard' resolves dead letters, 'agent-dispatch quarantine release/discard' resolves held items, then re-run enable (DAT-013)",
				routeID, detail, routeID), 14)
	}
	if resolved.Hermes == nil {
		// A webhook destination has no live version surface; its static
		// capability declaration was validated at configuration load.
		return "", 0
	}
	limits, err := hermesTargetProcessLimits(cfg, *resolved.Hermes)
	if err != nil {
		return "", planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	adapter, err := hermeskanban.New(resolved.ID, resolved.Hermes.Executable, resolved.Hermes.MinimumVersion, limits)
	if err != nil {
		return "", planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	summary, _, perr := adapter.ProbeVerbose(requestCtx())
	if perr != nil {
		// A persistent configuration defect fails the enable: the old
		// deferral to the submit gate let routes enable against targets
		// they could never honor (H-7).
		return "", planErr(stderr, command, "config_invalid", "configuration", perr.Error(), 3)
	}
	switch summary.State {
	case "available":
		dests := cfg.Routes[routeID].SortedDestinations()
		profile := ""
		if len(dests) > 0 {
			profile = dests[0].Profile
		}
		// HER-015 (AC-704): every configured destination profile must
		// exist on disk before enablement (E12-T2: the check covers each
		// lane's profile), with the bounded sorted alternatives preflight
		// lists. Only a CONFIRMED missing profile fails the enable — an
		// unreachable profile surface keeps the liveness posture below,
		// because an outage must not hold re-acknowledgement hostage.
		if profiles, aerr := adapter.Client().Assignees(requestCtx(), resolved.Hermes.Board); aerr == nil {
			var onDisk []string
			onDiskSet := map[string]bool{}
			for _, p := range profiles {
				if p.OnDisk {
					onDisk = append(onDisk, p.Name)
					onDiskSet[p.Name] = true
				}
			}
			for _, dest := range dests {
				if dest.Profile == "" || onDiskSet[dest.Profile] {
					continue
				}
				sort.Strings(onDisk)
				return "", planErr(stderr, command, "config_capability_missing", "configuration",
					fmt.Sprintf("destination profile %q does not exist on board %q; on-disk profiles: %s — run 'agent-dispatch route preflight --route %s', create the profile in Hermes, or select an on-disk profile with 'agent-dispatch route set-profile %s:%s <profile>' (HER-015)",
						dest.Profile, resolved.Hermes.Board, strings.Join(boundedAlternatives(onDisk), ", "), routeID, routeID, dest.ID), 3)
			}
		}
		// HER-018: activation binds the capability-evidence
		// fingerprint on top of the eligibility gate. A usable cached
		// record binds its fingerprint; an unavailable cache is probed
		// now; an unprobeable executable keeps the liveness deferral
		// with an empty fingerprint (the submit path re-proves it
		// before any side effect).
		fingerprint, ferr := currentCapabilityFingerprint(cfg, resolved, profile, limits)
		if ferr != nil {
			return "", planErr(stderr, command, "config_capability_missing", "configuration", ferr.Error(), 3)
		}
		return fingerprint, 0
	case "version_unsupported":
		return "", planErr(stderr, command, "config_invalid", "configuration",
			fmt.Sprintf("hermes_targets.%s probes version_unsupported: %s; a production route cannot be enabled against it", resolved.ID, summary.Detail), 3)
	default:
		// Target liveness (an absent or unprobeable executable) is a
		// warning, not an enable refusal: re-acknowledging a paused
		// production route must not be hostage to the target being up
		// (the submit path gates again at run time). The capability
		// fingerprint still binds whenever the cached evidence is fresh
		// for this executable — a liveness dip must not silently strip
		// the submit-path shape re-proof; only a genuinely unprobed
		// target enables without a binding, and the warning says so.
		fingerprint := ""
		dests := cfg.Routes[routeID].SortedDestinations()
		profile := ""
		if len(dests) > 0 {
			profile = dests[0].Profile
		}
		if cached, cerr := hermeskanban.LoadCapabilityRecord(capabilityCachePath(resolved.ID)); cerr == nil {
			if digest, derr := hermeskanban.ExecutableDigest(resolved.Hermes.Executable); derr == nil {
				if stale := cached.StaleReasonForProfile(resolved.Hermes.Executable, digest, "", profile); stale == "" && cached.AllRequiredPassed() {
					fingerprint = cached.Fingerprint
				}
			}
		}
		if fingerprint == "" {
			fmt.Fprintf(stderr, "warning: hermes_targets.%s probes %s: %s; enabled without a capability-evidence binding — run 'agent-dispatch hermes probe --target %s' and re-acknowledge to bind the fingerprint\n", resolved.ID, summary.State, summary.Detail, resolved.ID)
		} else {
			fmt.Fprintf(stderr, "warning: hermes_targets.%s probes %s: %s; the cached capability fingerprint stays bound and the submit path re-proves it at run time\n", resolved.ID, summary.State, summary.Detail)
		}
		return fingerprint, 0
	}
}

// currentCapabilityFingerprint resolves the capability-evidence
// fingerprint a hermes activation binds (HER-018): a fresh, non-stale
// cached record is bound as-is; anything else is probed now and the
// cache refreshed, so activation always names evidence this build can
// re-verify at submission time.
func currentCapabilityFingerprint(cfg *config.Config, resolved config.ResolvedTarget, profile string, limits hermeskanban.ProcessLimits) (string, error) {
	t := resolved.Hermes
	cachePath := capabilityCachePath(resolved.ID)
	if record, err := hermeskanban.LoadCapabilityRecord(cachePath); err == nil {
		if digest, derr := hermeskanban.ExecutableDigest(t.Executable); derr == nil {
			if stale := record.StaleReasonForProfile(t.Executable, digest, "", profile); stale == "" && record.AllRequiredPassed() {
				return record.Fingerprint, nil
			}
		}
	}
	prober, err := hermeskanban.NewProber(resolved.ID, t.Executable, t.MinimumVersion, t.Board, profile, limits)
	if err != nil {
		return "", err
	}
	record, err := prober.Probe(requestCtx())
	if err != nil {
		return "", err
	}
	if !record.AllRequiredPassed() {
		if _, cerr := record.Capabilities(); cerr != nil {
			return "", cerr
		}
	}
	if werr := hermeskanban.WriteCapabilityRecord(record, cachePath); werr != nil {
		return "", fmt.Errorf("write capability evidence: %w", werr)
	}
	return record.Fingerprint, nil
}

func runRouteDisable(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	routeID := flags.val("--route")
	if routeID == "" {
		return usageError(stderr, command, "route disable requires --route")
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	if err := store.SetRouteActivation(requestCtx(), routeID, "disabled", "", "", dispatch.Timestamp(time.Now())); err != nil {
		if errors.Is(err, sqlite.ErrOptimisticConcurrency) {
			return planErr(stderr, command, "transition_invalid", "conflict", err.Error(), 14)
		}
		return planErr(stderr, command, "route_not_registered", "conflict", err.Error(), 14)
	}
	warnings := []string{}
	if reason := strings.TrimSpace(flags.val("--reason")); reason != "" {
		warnings = append(warnings, "reason: "+reason)
	}
	return writeEnvelopeWithWarnings(stdout, command, map[string]any{"route_id": routeID, "activation_state": "disabled"}, warnings)
}

// runRouteStale moves an active route to UNCERTAIN through the declared
// execution-evidence-stale edge (E7-T7/M-6): the operator exit for a
// stale active route (older than active_stale_after). The uncertain
// route is then resolved through the documented reconciliation or
// lookup exits.
func runRouteStale(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	routeID := flags.val("--route")
	if routeID == "" {
		return usageError(stderr, command, "route stale requires --route")
	}
	if strings.TrimSpace(flags.val("--reason")) == "" {
		return usageError(stderr, command, "route stale requires --reason")
	}
	_, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	// cli-spec section 3 documents the precondition: the active dispatch
	// must be older than the route's active_stale_after before an
	// operator may stale it (E8-T4, M-23 — a minutes-old dispatch with a
	// 2h bound is live work, not stale evidence).
	// The precondition fails closed (epic audit round-1 F003): a
	// configuration that cannot load leaves the bound unknown, and
	// stale-ing live work on an unknown bound is exactly the failure the
	// precondition exists to prevent.
	cfg, cfgErr := config.Load(resolveConfigPath(flags.val("--config")))
	if cfgErr != nil {
		return planErr(stderr, command, "config_invalid", "configuration",
			fmt.Sprintf("route stale requires the active_stale_after bound, and the configuration failed to load: %v", cfgErr), 3)
	}
	// The eligibility rule is store-level (E9-T2/T4-F006): the CLI
	// reads the bound and asks the store, instead of composing the guard
	// chain here.
	if route, ok := cfg.Routes[routeID]; ok {
		bound := time.Duration(0)
		if route.ActiveStaleAfter != "" {
			if d, derr := config.ParseDuration(route.ActiveStaleAfter); derr == nil {
				bound = time.Duration(d.Nanos)
			}
		}
		eligible, why, gerr := closer.EligibleForStale(requestCtx(), routeID, bound)
		if gerr != nil {
			return intentErr(stderr, command, gerr)
		}
		if !eligible {
			return planErr(stderr, command, "transition_invalid", "conflict", why, 14)
		}
	}
	if err := closer.MarkRouteStaleWithReason(requestCtx(), routeID, "operator", flags.val("--reason"), dispatch.Timestamp(time.Now())); err != nil {
		if errors.Is(err, ports.ErrStateNotEligible) {
			return planErr(stderr, command, "transition_invalid", "conflict", err.Error(), 14)
		}
		return intentErr(stderr, command, err)
	}
	return writeEnvelope(stdout, command, map[string]any{
		"route_id": routeID, "route_state": "UNCERTAIN",
		"next": "resolve through reconcile --reason manual or the target lookup",
	})
}

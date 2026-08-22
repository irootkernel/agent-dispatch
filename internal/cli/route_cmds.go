package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

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
		return usageError(stderr, "route", "route requires a subcommand: plan, list, show, enable, or disable")
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
	if route.Dispatch.ActiveStaleAfter == "" {
		return 0, nil
	}
	d, err := config.ParseDuration(route.Dispatch.ActiveStaleAfter)
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
	if err := store.SetRouteActivation(requestCtx(), routeID, "enabled", revision, dispatch.Timestamp(time.Now())); err != nil {
		if errors.Is(err, sqlite.ErrOptimisticConcurrency) {
			// First use: materialize the registration from the
			// configuration and retry the activation once.
			if regErr := registerRouteState(requestCtx(), closer, cfg, routeID); regErr != nil {
				return planErr(stderr, command, "route_not_registered", "conflict", regErr.Error(), 14)
			}
			if err := store.SetRouteActivation(requestCtx(), routeID, "enabled", revision, dispatch.Timestamp(time.Now())); err != nil {
				return planErr(stderr, command, "transition_invalid", "conflict", err.Error(), 14)
			}
		} else {
			return planErr(stderr, command, "route_not_registered", "conflict", err.Error(), 14)
		}
	}
	return writeEnvelope(stdout, command, map[string]any{"route_id": routeID, "activation_state": "enabled", "acknowledged_revision": revision})
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
	if err := store.SetRouteActivation(requestCtx(), routeID, "disabled", "", dispatch.Timestamp(time.Now())); err != nil {
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

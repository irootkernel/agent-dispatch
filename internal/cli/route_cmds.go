package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
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
	case "stale":
		return runRouteStale("route stale", args[1:], stdout, stderr)
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
	if code := routeEnableGate(command, cfg, routeID, stderr); code != 0 {
		return code
	}
	if err := store.SetRouteActivation(requestCtx(), routeID, "enabled", revision, dispatch.Timestamp(time.Now())); err != nil {
		if errors.Is(err, sqlite.ErrOptimisticConcurrency) {
			// First use: materialize the registration from the
			// configuration and retry the activation once.
			if regErr := registerRouteState(requestCtx(), closer, cfg, routeID); regErr != nil {
				return planErr(stderr, command, "route_not_registered", "conflict", regErr.Error(), 14)
			}
			if code := routeEnableGate(command, cfg, routeID, stderr); code != 0 {
				return code
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

// routeEnableGate is the production enable precondition (E8-T3, H-7): a
// hermes-kanban production route may only be enabled when the live
// target probes available through the version-gated report (an
// unreadable or stale report, an unsupported version, or an unusable
// target fails closed at exit 3) and the report itself carries
// durable_acceptance and submit_idempotency_key — the delivery
// guarantees the durable core depends on, required unconditionally, not
// at the operator's option.
func routeEnableGate(command string, cfg *config.Config, routeID string, stderr io.Writer) int {
	route, ok := cfg.Routes[routeID]
	if !ok {
		return 0
	}
	target, ok := cfg.Targets[route.Dispatch.Target]
	if !ok || target.Type != "hermes-kanban" {
		return 0
	}
	limits, err := hermesProcessLimits(cfg, target)
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	adapter := hermeskanban.New(route.Dispatch.Target, target.Executable, target.CapabilityReport, target.RequiredCapabilities, limits)
	summary, caps, perr := adapter.ProbeVerbose(requestCtx())
	if perr != nil {
		// A persistent configuration defect (unreadable or stale report,
		// unknown capability name, missing required capability) fails the
		// enable: the old deferral to the submit gate let routes enable
		// against reports the target could never honor (H-7).
		return planErr(stderr, command, "config_capability_missing", "configuration", perr.Error(), 3)
	}
	switch summary.State {
	case "available":
		if !caps.DurableAcceptance || !caps.SubmitIdempotencyKey {
			return planErr(stderr, command, "config_capability_missing", "configuration",
				fmt.Sprintf("target %s must report durable_acceptance and submit_idempotency_key for a production route (durable=%v, idempotent=%v)",
					route.Dispatch.Target, caps.DurableAcceptance, caps.SubmitIdempotencyKey), 3)
		}
	case "version_unsupported":
		return planErr(stderr, command, "config_invalid", "configuration",
			fmt.Sprintf("target %s probes version_unsupported: %s; a production route cannot be enabled against it", route.Dispatch.Target, summary.Detail), 3)
	default:
		// Target liveness (an absent or unprobeable executable) is a
		// warning, not an enable refusal: re-acknowledging a paused
		// production route must not be hostage to the target being up
		// (the submit path gates again at run time). A report that IS
		// present still validates — parse, required capabilities, and the
		// unconditional guarantees never depend on target liveness
		// (E8-T3 round-1 F002); only a not-yet-placed report warns.
		fmt.Fprintf(stderr, "warning: target %s probes %s: %s; the submit path re-gates at run time\n", route.Dispatch.Target, summary.State, summary.Detail)
		if _, statErr := os.Stat(target.CapabilityReport); statErr == nil {
			report, lerr := hermeskanban.LoadReport(target.CapabilityReport)
			if lerr != nil {
				return planErr(stderr, command, "config_invalid", "configuration", fmt.Sprintf("target %s: %v", route.Dispatch.Target, lerr), 3)
			}
			caps := report.PortCapabilities()
			if verr := hermeskanban.ValidateRequired(route.Dispatch.Target, caps, target.RequiredCapabilities); verr != nil {
				return planErr(stderr, command, "config_capability_missing", "configuration", verr.Error(), 3)
			}
			if !caps.DurableAcceptance || !caps.SubmitIdempotencyKey {
				return planErr(stderr, command, "config_capability_missing", "configuration",
					fmt.Sprintf("target %s must report durable_acceptance and submit_idempotency_key for a production route (durable=%v, idempotent=%v)",
						route.Dispatch.Target, caps.DurableAcceptance, caps.SubmitIdempotencyKey), 3)
			}
		}
	}
	return 0
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

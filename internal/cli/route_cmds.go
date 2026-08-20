package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rootkernel/jjukkumi/internal/app/dispatch"
	"github.com/rootkernel/jjukkumi/internal/config"
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
		return planErr(stderr, command, "sqlite_open_failed", "storage", err.Error(), 20)
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
		return planErr(stderr, command, "sqlite_open_failed", "storage", err.Error(), 20)
	}
	for _, row := range rows {
		if row.RouteID == routeID {
			return writeEnvelope(stdout, command, row)
		}
	}
	return planErr(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q has no materialized state; run the route once or check the configuration", routeID), 3)
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
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	if err := store.SetRouteActivation(requestCtx(), routeID, "enabled", revision, dispatch.Timestamp(time.Now())); err != nil {
		return planErr(stderr, command, "route_not_registered", "conflict", err.Error(), 14)
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
		return planErr(stderr, command, "route_not_registered", "conflict", err.Error(), 14)
	}
	warnings := []string{}
	if reason := strings.TrimSpace(flags.val("--reason")); reason != "" {
		warnings = append(warnings, "reason: "+reason)
	}
	return writeEnvelopeWithWarnings(stdout, command, map[string]any{"route_id": routeID, "activation_state": "disabled"}, warnings)
}

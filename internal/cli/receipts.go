package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/app/receipts"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// runReceipts implements `receipts list|show` (cli-spec §6, OPS-002):
// inspectable acceptance, execution-projection, and work receipts with
// redacted bounded output.
func runReceipts(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "list" && args[0] != "show") {
		return usageError(stderr, "receipts", "receipts requires a subcommand: list or show")
	}
	command := "receipts " + args[0]
	if args[0] == "list" {
		return runReceiptsList(command, args[1:], stdout, stderr)
	}
	return runReceiptsShow(command, args[1:], stdout, stderr)
}

func runReceiptsList(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	filter := ports.ReceiptFilter{
		DispatchID: flags.val("--dispatch"),
		RouteID:    flags.val("--route"),
		Kind:       flags.val("--kind"),
	}
	if raw := flags.val("--limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			return usageError(stderr, command, "--limit must be 1..500")
		}
		filter.Limit = n
	}
	if filter.Kind != "" && filter.Kind != "acceptance" && filter.Kind != "execution_projection" && filter.Kind != "work" {
		return usageError(stderr, command, "--kind must be acceptance, execution_projection, or work")
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	rows, err := store.ListReceipts(requestCtx(), filter)
	if err != nil {
		writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
		return 20
	}
	if rows == nil {
		rows = []ports.ReceiptRecord{}
	}
	return writeEnvelope(stdout, command, map[string]any{"receipts": rows, "count": len(rows)})
}

func runReceiptsShow(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	if flags.positional == "" {
		return usageError(stderr, command, "receipts show requires a receipt ID")
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	detail, err := store.LoadReceipt(requestCtx(), flags.positional)
	if err != nil {
		if errors.Is(err, ports.ErrReceiptNotFound) {
			writeError(stderr, command, "receipt_not_found", "usage", fmt.Sprintf("receipt %q not found", flags.positional))
			return 4
		}
		writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
		return 20
	}
	return writeEnvelope(stdout, command, detail)
}

// runDispatchesRefresh implements the public lookup-refresh behavior
// (E4-T4): re-read the target execution for one accepted dispatch,
// persist the execution-projection receipt, and report the projection.
// Acceptance is never reinterpreted (HER-008).
func runDispatchesRefresh(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	if flags.positional == "" {
		return usageError(stderr, command, "dispatches refresh requires a dispatch ID")
	}
	configPath := resolveConfigPath(flags.val("--config"))
	cfg, err := config.Load(configPath)
	if err != nil {
		planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
		return 3
	}
	dispatchID := flags.positional
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	intent, err := store.LoadIntent(requestCtx(), dispatchID)
	if err != nil {
		return intentErr(stderr, command, err)
	}
	if routeID := flags.val("--route"); routeID != "" && routeID != intent.RouteID {
		return usageError(stderr, command, fmt.Sprintf("dispatch %s belongs to route %q, not %q", dispatchID, intent.RouteID, routeID))
	}
	route, ok := cfg.Routes[intent.RouteID]
	if !ok {
		return planErr(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q is not defined", intent.RouteID), 3)
	}
	target, ok := cfg.Targets[route.Dispatch.Target]
	if !ok {
		return planErr(stderr, command, "config_invalid", "configuration", fmt.Sprintf("target %q is not defined", route.Dispatch.Target), 3)
	}
	sink, err := resolveSink(cfg, target, route)
	if err != nil {
		return writeSinkError(stderr, command, err)
	}
	// The projection must read the target that accepted the dispatch —
	// both its identity and its recorded scope; a configuration change
	// since acceptance must not redirect it.
	if sink.ID() != intent.TargetID {
		writeError(stderr, command, "config_invalid", "configuration",
			fmt.Sprintf("dispatch %s was accepted by target %q but the route now resolves to %q; restore the accepting target configuration before refreshing", dispatchID, intent.TargetID, sink.ID()))
		return 3
	}
	if scope := targetBoard(cfg, intent.RouteID); intent.TargetScope != "" && intent.TargetScope != scope {
		writeError(stderr, command, "config_invalid", "configuration",
			fmt.Sprintf("dispatch %s was accepted against target scope %q but the route now resolves to scope %q; restore the accepting board before refreshing", dispatchID, intent.TargetScope, scope))
		return 3
	}
	warnings := []string{}
	if intent.TargetScope == "" {
		warnings = append(warnings, "dispatch "+dispatchID+" carries no recorded target scope (pre-v3 intent); its board identity cannot be verified")
	}
	service := &receipts.Service{Store: store, Sink: sink, Now: time.Now}
	result, err := service.Refresh(context.Background(), dispatchID)
	if err != nil {
		var noReference *receipts.NoReferenceError
		var persist *receipts.PersistError
		switch {
		case errors.As(err, &noReference):
			writeError(stderr, command, "transition_invalid", "conflict", err.Error())
			return 14
		case errors.Is(err, ports.ErrCapabilityUnsupported):
			writeError(stderr, command, "lookup_unsupported", "configuration", err.Error())
			return 3
		case errors.Is(err, ports.ErrIntentNotFound):
			return intentErr(stderr, command, err)
		case errors.As(err, &persist):
			// A local persistence failure is storage-class, never a
			// target failure.
			writeError(stderr, command, "sqlite_query_failed", "storage", persist.Error())
			return 20
		default:
			writeError(stderr, command, "target_definite_unavailable", "target_unavailable", err.Error())
			return 11
		}
	}
	return writeEnvelopeWithWarnings(stdout, command, result, warnings)
}

// targetBoard resolves the configured durable target scope for one
// route: the kanban board slug or the webhook endpoint.
func targetBoard(cfg *config.Config, routeID string) string {
	route, ok := cfg.Routes[routeID]
	if !ok {
		return ""
	}
	if target, ok := cfg.Targets[route.Dispatch.Target]; ok {
		return targetScope(target)
	}
	return ""
}

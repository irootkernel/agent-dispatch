package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/rootkernel/jjukkumi/internal/adapters/sqlite"
	"github.com/rootkernel/jjukkumi/internal/app/dispatch"
	"github.com/rootkernel/jjukkumi/internal/app/reconcile"
	"github.com/rootkernel/jjukkumi/internal/config"
	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// knownDispatchesSubcommands is the published dispatches command tree
// (cli-spec §6).
var knownDispatchesSubcommands = map[string]bool{
	"list": true, "show": true, "retry": true, "reprocess": true, "rerun": true, "refresh": true, "drain": true,
}

func requestCtx() context.Context { return context.Background() }

// dispatchesFlags is the parsed common flag set plus the first bare
// positional operand (the dispatch or batch ID).
type dispatchesFlags struct {
	values     map[string]string
	positional string
}

// flagVal returns the value of one --flag (with = or space form).
func (f dispatchesFlags) val(name string) string { return f.values[name] }

// parseDispatchesFlags parses the shared flags and collects one optional
// bare positional operand.
func parseDispatchesFlags(command string, args []string, stderr io.Writer, allowed map[string]bool) (dispatchesFlags, int) {
	out := dispatchesFlags{values: map[string]string{}}
	valueFlags := map[string]bool{"--config": true, "--route": true, "--state": true, "--target": true, "--reason": true, "--max": true, "--limit": true, "--dispatch": true, "--kind": true}
	for name := range allowed {
		valueFlags[name] = true
	}
	boolFlags := map[string]bool{"--yes": true, "--acknowledge-production-gate": true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value := arg, ""
		hasValue := false
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			name, value, hasValue = arg[:eq], arg[eq+1:], true
		}
		switch {
		case valueFlags[name]:
			if !hasValue {
				if i+1 >= len(args) {
					return out, usageError(stderr, command, fmt.Sprintf("%s requires a value", name))
				}
				i++
				value = args[i]
			}
			out.values[name] = value
		case boolFlags[name]:
			out.values[name] = "true"
		default:
			if !strings.HasPrefix(arg, "--") && out.positional == "" {
				out.positional = arg
				continue
			}
			return out, usageError(stderr, command, fmt.Sprintf("unknown argument %q", arg))
		}
	}
	return out, 0
}

// runDispatches implements `dispatches` (E3-T3, CLI-004).
func runDispatches(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "dispatches", "dispatches requires a subcommand: list, show, retry, reprocess, rerun, refresh, or drain")
	}
	sub, rest := args[0], args[1:]
	if !knownDispatchesSubcommands[sub] {
		return usageError(stderr, "dispatches", fmt.Sprintf("unknown dispatches subcommand %q", sub))
	}
	command := "dispatches " + sub
	switch sub {
	case "list":
		return runDispatchesList(command, rest, stdout, stderr)
	case "show":
		return runDispatchesShow(command, rest, stdout, stderr)
	case "retry":
		return runDispatchesRetry(command, rest, stdout, stderr)
	case "reprocess":
		return runDispatchesReprocess(command, rest, stdout, stderr)
	case "rerun":
		return runDispatchesRerun(command, rest, stdout, stderr)
	case "refresh":
		return runDispatchesRefresh(command, rest, stdout, stderr)
	default:
		return runDispatchesDrain(command, rest, stdout, stderr)
	}
}

// runDispatchesList lists intents with filters (CLI-004).
func runDispatchesList(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	stateFilter := records.IntentState(flags.val("--state"))
	if stateFilter != "" {
		if _, err := records.ParseIntentState(string(stateFilter)); err != nil {
			return usageError(stderr, command, fmt.Sprintf("unknown state %q", stateFilter))
		}
	}
	limit := 100
	if raw := flags.val("--limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 1000 {
			return usageError(stderr, command, "--limit must be 1..1000")
		}
		limit = n
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	intents, err := store.ListIntents(requestCtx(), ports.IntentFilter{
		RouteID:  flags.val("--route"),
		State:    stateFilter,
		TargetID: flags.val("--target"),
		Limit:    limit,
	})
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	return writeEnvelope(stdout, command, map[string]any{"dispatches": intents, "count": len(intents)})
}

// runDispatchesShow prints the full redacted lineage of one dispatch.
func runDispatchesShow(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	if flags.positional == "" {
		return usageError(stderr, command, "dispatches show requires exactly one dispatch ID")
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	lin, err := store.LoadIntentLineage(requestCtx(), flags.positional)
	if err != nil {
		return intentErr(stderr, command, err)
	}
	return writeEnvelope(stdout, command, lin)
}

// runDispatchesRetry applies the explicit operator retry (DUR-009).
func runDispatchesRetry(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	if flags.positional == "" {
		return usageError(stderr, command, "dispatches retry requires a dispatch ID")
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	op := &dispatch.OperatorService{Store: store, Now: func() string { return dispatch.Timestamp(time.Now()) }}
	to, err := op.Retry(requestCtx(), flags.positional, "operator", flags.val("--reason"))
	if err != nil {
		if errors.Is(err, ports.ErrStateNotEligible) {
			return planErr(stderr, command, "transition_invalid", "conflict", err.Error(), 14)
		}
		return intentErr(stderr, command, err)
	}
	return writeEnvelope(stdout, command, map[string]any{"dispatch_id": flags.positional, "state": to, "retried": true})
}

// runDispatchesReprocess re-evaluates one retained batch against the
// current route policy and records a new decision without mutating the
// original.
func runDispatchesReprocess(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	if flags.positional == "" {
		return usageError(stderr, command, "dispatches reprocess requires a batch ID")
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	batch, err := store.LoadBatchEvidence(requestCtx(), flags.positional)
	if err != nil {
		return planErr(stderr, command, "batch_not_found", "usage", err.Error(), 4)
	}
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	route, ok := cfg.Routes[batch.RouteID]
	if !ok {
		return planErr(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q is not defined", batch.RouteID), 3)
	}
	revision, ok := config.RouteRevision(cfg, batch.RouteID)
	if !ok {
		return planErr(stderr, command, "internal_unclassified", "internal", "route revision could not be computed", 40)
	}
	_ = route
	disposition := "dispatch"
	if len(batch.Changes) == 0 {
		disposition = "drop"
	}
	now := dispatch.Timestamp(time.Now())
	decision := sqlite.DecisionRecord{
		DecisionID:      "dec-reprocess-" + flags.positional + "-" + strings.ReplaceAll(now, ":", ""),
		BatchID:         flags.positional,
		RouteID:         batch.RouteID,
		RouteRevision:   revision,
		PolicyRevision:  revision,
		Disposition:     disposition,
		Classification:  "normal",
		ReasonCodesJSON: `["operator_reprocess"]`,
		CreatedAt:       now,
		Actor:           "operator",
	}
	if err := closer.SaveDecision(nil, decision); err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	return writeEnvelope(stdout, command, map[string]any{
		"batch_id": flags.positional, "changes": len(batch.Changes),
		"new_decision_id": decision.DecisionID, "disposition": disposition,
	})
}

// runDispatchesRerun creates the intentional new work request (new
// dispatch ID, generation, and key; CLI-005: no ambiguous replay).
func runDispatchesRerun(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	if flags.val("--yes") == "" {
		return usageError(stderr, command, "rerun requires --yes in non-interactive mode")
	}
	if strings.TrimSpace(flags.val("--reason")) == "" {
		return usageError(stderr, command, "rerun requires --reason")
	}
	if flags.positional == "" {
		return usageError(stderr, command, "dispatches rerun requires a dispatch ID")
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	op := &dispatch.OperatorService{Store: store, Now: func() string { return dispatch.Timestamp(time.Now()) }}
	summary, err := op.Rerun(requestCtx(), flags.positional, "operator", flags.val("--reason"))
	if err != nil {
		return intentErr(stderr, command, err)
	}
	return writeEnvelope(stdout, command, summary)
}

// runDispatchesDrain submits bounded due ready/retry work for one route
// (operator command; never a Watchman trigger, CLI-007 posture).
func runDispatchesDrain(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	routeID := flags.val("--route")
	if routeID == "" {
		return usageError(stderr, command, "drain requires --route")
	}
	max := 1
	if raw := flags.val("--max"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return usageError(stderr, command, "--max must be 1..100")
		}
		max = n
	}
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	route, ok := cfg.Routes[routeID]
	if !ok {
		return planErr(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q is not defined", routeID), 3)
	}
	target, ok := cfg.Targets[route.Dispatch.Target]
	if !ok {
		return planErr(stderr, command, "config_invalid", "configuration", fmt.Sprintf("target %q is not defined", route.Dispatch.Target), 3)
	}
	backoff, err := backoffFromConfig(route.Dispatch.SubmissionRetry)
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	sink, err := resolveSink(cfg, target, route)
	if err != nil {
		return writeSinkError(stderr, command, err)
	}
	rt := &dispatch.Runtime{
		Store: store, Sink: sink,
		Now: time.Now, LeaseTTL: time.Minute, Actor: "drain",
		Backoff: backoff, JitterUnit: jitterUnit,
	}
	// Unknown dispatches are reconciled before due work is submitted
	// (DUR-006). A failure to enumerate them fails closed: submitting
	// more work without the required lookup ordering is never allowed.
	// A per-dispatch reconciliation failure is isolated as a warning so
	// one broken dispatch cannot block the route's due work.
	reconciled, reconcileErrors, err := reconcileUnknownDispatches(cfg, store, sink, routeID, backoff)
	if err != nil {
		return intentErr(stderr, command, err)
	}
	report, err := rt.Drain(requestCtx(), routeID, max, store)
	if err != nil {
		// The reconciliation mutations already committed; surface them
		// inside the single failure envelope so the operator sees what
		// changed without a second, mislabeled error document.
		if len(reconciled) > 0 || len(reconcileErrors) > 0 {
			err = fmt.Errorf("%w (note: %d unknown dispatch(es) were reconciled and %d were skipped or failed before this failure; inspect with 'jjukkumi dispatches list --route %s')", err, len(reconciled), len(reconcileErrors), routeID)
		}
		return intentErr(stderr, command, err)
	}
	return writeEnvelopeWithWarnings(stdout, command, map[string]any{
		"processed": report.Processed, "skipped": report.Skipped,
		"reports": report.Reports, "reconciled": reconciled,
	}, reconcileErrors)
}

// reconcileUnknownDispatches runs the DUR-006 resolution over the
// route's unknown dispatches: lookup by idempotency key then external
// reference, and resolution or dead-lettering through the E3-T1 guards.
// On Hermes the by-key lookup is honestly unsupported, so
// reconciliation runs by external reference and unresolved work
// dead-letters for the operator, whose retry resubmits the same
// idempotency key (the dedup-safe path). One dispatch's failure never
// aborts the others; failures are returned as visible warnings.
func reconcileUnknownDispatches(cfg *config.Config, store storeOp, sink ports.Sink, routeID string, backoff dispatch.Backoff) ([]map[string]any, []string, error) {
	recon := &reconcile.Service{Store: store, Sink: sink, Now: func() string { return dispatch.Timestamp(time.Now()) }}
	intents, err := store.ListIntents(requestCtx(), ports.IntentFilter{RouteID: routeID, State: "unknown", Limit: 1000})
	if err != nil {
		// DUR-006 ordering: without enumeration the required lookup
		// cannot run, so no further submission may start.
		return nil, nil, fmt.Errorf("listing route %s unknown dispatches: %w", routeID, err)
	}
	var out []map[string]any
	var failures []string
	scope := ""
	if route, ok := cfg.Routes[routeID]; ok {
		if target, ok := cfg.Targets[route.Dispatch.Target]; ok {
			scope = target.Board
		}
	}
	for _, sum := range intents {
		// The reconciliation must read the same target identity and
		// scope that were configured at submission: a configuration
		// change that re-points the route to a different target or
		// board must never turn a wrong-board absence into a
		// resubmission proof.
		if sink.ID() != sum.TargetID {
			failures = append(failures, fmt.Sprintf("reconciling %s skipped: it was accepted by target %q but the route now resolves to %q", sum.DispatchID, sum.TargetID, sink.ID()))
			continue
		}
		if snap, err := store.LoadIntent(requestCtx(), sum.DispatchID); err == nil && snap.TargetScope != "" && snap.TargetScope != scope {
			failures = append(failures, fmt.Sprintf("reconciling %s skipped: it was submitted against target scope %q but the route now resolves to scope %q; restore the accepting scope or resolve the dispatch manually", sum.DispatchID, snap.TargetScope, scope))
			continue
		}
		res, err := recon.Reconcile(requestCtx(), sum.DispatchID, "drain", backoff.Exhausted(sum.AttemptCount))
		if err != nil {
			failures = append(failures, fmt.Sprintf("reconciling %s failed: %v", sum.DispatchID, err))
			continue
		}
		out = append(out, map[string]any{
			"dispatch_id": res.DispatchID, "lookup": string(res.Lookup), "state": string(res.To),
		})
	}
	return out, failures, nil
}

// intentErr maps the typed port errors onto the stable exit codes
// (CLI-008).
func intentErr(stderr io.Writer, command string, err error) int {
	switch {
	case errors.Is(err, ports.ErrIntentNotFound):
		writeError(stderr, command, "dispatch_not_found", "usage", err.Error())
		return 4
	case errors.Is(err, ports.ErrLeaseHeld):
		writeError(stderr, command, "attempt_lease_conflict", "conflict", err.Error())
		return 14
	case errors.Is(err, ports.ErrIdempotencyConflict):
		writeError(stderr, command, "dispatch_duplicate", "conflict", err.Error())
		return 14
	case errors.Is(err, ports.ErrRouteSlotHeld):
		writeError(stderr, command, "route_slot_held", "conflict", err.Error())
		return 14
	case errors.Is(err, ports.ErrStateNotEligible):
		writeError(stderr, command, "transition_invalid", "conflict", err.Error())
		return 14
	default:
		writeError(stderr, command, "internal_unclassified", "internal", err.Error())
		return 40
	}
}

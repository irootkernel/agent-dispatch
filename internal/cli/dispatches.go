package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/app/ingest"
	"github.com/irootkernel/agent-dispatch/internal/app/reconcile"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/ids"
	"github.com/irootkernel/agent-dispatch/internal/domain/policy"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// knownDispatchesSubcommands is the published dispatches command tree
// (cli-spec §6).
var knownDispatchesSubcommands = map[string]bool{
	"list": true, "show": true, "retry": true, "reprocess": true, "rerun": true, "refresh": true, "drain": true,
	"discard": true,
}

// requestCtx carries the global --timeout bound when one was given
// (cli-spec §1): a bounded command deadline for store operations.
var globalRequestTimeout time.Duration

func requestCtx() context.Context {
	if globalRequestTimeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), globalRequestTimeout)
		// The deadline covers the command's store operations; the
		// process exits with the command, so the cancel is a safety
		// valve for the goroutine leak checker rather than control
		// flow.
		_ = cancel
		return ctx
	}
	return context.Background()
}

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
	valueFlags := map[string]bool{"--config": true, "--route": true, "--state": true, "--target": true, "--reason": true, "--max": true, "--limit": true, "--offset": true, "--age": true, "--external-ref": true, "--causal": true, "--dispatch": true, "--kind": true, "--acknowledge-production-gate": true, "--output": true}
	for name := range allowed {
		valueFlags[name] = true
	}
	boolFlags := map[string]bool{"--yes": true}
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
	if v := out.values["--output"]; v != "" && v != "json" {
		return out, usageError(stderr, command, "--output requires 'json'")
	}
	return out, 0
}

// runDispatches implements `dispatches` (E3-T3, CLI-004).
func runDispatches(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "dispatches", "dispatches requires a subcommand: list, show, retry, reprocess, rerun, discard, refresh, or drain")
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
	case "discard":
		return runDispatchesDiscard(command, rest, stdout, stderr)
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
	offset := 0
	if raw := flags.val("--offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return usageError(stderr, command, "--offset must be >= 0")
		}
		offset = n
	}
	// The age filter takes a Go duration (for example 24h): the listing
	// then carries only intents created strictly before now minus the
	// age (cli-spec §6, E7-T5).
	olderThan := ""
	if raw := flags.val("--age"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return usageError(stderr, command, "--age must be a positive duration like 24h")
		}
		olderThan = time.Now().UTC().Add(-d).Truncate(time.Second).Format(time.RFC3339)
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	intents, err := store.ListIntents(requestCtx(), ports.IntentFilter{
		RouteID:      flags.val("--route"),
		State:        stateFilter,
		TargetID:     flags.val("--target"),
		OlderThan:    olderThan,
		ExternalRef:  flags.val("--external-ref"),
		CausalPrefix: flags.val("--causal"),
		Limit:        limit,
		Offset:       offset,
	})
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	return writeEnvelope(stdout, command, map[string]any{"dispatches": intents, "count": len(intents), "offset": offset})
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
	cfgRerun, cfgErr := config.Load(resolveConfigPath(flags.val("--config")))
	var scopeResolver func(routeID string) string
	if cfgErr == nil {
		rerunCfg := cfgRerun
		scopeResolver = func(routeID string) string {
			if route, ok := rerunCfg.Routes[routeID]; ok {
				if target, ok := rerunCfg.Targets[route.Dispatch.Target]; ok {
					return targetScope(target)
				}
			}
			return ""
		}
	}
	// The rerun carries the active configuration's revision and target
	// identity, never the stored plan's superseded values (E7-T3/H-1
	// round-1 remediation).
	var revisionResolver func(routeID string) (string, bool)
	var targetResolver func(routeID string) (string, string, string, bool)
	if cfgErr == nil {
		rerunCfg := cfgRerun
		revisionResolver = func(routeID string) (string, bool) {
			return config.RouteRevision(rerunCfg, routeID)
		}
		targetResolver = func(routeID string) (string, string, string, bool) {
			route, ok := rerunCfg.Routes[routeID]
			if !ok {
				return "", "", "", false
			}
			target, ok := rerunCfg.Targets[route.Dispatch.Target]
			if !ok {
				return "", "", "", false
			}
			return route.Dispatch.Target, target.Type, targetScope(target), true
		}
	}
	op := &dispatch.OperatorService{
		Store: store, Now: func() string { return dispatch.Timestamp(time.Now()) },
		TargetScopeResolver: scopeResolver, RevisionResolver: revisionResolver, TargetResolver: targetResolver,
	}
	// CLI-008 (E7-T5/M-15): a dead-lettered retry without --reason is a
	// usage defect, not an internal one.
	if strings.TrimSpace(flags.val("--reason")) == "" {
		snap, serr := store.LoadIntent(requestCtx(), flags.positional)
		switch {
		case serr != nil:
			return intentErr(stderr, command, serr)
		case snap.State == records.IntentDeadLettered:
			return usageError(stderr, command, "retrying dead-lettered dispatch "+flags.positional+" requires --reason")
		}
	}
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
		return planErr(stderr, command, "batch_not_found", "input_rejected", err.Error(), 4)
	}
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	if _, ok := cfg.Routes[batch.RouteID]; !ok {
		return planErr(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q is not defined", batch.RouteID), 3)
	}
	revision, ok := config.RouteRevision(cfg, batch.RouteID)
	if !ok {
		return planErr(stderr, command, "internal_unclassified", "internal", "route revision could not be computed", 40)
	}
	// The retained batch is evaluated through the planner itself
	// (E7-T6/M-19 round-1 remediation): the reprocess decision records
	// exactly what the active policy would do, with the planner's own
	// precedence instead of a re-implementation.
	route := cfg.Routes[batch.RouteID]
	resource := cfg.Resources[route.Source.Resource]
	runtime, rtErr := newRouteRuntime(cfg, route, resource)
	if rtErr != nil {
		return planErr(stderr, command, "config_invalid", "configuration", rtErr.Error(), 3)
	}
	target := cfg.Targets[route.Dispatch.Target]
	changes := make([]records.ChangeItem, 0, len(batch.Changes))
	var protected, immutable []string
	for _, c := range batch.Changes {
		op, oerr := records.ParseOperation(c.Operation)
		if oerr != nil {
			return planErr(stderr, command, "internal_unclassified", "internal", oerr.Error(), 40)
		}
		item := records.ChangeItem{
			Path: c.Path, Operation: op, ExistsAfter: c.ExistsAfter, FileType: records.FileType(c.FileType),
			BeforeDigest: records.Digest(c.BeforeDigest), AfterDigest: records.Digest(c.AfterDigest), DigestStatus: records.DigestStatus(c.DigestStatus),
		}
		st, cerr := runtime.engine.Classify(c.Path)
		if cerr != nil {
			return planErr(stderr, command, "internal_unclassified", "internal", cerr.Error(), 40)
		}
		switch st {
		case policy.StatusProtected:
			protected = append(protected, c.Path)
		case policy.StatusImmutable:
			immutable = append(immutable, c.Path)
		}
		changes = append(changes, item)
	}
	plan, perr := dispatch.Evaluate(dispatch.RoutePolicy{
		RouteID:              batch.RouteID,
		RouteRevision:        revision,
		ResourceID:           route.Source.Resource,
		AutomaticThreshold:   route.Batching.AutomaticThreshold,
		HardLimit:            route.Batching.HardLimit,
		MaxManifestBytes:     route.Batching.MaxManifestBytes,
		BulkAction:           route.Policy.BulkAction,
		OverflowAction:       route.Policy.OverflowAction,
		FreshInstanceAction:  route.Policy.FreshInstanceAction,
		RequiredCapabilities: target.RequiredCapabilities,
	}, dispatch.Input{Batch: &ingest.Result{Changes: changes, Protected: protected, Immutable: immutable}})
	if perr != nil {
		return planErr(stderr, command, "internal_unclassified", "internal", perr.Error(), 40)
	}
	disposition := plan.Disposition
	classification := "normal"
	if len(plan.Classification) > 0 {
		classification = plan.Classification[0]
	}
	reasons := append([]string{"operator_reprocess"}, plan.ReasonCodes...)
	sortedReasons := append([]string(nil), reasons...)
	sort.Strings(sortedReasons)
	encoded, _ := json.Marshal(sortedReasons)
	now := dispatch.Timestamp(time.Now())
	decision := sqlite.DecisionRecord{
		DecisionID:      "dec-reprocess-" + flags.positional + "-" + strings.ReplaceAll(now, ":", "") + "-" + ids.RandomSuffix(),
		BatchID:         flags.positional,
		RouteID:         batch.RouteID,
		RouteRevision:   revision,
		PolicyRevision:  revision,
		Disposition:     disposition,
		Classification:  classification,
		ReasonCodesJSON: string(encoded),
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
	cfgRerun, cfgErr := config.Load(resolveConfigPath(flags.val("--config")))
	var scopeResolver func(routeID string) string
	if cfgErr == nil {
		rerunCfg := cfgRerun
		scopeResolver = func(routeID string) string {
			if route, ok := rerunCfg.Routes[routeID]; ok {
				if target, ok := rerunCfg.Targets[route.Dispatch.Target]; ok {
					return targetScope(target)
				}
			}
			return ""
		}
	}
	// The rerun carries the active configuration's revision and target
	// identity, never the stored plan's superseded values (E7-T3/H-1
	// round-1 remediation).
	var revisionResolver func(routeID string) (string, bool)
	var targetResolver func(routeID string) (string, string, string, bool)
	if cfgErr == nil {
		rerunCfg := cfgRerun
		revisionResolver = func(routeID string) (string, bool) {
			return config.RouteRevision(rerunCfg, routeID)
		}
		targetResolver = func(routeID string) (string, string, string, bool) {
			route, ok := rerunCfg.Routes[routeID]
			if !ok {
				return "", "", "", false
			}
			target, ok := rerunCfg.Targets[route.Dispatch.Target]
			if !ok {
				return "", "", "", false
			}
			return route.Dispatch.Target, target.Type, targetScope(target), true
		}
	}
	op := &dispatch.OperatorService{
		Store: store, Now: func() string { return dispatch.Timestamp(time.Now()) },
		TargetScopeResolver: scopeResolver, RevisionResolver: revisionResolver, TargetResolver: targetResolver,
	}
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
		Now: time.Now, LeaseTTL: leaseTTLFor(target.SubmitTimeout), Actor: "drain",
		Backoff: backoff, JitterUnit: jitterUnit,
		Log: opsLogger(stderr, cfg), TraceID: globalTraceID,
		StalenessCheck: stalenessCheckOf(cfg), StaleRebuilder: staleRebuilderOf(store, cfg),
	}
	// Expired submitting leases are recovered before unknown
	// reconciliation so the DUR-006 lookup ordering covers them (DUR-010,
	// E7-T2/B-1): a process that died mid-submit leaves submitting work
	// that only this sweep moves to unknown. A failure to enumerate the
	// expired leases fails closed, exactly like the unknown enumeration.
	// The YAML key half of the two-key gate (E7-T6/M-2): a route whose
	// configuration key is off never submits automatically even when the
	// store activation was previously acknowledged; recovery and
	// reconciliation still run.
	if !route.Enabled {
		recovered, recErr := rt.Recover(requestCtx(), routeID)
		if recErr != nil {
			return intentErr(stderr, command, recErr)
		}
		warnings := []string{fmt.Sprintf("route %q is disabled in configuration (routes.%s.enabled: false); nothing was submitted", routeID, routeID)}
		return writeEnvelopeWithWarnings(stdout, command, map[string]any{
			"processed": 0, "skipped": 0, "recovered": recovered, "reconciled": nil,
		}, warnings)
	}
	recovered, err := rt.Recover(requestCtx(), routeID)
	if err != nil {
		return intentErr(stderr, command, err)
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
		// The recovery and reconciliation mutations already committed; surface them
		// inside the single failure envelope so the operator sees what
		// changed without a second, mislabeled error document.
		if len(recovered) > 0 || len(reconciled) > 0 || len(reconcileErrors) > 0 {
			err = fmt.Errorf("%w (note: %d expired lease(s) were recovered, %d unknown dispatch(es) were reconciled, and %d were skipped or failed before this failure; inspect with 'agent-dispatch dispatches list --route %s')", err, len(recovered), len(reconciled), len(reconcileErrors), routeID)
		}
		return intentErr(stderr, command, err)
	}
	warnings := append([]string{}, reconcileErrors...)
	warnings = append(warnings, report.Warnings...)
	return writeEnvelopeWithWarnings(stdout, command, map[string]any{
		"processed": report.Processed, "skipped": report.Skipped,
		"reports": report.Reports, "reconciled": reconciled, "recovered": recovered,
	}, warnings)
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
			scope = targetScope(target)
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
		snap, loadErr := store.LoadIntent(requestCtx(), sum.DispatchID)
		if loadErr != nil {
			// Failing closed: without the recorded scope the identity
			// proof is unavailable, so the dispatch is never
			// reconciled on trust alone.
			failures = append(failures, fmt.Sprintf("reconciling %s skipped: its durable scope could not be read: %v", sum.DispatchID, loadErr))
			continue
		}
		if snap.TargetScope == "" {
			// Pre-v3 intents carry no recorded scope: reconcile, but
			// surface the weaker identity proof visibly.
			failures = append(failures, fmt.Sprintf("reconciling %s proceeds without a recorded target scope (pre-v3 intent); its board identity cannot be verified", sum.DispatchID))
		} else if snap.TargetScope != scope {
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
		writeError(stderr, command, "dispatch_not_found", "input_rejected", err.Error())
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
	case errors.Is(err, ports.ErrStaleRouteRevision):
		// The acknowledged-revision pause is the documented route-paused
		// conflict, never an internal defect (E8-T3 round-1 F001).
		writeError(stderr, command, "transition_invalid", "conflict", err.Error())
		return 14
	case isStateTransitionError(err):
		// A route/intent guard rejection is a state conflict, never an
		// internal defect (E8-T2, H-9 — the work commands share the arm).
		writeError(stderr, command, "transition_invalid", "conflict", err.Error())
		return 14
	default:
		// Typed classification: store surfaces are storage, never an
		// internal relabel (mirrors reconcileErr and quarantineErr).
		var storeErr *ports.StoreError
		if errors.As(err, &storeErr) {
			writeError(stderr, command, "sqlite_query_failed", "storage", storeErr.Err.Error())
			return 20
		}
		writeError(stderr, command, "internal_unclassified", "internal", err.Error())
		return 40
	}
}

// runDispatchesDiscard closes one dead-lettered dispatch as superseded
// through the declared edge, releasing the route slot (E7-T7/M-7). The
// record and its audit history remain inspectable.
func runDispatchesDiscard(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	if flags.positional == "" {
		return usageError(stderr, command, "dispatches discard requires a dispatch ID")
	}
	if strings.TrimSpace(flags.val("--reason")) == "" {
		return usageError(stderr, command, "dispatches discard requires --reason")
	}
	closerStore, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	_ = closerStore
	outcome, err := closer.CloseDeadLetter(requestCtx(), flags.positional, "operator", flags.val("--reason"), dispatch.Timestamp(time.Now()))
	if err != nil {
		if errors.Is(err, ports.ErrStateNotEligible) {
			return planErr(stderr, command, "transition_invalid", "conflict", err.Error(), 14)
		}
		return intentErr(stderr, command, err)
	}
	return writeEnvelope(stdout, command, map[string]any{
		"dispatch_id": flags.positional, "state": "superseded",
		"slot_released": outcome.SlotReleased, "route_to_idle": outcome.RouteToIDLE,
		"dirty_generation_retained": outcome.DirtyRetained,
	})
}

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeskanban"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/app/ingest"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/ids"
	"github.com/irootkernel/agent-dispatch/internal/domain/policy"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// planOptions carries the shared planning flags.
type planOptions struct {
	routeID    string
	configPath string
	jsonOutput bool
}

func parsePlanFlags(command string, args []string, stdout, stderr io.Writer) (*planOptions, int) {
	opts := &planOptions{jsonOutput: true}
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
		case "--input":
			if i+1 >= len(args) || args[i+1] != "watchman" {
				return nil, usageError(stderr, command, "--input requires 'watchman' in this build")
			}
			i++
		case "--no-submit":
			// Consumed by the caller (runDispatch): the pipeline plans
			// and persists; the submit phase is skipped upstream.
		case "--output=json":
			opts.jsonOutput = true
		case "--output=human":
			return nil, usageError(stderr, command, fmt.Sprintf("%s prints the structured dispatch plan; only --output json is supported (cli-spec §3)", command))
		case "--output", "-o":
			if i+1 >= len(args) || args[i+1] != "json" {
				return nil, usageError(stderr, command, "--output requires 'json' for this command")
			}
			i++
			opts.jsonOutput = true
		default:
			return nil, usageError(stderr, command, fmt.Sprintf("unknown argument %q", args[i]))
		}
	}
	if opts.routeID == "" {
		return nil, usageError(stderr, command, "--route is required")
	}
	return opts, 0
}

// resolveConfigPath applies the shared configuration-path precedence
// (cli-spec §1): explicit path, then AGENT_DISPATCH_CONFIG, then the platform
// default.
func resolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("AGENT_DISPATCH_CONFIG"); env != "" {
		return env
	}
	return platformpaths.DefaultConfigPath()
}

// DefaultMaxHashFileBytes is the fallback hash bound when the operator
// sets no limits.max_hash_file_bytes, matching the documented
// configuration-spec example envelope (16 MiB).
const DefaultMaxHashFileBytes int64 = 16 << 20

// DefaultMaxManifestBytes is the fallback manifest bound (the example
// configuration's value) used where no route batching context exists,
// such as the doctor target probe (E7-T9/M-23).
const DefaultMaxManifestBytes int64 = 262144

// planArtifacts is one evaluated invocation: the loaded configuration,
// the trusted environment binding, the normalized batch, and the plan.
type planArtifacts struct {
	hints    *ports.TaskExecutionHints
	opts     *planOptions
	cfg      *config.Config
	route    config.Route
	resource config.Resource
	dest     config.Destination
	resolved config.ResolvedTarget
	revision string
	env      watchman.Env
	input    watchman.Input
	batch    *ingest.Result
	plan     *dispatch.Plan
}

// planPipeline runs the shared read-only planning pipeline: parse stdin,
// validate the trusted environment binding, normalize the batch, and
// evaluate the pure policy planner (POL-007, POL-008, SEC-010).
// factsSource supplies the durable prior-digest lookup for the batch
// build (E7-T3/H-2). The durable dispatch path wires the stored
// path-facts snapshot; plan and dry-run keep the conservative no-history
// fallback (CLI-003: they open no database).
type factsSource func(configPath, resourceID string) (ingest.PathFacts, error)

// bindingSource supplies the persisted managed Watchman binding for the
// ancestor-root validation (E10-T2, SRC-011). The durable dispatch path
// wires the stored record; plan and dry-run keep the conservative
// exact-root-only validation (CLI-003: they open no database).
type bindingSource func(configPath, routeID string) (*watchman.Binding, error)

func planPipeline(command string, args []string, stderr io.Writer, facts factsSource, bindings bindingSource) (*planArtifacts, int) {
	opts, code := parsePlanFlags(command, args, nil, stderr)
	if code != 0 {
		return nil, code
	}
	opts.configPath = resolveConfigPath(opts.configPath)
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return nil, planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	route, ok := cfg.Routes[opts.routeID]
	if !ok {
		return nil, planErr(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q is not defined", opts.routeID), 3)
	}
	resource, ok := cfg.Resources[route.Source.Resource]
	if !ok {
		return nil, planErr(stderr, command, "config_invalid", "configuration", fmt.Sprintf("resource %q is not defined", route.Source.Resource), 3)
	}
	dest, resolved, rerr := resolveRouteTarget(cfg, opts.routeID)
	if rerr != nil {
		return nil, planErr(stderr, command, "config_invalid", "configuration", rerr.Error(), 3)
	}
	required := hermeskanban.UnconditionalCapabilities
	if resolved.Webhook != nil {
		required = resolved.Webhook.RequiredCapabilities
	}
	revision, ok := config.RouteRevision(cfg, opts.routeID)
	if !ok {
		return nil, planErr(stderr, command, "internal_unclassified", "internal", "route revision could not be computed", 40)
	}

	env, err := watchman.ParseEnv(os.LookupEnv)
	if err != nil {
		return nil, planErr(stderr, command, "source_missing_required_metadata", "input_rejected", err.Error(), 4)
	}
	var stored *watchman.Binding
	if bindings != nil && env.HasRelative {
		binding, err := bindings(opts.configPath, opts.routeID)
		if err != nil {
			return nil, planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
		}
		stored = binding
	}
	if err := watchman.ValidateBinding(env, route.Source.TriggerName, resource.Root, stored); err != nil {
		return nil, planErr(stderr, command, "source_binding_mismatch", "input_rejected", err.Error(), 4)
	}

	maxStdin := int64(0)
	if cfg.Limits.MaxStdinBytes != nil {
		maxStdin = *cfg.Limits.MaxStdinBytes
	} else {
		maxStdin = watchman.DefaultMaxStdinBytes
	}
	input, err := watchman.ReadInput(os.Stdin, env, maxStdin)
	if err != nil {
		if errors.Is(err, watchman.ErrStdinTooLarge) {
			return nil, planErr(stderr, command, "source_input_too_large", "input_rejected", err.Error(), 4)
		}
		return nil, planErr(stderr, command, "source_malformed_json", "input_rejected", err.Error(), 4)
	}

	runtime, err := newRouteRuntime(cfg, route, resource)
	if err != nil {
		return nil, planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	engine, resolver, maxHash := runtime.engine, runtime.resolver, runtime.maxHash

	factsFor := ingest.PathFacts(ingest.NoFacts{})
	if facts != nil {
		loaded, err := facts(opts.configPath, route.Source.Resource)
		if err != nil {
			return nil, planErr(stderr, command, "sqlite_open_failed", "storage", err.Error(), 20)
		}
		if loaded != nil {
			factsFor = loaded
		}
	}
	batch, err := ingest.BuildBatch(input.Entries, engine, resolver, factsFor, env.Flags(), route.Source.Resource, ingest.Options{MaxHashBytes: maxHash, FileScope: resource.FileScope})
	if err != nil {
		if errors.Is(err, ingest.ErrUnsafePath) {
			return nil, planErr(stderr, command, "source_unsafe_path", "security", err.Error(), 30)
		}
		return nil, planErr(stderr, command, "internal_unclassified", "internal", err.Error(), 40)
	}

	plan, err := dispatch.Evaluate(dispatch.RoutePolicy{
		RouteID:              opts.routeID,
		RouteRevision:        revision,
		ResourceID:           route.Source.Resource,
		AutomaticThreshold:   route.Batching.AutomaticThreshold,
		HardLimit:            route.Batching.HardLimit,
		MaxManifestBytes:     route.Batching.MaxManifestBytes,
		BulkAction:           route.Policy.BulkAction,
		OverflowAction:       route.Policy.OverflowAction,
		FreshInstanceAction:  route.Policy.FreshInstanceAction,
		RequiredCapabilities: required,
	}, dispatch.Input{Batch: batch, Flags: env.Flags()})
	if err != nil {
		return nil, planErr(stderr, command, "internal_unclassified", "internal", err.Error(), 40)
	}

	// POL-008/SEC-010 live at the submit boundary (E7-T3/H-1): every
	// submit path revalidates the stored plan against the active
	// configuration and supersedes-and-rebuilds stale work, so this
	// plan-time self-comparison carried no authority and was removed.
	hints, err := hintsOf(dest)
	if err != nil {
		return nil, planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	return &planArtifacts{
		opts: opts, cfg: cfg, route: route, resource: resource,
		dest: dest, resolved: resolved, revision: revision,
		env: env, input: input, batch: batch, plan: plan, hints: hints,
	}, 0
}

// runPlan plans one Watchman invocation end to end with no SQLite
// mutation and no target call.
func runPlan(command string, args []string, stdout, stderr io.Writer) int {
	artifacts, code := planPipeline(command, args, stderr, nil, nil)
	if code != 0 {
		return code
	}
	_ = artifacts.opts.jsonOutput // machine output only (CLI-001)
	// The plan and dry-run surfaces open no database, so their batch is
	// built without the durable path-facts snapshot: unchanged-content
	// suppression is unreachable here and every modify is reported as
	// changed. The gap is stated instead of overstated (E8-T5, M-7).
	return writeEnvelopeWithWarnings(stdout, command, artifacts.plan, []string{
		"planned without the durable path-facts snapshot: unchanged-content suppression and merge_pending detection are unavailable on this surface; the durable dispatch path reports them",
	})
}

// runDispatch implements `dispatch` (cli-spec §5). `--dry-run` stays
// side-effect-free; the default path persists the full
// observation-to-intent lineage through the E3 durable core (DUR-002)
// and then attempts submission. Until the Hermes adapters arrive with
// E4, the submit phase reports the documented target-unavailable error
// while the persisted intent stays ready and inspectable; `--no-submit`
// persists and leaves the intent ready without a submit attempt.
func runDispatch(args []string, stdout, stderr io.Writer) int {
	dryRun := false
	noSubmit := false
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dryRun = true
		case "--no-submit":
			noSubmit = true
		default:
			rest = append(rest, args[i])
		}
	}
	if dryRun {
		return runPlan("dispatch", rest, stdout, stderr)
	}
	command := "dispatch"
	if noSubmit {
		rest = append(rest, "--no-submit")
	}
	// The durable path plans against the stored path-facts snapshot so
	// unchanged and metadata-only modifies suppress with audit
	// (PTH-006/PTH-007, E7-T3/H-2).
	artifacts, code := planPipeline(command, rest, stderr, durableFacts, storedBinding)
	if code != 0 {
		return code
	}
	// Structural dispositions never reach the coordinator's dispatch
	// path: quarantine holds durably (PTH-008), reconcile marks the
	// single pending generation (SRC-005), and drop persists only the
	// evidence (POL-006).
	switch artifacts.plan.Disposition {
	case "quarantine":
		return persistQuarantine(command, artifacts, stdout, stderr)
	case "reconcile":
		return persistReconcileArrival(command, artifacts, stdout, stderr)
	case "drop":
		return persistDrop(command, artifacts, stdout, stderr)
	case "dispatch", "merge_pending":
		// The coordinator owns these dispositions.
	default:
		// The disposition set is closed (POL-006): an unknown value
		// fails closed instead of silently dispatching (E5 audit).
		return planErr(stderr, command, "internal_unclassified", "internal",
			fmt.Sprintf("plan carried the unknown disposition %q", artifacts.plan.Disposition), 40)
	}
	outcome, exit := persistThroughCoordinator(command, artifacts, stderr)
	if exit != 0 {
		return exit
	}
	if outcome.merged {
		// Another dispatch holds every selected lane: the burst merged
		// into the lanes' durable dirty generations (CON-002, CON-008,
		// FBK-001). The envelope carries the merged lanes; stderr stays
		// quiet on this all-merged path by the legacy clean-stderr
		// contract the E8/G4 suites pin (the mixed activate+merge path
		// below prints the per-lane note).
		return writeEnvelope(stdout, command, map[string]any{
			"route_id": artifacts.opts.routeID, "disposition": "merge_pending",
			"dirty_generation": outcome.dirty, "submitted": false,
			"merged_lanes": laneResultsEnvelope(outcome.children),
			"failed_lanes": laneFailuresEnvelope(outcome.failures),
		})
	}
	// The fan-out envelope lists every selected destination's dispatch
	// (E12-T2, FAN-002): dispatch_id stays the canonically-first lane's
	// child, which the submit phase submits; the siblings stay ready for
	// the drain.
	fanout := laneResultsEnvelope(outcome.children)
	failedLanes := laneFailuresEnvelope(outcome.failures)
	// A lane that MERGED while another activated is never silent (E12 epic
	// validation): the merged lanes ride the envelope beside the
	// activated fan-out — the occurrence reached some lanes and merged
	// into the others' dirty generations in the same burst.
	mergedLanes := laneResultsEnvelope(outcome.mergedChildren)
	for _, failure := range outcome.failures {
		// Never silent: a lane that failed beside a successful sibling is
		// operator-visible on stderr too (CON-007 isolation cuts both
		// ways — the failure must be seen).
		fmt.Fprintf(stderr, "warning: destination lane %s failed: %s\n", failure.DestinationID, failure.Error)
	}
	for _, merged := range outcome.mergedChildren {
		fmt.Fprintf(stderr, "note: destination lane %s merged into its dirty generation (generation %d); a lane already held the slot\n", merged.DestinationID, merged.DirtyGeneration)
	}
	if noSubmit {
		outcome.Close()
		return writeEnvelope(stdout, command, map[string]any{
			"route_id": artifacts.opts.routeID, "dispatch_id": outcome.dispatchID,
			"state": "ready", "submitted": false, "fanout": fanout, "failed_lanes": failedLanes, "merged_lanes": mergedLanes,
		})
	}
	// The YAML key half of the two-key gate (E7-T6/M-2): a configuration
	// disabled route persists its arrival but never submits it.
	if !artifacts.route.Enabled {
		outcome.Close()
		return writeEnvelopeWithWarnings(stdout, command, map[string]any{
			"route_id": artifacts.opts.routeID, "dispatch_id": outcome.dispatchID,
			"state": "ready", "submitted": false, "fanout": fanout, "failed_lanes": failedLanes, "merged_lanes": mergedLanes,
		}, []string{fmt.Sprintf("route %q is disabled in configuration; the intent stays ready until the route is enabled", artifacts.opts.routeID)})
	}
	// The submit phase needs the E4 sink adapter; the intent is durable
	// and ready, and no automatic target fallback exists (DUR-008).
	sink, err := resolveSink(artifacts.cfg, artifacts.opts.routeID, opsLogger(stderr, artifacts.cfg))
	if err != nil {
		outcome.Close()
		return writeSinkError(stderr, command, err)
	}
	backoff, err := backoffFromConfig(artifacts.route.SubmissionRetry)
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	rt := newSubmitRuntime(outcome.store, sink, artifacts.cfg, submitTimeoutOf(artifacts.resolved), backoff, "dispatch", stderr)
	// The head-of-entry sweep already ran before the arrival was
	// evaluated; this second pass covers only the race where the lease
	// expired between that sweep and this submit (E8-T2/H-6).
	if _, err := rt.Recover(requestCtx(), artifacts.opts.routeID); err != nil {
		outcome.Close()
		return intentErr(stderr, command, err)
	}
	report, err := rt.SubmitOnce(requestCtx(), outcome.dispatchID, "agent-dispatch-dispatch")
	outcome.Close()
	if err != nil {
		return intentErr(stderr, command, err)
	}
	return writeEnvelope(stdout, command, map[string]any{
		"route_id": artifacts.opts.routeID, "dispatch_id": outcome.dispatchID,
		"state": string(report.To), "reason": string(report.Reason), "submitted": true,
		"fanout": laneResultsEnvelope(outcome.children), "failed_lanes": laneFailuresEnvelope(outcome.failures),
		"merged_lanes": mergedLanes,
	})
}

// laneFailuresEnvelope renders the per-destination fan-out failures for
// the dispatch envelope (E12-T2): bounded, redacted error text with the
// durable dispatch ID of a failed activation (an empty list renders as an
// empty array, never null).
func laneFailuresEnvelope(failures []dispatch.FanoutLaneFailure) []map[string]any {
	out := make([]map[string]any, 0, len(failures))
	for _, failure := range failures {
		out = append(out, map[string]any{
			"destination_id": failure.DestinationID,
			"dispatch_id":    failure.DispatchID,
			"error":          failure.Error,
		})
	}
	return out
}

// laneResultsEnvelope renders the per-destination fan-out results for the
// dispatch envelope (E12-T2): one {destination_id, dispatch_id} row per
// selected destination in selection order.
func laneResultsEnvelope(children []dispatch.FanoutLaneResult) []map[string]any {
	out := make([]map[string]any, 0, len(children))
	for _, child := range children {
		out = append(out, map[string]any{
			"destination_id":   child.DestinationID,
			"dispatch_id":      child.DispatchID,
			"dirty_generation": child.DirtyGeneration,
		})
	}
	return out
}

// persistOutcome reports what the coordinator did with one arrival. The
// store stays open for the submit phase and must be closed by the
// caller.
type persistOutcome struct {
	merged     bool
	dirty      int
	dispatchID string
	// children lists the per-destination dispatch IDs of the fan-out
	// (E12-T2, FAN-002): one entry per selected destination, destination
	// order.
	children []dispatch.FanoutLaneResult
	// failures lists the per-destination lanes whose arrival failed while
	// a sibling succeeded (E12-T2, CON-007): bounded, redacted error text
	// with the durable dispatch ID of a failed activation.
	failures []dispatch.FanoutLaneFailure
	// mergedChildren lists the per-destination lanes that merged into
	// their dirty generation while a sibling activated (E12 epic
	// validation): the mixed activate+merge envelope reports them beside
	// the fan-out.
	mergedChildren []dispatch.FanoutLaneResult
	store          storeOp
	closer         *sqlite.Store
}

// Close releases the outcome's store handle when the submit phase is
// done with it.
func (o persistOutcome) Close() {
	if o.closer != nil {
		o.closer.Close()
	}
}

// persistThroughCoordinator routes the planned fan-out through the E3-T4
// coordinator (E12-T2): each selected destination's lineage activates or
// merges on its own lane — one sibling's held slot merges into that
// lane's dirty generation while the others activate (CON-007, CON-008) —
// and the single slot winner of the canonically-first lane remains the
// submit phase's dispatch.
func persistThroughCoordinator(command string, artifacts *planArtifacts, stderr io.Writer) (persistOutcome, int) {
	store, closer, exit := openOperatorStore(command, artifacts.opts.configPath, stderr)
	if exit != 0 {
		return persistOutcome{}, exit
	}
	// Head-of-entry recovery (DUR-010, E8-T2/H-6): expired submitting
	// leases are recovered — and their unknowns reconciled — before this
	// arrival is evaluated, so a process that died mid-submit heals on
	// the next trigger and the freed lane can accept the arriving work
	// instead of silently merging into a wedged generation. A failure to
	// enumerate fails closed exactly like the drain.
	headRecover := dispatch.Runtime{Store: store, Now: time.Now, Actor: "dispatch-cli"}
	if recovered, err := headRecover.Recover(requestCtx(), artifacts.opts.routeID); err != nil {
		closer.Close()
		writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
		return persistOutcome{}, 20
	} else if len(recovered) > 0 {
		if sink, sinkErr := resolveSink(artifacts.cfg, artifacts.opts.routeID, opsLogger(stderr, artifacts.cfg)); sinkErr == nil {
			if backoff, bErr := backoffFromConfig(artifacts.route.SubmissionRetry); bErr == nil {
				// The healed unknowns resolve now; a failure here never
				// blocks the arrival, but it is never silent either — the
				// operator sees it on stderr (E8-T2 round-1 review).
				if _, warnings, rErr := reconcileUnknownDispatches(artifacts.cfg, store, sink, artifacts.opts.routeID, backoff); rErr != nil {
					fmt.Fprintf(stderr, "%s\n", rErr.Error())
				} else {
					for _, w := range warnings {
						fmt.Fprintf(stderr, "%s\n", w)
					}
				}
			}
		}
	}
	lins, err := buildLineages(artifacts)
	if err != nil {
		closer.Close()
		if errors.Is(err, errNoDestinationSelected) {
			writeError(stderr, command, "config_invalid", "configuration", err.Error())
			return persistOutcome{}, 3
		}
		writeError(stderr, command, "internal_unclassified", "internal", err.Error())
		return persistOutcome{}, 40
	}
	coordinator := &dispatch.Coordinator{
		Store: store, Now: func() string { return dispatch.Timestamp(time.Now()) }, Actor: "dispatch-cli",
	}
	outcome, err := coordinator.ArrivalFanout(requestCtx(), lins)
	if err != nil {
		closer.Close()
		switch {
		case errors.Is(err, ports.ErrIdempotencyConflict):
			writeError(stderr, command, "dispatch_duplicate", "conflict", err.Error())
			return persistOutcome{}, 14
		case errors.Is(err, ports.ErrRouteSlotHeld):
			writeError(stderr, command, "route_slot_held", "conflict", err.Error())
			return persistOutcome{}, 14
		case errors.Is(err, ports.ErrInvalidFanoutRecord):
			// A fan-out record that failed the store-boundary validation is
			// a producer/configuration defect — non-retryable, never a
			// conflict the operator should replay (E12 epic whole-review
			// round 2).
			writeError(stderr, command, "config_invalid", "configuration", err.Error())
			return persistOutcome{}, 3
		default:
			writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
			return persistOutcome{}, 20
		}
	}
	// An invalid fan-out record is NOT lane-isolated (E12 epic
	// whole-review round 3): the configuration is broken for every lane,
	// so even when sibling lanes activated or merged the failure is the
	// command-level configuration class (exit 3), never a per-lane
	// warning at exit 0 — the durable per-lane outcomes ride the error
	// envelope's result slot so the operator still sees what landed.
	for _, failure := range outcome.Failed {
		if errors.Is(failure.Err, ports.ErrInvalidFanoutRecord) {
			closer.Close()
			writeErrorWithResult(stderr, command, "config_invalid", "configuration", failure.Err.Error(),
				map[string]any{
					"fanout":       laneResultsEnvelope(outcome.Activated),
					"merged_lanes": laneResultsEnvelope(outcome.Merged),
					"failed_lanes": laneFailuresEnvelope(outcome.Failed),
				})
			return persistOutcome{}, 3
		}
	}
	if len(outcome.Activated) == 0 && len(outcome.Failed) == 0 {
		// Every lane merged: the occurrence is owed work recorded in the
		// lanes' dirty generations (CON-002, CON-008).
		snap, err := store.LoadRouteState(requestCtx(), artifacts.opts.routeID)
		closer.Close()
		if err != nil {
			writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
			return persistOutcome{}, 20
		}
		return persistOutcome{merged: true, dirty: snap.DirtyGeneration, children: outcome.Merged}, 0
	}
	if len(outcome.Activated) == 0 {
		// No lane activated but some failed beside merges: the occurrence
		// is partially recorded; report the merged lanes with the failures
		// visible instead of a bare merge envelope.
		snap, err := store.LoadRouteState(requestCtx(), artifacts.opts.routeID)
		closer.Close()
		if err != nil {
			writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
			return persistOutcome{}, 20
		}
		return persistOutcome{merged: true, dirty: snap.DirtyGeneration, children: outcome.Merged, failures: outcome.Failed}, 0
	}
	return persistOutcome{dispatchID: outcome.Activated[0].DispatchID, children: outcome.Activated, mergedChildren: outcome.Merged, failures: outcome.Failed, store: store, closer: closer}, 0
}

// persistQuarantine commits the quarantine-classified arrival with its
// durable hold and reports the operator-visible case (PTH-008: the
// protected or bulk paths never enter a task manifest).
func persistQuarantine(command string, a *planArtifacts, stdout, stderr io.Writer) int {
	store, closer, exit := openOperatorStore(command, a.opts.configPath, stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	lin, item, err := buildHeldLineage(a)
	if err != nil {
		writeError(stderr, command, "internal_unclassified", "internal", err.Error())
		return 40
	}
	if err := store.CommitQuarantineLineage(requestCtx(), lin, item); err != nil {
		writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
		return 20
	}
	return writeEnvelope(stdout, command, map[string]any{
		"route_id": a.opts.routeID, "disposition": "quarantine",
		"quarantine_id": item.QuarantineID, "reason_codes": a.plan.ReasonCodes,
		"submitted": false,
	})
}

// persistReconcileArrival commits the reconcile-classified arrival and
// marks the single pending reconciliation generation (SRC-005: overflow
// and fresh instance never dispatch partial ordinary changes).
func persistReconcileArrival(command string, a *planArtifacts, stdout, stderr io.Writer) int {
	store, closer, exit := openOperatorStore(command, a.opts.configPath, stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	lin, _, err := buildHeldLineage(a)
	if err != nil {
		writeError(stderr, command, "internal_unclassified", "internal", err.Error())
		return 40
	}
	if err := store.CommitReconcileLineage(requestCtx(), lin, a.env.Clock); err != nil {
		writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
		return 20
	}
	return writeEnvelope(stdout, command, map[string]any{
		"route_id": a.opts.routeID, "disposition": "reconcile",
		"reason_codes": a.plan.ReasonCodes, "pending_reconcile": true,
		"submitted": false,
	})
}

// persistDrop commits the dropped arrival's evidence only.
func persistDrop(command string, a *planArtifacts, stdout, stderr io.Writer) int {
	store, closer, exit := openOperatorStore(command, a.opts.configPath, stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	lin, _, err := buildHeldLineage(a)
	if err != nil {
		writeError(stderr, command, "internal_unclassified", "internal", err.Error())
		return 40
	}
	if err := store.CommitDropLineage(requestCtx(), lin); err != nil {
		writeError(stderr, command, "sqlite_query_failed", "storage", err.Error())
		return 20
	}
	return writeEnvelope(stdout, command, map[string]any{
		"route_id": a.opts.routeID, "disposition": "drop",
		"reason_codes": a.plan.ReasonCodes,
	})
}

// buildHeldLineage assembles the observation, batch, and decision of a
// non-dispatching arrival plus, when requested, its quarantine hold.
func buildHeldLineage(a *planArtifacts) (ports.Lineage, ports.QuarantineInput, error) {
	gen := ids.NewUUIDv7(time.Now)
	observationID, err := gen.NewID()
	if err != nil {
		return ports.Lineage{}, ports.QuarantineInput{}, err
	}
	batchID, err := gen.NewID()
	if err != nil {
		return ports.Lineage{}, ports.QuarantineInput{}, err
	}
	decisionID, err := gen.NewID()
	if err != nil {
		return ports.Lineage{}, ports.QuarantineInput{}, err
	}
	quarantineID, err := gen.NewID()
	if err != nil {
		return ports.Lineage{}, ports.QuarantineInput{}, err
	}
	now := dispatch.Timestamp(time.Now())
	changes := make([]ports.ObservationChange, 0, len(a.batch.Changes))
	for i, c := range a.batch.Changes {
		changes = append(changes, ports.ObservationChange{
			Ordinal: i, Path: c.Path, Operation: string(c.Operation), ExistsAfter: c.ExistsAfter,
			FileType: string(c.FileType), BeforeDigest: string(c.BeforeDigest), AfterDigest: string(c.AfterDigest),
			DigestStatus: string(c.DigestStatus),
		})
	}
	flagsJSON, err := json.Marshal(a.env.Flags())
	if err != nil {
		return ports.Lineage{}, ports.QuarantineInput{}, err
	}
	// The source position object persists verbatim (watchman since and
	// clock): the observation contract's source.position (E7-T8/M-11).
	positionJSON, _ := json.Marshal(map[string]any{
		"since":         a.env.Since,
		"clock":         a.env.Clock,
		"relative_root": a.env.RelativeRoot,
	})
	// The record contract sorts decision reason codes.
	sorted := append([]string(nil), a.plan.ReasonCodes...)
	sort.Strings(sorted)
	reasons, err := json.Marshal(sorted)
	if err != nil {
		return ports.Lineage{}, ports.QuarantineInput{}, err
	}
	classification := "normal"
	if len(a.plan.Classification) > 0 {
		classification = a.plan.Classification[0]
	}
	return ports.Lineage{
			Observation: ports.ObservationInput{
				ObservationID: string(observationID), SchemaVersion: "agent-dispatch.source-observation/v1",
				SourceType: "watchman", SourceID: a.route.Source.SourceID,
				SourceEventKey: a.input.SourceEventKey(a.route.Source.SourceID),
				TriggerName:    a.env.Trigger, ResourceID: a.route.Source.Resource,
				ObservedAt: now, ReceivedAt: now,
				RawPayloadDigest: string(a.input.RawDigest), IngestStatus: "accepted",
				FlagsJSON: string(flagsJSON), PositionJSON: string(positionJSON), Changes: changes,
			},
			Batch: ports.BatchInput{
				BatchID: string(batchID), RouteID: a.opts.routeID, RouteRevision: a.plan.Route.Revision,
				ResourceID: a.route.Source.Resource, CreatedAt: now,
				ContentFingerprint: a.plan.ContentFingerprint,
				ObservationIDs:     []string{string(observationID)},
			},
			Decision: ports.DecisionInput{
				DecisionID: string(decisionID), BatchID: string(batchID), RouteID: a.opts.routeID,
				RouteRevision: a.plan.Route.Revision, PolicyRevision: config.PolicyRevision(a.route),
				Disposition: a.plan.Disposition, Classification: classification,
				ReasonCodesJSON: string(reasons), CreatedAt: now, Actor: "planner",
			},
		}, ports.QuarantineInput{
			QuarantineID: "q-" + string(quarantineID), BatchID: string(batchID), DecisionID: string(decisionID),
			ReasonCodes: a.plan.ReasonCodes, CreatedAt: now,
		}, nil
}

// errNoDestinationSelected is the fail-closed no-selection sentinel
// (FAN-005): an occurrence every destination's conditions refuse creates
// nothing — no aggregate, no child, no intent — and classifies as a
// configuration refusal, never an internal defect.
var errNoDestinationSelected = errors.New("no destination selected")

// buildLineages assembles the durable persistence units of one fan-out
// occurrence from the planned artifacts (E12-T2, FAN-002/FAN-003): the
// shared observation, batch, and decision of buildHeldLineage plus ONE
// intent per selected destination, all under ONE aggregate ID and ONE
// shared decision. Each intent's request and idempotency key derive from
// its own destination lane; its fanout block names its lane, carries the
// full selection summary (every selected destination), and references
// every selected destination's revision record. Destinations whose
// structural conditions (FAN-004/FAN-005) do not select this occurrence
// are skipped.
func buildLineages(a *planArtifacts) ([]ports.Lineage, error) {
	base, _, err := buildHeldLineage(a)
	if err != nil {
		return nil, err
	}
	gen := ids.NewUUIDv7(time.Now)
	aggregateID, err := gen.NewID()
	if err != nil {
		return nil, err
	}
	lanes, err := certifiedLanes(a.cfg, a.opts.routeID)
	if err != nil {
		return nil, err
	}
	selCtx := selectionContextOf(a)
	matcher := destinationPathMatcher(a.route)
	var selected []certifiedLaneInfo
	var selections []records.DestinationSelection
	var revisions []ports.DestinationRevisionInput
	for _, lane := range lanes {
		reason, err := selectionReason(a, lane.dest, selCtx, matcher)
		if err != nil {
			return nil, err
		}
		if reason == "" {
			continue
		}
		selected = append(selected, lane)
		selections = append(selections, records.DestinationSelection{
			DestinationID: lane.lane.ID, DestinationRevision: lane.lane.Revision,
			Workstream: lane.lane.Workstream, Reason: reason,
		})
		revisions = append(revisions, laneRevisionInput(lane.lane, lane.projection))
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("%w: route %q selected no destination for this occurrence: every destination's conditions refused it (FAN-005)",
			errNoDestinationSelected, a.opts.routeID)
	}
	// The occurrence's selection rides on the shared batch (E12 epic
	// validation, migration v15): whichever lane commits the prefix
	// persists the batch WITH the full selection summary, so the lanes'
	// later follow-ups filter their dirty generations by the occurrence
	// the merge recorded — never by re-evaluating conditions per change.
	selectedIDs := make([]string, 0, len(selected))
	for _, lane := range selected {
		selectedIDs = append(selectedIDs, lane.lane.ID)
	}
	base.Batch.SelectedDestinations = selectedIDs
	out := make([]ports.Lineage, 0, len(selected))
	for _, lane := range selected {
		dispatchID, err := gen.NewID()
		if err != nil {
			return nil, err
		}
		hints, err := hintsOf(lane.dest)
		if err != nil {
			return nil, err
		}
		req, key, err := dispatch.BuildRequest(dispatch.RequestInput{
			DispatchID: string(dispatchID),
			Route:      ports.TaskRouteRef{ID: a.opts.routeID, Revision: a.plan.Route.Revision},
			// The contract workspace binding form (hermes-task-contract §2):
			// the destination's declared workspace, defaulting to
			// dir:<resolved root> for the v0.1 markdown vault resource.
			Resource:           ports.TaskResource{ID: a.route.Source.Resource, Workspace: workspaceOf(lane.dest, a.resource.Root)},
			TargetID:           a.dest.Target,
			TargetScope:        resolvedTargetScope(a.resolved),
			Destination:        lane.lane,
			Generation:         1,
			Fingerprint:        records.Digest(a.plan.ContentFingerprint),
			Changes:            a.batch.Changes,
			Flags:              flagsOf(a.env),
			AcceptanceCriteria: dispatch.WikiAcceptanceCriteria,
			Assignment:         assignmentOf(lane.dest),
			ExecutionHints:     hints,
		})
		if err != nil {
			return nil, fmt.Errorf("task request: %v", err)
		}
		requestJSON, err := dispatch.MarshalRequest(req)
		if err != nil {
			return nil, err
		}
		lin := base
		lin.Intent = ports.IntentInput{
			DispatchID: string(dispatchID), DecisionID: base.Decision.DecisionID, RouteID: a.opts.routeID,
			RouteRevision: a.plan.Route.Revision, TargetID: a.dest.Target, TargetType: a.resolved.Type(),
			TargetScope: resolvedTargetScope(a.resolved),
			ResourceID:  a.route.Source.Resource, Generation: 1, IdempotencyKey: key,
			ContentFingerprint: a.plan.ContentFingerprint, ManifestDigest: dispatch.ManifestDigest(a.batch.Changes),
			RequestVersion: dispatch.RequestContractVersion, RequestJSON: requestJSON, CreatedAt: base.Decision.CreatedAt,
			// The full multi-selection summary (all lanes) rides every
			// child; the constructor shape is per-child lane identity.
			Fanout: &ports.FanoutInput{
				AggregateID: string(aggregateID), Origin: string(records.OriginArrival),
				DestinationID: lane.lane.ID, DestinationRevision: lane.lane.Revision, Workstream: lane.lane.Workstream,
				Selections: selections,
				Revisions:  revisions,
			},
		}
		out = append(out, lin)
	}
	return out, nil
}

// selectionContextOf projects the occurrence's structural facts onto the
// closed selection context (FAN-004: paths, operations, classification,
// and policy outcome only — never content semantics).
func selectionContextOf(a *planArtifacts) dispatch.SelectionContext {
	paths := make([]string, 0, len(a.batch.Changes))
	operations := make([]records.Operation, 0, len(a.batch.Changes))
	for _, c := range a.batch.Changes {
		paths = append(paths, c.Path)
		operations = append(operations, c.Operation)
	}
	classification := "normal"
	if len(a.plan.Classification) > 0 {
		classification = a.plan.Classification[0]
	}
	return dispatch.SelectionContext{
		Paths: paths, Operations: operations,
		Classification: classification, Disposition: a.plan.Disposition,
	}
}

// selectionReason evaluates one destination's structural conditions with
// the FAN-005 evaluator and renders the closed selection-evidence reason
// (FAN-010): an empty string means the occurrence does not select the
// destination. The fanout_mode reason records the certified mode when the
// destination declares no conditions; a conditioned destination records
// the evaluator's closed outcome.
func selectionReason(a *planArtifacts, dest config.Destination, selCtx dispatch.SelectionContext, matcher func(pattern, path string) (bool, error)) (string, error) {
	var conds *dispatch.DestinationConditionSet
	if dest.Conditions != nil {
		conds = &dispatch.DestinationConditionSet{
			PathInclude: dest.Conditions.PathInclude, PathExclude: dest.Conditions.PathExclude,
			Operations: dest.Conditions.Operations, Classifications: dest.Conditions.Classifications,
			PolicyOutcomes: dest.Conditions.PolicyOutcomes,
		}
	}
	selected, reason, err := dispatch.SelectDestination(selCtx, conds, matcher)
	if err != nil {
		return "", fmt.Errorf("destination %q conditions: %v", dest.ID, err)
	}
	if !selected {
		return "", nil
	}
	if conds == nil || (len(conds.PathInclude) == 0 && len(conds.PathExclude) == 0 && len(conds.Operations) == 0 &&
		len(conds.Classifications) == 0 && len(conds.PolicyOutcomes) == 0) {
		mode := a.route.FanoutMode
		if mode == "" {
			mode = "all"
		}
		return "fanout_mode:" + mode, nil
	}
	return "conditions:" + reason, nil
}

// destinationPathMatcher adapts the pattern engine onto the evaluator's
// per-pattern matcher (E12-T2): one compiled single-pattern engine per
// distinct condition pattern, with the same case mode the route revision
// records, so a condition's matching behavior and the revision's recorded
// mode cannot diverge.
func destinationPathMatcher(route config.Route) func(pattern, path string) (bool, error) {
	mode := policy.CaseSensitive
	if config.CaseMode() == "insensitive" {
		mode = policy.CaseInsensitive
	}
	cache := map[string]*policy.Engine{}
	return func(pattern, path string) (bool, error) {
		engine, ok := cache[pattern]
		if !ok {
			compiled, err := policy.NewEngine([]string{pattern}, nil, nil, nil, mode)
			if err != nil {
				return false, err
			}
			cache[pattern] = compiled
			engine = compiled
		}
		status, err := engine.Classify(path)
		if err != nil {
			return false, err
		}
		return status == policy.StatusNormal, nil
	}
}

// workspaceOf resolves the destination's Hermes workspace form,
// defaulting to the vault resource root (E11-T1: the destination may
// bind a different scratch/worktree/dir workspace).
func workspaceOf(dest config.Destination, resourceRoot string) string {
	if dest.Workspace != "" {
		return dest.Workspace
	}
	return "dir:" + resourceRoot
}

// flagsOf projects the trusted source flags onto the request flag list.
func flagsOf(env watchman.Env) []string {
	flags := []string{}
	if env.Flags().Overflow {
		flags = append(flags, "overflow")
	}
	if env.Flags().FreshInstance {
		flags = append(flags, "fresh_instance")
	}
	if env.Flags().HasRelative {
		flags = append(flags, "relative_root")
	}
	return flags
}

// assignmentOf maps the certified destination onto the request
// assignment (sink-adapter-contract §8: the mutex is a capability
// request, never prompt text).
func assignmentOf(dest config.Destination) *ports.TaskAssignment {
	if dest.Profile == "" && len(dest.Skills) == 0 {
		return nil
	}
	return &ports.TaskAssignment{
		Profile:  dest.Profile,
		Skills:   dest.Skills,
		MutexKey: dest.MutexKey,
	}
}

// hintsOf maps the configured execution hints onto the request. The
// runtime parses through the one schema-exact parser (whole-day units
// included), and a configured but unparsable runtime fails closed
// instead of silently dropping the hint (HER-006: missing mappings are
// reported, never discarded).
func hintsOf(dest config.Destination) (*ports.TaskExecutionHints, error) {
	if dest.ExecutionHints.MaxRuntime == "" && dest.ExecutionHints.MaxAttempts == 0 {
		return nil, nil
	}
	hints := &ports.TaskExecutionHints{}
	if dest.ExecutionHints.MaxRuntime != "" {
		parsed, err := config.ParseDuration(dest.ExecutionHints.MaxRuntime)
		if err != nil {
			return nil, fmt.Errorf("execution_hints.max_runtime: %v", err)
		}
		hints.MaxRuntimeSeconds = parsed.Nanos / int64(time.Second)
	}
	hints.MaxAttempts = int64(dest.ExecutionHints.MaxAttempts)
	return hints, nil
}

// leaseTTLFor derives the attempt lease TTL from the route target's
// configured submit timeout plus a fixed margin, so a live submitter's
// lease can never be stolen by a recovery sweep while its invocation is
// still inside the operator-approved window (E8-T2, M-1). The default
// matches the shipped submit timeout (30 s + 30 s margin = one minute);
// an unparsable value falls back to it (configuration validation
// rejects those earlier).
func leaseTTLFor(submitTimeout string) time.Duration {
	const margin = 30 * time.Second
	if d, err := time.ParseDuration(submitTimeout); err == nil && d > 0 {
		return d + margin
	}
	return 30*time.Second + margin
}

// newPatternEngine compiles the route's pattern sets with the case mode
// resolved identically to the route revision (config.CaseMode), so the
// engine's behavior and the revision's recorded mode cannot diverge.
func newPatternEngine(route config.Route) (*policy.Engine, error) {
	mode := policy.CaseSensitive
	if config.CaseMode() == "insensitive" {
		mode = policy.CaseInsensitive
	}
	return policy.NewEngine(route.Source.Include, route.Source.Exclude, route.Policy.Protected, route.Policy.Immutable, mode)
}

// planErr writes one error envelope and returns the exit code.
func planErr(w io.Writer, command, code, category, message string, exit int) int {
	writeError(w, command, code, category, message)
	return exit
}

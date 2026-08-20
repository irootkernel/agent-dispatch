package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rootkernel/jjukkumi/internal/adapters/localfs"
	"github.com/rootkernel/jjukkumi/internal/adapters/watchman"
	"github.com/rootkernel/jjukkumi/internal/app/dispatch"
	"github.com/rootkernel/jjukkumi/internal/app/ingest"
	"github.com/rootkernel/jjukkumi/internal/config"
	"github.com/rootkernel/jjukkumi/internal/domain/ids"
	"github.com/rootkernel/jjukkumi/internal/domain/policy"
	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/platformpaths"
	"github.com/rootkernel/jjukkumi/internal/ports"
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
// (cli-spec §1): explicit path, then JJUKKUMI_CONFIG, then the platform
// default.
func resolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("JJUKKUMI_CONFIG"); env != "" {
		return env
	}
	return platformpaths.DefaultConfigPath()
}

// DefaultMaxHashFileBytes is the fallback hash bound when the operator
// sets no limits.max_hash_file_bytes, matching the documented
// configuration-spec example envelope (16 MiB).
const DefaultMaxHashFileBytes int64 = 16 << 20

// planArtifacts is one evaluated invocation: the loaded configuration,
// the trusted environment binding, the normalized batch, and the plan.
type planArtifacts struct {
	opts     *planOptions
	cfg      *config.Config
	route    config.Route
	resource config.Resource
	targetID string
	target   config.Target
	revision string
	env      watchman.Env
	input    watchman.Input
	batch    *ingest.Result
	plan     *dispatch.Plan
}

// planPipeline runs the shared read-only planning pipeline: parse stdin,
// validate the trusted environment binding, normalize the batch, and
// evaluate the pure policy planner (POL-007, POL-008, SEC-010).
func planPipeline(command string, args []string, stderr io.Writer) (*planArtifacts, int) {
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
	target, ok := cfg.Targets[route.Dispatch.Target]
	if !ok {
		return nil, planErr(stderr, command, "config_invalid", "configuration", fmt.Sprintf("target %q is not defined", route.Dispatch.Target), 3)
	}
	revision, ok := config.RouteRevision(cfg, opts.routeID)
	if !ok {
		return nil, planErr(stderr, command, "internal_unclassified", "internal", "route revision could not be computed", 40)
	}

	env, err := watchman.ParseEnv(os.LookupEnv)
	if err != nil {
		return nil, planErr(stderr, command, "source_missing_required_metadata", "input_rejected", err.Error(), 4)
	}
	if err := watchman.ValidateBinding(env, route.Source.TriggerName, resource.Root); err != nil {
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

	engine, err := newPatternEngine(route)
	if err != nil {
		return nil, planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	resolver, err := localfs.NewResolver(resource.Root)
	if err != nil {
		return nil, planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	if cfg.Limits.MaxPathBytes != nil {
		engine.SetMaxPathBytes(int(*cfg.Limits.MaxPathBytes))
		resolver.SetLimits(*cfg.Limits.MaxPathBytes)
	}
	maxHash := DefaultMaxHashFileBytes
	if cfg.Limits.MaxHashFileBytes != nil {
		if *cfg.Limits.MaxHashFileBytes <= 0 {
			return nil, planErr(stderr, command, "config_invalid", "configuration", "limits.max_hash_file_bytes must be positive when set", 3)
		}
		maxHash = *cfg.Limits.MaxHashFileBytes
	}

	batch, err := ingest.BuildBatch(input.Entries, engine, resolver, ingest.NoFacts{}, env.Flags(), route.Source.Resource, ingest.Options{MaxHashBytes: maxHash})
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
		RequiredCapabilities: target.RequiredCapabilities,
	}, dispatch.Input{Batch: batch, Flags: env.Flags()})
	if err != nil {
		return nil, planErr(stderr, command, "internal_unclassified", "internal", err.Error(), 40)
	}

	// POL-008/SEC-010: revalidate the plan's revision against the active
	// snapshot before emitting or persisting it.
	if plan.Route.Revision != revision {
		return nil, planErr(stderr, command, "internal_unclassified", "internal", "plan revision does not match the active route revision", 40)
	}
	return &planArtifacts{
		opts: opts, cfg: cfg, route: route, resource: resource,
		targetID: route.Dispatch.Target, target: target, revision: revision,
		env: env, input: input, batch: batch, plan: plan,
	}, 0
}

// runPlan plans one Watchman invocation end to end with no SQLite
// mutation and no target call.
func runPlan(command string, args []string, stdout, stderr io.Writer) int {
	artifacts, code := planPipeline(command, args, stderr)
	if code != 0 {
		return code
	}
	_ = artifacts.opts.jsonOutput // machine output only (CLI-001)
	return writeEnvelope(stdout, command, artifacts.plan)
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
	artifacts, code := planPipeline(command, rest, stderr)
	if code != 0 {
		return code
	}
	lin, exit := persistLineage(command, artifacts, stderr)
	if exit != 0 {
		return exit
	}
	if noSubmit {
		return writeEnvelope(stdout, command, map[string]any{
			"route_id": artifacts.opts.routeID, "dispatch_id": lin.Intent.DispatchID,
			"state": "ready", "submitted": false,
		})
	}
	// The submit phase needs the E4 sink adapter; the intent is durable
	// and ready, and no automatic target fallback exists (DUR-008).
	if _, err := resolveSink(artifacts.target.Type); err != nil {
		writeError(stderr, command, "target_definite_unavailable", "target_unavailable", err.Error())
		return sinkUnavailableExit
	}
	return writeEnvelope(stdout, command, map[string]any{"route_id": artifacts.opts.routeID, "dispatch_id": lin.Intent.DispatchID, "submitted": true})
}

// persistLineage commits the planned observation-to-intent lineage into
// the durable store (DUR-002: the committed intent exists before any
// target invocation).
func persistLineage(command string, artifacts *planArtifacts, stderr io.Writer) (ports.Lineage, int) {
	store, closer, exit := openOperatorStore(command, artifacts.opts.configPath, stderr)
	if exit != 0 {
		return ports.Lineage{}, exit
	}
	defer closer.Close()
	lin, err := buildLineage(artifacts)
	if err != nil {
		writeError(stderr, command, "internal_unclassified", "internal", err.Error())
		return ports.Lineage{}, 40
	}
	if err := store.CommitLineage(requestCtx(), lin); err != nil {
		switch {
		case errors.Is(err, ports.ErrIdempotencyConflict):
			writeError(stderr, command, "dispatch_duplicate", "conflict", err.Error())
			return ports.Lineage{}, 14
		case errors.Is(err, ports.ErrRouteSlotHeld):
			writeError(stderr, command, "route_slot_held", "conflict", err.Error())
			return ports.Lineage{}, 14
		default:
			writeError(stderr, command, "sqlite_open_failed", "storage", err.Error())
			return ports.Lineage{}, 20
		}
	}
	return lin, 0
}

// buildLineage assembles the durable persistence unit from the planned
// artifacts: one observation with its normalized changes, the canonical
// batch, the immutable decision, and the intent with its self-contained
// request.
func buildLineage(a *planArtifacts) (ports.Lineage, error) {
	gen := ids.NewUUIDv7(time.Now)
	observationID, err := gen.NewID()
	if err != nil {
		return ports.Lineage{}, err
	}
	batchID, err := gen.NewID()
	if err != nil {
		return ports.Lineage{}, err
	}
	decisionID, err := gen.NewID()
	if err != nil {
		return ports.Lineage{}, err
	}
	dispatchID, err := gen.NewID()
	if err != nil {
		return ports.Lineage{}, err
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
		return ports.Lineage{}, err
	}
	reasons, err := json.Marshal(a.plan.ReasonCodes)
	if err != nil {
		return ports.Lineage{}, err
	}
	classification := "normal"
	if len(a.plan.Classification) > 0 {
		classification = a.plan.Classification[0]
	}
	req, key, err := dispatch.BuildRequest(dispatch.RequestInput{
		DispatchID:         string(dispatchID),
		Route:              ports.TaskRouteRef{ID: a.opts.routeID, Revision: a.plan.Route.Revision},
		Resource:           ports.TaskResource{ID: a.route.Source.Resource, Workspace: a.resource.Root},
		TargetID:           a.targetID,
		Generation:         1,
		Fingerprint:        records.Digest(a.plan.ContentFingerprint),
		Changes:            a.batch.Changes,
		Flags:              flagsOf(a.env),
		AcceptanceCriteria: dispatch.WikiAcceptanceCriteria,
		Assignment:         assignmentOf(a.route),
		ExecutionHints:     hintsOf(a.route),
	})
	if err != nil {
		return ports.Lineage{}, fmt.Errorf("task request: %v", err)
	}
	requestJSON, err := dispatch.MarshalRequest(req)
	if err != nil {
		return ports.Lineage{}, err
	}
	return ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: string(observationID), SchemaVersion: "jjukkumi.source-observation/v1",
			SourceType: "watchman", SourceID: a.route.Source.SourceID,
			SourceEventKey: a.input.SourceEventKey(a.route.Source.SourceID),
			TriggerName:    a.env.Trigger, ResourceID: a.route.Source.Resource,
			ObservedAt: now, ReceivedAt: now,
			RawPayloadDigest: string(a.input.RawDigest), IngestStatus: "accepted",
			FlagsJSON: string(flagsJSON), Changes: changes,
		},
		Batch: ports.BatchInput{
			BatchID: string(batchID), RouteID: a.opts.routeID, RouteRevision: a.plan.Route.Revision,
			ResourceID: a.route.Source.Resource, CreatedAt: now,
			ContentFingerprint: a.plan.ContentFingerprint,
			ObservationIDs:     []string{string(observationID)},
		},
		Decision: ports.DecisionInput{
			DecisionID: string(decisionID), BatchID: string(batchID), RouteID: a.opts.routeID,
			RouteRevision: a.plan.Route.Revision, PolicyRevision: a.revision,
			Disposition: a.plan.Disposition, Classification: classification,
			ReasonCodesJSON: string(reasons), CreatedAt: now, Actor: "planner",
		},
		Intent: ports.IntentInput{
			DispatchID: string(dispatchID), DecisionID: string(decisionID), RouteID: a.opts.routeID,
			RouteRevision: a.plan.Route.Revision, TargetID: a.targetID, TargetType: a.target.Type,
			ResourceID: a.route.Source.Resource, Generation: 1, IdempotencyKey: key,
			ContentFingerprint: a.plan.ContentFingerprint, ManifestDigest: dispatch.ManifestDigest(a.batch.Changes),
			RequestVersion: dispatch.RequestContractVersion, RequestJSON: requestJSON, CreatedAt: now,
		},
	}, nil
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

// assignmentOf maps the route's dispatch block onto the request
// assignment (sink-adapter-contract §8: the mutex is a capability
// request, never prompt text).
func assignmentOf(route config.Route) *ports.TaskAssignment {
	if route.Dispatch.Profile == "" && len(route.Dispatch.Skills) == 0 {
		return nil
	}
	return &ports.TaskAssignment{
		Profile:  route.Dispatch.Profile,
		Skills:   route.Dispatch.Skills,
		MutexKey: route.Dispatch.MutexKey,
	}
}

// hintsOf maps the route's execution hints when set.
func hintsOf(route config.Route) *ports.TaskExecutionHints {
	if route.Dispatch.ExecutionHints.MaxRuntime == "" && route.Dispatch.ExecutionHints.MaxAttempts == 0 {
		return nil
	}
	hints := &ports.TaskExecutionHints{}
	if secs, err := time.ParseDuration(route.Dispatch.ExecutionHints.MaxRuntime); err == nil && secs > 0 {
		hints.MaxRuntimeSeconds = int64(secs.Seconds())
	}
	hints.MaxAttempts = int64(route.Dispatch.ExecutionHints.MaxAttempts)
	return hints
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

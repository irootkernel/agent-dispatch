package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/localfs"
	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/fingerprint"
	"github.com/irootkernel/agent-dispatch/internal/domain/ids"
	"github.com/irootkernel/agent-dispatch/internal/domain/policy"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// reconcileArtifacts carries the configuration-derived facts one full
// reconciliation needs (route, resource, resolver, patterns) without
// running the Watchman plan pipeline.
type reconcileArtifacts struct {
	cfg        *config.Config
	stderr     io.Writer
	route      config.Route
	resource   config.Resource
	target     config.Target
	targetID   string
	revision   string
	resolver   *localfs.Resolver
	engine     *policy.Engine
	resourceID string
	fileScope  string
	maxHash    int64
	hints      *ports.TaskExecutionHints
}

func (a *reconcileArtifacts) close() {}

// planConfigOnly loads the route configuration and builds the resolver
// and pattern engine the enumeration needs.
func planConfigOnly(command, configPath, routeID string, stderr io.Writer) (*reconcileArtifacts, int) {
	configPath = resolveConfigPath(configPath)
	cfg, err := config.Load(configPath)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return nil, 3
	}
	route, ok := cfg.Routes[routeID]
	if !ok {
		writeError(stderr, command, "config_route_not_found", "configuration", fmt.Sprintf("route %q is not defined", routeID))
		return nil, 3
	}
	resource, ok := cfg.Resources[route.Source.Resource]
	if !ok {
		writeError(stderr, command, "config_invalid", "configuration", fmt.Sprintf("resource %q is not defined", route.Source.Resource))
		return nil, 3
	}
	target, ok := cfg.Targets[route.Dispatch.Target]
	if !ok {
		writeError(stderr, command, "config_invalid", "configuration", fmt.Sprintf("target %q is not defined", route.Dispatch.Target))
		return nil, 3
	}
	revision, ok := config.RouteRevision(cfg, routeID)
	if !ok {
		writeError(stderr, command, "internal_unclassified", "internal", "route revision could not be computed")
		return nil, 40
	}
	runtime, err := newRouteRuntime(cfg, route, resource)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return nil, 3
	}
	hints, err := hintsOf(route)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return nil, 3
	}
	return &reconcileArtifacts{stderr: stderr,
		cfg: cfg, route: route, resource: resource, target: target,
		targetID: route.Dispatch.Target, revision: revision,
		resolver: runtime.resolver, engine: runtime.engine,
		resourceID: route.Source.Resource, fileScope: resource.FileScope,
		maxHash: runtime.maxHash, hints: hints,
	}, 0
}

// routeRuntime is the shared resolver, pattern-engine, and hash bound
// every CLI entry path derives from one route configuration.
type routeRuntime struct {
	resolver *localfs.Resolver
	engine   *policy.Engine
	maxHash  int64
}

// newRouteRuntime builds the runtime surface identically for the plan,
// reconcile, and work paths (E5 audit: one wiring, not three).
func newRouteRuntime(cfg *config.Config, route config.Route, resource config.Resource) (*routeRuntime, error) {
	engine, err := newPatternEngine(route)
	if err != nil {
		return nil, err
	}
	resolver, err := localfs.NewResolver(resource.Root)
	if err != nil {
		return nil, err
	}
	maxHash := DefaultMaxHashFileBytes
	if cfg.Limits.MaxPathBytes != nil {
		engine.SetMaxPathBytes(int(*cfg.Limits.MaxPathBytes))
		resolver.SetLimits(*cfg.Limits.MaxPathBytes)
	}
	if cfg.Limits.MaxHashFileBytes != nil {
		if *cfg.Limits.MaxHashFileBytes <= 0 {
			return nil, fmt.Errorf("limits.max_hash_file_bytes must be positive when set")
		}
		maxHash = *cfg.Limits.MaxHashFileBytes
	}
	return &routeRuntime{resolver: resolver, engine: engine, maxHash: maxHash}, nil
}

// reconcileIntentBuilder builds the one latest-state reconciliation
// intent for an idle route: full-scope instruction, empty evidence
// manifest, fresh idempotency key (CLI-005: no ambiguous replay).
func (a *reconcileArtifacts) reconcileIntentBuilder() func(routeID, reason, decisionID string, changes []records.ChangeItem) (ports.IntentInput, error) {
	return func(routeID, reason, decisionID string, changes []records.ChangeItem) (ports.IntentInput, error) {
		now := dispatch.Timestamp(time.Now())
		dispatchID := fmt.Sprintf("disp-reconcile-%s-%s", routeID, reason) + "-" + ids.CompactTimestamp(now) + "-" + ids.RandomSuffix()
		// The fingerprint derives from the reconciliation diff, so
		// distinct generations produce distinct idempotency keys (a
		// constant fingerprint would collide on the target's dedup).
		fpChanges := make([]records.FingerprintChange, 0, len(changes))
		for _, c := range changes {
			fpChanges = append(fpChanges, records.FingerprintChange{
				AfterDigest: string(c.AfterDigest), BeforeDigest: string(c.BeforeDigest),
				ExistsAfter: c.ExistsAfter, Operation: string(c.Operation), Path: c.Path,
			})
		}
		contentDigest, err := fingerprint.Content(records.ContentFingerprintInput{
			Changes: fpChanges, ResourceID: a.resourceID, RelativeRoot: a.resource.Root,
		})
		if err != nil {
			return ports.IntentInput{}, err
		}
		req, key, err := dispatch.BuildRequest(dispatch.RequestInput{
			DispatchID:         dispatchID,
			Route:              ports.TaskRouteRef{ID: routeID, Revision: a.revision},
			Resource:           ports.TaskResource{ID: a.resourceID, Workspace: "dir:" + a.resource.Root},
			TargetID:           a.targetID,
			Generation:         1,
			Fingerprint:        contentDigest,
			Changes:            changes, // the reconciliation diff: bounded evidence, never content
			Flags:              []string{"latest_state", "reconcile:" + reason},
			AcceptanceCriteria: dispatch.WikiAcceptanceCriteria,
			Assignment:         assignmentOf(a.route),
			ExecutionHints:     a.hints,
		})
		if err != nil {
			return ports.IntentInput{}, err
		}
		requestJSON, err := dispatch.MarshalRequest(req)
		if err != nil {
			return ports.IntentInput{}, err
		}
		return ports.IntentInput{
			DispatchID: dispatchID, DecisionID: decisionID, RouteID: routeID, RouteRevision: a.revision,
			TargetID: a.targetID, TargetType: a.target.Type, TargetScope: targetScope(a.target),
			ResourceID: a.resourceID, Generation: 1, IdempotencyKey: key,
			ContentFingerprint: string(contentDigest),
			ManifestDigest:     dispatch.ManifestDigest(changes),
			RequestVersion:     dispatch.RequestContractVersion, RequestJSON: requestJSON,
		}, nil
	}
}

// submitRuntime assembles the submission runtime for --submit; the
// resolved sink and backoff are returned for the head-of-submit recovery
// wiring (E8-T2/H-6).
func (a *reconcileArtifacts) submitRuntime(store storeOp) (*dispatch.Runtime, ports.Sink, dispatch.Backoff, error) {
	sink, err := resolveSink(a.cfg, a.target, a.route)
	if err != nil {
		return nil, nil, dispatch.Backoff{}, err
	}
	backoff, err := backoffFromConfig(a.route.Dispatch.SubmissionRetry)
	if err != nil {
		return nil, nil, dispatch.Backoff{}, err
	}
	return &dispatch.Runtime{
		Store: store, Sink: sink, Now: time.Now,
		LeaseTTL: leaseTTLFor(a.target.SubmitTimeout), Backoff: backoff, JitterUnit: jitterUnit, Actor: "reconcile",
		Log: opsLogger(a.stderr, a.cfg), TraceID: globalTraceID,
		StalenessCheck: stalenessCheckOf(a.cfg), StaleRebuilder: staleRebuilderOf(store, a.cfg),
	}, sink, backoff, nil
}

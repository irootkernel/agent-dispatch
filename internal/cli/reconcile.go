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
	routeID    string
	route      config.Route
	resource   config.Resource
	dest       config.Destination
	resolved   config.ResolvedTarget
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
	dest, resolved, rerr := resolveRouteTarget(cfg, routeID)
	if rerr != nil {
		writeError(stderr, command, "config_invalid", "configuration", rerr.Error())
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
	hints, err := hintsOf(dest)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return nil, 3
	}
	return &reconcileArtifacts{stderr: stderr,
		cfg: cfg, routeID: routeID, route: route, resource: resource, dest: dest, resolved: resolved,
		targetID: dest.Target, revision: revision,
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

// reconcileIntentBuilder builds the latest-state reconciliation intent
// for an idle route: full-scope instruction, empty evidence manifest,
// fresh idempotency key (CLI-005: no ambiguous replay). Since E12-T2 the
// child carries the full destination-selection summary of the route's
// lanes with every referenced destination revision persisted beside it;
// the child's own lane is the canonically-first selected destination
// (FAN-012), because the reconciliation flow commits exactly one intent.
func (a *reconcileArtifacts) reconcileIntentBuilder() func(routeID, reason, decisionID string, changes []records.ChangeItem) (ports.IntentInput, error) {
	return func(routeID, reason, decisionID string, changes []records.ChangeItem) (ports.IntentInput, error) {
		now := dispatch.Timestamp(time.Now())
		dispatchID := fmt.Sprintf("disp-reconcile-%s-%s", routeID, reason) + "-" + ids.CompactTimestamp(now) + "-" + ids.RandomSuffix()
		aggregateID, err := ids.NewUUIDv7(time.Now).NewID()
		if err != nil {
			return ports.IntentInput{}, err
		}
		lanes, err := certifiedLanes(a.cfg, routeID)
		if err != nil {
			return ports.IntentInput{}, err
		}
		lane := lanes[0]
		selections := make([]records.DestinationSelection, 0, len(lanes))
		revisions := make([]ports.DestinationRevisionInput, 0, len(lanes))
		for _, candidate := range lanes {
			selections = append(selections, records.DestinationSelection{
				DestinationID: candidate.lane.ID, DestinationRevision: candidate.lane.Revision,
				Workstream: candidate.lane.Workstream, Reason: "reconcile:" + reason,
			})
			revisions = append(revisions, laneRevisionInput(candidate.lane, candidate.projection))
		}
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
			// No relative-root flag: the reconciliation enumerated the
			// vault root itself, and hashing the absolute root would make
			// the idempotency key depend on where the vault is mounted
			// (E8-T5, M-11 - same content, same key, any mount point).
			Changes: fpChanges, ResourceID: a.resourceID,
		})
		if err != nil {
			return ports.IntentInput{}, err
		}
		req, key, err := dispatch.BuildRequest(dispatch.RequestInput{
			DispatchID:         dispatchID,
			Route:              ports.TaskRouteRef{ID: routeID, Revision: a.revision},
			Resource:           ports.TaskResource{ID: a.resourceID, Workspace: workspaceOf(a.dest, a.resource.Root)},
			TargetID:           a.targetID,
			TargetScope:        resolvedTargetScope(a.resolved),
			Destination:        lane.lane,
			Generation:         1,
			Fingerprint:        contentDigest,
			Changes:            changes, // the reconciliation diff: bounded evidence, never content
			Flags:              []string{"latest_state", "reconcile:" + reason},
			AcceptanceCriteria: dispatch.WikiAcceptanceCriteria,
			Assignment:         assignmentOf(a.dest),
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
			TargetID: a.targetID, TargetType: a.resolved.Type(), TargetScope: resolvedTargetScope(a.resolved),
			ResourceID: a.resourceID, Generation: 1, IdempotencyKey: key,
			ContentFingerprint: string(contentDigest),
			ManifestDigest:     dispatch.ManifestDigest(changes),
			RequestVersion:     dispatch.RequestContractVersion, RequestJSON: requestJSON,
			Fanout: &ports.FanoutInput{
				AggregateID: string(aggregateID), Origin: string(records.OriginReconcile),
				DestinationID: lane.lane.ID, DestinationRevision: lane.lane.Revision, Workstream: lane.lane.Workstream,
				Selections: selections,
				Revisions:  revisions,
			},
		}, nil
	}
}

// submitRuntime assembles the submission runtime for --submit; the
// resolved sink and backoff are returned for the head-of-submit recovery
// wiring (E8-T2/H-6).
func (a *reconcileArtifacts) submitRuntime(store storeOp) (*dispatch.Runtime, ports.Sink, dispatch.Backoff, error) {
	sink, err := resolveSink(a.cfg, a.routeID, opsLogger(a.stderr, a.cfg))
	if err != nil {
		return nil, nil, dispatch.Backoff{}, err
	}
	backoff, err := backoffFromConfig(a.route.SubmissionRetry)
	if err != nil {
		return nil, nil, dispatch.Backoff{}, err
	}
	return newSubmitRuntime(store, sink, a.cfg, submitTimeoutOf(a.resolved), backoff, "reconcile", a.stderr), sink, backoff, nil
}

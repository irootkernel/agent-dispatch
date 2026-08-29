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
// running the Watchman plan pipeline. siblings is the app-layer journal
// of the reconcile fan-out's sibling outcomes (committed DURABLY FIRST
// inside the builder, E12 epic whole-review round 3): the data is owned
// at the app seam — the CLI holds the reference and renders.
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
	siblings   *dispatch.ReconcileSiblingJournal
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

// reconcileIntentBuilder builds the latest-state reconciliation
// intents: full-scope instruction, empty evidence manifest, fresh
// idempotency key (CLI-005: no ambiguous replay). Since the E12 epic
// validation the reconciliation fans out PER LANE (FAN-002): one shared
// aggregate and decision, one child per certified lane. The builder
// returns the FIRST lane's intent for the store's single-intent
// transaction (CommitReconcileIntent and ResolveUncertainReconciliation
// are unchanged) and commits the sibling children through the app-layer
// dispatch.CommitReconcileSiblings BEFORE returning — DURABLE-FIRST (E12
// epic whole-review round 1): every sibling is durable before the first
// lane's transaction runs, so a crash between the two leaves ready
// siblings the existing drain submits, never a committed first lane with
// no durable marker of the remaining selection. The outcomes land on the
// app-layer journal the artifacts reference (round 3) for the envelope
// rendering and the exit-code policy.
func (a *reconcileArtifacts) reconcileIntentBuilder(store dispatch.ReconcileSiblingStore) func(routeID, reason, decisionID string, changes []records.ChangeItem) (ports.IntentInput, error) {
	return func(routeID, reason, decisionID string, changes []records.ChangeItem) (ports.IntentInput, error) {
		now := dispatch.Timestamp(time.Now())
		aggregateID, err := ids.NewUUIDv7(time.Now).NewID()
		if err != nil {
			return ports.IntentInput{}, err
		}
		lanes, err := certifiedLanes(a.cfg, routeID)
		if err != nil {
			return ports.IntentInput{}, err
		}
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
		build := func(index int, dispatchID string) (ports.IntentInput, error) {
			lane := lanes[index]
			req, key, err := dispatch.BuildRequest(dispatch.RequestInput{
				DispatchID:         dispatchID,
				Route:              ports.TaskRouteRef{ID: routeID, Revision: a.revision},
				Resource:           ports.TaskResource{ID: a.resourceID, Workspace: workspaceOf(lane.dest, a.resource.Root)},
				TargetID:           a.targetID,
				TargetScope:        resolvedTargetScope(a.resolved),
				Destination:        lane.lane,
				Generation:         1,
				Fingerprint:        contentDigest,
				Changes:            changes, // the reconciliation diff: bounded evidence, never content
				Flags:              []string{"latest_state", "reconcile:" + reason},
				AcceptanceCriteria: dispatch.WikiAcceptanceCriteria,
				Assignment:         assignmentOf(lane.dest),
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
				CreatedAt: now,
				Fanout: &ports.FanoutInput{
					AggregateID: string(aggregateID), Origin: string(records.OriginReconcile),
					DestinationID: lane.lane.ID, DestinationRevision: lane.lane.Revision, Workstream: lane.lane.Workstream,
					Selections: selections,
					Revisions:  revisions,
				},
			}, nil
		}
		first, err := build(0, fmt.Sprintf("disp-reconcile-%s-%s", routeID, reason)+"-"+ids.CompactTimestamp(now)+"-"+ids.RandomSuffix())
		if err != nil {
			return ports.IntentInput{}, err
		}
		siblings := make([]ports.IntentInput, 0, len(lanes)-1)
		for i := 1; i < len(lanes); i++ {
			sibling, err := build(i, fmt.Sprintf("disp-reconcile-%s-%s", routeID, reason)+"-"+ids.CompactTimestamp(now)+"-"+ids.RandomSuffix())
			if err != nil {
				return ports.IntentInput{}, err
			}
			siblings = append(siblings, sibling)
		}
		a.siblings = &dispatch.ReconcileSiblingJournal{}
		// Durable-first sibling commit (E12 epic whole-review round 1):
		// the loop and its slot-held-skip / failure-surface policy live in
		// the app layer; a sibling's failure never blocks the first lane
		// (CON-007) — the outcomes ride the app-layer journal afterwards
		// and a non-slot-held failure flips the exit code.
		a.siblings.CommitAll(requestCtx(), store, siblings)
		return first, nil
	}
}

// reconcileLaneOutcomes renders the fan-out's sibling-commit outcomes for
// the command envelope and the exit-code policy (E12 epic whole-review
// round 1): one reconcile_lanes entry per SIBLING lane (the first lane's
// outcome is the result's own members), every slot-held skip as a
// warning, and the first non-slot-held failure — a failed sibling is
// never silent. The data comes from the app-layer journal (round 3);
// only the rendering lives here.
func (a *reconcileArtifacts) reconcileLaneOutcomes() (entries []map[string]any, warnings []string, failure *dispatch.ReconcileLaneCommit) {
	commits := a.siblings.Outcomes()
	entries = make([]map[string]any, 0, len(commits))
	for i := range commits {
		commit := &commits[i]
		entry := map[string]any{"destination_id": commit.DestinationID, "dispatch_id": commit.DispatchID, "committed": commit.Committed}
		switch {
		case commit.Err != nil:
			entry["error"] = dispatch.BoundLaneError(commit.Err.Error())
		case commit.Note != "":
			entry["note"] = commit.Note
		}
		entries = append(entries, entry)
		if commit.Note != "" {
			warnings = append(warnings, fmt.Sprintf("reconcile sibling lane %s skipped: %s", commit.DestinationID, commit.Note))
		}
	}
	return entries, warnings, a.siblings.Failure()
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

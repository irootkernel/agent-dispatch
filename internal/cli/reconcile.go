package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/rootkernel/jjukkumi/internal/adapters/localfs"
	"github.com/rootkernel/jjukkumi/internal/app/dispatch"
	"github.com/rootkernel/jjukkumi/internal/config"
	"github.com/rootkernel/jjukkumi/internal/domain/fingerprint"
	"github.com/rootkernel/jjukkumi/internal/domain/policy"
	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// reconcileArtifacts carries the configuration-derived facts one full
// reconciliation needs (route, resource, resolver, patterns) without
// running the Watchman plan pipeline.
type reconcileArtifacts struct {
	cfg        *config.Config
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
	engine, err := newPatternEngine(route)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return nil, 3
	}
	resolver, err := localfs.NewResolver(resource.Root)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return nil, 3
	}
	if cfg.Limits.MaxPathBytes != nil {
		engine.SetMaxPathBytes(int(*cfg.Limits.MaxPathBytes))
		resolver.SetLimits(*cfg.Limits.MaxPathBytes)
	}
	maxHash := DefaultMaxHashFileBytes
	if cfg.Limits.MaxHashFileBytes != nil {
		if *cfg.Limits.MaxHashFileBytes <= 0 {
			writeError(stderr, command, "config_invalid", "configuration", "limits.max_hash_file_bytes must be positive when set")
			return nil, 3
		}
		maxHash = *cfg.Limits.MaxHashFileBytes
	}
	hints, err := hintsOf(route)
	if err != nil {
		writeError(stderr, command, "config_invalid", "configuration", err.Error())
		return nil, 3
	}
	return &reconcileArtifacts{
		cfg: cfg, route: route, resource: resource, target: target,
		targetID: route.Dispatch.Target, revision: revision,
		resolver: resolver, engine: engine,
		resourceID: route.Source.Resource, fileScope: resource.FileScope,
		maxHash: maxHash, hints: hints,
	}, 0
}

// reconcileIntentBuilder builds the one latest-state reconciliation
// intent for an idle route: full-scope instruction, empty evidence
// manifest, fresh idempotency key (CLI-005: no ambiguous replay).
func (a *reconcileArtifacts) reconcileIntentBuilder() func(routeID, reason, decisionID string, changes []records.ChangeItem) (ports.IntentInput, error) {
	return func(routeID, reason, decisionID string, changes []records.ChangeItem) (ports.IntentInput, error) {
		now := dispatch.Timestamp(time.Now())
		dispatchID := fmt.Sprintf("disp-reconcile-%s-%s", routeID, reason) + "-" + replaceAllClock(now) + "-" + randomSuffixHex()
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
			TargetID: a.targetID, TargetType: a.target.Type, TargetScope: a.target.Board,
			ResourceID: a.resourceID, Generation: 1, IdempotencyKey: key,
			ContentFingerprint: string(contentDigest),
			ManifestDigest:     dispatch.ManifestDigest(changes),
			RequestVersion:     dispatch.RequestContractVersion, RequestJSON: requestJSON,
		}, nil
	}
}

// submitRuntime assembles the submission runtime for --submit.
func (a *reconcileArtifacts) submitRuntime(store storeOp) (*dispatch.Runtime, error) {
	sink, err := resolveSink(a.cfg, a.target, a.route)
	if err != nil {
		return nil, err
	}
	backoff, err := backoffFromConfig(a.route.Dispatch.SubmissionRetry)
	if err != nil {
		return nil, err
	}
	return &dispatch.Runtime{
		Store: store, Sink: sink, Now: time.Now,
		LeaseTTL: time.Minute, Backoff: backoff, JitterUnit: jitterUnit, Actor: "reconcile",
	}, nil
}

// randomSuffixHex keeps same-second reconciliation intents distinct.
func randomSuffixHex() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b[:])
}

func replaceAllClock(now string) string {
	out := make([]byte, 0, len(now))
	for _, c := range now {
		if c != ':' && c != '-' && c != 'T' && c != 'Z' && c != '.' {
			out = append(out, byte(c))
		}
	}
	return string(out)
}

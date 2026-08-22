package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/app/ingest"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// stalenessCheckOf builds the POL-008/SEC-010 pre-submit revalidation
// from the active configuration (E7-T3/H-1): a stored intent is stale
// when its route revision, target identity, or target scope no longer
// matches what the operator currently has configured. The submit paths
// install it on the runtime so no side effect runs under a revoked plan.
func stalenessCheckOf(cfg *config.Config) func(context.Context, ports.IntentSnapshot) (bool, string, error) {
	return func(ctx context.Context, snap ports.IntentSnapshot) (bool, string, error) {
		route, ok := cfg.Routes[snap.RouteID]
		if !ok {
			return true, fmt.Sprintf("route %s removed from the configuration", snap.RouteID), nil
		}
		rev, ok := config.RouteRevision(cfg, snap.RouteID)
		if !ok {
			return true, fmt.Sprintf("route %s revision could not be computed", snap.RouteID), nil
		}
		if rev != snap.RouteRevision {
			return true, fmt.Sprintf("route revision %s (planned under %s)", rev, snap.RouteRevision), nil
		}
		target, ok := cfg.Targets[route.Dispatch.Target]
		if !ok {
			return true, fmt.Sprintf("target %s removed from the configuration", route.Dispatch.Target), nil
		}
		if snap.TargetID != route.Dispatch.Target {
			return true, fmt.Sprintf("target re-pointed to %s (stored %s)", route.Dispatch.Target, snap.TargetID), nil
		}
		if snap.TargetType != target.Type {
			return true, fmt.Sprintf("target type %s (stored %s)", target.Type, snap.TargetType), nil
		}
		if scope := targetScope(target); snap.TargetScope != scope {
			return true, fmt.Sprintf("target scope %s (stored %s)", scope, snap.TargetScope), nil
		}
		return false, "", nil
	}
}

// durableFacts loads the stored path-facts snapshot for one resource as
// the ingest prior-digest port (E7-T3/H-2). A missing or empty snapshot
// degrades to the conservative no-history fallback.
func durableFacts(configPath, resourceID string) (ingest.PathFacts, error) {
	store, err := openStateStore(resolveConfigPath(configPath))
	if err != nil {
		return nil, err
	}
	defer store.Close()
	facts, err := store.LoadPathFacts(context.Background(), resourceID)
	if err != nil {
		return nil, err
	}
	if len(facts) == 0 {
		return ingest.NoFacts{}, nil
	}
	digests := make(ingest.MapFacts, len(facts))
	for path, fact := range facts {
		if fact.Exists {
			digests[path] = records.Digest(fact.Digest)
		}
	}
	return digests, nil
}

// staleRebuilderOf builds the stale-intent rebuilder over the operator
// service: the original is superseded through its declared edge and the
// replacement carries the active revision and target identity.
func staleRebuilderOf(store storeOp, cfg *config.Config) func(context.Context, string) (string, error) {
	op := &dispatch.OperatorService{
		Store: store,
		Now:   func() string { return dispatch.Timestamp(time.Now()) },
		RevisionResolver: func(routeID string) (string, bool) {
			return config.RouteRevision(cfg, routeID)
		},
		TargetResolver: func(routeID string) (string, string, string, bool) {
			route, ok := cfg.Routes[routeID]
			if !ok {
				return "", "", "", false
			}
			target, ok := cfg.Targets[route.Dispatch.Target]
			if !ok {
				return "", "", "", false
			}
			return route.Dispatch.Target, target.Type, targetScope(target), true
		},
	}
	return func(ctx context.Context, dispatchID string) (string, error) {
		return op.RebuildStale(ctx, dispatchID, "agent-dispatch")
	}
}

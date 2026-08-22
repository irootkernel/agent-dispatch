package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// OperatorService implements the explicit operator actions over durable
// dispatches (E3-T3, CLI-004/CLI-005): retry retains the intent and
// idempotency key; rerun creates a new lineage and key.
type OperatorService struct {
	Store OperatorStorePort
	// Now renders the canonical audit timestamp.
	Now func() string
	// TargetScopeResolver supplies the currently configured target
	// scope (the hermes-kanban board) for a route, so a rerun records
	// the scope it will actually be submitted against rather than the
	// predecessor's; nil disables the resolution.
	TargetScopeResolver func(routeID string) string
	// RevisionResolver supplies the currently active route revision
	// (E7-T3/H-1); rerun and stale rebuilds carry it instead of the
	// stored plan's superseded revision. Nil keeps the stored revision.
	RevisionResolver func(routeID string) (string, bool)
	// TargetResolver supplies the currently configured target identity
	// for a route (id, type, scope); a stale rebuild records the target
	// the replacement will actually be submitted against. Nil keeps the
	// stored identity (E7-T3/H-1).
	TargetResolver func(routeID string) (id, targetType, scope string, ok bool)
}

// OperatorStorePort is the durable surface the operator actions need.
type OperatorStorePort interface {
	ports.OperatorStore
	ports.InspectionStore
	ports.DispatchStore
}

// Retry applies the explicit retry: dead-lettered work returns to ready
// with the recorded actor, reason, and a reset attempt budget (DUR-009);
// retry_wait work becomes due immediately, retaining its key and budget.
func (o *OperatorService) Retry(ctx context.Context, dispatchID, actor, reason string) (string, error) {
	lin, err := o.Store.LoadIntentLineage(ctx, dispatchID)
	if err != nil {
		return "", err
	}
	switch lin.Intent.State {
	case "dead_lettered":
		if strings.TrimSpace(reason) == "" {
			return "", fmt.Errorf("retrying dead-lettered dispatch %s requires --reason", dispatchID)
		}
		if err := o.Store.ApplyOperatorRetry(ctx, dispatchID, actor, reason, o.Now()); err != nil {
			return "", err
		}
		return "ready", nil
	case "retry_wait":
		if err := o.Store.MakeRetryDue(ctx, dispatchID, o.Now()); err != nil {
			return "", err
		}
		return "retry_wait", nil
	default:
		return "", fmt.Errorf("dispatch %s is %s; retry requires dead_lettered or retry_wait", dispatchID, lin.Intent.State)
	}
}

// Rerun creates the intentional new work request: a new dispatch ID,
// generation, and idempotency key under one superseding decision. The
// activation evidence (manifest, fingerprint) is retained; only the
// identity fields change. The original must be ready or dead-lettered:
// in-flight work (submitting, unknown, reconciling) must be recovered
// and reconciled first, retry_wait work belongs to retry, and accepted
// or otherwise terminal work keeps its authoritative lineage — a rerun
// may never create a second authoritative task beside live work
// (CON-001, E7-T2/B-2).
func (o *OperatorService) Rerun(ctx context.Context, dispatchID, actor, reason string) (ports.IntentSummary, error) {
	var zero ports.IntentSummary
	if strings.TrimSpace(reason) == "" {
		return zero, fmt.Errorf("rerun requires --reason")
	}
	snap, err := o.Store.LoadIntent(ctx, dispatchID)
	if err != nil {
		return zero, err
	}
	switch snap.State {
	case records.IntentReady, records.IntentDeadLettered:
		// The only supersede-eligible shapes: the store transitions the
		// original to superseded through its declared edge in the same
		// transaction that creates the rerun.
	case records.IntentSubmitting, records.IntentUnknown, records.IntentReconciling:
		return zero, fmt.Errorf("dispatch %s is %s; recover and reconcile it first (dispatches drain resolves expired leases and unknown work)", dispatchID, snap.State)
	case records.IntentRetryWait:
		return zero, fmt.Errorf("dispatch %s is retry_wait; use dispatches retry when it is due instead of rerunning it", dispatchID)
	default:
		return zero, fmt.Errorf("dispatch %s is %s; its lineage is authoritative and cannot be superseded by a rerun", dispatchID, snap.State)
	}
	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(snap.RequestJSON), &req); err != nil {
		return zero, fmt.Errorf("stored request is not the task contract shape: %w", err)
	}
	now := o.Now()
	newDispatchID := fmt.Sprintf("%s-rerun-%s", dispatchID, strings.ReplaceAll(strings.ReplaceAll(now, ":", ""), "-", ""))
	revision := o.activeRevision(snap.RouteID, req.Route.Revision)
	next, _, err := BuildRequest(RequestInput{
		DispatchID:         newDispatchID,
		Route:              ports.TaskRouteRef{ID: snap.RouteID, Revision: revision},
		Resource:           req.Resource,
		TargetID:           snap.TargetID,
		Generation:         snap.Generation + 1,
		Fingerprint:        records.Digest(req.Activation.ContentFingerprint),
		Changes:            manifestToChanges(req.Activation.Manifest),
		Flags:              req.Activation.Flags,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Assignment:         req.Assignment,
		ExecutionHints:     req.ExecutionHints,
	})
	if err != nil {
		return zero, fmt.Errorf("rerun request: %v", err)
	}
	requestJSON, err := MarshalRequest(next)
	if err != nil {
		return zero, err
	}
	return o.Store.RerunIntent(ctx, ports.RerunInput{
		OriginalDispatchID: dispatchID,
		Actor:              actor,
		Reason:             reason,
		New: ports.IntentInput{
			DispatchID: newDispatchID, DecisionID: "", RouteID: snap.RouteID, RouteRevision: revision, TargetScope: snap.TargetScope,
			TargetID: snap.TargetID, TargetType: snap.TargetType, ResourceID: req.Resource.ID,
			Generation: next.Activation.Generation, IdempotencyKey: next.IdempotencyKey,
			ContentFingerprint: next.Activation.ContentFingerprint,
			ManifestDigest:     snap.ManifestDigest,
			RequestVersion:     RequestContractVersion, RequestJSON: requestJSON, CreatedAt: now,
		},
	})
}

// RebuildStale replaces one stale ready intent under the active route
// revision and target identity (POL-008/SEC-010, E7-T3/H-1): the
// original is superseded through its declared edge in the same
// transaction and the replacement carries the current configuration, so
// a submission never runs under a revoked revision or against a moved
// target while recording false lineage.
func (o *OperatorService) RebuildStale(ctx context.Context, dispatchID, actor string) (string, error) {
	snap, err := o.Store.LoadIntent(ctx, dispatchID)
	if err != nil {
		return "", err
	}
	if snap.State != records.IntentReady {
		return "", fmt.Errorf("%w: stale dispatch %s is %s; rebuild requires ready work", ports.ErrStateNotEligible, dispatchID, snap.State)
	}
	// A route or target that left the configuration cannot be rebuilt
	// under it: fail closed for the operator instead of producing a
	// replacement that would itself be stale (round-1 remediation).
	if o.RevisionResolver != nil {
		if _, ok := o.RevisionResolver(snap.RouteID); !ok {
			return "", fmt.Errorf("route %s has no active revision to rebuild under", snap.RouteID)
		}
	}
	if o.TargetResolver != nil {
		if _, _, _, ok := o.TargetResolver(snap.RouteID); !ok {
			return "", fmt.Errorf("route %s has no active target to rebuild against", snap.RouteID)
		}
	}
	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(snap.RequestJSON), &req); err != nil {
		return "", fmt.Errorf("stored request is not the task contract shape: %w", err)
	}
	now := o.Now()
	newDispatchID := fmt.Sprintf("%s-rebuilt-%s", dispatchID, strings.ReplaceAll(strings.ReplaceAll(now, ":", ""), "-", ""))
	revision := o.activeRevision(snap.RouteID, req.Route.Revision)
	targetID, targetType, scope := snap.TargetID, snap.TargetType, snap.TargetScope
	if o.TargetResolver != nil {
		if id, typ, sc, ok := o.TargetResolver(snap.RouteID); ok {
			targetID, targetType, scope = id, typ, sc
		}
	}
	next, _, err := BuildRequest(RequestInput{
		DispatchID:         newDispatchID,
		Route:              ports.TaskRouteRef{ID: snap.RouteID, Revision: revision},
		Resource:           req.Resource,
		TargetID:           targetID,
		Generation:         snap.Generation + 1,
		Fingerprint:        records.Digest(req.Activation.ContentFingerprint),
		Changes:            manifestToChanges(req.Activation.Manifest),
		Flags:              req.Activation.Flags,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Assignment:         req.Assignment,
		ExecutionHints:     req.ExecutionHints,
	})
	if err != nil {
		return "", fmt.Errorf("rebuild request: %v", err)
	}
	requestJSON, err := MarshalRequest(next)
	if err != nil {
		return "", err
	}
	summary, err := o.Store.RerunIntent(ctx, ports.RerunInput{
		OriginalDispatchID: dispatchID,
		Actor:              actor,
		Reason:             "stored plan invalidated by the active route revision or target",
		ReasonCodes:        []string{string(state.ReasonRouteInvalidated)},
		New: ports.IntentInput{
			DispatchID: newDispatchID, DecisionID: "", RouteID: snap.RouteID, RouteRevision: revision, TargetScope: scope,
			TargetID: targetID, TargetType: targetType, ResourceID: req.Resource.ID,
			Generation: next.Activation.Generation, IdempotencyKey: next.IdempotencyKey,
			ContentFingerprint: next.Activation.ContentFingerprint,
			ManifestDigest:     snap.ManifestDigest,
			RequestVersion:     RequestContractVersion, RequestJSON: requestJSON, CreatedAt: now,
		},
	})
	if err != nil {
		return "", err
	}
	return summary.DispatchID, nil
}

// activeRevision resolves the route's current revision, falling back to
// the stored one when no resolver is wired (E7-T3/H-1).
func (o *OperatorService) activeRevision(routeID, stored string) string {
	if o.RevisionResolver != nil {
		if rev, ok := o.RevisionResolver(routeID); ok && rev != "" {
			return rev
		}
	}
	return stored
}

// manifestToChanges projects the stored activation manifest back into
// canonical change items for the request builder.
func manifestToChanges(manifest []ports.TaskManifestItem) []records.ChangeItem {
	items := make([]records.ChangeItem, 0, len(manifest))
	for _, m := range manifest {
		op, err := records.ParseOperation(m.Operation)
		if err != nil {
			continue
		}
		items = append(items, records.ChangeItem{
			Path:         m.Path,
			Operation:    op,
			ExistsAfter:  op != records.OpDelete,
			FileType:     records.FileRegular,
			BeforeDigest: records.Digest(m.BeforeDigest),
			AfterDigest:  records.Digest(m.AfterDigest),
			DigestStatus: records.DigestKnown,
		})
	}
	return items
}

package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// OperatorService implements the explicit operator actions over durable
// dispatches (E3-T3, CLI-004/CLI-005): retry retains the intent and
// idempotency key; rerun creates a new lineage and key.
type OperatorService struct {
	Store OperatorStorePort
	// Now renders the canonical audit timestamp.
	Now func() string
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
// identity fields change.
func (o *OperatorService) Rerun(ctx context.Context, dispatchID, actor, reason string) (ports.IntentSummary, error) {
	var zero ports.IntentSummary
	if strings.TrimSpace(reason) == "" {
		return zero, fmt.Errorf("rerun requires --reason")
	}
	snap, err := o.Store.LoadIntent(ctx, dispatchID)
	if err != nil {
		return zero, err
	}
	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(snap.RequestJSON), &req); err != nil {
		return zero, fmt.Errorf("stored request is not the task contract shape: %w", err)
	}
	now := o.Now()
	newDispatchID := fmt.Sprintf("%s-rerun-%s", dispatchID, strings.ReplaceAll(strings.ReplaceAll(now, ":", ""), "-", ""))
	next, _, err := BuildRequest(RequestInput{
		DispatchID:         newDispatchID,
		Route:              req.Route,
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
			DispatchID: newDispatchID, DecisionID: "", RouteID: snap.RouteID, RouteRevision: req.Route.Revision, TargetScope: snap.TargetScope,
			TargetID: snap.TargetID, TargetType: snap.TargetType, ResourceID: req.Resource.ID,
			Generation: next.Activation.Generation, IdempotencyKey: next.IdempotencyKey,
			ContentFingerprint: next.Activation.ContentFingerprint,
			ManifestDigest:     snap.ManifestDigest,
			RequestVersion:     RequestContractVersion, RequestJSON: requestJSON, CreatedAt: now,
		},
	})
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

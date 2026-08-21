// Package receipts implements the acceptance-receipt and
// execution-projection service (E4-T4, HER-008, OPS-002): refreshing a
// dispatch's target execution status into a durable execution-projection
// receipt without ever conflating it with the acceptance evidence, and
// the stale-active detection that warns instead of auto-failing.
package receipts

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rootkernel/jjukkumi/internal/domain/ids"
	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// Store is the durable surface the receipt service needs.
type Store interface {
	ports.ReceiptStore
	ports.DispatchStore
	ports.InspectionStore
}

// Service refreshes execution projections for accepted dispatches.
type Service struct {
	Store Store
	Sink  ports.Sink
	// Now is the wall clock; tests inject a deterministic one.
	Now func() time.Time
}

// RefreshResult reports one refresh outcome.
type RefreshResult struct {
	DispatchID  string                    `json:"dispatch_id"`
	ExternalRef string                    `json:"external_ref"`
	Projection  ports.ExecutionProjection `json:"projection"`
	ReceiptID   string                    `json:"receipt_id"`
	// UnavailableReason explains a projection whose state is
	// unavailable (HER-008: acceptance and execution are separate; an
	// accepted task may have no derivable execution).
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

// Refresh re-reads the target execution for one dispatch and persists
// the execution-projection receipt. Only a dispatch with a recorded
// external reference can be refreshed; a projection that cannot be
// derived is persisted as unavailable, never as success; a transport
// failure persists nothing and returns the error.
func (s *Service) Refresh(ctx context.Context, dispatchID string) (RefreshResult, error) {
	var out RefreshResult
	out.DispatchID = dispatchID
	intent, err := s.Store.LoadIntent(ctx, dispatchID)
	if err != nil {
		return out, err // typed port errors (missing dispatch) flow through
	}
	if intent.ExternalRef == "" {
		return out, &NoReferenceError{DispatchID: dispatchID}
	}
	out.ExternalRef = intent.ExternalRef
	projection, err := s.Sink.GetExecution(ctx, intent.ExternalRef)
	if err != nil {
		if projection.State != records.ExecUnavailable {
			// A transport or capability failure carries no projection:
			// persist nothing and surface the error (nothing is
			// emulated). A sink that cannot project execution at all
			// reports the typed capability error here.
			return out, err
		}
		// The sink returned the unknown projection (for example a
		// malformed target status): acceptance and execution stay
		// separate, and unavailable is persisted with the reason —
		// never success.
		out.UnavailableReason = err.Error()
	}
	out.Projection = projection
	receivedAt := s.Now()
	received := Timestamp(receivedAt)
	// One receipt per refresh: the projection history stays inspectable
	// (never overwritten), newest first on the list surface. The id
	// carries the nanosecond clock plus a random suffix so refreshes
	// stay unique even across a backward clock step; the stored
	// received_at keeps the canonical second precision.
	out.ReceiptID = "rcpt-exec-" + dispatchID + "-" + receivedAt.UTC().Format("20060102T150405.000000000") + "-" + ids.RandomSuffix()
	payload, _ := json.Marshal(map[string]string{
		"external_ref": projection.ExternalRef,
		"projection":   string(projection.State),
		"observed_at":  projection.TargetObservedAt,
	})
	if out.UnavailableReason != "" {
		payload, _ = json.Marshal(map[string]string{
			"external_ref": projection.ExternalRef,
			"projection":   string(projection.State),
			"observed_at":  projection.TargetObservedAt,
			"reason":       out.UnavailableReason,
		})
	}
	if err := s.Store.SaveExecutionProjection(ctx, ports.ExecutionProjectionInput{
		ReceiptID:        out.ReceiptID,
		DispatchID:       dispatchID,
		ExecutionState:   projection.State,
		ExternalRef:      projection.ExternalRef,
		TargetObservedAt: projection.TargetObservedAt,
		ReceivedAt:       received,
		BoundedPayload:   string(payload),
	}); err != nil {
		return out, &PersistError{Err: err}
	}
	return out, nil
}

// PersistError reports a local persistence failure during refresh: a
// storage-class outcome, never a target failure.
type PersistError struct{ Err error }

func (e *PersistError) Error() string { return "persisting the execution projection: " + e.Err.Error() }
func (e *PersistError) Unwrap() error { return e.Err }

// NoReferenceError reports a dispatch whose execution cannot be
// refreshed because no external reference was recorded: a local state
// conflict, never a target failure.
type NoReferenceError struct{ DispatchID string }

func (e *NoReferenceError) Error() string {
	return fmt.Sprintf("dispatch %s has no external reference to project; execution refresh requires an accepted task", e.DispatchID)
}

// StaleActive reports whether a route's active dispatch has exceeded
// the configured stale window; callers warn and never auto-fail
// (E4-T4 acceptance).
func StaleActive(activeSince, now string, staleAfter time.Duration) bool {
	started, err := time.Parse(time.RFC3339, activeSince)
	if err != nil {
		return false
	}
	current, err := time.Parse(time.RFC3339, now)
	if err != nil {
		return false
	}
	return current.Sub(started) > staleAfter
}

// randomSuffix renders four random bytes as hex, making receipt ids
// unique by construction rather than by wall-clock trust.
// Timestamp renders the canonical UTC RFC 3339 second-precision form.
func Timestamp(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

package ports

import (
	"context"
	"errors"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// Read-side and operator-action surface for the dispatch inspection and
// action commands (E3-T3, CLI-004 through CLI-008).

// IntentFilter selects intents for listing.
type IntentFilter struct {
	RouteID    string
	State      records.IntentState
	TargetID   string
	DispatchID string
	Limit      int
}

// IntentSummary is one listed dispatch intent row.
type IntentSummary struct {
	DispatchID     string              `json:"dispatch_id"`
	RouteID        string              `json:"route_id"`
	TargetID       string              `json:"target_id"`
	Generation     int64               `json:"generation"`
	IdempotencyKey string              `json:"idempotency_key"`
	State          records.IntentState `json:"state"`
	AttemptCount   int                 `json:"attempt_count"`
	NextAttemptAt  string              `json:"next_attempt_at"`
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
}

// AttemptRecord is one dispatch attempt row.
type AttemptRecord struct {
	AttemptID      string `json:"attempt_id"`
	DispatchID     string `json:"dispatch_id"`
	LeaseOwner     string `json:"lease_owner"`
	StartedAt      string `json:"started_at"`
	CompletedAt    string `json:"completed_at"`
	Outcome        string `json:"outcome"`
	ErrorCode      string `json:"error_code"`
	ResponseDigest string `json:"response_digest"`
	Diagnostic     string `json:"diagnostic"`
}

// ReceiptRecord is one dispatch receipt row.
type ReceiptRecord struct {
	ReceiptID        string                  `json:"receipt_id"`
	DispatchID       string                  `json:"dispatch_id"`
	ReceiptKind      string                  `json:"receipt_kind"`
	AcceptanceState  records.AcceptanceState `json:"acceptance_state"`
	ExecutionState   records.ExecutionState  `json:"execution_state"`
	Durable          bool                    `json:"durable"`
	ExternalRef      string                  `json:"external_ref"`
	TargetObservedAt string                  `json:"target_observed_at"`
	ReceivedAt       string                  `json:"received_at"`
}

// TransitionRecord is one audit history row.
type TransitionRecord struct {
	TransitionID string `json:"transition_id"`
	EntityType   string `json:"entity_type"`
	EntityID     string `json:"entity_id"`
	FromState    string `json:"from_state"`
	ToState      string `json:"to_state"`
	RecordedAt   string `json:"recorded_at"`
	ContextJSON  string `json:"context_json"`
}

// IntentLineage is the full inspectable lineage of one dispatch.
type IntentLineage struct {
	Intent      IntentSummary
	Attempts    []AttemptRecord
	Receipts    []ReceiptRecord
	Transitions []TransitionRecord
}

// BatchEvidence is a retained batch with its normalized changes, for
// reprocessing against the current policy.
type BatchEvidence struct {
	BatchID            string
	RouteID            string
	RouteRevision      string
	ResourceID         string
	CreatedAt          string
	ContentFingerprint string
	Changes            []ObservationChange
}

// InspectionStore is the read side the dispatches commands consume.
type InspectionStore interface {
	// ListIntents returns intents matching the filter, newest first.
	ListIntents(ctx context.Context, f IntentFilter) ([]IntentSummary, error)
	// LoadIntentLineage returns the full redacted lineage of one dispatch.
	LoadIntentLineage(ctx context.Context, dispatchID string) (IntentLineage, error)
	// LoadBatchEvidence returns one retained batch and its changes.
	LoadBatchEvidence(ctx context.Context, batchID string) (BatchEvidence, error)
}

// ReceiptFilter bounds one receipts list query.
type ReceiptFilter struct {
	DispatchID string
	RouteID    string
	Kind       string // acceptance | execution_projection | work; empty lists all
	Limit      int
}

// ReceiptDetail is one receipt with its bounded persisted payload
// (already redacted at the source; OPS-002). The work-receipt fields
// apply to kind work only.
type ReceiptDetail struct {
	ReceiptRecord
	BoundedPayload  string `json:"bounded_payload"`
	RunID           string `json:"run_id,omitempty"`
	ResourceID      string `json:"resource_id,omitempty"`
	FailureCode     string `json:"failure_code,omitempty"`
	ValidationState string `json:"validation_state,omitempty"`
}

// ReceiptStore is the receipt repository surface (E4-T4): durable
// execution-projection receipts plus the inspectable receipt list and
// detail behind `receipts list|show` (OPS-002). Each refresh appends a
// new projection receipt so the projection history stays inspectable.
type ReceiptStore interface {
	// SaveExecutionProjection appends one execution-projection receipt
	// for a dispatch (HER-008: separate from acceptance; one per
	// refresh, never overwriting history).
	SaveExecutionProjection(ctx context.Context, in ExecutionProjectionInput) error
	// ListReceipts returns receipts matching the filter, newest first.
	// Kind "work" reads the work-receipt table; an empty kind unions
	// dispatch receipts and work receipts.
	ListReceipts(ctx context.Context, f ReceiptFilter) ([]ReceiptRecord, error)
	// LoadReceipt returns one receipt with its bounded payload.
	// ErrReceiptNotFound reports an unknown receipt id.
	LoadReceipt(ctx context.Context, receiptID string) (ReceiptDetail, error)
}

// ErrReceiptNotFound reports that no receipt carries the given id.
var ErrReceiptNotFound = errors.New("receipt not found")

// ExecutionProjectionInput is one persisted execution projection.
type ExecutionProjectionInput struct {
	ReceiptID        string
	DispatchID       string
	ExecutionState   records.ExecutionState
	ExternalRef      string
	TargetObservedAt string
	ReceivedAt       string
	BoundedPayload   string
}

// OperatorStore is the operator-action surface (explicit retry, due
// marking, route activation).
type OperatorStore interface {
	// ApplyOperatorRetry performs the explicit operator retry of a
	// dead-lettered dispatch: dead_lettered -> ready with the actor and
	// reason recorded (DUR-009). The attempt budget resets; the request
	// and idempotency key are retained.
	ApplyOperatorRetry(ctx context.Context, dispatchID, actor, reason string, now string) error
	// MakeRetryDue makes a retry_wait dispatch eligible immediately; the
	// idempotency key and budget are retained.
	MakeRetryDue(ctx context.Context, dispatchID string, now string) error
	// RerunIntent persists an intentional new work request built by the
	// caller (new dispatch ID, generation, and idempotency key) under one
	// new decision superseding the original lineage (CLI-005 posture: no
	// ambiguous replay).
	RerunIntent(ctx context.Context, rerun RerunInput) (IntentSummary, error)
}

// RerunInput is one intentional rerun persistence unit.
type RerunInput struct {
	OriginalDispatchID string
	OriginalDecisionID string
	// New is the fully built intent: the caller derived the new
	// generation, idempotency key, and self-contained request JSON.
	New    IntentInput
	Actor  string
	Reason string
}

// ReconcileStore extends the durable surface with the unknown-resolution
// workflow (DUR-006): lookup before another submission.
type ReconcileStore interface {
	// ReconcileUnknown moves an unknown dispatch through reconciling and
	// applies the lookup result with domain-guarded evidence in one
	// transaction, returning the resulting state.
	ReconcileUnknown(ctx context.Context, dispatchID, actor string, lookup LookupResult, attemptsExhausted bool, now string) (records.IntentState, error)
}

// ErrRouteDisabled reports the route's activation state prevents new
// submissions (route disable semantics).
var ErrRouteDisabled = errors.New("route activation state prevents submissions")

// ErrStateNotEligible reports the dispatch's current state does not
// permit the requested operator action.
var ErrStateNotEligible = errors.New("dispatch state not eligible for action")

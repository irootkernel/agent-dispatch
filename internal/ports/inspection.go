package ports

import (
	"context"
	"encoding/json"
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
	// OlderThan restricts the listing to intents created strictly before
	// this canonical timestamp (the age filter, E7-T5).
	OlderThan string
	// ExternalRef restricts the listing to intents carrying this stored
	// external task reference (E7-T5).
	ExternalRef string
	// CausalPrefix restricts the listing to intents whose dispatch or
	// decision ID starts with this prefix (the causal-ID filter, E7-T5).
	CausalPrefix string
	// Offset skips the first Offset rows of the ordered listing
	// (pagination, E7-T5).
	Offset int
}

// Record schema versions the CLI emits beside their records
// (docs/schemas/*.schema.json; E9-T1/M-16).
const (
	IntentRecordSchemaVersion     = "agent-dispatch.dispatch-intent/v1"
	AttemptRecordSchemaVersion    = "agent-dispatch.dispatch-attempt/v1"
	ReceiptRecordSchemaVersion    = "agent-dispatch.dispatch-receipt/v1"
	DeadLetterRecordSchemaVersion = "agent-dispatch.dead-letter-record/v1"
)

// IntentSummary is one listed dispatch intent row. Every required member
// of the published dispatch-intent schema is emitted (E9-T1/M-16).
type IntentSummary struct {
	SchemaVersion string `json:"schema_version"`
	DispatchID    string `json:"dispatch_id"`
	DecisionID    string `json:"decision_id"`
	RouteID       string `json:"-"`
	RouteRevision string `json:"-"`
	// Route is the schema's {id, revision} object (dispatch-intent
	// schema-required member shape).
	Route              RouteRef            `json:"route"`
	TargetID           string              `json:"target_id"`
	ResourceID         string              `json:"resource_id"`
	Generation         int64               `json:"generation"`
	IdempotencyKey     string              `json:"idempotency_key"`
	ContentFingerprint string              `json:"content_fingerprint"`
	State              records.IntentState `json:"state"`
	// Request is the stored task-request document passed through as the
	// object the published schema requires — never a JSON string (E9-T1
	// audit F001, reconciled by the E9 validation).
	Request json.RawMessage `json:"request"`
	// AggregateID and DestinationID surface the child linkage (E12-T1,
	// FAN-010 posture): the aggregate event the intent hangs beneath and
	// the destination lane it belongs to. Empty marks the pre-cutover
	// legacy shape (DAT-012's historical intents carry no child row).
	AggregateID   string `json:"aggregate_id"`
	DestinationID string `json:"destination_id"`
	// Operator convenience members the published schema's
	// additionalProperties: false excludes; internal-only (E9-T1 audit
	// F002).
	AttemptCount  int    `json:"-"`
	NextAttemptAt string `json:"-"`
	UpdatedAt     string `json:"-"`
	CreatedAt     string `json:"created_at"`
}

// RouteRef is the schema's route{id, revision} object.
type RouteRef struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

// AttemptRecord is one dispatch attempt row (schema-conformant:
// schema_version first, E9-T1/M-16).
type AttemptRecord struct {
	SchemaVersion  string  `json:"schema_version"`
	AttemptID      string  `json:"attempt_id"`
	DispatchID     string  `json:"dispatch_id"`
	LeaseOwner     string  `json:"lease_owner"`
	StartedAt      string  `json:"started_at"`
	CompletedAt    *string `json:"completed_at"`
	Outcome        string  `json:"outcome"`
	ErrorCode      *string `json:"error_code"`
	ResponseDigest *string `json:"response_digest"`
	Diagnostic     *string `json:"diagnostic"`
}

// ReceiptRecord is one dispatch receipt row (schema-conformant:
// schema_version first, E9-T1/M-16). The enum members drop when unset
// and the nullable members render null, never empty strings (E9-T1
// audit F007, reconciled by the E9 validation).
type ReceiptRecord struct {
	SchemaVersion    string                  `json:"schema_version"`
	ReceiptID        string                  `json:"receipt_id"`
	DispatchID       string                  `json:"dispatch_id"`
	ReceiptKind      string                  `json:"receipt_kind"`
	AcceptanceState  records.AcceptanceState `json:"acceptance_state,omitempty"`
	ExecutionState   records.ExecutionState  `json:"execution_state,omitempty"`
	Durable          bool                    `json:"durable"`
	ExternalRef      *string                 `json:"external_ref"`
	TargetObservedAt *string                 `json:"target_observed_at"`
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

// IntentLineage is the full inspectable lineage of one dispatch,
// including the causal decision, its retained batch, the batch's source
// observations, and the cooperative work receipts (OPS-002, E7-T5).
type IntentLineage struct {
	Intent      IntentSummary        `json:"intent"`
	Attempts    []AttemptRecord      `json:"attempts"`
	Receipts    []ReceiptRecord      `json:"receipts"`
	Transitions []TransitionRecord   `json:"transitions"`
	Decision    *DecisionLineage     `json:"decision,omitempty"`
	WorkReceipt []WorkReceiptLineage `json:"work_receipt,omitempty"`
	// DeadLetter carries the published dead-letter-record view when the
	// dispatch is dead-lettered (schema-required members derived from the
	// lineage; nil otherwise — E9-T1/M-16).
	DeadLetter *DeadLetterRecord `json:"dead_letter_record,omitempty"`
}

// DeadLetterRecord is the dead-letter view of a dispatch lineage
// (docs/schemas/dead-letter-record.schema.json).
type DeadLetterRecord struct {
	SchemaVersion    string              `json:"schema_version"`
	DispatchID       string              `json:"dispatch_id"`
	RouteID          string              `json:"route_id"`
	TargetID         string              `json:"target_id"`
	IdempotencyKey   string              `json:"idempotency_key"`
	State            records.IntentState `json:"state"`
	AttemptCount     int                 `json:"attempt_count"`
	DeadLetterReason string              `json:"dead_letter_reason"`
	Attempts         []AttemptRecord     `json:"attempts"`
	CreatedAt        string              `json:"created_at"`
}

// DecisionLineage is the decision that created the dispatch with its
// retained batch and that batch's source observations.
type DecisionLineage struct {
	DecisionID  string        `json:"decision_id"`
	RouteID     string        `json:"route_id"`
	Revision    string        `json:"route_revision"`
	Disposition string        `json:"disposition"`
	Class       string        `json:"classification"`
	ReasonCodes string        `json:"reason_codes"`
	Actor       string        `json:"actor"`
	CreatedAt   string        `json:"created_at"`
	Batch       *BatchLineage `json:"batch,omitempty"`
}

// BatchLineage is the retained batch behind one decision.
type BatchLineage struct {
	BatchID      string               `json:"batch_id"`
	CreatedAt    string               `json:"created_at"`
	Fingerprint  string               `json:"content_fingerprint"`
	Observations []ObservationLineage `json:"observations"`
}

// ObservationLineage is one source observation row in the causal chain.
type ObservationLineage struct {
	ObservationID string `json:"observation_id"`
	SourceID      string `json:"source_id"`
	ObservedAt    string `json:"observed_at"`
	Status        string `json:"ingest_status"`
}

// WorkReceiptLineage is one cooperative work receipt over the dispatch.
type WorkReceiptLineage struct {
	ReceiptID   string `json:"receipt_id"`
	RunID       string `json:"run_id"`
	Status      string `json:"status"`
	FailureCode string `json:"failure_code,omitempty"`
	SubmittedAt string `json:"submitted_at"`
	BegunAt     string `json:"begun_at"`
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
	// MakeRetryDue makes a retry_wait dispatch eligible immediately and
	// resets its attempt budget in one audited transaction (E8-T2, M-2:
	// the explicit retry is the operator exit for a budget-exhausted
	// wait); the idempotency key is retained.
	MakeRetryDue(ctx context.Context, dispatchID, actor, now string) error
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
	// ReasonCodes override the superseding decision's reason codes; nil
	// keeps the operator_rerun default (E7-T3/H-1: stale rebuilds record
	// route_revision_invalidated).
	ReasonCodes []string
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

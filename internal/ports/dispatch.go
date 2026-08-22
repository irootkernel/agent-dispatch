package ports

import (
	"context"
	"errors"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
)

// DispatchStore is the durable surface the dispatch runtime consumes
// (E3-T2). Every method opens and completes its own transaction before
// returning, so no store transaction is ever open across a sink call
// (DUR-001, ADR-0005). All timestamp strings are canonical UTC RFC 3339
// at second precision.
type DispatchStore interface {
	// CommitLineage persists one observation-to-intent lineage and
	// reserves the route's active slot in a single transaction: the
	// committed intent exists before any target invocation (DUR-002) and
	// the target/idempotency uniqueness constraint is enforced (DUR-012
	// posture). It returns ErrIdempotencyConflict on a duplicate
	// (target, idempotency key) pair and ErrRouteSlotHeld when the route
	// already has an active dispatch.
	CommitLineage(ctx context.Context, lin Lineage) error

	// LoadIntent returns the durable snapshot of one dispatch intent.
	LoadIntent(ctx context.Context, dispatchID string) (IntentSnapshot, error)

	// AcquireAttempt conditionally leases the intent to one owner and
	// opens the attempt row in one transaction (DUR-012): the intent must
	// be ready or retry_wait, the next attempt must be due, and any
	// previous lease expired. Exactly one competing process succeeds; the
	// others receive ErrLeaseHeld.
	AcquireAttempt(ctx context.Context, req AcquireAttempt) (string, error)

	// CompleteAttempt atomically records the attempt outcome, an optional
	// acceptance receipt, and the validated intent transition with its
	// audit entry (DUR-011). The transition must satisfy the domain state
	// machine's guards for the given reason and evidence.
	CompleteAttempt(ctx context.Context, res AttemptResult) error

	// RecoverExpiredSubmitting moves every submitting intent on one route
	// whose lease expired at now to unknown with audit evidence and closes
	// its open attempt row (persistence §5: an abandoned submitting state
	// defaults to unknown, never to success or failure). An empty routeID
	// sweeps the whole store.
	RecoverExpiredSubmitting(ctx context.Context, routeID, now string) ([]RecoveredLease, error)
}

// Lineage is the full observation-to-intent persistence unit of one
// ingestion transaction (persistence §7).
type Lineage struct {
	Observation ObservationInput
	Batch       BatchInput
	Decision    DecisionInput
	Intent      IntentInput
}

// ObservationInput is one immutable source delivery.
type ObservationInput struct {
	ObservationID    string
	SchemaVersion    string
	SourceType       string
	SourceID         string
	SourceEventKey   string
	TriggerName      string
	ResourceID       string
	ObservedAt       string
	ReceivedAt       string
	RawPayloadDigest string
	IngestStatus     string
	FlagsJSON        string
	Changes          []ObservationChange
}

// ObservationChange is one normalized path evidence row.
type ObservationChange struct {
	Ordinal      int
	Path         string
	Operation    string
	ExistsAfter  bool
	FileType     string
	BeforeDigest string
	AfterDigest  string
	DigestStatus string
}

// BatchInput is the canonical policy unit.
type BatchInput struct {
	BatchID            string
	RouteID            string
	RouteRevision      string
	ResourceID         string
	CreatedAt          string
	ContentFingerprint string
	ObservationIDs     []string
}

// DecisionInput is the immutable policy decision.
type DecisionInput struct {
	DecisionID            string
	BatchID               string
	RouteID               string
	RouteRevision         string
	PolicyRevision        string
	Disposition           string
	Classification        string
	ReasonCodesJSON       string
	CreatedAt             string
	Actor                 string
	GenerationLineageJSON string
}

// IntentInput is the durable dispatch-intent creation.
type IntentInput struct {
	// TargetScope records the resolved target scope at submission
	// planning time (for hermes-kanban, the board slug): reconciliation
	// proves it still reads the same scope before trusting absence.
	TargetScope        string
	DispatchID         string
	DecisionID         string
	RouteID            string
	RouteRevision      string
	TargetID           string
	TargetType         string
	ResourceID         string
	Generation         int64
	IdempotencyKey     string
	ContentFingerprint string
	ManifestDigest     string
	RequestVersion     string
	RequestJSON        string
	CreatedAt          string
}

// ErrStaleRouteRevision reports a stored intent whose planned route
// revision or target identity is no longer active (POL-008/SEC-010,
// E7-T3/H-1): the intent must be superseded and rebuilt under the
// current configuration, never submitted as stored.
var ErrStaleRouteRevision = errors.New("stored intent is stale against the active configuration")

// IntentSnapshot is the durable state of one dispatch intent.
type IntentSnapshot struct {
	DispatchID     string
	RouteID        string
	RouteRevision  string
	TargetID       string
	TargetScope    string
	TargetType     string
	ResourceID     string
	Generation     int64
	IdempotencyKey string
	State          records.IntentState
	RequestJSON    string
	ManifestDigest string
	ExternalRef    string
	LeaseOwner     string
	LeaseExpiresAt string
	AttemptCount   int
	NextAttemptAt  string
}

// AcquireAttempt is one conditional lease request.
type AcquireAttempt struct {
	DispatchID     string
	AttemptID      string
	Owner          string
	Now            string
	LeaseExpiresAt string
	NextAttemptAt  string
}

// AttemptResult records one completed attempt and the transition it
// justifies.
type AttemptResult struct {
	AttemptID      string
	DispatchID     string
	Outcome        string // dispatch_attempts outcome: accepted|rejected|unknown|transport_failure
	ErrorCode      string
	ResponseDigest string
	Diagnostic     string
	CompletedAt    string
	// NextAttemptAt persists the backoff deadline for retryable outcomes
	// (DUR-007); empty leaves the stored value unchanged.
	NextAttemptAt string
	// Transition is the validated intent transition this result carries.
	Transition AttemptTransition
	// Receipt, when non-nil, persists the acceptance evidence durably in
	// the same transaction.
	Receipt *ReceiptInput
}

// AttemptTransition is the domain-validated transition recorded with the
// attempt completion.
type AttemptTransition struct {
	To     records.IntentState
	Reason state.IntentReason
	// Evidence carries the typed proof for the domain guards (lease,
	// reconciliation, explicit retry, receipt reference).
	Evidence state.IntentEvidence
	// TransitionID identifies the audit entry.
	TransitionID string
}

// ReceiptInput is one acceptance receipt persisted with an attempt.
type ReceiptInput struct {
	ReceiptID        string
	Acceptance       records.AcceptanceState
	Durable          bool
	ExternalRef      string
	TargetObservedAt string
	ReceivedAt       string
	PayloadVersion   string
	BoundedPayload   string
}

// RecoveredLease names one recovered abandoned submitting intent.
type RecoveredLease struct {
	DispatchID string
	AttemptID  string
	Owner      string
}

// ErrIdempotencyConflict reports a duplicate (target, idempotency key)
// pair rejected by the uniqueness constraint.
var ErrIdempotencyConflict = errors.New("duplicate target idempotency key")

// ErrRouteSlotHeld reports the route already has one active dispatch
// (CON-001: a route cannot hold two active dispatch IDs).
var ErrRouteSlotHeld = errors.New("route already has an active dispatch")

// ErrLeaseHeld reports another process owns the attempt lease (DUR-012).
var ErrLeaseHeld = errors.New("attempt lease held by another owner")

// ErrIntentNotFound reports no durable intent for the dispatch ID.
var ErrIntentNotFound = errors.New("dispatch intent not found")

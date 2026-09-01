package ports

import (
	"context"
	"errors"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// Durable notification surface (E13-T1, ADR-0019): reportable state
// transitions commit channel-neutral notification intents in the same
// transaction; attempts and outcomes are separate durable records
// (DAT-010, DUR-016, NTF-004). Delivery arrives with E13-T2 and never
// mutates dispatch or work-completion state (NTF-005).

// ErrNotificationNotFound is the stable sentinel for a missing
// notification record.
var ErrNotificationNotFound = errors.New("notification not found")

// ErrDrainRunNotFound is the stable sentinel for a missing drain-run
// evidence row: drain evidence identities are distinct from notification
// identities, so an unknown run never masquerades as a missing
// notification (E16-T1).
var ErrDrainRunNotFound = errors.New("drain run not found")

// DrainRunInput opens one bounded drain pass's evidence row (E16-T1,
// ADR-0022): the deterministic drain identity, the route and trigger the
// pass ran under, and the effective mode it resolved.
type DrainRunInput struct {
	DrainID   string
	RouteID   string
	Trigger   string
	Mode      string
	StartedAt string
}

// DrainRunCounts completes one drain pass's evidence row: the work the
// pass claimed and how it resolved. RetryScheduled counts the
// ambiguous/retryable outcomes whose backoff deadline was persisted;
// BudgetExpired records that the pass hit its wall-clock budget.
type DrainRunCounts struct {
	Claimed        int
	Delivered      int
	Refused        int
	RetryScheduled int
	BudgetExpired  bool
}

// NotificationSinkRef is one configured sink reference of a route's
// effective notification policy: identity and adapter kind only — the
// endpoint and authentication reference stay configuration-owned and
// never enter notification identity (SEC-012).
type NotificationSinkRef struct {
	ID   string
	Type string
}

// NotificationPolicy is one route's effective notification policy at
// evaluation time: the effective event set (the declared list, or the
// NTF-002 defaults when a sink exists and no event list is declared),
// the configured sink references, and the revision digest of exactly
// that effective projection (NTF-003's notification-policy revision).
// A nil policy — or one without sinks — disables notifications (NTF-001).
type NotificationPolicy struct {
	Events   []records.NotificationEventKind
	Sinks    []NotificationSinkRef
	Revision string
}

// NotificationEventRecord is one durable notification intent as stored:
// the channel-neutral payload is the notification-event/v1 projection;
// the CLI owns all presentation JSON. The attempt projection is the
// read-model join the list surface renders.
type NotificationEventRecord struct {
	NotificationID string
	RouteID        string
	Event          records.NotificationEventKind
	DestinationID  string
	Transition     string
	SinkID         string
	SinkType       string
	PolicyRevision string
	PayloadJSON    string
	State          records.NotificationState
	IdempotencyKey string
	CreatedAt      string
	ResolvedAt     string
	// AttemptCount and LastOutcome join the attempt projection for the
	// listing surface (zero and empty when no attempt ran yet).
	AttemptCount int
	LastOutcome  records.NotificationAttemptOutcome
	// Drain-delivery state (E16-T1/E16-T2): the persisted due deadline
	// and the current lease columns. DueAt/lease fields are populated by
	// the claim and direct-load surfaces; the listing projection leaves
	// them zeroed.
	DueAt          string
	LeaseOwner     string
	LeaseToken     int64
	LeaseExpiresAt string
}

// NotificationAttemptInput records one sink delivery attempt (NTF-004,
// NTF-007): the attempt number is the store's sequential assignment; a
// terminal outcome resolves the notification, an ambiguous or retryable
// outcome leaves it pending.
type NotificationAttemptInput struct {
	NotificationID string
	Outcome        records.NotificationAttemptOutcome
	ErrorCode      string
	ResponseDigest string
	StartedAt      string
	CompletedAt    string
}

// NotificationAttemptRecord is one durable attempt as stored.
type NotificationAttemptRecord struct {
	AttemptID      string
	NotificationID string
	AttemptNumber  int
	Outcome        records.NotificationAttemptOutcome
	ErrorCode      string
	ResponseDigest string
	StartedAt      string
	CompletedAt    string
}

// NotificationFilter bounds one notification listing.
type NotificationFilter struct {
	RouteID string
	State   string
	SinkID  string
	Event   records.NotificationEventKind
	Limit   int
}

// NotificationStore is the durable notification inspection and attempt
// surface. Intent creation itself is not part of this interface: it
// joins the owning state-transition transactions (DUR-016) through the
// store's transactional enqueue.
type NotificationStore interface {
	// ListNotifications returns notifications matching the filter,
	// newest first, bounded by the filter's limit.
	ListNotifications(ctx context.Context, filter NotificationFilter) ([]NotificationEventRecord, error)
	// LoadNotification returns one notification by id.
	LoadNotification(ctx context.Context, notificationID string) (NotificationEventRecord, error)
	// ListNotificationAttempts returns one notification's attempts in
	// attempt order.
	ListNotificationAttempts(ctx context.Context, notificationID string) ([]NotificationAttemptRecord, error)
	// RecordNotificationAttempt durably records one delivery attempt and
	// applies its outcome to the notification's state (NTF-005: nothing
	// outside the notification tables changes).
	RecordNotificationAttempt(ctx context.Context, in NotificationAttemptInput) (NotificationAttemptRecord, error)
	// CountNotificationsByState aggregates notification counts by state
	// for the status surface.
	CountNotificationsByState(ctx context.Context) (map[string]int64, error)
	// EnqueueRouteNotification creates the notification intents of one
	// route-scoped reportable transition that has no owning store method
	// (E13-T2: the drift evaluation pass): the same transactional
	// enqueue, dedup identity, and effective-policy filter the store's
	// internal transitions use (OPS-013). The returned count is the
	// number of sink intents the pass created — zero means the effective
	// policy filtered the event, which the caller must report honestly.
	EnqueueRouteNotification(ctx context.Context, routeID string, event records.NotificationEventKind, transition, destinationID string, source map[string]string, now string) (int, error)
	// RetryNotification re-arms one refused notification for delivery
	// (the explicit operator retry): the stable idempotency identity is
	// untouched and a delivered notification never re-arms (NTF-007).
	RetryNotification(ctx context.Context, notificationID string) error
	// PendingNotifications returns the pending delivery work, oldest
	// first, bounded (the drain surface).
	PendingNotifications(ctx context.Context, limit int) ([]NotificationEventRecord, error)
}

// NotificationDelivery is one pending notification bound for one sink:
// the channel-neutral payload is the notification-event/v1 projection
// exactly as stored; the idempotency key is the notification's stable
// delivery identity every attempt reuses (NTF-007).
type NotificationDelivery struct {
	NotificationID string
	RouteID        string
	Event          records.NotificationEventKind
	DestinationID  string
	SinkID         string
	IdempotencyKey string
	PayloadJSON    string
}

// NotificationSink is one channel-neutral notification sink adapter
// (sink-adapter-contract §10, NTF-006): it consumes a
// notification-event/v1 payload and returns a definite success, a
// definite refusal, an ambiguous outcome, or a retryable pre-delivery
// failure — and never mutates any state outside the notification
// tables (NTF-005). Adding a future channel adapter requires no change
// to dispatch or work-completion semantics (NTF-009).
type NotificationSink interface {
	// Deliver performs one bounded delivery attempt.
	Deliver(ctx context.Context, in NotificationDelivery) NotificationAttemptInput
}

// ErrNotificationLeaseLost reports a fenced outcome that arrived after
// another drainer recovered the notification (E16-T2, NTF-012): the
// claim's fencing token no longer matches the stored lease, so the
// stale owner's outcome is refused instead of overwriting the recovery.
var ErrNotificationLeaseLost = errors.New("notification lease lost to a recovering drainer")

// NotificationClaimFilter selects the due work one drain pass claims
// (E16-T2, NTF-011): only pending notifications whose persisted due
// deadline has arrived and whose lease is free or expired, oldest
// first, bounded by Limit, optionally scoped to one route.
type NotificationClaimFilter struct {
	RouteID string
	Limit   int
	Owner   string
	// LeaseUntil is the fence's expiry timestamp (RFC3339): the
	// effective delivery deadline plus the thirty-second margin.
	LeaseUntil string
}

// NotificationClaim is one atomically claimed due notification: the
// stored record plus the fencing token this claim advanced to. The
// token rides every outcome the claiming worker records; a worker that
// lost the claim cannot commit after recovery advanced the token.
type NotificationClaim struct {
	NotificationEventRecord
	LeaseToken int64
}

// NotificationBackoff is the persisted retry envelope one ambiguous or
// retryable outcome applies (NTF-014): the delay for attempt n is
// min(Initial * Multiplier^(n-1), Max), one symmetric ±JitterFraction
// jitter is applied per retry, and the resulting deadline is persisted
// so every process observes the same due time.
type NotificationBackoff struct {
	Initial        time.Duration
	Max            time.Duration
	Multiplier     float64
	JitterFraction float64
}

// NotificationDrainStore is the lease-safe claim and fenced-outcome
// surface the bounded drain service drives (E16-T2): concurrent
// drainers claim disjoint due work, outcomes are fenced by the claim's
// token, and unstarted claims are released at budget expiry.
type NotificationDrainStore interface {
	// ClaimDueNotifications atomically leases the currently due,
	// unclaimed pending work bounded by the filter.
	ClaimDueNotifications(ctx context.Context, filter NotificationClaimFilter) ([]NotificationClaim, error)
	// RecordNotificationAttemptFenced records one delivery outcome
	// under the claim's fence, persists the backoff deadline of an
	// ambiguous or retryable outcome, and releases the lease; a stale
	// owner fails with ErrNotificationLeaseLost.
	RecordNotificationAttemptFenced(ctx context.Context, in NotificationAttemptInput, claim NotificationClaim, backoff NotificationBackoff) (NotificationAttemptRecord, error)
	// ReleaseNotificationClaims releases still-leased claims a pass
	// could not start before its budget expired.
	ReleaseNotificationClaims(ctx context.Context, owner string, claims []NotificationClaim) error
}

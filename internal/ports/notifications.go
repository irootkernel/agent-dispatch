package ports

import (
	"context"
	"errors"

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

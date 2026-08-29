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
// the CLI owns all presentation rendering.
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
}

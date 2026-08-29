// Package notifications owns the sink delivery service of the durable
// notification outbox (E13-T2, ADR-0019): pending intents are delivered
// one bounded attempt at a time through channel-neutral sink adapters,
// every attempt reuses the notification's stable idempotency identity
// (NTF-007), and no delivery outcome ever mutates state outside the
// notification tables (NTF-005).
package notifications

import (
	"context"
	"errors"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Store is the durable surface the delivery service drives.
type Store interface {
	PendingNotifications(ctx context.Context, limit int) ([]ports.NotificationEventRecord, error)
	RecordNotificationAttempt(ctx context.Context, in ports.NotificationAttemptInput) (ports.NotificationAttemptRecord, error)
}

// SinkResolver builds one sink adapter for a route's configured sink
// reference; a construction failure is the configuration class (the
// declaration is defective, never a delivery outcome).
type SinkResolver func(routeID string, sink ports.NotificationSinkRef) (ports.NotificationSink, error)

// SinkConstructionError marks one sink-declaration defect (a plain-http
// endpoint, an unparseable secret reference, an unknown type): the
// configuration class at the CLI boundary. A defective declaration is
// sink-local — it never aborts the other sinks' deliveries.
type SinkConstructionError struct{ Err error }

func (e *SinkConstructionError) Error() string { return e.Err.Error() }
func (e *SinkConstructionError) Unwrap() error { return e.Err }

// Delivery is the bounded drain service.
type Delivery struct {
	Store    Store
	Resolver SinkResolver
	Now      func() time.Time
	// MaxPerRun bounds one drain pass; zero means the default.
	MaxPerRun int
}

// DefaultMaxPerRun bounds one drain pass (the schedule integration's
// one-shot posture: bounded work, visible remainder).
const DefaultMaxPerRun = 100

// DrainReport summarizes one drain pass.
type DrainReport struct {
	Delivered int `json:"delivered"`
	Refused   int `json:"refused"`
	Ambiguous int `json:"ambiguous"`
	Retryable int `json:"retryable"`
}

// Pending reports whether any outcome leaves work for the next pass.
func (r DrainReport) Pending() bool { return r.Ambiguous > 0 || r.Retryable > 0 }

// DeliverOne performs exactly one bounded delivery attempt of one
// notification and durably records its outcome (NTF-004/NTF-005): a
// sink-construction failure returns as *SinkConstructionError for the
// caller to classify; a delivery outcome — any of the four — is data.
func (d *Delivery) DeliverOne(ctx context.Context, rec ports.NotificationEventRecord) (records.NotificationAttemptOutcome, error) {
	sink, err := d.Resolver(rec.RouteID, ports.NotificationSinkRef{ID: rec.SinkID, Type: rec.SinkType})
	if err != nil {
		return "", &SinkConstructionError{Err: err}
	}
	return d.deliverThrough(ctx, rec, sink)
}

// deliverThrough performs the sink delivery and records the attempt.
func (d *Delivery) deliverThrough(ctx context.Context, rec ports.NotificationEventRecord, sink ports.NotificationSink) (records.NotificationAttemptOutcome, error) {
	started := d.now()
	attempt := sink.Deliver(ctx, ports.NotificationDelivery{
		NotificationID: rec.NotificationID,
		RouteID:        rec.RouteID,
		Event:          rec.Event,
		DestinationID:  rec.DestinationID,
		SinkID:         rec.SinkID,
		IdempotencyKey: rec.IdempotencyKey,
		PayloadJSON:    rec.PayloadJSON,
	})
	attempt.NotificationID = rec.NotificationID
	attempt.StartedAt = started
	attempt.CompletedAt = d.now()
	if _, err := d.Store.RecordNotificationAttempt(ctx, attempt); err != nil {
		return "", err
	}
	return attempt.Outcome, nil
}

// DeliverPending drains the pending notifications, oldest first,
// bounded by MaxPerRun: one attempt per notification per pass. An
// ambiguous or retryable outcome leaves the notification pending for
// the next pass under the same stable idempotency identity (NTF-007).
// A defective sink DECLARATION is sink-local: its notifications record
// a retryable attempt and the pass continues — one broken declaration
// never blocks the healthy sinks' deliveries; only a durable-store
// failure aborts the pass.
func (d *Delivery) DeliverPending(ctx context.Context) (DrainReport, error) {
	limit := d.MaxPerRun
	if limit <= 0 {
		limit = DefaultMaxPerRun
	}
	pending, err := d.Store.PendingNotifications(ctx, limit)
	if err != nil {
		return DrainReport{}, err
	}
	report := DrainReport{}
	count := func(outcome records.NotificationAttemptOutcome) {
		switch outcome {
		case records.NotificationDeliveredOutcome:
			report.Delivered++
		case records.NotificationRefusedOutcome:
			report.Refused++
		case records.NotificationAmbiguousOutcome:
			report.Ambiguous++
		case records.NotificationRetryableOutcome:
			report.Retryable++
		}
	}
	for _, rec := range pending {
		outcome, err := d.DeliverOne(ctx, rec)
		if err == nil {
			count(outcome)
			continue
		}
		var construction *SinkConstructionError
		if !errors.As(err, &construction) {
			return report, err
		}
		// The declaration is defective: record the retryable attempt so
		// the notification's evidence shows why it stayed pending, and
		// keep draining the other sinks.
		started := d.now()
		if _, recordErr := d.Store.RecordNotificationAttempt(ctx, ports.NotificationAttemptInput{
			NotificationID: rec.NotificationID,
			Outcome:        records.NotificationRetryableOutcome,
			ErrorCode:      "sink_unresolvable",
			StartedAt:      started,
			CompletedAt:    d.now(),
		}); recordErr != nil {
			return report, recordErr
		}
		report.Retryable++
	}
	return report, nil
}

func (d *Delivery) now() string {
	if d.Now == nil {
		return time.Now().UTC().Format(time.RFC3339)
	}
	return d.Now().UTC().Format(time.RFC3339)
}

// Package notifications owns the sink delivery service of the durable
// notification outbox (E13-T2, ADR-0019): pending intents are delivered
// one bounded attempt at a time through channel-neutral sink adapters,
// every attempt reuses the notification's stable idempotency identity
// (NTF-007), and no delivery outcome ever mutates state outside the
// notification tables (NTF-005).
package notifications

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Store is the durable surface the delivery service drives.
type Store interface {
	PendingNotifications(ctx context.Context, limit int) ([]ports.NotificationEventRecord, error)
	RecordNotificationAttempt(ctx context.Context, in ports.NotificationAttemptInput) (ports.NotificationAttemptRecord, error)
}

// NotificationDrainStore is the lease-safe store surface the drain
// service drives: the minimal claim/fence/release contract (the full
// ports.NotificationDrainStore embeds it with the evidence and
// inspection surfaces).
type NotificationDrainStore interface {
	ClaimDueNotifications(ctx context.Context, filter ports.NotificationClaimFilter) ([]ports.NotificationClaim, error)
	RecordNotificationAttemptFenced(ctx context.Context, in ports.NotificationAttemptInput, claim ports.NotificationClaim, backoff ports.NotificationBackoff) (ports.NotificationAttemptRecord, error)
	ReleaseNotificationClaims(ctx context.Context, owner string, claims []ports.NotificationClaim) error
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

// DrainService is the lease-safe bounded drain pass of E16-T2: one
// service serves the explicit manual drain, the automatic after-command
// pass, and the scheduled runner. Every pass claims only due work
// atomically (concurrent drainers see disjoint records), records fenced
// outcomes, persists the retry backoff of ambiguous and retryable
// outcomes, and releases unstarted claims when its wall budget expires.
type DrainService struct {
	Store    NotificationDrainStore
	Resolver SinkResolver
	Now      func() time.Time
	// Limit bounds one pass; zero means DefaultMaxPerRun.
	Limit int
	// Retry is the backoff envelope persisted on ambiguous/retryable
	// outcomes; zero members fall back to the documented defaults.
	Retry ports.NotificationBackoff
	// Owner identifies this drainer's leases; empty derives a unique
	// per-process owner.
	Owner string
}

func (d *DrainService) limit() int {
	if d.Limit <= 0 {
		return DefaultMaxPerRun
	}
	return d.Limit
}

func (d *DrainService) retry() ports.NotificationBackoff {
	b := d.Retry
	if b.Initial <= 0 {
		b.Initial = 30 * time.Second
	}
	if b.Max <= 0 {
		b.Max = 15 * time.Minute
	}
	if b.Multiplier <= 0 {
		b.Multiplier = 2.0
	}
	return b
}

func (d *DrainService) owner() string {
	if d.Owner != "" {
		return d.Owner
	}
	return fmt.Sprintf("drain-%d-%s", os.Getpid(), randomOwnerSuffix())
}

func randomOwnerSuffix() string {
	buf := make([]byte, 8)
	if _, err := crand.Read(buf); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(buf)
}

func (d *DrainService) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// DrainClaimedReport summarizes one lease-safe drain pass. The four
// outcome counters keep the E13 envelope shape (ambiguous and retryable
// both stay pending, reported separately); Claimed adds the lease-safe
// pass's claim count.
type DrainClaimedReport struct {
	Claimed   int `json:"claimed"`
	Delivered int `json:"delivered"`
	Refused   int `json:"refused"`
	Ambiguous int `json:"ambiguous"`
	Retryable int `json:"retryable"`
	// BudgetExpired records that the pass hit its wall budget; unstarted
	// claims were released, started delivery received cancellation.
	BudgetExpired bool `json:"budget_expired"`
}

// Pending reports whether the pass left retry-scheduled work.
func (r DrainClaimedReport) Pending() bool { return r.Ambiguous > 0 || r.Retryable > 0 }

// DrainDue performs one bounded lease-safe pass over the due work
// (optionally route-scoped). The caller's context deadline is the wall
// budget: at expiry, unstarted claims are released and the pass stops.
// A defective sink declaration is sink-local (a retryable attempt is
// recorded for its notifications and the pass continues); only a
// durable-store failure aborts.
func (d *DrainService) DrainDue(ctx context.Context, routeID string) (DrainClaimedReport, error) {
	report := DrainClaimedReport{}
	now := d.now()
	leaseUntil := now.Add(notificationLeaseMargin)
	if deadline, ok := ctx.Deadline(); ok && deadline.After(now) {
		leaseUntil = deadline.Add(notificationLeaseMargin)
	}
	claims, err := d.Store.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{
		RouteID: routeID, Limit: d.limit(), Owner: d.owner(), LeaseUntil: leaseUntil.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return report, err
	}
	report.Claimed = len(claims)
	unstarted := claims
	for i, claim := range claims {
		if ctx.Err() != nil {
			report.BudgetExpired = true
			unstarted = claims[i:]
			break
		}
		unstarted = claims[i+1:]
		outcome, err := d.deliverClaimed(ctx, claim)
		if err != nil {
			if errors.Is(err, ports.ErrNotificationLeaseLost) {
				continue // recovered by another drainer; not this pass's outcome
			}
			return report, err
		}
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
	// Release the claims this pass never started: a released claim is
	// immediately claimable again; a budget-expired started delivery
	// keeps its fence and remains lease-recoverable (v0.1.6 §4).
	if len(unstarted) > 0 {
		if err := d.Store.ReleaseNotificationClaims(ctx, d.owner(), unstarted); err != nil {
			return report, err
		}
	}
	return report, nil
}

// notificationLeaseMargin mirrors the storage fence's margin: the lease
// outlives the caller's delivery deadline by thirty seconds (v0.1.6
// §4). Declared here so the service's lease request and the store's
// fallback share one documented constant value.
const notificationLeaseMargin = 30 * time.Second

// deliverClaimed delivers one claimed notification through its sink and
// records the fenced outcome with the persisted backoff. A lease lost
// to a recovering drainer is not this pass's error: the recovery owns
// the outcome now, so the claim counts as neither delivered nor
// retried here.
func (d *DrainService) deliverClaimed(ctx context.Context, claim ports.NotificationClaim) (records.NotificationAttemptOutcome, error) {
	started := d.now().UTC().Format(time.RFC3339)
	outcome, recordErr := d.attemptThroughSink(ctx, claim.NotificationEventRecord)
	if errors.Is(recordErr, ports.ErrNotificationLeaseLost) {
		return "", recordErr
	}
	if recordErr != nil {
		var construction *SinkConstructionError
		if !errors.As(recordErr, &construction) {
			return "", recordErr
		}
		// Unresolvable sink: a retryable attempt records why the work
		// stayed pending and keeps the other sinks draining.
		if _, err := d.Store.RecordNotificationAttemptFenced(ctx, ports.NotificationAttemptInput{
			NotificationID: claim.NotificationID,
			Outcome:        records.NotificationRetryableOutcome,
			ErrorCode:      "sink_unresolvable",
			StartedAt:      started,
			CompletedAt:    d.now().UTC().Format(time.RFC3339),
		}, claim, d.retry()); err != nil {
			if errors.Is(err, ports.ErrNotificationLeaseLost) {
				return "", err
			}
			return "", err
		}
		return records.NotificationRetryableOutcome, nil
	}
	if _, err := d.Store.RecordNotificationAttemptFenced(ctx, ports.NotificationAttemptInput{
		NotificationID: claim.NotificationID,
		Outcome:        outcome,
		StartedAt:      started,
		CompletedAt:    d.now().UTC().Format(time.RFC3339),
	}, claim, d.retry()); err != nil {
		if errors.Is(err, ports.ErrNotificationLeaseLost) {
			return "", err
		}
		return "", err
	}
	return outcome, nil
}

// attemptThroughSink resolves the sink and performs the delivery without
// recording; the caller owns the fenced record.
func (d *DrainService) attemptThroughSink(ctx context.Context, rec ports.NotificationEventRecord) (records.NotificationAttemptOutcome, error) {
	sink, err := d.Resolver(rec.RouteID, ports.NotificationSinkRef{ID: rec.SinkID, Type: rec.SinkType})
	if err != nil {
		return "", &SinkConstructionError{Err: err}
	}
	attempt := sink.Deliver(ctx, ports.NotificationDelivery{
		NotificationID: rec.NotificationID,
		RouteID:        rec.RouteID,
		Event:          rec.Event,
		DestinationID:  rec.DestinationID,
		SinkID:         rec.SinkID,
		IdempotencyKey: rec.IdempotencyKey,
		PayloadJSON:    rec.PayloadJSON,
	})
	return attempt.Outcome, nil
}

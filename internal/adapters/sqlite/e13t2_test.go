package sqlite

import (
	"context"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E13-T2 store coverage: the drift evaluation's enqueue surface — the
// created-count contract (zero means the effective policy filtered the
// event, which the drain must report honestly) and the pending/retry
// delivery surfaces.

func TestE13T2EnqueueRouteNotificationCountsAndFilters(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	// A policy that carries only watchman_drift: the integration class
	// filters to zero (the honest filtered report), the watchman class
	// creates one intent per sink.
	policy := &ports.NotificationPolicy{
		Events:   []records.NotificationEventKind{records.EventWatchmanDrift},
		Sinks:    []ports.NotificationSinkRef{{ID: "ops-log", Type: "log"}, {ID: "ops-webhook", Type: "webhook"}},
		Revision: "policy-rev-e13t2",
	}
	s.SetNotificationPolicy(func(routeID string) *ports.NotificationPolicy {
		if routeID != "wiki-maintenance" {
			return nil
		}
		return policy
	})
	created, err := s.EnqueueRouteNotification(ctx, "wiki-maintenance", records.EventIntegrationDrift,
		"drift:profile:abc123", "", map[string]string{"class": "profile", "origin": "drift_evaluation"}, "2026-08-30T10:00:00Z")
	if err != nil || created != 0 {
		t.Fatalf("a filtered event must create nothing and report zero: %d %v", created, err)
	}
	created, err = s.EnqueueRouteNotification(ctx, "wiki-maintenance", records.EventWatchmanDrift,
		"drift:watchman:def456", "", map[string]string{"class": "watchman", "origin": "drift_evaluation"}, "2026-08-30T10:00:01Z")
	if err != nil || created != 2 {
		t.Fatalf("the carried event must create one intent per sink: %d %v", created, err)
	}
	// Re-evaluating the same occurrence reports zero new intents.
	created, err = s.EnqueueRouteNotification(ctx, "wiki-maintenance", records.EventWatchmanDrift,
		"drift:watchman:def456", "", map[string]string{"class": "watchman", "origin": "drift_evaluation"}, "2026-08-30T10:00:02Z")
	if err != nil || created != 0 {
		t.Fatalf("a re-evaluated occurrence must report zero created: %d %v", created, err)
	}
	// The pending surface lists the two intents oldest-first.
	pending, err := s.PendingNotifications(ctx, 10)
	if err != nil || len(pending) != 2 {
		t.Fatalf("the pending surface must list both sink intents: %+v %v", pending, err)
	}
	// A disabled route (no policy) enqueues nothing.
	if created, err = s.EnqueueRouteNotification(ctx, "other-route", records.EventWatchmanDrift,
		"drift:watchman:x", "", nil, "2026-08-30T10:00:03Z"); err != nil || created != 0 {
		t.Fatalf("a route without a policy must create nothing: %d %v", created, err)
	}
}

func TestE13T2RetryNotificationReArmsOnlyRefused(t *testing.T) {
	s := openTestStore(t)
	e13t1Policy(s, records.EventReconciliationRequired)
	ctx := context.Background()
	if err := s.MarkPendingReconcile(ctx, "wiki-maintenance", "pos-retry", "2026-08-30T11:00:00Z"); err != nil {
		t.Fatal(err)
	}
	notifications, err := s.ListNotifications(ctx, ports.NotificationFilter{SinkID: "ops-log"})
	if err != nil || len(notifications) != 1 {
		t.Fatalf("one log intent: %+v %v", notifications, err)
	}
	id := notifications[0].NotificationID
	// Pending needs no re-arm: the retry is a no-op success.
	if err := s.RetryNotification(ctx, id); err != nil {
		t.Fatalf("a pending notification needs no re-arm: %v", err)
	}
	// Delivered never re-arms.
	if _, err := s.RecordNotificationAttempt(ctx, ports.NotificationAttemptInput{
		NotificationID: id, Outcome: records.NotificationDeliveredOutcome,
		StartedAt: "2026-08-30T11:00:01Z", CompletedAt: "2026-08-30T11:00:02Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryNotification(ctx, id); err == nil {
		t.Fatal("a delivered notification must never re-arm")
	}
	// Refused re-arms to pending with the identity untouched.
	other := notificationsIDOf(t, s, "ops-webhook")
	if _, err := s.RecordNotificationAttempt(ctx, ports.NotificationAttemptInput{
		NotificationID: other, Outcome: records.NotificationRefusedOutcome,
		StartedAt: "2026-08-30T11:00:03Z", CompletedAt: "2026-08-30T11:00:04Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryNotification(ctx, other); err != nil {
		t.Fatalf("a refused notification must re-arm: %v", err)
	}
	rearmed, err := s.LoadNotification(ctx, other)
	if err != nil || rearmed.State != records.NotificationPending || rearmed.ResolvedAt != "" {
		t.Fatalf("the re-arm restores pending cleanly: %+v %v", rearmed, err)
	}
	if rearmed.IdempotencyKey != records.NotificationIdempotencyKey(other) {
		t.Fatal("the re-arm must keep the stable idempotency identity")
	}
}

func notificationsIDOf(t *testing.T, s *Store, sinkID string) string {
	t.Helper()
	notifications, err := s.ListNotifications(context.Background(), ports.NotificationFilter{SinkID: sinkID})
	if err != nil || len(notifications) != 1 {
		t.Fatalf("one %s intent: %+v %v", sinkID, notifications, err)
	}
	return notifications[0].NotificationID
}

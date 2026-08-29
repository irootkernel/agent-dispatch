package config

import (
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// E13-T1 coverage: the effective notification policy (NTF-001/NTF-002)
// — defaults when a sink exists and no event list is declared, the
// declared closed-vocabulary list otherwise, disabled without sinks —
// and the notification-policy revision digest (NTF-003) over exactly
// the effective projection.

func e13t1Route(events []string, sinks ...NotificationSink) Route {
	return Route{Notifications: &Notifications{Events: events, Sinks: sinks}}
}

func e13t1LogSink() NotificationSink {
	return NotificationSink{ID: "ops-log", Type: "log"}
}

func TestE13T1EffectiveEventsDisabledWithoutSinks(t *testing.T) {
	// No block at all: disabled.
	events, err := EffectiveNotificationEvents(Route{})
	if err != nil || events != nil {
		t.Fatalf("a route without a notifications block is disabled: %+v %v", events, err)
	}
	// A block without sinks is disabled regardless of its event list
	// (NTF-001: no sinks, no notifications).
	events, err = EffectiveNotificationEvents(e13t1Route([]string{"work_completed"}))
	if err != nil || events != nil {
		t.Fatalf("a route without sinks is disabled: %+v %v", events, err)
	}
}

func TestE13T1EffectiveEventsDefaultWhenOmitted(t *testing.T) {
	events, err := EffectiveNotificationEvents(e13t1Route(nil, e13t1LogSink()))
	if err != nil {
		t.Fatal(err)
	}
	// The NTF-002 default set: completed work, exhausted failure, unknown
	// delivery, quarantine, reconciliation required, integration drift,
	// and Watchman drift — everything except work_failed.
	want := map[records.NotificationEventKind]bool{
		records.EventWorkCompleted: true, records.EventWorkExhausted: true,
		records.EventDeliveryUnknown: true, records.EventQuarantined: true,
		records.EventReconciliationRequired: true, records.EventIntegrationDrift: true,
		records.EventWatchmanDrift: true,
	}
	if len(events) != len(want) {
		t.Fatalf("the default set is exactly the seven NTF-002 classes: %+v", events)
	}
	for _, kind := range events {
		if !want[kind] {
			t.Fatalf("unexpected default event %q", kind)
		}
	}
	// An explicitly empty events list selects the same defaults.
	empty, err := EffectiveNotificationEvents(e13t1Route([]string{}, e13t1LogSink()))
	if err != nil || len(empty) != len(events) {
		t.Fatalf("an omitted and an empty event list are the same policy: %+v %v", empty, err)
	}
}

func TestE13T1EffectiveEventsDeclaredListSortsUnique(t *testing.T) {
	events, err := EffectiveNotificationEvents(e13t1Route(
		[]string{"watchman_drift", "work_completed", "work_completed"}, e13t1LogSink()))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0] != records.EventWatchmanDrift || events[1] != records.EventWorkCompleted {
		t.Fatalf("the declared list resolves sorted and unique: %+v", events)
	}
	if _, err := EffectiveNotificationEvents(e13t1Route([]string{"not_an_event"}, e13t1LogSink())); err == nil {
		t.Fatal("an unknown event name must fail closed, never silently default")
	}
}

func TestE13T1PolicyRevisionStableAndSensitive(t *testing.T) {
	base := e13t1Route(nil, e13t1LogSink())
	first := NotificationPolicyRevision(base)
	if first == "" {
		t.Fatal("the revision must be a digest")
	}
	if again := NotificationPolicyRevision(base); again != first {
		t.Fatal("the revision must be deterministic")
	}
	// A disabled route digests the empty policy — distinct from any sink
	// policy (NTF-001's disabled state has its own identity).
	if disabled := NotificationPolicyRevision(Route{}); disabled == first {
		t.Fatal("a disabled route must not share a sink policy's revision")
	}
	// Changing the effective event set changes the revision.
	declared := e13t1Route([]string{"work_completed"}, e13t1LogSink())
	if NotificationPolicyRevision(declared) == first {
		t.Fatal("a different effective event set must change the revision")
	}
	// Adding a sink changes the revision.
	twoSinks := e13t1Route(nil, e13t1LogSink(), NotificationSink{ID: "ops-webhook", Type: "webhook"})
	if NotificationPolicyRevision(twoSinks) == first {
		t.Fatal("a different sink set must change the revision")
	}
	// Repointing the webhook endpoint or credential does NOT change the
	// revision: endpoint and authentication references stay outside the
	// identity projection, so a repointed sink keeps notifying the same
	// logical notifications under their existing identities (SEC-012).
	repointed := e13t1Route(nil, NotificationSink{ID: "ops-log", Type: "log", Endpoint: "https://other.example.invalid"})
	if NotificationPolicyRevision(repointed) != first {
		t.Fatal("an endpoint change must not re-identify notifications")
	}
}

func TestE13T1EmptyEventsDeclarationIsValid(t *testing.T) {
	// The semantic validator accepts an empty event list (the NTF-002
	// defaults apply at evaluation time) and still rejects names outside
	// the closed vocabulary.
	cfg := &Config{Routes: map[string]Route{
		"r1": e13t1Route(nil, e13t1LogSink()),
	}}
	if errs := validateNotifications(cfg); len(errs) != 0 {
		t.Fatalf("an omitted event list with a sink is valid: %v", errs)
	}
	cfg.Routes["r2"] = e13t1Route([]string{"not_an_event"}, e13t1LogSink())
	errs := validateNotifications(cfg)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "r2") {
		t.Fatalf("only the invalid vocabulary rejection remains: %v", errs)
	}
}

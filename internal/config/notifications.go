package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// defaultNotificationEvents is the default reportable event set (NTF-002):
// when at least one sink exists and no event list is declared, exactly
// these classes notify. The omitted class is work_failed — a failed run
// with retry budget remaining schedules its follow-up silently; the
// exhausted and unknown outcomes are the operator-visible terminals.
var defaultNotificationEvents = []records.NotificationEventKind{
	records.EventWorkCompleted,
	records.EventWorkExhausted,
	records.EventDeliveryUnknown,
	records.EventQuarantined,
	records.EventReconciliationRequired,
	records.EventIntegrationDrift,
	records.EventWatchmanDrift,
}

// EffectiveNotificationEvents resolves the route's effective reportable
// event set (NTF-001/NTF-002): nil when notifications are disabled (no
// block or no sink), the declared closed-vocabulary list when present,
// and the default set when a sink exists and no event list is declared.
// Every name is validated against the vocabulary, so an invalid
// declaration yields an error, never a silent default.
func EffectiveNotificationEvents(route Route) ([]records.NotificationEventKind, error) {
	if route.Notifications == nil || len(route.Notifications.Sinks) == 0 {
		return nil, nil
	}
	declared := route.Notifications.Events
	if len(declared) == 0 {
		out := make([]records.NotificationEventKind, len(defaultNotificationEvents))
		copy(out, defaultNotificationEvents)
		return out, nil
	}
	out := make([]records.NotificationEventKind, 0, len(declared))
	for _, name := range declared {
		kind, err := records.ParseNotificationEventKind(name)
		if err != nil {
			return nil, err
		}
		out = append(out, kind)
	}
	return records.NotificationEventKinds(out).SortUnique(), nil
}

// notificationSinkRef is one sink's identity inside the canonical
// effective-policy projection.
type notificationSinkRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// notificationPolicyProjection is the canonical effective-policy
// projection the notification policy revision digests (NTF-003): the
// effective event set and the sink identity/kind references. Endpoints
// and authentication references are deliberately absent — repointing a
// sink's endpoint or credential must not re-identify notifications the
// previous policy already created.
type notificationPolicyProjection struct {
	Events []string              `json:"events"`
	Sinks  []notificationSinkRef `json:"sinks"`
}

// NotificationPolicyRevision digests the route's effective notification
// policy (the effective event set plus sink identity references,
// canonically sorted): the revision a notification's dedup identity
// carries so an audit can attribute every notification to the exact
// policy that produced it. A disabled route digests the empty policy.
func NotificationPolicyRevision(route Route) string {
	projection := notificationPolicyProjection{Events: []string{}, Sinks: []notificationSinkRef{}}
	if events, err := EffectiveNotificationEvents(route); err == nil {
		for _, kind := range events {
			projection.Events = append(projection.Events, string(kind))
		}
	}
	if route.Notifications != nil {
		for _, sink := range route.Notifications.Sinks {
			projection.Sinks = append(projection.Sinks, notificationSinkRef{ID: sink.ID, Type: sink.Type})
		}
	}
	sort.Strings(projection.Events)
	sort.Slice(projection.Sinks, func(i, j int) bool {
		if projection.Sinks[i].ID != projection.Sinks[j].ID {
			return projection.Sinks[i].ID < projection.Sinks[j].ID
		}
		return projection.Sinks[i].Type < projection.Sinks[j].Type
	})
	enc, err := json.Marshal(projection)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(enc)
	return hex.EncodeToString(sum[:])
}

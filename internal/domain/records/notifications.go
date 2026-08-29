package records

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// NotificationEventKind is one reportable transition class of the
// notification contract (NTF-001/NTF-002, ADR-0019): the closed v0.1.5
// vocabulary the per-route policy configures and the default set covers
// when a sink exists and no event list is declared.
type NotificationEventKind string

const (
	EventWorkCompleted          NotificationEventKind = "work_completed"
	EventWorkFailed             NotificationEventKind = "work_failed"
	EventWorkExhausted          NotificationEventKind = "work_exhausted"
	EventDeliveryUnknown        NotificationEventKind = "delivery_unknown"
	EventQuarantined            NotificationEventKind = "quarantined"
	EventReconciliationRequired NotificationEventKind = "reconciliation_required"
	EventIntegrationDrift       NotificationEventKind = "integration_drift"
	EventWatchmanDrift          NotificationEventKind = "watchman_drift"
)

// ParseNotificationEventKind validates one event name against the closed
// vocabulary (fail-closed like every enum here, DAT-009 posture).
func ParseNotificationEventKind(s string) (NotificationEventKind, error) {
	switch NotificationEventKind(s) {
	case EventWorkCompleted, EventWorkFailed, EventWorkExhausted, EventDeliveryUnknown,
		EventQuarantined, EventReconciliationRequired, EventIntegrationDrift, EventWatchmanDrift:
		return NotificationEventKind(s), nil
	}
	return "", fmt.Errorf("unknown notification event %q", s)
}

// NotificationEventKinds is a sortable, deduplicatable kind set.
type NotificationEventKinds []NotificationEventKind

// SortOrders canonicalizes the set: sorted, unique.
func (kinds NotificationEventKinds) SortUnique() NotificationEventKinds {
	seen := make(map[NotificationEventKind]bool, len(kinds))
	out := make(NotificationEventKinds, 0, len(kinds))
	for _, k := range kinds {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Contains reports whether the set carries one kind.
func (kinds NotificationEventKinds) Contains(k NotificationEventKind) bool {
	for _, have := range kinds {
		if have == k {
			return true
		}
	}
	return false
}

// NotificationState is the durable notification intent lifecycle: a
// notification is created pending and resolves through delivery (a
// definite sink success) or refusal (a definite sink rejection). An
// ambiguous or retryable delivery outcome leaves it pending — delivery
// failure never mutates any other record (NTF-005, NTF-007).
type NotificationState string

const (
	NotificationPending   NotificationState = "pending"
	NotificationDelivered NotificationState = "delivered"
	NotificationRefused   NotificationState = "refused"
)

// ParseNotificationState validates one notification state.
func ParseNotificationState(s string) (NotificationState, error) {
	switch NotificationState(s) {
	case NotificationPending, NotificationDelivered, NotificationRefused:
		return NotificationState(s), nil
	}
	return "", fmt.Errorf("unknown notification state %q", s)
}

// NotificationAttemptOutcome is one sink delivery attempt's outcome
// (sink-adapter-contract §10): definite success, definite refusal, an
// ambiguous transport result, or a retryable pre-delivery failure. The
// ambiguous and retryable classes keep the notification pending for
// at-least-once retry under the stable idempotency key (NTF-007).
type NotificationAttemptOutcome string

const (
	NotificationDeliveredOutcome NotificationAttemptOutcome = "delivered"
	NotificationRefusedOutcome   NotificationAttemptOutcome = "refused"
	NotificationAmbiguousOutcome NotificationAttemptOutcome = "ambiguous"
	NotificationRetryableOutcome NotificationAttemptOutcome = "retryable"
)

// ParseNotificationAttemptOutcome validates one attempt outcome.
func ParseNotificationAttemptOutcome(s string) (NotificationAttemptOutcome, error) {
	switch NotificationAttemptOutcome(s) {
	case NotificationDeliveredOutcome, NotificationRefusedOutcome, NotificationAmbiguousOutcome, NotificationRetryableOutcome:
		return NotificationAttemptOutcome(s), nil
	}
	return "", fmt.Errorf("unknown notification attempt outcome %q", s)
}

// Terminal reports whether the outcome resolves the notification.
func (o NotificationAttemptOutcome) Terminal() bool {
	return o == NotificationDeliveredOutcome || o == NotificationRefusedOutcome
}

// notificationDedupMaterial is the canonical deduplication projection of
// one logical notification (NTF-003): event, optional destination,
// transition, sink, and notification-policy revision. The field order is
// the canonical serialization order.
type notificationDedupMaterial struct {
	Event      string `json:"event"`
	Dest       string `json:"destination,omitempty"`
	Transition string `json:"transition"`
	SinkID     string `json:"sink_id"`
	Policy     string `json:"notification_policy_revision"`
}

// NotificationID derives the deterministic notification identity from
// the five dedup components (NTF-003): the same transition evaluated
// twice — a replay, an aggregate rerun, a crash recovery — derives the
// same id, so the durable unique key collapses the duplicate instead of
// notifying twice (AC-902). It follows the content-derived record-id
// precedent of destination revisions ("dst-"+hex).
func NotificationID(event NotificationEventKind, destinationID, transition, sinkID, policyRevision string) string {
	material, err := json.Marshal(notificationDedupMaterial{
		Event:      string(event),
		Dest:       destinationID,
		Transition: transition,
		SinkID:     sinkID,
		Policy:     policyRevision,
	})
	if err != nil {
		// A struct of strings always marshals; the branch is unreachable
		// and exists only so the derivation has no hidden error path.
		material = []byte(strings.Join([]string{string(event), destinationID, transition, sinkID, policyRevision}, "\x00"))
	}
	sum := sha256.Sum256(material)
	return "ntf-" + hex.EncodeToString(sum[:])
}

// NotificationIdempotencyKey derives the stable delivery identity one
// notification's every attempt reuses (NTF-007): distinct from the record
// id so the wire header never carries the storage identity, and stable
// across every retry of the same logical notification.
func NotificationIdempotencyKey(notificationID string) string {
	sum := sha256.Sum256([]byte("agent-dispatch/notification-delivery/v1\x00" + notificationID))
	return "ntfidem-" + hex.EncodeToString(sum[:])
}

// NotificationAttemptID derives the deterministic attempt identity: one
// notification's nth attempt always derives the same row id, so a
// crash between the attempt and its record replays idempotently.
func NotificationAttemptID(notificationID string, attemptNumber int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("agent-dispatch/notification-attempt/v1\x00%s\x00%d", notificationID, attemptNumber)))
	return "ntfa-" + hex.EncodeToString(sum[:])
}

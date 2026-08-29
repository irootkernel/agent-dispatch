// Package notificationsink implements the two shipped notification
// sink adapters of E13-T2 (NTF-006, sink-adapter-contract §10): the
// structured log sink and the authenticated HTTPS webhook sink.
// Both consume the notification-event/v1 payload, return the four-way
// outcome contract, and never mutate state outside the notification
// tables (NTF-005).
package notificationsink

import (
	"context"
	"io"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// LogSink is the structured log (stderr) sink: one delivery appends the
// stored notification-event/v1 payload as a single JSON line to the
// operator's stream — no external effect, no secret, no document
// content (the payload is already the safe projection, SEC-011). A
// write failure is a retryable pre-delivery failure; the sink has no
// refusal or ambiguity mode of its own. Deliveries are sequential by
// contract (the delivery service drives one attempt at a time), so the
// writer needs no synchronization here.
type LogSink struct {
	// ID is the configured sink identity.
	ID string
	// Out receives the payload lines; a nil writer is unusable and every
	// delivery reports the retryable construction error.
	Out io.Writer
}

// Deliver appends the payload as one JSON line and reports the outcome.
func (s *LogSink) Deliver(_ context.Context, in ports.NotificationDelivery) ports.NotificationAttemptInput {
	out := ports.NotificationAttemptInput{
		NotificationID: in.NotificationID,
		Outcome:        records.NotificationDeliveredOutcome,
	}
	if s.Out == nil {
		out.Outcome = records.NotificationRetryableOutcome
		out.ErrorCode = "log_sink_unwritable"
		return out
	}
	if _, err := io.WriteString(s.Out, in.PayloadJSON+"\n"); err != nil {
		out.Outcome = records.NotificationRetryableOutcome
		out.ErrorCode = "log_sink_write_failed"
		return out
	}
	return out
}

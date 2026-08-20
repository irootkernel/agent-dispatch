package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/domain/state"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// Runtime drives the durable submit flow (E3-T2): commit the
// observation-to-intent lineage, acquire one transactional attempt lease,
// invoke the sink, and complete the attempt with a domain-validated
// transition. The intent is committed and leased before the sink
// invocation and no store transaction is open while the sink runs
// (DUR-001, DUR-002, ADR-0005).
type Runtime struct {
	Store ports.DispatchStore
	Sink  ports.Sink
	// Now is the wall clock; tests inject a deterministic one.
	Now func() time.Time
	// LeaseTTL bounds how long one attempt owner may hold the submitting
	// state before recovery may take it over.
	LeaseTTL time.Duration
	// Actor labels audit records from this runtime.
	Actor string
}

// SubmitReport summarizes one completed submit attempt.
type SubmitReport struct {
	DispatchID     string
	AttemptID      string
	From           records.IntentState
	To             records.IntentState
	Reason         state.IntentReason
	Classification ports.SubmitClassification
}

// SubmitOnce runs the flow for one durable intent: it refuses terminal or
// leased intents, commits the lease before invoking the sink, and records
// the outcome through the domain state machine. A sink error is an
// ambiguous outcome and completes as unknown, never failed (DUR-005).
func (r *Runtime) SubmitOnce(ctx context.Context, dispatchID, owner string) (SubmitReport, error) {
	var report SubmitReport
	snap, err := r.Store.LoadIntent(ctx, dispatchID)
	if err != nil {
		return report, err
	}
	switch snap.State {
	case records.IntentReady, records.IntentRetryWait:
	case records.IntentSubmitting:
		return report, fmt.Errorf("%w: dispatch %s is submitting under owner %q", ports.ErrLeaseHeld, dispatchID, snap.LeaseOwner)
	default:
		return report, fmt.Errorf("dispatch %s is %s, not submittable", dispatchID, snap.State)
	}
	now := r.Now()
	report.DispatchID = dispatchID
	report.From = snap.State

	// The lease and its audit entry commit in one transaction that closes
	// before the sink call; from here until CompleteAttempt the runtime
	// holds no store transaction (DUR-001).
	attemptID := fmt.Sprintf("%s-%d", dispatchID, now.UnixNano())
	acquired, err := r.Store.AcquireAttempt(ctx, ports.AcquireAttempt{
		DispatchID:     dispatchID,
		AttemptID:      attemptID,
		Owner:          owner,
		Now:            Timestamp(now),
		LeaseExpiresAt: Timestamp(now.Add(r.LeaseTTL)),
	})
	if err != nil {
		return report, err
	}
	report.AttemptID = acquired

	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(snap.RequestJSON), &req); err != nil {
		return report, fmt.Errorf("stored request is not the task contract shape: %w", err)
	}
	res, sinkErr := r.Sink.Submit(ctx, req)
	if sinkErr != nil {
		// A returned error is an ambiguous outcome: the attempt may or may
		// not have reached the target (DUR-005).
		res = ports.SubmitResult{
			Classification: ports.SubmitUnknown,
			Durable:        ports.DurableUnknown,
			Diagnostic:     sinkErr.Error(),
		}
	}
	report.Classification = res.Classification
	completion := ports.AttemptResult{
		AttemptID:   acquired,
		DispatchID:  dispatchID,
		CompletedAt: Timestamp(r.Now()),
		Diagnostic:  boundedDiagnostic(res.Diagnostic),
	}
	switch res.Classification {
	case ports.SubmitAccepted:
		completion.Outcome = "accepted"
		completion.Transition = ports.AttemptTransition{
			To:     records.IntentAccepted,
			Reason: state.ReasonDurableAcceptance,
		}
		completion.Receipt = r.receipt(acquired, res, records.AcceptanceAccepted)
	case ports.SubmitRejected:
		completion.Outcome = "rejected"
		completion.Transition = ports.AttemptTransition{
			To:     records.IntentRejected,
			Reason: state.ReasonDefiniteRejection,
		}
		completion.Receipt = r.receipt(acquired, res, records.AcceptanceRejected)
	case ports.SubmitDefiniteNotSubmitted:
		completion.Outcome = "transport_failure"
		completion.ErrorCode = "definite_not_submitted"
		completion.Transition = ports.AttemptTransition{
			To:     records.IntentRetryWait,
			Reason: state.ReasonTransientFailure,
		}
	default: // ports.SubmitUnknown, including malformed responses
		completion.Outcome = "unknown"
		completion.Transition = ports.AttemptTransition{
			To:     records.IntentUnknown,
			Reason: state.ReasonAmbiguousOutcome,
		}
	}
	if completion.Receipt != nil {
		completion.Transition.Evidence.ReceiptRef = completion.Receipt.ReceiptID
	}
	completion.Transition.TransitionID = acquired + ":" + string(completion.Transition.Reason)
	if err := r.Store.CompleteAttempt(ctx, completion); err != nil {
		return report, err
	}
	report.To = completion.Transition.To
	report.Reason = completion.Transition.Reason
	return report, nil
}

// Recover takes over every submitting intent whose attempt lease expired,
// defaulting it to unknown with audit evidence (persistence §5).
func (r *Runtime) Recover(ctx context.Context) ([]ports.RecoveredLease, error) {
	return r.Store.RecoverExpiredSubmitting(ctx, Timestamp(r.Now()))
}

// receipt builds the durable acceptance evidence for one submit result.
func (r *Runtime) receipt(attemptID string, res ports.SubmitResult, acceptance records.AcceptanceState) *ports.ReceiptInput {
	return &ports.ReceiptInput{
		ReceiptID:        "rcpt-" + attemptID,
		Acceptance:       acceptance,
		Durable:          res.Durable == ports.DurableTrue,
		ExternalRef:      res.ExternalRef,
		TargetObservedAt: res.TargetObservedAt,
		ReceivedAt:       Timestamp(r.Now()),
		BoundedPayload:   boundedPayload(res.StructuredPayload),
	}
}

// Timestamp renders the canonical UTC RFC 3339 second-precision form the
// store's TEXT comparisons rely on.
func Timestamp(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

// boundedDiagnostic keeps the recorded diagnostic bounded.
func boundedDiagnostic(d string) string {
	const max = 2000
	if len(d) > max {
		return d[:max] + "...(truncated)"
	}
	return d
}

// boundedPayload keeps the recorded structured evidence bounded; digests
// and refs are stored as real columns.
func boundedPayload(p []byte) string {
	const max = 4096
	if len(p) > max {
		p = p[:max]
	}
	if len(p) == 0 {
		return "{}"
	}
	return string(p)
}

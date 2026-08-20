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

// Runtime drives the durable submit flow (E3-T2/E3-T3): commit the
// observation-to-intent lineage, acquire one transactional attempt lease,
// invoke the sink, classify the result, and complete the attempt with a
// domain-validated transition. The intent is committed and leased before
// the sink invocation and no store transaction is open while the sink
// runs (DUR-001, DUR-002, ADR-0005).
type Runtime struct {
	Store ports.DispatchStore
	Sink  ports.Sink
	// Now is the wall clock; tests inject a deterministic one.
	Now func() time.Time
	// LeaseTTL bounds how long one attempt owner may hold the submitting
	// state before recovery may take it over.
	LeaseTTL time.Duration
	// Backoff is the persisted submission retry policy (DUR-007); a zero
	// value disables automatic backoff scheduling (used by tests that
	// drive eligibility directly).
	Backoff Backoff
	// JitterUnit supplies the deterministic jitter fraction in [0, 1).
	JitterUnit func() float64
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
	// NextAttemptAt is the persisted backoff deadline for retryable
	// outcomes, empty otherwise.
	NextAttemptAt string
}

// SubmitOnce runs the flow for one durable intent: it refuses terminal or
// leased intents, commits the lease before invoking the sink, classifies
// the adapter result, and records the outcome through the domain state
// machine. A sink error is an ambiguous outcome and completes as unknown,
// never failed (DUR-005), and no automatic target fallback exists
// (DUR-008).
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
	classified := ClassifyResult(res, sinkErr)
	if sinkErr != nil {
		res.Diagnostic = sinkErr.Error()
	}
	report.Classification = res.Classification
	completion := ports.AttemptResult{
		AttemptID:   acquired,
		DispatchID:  dispatchID,
		Outcome:     classified.AttemptOutcome,
		ErrorCode:   classified.ErrorCode,
		CompletedAt: Timestamp(r.Now()),
		Diagnostic:  boundedDiagnostic(res.Diagnostic),
		Transition: ports.AttemptTransition{
			To:           classified.To,
			Reason:       classified.Reason,
			TransitionID: acquired + ":" + string(classified.Reason),
		},
	}
	if classified.ReceiptWorthy {
		acceptance := records.AcceptanceAccepted
		if classified.To == records.IntentRejected {
			acceptance = records.AcceptanceRejected
		}
		completion.Receipt = r.receipt(acquired, res, acceptance)
		completion.Transition.Evidence.ReceiptRef = completion.Receipt.ReceiptID
	}
	// A retryable outcome schedules the persisted backoff deadline the
	// lease predicate enforces (DUR-007).
	if classified.To == records.IntentRetryWait && r.Backoff != (Backoff{}) {
		attempts := snap.AttemptCount + 1
		unit := 0.0
		if r.JitterUnit != nil {
			unit = r.JitterUnit()
		}
		delay, derr := r.Backoff.Delay(attempts, unit)
		if derr != nil {
			return report, derr
		}
		deadline := Timestamp(r.Now().Add(delay))
		completion.NextAttemptAt = deadline
		report.NextAttemptAt = deadline
	}
	if err := r.Store.CompleteAttempt(ctx, completion); err != nil {
		return report, err
	}
	report.To = classified.To
	report.Reason = classified.Reason
	return report, nil
}

// DrainReport summarizes one bounded drain run (CLI dispatches drain).
type DrainReport struct {
	Processed int
	Skipped   int
	Reports   []SubmitReport
}

// Drain submits up to max due ready/retry_wait intents for one route.
// The attempt budget stops automatic processing (DUR-007); an ambiguity
// stops the drain for operator reconciliation rather than falling over
// (DUR-008).
func (r *Runtime) Drain(ctx context.Context, routeID string, max int, lister ports.InspectionStore) (DrainReport, error) {
	var out DrainReport
	if max < 1 {
		return out, fmt.Errorf("drain max must be >= 1")
	}
	intents, err := lister.ListIntents(ctx, ports.IntentFilter{RouteID: routeID, Limit: 1000})
	if err != nil {
		return out, err
	}
	now := Timestamp(r.Now())
	for _, sum := range intents {
		if out.Processed >= max {
			break
		}
		switch sum.State {
		case records.IntentReady, records.IntentRetryWait:
		default:
			continue
		}
		if sum.NextAttemptAt != "" && sum.NextAttemptAt > now {
			out.Skipped++
			continue
		}
		if r.Backoff != (Backoff{}) && r.Backoff.Exhausted(sum.AttemptCount) {
			out.Skipped++
			continue
		}
		report, err := r.SubmitOnce(ctx, sum.DispatchID, "drain")
		if err != nil {
			return out, fmt.Errorf("drain stopped at %s: %w", sum.DispatchID, err)
		}
		out.Processed++
		out.Reports = append(out.Reports, report)
	}
	return out, nil
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

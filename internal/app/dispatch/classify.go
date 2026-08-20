package dispatch

import (
	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/domain/state"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// Classified maps one sink outcome onto the durable attempt record and
// the domain-validated intent transition (E3-T3 adapter result
// classifier).
type Classified struct {
	// AttemptOutcome is the dispatch_attempts outcome column value.
	AttemptOutcome string
	// ErrorCode is the bounded machine code for transport failures.
	ErrorCode string
	To        records.IntentState
	Reason    state.IntentReason
	// DurableAcceptance is true only for accepted with durable=true.
	DurableAcceptance bool
	// ReceiptWorthy marks outcomes that persist an acceptance receipt.
	ReceiptWorthy bool
}

// ClassifyResult applies the sink-adapter contract classification to one
// submit outcome (DUR-004, DUR-005, DUR-008):
//
//   - accepted (durable or not) proves target acceptance;
//   - rejected is a definite refusal (terminal policy dead-letters it);
//   - definite_not_submitted is a provable pre-invocation failure and the
//     only automatically retryable outcome;
//   - unknown — including sink errors and malformed responses after a
//     possible submission — never becomes failed and never falls over to
//     another target; reconciliation must resolve it first.
func ClassifyResult(res ports.SubmitResult, sinkErr error) Classified {
	if sinkErr != nil {
		// A returned error carries no proof either way: the request may or
		// may not have reached the target (DUR-005).
		return Classified{
			AttemptOutcome: "unknown",
			To:             records.IntentUnknown,
			Reason:         state.ReasonAmbiguousOutcome,
		}
	}
	switch res.Classification {
	case ports.SubmitAccepted:
		return Classified{
			AttemptOutcome:    "accepted",
			To:                records.IntentAccepted,
			Reason:            state.ReasonDurableAcceptance,
			DurableAcceptance: res.Durable == ports.DurableTrue,
			ReceiptWorthy:     true,
		}
	case ports.SubmitRejected:
		return Classified{
			AttemptOutcome: "rejected",
			To:             records.IntentRejected,
			Reason:         state.ReasonDefiniteRejection,
			ReceiptWorthy:  true,
		}
	case ports.SubmitDefiniteNotSubmitted:
		return Classified{
			AttemptOutcome: "transport_failure",
			ErrorCode:      "definite_not_submitted",
			To:             records.IntentRetryWait,
			Reason:         state.ReasonTransientFailure,
		}
	default: // ports.SubmitUnknown
		return Classified{
			AttemptOutcome: "unknown",
			To:             records.IntentUnknown,
			Reason:         state.ReasonAmbiguousOutcome,
		}
	}
}

package cli

import (
	"fmt"
	"io"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// Aggregate-event inspection (E12-T3, CLI-013/FAN-010/DAT-011): `events
// show` projects one occurrence's aggregate — the selection summary with
// its closed reasons, every child beneath it, and each child's separate
// destination, submit, acceptance, execution, work-receipt, retry, and
// completion-evidence projection. The aggregate's own status is a
// projection over its children (DAT-011), never a stored enum.

// runEvents implements the `events` command tree.
func runEvents(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "events", "events requires a subcommand: show")
	}
	sub, rest := args[0], args[1:]
	if sub != "show" {
		return usageError(stderr, "events", fmt.Sprintf("unknown events subcommand %q", sub))
	}
	return runEventsShow("events show", rest, stdout, stderr)
}

// runEventsShow renders one aggregate event with its children (FBK-012
// gap surfaced per child; the aggregate status names the worst class).
func runEventsShow(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, nil)
	if code != 0 {
		return code
	}
	aggregateID := flags.positional
	if aggregateID == "" {
		return usageError(stderr, command, "events show requires an aggregate event ID")
	}
	_, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	agg, err := closer.LoadAggregateEvent(requestCtx(), aggregateID)
	if err != nil {
		return intentErr(stderr, command, err)
	}
	children, err := closer.LoadAggregateChildren(requestCtx(), aggregateID)
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	rendered := make([]map[string]any, 0, len(children))
	status := "completed"
	for _, child := range children {
		row := eventsChildProjection(child)
		rendered = append(rendered, row)
		// The aggregate status is the worst class over its children
		// (DAT-011): evidence-gap > manual-intervention > failed >
		// in-progress > completed. A valid BEGUN receipt classifies
		// in-progress, NOT evidence-gap (E12 epic whole-review round 1):
		// its completion evidence is missing but the run itself is
		// known-busy work — the gap class stays for accepted work with no
		// (or an invalid) receipt to trust.
		switch {
		case row["completion_evidence"] == "missing" && !begunReceipt(child):
			status = worstAggregateStatus(status, "evidence-gap")
		case child.WorkReceipt == string(records.WorkBlocked):
			status = worstAggregateStatus(status, "manual-intervention")
		case child.WorkReceipt == string(records.WorkFailed) || child.IntentState == string(records.IntentDeadLettered):
			status = worstAggregateStatus(status, "failed")
		case !childReceiptResolved(child):
			status = worstAggregateStatus(status, "in-progress")
		}
	}
	return writeEnvelope(stdout, command, map[string]any{
		"aggregate_id":        agg.AggregateID,
		"decision_id":         agg.DecisionID,
		"route_id":            agg.RouteID,
		"route_revision":      agg.RouteRevision,
		"origin":              agg.Origin,
		"generation":          agg.Generation,
		"content_fingerprint": agg.ContentFingerprint,
		"selections":          agg.SelectionJSON,
		"created_at":          agg.CreatedAt,
		"aggregate_status":    status,
		"children":            rendered,
		"count":               len(rendered),
	})
}

// eventsChildProjection renders one child's separate projections: the
// destination lane, the submit state, the latest acceptance and execution
// receipts, the work receipt with its validity, the retry state, and the
// FBK-012 completion-evidence gap (accepted work without a valid
// attributable work receipt is NEVER reported as completed).
func eventsChildProjection(child sqlite.AggregateChildRow) map[string]any {
	row := map[string]any{
		"dispatch_id": child.DispatchID,
		"destination": map[string]any{
			"id":         child.DestinationID,
			"revision":   child.DestinationRevision,
			"workstream": child.Workstream,
		},
		"intent_state": child.IntentState,
		"acceptance":   nilIfEmpty(child.Acceptance),
		"execution":    nilIfEmpty(child.Execution),
		"retry": map[string]any{
			"attempt_count":   child.AttemptCount,
			"next_attempt_at": nilIfEmpty(child.NextAttemptAt),
		},
	}
	if child.WorkReceipt != "" {
		row["work_receipt"] = map[string]any{
			"status": child.WorkReceipt,
			"valid":  child.WorkReceiptValid,
		}
	} else {
		row["work_receipt"] = nil
	}
	// FBK-012 (AC-806 posture): accepted work without a valid TERMINAL
	// work receipt is a completion-evidence gap with the actionable next
	// step — never "completed". present demands a valid receipt in a
	// terminal outcome (completed, partially_completed, blocked, failed):
	// a valid BEGUN receipt is a run in flight, not completion evidence
	// (E12 epic validation). A child that was never accepted has no
	// completion evidence to audit yet and renders not-applicable.
	accepted := child.Acceptance == string(records.AcceptanceAccepted) || child.IntentState == string(records.IntentAccepted)
	terminalReceipt := child.WorkReceiptValid && child.WorkReceipt != "" && child.WorkReceipt != string(records.WorkBegan)
	switch {
	case !accepted:
		row["completion_evidence"] = "not-applicable"
	case !terminalReceipt:
		row["completion_evidence"] = "missing"
		row["completion_evidence_next_step"] = fmt.Sprintf(
			"collect the work receipt for dispatch %s (agent-dispatch work begin/work complete --dispatch-id %s --run-id <run>) or record its absence through reconciliation", child.DispatchID, child.DispatchID)
	default:
		row["completion_evidence"] = "present"
	}
	if child.ExternalRef != "" {
		row["external_ref"] = child.ExternalRef
	}
	return row
}

// begunReceipt reports one child's valid BEGUN work receipt: a run in
// flight (E12 epic whole-review round 1). Its completion evidence is
// still missing — present demands a terminal receipt — but the aggregate
// classifies the child in-progress instead of evidence-gap: the record is
// trustworthy, the work simply has not finished.
func begunReceipt(child sqlite.AggregateChildRow) bool {
	return child.WorkReceiptValid && child.WorkReceipt == string(records.WorkBegan)
}

// childReceiptResolved reports whether one child's work receipt reached a
// terminal, valid completion (partially_completed still owes its follow-up
// and blocked awaits the operator — both keep the aggregate busy).
func childReceiptResolved(child sqlite.AggregateChildRow) bool {
	// A resolved child carries a valid TERMINAL completed receipt (E12
	// epic validation): a begun or blocked receipt keeps the aggregate
	// busy — begun is a run in flight, blocked is manual intervention
	// (named by its own class above).
	return child.WorkReceiptValid && child.WorkReceipt == string(records.WorkCompleted)
}

// aggregateStatusRank orders the aggregate status classes (DAT-011):
// evidence-gap is the worst (the operator cannot even trust the record),
// then manual-intervention, failed, in-progress, completed.
func aggregateStatusRank(status string) int {
	switch status {
	case "evidence-gap":
		return 0
	case "manual-intervention":
		return 1
	case "failed":
		return 2
	case "in-progress":
		return 3
	default:
		return 4
	}
}

func worstAggregateStatus(current, candidate string) string {
	if aggregateStatusRank(candidate) < aggregateStatusRank(current) {
		return candidate
	}
	return current
}

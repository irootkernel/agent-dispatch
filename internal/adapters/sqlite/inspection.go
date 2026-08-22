package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Read side, operator actions, and unknown reconciliation (E3-T3).

// ListIntents returns intents matching the filter, newest first.
func (s *Store) ListIntents(ctx context.Context, f ports.IntentFilter) ([]ports.IntentSummary, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.RouteID != "" {
		where = append(where, "route_id = ?")
		args = append(args, f.RouteID)
	}
	if f.State != "" {
		where = append(where, "state = ?")
		args = append(args, string(f.State))
	}
	if f.TargetID != "" {
		where = append(where, "target_id = ?")
		args = append(args, f.TargetID)
	}
	if f.DispatchID != "" {
		where = append(where, "dispatch_id = ?")
		args = append(args, f.DispatchID)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	rows, err := s.QueryContext(ctx, `SELECT dispatch_id, route_id, target_id, generation, idempotency_key, state, attempt_count, next_attempt_at, created_at, updated_at
		FROM dispatch_intents WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC, dispatch_id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ports.IntentSummary
	for rows.Next() {
		var sum ports.IntentSummary
		var next sql.NullString
		if err := rows.Scan(&sum.DispatchID, &sum.RouteID, &sum.TargetID, &sum.Generation, &sum.IdempotencyKey,
			&sum.State, &sum.AttemptCount, &next, &sum.CreatedAt, &sum.UpdatedAt); err != nil {
			return nil, err
		}
		sum.NextAttemptAt = nullText(next)
		out = append(out, sum)
	}
	return out, rows.Err()
}

// LoadIntentLineage returns the full inspectable lineage of one dispatch
// (DUR-009: dead-lettered work remains fully inspectable).
func (s *Store) LoadIntentLineage(ctx context.Context, dispatchID string) (ports.IntentLineage, error) {
	var lin ports.IntentLineage
	var next sql.NullString
	err := s.QueryRowContext(ctx, `SELECT dispatch_id, route_id, target_id, generation, idempotency_key, state, attempt_count, next_attempt_at, created_at, updated_at
		FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(
		&lin.Intent.DispatchID, &lin.Intent.RouteID, &lin.Intent.TargetID, &lin.Intent.Generation, &lin.Intent.IdempotencyKey,
		&lin.Intent.State, &lin.Intent.AttemptCount, &next, &lin.Intent.CreatedAt, &lin.Intent.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return lin, fmt.Errorf("%w: %s", ports.ErrIntentNotFound, dispatchID)
	}
	if err != nil {
		return lin, err
	}
	lin.Intent.NextAttemptAt = nullText(next)

	arows, err := s.QueryContext(ctx, `SELECT attempt_id, dispatch_id, lease_owner, started_at, completed_at, outcome, error_code, response_digest, diagnostic
		FROM dispatch_attempts WHERE dispatch_id = ? ORDER BY started_at, attempt_id`, dispatchID)
	if err != nil {
		return lin, err
	}
	for arows.Next() {
		var a ports.AttemptRecord
		var completed, outcome, code, digest, diag sql.NullString
		if err := arows.Scan(&a.AttemptID, &a.DispatchID, &a.LeaseOwner, &a.StartedAt, &completed, &outcome, &code, &digest, &diag); err != nil {
			arows.Close()
			return lin, err
		}
		a.CompletedAt, a.Outcome, a.ErrorCode, a.ResponseDigest, a.Diagnostic =
			nullText(completed), nullText(outcome), nullText(code), nullText(digest), nullText(diag)
		lin.Attempts = append(lin.Attempts, a)
	}
	arows.Close()
	if err := arows.Err(); err != nil {
		return lin, err
	}

	rrows, err := s.QueryContext(ctx, `SELECT receipt_id, dispatch_id, receipt_kind, acceptance_state, execution_state, durable, external_ref, target_observed_at, received_at
		FROM dispatch_receipts WHERE dispatch_id = ? ORDER BY received_at, receipt_id`, dispatchID)
	if err != nil {
		return lin, err
	}
	for rrows.Next() {
		var r ports.ReceiptRecord
		var acceptance, execution, ref, observed sql.NullString
		var durable sql.NullInt64
		if err := rrows.Scan(&r.ReceiptID, &r.DispatchID, &r.ReceiptKind, &acceptance, &execution, &durable, &ref, &observed, &r.ReceivedAt); err != nil {
			rrows.Close()
			return lin, err
		}
		r.AcceptanceState = records.AcceptanceState(nullText(acceptance))
		r.ExecutionState = records.ExecutionState(nullText(execution))
		r.Durable = durable.Valid && durable.Int64 == 1
		r.ExternalRef, r.TargetObservedAt = nullText(ref), nullText(observed)
		lin.Receipts = append(lin.Receipts, r)
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		return lin, err
	}

	trows, err := s.QueryContext(ctx, `SELECT transition_id, entity_type, entity_id, from_state, to_state, recorded_at, context_json
		FROM state_transitions WHERE entity_type = 'dispatch_intent' AND entity_id = ? ORDER BY recorded_at, transition_id`, dispatchID)
	if err != nil {
		return lin, err
	}
	for trows.Next() {
		var t ports.TransitionRecord
		var from sql.NullString
		if err := trows.Scan(&t.TransitionID, &t.EntityType, &t.EntityID, &from, &t.ToState, &t.RecordedAt, &t.ContextJSON); err != nil {
			trows.Close()
			return lin, err
		}
		t.FromState = nullText(from)
		lin.Transitions = append(lin.Transitions, t)
	}
	trows.Close()
	if err := trows.Err(); err != nil {
		return lin, err
	}
	return lin, nil
}

// LoadBatchEvidence returns one retained batch and its normalized changes
// for reprocessing.
func (s *Store) LoadBatchEvidence(ctx context.Context, batchID string) (ports.BatchEvidence, error) {
	var b ports.BatchEvidence
	err := s.QueryRowContext(ctx, `SELECT batch_id, route_id, route_revision, resource_id, created_at, content_fingerprint
		FROM change_batches WHERE batch_id = ?`, batchID).Scan(
		&b.BatchID, &b.RouteID, &b.RouteRevision, &b.ResourceID, &b.CreatedAt, &b.ContentFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return b, fmt.Errorf("batch %s not found", batchID)
	}
	if err != nil {
		return b, err
	}
	rows, err := s.QueryContext(ctx, `SELECT ordinal, path, operation, exists_after, file_type, before_digest, after_digest, digest_status
		FROM observation_changes oc
		JOIN batch_observations bo ON bo.observation_id = oc.observation_id
		WHERE bo.batch_id = ? ORDER BY oc.observation_id, oc.ordinal`, batchID)
	if err != nil {
		return b, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ports.ObservationChange
		var before, after sql.NullString
		if err := rows.Scan(&c.Ordinal, &c.Path, &c.Operation, &c.ExistsAfter, &c.FileType, &before, &after, &c.DigestStatus); err != nil {
			return b, err
		}
		c.BeforeDigest, c.AfterDigest = nullText(before), nullText(after)
		b.Changes = append(b.Changes, c)
	}
	return b, rows.Err()
}

// ApplyOperatorRetry performs the explicit operator retry of a
// dead-lettered dispatch (DUR-009): the domain guard demands the actor
// and the explicit-retry marker.
func (s *Store) ApplyOperatorRetry(ctx context.Context, dispatchID, actor, reason, now string) error {
	now = normalizeTimestamp(now)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ports.ErrIntentNotFound, dispatchID)
		}
		return err
	}
	evidence := state.IntentEvidence{Actor: actor, ExplicitRetry: true}
	if err := state.ValidateIntentTransition(records.IntentState(current), records.IntentReady, state.ReasonExplicitRetry, evidence); err != nil {
		return fmt.Errorf("%w: %v", ports.ErrStateNotEligible, err)
	}
	res, err := tx.Exec(`UPDATE dispatch_intents
		SET state = 'ready', attempt_count = 0, next_attempt_at = NULL, updated_at = ?
		WHERE dispatch_id = ? AND state = 'dead_lettered'`, now, dispatchID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s left dead_lettered concurrently", ports.ErrStateNotEligible, dispatchID)
	}
	transitionID := fmt.Sprintf("%s:%s:operator-retry", dispatchID, now)
	if err := s.AppendTransition(tx, transitionID, "dispatch_intent", dispatchID, current, "ready", now,
		fmt.Sprintf(`{"reason":%q,"actor":%q,"operator_reason":%q,"attempt_budget_reset":true}`, state.ReasonExplicitRetry, actor, reason)); err != nil {
		return err
	}
	return tx.Commit()
}

// MakeRetryDue makes a retry_wait dispatch eligible immediately, keeping
// the request, idempotency key, and attempt budget.
func (s *Store) MakeRetryDue(ctx context.Context, dispatchID, now string) error {
	now = normalizeTimestamp(now)
	res, err := s.ExecContext(ctx, `UPDATE dispatch_intents SET next_attempt_at = NULL, updated_at = ? WHERE dispatch_id = ? AND state = 'retry_wait'`, now, dispatchID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s is not retry_wait", ports.ErrStateNotEligible, dispatchID)
	}
	return nil
}

// RerunIntent persists the caller-built intentional rerun: a new decision
// superseding the original lineage and a new intent reserving the route
// slot when free (CLI-005 posture).
func (s *Store) RerunIntent(ctx context.Context, in ports.RerunInput) (ports.IntentSummary, error) {
	var sum ports.IntentSummary
	now := normalizeTimestamp(in.New.CreatedAt)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return sum, err
	}
	defer tx.Rollback()
	var routeID, originalDecision string
	err = tx.QueryRow(`SELECT route_id, decision_id FROM dispatch_intents WHERE dispatch_id = ?`, in.OriginalDispatchID).Scan(&routeID, &originalDecision)
	if errors.Is(err, sql.ErrNoRows) {
		return sum, fmt.Errorf("%w: %s", ports.ErrIntentNotFound, in.OriginalDispatchID)
	}
	if err != nil {
		return sum, err
	}
	_ = routeID
	newDecision := "dec-" + in.New.DispatchID
	reasonCodes := `["operator_rerun"]`
	if len(in.ReasonCodes) > 0 {
		encoded, err := json.Marshal(in.ReasonCodes)
		if err != nil {
			return sum, err
		}
		reasonCodes = string(encoded)
	}
	// SaveIntent persists decision_id from the input; align it with the
	// superseding decision created above. The superseding decision keeps
	// the route and policy revisions of the replacement intent (E7-T3).
	in.New.DecisionID = newDecision
	if _, err := tx.Exec(`INSERT INTO policy_decisions
		(decision_id, batch_id, route_id, route_revision, policy_revision, generation_lineage_json, disposition, classification, reason_codes_json, created_at, actor, supersedes_decision_id)
		SELECT ?, batch_id, route_id, ?, policy_revision, generation_lineage_json, 'dispatch', 'normal', ?, ?, ?, decision_id
		FROM policy_decisions WHERE decision_id = ?`,
		newDecision, in.New.RouteRevision, reasonCodes, now, in.Actor, originalDecision); err != nil {
		return sum, err
	}
	// The rerun takes over the active slot when its own original holds
	// it: the superseded work is exactly what the operator replaced.
	if err := s.saveIntentTakeOverOriginal(tx, portsIntent(in.New), in.OriginalDispatchID); err != nil {
		return sum, mapIntentConstraint(err)
	}
	// The original leaves its live state through the declared superseded
	// edge in the same transaction, so exactly one authoritative request
	// remains per route (CON-001, E7-T2/B-2).
	var originalState string
	if err := tx.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, in.OriginalDispatchID).Scan(&originalState); err != nil {
		return sum, err
	}
	supersedeContext := func(reason state.IntentReason) string {
		return fmt.Sprintf(`{"reason":%q,"actor":%q,"operator_reason":%q,"superseded_by":%q}`, reason, in.Actor, in.Reason, in.New.DispatchID)
	}
	switch records.IntentState(originalState) {
	case records.IntentReady:
		if err := s.transitionWithin(ctx, tx, in.OriginalDispatchID, records.IntentReady, records.IntentSuperseded, state.ReasonRouteInvalidated,
			state.IntentEvidence{Actor: in.Actor}, now, supersedeContext(state.ReasonRouteInvalidated)); err != nil {
			return sum, err
		}
	case records.IntentDeadLettered:
		if err := s.transitionWithin(ctx, tx, in.OriginalDispatchID, records.IntentDeadLettered, records.IntentSuperseded, state.ReasonReprocessOrDiscard,
			state.IntentEvidence{Actor: in.Actor}, now, supersedeContext(state.ReasonReprocessOrDiscard)); err != nil {
			return sum, err
		}
	default:
		return sum, fmt.Errorf("%w: original %s is %s; rerun requires ready or dead-lettered work", ports.ErrStateNotEligible, in.OriginalDispatchID, originalState)
	}
	// The takeover applies the route transition matching the state it
	// found, so the rerun holds the slot as genuinely active work.
	snap, err := s.routeSnapshotInTx(tx, in.New.RouteID)
	if err != nil {
		return sum, err
	}
	switch snap.State {
	case state.RouteIdle:
		if err := applyRouteTransition(tx, snap, state.RouteActiveClean, state.ReasonDispatchAccepted,
			state.RouteEvidence{Actor: in.Actor, ActivatingDispatchID: in.New.DispatchID}, now,
			fmt.Sprintf(`{"reason":%q,"dispatch_id":%q,"operator_rerun":true}`, state.ReasonDispatchAccepted, in.New.DispatchID)); err != nil {
			return sum, err
		}
	case state.RouteFollowupReady, state.RouteActiveClean, state.RouteActiveDirty:
		// Already an activation shape holding the rerun's own takeover.
	default:
		return sum, fmt.Errorf("%w: route %s is %s; rerun requires a resolved route", ports.ErrStateNotEligible, in.New.RouteID, snap.State)
	}
	if err := s.AppendTransition(tx, in.New.DispatchID+":created", "dispatch_intent", in.New.DispatchID, "", "ready", now,
		fmt.Sprintf(`{"reason":"operator_rerun","actor":%q,"operator_reason":%q,"supersedes_dispatch":%q,"new_generation":%d}`, in.Actor, in.Reason, in.OriginalDispatchID, in.New.Generation)); err != nil {
		return sum, err
	}
	if err := tx.Commit(); err != nil {
		return sum, err
	}
	return s.intentSummaryByID(ctx, in.New.DispatchID)
}

func (s *Store) intentSummaryByID(ctx context.Context, dispatchID string) (ports.IntentSummary, error) {
	var sum ports.IntentSummary
	var next sql.NullString
	err := s.QueryRowContext(ctx, `SELECT dispatch_id, route_id, target_id, generation, idempotency_key, state, attempt_count, next_attempt_at, created_at, updated_at
		FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(
		&sum.DispatchID, &sum.RouteID, &sum.TargetID, &sum.Generation, &sum.IdempotencyKey,
		&sum.State, &sum.AttemptCount, &next, &sum.CreatedAt, &sum.UpdatedAt)
	sum.NextAttemptAt = nullText(next)
	return sum, err
}

// ReconcileUnknown moves an unknown dispatch through reconciling and
// applies the lookup result in one transaction (DUR-006): lookup by
// idempotency key or external reference happens before any other
// submission, and an ambiguous result never switches targets (DUR-008).
func (s *Store) ReconcileUnknown(ctx context.Context, dispatchID, actor string, lookup ports.LookupResult, attemptsExhausted bool, now string) (records.IntentState, error) {
	now = normalizeTimestamp(now)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: %s", ports.ErrIntentNotFound, dispatchID)
		}
		return "", err
	}
	if current != string(records.IntentUnknown) {
		return "", fmt.Errorf("%w: %s is %s, not unknown", ports.ErrStateNotEligible, dispatchID, current)
	}
	// unknown -> reconciling with the actor guard.
	if err := s.transitionWithin(ctx, tx, dispatchID, records.IntentUnknown, records.IntentReconciling, state.ReasonLookupStarted,
		state.IntentEvidence{Actor: actor}, now, fmt.Sprintf(`{"reason":%q,"actor":%q}`, state.ReasonLookupStarted, actor)); err != nil {
		return "", err
	}
	evidence := state.IntentEvidence{Actor: actor, Reconciliation: &state.ReconciliationEvidence{
		Result:            lookupToAcceptance(lookup),
		ExternalRef:       lookup.ExternalRef,
		AttemptsExhausted: attemptsExhausted,
	}}
	var to records.IntentState
	var reason state.IntentReason
	switch {
	case lookup.Status == ports.LookupFound && lookup.Acceptance == records.AcceptanceAccepted:
		to, reason = records.IntentAccepted, state.ReasonLookupFoundAccepted
	case lookup.Status == ports.LookupFound && lookup.Acceptance == records.AcceptanceRejected:
		to, reason = records.IntentRetryWait, state.ReasonLookupNotAccepted
	case lookup.Status == ports.LookupAbsent:
		// Proven never accepted: retry while budget remains, otherwise
		// dead-letter on the reached limit.
		if attemptsExhausted {
			to, reason = records.IntentDeadLettered, state.ReasonUnresolvedOrLimit
		} else {
			to, reason = records.IntentRetryWait, state.ReasonLookupNotAccepted
		}
	default: // ambiguous or unresolved lookup
		to, reason = records.IntentDeadLettered, state.ReasonUnresolvedOrLimit
	}
	if err := s.transitionWithin(ctx, tx, dispatchID, records.IntentReconciling, to, reason, evidence, now,
		fmt.Sprintf(`{"reason":%q,"actor":%q,"lookup_status":%q,"attempts_exhausted":%v}`, reason, actor, lookup.Status, attemptsExhausted)); err != nil {
		return "", err
	}
	if to == records.IntentAccepted {
		if _, err := tx.Exec(`UPDATE dispatch_intents SET external_ref = ?, updated_at = ? WHERE dispatch_id = ?`,
			nullString(lookup.ExternalRef), now, dispatchID); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return to, nil
}

// lookupToAcceptance maps a lookup result onto the reconciliation
// evidence axis the domain guards consume.
func lookupToAcceptance(lookup ports.LookupResult) records.AcceptanceState {
	switch {
	case lookup.Status == ports.LookupFound && lookup.Acceptance == records.AcceptanceAccepted:
		return records.AcceptanceAccepted
	case lookup.Status == ports.LookupFound && lookup.Acceptance == records.AcceptanceRejected:
		return records.AcceptanceRejected
	case lookup.Status == ports.LookupAbsent:
		// Absent proves the target never accepted the request, the same
		// non-acceptance evidence a found rejection carries.
		return records.AcceptanceRejected
	default:
		return records.AcceptanceUnknown
	}
}

// transitionWithin applies one domain-validated transition inside the
// caller's transaction.
func (s *Store) transitionWithin(ctx context.Context, tx *sql.Tx, dispatchID string, from, to records.IntentState, reason state.IntentReason, evidence state.IntentEvidence, now, contextJSON string) error {
	if err := state.ValidateIntentTransition(from, to, reason, evidence); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE dispatch_intents SET state = ?, updated_at = ? WHERE dispatch_id = ? AND state = ?`,
		string(to), now, dispatchID, string(from))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("intent %s left %s concurrently: %w", dispatchID, from, ErrOptimisticConcurrency)
	}
	transitionID := fmt.Sprintf("%s:%s:%s", dispatchID, now, reason)
	return s.AppendTransition(tx, transitionID, "dispatch_intent", dispatchID, string(from), string(to), now, contextJSON)
}

// SetRouteActivation updates one route's activation state (E3-T3 route
// enable/disable). Enable records the acknowledged route revision, so a
// later behavior-sensitive revision change requires a fresh
// acknowledgement; disable preserves observations, active work, and dirty
// state.
func (s *Store) SetRouteActivation(ctx context.Context, routeID, activation, acknowledgeRevision, now string) error {
	if activation != "enabled" && activation != "disabled" && activation != "paused" {
		return fmt.Errorf("unknown activation state %q", activation)
	}
	var res sql.Result
	var err error
	if activation == "enabled" {
		res, err = s.ExecContext(ctx, `UPDATE route_runtime_state
			SET activation_state = 'enabled', acknowledged_revision = ?, last_reconciled_at = ?, version = version + 1
			WHERE route_id = ?`, acknowledgeRevision, now, routeID)
	} else {
		res, err = s.ExecContext(ctx, `UPDATE route_runtime_state
			SET activation_state = ?, version = version + 1
			WHERE route_id = ?`, activation, routeID)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("route %s has no runtime state: %w", routeID, ErrOptimisticConcurrency)
	}
	return nil
}

// RouteRow is one listed route with its runtime state.
type RouteRow struct {
	RouteID              string `json:"route_id"`
	Revision             string `json:"revision"`
	ResourceID           string `json:"resource_id"`
	TargetID             string `json:"target_id"`
	ActivationState      string `json:"activation_state"`
	AcknowledgedRevision string `json:"acknowledged_revision"`
	RouteState           string `json:"route_state"`
	ActiveDispatchID     string `json:"active_dispatch_id"`
	DirtyGeneration      int    `json:"dirty_generation"`
	PendingReconcile     int    `json:"pending_reconcile"`
	LastSourcePosition   string `json:"last_source_position,omitempty"`
	LastReconciledAt     string `json:"last_reconciled_at,omitempty"`
}

// ListRoutes returns every materialized route with its runtime state.
func (s *Store) ListRoutes(ctx context.Context) ([]RouteRow, error) {
	rows, err := s.QueryContext(ctx, `SELECT r.route_id, r.revision, r.resource_id, r.target_id,
			COALESCE(rr.activation_state, 'disabled'), COALESCE(rr.acknowledged_revision, ''), COALESCE(rr.route_state, 'IDLE'),
			COALESCE(rr.active_dispatch_id, ''), COALESCE(rr.dirty_generation, 0),
			COALESCE(rr.pending_reconcile, 0), COALESCE(rr.last_source_position, ''), COALESCE(rr.last_reconciled_at, '')
		FROM routes r LEFT JOIN route_runtime_state rr ON rr.route_id = r.route_id
		ORDER BY r.route_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RouteRow
	for rows.Next() {
		var row RouteRow
		if err := rows.Scan(&row.RouteID, &row.Revision, &row.ResourceID, &row.TargetID,
			&row.ActivationState, &row.AcknowledgedRevision, &row.RouteState, &row.ActiveDispatchID, &row.DirtyGeneration,
			&row.PendingReconcile, &row.LastSourcePosition, &row.LastReconciledAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// saveIntentTakeOverOriginal persists the rerun intent and takes over
// the route slot held by the dispatch it supersedes.
func (s *Store) saveIntentTakeOverOriginal(tx *sql.Tx, i IntentRecord, originalDispatchID string) error {
	if _, err := execOn(tx, s.DB, `INSERT INTO dispatch_intents
		(dispatch_id, decision_id, route_id, route_revision, target_id, target_type, target_scope, resource_id, generation, idempotency_key, content_fingerprint, manifest_digest, request_version, request_json, state, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,'ready',?,?)`,
		i.DispatchID, i.DecisionID, i.RouteID, i.RouteRevision, i.TargetID, i.TargetType, i.TargetScope, i.ResourceID, int(i.Generation),
		i.IdempotencyKey, i.ContentFingerprint, i.ManifestDigest, i.RequestVersion, i.RequestJSON, i.CreatedAt, i.CreatedAt); err != nil {
		return err
	}
	res, err := execOn(tx, s.DB, `UPDATE route_runtime_state
		SET active_dispatch_id = ?, active_generation = ?, version = version + 1
		WHERE route_id = ? AND (active_dispatch_id IS NULL OR active_dispatch_id = ?)`,
		i.DispatchID, int(i.Generation), i.RouteID, originalDispatchID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("route %s already has an active dispatch (invariant 5): %w", i.RouteID, ErrOptimisticConcurrency)
	}
	return nil
}

// SaveExecutionProjection appends one execution-projection receipt
// (E4-T4, HER-008): separate from acceptance, one row per refresh so
// the projection history stays inspectable; the caller mints a unique
// receipt id per refresh.
func (s *Store) SaveExecutionProjection(ctx context.Context, in ports.ExecutionProjectionInput) error {
	_, err := s.ExecContext(ctx, `INSERT INTO dispatch_receipts
		(receipt_id, dispatch_id, receipt_kind, execution_state, durable, external_ref, target_observed_at, received_at, payload_version, bounded_payload)
		VALUES (?,?, 'execution_projection', ?, NULL, ?, ?, ?, 'agent-dispatch.execution/v1', ?)`,
		in.ReceiptID, in.DispatchID, string(in.ExecutionState), nullString(in.ExternalRef),
		nullString(in.TargetObservedAt), in.ReceivedAt, in.BoundedPayload)
	return err
}

// ListReceipts returns receipts matching the filter, newest first
// (receipts list, OPS-002). Work receipts live in their own table and
// are selected (or unioned, with an empty kind) from it.
func (s *Store) ListReceipts(ctx context.Context, f ports.ReceiptFilter) ([]ports.ReceiptRecord, error) {
	if f.Kind == "work" {
		return s.listWorkReceipts(ctx, f)
	}
	disp, err := s.listDispatchReceipts(ctx, f, f.Kind)
	if err != nil || f.Kind != "" {
		return disp, err
	}
	work, err := s.listWorkReceipts(ctx, f)
	if err != nil {
		return nil, err
	}
	merged := append(disp, work...)
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].ReceivedAt != merged[j].ReceivedAt {
			return merged[i].ReceivedAt > merged[j].ReceivedAt
		}
		return merged[i].ReceiptID > merged[j].ReceiptID
	})
	if cap := f.Limit; cap > 0 && len(merged) > cap {
		merged = merged[:cap]
	}
	return merged, nil
}

func (s *Store) listDispatchReceipts(ctx context.Context, f ports.ReceiptFilter, kind string) ([]ports.ReceiptRecord, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.DispatchID != "" {
		where = append(where, "r.dispatch_id = ?")
		args = append(args, f.DispatchID)
	}
	if f.RouteID != "" {
		where = append(where, "i.route_id = ?")
		args = append(args, f.RouteID)
	}
	if kind != "" {
		where = append(where, "r.receipt_kind = ?")
		args = append(args, kind)
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args = append(args, limit)
	rows, err := s.QueryContext(ctx, `SELECT r.receipt_id, r.dispatch_id, r.receipt_kind, r.acceptance_state, r.execution_state, r.durable, r.external_ref, r.target_observed_at, r.received_at
		FROM dispatch_receipts r JOIN dispatch_intents i ON i.dispatch_id = r.dispatch_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY r.received_at DESC, r.receipt_id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ports.ReceiptRecord
	for rows.Next() {
		var rec ports.ReceiptRecord
		var acceptance, execution, external, observed sql.NullString
		var durable sql.NullInt64
		if err := rows.Scan(&rec.ReceiptID, &rec.DispatchID, &rec.ReceiptKind, &acceptance, &execution, &durable, &external, &observed, &rec.ReceivedAt); err != nil {
			return nil, err
		}
		rec.AcceptanceState = records.AcceptanceState(acceptance.String)
		rec.ExecutionState = records.ExecutionState(execution.String)
		rec.Durable = durable.Int64 == 1
		rec.ExternalRef = external.String
		rec.TargetObservedAt = observed.String
		out = append(out, rec)
	}
	return out, rows.Err()
}

// loadWorkReceipt returns one work receipt as detail.
func (s *Store) loadWorkReceipt(ctx context.Context, receiptID string) (ports.ReceiptDetail, error) {
	var detail ports.ReceiptDetail
	var status, external, baseRev, resultRev, changes, reasons, failure sql.NullString
	err := s.QueryRowContext(ctx, `SELECT receipt_id, dispatch_id, run_id, resource_id, status, failure_code, external_task_id, base_revision, result_revision, changes_json, submitted_at, validation_state, validation_reasons_json
		FROM work_receipts WHERE receipt_id = ?`, receiptID).
		Scan(&detail.ReceiptID, &detail.DispatchID, &detail.RunID, &detail.ResourceID, &status, &failure,
			&external, &baseRev, &resultRev, &changes, &detail.ReceivedAt, &detail.ValidationState, &reasons)
	detail.FailureCode = failure.String
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return detail, ports.ErrReceiptNotFound
		}
		return detail, err
	}
	detail.ReceiptKind = "work"
	detail.ExternalRef = external.String
	switch status.String {
	case "completed":
		detail.ExecutionState = records.ExecSucceeded
	case "failed":
		detail.ExecutionState = records.ExecFailed
	default:
		detail.ExecutionState = records.ExecRunning
	}
	// The bounded payload is the stored changes projection; work
	// receipts never carry note bodies (DAT-008).
	detail.BoundedPayload = changes.String
	if detail.BoundedPayload == "" {
		detail.BoundedPayload = "{}"
	}
	return detail, nil
}

// listWorkReceipts selects work receipts from their own table, mapped
// onto the shared receipt record shape.
func (s *Store) listWorkReceipts(ctx context.Context, f ports.ReceiptFilter) ([]ports.ReceiptRecord, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.DispatchID != "" {
		where = append(where, "w.dispatch_id = ?")
		args = append(args, f.DispatchID)
	}
	if f.RouteID != "" {
		where = append(where, "i.route_id = ?")
		args = append(args, f.RouteID)
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args = append(args, limit)
	rows, err := s.QueryContext(ctx, `SELECT w.receipt_id, w.dispatch_id, w.status, w.external_task_id, w.submitted_at
		FROM work_receipts w JOIN dispatch_intents i ON i.dispatch_id = w.dispatch_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY w.submitted_at DESC, w.receipt_id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ports.ReceiptRecord
	for rows.Next() {
		var rec ports.ReceiptRecord
		var status, external sql.NullString
		if err := rows.Scan(&rec.ReceiptID, &rec.DispatchID, &status, &external, &rec.ReceivedAt); err != nil {
			return nil, err
		}
		rec.ReceiptKind = "work"
		rec.ExternalRef = external.String
		// The work-receipt status is the agent run's execution outcome;
		// it maps onto the portable execution axis (begun=running,
		// completed=succeeded, failed=failed).
		switch status.String {
		case "completed":
			rec.ExecutionState = records.ExecSucceeded
		case "failed":
			rec.ExecutionState = records.ExecFailed
		default:
			rec.ExecutionState = records.ExecRunning
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// LoadReceipt returns one receipt with its bounded persisted payload
// (receipts show, OPS-002).
func (s *Store) LoadReceipt(ctx context.Context, receiptID string) (ports.ReceiptDetail, error) {
	var detail ports.ReceiptDetail
	var acceptance, execution, external, observed, payload sql.NullString
	var durable sql.NullInt64
	err := s.QueryRowContext(ctx, `SELECT receipt_id, dispatch_id, receipt_kind, acceptance_state, execution_state, durable, external_ref, target_observed_at, received_at, bounded_payload
		FROM dispatch_receipts WHERE receipt_id = ?`, receiptID).
		Scan(&detail.ReceiptID, &detail.DispatchID, &detail.ReceiptKind, &acceptance, &execution, &durable, &external, &observed, &detail.ReceivedAt, &payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Work receipts live in their own table; show them too.
			return s.loadWorkReceipt(ctx, receiptID)
		}
		return detail, err
	}
	detail.AcceptanceState = records.AcceptanceState(acceptance.String)
	detail.ExecutionState = records.ExecutionState(execution.String)
	detail.Durable = durable.Int64 == 1
	detail.ExternalRef = external.String
	detail.TargetObservedAt = observed.String
	detail.BoundedPayload = payload.String
	return detail, nil
}

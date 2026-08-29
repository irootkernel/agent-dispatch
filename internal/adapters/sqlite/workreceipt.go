package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"

	"modernc.org/sqlite"
)

// Work-receipt persistence (E5-T1): one row per (dispatch, run) pair —
// `work begin` inserts it, `work complete` / `work fail` update it inside
// the atomic completion transaction, and audited-invalid receipts keep
// their evidence through the append-only transition history (FBK-003).

// LoadWorkReceipt returns the run's receipt row with its child-lane
// association (E12-T3, DAT-011: the destination comes from the child
// join; empty for a pre-cutover legacy dispatch).
func (s *Store) LoadWorkReceipt(ctx context.Context, dispatchID, runID string) (ports.WorkReceiptView, error) {
	var view ports.WorkReceiptView
	var failureCode, begunAt, manualReason sql.NullString
	err := s.QueryRowContext(ctx, `SELECT w.receipt_id, w.dispatch_id, w.run_id, w.status, w.failure_code, w.validation_state, w.validation_reasons_json, w.submitted_at, w.begun_at, w.manual_reason,
			COALESCE((SELECT c.destination_id FROM child_dispatches c WHERE c.dispatch_id = w.dispatch_id), '')
		FROM work_receipts w WHERE w.dispatch_id = ? AND w.run_id = ?`, dispatchID, runID).
		Scan(&view.ReceiptID, &view.DispatchID, &view.RunID, &view.Status, &failureCode, &view.ValidationState, &view.ValidationReasonsJSON, &view.SubmittedAt, &begunAt, &manualReason, &view.DestinationID)
	if errors.Is(err, sql.ErrNoRows) {
		return view, fmt.Errorf("%w: dispatch %s run %s", ports.ErrWorkReceiptNotFound, dispatchID, runID)
	}
	if err != nil {
		return view, err
	}
	view.FailureCode = nullText(failureCode)
	view.BegunAt = nullText(begunAt)
	view.ManualReason = nullText(manualReason)
	return view, nil
}

// InsertWorkReceipt persists a new begun or audited-invalid receipt. The
// UNIQUE(dispatch_id, run_id) constraint surfaces a replay as
// ErrRunAlreadyRecorded.
func (s *Store) InsertWorkReceipt(ctx context.Context, w ports.WorkReceiptInput) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.SaveWorkReceipt(tx, workReceiptRow(w)); err != nil {
		return mapWorkReceiptConstraint(err)
	}
	if err := s.AppendTransition(tx, w.ReceiptID+":recorded", "work_receipt", w.DispatchID, "", receiptAuditState(w.Status, w.ValidationState), w.SubmittedAt,
		workReceiptAuditContext(w, "")); err != nil {
		return err
	}
	return tx.Commit()
}

// CompleteWork applies the terminal receipt update and the completion
// transaction atomically: the receipt row must still be the begun receipt
// of the same run, and the route transition, dirty-generation collapse,
// and any follow-up scheduling commit with it or not at all.
func (s *Store) CompleteWork(ctx context.Context, w ports.WorkReceiptInput, req ports.ActiveCompletion) (ports.FollowupCreated, error) {
	req.Now = normalizeTimestamp(req.Now)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return ports.FollowupCreated{}, err
	}
	defer tx.Rollback()
	row := workReceiptRow(w)
	res, err := tx.Exec(`UPDATE work_receipts
		SET status = ?, failure_code = ?, result_revision = ?, changes_json = ?, completed_scope_json = ?, remaining_scope_json = ?, manual_reason = ?,
		    submitted_at = ?, validation_state = ?, validation_reasons_json = ?
		WHERE dispatch_id = ? AND run_id = ? AND status = 'begun'`,
		w.Status, nullString(w.FailureCode), nullString(w.ResultRevision), w.ChangesJSON,
		row.CompletedScopeJSON, row.RemainingScopeJSON, nullString(w.ManualReason),
		w.SubmittedAt, w.ValidationState, w.ValidationReasonsJSON,
		w.DispatchID, w.RunID)
	if err != nil {
		return ports.FollowupCreated{}, mapWorkReceiptConstraint(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Distinguish the two contract failures for the caller's error
		// mapping: an absent row versus an already-terminal run.
		var status string
		err := tx.QueryRow(`SELECT status FROM work_receipts WHERE dispatch_id = ? AND run_id = ?`, w.DispatchID, w.RunID).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return ports.FollowupCreated{}, fmt.Errorf("%w: dispatch %s run %s", ports.ErrRunNotBegun, w.DispatchID, w.RunID)
		}
		if err != nil {
			return ports.FollowupCreated{}, err
		}
		return ports.FollowupCreated{}, fmt.Errorf("%w: run %s of dispatch %s is already %s", ports.ErrRunAlreadyRecorded, w.RunID, w.DispatchID, status)
	}
	out, err := s.completeActiveTx(ctx, tx, req)
	if err != nil {
		return out, err
	}
	if err := s.AppendTransition(tx, w.ReceiptID+":"+w.Status, "work_receipt", w.DispatchID, "begun", receiptAuditState(w.Status, w.ValidationState), w.SubmittedAt,
		workReceiptAuditContext(w, req.Actor)); err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

// BlockWork records the blocked outcome (E12-T3, FBK-011): the run's
// begun receipt becomes blocked with its manual reason inside one
// transaction with the audit row. NO lane completion, follow-up
// scheduling, or slot change happens — the child stays active on its
// lane awaiting operator resolution (`work complete`/`work fail` later,
// or a rerun); the automatic retry machinery never touches it because it
// never enters retry_wait or dead_lettered.
func (s *Store) BlockWork(ctx context.Context, w ports.WorkReceiptInput) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if w.ManualReason == "" {
		// An input-shape defect, never a row-state failure: the row-state
		// sentinels stay reserved for absent and already-terminal runs
		// (review round 1, product finding).
		return fmt.Errorf("invalid blocked work receipt: a blocked receipt requires a non-empty manual reason")
	}
	if _, err := records.ParseWorkStatus(w.Status); err != nil || w.Status != string(records.WorkBlocked) {
		return fmt.Errorf("invalid blocked work receipt: status %q is not the blocked outcome", w.Status)
	}
	res, err := tx.Exec(`UPDATE work_receipts
		SET status = ?, manual_reason = ?, submitted_at = ?, validation_state = ?, validation_reasons_json = ?
		WHERE dispatch_id = ? AND run_id = ? AND status = ?`,
		w.Status, w.ManualReason, w.SubmittedAt, w.ValidationState, w.ValidationReasonsJSON, w.DispatchID, w.RunID, string(records.WorkBegan))
	if err != nil {
		return mapWorkReceiptConstraint(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var status string
		err := tx.QueryRow(`SELECT status FROM work_receipts WHERE dispatch_id = ? AND run_id = ?`, w.DispatchID, w.RunID).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: dispatch %s run %s", ports.ErrRunNotBegun, w.DispatchID, w.RunID)
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: run %s of dispatch %s is already %s", ports.ErrRunAlreadyRecorded, w.RunID, w.DispatchID, status)
	}
	if err := s.AppendTransition(tx, w.ReceiptID+":"+w.Status, "work_receipt", w.DispatchID, string(records.WorkBegan),
		receiptAuditState(w.Status, w.ValidationState), w.SubmittedAt, workReceiptAuditContext(w, "")); err != nil {
		return err
	}
	return tx.Commit()
}

// FailureBudgetRemaining returns the configured budget minus the failed
// work completions recorded for the route since its last completed work
// receipt (the consecutive-failure streak the budget bounds).
func (s *Store) FailureBudgetRemaining(ctx context.Context, routeID string, budget int) (int, error) {
	var streak int
	err := s.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_receipts w
		JOIN dispatch_intents d ON d.dispatch_id = w.dispatch_id
		WHERE d.route_id = ? AND w.status = 'failed' AND w.validation_state = 'valid'
		AND w.submitted_at >= COALESCE((
			SELECT MAX(w2.submitted_at) FROM work_receipts w2
			JOIN dispatch_intents d2 ON d2.dispatch_id = w2.dispatch_id
			WHERE d2.route_id = ? AND w2.status = 'completed' AND w2.validation_state = 'valid'
		), '')`, routeID, routeID).Scan(&streak)
	if err != nil {
		return 0, err
	}
	remaining := budget - streak
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

// AuditWorkReceipt appends one work-receipt audit transition (invalid
// receipts never delete evidence, FBK-003).
func (s *Store) AuditWorkReceipt(ctx context.Context, transitionID, dispatchID, fromState, toState, recordedAt, contextJSON string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.AppendTransition(tx, transitionID, "work_receipt", dispatchID, fromState, toState, recordedAt, contextJSON); err != nil {
		return err
	}
	return tx.Commit()
}

// mapWorkReceiptConstraint converts the run uniqueness violation into its
// typed port error.
func mapWorkReceiptConstraint(err error) error {
	var derr *sqlite.Error
	if errors.As(err, &derr) && isUniqueConstraint(derr) {
		return fmt.Errorf("%w: %v", ports.ErrRunAlreadyRecorded, err)
	}
	return err
}

func workReceiptRow(w ports.WorkReceiptInput) WorkReceiptRecord {
	completed, _ := json.Marshal(w.CompletedScope)
	if w.CompletedScope == nil {
		completed = []byte("[]")
	}
	remaining, _ := json.Marshal(w.RemainingScope)
	if w.RemainingScope == nil {
		remaining = []byte("[]")
	}
	return WorkReceiptRecord{
		ReceiptID: w.ReceiptID, DispatchID: w.DispatchID, RunID: w.RunID, ResourceID: w.ResourceID,
		Status: w.Status, FailureCode: w.FailureCode, ExternalTaskID: w.ExternalTaskID,
		BaseRevision: w.BaseRevision, ResultRevision: w.ResultRevision, ChangesJSON: w.ChangesJSON,
		CompletedScopeJSON: string(completed), RemainingScopeJSON: string(remaining), ManualReason: w.ManualReason,
		SubmittedAt: w.SubmittedAt, ValidationState: w.ValidationState, ValidationReasonsJSON: w.ValidationReasonsJSON,
		BegunAt: w.BegunAt,
	}
}

func receiptAuditState(status, validation string) string {
	if validation == "invalid" {
		return "invalid"
	}
	return status
}

func workReceiptAuditContext(w ports.WorkReceiptInput, actor string) string {
	doc := map[string]any{
		"receipt_id": w.ReceiptID, "run_id": w.RunID, "status": w.Status,
		"validation_state": w.ValidationState, "reasons": json.RawMessage(reasonsOrArray(w.ValidationReasonsJSON)),
	}
	if w.FailureCode != "" {
		doc["failure_code"] = w.FailureCode
	}
	if w.ManualReason != "" {
		doc["manual_reason"] = w.ManualReason
	}
	if actor != "" {
		doc["actor"] = actor
	}
	raw, _ := json.Marshal(doc)
	return string(raw)
}

func reasonsOrArray(reasons string) string {
	if reasons == "" {
		return "[]"
	}
	return reasons
}

// LoadActiveGenerationChanges returns every observation change of the
// batches recorded after the active dispatch's generation-window
// watermark (the dirty generation a completion receipt is matched
// against), oldest first. The window is keyed on the monotonic
// batch_seq watermark rather than second-truncated timestamps, so a
// batch merged in the same second as a completion is never re-imported
// into the next generation (E8-T1, H-1.3). The merging decision's class
// and outcome ride along per batch (E12-T2): the follow-up projection
// re-evaluates the completing lane's destination conditions per change,
// and the classification and policy-outcome classes read the decision
// that merged the batch; the correlated subqueries pick each batch's
// first decision deterministically.
func (s *Store) LoadActiveGenerationChanges(ctx context.Context, routeID, dispatchID string) ([]ports.DirtyChange, error) {
	rows, err := s.QueryContext(ctx, `SELECT oc.path, oc.operation, oc.before_digest, oc.after_digest, oc.digest_status, so.observed_at, bo.batch_id,
			COALESCE((SELECT pd.classification FROM policy_decisions pd WHERE pd.batch_id = cb.batch_id ORDER BY pd.created_at, pd.decision_id LIMIT 1), ''),
			COALESCE((SELECT pd.disposition FROM policy_decisions pd WHERE pd.batch_id = cb.batch_id ORDER BY pd.created_at, pd.decision_id LIMIT 1), ''),
			COALESCE(cb.selected_destinations_json, '')
		FROM observation_changes oc
		JOIN source_observations so ON so.observation_id = oc.observation_id
		JOIN batch_observations bo ON bo.observation_id = oc.observation_id
		JOIN change_batches cb ON cb.batch_id = bo.batch_id
		WHERE cb.route_id = ? AND cb.batch_seq > (SELECT COALESCE(base_batch_seq, 0) FROM dispatch_intents WHERE dispatch_id = ?)
		AND bo.batch_id != (SELECT COALESCE((SELECT batch_id FROM policy_decisions
			WHERE decision_id = (SELECT decision_id FROM dispatch_intents WHERE dispatch_id = ?)), ''))
		ORDER BY so.observed_at, bo.batch_id, oc.ordinal`, routeID, dispatchID, dispatchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ports.DirtyChange
	for rows.Next() {
		var c ports.DirtyChange
		var before, after sql.NullString
		var selectionJSON string
		if err := rows.Scan(&c.Path, &c.Operation, &before, &after, &c.DigestStatus, &c.ObservedAt, &c.BatchID, &c.Classification, &c.Disposition, &selectionJSON); err != nil {
			return nil, err
		}
		c.BeforeDigest, c.AfterDigest = nullText(before), nullText(after)
		// The batch's recorded selection evidence rides along (E12 epic
		// validation, migration v15): empty stays empty (the legacy
		// unrecorded shape) — the service filter then falls back to
		// per-change evaluation.
		if selectionJSON != "" {
			var selected []string
			if err := json.Unmarshal([]byte(selectionJSON), &selected); err != nil {
				return nil, fmt.Errorf("batch %s selection evidence is not the recorded JSON shape: %v", c.BatchID, err)
			}
			c.SelectedDestinations = selected
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AuditAttribution appends the suppression-decision evidence for one
// completion receipt (entity attribution, one transition per receipt).
func (s *Store) AuditAttribution(ctx context.Context, transitionID, dispatchID, recordedAt, contextJSON string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.AppendTransition(tx, transitionID, "attribution", dispatchID, "dirty", "evaluated", recordedAt, contextJSON); err != nil {
		return err
	}
	return tx.Commit()
}

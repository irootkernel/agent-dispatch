package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/rootkernel/jjukkumi/internal/ports"

	"modernc.org/sqlite"
)

// Work-receipt persistence (E5-T1): one row per (dispatch, run) pair —
// `work begin` inserts it, `work complete` / `work fail` update it inside
// the atomic completion transaction, and audited-invalid receipts keep
// their evidence through the append-only transition history (FBK-003).

// LoadWorkReceipt returns the run's receipt row.
func (s *Store) LoadWorkReceipt(ctx context.Context, dispatchID, runID string) (ports.WorkReceiptView, error) {
	var view ports.WorkReceiptView
	var failureCode, begunAt sql.NullString
	err := s.QueryRowContext(ctx, `SELECT receipt_id, dispatch_id, run_id, status, failure_code, validation_state, validation_reasons_json, submitted_at, begun_at
		FROM work_receipts WHERE dispatch_id = ? AND run_id = ?`, dispatchID, runID).
		Scan(&view.ReceiptID, &view.DispatchID, &view.RunID, &view.Status, &failureCode, &view.ValidationState, &view.ValidationReasonsJSON, &view.SubmittedAt, &begunAt)
	if errors.Is(err, sql.ErrNoRows) {
		return view, fmt.Errorf("%w: dispatch %s run %s", ports.ErrWorkReceiptNotFound, dispatchID, runID)
	}
	if err != nil {
		return view, err
	}
	view.FailureCode = nullText(failureCode)
	view.BegunAt = nullText(begunAt)
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
	res, err := tx.Exec(`UPDATE work_receipts
		SET status = ?, failure_code = ?, result_revision = ?, changes_json = ?, submitted_at = ?, validation_state = ?, validation_reasons_json = ?
		WHERE dispatch_id = ? AND run_id = ? AND status = 'begun'`,
		w.Status, nullString(w.FailureCode), nullString(w.ResultRevision), w.ChangesJSON, w.SubmittedAt, w.ValidationState, w.ValidationReasonsJSON,
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
	return WorkReceiptRecord{
		ReceiptID: w.ReceiptID, DispatchID: w.DispatchID, RunID: w.RunID, ResourceID: w.ResourceID,
		Status: w.Status, FailureCode: w.FailureCode, ExternalTaskID: w.ExternalTaskID,
		BaseRevision: w.BaseRevision, ResultRevision: w.ResultRevision, ChangesJSON: w.ChangesJSON,
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
	ctx := fmt.Sprintf(`{"receipt_id":%q,"run_id":%q,"status":%q,"validation_state":%q,"reasons":%s`,
		w.ReceiptID, w.RunID, w.Status, w.ValidationState, reasonsOrArray(w.ValidationReasonsJSON))
	if w.FailureCode != "" {
		ctx += fmt.Sprintf(`,"failure_code":%q`, w.FailureCode)
	}
	if actor != "" {
		ctx += fmt.Sprintf(`,"actor":%q`, actor)
	}
	return ctx + "}"
}

func reasonsOrArray(reasons string) string {
	if reasons == "" {
		return "[]"
	}
	return reasons
}

// LoadActiveGenerationChanges returns every observation change of the
// batches recorded since the active dispatch was created (the dirty
// generation a completion receipt is matched against), oldest first.
func (s *Store) LoadActiveGenerationChanges(ctx context.Context, routeID, dispatchID string) ([]ports.DirtyChange, error) {
	rows, err := s.QueryContext(ctx, `SELECT oc.path, oc.operation, oc.before_digest, oc.after_digest, oc.digest_status, so.observed_at, bo.batch_id
		FROM observation_changes oc
		JOIN source_observations so ON so.observation_id = oc.observation_id
		JOIN batch_observations bo ON bo.observation_id = oc.observation_id
		JOIN change_batches cb ON cb.batch_id = bo.batch_id
		WHERE cb.route_id = ? AND cb.created_at >= (SELECT created_at FROM dispatch_intents WHERE dispatch_id = ?)
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
		if err := rows.Scan(&c.Path, &c.Operation, &before, &after, &c.DigestStatus, &c.ObservedAt, &c.BatchID); err != nil {
			return nil, err
		}
		c.BeforeDigest, c.AfterDigest = nullText(before), nullText(after)
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

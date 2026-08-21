package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rootkernel/jjukkumi/internal/domain/state"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// Quarantine and full-reconciliation persistence (E5-T4): the three
// non-dispatching arrival lineages, the operator hold surface with
// release/discard lineage, the single pending reconciliation generation,
// and the full-scope path-fact snapshot.

// CommitQuarantineLineage persists one quarantine-classified arrival
// with its hold and no intent (PTH-008).
func (s *Store) CommitQuarantineLineage(ctx context.Context, lin ports.Lineage, item ports.QuarantineInput) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.SaveObservation(tx, portsObservation(lin.Observation)); err != nil {
		return err
	}
	if err := s.SaveBatch(tx, lin.Batch.BatchID, lin.Batch.RouteID, lin.Batch.RouteRevision,
		lin.Batch.ResourceID, lin.Batch.CreatedAt, lin.Batch.ContentFingerprint, lin.Batch.ObservationIDs); err != nil {
		return err
	}
	if err := s.SaveDecision(tx, portsDecision(lin.Decision)); err != nil {
		return err
	}
	reasons, _ := json.Marshal(item.ReasonCodes)
	if _, err := tx.Exec(`INSERT INTO quarantine_items (quarantine_id, batch_id, decision_id, reason_codes_json, state, created_at)
		VALUES (?,?,?,?, 'held', ?)`, item.QuarantineID, nullString(item.BatchID), item.DecisionID, string(reasons), item.CreatedAt); err != nil {
		return err
	}
	if err := s.AppendTransition(tx, item.QuarantineID+":held", "quarantine", item.QuarantineID, "", "held", item.CreatedAt,
		fmt.Sprintf(`{"reason_codes":%s,"decision_id":%q,"batch_id":%q}`, reasons, item.DecisionID, item.BatchID)); err != nil {
		return err
	}
	return tx.Commit()
}

// CommitDropLineage persists a dropped arrival: the observation and the
// decision remain inspectable, no intent or hold exists.
func (s *Store) CommitDropLineage(ctx context.Context, lin ports.Lineage) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.SaveObservation(tx, portsObservation(lin.Observation)); err != nil {
		return err
	}
	if err := s.SaveBatch(tx, lin.Batch.BatchID, lin.Batch.RouteID, lin.Batch.RouteRevision,
		lin.Batch.ResourceID, lin.Batch.CreatedAt, lin.Batch.ContentFingerprint, lin.Batch.ObservationIDs); err != nil {
		return err
	}
	if err := s.SaveDecision(tx, portsDecision(lin.Decision)); err != nil {
		return err
	}
	return tx.Commit()
}

// CommitReconcileLineage persists one reconcile-classified arrival and
// marks the single pending reconciliation generation (SRC-005: overflow
// and fresh-instance never dispatch partial ordinary changes).
func (s *Store) CommitReconcileLineage(ctx context.Context, lin ports.Lineage, sourcePosition string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.SaveObservation(tx, portsObservation(lin.Observation)); err != nil {
		return err
	}
	if err := s.SaveBatch(tx, lin.Batch.BatchID, lin.Batch.RouteID, lin.Batch.RouteRevision,
		lin.Batch.ResourceID, lin.Batch.CreatedAt, lin.Batch.ContentFingerprint, lin.Batch.ObservationIDs); err != nil {
		return err
	}
	if err := s.SaveDecision(tx, portsDecision(lin.Decision)); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE route_runtime_state SET pending_reconcile = 1,
		last_source_position = COALESCE(?, last_source_position) WHERE route_id = ?`, nullString(sourcePosition), lin.Decision.RouteID); err != nil {
		return err
	}
	if err := s.AppendTransition(tx, lin.Decision.DecisionID+":reconcile", "route", lin.Decision.RouteID, "", "reconcile_pending", lin.Decision.CreatedAt,
		fmt.Sprintf(`{"decision_id":%q,"reason_codes":%s,"pending_reconcile":1}`, lin.Decision.DecisionID, lin.Decision.ReasonCodesJSON)); err != nil {
		return err
	}
	return tx.Commit()
}

// ListQuarantine returns holds matching the filter, newest first.
func (s *Store) ListQuarantine(ctx context.Context, filter ports.QuarantineFilter) ([]ports.QuarantineRecord, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT q.quarantine_id, q.batch_id, q.decision_id, q.reason_codes_json, q.state, q.created_at,
		q.resolved_at, q.resolved_by, q.resolution_reason, q.replacement_decision_id
		FROM quarantine_items q`
	args := []any{}
	if filter.RouteID != "" {
		query += ` JOIN policy_decisions d ON d.decision_id = q.decision_id WHERE d.route_id = ?`
		args = append(args, filter.RouteID)
		if filter.State != "" {
			query += ` AND q.state = ?`
			args = append(args, filter.State)
		}
	} else if filter.State != "" {
		query += ` WHERE q.state = ?`
		args = append(args, filter.State)
	}
	query += ` ORDER BY q.created_at DESC, q.quarantine_id LIMIT ?`
	args = append(args, limit)
	rows, err := s.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ports.QuarantineRecord
	for rows.Next() {
		rec, err := scanQuarantine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// LoadQuarantine returns one hold.
func (s *Store) LoadQuarantine(ctx context.Context, quarantineID string) (ports.QuarantineRecord, error) {
	row := s.QueryRowContext(ctx, `SELECT q.quarantine_id, q.batch_id, q.decision_id, q.reason_codes_json, q.state, q.created_at,
		q.resolved_at, q.resolved_by, q.resolution_reason, q.replacement_decision_id
		FROM quarantine_items q WHERE q.quarantine_id = ?`, quarantineID)
	rec, err := scanQuarantine(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.QuarantineRecord{}, fmt.Errorf("%w: %s", ports.ErrQuarantineNotFound, quarantineID)
	}
	return rec, err
}

func scanQuarantine(row interface{ Scan(...any) error }) (ports.QuarantineRecord, error) {
	var rec ports.QuarantineRecord
	var batch, resolvedAt, resolvedBy, resolutionReason, replacement sql.NullString
	var reasonsJSON string
	err := row.Scan(&rec.QuarantineID, &batch, &rec.DecisionID, &reasonsJSON, &rec.State, &rec.CreatedAt,
		&resolvedAt, &resolvedBy, &resolutionReason, &replacement)
	if err != nil {
		return rec, err
	}
	rec.BatchID, rec.ResolvedAt, rec.ResolvedBy, rec.ResolutionReason, rec.ReplacementDecisionID =
		nullText(batch), nullText(resolvedAt), nullText(resolvedBy), nullText(resolutionReason), nullText(replacement)
	var reasons []string
	if err := json.Unmarshal([]byte(reasonsJSON), &reasons); err == nil {
		rec.ReasonCodes = reasons
	}
	return rec, nil
}

// ReleaseQuarantine resolves one held item by creating the replacement
// reconciliation decision with full operator lineage (CLI-006).
func (s *Store) ReleaseQuarantine(ctx context.Context, quarantineID, actor, reason, now string) (ports.QuarantineRecord, error) {
	return s.resolveQuarantine(ctx, quarantineID, "released", actor, reason, now, true)
}

// DiscardQuarantine resolves one held item without task creation.
func (s *Store) DiscardQuarantine(ctx context.Context, quarantineID, actor, reason, now string) (ports.QuarantineRecord, error) {
	return s.resolveQuarantine(ctx, quarantineID, "discarded", actor, reason, now, false)
}

func (s *Store) resolveQuarantine(ctx context.Context, quarantineID, action, actor, reason, now string, createReplacement bool) (ports.QuarantineRecord, error) {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return ports.QuarantineRecord{}, err
	}
	defer tx.Rollback()
	var rec ports.QuarantineRecord
	var batch, resolvedAt, resolvedBy, resolutionReason, replacement sql.NullString
	var reasonsJSON string
	err = tx.QueryRow(`SELECT quarantine_id, batch_id, decision_id, reason_codes_json, state, created_at,
		resolved_at, resolved_by, resolution_reason, replacement_decision_id
		FROM quarantine_items WHERE quarantine_id = ?`, quarantineID).
		Scan(&rec.QuarantineID, &batch, &rec.DecisionID, &reasonsJSON, &rec.State, &rec.CreatedAt,
			&resolvedAt, &resolvedBy, &resolutionReason, &replacement)
	if err == nil {
		var reasons []string
		if json.Unmarshal([]byte(reasonsJSON), &reasons) == nil {
			rec.ReasonCodes = reasons
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ports.QuarantineRecord{}, fmt.Errorf("%w: %s", ports.ErrQuarantineNotFound, quarantineID)
	}
	if err != nil {
		return ports.QuarantineRecord{}, err
	}
	if rec.State != "held" {
		return ports.QuarantineRecord{}, fmt.Errorf("%w: %s is %s", ports.ErrQuarantineNotHeld, quarantineID, rec.State)
	}
	var routeID string
	if err := tx.QueryRow(`SELECT route_id FROM policy_decisions WHERE decision_id = ?`, rec.DecisionID).Scan(&routeID); err != nil {
		return ports.QuarantineRecord{}, err
	}
	replacementID := ""
	if createReplacement {
		replacementID = "dec-release-" + quarantineID
		if _, err := tx.Exec(`INSERT INTO policy_decisions (decision_id, route_id, route_revision, policy_revision, generation_lineage_json, disposition, classification, reason_codes_json, created_at, actor, supersedes_decision_id)
			SELECT ?, route_id, route_revision, policy_revision, ?, 'reconcile', 'normal', ?, ?, ?, decision_id
			FROM policy_decisions WHERE decision_id = ?`,
			replacementID,
			fmt.Sprintf(`{"route_id":%q,"origin":"quarantine_release","quarantine_id":%q}`, routeID, quarantineID),
			fmt.Sprintf(`["operator_release","quarantine:%s"]`, quarantineID),
			now, actor, rec.DecisionID); err != nil {
			return ports.QuarantineRecord{}, err
		}
		if _, err := tx.Exec(`UPDATE route_runtime_state SET pending_reconcile = 1 WHERE route_id = ?`, routeID); err != nil {
			return ports.QuarantineRecord{}, err
		}
	}
	res, err := tx.Exec(`UPDATE quarantine_items SET state = ?, resolved_at = ?, resolved_by = ?, resolution_reason = ?, replacement_decision_id = ?
		WHERE quarantine_id = ? AND state = 'held'`, action, now, actor, reason, nullString(replacementID), quarantineID)
	if err != nil {
		return ports.QuarantineRecord{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ports.QuarantineRecord{}, fmt.Errorf("%w: %s was resolved concurrently", ports.ErrQuarantineNotHeld, quarantineID)
	}
	if err := s.AppendTransition(tx, quarantineID+":"+action, "quarantine", quarantineID, "held", action, now,
		fmt.Sprintf(`{"actor":%q,"reason":%q,"replacement_decision_id":%q,"previous_decision_id":%q}`, actor, reason, replacementID, rec.DecisionID)); err != nil {
		return ports.QuarantineRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return ports.QuarantineRecord{}, err
	}
	rec.State, rec.ResolvedAt, rec.ResolvedBy, rec.ResolutionReason, rec.ReplacementDecisionID = action, now, actor, reason, replacementID
	return rec, nil
}

// MarkPendingReconcile idempotently marks the pending generation.
func (s *Store) MarkPendingReconcile(ctx context.Context, routeID, sourcePosition, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE route_runtime_state SET pending_reconcile = 1,
		last_source_position = COALESCE(?, last_source_position), last_reconciled_at = ? WHERE route_id = ?`,
		nullString(sourcePosition), now, routeID); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearPendingReconcile resolves the pending generation after an idle
// full reconciliation proved no work remains.
func (s *Store) ClearPendingReconcile(ctx context.Context, routeID, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE route_runtime_state SET pending_reconcile = 0, last_reconciled_at = ? WHERE route_id = ?`, now, routeID); err != nil {
		return err
	}
	return tx.Commit()
}

// ReplacePathFacts stores one full-scope snapshot (previous facts for
// the resource are replaced atomically).
func (s *Store) ReplacePathFacts(ctx context.Context, resourceID string, facts []ports.PathFact, observedAt string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM path_facts WHERE resource_id = ?`, resourceID); err != nil {
		return err
	}
	for _, f := range facts {
		if _, err := tx.Exec(`INSERT INTO path_facts (resource_id, path, digest, "exists", observed_at) VALUES (?,?,?,?,?)`,
			resourceID, f.Path, nullString(f.Digest), f.Exists, observedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LoadPathFacts returns the stored snapshot keyed by path.
func (s *Store) LoadPathFacts(ctx context.Context, resourceID string) (map[string]ports.PathFact, error) {
	rows, err := s.QueryContext(ctx, `SELECT path, digest, "exists", observed_at FROM path_facts WHERE resource_id = ?`, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ports.PathFact{}
	for rows.Next() {
		var f ports.PathFact
		var digest sql.NullString
		if err := rows.Scan(&f.Path, &digest, &f.Exists, &f.ObservedAt); err != nil {
			return nil, err
		}
		f.Digest = nullText(digest)
		out[f.Path] = f
	}
	return out, rows.Err()
}

// CommitReconcileDecision persists one batch-less reconciliation
// decision with generation lineage.
func (s *Store) CommitReconcileDecision(ctx context.Context, d ports.DecisionInput) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.SaveDecision(tx, portsDecision(d)); err != nil {
		return err
	}
	return tx.Commit()
}

// CommitReconcileIntent persists and activates one latest-state
// reconciliation intent for an idle route in a single transaction: the
// intent, its audit transition, and the IDLE to ACTIVE_CLEAN activation
// commit together or not at all.
func (s *Store) CommitReconcileIntent(ctx context.Context, intent ports.IntentInput, actor, now string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.SaveIntent(tx, portsIntent(intent)); err != nil {
		return mapIntentConstraint(err)
	}
	if err := s.AppendTransition(tx, intent.DispatchID+":created", "dispatch_intent", intent.DispatchID, "", "ready", now,
		fmt.Sprintf(`{"reason":"reconcile","route_id":%q,"latest_state":true}`, intent.RouteID)); err != nil {
		return err
	}
	snap, err := s.routeSnapshotInTx(tx, intent.RouteID)
	if err != nil {
		return err
	}
	if err := applyRouteTransition(tx, snap, state.RouteActiveClean, state.ReasonDispatchAccepted,
		state.RouteEvidence{Actor: actor, ActivatingDispatchID: intent.DispatchID}, now,
		fmt.Sprintf(`{"reason":"dispatch_accepted","dispatch_id":%q,"origin":"reconcile"}`, intent.DispatchID)); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE route_runtime_state SET active_dispatch_id = ? WHERE route_id = ? AND (active_dispatch_id IS NULL OR active_dispatch_id = ?)`,
		intent.DispatchID, intent.RouteID, intent.DispatchID); err != nil {
		return err
	}
	return tx.Commit()
}

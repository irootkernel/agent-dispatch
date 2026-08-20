package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/domain/state"
	"github.com/rootkernel/jjukkumi/internal/ports"

	"modernc.org/sqlite"
)

// The Store type implements ports.DispatchStore (E3-T2). Every method
// commits its transaction before returning; no transaction ever spans a
// sink invocation (ADR-0005, DUR-001).

// CommitLineage persists one observation-to-intent lineage and reserves
// the route's active slot in a single transaction (DUR-002).
func (s *Store) CommitLineage(ctx context.Context, lin ports.Lineage) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.SaveObservation(tx, portsObservation(lin.Observation)); err != nil {
		return mapIntentConstraint(err)
	}
	if err := s.SaveBatch(tx, lin.Batch.BatchID, lin.Batch.RouteID, lin.Batch.RouteRevision,
		lin.Batch.ResourceID, lin.Batch.CreatedAt, lin.Batch.ContentFingerprint, lin.Batch.ObservationIDs); err != nil {
		return err
	}
	if err := s.SaveDecision(tx, portsDecision(lin.Decision)); err != nil {
		return err
	}
	if err := s.SaveIntent(tx, portsIntent(lin.Intent)); err != nil {
		return mapIntentConstraint(err)
	}
	return tx.Commit()
}

// mapIntentConstraint converts driver constraint rejections into the
// typed port errors callers branch on. The slot error is typed at its
// source (SaveIntent); unique-constraint identification inspects the
// driver error code and the constrained columns, never prose alone.
func mapIntentConstraint(err error) error {
	if errors.Is(err, ports.ErrRouteSlotHeld) {
		return err
	}
	if errors.Is(err, ErrOptimisticConcurrency) {
		return err
	}
	var derr *sqlite.Error
	if errors.As(err, &derr) && isUniqueConstraint(derr) && strings.Contains(derr.Error(), "idempotency_key") {
		return fmt.Errorf("%w: %v", ports.ErrIdempotencyConflict, err)
	}
	return err
}

// isUniqueConstraint reports a SQLITE_CONSTRAINT_UNIQUE (2067) or
// generic constraint (19) rejection from the driver.
func isUniqueConstraint(err *sqlite.Error) bool {
	code := err.Code()
	return code == 2067 || code == 19
}

func portsObservation(o ports.ObservationInput) ObservationRecord {
	rec := ObservationRecord{
		ObservationID:    o.ObservationID,
		SchemaVersion:    o.SchemaVersion,
		SourceType:       o.SourceType,
		SourceID:         o.SourceID,
		SourceEventKey:   o.SourceEventKey,
		TriggerName:      o.TriggerName,
		ResourceID:       o.ResourceID,
		ObservedAt:       o.ObservedAt,
		ReceivedAt:       o.ReceivedAt,
		RawPayloadDigest: o.RawPayloadDigest,
		IngestStatus:     o.IngestStatus,
		FlagsJSON:        o.FlagsJSON,
	}
	for _, c := range o.Changes {
		rec.Changes = append(rec.Changes, ChangeRecord{
			Ordinal: c.Ordinal, Path: c.Path, Operation: c.Operation, ExistsAfter: c.ExistsAfter,
			FileType: c.FileType, BeforeDigest: c.BeforeDigest, AfterDigest: c.AfterDigest, DigestStatus: c.DigestStatus,
		})
	}
	return rec
}

func portsDecision(d ports.DecisionInput) DecisionRecord {
	return DecisionRecord{
		DecisionID: d.DecisionID, BatchID: d.BatchID, RouteID: d.RouteID, RouteRevision: d.RouteRevision,
		PolicyRevision: d.PolicyRevision, Disposition: d.Disposition, Classification: d.Classification,
		ReasonCodesJSON: d.ReasonCodesJSON, CreatedAt: d.CreatedAt, Actor: d.Actor,
		GenerationLineageJSON: d.GenerationLineageJSON,
	}
}

func portsIntent(i ports.IntentInput) IntentRecord {
	return IntentRecord{
		DispatchID: i.DispatchID, DecisionID: i.DecisionID, RouteID: i.RouteID, RouteRevision: i.RouteRevision,
		TargetID: i.TargetID, TargetType: i.TargetType, ResourceID: i.ResourceID, Generation: int(i.Generation),
		IdempotencyKey: i.IdempotencyKey, ContentFingerprint: i.ContentFingerprint, ManifestDigest: i.ManifestDigest,
		RequestVersion: i.RequestVersion, RequestJSON: i.RequestJSON, CreatedAt: i.CreatedAt,
	}
}

// LoadIntent returns the durable snapshot of one dispatch intent.
func (s *Store) LoadIntent(ctx context.Context, dispatchID string) (ports.IntentSnapshot, error) {
	var snap ports.IntentSnapshot
	var leaseOwner, leaseExpires, nextAttempt, externalRef sql.NullString
	err := s.QueryRowContext(ctx, `SELECT dispatch_id, route_id, target_id, target_type, generation, idempotency_key, state, request_json, manifest_digest, external_ref, lease_owner, lease_expires_at, attempt_count, next_attempt_at
		FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(
		&snap.DispatchID, &snap.RouteID, &snap.TargetID, &snap.TargetType, &snap.Generation, &snap.IdempotencyKey,
		&snap.State, &snap.RequestJSON, &snap.ManifestDigest, &externalRef,
		&leaseOwner, &leaseExpires, &snap.AttemptCount, &nextAttempt)
	if errors.Is(err, sql.ErrNoRows) {
		return snap, fmt.Errorf("%w: %s", ports.ErrIntentNotFound, dispatchID)
	}
	if err != nil {
		return snap, err
	}
	if _, perr := records.ParseIntentState(string(snap.State)); perr != nil {
		return snap, fmt.Errorf("stored intent state %q is not a contract state: %v", snap.State, perr)
	}
	snap.ExternalRef, snap.LeaseOwner, snap.LeaseExpiresAt, snap.NextAttemptAt =
		nullText(externalRef), nullText(leaseOwner), nullText(leaseExpires), nullText(nextAttempt)
	return snap, nil
}

// AcquireAttempt conditionally leases the intent and opens the attempt
// row in one transaction; exactly one competing process succeeds
// (DUR-012, persistence §5).
func (s *Store) AcquireAttempt(ctx context.Context, req ports.AcquireAttempt) (string, error) {
	now := normalizeTimestamp(req.Now)
	expires := normalizeTimestamp(req.LeaseExpiresAt)
	next := normalizeTimestamp(req.NextAttemptAt)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	// Capture the from-state under the same eligibility predicates the
	// conditional update rechecks, so the audit transition is exact.
	var fromState string
	err = tx.QueryRow(`SELECT state FROM dispatch_intents
		WHERE dispatch_id = ?
		  AND state IN ('ready','retry_wait')
		  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		  AND (lease_expires_at IS NULL OR lease_expires_at < ?)`, req.DispatchID, now, now).Scan(&fromState)
	if errors.Is(err, sql.ErrNoRows) {
		return "", s.explainAcquireFailure(ctx, tx, req.DispatchID, now)
	}
	if err != nil {
		return "", err
	}
	res, err := tx.Exec(`UPDATE dispatch_intents
		SET state = 'submitting', lease_owner = ?, lease_expires_at = ?, next_attempt_at = ?, attempt_count = attempt_count + 1, updated_at = ?
		WHERE dispatch_id = ?
		  AND state IN ('ready','retry_wait')
		  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		  AND (lease_expires_at IS NULL OR lease_expires_at < ?)`,
		req.Owner, nullString(expires), nullString(next), now, req.DispatchID, now, now)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", s.explainAcquireFailure(ctx, tx, req.DispatchID, now)
	}
	if _, err := tx.Exec(`INSERT INTO dispatch_attempts (attempt_id, dispatch_id, lease_owner, started_at) VALUES (?,?,?,?)`,
		req.AttemptID, req.DispatchID, req.Owner, now); err != nil {
		return "", err
	}
	reason := state.ReasonLeaseAcquired
	if fromState == string(records.IntentRetryWait) {
		reason = state.ReasonRetryDue
	}
	// One audit entry per (attempt, reason): acquiring and completing the
	// same attempt append distinct transition IDs.
	transitionID := req.AttemptID + ":" + string(reason)
	if err := s.appendValidatedTransition(tx, transitionID, req.DispatchID, records.IntentState(fromState), records.IntentSubmitting,
		reason, state.IntentEvidence{Lease: &state.LeaseEvidence{Owner: req.Owner, ExpiresAt: expires}}, now,
		fmt.Sprintf(`{"reason":%q,"attempt_id":%q,"lease_owner":%q,"lease_expires_at":%q}`, reason, req.AttemptID, req.Owner, expires)); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return req.AttemptID, nil
}

// explainAcquireFailure distinguishes the port errors a lost lease
// competition produces from other refusals.
func (s *Store) explainAcquireFailure(ctx context.Context, q queryer, dispatchID, now string) error {
	var current string
	var leaseExpires sql.NullString
	err := q.QueryRow(`SELECT state, lease_expires_at FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&current, &leaseExpires)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", ports.ErrIntentNotFound, dispatchID)
	}
	if err != nil {
		return err
	}
	if current == string(records.IntentSubmitting) && leaseExpires.Valid && leaseExpires.String >= now {
		return fmt.Errorf("%w: dispatch %s leased to another owner until %s", ports.ErrLeaseHeld, dispatchID, leaseExpires.String)
	}
	return fmt.Errorf("attempt not acquirable for %s in state %s at %s: %w", dispatchID, current, now, ErrOptimisticConcurrency)
}

// CompleteAttempt atomically records the attempt outcome, an optional
// acceptance receipt, and the domain-validated intent transition with its
// audit entry (DUR-011).
func (s *Store) CompleteAttempt(ctx context.Context, res ports.AttemptResult) error {
	completedAt := normalizeTimestamp(res.CompletedAt)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, res.DispatchID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ports.ErrIntentNotFound, res.DispatchID)
		}
		return err
	}
	from := records.IntentState(current)
	if err := state.ValidateIntentTransition(from, res.Transition.To, res.Transition.Reason, res.Transition.Evidence); err != nil {
		return err
	}
	var upd sql.Result
	if res.NextAttemptAt != "" {
		upd, err = tx.Exec(`UPDATE dispatch_intents SET state = ?, lease_owner = NULL, lease_expires_at = NULL, next_attempt_at = ?, updated_at = ? WHERE dispatch_id = ? AND state = ?`,
			string(res.Transition.To), normalizeTimestamp(res.NextAttemptAt), completedAt, res.DispatchID, current)
	} else {
		upd, err = tx.Exec(`UPDATE dispatch_intents SET state = ?, lease_owner = NULL, lease_expires_at = NULL, updated_at = ? WHERE dispatch_id = ? AND state = ?`,
			string(res.Transition.To), completedAt, res.DispatchID, current)
	}
	if err != nil {
		return err
	}
	if n, _ := upd.RowsAffected(); n == 0 {
		return fmt.Errorf("intent %s left %s concurrently: %w", res.DispatchID, current, ErrOptimisticConcurrency)
	}
	if _, err := tx.Exec(`UPDATE dispatch_attempts SET completed_at = ?, outcome = ?, error_code = ?, response_digest = ?, diagnostic = ? WHERE attempt_id = ?`,
		completedAt, nullString(res.Outcome), nullString(res.ErrorCode), nullString(res.ResponseDigest), nullString(res.Diagnostic), res.AttemptID); err != nil {
		return err
	}
	if res.Receipt != nil {
		if _, err := tx.Exec(`INSERT INTO dispatch_receipts
			(receipt_id, dispatch_id, receipt_kind, acceptance_state, durable, external_ref, target_observed_at, received_at, payload_version, bounded_payload)
			VALUES (?,?,'acceptance',?,?,?,?,?,?,?)`,
			res.Receipt.ReceiptID, res.DispatchID, string(res.Receipt.Acceptance), boolInt(res.Receipt.Durable),
			nullString(res.Receipt.ExternalRef), nullString(res.Receipt.TargetObservedAt), res.Receipt.ReceivedAt,
			nullString(res.Receipt.PayloadVersion), res.Receipt.BoundedPayload); err != nil {
			return err
		}
	}
	if err := s.appendValidatedTransition(tx, res.Transition.TransitionID, res.DispatchID, from, res.Transition.To, res.Transition.Reason,
		res.Transition.Evidence, completedAt,
		fmt.Sprintf(`{"reason":%q,"attempt_id":%q,"outcome":%q}`, res.Transition.Reason, res.AttemptID, res.Outcome)); err != nil {
		return err
	}
	return tx.Commit()
}

// RecoverExpiredSubmitting moves every submitting intent whose lease
// expired to unknown with audit evidence and closes its open attempt row;
// an abandoned submitting state defaults to unknown (persistence §5).
func (s *Store) RecoverExpiredSubmitting(ctx context.Context, now string) ([]ports.RecoveredLease, error) {
	now = normalizeTimestamp(now)
	rows, err := s.QueryContext(ctx, `SELECT dispatch_id, lease_owner FROM dispatch_intents
		WHERE state = 'submitting' AND lease_expires_at IS NOT NULL AND lease_expires_at < ?`, now)
	if err != nil {
		return nil, err
	}
	var recovered []ports.RecoveredLease
	for rows.Next() {
		var dispatchID, owner string
		if err := rows.Scan(&dispatchID, &owner); err != nil {
			rows.Close()
			return nil, err
		}
		recovered = append(recovered, ports.RecoveredLease{DispatchID: dispatchID, Owner: owner})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []ports.RecoveredLease
	for _, r := range recovered {
		attemptID, err := s.recoverOne(ctx, r, now)
		if err != nil {
			return out, err
		}
		r.AttemptID = attemptID
		out = append(out, r)
	}
	return out, nil
}

func (s *Store) recoverOne(ctx context.Context, r ports.RecoveredLease, now string) (string, error) {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	upd, err := tx.Exec(`UPDATE dispatch_intents
		SET state = 'unknown', lease_owner = NULL, lease_expires_at = NULL, updated_at = ?
		WHERE dispatch_id = ? AND state = 'submitting' AND lease_expires_at IS NOT NULL AND lease_expires_at < ?`,
		now, r.DispatchID, now)
	if err != nil {
		return "", err
	}
	if n, _ := upd.RowsAffected(); n == 0 {
		return "", nil // recovered concurrently; nothing to do
	}
	var attemptID string
	if err := tx.QueryRow(`SELECT attempt_id FROM dispatch_attempts WHERE dispatch_id = ? AND completed_at IS NULL
		ORDER BY started_at DESC, attempt_id DESC LIMIT 1`, r.DispatchID).Scan(&attemptID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if attemptID != "" {
		if _, err := tx.Exec(`UPDATE dispatch_attempts SET completed_at = ?, outcome = 'unknown', error_code = 'lease_expired', diagnostic = ? WHERE attempt_id = ? AND completed_at IS NULL`,
			now, "lease expired before completion; outcome ambiguous", attemptID); err != nil {
			return "", err
		}
	}
	transitionID := r.DispatchID + ":" + now + ":recover"
	if attemptID != "" {
		transitionID = attemptID + ":" + string(state.ReasonAmbiguousOutcome)
	}
	if err := s.appendValidatedTransition(tx, transitionID, r.DispatchID, records.IntentSubmitting, records.IntentUnknown,
		state.ReasonAmbiguousOutcome, state.IntentEvidence{},
		now, fmt.Sprintf(`{"reason":%q,"attempt_id":%q,"recovered_from_owner":%q,"lease_expired":true}`, state.ReasonAmbiguousOutcome, attemptID, r.Owner)); err != nil {
		return "", err
	}
	return attemptID, tx.Commit()
}

// appendValidatedTransition re-checks the domain guards and appends the
// audit entry inside the caller's transaction.
func (s *Store) appendValidatedTransition(tx *sql.Tx, transitionID, dispatchID string, from, to records.IntentState, reason state.IntentReason, evidence state.IntentEvidence, recordedAt, contextJSON string) error {
	if err := state.ValidateIntentTransition(from, to, reason, evidence); err != nil {
		return err
	}
	return s.AppendTransition(tx, transitionID, "dispatch_intent", dispatchID, string(from), string(to), recordedAt, contextJSON)
}

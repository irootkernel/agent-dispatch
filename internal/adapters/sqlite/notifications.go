package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Durable notification outbox (E13-T1, ADR-0019): the transactional
// enqueue joins the owning state-transition transactions; this file adds
// the enqueue seam, the policy resolver the CLI installs, and the
// inspection/attempt surface of ports.NotificationStore. Delivery itself
// arrives with E13-T2 and records its outcomes through
// RecordNotificationAttempt without ever touching another table
// (NTF-005).

// notificationSourceBound caps the safe source fields one notification
// payload carries: the projection stays bounded even if a future
// emission site grows its context.
const notificationSourceBound = 12

// notificationSourceValueBound caps one source field's value and its
// key (E13-T1 review round 1 F003, reconciled by the epic audit): the
// payload's safety is enforced at this boundary, not left to call-site
// discipline. A violating field is DROPPED whole — never truncated,
// never leaked, and never blocking the owning transition (ADR-0019: a
// notification defect must not affect state): the notification still
// commits with its remaining safe fields.
const (
	notificationSourceValueBound = 256
	notificationSourceKeyBound   = 64
)

// sanitizeNotificationSource enforces the bounded, safe payload
// projection (SEC-011): at most notificationSourceBound fields, each
// with a non-empty bounded key and at most notificationSourceValueBound
// bytes of valid UTF-8 without control characters. Violating fields are
// dropped whole; the caller proceeds with the safe remainder.
func sanitizeNotificationSource(source map[string]string) map[string]string {
	safe := make(map[string]string, len(source))
	for k, v := range source {
		if k == "" || v == "" || len(k) > notificationSourceKeyBound || len(v) > notificationSourceValueBound {
			continue
		}
		if !utf8.ValidString(v) {
			continue
		}
		unsafe := false
		for _, r := range v {
			if r < 0x20 || r == 0x7f {
				unsafe = true
				break
			}
		}
		if !unsafe {
			safe[k] = v
		}
	}
	// The field-count bound drops deterministically: the canonically
	// first bound keys stay, the overflow drops.
	if len(safe) > notificationSourceBound {
		keys := make([]string, 0, len(safe))
		for k := range safe {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys[notificationSourceBound:] {
			delete(safe, k)
		}
	}
	return safe
}

// SetNotificationPolicy installs the per-route effective-policy resolver
// the transactional enqueue consults (E13-T1): the CLI builds it from
// the loaded configuration, so the policy evaluated inside a transition
// transaction is exactly the policy of the command run that owns it. A
// nil resolver — the default for every store without one — disables
// notification creation entirely (NTF-001), which keeps stores opened by
// doctor, tests, and read-only surfaces inert.
func (s *Store) SetNotificationPolicy(resolve func(routeID string) *ports.NotificationPolicy) {
	s.notificationPolicy = resolve
}

// enqueueNotificationTx creates the notification intents of one
// reportable transition inside the caller's transaction (DUR-016): one
// intent per configured sink whose effective policy carries the event,
// each collapsing onto its deterministic identity when the same
// transition is evaluated again (NTF-003, AC-902). The source map is the
// bounded, channel-neutral payload projection: safe identities, states,
// and reason codes only — document contents, front matter, resolved
// secrets, and unredacted paths never enter it (SEC-011).
func (s *Store) enqueueNotificationTx(tx *sql.Tx, routeID string, event records.NotificationEventKind, transition, destinationID string, source map[string]string, now string) (int, error) {
	if s.notificationPolicy == nil {
		return 0, nil
	}
	policy := s.notificationPolicy(routeID)
	if policy == nil || len(policy.Sinks) == 0 || policy.Revision == "" {
		return 0, nil
	}
	if !records.NotificationEventKinds(policy.Events).Contains(event) {
		return 0, nil
	}
	// The payload projection is enforced before any sink write: an
	// unbounded or unsafe source value drops whole instead of leaking
	// into a notification, and the transition is never blocked (the
	// epic audit's reconciliation of the round-2 availability coupling).
	source = sanitizeNotificationSource(source)
	sinks := make([]ports.NotificationSinkRef, len(policy.Sinks))
	copy(sinks, policy.Sinks)
	sort.Slice(sinks, func(i, j int) bool { return sinks[i].ID < sinks[j].ID })
	now = normalizeTimestamp(now)
	created := 0
	for _, sink := range sinks {
		notificationID := records.NotificationID(event, destinationID, transition, sink.ID, policy.Revision)
		payload, err := notificationPayloadJSON(notificationID, routeID, event, destinationID, transition, sink.ID, policy.Revision, source, now)
		if err != nil {
			return 0, err
		}
		// A plain INSERT, never OR IGNORE (the epic audit's reconciliation
		// of the E13-T1 deferral): the only tolerated failure is the
		// dedup hit on the deterministic identity — every other
		// constraint violation (a CHECK or NOT NULL drift) fails loudly
		// instead of silently dropping the notification.
		// A fresh notification intent is immediately due (E16-T1,
		// NTF-015): due_at starts at creation time and only a retryable
		// or ambiguous outcome pushes it into the backoff future.
		_, err = tx.Exec(`INSERT INTO notification_events
			(notification_id, route_id, event, destination_id, transition, sink_id, sink_type, policy_revision, idempotency_key, payload_json, state, created_at, due_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
			notificationID, routeID, string(event), nullString(destinationID), transition, sink.ID, sink.Type, policy.Revision,
			records.NotificationIdempotencyKey(notificationID), payload, now, now)
		if err != nil {
			// The only tolerated insert failure is the dedup hit: the
			// deterministic identity already exists (a replayed or rerun
			// transition). It is proven by the row's EXISTENCE — never by
			// matching driver error text, which would silently absorb a
			// CHECK or NOT NULL drift as a fake dedup.
			var existing int
			if qerr := tx.QueryRow(`SELECT COUNT(*) FROM notification_events WHERE notification_id = ?`, notificationID).Scan(&existing); qerr == nil && existing > 0 {
				continue
			}
			return 0, err
		}
		created++
	}
	return created, nil
}

// notificationPayloadJSON renders the notification-event/v1 projection
// through the JSON encoder (never hand-assembled quoting): the bounded
// source fields ride as a nested object of safe string values. The
// projection is already validated; the render keeps the bound as a
// defensive invariant.
func notificationPayloadJSON(notificationID, routeID string, event records.NotificationEventKind, destinationID, transition, sinkID, policyRevision string, source map[string]string, now string) (string, error) {
	keys := make([]string, 0, len(source))
	for k := range source {
		if v := source[k]; v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) > notificationSourceBound {
		return "", fmt.Errorf("notification payload: source projection exceeds %d fields", notificationSourceBound)
	}
	safe := make(map[string]string, len(keys))
	for _, k := range keys {
		safe[k] = source[k]
	}
	doc := map[string]any{
		"schema_version":               "agent-dispatch.notification-event/v1",
		"notification_id":              notificationID,
		"route_id":                     routeID,
		"event":                        string(event),
		"transition":                   transition,
		"sink_id":                      sinkID,
		"notification_policy_revision": policyRevision,
		"created_at":                   now,
		"source":                       safe,
	}
	if destinationID != "" {
		doc["destination_id"] = destinationID
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("notification payload: %w", err)
	}
	return string(raw), nil
}

// notifyPendingReconcileTx enqueues the reconciliation_required intent of
// a pending-generation transition inside the caller's transaction,
// BEFORE the caller's UPDATE sets the flag: the notification fires only
// when the route was not already pending — the state appearing is the
// reportable transition, and an already-pending route that receives
// another mark is not a second appearance (OPS-013, AC-901). The
// occurrence discriminator keeps replays of the same mark deduplicated
// while a genuinely later generation at a new occurrence notifies again.
func (s *Store) notifyPendingReconcileTx(tx *sql.Tx, routeID, occurrence string, source map[string]string, now string) error {
	if s.notificationPolicy == nil {
		return nil
	}
	var pending int
	if err := tx.QueryRow(`SELECT pending_reconcile FROM route_runtime_state WHERE route_id = ?`, routeID).Scan(&pending); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if pending != 0 {
		return nil
	}
	_, err := s.enqueueNotificationTx(tx, routeID, records.EventReconciliationRequired, "route:"+routeID+":pending_reconcile:"+occurrence, "", source, now)
	return err
}

// notificationEventOfWork maps one completion's lane transition onto its
// reportable event class (E13-T1, NTF-002 vocabulary): a clean or
// dirty-collapsed completion with work done is work_completed; a failed
// completion that keeps retry budget is work_failed; the UNCERTAIN
// holds — retry budget exhausted or follow-up budget exhausted — are
// work_exhausted.
func notificationEventOfWork(to state.RouteState, failed bool) records.NotificationEventKind {
	if to == state.RouteUncertain {
		return records.EventWorkExhausted
	}
	if failed {
		return records.EventWorkFailed
	}
	return records.EventWorkCompleted
}

// EnqueueRouteNotification creates the notification intents of one
// route-scoped reportable transition that has no owning store method —
// the E13-T2 drift evaluation pass: one transaction around the shared
// enqueue, so a drift appearance commits its intents atomically under
// the effective policy (OPS-013) exactly like the store's internal
// transitions (DUR-016 posture).
func (s *Store) EnqueueRouteNotification(ctx context.Context, routeID string, event records.NotificationEventKind, transition, destinationID string, source map[string]string, now string) (int, error) {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	created, err := s.enqueueNotificationTx(tx, routeID, event, transition, destinationID, source, now)
	if err != nil {
		return 0, err
	}
	return created, tx.Commit()
}

// RetryNotification is the sole operator bypass around the drain
// backoff (E16-T2, NTF-013): an ambiguous, retryable, or refused record
// returns to pending and becomes immediately due — the persisted
// backoff deadline is deliberately discarded. A delivered success never
// re-arms (the endpoint already holds the idempotency key). The stable
// idempotency identity is untouched, so the retried delivery presents
// the same key the endpoint deduplicated before (NTF-007); nothing
// outside the notification tables changes (NTF-005).
func (s *Store) RetryNotification(ctx context.Context, notificationID string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state, leaseOwner, leaseExpiresAt string
	if err := tx.QueryRow(`SELECT state, COALESCE(lease_owner, ''), COALESCE(lease_expires_at, '') FROM notification_events WHERE notification_id = ?`, notificationID).Scan(&state, &leaseOwner, &leaseExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ports.ErrNotificationNotFound, notificationID)
		}
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	switch records.NotificationState(state) {
	case records.NotificationDelivered:
		return fmt.Errorf("%w: %s is already delivered", ports.ErrStateNotEligible, notificationID)
	case records.NotificationPending:
		// A live lease is a transient conflict (round-4 F012): the
		// drainer holding it owns the in-flight outcome, so the bypass
		// refuses instead of silently superseding it when the fenced
		// record lands. The lease predicate rides the UPDATE itself
		// (E17 audit F006): a claim landing between the check above and
		// this write fails the conditional instead of re-arming claimed
		// work — the affected-rows check maps that race to the same
		// live-lease refusal.
		if leaseOwner != "" && leaseExpiresAt > now {
			return fmt.Errorf("%w: %s is under a live delivery lease until %s", ports.ErrNotificationLeaseActive, notificationID, leaseExpiresAt)
		}
		res, err := tx.Exec(`UPDATE notification_events SET due_at = ? WHERE notification_id = ? AND state = 'pending'
			AND (lease_owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?)`, now, notificationID, now)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return fmt.Errorf("%w: %s acquired a live delivery lease during the retry", ports.ErrNotificationLeaseActive, notificationID)
		}
		return tx.Commit()
	case records.NotificationRefused:
		if _, err := tx.Exec(`UPDATE notification_events SET state = 'pending', resolved_at = NULL, due_at = ? WHERE notification_id = ? AND state = 'refused'`, now, notificationID); err != nil {
			return err
		}
		return tx.Commit()
	default:
		return fmt.Errorf("unknown notification state %q", state)
	}
}

// CountNotificationsByTransition counts the durable notifications of
// one route transition occurrence: the drain's replay-versus-filtered
// diagnostic reads through this store surface instead of raw SQL from
// the CLI layer (E13 epic audit).
func (s *Store) CountNotificationsByTransition(ctx context.Context, routeID, transition string) (int, error) {
	var n int
	if err := s.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_events WHERE route_id = ? AND transition = ?`, routeID, transition).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// PendingNotifications returns the pending delivery work, oldest
// first, bounded (the drain surface).
func (s *Store) PendingNotifications(ctx context.Context, limit int) ([]ports.NotificationEventRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.QueryContext(ctx, `SELECT e.notification_id, e.route_id, e.event, e.destination_id, e.transition, e.sink_id, e.sink_type, e.policy_revision, e.payload_json, e.state, e.idempotency_key, e.created_at, e.resolved_at,
		COALESCE(c.attempts, 0), COALESCE(last.outcome, '')`+notificationAttemptJoinSQL+` WHERE e.state = 'pending' ORDER BY e.created_at, e.notification_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ports.NotificationEventRecord
	for rows.Next() {
		rec, err := scanNotificationEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// notificationAttemptJoinSQL is the shared attempt projection of the
// listing surfaces: each intent's attempt count and the outcome of its
// latest attempt (E13-T2).
const notificationAttemptJoinSQL = `
		FROM notification_events e
		LEFT JOIN (SELECT notification_id, COUNT(*) AS attempts, MAX(attempt_number) AS last_number
			FROM notification_attempts GROUP BY notification_id) c ON c.notification_id = e.notification_id
		LEFT JOIN notification_attempts last ON last.notification_id = e.notification_id AND last.attempt_number = c.last_number`

// ListNotifications returns notifications matching the filter, newest
// first, with the attempt projection joined (NTF-004: every intent and
// outcome stays inspectable).
func (s *Store) ListNotifications(ctx context.Context, filter ports.NotificationFilter) ([]ports.NotificationEventRecord, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT e.notification_id, e.route_id, e.event, e.destination_id, e.transition, e.sink_id, e.sink_type, e.policy_revision, e.payload_json, e.state, e.idempotency_key, e.created_at, e.resolved_at,
		COALESCE(c.attempts, 0), COALESCE(last.outcome, '')` + notificationAttemptJoinSQL
	conds := []string{}
	args := []any{}
	if filter.RouteID != "" {
		conds = append(conds, "e.route_id = ?")
		args = append(args, filter.RouteID)
	}
	if filter.State != "" {
		conds = append(conds, "e.state = ?")
		args = append(args, filter.State)
	}
	if filter.SinkID != "" {
		conds = append(conds, "e.sink_id = ?")
		args = append(args, filter.SinkID)
	}
	if filter.Event != "" {
		conds = append(conds, "e.event = ?")
		args = append(args, string(filter.Event))
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY e.created_at DESC, e.notification_id LIMIT ?"
	args = append(args, limit)
	rows, err := s.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ports.NotificationEventRecord
	for rows.Next() {
		rec, err := scanNotificationEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// LoadNotification returns one notification by id.
func (s *Store) LoadNotification(ctx context.Context, notificationID string) (ports.NotificationEventRecord, error) {
	var rec ports.NotificationEventRecord
	var destinationID, resolvedAt sql.NullString
	err := s.QueryRowContext(ctx, `SELECT notification_id, route_id, event, destination_id, transition, sink_id, sink_type, policy_revision, payload_json, state, idempotency_key, created_at, resolved_at
		FROM notification_events WHERE notification_id = ?`, notificationID).Scan(
		&rec.NotificationID, &rec.RouteID, &rec.Event, &destinationID, &rec.Transition, &rec.SinkID, &rec.SinkType,
		&rec.PolicyRevision, &rec.PayloadJSON, &rec.State, &rec.IdempotencyKey, &rec.CreatedAt, &resolvedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.NotificationEventRecord{}, fmt.Errorf("%w: %s", ports.ErrNotificationNotFound, notificationID)
		}
		return ports.NotificationEventRecord{}, err
	}
	rec.DestinationID, rec.ResolvedAt = destinationID.String, resolvedAt.String
	return rec, nil
}

// ListNotificationAttempts returns one notification's attempts in
// attempt order (NTF-004).
func (s *Store) ListNotificationAttempts(ctx context.Context, notificationID string) ([]ports.NotificationAttemptRecord, error) {
	rows, err := s.QueryContext(ctx, `SELECT attempt_id, notification_id, attempt_number, outcome, error_code, response_digest, started_at, completed_at
		FROM notification_attempts WHERE notification_id = ? ORDER BY attempt_number`, notificationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ports.NotificationAttemptRecord
	for rows.Next() {
		var rec ports.NotificationAttemptRecord
		var errorCode, responseDigest sql.NullString
		if err := rows.Scan(&rec.AttemptID, &rec.NotificationID, &rec.AttemptNumber, &rec.Outcome, &errorCode, &responseDigest, &rec.StartedAt, &rec.CompletedAt); err != nil {
			return nil, err
		}
		rec.ErrorCode, rec.ResponseDigest = errorCode.String, responseDigest.String
		out = append(out, rec)
	}
	return out, rows.Err()
}

// RecordNotificationAttempt durably records one delivery attempt and
// applies its outcome to the notification's state (NTF-004/NTF-005): a
// definite success resolves the notification delivered, a definite
// refusal resolves it refused, and an ambiguous or retryable outcome
// leaves it pending for the stable idempotency key's next retry. The
// attempt identity is the deterministic nth-attempt derivation, so a
// crash between the sink outcome and this record replays idempotently.
// Nothing outside the notification tables is read or written.
func (s *Store) RecordNotificationAttempt(ctx context.Context, in ports.NotificationAttemptInput) (ports.NotificationAttemptRecord, error) {
	if _, err := records.ParseNotificationAttemptOutcome(string(in.Outcome)); err != nil {
		return ports.NotificationAttemptRecord{}, err
	}
	startedAt := normalizeTimestamp(in.StartedAt)
	completedAt := normalizeTimestamp(in.CompletedAt)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return ports.NotificationAttemptRecord{}, err
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRow(`SELECT state FROM notification_events WHERE notification_id = ?`, in.NotificationID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.NotificationAttemptRecord{}, fmt.Errorf("%w: %s", ports.ErrNotificationNotFound, in.NotificationID)
		}
		return ports.NotificationAttemptRecord{}, err
	}
	var next int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(attempt_number), 0) + 1 FROM notification_attempts WHERE notification_id = ?`, in.NotificationID).Scan(&next); err != nil {
		return ports.NotificationAttemptRecord{}, err
	}
	attemptID := records.NotificationAttemptID(in.NotificationID, next)
	if _, err := tx.Exec(`INSERT INTO notification_attempts (attempt_id, notification_id, attempt_number, outcome, error_code, response_digest, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		attemptID, in.NotificationID, next, string(in.Outcome), nullString(in.ErrorCode), nullString(in.ResponseDigest), startedAt, completedAt); err != nil {
		return ports.NotificationAttemptRecord{}, err
	}
	if in.Outcome.Terminal() {
		state := records.NotificationDelivered
		if in.Outcome == records.NotificationRefusedOutcome {
			state = records.NotificationRefused
		}
		// The resolution is conditional on the pending state (E13-T1
		// review round 1, F006/F007): a late terminal attempt after the
		// notification already resolved — the at-least-once race of a
		// retried delivery — records as evidence but never rewrites the
		// first resolution.
		if _, err := tx.Exec(`UPDATE notification_events SET state = ?, resolved_at = ? WHERE notification_id = ? AND state = 'pending'`,
			string(state), completedAt, in.NotificationID); err != nil {
			return ports.NotificationAttemptRecord{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ports.NotificationAttemptRecord{}, err
	}
	return ports.NotificationAttemptRecord{
		AttemptID: attemptID, NotificationID: in.NotificationID, AttemptNumber: next, Outcome: in.Outcome,
		ErrorCode: in.ErrorCode, ResponseDigest: in.ResponseDigest, StartedAt: startedAt, CompletedAt: completedAt,
	}, nil
}

// CountNotificationsByState aggregates notification counts by state for
// the status surface (observability-and-operations §10).
func (s *Store) CountNotificationsByState(ctx context.Context) (map[string]int64, error) {
	rows, err := s.QueryContext(ctx, `SELECT state, COUNT(*) FROM notification_events GROUP BY state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var state string
		var n int64
		if err := rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		out[state] = n
	}
	return out, rows.Err()
}

// scanNotificationEvent reads one notification row through any scanner.
func scanNotificationEvent(rows *sql.Rows) (ports.NotificationEventRecord, error) {
	var rec ports.NotificationEventRecord
	var destinationID, resolvedAt sql.NullString
	if err := rows.Scan(&rec.NotificationID, &rec.RouteID, &rec.Event, &destinationID, &rec.Transition, &rec.SinkID, &rec.SinkType,
		&rec.PolicyRevision, &rec.PayloadJSON, &rec.State, &rec.IdempotencyKey, &rec.CreatedAt, &resolvedAt,
		&rec.AttemptCount, &rec.LastOutcome); err != nil {
		return ports.NotificationEventRecord{}, err
	}
	rec.DestinationID, rec.ResolvedAt = destinationID.String, resolvedAt.String
	return rec, nil
}

// StartDrainRun records the beginning of one bounded drain pass
// (ports.DrainRunInput is the shared contract; the row is written only
// by drain code paths and never read by delivery-adjacent
// transactions).
func (s *Store) StartDrainRun(ctx context.Context, in ports.DrainRunInput) error {
	startedAt := normalizeTimestamp(in.StartedAt)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO drain_runs (drain_id, route_id, trigger, mode, started_at)
		VALUES (?, ?, ?, ?, ?)`, in.DrainID, in.RouteID, in.Trigger, in.Mode, startedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// FinishDrainRun completes one drain pass's evidence row. An unknown
// drain identity fails loudly — evidence rows are never invented.
func (s *Store) FinishDrainRun(ctx context.Context, drainID string, counts ports.DrainRunCounts, completedAt string) error {
	completedAt = normalizeTimestamp(completedAt)
	budget := 0
	if counts.BudgetExpired {
		budget = 1
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE drain_runs SET completed_at = ?, claimed = ?, delivered = ?, refused = ?, retry_scheduled = ?, budget_expired = ?
		WHERE drain_id = ? AND completed_at IS NULL`, completedAt, counts.Claimed, counts.Delivered, counts.Refused, counts.RetryScheduled, budget, drainID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ports.ErrDrainRunNotFound, drainID)
	}
	return tx.Commit()
}

// notificationLeaseMargin is the fence's margin over the effective
// delivery deadline (v0.1.6 §4): the lease outlives the caller's
// delivery budget by thirty seconds, so a worker racing its own budget
// expiry keeps the fence until its outcome can land.
const notificationLeaseMargin = 30 * time.Second

// notificationDueSQL is the due-selection predicate every drain claim
// shares: pending work whose persisted due deadline has arrived and
// whose lease is free or expired (E16-T2, NTF-011/NTF-015).
const notificationDueSQL = `e.state = 'pending' AND e.due_at != '' AND e.due_at <= ? AND (e.lease_owner = '' OR e.lease_expires_at IS NULL OR e.lease_expires_at <= ?)`

// ClaimDueNotifications atomically leases the currently due, unclaimed
// pending work (E16-T2): one transaction selects the bounded candidate
// set and advances each row's fencing token under a conditional UPDATE
// keyed on the row's current lease-freedom, so two concurrent drainers
// always claim disjoint records — the UPDATE's row count, never the
// SELECT, decides the claim.
func (s *Store) ClaimDueNotifications(ctx context.Context, filter ports.NotificationClaimFilter) ([]ports.NotificationClaim, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// UTC at nanosecond precision: every lease predicate compares these
	// strings against the stored RFC 3339 UTC values, so a local-zone
	// rendering would silently widen the due and expiry windows.
	now := time.Now().UTC()
	leaseUntil := filter.LeaseUntil
	if leaseUntil == "" {
		leaseUntil = now.Add(notificationLeaseMargin).Format(time.RFC3339Nano)
	}
	leaseUntil = normalizeTimestamp(leaseUntil)
	// The stored due and lease-expiry values are second-precision
	// (normalizeTimestamp); the predicate's bound must be too, or a
	// notification created in the same second as the claim would compare
	// "12Z" > "12.123Z" and stay unclaimable for up to a second.
	nowSecond := now.Truncate(time.Second).Format(time.RFC3339)
	conds := notificationDueSQL
	args := []any{nowSecond, nowSecond}
	if filter.RouteID != "" {
		conds += " AND e.route_id = ?"
		args = append(args, filter.RouteID)
	}
	if filter.NotificationID != "" {
		conds += " AND e.notification_id = ?"
		args = append(args, filter.NotificationID)
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT e.notification_id FROM notification_events e WHERE `+conds+`
		ORDER BY e.created_at, e.notification_id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	claims := make([]ports.NotificationClaim, 0, len(ids))
	for _, id := range ids {
		// The conditional UPDATE is the atomic claim: it succeeds only
		// while the row's lease is still free or expired, and the
		// advancing token is the fence a recovering drainer checks.
		res, err := tx.Exec(`UPDATE notification_events
			SET lease_owner = ?, lease_token = lease_token + 1, lease_expires_at = ?
			WHERE notification_id = ? AND state = 'pending'
			  AND (lease_owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?)`,
			filter.Owner, leaseUntil, id, nowSecond)
		if err != nil {
			return nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n == 0 {
			continue // another drainer won this row inside our transaction
		}
		rec, err := scanNotificationEventByIDTx(tx, id)
		if err != nil {
			return nil, err
		}
		claims = append(claims, ports.NotificationClaim{NotificationEventRecord: rec, LeaseToken: rec.LeaseToken})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claims, nil
}

// scanNotificationEventByIDTx loads one notification row plus its
// current lease columns through an open transaction.
func scanNotificationEventByIDTx(tx *sql.Tx, id string) (ports.NotificationEventRecord, error) {
	var rec ports.NotificationEventRecord
	var destinationID, resolvedAt sql.NullString
	err := tx.QueryRow(`SELECT notification_id, route_id, event, destination_id, transition, sink_id, sink_type, policy_revision, payload_json, state, idempotency_key, created_at, resolved_at, due_at, lease_owner, lease_token, lease_expires_at
		FROM notification_events WHERE notification_id = ?`, id).Scan(
		&rec.NotificationID, &rec.RouteID, &rec.Event, &destinationID, &rec.Transition, &rec.SinkID, &rec.SinkType,
		&rec.PolicyRevision, &rec.PayloadJSON, &rec.State, &rec.IdempotencyKey, &rec.CreatedAt, &resolvedAt,
		&rec.DueAt, &rec.LeaseOwner, &rec.LeaseToken, &rec.LeaseExpiresAt)
	if err != nil {
		return ports.NotificationEventRecord{}, err
	}
	rec.DestinationID, rec.ResolvedAt = destinationID.String, resolvedAt.String
	return rec, nil
}

// notificationBackoffDelay computes the persisted retry delay for
// attempt n (NTF-014): min(Initial * Multiplier^(n-1), Max), then one
// symmetric ±JitterFraction jitter applied per retry — the jitter draw
// happens once here and the resulting deadline is stored, so every
// process observes the same due time.
func notificationBackoffDelay(backoff ports.NotificationBackoff, attempt int, draw float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := float64(backoff.Initial)
	for i := 1; i < attempt; i++ {
		delay *= backoff.Multiplier
		if backoff.Max > 0 && delay > float64(backoff.Max) {
			delay = float64(backoff.Max)
			break
		}
	}
	if backoff.Max > 0 && delay > float64(backoff.Max) {
		delay = float64(backoff.Max)
	}
	jitter := 1 + (draw*2-1)*backoff.JitterFraction
	if jitter < 0 {
		jitter = 0
	}
	return time.Duration(delay * jitter)
}

// notificationJitterDraw is the symmetric jitter source; a package
// variable so tests pin the persisted deadline deterministically.
var notificationJitterDraw = func() float64 { return rand.Float64() }

// RecordNotificationAttemptFenced records one delivery outcome under
// the claim's fence (E16-T2, NTF-012): the attempt is admitted only
// while the stored lease still carries the claiming token and owner, a
// terminal outcome resolves the notification, an ambiguous or
// retryable outcome persists the jittered backoff deadline, and the
// lease is released in the same transaction. A stale owner — one whose
// lease expired and was recovered by another drainer — fails with
// ports.ErrNotificationLeaseLost and records nothing.
func (s *Store) RecordNotificationAttemptFenced(ctx context.Context, in ports.NotificationAttemptInput, claim ports.NotificationClaim, backoff ports.NotificationBackoff) (ports.NotificationAttemptRecord, error) {
	if _, err := records.ParseNotificationAttemptOutcome(string(in.Outcome)); err != nil {
		return ports.NotificationAttemptRecord{}, err
	}
	startedAt := normalizeTimestamp(in.StartedAt)
	completedAt := normalizeTimestamp(in.CompletedAt)
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return ports.NotificationAttemptRecord{}, err
	}
	defer tx.Rollback()
	// The fence: the row must still carry this claim exactly. A
	// recovered lease (advanced token, new owner) refuses the stale
	// outcome before any evidence is written.
	var owner string
	var token int64
	if err := tx.QueryRow(`SELECT lease_owner, lease_token FROM notification_events WHERE notification_id = ?`, in.NotificationID).Scan(&owner, &token); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.NotificationAttemptRecord{}, fmt.Errorf("%w: %s", ports.ErrNotificationNotFound, in.NotificationID)
		}
		return ports.NotificationAttemptRecord{}, err
	}
	if owner == "" || token != claim.LeaseToken {
		return ports.NotificationAttemptRecord{}, fmt.Errorf("%w: %s (claim token %d, stored %d)", ports.ErrNotificationLeaseLost, in.NotificationID, claim.LeaseToken, token)
	}
	var next int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(attempt_number), 0) + 1 FROM notification_attempts WHERE notification_id = ?`, in.NotificationID).Scan(&next); err != nil {
		return ports.NotificationAttemptRecord{}, err
	}
	attemptID := records.NotificationAttemptID(in.NotificationID, next)
	if _, err := tx.Exec(`INSERT INTO notification_attempts (attempt_id, notification_id, attempt_number, outcome, error_code, response_digest, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		attemptID, in.NotificationID, next, string(in.Outcome), nullString(in.ErrorCode), nullString(in.ResponseDigest), startedAt, completedAt); err != nil {
		return ports.NotificationAttemptRecord{}, err
	}
	resolution := ""
	if in.Outcome.Terminal() {
		resolution = string(records.NotificationDelivered)
		if in.Outcome == records.NotificationRefusedOutcome {
			resolution = string(records.NotificationRefused)
		}
	} else {
		// The backoff deadline is persisted once, jittered once: every
		// later process reads the same due time (NTF-014).
		completed, err := time.Parse(time.RFC3339, completedAt)
		if err != nil {
			return ports.NotificationAttemptRecord{}, fmt.Errorf("fenced attempt completed_at: %w", err)
		}
		delay := notificationBackoffDelay(backoff, next, notificationJitterDraw())
		resolution = completed.Add(delay).UTC().Format(time.RFC3339Nano)
	}
	if resolution != "" && (resolution == string(records.NotificationDelivered) || resolution == string(records.NotificationRefused)) {
		if _, err := tx.Exec(`UPDATE notification_events SET state = ?, resolved_at = ?, lease_owner = '', lease_expires_at = NULL WHERE notification_id = ? AND state = 'pending'`,
			resolution, completedAt, in.NotificationID); err != nil {
			return ports.NotificationAttemptRecord{}, err
		}
	} else if resolution != "" {
		if _, err := tx.Exec(`UPDATE notification_events SET due_at = ?, lease_owner = '', lease_expires_at = NULL WHERE notification_id = ? AND state = 'pending'`,
			normalizeTimestamp(resolution), in.NotificationID); err != nil {
			return ports.NotificationAttemptRecord{}, err
		}
	} else {
		if _, err := tx.Exec(`UPDATE notification_events SET lease_owner = '', lease_expires_at = NULL WHERE notification_id = ?`, in.NotificationID); err != nil {
			return ports.NotificationAttemptRecord{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ports.NotificationAttemptRecord{}, err
	}
	return ports.NotificationAttemptRecord{
		AttemptID: attemptID, NotificationID: in.NotificationID, AttemptNumber: next, Outcome: in.Outcome,
		ErrorCode: in.ErrorCode, ResponseDigest: in.ResponseDigest, StartedAt: startedAt, CompletedAt: completedAt,
	}, nil
}

// ReleaseNotificationClaims returns unstarted claims to the due pool at
// budget expiry: the release is fenced on the claim's token, so a claim
// already recovered by another drainer is left untouched.
func (s *Store) ReleaseNotificationClaims(ctx context.Context, owner string, claims []ports.NotificationClaim) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, claim := range claims {
		if _, err := tx.Exec(`UPDATE notification_events SET lease_owner = '', lease_expires_at = NULL
			WHERE notification_id = ? AND lease_owner = ? AND lease_token = ?`, claim.NotificationID, owner, claim.LeaseToken); err != nil {
			return err
		}
	}
	return tx.Commit()
}

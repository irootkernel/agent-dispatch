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

// notificationSourceValueBound caps one source field's value and rejects
// control characters (E13-T1 review round 1, F003): the payload's safety
// is enforced at this boundary, not left to call-site discipline — an
// emission site that passes an over-long value (a document body, a
// resolved secret, anything unbounded) fails its transaction loudly
// instead of leaking into a notification.
const notificationSourceValueBound = 256

// validateNotificationSource enforces the bounded, safe payload
// projection (SEC-011): at most notificationSourceBound fields, each
// non-empty, at most notificationSourceValueBound bytes of valid UTF-8
// without control characters.
func validateNotificationSource(source map[string]string) error {
	if len(source) > notificationSourceBound {
		return fmt.Errorf("notification source projection exceeds %d fields", notificationSourceBound)
	}
	for k, v := range source {
		if k == "" || v == "" {
			return fmt.Errorf("notification source projection carries an empty field")
		}
		if len(v) > notificationSourceValueBound {
			return fmt.Errorf("notification source field %q exceeds %d bytes", k, notificationSourceValueBound)
		}
		for _, r := range v {
			if r < 0x20 || r == 0x7f {
				return fmt.Errorf("notification source field %q carries a control character", k)
			}
		}
	}
	return nil
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
func (s *Store) enqueueNotificationTx(tx *sql.Tx, routeID string, event records.NotificationEventKind, transition, destinationID string, source map[string]string, now string) error {
	if s.notificationPolicy == nil {
		return nil
	}
	policy := s.notificationPolicy(routeID)
	if policy == nil || len(policy.Sinks) == 0 || policy.Revision == "" {
		return nil
	}
	if !records.NotificationEventKinds(policy.Events).Contains(event) {
		return nil
	}
	// The payload projection is enforced before any sink write: an
	// unbounded or unsafe source value fails the owning transaction
	// instead of leaking into a notification (SEC-011, review round 1).
	if err := validateNotificationSource(source); err != nil {
		return fmt.Errorf("route %s event %s: %w", routeID, event, err)
	}
	sinks := make([]ports.NotificationSinkRef, len(policy.Sinks))
	copy(sinks, policy.Sinks)
	sort.Slice(sinks, func(i, j int) bool { return sinks[i].ID < sinks[j].ID })
	now = normalizeTimestamp(now)
	for _, sink := range sinks {
		notificationID := records.NotificationID(event, destinationID, transition, sink.ID, policy.Revision)
		payload, err := notificationPayloadJSON(notificationID, routeID, event, destinationID, transition, sink.ID, policy.Revision, source, now)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO notification_events
			(notification_id, route_id, event, destination_id, transition, sink_id, sink_type, policy_revision, idempotency_key, payload_json, state, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)`,
			notificationID, routeID, string(event), nullString(destinationID), transition, sink.ID, sink.Type, policy.Revision,
			records.NotificationIdempotencyKey(notificationID), payload, now); err != nil {
			return err
		}
	}
	return nil
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
	return s.enqueueNotificationTx(tx, routeID, records.EventReconciliationRequired, "route:"+routeID+":pending_reconcile:"+occurrence, "", source, now)
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

// ListNotifications returns notifications matching the filter, newest
// first (NTF-004: every intent and outcome stays inspectable).
func (s *Store) ListNotifications(ctx context.Context, filter ports.NotificationFilter) ([]ports.NotificationEventRecord, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT notification_id, route_id, event, destination_id, transition, sink_id, sink_type, policy_revision, payload_json, state, idempotency_key, created_at, resolved_at
		FROM notification_events`
	conds := []string{}
	args := []any{}
	if filter.RouteID != "" {
		conds = append(conds, "route_id = ?")
		args = append(args, filter.RouteID)
	}
	if filter.State != "" {
		conds = append(conds, "state = ?")
		args = append(args, filter.State)
	}
	if filter.SinkID != "" {
		conds = append(conds, "sink_id = ?")
		args = append(args, filter.SinkID)
	}
	if filter.Event != "" {
		conds = append(conds, "event = ?")
		args = append(args, string(filter.Event))
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY created_at DESC, notification_id LIMIT ?"
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
		&rec.PolicyRevision, &rec.PayloadJSON, &rec.State, &rec.IdempotencyKey, &rec.CreatedAt, &resolvedAt); err != nil {
		return ports.NotificationEventRecord{}, err
	}
	rec.DestinationID, rec.ResolvedAt = destinationID.String, resolvedAt.String
	return rec, nil
}

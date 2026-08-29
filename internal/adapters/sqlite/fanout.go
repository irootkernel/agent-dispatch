package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Aggregate fan-out persistence (E12-T1, ADR-0016): the aggregate event,
// destination revision, and child dispatch record families introduced by
// migration v12. Every write joins the intent's own transaction, so the
// aggregate-to-child creation is atomic with the dispatch intent
// (DAT-010); historic pre-cutover intents simply carry no child row and
// stay queryable unchanged (DAT-012).

// AggregateEventSchemaVersion is the stored contract of aggregate-event
// rows (canonical-record-contracts §10).
const AggregateEventSchemaVersion = "agent-dispatch.aggregate-event/v1"

// DestinationRevisionSchemaVersion is the stored contract of
// destination-revision rows.
const DestinationRevisionSchemaVersion = "agent-dispatch.destination-revision/v1"

// ChildDispatchSchemaVersion is the stored contract of child-dispatch
// rows.
const ChildDispatchSchemaVersion = "agent-dispatch.child-dispatch/v1"

// saveFanoutTx persists the aggregate event, the destination-revision
// records, and the child-dispatch row of one new-contract intent inside
// the caller's transaction (E12-T1, DAT-010/FAN-003). The destination
// revisions are insert-if-absent: the projection is content-addressed by
// its revision, so re-referencing an already-persisted revision is the
// identity operation. The selection summary is stored in canonical
// destination-ID order regardless of configuration order (FAN-012).
func (s *Store) saveFanoutTx(tx *sql.Tx, i IntentRecord) error {
	f := i.Fanout
	if _, err := records.ParseAggregateOrigin(f.Origin); err != nil {
		return fmt.Errorf("fanout origin: %w", err)
	}
	if len(f.Selections) == 0 {
		return fmt.Errorf("fanout needs at least one destination selection")
	}
	selections := make([]records.DestinationSelection, len(f.Selections))
	copy(selections, f.Selections)
	records.SortSelections(selections)
	for _, sel := range selections {
		if err := sel.Validate(); err != nil {
			return err
		}
	}
	selectionJSON, err := json.Marshal(selections)
	if err != nil {
		return err
	}
	// The aggregate event is one per occurrence (FAN-002): siblings of a
	// fan-out reference the same aggregate a first child already created,
	// so the insert is idempotent and the creation audit lands once.
	res, err := execOn(tx, s.DB, `INSERT INTO aggregate_events
		(aggregate_id, decision_id, route_id, route_revision, resource_id, origin, generation, content_fingerprint, schema_version, selection_json, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT (aggregate_id) DO NOTHING`,
		f.AggregateID, i.DecisionID, i.RouteID, i.RouteRevision, i.ResourceID, f.Origin, i.Generation,
		i.ContentFingerprint, AggregateEventSchemaVersion, string(selectionJSON), i.CreatedAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		// The audit context is built through the JSON encoder, never Go %q
		// quoting: %q renders non-printable code points as escapes JSON
		// rejects, and this row is append-only evidence (review round 1).
		auditContext, err := json.Marshal(map[string]any{
			"reason": "fanout", "origin": f.Origin, "dispatch_id": i.DispatchID,
			"destination_id": f.DestinationID, "selections": len(selections),
		})
		if err != nil {
			return err
		}
		if err := s.AppendTransition(tx, f.AggregateID+":created", "aggregate_event", f.AggregateID, "", f.Origin, i.CreatedAt, string(auditContext)); err != nil {
			return err
		}
	}
	for _, rev := range f.Revisions {
		// Store-boundary verification (E12 epic validation, DAT-010): a
		// destination-revision record is content-addressed — the revision
		// must equal the canonical derivation "dst-"+hex(sha256(projection
		// bytes)) in records.RevisionOfProjection, the SAME function the
		// config-level computation uses (E12 epic whole-review round 1). A
		// mismatch is a producer defect, and persisting it would plant a row
		// whose revision can never be re-derived from its bytes: reject as a
		// NON-RETRYABLE validation failure (E12 epic whole-review round 2) —
		// the data is wrong, not racy, so the operator fixes the input
		// instead of retrying.
		if want := records.RevisionOfProjection(rev.ProjectionJSON); rev.Revision != want {
			return fmt.Errorf("destination %q revision %q is not the content address of its projection bytes (validation failure, want %s): %w",
				rev.DestinationID, rev.Revision, want, ports.ErrInvalidFanoutRecord)
		}
		if _, err := execOn(tx, s.DB, `INSERT INTO destination_revisions (route_id, destination_id, revision, projection_json, created_at)
			VALUES (?,?,?,?,?) ON CONFLICT (route_id, destination_id, revision) DO NOTHING`,
			i.RouteID, rev.DestinationID, rev.Revision, rev.ProjectionJSON, i.CreatedAt); err != nil {
			return err
		}
	}
	// The child's destination revision must be a row that exists (E12 epic
	// validation): inserted in THIS transaction's revisions above or
	// persisted previously — a dangling reference would make the stored
	// child unverifiable forever after. Derived work (follow-ups, reruns,
	// rebuilds) references revisions earlier arrivals persisted and omits
	// them from Revisions, so the row must already exist for those. Like
	// the content-address check this is a validation failure, never a
	// retryable conflict (E12 epic whole-review round 2).
	var revisionRow int
	if err := txOrDB(tx, s.DB).QueryRow(`SELECT 1 FROM destination_revisions WHERE route_id = ? AND destination_id = ? AND revision = ?`,
		i.RouteID, f.DestinationID, f.DestinationRevision).Scan(&revisionRow); err != nil {
		// Only the absent row is the validation failure: a transient store
		// fault must keep its storage class (the epic's own sibling-commit
		// invariant — a storage fault is storage, never a replayed
		// configuration conflict).
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return fmt.Errorf("child %s references destination %q revision %q with no durable record (persist the referenced revision beside the fanout; validation failure): %w",
			i.DispatchID, f.DestinationID, f.DestinationRevision, ports.ErrInvalidFanoutRecord)
	}
	if _, err := execOn(tx, s.DB, `INSERT INTO child_dispatches
		(child_id, aggregate_id, dispatch_id, route_id, destination_id, destination_revision, workstream, idempotency_key, schema_version, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		"child-"+i.DispatchID, f.AggregateID, i.DispatchID, i.RouteID, f.DestinationID, f.DestinationRevision, f.Workstream,
		i.IdempotencyKey, ChildDispatchSchemaVersion, i.CreatedAt); err != nil {
		return err
	}
	return nil
}

// childJoinColumns selects the child linkage of one intent: empty
// destination identity marks a pre-cutover legacy intent with no child
// row (DAT-012's historical shape). The intent-side columns must be
// table-qualified by callers because the join brings a second
// dispatch_id column.
const childJoinColumns = `COALESCE(c.aggregate_id, ''), COALESCE(c.destination_id, ''), COALESCE(c.destination_revision, ''), COALESCE(c.workstream, '')`

// childJoin is the LEFT JOIN every intent read uses to surface the
// destination lineage.
const childJoin = `LEFT JOIN child_dispatches c ON c.dispatch_id = dispatch_intents.dispatch_id`

// AggregateEventRecord is the durable aggregate-event row.
type AggregateEventRecord struct {
	AggregateID        string
	DecisionID         string
	RouteID            string
	RouteRevision      string
	ResourceID         string
	Origin             string
	Generation         int
	ContentFingerprint string
	SchemaVersion      string
	SelectionJSON      string
	CreatedAt          string
}

// ChildDispatchRecord is the durable child-dispatch row.
type ChildDispatchRecord struct {
	ChildID             string
	AggregateID         string
	DispatchID          string
	RouteID             string
	DestinationID       string
	DestinationRevision string
	Workstream          string
	IdempotencyKey      string
	SchemaVersion       string
	CreatedAt           string
}

// LoadAggregateEvent returns one aggregate event by ID.
func (s *Store) LoadAggregateEvent(ctx context.Context, aggregateID string) (AggregateEventRecord, error) {
	var rec AggregateEventRecord
	err := s.QueryRowContext(ctx, `SELECT aggregate_id, decision_id, route_id, route_revision, resource_id, origin, generation, content_fingerprint, schema_version, selection_json, created_at
		FROM aggregate_events WHERE aggregate_id = ?`, aggregateID).Scan(
		&rec.AggregateID, &rec.DecisionID, &rec.RouteID, &rec.RouteRevision, &rec.ResourceID, &rec.Origin, &rec.Generation,
		&rec.ContentFingerprint, &rec.SchemaVersion, &rec.SelectionJSON, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rec, fmt.Errorf("%w: aggregate %s", ports.ErrIntentNotFound, aggregateID)
	}
	return rec, err
}

// LoadChildDispatch returns the child record of one dispatch intent; a
// legacy pre-cutover intent has no child row and fails with
// ErrIntentNotFound's family (the empty destination identity on the
// intent snapshot is the legacy marker).
func (s *Store) LoadChildDispatch(ctx context.Context, dispatchID string) (ChildDispatchRecord, error) {
	var rec ChildDispatchRecord
	err := s.QueryRowContext(ctx, `SELECT child_id, aggregate_id, dispatch_id, route_id, destination_id, destination_revision, workstream, idempotency_key, schema_version, created_at
		FROM child_dispatches WHERE dispatch_id = ?`, dispatchID).Scan(
		&rec.ChildID, &rec.AggregateID, &rec.DispatchID, &rec.RouteID, &rec.DestinationID, &rec.DestinationRevision,
		&rec.Workstream, &rec.IdempotencyKey, &rec.SchemaVersion, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rec, fmt.Errorf("%w: dispatch %s has no child record", ports.ErrIntentNotFound, dispatchID)
	}
	return rec, err
}

// AggregateChildRow is one child's inspection projection beneath an
// aggregate event (E12-T3, CLI-013/DAT-011): the destination lane, the
// intent's submit state, the latest acceptance and execution-projection
// receipts, and the latest work receipt with its validity.
//
// Read-model convention (E12 epic validation, recorded as settled): this
// struct is a PLAIN ROW PROJECTION — every member is a durable column
// value (WorkReceiptValid is the row's validation_state, a stored fact,
// never a presentation decision), and the CLI's events.go owns all
// projection semantics (completion_evidence, aggregate_status, JSON
// shapes). No presentation JSON is serialized in the store read models.
type AggregateChildRow struct {
	DispatchID          string
	DestinationID       string
	DestinationRevision string
	Workstream          string
	IntentState         string
	AttemptCount        int
	NextAttemptAt       string
	ExternalRef         string
	// Acceptance is the latest acceptance receipt's state ('' when none).
	Acceptance string
	// Execution is the latest execution-projection receipt's state (''
	// when none).
	Execution string
	// WorkReceipt is the latest work receipt's status ('' when none).
	WorkReceipt string
	// WorkReceiptValid reports the latest work receipt's validation state.
	WorkReceiptValid bool
}

// LoadAggregateChildren returns every child beneath one aggregate event
// with its per-child evidence projections (E12-T3, CLI-013): the joins
// are table-qualified and the latest-receipt columns come from
// deterministic correlated subqueries.
func (s *Store) LoadAggregateChildren(ctx context.Context, aggregateID string) ([]AggregateChildRow, error) {
	rows, err := s.QueryContext(ctx, `SELECT c.dispatch_id, c.destination_id, c.destination_revision, c.workstream,
			i.state, i.attempt_count, COALESCE(i.next_attempt_at, ''), COALESCE(i.external_ref, ''),
			COALESCE((SELECT r.acceptance_state FROM dispatch_receipts r
				WHERE r.dispatch_id = c.dispatch_id AND r.receipt_kind = 'acceptance'
				ORDER BY r.received_at DESC, r.receipt_id DESC LIMIT 1), ''),
			COALESCE((SELECT r2.execution_state FROM dispatch_receipts r2
				WHERE r2.dispatch_id = c.dispatch_id AND r2.receipt_kind = 'execution_projection'
				ORDER BY r2.received_at DESC, r2.receipt_id DESC LIMIT 1), ''),
			COALESCE((SELECT w.status FROM work_receipts w
				WHERE w.dispatch_id = c.dispatch_id
				ORDER BY w.submitted_at DESC, w.receipt_id DESC LIMIT 1), ''),
			COALESCE((SELECT w2.validation_state FROM work_receipts w2
				WHERE w2.dispatch_id = c.dispatch_id
				ORDER BY w2.submitted_at DESC, w2.receipt_id DESC LIMIT 1), '')
		FROM child_dispatches c
		JOIN dispatch_intents i ON i.dispatch_id = c.dispatch_id
		WHERE c.aggregate_id = ?
		ORDER BY c.destination_id`, aggregateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AggregateChildRow
	for rows.Next() {
		var row AggregateChildRow
		var validity string
		if err := rows.Scan(&row.DispatchID, &row.DestinationID, &row.DestinationRevision, &row.Workstream,
			&row.IntentState, &row.AttemptCount, &row.NextAttemptAt, &row.ExternalRef,
			&row.Acceptance, &row.Execution, &row.WorkReceipt, &validity); err != nil {
			return nil, err
		}
		row.WorkReceiptValid = validity == "valid"
		out = append(out, row)
	}
	return out, rows.Err()
}

// LaneStateRow is one destination lane's coordination summary (E12-T3,
// OPS-011): the status surface's bounded per-lane projection.
type LaneStateRow struct {
	DestinationID    string `json:"destination_id"`
	LaneState        string `json:"lane_state"`
	ActiveDispatchID string `json:"active_dispatch_id,omitempty"`
	DirtyGeneration  int    `json:"dirty_generation"`
}

// ListRouteLanes returns one row per destination lane of a route in
// destination order (E12-T3, OPS-011); a route with no materialized lane
// returns an empty slice.
func (s *Store) ListRouteLanes(ctx context.Context, routeID string) ([]LaneStateRow, error) {
	rows, err := s.QueryContext(ctx, `SELECT destination_id, lane_state, COALESCE(active_dispatch_id, ''), dirty_generation
		FROM destination_lane_state WHERE route_id = ? ORDER BY destination_id`, routeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LaneStateRow{}
	for rows.Next() {
		var row LaneStateRow
		if err := rows.Scan(&row.DestinationID, &row.LaneState, &row.ActiveDispatchID, &row.DirtyGeneration); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

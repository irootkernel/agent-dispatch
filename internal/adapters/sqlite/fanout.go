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
		if _, err := execOn(tx, s.DB, `INSERT INTO destination_revisions (route_id, destination_id, revision, projection_json, created_at)
			VALUES (?,?,?,?,?) ON CONFLICT (route_id, destination_id, revision) DO NOTHING`,
			i.RouteID, rev.DestinationID, rev.Revision, rev.ProjectionJSON, i.CreatedAt); err != nil {
			return err
		}
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

package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// ErrOptimisticConcurrency is returned when a conditional update matched
// no row (stale version or expired conditions).
// ErrOptimisticConcurrency is the adapter's alias of the shared
// ports-level generation-conflict sentinel.
var ErrOptimisticConcurrency = ports.ErrGenerationConflict

// ErrConflict is returned when a unique constraint rejects a duplicate.
var ErrConflict = errors.New("uniqueness conflict")

// RegisterResource materializes a trusted resource revision.
func (s *Store) RegisterResource(tx *sql.Tx, resourceID, revision, root, canonicalRoot, fileScope, gitMode string) error {
	_, err := execOn(tx, s.DB, `INSERT INTO resources (resource_id, revision, root, canonical_root, file_scope, git_mode) VALUES (?,?,?,?,?,?)
		ON CONFLICT(resource_id) DO UPDATE SET revision=excluded.revision, root=excluded.root, canonical_root=excluded.canonical_root, file_scope=excluded.file_scope, git_mode=excluded.git_mode`,
		resourceID, revision, root, canonicalRoot, fileScope, gitMode)
	return err
}

// RegisterRoute materializes a route revision.
func (s *Store) RegisterRoute(tx *sql.Tx, routeID, revision, policyRevision, resourceID, targetID, definition, updatedAt string) error {
	_, err := execOn(tx, s.DB, `INSERT INTO routes (route_id, revision, policy_revision, resource_id, target_id, definition, updated_at) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(route_id) DO UPDATE SET revision=excluded.revision, policy_revision=excluded.policy_revision, resource_id=excluded.resource_id, target_id=excluded.target_id, definition=excluded.definition, updated_at=excluded.updated_at`,
		routeID, revision, policyRevision, resourceID, targetID, definition, updatedAt)
	return err
}

// SaveObservation stores the immutable observation and its normalized
// changes (DAT-007: causal and attribution fields are real columns).
func (s *Store) SaveObservation(tx *sql.Tx, o ObservationRecord) error {
	if _, err := execOn(tx, s.DB, `INSERT INTO source_observations
		(observation_id, schema_version, source_type, source_id, source_event_key, trigger_name, resource_id, observed_at, received_at, raw_payload_digest, ingest_status, flags_json, position_json)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.ObservationID, o.SchemaVersion, o.SourceType, o.SourceID, nullString(o.SourceEventKey), o.TriggerName, o.ResourceID,
		o.ObservedAt, o.ReceivedAt, o.RawPayloadDigest, o.IngestStatus, o.FlagsJSON, nullString(o.PositionJSON)); err != nil {
		return err
	}
	for _, c := range o.Changes {
		if _, err := execOn(tx, s.DB, `INSERT INTO observation_changes
			(observation_id, ordinal, path, operation, exists_after, file_type, before_digest, after_digest, digest_status)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			o.ObservationID, c.Ordinal, c.Path, c.Operation, boolInt(c.ExistsAfter), c.FileType, nullString(c.BeforeDigest), nullString(c.AfterDigest), c.DigestStatus); err != nil {
			return err
		}
	}
	return nil
}

// ObservationRecord is the persistence shape of a source observation.
type ObservationRecord struct {
	ObservationID    string
	SchemaVersion    string
	SourceType       string
	SourceID         string
	SourceEventKey   string
	TriggerName      string
	ResourceID       string
	ObservedAt       string
	ReceivedAt       string
	RawPayloadDigest string
	IngestStatus     string
	FlagsJSON        string
	// PositionJSON is the verbatim source position object (E7-T8/M-11).
	PositionJSON string
	Changes      []ChangeRecord
}

// ChangeRecord is the persistence shape of one normalized change.
type ChangeRecord struct {
	Ordinal      int
	Path         string
	Operation    string
	ExistsAfter  bool
	FileType     string
	BeforeDigest string
	AfterDigest  string
	DigestStatus string
}

// SaveBatch stores the canonical batch and its observation lineage.
func (s *Store) SaveBatch(tx *sql.Tx, batchID, routeID, routeRevision, resourceID, createdAt, contentFingerprint string, observationIDs []string) error {
	// batch_seq is the monotonic watermark assigned at insert (MAX+1)
	// inside the insert transaction; the unique index makes a lost
	// assignment a hard failure instead of a silent tie (E8-T1, H-1.3).
	if _, err := execOn(tx, s.DB, `INSERT INTO change_batches (batch_id, route_id, route_revision, resource_id, created_at, content_fingerprint, batch_seq)
		VALUES (?,?,?,?,?,?, (SELECT COALESCE(MAX(batch_seq), 0) + 1 FROM change_batches))`,
		batchID, routeID, routeRevision, resourceID, createdAt, contentFingerprint); err != nil {
		return err
	}
	for _, obsID := range observationIDs {
		if _, err := execOn(tx, s.DB, `INSERT INTO batch_observations (batch_id, observation_id) VALUES (?,?)`, batchID, obsID); err != nil {
			return err
		}
	}
	return nil
}

// SaveDecision stores an immutable policy decision referencing exactly one
// batch or generation lineage.
func (s *Store) SaveDecision(tx *sql.Tx, d DecisionRecord) error {
	_, err := execOn(tx, s.DB, `INSERT INTO policy_decisions
		(decision_id, batch_id, route_id, route_revision, policy_revision, generation_lineage_json, disposition, classification, reason_codes_json, created_at, actor, supersedes_decision_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.DecisionID, nullString(d.BatchID), d.RouteID, d.RouteRevision, d.PolicyRevision, nullString(d.GenerationLineageJSON),
		d.Disposition, d.Classification, d.ReasonCodesJSON, d.CreatedAt, d.Actor, nullString(d.SupersedesDecisionID))
	return err
}

// DecisionRecord is the persistence shape of a policy decision.
type DecisionRecord struct {
	DecisionID            string
	BatchID               string
	RouteID               string
	RouteRevision         string
	PolicyRevision        string
	GenerationLineageJSON string
	Disposition           string
	Classification        string
	ReasonCodesJSON       string
	CreatedAt             string
	Actor                 string
	SupersedesDecisionID  string
}

// SaveIntent stores the immutable dispatch request and reserves the route
// slot in the same transaction (intent transaction, ADR-0005).
func (s *Store) SaveIntent(tx *sql.Tx, i IntentRecord) error {
	// base_batch_seq is the generation-window watermark: the intent's own
	// arrival batch when its decision carries one, otherwise the current
	// maximum (the follow-up shape — its window opens at creation, E8-T1).
	if _, err := execOn(tx, s.DB, `INSERT INTO dispatch_intents
		(dispatch_id, decision_id, route_id, route_revision, target_id, target_type, target_scope, resource_id, generation, idempotency_key, content_fingerprint, manifest_digest, request_version, request_json, state, created_at, updated_at, base_batch_seq)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,'ready',?,?,
			COALESCE((SELECT b.batch_seq FROM policy_decisions p
				JOIN change_batches b ON b.batch_id = p.batch_id
				WHERE p.decision_id = ?), (SELECT COALESCE(MAX(batch_seq), 0) FROM change_batches)))`,
		i.DispatchID, i.DecisionID, i.RouteID, i.RouteRevision, i.TargetID, i.TargetType, i.TargetScope, i.ResourceID, i.Generation,
		i.IdempotencyKey, i.ContentFingerprint, i.ManifestDigest, i.RequestVersion, i.RequestJSON, i.CreatedAt, i.CreatedAt,
		nullString(i.DecisionID)); err != nil {
		return err
	}
	// Reserve the route's active slot in the same transaction.
	res, err := execOn(tx, s.DB, `UPDATE route_runtime_state
		SET active_dispatch_id = ?, active_generation = ?, version = version + 1
		WHERE route_id = ? AND active_dispatch_id IS NULL`,
		i.DispatchID, i.Generation, i.RouteID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		if err := txOrDB(tx, s.DB).QueryRow(`SELECT 1 FROM route_runtime_state WHERE route_id = ?`, i.RouteID).Scan(&exists); err != nil {
			return fmt.Errorf("route %s has no runtime state row: %w", i.RouteID, ErrOptimisticConcurrency)
		}
		return fmt.Errorf("route %s: %w", i.RouteID, ports.ErrRouteSlotHeld)
	}
	return nil
}

// IntentRecord is the persistence shape of a dispatch intent.
type IntentRecord struct {
	TargetScope        string
	DispatchID         string
	DecisionID         string
	RouteID            string
	RouteRevision      string
	TargetID           string
	TargetType         string
	ResourceID         string
	Generation         int
	IdempotencyKey     string
	ContentFingerprint string
	ManifestDigest     string
	RequestVersion     string
	RequestJSON        string
	CreatedAt          string
}

// AppendTransition appends one audit record; the table triggers make any
// later mutation fail closed.
func (s *Store) AppendTransition(tx *sql.Tx, transitionID, entityType, entityID, fromState, toState, recordedAt, contextJSON string) error {
	_, err := execOn(tx, s.DB, `INSERT INTO state_transitions (transition_id, entity_type, entity_id, from_state, to_state, recorded_at, context_json) VALUES (?,?,?,?,?,?,?)`,
		transitionID, entityType, entityID, nullString(fromState), toState, recordedAt, contextJSON)
	return err
}

// UpdateRouteRuntimeState applies optimistic-concurrency-checked updates.
func (s *Store) UpdateRouteRuntimeState(tx *sql.Tx, routeID string, version int, mutate func(*RouteRuntimeStateRecord)) error {
	db := txOrDB(tx, s.DB)
	rec, err := s.loadRouteRuntimeState(db, routeID)
	if err != nil {
		return err
	}
	if rec.Version != version {
		return fmt.Errorf("route %s version %d is stale (current %d): %w", routeID, version, rec.Version, ErrOptimisticConcurrency)
	}
	mutate(rec)
	res, err := execOn(tx, s.DB, `UPDATE route_runtime_state SET
		activation_state=?, acknowledged_revision=?, route_state=?, active_dispatch_id=?, active_generation=?, dirty_generation=?, dirty_since=?, pending_reconcile=?, last_source_position=?, last_reconciled_at=?, version=version+1
		WHERE route_id=? AND version=?`,
		rec.ActivationState, nullString(rec.AcknowledgedRevision), rec.RouteState, nullString(rec.ActiveDispatchID),
		rec.ActiveGeneration, rec.DirtyGeneration, nullString(rec.DirtySince), boolInt(rec.PendingReconcile),
		nullString(rec.LastSourcePosition), nullString(rec.LastReconciledAt), routeID, version)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("route %s concurrent modification: %w", routeID, ErrOptimisticConcurrency)
	}
	return nil
}

// RouteRuntimeStateRecord is the persistence shape of route runtime state.
type RouteRuntimeStateRecord struct {
	RouteID              string
	ActivationState      string
	AcknowledgedRevision string
	RouteState           string
	ActiveDispatchID     string
	ActiveGeneration     int
	DirtyGeneration      int
	DirtySince           string
	PendingReconcile     bool
	LastSourcePosition   string
	LastReconciledAt     string
	Version              int
}

// LoadRouteRuntimeState reads the current route runtime state.
func (s *Store) LoadRouteRuntimeState(routeID string) (*RouteRuntimeStateRecord, error) {
	return s.loadRouteRuntimeState(s.DB, routeID)
}

func (s *Store) loadRouteRuntimeState(q queryer, routeID string) (*RouteRuntimeStateRecord, error) {
	var rec RouteRuntimeStateRecord
	var ack, active, dirtySince, pos, reconciled sql.NullString
	err := q.QueryRow(`SELECT route_id, activation_state, acknowledged_revision, route_state, active_dispatch_id, active_generation, dirty_generation, dirty_since, pending_reconcile, last_source_position, last_reconciled_at, version
		FROM route_runtime_state WHERE route_id = ?`, routeID).Scan(
		&rec.RouteID, &rec.ActivationState, &ack, &rec.RouteState, &active, &rec.ActiveGeneration, &rec.DirtyGeneration,
		&dirtySince, &rec.PendingReconcile, &pos, &reconciled, &rec.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("route %s has no runtime state: %w", routeID, ErrOptimisticConcurrency)
	}
	if err != nil {
		return nil, err
	}
	rec.AcknowledgedRevision, rec.ActiveDispatchID, rec.DirtySince, rec.LastSourcePosition, rec.LastReconciledAt =
		nullText(ack), nullText(active), nullText(dirtySince), nullText(pos), nullText(reconciled)
	return &rec, nil
}

// InitializeRouteState creates the runtime state row for a route (IDLE,
// disabled, version 0) when it is first registered.
func (s *Store) InitializeRouteState(tx *sql.Tx, routeID string) error {
	_, err := execOn(tx, s.DB, `INSERT INTO route_runtime_state (route_id, activation_state, route_state) VALUES (?, 'disabled', 'IDLE')`, routeID)
	return err
}

// SaveWorkReceipt stores the verified work receipt (bounded: manifest
// paths and digests only, DAT-008).
func (s *Store) SaveWorkReceipt(tx *sql.Tx, w WorkReceiptRecord) error {
	_, err := execOn(tx, s.DB, `INSERT INTO work_receipts
		(receipt_id, dispatch_id, run_id, resource_id, status, failure_code, external_task_id, base_revision, result_revision, changes_json, submitted_at, validation_state, validation_reasons_json, begun_at, route_revision)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, route_revision FROM dispatch_intents WHERE dispatch_id = ?`,
		w.ReceiptID, w.DispatchID, w.RunID, w.ResourceID, w.Status, nullString(w.FailureCode), nullString(w.ExternalTaskID),
		nullString(w.BaseRevision), nullString(w.ResultRevision), w.ChangesJSON, w.SubmittedAt, w.ValidationState, w.ValidationReasonsJSON,
		nullString(w.BegunAt), w.DispatchID)
	return err
}

// WorkReceiptRecord is the persistence shape of a work receipt.
type WorkReceiptRecord struct {
	ReceiptID             string
	DispatchID            string
	RunID                 string
	ResourceID            string
	Status                string
	FailureCode           string
	ExternalTaskID        string
	BaseRevision          string
	ResultRevision        string
	ChangesJSON           string
	SubmittedAt           string
	ValidationState       string
	ValidationReasonsJSON string
	// BegunAt preserves the run's begin timestamp across terminal
	// updates (migration v4); it equals SubmittedAt on a begin insert.
	BegunAt string
}

// validIntentTransition enforces the dispatch state machine (§3) by
// delegating to the authoritative domain table (E3-T1); unknown strings
// fail closed through the records parser.
func validIntentTransition(from, to string) bool {
	fromState, err := records.ParseIntentState(from)
	if err != nil {
		return false
	}
	toState, err := records.ParseIntentState(to)
	if err != nil {
		return false
	}
	return state.CanTransitionIntent(fromState, toState)
}

type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

func txOrDB(tx *sql.Tx, db *sql.DB) queryer {
	if tx != nil {
		return tx
	}
	return db
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func execOn(tx *sql.Tx, db *sql.DB, query string, args ...any) (sql.Result, error) {
	var e execer = db
	if tx != nil {
		e = tx
	}
	res, err := e.Exec(query, args...)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullText(n sql.NullString) string {
	if n.Valid {
		return n.String
	}
	return ""
}

// nullPtr maps a nullable column onto the JSON null shape: absent stays
// nil (marshals null), present becomes a string pointer (E9-T1 audit
// F007/F008, reconciled by the E9 validation).
func nullPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	s := n.String
	return &s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// normalizeTimestamp parses an RFC 3339 timestamp and renders it in UTC
// at second precision, the canonical TEXT-comparable form used by lease
// predicates.
func normalizeTimestamp(ts string) string {
	if ts == "" {
		return ts
	}
	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		// Non-RFC-3339 values are rejected elsewhere; pass through so the
		// failure surfaces with the original text.
		return ts
	}
	return parsed.UTC().Truncate(time.Second).Format(time.RFC3339)
}

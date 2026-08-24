package sqlite

// schemaV1 is the initial durable schema (persistence-and-state-machines
// §2): one table per canonical record (DAT-001), real columns for the
// query-relevant and causal fields the primary lineages carry (DAT-007),
// no note bodies (DAT-008), foreign keys and unique constraints enforcing
// the invariants, and the append-only audit history (DUR-011). Bounded
// JSON payloads carry their schema version. Revision columns live on the
// lineage heads (change_batches, policy_decisions, dispatch_intents);
// the attempt, receipt, work-receipt, and quarantine tables gained
// their own route_revision with migration v7 (E9-T1/M-17, written from
// the creating intent at insert); state_transitions keeps its
// context-JSON lineage by design (its write volume would duplicate the
// revision on every row for no query).
const schemaV1 = `
CREATE TABLE resources (
	resource_id    TEXT PRIMARY KEY,
	revision       TEXT NOT NULL UNIQUE,
	root           TEXT NOT NULL,
	canonical_root TEXT NOT NULL,
	file_scope     TEXT NOT NULL,
	git_mode       TEXT NOT NULL
);

CREATE TABLE routes (
	route_id    TEXT PRIMARY KEY,
	revision    TEXT NOT NULL UNIQUE,
	policy_revision TEXT NOT NULL,
	resource_id TEXT NOT NULL REFERENCES resources(resource_id),
	target_id   TEXT NOT NULL,
	definition  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);

CREATE TABLE source_observations (
	observation_id     TEXT PRIMARY KEY,
	schema_version     TEXT NOT NULL,
	source_type        TEXT NOT NULL,
	source_id          TEXT NOT NULL,
	source_event_key   TEXT,
	trigger_name       TEXT NOT NULL,
	resource_id        TEXT NOT NULL REFERENCES resources(resource_id),
	observed_at        TEXT NOT NULL,
	received_at        TEXT NOT NULL,
	raw_payload_digest TEXT NOT NULL,
	ingest_status      TEXT NOT NULL,
	flags_json         TEXT NOT NULL DEFAULT '{}'
);

CREATE UNIQUE INDEX idx_observations_source_event ON source_observations(source_id, source_event_key) WHERE source_event_key IS NOT NULL;

CREATE TABLE observation_changes (
	observation_id TEXT NOT NULL REFERENCES source_observations(observation_id) ON DELETE CASCADE,
	ordinal        INTEGER NOT NULL,
	path           TEXT NOT NULL,
	operation      TEXT NOT NULL CHECK (operation IN ('create','modify','delete')),
	exists_after   INTEGER NOT NULL CHECK (exists_after IN (0,1)),
	file_type      TEXT NOT NULL CHECK (file_type IN ('regular','directory','symlink','other','unknown')),
	before_digest  TEXT,
	after_digest   TEXT,
	digest_status  TEXT NOT NULL CHECK (digest_status IN ('known','unavailable','not_applicable')),
	PRIMARY KEY (observation_id, ordinal)
);
CREATE INDEX idx_observation_changes_path ON observation_changes(path);

CREATE TABLE change_batches (
	batch_id           TEXT PRIMARY KEY,
	route_id           TEXT NOT NULL REFERENCES routes(route_id),
	route_revision     TEXT NOT NULL,
	resource_id        TEXT NOT NULL REFERENCES resources(resource_id),
	created_at         TEXT NOT NULL,
	content_fingerprint TEXT NOT NULL,
	window_json        TEXT NOT NULL DEFAULT '{}',
	flags_json         TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE batch_observations (
	batch_id       TEXT NOT NULL REFERENCES change_batches(batch_id) ON DELETE CASCADE,
	observation_id TEXT NOT NULL REFERENCES source_observations(observation_id),
	PRIMARY KEY (batch_id, observation_id)
);

CREATE TABLE policy_decisions (
	decision_id       TEXT PRIMARY KEY,
	batch_id          TEXT REFERENCES change_batches(batch_id),
	route_id          TEXT NOT NULL REFERENCES routes(route_id),
	route_revision    TEXT NOT NULL,
	policy_revision   TEXT NOT NULL,
	generation_lineage_json TEXT,
	disposition       TEXT NOT NULL CHECK (disposition IN ('drop','dispatch','merge_pending','quarantine','reconcile')),
	classification    TEXT NOT NULL CHECK (classification IN ('normal','protected','bulk','overflow','malformed','stale','unknown')),
	reason_codes_json TEXT NOT NULL DEFAULT '[]',
	created_at        TEXT NOT NULL,
	actor             TEXT NOT NULL,
	supersedes_decision_id TEXT,
	CHECK ((batch_id IS NULL) <> (generation_lineage_json IS NULL))
);

CREATE TABLE dispatch_intents (
	dispatch_id      TEXT PRIMARY KEY,
	decision_id      TEXT NOT NULL REFERENCES policy_decisions(decision_id),
	route_id         TEXT NOT NULL REFERENCES routes(route_id),
	route_revision   TEXT NOT NULL,
	target_id        TEXT NOT NULL,
	target_type      TEXT NOT NULL,
	resource_id      TEXT NOT NULL REFERENCES resources(resource_id),
	generation       INTEGER NOT NULL CHECK (generation >= 1),
	idempotency_key  TEXT NOT NULL,
	content_fingerprint TEXT NOT NULL,
	manifest_digest  TEXT NOT NULL,
	request_version  TEXT NOT NULL,
	request_json     TEXT NOT NULL,
	state            TEXT NOT NULL CHECK (state IN ('ready','submitting','accepted','rejected','unknown','retry_wait','reconciling','dead_lettered','superseded','completed','failed','canceled')),
	lease_owner      TEXT,
	lease_expires_at TEXT,
	attempt_count    INTEGER NOT NULL DEFAULT 0,
	next_attempt_at  TEXT,
	external_ref     TEXT,
	created_at       TEXT NOT NULL,
	updated_at       TEXT NOT NULL,
	UNIQUE (target_id, idempotency_key)
);
CREATE INDEX idx_intents_state ON dispatch_intents(state);

CREATE TABLE dispatch_attempts (
	attempt_id     TEXT PRIMARY KEY,
	dispatch_id    TEXT NOT NULL REFERENCES dispatch_intents(dispatch_id),
	lease_owner    TEXT NOT NULL,
	started_at     TEXT NOT NULL,
	completed_at   TEXT,
	outcome        TEXT CHECK (outcome IN ('accepted','rejected','unknown','transport_failure')),
	error_code     TEXT,
	response_digest TEXT,
	diagnostic     TEXT,
	UNIQUE (dispatch_id, started_at)
);

CREATE TABLE dispatch_receipts (
	receipt_id        TEXT PRIMARY KEY,
	dispatch_id       TEXT NOT NULL REFERENCES dispatch_intents(dispatch_id),
	receipt_kind      TEXT NOT NULL CHECK (receipt_kind IN ('acceptance','execution_projection')),
	acceptance_state  TEXT CHECK (acceptance_state IN ('accepted','rejected','unknown')),
	execution_state   TEXT CHECK (execution_state IN ('unavailable','queued','running','succeeded','failed','canceled')),
	durable           INTEGER,
	external_ref      TEXT,
	target_observed_at TEXT,
	received_at       TEXT NOT NULL,
	payload_version   TEXT,
	bounded_payload   TEXT NOT NULL DEFAULT '{}',
	CHECK (receipt_kind != 'acceptance' OR acceptance_state IS NOT NULL),
	CHECK (receipt_kind != 'execution_projection' OR execution_state IS NOT NULL)
);
CREATE INDEX idx_receipts_dispatch ON dispatch_receipts(dispatch_id);

CREATE TABLE work_receipts (
	receipt_id      TEXT PRIMARY KEY,
	dispatch_id     TEXT NOT NULL REFERENCES dispatch_intents(dispatch_id),
	run_id          TEXT NOT NULL,
	resource_id     TEXT NOT NULL REFERENCES resources(resource_id),
	status          TEXT NOT NULL CHECK (status IN ('begun','completed','failed')),
	failure_code    TEXT CHECK (failure_code IN ('agent_error','canceled','timeout','environment_error')),
	external_task_id TEXT,
	base_revision   TEXT,
	result_revision TEXT,
	changes_json    TEXT NOT NULL DEFAULT '[]',
	submitted_at    TEXT NOT NULL,
	validation_state TEXT NOT NULL CHECK (validation_state IN ('valid','invalid','incomplete')),
	validation_reasons_json TEXT NOT NULL DEFAULT '[]',
	UNIQUE (dispatch_id, run_id)
);

CREATE TABLE route_runtime_state (
	route_id              TEXT PRIMARY KEY REFERENCES routes(route_id),
	activation_state      TEXT NOT NULL CHECK (activation_state IN ('disabled','enabled','paused')),
	acknowledged_revision TEXT,
	route_state           TEXT NOT NULL CHECK (route_state IN ('IDLE','ACTIVE_CLEAN','ACTIVE_DIRTY','FOLLOWUP_READY','UNCERTAIN','QUARANTINED')),
	active_dispatch_id    TEXT REFERENCES dispatch_intents(dispatch_id),
	active_generation     INTEGER NOT NULL DEFAULT 0,
	dirty_generation      INTEGER NOT NULL DEFAULT 0,
	dirty_since           TEXT,
	pending_reconcile     INTEGER NOT NULL DEFAULT 0 CHECK (pending_reconcile IN (0,1)),
	last_source_position  TEXT,
	last_reconciled_at    TEXT,
	version               INTEGER NOT NULL DEFAULT 0 CHECK (version >= 0)
);

CREATE TABLE path_facts (
	resource_id TEXT NOT NULL REFERENCES resources(resource_id) ON DELETE CASCADE,
	path        TEXT NOT NULL,
	digest      TEXT,
	"exists"    INTEGER NOT NULL CHECK ("exists" IN (0,1)),
	observed_at TEXT NOT NULL,
	PRIMARY KEY (resource_id, path)
);

CREATE TABLE quarantine_items (
	quarantine_id            TEXT PRIMARY KEY,
	batch_id                 TEXT REFERENCES change_batches(batch_id),
	decision_id              TEXT NOT NULL REFERENCES policy_decisions(decision_id),
	reason_codes_json        TEXT NOT NULL DEFAULT '[]',
	state                    TEXT NOT NULL CHECK (state IN ('held','released','discarded','superseded')),
	created_at               TEXT NOT NULL,
	resolved_at              TEXT,
	resolved_by              TEXT,
	resolution_reason        TEXT,
	replacement_decision_id  TEXT
);

CREATE TABLE state_transitions (
	transition_id TEXT PRIMARY KEY,
	entity_type   TEXT NOT NULL,
	entity_id     TEXT NOT NULL,
	from_state    TEXT,
	to_state      TEXT NOT NULL,
	recorded_at   TEXT NOT NULL,
	context_json  TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_transitions_entity ON state_transitions(entity_type, entity_id);

-- Append-only audit history (DUR-011): updates and deletes fail closed.
CREATE TRIGGER state_transitions_no_update BEFORE UPDATE ON state_transitions
BEGIN
	SELECT RAISE(ABORT, 'state_transitions is append-only');
END;
CREATE TRIGGER state_transitions_no_delete BEFORE DELETE ON state_transitions
BEGIN
	SELECT RAISE(ABORT, 'state_transitions is append-only');
END;
`

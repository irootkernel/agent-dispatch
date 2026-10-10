package sqlite

// Prepared rows deliberately do not reference sync_controls: an inert draft
// must not materialize control intent or fabricate historical job lineage.
const schemaV28SyncManagement = `
CREATE TABLE sync_management_requests (
 request_id TEXT PRIMARY KEY,
 group_id TEXT NOT NULL,
 instance_id TEXT NOT NULL,
 state_incarnation_id TEXT NOT NULL,
 action TEXT NOT NULL CHECK(action IN ('sync-now','verify-all','retry','pause','resume')),
 target_request_id TEXT REFERENCES sync_management_requests(request_id),
 policy_revision TEXT NOT NULL,
 config_revision TEXT NOT NULL,
 expected_control_revision INTEGER CHECK(expected_control_revision >= 1),
 fingerprint TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('prepared','expired','queued','running','blocked','uncertain','finished')),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision >= 1),
 created_at TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 accepted_at TEXT,
 resolved_at TEXT,
 attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 20),
 fence INTEGER NOT NULL DEFAULT 0 CHECK(fence >= 0),
 claim_owner TEXT,
 claim_expires_at TEXT,
 execution_deadline TEXT,
 association_reserved INTEGER NOT NULL DEFAULT 0 CHECK(association_reserved BETWEEN 0 AND 1000),
 outcome_json TEXT CHECK(outcome_json IS NULL OR json_valid(outcome_json)),
 CHECK((action='retry')=(target_request_id IS NOT NULL)),
 CHECK((action IN ('pause','resume'))=(expected_control_revision IS NOT NULL)),
 CHECK((state IN ('prepared','expired'))=(accepted_at IS NULL)),
 CHECK((state='finished')=(resolved_at IS NOT NULL)),
 CHECK(state!='finished' OR outcome_json IS NOT NULL),
 CHECK((claim_owner IS NULL)=(claim_expires_at IS NULL)),
 CHECK((claim_owner IS NULL)=(execution_deadline IS NULL)),
 CHECK((state='running')=(claim_owner IS NOT NULL))
);
CREATE INDEX idx_management_capacity ON sync_management_requests(group_id,action,state);
CREATE INDEX idx_management_retry ON sync_management_requests(target_request_id);
CREATE INDEX idx_management_queue ON sync_management_requests(group_id,state,accepted_at,request_id);
CREATE TRIGGER management_immutable_binding BEFORE UPDATE ON sync_management_requests
WHEN NEW.request_id IS NOT OLD.request_id OR NEW.group_id IS NOT OLD.group_id
 OR NEW.instance_id IS NOT OLD.instance_id OR NEW.state_incarnation_id IS NOT OLD.state_incarnation_id
 OR NEW.action IS NOT OLD.action OR NEW.target_request_id IS NOT OLD.target_request_id
 OR NEW.policy_revision IS NOT OLD.policy_revision OR NEW.config_revision IS NOT OLD.config_revision
 OR NEW.expected_control_revision IS NOT OLD.expected_control_revision OR NEW.fingerprint IS NOT OLD.fingerprint
 OR NEW.created_at IS NOT OLD.created_at OR NEW.expires_at IS NOT OLD.expires_at
 OR (OLD.accepted_at IS NOT NULL AND NEW.accepted_at IS NOT OLD.accepted_at)
 OR (OLD.state='finished' AND (NEW.state IS NOT OLD.state OR NEW.outcome_json IS NOT OLD.outcome_json OR NEW.resolved_at IS NOT OLD.resolved_at))
BEGIN SELECT RAISE(ABORT,'immutable management request'); END;
CREATE TRIGGER management_lifecycle BEFORE UPDATE OF state ON sync_management_requests
WHEN NEW.state!=OLD.state AND NOT (
 (OLD.state='prepared' AND NEW.state IN ('expired','queued','finished')) OR
 (OLD.state='queued' AND NEW.state IN ('running','blocked')) OR
 (OLD.state='running' AND NEW.state IN ('queued','blocked','uncertain','finished')) OR
 (OLD.state='blocked' AND NEW.state IN ('queued','uncertain','finished')) OR
 (OLD.state='uncertain' AND NEW.state IN ('queued','blocked','finished')))
BEGIN SELECT RAISE(ABORT,'invalid management lifecycle'); END;
CREATE TABLE sync_management_acceptances (
 request_id TEXT PRIMARY KEY REFERENCES sync_management_requests(request_id) ON DELETE CASCADE,
 accepted_at TEXT NOT NULL,
 fingerprint TEXT NOT NULL
);
CREATE TRIGGER management_acceptance_immutable BEFORE UPDATE ON sync_management_acceptances
BEGIN SELECT RAISE(ABORT,'immutable management acceptance'); END;
CREATE TABLE sync_management_executions (
 request_id TEXT NOT NULL REFERENCES sync_management_requests(request_id) ON DELETE CASCADE,
 fence INTEGER NOT NULL CHECK(fence BETWEEN 1 AND 20),
 owner TEXT NOT NULL,
 started_at TEXT NOT NULL,
 deadline TEXT NOT NULL,
 ended_at TEXT,
 disposition TEXT,
 evidence_reference TEXT,
 outcome_json TEXT CHECK(outcome_json IS NULL OR json_valid(outcome_json)),
 PRIMARY KEY(request_id,fence),
 CHECK((ended_at IS NULL)=(disposition IS NULL)),
 CHECK((ended_at IS NULL)=(evidence_reference IS NULL))
);
CREATE TRIGGER management_execution_immutable BEFORE UPDATE ON sync_management_executions
WHEN NEW.request_id IS NOT OLD.request_id OR NEW.fence IS NOT OLD.fence OR NEW.owner IS NOT OLD.owner
 OR NEW.started_at IS NOT OLD.started_at OR NEW.deadline IS NOT OLD.deadline OR OLD.ended_at IS NOT NULL
BEGIN SELECT RAISE(ABORT,'immutable management execution'); END;
CREATE TABLE sync_management_jobs (
 request_id TEXT NOT NULL REFERENCES sync_management_requests(request_id) ON DELETE CASCADE,
 job_id TEXT NOT NULL REFERENCES sync_jobs(job_id),
 kind TEXT NOT NULL CHECK(kind IN ('publication','delivery','import','verification')),
 target_commit TEXT,
 association_reason TEXT NOT NULL CHECK(association_reason IN ('observed_reconciliation','already_signed_recovery','verification_target','retry_obligation')),
 PRIMARY KEY(request_id,job_id)
);
CREATE INDEX idx_management_job_retention ON sync_management_jobs(job_id);
CREATE TRIGGER management_job_immutable BEFORE UPDATE ON sync_management_jobs
BEGIN SELECT RAISE(ABORT,'immutable management job association'); END;
`

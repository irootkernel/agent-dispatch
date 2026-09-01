package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// ErrSchemaTooNewType is the typed newer-database failure.
type ErrSchemaTooNewType struct{ Detail string }

func (e *ErrSchemaTooNewType) Error() string { return e.Detail }

// ErrSchemaTooNew is the matching sentinel for errors.Is.
var ErrSchemaTooNew = &ErrSchemaTooNewType{Detail: "database schema newer than supported"}

// Migration is one forward-only schema unit. Checksum is computed from
// the SQL text and recorded immutably in the ledger (migration doc §3).
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrations is the ordered, gap-free migration list the binary supports.
var Migrations = []Migration{
	{Version: 1, Name: "initial-schema", SQL: schemaV1},
	{Version: 2, Name: "attempts-unique-by-attempt-id", SQL: schemaV2AttemptsUniqueByAttemptID},
	{Version: 3, Name: "intent-target-scope", SQL: schemaV3IntentTargetScope},
	{Version: 4, Name: "work-receipts-begun-at", SQL: schemaV4WorkReceiptsBegunAt},
	{Version: 5, Name: "observation-position", SQL: schemaV5ObservationPosition},
	{Version: 6, Name: "batch-sequence-watermark", SQL: schemaV6BatchSequenceWatermark},
	{Version: 7, Name: "record-revision-columns", SQL: schemaV7RecordRevisionColumns},
	{Version: 8, Name: "resource-observation-revision", SQL: schemaV8ResourceObservationRevision},
	{Version: 9, Name: "watch-bindings", SQL: schemaV9WatchBindings},
	{Version: 10, Name: "destinations-contract-cutover", SQL: schemaV10DestinationsContractCutover},
	{Version: 11, Name: "capability-fingerprint", SQL: schemaV11CapabilityFingerprint},
	{Version: 12, Name: "aggregate-fanout-records", SQL: schemaV12AggregateFanoutRecords},
	{Version: 13, Name: "destination-lane-state", SQL: schemaV13DestinationLaneState},
	{Version: 14, Name: "work-receipt-v2-outcomes", SQL: schemaV14WorkReceiptV2Outcomes},
	{Version: 15, Name: "merge-selection-evidence", SQL: schemaV15MergeSelectionEvidence},
	{Version: 16, Name: "notification-events-attempts", SQL: schemaV16NotificationEventsAttempts},
	{Version: 17, Name: "route-baselines", SQL: schemaV17RouteBaselines},
	{Version: 18, Name: "serialization-group-state", SQL: schemaV18SerializationGroupState},
	{Version: 19, Name: "notification-drain-leases", SQL: schemaV19NotificationDrainLeases},
}

// MaxSchemaVersion is the highest version this binary understands; a
// database at a newer version is refused rather than silently modified.
var MaxSchemaVersion = Migrations[len(Migrations)-1].Version

// migrationVersion resolves one registered migration's version by
// name. It panics at init for an unregistered name: callers bind
// schema-versioned gates to registry entries, and a missing entry is
// a programming error every test run catches, never a runtime
// posture.
func migrationVersion(name string) int64 {
	for _, m := range Migrations {
		if m.Name == name {
			return int64(m.Version)
		}
	}
	panic(fmt.Sprintf("migration %q is not registered", name))
}

// schemaV2AttemptsUniqueByAttemptID drops the over-constraining
// UNIQUE(dispatch_id, started_at) on dispatch_attempts: with the
// canonical second-precision timestamps it rejected legitimate
// same-second retries of one dispatch (the explicit operator retry of
// dead-lettered work). Attempt identity is the primary-keyed attempt
// id; per-dispatch ordering stays queryable through started_at.
const schemaV2AttemptsUniqueByAttemptID = `
CREATE TABLE dispatch_attempts_v2 (
	attempt_id     TEXT PRIMARY KEY,
	dispatch_id    TEXT NOT NULL REFERENCES dispatch_intents(dispatch_id),
	lease_owner    TEXT NOT NULL,
	started_at     TEXT NOT NULL,
	completed_at   TEXT,
	outcome        TEXT CHECK (outcome IN ('accepted','rejected','unknown','transport_failure')),
	error_code     TEXT,
	response_digest TEXT,
	diagnostic     TEXT
);
INSERT INTO dispatch_attempts_v2 SELECT attempt_id, dispatch_id, lease_owner, started_at, completed_at, outcome, error_code, response_digest, diagnostic FROM dispatch_attempts;
DROP TABLE dispatch_attempts;
ALTER TABLE dispatch_attempts_v2 RENAME TO dispatch_attempts;
CREATE INDEX idx_attempts_dispatch ON dispatch_attempts(dispatch_id, started_at);
`

// schemaV3IntentTargetScope records the resolved target scope (for
// hermes-kanban, the board slug) on each dispatch intent at lineage
// commit, so later reconciliation can prove it reads the same target
// scope that was configured at submission time (E4 audit: a re-pointed
// board must never turn a wrong-board absence into a resubmission
// proof).
const schemaV3IntentTargetScope = `
ALTER TABLE dispatch_intents ADD COLUMN target_scope TEXT NOT NULL DEFAULT '';
`

// schemaV4WorkReceiptsBegunAt preserves the begin timestamp of a run
// across its terminal update (E5-T3): the receipt row's submitted_at is
// rewritten by `work complete`/`work fail`, but exact self-change
// attribution must compare observations against the run's begin window,
// so the begin time is kept in its own column and backfilled from the
// submitted timestamp of existing begun rows.
const schemaV4WorkReceiptsBegunAt = `
ALTER TABLE work_receipts ADD COLUMN begun_at TEXT;
UPDATE work_receipts SET begun_at = submitted_at WHERE begun_at IS NULL;
`

// checksum returns the immutable migration checksum.
func (m Migration) checksum() string {
	sum := sha256.Sum256([]byte(m.Name + "\x00" + m.SQL))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Migrate applies pending migrations serialized on the store's single
// connection under the SQLite write lock (the application-level migration
// lock for this single-writer design), verifying ledger checksums and
// prefix integrity, refusing newer unsupported schema versions, and
// creating a validated online backup in backupDir before any migration
// runs. It performs no external side effects inside the migration
// transaction (OPS-009).
func (s *Store) Migrate(backupDir string) error {
	if backupDir == "" {
		return fmt.Errorf("migrations require a backup directory (backup before migration)")
	}
	// The serialization-group gate cache may predate the units this
	// run applies: reset it so the slot paths re-probe the ledger
	// after the upgrade (E15 cold-validation F002).
	s.groupGate.Store(groupGateUnknown)
	list := s.migrations
	if list == nil {
		list = Migrations
	}
	maxVersion := list[len(list)-1].Version

	// Application-level migration lock (E7-T7/M-4, OPS-009): concurrent
	// first opens serialize through an exclusive lock file; exactly one
	// process applies the units and the others wait for its completion
	// and then observe the finished ledger. A crashed holder's lock is
	// stolen after the bounded staleness window.
	lockRelease, err := acquireMigrationFileLock(s.Path())
	if err != nil {
		return err
	}
	defer lockRelease()
	if err := s.ensureMigrationLedger(); err != nil {
		return err
	}
	applied, err := s.appliedMigrations()
	if err != nil {
		return err
	}
	newest := 0
	for version := range applied {
		if version > newest {
			newest = version
		}
	}
	if newest > maxVersion {
		return &ErrSchemaTooNewType{Detail: fmt.Sprintf("database schema version %d is newer than the supported maximum %d; upgrade agent-dispatch instead of downgrading", newest, maxVersion)}
	}
	// Prefix integrity: the applied set must be exactly {1..newest} drawn
	// from this binary's list, with no gaps and no foreign versions.
	for version := 1; version <= newest; version++ {
		sum, ok := applied[version]
		if !ok {
			return fmt.Errorf("migration ledger has a gap at version %d", version)
		}
		want := ""
		for _, m := range list {
			if m.Version == version {
				want = m.checksum()
			}
		}
		if want == "" || sum != want {
			return fmt.Errorf("migration ledger entry %d does not match this binary's immutable history", version)
		}
	}
	// One verified backup per migration run (L-20, E9-T1): the snapshot
	// before the first pending unit already contains every pending unit's
	// pre-state, so a per-unit copy multiplied backups by the pending
	// count (a fresh open took six) without adding a restore point.
	pending := 0
	for _, m := range list {
		if _, ok := applied[m.Version]; !ok {
			pending++
		}
	}
	if pending > 0 {
		if err := s.Backup(fmt.Sprintf("%s/agent-dispatch-v%d-to-v%d-%s.backup", backupDir, newest, maxVersion, nowFileTimestamp())); err != nil {
			return fmt.Errorf("pre-migration backup: %w", err)
		}
	}
	for _, m := range list {
		sum, ok := applied[m.Version]
		if ok {
			if sum != m.checksum() {
				return fmt.Errorf("migration %d checksum mismatch: ledger %s, binary %s (migrations are immutable)", m.Version, sum, m.checksum())
			}
			continue
		}
		if err := s.applyMigration(m); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.Version, m.Name, err)
		}
	}
	return nil
}

func (s *Store) ensureMigrationLedger() error {
	_, err := s.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		checksum   TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	)`)
	return err
}

func (s *Store) appliedMigrations() (map[int]string, error) {
	rows, err := s.Query(`SELECT version, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[int]string{}
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, err
		}
		applied[version] = checksum
	}
	return applied, rows.Err()
}

// applyMigration runs one migration in one transaction on the store's
// single connection. The connection is process-serial and the SQLite
// write lock excludes other writers for the transaction's duration, which
// is the application-level migration lock for this single-writer design;
// concurrent processes serialize on busy_timeout instead of interleaving.
func (s *Store) applyMigration(m Migration) error {
	tx, err := s.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(m.SQL); err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (version, name, checksum) VALUES (?, ?, ?)`, m.Version, m.Name, m.checksum()); err != nil {
		return err
	}
	return tx.Commit()
}

// Backup creates an online backup of the database at path using
// VACUUM INTO, which works concurrently with readers (persistence §9).
// The backup's parent directory is created owner-only if absent.
func (s *Store) Backup(path string) error {
	if _, err := osStatFile(path); err == nil {
		return fmt.Errorf("backup %s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := s.Exec(`VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("backup %s: %w", path, err)
	}
	// VACUUM INTO creates the file with the process umask; database
	// backups are owner-only by default (SEC-008).
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("backup %s permissions: %w", path, err)
	}
	// The backup must be a restorable database, not just a file.
	if err := quickCheckFile(path); err != nil {
		return fmt.Errorf("backup %s: %w", path, err)
	}
	return nil
}

// quickCheckFile opens a database file read-only and verifies it passes
// PRAGMA quick_check.
func quickCheckFile(path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	defer db.Close()
	var result string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("verification reported %q", result)
	}
	return nil
}

// IntegrityCheck runs PRAGMA quick_check (full selects PRAGMA
// integrity_check) for doctor and startup checks (OPS-005 posture).
func (s *Store) IntegrityCheck(full bool) error {
	stmt := "PRAGMA quick_check"
	if full {
		stmt = "PRAGMA integrity_check"
	}
	rows, err := s.Query(stmt)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return err
		}
		if result != "ok" {
			return fmt.Errorf("integrity check reported: %s", result)
		}
	}
	return rows.Err()
}

// SchemaVersion returns the current applied schema version.
func (s *Store) SchemaVersion() (int, error) {
	var version sql.NullInt64
	if err := s.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, err
	}
	if !version.Valid {
		return 0, nil
	}
	return int(version.Int64), nil
}

// ApplyMigrationForHarness applies exactly one migration unit inside
// its own transaction. It exists for the E3-T5 crash-injection harness
// (TST-004) and is not called by the product paths.
func (s *Store) ApplyMigrationForHarness(m Migration) error {
	return s.applyMigration(m)
}

// EnsureLedgerForHarness creates the migration ledger without applying
// any unit, so the crash harness can interrupt at exact unit boundaries
// (AC-207, E7-T4).
func (s *Store) EnsureLedgerForHarness() error {
	return s.ensureMigrationLedger()
}

// ApplyMigrationSQLWithoutLedgerForHarness executes one migration
// unit's SQL inside an open transaction that the harness abandons by
// dying hard: the unit's statements run, the ledger insert never
// commits, and the WAL discards the unit (AC-207's in-unit window,
// E7-T4 round-1 remediation: the transaction structure stays inside the
// sqlite package, not re-encoded in the harness).
func (s *Store) ApplyMigrationSQLWithoutLedgerForHarness(m Migration) error {
	tx, err := s.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(m.SQL); err != nil {
		return err
	}
	return nil
}

// migrationLockStaleness is how old a lock file may be before a waiter
// atomically steals it from a holder that died without releasing.
const migrationLockStaleness = 30 * time.Second

// migrationLockRefresh is how often a live holder refreshes its lock's
// mtime so a migration slower than the staleness bound is never stolen
// from its owner (E8-T2, M-4); a package variable so tests can shorten
// the tick.
var migrationLockRefresh = migrationLockStaleness / 3

// acquireMigrationFileLock serializes concurrent migration passes across
// processes with an exclusive lock file beside the database. Waiters
// poll for the holder's completion; a lock older than the staleness
// bound is stolen through an atomic rename (exactly one stealer wins),
// and the release removes only the file this process owns. A wait that
// outlives the retryable window surfaces as busy congestion, never a
// fatal open failure.
func acquireMigrationFileLock(dbPath string) (func(), error) {
	lockPath := dbPath + ".migration-lock"
	deadline := time.Now().Add(2 * time.Minute)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			// Heartbeat: a migration slower than the staleness bound
			// refreshes its lock's mtime so a waiter cannot steal a lock
			// its holder still owns (E8-T2, M-4 — a VACUUM INTO plus
			// table rebuilds can outlive the old once-written mtime). The
			// interval is read before the goroutine spawns: the package
			// variable is a test injection point, and capturing it here
			// keeps the spawn race-free against a concurrent restore.
			interval := migrationLockRefresh
			stop := make(chan struct{})
			go func() {
				ticker := time.NewTicker(interval)
				defer ticker.Stop()
				for {
					select {
					case <-stop:
						return
					case <-ticker.C:
						if data, readErr := os.ReadFile(lockPath); readErr == nil && strings.TrimSpace(string(data)) == fmt.Sprint(os.Getpid()) {
							_ = os.Chtimes(lockPath, time.Now(), time.Now())
						}
					}
				}
			}()
			return func() {
				close(stop)
				// Owned release: remove the lock only when it is still
				// ours (a stealer may already have replaced it).
				if data, readErr := os.ReadFile(lockPath); readErr == nil && strings.TrimSpace(string(data)) == fmt.Sprint(os.Getpid()) {
					os.Remove(lockPath)
				}
			}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > migrationLockStaleness {
			// Atomic steal: rename moves the stale lock aside; exactly
			// one racing stealer succeeds, the losers see it gone.
			stolen := fmt.Sprintf("%s.stolen-%d-%d", lockPath, os.Getpid(), time.Now().UnixNano())
			if renameErr := os.Rename(lockPath, stolen); renameErr == nil {
				os.Remove(stolen)
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, &ports.StoreError{Err: fmt.Errorf("migration lock held by another process past the wait window: %s", lockPath)}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// schemaV5ObservationPosition persists the verbatim source position
// object on each observation (the observation contract's
// source.position, E7-T8/M-11).
const schemaV5ObservationPosition = `
ALTER TABLE source_observations ADD COLUMN position_json TEXT;
`

// schemaV6BatchSequenceWatermark keys the active generation window on a
// monotonic batch sequence instead of second-truncated timestamps
// (E8-T1, H-1.3): change_batches.batch_seq is assigned by SaveBatch as
// MAX+1 inside the insert transaction, dispatch_intents.base_batch_seq
// records the sequence the intent's generation window starts after (the
// own arrival batch for normal dispatches, the current maximum at
// creation for follow-ups), and LoadActiveGenerationChanges compares
// batch_seq so a batch merged in the same second as a completion is
// never re-imported into the next generation. The backfill orders
// existing rows by (created_at, batch_id), which is deterministic. One
// recorded residual (E8-T1 round-1 F006): the fallback arm keys on
// second-truncated created_at, so a pre-migration decision-less intent
// (a follow-up in flight at upgrade time) may exclude a batch that
// arrived later within its creation second — a one-time degraded first
// follow-up (the parent-manifest fallback covers it), never a wedge;
// every intent created after the migration anchors on the exact
// watermark.
const schemaV6BatchSequenceWatermark = `
ALTER TABLE change_batches ADD COLUMN batch_seq INTEGER;
UPDATE change_batches SET batch_seq = (
	SELECT COUNT(*) FROM change_batches b2
	WHERE b2.created_at < change_batches.created_at
	   OR (b2.created_at = change_batches.created_at AND b2.batch_id <= change_batches.batch_id)
);
CREATE UNIQUE INDEX idx_change_batches_batch_seq ON change_batches(batch_seq);
ALTER TABLE dispatch_intents ADD COLUMN base_batch_seq INTEGER NOT NULL DEFAULT 0;
UPDATE dispatch_intents SET base_batch_seq = COALESCE(
	(SELECT b.batch_seq FROM policy_decisions p
		JOIN change_batches b ON b.batch_id = p.batch_id
		WHERE p.decision_id = dispatch_intents.decision_id),
	(SELECT COALESCE(MAX(b2.batch_seq), 0) FROM change_batches b2
		WHERE b2.created_at <= dispatch_intents.created_at)
);
`

// schemaV7RecordRevisionColumns adds the route revision to the four
// record tables that previously reached it joinably (E9-T1, M-17): the
// value is the creating intent's revision, backfilled by join; every
// insert site writes it from the intent's revision from this version
// on. state_transitions keeps its context-JSON lineage (its write
// volume would duplicate the revision on every row for no query).
const schemaV7RecordRevisionColumns = `
ALTER TABLE dispatch_attempts ADD COLUMN route_revision TEXT NOT NULL DEFAULT '';
UPDATE dispatch_attempts SET route_revision = (
	SELECT i.route_revision FROM dispatch_intents i WHERE i.dispatch_id = dispatch_attempts.dispatch_id);
ALTER TABLE dispatch_receipts ADD COLUMN route_revision TEXT NOT NULL DEFAULT '';
UPDATE dispatch_receipts SET route_revision = (
	SELECT i.route_revision FROM dispatch_intents i WHERE i.dispatch_id = dispatch_receipts.dispatch_id);
ALTER TABLE work_receipts ADD COLUMN route_revision TEXT NOT NULL DEFAULT '';
UPDATE work_receipts SET route_revision = (
	SELECT i.route_revision FROM dispatch_intents i WHERE i.dispatch_id = work_receipts.dispatch_id);
ALTER TABLE quarantine_items ADD COLUMN route_revision TEXT NOT NULL DEFAULT '';
UPDATE quarantine_items SET route_revision = (
	SELECT i.route_revision FROM dispatch_intents i WHERE i.decision_id = quarantine_items.decision_id);
CREATE INDEX idx_attempts_route_revision ON dispatch_attempts(route_revision);
CREATE INDEX idx_receipts_route_revision ON dispatch_receipts(route_revision);
CREATE INDEX idx_work_receipts_route_revision ON work_receipts(route_revision);
CREATE INDEX idx_quarantine_route_revision ON quarantine_items(route_revision);
`

// schemaV8ResourceObservationRevision gives every resource the monotonic
// path-fact observation revision (E10-T1, DUR-013): each durable path-fact
// mutation advances it inside its own transaction, and full reconciliation
// fences its snapshot replacement on the pre-enumeration value (DUR-014).
// Existing rows backfill to revision 0 — the first fenced reconciliation
// after the upgrade simply observes whatever the next mutation advances.
const schemaV8ResourceObservationRevision = `
ALTER TABLE resources ADD COLUMN observation_revision INTEGER NOT NULL DEFAULT 0 CHECK (observation_revision >= 0);
`

// schemaV9WatchBindings persists the four-part managed Watchman binding
// per route (E10-T2, SRC-009): the configured resource root, the actual
// watch root Watchman canonicalized at install time (which may be an
// ancestor of the configured root), the configured-root-relative path
// between them, and the stable trigger name. Every lifecycle command
// resolves and reports the same record; the dispatch-side binding
// validation accepts an ancestor root only through this record, so a
// forged or drifted environment fails closed (SRC-011).
const schemaV9WatchBindings = `
CREATE TABLE watch_bindings (
	route_id        TEXT PRIMARY KEY REFERENCES routes(route_id) ON DELETE CASCADE,
	resource_id     TEXT NOT NULL REFERENCES resources(resource_id) ON DELETE CASCADE,
	configured_root TEXT NOT NULL,
	actual_root     TEXT NOT NULL,
	relative_root   TEXT NOT NULL,
	trigger_name    TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);
`

// schemaV10DestinationsContractCutover records the v0.1.5 configuration
// cutover boundary (E11-T1, D-025): the forward migration for the
// destinations[] contract. No historic row is rewritten — every task,
// attempt, receipt, and work-receipt created under the legacy
// single-dispatch contract remains exactly as queryable as before
// (DAT-012) — but the durable marker records that this database passed
// through the cutover, and route enablement under the new contract
// refuses while unresolved work created under a different (pre-cutover)
// route revision remains (DAT-013, UnresolvedLegacyWork). The per-run
// pre-migration backup (OPS-015) plus the documented rollback procedure
// (keep the upgraded database aside, restore the verified backup with
// the previous binary and configuration) cover the reverse direction;
// no down migration exists.
const schemaV10DestinationsContractCutover = `
CREATE TABLE contract_state (
	contract   TEXT PRIMARY KEY,
	applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
INSERT INTO contract_state (contract) VALUES ('destinations-v1');
`

// schemaV11CapabilityFingerprint binds route activation to the accepted
// capability-evidence fingerprint in addition to the acknowledged route
// revision (E11-T2, HER-018): enable records the fingerprint the probe
// produced, and the submit path re-proves the executable identity
// before any side effect, so an executable or probe-contract change
// invalidates the activation exactly like a behavior change.
const schemaV11CapabilityFingerprint = `
ALTER TABLE route_runtime_state ADD COLUMN capability_fingerprint TEXT NOT NULL DEFAULT '';
`

// schemaV12AggregateFanoutRecords introduces the multi-destination record
// families (E12-T1, ADR-0016, DAT-010): one aggregate event per normalized
// source/policy occurrence, one durable destination-revision row per
// canonical behavior projection, and one child dispatch per selected
// destination beneath its aggregate. No historic row is rewritten: intents
// created before the destinations[] contract keep their exact lineage and
// remain queryable (DAT-012) — they simply have no child row, which is the
// legacy marker UnresolvedLegacyWork and the request's absent destination
// block already key on. The child's idempotency key is the DAT-014
// destination-scoped projection, so the historical UNIQUE(target_id,
// idempotency_key) on dispatch_intents keeps guarding duplicates while
// UNIQUE(aggregate_id, destination_id) enforces one child per selected
// destination per occurrence (FAN-003) and UNIQUE(dispatch_id) keeps the
// child-to-intent mapping total where a child exists.
const schemaV12AggregateFanoutRecords = `
CREATE TABLE aggregate_events (
	aggregate_id        TEXT PRIMARY KEY,
	decision_id         TEXT NOT NULL REFERENCES policy_decisions(decision_id),
	route_id            TEXT NOT NULL REFERENCES routes(route_id),
	route_revision      TEXT NOT NULL,
	resource_id         TEXT NOT NULL REFERENCES resources(resource_id),
	origin              TEXT NOT NULL CHECK (origin IN ('arrival','followup','rerun','rebuild','reconcile')),
	generation          INTEGER NOT NULL CHECK (generation >= 1),
	content_fingerprint TEXT NOT NULL,
	schema_version      TEXT NOT NULL,
	selection_json      TEXT NOT NULL,
	created_at          TEXT NOT NULL
);
CREATE INDEX idx_aggregate_events_route ON aggregate_events(route_id, created_at);

CREATE TABLE destination_revisions (
	route_id        TEXT NOT NULL REFERENCES routes(route_id),
	destination_id  TEXT NOT NULL,
	revision        TEXT NOT NULL,
	projection_json TEXT NOT NULL,
	created_at      TEXT NOT NULL,
	PRIMARY KEY (route_id, destination_id, revision)
);

CREATE TABLE child_dispatches (
	child_id             TEXT PRIMARY KEY,
	aggregate_id         TEXT NOT NULL REFERENCES aggregate_events(aggregate_id),
	dispatch_id          TEXT NOT NULL UNIQUE REFERENCES dispatch_intents(dispatch_id),
	route_id             TEXT NOT NULL REFERENCES routes(route_id),
	destination_id       TEXT NOT NULL,
	destination_revision TEXT NOT NULL,
	workstream           TEXT NOT NULL,
	idempotency_key      TEXT NOT NULL,
	schema_version       TEXT NOT NULL,
	created_at           TEXT NOT NULL,
	UNIQUE (aggregate_id, destination_id)
);
CREATE INDEX idx_child_dispatches_lane ON child_dispatches(route_id, destination_id, created_at);
`

// schemaV13DestinationLaneState re-keys route coordination onto
// per-destination lanes (E12-T2, CON-007/CON-008): one destination_lane_state
// row per (route, destination) holds the lane's coordination state — the
// per-lane single-active slot, dirty generation, and failure budget — while
// route_runtime_state keeps the route envelope (activation, acknowledged
// revision, capability fingerprint, pending reconciliation) and the
// route-level QUARANTINED/UNCERTAIN holds. The route row's v12-era
// coordination columns become frozen history: the backfill copies each
// route's current in-flight coordination onto the lane its active dispatch
// belongs to (the child row's destination, else the synthetic '__legacy__'
// lane of ADR-0016), and routes without an active dispatch keep no lane row
// — lanes materialize lazily on their first write.
const schemaV13DestinationLaneState = `
CREATE TABLE destination_lane_state (
    route_id          TEXT NOT NULL REFERENCES routes(route_id),
    destination_id    TEXT NOT NULL,
    lane_state        TEXT NOT NULL CHECK (lane_state IN ('IDLE','ACTIVE_CLEAN','ACTIVE_DIRTY','FOLLOWUP_READY','UNCERTAIN','QUARANTINED')),
    active_dispatch_id TEXT REFERENCES dispatch_intents(dispatch_id),
    active_generation INTEGER NOT NULL DEFAULT 0,
    dirty_generation  INTEGER NOT NULL DEFAULT 0,
    dirty_since       TEXT,
    failure_budget    INTEGER NOT NULL DEFAULT 0,
    version           INTEGER NOT NULL DEFAULT 0 CHECK (version >= 0),
    PRIMARY KEY (route_id, destination_id)
);
INSERT INTO destination_lane_state
    (route_id, destination_id, lane_state, active_dispatch_id, active_generation, dirty_generation, dirty_since, version)
SELECT route_id,
    COALESCE((SELECT c.destination_id FROM child_dispatches c WHERE c.dispatch_id = route_runtime_state.active_dispatch_id), '__legacy__'),
    route_state, active_dispatch_id, active_generation, dirty_generation, dirty_since, version
FROM route_runtime_state
WHERE active_dispatch_id IS NOT NULL;
`

// schemaV14WorkReceiptV2Outcomes widens the work-receipt outcome vocabulary
// (E12-T3, FBK-009/FBK-010/FBK-011): the status CHECK gains
// partially_completed and blocked, and the v2 record members — the partial
// completion's completed/remaining scope and the blocked outcome's manual
// reason — persist as their own bounded JSON/text columns. The table is
// rebuilt the way migration v2 rebuilt dispatch_attempts (create beside,
// copy, drop, rename): no historic row is rewritten, every v1 receipt
// keeps its exact evidence (DAT-009 — the v1 schema version stays
// readable), and the UNIQUE(dispatch_id, run_id) semantics carry over.
const schemaV14WorkReceiptV2Outcomes = `
CREATE TABLE work_receipts_v14 (
	receipt_id      TEXT PRIMARY KEY,
	dispatch_id     TEXT NOT NULL REFERENCES dispatch_intents(dispatch_id),
	run_id          TEXT NOT NULL,
	resource_id     TEXT NOT NULL REFERENCES resources(resource_id),
	status          TEXT NOT NULL CHECK (status IN ('begun','completed','partially_completed','blocked','failed')),
	failure_code    TEXT CHECK (failure_code IN ('agent_error','canceled','timeout','environment_error')),
	external_task_id TEXT,
	base_revision   TEXT,
	result_revision TEXT,
	changes_json    TEXT NOT NULL DEFAULT '[]',
	completed_scope_json TEXT NOT NULL DEFAULT '[]',
	remaining_scope_json TEXT NOT NULL DEFAULT '[]',
	manual_reason   TEXT,
	submitted_at    TEXT NOT NULL,
	begun_at        TEXT,
	validation_state TEXT NOT NULL CHECK (validation_state IN ('valid','invalid','incomplete')),
	validation_reasons_json TEXT NOT NULL DEFAULT '[]',
	route_revision  TEXT NOT NULL DEFAULT '',
	UNIQUE (dispatch_id, run_id)
);
INSERT INTO work_receipts_v14
	(receipt_id, dispatch_id, run_id, resource_id, status, failure_code, external_task_id, base_revision,
	 result_revision, changes_json, submitted_at, begun_at, validation_state, validation_reasons_json, route_revision)
SELECT receipt_id, dispatch_id, run_id, resource_id, status, failure_code, external_task_id, base_revision,
       result_revision, changes_json, submitted_at, begun_at, validation_state, validation_reasons_json,
       COALESCE(route_revision, '')
FROM work_receipts;
DROP TABLE work_receipts;
ALTER TABLE work_receipts_v14 RENAME TO work_receipts;
CREATE INDEX idx_work_receipts_route_revision ON work_receipts(route_revision);
`

// schemaV15MergeSelectionEvidence records each merged batch's
// destination-selection summary at merge time (E12 epic validation,
// FAN-005 occurrence-level semantics): a lane's follow-up filters its
// dirty generation by the merging OCCURRENCE's selection, not by
// re-evaluating the conditions per change — per-change evaluation
// silently dropped changes whose path alone failed a path_include while
// the occurrence as a whole selected the lane. The column is
// additive-only: legacy rows keep NULL (unknown selection) and the
// follow-up filter falls back to the per-change evaluation for them.
const schemaV15MergeSelectionEvidence = `
ALTER TABLE change_batches ADD COLUMN selected_destinations_json TEXT;
`

// schemaV16NotificationEventsAttempts creates the durable notification
// outbox of ADR-0019 (E13-T1, DUR-016, DAT-010, NTF-003/NTF-004):
// notification_events holds one channel-neutral intent per logical
// (event, optional destination, transition, sink, notification-policy
// revision) identity — the deterministic ntf- id derived from exactly
// that projection is the primary key, so a replayed or rerun transition
// collapses onto its existing row instead of notifying twice (AC-902);
// notification_attempts holds the independent delivery lifecycle as
// separate durable records whose outcomes never rewrite the intent's
// source state (NTF-005). Both tables are new: no legacy rows exist and
// no other table references them, so the migration is create-only.
const schemaV16NotificationEventsAttempts = `
CREATE TABLE notification_events (
	notification_id  TEXT PRIMARY KEY,
	route_id         TEXT NOT NULL,
	event            TEXT NOT NULL CHECK (event IN ('work_completed','work_failed','work_exhausted','delivery_unknown','quarantined','reconciliation_required','integration_drift','watchman_drift')),
	destination_id   TEXT,
	transition       TEXT NOT NULL,
	sink_id          TEXT NOT NULL,
	sink_type        TEXT NOT NULL CHECK (sink_type IN ('webhook','log')),
	policy_revision  TEXT NOT NULL,
	idempotency_key  TEXT NOT NULL UNIQUE,
	payload_json     TEXT NOT NULL,
	state            TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','delivered','refused')),
	created_at       TEXT NOT NULL,
	resolved_at      TEXT
);
CREATE INDEX idx_notification_events_route ON notification_events(route_id, created_at);
CREATE INDEX idx_notification_events_state ON notification_events(state, created_at);
CREATE TABLE notification_attempts (
	attempt_id      TEXT PRIMARY KEY,
	notification_id TEXT NOT NULL REFERENCES notification_events(notification_id),
	attempt_number  INTEGER NOT NULL,
	outcome         TEXT NOT NULL CHECK (outcome IN ('delivered','refused','ambiguous','retryable')),
	error_code      TEXT,
	response_digest TEXT,
	started_at      TEXT NOT NULL,
	completed_at    TEXT NOT NULL,
	UNIQUE (notification_id, attempt_number)
);
CREATE INDEX idx_notification_attempts_notification ON notification_attempts(notification_id, attempt_number);
`

// schemaV17RouteBaselines creates the disabled-route baseline record of
// ADR-0020 (E14-T2, DUR-017): one row per route holding the evidence of
// the latest baseline-only reconciliation — the observation revision the
// snapshot committed at, the bounded fact count and canonical snapshot
// digest, the route and policy revisions the evidence names, and the
// reason and timestamp of establishment. The row is written only inside
// the same observation-fenced transaction that stores the snapshot (see
// ReplacePathFactsWithBaseline), so a crash leaves either the previous
// baseline or the complete new one and a rerun converges by replacing
// the row. The table is create-only: no row exists before v0.1.6 and no
// other table references it. It deliberately carries no foreign key to
// routes or route_runtime_state — a clean host establishes its baseline
// before any route row exists.
const schemaV17RouteBaselines = `
CREATE TABLE route_baselines (
	route_id             TEXT PRIMARY KEY,
	resource_id          TEXT NOT NULL,
	observation_revision INTEGER NOT NULL,
	fact_count           INTEGER NOT NULL,
	snapshot_sha256      TEXT NOT NULL,
	route_revision       TEXT NOT NULL,
	policy_revision      TEXT NOT NULL,
	reason               TEXT NOT NULL,
	established_at       TEXT NOT NULL
);
CREATE INDEX idx_route_baselines_resource ON route_baselines(resource_id);
`

// schemaV18SerializationGroupState persists the Agent Dispatch
// serialization-group topology of ADR-0021 (E15-T1, CON-011 through
// CON-013): one row per materialized group holds the group's durable
// slot state, and one member row per (route, destination) records the
// effective group current configuration resolved for that lane. The
// migration is additive and configuration-independent — it creates no
// group and rewrites no historic row (DAT-012: every existing dispatch,
// lane, and receipt identity stays exactly as queryable as before).
// Group membership materializes from current configuration at the first
// topology reconciliation after the upgrade; if several preserved
// active children resolve to one group the group reports
// serialization_conflict (state CONFLICT, no holder), and new group
// acquisition, promotion, retry, and rerun are blocked until allowed
// existing-work exits leave at most one active child (E15-T3 enforces
// the acquisition gate against this state).
const schemaV18SerializationGroupState = `
CREATE TABLE serialization_groups (
	group_id              TEXT PRIMARY KEY,
	state                 TEXT NOT NULL CHECK (state IN ('OPEN','HELD','CONFLICT')),
	holder_route_id       TEXT,
	holder_destination_id TEXT,
	holder_dispatch_id    TEXT,
	conflict_json         TEXT NOT NULL DEFAULT '[]',
	materialized_at       TEXT NOT NULL,
	updated_at            TEXT NOT NULL,
	version               INTEGER NOT NULL DEFAULT 0 CHECK (version >= 0)
);
CREATE TABLE serialization_group_members (
	route_id       TEXT NOT NULL,
	destination_id TEXT NOT NULL,
	group_id       TEXT NOT NULL REFERENCES serialization_groups(group_id) ON DELETE CASCADE,
	PRIMARY KEY (route_id, destination_id)
);
CREATE INDEX idx_serialization_group_members_group ON serialization_group_members(group_id);
`

// schemaV19NotificationDrainLeases persists the drain-delivery state the
// v0.1.6 automatic notification draining requires (E16-T1, ADR-0022,
// NTF-011/NTF-012): due deadlines, fenced lease columns, and drain-run
// evidence. The migration is additive and rewrites no historic identity
// — every notification_id, idempotency_key, and attempt row stays
// exactly as queryable as before (the acceptance's "identities and
// attempts survive migration unchanged"). Existing pending notifications
// backfill due_at to their created_at, which is in the past, so migrated
// pending work is immediately due (NTF-015); resolved rows receive the
// same backfill harmlessly — due_at only selects pending work. The lease
// columns start unowned (empty owner, token 0, no expiry); lease_token
// is the fencing counter each new claim advances monotonically, so a
// stale owner that lost its claim can never record an outcome after
// another worker recovered the notification (E16-T2 enforces the fence).
// drain_runs holds one evidence row per bounded drain pass — its
// trigger, counts, and budget expiry — written only by drain code paths
// and never read by delivery-adjacent transactions.
const schemaV19NotificationDrainLeases = `
ALTER TABLE notification_events ADD COLUMN due_at TEXT NOT NULL DEFAULT '';
UPDATE notification_events SET due_at = created_at;
ALTER TABLE notification_events ADD COLUMN lease_owner TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_events ADD COLUMN lease_token INTEGER NOT NULL DEFAULT 0 CHECK (lease_token >= 0);
ALTER TABLE notification_events ADD COLUMN lease_expires_at TEXT;
CREATE INDEX idx_notification_events_due ON notification_events(state, due_at, created_at);
CREATE TABLE drain_runs (
	drain_id        TEXT PRIMARY KEY,
	route_id        TEXT NOT NULL,
	trigger         TEXT NOT NULL CHECK (trigger IN ('manual','after-command','scheduled')),
	mode            TEXT NOT NULL CHECK (mode IN ('manual','after-command','scheduled')),
	started_at      TEXT NOT NULL,
	completed_at    TEXT,
	claimed         INTEGER NOT NULL DEFAULT 0,
	delivered       INTEGER NOT NULL DEFAULT 0,
	refused         INTEGER NOT NULL DEFAULT 0,
	retry_scheduled INTEGER NOT NULL DEFAULT 0,
	budget_expired  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_drain_runs_route ON drain_runs(route_id, started_at);
`

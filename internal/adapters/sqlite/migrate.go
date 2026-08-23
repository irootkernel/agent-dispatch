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
}

// MaxSchemaVersion is the highest version this binary understands; a
// database at a newer version is refused rather than silently modified.
var MaxSchemaVersion = Migrations[len(Migrations)-1].Version

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
	for _, m := range list {
		sum, ok := applied[m.Version]
		if ok {
			if sum != m.checksum() {
				return fmt.Errorf("migration %d checksum mismatch: ledger %s, binary %s (migrations are immutable)", m.Version, sum, m.checksum())
			}
			continue
		}
		if err := s.Backup(fmt.Sprintf("%s/agent-dispatch-v%d-%s.backup", backupDir, m.Version, nowFileTimestamp())); err != nil {
			return fmt.Errorf("pre-migration backup: %w", err)
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
			// table rebuilds can outlive the old once-written mtime).
			stop := make(chan struct{})
			go func() {
				ticker := time.NewTicker(migrationLockRefresh)
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

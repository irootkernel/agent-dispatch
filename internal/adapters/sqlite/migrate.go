package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

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
		return fmt.Errorf("database schema version %d is newer than the supported maximum %d; upgrade jjukkumi instead of downgrading", newest, maxVersion)
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
		if err := s.Backup(fmt.Sprintf("%s/jjukkumi-v%d-%s.backup", backupDir, m.Version, nowFileTimestamp())); err != nil {
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

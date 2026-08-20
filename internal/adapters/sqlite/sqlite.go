// Package sqlite implements the durable local store (SCP-007): SQLite on
// a local filesystem with verified pragmas (OPS-008), a forward-only
// checksummed migration framework with pre-migration online backup
// (OPS-009), repositories for the canonical records (DAT-007, DAT-008),
// and the append-only state transition history (DUR-011). No external
// side-effect code ever runs inside a transaction (ADR-0005, DUR-001).
package sqlite

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
	"path/filepath"
	"runtime"
	"syscall"
)

// Open opens (creating if needed) the database at path and applies the
// required connection pragmas, verifying the resulting journal mode. It
// rejects placement on filesystems it can reliably identify as network
// filesystems (SCP-007); where detection is not reliable, placement on
// network storage remains explicitly unsupported per the roadmap
// acceptance.
func Open(path string) (*Store, error) {
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("state path: %w", err)
		}
		path = abs
	}
	if err := rejectNetworkPlacement(path); err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		// Create the parent with owner-only permissions so database and
		// configuration permissions stay owner-only by default (SEC-008).
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single-writer product: one connection keeps pragmas and locks
	// coherent without pool-wide reconfiguration.
	db.SetMaxOpenConns(1)
	// synchronous returns no result row; apply it and verify the others.
	if _, err := db.Exec("PRAGMA synchronous = FULL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma synchronous: %w", err)
	}
	for _, pragma := range []struct {
		stmt string
		want string
	}{
		{"PRAGMA journal_mode = WAL", "wal"},
	} {
		var got string
		if err := db.QueryRow(pragma.stmt).Scan(&got); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %q: %w", pragma.stmt, err)
		}
		if got != pragma.want {
			db.Close()
			return nil, fmt.Errorf("pragma %q yielded %q, want %q (unsupported storage?)", pragma.stmt, got, pragma.want)
		}
	}
	// foreign_keys and busy_timeout return no result rows through this
	// driver; set them and verify through their read forms.
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma foreign_keys: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma busy_timeout: %w", err)
	}
	for _, pragma := range []struct {
		stmt string
		want string
	}{
		{"PRAGMA foreign_keys", "1"},
		{"PRAGMA busy_timeout", "5000"},
	} {
		var got string
		if err := db.QueryRow(pragma.stmt).Scan(&got); err != nil {
			db.Close()
			return nil, fmt.Errorf("verify %q: %w", pragma.stmt, err)
		}
		if got != pragma.want {
			db.Close()
			return nil, fmt.Errorf("%q yielded %q, want %q", pragma.stmt, got, pragma.want)
		}
	}
	return &Store{DB: db, path: path}, nil
}

// Store wraps the database handle with the repositories. The migrations
// field overrides the package list for tests simulating future histories.
type Store struct {
	*sql.DB
	path       string
	migrations []Migration
}

// Path is the absolute database file path.
func (s *Store) Path() string { return s.path }

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

// rejectNetworkPlacement fails closed where the underlying filesystem can
// be reliably identified as non-local. On macOS the mount table reports
// MNT_LOCAL; on Linux known network filesystem magic numbers are checked.
// Other placements cannot be reliably classified and remain covered by
// the explicit unsupported-placement policy (journal mode verification,
// doctor quick_check). statfs is injectable for tests.
func rejectNetworkPlacement(path string) error {
	return rejectNetworkPlacementStatfs(path, syscall.Statfs)
}

func rejectNetworkPlacementStatfs(path string, statfs func(string, *syscall.Statfs_t) error) error {
	var st syscall.Statfs_t
	if err := statfs(filepath.Dir(path), &st); err != nil {
		// An unstatable directory will fail at open anyway; report it here
		// with context.
		return fmt.Errorf("stat state directory: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		const mntLocal = 0x00001000
		if st.Flags&mntLocal == 0 {
			return fmt.Errorf("%s is on a network filesystem; durable local state requires a local filesystem (SCP-007)", path)
		}
	case "linux":
		network := map[int64]bool{
			0x00006969: true, // NFS
			0x517B:     true, // SMB
			0xFF534D42: true, // CIFS
			0x73757245: true, // FUSE-based network mounts may report this
		}
		if network[int64(uint64(st.Type))] {
			return fmt.Errorf("%s is on a network filesystem (type %#x); durable local state requires a local filesystem (SCP-007)", path, st.Type)
		}
	}
	return nil
}

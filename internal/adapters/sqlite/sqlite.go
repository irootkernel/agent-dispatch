// Package sqlite implements the durable local store (SCP-007): SQLite on
// a local filesystem with verified pragmas (OPS-008), a forward-only
// checksummed migration framework with pre-migration online backup
// (OPS-009), repositories for the canonical records (DAT-007, DAT-008),
// and the append-only state transition history (DUR-011). No external
// side-effect code ever runs inside a transaction (ADR-0005, DUR-001).
// Since E13-T1 the store also owns the durable notification outbox of
// ADR-0019: reportable transitions enqueue notification intents inside
// their own transactions through the injectable per-route policy
// resolver, and delivery attempts land as separate records that never
// rewrite the transitioned state (NTF-005).
package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/ports"

	"modernc.org/sqlite"
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
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		// Create the parent with owner-only permissions so database and
		// configuration permissions stay owner-only by default (SEC-008);
		// create it before the placement check so a missing directory is
		// not misreported as a placement failure.
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	if err := rejectNetworkPlacement(path); err != nil {
		return nil, err
	}
	// The database file is owner-only from creation (SEC-008, E8-T4/
	// M-19): a fresh SQLite create honors the process umask otherwise,
	// which shipped 0644 databases. Best-effort for existing files —
	// the doctor surfaces a persistent wrong mode.
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		if f, ferr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600); ferr == nil {
			f.Close()
		}
	}
	// All connection-scoped pragmas ride the DSN so a replacement pooled
	// connection re-applies them (L-16, E9-T1); the pragma verification
	// block below still proves each took effect.
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single-writer product: one connection keeps pragmas and locks
	// coherent without pool-wide reconfiguration.
	db.SetMaxOpenConns(1)
	// A concurrent first open can hit SQLITE_BUSY switching the journal
	// mode: that is transient initialization congestion (OPS-008,
	// E7-T7/M-4/M-5), so the switch retries inside a bounded window
	// before falling back to the retryable busy classification.
	var walErr error
	for attempt := 0; attempt < 20; attempt++ {
		var got string
		walErr = db.QueryRow("PRAGMA journal_mode = WAL").Scan(&got)
		if walErr == nil {
			if got != "wal" {
				db.Close()
				return nil, fmt.Errorf("pragma journal_mode yielded %q, want wal (unsupported storage?)", got)
			}
			walErr = nil
			break
		}
		if !isBusy(walErr) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if walErr != nil {
		db.Close()
		if isBusy(walErr) {
			return nil, &ports.StoreError{Err: fmt.Errorf("database is initializing concurrently (WAL switch busy): %w", walErr)}
		}
		return nil, fmt.Errorf("pragma journal_mode: %w", walErr)
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
		{"PRAGMA synchronous", "2"}, // FULL
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
	// notificationPolicy resolves one route's effective notification
	// policy inside transition transactions (E13-T1, ADR-0019); nil
	// disables notification creation (NTF-001). Installed by the CLI
	// from the loaded configuration; never mutated concurrently because
	// a store is opened, used, and closed by one command run.
	notificationPolicy func(routeID string) *ports.NotificationPolicy
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

// isBusy reports whether the driver error is SQLITE_BUSY congestion.
func isBusy(err error) bool {
	var derr *sqlite.Error
	if errors.As(err, &derr) {
		return derr.Code() == 5 || derr.Code() == 261
	}
	return false
}

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// OpenReadOnly never creates state, changes journal mode or runs migrations.
// mode=ro preserves visibility of committed WAL records; immutable=1 would not.
func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("state is not a regular database file")
	}
	if err := rejectNetworkPlacement(path); err != nil {
		return nil, err
	}
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	db, err := sql.Open("sqlite", uri+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(0)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db, path: path}, nil
}

// CheckManagementSchema verifies the existing immutable migration history
// without creating a ledger or applying pending migrations.
func (s *Store) CheckManagementSchema(ctx context.Context) error {
	applied, err := s.appliedMigrationsContext(ctx)
	if err != nil {
		return err
	}
	maxVersion := 0
	for version := range applied {
		maxVersion = max(maxVersion, version)
	}
	supported := Migrations[len(Migrations)-1].Version
	if maxVersion > supported {
		return &ErrSchemaTooNewType{Detail: "state schema is newer than this binary"}
	}
	if maxVersion < 28 {
		return fmt.Errorf("management schema is unavailable")
	}
	for _, m := range Migrations {
		if m.Version <= maxVersion && applied[m.Version] != m.checksum() {
			return fmt.Errorf("migration ledger does not match immutable history")
		}
	}
	return nil
}

package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// TestReleaseUpgradeFromV018 preserves operator state across the exact
// schema-20 baseline and exercises the new sync inbox after migration.
func TestReleaseUpgradeFromV018(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.migrations = Migrations[:20]
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if version, err := s.SchemaVersion(); err != nil || version != 20 {
		t.Fatalf("release baseline yielded schema %d: %v", version, err)
	}
	if err := s.RegisterResource(nil, "vault-release", "revision-old", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki-release", "revision-old", "policy-old", "vault-release", "target-old", "{}", syncT0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO schedule_at_overrides(label, at) VALUES ('agent-dispatch.release', '04:15')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if version, err := s.SchemaVersion(); err != nil || version != 27 {
		t.Fatalf("schema-20 upgrade yielded %d: %v", version, err)
	}
	var revision, root, at string
	if err := s.QueryRow(`SELECT revision FROM routes WHERE route_id='wiki-release'`).Scan(&revision); err != nil || revision != "revision-old" {
		t.Fatalf("route preservation: %q %v", revision, err)
	}
	if err := s.QueryRow(`SELECT root FROM resources WHERE resource_id='vault-release'`).Scan(&root); err != nil || root != "/srv/vault" {
		t.Fatalf("resource preservation: %q %v", root, err)
	}
	if err := s.QueryRow(`SELECT at FROM schedule_at_overrides WHERE label='agent-dispatch.release'`).Scan(&at); err != nil || at != "04:15" {
		t.Fatalf("schedule preservation: %q %v", at, err)
	}
	if duplicate, err := s.AdmitPeerNudge(context.Background(), peerNudge("release-upgrade")); err != nil || duplicate {
		t.Fatalf("upgraded sync inbox: duplicate=%v err=%v", duplicate, err)
	}
	var integrity string
	if err := s.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("upgraded integrity: %q %v", integrity, err)
	}
}

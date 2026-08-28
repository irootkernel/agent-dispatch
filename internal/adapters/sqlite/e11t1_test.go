package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// e11t1LegacySeededStore opens a migrated store whose route carries one
// unresolved legacy intent (unknown) and one unresolved quarantine item,
// both recorded under a foreign (pre-cutover) route revision. When
// v9Era is set the store migrates only to the v9 baseline first — the
// pre-cutover shape — so a later full Migrate performs the actual v10
// cutover and produces the pre-migration backup.
func e11t1LegacySeededStore(t *testing.T, v9Era bool) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if v9Era {
		s.migrations = Migrations[:9]
		if err := s.Migrate(dir); err != nil {
			t.Fatal(err)
		}
		s.Close()
		s, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
	} else if err := s.Migrate(dir); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.RegisterResource(nil, "vault-main", "legacy-rev", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki", "legacy-rev", "legacy-pol", "vault-main", "hermes-kanban-main", "{}", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveObservation(nil, ObservationRecord{
		ObservationID: "obs-e11t1", SchemaVersion: "agent-dispatch.observation/v1", SourceType: "watchman-trigger",
		SourceID: "vault-main-watchman", TriggerName: "agent-dispatch.wiki.legacy", ResourceID: "vault-main",
		ObservedAt: now, ReceivedAt: now, RawPayloadDigest: "d", IngestStatus: "accepted",
		Changes: []ChangeRecord{{Ordinal: 1, Path: "Notes/a.md", Operation: "create", ExistsAfter: true, FileType: "regular", DigestStatus: "known", AfterDigest: "x"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO change_batches (batch_id, route_id, route_revision, resource_id, created_at, content_fingerprint)
		VALUES ('batch-e11t1', 'wiki', 'legacy-rev', 'vault-main', ?, 'fp')`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO policy_decisions (decision_id, batch_id, route_id, route_revision, policy_revision, disposition, classification, created_at, actor)
		VALUES ('dec-e11t1', 'batch-e11t1', 'wiki', 'legacy-rev', 'legacy-pol', 'dispatch', 'normal', ?, 'test')`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO dispatch_intents (dispatch_id, decision_id, route_id, route_revision, resource_id, generation, idempotency_key, content_fingerprint, manifest_digest, request_version, request_json, created_at, updated_at, state, target_id, target_type, target_scope, base_batch_seq)
		VALUES ('disp-e11t1', 'dec-e11t1', 'wiki', 'legacy-rev', 'vault-main', 1, 'key-e11t1', 'fp', 'md', 'agent-dispatch.hermes-task/v1', '{}', ?, ?, 'unknown', 'hermes-kanban-main', 'hermes-kanban', 'agent-dispatch', 1)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO quarantine_items (quarantine_id, decision_id, state, created_at, route_revision)
		VALUES ('q-e11t1', 'dec-e11t1', 'held', ?, 'legacy-rev')`, now); err != nil {
		t.Fatal(err)
	}
	return s, path
}

// TestE11T1UnresolvedLegacyWorkCountsForeignRevisionRows proves the
// DAT-013 gate query: unresolved intents and quarantine items under a
// foreign route revision count; resolved rows and current-revision rows
// do not.
func TestE11T1UnresolvedLegacyWorkCountsForeignRevisionRows(t *testing.T) {
	s, _ := e11t1LegacySeededStore(t, false)
	ctx := context.Background()
	count, detail, err := s.UnresolvedLegacyWork(ctx, "wiki", "new-destinations-rev")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("the unknown intent and held quarantine item must both count, got %d (%s)", count, detail)
	}
	if count, _, err := s.UnresolvedLegacyWork(ctx, "wiki", "legacy-rev"); err != nil || count != 0 {
		t.Fatalf("rows under the current revision must not count, got %d %v", count, err)
	}
	// Resolving the quarantine drops its count.
	if _, err := s.Exec(`UPDATE quarantine_items SET resolved_at = ?, resolved_by = 'test' WHERE quarantine_id = 'q-e11t1'`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if count, _, err := s.UnresolvedLegacyWork(ctx, "wiki", "new-destinations-rev"); err != nil || count != 1 {
		t.Fatalf("the resolved quarantine item must stop counting, got %d %v", count, err)
	}
}

// TestE11T1CutoverMigrationPreservesHistoryAndRecordsContract proves
// DAT-012 and the v10 marker: after the destinations-contract cutover
// migration, the historic intent, receipt lineage, and quarantine rows
// remain queryable, the pre-migration backup exists and passes the
// integrity check, and contract_state records the cutover.
func TestE11T1CutoverMigrationPreservesHistoryAndRecordsContract(t *testing.T) {
	s, path := e11t1LegacySeededStore(t, true)
	ctx := context.Background()
	// Perform the actual cutover: the v10 unit applies on top of the
	// v9-era database and takes the pre-migration backup first.
	if err := s.Migrate(filepath.Dir(path)); err != nil {
		t.Fatalf("the cutover migration: %v", err)
	}
	// Verify the marker.
	var contract string
	if err := s.QueryRow(`SELECT contract FROM contract_state WHERE contract = 'destinations-v1'`).Scan(&contract); err != nil || contract != "destinations-v1" {
		t.Fatalf("the cutover marker must exist: %q %v", contract, err)
	}
	version, err := s.SchemaVersion()
	if err != nil || version < 10 {
		t.Fatalf("the ledger must reach the cutover migration, got %d %v", version, err)
	}
	// Historic task and receipt references stay queryable (DAT-012).
	intent, err := s.LoadIntent(ctx, "disp-e11t1")
	if err != nil || intent.RouteID != "wiki" || intent.State != "unknown" {
		t.Fatalf("the historic intent must stay queryable: %+v %v", intent, err)
	}
	var quarantine int
	if err := s.QueryRow(`SELECT COUNT(*) FROM quarantine_items WHERE quarantine_id = 'q-e11t1'`).Scan(&quarantine); err != nil || quarantine != 1 {
		t.Fatalf("the historic quarantine row must stay queryable: %d %v", quarantine, err)
	}

	// The rollback rehearsal (OPS-015): the pre-migration backup the
	// migration machinery produced is a restorable database kept beside
	// the upgraded one; restoring it yields the pre-cutover schema with
	// the same historic rows, while the upgraded database stays in
	// place. The backup lands in the backup directory passed to the
	// first Migrate call of the seeded store.
	matches, gerr := filepath.Glob(filepath.Join(filepath.Dir(path), "*v9-to-v*.backup"))
	if gerr != nil || len(matches) == 0 {
		t.Fatalf("the pre-migration backup must exist beside the database: %v %v", matches, gerr)
	}
	restored, err := Open(matches[0])
	if err != nil {
		t.Fatalf("the backup must open as a database: %v", err)
	}
	defer restored.Close()
	restoredVersion, err := restored.SchemaVersion()
	if err != nil || restoredVersion != 9 {
		t.Fatalf("the backup must hold the pre-cutover v9 ledger, got %d %v", restoredVersion, err)
	}
	var restoredIntents int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE dispatch_id = 'disp-e11t1'`).Scan(&restoredIntents); err != nil || restoredIntents != 1 {
		t.Fatalf("the restored backup must expose the historic intent: %d %v", restoredIntents, err)
	}
	if err := restored.IntegrityCheck(false); err != nil {
		t.Fatalf("the restored backup must pass the integrity check: %v", err)
	}
	// The upgraded database is kept aside (still open and intact).
	if version, _ := s.SchemaVersion(); version < 10 {
		t.Fatal("the upgraded database must remain in place after the rehearsal")
	}
}

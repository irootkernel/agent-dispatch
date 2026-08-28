package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestE9ValidationMigrationV7BackfillsAndPropagates pins the E9-T1 audit
// F004: the v7 migration's real SQL backfills the four record tables'
// route_revision from the creating intent's revision, and the migration
// ledger accepts the re-application over a v6-era shape.
func TestE9ValidationMigrationV7BackfillsAndPropagates(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")
	// One record of each of the four v7-covered kinds over the intent.
	if _, err := s.Exec(`INSERT INTO dispatch_attempts (attempt_id, dispatch_id, lease_owner, started_at, completed_at, outcome)
		VALUES ('a1', 'dispatch-1', 'op', '2026-08-20T00:00:00Z', '2026-08-20T00:00:30Z', 'accepted')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO dispatch_receipts (receipt_id, dispatch_id, receipt_kind, acceptance_state, received_at)
		VALUES ('r1', 'dispatch-1', 'acceptance', 'accepted', '2026-08-20T00:00:31Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO work_receipts (receipt_id, dispatch_id, run_id, resource_id, status, submitted_at, validation_state)
		VALUES ('w1', 'dispatch-1', 'run-1', 'vault-main', 'begun', '2026-08-20T00:01:00Z', 'valid')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO quarantine_items (quarantine_id, decision_id, reason_codes_json, state, created_at)
		VALUES ('q1', 'decision-1', '[]', 'held', '2026-08-20T00:02:00Z')`); err != nil {
		t.Fatal(err)
	}

	// Rewind to the v6-era shape: the v7 columns and indexes are gone and
	// the ledger no longer records v7, exactly as a pre-upgrade database
	// would look.
	for _, stmt := range []string{
		`DROP INDEX idx_attempts_route_revision`,
		`DROP INDEX idx_receipts_route_revision`,
		`DROP INDEX idx_work_receipts_route_revision`,
		`DROP INDEX idx_quarantine_route_revision`,
		`ALTER TABLE dispatch_attempts DROP COLUMN route_revision`,
		`ALTER TABLE dispatch_receipts DROP COLUMN route_revision`,
		`ALTER TABLE work_receipts DROP COLUMN route_revision`,
		`ALTER TABLE quarantine_items DROP COLUMN route_revision`,
		`ALTER TABLE resources DROP COLUMN observation_revision`,
		`DROP TABLE watch_bindings`,
		`DROP TABLE contract_state`,
		`ALTER TABLE route_runtime_state DROP COLUMN capability_fingerprint`,
		`DELETE FROM schema_migrations WHERE version IN (7, 8, 9, 10, 11)`,
	} {
		if _, err := s.Exec(stmt); err != nil {
			t.Fatalf("rewind %q: %v", stmt, err)
		}
	}

	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatalf("re-applying migration v7 over the v6-era shape: %v", err)
	}
	var version int
	if err := s.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != MaxSchemaVersion {
		t.Fatalf("the ledger must record the current baseline as newest: %d %v", version, err)
	}
	for _, table := range []string{"dispatch_attempts", "dispatch_receipts", "work_receipts", "quarantine_items"} {
		var rev string
		if err := s.QueryRow(`SELECT route_revision FROM ` + table + ` WHERE rowid = 1`).Scan(&rev); err != nil || rev != "route-rev-1" {
			t.Fatalf("%s: the backfill must carry the creating intent's revision, got %q %v", table, rev, err)
		}
	}
}

// TestE9ValidationWireShapesStaySnakeCase pins the E9-T1 audit F010: the
// prune cutoffs and the watchman envelope serialize snake_case — the
// published wire shapes, not Go field names.
func TestE9ValidationWireShapesStaySnakeCase(t *testing.T) {
	raw, err := json.Marshal(PruneCutoffs{
		Observations:       "168h",
		Attempts:           "168h",
		CompletedReceipts:  "168h",
		ResolvedQuarantine: "168h",
	})
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, key := range []string{`"observations"`, `"attempts"`, `"completed_receipts"`, `"resolved_quarantine"`} {
		if !strings.Contains(doc, key) {
			t.Fatalf("the prune cutoffs wire must stay snake_case, got %s", doc)
		}
	}
	for _, camel := range []string{"completedReceipts", "resolvedQuarantine"} {
		if strings.Contains(doc, camel) {
			t.Fatalf("a camelCase key leaked into the prune wire: %s", doc)
		}
	}
}

// TestE9ValidationPruneUnwedgesTerminalBegunReceipt pins the epic
// round-1 F001 fix: a begun work receipt on a TERMINAL dispatch past
// retention prunes with the lineage — previously its un-cascaded
// foreign key blocked the intent delete and wedged the whole prune —
// while an active slot's begun receipt still never prunes.
func TestE9ValidationPruneUnwedgesTerminalBegunReceipt(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")
	if _, err := s.Exec(`UPDATE dispatch_intents SET state = 'superseded', updated_at = '2020-01-01T00:00:00Z' WHERE dispatch_id = 'dispatch-1'`); err != nil {
		t.Fatal(err)
	}
	// A superseded dispatch holds no active slot.
	if _, err := s.Exec(`UPDATE route_runtime_state SET active_dispatch_id = NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO work_receipts (receipt_id, dispatch_id, run_id, resource_id, status, submitted_at, validation_state)
		VALUES ('w1', 'dispatch-1', 'run-1', 'vault-main', 'begun', '2020-01-01T00:00:00Z', 'valid')`); err != nil {
		t.Fatal(err)
	}

	// Before the fix this transaction wedged on the foreign key.
	counts, err := s.ExecutePrune(context.Background(), PruneCutoffs{
		Observations: "2026-08-23T00:00:00Z", Attempts: "2026-08-23T00:00:00Z",
		CompletedReceipts: "2026-08-23T00:00:00Z", ResolvedQuarantine: "2026-08-23T00:00:00Z",
	}, "operator", "retention", "2026-08-24T00:00:00Z")
	if err != nil {
		t.Fatalf("the prune must not wedge on a terminal dispatch's begun receipt: %v", err)
	}
	var intents, receipts int64
	if err := s.QueryRow(`SELECT (SELECT COUNT(*) FROM dispatch_intents WHERE dispatch_id = 'dispatch-1'), (SELECT COUNT(*) FROM work_receipts WHERE receipt_id = 'w1')`).Scan(&intents, &receipts); err != nil {
		t.Fatal(err)
	}
	if intents != 0 || receipts != 0 {
		t.Fatalf("the terminal lineage and its begun receipt must prune together: intents=%d receipts=%d counts=%+v", intents, receipts, counts)
	}

	// The other half of the contract: a begun receipt whose dispatch
	// HOLDS the active slot never prunes, whatever its age (E7-T9's
	// e7t9 test pins the accepted case end to end; this pins the slot
	// guard itself by making the dispatch terminal so ONLY the guard
	// can protect it).
	seedIntentChain(t, s, "dispatch-live")
	if _, err := s.Exec(`UPDATE dispatch_intents SET state = 'superseded', updated_at = '2020-01-01T00:00:00Z' WHERE dispatch_id = 'dispatch-live'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO work_receipts (receipt_id, dispatch_id, run_id, resource_id, status, submitted_at, validation_state)
		VALUES ('w2', 'dispatch-live', 'run-1', 'vault-main', 'begun', '2020-01-01T00:00:00Z', 'valid')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE route_runtime_state SET active_dispatch_id = 'dispatch-live'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecutePrune(context.Background(), PruneCutoffs{
		Observations: "2026-08-23T00:00:00Z", Attempts: "2026-08-23T00:00:00Z",
		CompletedReceipts: "2026-08-23T00:00:00Z", ResolvedQuarantine: "2026-08-23T00:00:00Z",
	}, "operator", "retention", "2026-08-24T00:00:00Z"); err != nil {
		t.Fatalf("prune over the active slot: %v", err)
	}
	var survived int
	if err := s.QueryRow(`SELECT COUNT(*) FROM work_receipts WHERE receipt_id = 'w2'`).Scan(&survived); err != nil || survived != 1 {
		t.Fatalf("an active slot's begun receipt must survive: %d %v", survived, err)
	}
}

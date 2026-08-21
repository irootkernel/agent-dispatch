package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/domain/state"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// openTestStore opens a real SQLite file under t.TempDir(), migrates it,
// and seeds one resource and route so record repositories can be
// exercised (TST-002: real files, not mocks).
func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := s.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki-maintenance", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", now()); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki-maintenance"); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFreshAndMigratedSchemasIdentical(t *testing.T) {
	dir := t.TempDir()
	fresh, err := Open(filepath.Join(dir, "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err := fresh.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// A database that goes through migration in two steps (ledger
	// created, then migration applied on reopen) reaches the same schema.
	stepwise, err := Open(filepath.Join(dir, "stepwise.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer stepwise.Close()
	if err := stepwise.ensureMigrationLedger(); err != nil {
		t.Fatal(err)
	}
	if err := stepwise.Close(); err != nil {
		t.Fatal(err)
	}
	stepwise, err = Open(filepath.Join(dir, "stepwise.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer stepwise.Close()
	if err := stepwise.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	a := schemaObjects(t, fresh)
	b := schemaObjects(t, stepwise)
	if a != b {
		t.Fatalf("fresh and stepwise-migrated schemas differ:\n%s\n---\n%s", a, b)
	}
}

func schemaObjects(t *testing.T, s *Store) string {
	t.Helper()
	rows, err := s.Query(`SELECT type, name, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var typ, name, ddl string
		if err := rows.Scan(&typ, &name, &ddl); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s %s %s\n", typ, name, ddl)
	}
	return b.String()
}

func TestNewerSchemaRefused(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Exec(`INSERT INTO schema_migrations (version, name, checksum) VALUES (?, 'future', 'sha256:x')`, MaxSchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	err := s.Migrate(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("newer schema must be refused: %v", err)
	}
}

func TestMigrationChecksumImmutability(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Exec(`UPDATE schema_migrations SET checksum = 'sha256:tampered' WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	err := s.Migrate(t.TempDir())
	if err == nil || !(strings.Contains(err.Error(), "checksum mismatch") || strings.Contains(err.Error(), "does not match")) {
		t.Fatalf("tampered ledger checksum must fail: %v", err)
	}
}

func TestBackupBeforeMigration(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	backupDir := filepath.Join(dir, "backups")
	if err := s.Migrate(backupDir); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(backupDir, "jjukkumi-v1-*.backup"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one pre-migration backup, got %v (%v)", matches, err)
	}
}

func TestForeignKeyEnforcement(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Exec(`INSERT INTO source_observations (observation_id, schema_version, source_type, source_id, trigger_name, resource_id, observed_at, received_at, raw_payload_digest, ingest_status)
		VALUES ('obs-1','v1','watchman','src-1','trig','nope','t','t','sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855','ok')`); err == nil {
		t.Fatal("FK violation must fail (foreign_keys pragma enforced)")
	}
}

func TestUniqueConstraints(t *testing.T) {
	s := openTestStore(t)
	o := ObservationRecord{
		ObservationID: "0192e6c6-4d7f-7abc-8def-012345678901", SchemaVersion: "jjukkumi.source-observation/v1",
		SourceType: "watchman", SourceID: "src-1", TriggerName: "trig", ResourceID: "vault-main",
		ObservedAt: "2026-08-20T00:00:00Z", ReceivedAt: "2026-08-20T00:00:00Z",
		RawPayloadDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", IngestStatus: "ok",
	}
	if err := s.SaveObservation(nil, o); err != nil {
		t.Fatalf("save observation: %v", err)
	}
	if err := s.SaveObservation(nil, o); err == nil {
		t.Fatal("duplicate observation ID must violate uniqueness")
	}
}

func TestIntentReservationAndLease(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")

	// A second intent for the same route cannot reserve the active slot.
	second := intentRecord("dispatch-2", "decision-2")
	if err := s.SaveIntent(nil, second); err == nil {
		t.Fatal("route with an active dispatch must refuse a second intent (invariant 5)")
	}

	// Lease acquisition is a conditional write.
	if err := s.AcquireLease(nil, "dispatch-1", "owner-a", "2026-08-20T00:01:00Z", "", "2026-08-20T00:00:30Z"); err != nil {
		t.Fatalf("lease: %v", err)
	}
	// Re-acquiring while in submitting state must fail.
	if err := s.AcquireLease(nil, "dispatch-1", "owner-b", "2026-08-20T00:02:00Z", "", "2026-08-20T00:01:30Z"); err == nil {
		t.Fatal("lease re-acquisition must be conditional")
	}
}

func TestStateTransitionsAppendOnlyAndValidated(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")
	if err := s.TransitionIntent(nil, "tr-1", "dispatch-1", "ready", "submitting", now(), "{}"); err != nil {
		t.Fatalf("ready->submitting: %v", err)
	}
	if err := s.TransitionIntent(nil, "tr-2", "dispatch-1", "ready", "submitting", now(), "{}"); err == nil {
		t.Fatal("stale from-state must fail (invariant 3)")
	}
	if err := s.TransitionIntent(nil, "tr-3", "dispatch-1", "submitting", "completed", now(), "{}"); err == nil {
		t.Fatal("submitting->completed is not a legal transition")
	}
	if _, err := s.Exec(`UPDATE state_transitions SET to_state = 'tampered'`); err == nil {
		t.Fatal("audit history must be append-only")
	}
	if _, err := s.Exec(`DELETE FROM state_transitions`); err == nil {
		t.Fatal("audit history must reject deletes")
	}
}

// TestDomainStateEnumsMatchSchemaChecks proves the domain state sets and
// the schema CHECK constraints accept exactly the same values (E3-T1
// acceptance: all state enums match contracts and schemas).
func TestDomainStateEnumsMatchSchemaChecks(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")
	for _, st := range state.AllIntentStates() {
		if _, err := s.Exec(`UPDATE dispatch_intents SET state = ? WHERE dispatch_id = 'dispatch-1'`, string(st)); err != nil {
			t.Errorf("intent state %q rejected by the schema CHECK: %v", st, err)
		}
	}
	for _, st := range state.AllRouteStates() {
		if _, err := s.Exec(`UPDATE route_runtime_state SET route_state = ? WHERE route_id = 'wiki-maintenance'`, string(st)); err != nil {
			t.Errorf("route state %q rejected by the schema CHECK: %v", st, err)
		}
	}
}

// TestInvalidTransitionNoPartialPersistence verifies a rejected
// transition leaves neither the intent row nor the audit history changed
// (E3-T1 acceptance: invalid transitions fail without partial
// persistence).
func TestInvalidTransitionNoPartialPersistence(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")
	if err := s.TransitionIntent(nil, "tr-bad", "dispatch-1", "ready", "completed", now(), "{}"); err == nil {
		t.Fatal("ready -> completed must be rejected by the domain table")
	}
	var current string
	if err := s.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = 'dispatch-1'`).Scan(&current); err != nil || current != "ready" {
		t.Fatalf("rejected transition must not change the intent state: %q %v", current, err)
	}
	var transitions int
	if err := s.QueryRow(`SELECT COUNT(*) FROM state_transitions WHERE entity_id = 'dispatch-1'`).Scan(&transitions); err != nil || transitions != 0 {
		t.Fatalf("rejected transition must not append audit history: %d %v", transitions, err)
	}
	if err := s.TransitionIntent(nil, "tr-ok", "dispatch-1", "ready", "submitting", now(), "{}"); err != nil {
		t.Fatalf("valid transition after a rejected one: %v", err)
	}
}

func TestRouteRuntimeStateOptimisticConcurrency(t *testing.T) {
	s := openTestStore(t)
	rec, err := s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Version != 0 || rec.RouteState != "IDLE" || rec.ActivationState != "disabled" {
		t.Fatalf("unexpected initial state: %+v", rec)
	}
	stale := rec.Version
	if err := s.UpdateRouteRuntimeState(nil, "wiki-maintenance", stale, func(r *RouteRuntimeStateRecord) {
		r.RouteState = "ACTIVE_CLEAN"
	}); err != nil {
		t.Fatalf("first update: %v", err)
	}
	if err := s.UpdateRouteRuntimeState(nil, "wiki-maintenance", stale, func(r *RouteRuntimeStateRecord) {
		r.RouteState = "QUARANTINED"
	}); err == nil {
		t.Fatal("stale version must fail optimistically")
	}
	rec, err = s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil || rec.Version != 1 || rec.RouteState != "ACTIVE_CLEAN" {
		t.Fatalf("unexpected updated state: %+v %v", rec, err)
	}
}

func TestWorkReceiptStored(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")
	w := WorkReceiptRecord{
		ReceiptID: "wr-1", DispatchID: "dispatch-1", RunID: "run-1", ResourceID: "vault-main",
		Status: "completed", ChangesJSON: `[{"path":"a.md"}]`, SubmittedAt: now(), ValidationState: "valid",
	}
	if err := s.SaveWorkReceipt(nil, w); err != nil {
		t.Fatalf("save work receipt: %v", err)
	}
	if err := s.SaveWorkReceipt(nil, w); err == nil {
		t.Fatal("duplicate work receipt must violate uniqueness")
	}
}

func TestIntegrityCheckAndSchemaVersion(t *testing.T) {
	s := openTestStore(t)
	if err := s.IntegrityCheck(false); err != nil {
		t.Fatalf("quick_check: %v", err)
	}
	if err := s.IntegrityCheck(true); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if v, err := s.SchemaVersion(); err != nil || v != MaxSchemaVersion {
		t.Fatalf("schema version = %d, %v", v, err)
	}
}

func TestDecisionRequiresExactlyOneLineage(t *testing.T) {
	s := openTestStore(t)
	base := DecisionRecord{DecisionID: "decision-bad", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
		PolicyRevision: "policy-rev-1", Disposition: "dispatch", Classification: "normal", CreatedAt: now(), Actor: "system"}
	if err := s.SaveDecision(nil, base); err == nil {
		t.Fatal("decision with neither batch nor generation lineage must fail")
	}
	withBatch := base
	withBatch.DecisionID = "decision-b1"
	seedObservation(t, s)
	if err := s.SaveBatch(nil, "batch-1", "wiki-maintenance", "route-rev-1", "vault-main", now(), "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", []string{"0192e6c6-4d7f-7abc-8def-012345678901"}); err != nil {
		t.Fatalf("save batch: %v", err)
	}
	withBatch.BatchID = "batch-1"
	if err := s.SaveDecision(nil, withBatch); err != nil {
		t.Fatalf("batch decision: %v", err)
	}
	both := withBatch
	both.DecisionID = "decision-b2"
	both.GenerationLineageJSON = `{"route_id":"wiki-maintenance","generation":1}`
	if err := s.SaveDecision(nil, both); err == nil {
		t.Fatal("decision with both lineages must fail")
	}
}

func TestTransactionsRollbackAtomically(t *testing.T) {
	s := openTestStore(t)
	tx, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	o := ObservationRecord{
		ObservationID: "0192e6c6-4d7f-7abc-8def-012345678902", SchemaVersion: "jjukkumi.source-observation/v1",
		SourceType: "watchman", SourceID: "src-1", TriggerName: "trig", ResourceID: "vault-main",
		ObservedAt: now(), ReceivedAt: now(),
		RawPayloadDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", IngestStatus: "ok",
		Changes: []ChangeRecord{{Ordinal: 0, Path: "bad path", Operation: "create", ExistsAfter: true, FileType: "regular", DigestStatus: "known"}},
	}
	if err := s.SaveObservation(tx, o); err != nil {
		t.Fatalf("insert in tx: %v", err)
	}
	// Break the transaction; everything rolls back together.
	if _, err := tx.Exec(`INSERT INTO nonexistent_table VALUES (1)`); err == nil {
		t.Fatal("expected failure")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM source_observations WHERE observation_id = ?`, o.ObservationID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("rolled-back observation must not persist")
	}
}

func TestOpenRejectsDirectoryOverFile(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "state.db")
	if err := writeDir(badPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(badPath); err == nil {
		t.Fatal("directory in place of database must fail")
	}
}

func seedObservation(t *testing.T, s *Store) {
	t.Helper()
	o := ObservationRecord{
		ObservationID: "0192e6c6-4d7f-7abc-8def-012345678901", SchemaVersion: "jjukkumi.source-observation/v1",
		SourceType: "watchman", SourceID: "src-1", TriggerName: "trig", ResourceID: "vault-main",
		ObservedAt: now(), ReceivedAt: now(),
		RawPayloadDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", IngestStatus: "ok",
	}
	if err := s.SaveObservation(nil, o); err != nil {
		t.Fatal(err)
	}
}

func seedIntentChain(t *testing.T, s *Store, dispatchID string) {
	t.Helper()
	seedObservation(t, s)
	if err := s.SaveBatch(nil, "batch-1", "wiki-maintenance", "route-rev-1", "vault-main", now(), "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", []string{"0192e6c6-4d7f-7abc-8def-012345678901"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDecision(nil, DecisionRecord{DecisionID: "decision-1", BatchID: "batch-1", RouteID: "wiki-maintenance",
		RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch", Classification: "normal", CreatedAt: now(), Actor: "system"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIntent(nil, intentRecord(dispatchID, "decision-1")); err != nil {
		t.Fatalf("save intent: %v", err)
	}
}

func intentRecord(dispatchID, decisionID string) IntentRecord {
	return IntentRecord{
		DispatchID: dispatchID, DecisionID: decisionID, RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
		TargetID: "hermes-kanban-main", TargetType: "hermes-kanban", ResourceID: "vault-main", Generation: 1,
		IdempotencyKey: "jjukkumi:v1:sha256:" + strings.Repeat("0", 63) + "1", ContentFingerprint: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		ManifestDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", RequestVersion: "jjukkumi.dispatch-intent/v1",
		RequestJSON: "{}", CreatedAt: now(),
	}
}

func writeDir(path string) error {
	return osMkdirAll(path)
}

var now = func() string { return "2026-08-20T00:00:00Z" }

func TestIntentReservationAtomicInTransaction(t *testing.T) {
	s := openTestStore(t)
	seedObservation(t, s)
	if err := s.SaveBatch(nil, "batch-1", "wiki-maintenance", "route-rev-1", "vault-main", now(), "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", []string{"0192e6c6-4d7f-7abc-8def-012345678901"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDecision(nil, DecisionRecord{DecisionID: "decision-1", BatchID: "batch-1", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch", Classification: "normal", CreatedAt: now(), Actor: "system"}); err != nil {
		t.Fatal(err)
	}
	// Break the transaction after the intent insert but before commit:
	// neither the intent nor the reservation may survive.
	tx, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIntent(tx, intentRecord("dispatch-tx", "decision-1")); err != nil {
		t.Fatalf("insert in tx: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO missing_table VALUES (1)`); err == nil {
		t.Fatal("expected failure")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE dispatch_id = ?`, "dispatch-tx").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("rolled-back intent must not persist")
	}
	rec, err := s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil || rec.ActiveDispatchID != "" {
		t.Fatalf("rolled-back reservation must not persist: %+v %v", rec, err)
	}
	// The same transaction succeeds when nothing breaks.
	tx2, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIntent(tx2, intentRecord("dispatch-tx", "decision-1")); err != nil {
		t.Fatalf("intent in tx: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestSaveIntentWithoutRuntimeRow(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki-maintenance", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", now()); err != nil {
		t.Fatal(err)
	}
	seedIntentChainWithoutRouteState(t, s)
	err = s.SaveIntent(nil, intentRecord("dispatch-x", "decision-1"))
	if err == nil || !strings.Contains(err.Error(), "no runtime state") {
		t.Fatalf("missing runtime row must be reported distinctly: %v", err)
	}
}

func TestLeasePersistsNextAttemptAt(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")
	if err := s.AcquireLease(nil, "dispatch-1", "owner-a", "2026-08-20T00:01:00Z", "2026-08-20T00:05:00Z", now()); err != nil {
		t.Fatal(err)
	}
	var next sql.NullString
	if err := s.QueryRow(`SELECT next_attempt_at FROM dispatch_intents WHERE dispatch_id = ?`, "dispatch-1").Scan(&next); err != nil {
		t.Fatal(err)
	}
	if !next.Valid || next.String != "2026-08-20T00:05:00Z" {
		t.Fatalf("next_attempt_at not persisted: %+v", next)
	}
}

func TestIntentTransitionMatrix(t *testing.T) {
	legal := map[string][]string{
		"ready":         {"submitting", "superseded"},
		"submitting":    {"accepted", "rejected", "unknown", "retry_wait"},
		"retry_wait":    {"submitting"},
		"unknown":       {"reconciling"},
		"reconciling":   {"accepted", "retry_wait", "dead_lettered"},
		"rejected":      {"dead_lettered"},
		"accepted":      {"completed", "failed", "canceled"},
		"dead_lettered": {"ready", "superseded"},
	}
	terminal := map[string]bool{"superseded": true, "completed": true, "failed": true, "canceled": true}
	states := []string{"ready", "submitting", "accepted", "rejected", "unknown", "retry_wait", "reconciling", "dead_lettered", "superseded", "completed", "failed", "canceled"}
	for _, from := range states {
		for _, to := range states {
			want := false
			for _, w := range legal[from] {
				if w == to {
					want = true
				}
			}
			if got := validIntentTransition(from, to); got != want {
				t.Errorf("transition %s -> %s = %v, want %v", from, to, got, want)
			}
			if terminal[from] && validIntentTransition(from, to) {
				t.Errorf("terminal state %s must have no outgoing transitions", from)
			}
		}
	}
}

func TestCheckConstraintsFailClosed(t *testing.T) {
	s := openTestStore(t)
	bad := []struct {
		name string
		sql  string
	}{
		{"bad operation", `INSERT INTO observation_changes (observation_id, ordinal, path, operation, exists_after, file_type, digest_status) VALUES ('x',0,'a.md','upsert',1,'regular','known')`},
		{"bad file type", `INSERT INTO observation_changes (observation_id, ordinal, path, operation, exists_after, file_type, digest_status) VALUES ('x',0,'a.md','create',1,'socket','known')`},
		{"bad digest status", `INSERT INTO observation_changes (observation_id, ordinal, path, operation, exists_after, file_type, digest_status) VALUES ('x',0,'a.md','create',1,'regular','maybe')`},
		{"bad intent state", `INSERT INTO dispatch_intents (dispatch_id, decision_id, route_id, route_revision, target_id, target_type, resource_id, generation, idempotency_key, content_fingerprint, manifest_digest, request_version, request_json, state, created_at, updated_at) VALUES ('d','x','r','rr','t','tt','vault-main',1,'k','sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855','sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855','v','{}','sleeping','t','t')`},
		{"zero generation", `INSERT INTO dispatch_intents (dispatch_id, decision_id, route_id, route_revision, target_id, target_type, resource_id, generation, idempotency_key, content_fingerprint, manifest_digest, request_version, request_json, state, created_at, updated_at) VALUES ('d','x','r','rr','t','tt','vault-main',0,'k','sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855','sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855','v','{}','ready','t','t')`},
		{"bad route state", `INSERT INTO route_runtime_state (route_id, activation_state, route_state) VALUES ('wiki-maintenance','enabled','SLEEPING')`},
		{"bad activation", `INSERT INTO route_runtime_state (route_id, activation_state, route_state) VALUES ('wiki-maintenance','on','IDLE')`},
	}
	for _, c := range bad {
		if _, err := s.Exec(c.sql); err == nil {
			t.Errorf("%s: CHECK constraint must reject", c.name)
		}
	}
}

func TestMigrationFailureIsAtomic(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	bad := Migration{Version: MaxSchemaVersion + 1, Name: "broken", SQL: `CREATE TABLE broken (x); THIS IS NOT SQL`}
	if err := s.applyMigration(bad); err == nil {
		t.Fatal("broken migration must fail")
	}
	// Nothing of the failed migration persisted: still at the shipped
	// baseline and no broken artifacts.
	v, err := s.SchemaVersion()
	if err != nil || v != MaxSchemaVersion {
		t.Fatalf("failed migration must not advance the ledger: %d %v", v, err)
	}
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'broken'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("failed migration must not leave partial DDL")
	}
}

func TestNetworkPlacementRejected(t *testing.T) {
	// darwin: non-local mount flags
	if runtime.GOOS == "darwin" {
		networkStatfs := func(path string, st *syscall.Statfs_t) error {
			*st = syscall.Statfs_t{}
			// Flags zero: MNT_LOCAL unset.
			return nil
		}
		err := rejectNetworkPlacementStatfs("/net/server/state.db", networkStatfs)
		if err == nil || !strings.Contains(err.Error(), "network filesystem") {
			t.Fatalf("darwin network placement must be rejected: %v", err)
		}
	} else {
		err := rejectNetworkPlacementStatfs("/mnt/nfs/state.db", func(path string, st *syscall.Statfs_t) error {
			*st = syscall.Statfs_t{}
			st.Type = 0x6969 // NFS magic on linux
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "network filesystem") {
			t.Fatalf("linux NFS placement must be rejected: %v", err)
		}
	}
	if err := rejectNetworkPlacementStatfs("/tmp/x/state.db", func(path string, st *syscall.Statfs_t) error {
		*st = syscall.Statfs_t{}
		if runtime.GOOS == "darwin" {
			st.Flags = 0x00001000 // MNT_LOCAL
		}
		return nil
	}); err != nil {
		t.Fatalf("local placement must be accepted: %v", err)
	}
	if err := rejectNetworkPlacementStatfs("/gone/state.db", func(path string, st *syscall.Statfs_t) error {
		return fmt.Errorf("boom")
	}); err == nil {
		t.Fatal("statfs failure must surface")
	}
}

func TestSourceEventKeyUniquePerSource(t *testing.T) {
	s := openTestStore(t)
	mk := func(id, sourceID, eventKey string) ObservationRecord {
		return ObservationRecord{
			ObservationID: id, SchemaVersion: "jjukkumi.source-observation/v1", SourceType: "watchman",
			SourceID: sourceID, SourceEventKey: eventKey, TriggerName: "trig", ResourceID: "vault-main",
			ObservedAt: now(), ReceivedAt: now(),
			RawPayloadDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", IngestStatus: "ok",
		}
	}
	if err := s.SaveObservation(nil, mk("0192e6c6-4d7f-7abc-8def-012345678901", "src-a", "event-1")); err != nil {
		t.Fatal(err)
	}
	// Same event key on a different source is a distinct delivery.
	if err := s.SaveObservation(nil, mk("0192e6c6-4d7f-7abc-8def-012345678902", "src-b", "event-1")); err != nil {
		t.Fatalf("per-source event key must allow different sources: %v", err)
	}
	// Same source, same event key is a retransmission and must collide.
	if err := s.SaveObservation(nil, mk("0192e6c6-4d7f-7abc-8def-012345678903", "src-a", "event-1")); err == nil {
		t.Fatal("duplicate (source, event key) must be rejected")
	}
}

func seedIntentChainWithoutRouteState(t *testing.T, s *Store) {
	t.Helper()
	seedObservation(t, s)
	if err := s.SaveBatch(nil, "batch-1", "wiki-maintenance", "route-rev-1", "vault-main", now(), "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", []string{"0192e6c6-4d7f-7abc-8def-012345678901"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDecision(nil, DecisionRecord{DecisionID: "decision-1", BatchID: "batch-1", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch", Classification: "normal", CreatedAt: now(), Actor: "system"}); err != nil {
		t.Fatal(err)
	}
	// Deliberately no InitializeRouteState call.
}

func TestBackupVerificationFailsClosed(t *testing.T) {
	// quickCheckFile on a non-database file must fail, and Backup refuses
	// an existing target.
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.db")
	if err := os.WriteFile(junk, []byte("definitely not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := quickCheckFile(junk); err == nil {
		t.Fatal("non-database backup must fail verification")
	}
	s := openTestStore(t)
	backupPath := filepath.Join(dir, "new.backup")
	if err := s.Backup(backupPath); err != nil {
		t.Fatalf("first backup: %v", err)
	}
	if info, err := os.Stat(backupPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup must be owner-only: %v %+v", err, info)
	}
	err := s.Backup(filepath.Join(dir, "new.backup"))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second backup must refuse overwrite: %v", err)
	}
}

func TestMigrationLedgerGapDetected(t *testing.T) {
	s := openTestStore(t)
	// Forge a ledger entry with a foreign checksum below the effective
	// maximum: the binary's test history knows versions 1..3 beyond the
	// shipped baseline.
	s.migrations = append(append([]Migration{}, Migrations...),
		Migration{Version: MaxSchemaVersion + 1, Name: "test-next", SQL: "CREATE TABLE t2 (x)"},
		Migration{Version: MaxSchemaVersion + 2, Name: "test-last", SQL: "CREATE TABLE t3 (x)"})
	if _, err := s.Exec(fmt.Sprintf(`INSERT INTO schema_migrations (version, name, checksum) VALUES (%d, 'ghost', 'sha256:0')`, MaxSchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	err := s.Migrate(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("foreign ledger checksum must be detected: %v", err)
	}
	// A true gap: ledger {1,3} against a test history of three
	// migrations, so version 3 is supported and the missing version 2 is
	// the detected defect.
	if _, err := s.Exec(fmt.Sprintf(`DELETE FROM schema_migrations WHERE version = %d`, MaxSchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(fmt.Sprintf(`INSERT INTO schema_migrations (version, name, checksum) VALUES (%d, 'ghost', 'sha256:0')`, MaxSchemaVersion+2)); err != nil {
		t.Fatal(err)
	}
	err = s.Migrate(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "gap") {
		t.Fatalf("ledger gap must be detected: %v", err)
	}
}

// TestSameSecondRetriesAllowed proves the migration v2 remediation: two
// attempts of one dispatch inside the same canonical second both
// persist (the over-constraining UNIQUE(dispatch_id, started_at) is
// gone), and per-dispatch ordering stays queryable.
func TestSameSecondRetriesAllowed(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedRouteForAttempts(t, s)
	now := "2026-08-21T00:00:00Z"
	first, err := s.AcquireAttempt(ctx, ports.AcquireAttempt{
		DispatchID: "dispatch-samesecond", Owner: "drain",
		AttemptID: "att-ss-1", Now: now, LeaseExpiresAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The first attempt fails definitely and becomes due again inside
	// the same canonical second (backoff elapsed, operator made it due).
	if err := s.CompleteAttempt(ctx, ports.AttemptResult{
		AttemptID: first, DispatchID: "dispatch-samesecond", Outcome: "transport_failure",
		ErrorCode: "definite_not_submitted", CompletedAt: now,
		Transition: ports.AttemptTransition{To: records.IntentRetryWait, Reason: state.ReasonTransientFailure,
			TransitionID: first + ":transient_failure"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.MakeRetryDue(ctx, "dispatch-samesecond", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcquireAttempt(ctx, ports.AcquireAttempt{
		DispatchID: "dispatch-samesecond", Owner: "operator-retry",
		AttemptID: "att-ss-2", Now: now, LeaseExpiresAt: now,
	}); err != nil {
		t.Fatalf("the same-second retry must persist after migration v2: %v", err)
	}
	var count int
	if err := s.QueryRow(`SELECT COUNT(*) FROM dispatch_attempts WHERE dispatch_id = 'dispatch-samesecond' AND started_at = ?`, now).Scan(&count); err != nil || count != 2 {
		t.Fatalf("both same-second attempts must be recorded: %d (err=%v)", count, err)
	}
}

// TestMigrationV2PreservesAttemptRows proves the v1→v2 upgrade: attempt
// rows written at v1 survive the destructive rebuild with their
// columns, a pre-migration backup is taken, and the over-constraint is
// gone afterwards.
func TestMigrationV2PreservesAttemptRows(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "state.db")
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	// Apply only v1 (a one-migration history), then write one attempt
	// row.
	s.migrations = []Migration{Migrations[0]}
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResource(nil, "vault-main", "r", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki", "rev-1", "rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-08-21T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki"); err != nil {
		t.Fatal(err)
	}
	// The intent is written with the v1 column list: this phase tests
	// the migration from a real v1 database, not from this binary's
	// current writer.
	batchv1 := `INSERT INTO change_batches (batch_id, route_id, route_revision, resource_id, created_at, content_fingerprint)
		VALUES ('batch-mig', 'wiki', 'rev-1', 'vault-main', '2026-08-21T00:00:00Z', 'sha256:a')`
	if _, err := s.Exec(batchv1); err != nil {
		t.Fatal(err)
	}
	decisionv1 := `INSERT INTO policy_decisions (decision_id, batch_id, route_id, route_revision, policy_revision, disposition, classification, reason_codes_json, created_at, actor)
		VALUES ('decision-mig', 'batch-mig', 'wiki', 'rev-1', 'rev-1', 'dispatch', 'normal', '[]', '2026-08-21T00:00:00Z', 'planner')`
	if _, err := s.Exec(decisionv1); err != nil {
		t.Fatal(err)
	}
	seedv1 := `INSERT INTO dispatch_intents (dispatch_id, decision_id, route_id, route_revision, target_id, target_type, resource_id, generation, idempotency_key, content_fingerprint, manifest_digest, request_version, request_json, state, created_at, updated_at)
		VALUES ('dispatch-mig', 'decision-mig', 'wiki', 'rev-1', 'hermes-kanban-main', 'hermes-kanban', 'vault-main', 1, 'key', 'sha256:a', 'sha256:b', 'jjukkumi.hermes-task/v1', '{}', 'accepted', '2026-08-21T00:00:00Z', '2026-08-21T00:00:00Z')`
	if _, err := s.Exec(seedv1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO dispatch_attempts (attempt_id, dispatch_id, lease_owner, started_at, completed_at, outcome, diagnostic)
		VALUES ('att-mig', 'dispatch-mig', 'drain', '2026-08-21T00:00:00Z', '2026-08-21T00:00:01Z', 'accepted', 'ok')`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Reopen and migrate to v2 with a real backup directory.
	s2, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	backupDir := filepath.Join(dir, "backups")
	if err := s2.Migrate(backupDir); err != nil {
		t.Fatal(err)
	}
	var owner, completed, outcome string
	if err := s2.QueryRow(`SELECT lease_owner, completed_at, outcome FROM dispatch_attempts WHERE attempt_id = 'att-mig'`).
		Scan(&owner, &completed, &outcome); err != nil {
		t.Fatalf("v1 attempt row must survive the v2 rebuild: %v", err)
	}
	if owner != "drain" || completed != "2026-08-21T00:00:01Z" || outcome != "accepted" {
		t.Fatalf("v1 attempt row altered by the rebuild: %s %s %s", owner, completed, outcome)
	}
	// A second same-second attempt is now allowed.
	if _, err := s2.Exec(`INSERT INTO dispatch_attempts (attempt_id, dispatch_id, lease_owner, started_at)
		VALUES ('att-mig-2', 'dispatch-mig', 'retry', '2026-08-21T00:00:00Z')`); err != nil {
		t.Fatalf("same-second second attempt must be allowed after v2: %v", err)
	}
	// A pre-migration backup was taken.
	matches, _ := filepath.Glob(filepath.Join(backupDir, "*"))
	if len(matches) == 0 {
		t.Fatal("the v2 migration must take a pre-migration backup")
	}
}

// attemptLineage builds one full observation-to-intent lineage for the
// attempts tests.
func attemptLineage(dispatchID, decisionID, obsID, batchID, now string) ports.Lineage {
	return ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: obsID, SchemaVersion: "jjukkumi.source-observation/v1",
			SourceType: "watchman", SourceID: "watchman-main", TriggerName: "trig",
			ResourceID: "vault-main", ObservedAt: now, ReceivedAt: now,
			RawPayloadDigest: "sha256:a", IngestStatus: "accepted",
		},
		Batch: ports.BatchInput{
			BatchID: batchID, RouteID: "wiki", RouteRevision: "rev-1",
			ResourceID: "vault-main", CreatedAt: now,
			ContentFingerprint: "sha256:a", ObservationIDs: []string{obsID},
		},
		Decision: ports.DecisionInput{
			DecisionID: decisionID, BatchID: batchID, RouteID: "wiki",
			RouteRevision: "rev-1", PolicyRevision: "rev-1",
			Disposition: "dispatch", Classification: "normal", CreatedAt: now, Actor: "planner",
		},
		Intent: ports.IntentInput{
			DispatchID: dispatchID, DecisionID: decisionID, RouteID: "wiki", RouteRevision: "rev-1",
			TargetID: "hermes-kanban-main", TargetType: "hermes-kanban", ResourceID: "vault-main",
			Generation: 1, IdempotencyKey: "key-" + dispatchID, ContentFingerprint: "sha256:a",
			ManifestDigest: "sha256:b", RequestVersion: "jjukkumi.hermes-task/v1", RequestJSON: "{}", CreatedAt: now,
		},
	}
}

// seedRouteForAttempts registers the minimal lineage the attempts table
// requires.
func seedRouteForAttempts(t *testing.T, s *Store) {
	t.Helper()
	if err := s.RegisterResource(nil, "vault-main", "r", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki", "rev-1", "rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-08-21T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki"); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitLineage(context.Background(), attemptLineage("dispatch-samesecond", "decision-ss", "obs-ss", "batch-ss", "2026-08-21T00:00:00Z")); err != nil {
		t.Fatal(err)
	}
}

// TestMigrationV4BackfillsBegunAt proves the v4 backfill preserves the
// begin timestamp of rows created at schema v3 (E5 audit round 10).
func TestMigrationV4BackfillsBegunAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	partial := []Migration{Migrations[0], Migrations[1], Migrations[2]}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.migrations = partial
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatalf("migrate to v3: %v", err)
	}
	if err := s.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki-maintenance", "route-rev-1", "policy-rev-1", "vault-main", "hermes-main", "{}", now()); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki-maintenance"); err != nil {
		t.Fatal(err)
	}
	seedIntentChain(t, s, "dispatch-v4")
	// A v3-era row: the begun_at column does not exist yet.
	if _, err := s.Exec(`INSERT INTO work_receipts
		(receipt_id, dispatch_id, run_id, resource_id, status, changes_json, submitted_at, validation_state, validation_reasons_json)
		VALUES ('wr-v4', 'dispatch-v4', 'run-v4', 'vault-main', 'begun', '[]', '2026-08-01T00:00:00Z', 'valid', '[]')`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if err := upgraded.Migrate(t.TempDir()); err != nil {
		t.Fatalf("migrate to v4: %v", err)
	}
	view, err := upgraded.LoadWorkReceipt(context.Background(), "dispatch-v4", "run-v4")
	if err != nil {
		t.Fatal(err)
	}
	if view.BegunAt != "2026-08-01T00:00:00Z" {
		t.Fatalf("v4 backfill must preserve the begin timestamp, got %q", view.BegunAt)
	}
}

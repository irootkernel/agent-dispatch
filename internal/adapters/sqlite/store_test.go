package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
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
	// The lease transaction requires an enabled route (the epic audit's
	// transactional activation gate, E7 round 1).
	if err := s.SetRouteActivation(context.Background(), "wiki-maintenance", "enabled", "route-rev-1", "", now()); err != nil {
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
	// One verified backup per migration run (E9-T1/L-20), named for the
	// applied range instead of the unit.
	matches, err := filepath.Glob(filepath.Join(backupDir, "agent-dispatch-v*-to-v*.backup"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected exactly one per-run pre-migration backup, got %v (%v)", matches, err)
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
		ObservationID: "0192e6c6-4d7f-7abc-8def-012345678901", SchemaVersion: "agent-dispatch.source-observation/v1",
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

// transitionInTx applies one guarded intent transition inside a test
// transaction through the same unexported primitive the guarded store
// flows compose (the raw TransitionIntent export is gone, E8-T2/L-2).
func transitionInTx(t *testing.T, s *Store, dispatchID string, from, to records.IntentState, reason state.IntentReason) error {
	t.Helper()
	tx, err := s.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	evidence := state.IntentEvidence{Actor: "test"}
	if to == records.IntentSubmitting {
		// Entering submitting demands lease evidence (the same guard the
		// guarded AcquireAttempt flow satisfies).
		evidence.Lease = &state.LeaseEvidence{Owner: "test-owner", ExpiresAt: "2099-01-01T00:00:00Z"}
	}
	if err := s.transitionWithin(context.Background(), tx, dispatchID, from, to, reason,
		evidence, now(), fmt.Sprintf(`{"reason":%q,"actor":"test"}`, reason)); err != nil {
		return err
	}
	return tx.Commit()
}

func TestIntentReservationAndLease(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")

	// A second intent for the same route cannot reserve the active slot.
	second := intentRecord("dispatch-2", "decision-2")
	if err := s.SaveIntent(nil, second); err == nil {
		t.Fatal("route with an active dispatch must refuse a second intent (invariant 5)")
	}

	// Lease acquisition is a conditional write through the guarded flow
	// (the raw test-only AcquireLease export is gone, E8-T2/L-2).
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-a", Owner: "owner-a",
		Now: "2026-08-20T00:00:30Z", LeaseExpiresAt: "2026-08-20T00:01:00Z",
	}); err != nil {
		t.Fatalf("lease: %v", err)
	}
	// Re-acquiring while in submitting state must fail.
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-b", Owner: "owner-b",
		Now: "2026-08-20T00:01:30Z", LeaseExpiresAt: "2026-08-20T00:02:00Z",
	}); err == nil {
		t.Fatal("lease re-acquisition must be conditional")
	}
}

func TestStateTransitionsAppendOnlyAndValidated(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")
	if err := transitionInTx(t, s, "dispatch-1", records.IntentReady, records.IntentSubmitting, state.ReasonLeaseAcquired); err != nil {
		t.Fatalf("ready->submitting: %v", err)
	}
	if err := transitionInTx(t, s, "dispatch-1", records.IntentReady, records.IntentSubmitting, state.ReasonLeaseAcquired); err == nil {
		t.Fatal("stale from-state must fail (invariant 3)")
	}
	if err := transitionInTx(t, s, "dispatch-1", records.IntentSubmitting, records.IntentCompleted, state.ReasonExecutionSucceeded); err == nil {
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
	if err := transitionInTx(t, s, "dispatch-1", records.IntentReady, records.IntentCompleted, state.ReasonExecutionSucceeded); err == nil {
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
	if err := transitionInTx(t, s, "dispatch-1", records.IntentReady, records.IntentSubmitting, state.ReasonLeaseAcquired); err != nil {
		t.Fatalf("valid transition after a rejected one: %v", err)
	}
}

func TestRouteRuntimeStateOptimisticConcurrency(t *testing.T) {
	s := openTestStore(t)
	rec, err := s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	// The shared fixture enables the route (version 1); the optimistic
	// check is relative to that baseline.
	if rec.Version != 1 || rec.RouteState != "IDLE" || rec.ActivationState != "enabled" {
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
	if err != nil || rec.Version != 2 || rec.RouteState != "ACTIVE_CLEAN" {
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
	if err := s.SaveBatch(nil, BatchRecord{BatchID: "batch-1", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
		ResourceID: "vault-main", CreatedAt: now(), ContentFingerprint: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		ObservationIDs: []string{"0192e6c6-4d7f-7abc-8def-012345678901"}}); err != nil {
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
		ObservationID: "0192e6c6-4d7f-7abc-8def-012345678902", SchemaVersion: "agent-dispatch.source-observation/v1",
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
		ObservationID: "0192e6c6-4d7f-7abc-8def-012345678901", SchemaVersion: "agent-dispatch.source-observation/v1",
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
	if err := s.SaveBatch(nil, BatchRecord{BatchID: "batch-1", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
		ResourceID: "vault-main", CreatedAt: now(), ContentFingerprint: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		ObservationIDs: []string{"0192e6c6-4d7f-7abc-8def-012345678901"}}); err != nil {
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
		IdempotencyKey: "agent-dispatch:v1:sha256:" + strings.Repeat("0", 63) + "1", ContentFingerprint: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		ManifestDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", RequestVersion: "agent-dispatch.dispatch-intent/v1",
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
	if err := s.SaveBatch(nil, BatchRecord{BatchID: "batch-1", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
		ResourceID: "vault-main", CreatedAt: now(), ContentFingerprint: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		ObservationIDs: []string{"0192e6c6-4d7f-7abc-8def-012345678901"}}); err != nil {
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
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-next", Owner: "owner-a",
		Now: now(), LeaseExpiresAt: "2026-08-20T00:01:00Z", NextAttemptAt: "2026-08-20T00:05:00Z",
	}); err != nil {
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
			ObservationID: id, SchemaVersion: "agent-dispatch.source-observation/v1", SourceType: "watchman",
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
	if err := s.SaveBatch(nil, BatchRecord{BatchID: "batch-1", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
		ResourceID: "vault-main", CreatedAt: now(), ContentFingerprint: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		ObservationIDs: []string{"0192e6c6-4d7f-7abc-8def-012345678901"}}); err != nil {
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
	if err := s.MakeRetryDue(ctx, "dispatch-samesecond", "operator", now); err != nil {
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
		VALUES ('dispatch-mig', 'decision-mig', 'wiki', 'rev-1', 'hermes-kanban-main', 'hermes-kanban', 'vault-main', 1, 'key', 'sha256:a', 'sha256:b', 'agent-dispatch.hermes-task/v1', '{}', 'accepted', '2026-08-21T00:00:00Z', '2026-08-21T00:00:00Z')`
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
			ObservationID: obsID, SchemaVersion: "agent-dispatch.source-observation/v1",
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
			ManifestDigest: "sha256:b", RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: "{}", CreatedAt: now,
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
	if err := s.SetRouteActivation(context.Background(), "wiki", "enabled", "rev-1", "", "2026-08-21T00:00:00Z"); err != nil {
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
	seedIntentChainV3Era(t, s, "dispatch-v4")
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

// TestConcurrentQuarantineResolutionSingleWinner proves the held-row
// conditional update: a second resolution of the same hold fails (E5
// audit round 11, F007).
func TestConcurrentQuarantineResolutionSingleWinner(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-q")
	lin := ports.Lineage{
		Observation: ports.ObservationInput{ObservationID: "obs-q", SchemaVersion: "agent-dispatch.source-observation/v1",
			SourceType: "watchman", SourceID: "src", ResourceID: "vault-main", ObservedAt: now(), ReceivedAt: now(),
			IngestStatus: "accepted"},
		Batch: ports.BatchInput{BatchID: "batch-q", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: now(),
			ContentFingerprint: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		Decision: ports.DecisionInput{DecisionID: "decision-q", BatchID: "batch-q", RouteID: "wiki-maintenance",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "quarantine",
			Classification: "protected", CreatedAt: now(), Actor: "planner"},
	}
	if err := s.CommitQuarantineLineage(context.Background(), lin, ports.QuarantineInput{
		QuarantineID: "q-1", BatchID: "batch-q", DecisionID: "decision-q",
		ReasonCodes: []string{"protected_path_present"}, CreatedAt: now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReleaseQuarantine(context.Background(), "q-1", "operator", "reviewed", now()); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if _, err := s.DiscardQuarantine(context.Background(), "q-1", "operator", "reviewed", now()); err == nil {
		t.Fatal("a second resolution must fail against the resolved hold")
	}
}

func TestCompleteActiveRefusesUnpreparedFollowup(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")
	rec0, err := s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRouteRuntimeState(nil, "wiki-maintenance", rec0.Version, func(r *RouteRuntimeStateRecord) {
		r.ActivationState = "enabled"
		r.RouteState = "ACTIVE_CLEAN"
		r.ActiveDispatchID = "dispatch-1"
	}); err != nil {
		t.Fatal(err)
	}
	seedLegacyLane(t, s, "ACTIVE_CLEAN", "dispatch-1", 0)
	// A reconciliation arrival or quarantine release flips pending_reconcile
	// without moving the fenced dirty generation: the caller that read the
	// earlier snapshot decided no follow-up was needed. The completion must
	// refuse rather than drop the pending signal into a follow-up-less
	// FOLLOWUP_READY.
	if err := s.MarkPendingReconcile(context.Background(), "wiki-maintenance", "", now()); err != nil {
		t.Fatal(err)
	}
	_, err = s.CompleteActive(context.Background(), ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: "dispatch-1",
		ReceiptRef: "rcpt-work-1", Actor: "hermes-task", Now: now(),
	})
	if !errors.Is(err, ErrOptimisticConcurrency) {
		t.Fatalf("completion without the follow-up the route now needs must conflict, got %v", err)
	}
	rec, loadErr := s.LoadRouteRuntimeState("wiki-maintenance")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if rec.RouteState != "ACTIVE_CLEAN" || !rec.PendingReconcile || rec.ActiveDispatchID != "dispatch-1" {
		t.Fatalf("the refusal must leave the route and its pending signal intact: %+v", rec)
	}
}

// TestCompleteActiveFollowupBudgetExhausted proves the consecutive
// follow-up bound (E8-T1, H-1.1): a completion whose follow-up would
// carry a generation beyond state.MaxConsecutiveFollowups schedules no
// follow-up and resolves the route through UNCERTAIN with the slot and
// dirty generation retained for operator reconciliation.
func TestCompleteActiveFollowupBudgetExhausted(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")
	rec0, err := s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRouteRuntimeState(nil, "wiki-maintenance", rec0.Version, func(r *RouteRuntimeStateRecord) {
		r.ActivationState = "enabled"
		r.RouteState = "ACTIVE_DIRTY"
		r.ActiveDispatchID = "dispatch-1"
		r.DirtyGeneration = 1
	}); err != nil {
		t.Fatal(err)
	}
	seedLegacyLane(t, s, "ACTIVE_DIRTY", "dispatch-1", 1)
	out, err := s.CompleteActive(context.Background(), ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: "dispatch-1",
		ReceiptRef: "rcpt-work-1", Actor: "hermes-task", Now: now(),
		FollowupGeneration: state.MaxConsecutiveFollowups + 1,
	})
	if err != nil {
		t.Fatalf("over-budget completion must resolve through UNCERTAIN, got %v", err)
	}
	if out.RouteTo != state.RouteUncertain || out.FollowupDispatchID != "" {
		t.Fatalf("over-budget completion must move the route to UNCERTAIN without a follow-up: %+v", out)
	}
	rec, loadErr := s.LoadRouteRuntimeState("wiki-maintenance")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if rec.RouteState != "UNCERTAIN" || rec.ActiveDispatchID != "dispatch-1" || rec.DirtyGeneration != 1 {
		t.Fatalf("the uncertain resolution must retain the slot and dirty generation: %+v", rec)
	}
	var intents int
	if err := s.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if intents != 1 {
		t.Fatalf("over-budget completion must schedule no follow-up beyond the seeded intent, got %d intents", intents)
	}
}

// TestCompleteActiveFollowupBudgetExhaustedOnFailure pins the store's
// single decision point for the failed-over-budget intersection
// (E8-T1 round-1 F002): a failure-path completion with remaining failure
// budget but a follow-up generation beyond the chain bound also resolves
// through UNCERTAIN with the followup-budget reason, and the prepared
// follow-up (if any) is never scheduled.
func TestCompleteActiveFollowupBudgetExhaustedOnFailure(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")
	rec0, err := s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRouteRuntimeState(nil, "wiki-maintenance", rec0.Version, func(r *RouteRuntimeStateRecord) {
		r.ActivationState = "enabled"
		r.RouteState = "ACTIVE_DIRTY"
		r.ActiveDispatchID = "dispatch-1"
		r.DirtyGeneration = 1
	}); err != nil {
		t.Fatal(err)
	}
	seedLegacyLane(t, s, "ACTIVE_DIRTY", "dispatch-1", 1)
	out, err := s.CompleteActive(context.Background(), ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: "dispatch-1", Failed: true,
		FailureBudgetRemaining: 2, ReceiptRef: "rcpt-work-1", Actor: "hermes-task", Now: now(),
		FollowupGeneration: state.MaxConsecutiveFollowups + 1,
	})
	if err != nil {
		t.Fatalf("failed over-budget completion must resolve through UNCERTAIN, got %v", err)
	}
	if out.RouteTo != state.RouteUncertain || out.FollowupDispatchID != "" {
		t.Fatalf("the chain bound dominates the failure path: %+v", out)
	}
	rec, loadErr := s.LoadRouteRuntimeState("wiki-maintenance")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if rec.RouteState != "UNCERTAIN" || rec.DirtyGeneration != 1 {
		t.Fatalf("the uncertain resolution must retain the dirty generation: %+v", rec)
	}
}

// TestCommitMergePendingIdleEmptySlotRecordsPendingReconcile proves the
// IDLE empty-slot merge records owed work as a pending reconciliation
// instead of a dirty generation nothing can later clear (E8-T1 round-1
// F001: the pre-watermark batches sit below any later dispatch's
// generation window, so a dirty count would wedge the next clean
// completion).
func TestCommitMergePendingIdleEmptySlotRecordsPendingReconcile(t *testing.T) {
	// openTestStore seeds the route with an IDLE, empty-slot runtime row.
	s := openTestStore(t)
	lin := mergeLineage("batch-idle", "decision-idle", now())
	dirty, err := s.CommitMergePending(context.Background(), lin, nil, nil, "watchman", now())
	if err != nil {
		t.Fatalf("idle empty-slot merge: %v", err)
	}
	if dirty != 0 {
		t.Fatalf("no dispatch owns this burst; the dirty count must stay 0, got %d", dirty)
	}
	rec, err := s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if !rec.PendingReconcile || rec.DirtyGeneration != 0 || rec.RouteState != "IDLE" {
		t.Fatalf("owed work must be recorded as a pending reconciliation on IDLE: %+v", rec)
	}
}

// seedLegacyLane mirrors a route-row coordination seeding onto the
// synthetic legacy lane (E12-T2: the seeded intents have no child rows, so
// their coordination lives on the '__legacy__' lane row).
func seedLegacyLane(t *testing.T, s *Store, laneState, active string, dirty int) {
	t.Helper()
	if _, err := s.Exec(`INSERT INTO destination_lane_state (route_id, destination_id, lane_state, active_dispatch_id, dirty_generation)
		VALUES ('wiki-maintenance', '__legacy__', ?, ?, ?)
		ON CONFLICT (route_id, destination_id) DO UPDATE SET lane_state = excluded.lane_state, active_dispatch_id = excluded.active_dispatch_id, dirty_generation = excluded.dirty_generation`,
		laneState, nullString(active), dirty); err != nil {
		t.Fatal(err)
	}
}

// mergeLineage builds one minimal arriving lineage for merge tests.
func mergeLineage(batchID, decisionID, at string) ports.Lineage {
	return ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: "obs-" + batchID, SchemaVersion: "agent-dispatch.source-observation/v1", SourceType: "watchman",
			SourceID: "watchman-main", TriggerName: "trig", ResourceID: "vault-main",
			ObservedAt: at, ReceivedAt: at, RawPayloadDigest: "sha256:" + strings.Repeat("e", 64), IngestStatus: "accepted",
		},
		Batch: ports.BatchInput{
			BatchID: batchID, RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: at, ContentFingerprint: "sha256:" + strings.Repeat("f", 64),
			ObservationIDs: []string{"obs-" + batchID},
		},
		Decision: ports.DecisionInput{
			DecisionID: decisionID, BatchID: batchID, RouteID: "wiki-maintenance",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch",
			Classification: "normal", CreatedAt: at, Actor: "planner",
		},
	}
}

// TestMigrationV6BackfillsBatchSequence proves the v6 backfill derives
// the deterministic batch ordering and both base_batch_seq resolutions:
// a normal dispatch anchors on its decision's arrival batch, and a
// decision-less (follow-up-shaped) intent anchors on the maximum batch
// at or before its creation second (E8-T1 round-1 testing finding).
func TestMigrationV6BackfillsBatchSequence(t *testing.T) {
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
	// v3-era rows in the v1 column set: batch-1 precedes the normal
	// dispatch; batch-2 shares the follow-up intent's creation second.
	seedV3Batch(t, s, "batch-1", "2026-08-01T00:00:01Z", "obs-a")
	if err := s.SaveDecision(nil, DecisionRecord{DecisionID: "decision-1", BatchID: "batch-1", RouteID: "wiki-maintenance",
		RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch", Classification: "normal",
		CreatedAt: "2026-08-01T00:00:01Z", Actor: "system"}); err != nil {
		t.Fatal(err)
	}
	seedV3Intent(t, s, "dispatch-normal", "decision-1", "2026-08-01T00:00:02Z")
	seedV3Batch(t, s, "batch-2", "2026-08-01T00:00:03Z", "obs-b")
	// A decision-less batch relation: the follow-up-shaped decision carries
	// its generation lineage instead of a batch reference (schema CHECK).
	if err := s.SaveDecision(nil, DecisionRecord{DecisionID: "decision-fu", RouteID: "wiki-maintenance",
		RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch", Classification: "normal",
		GenerationLineageJSON: `{"generations":[]}`, CreatedAt: "2026-08-01T00:00:03Z", Actor: "system"}); err != nil {
		t.Fatal(err)
	}
	seedV3Intent(t, s, "dispatch-followup", "decision-fu", "2026-08-01T00:00:03Z")
	s.Close()

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if err := upgraded.Migrate(t.TempDir()); err != nil {
		t.Fatalf("reopen must migrate through v6: %v", err)
	}
	var seq1, seq2 int
	if err := upgraded.QueryRow(`SELECT batch_seq FROM change_batches WHERE batch_id='batch-1'`).Scan(&seq1); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.QueryRow(`SELECT batch_seq FROM change_batches WHERE batch_id='batch-2'`).Scan(&seq2); err != nil {
		t.Fatal(err)
	}
	if seq1 != 1 || seq2 != 2 {
		t.Fatalf("backfill must derive the deterministic 1..N ordering by (created_at, batch_id), got %d and %d", seq1, seq2)
	}
	var baseNormal, baseFollowup int
	if err := upgraded.QueryRow(`SELECT base_batch_seq FROM dispatch_intents WHERE dispatch_id='dispatch-normal'`).Scan(&baseNormal); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.QueryRow(`SELECT base_batch_seq FROM dispatch_intents WHERE dispatch_id='dispatch-followup'`).Scan(&baseFollowup); err != nil {
		t.Fatal(err)
	}
	if baseNormal != 1 {
		t.Fatalf("a normal dispatch must anchor on its decision's arrival batch, got %d", baseNormal)
	}
	if baseFollowup != 2 {
		t.Fatalf("a decision-less intent must anchor on the maximum batch at its creation second, got %d", baseFollowup)
	}
	window, err := upgraded.LoadActiveGenerationChanges(context.Background(), "wiki-maintenance", "dispatch-followup")
	if err != nil {
		t.Fatal(err)
	}
	if len(window) != 0 {
		t.Fatalf("the migrated follow-up window must start above its watermark, got %d changes", len(window))
	}
}

// seedV3Batch inserts one v3-era batch row in the v1 column set.
func seedV3Batch(t *testing.T, s *Store, batchID, at, obsID string) {
	t.Helper()
	if _, err := s.Exec(`INSERT INTO source_observations
		(observation_id, schema_version, source_type, source_id, trigger_name, resource_id, observed_at, received_at, raw_payload_digest, ingest_status, flags_json)
		VALUES (?, 'agent-dispatch.source-observation/v1', 'watchman', 'watchman-main', 'trig', 'vault-main', ?, ?, 'sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855', 'accepted', '{}')`,
		obsID, at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO change_batches (batch_id, route_id, route_revision, resource_id, created_at, content_fingerprint)
		VALUES (?, 'wiki-maintenance', 'route-rev-1', 'vault-main', ?, 'sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855')`, batchID, at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO batch_observations (batch_id, observation_id) VALUES (?, ?)`, batchID, obsID); err != nil {
		t.Fatal(err)
	}
}

// seedV3Intent inserts one v3-era intent row in the v1 column set
// without reserving the slot (the fixture routes stay IDLE).
func seedV3Intent(t *testing.T, s *Store, dispatchID, decisionID, at string) {
	t.Helper()
	rec := intentRecord(dispatchID, decisionID)
	if _, err := s.Exec(`INSERT INTO dispatch_intents
		(dispatch_id, decision_id, route_id, route_revision, target_id, target_type, target_scope, resource_id, generation, idempotency_key, content_fingerprint, manifest_digest, request_version, request_json, state, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,'ready',?,?)`,
		rec.DispatchID, rec.DecisionID, rec.RouteID, rec.RouteRevision, rec.TargetID, rec.TargetType, rec.TargetScope, rec.ResourceID, rec.Generation,
		rec.IdempotencyKey+"-"+dispatchID, rec.ContentFingerprint, rec.ManifestDigest, rec.RequestVersion, rec.RequestJSON, at, at); err != nil {
		t.Fatalf("seed intent %s: %v", dispatchID, err)
	}
}

func TestResolveUncertainReconciliation(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-u")
	forceRoute := func(t *testing.T, routeState, active string) {
		rec, err := s.LoadRouteRuntimeState("wiki-maintenance")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateRouteRuntimeState(nil, "wiki-maintenance", rec.Version, func(r *RouteRuntimeStateRecord) {
			r.ActivationState = "enabled"
			r.RouteState = routeState
			r.ActiveDispatchID = active
			r.DirtyGeneration = 2
			r.DirtySince = now()
			r.PendingReconcile = true
		}); err != nil {
			t.Fatal(err)
		}
		// The uncertain hold also moved the resolved dispatch's lane into
		// its own UNCERTAIN state, retaining the in-flight coordination
		// (E12-T2: the hold's fence values live on the route row).
		seedLegacyLane(t, s, "UNCERTAIN", active, 2)
	}
	intent := &ports.IntentInput{
		DispatchID: "disp-u-resolved", DecisionID: "decision-r", RouteID: "wiki-maintenance",
		RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes-kanban",
		ResourceID: "vault-main", Generation: 3,
		IdempotencyKey:     "agent-dispatch:v1:sha256:" + strings.Repeat("9", 64),
		ContentFingerprint: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		ManifestDigest:     "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		RequestVersion:     "agent-dispatch.dispatch-intent/v1", RequestJSON: "{}",
	}

	// With due work: the resolution releases the stale slot, collapses the
	// retained generation, and leaves exactly one ready intent behind a
	// FOLLOWUP_READY route with its pending generation marked atomically.
	if err := s.SaveDecision(nil, DecisionRecord{DecisionID: "decision-r", RouteID: "wiki-maintenance",
		RouteRevision: "route-rev-1", PolicyRevision: "route-rev-1", Disposition: "reconcile", Classification: "normal",
		GenerationLineageJSON: `{"route_id":"wiki-maintenance"}`, CreatedAt: now(), Actor: "reconcile"}); err != nil {
		t.Fatal(err)
	}
	forceRoute(t, "UNCERTAIN", "dispatch-u")
	if err := s.ResolveUncertainReconciliation(context.Background(), "wiki-maintenance", intent, 2, true, "reconcile", now()); err != nil {
		t.Fatalf("resolve with work: %v", err)
	}
	rec, err := s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	// The resolved intent holds its LANE's slot as its creation
	// reservation — the same shape a completion's follow-up leaves behind
	// (E12-T2) — and keeps its pending generation for the documented
	// completion follow-up on the route row.
	if rec.RouteState != "FOLLOWUP_READY" || rec.DirtyGeneration != 0 || !rec.PendingReconcile {
		t.Fatalf("resolution must collapse the route's hold behind the resolved intent: %+v", rec)
	}
	var laneActive string
	if err := s.QueryRow(`SELECT COALESCE(active_dispatch_id, '') FROM destination_lane_state WHERE route_id = 'wiki-maintenance' AND destination_id = '__legacy__'`).Scan(&laneActive); err != nil || laneActive != "disp-u-resolved" {
		t.Fatalf("the resolved intent must hold its lane's slot: %q %v", laneActive, err)
	}
	var ready int
	if err := s.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE dispatch_id = 'disp-u-resolved' AND state = 'ready'`).Scan(&ready); err != nil || ready != 1 {
		t.Fatalf("exactly one resolved intent: %d %v", ready, err)
	}

	// Without due work: the route lands IDLE through the declared
	// follow-up-dropped edge.
	forceRoute(t, "UNCERTAIN", "dispatch-u")
	if err := s.ResolveUncertainReconciliation(context.Background(), "wiki-maintenance", nil, 2, true, "reconcile", now()); err != nil {
		t.Fatalf("resolve without work: %v", err)
	}
	rec, err = s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if rec.RouteState != "IDLE" || rec.ActiveDispatchID != "" || rec.DirtyGeneration != 0 || rec.PendingReconcile {
		t.Fatalf("a no-work resolution must land idle: %+v", rec)
	}

	// The reconciliation's own read is fenced: a merge or release that
	// landed inside the enumeration window refuses instead of being
	// silently absorbed by the resolution.
	forceRoute(t, "UNCERTAIN", "dispatch-u")
	if err := s.ResolveUncertainReconciliation(context.Background(), "wiki-maintenance", nil, 0, false, "reconcile", now()); !errors.Is(err, ErrOptimisticConcurrency) {
		t.Fatalf("a moved generation must fence the resolution, got %v", err)
	}
	after, err := s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if after.RouteState != "UNCERTAIN" || after.DirtyGeneration != 2 || !after.PendingReconcile {
		t.Fatalf("the fenced refusal must leave the uncertain route intact: %+v", after)
	}

	// The pending-only fence arm: a release that marked the pending
	// generation inside the window refuses exactly like a moved dirty
	// count.
	forceRoute(t, "UNCERTAIN", "dispatch-u")
	if err := s.ResolveUncertainReconciliation(context.Background(), "wiki-maintenance", nil, 2, false, "reconcile", now()); !errors.Is(err, ErrOptimisticConcurrency) {
		t.Fatalf("a moved pending flag must fence the resolution, got %v", err)
	}

	// Any other state refuses: the resolution is the uncertain route's
	// operator exit, not a general transition.
	forceRoute(t, "ACTIVE_CLEAN", "dispatch-u")
	if err := s.ResolveUncertainReconciliation(context.Background(), "wiki-maintenance", nil, 2, true, "reconcile", now()); !errors.Is(err, ports.ErrStateNotEligible) {
		t.Fatalf("non-uncertain resolution must refuse, got %v", err)
	}
}

func TestConcurrentUncertainResolutionSingleWinner(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-u2")
	rec, err := s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRouteRuntimeState(nil, "wiki-maintenance", rec.Version, func(r *RouteRuntimeStateRecord) {
		r.ActivationState = "enabled"
		r.RouteState = "UNCERTAIN"
		r.ActiveDispatchID = "dispatch-u2"
		r.DirtyGeneration = 1
		r.PendingReconcile = true
	}); err != nil {
		t.Fatal(err)
	}
	seedLegacyLane(t, s, "UNCERTAIN", "dispatch-u2", 1)
	if err := s.SaveDecision(nil, DecisionRecord{DecisionID: "decision-rr", RouteID: "wiki-maintenance",
		RouteRevision: "route-rev-1", PolicyRevision: "route-rev-1", Disposition: "reconcile", Classification: "normal",
		GenerationLineageJSON: `{"route_id":"wiki-maintenance"}`, CreatedAt: now(), Actor: "reconcile"}); err != nil {
		t.Fatal(err)
	}
	intent := &ports.IntentInput{
		DispatchID: "disp-u2-resolved", DecisionID: "decision-rr", RouteID: "wiki-maintenance",
		RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes-kanban",
		ResourceID: "vault-main", Generation: 2,
		IdempotencyKey:     "agent-dispatch:v1:sha256:" + strings.Repeat("7", 64),
		ContentFingerprint: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		ManifestDigest:     "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		RequestVersion:     "agent-dispatch.dispatch-intent/v1", RequestJSON: "{}",
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.ResolveUncertainReconciliation(context.Background(), "wiki-maintenance", intent, 1, true, "reconcile", now())
		}()
	}
	wg.Wait()
	close(results)
	successes, refusals := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ports.ErrStateNotEligible), errors.Is(err, ErrOptimisticConcurrency), errors.Is(err, ports.ErrRouteSlotHeld):
			// Every lost-race arm is a typed conflict.
			refusals++
		default:
			t.Fatalf("concurrent resolution must succeed or refuse on a typed conflict, got %v", err)
		}
	}
	if successes != 1 || refusals != 1 {
		t.Fatalf("exactly one resolution may win, got %d successes and %d refusals", successes, refusals)
	}
	var intents int
	if err := s.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE dispatch_id = 'disp-u2-resolved'`).Scan(&intents); err != nil || intents != 1 {
		t.Fatalf("the winner creates exactly one intent: %d %v", intents, err)
	}
}

func TestClearPendingReconcileConditional(t *testing.T) {
	s := openTestStore(t)
	setPending := func(pending bool) {
		rec, err := s.LoadRouteRuntimeState("wiki-maintenance")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateRouteRuntimeState(nil, "wiki-maintenance", rec.Version, func(r *RouteRuntimeStateRecord) {
			r.PendingReconcile = pending
		}); err != nil {
			t.Fatal(err)
		}
	}
	// The observed flag clears.
	setPending(true)
	cleared, err := s.ClearPendingReconcile(context.Background(), "wiki-maintenance", true, now())
	if err != nil || !cleared {
		t.Fatalf("the observed pending flag must clear: %v %v", cleared, err)
	}
	// A fresh mark by another actor inside the window survives.
	setPending(true)
	cleared, err = s.ClearPendingReconcile(context.Background(), "wiki-maintenance", false, now())
	if err != nil || cleared {
		t.Fatalf("an unobserved fresh mark must survive, got cleared=%v err=%v", cleared, err)
	}
	if rec, _ := s.LoadRouteRuntimeState("wiki-maintenance"); !rec.PendingReconcile {
		t.Fatal("the fresh pending signal must stand")
	}
	// A flag another actor already cleared is an idempotent miss.
	setPending(false)
	cleared, err = s.ClearPendingReconcile(context.Background(), "wiki-maintenance", true, now())
	if err != nil || cleared {
		t.Fatalf("an already-cleared flag is a miss, got cleared=%v err=%v", cleared, err)
	}
}

// seedIntentChainV3Era seeds the lineage with the v3-era observation
// shape: the raw insert omits the v5 position column this era does not
// have (the store helper writes it since E7-T8).
func seedIntentChainV3Era(t *testing.T, s *Store, dispatchID string) {
	t.Helper()
	if _, err := s.Exec(`INSERT INTO source_observations
		(observation_id, schema_version, source_type, source_id, trigger_name, resource_id, observed_at, received_at, raw_payload_digest, ingest_status, flags_json)
		VALUES ('0192e6c6-4d7f-7abc-8def-012345678901', 'agent-dispatch.source-observation/v1', 'watchman', 'watchman-main', 'trig', 'vault-main', ?, ?, 'sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855', 'accepted', '{}')`,
		now(), now()); err != nil {
		t.Fatal(err)
	}
	// The v6-era store API names the batch_seq/base_batch_seq columns;
	// seeding a genuine v3 database writes the v1 column set directly.
	if _, err := s.Exec(`INSERT INTO change_batches (batch_id, route_id, route_revision, resource_id, created_at, content_fingerprint)
		VALUES ('batch-1', 'wiki-maintenance', 'route-rev-1', 'vault-main', ?, 'sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855')`, now()); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDecision(nil, DecisionRecord{DecisionID: "decision-1", BatchID: "batch-1", RouteID: "wiki-maintenance",
		RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch", Classification: "normal", CreatedAt: now(), Actor: "system"}); err != nil {
		t.Fatal(err)
	}
	rec := intentRecord(dispatchID, "decision-1")
	if _, err := s.Exec(`INSERT INTO dispatch_intents
		(dispatch_id, decision_id, route_id, route_revision, target_id, target_type, target_scope, resource_id, generation, idempotency_key, content_fingerprint, manifest_digest, request_version, request_json, state, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,'ready',?,?)`,
		rec.DispatchID, rec.DecisionID, rec.RouteID, rec.RouteRevision, rec.TargetID, rec.TargetType, rec.TargetScope, rec.ResourceID, rec.Generation,
		rec.IdempotencyKey, rec.ContentFingerprint, rec.ManifestDigest, rec.RequestVersion, rec.RequestJSON, rec.CreatedAt, rec.CreatedAt); err != nil {
		t.Fatalf("save intent: %v", err)
	}
	if _, err := s.Exec(`UPDATE route_runtime_state SET active_dispatch_id = ?, active_generation = ?, version = version + 1 WHERE route_id = ? AND active_dispatch_id IS NULL`,
		rec.DispatchID, rec.Generation, rec.RouteID); err != nil {
		t.Fatal(err)
	}
}

// TestE8T2MigrationLockHeartbeatRefreshes proves the M-4 heartbeat: a
// live holder's lock mtime is refreshed on every tick, so a waiter can
// never steal a lock from a migration still in progress even when it
// outlives the staleness bound.
func TestE8T2MigrationLockHeartbeatRefreshes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	lockPath := dbPath + ".migration-lock"
	origRefresh := migrationLockRefresh
	migrationLockRefresh = 20 * time.Millisecond
	defer func() { migrationLockRefresh = origRefresh }()
	release, err := acquireMigrationFileLock(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// Age the lock past the staleness bound, then let at least one tick
	// fire: the heartbeat must refresh the mtime forward.
	aged := time.Now().Add(-2 * migrationLockStaleness)
	if err := os.Chtimes(lockPath, aged, aged); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > migrationLockStaleness {
		t.Fatalf("the heartbeat must refresh a live holder's lock mtime, last refresh %v ago", time.Since(info.ModTime()))
	}
}

// TestE8T2DeadLetterRejectedGuards pins the M-3 store guard branches:
// only a rejected dispatch dead-letters; the dead-lettered shape is
// idempotent, and any other state refuses with the typed conflict.
func TestE8T2DeadLetterRejectedGuards(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")
	if err := s.DeadLetterRejected(context.Background(), "dispatch-1", "runtime", now()); err == nil || !errors.Is(err, ports.ErrStateNotEligible) {
		t.Fatalf("a ready dispatch must refuse: %v", err)
	}
	// Force the rejected shape through the guarded flow's classification
	// table (the runtime hook applies the same edge in production).
	if err := transitionInTx(t, s, "dispatch-1", records.IntentReady, records.IntentSubmitting, state.ReasonLeaseAcquired); err != nil {
		t.Fatal(err)
	}
	// The definite-rejection edge demands its receipt evidence (the same
	// guard the classified completion flow satisfies).
	tx, txErr := s.BeginTx(context.Background(), nil)
	if txErr != nil {
		t.Fatal(txErr)
	}
	if err := s.transitionWithin(context.Background(), tx, "dispatch-1", records.IntentSubmitting, records.IntentRejected, state.ReasonDefiniteRejection,
		state.IntentEvidence{Actor: "runtime", ReceiptRef: "rcpt-test-rejected"}, now(),
		`{"reason":"definite_rejection","receipt":"rcpt-test-rejected"}`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.DeadLetterRejected(context.Background(), "dispatch-1", "runtime", now()); err != nil {
		t.Fatalf("a rejected dispatch must dead-letter: %v", err)
	}
	// Idempotent for the already-dead-lettered shape.
	if err := s.DeadLetterRejected(context.Background(), "dispatch-1", "runtime", now()); err != nil {
		t.Fatalf("the dead-lettered shape must be idempotent: %v", err)
	}
	var got string
	if err := s.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = 'dispatch-1'`).Scan(&got); err != nil || got != string(records.IntentDeadLettered) {
		t.Fatalf("the dispatch must be dead-lettered: %q %v", got, err)
	}
}

// TestE8AuditFailPathGenerationFence pins the T1 deferred finding: a
// racing merge between the Fail snapshot and its transaction refuses as
// a generation conflict, never silently dropping the merged work.
func TestE8AuditFailPathGenerationFence(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")
	rec0, err := s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRouteRuntimeState(nil, "wiki-maintenance", rec0.Version, func(r *RouteRuntimeStateRecord) {
		r.ActivationState = "enabled"
		r.RouteState = "ACTIVE_CLEAN"
		r.ActiveDispatchID = "dispatch-1"
		r.DirtyGeneration = 1
	}); err != nil {
		t.Fatal(err)
	}
	seedLegacyLane(t, s, "ACTIVE_CLEAN", "dispatch-1", 1)
	// The caller fenced generation 0; the lane moved to 1 inside the
	// window — the completion must refuse.
	_, err = s.CompleteActive(context.Background(), ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: "dispatch-1", Failed: true,
		FailureBudgetRemaining: 2, ReceiptRef: "rcpt-work-1", Actor: "hermes-task", Now: now(),
		FenceGeneration: true, ExpectedDirtyGeneration: 0,
	})
	if err == nil || !errors.Is(err, ErrOptimisticConcurrency) {
		t.Fatalf("the fail path must honor the generation fence, got %v", err)
	}
}

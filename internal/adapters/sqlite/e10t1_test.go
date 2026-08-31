package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// e10t1Digest builds a syntactically valid digest placeholder.
func e10t1Digest(seed string) string {
	return "sha256:" + strings.Repeat(seed, 64)[:64]
}

// e10t1Lineage builds one ordinary dispatch lineage whose observation
// carries exactly the given changes, so CommitLineage exercises the
// ingestion path-fact writer (and with it the observation-revision
// advance).
func e10t1Lineage(suffix string, changes []ports.ObservationChange) ports.Lineage {
	now := "2026-08-26T00:00:0" + suffix + "Z"
	return ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: "obs-e10t1-" + suffix, SchemaVersion: "agent-dispatch.source-observation/v1",
			SourceType: "watchman", SourceID: "watchman-main", TriggerName: "trig", ResourceID: "vault-main",
			ObservedAt: now, ReceivedAt: now, RawPayloadDigest: e10t1Digest("a"), IngestStatus: "accepted",
			Changes: changes,
		},
		Batch: ports.BatchInput{
			BatchID: "batch-e10t1-" + suffix, RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: now, ContentFingerprint: e10t1Digest("c"),
			ObservationIDs: []string{"obs-e10t1-" + suffix},
		},
		Decision: ports.DecisionInput{
			DecisionID: "decision-e10t1-" + suffix, BatchID: "batch-e10t1-" + suffix, RouteID: "wiki-maintenance",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch",
			Classification: "normal", CreatedAt: now, Actor: "planner",
		},
		Intent: ports.IntentInput{
			DispatchID: "dispatch-e10t1-" + suffix, DecisionID: "decision-e10t1-" + suffix, RouteID: "wiki-maintenance",
			RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes_kanban",
			ResourceID: "vault-main", Generation: 1, IdempotencyKey: "agent-dispatch:v1:" + e10t1Digest(suffix),
			ContentFingerprint: e10t1Digest("c"), ManifestDigest: e10t1Digest("d"),
			RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: "{}", CreatedAt: now,
		},
	}
}

// e10t1ReleaseSlot clears the route's reserved active slot between
// test lineages so consecutive commits do not collide with the
// one-active-dispatch invariant.
func e10t1ReleaseSlot(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.Exec(`UPDATE destination_lane_state SET active_dispatch_id = NULL, active_generation = 0 WHERE route_id = 'wiki-maintenance'`); err != nil {
		t.Fatal(err)
	}
}

// TestE10T1EveryPathFactMutationAdvancesObservationRevision pins
// DUR-013: the observation revision starts at zero after the v8
// migration, every durable path-fact mutation advances it exactly once,
// and a lineage whose observation records no digest-bearing fact leaves
// it untouched.
func TestE10T1EveryPathFactMutationAdvancesObservationRevision(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	rev, err := s.ObservationRevision(ctx, "vault-main")
	if err != nil || rev != 0 {
		t.Fatalf("fresh resource must start at revision 0: %d %v", rev, err)
	}
	first := e10t1Lineage("1", []ports.ObservationChange{
		{Ordinal: 1, Path: "Inbox/a.md", Operation: "create", ExistsAfter: true, FileType: "regular", AfterDigest: e10t1Digest("1"), DigestStatus: "known"},
	})
	if err := s.CommitLineage(ctx, first); err != nil {
		t.Fatalf("first lineage: %v", err)
	}
	e10t1ReleaseSlot(t, s)
	rev, err = s.ObservationRevision(ctx, "vault-main")
	if err != nil || rev != 1 {
		t.Fatalf("one mutation must advance the revision exactly once: %d %v", rev, err)
	}
	// A lineage whose only change carries no digest (an unhashed modify
	// keeps the prior fact) mutates nothing: the revision holds.
	noFact := e10t1Lineage("2", []ports.ObservationChange{
		{Ordinal: 1, Path: "Inbox/unhashed.md", Operation: "modify", ExistsAfter: true, FileType: "regular", DigestStatus: "unavailable"},
	})
	if err := s.CommitLineage(ctx, noFact); err != nil {
		t.Fatalf("no-fact lineage: %v", err)
	}
	e10t1ReleaseSlot(t, s)
	rev, err = s.ObservationRevision(ctx, "vault-main")
	if err != nil || rev != 1 {
		t.Fatalf("a no-fact lineage must not advance the revision: %d %v", rev, err)
	}
	second := e10t1Lineage("3", []ports.ObservationChange{
		{Ordinal: 1, Path: "Inbox/a.md", Operation: "modify", ExistsAfter: true, FileType: "regular", AfterDigest: e10t1Digest("2"), DigestStatus: "known"},
	})
	if err := s.CommitLineage(ctx, second); err != nil {
		t.Fatalf("second lineage: %v", err)
	}
	rev, err = s.ObservationRevision(ctx, "vault-main")
	if err != nil || rev != 2 {
		t.Fatalf("the second mutation must advance again: %d %v", rev, err)
	}
}

// TestE10T1ReplacePathFactsCAS pins DUR-014/DUR-015 at the store: the
// snapshot replacement and the revision advancement are one atomic
// transaction gated on the caller's expected revision; a stale
// expectation refuses with the typed conflict and leaves the stored —
// newer — facts untouched; a missing resource row is its own typed
// failure, never a silent empty snapshot.
func TestE10T1ReplacePathFactsCAS(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seed := []ports.PathFact{
		{Path: "Inbox/seed.md", Digest: e10t1Digest("s"), Exists: true},
	}
	if err := s.ReplacePathFacts(ctx, "vault-main", 0, seed, "2026-08-26T00:00:00Z"); err != nil {
		t.Fatalf("initial fenced replacement: %v", err)
	}
	rev, err := s.ObservationRevision(ctx, "vault-main")
	if err != nil || rev != 1 {
		t.Fatalf("the replacement must advance the revision atomically: %d %v", rev, err)
	}
	facts, err := s.LoadPathFacts(ctx, "vault-main")
	if err != nil || len(facts) != 1 {
		t.Fatalf("seed snapshot: %v %v", facts, err)
	}

	// A newer ingestion fact lands (revision 2); the reconciliation's
	// stale expectation of 1 must refuse and preserve it.
	newer := e10t1Lineage("1", []ports.ObservationChange{
		{Ordinal: 1, Path: "Inbox/newer.md", Operation: "create", ExistsAfter: true, FileType: "regular", AfterDigest: e10t1Digest("n"), DigestStatus: "known"},
	})
	if err := s.CommitLineage(ctx, newer); err != nil {
		t.Fatalf("newer lineage: %v", err)
	}
	stale := []ports.PathFact{
		{Path: "Inbox/stale-snapshot.md", Digest: e10t1Digest("x"), Exists: true},
	}
	err = s.ReplacePathFacts(ctx, "vault-main", 1, stale, "2026-08-26T00:01:00Z")
	if !errors.Is(err, ports.ErrObservationConflict) {
		t.Fatalf("a stale expectation must refuse with the typed conflict, got %v", err)
	}
	facts, err = s.LoadPathFacts(ctx, "vault-main")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := facts["Inbox/newer.md"]; !ok {
		t.Fatalf("the newer fact must survive the refused replacement: %v", facts)
	}
	if _, ok := facts["Inbox/stale-snapshot.md"]; ok {
		t.Fatalf("the refused snapshot must not be stored: %v", facts)
	}
	rev, err = s.ObservationRevision(ctx, "vault-main")
	if err != nil || rev != 2 {
		t.Fatalf("the refused replacement must not advance the revision: %d %v", rev, err)
	}

	// The current expectation replaces atomically: new facts in, old
	// facts out, revision advanced, all visible together.
	current := []ports.PathFact{
		{Path: "Inbox/newer.md", Digest: e10t1Digest("n"), Exists: true},
		{Path: "Inbox/second.md", Digest: e10t1Digest("2"), Exists: true},
	}
	if err := s.ReplacePathFacts(ctx, "vault-main", 2, current, "2026-08-26T00:02:00Z"); err != nil {
		t.Fatalf("current fenced replacement: %v", err)
	}
	facts, err = s.LoadPathFacts(ctx, "vault-main")
	if err != nil || len(facts) != 2 {
		t.Fatalf("the replacement must swap the whole snapshot: %v %v", facts, err)
	}
	if _, ok := facts["Inbox/seed.md"]; ok {
		t.Fatalf("the seed fact must be replaced away: %v", facts)
	}
	rev, err = s.ObservationRevision(ctx, "vault-main")
	if err != nil || rev != 3 {
		t.Fatalf("the replacement must advance once more: %d %v", rev, err)
	}

	// A missing resource row is the typed not-found failure.
	if err := s.ReplacePathFacts(ctx, "absent", 0, nil, "2026-08-26T00:03:00Z"); !errors.Is(err, ports.ErrResourceNotFound) {
		t.Fatalf("a missing resource must fail typed, got %v", err)
	}
	if _, err := s.ObservationRevision(ctx, "absent"); !errors.Is(err, ports.ErrResourceNotFound) {
		t.Fatalf("observation read of a missing resource must fail typed, got %v", err)
	}
}

// TestE10T1MigrationV8BackfillAndIntegrity pins the v8 unit: existing
// resources backfill to revision 0, the CHECK refuses negative writes at
// the storage boundary, and a database whose v8 unit died before the
// ledger commit (the crash window) migrates cleanly on reopen.
func TestE10T1MigrationV8BackfillAndIntegrity(t *testing.T) {
	s := openTestStore(t)
	var rev int
	if err := s.QueryRow(`SELECT observation_revision FROM resources WHERE resource_id = 'vault-main'`).Scan(&rev); err != nil || rev != 0 {
		t.Fatalf("v8 backfill must default existing resources to 0: %d %v", rev, err)
	}
	if _, err := s.Exec(`UPDATE resources SET observation_revision = -1 WHERE resource_id = 'vault-main'`); err == nil {
		t.Fatal("the CHECK constraint must refuse a negative revision")
	}

	// The crash window: against the complete v7-era shape, the v8 unit's
	// SQL runs but the ledger insert never commits (the harness dies
	// inside the open transaction, and the WAL discards the unit).
	// Reopen and migrate: the ledger gap heals by re-applying the unit.
	path := t.TempDir() + "/v8-crash.db"
	crashed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := crashed.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// Rewind to the v7-era shape: the v8 column is gone and the ledger
	// no longer records v8, exactly as a pre-upgrade (or interrupted-
	// upgrade) database would look.
	for _, stmt := range []string{
		`DROP TABLE watch_bindings`,
		`DROP TABLE contract_state`,
		`DROP TABLE destination_lane_state`,
		`DROP TABLE work_receipts`,
		`CREATE TABLE work_receipts (
	receipt_id      TEXT PRIMARY KEY,
	dispatch_id     TEXT NOT NULL REFERENCES dispatch_intents(dispatch_id),
	run_id          TEXT NOT NULL,
	resource_id     TEXT NOT NULL REFERENCES resources(resource_id),
	status          TEXT NOT NULL CHECK (status IN ('begun','completed','failed')),
	failure_code    TEXT CHECK (failure_code IN ('agent_error','canceled','timeout','environment_error')),
	external_task_id TEXT,
	base_revision   TEXT,
	result_revision TEXT,
	changes_json    TEXT NOT NULL DEFAULT '[]',
	submitted_at    TEXT NOT NULL,
	begun_at        TEXT,
	validation_state TEXT NOT NULL CHECK (validation_state IN ('valid','invalid','incomplete')),
	validation_reasons_json TEXT NOT NULL DEFAULT '[]',
	route_revision  TEXT NOT NULL DEFAULT '',
	UNIQUE (dispatch_id, run_id)
)`,
		`CREATE INDEX idx_work_receipts_route_revision ON work_receipts(route_revision)`,
		`DROP INDEX idx_child_dispatches_lane`,
		`DROP TABLE child_dispatches`,
		`DROP TABLE destination_revisions`,
		`DROP INDEX idx_aggregate_events_route`,
		`DROP TABLE aggregate_events`,
		`DROP INDEX idx_notification_attempts_notification`,
		`DROP TABLE notification_attempts`,
		`DROP INDEX idx_notification_events_state`,
		`DROP INDEX idx_notification_events_route`,
		`DROP TABLE notification_events`,
		`DROP INDEX idx_route_baselines_resource`,
		`DROP TABLE route_baselines`,
		`DROP INDEX idx_serialization_group_members_group`,
		`DROP TABLE serialization_group_members`,
		`DROP TABLE serialization_groups`,
		`ALTER TABLE resources DROP COLUMN observation_revision`,
		`ALTER TABLE route_runtime_state DROP COLUMN capability_fingerprint`,
		`ALTER TABLE change_batches DROP COLUMN selected_destinations_json`,
		`DELETE FROM schema_migrations WHERE version IN (8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18)`,
	} {
		if _, err := crashed.Exec(stmt); err != nil {
			t.Fatalf("rewind %q: %v", stmt, err)
		}
	}
	var v8unit Migration
	for _, m := range Migrations {
		if m.Version == 8 {
			v8unit = m
		}
	}
	if err := crashed.ApplyMigrationSQLWithoutLedgerForHarness(v8unit); err != nil {
		t.Fatalf("harness apply v8 inside the crash window: %v", err)
	}
	crashed.Close()
	healed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer healed.Close()
	if err := healed.Migrate(t.TempDir()); err != nil {
		t.Fatalf("heal after the interrupted v8 window: %v", err)
	}
	version, err := healed.SchemaVersion()
	if err != nil || version != MaxSchemaVersion {
		t.Fatalf("the healed ledger must reach the current baseline: %d %v", version, err)
	}
	if err := healed.RegisterResource(nil, "post-crash", "res-rev-2", "/srv/other", "/srv/other", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	revAfter, err := healed.ObservationRevision(context.Background(), "post-crash")
	if err != nil || revAfter != 0 {
		t.Fatalf("the healed store must serve the observation revision: %d %v", revAfter, err)
	}
}

// TestE10T1RetentionPurgeAdvancesObservationRevision pins the round-1
// review remediation: the retention cut is a durable path-fact mutation,
// so every resource whose facts the prune deletes advances its
// observation revision in the same transaction (DUR-013) — and a
// resource whose facts all survive does not.
func TestE10T1RetentionPurgeAdvancesObservationRevision(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.ReplacePathFacts(ctx, "vault-main", 0, []ports.PathFact{
		{Path: "Inbox/old.md", Digest: e10t1Digest("o"), Exists: true},
		{Path: "Inbox/keep.md", Digest: e10t1Digest("k"), Exists: true},
	}, "2020-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	// The retained fact was re-observed recently (as ingestion would);
	// the fenced snapshot wrote one observed_at for both rows, so the
	// fresh observation is recorded directly.
	if _, err := s.Exec(`UPDATE path_facts SET observed_at = '2026-08-01T00:00:00Z' WHERE path = 'Inbox/keep.md'`); err != nil {
		t.Fatal(err)
	}
	before, err := s.ObservationRevision(ctx, "vault-main")
	if err != nil || before != 1 {
		t.Fatalf("baseline revision: %d %v", before, err)
	}
	counts, err := s.ExecutePrune(ctx, PruneCutoffs{Observations: "2024-01-01T00:00:00Z"}, "operator", "retention test", "2026-08-26T00:00:00Z")
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if counts.PathFacts != 1 {
		t.Fatalf("the stale fact must be the only purged row: %+v", counts)
	}
	after, err := s.ObservationRevision(ctx, "vault-main")
	if err != nil || after != before+1 {
		t.Fatalf("the retention cut must advance the observation revision once: %d -> %d %v", before, after, err)
	}
	facts, err := s.LoadPathFacts(ctx, "vault-main")
	if err != nil || len(facts) != 1 {
		t.Fatalf("only the retained fact may survive: %v %v", facts, err)
	}
	if _, ok := facts["Inbox/keep.md"]; !ok {
		t.Fatalf("the fresh fact must survive the retention cut: %v", facts)
	}
	// A prune that deletes no facts advances nothing.
	again, err := s.ExecutePrune(ctx, PruneCutoffs{Observations: "2024-01-01T00:00:00Z"}, "operator", "retention test", "2026-08-26T00:01:00Z")
	if err != nil {
		t.Fatalf("second prune: %v", err)
	}
	if again.PathFacts != 0 {
		t.Fatalf("the second prune must cut no facts: %+v", again)
	}
	idle, err := s.ObservationRevision(ctx, "vault-main")
	if err != nil || idle != after {
		t.Fatalf("a fact-free prune must not advance the revision: %d %v", idle, err)
	}
}

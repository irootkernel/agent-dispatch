package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E14-T2 (DUR-013 through DUR-017, TST-002, OPS-009): the disabled-route
// baseline record and its observation-fenced snapshot transaction,
// proven against real SQLite files.

// e14t2Open opens and migrates a real SQLite file.
func e14t2Open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return s
}

func e14t2Baseline(facts int, revision int64, route string) ports.RouteBaselineInput {
	return ports.RouteBaselineInput{
		RouteID: route, ResourceID: "vault-main",
		ObservationRevision: revision, FactCount: facts,
		SnapshotSHA256: "sha256:" + e10t1Digest("b"),
		RouteRevision:  "route-rev-e14t2", PolicyRevision: "policy-rev-e14t2",
		Reason: "initial", EstablishedAt: "2026-08-31T00:00:00Z",
	}
}

// TestE14T2MigrationAppliesRouteBaselines proves migration v17 applies
// on a fresh database and advances MaxSchemaVersion (TST-002, OPS-009).
func TestE14T2MigrationAppliesRouteBaselines(t *testing.T) {
	s := e14t2Open(t)
	if MaxSchemaVersion < 17 {
		t.Fatalf("migration v17 must be registered: max is %d", MaxSchemaVersion)
	}
	var name string
	if err := s.QueryRow(`SELECT name FROM schema_migrations WHERE version = 17`).Scan(&name); err != nil {
		t.Fatalf("migration v17 must be recorded: %v", err)
	}
	if name != "route-baselines" {
		t.Fatalf("migration v17 name mismatch: %q", name)
	}
}

// TestE14T2ReplacePathFactsWithBaselineCommitsAtomically proves the
// fenced transaction: snapshot, revision advancement, and baseline row
// commit together, a rerun replaces the row, and no production row is
// touched (DUR-014/DUR-017, AC-1004's zero-row clause).
func TestE14T2ReplacePathFactsWithBaselineCommitsAtomically(t *testing.T) {
	s := e14t2Open(t)
	ctx := context.Background()
	register := &ports.ResourceRegistrationInput{
		ResourceID: "vault-main", Revision: "route-rev-e14t2",
		Root: "/srv/vault", CanonicalRoot: "/srv/vault", FileScope: "markdown", GitMode: "disabled",
	}
	facts := []ports.PathFact{
		{Path: "Inbox/a.md", Digest: e10t1Digest("a"), Exists: true},
		{Path: "Notes/b.md", Digest: e10t1Digest("b"), Exists: true},
	}
	if err := s.ReplacePathFactsWithBaseline(ctx, 0, facts, e14t2Baseline(2, 1, "wiki"), register, "2026-08-31T00:00:00Z"); err != nil {
		t.Fatalf("the fenced baseline commit: %v", err)
	}
	if rev, err := s.ObservationRevision(ctx, "vault-main"); err != nil || rev != 1 {
		t.Fatalf("the revision must advance to 1: %d %v", rev, err)
	}
	stored, err := s.LoadPathFacts(ctx, "vault-main")
	if err != nil || len(stored) != 2 {
		t.Fatalf("the snapshot must hold both facts: %d %v", len(stored), err)
	}
	rec, err := s.LoadRouteBaseline(ctx, "wiki")
	if err != nil || rec == nil {
		t.Fatalf("the baseline row must exist: %v %v", rec, err)
	}
	if rec.ObservationRevision != 1 || rec.FactCount != 2 || rec.Reason != "initial" || rec.RouteRevision != "route-rev-e14t2" {
		t.Fatalf("the baseline evidence is wrong: %+v", rec)
	}
	// A rerun replaces the row instead of duplicating work: the new
	// revision is 2 and the fact set narrowed to one file.
	rerun := []ports.PathFact{{Path: "Inbox/a.md", Digest: e10t1Digest("a"), Exists: true}}
	if err := s.ReplacePathFactsWithBaseline(ctx, 1, rerun, e14t2Baseline(1, 2, "wiki"), nil, "2026-08-31T00:01:00Z"); err != nil {
		t.Fatalf("the rerun commit: %v", err)
	}
	stored, err = s.LoadPathFacts(ctx, "vault-main")
	if err != nil || len(stored) != 1 {
		t.Fatalf("the rerun snapshot must replace the fact set: %d %v", len(stored), err)
	}
	rec, err = s.LoadRouteBaseline(ctx, "wiki")
	if err != nil || rec == nil || rec.ObservationRevision != 2 || rec.FactCount != 1 {
		t.Fatalf("the rerun must replace the baseline row: %+v %v", rec, err)
	}
	// The zero-production-row guarantee is structural and asserted by
	// count: no decision, intent, receipt, runtime, or notification row.
	for table := range map[string]int{
		"policy_decisions": 0, "dispatch_intents": 0, "dispatch_receipts": 0,
		"work_receipts": 0, "route_runtime_state": 0, "notification_events": 0,
	} {
		var n int
		if err := s.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("the baseline must create zero rows in %s: %d", table, n)
		}
	}
}

// TestE14T2BaselineFenceRefusesConcurrentMutation proves the observation
// fence on the baseline path (DUR-015): a stale expected revision
// refuses with the typed conflict and leaves the previous snapshot and
// baseline untouched.
func TestE14T2BaselineFenceRefusesConcurrentMutation(t *testing.T) {
	s := e14t2Open(t)
	ctx := context.Background()
	register := &ports.ResourceRegistrationInput{
		ResourceID: "vault-main", Revision: "route-rev-e14t2",
		Root: "/srv/vault", CanonicalRoot: "/srv/vault", FileScope: "markdown", GitMode: "disabled",
	}
	first := []ports.PathFact{{Path: "Inbox/a.md", Digest: e10t1Digest("a"), Exists: true}}
	if err := s.ReplacePathFactsWithBaseline(ctx, 0, first, e14t2Baseline(1, 1, "wiki"), register, "2026-08-31T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	// A concurrent durable mutation advanced the revision to 2.
	if err := s.ReplacePathFacts(ctx, "vault-main", 1, []ports.PathFact{{Path: "Inbox/z.md", Digest: e10t1Digest("z"), Exists: true}}, "2026-08-31T00:00:30Z"); err != nil {
		t.Fatal(err)
	}
	stale := []ports.PathFact{{Path: "Inbox/a.md", Digest: e10t1Digest("a"), Exists: true}}
	err := s.ReplacePathFactsWithBaseline(ctx, 1, stale, e14t2Baseline(1, 2, "wiki"), nil, "2026-08-31T00:01:00Z")
	if !errors.Is(err, ports.ErrObservationConflict) {
		t.Fatalf("the stale fence must refuse with the typed conflict: %v", err)
	}
	// The newer facts and the previous baseline stand.
	stored, lerr := s.LoadPathFacts(ctx, "vault-main")
	if lerr != nil || len(stored) != 1 {
		t.Fatalf("the concurrent facts must stand: %d %v", len(stored), lerr)
	}
	if _, ok := stored["Inbox/z.md"]; !ok {
		t.Fatalf("the newer fact must survive the refused baseline: %v", stored)
	}
	rec, lerr := s.LoadRouteBaseline(ctx, "wiki")
	if lerr != nil || rec == nil || rec.ObservationRevision != 1 {
		t.Fatalf("the previous baseline must be intact: %+v %v", rec, lerr)
	}
}

// TestE14T2BaselineRegistersCleanHostResource proves the clean-host
// posture (ADR-0020): a database with no resource row materializes it
// inside the fenced transaction, and a later registration never
// rewrites an existing row.
func TestE14T2BaselineRegistersCleanHostResource(t *testing.T) {
	s := e14t2Open(t)
	ctx := context.Background()
	register := &ports.ResourceRegistrationInput{
		ResourceID: "vault-main", Revision: "route-rev-e14t2",
		Root: "/srv/vault", CanonicalRoot: "/srv/vault", FileScope: "markdown", GitMode: "disabled",
	}
	if err := s.ReplacePathFactsWithBaseline(ctx, 0, []ports.PathFact{{Path: "a.md", Digest: e10t1Digest("a"), Exists: true}}, e14t2Baseline(1, 1, "wiki"), register, "2026-08-31T00:00:00Z"); err != nil {
		t.Fatalf("the clean-host baseline: %v", err)
	}
	// The trusted registration survives a second baseline whose carried
	// registration differs: ON CONFLICT DO NOTHING keeps the stored row.
	other := *register
	other.Revision = "route-rev-later"
	other.Root = "/elsewhere"
	if err := s.ReplacePathFactsWithBaseline(ctx, 1, []ports.PathFact{{Path: "a.md", Digest: e10t1Digest("a"), Exists: true}}, e14t2Baseline(1, 2, "wiki"), &other, "2026-08-31T00:01:00Z"); err != nil {
		t.Fatal(err)
	}
	var root string
	if err := s.QueryRow(`SELECT root FROM resources WHERE resource_id = 'vault-main'`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if root != "/srv/vault" {
		t.Fatalf("an existing trusted registration must never be rewritten: %q", root)
	}
}

// TestE14T2BaselineTransactionRefusesActivationFlip proves the
// in-transaction re-check of the runtime gate half: an activation
// flipped away from disabled inside the enumeration window refuses the
// commit with the typed state conflict and leaves the previous
// baseline untouched (E14-T2 round-1 F002).
func TestE14T2BaselineTransactionRefusesActivationFlip(t *testing.T) {
	s := e14t2Open(t)
	ctx := context.Background()
	register := &ports.ResourceRegistrationInput{
		ResourceID: "vault-main", Revision: "route-rev-e14t2",
		Root: "/srv/vault", CanonicalRoot: "/srv/vault", FileScope: "markdown", GitMode: "disabled",
	}
	if err := s.ReplacePathFactsWithBaseline(ctx, 0, []ports.PathFact{{Path: "a.md", Digest: e10t1Digest("a"), Exists: true}}, e14t2Baseline(1, 1, "wiki"), register, "2026-08-31T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	// A materialized route row whose activation flips before the commit.
	if err := s.RegisterRoute(nil, "wiki", "route-rev-e14t2", "policy-rev-e14t2", "vault-main", "hermes-kanban-main", "{}", "2026-08-31T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRouteActivation(ctx, "wiki", "enabled", "route-rev-e14t2", "", "2026-08-31T00:01:00Z"); err != nil {
		t.Fatal(err)
	}
	err := s.ReplacePathFactsWithBaseline(ctx, 1, []ports.PathFact{{Path: "a.md", Digest: e10t1Digest("a"), Exists: true}}, e14t2Baseline(1, 2, "wiki"), nil, "2026-08-31T00:02:00Z")
	if !errors.Is(err, ports.ErrStateNotEligible) {
		t.Fatalf("the activation flip must refuse the commit with the typed conflict: %v", err)
	}
	rec, lerr := s.LoadRouteBaseline(ctx, "wiki")
	if lerr != nil || rec == nil || rec.ObservationRevision != 1 {
		t.Fatalf("the previous baseline must stand after the refusal: %+v %v", rec, lerr)
	}
}

// TestE14T2BaselineTransactionRefusesRouteHold proves the in-transaction
// re-check refuses a route hold raised inside the enumeration window:
// an uncertain or quarantined route_state refuses the commit with the
// typed state conflict even while the runtime activation stays disabled
// (CLI-017; cold-validation round-1 F002).
func TestE14T2BaselineTransactionRefusesRouteHold(t *testing.T) {
	for name, hold := range map[string]string{
		"uncertain":   "UNCERTAIN",
		"quarantined": "QUARANTINED",
	} {
		t.Run(name, func(t *testing.T) {
			s := e14t2Open(t)
			ctx := context.Background()
			register := &ports.ResourceRegistrationInput{
				ResourceID: "vault-main", Revision: "route-rev-e14t2",
				Root: "/srv/vault", CanonicalRoot: "/srv/vault", FileScope: "markdown", GitMode: "disabled",
			}
			if err := s.ReplacePathFactsWithBaseline(ctx, 0, []ports.PathFact{{Path: "a.md", Digest: e10t1Digest("a"), Exists: true}}, e14t2Baseline(1, 1, "wiki"), register, "2026-08-31T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
			// A materialized route row whose hold rises before the commit
			// while its activation half stays disabled.
			if err := s.RegisterRoute(nil, "wiki", "route-rev-e14t2", "policy-rev-e14t2", "vault-main", "hermes-kanban-main", "{}", "2026-08-31T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
			if err := s.InitializeRouteState(nil, "wiki"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Exec(`UPDATE route_runtime_state SET route_state = ? WHERE route_id = ?`, hold, "wiki"); err != nil {
				t.Fatal(err)
			}
			err := s.ReplacePathFactsWithBaseline(ctx, 1, []ports.PathFact{{Path: "a.md", Digest: e10t1Digest("a"), Exists: true}}, e14t2Baseline(1, 2, "wiki"), nil, "2026-08-31T00:02:00Z")
			if !errors.Is(err, ports.ErrStateNotEligible) {
				t.Fatalf("a %s route must refuse the commit with the typed conflict: %v", name, err)
			}
			rec, lerr := s.LoadRouteBaseline(ctx, "wiki")
			if lerr != nil || rec == nil || rec.ObservationRevision != 1 {
				t.Fatalf("the previous baseline must stand after the refusal: %+v %v", rec, lerr)
			}
		})
	}
}

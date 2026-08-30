package reconcile

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/localfs"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/domain/policy"
	"github.com/irootkernel/agent-dispatch/internal/ports"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/crashbinbuild"
)

// E14-T2 service-level proof (CLI-017, DUR-013 through DUR-017, AC-1004,
// TST-004): the guards, the clean-host posture, rerun convergence, and
// the interrupted-attempt crash window, against a real SQLite file.

func e14t2WriteVault(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func e14t2Service(t *testing.T, store ports.BaselineStore, root string) *BaselineService {
	t.Helper()
	resolver, err := localfs.NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := policy.NewEngine([]string{"**/*.md"}, nil, nil, nil, policy.CaseSensitive)
	if err != nil {
		t.Fatal(err)
	}
	return &BaselineService{
		Store: store, Resolver: resolver, Engine: engine,
		FileScope: "markdown", MaxHash: 1 << 20,
		ResourceID: "vault-main", RouteID: "wiki-maintenance",
		RouteRevision: "route-rev-e14t2", PolicyRevision: "policy-rev-e14t2",
		ResourceRegistration: &ports.ResourceRegistrationInput{
			ResourceID: "vault-main", Revision: "route-rev-e14t2",
			Root: root, CanonicalRoot: root, FileScope: "markdown", GitMode: "disabled",
		},
		Now: time.Now,
	}
}

func e14t2OpenStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return s
}

// e14t2Count proves the zero-production-row guarantee by table count.
func e14t2Count(t *testing.T, s *sqlite.Store, table string) int {
	t.Helper()
	var n int
	if err := s.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestE14T2BaselineCleanHostEstablishesSnapshot proves the clean-host
// posture: no route row, no resource row — the fenced transaction
// materializes the resource and stores the complete baseline with zero
// production rows (ADR-0020, DUR-017, AC-1004).
func TestE14T2BaselineCleanHostEstablishesSnapshot(t *testing.T) {
	root := e14t2WriteVault(t, map[string]string{"Inbox/a.md": "alpha", "Notes/b.md": "beta"})
	s := e14t2OpenStore(t)
	service := e14t2Service(t, s, root)
	out, err := service.Run(context.Background(), "wiki-maintenance", "initial")
	if err != nil {
		t.Fatalf("the clean-host baseline: %v", err)
	}
	if !out.Baseline || !out.SnapshotStored || out.Enumerated != 2 || out.FactCount != 2 || out.ObservationRevision != 1 {
		t.Fatalf("the clean-host result is wrong: %+v", out)
	}
	if out.SnapshotSHA256 == "" || out.EstablishedAt == "" {
		t.Fatalf("the baseline evidence must carry its digest and timestamp: %+v", out)
	}
	rec, err := s.LoadRouteBaseline(context.Background(), "wiki-maintenance")
	if err != nil || rec == nil {
		t.Fatalf("the baseline row must exist: %v %v", rec, err)
	}
	if rec.SnapshotSHA256 != out.SnapshotSHA256 || rec.ObservationRevision != 1 {
		t.Fatalf("the stored evidence diverges from the result: %+v", rec)
	}
	for table := range map[string]struct{}{
		"policy_decisions": {}, "dispatch_intents": {}, "dispatch_receipts": {},
		"work_receipts": {}, "route_runtime_state": {}, "notification_events": {},
	} {
		if n := e14t2Count(t, s, table); n != 0 {
			t.Fatalf("the baseline must create zero rows in %s: %d", table, n)
		}
	}
}

// TestE14T2BaselineRerunConverges proves rerun safety: an unchanged
// rerun replaces the baseline row (no duplicate work), a changed vault
// refreshes it, and the snapshot stays complete (AC-1004's rerun
// clause).
func TestE14T2BaselineRerunConverges(t *testing.T) {
	root := e14t2WriteVault(t, map[string]string{"Inbox/a.md": "alpha"})
	s := e14t2OpenStore(t)
	service := e14t2Service(t, s, root)
	first, err := service.Run(context.Background(), "wiki-maintenance", "initial")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Run(context.Background(), "wiki-maintenance", "initial")
	if err != nil {
		t.Fatalf("the unchanged rerun: %v", err)
	}
	if !second.SnapshotStored || second.ObservationRevision != 2 || second.Compared != 1 {
		t.Fatalf("the unchanged rerun must converge: %+v", second)
	}
	if second.SnapshotSHA256 != first.SnapshotSHA256 {
		t.Fatalf("identical content must produce identical baseline digests: %s %s", first.SnapshotSHA256, second.SnapshotSHA256)
	}
	if n := e14t2Count(t, s, "route_baselines"); n != 1 {
		t.Fatalf("one route keeps exactly one baseline row: %d", n)
	}
	if err := os.WriteFile(filepath.Join(root, "Inbox", "c.md"), []byte("gamma"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := service.Run(context.Background(), "wiki-maintenance", "initial")
	if err != nil {
		t.Fatal(err)
	}
	if !third.SnapshotStored || third.FactCount != 2 || third.ObservationRevision != 3 {
		t.Fatalf("the refreshed baseline: %+v", third)
	}
}

// TestE14T2BaselineGuardsRefuseNonDisabledStates proves CLI-017: a
// YAML-enabled route, an enabled or paused runtime activation, and an
// uncertain or quarantined route state all fail closed with
// ports.ErrStateNotEligible before any durable write.
func TestE14T2BaselineGuardsRefuseNonDisabledStates(t *testing.T) {
	root := e14t2WriteVault(t, map[string]string{"Inbox/a.md": "alpha"})

	t.Run("configuration enabled", func(t *testing.T) {
		s := e14t2OpenStore(t)
		service := e14t2Service(t, s, root)
		service.ConfigEnabled = true
		_, err := service.Run(context.Background(), "wiki-maintenance", "initial")
		if !errors.Is(err, ports.ErrStateNotEligible) {
			t.Fatalf("an enabled configuration key must refuse: %v", err)
		}
		if n := e14t2Count(t, s, "route_baselines"); n != 0 {
			t.Fatalf("the refusal must write nothing: %d", n)
		}
	})

	for name, setup := range map[string]func(t *testing.T, s *sqlite.Store){
		"runtime enabled": func(t *testing.T, s *sqlite.Store) {
			if err := s.SetRouteActivation(context.Background(), "wiki-maintenance", "enabled", "route-rev-e14t2", "", "2026-08-31T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
		},
		"runtime paused": func(t *testing.T, s *sqlite.Store) {
			if err := s.SetRouteActivation(context.Background(), "wiki-maintenance", "paused", "route-rev-e14t2", "", "2026-08-31T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := e14t2OpenStore(t)
			e14t2Register(t, s)
			setup(t, s)
			service := e14t2Service(t, s, root)
			_, err := service.Run(context.Background(), "wiki-maintenance", "initial")
			if !errors.Is(err, ports.ErrStateNotEligible) {
				t.Fatalf("%s must refuse baseline-only: %v", name, err)
			}
			if n := e14t2Count(t, s, "route_baselines"); n != 0 {
				t.Fatalf("the refusal must write nothing: %d", n)
			}
		})
	}

	for name, hold := range map[string]string{
		"uncertain":   "UNCERTAIN",
		"quarantined": "QUARANTINED",
	} {
		t.Run("route "+name, func(t *testing.T) {
			s := e14t2OpenStore(t)
			e14t2Register(t, s)
			if _, err := s.Exec(`UPDATE route_runtime_state SET route_state = ? WHERE route_id = ?`, hold, "wiki-maintenance"); err != nil {
				t.Fatal(err)
			}
			service := e14t2Service(t, s, root)
			_, err := service.Run(context.Background(), "wiki-maintenance", "initial")
			if !errors.Is(err, ports.ErrStateNotEligible) {
				t.Fatalf("a %s route must refuse baseline-only: %v", name, err)
			}
		})
	}
}

// e14t2Register materializes the route rows the runtime guards read.
func e14t2Register(t *testing.T, s *sqlite.Store) {
	t.Helper()
	if err := s.RegisterResource(nil, "vault-main", "route-rev-e14t2", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki-maintenance", "route-rev-e14t2", "policy-rev-e14t2", "vault-main", "hermes-kanban-main", "{}", "2026-08-31T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki-maintenance"); err != nil {
		t.Fatal(err)
	}
}

// TestE14T2BaselineDisabledMaterializedRowSucceeds proves the
// materialized-row posture: a registered, disabled route baseline is
// the ordinary rerunnable case.
func TestE14T2BaselineDisabledMaterializedRowSucceeds(t *testing.T) {
	root := e14t2WriteVault(t, map[string]string{"Inbox/a.md": "alpha"})
	s := e14t2OpenStore(t)
	e14t2Register(t, s)
	service := e14t2Service(t, s, root)
	out, err := service.Run(context.Background(), "wiki-maintenance", "initial")
	if err != nil {
		t.Fatalf("a registered disabled route must baseline: %v", err)
	}
	if !out.SnapshotStored {
		t.Fatalf("the baseline must store: %+v", out)
	}
	if n := e14t2Count(t, s, "policy_decisions") + e14t2Count(t, s, "dispatch_intents") + e14t2Count(t, s, "notification_events"); n != 0 {
		t.Fatalf("a materialized disabled route still creates zero production rows: %d", n)
	}
}

// TestE14T2InterruptedBaselineRerunConverges proves the crash window
// (TST-004, AC-1004): a process dying after the enumeration, before the
// fenced transaction, leaves no partial state, and the rerun converges
// on the complete new baseline.
func TestE14T2InterruptedBaselineRerunConverges(t *testing.T) {
	root := e14t2WriteVault(t, map[string]string{"Inbox/a.md": "alpha"})
	dir := t.TempDir()
	db := filepath.Join(dir, "state.db")
	bin := crashbinbuild.Build(t)
	run := func(args ...string) *exec.Cmd {
		return exec.Command(bin, args...)
	}
	// Establish the first complete baseline.
	first := run("baseline", "--db", db, "--vault", root, "--route", "wiki-maintenance", "--resource", "vault-main")
	if out, err := first.CombinedOutput(); err != nil {
		t.Fatalf("the first baseline: %v %s", err, out)
	}
	// The vault changes; the rerun dies after the enumeration, before
	// the fenced transaction — the interrupted attempt.
	if err := os.WriteFile(filepath.Join(root, "Inbox", "b.md"), []byte("beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	crashed := run("baseline", "--db", db, "--vault", root, "--route", "wiki-maintenance", "--resource", "vault-main", "--window", "die-before-write")
	out, err := crashed.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "dying before the baseline transaction") {
		t.Fatalf("the crash window must die hard before the transaction: %v %s", err, out)
	}
	// Nothing partial: the previous baseline and its snapshot stand.
	s := e14t2OpenExisting(t, db)
	rec, err := s.LoadRouteBaseline(context.Background(), "wiki-maintenance")
	if err != nil || rec == nil {
		t.Fatalf("the previous baseline must survive the crash: %v %v", rec, err)
	}
	if rec.ObservationRevision != 1 || rec.FactCount != 1 {
		t.Fatalf("the interrupted attempt must leave the previous baseline intact: %+v", rec)
	}
	stored, err := s.LoadPathFacts(context.Background(), "vault-main")
	if err != nil || len(stored) != 1 {
		t.Fatalf("the interrupted attempt must leave the previous snapshot intact: %d %v", len(stored), err)
	}
	s.Close()
	// The rerun converges on the complete new baseline.
	rerun := run("baseline", "--db", db, "--vault", root, "--route", "wiki-maintenance", "--resource", "vault-main")
	if out, err := rerun.CombinedOutput(); err != nil {
		t.Fatalf("the converging rerun: %v %s", err, out)
	}
	s = e14t2OpenExisting(t, db)
	defer s.Close()
	rec, err = s.LoadRouteBaseline(context.Background(), "wiki-maintenance")
	if err != nil || rec == nil || rec.ObservationRevision != 2 || rec.FactCount != 2 {
		t.Fatalf("the rerun must converge on the complete new baseline: %+v %v", rec, err)
	}
}

// e14t2OpenExisting opens an already-migrated database without a second
// migration backup directory.
func e14t2OpenExisting(t *testing.T, db string) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestE14T2SnapshotDigestIsDelimiterUnambiguous proves the canonical
// projection cannot be forged by crafted filenames: a path containing
// the old framing bytes (tab and newline) never reproduces another
// snapshot's digest (E14-T2 round-1 F003).
func TestE14T2SnapshotDigestIsDelimiterUnambiguous(t *testing.T) {
	honest := snapshotDigest([]ports.PathFact{
		{Path: "a.md", Digest: "sha256:one"},
		{Path: "b.md", Digest: "sha256:two"},
	})
	crafted := snapshotDigest([]ports.PathFact{
		{Path: "a.md\tb.md\n", Digest: ""},
		{Path: "", Digest: ""},
	})
	if honest == crafted {
		t.Fatalf("a crafted delimiter-bearing path must not forge another snapshot's digest: %s", honest)
	}
	if snapshotDigest(nil) == snapshotDigest([]ports.PathFact{{Path: "\n", Digest: "\t"}}) {
		t.Fatal("the empty projection must stay distinct from any content-bearing one")
	}
}

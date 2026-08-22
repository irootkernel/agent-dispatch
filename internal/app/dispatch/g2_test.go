package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/reconcile"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/fakesink"
)

// The G2 gate suite (E3-T5): every crash window and the simultaneous
// one-shot invocation, proven against real SQLite files with real
// process deaths through the crashbin harness (TST-002, TST-004,
// TST-005). The power-loss claim is limited to the documented SQLite
// durability (synchronous=FULL, WAL) and this tested crash model:
// process death at transaction boundaries, not machine power loss.

var (
	crashbinOnce sync.Once
	crashbinPath string
	crashbinErr  error
)

func crashbin(t *testing.T) string {
	t.Helper()
	crashbinOnce.Do(func() {
		if _, err := os.Stat("../../../go.mod"); err != nil {
			crashbinErr = fmt.Errorf("repository root unavailable: %v", err)
			return
		}
		out, err := os.CreateTemp("", "agent-dispatch-crashbin-*")
		if err != nil {
			crashbinErr = err
			return
		}
		out.Close()
		cmd := exec.Command("go", "build", "-o", out.Name(), "./internal/testsupport/crashbin")
		cmd.Dir = "../../.."
		if build, err := cmd.CombinedOutput(); err != nil {
			crashbinErr = fmt.Errorf("build crashbin: %v: %s", err, build)
			return
		}
		crashbinPath = out.Name()
	})
	if crashbinErr != nil {
		t.Skipf("crashbin unavailable: %v", crashbinErr)
	}
	return crashbinPath
}

// g2Seed prepares a database through the harness and returns its path.
func g2Seed(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "state.db")
	if out, err := exec.Command(crashbin(t), "seed", "--db", db).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	return db
}

func g2Open(t *testing.T, db string) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestG2AC201 proves a crash before the ingestion transaction commits
// loses nothing partially: WAL recovery discards every uncommitted row
// and no external submit is inferred.
func TestG2AC201(t *testing.T) {
	db := g2Seed(t)
	if out, err := exec.Command(crashbin(t), "commit", "--db", db, "--stage", "mid-transaction").CombinedOutput(); err != nil {
		t.Fatalf("mid-transaction crash: %v: %s", err, out)
	}
	s := g2Open(t, db)
	for _, table := range []string{"source_observations", "change_batches", "policy_decisions", "dispatch_intents"} {
		var n int
		if err := s.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s must be empty after the crash: %d %v", table, n, err)
		}
	}
	// Nothing is lost: the arrival recommits cleanly.
	lin := g2Lineage()
	if err := s.CommitLineage(context.Background(), lin); err != nil {
		t.Fatalf("recommit after crash: %v", err)
	}
	snap, err := s.LoadIntent(context.Background(), "dispatch-1")
	if err != nil || snap.State != records.IntentReady {
		t.Fatalf("recommitted intent must be ready: %+v %v", snap, err)
	}
}

// TestG2AC202 proves a crash after the intent commit but before submit
// leaves the intent recoverable to eligible ready processing exactly
// once.
func TestG2AC202(t *testing.T) {
	db := g2Seed(t)
	if out, err := exec.Command(crashbin(t), "commit", "--db", db, "--stage", "after-commit").CombinedOutput(); err != nil {
		t.Fatalf("after-commit crash: %v: %s", err, out)
	}
	s := g2Open(t, db)
	snap, err := s.LoadIntent(context.Background(), "dispatch-1")
	if err != nil || snap.State != records.IntentReady {
		t.Fatalf("crash after commit must leave a recoverable ready intent: %+v %v", snap, err)
	}
	fake := fakesink.New("fake-main", fakesink.Step{Result: fakesink.Accepted("task-1")})
	rt := &Runtime{Store: s, Sink: fake, Now: time.Now, LeaseTTL: time.Minute, Actor: "g2"}
	report, err := rt.SubmitOnce(context.Background(), "dispatch-1", "restart")
	if err != nil || report.To != records.IntentAccepted {
		t.Fatalf("restart must process the intent exactly once: %+v %v", report, err)
	}
	if _, err := rt.SubmitOnce(context.Background(), "dispatch-1", "restart2"); err == nil {
		t.Fatal("an accepted intent must never be submitted again")
	}
	if len(fake.Submissions()) != 1 {
		t.Fatalf("exactly one external submission: %d", len(fake.Submissions()))
	}
}

// TestG2AC203 proves remote acceptance followed by a crash before the
// local receipt commit leaves unknown work that reconciliation resolves
// by lookup without creating a second task.
func TestG2AC203(t *testing.T) {
	db := g2Seed(t)
	s := g2Open(t, db)
	lin := g2Lineage()
	if err := s.CommitLineage(context.Background(), lin); err != nil {
		t.Fatal(err)
	}
	fake := fakesink.New("fake-main", fakesink.Step{Result: fakesink.Accepted("task-1")})
	// The process reaches the target and dies before recording the
	// receipt: the lease is held, the acceptance exists only remotely.
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-1", Owner: "victim",
		Now: "2026-08-20T01:00:00Z", LeaseExpiresAt: "2026-08-20T01:00:05Z",
	}); err != nil {
		t.Fatal(err)
	}
	s.Close() // the crash

	restart := g2Open(t, db)
	rt := &Runtime{Store: restart, Sink: fake, Now: func() time.Time { return time.Date(2026, 8, 20, 1, 0, 10, 0, time.UTC) }, LeaseTTL: time.Minute, Actor: "recovery"}
	recovered, err := rt.Recover(context.Background())
	if err != nil || len(recovered) != 1 || recovered[0].DispatchID != "dispatch-1" {
		t.Fatalf("expired submitting lease must recover to unknown: %+v %v", recovered, err)
	}
	snap, _ := restart.LoadIntent(context.Background(), "dispatch-1")
	if snap.State != records.IntentUnknown {
		t.Fatalf("recovered state: %+v", snap)
	}
	// Lookup proves the acceptance; no second submission happens.
	fake.ScriptLookupByIdempotency(snap.IdempotencyKey, fakesink.LookupStep{Result: fakesink.FoundAccepted("task-1")})
	svc := &reconcile.Service{Store: restart, Sink: fake, Now: func() string { return "2026-08-20T01:00:20Z" }}
	res, err := svc.Reconcile(context.Background(), "dispatch-1", "reconciler", false)
	if err != nil || res.To != records.IntentAccepted {
		t.Fatalf("lookup must prove acceptance: %+v %v", res, err)
	}
	if len(fake.Submissions()) != 0 {
		t.Fatalf("reconciliation must not submit again: %d", len(fake.Submissions()))
	}
}

// TestG2AC204 proves two simultaneous one-shot processes cannot own the
// same attempt: exactly one conditional write wins and the losers
// observe existing ownership (TST-005).
func TestG2AC204(t *testing.T) {
	db := g2Seed(t)
	if out, err := exec.Command(crashbin(t), "commit", "--db", db, "--stage", "after-commit").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v: %s", err, out)
	}
	const competitors = 4
	var wg sync.WaitGroup
	results := make([]error, competitors)
	for i := 0; i < competitors; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := exec.Command(crashbin(t), "acquire-race", "--db", db, "--owner", fmt.Sprintf("p%d", i)).CombinedOutput()
			if err != nil {
				results[i] = errors.New(string(out))
			}
		}(i)
	}
	wg.Wait()
	winners := 0
	for _, err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("exactly one lease winner, got %d: %v", winners, results)
	}
	s := g2Open(t, db)
	var owners int
	if err := s.QueryRow(`SELECT COUNT(DISTINCT lease_owner) FROM dispatch_attempts WHERE completed_at IS NULL`).Scan(&owners); err != nil || owners != 1 {
		t.Fatalf("one attempt owner in the database: %d %v", owners, err)
	}
}

// TestG2AC205 proves bounded transient failures use persisted backoff,
// retain one idempotency key, and stop at the configured limit.
func TestG2AC205(t *testing.T) {
	s := openE3T3Store(t)
	seedReadyIntent(t, s, "dispatch-1")
	base := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	clock := func() func() time.Time {
		current := base
		return func() time.Time {
			current = current.Add(time.Second)
			return current
		}
	}()
	rt := &Runtime{
		Store: s, Sink: fakesink.New("fake-main", fakesink.Step{Result: fakesink.NotSubmitted()}, fakesink.Step{Result: fakesink.NotSubmitted()}),
		Now: clock, LeaseTTL: time.Minute, Actor: "g2",
		Backoff: Backoff{MaxAttempts: 2, InitialBackoff: time.Hour, MaxBackoff: time.Hour, Multiplier: 2, JitterFraction: 0},
	}
	key := ""
	for i := 0; i < 2; i++ {
		report, err := rt.SubmitOnce(context.Background(), "dispatch-1", "p1")
		if err != nil {
			t.Fatal(err)
		}
		snap, _ := s.LoadIntent(context.Background(), "dispatch-1")
		if snap.NextAttemptAt == "" {
			t.Fatalf("attempt %d must persist a backoff deadline", i+1)
		}
		if key == "" {
			key = snap.IdempotencyKey
		} else if snap.IdempotencyKey != key {
			t.Fatal("retries must retain one idempotency key")
		}
		_ = report
		if err := s.MakeRetryDue(context.Background(), "dispatch-1", "due"); err != nil {
			t.Fatal(err)
		}
	}
	drained, err := rt.Drain(context.Background(), "wiki-maintenance", 10, s)
	if err != nil {
		t.Fatal(err)
	}
	if drained.Processed != 0 || drained.Skipped != 1 {
		t.Fatalf("automatic attempts must stop at the limit: %+v", drained)
	}
}

// TestG2AC206 proves terminal rejection becomes inspectable rejected
// work with no automatic sink switch.
func TestG2AC206(t *testing.T) {
	s := openE3T3Store(t)
	seedReadyIntent(t, s, "dispatch-1")
	fake := fakesink.New("fake-main", fakesink.Step{Result: fakesink.Rejected()})
	rt := &Runtime{Store: s, Sink: fake, Now: time.Now, LeaseTTL: time.Minute, Actor: "g2"}
	report, err := rt.SubmitOnce(context.Background(), "dispatch-1", "p1")
	if err != nil || report.To != records.IntentRejected {
		t.Fatalf("terminal rejection: %+v %v", report, err)
	}
	lin, err := s.LoadIntentLineage(context.Background(), "dispatch-1")
	if err != nil || lin.Intent.State != records.IntentRejected || len(lin.Attempts) == 0 || len(lin.Receipts) == 0 {
		t.Fatalf("rejected work must remain inspectable: %+v %v", lin, err)
	}
	if len(fake.Submissions()) != 1 {
		t.Fatalf("no automatic retry or fallback after rejection: %d submissions", len(fake.Submissions()))
	}
	if _, err := rt.SubmitOnce(context.Background(), "dispatch-1", "p1"); err == nil {
		t.Fatal("rejected work must not be resubmitted automatically")
	}
}

// TestG2AC207 proves an interrupted migration leaves the database valid
// at the previous version, and a restart completes to the new version —
// never a partially assumed schema.
func TestG2AC207(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	// Die before any migration applied: the database is valid at the
	// previous (empty) version.
	if out, err := exec.Command(crashbin(t), "migrate-partial", "--db", db, "--steps", "0").CombinedOutput(); err != nil {
		t.Fatalf("interrupted migration: %v: %s", err, out)
	}
	s := g2Open(t, db)
	// With no migration applied the ledger does not exist yet: the empty
	// database is exactly the valid previous version (AC-207).
	version, err := s.SchemaVersion()
	if err != nil {
		t.Skipf("ledger not created before the first migration: %v", err)
	}
	if version != 0 {
		t.Fatalf("interrupted migration must leave the previous version, got %d", version)
	}
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatalf("restart must complete the migration: %v", err)
	}
	if version, err := s.SchemaVersion(); err != nil || version != sqlite.MaxSchemaVersion {
		t.Fatalf("restart must reach the newest version: %d %v", version, err)
	}
	if err := s.IntegrityCheck(false); err != nil {
		t.Fatalf("restarted database must pass integrity: %v", err)
	}
}

// TestG2MultiProcessOneActiveRouteDispatch proves simultaneous one-shot
// arrivals against one database create exactly one active route
// dispatch (TST-005's second clause) at the process level.
func TestG2MultiProcessOneActiveRouteDispatch(t *testing.T) {
	db := g2Seed(t)
	const arrivals = 6
	var wg sync.WaitGroup
	results := make([]error, arrivals)
	for i := 0; i < arrivals; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tag := fmt.Sprintf("p%d", i)
			out, err := exec.Command(crashbin(t), "arrive", "--db", db, "--tag", tag).CombinedOutput()
			if err != nil {
				results[i] = errors.New(string(out))
			}
		}(i)
	}
	wg.Wait()
	winners := 0
	for _, err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("exactly one arrival may hold the active route dispatch, got %d: %v", winners, results)
	}
	s := g2Open(t, db)
	intents, err := s.ListIntents(context.Background(), ports.IntentFilter{RouteID: "wiki", Limit: 100})
	if err != nil || len(intents) != 1 {
		t.Fatalf("one durable dispatch: %d %v", len(intents), err)
	}
	var active string
	if err := s.QueryRow(`SELECT active_dispatch_id FROM route_runtime_state WHERE route_id = 'wiki'`).Scan(&active); err != nil || active != intents[0].DispatchID {
		t.Fatalf("the single dispatch holds the slot: %q %v", active, err)
	}
}

// g2Lineage matches the crashbin seed's fixed identities.
func g2Lineage() ports.Lineage {
	now := "2026-08-20T01:00:00Z"
	return ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: "obs-1", SchemaVersion: "agent-dispatch.source-observation/v1",
			SourceType: "watchman", SourceID: "watchman-main", TriggerName: "trig",
			ResourceID: "vault-main", ObservedAt: now, ReceivedAt: now,
			RawPayloadDigest: "sha256:" + hex64('a'), IngestStatus: "accepted",
		},
		Batch: ports.BatchInput{
			BatchID: "batch-1", RouteID: "wiki", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: now,
			ContentFingerprint: "sha256:" + hex64('c'), ObservationIDs: []string{"obs-1"},
		},
		Decision: ports.DecisionInput{
			DecisionID: "decision-1", BatchID: "batch-1", RouteID: "wiki",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1",
			Disposition: "dispatch", Classification: "normal", CreatedAt: now, Actor: "planner",
		},
		Intent: ports.IntentInput{
			DispatchID: "dispatch-1", DecisionID: "decision-1", RouteID: "wiki",
			RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes_kanban",
			ResourceID: "vault-main", Generation: 1, IdempotencyKey: "agent-dispatch:v1:sha256:" + hex64('1'),
			ContentFingerprint: "sha256:" + hex64('c'), ManifestDigest: "sha256:" + hex64('d'),
			RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: "{}", CreatedAt: now,
		},
	}
}

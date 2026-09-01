package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// e16t3Store opens a migrated store with the test route registered and
// the notification policy installed.
func e16t3Store(t *testing.T) *sqlite.Store {
	t.Helper()
	return e16t3StoreAt(t, t.TempDir())
}

// e16t3StoreAt opens the migrated test store inside one explicit state
// directory (the runner's configuration-owned location).
func e16t3StoreAt(t *testing.T, stateDir string) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(filepath.Join(stateDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki", "route-rev-1", "policy-rev-1", "vault-main", "hermes-main", "{}", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki"); err != nil {
		t.Fatal(err)
	}
	policy := &ports.NotificationPolicy{
		Events:   []records.NotificationEventKind{records.EventWorkCompleted},
		Sinks:    []ports.NotificationSinkRef{{ID: "ops-log", Type: "log"}},
		Revision: "policy-rev-e16t3",
	}
	s.SetNotificationPolicy(func(routeID string) *ports.NotificationPolicy {
		if routeID != "wiki" {
			return nil
		}
		return policy
	})
	return s
}

// e16t3Config returns a configuration whose wiki route drains
// after-command through a log sink.
func e16t3Config(mode string) *config.Config {
	cfg := e16t1BaseConfigCLI()
	cfg.Routes["wiki"].Notifications.Drain = &config.NotificationDrain{Mode: mode}
	return cfg
}

// e16t1BaseConfigCLI mirrors the config test fixture at the CLI layer.
func e16t1BaseConfigCLI() *config.Config {
	return &config.Config{
		Version:  1,
		Instance: config.Instance{ID: "test"},
		Resources: map[string]config.Resource{
			"vault-main": {Type: "directory", Root: "/srv/vault", FileScope: "markdown"},
		},
		HermesTargets: map[string]config.HermesTarget{
			"hermes-main": {Board: "agent-dispatch", Executable: "hermes", MinimumVersion: "0.20.5", Compatibility: "capability_probe"},
		},
		Routes: map[string]config.Route{
			"wiki": {
				Source:   config.Source{Type: "watchman-trigger", SourceID: "w", Resource: "vault-main", TriggerName: "t", Include: []string{"**/*.md"}, Exclude: []string{".git/**"}},
				Batching: config.Batching{AutomaticThreshold: 25, HardLimit: 100, MaxManifestBytes: 262144},
				Policy: config.Policy{Protected: []string{"raw/**"}, Immutable: []string{}, BulkAction: "quarantine", OverflowAction: "reconcile",
					FreshInstanceAction: "reconcile", UnsafePathAction: "quarantine"},
				FanoutMode:       "all",
				Destinations:     []config.Destination{{ID: "indexing", Target: "hermes-main", Profile: "p", Skills: []string{"s"}, Workstream: "indexing", ExecutionHints: config.ExecutionHints{MaxRuntime: "30m", MaxAttempts: 2}}},
				Notifications:    &config.Notifications{Sinks: []config.NotificationSink{{ID: "ops-log", Type: "log"}}},
				SubmissionRetry:  config.Retry{MaxAttempts: 3, InitialBackoff: "2s", MaxBackoff: "2m", Multiplier: 2, JitterFraction: 0.2},
				FailureBudget:    2,
				LatestState:      true,
				ActiveStaleAfter: "2h",
			},
		},
	}
}

// TestE16T3AfterCommandDrainDeliversExistingDueWork pins the registry's
// core acceptance: after a successful registered command, every
// affected after-command route drains its existing due work — work that
// predates the invocation included — silently on success, with one
// drain-run evidence row.
func TestE16T3AfterCommandDrainDeliversExistingDueWork(t *testing.T) {
	s := e16t3Store(t)
	ctx := context.Background()
	// Due work created before the invocation.
	if _, err := s.EnqueueRouteNotification(ctx, "wiki", records.EventWorkCompleted, "pre-existing", "", nil, "2026-08-30T09:00:00Z"); err != nil {
		t.Fatal(err)
	}
	markInvocationStart()
	var stdout, stderr bytes.Buffer
	maybeAfterCommandDrainCfg("work complete", e16t3Config("after-command"), s, &stderr, "wiki")
	// Silent success means no pass diagnostics: stdout stays untouched
	// and stderr carries at most the operator-configured log sink's own
	// delivery records (never an automatic-drain problem line).
	if stdout.Len() != 0 {
		t.Fatalf("the automatic pass never writes stdout: %q", stdout.String())
	}
	if bytes.Contains(stderr.Bytes(), []byte("automatic notification drain")) || bytes.Contains(stderr.Bytes(), []byte("after-command drain")) {
		t.Fatalf("a successful pass writes no diagnostics: %q", stderr.String())
	}
	byState, err := s.CountNotificationsByState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if byState["delivered"] != 1 || byState["pending"] != 0 {
		t.Fatalf("the pre-existing due work must deliver: %v", byState)
	}
	var runs int
	if err := s.QueryRow(`SELECT COUNT(*) FROM drain_runs WHERE route_id = 'wiki' AND trigger = 'after-command' AND completed_at IS NOT NULL`).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("one completed after-command drain-run evidence row: %d %v", runs, err)
	}
}

// TestE16T3ManualAndUnaffectedRoutesStayDry pins the exclusions: a
// manual-mode route never auto-drains, and an affected set that does not
// include the after-command route leaves it untouched.
func TestE16T3ManualAndUnaffectedRoutesStayDry(t *testing.T) {
	s := e16t3Store(t)
	ctx := context.Background()
	if _, err := s.EnqueueRouteNotification(ctx, "wiki", records.EventWorkCompleted, "manual-1", "", nil, "2026-08-30T09:00:00Z"); err != nil {
		t.Fatal(err)
	}
	markInvocationStart()
	var stderr bytes.Buffer
	maybeAfterCommandDrainCfg("work complete", e16t3Config("manual"), s, &stderr, "wiki")
	// Unaffected route name against an after-command configuration.
	maybeAfterCommandDrainCfg("work complete", e16t3Config("after-command"), s, &stderr, "other-route")
	byState, err := s.CountNotificationsByState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if byState["pending"] != 1 || byState["delivered"] != 0 {
		t.Fatalf("manual mode and unaffected routes must not auto-drain: %v", byState)
	}
	var rows int
	if err := s.QueryRow(`SELECT COUNT(*) FROM drain_runs`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("no drain-run evidence may appear for a dry pass: %d %v", rows, err)
	}
}

// TestE16T3ExhaustedBudgetDrainsNothing pins the one-invocation
// ten-second budget: an invocation whose budget is already spent drains
// nothing and stays silent.
func TestE16T3ExhaustedBudgetDrainsNothing(t *testing.T) {
	s := e16t3Store(t)
	ctx := context.Background()
	if _, err := s.EnqueueRouteNotification(ctx, "wiki", records.EventWorkCompleted, "budget-1", "", nil, "2026-08-30T09:00:00Z"); err != nil {
		t.Fatal(err)
	}
	afterCommandClock.mu.Lock()
	afterCommandClock.startedAt = time.Now().Add(-afterCommandBudget - time.Second)
	afterCommandClock.mu.Unlock()
	var stderr bytes.Buffer
	maybeAfterCommandDrainCfg("dispatch", e16t3Config("after-command"), s, &stderr, "wiki")
	byState, err := s.CountNotificationsByState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if byState["pending"] != 1 || byState["delivered"] != 0 {
		t.Fatalf("an exhausted invocation budget drains nothing: %v", byState)
	}
}

// TestE16T3AfterCommandRoutesOrderAndFilter pins the deterministic
// route-ID visiting order and the mode/affected filters.
func TestE16T3AfterCommandRoutesOrderAndFilter(t *testing.T) {
	cfg := e16t3Config("after-command")
	cloneRoute := func(mode string) config.Route {
		r := cfg.Routes["wiki"]
		r.Notifications = &config.Notifications{
			Sinks: append([]config.NotificationSink(nil), r.Notifications.Sinks...),
			Drain: &config.NotificationDrain{Mode: mode},
		}
		return r
	}
	cfg.Routes["beta"] = cloneRoute("manual")
	cfg.Routes["alpha"] = cloneRoute("scheduled")
	got := afterCommandRoutes(cfg, nil)
	if len(got) != 1 || got[0] != "wiki" {
		t.Fatalf("only after-command routes qualify: %v", got)
	}
	cfg.Routes["alpha"] = cloneRoute("after-command")
	got = afterCommandRoutes(cfg, nil)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "wiki" {
		t.Fatalf("route-ID order governs the round-robin: %v", got)
	}
	got = afterCommandRoutes(cfg, []string{"wiki"})
	if len(got) != 1 || got[0] != "wiki" {
		t.Fatalf("the affected set narrows the pass: %v", got)
	}
}

// TestE16T3RoundRobinSharesTheBudget pins the fair-share posture: with
// more due work than one round carries, the pass advances in
// one-per-round rounds bounded by the per-route limit, and the drain-run
// evidence aggregates the rounds.
func TestE16T3RoundRobinSharesTheBudget(t *testing.T) {
	s := e16t3Store(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := s.EnqueueRouteNotification(ctx, "wiki", records.EventWorkCompleted, "rr-"+string(rune('a'+i)), "", nil, "2026-08-30T09:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	markInvocationStart()
	var stderr bytes.Buffer
	cfg := e16t3Config("after-command")
	cfg.Routes["wiki"].Notifications.Drain = &config.NotificationDrain{Mode: "after-command", Limit: 2}
	maybeAfterCommandDrainCfg("reconcile", cfg, s, &stderr, "wiki")
	var delivered, pending int
	if err := s.QueryRow(`SELECT COUNT(*) FROM notification_events WHERE state = 'delivered'`).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if err := s.QueryRow(`SELECT COUNT(*) FROM notification_events WHERE state = 'pending'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if delivered != 2 || pending != 1 {
		t.Fatalf("the configured per-route limit bounds the pass: delivered=%d pending=%d", delivered, pending)
	}
	var claimed int
	if err := s.QueryRow(`SELECT claimed FROM drain_runs WHERE route_id = 'wiki'`).Scan(&claimed); err != nil || claimed != 2 {
		t.Fatalf("the evidence row aggregates the rounds: %d %v", claimed, err)
	}
}

// TestE16T3RunAnchorsTheInvocationBudget pins the wiring: Run's start
// anchors the ten-second budget (F002's regression half).
func TestE16T3RunAnchorsTheInvocationBudget(t *testing.T) {
	afterCommandClock.mu.Lock()
	afterCommandClock.startedAt = time.Time{}
	afterCommandClock.mu.Unlock()
	var out, errb bytes.Buffer
	if code := Run([]string{"version"}, &out, &errb); code != 0 {
		t.Fatalf("version: %d", code)
	}
	afterCommandClock.mu.Lock()
	started := afterCommandClock.startedAt
	afterCommandClock.mu.Unlock()
	if started.IsZero() {
		t.Fatal("Run must anchor the after-command budget at invocation start")
	}
}

// TestE16T3FailedCoreCommandNeverDrains pins the failed-core exclusion:
// a command that fails before its core commit leaves no drain-run
// evidence (F006).
func TestE16T3FailedCoreCommandNeverDrains(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	cfg := e16t3Config("after-command")
	cfg.Instance.StateDir = filepath.Join(dir, "state")
	if err := config.WriteExample(cfg, configPath); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	markInvocationStart()
	code := Run([]string{"work", "complete", "--dispatch-id", "missing", "--run-id", "r1", "--manifest", "[]", "--config", configPath}, &out, &errb)
	if code == 0 {
		t.Fatal("an unknown dispatch must fail")
	}
	s, err := sqlite.Open(filepath.Join(dir, "state", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.QueryRow(`SELECT COUNT(*) FROM drain_runs`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("a failed core command never auto-drains: %d %v", rows, err)
	}
}

// TestE16T3BudgetExpiryStillCompletesEvidence pins round-1 F001's
// remediation: when the invocation budget is exhausted mid-pass, the
// drain-run evidence row still completes.
func TestE16T3BudgetExpiryStillCompletesEvidence(t *testing.T) {
	s := e16t3Store(t)
	ctx := context.Background()
	if _, err := s.EnqueueRouteNotification(ctx, "wiki", records.EventWorkCompleted, "expiry-1", "", nil, "2026-08-30T09:00:00Z"); err != nil {
		t.Fatal(err)
	}
	// A budget with only a sliver left: the pass starts, hits expiry
	// before finishing, and must still close its evidence row.
	afterCommandClock.mu.Lock()
	afterCommandClock.startedAt = time.Now().Add(-afterCommandBudget + 50*time.Millisecond)
	afterCommandClock.mu.Unlock()
	var stderr bytes.Buffer
	maybeAfterCommandDrainCfg("dispatch", e16t3Config("after-command"), s, &stderr, "wiki")
	var open int
	if err := s.QueryRow(`SELECT COUNT(*) FROM drain_runs WHERE completed_at IS NULL`).Scan(&open); err != nil || open != 0 {
		t.Fatalf("an expired budget must still complete its evidence row: %d %v", open, err)
	}
}

// TestE16T3StderrBoundAndConfigUnreadable pins the bounded-diagnostic
// contract and the unreadable-configuration branch (round-1 F003).
func TestE16T3StderrBoundAndConfigUnreadable(t *testing.T) {
	s := e16t3Store(t)
	markInvocationStart()
	afterCommandClock.mu.Lock()
	afterCommandClock.notes = afterCommandStderrBound // already at the bound
	afterCommandClock.mu.Unlock()
	var stderr bytes.Buffer
	boundedAfterCommandNote(&stderr, "this line must be dropped")
	if stderr.Len() != 0 {
		t.Fatal("the stderr bound must drop notes past its limit")
	}
	// The unreadable-configuration branch reports once and drains
	// nothing.
	var out2, errb2 bytes.Buffer
	_ = out2
	markInvocationStart()
	maybeAfterCommandDrain("dispatch", "/nonexistent/config.yaml", s, &errb2, "wiki")
	if !bytes.Contains(errb2.Bytes(), []byte("configuration unreadable")) {
		t.Fatalf("the unreadable-configuration branch must report once: %q", errb2.String())
	}
}

// TestE16T3ExcludedCommandsNeverSpawnAfterCommandDrain pins the
// registry's exclusion convention (round-2 F006): the explicit manual
// drain and the read-only inspection commands are not registered
// success sites, so neither may spawn an after-command pass — asserted
// as the absence of any drain_runs evidence row — even though the
// explicit drain itself delivers and an after-command route has due
// work the whole time.
func TestE16T3ExcludedCommandsNeverSpawnAfterCommandDrain(t *testing.T) {
	dir := t.TempDir()
	cfg := e16t3Config("after-command")
	cfg.Instance.StateDir = filepath.Join(dir, "state")
	configPath := filepath.Join(dir, "config.yaml")
	if err := config.WriteExample(cfg, configPath); err != nil {
		t.Fatal(err)
	}
	s := e16t3StoreAt(t, cfg.Instance.StateDir)
	ctx := context.Background()
	if err := s.SetRouteActivation(ctx, "wiki", "enabled", "route-rev-1", "", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	seed := func(id string) {
		t.Helper()
		if _, err := s.EnqueueRouteNotification(ctx, "wiki", records.EventWorkCompleted, id, "", nil, "2026-08-30T09:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	afterCommandRuns := func() int {
		t.Helper()
		var n int
		if err := s.QueryRow(`SELECT COUNT(*) FROM drain_runs WHERE trigger = 'after-command'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// The explicit manual drain delivers the route's due work through
	// the shared lease-safe service but never records an after-command
	// pass: the drain-success recursion exclusion is structural.
	seed("manual-pass")
	markInvocationStart()
	var out, errb bytes.Buffer
	if code := Run([]string{"notifications", "drain", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("manual drain: %d %s", code, errb.String())
	}
	byState, err := s.CountNotificationsByState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if byState["delivered"] < 1 || byState["pending"] != 0 {
		t.Fatalf("the manual drain must deliver the due work (the drift evaluation may add its own): %v", byState)
	}
	if n := afterCommandRuns(); n != 0 {
		t.Fatalf("the explicit drain must never spawn an after-command pass: %d evidence rows", n)
	}
	// A read-only inspection command stays completely dry: no delivery
	// and no automatic evidence row of any trigger.
	seed("read-only-pass")
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"notifications", "list", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("notifications list: %d %s", code, errb.String())
	}
	byState, err = s.CountNotificationsByState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if byState["pending"] != 1 {
		t.Fatalf("a read-only command must not drain: %v", byState)
	}
	var anyRuns int
	if err := s.QueryRow(`SELECT COUNT(*) FROM drain_runs`).Scan(&anyRuns); err != nil {
		t.Fatal(err)
	}
	if anyRuns != 0 {
		t.Fatalf("no excluded command may write drain evidence: %d rows", anyRuns)
	}
}

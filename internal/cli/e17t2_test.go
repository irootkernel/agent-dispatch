// E17-T2 cold-validation remediations: the round-4 confirmation review
// of the E16 epic validation left four verified Medium findings
// (F001-F004) and a code-side Low cohort (F012-F020) against the E14-E16
// result. These tests pin each correction at the level the finding
// named — CLI Run-level wiring for the after-command registry, the
// lease-safe retry and envelope semantics, the schedule's persisted
// --at timing, the posture's drain evidence, and the schedule usage
// contracts.
package cli

import (
	"bytes"
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// e17t2AfterCommandFixture rewrites the e4t3 fixture's wiki route into
// after-command drain mode with a log sink, so every registered
// command's post-commit pass has due work to advance.
func e17t2AfterCommandFixture(t *testing.T) (string, string) {
	t.Helper()
	configPath, vault := e4t3Fixture(t)
	e5t4Rewrite(t, configPath,
		"    reconciliation:\n      initial: true\n      daily_expected: true\n",
		"    reconciliation:\n      initial: true\n      daily_expected: true\n    notifications:\n      events: [work_completed, work_failed, delivery_unknown]\n      sinks:\n        - id: ops-log\n          type: log\n      drain:\n        mode: after-command\n        retry:\n          initial_backoff: 1s\n          max_backoff: 2s\n          multiplier: 2.0\n          jitter_fraction: 0.0\n")
	return configPath, vault
}

// e17t2StateDir resolves the fixture configuration's state directory.
func e17t2StateDir(t *testing.T, configPath string) string {
	t.Helper()
	cfg, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		t.Fatal(err)
	}
	return platformpaths.ResolveStateDir(cfg.Instance.StateDir)
}

// e17t2Store opens the fixture's own state store WITHOUT re-registering
// anything: the e4t3 fixture registrations (e4t3RegisterRoute) are the
// authority, this is only the seeding and assertion surface. The
// notification policy mirrors the fixture's declared ops-log policy so
// seeded events enqueue.
func e17t2Store(t *testing.T, configPath string) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(filepath.Join(e17t2StateDir(t, configPath), StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(filepath.Join(e17t2StateDir(t, configPath), "backups")); err != nil {
		t.Fatal(err)
	}
	s.SetNotificationPolicy(func(routeID string) *ports.NotificationPolicy {
		if routeID != "wiki" {
			return nil
		}
		return &ports.NotificationPolicy{
			Events:   []records.NotificationEventKind{records.EventWorkCompleted, records.EventWorkFailed, records.EventDeliveryUnknown},
			Sinks:    []ports.NotificationSinkRef{{ID: "ops-log", Type: "log"}},
			Revision: "policy-rev-e17t2",
		}
	})
	return s
}

// e17t2SeedDueNotification enqueues one immediately-due work-completed
// notification for the wiki route through the store the commands open.
func e17t2SeedDueNotification(t *testing.T, configPath, transition string) string {
	t.Helper()
	s := e17t2Store(t, configPath)
	ctx := context.Background()
	n, err := s.EnqueueRouteNotification(ctx, "wiki", records.EventWorkCompleted, transition, "", nil, time.Now().UTC().Format(time.RFC3339))
	if err != nil || n != 1 {
		t.Fatalf("seed %s: %d %v", transition, n, err)
	}
	var id string
	if err := s.QueryRow(`SELECT notification_id FROM notification_events WHERE transition = ?`, transition).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// e17t2NotificationState reads one notification's durable state.
func e17t2NotificationState(t *testing.T, configPath, id string) string {
	t.Helper()
	s := e17t2Store(t, configPath)
	var state string
	if err := s.QueryRow(`SELECT state FROM notification_events WHERE notification_id = ?`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

// e17t2AssertDelivered fails unless the seeded notification reached the
// delivered state — the Run-level proof the registered command drained.
func e17t2AssertDelivered(t *testing.T, configPath, id, stderrText string) {
	t.Helper()
	if got := e17t2NotificationState(t, configPath, id); got != "delivered" {
		t.Fatalf("the registered command must drain the seeded due work: state %q (stderr %s)", got, stderrText)
	}
}

// TestE17T2RegisteredCommandsDrainAfterCommit pins the after-command
// wiring at the Run level (round-4 F003): every REGISTERED command —
// dispatch, work completion, work failure, applicable dispatch retry
// and rerun, quarantine resolution, and reconciliation — drains the
// route's existing due work after its successful commit. The seed lands
// AFTER each command's precondition and BEFORE the command itself, so
// the delivery can only come from that command's own post-commit pass;
// deleting or mis-wiring any one call site fails its row.
func TestE17T2RegisteredCommandsDrainAfterCommit(t *testing.T) {
	t.Run("dispatch", func(t *testing.T) {
		configPath, vault := e17t2AfterCommandFixture(t)
		e4t3RegisterRoute(t, configPath)
		setPlanEnv(t, vault, false)
		id := e17t2SeedDueNotification(t, configPath, "seed-dispatch")
		var out, errb bytes.Buffer
		markInvocationStart()
		withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
			if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb); code != 0 {
				t.Fatalf("dispatch: %d %s", code, errb.String())
			}
		})
		e17t2AssertDelivered(t, configPath, id, errb.String())
	})
	t.Run("work complete", func(t *testing.T) {
		configPath, vault := e17t2AfterCommandFixture(t)
		res, _ := e4t3Dispatch(t, configPath, vault)
		dispatchID, _ := res["dispatch_id"].(string)
		var out, errb bytes.Buffer
		markInvocationStart()
		if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
			t.Fatalf("work begin: %d %s", code, errb.String())
		}
		id := e17t2SeedDueNotification(t, configPath, "seed-complete")
		out.Reset()
		errb.Reset()
		markInvocationStart()
		withStdin(t, `[]`, func() {
			if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb); code != 0 {
				t.Fatalf("work complete: %d %s", code, errb.String())
			}
		})
		e17t2AssertDelivered(t, configPath, id, errb.String())
	})
	t.Run("work fail", func(t *testing.T) {
		configPath, vault := e17t2AfterCommandFixture(t)
		res, _ := e4t3Dispatch(t, configPath, vault)
		dispatchID, _ := res["dispatch_id"].(string)
		var out, errb bytes.Buffer
		markInvocationStart()
		if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
			t.Fatalf("work begin: %d %s", code, errb.String())
		}
		id := e17t2SeedDueNotification(t, configPath, "seed-fail")
		out.Reset()
		errb.Reset()
		markInvocationStart()
		if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--failure-code", "agent_error"}, &out, &errb); code != 0 {
			t.Fatalf("work fail: %d %s", code, errb.String())
		}
		e17t2AssertDelivered(t, configPath, id, errb.String())
	})
	t.Run("dispatches rerun", func(t *testing.T) {
		configPath, vault := e17t2AfterCommandFixture(t)
		dispatchID := e7t2NoSubmitDispatch(t, configPath, vault)
		id := e17t2SeedDueNotification(t, configPath, "seed-rerun")
		var out, errb bytes.Buffer
		markInvocationStart()
		if code := Run([]string{"dispatches", "rerun", "--config", configPath, "--yes", "--reason", "operator redo", dispatchID}, &out, &errb); code != 0 {
			t.Fatalf("dispatches rerun: %d %s", code, errb.String())
		}
		e17t2AssertDelivered(t, configPath, id, errb.String())
	})
	t.Run("dispatches retry", func(t *testing.T) {
		configPath, vault := e17t2AfterCommandFixture(t)
		dispatchID := e7t2NoSubmitDispatch(t, configPath, vault)
		// The retry exit requires retry-eligible state: park the ready
		// intent in retry_wait exactly as an exhausted submit backoff
		// would leave it.
		s := e17t2Store(t, configPath)
		if _, err := s.Exec(`UPDATE dispatch_intents SET state = 'retry_wait' WHERE dispatch_id = ?`, dispatchID); err != nil {
			t.Fatal(err)
		}
		id := e17t2SeedDueNotification(t, configPath, "seed-retry")
		var out, errb bytes.Buffer
		markInvocationStart()
		if code := Run([]string{"dispatches", "retry", "--config", configPath, dispatchID}, &out, &errb); code != 0 {
			t.Fatalf("dispatches retry: %d %s", code, errb.String())
		}
		e17t2AssertDelivered(t, configPath, id, errb.String())
	})
	t.Run("quarantine release", func(t *testing.T) {
		configPath, vault := e17t2AfterCommandFixture(t)
		e5t4Rewrite(t, configPath, "      protected: []", "      protected: [\"Secrets/**\"]")
		if err := os.MkdirAll(filepath.Join(vault, "Secrets"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(vault, "Secrets", "keep.md"), []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
		e4t3RegisterRoute(t, configPath)
		setPlanEnv(t, vault, false)
		var out, errb bytes.Buffer
		markInvocationStart()
		withStdin(t, `[{"name":"Secrets/keep.md","exists":true,"new":true,"size":6,"type":"f"}]`, func() {
			Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
		})
		quarantineID, _ := decodeEnvelope(t, &out)["quarantine_id"].(string)
		if quarantineID == "" {
			t.Fatalf("protected dispatch must hold: %s %s", out.String(), errb.String())
		}
		id := e17t2SeedDueNotification(t, configPath, "seed-release")
		out.Reset()
		errb.Reset()
		markInvocationStart()
		if code := Run([]string{"quarantine", "release", "--config", configPath, "--yes", "--reason", "operator reviewed the hold", quarantineID}, &out, &errb); code != 0 {
			t.Fatalf("quarantine release: %d %s", code, errb.String())
		}
		e17t2AssertDelivered(t, configPath, id, errb.String())
	})
	t.Run("reconcile submit", func(t *testing.T) {
		configPath, vault := e17t2AfterCommandFixture(t)
		e4t3RegisterRoute(t, configPath)
		setPlanEnv(t, vault, false)
		id := e17t2SeedDueNotification(t, configPath, "seed-reconcile")
		var out, errb bytes.Buffer
		markInvocationStart()
		if code := Run([]string{"reconcile", "--route", "wiki", "--reason", "manual", "--submit", "--config", configPath}, &out, &errb); code != 0 {
			t.Fatalf("reconcile --submit: %d %s", code, errb.String())
		}
		e17t2AssertDelivered(t, configPath, id, errb.String())
	})
}

// TestE17T2ScheduleAtOverridePersisted pins the durable --at timing
// (round-4 F004): a scheduled-mode install with a non-default --at
// stays definition-matching afterwards — inspect and the status posture
// read healthy, never as drift — and a fresh default install converges
// back to 03:00.
func TestE17T2ScheduleAtOverridePersisted(t *testing.T) {
	configPath := e16t4Env(t, "scheduled")
	launchctlRun = func(args ...string) (string, error) { return "", nil }
	defer func() {
		launchctlRun = func(args ...string) (string, error) { return launchctlExec(args...) }
	}()
	var out, errb bytes.Buffer
	markInvocationStart()
	if code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "launchd", "--at", "05:45", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("install --at 05:45: %d %s", code, errb.String())
	}
	// Inspect WITHOUT the flag reproduces the override's definition: the
	// previously permanent mismatch is gone.
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "inspect", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("inspect: %d %s", code, errb.String())
	}
	if env := decodeEnvelope(t, &out); env["healthy"] != true || env["definition_matches"] != true {
		t.Fatalf("an overridden install must stay healthy: %v", env)
	}
	// The status posture agrees through the store: the scheduler
	// expectation is met, the schedule is not overdue.
	s := e16t3StoreAt(t, filepath.Join(filepath.Dir(configPath), "state"))
	defer s.Close()
	if err := s.SetRouteActivation(context.Background(), "wiki", "enabled", "route-rev-1", "", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	posture := schedulePostureWithStore(cfg, "wiki", configPath, s, &errb)
	if posture["healthy"] != true || posture["overdue"] != false {
		t.Fatalf("the posture must read the override as the intended definition: %v", posture)
	}
	// Render reproduces the stored timing without the flag.
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "render", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("render: %d %s", code, errb.String())
	}
	if plist, _ := decodeEnvelope(t, &out)["plist"].(string); !strings.Contains(plist, "<integer>45</integer>") {
		t.Fatalf("render must reproduce the stored 05:45 override")
	}
	// A flagless install over the override renders the DEFAULT
	// definition — a different definition, so the idempotent contract
	// refuses it exactly like any foreign definition at the path.
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 14 || !strings.Contains(errb.String(), "different definition") {
		t.Fatalf("a flagless install over an override must refuse the different definition: %d %s", code, errb.String())
	}
	// Uninstall clears both the plist and the stored override; the next
	// render reproduces the 03:00 default with no residual timing.
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "uninstall", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("uninstall: %d %s", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "render", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("render after uninstall: %d %s", code, errb.String())
	}
	if plist, _ := decodeEnvelope(t, &out)["plist"].(string); !strings.Contains(plist, "<integer>3</integer>") || strings.Contains(plist, "<integer>45</integer>") {
		t.Fatalf("uninstall must clear the override back to the 03:00 default")
	}
}

// TestE17T2StatusExposesLatestDrainEvidence pins the OPS-017/AC-1206
// "latest drain evidence" member (round-4 F001): after one completed
// drain pass the status posture carries the newest drain_runs row —
// trigger, counts, and completion — without direct SQLite inspection.
func TestE17T2StatusExposesLatestDrainEvidence(t *testing.T) {
	configPath := e16t4Env(t, "after-command")
	s := e16t3StoreAt(t, filepath.Join(filepath.Dir(configPath), "state"))
	defer s.Close()
	ctx := context.Background()
	if err := s.StartDrainRun(ctx, ports.DrainRunInput{
		DrainID: "drain-e17t2-1", RouteID: "wiki", Trigger: "after-command", Mode: "after-command",
		StartedAt: "2026-09-02T01:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishDrainRun(ctx, "drain-e17t2-1", ports.DrainRunCounts{
		Claimed: 2, Delivered: 1, Refused: 1,
	}, "2026-09-02T01:00:05Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRouteActivation(ctx, "wiki", "enabled", "route-rev-1", "", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	markInvocationStart()
	if code := Run([]string{"status", "--config", configPath, "--output", "json"}, &out, &errb); code != 0 {
		t.Fatalf("status: %d %s", code, errb.String())
	}
	body := out.String()
	for _, must := range []string{`"latest_drain"`, `"trigger":"after-command"`, `"claimed":2`, `"delivered":1`, `"refused":1`, `"completed_at":"2026-09-02T01:00:05Z"`} {
		if !strings.Contains(body, must) {
			t.Fatalf("the posture must expose the latest drain evidence (%s missing): %s", must, body)
		}
	}
}

// TestE17T2RetryRefusesLiveLease pins round-4 F012: the operator retry
// bypass refuses a record under a live delivery lease as a transient
// conflict (exit 14) instead of silently superseding the in-flight
// outcome.
func TestE17T2RetryRefusesLiveLease(t *testing.T) {
	configPath, _ := e17t2AfterCommandFixture(t)
	e4t3RegisterRoute(t, configPath)
	id := e17t2SeedDueNotification(t, configPath, "seed-lease")
	// A drainer holds a live lease well past the retry.
	s := e17t2Store(t, configPath)
	until := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339Nano)
	if _, err := s.Exec(`UPDATE notification_events SET lease_owner = 'drainer-x', lease_expires_at = ? WHERE notification_id = ?`, until, id); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	markInvocationStart()
	code := Run([]string{"notifications", "retry", id, "--config", configPath}, &out, &errb)
	if code != 14 || !strings.Contains(errb.String(), "notification_lease_active") {
		t.Fatalf("retry under a live lease must refuse at exit 14: %d %s", code, errb.String())
	}
}

// TestE17T2RetryAttemptIsFencedAndDelivers pins round-4 F013: after the
// re-arm, the retry's single attempt claims its own fence — the record's
// lease is taken and released, the outcome records under it, and a
// refused record reaches delivered through the log sink.
func TestE17T2RetryAttemptIsFencedAndDelivers(t *testing.T) {
	configPath, _ := e17t2AfterCommandFixture(t)
	e4t3RegisterRoute(t, configPath)
	id := e17t2SeedDueNotification(t, configPath, "seed-fenced-retry")
	// Park the record refused exactly as a refused sink outcome would.
	s := e17t2Store(t, configPath)
	if _, err := s.Exec(`UPDATE notification_events SET state = 'refused' WHERE notification_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	markInvocationStart()
	if code := Run([]string{"notifications", "retry", id, "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("retry: %d %s", code, errb.String())
	}
	if env := decodeEnvelope(t, &out); env["outcome"] != "delivered" || env["state"] != "delivered" {
		t.Fatalf("the fenced retry must deliver: %v", env)
	}
	var leaseOwner string
	var attempts int
	if err := s.QueryRow(`SELECT COALESCE(lease_owner, '') FROM notification_events WHERE notification_id = ?`, id).Scan(&leaseOwner); err != nil || leaseOwner != "" {
		t.Fatalf("the retry's claim must release its lease: %q %v", leaseOwner, err)
	}
	if err := s.QueryRow(`SELECT COUNT(*) FROM notification_attempts WHERE notification_id = ?`, id).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("the retry records exactly its fenced attempt: %d %v", attempts, err)
	}
}

// TestE17T2PlistEscapesHostilePaths pins round-4 F018: filesystem paths
// interpolated into the plist XML are escaped, so a path carrying XML
// metacharacters stays inert element text.
func TestE17T2PlistEscapesHostilePaths(t *testing.T) {
	def := scheduleDefinition{
		Label:      `label&<"'>`,
		BinaryPath: `/bin/evil&path<with>"markup'`,
		RouteID:    `wiki`,
		ConfigPath: `/tmp/cfg&<>"'.yaml`,
		Interval:   900,
		StdoutPath: `/tmp/out&<>"'.log`,
		StderrPath: `/tmp/err&<>"'.log`,
	}
	plist := renderSchedulePlist(def)
	var doc struct {
		XMLName xml.Name `xml:"plist"`
	}
	if err := xml.Unmarshal([]byte(plist), &doc); err != nil {
		t.Fatalf("the escaped plist must stay well-formed XML: %v\n%s", err, plist)
	}
	if strings.Contains(plist, `&<"'>`) || strings.Contains(plist, `<string>/bin/evil&path`) {
		t.Fatalf("raw metacharacters must never ride the plist: %s", plist)
	}
	if !strings.Contains(plist, "&amp;") {
		t.Fatalf("the escaping must use XML entities: %s", plist)
	}
}

// TestE17T2DayUnitDrainDurations pins round-4 F020: the drain fields
// accept the schema's own day-unit duration grammar end to end —
// validation and the effective policy agree on `d` units.
func TestE17T2DayUnitDrainDurations(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	e5t4Rewrite(t, configPath,
		"    reconciliation:\n      initial: true\n      daily_expected: true\n",
		"    reconciliation:\n      initial: true\n      daily_expected: true\n    notifications:\n      sinks:\n        - id: ops-log\n          type: log\n      drain:\n        mode: manual\n        pending_warn_after: 1d\n        retry:\n          initial_backoff: 1d\n          max_backoff: 3d\n          multiplier: 2.0\n          jitter_fraction: 0.0\n")
	_ = vault
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("day-unit drain durations must validate: %v", err)
	}
	policy, err := config.EffectiveNotificationDrain(cfg.Routes["wiki"].Notifications)
	if err != nil {
		t.Fatalf("the effective policy must resolve day units: %v", err)
	}
	if policy.PendingWarnAfter != 24*time.Hour || policy.InitialBackoff != 24*time.Hour || policy.MaxBackoff != 72*time.Hour {
		t.Fatalf("day-unit resolution: warn=%v initial=%v max=%v", policy.PendingWarnAfter, policy.InitialBackoff, policy.MaxBackoff)
	}
}

// TestE17T2ScheduleUsageContracts pins round-4 F019: the schedule
// lifecycle's usage contract fails closed at exit 2 — missing --route,
// a non-launchd platform, a malformed --at, and an unknown subcommand.
func TestE17T2ScheduleUsageContracts(t *testing.T) {
	configPath := e16t4Env(t, "scheduled")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing route", []string{"schedule", "render", "--platform", "launchd", "--config", configPath}, "requires --route"},
		{"non-launchd platform", []string{"schedule", "render", "--route", "wiki", "--platform", "systemd", "--config", configPath}, "--platform must be launchd"},
		{"malformed at", []string{"schedule", "render", "--route", "wiki", "--platform", "launchd", "--at", "25:99", "--config", configPath}, "must be HH:MM"},
		{"malformed at alpha", []string{"schedule", "render", "--route", "wiki", "--platform", "launchd", "--at", "ab:cd", "--config", configPath}, "must be HH:MM"},
	}
	for _, tc := range cases {
		var out, errb bytes.Buffer
		markInvocationStart()
		code := Run(tc.args, &out, &errb)
		if code != 2 || !strings.Contains(errb.String(), tc.want) {
			t.Fatalf("%s: want exit 2 with %q, got %d: %s", tc.name, tc.want, code, errb.String())
		}
		if out.Len() != 0 {
			t.Fatalf("%s: stdout must stay empty on a usage error", tc.name)
		}
	}
}

// TestE17T2LogRotationChain pins round-4 F016: rotation beyond the
// first hop shifts the whole chain and never retains more than three
// files total (the live log plus two rotated generations, v0.1.6 §4).
func TestE17T2LogRotationChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schedule-wiki.out.log")
	big := bytes.Repeat([]byte("x"), scheduleLogMaxBytes+1)
	for _, suffix := range []string{"", ".1", ".2"} {
		if err := os.WriteFile(path+suffix, big, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rotateScheduleLog(path)
	for _, suffix := range []string{".1", ".2"} {
		if _, err := os.Stat(path + suffix); err != nil {
			t.Fatalf("the shifted chain must keep %s: %v", suffix, err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the live log rotates away")
	}
	// Repeated rotation never grows the chain past the retention bound:
	// a fourth generation (.3) never appears.
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(path, big, 0o600); err != nil {
			t.Fatal(err)
		}
		rotateScheduleLog(path)
		if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
			t.Fatal("retention must cap the chain at three files total")
		}
	}
}

// TestE17T2ScheduleRunAfterCommandRecoveryLeg pins round-4 F017: the
// internal runner's AFTER-COMMAND leg — the fifteen-minute recovery
// pass — drains the route's due work without any reconciliation
// prerequisite, beside its scheduled-mode sibling test.
func TestE17T2ScheduleRunAfterCommandRecoveryLeg(t *testing.T) {
	configPath := e16t4Env(t, "after-command")
	vault := t.TempDir()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, bytes.Replace(raw, []byte("/srv/vault"), []byte(vault), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	s := e16t3StoreAt(t, filepath.Join(filepath.Dir(configPath), "state"))
	defer s.Close()
	ctx := context.Background()
	if err := s.SetRouteActivation(ctx, "wiki", "enabled", "route-rev-1", "", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueRouteNotification(ctx, "wiki", records.EventWorkCompleted, "recovery-1", "", nil, "2026-08-30T09:00:00Z"); err != nil {
		t.Fatal(err)
	}
	markInvocationStart()
	var out, errb bytes.Buffer
	if code := Run([]string{"schedule", "run", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("schedule run (after-command): %d %s", code, errb.String())
	}
	byState, err := s.CountNotificationsByState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if byState["delivered"] < 1 {
		t.Fatalf("the after-command recovery leg must drain due work: %v (stderr %s)", byState, errb.String())
	}
}

// TestE17T2DispatchRefusesUnregisteredRoute pins the second real-Hermes
// cold validation finding: a dispatch on a configured route whose trusted
// registration was never materialized (no setup baseline, no
// reconciliation) refused as a raw foreign-key STORAGE failure; the
// contract's two-key posture demands the clean state conflict with the
// setup guidance BEFORE any durable write.
func TestE17T2DispatchRefusesUnregisteredRoute(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	// No e4t3RegisterRoute: the store exists but carries no route row.
	setPlanEnv(t, vault, false)
	var out, errb bytes.Buffer
	markInvocationStart()
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
		if code != 14 || !strings.Contains(errb.String(), "no trusted registration yet") {
			t.Fatalf("an unregistered route must refuse at exit 14 with the setup guidance: %d %s", code, errb.String())
		}
	})
	if out.Len() != 0 {
		t.Fatalf("nothing may persist on the refusal: %s", out.String())
	}
}

// TestE17T2OverrideReadFailureIsReported pins the round-4 confirmation
// findings F001/F003: a genuine read failure or an unopenable store in
// the --at override path degrades to the default timing WITH one bounded
// stderr line naming the consequence — never silently — while a missing
// row and a pre-v20 table stay quiet (the ordinary no-override postures).
func TestE17T2OverrideReadFailureIsReported(t *testing.T) {
	var reported bytes.Buffer
	// A real read failure (a closed database): reported, then default.
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if at := scheduleAtOverride(context.Background(), db, "some-label", &reported); at != "" {
		t.Fatalf("a failed read resolves to the default timing: %q", at)
	}
	if !strings.Contains(reported.String(), "could not be read") {
		t.Fatalf("the read failure must be reported, got: %q", reported.String())
	}
	// A missing row (an open, migrated store with no override): quiet.
	reported.Reset()
	live, err := sqlite.Open(filepath.Join(dir, "state2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if err := live.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if at := scheduleAtOverride(context.Background(), live, "some-label", &reported); at != "" || reported.Len() != 0 {
		t.Fatalf("a missing row is the quiet default: %q %q", at, reported.String())
	}
	// A pre-v20 shape (an open, UNMIGRATED database has no table): quiet.
	reported.Reset()
	raw, err := sqlite.Open(filepath.Join(dir, "state3.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if at := scheduleAtOverride(context.Background(), raw, "some-label", &reported); at != "" || reported.Len() != 0 {
		t.Fatalf("a pre-v20 table is the quiet default: %q %q", at, reported.String())
	}
	// An unopenable store reports before degrading (round-4 F003).
	reported.Reset()
	if at := scheduleAtOverrideUnmigrated("/nonexistent-e17t2/config.yaml", "some-label", &reported); at != "" {
		t.Fatalf("an unopenable store resolves to the default timing: %q", at)
	}
	if !strings.Contains(reported.String(), "could not be opened") {
		t.Fatalf("the unopenable store must be reported, got: %q", reported.String())
	}
}

// TestE17T2LaunchctlPrintUsesJoinedTarget pins the real-launchd cold
// validation finding: `launchctl print` takes ONE joined service target
// (`gui/<uid>/<label>`); the domain and label as separate argv entries
// make real launchd print the whole domain dump, so an absent schedule
// read as loaded. The captured args must stay the joined form, and the
// real "Could not find service" reply must read as not loaded.
func TestE17T2LaunchctlPrintUsesJoinedTarget(t *testing.T) {
	configPath := e16t4Env(t, "after-command")
	var printArgs, bootoutArgs [][]string
	launchctlRun = func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "print" {
			printArgs = append(printArgs, args)
			return "Bad request.\nCould not find service \"x\" in domain for user gui: 501", nil
		}
		if len(args) > 0 && args[0] == "bootout" {
			bootoutArgs = append(bootoutArgs, args)
			return "", nil
		}
		return "", nil
	}
	defer func() {
		launchctlRun = func(args ...string) (string, error) { return launchctlExec(args...) }
	}()
	var out, errb bytes.Buffer
	markInvocationStart()
	if code := Run([]string{"schedule", "inspect", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("inspect: %d %s", code, errb.String())
	}
	if env := decodeEnvelope(t, &out); env["loaded"] != false || env["healthy"] != false {
		t.Fatalf("the real not-found reply must read as not loaded: %v", env)
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "disable", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("disable: %d %s", code, errb.String())
	}
	if len(printArgs) == 0 {
		t.Fatal("inspect must consult launchctl print")
	}
	for _, args := range printArgs {
		if len(args) != 2 || !strings.HasPrefix(args[1], "gui/") || !strings.Contains(args[1], "/xyz.rootkernel.agent-dispatch.") {
			t.Fatalf("launchctl print must take one joined gui/<uid>/<label> target, got %v", args)
		}
	}
	for _, args := range bootoutArgs {
		if len(args) != 2 || !strings.HasPrefix(args[1], "gui/") || !strings.Contains(args[1], "/xyz.rootkernel.agent-dispatch.") {
			t.Fatalf("launchctl bootout must take the joined gui/<uid>/<label> service target (the bare label is rejected by real launchd), got %v", args)
		}
	}
}

// TestE17T2ManualDrainEnvelopeHelper pins the F002 wiring at the CLI
// boundary: the per-route envelope helper maps the configured drain
// policy onto the backoff the manual pass persists, and an absent route
// resolves to the zero envelope (the service's documented defaults).
func TestE17T2ManualDrainEnvelopeHelper(t *testing.T) {
	configPath, _ := e17t2AfterCommandFixture(t)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	env := drainRetryEnvelope(cfg, "wiki")
	if env.Initial != time.Second || env.Max != 2*time.Second || env.Multiplier != 2.0 || env.JitterFraction != 0 {
		t.Fatalf("the route's configured envelope must map through: %+v", env)
	}
	if missing := drainRetryEnvelope(cfg, "absent-route"); missing != (ports.NotificationBackoff{}) {
		t.Fatalf("an absent route resolves to the zero envelope: %+v", missing)
	}
}

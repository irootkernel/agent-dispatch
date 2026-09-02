package cli

import (
	"bytes"
	"context"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/stubhermes"
)

// e16t4Env prepares a configuration file whose wiki route drains in the
// given mode, with the state directory under a temp root, and returns
// the config path.
func e16t4Env(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	// Hermetic launchd location (round-1 F004): the lifecycle tests
	// never touch the real ~/Library/LaunchAgents.
	agents := filepath.Join(dir, "LaunchAgents")
	savedAgents := launchAgentsDir
	launchAgentsDir = func() string { return agents }
	t.Cleanup(func() { launchAgentsDir = savedAgents })
	cfg := e16t1BaseConfigCLI()
	cfg.Instance.StateDir = filepath.Join(dir, "state")
	cfg.Routes["wiki"].Notifications.Drain = &config.NotificationDrain{Mode: mode}
	configPath := filepath.Join(dir, "config.yaml")
	if err := config.WriteExample(cfg, configPath); err != nil {
		t.Fatal(err)
	}
	return configPath
}

// e16t4Render decodes the render envelope of one schedule definition.
func e16t4Render(t *testing.T, mode, at string) map[string]any {
	t.Helper()
	configPath := e16t4Env(t, mode)
	var out, errb bytes.Buffer
	args := []string{"schedule", "render", "--route", "wiki", "--platform", "launchd", "--config", configPath}
	if at != "" {
		args = append(args, "--at", at)
	}
	markInvocationStart()
	if code := Run(args, &out, &errb); code != 0 {
		t.Fatalf("render: %d %s", code, errb.String())
	}
	return decodeEnvelope(t, &out)
}

// TestE16T4RenderProducesValidLaunchdSyntax pins the launchd syntax
// acceptance: the rendered plist is well-formed XML carrying the direct
// internal runner, the managed label, the resolved paths, and the
// mode-specific timing.
func TestE16T4RenderProducesValidLaunchdSyntax(t *testing.T) {
	for _, tc := range []struct {
		mode     string
		interval bool
	}{
		{"after-command", true},
		{"scheduled", false},
	} {
		env := e16t4Render(t, tc.mode, "")
		plist, _ := env["plist"].(string)
		if plist == "" {
			t.Fatalf("%s: the render must carry the plist text", tc.mode)
		}
		var doc struct {
			XMLName xml.Name `xml:"plist"`
			Dict    struct {
				Keys []string `xml:"key"`
			} `xml:"dict"`
		}
		if err := xml.Unmarshal([]byte(plist), &doc); err != nil {
			t.Fatalf("%s: the plist must be well-formed XML: %v", tc.mode, err)
		}
		if !strings.Contains(plist, "<string>schedule</string>") || !strings.Contains(plist, "<string>run</string>") || !strings.Contains(plist, "<string>wiki</string>") {
			t.Fatalf("%s: the plist must invoke the internal runner directly", tc.mode)
		}
		if strings.Contains(plist, "/bin/sh") {
			t.Fatalf("%s: the plist must never carry a shell chain", tc.mode)
		}
		keys := strings.Join(doc.Dict.Keys, ",")
		if tc.interval && !strings.Contains(keys, "StartInterval") {
			t.Fatalf("after-command recovery must use StartInterval: %s", keys)
		}
		if !tc.interval && !strings.Contains(keys, "StartCalendarInterval") {
			t.Fatalf("scheduled mode must use StartCalendarInterval: %s", keys)
		}
		label, _ := env["label"].(string)
		if !strings.HasPrefix(label, "xyz.rootkernel.agent-dispatch.") {
			t.Fatalf("managed label shape: %q", label)
		}
		if dig, _ := env["digest"].(string); !strings.HasPrefix(dig, "sha256:") {
			t.Fatalf("definition digest: %q", dig)
		}
	}
}

// TestE16T4ScheduledAtOverride pins the --at HH:MM override and the
// 03:00 default.
func TestE16T4ScheduledAtOverride(t *testing.T) {
	env := e16t4Render(t, "scheduled", "")
	if !strings.Contains(env["plist"].(string), "<integer>3</integer>") {
		t.Fatalf("scheduled default must be 03:00: %s", env["plist"])
	}
	env = e16t4Render(t, "scheduled", "05:45")
	plist := env["plist"].(string)
	if !strings.Contains(plist, "<integer>5</integer>") || !strings.Contains(plist, "<integer>45</integer>") {
		t.Fatalf("--at 05:45 must override the calendar: %s", plist)
	}
}

// TestE16T4InstallIdempotentAndConflicting pins the managed-lifecycle
// acceptance: an identical definition installs twice cleanly and a
// different definition is refused.
func TestE16T4InstallIdempotentAndConflicting(t *testing.T) {
	configPath := e16t4Env(t, "after-command")
	launchctlRun = func(args ...string) (string, error) { return "", nil }
	defer func() {
		launchctlRun = func(args ...string) (string, error) {
			out, err := launchctlExec(args...)
			return out, err
		}
	}()
	var out, errb bytes.Buffer
	markInvocationStart()
	if code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("install: %d %s", code, errb.String())
	}
	env := decodeEnvelope(t, &out)
	plistPath, _ := env["plist_path"].(string)
	if plistPath == "" || !strings.HasSuffix(plistPath, ".plist") {
		t.Fatalf("managed plist path: %v", plistPath)
	}
	raw, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatal(err)
	}
	if info, ierr := os.Stat(plistPath); ierr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the managed plist must be owner-only: %v %v", info, ierr)
	}
	// Identical definition: idempotent.
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("idempotent install: %d %s", code, errb.String())
	}
	// A different definition at the same path is refused... the label
	// derives from the config path, so simulate by rewriting the file.
	if err := os.WriteFile(plistPath, []byte(strings.Replace(string(raw), "<integer>900</integer>", "<integer>600</integer>", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code == 0 {
		t.Fatal("a different definition must be refused")
	}
}

// TestE16T4DisablePreservesAndUninstallRemoves pins the lifecycle
// boundaries: disable keeps the plist; uninstall removes exactly the
// managed plist and refuses a foreign file at the same path.
func TestE16T4DisablePreservesAndUninstallRemoves(t *testing.T) {
	configPath := e16t4Env(t, "scheduled")
	launchctlRun = func(args ...string) (string, error) { return "", nil }
	defer func() {
		launchctlRun = func(args ...string) (string, error) {
			return launchctlExec(args...)
		}
	}()
	var out, errb bytes.Buffer
	markInvocationStart()
	if code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("install: %d %s", code, errb.String())
	}
	plistPath := decodeEnvelope(t, &out)["plist_path"].(string)
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "disable", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("disable: %d %s", code, errb.String())
	}
	if _, err := os.Stat(plistPath); err != nil {
		t.Fatal("disable must preserve the plist")
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "uninstall", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("uninstall: %d %s", code, errb.String())
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatal("uninstall must remove the managed plist")
	}
	// A foreign file at the same path is refused.
	if err := os.WriteFile(plistPath, []byte("<plist version=\"1.0\"><dict><key>Label</key><string>foreign</string></dict></plist>"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "uninstall", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code == 0 {
		t.Fatal("uninstall must refuse a non-managed plist")
	}
	if _, err := os.Stat(plistPath); err != nil {
		t.Fatal("the foreign plist must survive the refusal")
	}
}

// TestE16T4InspectReportsHealth pins the inspection acceptance:
// presence, loaded state, and the definition-digest match.
func TestE16T4InspectReportsHealth(t *testing.T) {
	configPath := e16t4Env(t, "after-command")
	launchctlRun = func(args ...string) (string, error) { return "", nil }
	defer func() {
		launchctlRun = func(args ...string) (string, error) {
			return launchctlExec(args...)
		}
	}()
	var out, errb bytes.Buffer
	markInvocationStart()
	if code := Run([]string{"schedule", "inspect", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("inspect: %d %s", code, errb.String())
	}
	env := decodeEnvelope(t, &out)
	if env["present"] != false || env["healthy"] != false {
		t.Fatalf("an absent schedule is unhealthy: %v", env)
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("install: %d %s", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "inspect", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("inspect: %d %s", code, errb.String())
	}
	env = decodeEnvelope(t, &out)
	if env["present"] != true || env["loaded"] != true || env["healthy"] != true {
		t.Fatalf("an installed loaded matching schedule is healthy: %v", env)
	}
}

// TestE16T4LogRotation pins the 10 MiB × 3 rotation posture.
func TestE16T4LogRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schedule-wiki.out.log")
	big := bytes.Repeat([]byte("x"), scheduleLogMaxBytes+1)
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatal(err)
	}
	rotateScheduleLog(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("an over-bound log rotates away")
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatal("the rotated file lands at .1")
	}
}

// launchctlExec is the real runner the test stub restores.
func launchctlExec(args ...string) (string, error) {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	return string(out), err
}

// TestE16T4EnablementRequiresSchedule pins the production gate: an
// after-command route cannot enable without an installed, loaded,
// definition-matching schedule, while preflight only warns.
func TestE16T4EnablementRequiresSchedule(t *testing.T) {
	configPath := e16t4Env(t, "after-command")
	// The two-key gate needs the configuration key on.
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, bytes.Replace(raw, []byte("enabled: false"), []byte("enabled: true"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	// No schedule installed: enablement refuses.
	var out, errb bytes.Buffer
	markInvocationStart()
	code := Run([]string{"route", "enable", "--route", "wiki", "--acknowledge-production-gate", "any", "--yes", "--config", configPath}, &out, &errb)
	if code == 0 || !strings.Contains(errb.String(), "managed schedule") {
		t.Fatalf("enablement must require the schedule: %d %s", code, errb.String())
	}
	// Preflight reports the schedule prerequisite as its own warn check,
	// never as a failing check of its own (other checks may fail for
	// their own reasons, as here: no live Hermes board).
	out.Reset()
	errb.Reset()
	markInvocationStart()
	_ = Run([]string{"route", "preflight", "--route", "wiki", "--config", configPath}, &out, &errb)
	combined := out.String() + errb.String()
	scheduleAt := strings.Index(combined, `"check":"schedule"`)
	if scheduleAt < 0 {
		t.Fatalf("preflight must carry the schedule check: %s", combined)
	}
	// The check object marshals its keys alphabetically, so the state
	// rides after the detail and remediation: take the object up to the
	// next check (or the end) and require its own state to be warn.
	end := strings.Index(combined[scheduleAt+1:], `"check":"`)
	if end < 0 {
		end = len(combined) - scheduleAt
	}
	window := combined[scheduleAt : scheduleAt+end]
	if !strings.Contains(window, `"state":"warn"`) {
		t.Fatalf("the schedule check must warn, not fail: %s", window)
	}
}

// TestE16T4ScheduleRunDrainsScheduledRoute pins round-1 F001's
// remediation: the internal runner drains its own route under either
// automatic mode — a scheduled route's due work advances after the
// runner even without a reconciliation dispatch.
func TestE16T4ScheduleRunDrainsScheduledRoute(t *testing.T) {
	// Capability evidence is operator-global by default. Keep this
	// fixture independent from both the operator cache and sibling tests.
	t.Setenv("HOME", t.TempDir())
	configPath := e16t4Env(t, "scheduled")
	// The scheduled runner reconciles first: give the fixture a real
	// vault root so the reconciliation can enumerate an empty snapshot,
	// and a stub Hermes so the ten-second drain budget never depends on
	// an installed operator binary's startup or profile state.
	vault := t.TempDir()
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	resource := cfg.Resources["vault-main"]
	resource.Root = vault
	cfg.Resources["vault-main"] = resource
	target := cfg.HermesTargets["hermes-main"]
	target.Executable = stubhermes.Write(t)
	cfg.HermesTargets["hermes-main"] = target
	configPath = filepath.Join(filepath.Dir(configPath), "scheduled-fixture.yaml")
	if err := config.WriteExample(cfg, configPath); err != nil {
		t.Fatal(err)
	}
	// The runner opens the configuration's own state store, so the due
	// work must live there.
	s := e16t3StoreAt(t, filepath.Join(filepath.Dir(configPath), "state"))
	if err := s.SetRouteActivation(context.Background(), "wiki", "enabled", "route-rev-1", "", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	// Pin the drift fixture locally. Before the Hermes test environment
	// was isolated, an operator capability cache could accidentally
	// supply the second notification this assertion expects.
	if err := s.SaveWatchBinding(context.Background(), watchman.Binding{
		RouteID: "wiki", ResourceID: "vault-main",
		ConfiguredRoot: vault, ActualRoot: vault, RelativeRoot: ".",
		TriggerName: "stale-trigger", UpdatedAt: "2026-08-30T09:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.EnqueueRouteNotification(ctx, "wiki", records.EventWorkCompleted, "sched-run-1", "", nil, "2026-08-30T09:00:00Z"); err != nil {
		t.Fatal(err)
	}
	markInvocationStart()
	var out, errb bytes.Buffer
	code := Run([]string{"schedule", "run", "--route", "wiki", "--config", configPath}, &out, &errb)
	if code != 0 {
		t.Fatalf("schedule run: %d %s", code, errb.String())
	}
	byState, err := s.CountNotificationsByState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The runner carries the drift evaluation (its only automatic
	// surface since v0.1.6 §4): the fixture's stale binding enqueues its
	// watchman-drift intent beside the seeded work, and the scheduled
	// pass delivers both.
	if byState["delivered"] != 2 {
		t.Fatalf("the scheduled runner must drain its route's due and drift work: %v (stderr: %s)", byState, errb.String())
	}
}

// TestE16T4ScheduleRunSkipsDrainWhenReconciliationFails pins the
// scheduled runner's chaining guard (round-2 F002): when the scheduled
// reconciliation exits nonzero, the runner propagates the failure,
// never drains, and leaves no drain evidence — a regression that
// reorders the drain ahead of the reconciliation or ignores the exit
// code now fails here.
func TestE16T4ScheduleRunSkipsDrainWhenReconciliationFails(t *testing.T) {
	configPath := e16t4Env(t, "scheduled")
	// A resource root that cannot exist makes the scheduled
	// reconciliation's snapshot enumeration fail closed.
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(filepath.Dir(configPath), "missing-vault")
	if err := os.WriteFile(configPath, bytes.Replace(raw, []byte("/srv/vault"), []byte(missing), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	s := e16t3StoreAt(t, filepath.Join(filepath.Dir(configPath), "state"))
	ctx := context.Background()
	if err := s.SetRouteActivation(ctx, "wiki", "enabled", "route-rev-1", "", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueRouteNotification(ctx, "wiki", records.EventWorkCompleted, "sched-run-neg", "", nil, "2026-08-30T09:00:00Z"); err != nil {
		t.Fatal(err)
	}
	markInvocationStart()
	var out, errb bytes.Buffer
	code := Run([]string{"schedule", "run", "--route", "wiki", "--config", configPath}, &out, &errb)
	if code == 0 {
		t.Fatalf("a failing reconciliation must exit nonzero: %s", errb.String())
	}
	byState, err := s.CountNotificationsByState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if byState["delivered"] != 0 || byState["pending"] < 1 {
		t.Fatalf("an unhealthy reconciliation must leave the due work undrained: %v", byState)
	}
	var runs int
	if err := s.QueryRow(`SELECT COUNT(*) FROM drain_runs WHERE route_id = 'wiki'`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("the runner must leave no drain evidence on a failed reconciliation: %d %v", runs, err)
	}
}

// TestE16T4StatusAndDoctorProjectDrainPosture pins the AC-1206
// operator surface (round-2 F003): the status envelope's
// notification_drain projection and the doctor findings carry the
// due/backoff split, live claims, the overdue warning window, repeated
// ambiguous/retryable outcomes, unresolvable sinks, and the scheduler
// expectation/overdue state without direct SQLite inspection.
func TestE16T4StatusAndDoctorProjectDrainPosture(t *testing.T) {
	dir := t.TempDir()
	agents := filepath.Join(dir, "LaunchAgents")
	savedAgents := launchAgentsDir
	launchAgentsDir = func() string { return agents }
	t.Cleanup(func() { launchAgentsDir = savedAgents })
	vault := filepath.Join(dir, "vault")
	if err := os.Mkdir(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := e16t1BaseConfigCLI()
	cfg.Instance.StateDir = filepath.Join(dir, "state")
	res := cfg.Resources["vault-main"]
	res.Root = vault
	cfg.Resources["vault-main"] = res
	cfg.Routes["wiki"].Notifications.Drain = &config.NotificationDrain{Mode: "after-command"}
	// A config-valid webhook sink whose endpoint embeds userinfo: the
	// resolver rejects it (SEC-013), so the route carries exactly one
	// unresolvable sink declaration.
	cfg.Routes["wiki"].Notifications.Sinks = []config.NotificationSink{
		{ID: "ops-log", Type: "log"},
		{ID: "bad-hook", Type: "webhook", Endpoint: "https://alice@hooks.invalid/v1", Auth: &config.Auth{Type: "bearer", SecretRef: "keychain://agent-dispatch/bad-hook"}},
	}
	configPath := filepath.Join(dir, "config.yaml")
	if err := config.WriteExample(cfg, configPath); err != nil {
		t.Fatal(err)
	}
	s := e16t3StoreAt(t, cfg.Instance.StateDir)
	ctx := context.Background()
	seed := func(id string) {
		t.Helper()
		if _, err := s.EnqueueRouteNotification(ctx, "wiki", records.EventWorkCompleted, id, "", nil, "2026-08-30T09:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	seed("plain-due")
	seed("live-claim")
	seed("backoff-retry")
	// One live unclaimed-forever claim and one future-due backoff whose
	// last attempt is ambiguous (the repeated-outcome posture).
	claimed, err := s.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 10, Owner: "posture-claimer"})
	if err != nil {
		t.Fatal(err)
	}
	var backoffClaim, liveClaim, plainDue ports.NotificationClaim
	for _, c := range claimed {
		switch c.Transition {
		case "live-claim":
			liveClaim = c
		case "backoff-retry":
			backoffClaim = c
		case "plain-due":
			plainDue = c
		}
	}
	if liveClaim.NotificationID == "" || backoffClaim.NotificationID == "" || plainDue.NotificationID == "" {
		t.Fatalf("the posture seed must hold all three claims: %+v", claimed)
	}
	// plain-due returns to the pool unclaimed; live-claim keeps its
	// lease for the live-claims posture.
	if err := s.ReleaseNotificationClaims(ctx, "posture-claimer", []ports.NotificationClaim{plainDue}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordNotificationAttemptFenced(ctx, ports.NotificationAttemptInput{
		NotificationID: backoffClaim.NotificationID, Outcome: records.NotificationAmbiguousOutcome, ErrorCode: "transport",
		StartedAt: "2099-01-01T00:00:10Z", CompletedAt: "2099-01-01T00:00:11Z",
	}, backoffClaim, ports.NotificationBackoff{Initial: 30 * time.Second, Max: 15 * time.Minute, Multiplier: 2, JitterFraction: 0}); err != nil {
		t.Fatal(err)
	}
	// The status envelope projects the whole posture.
	markInvocationStart()
	var out, errb bytes.Buffer
	if code := Run([]string{"status", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("status: %d %s", code, errb.String())
	}
	env := decodeEnvelope(t, &out)
	drain, ok := env["notification_drain"].(map[string]any)
	if !ok {
		t.Fatalf("the status envelope must carry notification_drain: %v", env)
	}
	row, ok := drain["wiki"].(map[string]any)
	if !ok {
		t.Fatalf("notification_drain must carry the wiki route: %v", drain)
	}
	for _, check := range []struct {
		key  string
		want any
	}{
		{"mode", "after-command"},
		{"due", float64(2)},
		{"backoff", float64(1)},
		{"live_claims", float64(1)},
		{"repeated_retry_outcomes", float64(1)},
		{"unresolvable_sinks", float64(1)},
		{"pending_overdue", true},
		{"scheduler_expected", true},
		{"scheduler_overdue", true},
	} {
		if got := row[check.key]; got != check.want {
			t.Fatalf("posture %s = %v, want %v (row %v)", check.key, got, check.want, row)
		}
	}
	if _, ok := row["oldest_pending_at"]; !ok {
		t.Fatalf("the posture must carry the oldest pending timestamp: %v", row)
	}
	// Doctor maps the posture onto typed findings; the unresolvable sink
	// and the overdue schedule are error-severity, so the command exits
	// with the stable doctor code.
	markInvocationStart()
	out.Reset()
	errb.Reset()
	if code := Run([]string{"doctor", "--config", configPath}, &out, &errb); code != 3 {
		t.Fatalf("doctor must exit 3 on error-severity findings: %d %s", code, errb.String())
	}
	env = decodeEnvelope(t, &out)
	findings, _ := env["findings"].([]any)
	codes := map[string]bool{}
	for _, f := range findings {
		if m, ok := f.(map[string]any); ok {
			if c, ok := m["code"].(string); ok {
				codes[c] = true
			}
		}
	}
	for _, want := range []string{"notification_pending_overdue", "notification_repeated_retry", "notification_sink_unresolvable", "schedule_overdue"} {
		if !codes[want] {
			t.Fatalf("doctor must report %s (codes %v)", want, codes)
		}
	}
}

// TestE16T4DriftedDefinitionIsUnhealthyAndBlocksEnablement pins the
// definition-matching posture (round-2 F007): a present, loaded
// schedule whose installed bytes drifted from the managed definition
// reports definition_matches false and healthy false, and production
// enablement refuses on it.
func TestE16T4DriftedDefinitionIsUnhealthyAndBlocksEnablement(t *testing.T) {
	configPath := e16t4Env(t, "after-command")
	launchctlRun = func(args ...string) (string, error) { return "", nil }
	defer func() {
		launchctlRun = func(args ...string) (string, error) {
			return launchctlExec(args...)
		}
	}()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, bytes.Replace(raw, []byte("enabled: false"), []byte("enabled: true"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	markInvocationStart()
	var out, errb bytes.Buffer
	if code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("install: %d %s", code, errb.String())
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		t.Fatal(err)
	}
	plist := schedulePlistPath(scheduleLabel("test", "wiki", abs))
	installed, err := os.ReadFile(plist)
	if err != nil {
		t.Fatal(err)
	}
	drifted := bytes.Replace(installed, []byte("<integer>900</integer>"), []byte("<integer>901</integer>"), 1)
	if bytes.Equal(drifted, installed) {
		t.Fatal("the drift fixture must modify the installed definition")
	}
	if err := os.WriteFile(plist, drifted, 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "inspect", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("inspect: %d %s", code, errb.String())
	}
	env := decodeEnvelope(t, &out)
	if env["present"] != true || env["loaded"] != true || env["definition_matches"] != false || env["healthy"] != false {
		t.Fatalf("a drifted definition is unhealthy even while loaded: %v", env)
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	code := Run([]string{"route", "enable", "--route", "wiki", "--acknowledge-production-gate", "any", "--yes", "--config", configPath}, &out, &errb)
	if code == 0 || !strings.Contains(errb.String(), "managed schedule") {
		t.Fatalf("enablement must refuse a drifted definition: %d %s", code, errb.String())
	}
}

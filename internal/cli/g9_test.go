package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeskanban"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/hermesenv"
)

// G9 acceptance coverage (E13-T3, AC-905): the complete operational
// walkthrough — detection through the completion receipt to the
// configured notification on two destinations in an isolated
// environment (BND-003/BND-004, TST-012..TST-014) — plus the
// failure-diagnosis, disable, trigger-removal, notification-retry, and
// rollback walkthrough, and the versioned skills' packaging proof.

// g9NotificationFixture builds the shared two-destination fixture
// with both notification sinks wired to the loopback capture endpoint
// (the E13-T2 scaffolding, parameterized for the walkthrough's token).
func g9NotificationFixture(t *testing.T) (string, string, *e13t2WebhookFixture) {
	t.Helper()
	f := newNotificationWebhookFixture(t, "G9_NOTIFICATION_TOKEN", "g9-walkthrough-token", 2*time.Second)
	return f.configPath, f.vault, f
}

// TestG9AC905IsolatedTwoDestinationNotificationWalkthrough proves
// AC-905's core loop deterministically: one vault change detected on a
// two-destination route fans out to both lanes, both runs complete
// through the work-receipt surface, and the configured notification
// carries the completion to both sinks — all inside the disposable
// fixture, with no production state or Hermes source touched
// (BND-003/BND-004).
func TestG9AC905IsolatedTwoDestinationNotificationWalkthrough(t *testing.T) {
	configPath, vault, f := g9NotificationFixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	e12t2Enable(t, configPath)
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "g9-walkthrough.md"), []byte("ac-905 walkthrough note"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/g9-walkthrough.md","exists":true,"new":true,"size":20,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb); code != 0 {
			t.Fatalf("dispatch: %s", errb.String())
		}
	})
	store := e5t1Store(t, configPath)
	defer store.Close()
	both := map[string]string{}
	for _, lane := range []string{"main", "review"} {
		var id string
		if err := store.QueryRow(`SELECT dispatch_id FROM child_dispatches WHERE destination_id = ?`, lane).Scan(&id); err != nil {
			t.Fatalf("lane %s child: %v", lane, err)
		}
		both[lane] = id
	}
	for _, lane := range []string{"main", "review"} {
		out.Reset()
		errb.Reset()
		if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", both[lane], "--run-id", "g9-" + lane}, &out, &errb); code != 0 {
			t.Fatalf("work begin %s: %s", lane, errb.String())
		}
		out.Reset()
		errb.Reset()
		withStdin(t, `[]`, func() {
			if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", both[lane], "--run-id", "g9-" + lane, "--manifest", "-"}, &out, &errb); code != 0 {
				t.Fatalf("work complete %s: %s", lane, errb.String())
			}
		})
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"notifications", "drain", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("notifications drain: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	drain, _ := res["drain"].(map[string]any)
	// The drain delivers each sink's intent: both lanes' completions on
	// the log and webhook sinks plus the drift evaluation's watchman
	// finding (the fixture route has no installed binding).
	if drain["delivered"] != float64(6) {
		t.Fatalf("the walkthrough's notifications must all deliver: %v", res)
	}
	payloads := f.deliveredPayloads()
	completions := 0
	for _, payload := range payloads {
		if !strings.Contains(payload, "notification-event/v1") {
			t.Fatalf("each delivery must carry the notification record: %s", payload)
		}
		if strings.Contains(payload, `"event":"work_completed"`) {
			completions++
		}
		if strings.Contains(payload, "ac-905 walkthrough note") || strings.Contains(payload, "g9-walkthrough-token") {
			t.Fatalf("the notification must carry no note body or credential: %s", payload)
		}
		// The hostile-paths leg of AC-904: no absolute path of the
		// walkthrough environment (the disposable vault and the fixture
		// config) enters the channel-neutral payload.
		if strings.Contains(payload, vault) || strings.Contains(payload, configPath) {
			t.Fatalf("the notification must carry no hostile path: %s", payload)
		}
	}
	if completions != 2 {
		t.Fatalf("the webhook sink must have received both completions: %d of %d", completions, len(payloads))
	}
	if !strings.Contains(errb.String(), "notification-event/v1") {
		t.Fatalf("the log sink's structured lines belong on stderr: %.200s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"events", "show", "--config", configPath, e12t3AggregateOf(t, configPath)}, &out, &errb); code != 0 {
		t.Fatalf("events show: %s", errb.String())
	}
	shown := decodeEnvelope(t, &out)
	if shown["aggregate_status"] != "completed" {
		t.Fatalf("both lanes' receipts must complete the aggregate: %v", shown["aggregate_status"])
	}
}

// TestG9AC905FailureDiagnosisDisableRemovalRetryRollback proves the
// operational lifecycle around the loop: a failed run is diagnosed
// through the inspection surfaces, a refused notification retries under
// its stable identity, disable and managed-trigger removal preserve
// history and leave no managed trigger, and the quiesced system keeps
// every record (the rollback posture).
func TestG9AC905FailureDiagnosisDisableRemovalRetryRollback(t *testing.T) {
	configPath, vault, f := g9NotificationFixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	e12t2Enable(t, configPath)
	os.WriteFile(filepath.Join(vault, "Inbox", "g9-lifecycle.md"), []byte("lifecycle"), 0o644)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/g9-lifecycle.md","exists":true,"new":true,"size":9,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb); code != 0 {
			t.Fatalf("dispatch: %s", errb.String())
		}
	})
	store := e5t1Store(t, configPath)
	defer store.Close()
	var mainChild string
	if err := store.QueryRow(`SELECT dispatch_id FROM child_dispatches WHERE destination_id = 'main'`).Scan(&mainChild); err != nil {
		t.Fatal(err)
	}
	// The aggregate is resolved BEFORE the receipts: work fail schedules
	// the same-lane retry follow-up as a NEW occurrence, and resolving
	// the newest child afterwards would race between the aggregates.
	aggregateID := e12t3AggregateOf(t, configPath)
	// Failure diagnosis: the failed run surfaces through the inspection
	// commands, never by editing state.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "g9-fail"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "fail", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "g9-fail", "--failure-code", "agent_error"}, &out, &errb); code != 0 {
		t.Fatalf("work fail: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"events", "show", "--config", configPath, aggregateID}, &out, &errb); code != 0 {
		t.Fatalf("events show: %s", errb.String())
	}
	if res := decodeEnvelope(t, &out); res["aggregate_status"] != "failed" {
		t.Fatalf("the failed lane must be diagnosable through events show: %v", res["aggregate_status"])
	}
	// Notification retry: the endpoint refuses, the operator fixes it,
	// and the explicit retry delivers under the same identity.
	f.setStatus(http.StatusForbidden, 0)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"notifications", "drain", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain under refusal: %s", errb.String())
	}
	var refusedID string
	if err := store.QueryRow(`SELECT notification_id FROM notification_events WHERE sink_id = 'ops-webhook' AND state = 'refused' LIMIT 1`).Scan(&refusedID); err != nil {
		t.Fatalf("the refused notification must stay inspectable: %v", err)
	}
	f.setStatus(http.StatusOK, 0)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"notifications", "retry", refusedID, "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("notifications retry: %s", errb.String())
	}
	if res := decodeEnvelope(t, &out); res["outcome"] != "delivered" {
		t.Fatalf("the retried notification must deliver: %v", res)
	}
	// Disable: the route pauses with every record preserved.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"route", "disable", "--config", configPath, "--route", "wiki"}, &out, &errb); code != 0 {
		t.Fatalf("route disable: %s", errb.String())
	}
	var dispatchCount int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&dispatchCount); err != nil || dispatchCount == 0 {
		t.Fatalf("disable must preserve the durable history: %d %v", dispatchCount, err)
	}
	// Managed-trigger removal: nothing left behind, history intact.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"watchman", "remove", "--route", "wiki", "--config", configPath, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("watchman remove: %s", errb.String())
	}
	var bindingCount int
	if err := store.QueryRow(`SELECT COUNT(*) FROM watch_bindings WHERE route_id = 'wiki'`).Scan(&bindingCount); err != nil || bindingCount != 0 {
		t.Fatalf("the managed trigger must leave no binding: %d %v", bindingCount, err)
	}
	var auditCount int
	if err := store.QueryRow(`SELECT COUNT(*) FROM state_transitions`).Scan(&auditCount); err != nil || auditCount == 0 {
		t.Fatalf("removal must preserve the audit history: %d %v", auditCount, err)
	}
	// The rollback posture: the quiesced system still answers every
	// inspection with the full history.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"status", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("status after rollback: %s", errb.String())
	}
	if res := decodeEnvelope(t, &out); res["routes"] == nil {
		t.Fatal("status must still project the disabled route's history")
	}
}

// TestG9SkillsVersionedValidatedAndInstallable proves the packaging
// surface: both distributable skills carry their versioned frontmatter
// with the E13-T3 guidance, their bytes are covered by the docs
// manifest, and the operator skill installs and lists through the
// documented public mechanism in a disposable Hermes profile (the
// worker skill's identical mechanism is proven by its E5-T2 harness).
func TestG9SkillsVersionedValidatedAndInstallable(t *testing.T) {
	operator, err := os.ReadFile("../../docs/skills/agent-dispatch-operator/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := os.ReadFile("../../docs/skills/agent-dispatch-wiki-maintenance/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{"version: 2.0.0", "notifications drain", "notifications retry", "notifications test", "route disable", "watchman remove"} {
		if !strings.Contains(string(operator), must) {
			t.Fatalf("the operator skill must carry its v0.1.5 guidance (%q missing)", must)
		}
	}
	for _, must := range []string{"version: 1.2.0", "workstream", "excluded", "work-receipt/v2", "untrusted"} {
		if !strings.Contains(string(worker), must) {
			t.Fatalf("the worker skill must carry its scope and receipt guidance (%q missing)", must)
		}
	}
	manifest, err := os.ReadFile("../../docs/MANIFEST.sha256")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"skills/agent-dispatch-operator/SKILL.md",
		"skills/agent-dispatch-operator/INSTALL.md",
		"skills/agent-dispatch-wiki-maintenance/SKILL.md",
		"skills/agent-dispatch-wiki-maintenance/INSTALL.md",
	} {
		if !strings.Contains(string(manifest), " "+path+"\n") {
			t.Fatalf("the docs manifest must cover %s", path)
		}
	}
	// Installability through the documented public mechanism: the local
	// copy of the operator skill lists in a disposable Hermes profile
	// (INSTALL.md option A; skip-guarded as environment-dependent).
	hermesenv.SkipUnlessSupportedHermes(t, func(firstLine string) bool {
		ver, perr := hermeskanban.ParseVersionOutput(firstLine)
		return perr == nil && ver.Eligible(hermeskanban.MinimumEligibleVersion)
	})
	home := t.TempDir()
	skillsDir := filepath.Join(home, ".hermes", "skills", "operations")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := filepath.Abs("../../docs/skills/agent-dispatch-operator")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cp", "-R", src, skillsDir).CombinedOutput(); err != nil {
		t.Fatalf("installing the operator skill into the disposable profile: %v: %s", err, out)
	}
	cmd := exec.Command("hermes", "skills", "list")
	cmd.Env = append(os.Environ(), "HOME="+home)
	listing, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("skills list under the disposable profile unavailable: %v: %s", err, listing)
	}
	if !strings.Contains(string(listing), "agent-dispatch-operator") {
		t.Fatalf("the disposable profile must list the installed operator skill: %.200s", listing)
	}
}

// TestG9RealHermesNotificationWalkthrough is the isolated real-Hermes
// leg of AC-905 (TST-007/TST-012 posture): an isolated HOME with a
// disposable board, a real Watchman binding on a disposable vault, two
// destinations driven from detection through the completion receipt to
// the delivered notification — and the managed trigger removed
// afterwards with the board hard-deleted exactly as the G8 walkthrough
// did. Skipped as an explicit environment-dependent evidence gap when
// no supported Hermes is installed; the deterministic leg above owns
// the gate.
func TestG9RealHermesNotificationWalkthrough(t *testing.T) {
	hermesenv.SkipUnlessSupportedHermes(t, func(firstLine string) bool {
		ver, perr := hermeskanban.ParseVersionOutput(firstLine)
		return perr == nil && ver.Eligible(hermeskanban.MinimumEligibleVersion)
	})
	f := newNotificationWebhookFixture(t, "G9_NOTIFICATION_TOKEN", "g9-real-token", 2*time.Second)
	// The fixture's config is rebuilt below for the real target, but the
	// capture endpoint and client override stay live.
	capture := f
	_ = capture

	bin, _ := exec.LookPath("hermes")
	realHome := os.Getenv("HOME")
	cleanupEnv := func(argv ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, argv...)
		cmd.Env = append(os.Environ(), "HOME="+realHome)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if help, herr := cleanupEnv("kanban", "create", "-h"); herr != nil || !strings.Contains(help, "--mutex-key") {
		t.Skipf("installed hermes create surface drifted from the frozen 0.19.1 flags: %s", help)
	}
	board := fmt.Sprintf("agent-dispatch-g9-%d", time.Now().UnixNano())
	if out, err := cleanupEnv("kanban", "boards", "create", board); err != nil {
		t.Skipf("boards create unavailable (%v): %s", err, out)
	}
	t.Logf("disposable walkthrough board: %s", board)
	defer func() {
		if out, err := cleanupEnv("kanban", "boards", "rm", board, "--delete"); err != nil {
			t.Logf("best-effort cleanup of the disposable board %s failed (%v): %s — remove it manually", board, err, out)
		}
	}()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AGENT_DISPATCH_CONFIG", "")
	t.Setenv("AGENT_DISPATCH_STATE_DIR", filepath.Join(home, "state"))
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	if err := os.MkdirAll(filepath.Join(vault, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "g9-real.md"), []byte("real walkthrough"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	raw := fmt.Sprintf(`version: 1
instance:
  id: g9-real
  state_dir: %[1]s
resources:
  vault-main:
    type: directory
    root: %[2]s
    file_scope: markdown
hermes_targets:
  hermes-main:
    board: %[3]s
    minimum_version: 0.20.5
    compatibility: capability_probe
    executable: %[4]s
    submit_timeout: 30s
    lookup_timeout: 30s
    environment_allowlist: [PATH, HOME]
routes:
  wiki:
    enabled: true
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: agent-dispatch.wiki.g9
      include: ["**/*.md"]
      exclude: [".obsidian/workspace*.json"]
    batching:
      automatic_threshold: 25
      hard_limit: 100
      max_manifest_bytes: 262144
    policy:
      protected: []
      immutable: []
      bulk_action: quarantine
      overflow_action: reconcile
      fresh_instance_action: reconcile
      unsafe_path_action: quarantine
    fanout_mode: all
    destinations:
      - id: main
        target: hermes-main
        profile: default
        skills: [llm-wiki]
        workstream: main
        execution_hints:
          max_runtime: 30m
          max_attempts: 2
      - id: review
        target: hermes-main
        profile: default
        skills: [llm-wiki]
        workstream: review
        execution_hints:
          max_runtime: 30m
          max_attempts: 2
    notifications:
      sinks:
        - id: ops-log
          type: log
        - id: ops-webhook
          type: webhook
          endpoint: %[5]s
          auth:
            type: bearer
            secret_ref: env:G9_NOTIFICATION_TOKEN
    submission_retry:
      max_attempts: 3
      initial_backoff: 1s
      max_backoff: 2s
      multiplier: 2.0
      jitter_fraction: 0.0
    latest_state: true
    failure_budget: 2
    active_stale_after: 2h
    reconciliation:
      initial: true
      daily_expected: true
`, filepath.Join(home, "state"), vault, board, bin, capture.server.URL)
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	setPlanEnv(t, vault, false)
	var out, errb bytes.Buffer
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	rev, _ := config.RouteRevision(cfg, "wiki")
	if code := Run([]string{"route", "enable", "--config", cfgPath, "--route", "wiki", "--acknowledge-production-gate", rev, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("real enable: %s", errb.String())
	}
	// The real Watchman binding: install the managed trigger against the
	// disposable vault (skip-guarded where watchman is unusable).
	out.Reset()
	errb.Reset()
	if code := Run([]string{"watchman", "install", "--config", cfgPath, "--route", "wiki"}, &out, &errb); code != 0 {
		t.Skipf("real watchman binding unavailable in this environment: %s", errb.String())
	}
	// Detection: the event feed the managed trigger would carry.
	out.Reset()
	errb.Reset()
	withStdin(t, `[{"name":"Inbox/g9-real.md","exists":true,"new":true,"size":16,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", cfgPath, "--input", "watchman"}, &out, &errb); code != 0 {
			t.Fatalf("real dispatch: %s", errb.String())
		}
	})
	res := decodeEnvelope(t, &out)
	if res["submitted"] != true {
		t.Fatalf("the first lane must submit against the real Hermes: %v", res)
	}
	store, err := sqlite.Open(filepath.Join(platformpaths.ResolveStateDir(filepath.Join(home, "state")), StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	both := map[string]string{}
	for _, lane := range []string{"main", "review"} {
		var id string
		if err := store.QueryRow(`SELECT dispatch_id FROM child_dispatches WHERE destination_id = ?`, lane).Scan(&id); err != nil {
			t.Fatalf("lane %s child: %v", lane, err)
		}
		both[lane] = id
	}
	for _, lane := range []string{"main", "review"} {
		manifest := filepath.Join(t.TempDir(), lane+".json")
		os.WriteFile(manifest, []byte(`[]`), 0o600)
		var wOut, wErr bytes.Buffer
		if code := Run([]string{"work", "begin", "--config", cfgPath, "--dispatch-id", both[lane], "--run-id", "run-" + lane}, &wOut, &wErr); code != 0 {
			t.Fatalf("real work begin %s: %s", lane, wErr.String())
		}
		wOut.Reset()
		wErr.Reset()
		if code := Run([]string{"work", "complete", "--config", cfgPath, "--dispatch-id", both[lane], "--run-id", "run-" + lane, "--manifest", manifest}, &wOut, &wErr); code != 0 {
			t.Fatalf("real work complete %s: %s", lane, wErr.String())
		}
	}
	// The notification leg: the drain delivers both completions to the
	// configured sinks, with the capture proving the payload.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"notifications", "drain", "--config", cfgPath}, &out, &errb); code != 0 {
		t.Fatalf("real notifications drain: %s", errb.String())
	}
	// The real-leg assertion is by content, never exact total: both
	// lanes' work_completed payloads must arrive, and the drain's drift
	// evaluation may legitimately add drift findings on the same sink
	// (the CHANGELOG 1.1.18 remediation this matches).
	completions := 0
	for _, payload := range f.deliveredPayloads() {
		if strings.Contains(payload, `"event":"work_completed"`) {
			completions++
		}
	}
	if completions != 2 {
		t.Fatalf("the real walkthrough must notify both completions by content: %d of %d payloads", completions, len(f.deliveredPayloads()))
	}
	// Managed-trigger removal leaves no binding behind.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"watchman", "remove", "--route", "wiki", "--config", cfgPath, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("watchman remove: %s", errb.String())
	}
}

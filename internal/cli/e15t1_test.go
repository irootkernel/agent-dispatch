package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fmt"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/stubhermes"
)

// e15t2ArrivalLineage builds one direct child lineage for a route's
// main lane: the g8 stress shape pinned to this suite's IDs.
func e15t2ArrivalLineage(t *testing.T, routeID string, n int) ports.Lineage {
	t.Helper()
	now := "2026-08-31T00:00:00Z"
	projection := `{"id":"main","workstream":"main"}`
	revision := records.RevisionOfProjection(projection)
	return ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: fmt.Sprintf("obs-e15t1r-%d", n), SchemaVersion: "agent-dispatch.source-observation/v1",
			SourceType: "watchman", SourceID: "watchman-main", TriggerName: "trig",
			ResourceID: "vault-main", ObservedAt: now, ReceivedAt: now,
			RawPayloadDigest: fmt.Sprintf("sha256:%064d", n), IngestStatus: "accepted",
			Changes: []ports.ObservationChange{{
				Ordinal: 0, Path: fmt.Sprintf("Inbox/n%d.md", n), Operation: "modify", ExistsAfter: true, FileType: "regular",
				AfterDigest: fmt.Sprintf("sha256:%064d", n+1), DigestStatus: "known",
			}},
		},
		Batch: ports.BatchInput{
			BatchID: fmt.Sprintf("batch-e15t1r-%d", n), RouteID: routeID, RouteRevision: "route-rev-" + routeID,
			ResourceID: "vault-main", CreatedAt: now,
			ContentFingerprint: fmt.Sprintf("sha256:%064d", n+2), ObservationIDs: []string{fmt.Sprintf("obs-e15t1r-%d", n)},
		},
		Decision: ports.DecisionInput{
			DecisionID: fmt.Sprintf("decision-e15t1r-%d", n), BatchID: fmt.Sprintf("batch-e15t1r-%d", n), RouteID: routeID,
			RouteRevision: "route-rev-" + routeID, PolicyRevision: "policy-rev", Disposition: "dispatch",
			Classification: "normal", ReasonCodesJSON: `["normal_batch"]`, CreatedAt: now, Actor: "seed",
		},
		Intent: ports.IntentInput{
			DispatchID: fmt.Sprintf("dispatch-e15t1r-%d", n), DecisionID: fmt.Sprintf("decision-e15t1r-%d", n), RouteID: routeID,
			RouteRevision: "route-rev-" + routeID, TargetID: "hermes-main", TargetType: "hermes_kanban",
			ResourceID: "vault-main", Generation: 1, IdempotencyKey: fmt.Sprintf("agent-dispatch:v2:sha256:%064d", n),
			ContentFingerprint: fmt.Sprintf("sha256:%064d", n+2), ManifestDigest: fmt.Sprintf("sha256:%064d", n+3),
			RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: `{"contract_version":"agent-dispatch.hermes-task/v1"}`,
			CreatedAt: now,
			Fanout: &ports.FanoutInput{
				AggregateID: fmt.Sprintf("agg-e15t1r-%d", n), Origin: "arrival",
				DestinationID: "main", DestinationRevision: revision, Workstream: "main",
				Selections: []records.DestinationSelection{{
					DestinationID: "main", DestinationRevision: revision, Workstream: "main", Reason: "fanout_mode:all",
				}},
				Revisions: []ports.DestinationRevisionInput{{
					DestinationID: "main", Revision: revision, ProjectionJSON: projection,
				}},
			},
		},
	}
}

// E15-T1 CLI coverage (CON-013): the same-resource cross-group topology
// gate of `route preflight` — unacknowledged different groups fail
// before any probe, every involved route acknowledging permits the
// topology, and one effective group is the normal serialized posture.

// e15t1TwoRouteConfig writes a two-route configuration over one shared
// resource whose destinations take configurable serialization groups
// and acknowledgements.
func e15t1TwoRouteConfig(t *testing.T, bin string, groupA, groupB string, ackA, ackB bool) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AGENT_DISPATCH_CONFIG", "")
	t.Setenv("AGENT_DISPATCH_STATE_DIR", filepath.Join(home, "state"))
	dir := t.TempDir()
	route := func(id, trigger, group string, ack bool) string {
		text := `  ` + id + `:
    enabled: false
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: ` + trigger + `
      include: ["**/*.md"]
      exclude: [".git/**"]
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
        profile: wiki-maintainer
        skills: [llm-wiki]
        workstream: main
        execution_hints:
          max_runtime: 30m
          max_attempts: 2
`
		if group != "" {
			text += `        serialization_group: ` + group + "\n"
		}
		if ack {
			text += `    allow_cross_group_concurrency: true` + "\n"
		}
		text += `    submission_retry:
      max_attempts: 3
      initial_backoff: 2s
      max_backoff: 2m
      multiplier: 2.0
      jitter_fraction: 0.2
    latest_state: true
    failure_budget: 2
    active_stale_after: 2h
    reconciliation:
      initial: true
      daily_expected: true
`
		return text
	}
	cfg := `version: 1
instance:
  id: e15t1-cli
resources:
  vault-main:
    type: directory
    root: ` + filepath.Join(dir, "vault") + `
    file_scope: markdown
hermes_targets:
  hermes-main:
    board: agent-dispatch-test
    minimum_version: 0.20.5
    compatibility: capability_probe
    executable: ` + bin + `
    submit_timeout: 30s
    lookup_timeout: 30s
    environment_allowlist: [PATH, HOME]
routes:
` + route("wiki", "agent-dispatch.wiki.e15t1", groupA, ackA) + route("audit", "agent-dispatch.audit.e15t1", groupB, ackB)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestE15T1PreflightBlocksUnacknowledgedCrossGroupTopology(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e15t1TwoRouteConfig(t, bin, "group-a", "group-b", false, false)
	var out, errb bytes.Buffer
	code := Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code == 0 {
		t.Fatal("an unacknowledged cross-group topology must fail preflight")
	}
	stderr := errb.String()
	if !strings.Contains(stderr, "allow_cross_group_concurrency") {
		t.Fatalf("the failure must name the acknowledgement remediation: %s", stderr)
	}
	if !strings.Contains(stderr, "group-a") || !strings.Contains(stderr, "group-b") {
		t.Fatalf("the failure must name both groups: %s", stderr)
	}
}

func TestE15T1PreflightPartialAcknowledgementStillBlocks(t *testing.T) {
	// CON-013: EVERY involved route must acknowledge; one is not enough.
	bin := stubhermes.Write(t)
	configPath := e15t1TwoRouteConfig(t, bin, "group-a", "group-b", true, false)
	var out, errb bytes.Buffer
	code := Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code == 0 {
		t.Fatal("one unacknowledged involved route keeps the block")
	}
	if !strings.Contains(errb.String(), "unacknowledged: audit") {
		t.Fatalf("the failure must name the unacknowledged route: %s", errb.String())
	}
}

func TestE15T1PreflightAcknowledgedCrossGroupPasses(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e15t1TwoRouteConfig(t, bin, "group-a", "group-b", true, true)
	var out, errb bytes.Buffer
	code := Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code != 0 {
		t.Fatalf("a fully acknowledged cross-group topology passes preflight: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	checks, _ := res["checks"].([]any)
	var serializationDetail string
	for _, entry := range checks {
		check, _ := entry.(map[string]any)
		if check["check"] == "serialization" && check["state"] == "pass" {
			detail, _ := check["detail"].(string)
			if strings.Contains(detail, "group-a") {
				serializationDetail = detail
			}
		}
	}
	if serializationDetail == "" {
		t.Fatalf("the passing serialization check must name the route's effective groups: %v", checks)
	}
}

func TestE15T1PreflightSharedGroupPasses(t *testing.T) {
	// One effective group over the resource — the normal serialized
	// posture — needs no acknowledgement.
	bin := stubhermes.Write(t)
	configPath := e15t1TwoRouteConfig(t, bin, "shared", "shared", false, false)
	var out, errb bytes.Buffer
	code := Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code != 0 {
		t.Fatalf("one effective group over the resource passes preflight: %s", errb.String())
	}
	// The resource default is one group as well: both routes omitted the
	// explicit field, so both join resource:vault-main.
	configPath = e15t1TwoRouteConfig(t, bin, "", "", false, false)
	out.Reset()
	errb.Reset()
	code = Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code != 0 {
		t.Fatalf("the shared resource default is one group and passes preflight: %s", errb.String())
	}
	if !strings.Contains(out.String(), "resource:vault-main") {
		t.Fatalf("the passing detail names the resource default group: %s", out.String())
	}
}

func TestE15T1PreflightReportsLocalEnforcementForExplicitGroup(t *testing.T) {
	// An explicitly declared group on a target without --mutex-key is
	// the AC-1101 posture: a warn that the group is enforced locally and
	// the flag is suppressed at submit — never a compatibility failure.
	// The stub's create surface is rewritten to drop the flag the way a
	// Hermes 0.20.5 target presents itself.
	bin := stubhermes.Write(t)
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.Replace(string(raw), " [--mutex-key KEY]", "", 1)
	if rewritten == string(raw) {
		t.Fatal("the stub does not carry the --mutex-key help line")
	}
	if err := os.WriteFile(bin, []byte(rewritten), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := e15t1TwoRouteConfig(t, bin, "group-a", "group-a", false, false)
	var out, errb bytes.Buffer
	code := Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code != 0 {
		t.Fatalf("a shared explicit group passes preflight: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	checks, _ := res["checks"].([]any)
	foundWarn := false
	for _, entry := range checks {
		check, _ := entry.(map[string]any)
		if check["check"] == "serialization" && check["state"] == "warn" && strings.Contains(check["detail"].(string), "enforced locally") {
			foundWarn = true
		}
	}
	if !foundWarn {
		t.Fatalf("the explicit group on a target without --mutex-key warns about local enforcement: %v", checks)
	}
}

func TestE15T1PreflightBlocksOnPersistedConflict(t *testing.T) {
	// The persisted-conflict gate (round-1 F002 coverage): a group state
	// row reporting serialization_conflict on this route's effective
	// group blocks preflight before any probe, naming both preserved
	// children and the allowed exits.
	bin := stubhermes.Write(t)
	configPath := e15t1TwoRouteConfig(t, bin, "group-a", "group-a", false, false)
	store, err := sqlite.Open(filepath.Join(os.Getenv("AGENT_DISPATCH_STATE_DIR"), StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`INSERT INTO serialization_groups (group_id, state, holder_dispatch_id, conflict_json, materialized_at, updated_at) VALUES (?, 'CONFLICT', NULL, ?, '2026-08-31T00:00:00Z', '2026-08-31T00:00:00Z')`,
		"group-a", `[{"route_id":"wiki","destination_id":"main","dispatch_id":"d-e15-1"},{"route_id":"audit","destination_id":"main","dispatch_id":"d-e15-2"}]`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`INSERT INTO serialization_group_members (route_id, destination_id, group_id) VALUES ('wiki', 'main', 'group-a')`); err != nil {
		t.Fatal(err)
	}
	store.Close()
	var out, errb bytes.Buffer
	code := Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code == 0 {
		t.Fatal("a persisted serialization conflict must block preflight")
	}
	stderr := errb.String()
	if !strings.Contains(stderr, "preserves an active collision") || !strings.Contains(stderr, "d-e15-1") || !strings.Contains(stderr, "d-e15-2") {
		t.Fatalf("the block must name the conflict and both preserved children: %s", stderr)
	}
	if !strings.Contains(stderr, "work complete") {
		t.Fatalf("the block must name the allowed existing-work exits: %s", stderr)
	}
}

func TestE15T1ReconcileMaterializesGroupMembership(t *testing.T) {
	// The reconcile wiring (round-1 F002 coverage): even the disabled
	// baseline-only reconciliation materializes the group membership
	// from current configuration before its snapshot work.
	bin := stubhermes.Write(t)
	configPath := e15t1TwoRouteConfig(t, bin, "group-a", "group-a", false, false)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Resources["vault-main"].Root, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"reconcile", "--config", configPath, "--route", "wiki", "--reason", "initial", "--baseline-only"}, &out, &errb)
	if code != 0 {
		t.Fatalf("baseline-only reconcile: %s", errb.String())
	}
	store, err := sqlite.Open(filepath.Join(os.Getenv("AGENT_DISPATCH_STATE_DIR"), StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows, err := store.LoadSerializationGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].GroupID != "group-a" || rows[0].State != sqlite.GroupStateOpen {
		t.Fatalf("the reconcile must materialize the configured group open: %+v", rows)
	}
	group, ok, err := store.SerializationGroupOf(context.Background(), "wiki", "main")
	if err != nil || !ok || group != "group-a" {
		t.Fatalf("the route's lane must hold its membership: %q ok=%v err=%v", group, ok, err)
	}
}

func TestE15T1AssignmentCarriesEffectiveGroup(t *testing.T) {
	// The wire change (round-1 F003 coverage): the request assignment's
	// mutex member carries the destination's EFFECTIVE serialization
	// group, so a target that supports --mutex-key receives the
	// complementary group and an omitted field still names the resource
	// default.
	if a := assignmentOf("vault-main", config.Destination{Profile: "p", Skills: []string{"s"}}); a.MutexKey != "resource:vault-main" {
		t.Fatalf("an omitted group must ride the resource default: %+v", a)
	}
	if a := assignmentOf("vault-main", config.Destination{Profile: "p", Skills: []string{"s"}, SerializationGroup: "wiki-publish"}); a.MutexKey != "wiki-publish" {
		t.Fatalf("an explicit group must ride verbatim: %+v", a)
	}
	if a := assignmentOf("vault-main", config.Destination{Profile: "p", Skills: []string{"s"}, MutexKey: "legacy-key"}); a.MutexKey != "legacy-key" {
		t.Fatalf("the deprecated alias must ride verbatim: %+v", a)
	}
}

func TestE15T1DispatchSendsEffectiveGroupAsTargetMutex(t *testing.T) {
	// End to end over the stub (whose create surface carries
	// --mutex-key): a destination with no explicit group submits the
	// resource-derived effective group as the complementary target mutex
	// (CON-011 wire posture of round-1 F003).
	bin := stubhermes.Write(t)
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Replace(string(raw), "  create)\n", "  create)\n    printf '%s\\n' \"$*\" >> \"$DIR/argv-create\"\n", 1)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := e15t1TwoRouteConfig(t, bin, "", "", false, false)
	var out, errb bytes.Buffer
	if code := Run([]string{"hermes", "probe", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("probe: %s", errb.String())
	}
	cfgRaw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, bytes.Replace(cfgRaw, []byte("enabled: false"), []byte("enabled: true"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, cfg.Resources["vault-main"].Root, false)
	t.Setenv("WATCHMAN_TRIGGER", "agent-dispatch.wiki.e15t1")
	e4t3RegisterRoute(t, configPath)
	revision, _ := config.RouteRevision(cfg, "wiki")
	out.Reset()
	errb.Reset()
	if code := Run([]string{"route", "enable", "--config", configPath, "--route", "wiki", "--acknowledge-production-gate", revision, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("route enable: %s", errb.String())
	}
	if err := os.MkdirAll(filepath.Join(cfg.Resources["vault-main"].Root, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Resources["vault-main"].Root, "Inbox", "new.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	var code int
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		code = Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("dispatch exit %d: %s", code, errb.String())
	}
	stateDir := filepath.Join(filepath.Dir(bin), "state")
	argvLog, err := os.ReadFile(filepath.Join(stateDir, "argv-create"))
	if err != nil {
		t.Fatalf("the task must reach the create surface: %v", err)
	}
	if !strings.Contains(string(argvLog), "--mutex-key") || !strings.Contains(string(argvLog), "resource:vault-main") {
		t.Fatalf("a mutex-capable target must receive the effective group as the complementary mutex, got: %s", argvLog)
	}
}

func TestE15T1PreflightWarnsOnUnreadableGroupState(t *testing.T) {
	// E15-T1 hardening revalidation F002: the persisted-conflict gate
	// fails CLOSED on an unreadable group state — a warn naming the
	// degraded read, never a silent no-conflict pass.
	bin := stubhermes.Write(t)
	configPath := e15t1TwoRouteConfig(t, bin, "group-a", "group-a", false, false)
	store, err := sqlite.Open(filepath.Join(os.Getenv("AGENT_DISPATCH_STATE_DIR"), StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// Degrade exactly the group-state read: the ledger still records v18
	// but the tables are gone (the corruption class the warn names).
	if _, err := store.Exec(`DROP TABLE serialization_group_members`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`DROP TABLE serialization_groups`); err != nil {
		t.Fatal(err)
	}
	store.Close()
	var out, errb bytes.Buffer
	code := Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code != 0 {
		t.Fatalf("a degraded read must not fail the preflight: %s", errb.String())
	}
	if !strings.Contains(out.String(), "could not be read") {
		t.Fatalf("the degraded serialization-state read must warn on the envelope: %s", out.String())
	}
}

func TestE15T1ReconcileWarnsOnConflictAndTopologyFailure(t *testing.T) {
	// E15-T1 hardening revalidation F003/F006: the reconcile surfaces a
	// persisted serialization conflict on stderr and a topology
	// materialization failure is reported, never silent.
	bin := stubhermes.Write(t)
	configPath := e15t1TwoRouteConfig(t, bin, "group-a", "group-a", false, false)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Resources["vault-main"].Root, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two preserved actives in one group: the topology materialization
	// recomputes the blocking conflict from live lane state (a seeded
	// CONFLICT row alone would be dissolved by the same reconciliation).
	store, err := sqlite.Open(filepath.Join(os.Getenv("AGENT_DISPATCH_STATE_DIR"), StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	for _, routeID := range []string{"wiki", "audit"} {
		if err := store.RegisterRoute(nil, routeID, "route-rev-"+routeID, "policy-rev", "vault-main", "hermes-main", "{}", "2026-08-31T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if err := store.InitializeRouteState(nil, routeID); err != nil {
			t.Fatal(err)
		}
		if err := store.SetRouteActivation(context.Background(), routeID, "enabled", "route-rev-"+routeID, "", "2026-08-31T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	// Both lanes activate BEFORE the group topology exists (the
	// pre-upgrade shape): the reconcile's own materialization then
	// resolves the two live actives into one group and reports the
	// preserved conflict.
	c := &dispatch.Coordinator{Store: store, Now: func() string { return "2026-08-31T00:00:00Z" }, Actor: "seed"}
	wikiLin := e15t2ArrivalLineage(t, "wiki", 601)
	if _, err := c.Arrival(context.Background(), wikiLin); err != nil {
		t.Fatalf("seed wiki child: %v", err)
	}
	auditLin := e15t2ArrivalLineage(t, "audit", 602)
	if _, err := c.Arrival(context.Background(), auditLin); err != nil {
		t.Fatalf("seed audit child: %v", err)
	}
	// The configuration's effective groups are what the reconcile
	// materializes; seed them durable so the assertion can re-read them.
	if _, err := store.MaterializeSerializationGroups(context.Background(), []sqlite.SerializationGroupMemberInput{
		{RouteID: "wiki", DestinationID: "main", GroupID: "group-a"},
		{RouteID: "audit", DestinationID: "main", GroupID: "group-a"},
	}, "seed", "2026-08-31T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	store.Close()
	var out, errb bytes.Buffer
	code := Run([]string{"reconcile", "--config", configPath, "--route", "wiki", "--reason", "manual"}, &out, &errb)
	if code != 0 {
		t.Fatalf("reconcile: %s", errb.String())
	}
	if !strings.Contains(errb.String(), "reports a preserved conflict") {
		t.Fatalf("the reconcile must warn about the persisted conflict: stderr=%q stdout=%q", errb.String(), out.String())
	}
	verify, verr := sqlite.Open(filepath.Join(os.Getenv("AGENT_DISPATCH_STATE_DIR"), StateDBName))
	if verr != nil {
		t.Fatal(verr)
	}
	defer verify.Close()
	groups, gerr := verify.LoadSerializationGroups(context.Background())
	if gerr != nil {
		t.Fatal(gerr)
	}
	for _, g := range groups {
		if g.GroupID == "group-a" && g.State != sqlite.GroupStateConflict {
			t.Fatalf("the shared group must report the preserved conflict: %+v", g)
		}
	}
}

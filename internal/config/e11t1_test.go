package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyDispatchYAML is the v0.1.4 shape: one route with a `dispatch`
// block and a hermes-kanban target under `targets`.
func legacyDispatchYAML(t *testing.T) []byte {
	t.Helper()
	return []byte(`version: 1
instance:
  id: legacy-test
resources:
  vault-main:
    type: directory
    root: /srv/vault
    file_scope: markdown
targets:
  hermes-kanban-main:
    type: hermes-kanban
    board: agent-dispatch
    executable: hermes
    capability_report: /etc/agent-dispatch/caps.json
    required_capabilities: [durable_acceptance]
routes:
  wiki:
    enabled: false
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: agent-dispatch.wiki.abc123
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
    dispatch:
      target: hermes-kanban-main
      profile: wiki-maintainer
      skills: [llm-wiki]
      mutex_key: wiki-publish
      latest_state: true
      submission_retry:
        max_attempts: 3
        initial_backoff: 2s
        max_backoff: 2m
        multiplier: 2.0
        jitter_fraction: 0.2
      execution_hints:
        max_runtime: 30m
        max_attempts: 2
      failure_budget: 2
      active_stale_after: 2h
    reconciliation:
      initial: true
      daily_expected: true
`)
}

// TestE11T1LegacyDispatchRefusedWithRegenerationPath proves OPS-014: a
// legacy `dispatch` block refuses loading with the exact regeneration
// path, never a conversion or preview.
func TestE11T1LegacyDispatchRefusedWithRegenerationPath(t *testing.T) {
	_, err := Parse(legacyDispatchYAML(t))
	if err == nil {
		t.Fatal("the legacy dispatch shape must be refused")
	}
	var legacy *LegacyShapeError
	if !errors.As(err, &legacy) {
		t.Fatalf("the refusal must be the typed legacy error, got %v", err)
	}
	if len(legacy.Routes) != 1 || legacy.Routes[0] != "wiki" {
		t.Fatalf("the refusal must name the offending route: %v", legacy.Routes)
	}
	msg := err.Error()
	for _, want := range []string{
		"regenerate the configuration",
		"`agent-dispatch setup wiki`",
		"`agent-dispatch init`",
		"destinations[]",
		"hermes_targets",
		"docs/contracts/configuration-spec.md",
		"docs/examples/config.yaml",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("regeneration path must mention %q: %s", want, msg)
		}
	}
}

// TestE11T1LegacyKanbanTargetUnderTargetsRefused proves the second
// legacy shape: a hermes-kanban entry under `targets` refuses with the
// move-to-hermes_targets guidance.
func TestE11T1LegacyKanbanTargetUnderTargetsRefused(t *testing.T) {
	raw := string(minimalYAML(t))
	// Replace the webhook target with a legacy kanban entry.
	raw = strings.Replace(raw, `targets:
  hermes-webhook-immediate:
    type: hermes-webhook
    endpoint: https://example.invalid/hook
    auth:
      type: bearer
      secret_ref: env:TEST_TOKEN`, `targets:
  hermes-kanban-main:
    type: hermes-kanban
    board: agent-dispatch
    executable: hermes
    capability_report: /etc/agent-dispatch/caps.json
    required_capabilities: [durable_acceptance]`, 1)
	raw = strings.Replace(raw, "target: hermes-webhook-immediate", "target: hermes-kanban-main", 1)
	_, err := Parse([]byte(raw))
	if err == nil {
		t.Fatal("a hermes-kanban entry under targets must be refused")
	}
	var legacy *LegacyShapeError
	if !errors.As(err, &legacy) || len(legacy.KanbanTargets) != 1 {
		t.Fatalf("the refusal must name the kanban target, got %v", err)
	}
}

// destinationsYAML builds a two-destination route over the minimal
// fixture with the destinations in the given order (FAN-012).
func destinationsYAML(t *testing.T, swap bool) []byte {
	t.Helper()
	raw := string(minimalYAML(t))
	first := `      - id: alpha
        target: hermes-kanban-main
        profile: wiki-maintainer
        skills: [llm-wiki]
        workstream: indexing
        mutex_key: wiki-publish
        execution_hints:
          max_runtime: 30m
          max_attempts: 2`
	second := `      - id: beta
        target: hermes-kanban-main
        profile: wiki-maintainer
        skills: [llm-wiki]
        workstream: review
        execution_hints:
          max_runtime: 30m
          max_attempts: 2`
	blocks := first + "\n" + second
	if swap {
		blocks = second + "\n" + first
	}
	start := strings.Index(raw, "    fanout_mode: all\n    destinations:\n")
	end := strings.Index(raw, "\n    submission_retry:")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("minimal fixture no longer carries the single-destination block")
	}
	return []byte(raw[:start] + "    fanout_mode: all\n    destinations:\n" + blocks + raw[end:])
}

// TestE11T1FanoutOrderNeverSemantic proves FAN-012: reversing the
// declared destination order changes neither the route revision nor
// the per-destination revisions, and the multi-destination
// configuration parses and validates.
func TestE11T1FanoutOrderNeverSemantic(t *testing.T) {
	a, err := Parse(destinationsYAML(t, false))
	if err != nil {
		t.Fatalf("two-destination route must validate: %v", err)
	}
	b, err := Parse(destinationsYAML(t, true))
	if err != nil {
		t.Fatalf("swapped declaration must validate: %v", err)
	}
	revA, _ := RouteRevision(a, "r1")
	revB, _ := RouteRevision(b, "r1")
	if revA != revB || revA == "" {
		t.Fatalf("declaration order must not change the route revision: %q vs %q", revA, revB)
	}
	route := a.Routes["r1"]
	if len(route.SortedDestinations()) != 2 || route.SortedDestinations()[0].ID != "alpha" {
		t.Fatal("sorted destinations must order by ID")
	}
	alpha, _ := route.DestinationByID("alpha")
	if d := DestinationRevision(a, route, alpha); d == "" {
		t.Fatal("destination revision must compute")
	}
	// The pre-E12 pipeline executes exactly one destination and refuses
	// more (the E12 bound).
	if _, err := route.CertifiedDestination("r1"); err == nil {
		t.Fatal("two destinations must fail the certified-single-destination bound")
	} else {
		var multi *ErrMultiDestination
		if !errors.As(err, &multi) || multi.Count != 2 {
			t.Fatalf("the refusal must be the typed multi-destination error, got %v", err)
		}
	}
}

// TestE11T1DestinationContractRejections proves the semantic
// rejections: duplicate destination IDs, empty workstream, non-all
// fan-out mode, duplicate skills, and an unknown condition value.
func TestE11T1DestinationContractRejections(t *testing.T) {
	base := func() string { return string(destinationsYAML(t, false)) }
	cases := []struct {
		name string
		in   string
	}{
		// The duplicate destination ID is semantic-only: the schema's
		// list shape cannot see it.
		{"duplicate destination id", strings.Replace(base(), "id: beta", "id: alpha", 1)},
		// The remaining shapes are schema-caught (minLength, const,
		// uniqueItems, and the closed condition enums); the semantic
		// layer doubles them for direct SemanticValidate callers.
		{"empty workstream", strings.Replace(base(), "workstream: review", "workstream: \"\"", 1)},
		{"unsupported fanout mode", strings.Replace(base(), "fanout_mode: all", "fanout_mode: prefer-first", 1)},
		{"duplicate skills", strings.Replace(base(), "skills: [llm-wiki]", "skills: [llm-wiki, llm-wiki]", 1)},
		{"unknown condition operation", strings.Replace(base(), "workstream: review", "workstream: review\n        conditions:\n          operations: [explode]", 1)},
		{"unknown classification", strings.Replace(base(), "workstream: review", "workstream: review\n        conditions:\n          classifications: [spicy]", 1)},
		{"unknown policy outcome", strings.Replace(base(), "workstream: review", "workstream: review\n        conditions:\n          policy_outcomes: [explode]", 1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.in))
			if err == nil {
				t.Fatalf("%s must fail validation", c.name)
			}
			if c.name == "duplicate destination id" && !strings.Contains(err.Error(), "declared more than once") {
				t.Fatalf("the duplicate id must be the semantic rejection, got %v", err)
			}
		})
	}
}

// TestE11T1HermesDestinationRequiresProfile proves the destination
// envelope rejections (HER-015): a hermes destination requires a
// non-empty profile, and a webhook destination carries none of the
// hermes-only execution fields.
func TestE11T1HermesDestinationRequiresProfile(t *testing.T) {
	base := string(minimalYAML(t))
	// A hermes destination without a profile is refused by the semantic
	// layer (the schema leaves the key optional; HER-015 owns the rule).
	noProfile := strings.Replace(base, "        profile: wiki-maintainer\n", "", 1)
	_, err := Parse([]byte(noProfile))
	if err == nil || !strings.Contains(err.Error(), "profile must be non-empty for a hermes destination (HER-015)") {
		t.Fatalf("a hermes destination without a profile must be refused with the HER-015 rejection, got %v", err)
	}
	// A webhook destination carrying hermes-only fields is refused.
	webhookWithProfile := strings.Replace(base, "        target: hermes-kanban-main\n", "        target: hermes-webhook-immediate\n", 1)
	_, err = Parse([]byte(webhookWithProfile))
	if err == nil || !strings.Contains(err.Error(), "apply only to a hermes destination") {
		t.Fatalf("a webhook destination carrying profile/skills/mutex_key must be refused, got %v", err)
	}
}

// TestE11T1WebhookEndpointQueryIsRevisionSensitive proves the endpoint
// commitment in the revision digest (configuration-spec §13): the digest
// commits to the FULL endpoint through a one-way commitment, so a
// query-string change — where webhook tokens ride — pauses the
// acknowledged route while the redacted display form stays cosmetic.
func TestE11T1WebhookEndpointQueryIsRevisionSensitive(t *testing.T) {
	mutate := func(endpoint string) string {
		raw := string(minimalYAML(t))
		cfg, err := Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		// Deliver r1 through the webhook target so its transport
		// projection joins the route revision, and strip the
		// hermes-only fields a webhook destination must not carry.
		dest := &cfg.Routes["r1"].Destinations[0]
		dest.Target = "hermes-webhook-immediate"
		dest.Profile = ""
		dest.Skills = nil
		dest.MutexKey = ""
		hook := cfg.Targets["hermes-webhook-immediate"]
		hook.Endpoint = endpoint
		cfg.Targets["hermes-webhook-immediate"] = hook
		out, merr := MarshalYAML(cfg)
		if merr != nil {
			t.Fatal(merr)
		}
		return string(out)
	}
	a, err := Parse([]byte(mutate("https://example.invalid/hook?token=alpha")))
	if err != nil {
		t.Fatalf("webhook delivery must validate: %v", err)
	}
	b, err := Parse([]byte(mutate("https://example.invalid/hook?token=beta")))
	if err != nil {
		t.Fatalf("rotated query must validate: %v", err)
	}
	revA, okA := RouteRevision(a, "r1")
	revB, okB := RouteRevision(b, "r1")
	if !okA || !okB || revA == "" || revB == "" {
		t.Fatalf("both revisions must compute: %q %q %v %v", revA, revB, okA, okB)
	}
	if revA == revB {
		t.Fatal("a webhook endpoint query-string change must change the route revision (the digest commits to the full endpoint)")
	}
}

// TestE11T1HermesTargetFloorValidation proves the hermes_targets
// contract: a missing board, a floor below 0.20.5, a non-capability_probe
// mode, and an unparseable floor all fail validation; a higher declared
// floor passes.
func TestE11T1HermesTargetFloorValidation(t *testing.T) {
	mutate := func(f func(*HermesTarget)) string {
		raw := string(minimalYAML(t))
		cfg, err := Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		tg := cfg.HermesTargets["hermes-kanban-main"]
		f(&tg)
		cfg.HermesTargets["hermes-kanban-main"] = tg
		out, merr := MarshalYAML(cfg)
		if merr != nil {
			t.Fatal(merr)
		}
		return string(out)
	}
	if _, err := Parse([]byte(mutate(func(tg *HermesTarget) { tg.Board = "" }))); err == nil {
		t.Fatal("a missing board must fail validation")
	}
	if _, err := Parse([]byte(mutate(func(tg *HermesTarget) { tg.MinimumVersion = "0.20.4" }))); err == nil || !strings.Contains(err.Error(), "support floor") {
		t.Fatalf("a floor below 0.20.5 must fail (HER-011): %v", err)
	}
	if _, err := Parse([]byte(mutate(func(tg *HermesTarget) { tg.Compatibility = "vendor-pinned" }))); err == nil || !strings.Contains(err.Error(), "capability_probe") {
		t.Fatalf("a non-probe compatibility mode must fail: %v", err)
	}
	if _, err := Parse([]byte(mutate(func(tg *HermesTarget) { tg.MinimumVersion = "1.2.3" }))); err != nil {
		t.Fatalf("a higher declared floor must pass: %v", err)
	}
	if _, err := Parse([]byte(mutate(func(tg *HermesTarget) { tg.MinimumVersion = "0.20" }))); err == nil || !strings.Contains(err.Error(), "minimum_version") {
		t.Fatalf("an unparseable floor must fail naming the field: %v", err)
	}
	// E15-T2 (AC-1107): an omitted floor fails closed — the schema (and,
	// for direct SemanticValidate callers, the semantic layer) never
	// silently defaults or rewrites it.
	if _, err := Parse([]byte(mutate(func(tg *HermesTarget) { tg.MinimumVersion = "" }))); err == nil || !strings.Contains(err.Error(), "minimum_version") {
		t.Fatalf("an omitted floor must fail closed without rewriting (HER-011): %v", err)
	}
	// A hermes_targets key outside the destination-ID pattern is refused:
	// the key names the capability-cache file, so a traversal segment
	// must never reach a file-path join. The schema rejects the key
	// shape and the semantic layer doubles the rule for direct
	// SemanticValidate callers.
	traversal := strings.Replace(string(minimalYAML(t)), "  hermes-kanban-main:", "  ../../escape:", 1)
	if _, terr := Parse([]byte(traversal)); terr == nil || !strings.Contains(terr.Error(), "../../escape") {
		t.Fatalf("a hermes_targets key with path separators must be refused naming the key, got %v", terr)
	}
}

// TestE11T1TargetMapClashRejected proves the ambiguity guard: one ID
// declared under both targets and hermes_targets fails validation, so
// destination bindings can never be ambiguous.
func TestE11T1TargetMapClashRejected(t *testing.T) {
	raw := string(minimalYAML(t))
	clash := "targets:\n" + "  hermes-kanban-main:\n" + "    type: hermes-webhook\n" + "    endpoint: https://example.invalid/hook\n" + "    auth:\n" + "      type: bearer\n" + "      secret_ref: env:TEST_TOKEN\n" + "  hermes-webhook-immediate:"
	if !strings.Contains(raw, "targets:\n  hermes-webhook-immediate:") {
		t.Fatal("minimal fixture no longer carries the webhook target first")
	}
	raw = strings.Replace(raw, "targets:\n  hermes-webhook-immediate:", clash, 1)
	_, err := Parse([]byte(raw))
	if err == nil || !strings.Contains(err.Error(), "both targets and hermes_targets") {
		t.Fatalf("a shared target ID must be rejected as ambiguous, got %v", err)
	}
}

// TestE11T1MutateDestinationAtomic proves CLI-015: a destination-qualified
// mutation validates the candidate before writing and replaces the file
// atomically; a rejected candidate leaves the original untouched and
// unrelated routes byte-identical.
func TestE11T1MutateDestinationAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := minimalYAML(t)
	if werr := os.WriteFile(path, original, 0o600); werr != nil {
		t.Fatal(werr)
	}
	if merr := MutateDestination(path, "r1", "main", func(d *Destination) error {
		d.Profile = "reviewer"
		return nil
	}); merr != nil {
		t.Fatalf("valid mutation must apply: %v", merr)
	}
	mutated, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	cfg, perr := Parse(mutated)
	if perr != nil {
		t.Fatalf("mutated file must still load: %v", perr)
	}
	if got := cfg.Routes["r1"].Destinations[0].Profile; got != "reviewer" {
		t.Fatalf("mutation must apply to the destination, got %q", got)
	}
	if info, serr := os.Stat(path); serr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the replacement must preserve the file mode: %v %v", info.Mode(), serr)
	}
	// A rejected candidate writes nothing.
	before, _ := os.ReadFile(path)
	if merr := MutateDestination(path, "r1", "main", func(d *Destination) error {
		d.Workstream = ""
		return nil
	}); merr == nil || !strings.Contains(merr.Error(), "nothing was written") {
		t.Fatalf("an invalid candidate must refuse before writing: %v", merr)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("a rejected candidate must leave the file byte-identical")
	}
	// An unknown route or destination is a usage-style error, not a write.
	if merr := MutateDestination(path, "nope", "main", func(d *Destination) error { return nil }); merr == nil {
		t.Fatal("an unknown route must fail")
	}
}

// TestE11T1NotificationsContract proves the declared notification
// policy: the closed event vocabulary, unique sink IDs, the webhook
// sink's https endpoint and auth requirement, and the log sink's bare
// shape; a no-sink policy stays valid and disabled.
func TestE11T1NotificationsContract(t *testing.T) {
	raw := strings.Replace(string(destinationsYAML(t, false)), `    submission_retry:`, `    notifications:
      events: [work_completed, delivery_unknown]
      sinks:
        - id: operations-webhook
          type: webhook
          endpoint: https://notify.example.invalid/agent-dispatch
          auth:
            type: bearer
            secret_ref: env:AGENT_DISPATCH_NOTIFICATION_TOKEN
    submission_retry:`, 1)
	cfg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("a declared notification policy must validate: %v", err)
	}
	if n := cfg.Routes["r1"].Notifications; n == nil || len(n.Sinks) != 1 || n.Sinks[0].Type != "webhook" {
		t.Fatalf("notifications must parse: %+v", n)
	}
	bad := []struct {
		name, old, new string
	}{
		{"unknown event", "work_completed", "work_celebrated"},
		{"duplicate sink id", "operations-webhook", "operations-webhook2"},
		{"http endpoint", "https://notify.example.invalid/agent-dispatch", "http://notify.example.invalid/agent-dispatch"},
		{"log sink with endpoint", "type: webhook", "type: log"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			mutated := strings.Replace(raw, c.old, c.new, 1)
			if c.name == "duplicate sink id" {
				// Duplicate requires two sinks with the same id.
				mutated = strings.Replace(raw, "        - id: operations-webhook", "        - id: ops\n          type: log\n        - id: ops", 1)
			}
			if _, err := Parse([]byte(mutated)); err == nil {
				t.Fatalf("%s must fail validation", c.name)
			}
		})
	}
}

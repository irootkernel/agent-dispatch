package config

import (
	"strings"
	"testing"
)

// E15-T1 (ADR-0021, CON-011 through CON-014): serialization-group
// configuration, resolution, topology acknowledgement, and revision
// participation.

// e15Config renders a two-route configuration over one shared resource
// with configurable serialization fields and acknowledgements.
func e15Config(t *testing.T, mutate func(cfg *Config)) *Config {
	t.Helper()
	text := `
version: 1
instance:
  id: e15
  state_dir: /var/lib/agent-dispatch
resources:
  vault-main:
    type: directory
    root: /srv/vault
    file_scope: markdown
hermes_targets:
  hermes-main:
    board: agent-dispatch
    executable: hermes
    minimum_version: 0.20.5
    compatibility: capability_probe
routes:
  wiki-maintenance:
    enabled: false
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: t1
      include: ["**/*.md"]
      exclude: [".git/**"]
    batching: {automatic_threshold: 25, hard_limit: 100, max_manifest_bytes: 262144}
    policy:
      protected: ["raw/**"]
      immutable: []
      bulk_action: quarantine
      overflow_action: reconcile
      fresh_instance_action: reconcile
      unsafe_path_action: quarantine
    fanout_mode: all
    destinations:
      - id: indexing
        target: hermes-main
        profile: wiki-maintainer
        skills: [llm-wiki]
        workstream: indexing
        execution_hints: {max_runtime: 30m, max_attempts: 2}
    notifications:
      events: [work_completed]
      sinks: []
    submission_retry: {max_attempts: 3, initial_backoff: 2s, max_backoff: 2m, multiplier: 2.0, jitter_fraction: 0.2}
    latest_state: true
    failure_budget: 2
    active_stale_after: 2h
    reconciliation: {initial: true, daily_expected: true}
  nightly-audit:
    enabled: false
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: t2
      include: ["**/*.md"]
      exclude: [".git/**"]
    batching: {automatic_threshold: 25, hard_limit: 100, max_manifest_bytes: 262144}
    policy:
      protected: ["raw/**"]
      immutable: []
      bulk_action: quarantine
      overflow_action: reconcile
      fresh_instance_action: reconcile
      unsafe_path_action: quarantine
    fanout_mode: all
    destinations:
      - id: audit
        target: hermes-main
        profile: wiki-maintainer
        skills: [llm-wiki]
        workstream: audit
        execution_hints: {max_runtime: 30m, max_attempts: 2}
    notifications:
      events: [work_completed]
      sinks: []
    submission_retry: {max_attempts: 3, initial_backoff: 2s, max_backoff: 2m, multiplier: 2.0, jitter_fraction: 0.2}
    latest_state: true
    failure_budget: 2
    active_stale_after: 2h
    reconciliation: {initial: true, daily_expected: true}
`
	cfg, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("parse e15 fixture: %v", err)
	}
	if mutate != nil {
		mutate(cfg)
	}
	return cfg
}

func e15Destination(t *testing.T, cfg *Config, routeID, destID string) *Destination {
	t.Helper()
	route := cfg.Routes[routeID]
	for i, dest := range route.Destinations {
		if dest.ID == destID {
			return &route.Destinations[i]
		}
	}
	t.Fatalf("destination %s not found in route %s", destID, routeID)
	return nil
}

func TestE15T1EffectiveGroupResolutionOrder(t *testing.T) {
	// CON-011: explicit serialization_group, then the deprecated
	// mutex_key alias, then exactly resource:<resource_id>.
	cfg := e15Config(t, nil)
	if got := EffectiveSerializationGroup("vault-main", Destination{SerializationGroup: "group-a", MutexKey: "alias-b"}); got != "group-a" {
		t.Fatalf("explicit field must win: %q", got)
	}
	if got := EffectiveSerializationGroup("vault-main", Destination{MutexKey: "alias-b"}); got != "alias-b" {
		t.Fatalf("deprecated alias resolves second: %q", got)
	}
	if got := EffectiveSerializationGroup("vault-main", Destination{}); got != "resource:vault-main" {
		t.Fatalf("omitted fields resolve the resource default: %q", got)
	}
	// An explicit default-form value intentionally joins the
	// resource-derived group (AC-1108).
	if got := EffectiveSerializationGroup("vault-main", Destination{SerializationGroup: "resource:vault-main"}); got != "resource:vault-main" {
		t.Fatalf("explicit default form joins the default group: %q", got)
	}
	// The default's resource segment is the unchanged resources map key.
	if got := EffectiveSerializationGroup("Vault_Main", Destination{}); got != "resource:Vault_Main" {
		t.Fatalf("the resource key is never normalized: %q", got)
	}
	// Resolution provenance drives operator surfaces.
	res := ResolveSerializationGroup("wiki-maintenance", cfg.Routes["wiki-maintenance"], *e15Destination(t, cfg, "wiki-maintenance", "indexing"))
	if res.Group != "resource:vault-main" || res.Source != SerializationGroupResourceDefault {
		t.Fatalf("omitted fields resolve the resource default with provenance: %+v", res)
	}
}

func TestE15T1GroupGrammar(t *testing.T) {
	valid := []string{"a", "wiki-publish", "resource:vault-main", "Team.Tasks/v2-beta:nightly", "A9._:/-"}
	for _, v := range valid {
		if err := ValidateSerializationGroupValue(v); err != nil {
			t.Errorf("%q must satisfy the grammar: %v", v, err)
		}
	}
	invalid := []string{"", "-lead", ".dot", "sp ace", "ünïcode", "tab\tchar", "trailing\n"}
	for _, v := range invalid {
		if err := ValidateSerializationGroupValue(v); err == nil {
			t.Errorf("%q must fail the grammar", v)
		}
	}
	if err := ValidateSerializationGroupValue(strings.Repeat("a", 255)); err != nil {
		t.Errorf("255 ASCII bytes must pass: %v", err)
	}
	if err := ValidateSerializationGroupValue(strings.Repeat("a", 256)); err == nil {
		t.Error("256 ASCII bytes must fail the byte bound")
	}
	// No case folding: two groups differing only in case stay distinct.
	lower := EffectiveSerializationGroup("r", Destination{SerializationGroup: "wiki"})
	upper := EffectiveSerializationGroup("r", Destination{SerializationGroup: "Wiki"})
	if lower == upper {
		t.Fatal("case folding must never merge two groups")
	}
}

func TestE15T1AliasAgreementConflictAndWarning(t *testing.T) {
	// AC-1108: identical dual fields produce one deprecated-alias
	// warning; differing values are a configuration error.
	cfg := e15Config(t, func(cfg *Config) {
		e15Destination(t, cfg, "wiki-maintenance", "indexing").SerializationGroup = "wiki-publish"
		e15Destination(t, cfg, "wiki-maintenance", "indexing").MutexKey = "wiki-publish"
	})
	errs, warnings := SemanticValidate(cfg)
	if len(errs) != 0 {
		t.Fatalf("identical dual fields must pass with a warning: %v", errs)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "deprecated") && strings.Contains(w, "wiki-publish") {
			found = true
		}
	}
	if !found {
		t.Fatalf("identical dual fields must emit exactly one deprecation warning, got %v", warnings)
	}

	cfg = e15Config(t, func(cfg *Config) {
		e15Destination(t, cfg, "wiki-maintenance", "indexing").SerializationGroup = "group-a"
		e15Destination(t, cfg, "wiki-maintenance", "indexing").MutexKey = "group-b"
	})
	errs, _ = SemanticValidate(cfg)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "different values") {
		t.Fatalf("conflicting dual fields must fail validation, got %v", errs)
	}

	// An alias value that violates the grammar is the effective group and
	// must fail the grammar, not pass as a legacy free-form string.
	cfg = e15Config(t, func(cfg *Config) {
		e15Destination(t, cfg, "wiki-maintenance", "indexing").MutexKey = "not valid"
	})
	errs, _ = SemanticValidate(cfg)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "mutex_key") {
		t.Fatalf("an ungrammatical alias value must fail validation, got %v", errs)
	}

	// A webhook destination refuses both fields.
	cfg = e15Config(t, func(cfg *Config) {
		e15Destination(t, cfg, "wiki-maintenance", "indexing").SerializationGroup = "group-a"
		e15Destination(t, cfg, "wiki-maintenance", "indexing").Target = "hook"
		cfg.Targets["hook"] = Target{Type: "hermes-webhook", Endpoint: "https://example.test/hook", Auth: &Auth{Type: "bearer", SecretRef: "test://webhook/token"}}
	})
	errs, _ = SemanticValidate(cfg)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "serialization_group") {
		t.Fatalf("a webhook destination must refuse serialization_group, got %v", errs)
	}
}

func TestE15T1SerializationEditsChangeBothRevisions(t *testing.T) {
	// CON-014 / AC-1105 posture: a serialization edit changes the
	// destination revision and the route revision.
	base := e15Config(t, nil)
	baseRoute := base.Routes["wiki-maintenance"]
	baseDest := *e15Destination(t, base, "wiki-maintenance", "indexing")
	destRev := DestinationRevision(base, baseRoute, baseDest)
	routeRev, ok := RouteRevision(base, "wiki-maintenance")
	if destRev == "" || !ok || routeRev == "" {
		t.Fatal("baseline revisions must compute")
	}

	edited := e15Config(t, func(cfg *Config) {
		e15Destination(t, cfg, "wiki-maintenance", "indexing").SerializationGroup = "wiki-publish"
	})
	editedRoute := edited.Routes["wiki-maintenance"]
	editedDest := *e15Destination(t, edited, "wiki-maintenance", "indexing")
	if newDest := DestinationRevision(edited, editedRoute, editedDest); newDest == destRev {
		t.Fatal("a serialization edit must change the destination revision")
	}
	if newRoute, _ := RouteRevision(edited, "wiki-maintenance"); newRoute == routeRev {
		t.Fatal("a serialization edit must change the route revision (the destination projection joins it)")
	}

	// The cross-group acknowledgement joins the route revision on its
	// own: flipping it moves the route revision while every destination
	// revision stays fixed.
	acked := e15Config(t, func(cfg *Config) {
		route := cfg.Routes["wiki-maintenance"]
		route.AllowCrossGroupConcurrency = true
		cfg.Routes["wiki-maintenance"] = route
	})
	ackedRoute := acked.Routes["wiki-maintenance"]
	ackedDest := *e15Destination(t, acked, "wiki-maintenance", "indexing")
	if newRoute, _ := RouteRevision(acked, "wiki-maintenance"); newRoute == routeRev {
		t.Fatal("flipping allow_cross_group_concurrency must change the route revision")
	}
	if newDest := DestinationRevision(acked, ackedRoute, ackedDest); newDest != destRev {
		t.Fatal("the route-level acknowledgement must not change the destination revision")
	}
}

func TestE15T1IdenticalEffectiveGroupsHashIdentically(t *testing.T) {
	// The projection carries the EFFECTIVE group: an explicit field, the
	// deprecated alias with the same value, and (for the default form)
	// omission produce one identity and therefore one revision.
	explicit := e15Config(t, func(cfg *Config) {
		e15Destination(t, cfg, "wiki-maintenance", "indexing").SerializationGroup = "resource:vault-main"
	})
	alias := e15Config(t, func(cfg *Config) {
		e15Destination(t, cfg, "wiki-maintenance", "indexing").MutexKey = "resource:vault-main"
	})
	omitted := e15Config(t, nil)
	revOf := func(cfg *Config) (string, string) {
		route := cfg.Routes["wiki-maintenance"]
		dest := *e15Destination(t, cfg, "wiki-maintenance", "indexing")
		routeRev, _ := RouteRevision(cfg, "wiki-maintenance")
		return DestinationRevision(cfg, route, dest), routeRev
	}
	eDest, eRoute := revOf(explicit)
	aDest, aRoute := revOf(alias)
	oDest, oRoute := revOf(omitted)
	if eDest != oDest || aDest != oDest || eRoute != oRoute || aRoute != oRoute {
		t.Fatalf("identical effective groups must hash identically: explicit=%v/%v alias=%v/%v omitted=%v/%v", eDest, eRoute, aDest, aRoute, oDest, oRoute)
	}
	// And the projection bytes name the effective group, never the
	// retired mutex_key member.
	projection, err := DestinationProjectionJSON(omitted, omitted.Routes["wiki-maintenance"], *e15Destination(t, omitted, "wiki-maintenance", "indexing"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(projection, `"serialization_group":"resource:vault-main"`) {
		t.Fatalf("the projection must carry the effective group: %s", projection)
	}
	if strings.Contains(projection, "mutex_key") {
		t.Fatalf("the projection must not carry the deprecated alias member: %s", projection)
	}
}

func TestE15T1CrossGroupTopologyAcknowledgement(t *testing.T) {
	// CON-013: destinations governing one resource under different
	// groups conflict unless EVERY involved route acknowledges.
	base := e15Config(t, func(cfg *Config) {
		e15Destination(t, cfg, "wiki-maintenance", "indexing").SerializationGroup = "group-a"
		e15Destination(t, cfg, "nightly-audit", "audit").SerializationGroup = "group-b"
	})
	conflicts := CrossGroupConflicts(base)
	if len(conflicts) != 1 {
		t.Fatalf("two groups over one resource must conflict: %+v", conflicts)
	}
	c := conflicts[0]
	if c.ResourceID != "vault-main" || len(c.Groups) != 2 || len(c.UnacknowledgedRoutes) != 2 || len(c.InvolvedRoutes) != 2 {
		t.Fatalf("conflict detail mismatch: %+v", c)
	}
	if !strings.Contains(c.Describe(), "allow_cross_group_concurrency") {
		t.Fatalf("the conflict must name the remediation: %s", c.Describe())
	}

	oneAck := e15Config(t, func(cfg *Config) {
		e15Destination(t, cfg, "wiki-maintenance", "indexing").SerializationGroup = "group-a"
		e15Destination(t, cfg, "nightly-audit", "audit").SerializationGroup = "group-b"
		maint := cfg.Routes["wiki-maintenance"]
		maint.AllowCrossGroupConcurrency = true
		cfg.Routes["wiki-maintenance"] = maint
	})
	if conflicts := CrossGroupConflicts(oneAck); len(conflicts) != 1 || len(conflicts[0].UnacknowledgedRoutes) != 1 {
		t.Fatalf("one unacknowledged route keeps the conflict: %+v", conflicts)
	}

	allAck := e15Config(t, func(cfg *Config) {
		e15Destination(t, cfg, "wiki-maintenance", "indexing").SerializationGroup = "group-a"
		e15Destination(t, cfg, "nightly-audit", "audit").SerializationGroup = "group-b"
		maint := cfg.Routes["wiki-maintenance"]
		maint.AllowCrossGroupConcurrency = true
		cfg.Routes["wiki-maintenance"] = maint
		audit := cfg.Routes["nightly-audit"]
		audit.AllowCrossGroupConcurrency = true
		cfg.Routes["nightly-audit"] = audit
	})
	if conflicts := CrossGroupConflicts(allAck); len(conflicts) != 0 {
		t.Fatalf("every involved route acknowledging resolves the topology: %+v", conflicts)
	}

	sameGroup := e15Config(t, func(cfg *Config) {
		e15Destination(t, cfg, "wiki-maintenance", "indexing").SerializationGroup = "shared"
		e15Destination(t, cfg, "nightly-audit", "audit").MutexKey = "shared"
	})
	if conflicts := CrossGroupConflicts(sameGroup); len(conflicts) != 0 {
		t.Fatalf("one effective group over one resource is not a conflict: %+v", conflicts)
	}

	// Default groups: both routes omit explicit fields, so both join
	// resource:vault-main — one group, no conflict.
	if conflicts := CrossGroupConflicts(e15Config(t, nil)); len(conflicts) != 0 {
		t.Fatalf("the shared resource default is one group: %+v", conflicts)
	}
}

func TestE15T1MembershipsCoverEveryDestination(t *testing.T) {
	cfg := e15Config(t, func(cfg *Config) {
		e15Destination(t, cfg, "wiki-maintenance", "indexing").SerializationGroup = "group-a"
	})
	members := SerializationGroupMemberships(cfg)
	if len(members) != 2 {
		t.Fatalf("every configured destination resolves a membership: %+v", members)
	}
	byLane := map[string]SerializationGroupResolution{}
	for _, m := range members {
		byLane[m.RouteID+"/"+m.DestinationID] = m
	}
	if m := byLane["wiki-maintenance/indexing"]; m.Group != "group-a" || m.Source != SerializationGroupExplicit || m.ResourceID != "vault-main" {
		t.Fatalf("explicit membership mismatch: %+v", m)
	}
	if m := byLane["nightly-audit/audit"]; m.Group != "resource:vault-main" || m.Source != SerializationGroupResourceDefault {
		t.Fatalf("default membership mismatch: %+v", m)
	}
}

func TestE15T1GeneratedExampleNeverEmitsMutexKey(t *testing.T) {
	// New configuration never emits mutex_key: the generated example
	// declares the explicit field and keeps the same effective group.
	cfg := Example("e15-example", "/srv/vault")
	dest := cfg.Routes["wiki-maintenance"].Destinations[0]
	if dest.MutexKey != "" {
		t.Fatalf("the generated example must not emit mutex_key: %+v", dest)
	}
	if dest.SerializationGroup != "wiki-publish" {
		t.Fatalf("the generated example keeps its named group explicitly: %+v", dest)
	}
	if got := EffectiveSerializationGroup("vault-main", dest); got != "wiki-publish" {
		t.Fatalf("the example's effective group must stay wiki-publish: %q", got)
	}
	if errs, _ := SemanticValidate(cfg); len(errs) != 0 {
		t.Fatalf("the generated example must pass semantic validation: %v", errs)
	}
}

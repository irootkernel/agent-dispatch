package schemavalid

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture creates a minimal valid docs package under root: one
// schema, one matching example, and the required report target.
func writeFixture(t *testing.T, root string) {
	t.Helper()
	for _, dir := range []string{"schemas", "examples", "examples/invalid", "integrations"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	schema := `{"$id":"urn:agent-dispatch:schema:hermes-capabilities:v1","type":"object"}`
	if err := os.WriteFile(filepath.Join(root, "schemas", "caps.schema.json"), []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}
	// A permissive stand-in with the config schema $id so the config.yaml
	// check is exercisable in isolation; the real check runs against the
	// SOT docs package.
	configSchema := `{"$id":"urn:agent-dispatch:schema:config:v1","type":"object"}`
	if err := os.WriteFile(filepath.Join(root, "schemas", "config.schema.json"), []byte(configSchema), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "examples", "report.json"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	negativeSchema := `{"$id":"urn:agent-dispatch:schema:sync-control:v1","type":"object","required":["state"],"properties":{"state":{"const":"active"}}}`
	if err := os.WriteFile(filepath.Join(root, "schemas", "sync-control.schema.json"), []byte(negativeSchema), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "examples", "invalid", "sync-control-invalid-state.json"), []byte(`{"state":"broken"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "integrations", "hermes-capability-report.json"), []byte(`{"b":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A minimal valid YAML configuration for the config.yaml check.
	minimalConfig := `version: 1
instance:
  id: fixture
resources:
  vault:
    type: directory
    root: /srv/vault
    file_scope: markdown
hermes_targets:
  kanban:
    executable: hermes
routes:
  r1:
    enabled: false
    source:
      type: watchman-trigger
      source_id: v
      resource: vault
      trigger_name: agent-dispatch.r1.x
      include: ["**/*.md"]
    batching: {automatic_threshold: 25, hard_limit: 100, max_manifest_bytes: 262144}
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
        target: kanban
        profile: p
        skills: [s]
        mutex_key: m
        workstream: main
        execution_hints: {max_runtime: 30m, max_attempts: 2}
    submission_retry: {max_attempts: 3, initial_backoff: 2s, max_backoff: 2m, multiplier: 2.0, jitter_fraction: 0.2}
    latest_state: true
    failure_budget: 2
    active_stale_after: 2h
    reconciliation: {initial: true, daily_expected: true}
`
	if err := os.WriteFile(filepath.Join(root, "examples", "config.yaml"), []byte(minimalConfig), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateHappyPath(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root)
	lines, failures, err := Validate(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"compiled 3 schema documents", "ok   examples/report.json", "ok   examples/invalid/sync-control-invalid-state.json", "ok   integrations/hermes-capability-report.json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("output missing %q; lines: %v", want, lines)
		}
	}
}

func TestValidateRejectsNegativeFixtureThatPasses(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root)
	if err := os.WriteFile(filepath.Join(root, "examples", "invalid", "sync-control-invalid-state.json"), []byte(`{"state":"active"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, failures, err := Validate(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || (!strings.Contains(failures[0].Detail, "unexpectedly validates") && !strings.Contains(failures[0].Detail, "expected schema rejection")) {
		t.Fatalf("expected one negative-fixture failure, got: %v", failures)
	}
}

func TestSyncSemanticValidationRejectsDuplicatePair(t *testing.T) {
	doc := map[string]any{
		"schema_version": "agent-dispatch.sync-verification/v1",
		"expected_nodes": []any{
			map[string]any{"instance_id": "node-a", "state_incarnation_id": "node-a-0001"},
			map[string]any{"instance_id": "node-b", "state_incarnation_id": "node-b-0001"},
		},
		"nodes": []any{
			map[string]any{"instance_id": "node-a", "state_incarnation_id": "node-a-0001"},
			map[string]any{"instance_id": "node-a", "state_incarnation_id": "node-a-0001"},
		},
	}
	if err := validateSyncSemantics(doc); err == nil || !strings.Contains(err.Error(), "distinct") {
		t.Fatalf("expected duplicate-pair rejection, got %v", err)
	}
}

func TestSyncSemanticValidationRejectsDuplicateVerificationIncarnation(t *testing.T) {
	doc := map[string]any{
		"schema_version": "agent-dispatch.sync-verification/v1",
		"expected_nodes": []any{
			map[string]any{"instance_id": "node-a", "state_incarnation_id": "shared-0001"},
			map[string]any{"instance_id": "node-b", "state_incarnation_id": "shared-0001"},
		},
		"nodes": []any{},
	}
	if err := validateSyncSemantics(doc); err == nil || !strings.Contains(err.Error(), "incarnation identities must be distinct") {
		t.Fatalf("expected duplicate-incarnation rejection, got %v", err)
	}
}

func TestSyncSemanticValidationRejectsStaleMembershipPlan(t *testing.T) {
	doc := map[string]any{
		"schema_version":       "agent-dispatch.sync-membership-plan/v1",
		"group_id":             "wiki-pair",
		"expected_predecessor": "old",
		"administrator_key":    "admin",
		"proposed_membership": map[string]any{
			"group_id": "wiki-pair", "predecessor": "new", "administrator_key": "admin",
			"active_members": []any{}, "historical_members": []any{},
		},
	}
	if err := validateSyncSemantics(doc); err == nil || !strings.Contains(err.Error(), "binding mismatch") {
		t.Fatalf("expected stale-plan rejection, got %v", err)
	}
}

func TestSyncMembershipAllowsRetainedIncarnationHistory(t *testing.T) {
	doc := map[string]any{
		"schema_version":    "agent-dispatch.sync-membership/v1",
		"administrator_key": "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"active_members": []any{
			map[string]any{"instance_id": "node-a", "state_incarnation_id": "node-a-0002", "publisher_key": "SHA256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA", "endpoint": "https://node-a.example.ts.net"},
			map[string]any{"instance_id": "node-b", "state_incarnation_id": "node-b-0001", "publisher_key": "SHA256:CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCA", "endpoint": "https://node-b.example.ts.net"},
		},
		"historical_members": []any{
			map[string]any{"instance_id": "node-a", "state_incarnation_id": "node-a-0001", "publisher_key": "SHA256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA"},
		},
	}
	if err := validateSyncSemantics(doc); err != nil {
		t.Fatalf("retained prior incarnation must remain representable: %v", err)
	}
}

func TestRequireBasenamesFailsClosed(t *testing.T) {
	err := requireBasenames("examples", []string{"examples/sync-membership.json"}, []string{"sync-membership.json", "sync-publication.json"})
	if err == nil || !strings.Contains(err.Error(), "sync-publication.json") {
		t.Fatalf("expected missing-family error, got %v", err)
	}
}

func TestSyncSemanticValidationRejectsPublicationCommitMismatch(t *testing.T) {
	doc := map[string]any{"schema_version": "agent-dispatch.sync-publication/v1", "state": "published", "content_ref": "refs/heads/wiki", "candidate_commit": "a", "remote_commit": "b"}
	if err := validateSyncSemantics(doc); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("expected publication mismatch rejection, got %v", err)
	}
}

func TestSyncSemanticValidationRejectsGitInvalidRef(t *testing.T) {
	doc := map[string]any{"schema_version": "agent-dispatch.sync-membership/v1", "content_ref": "refs/heads/wiki..sync"}
	if err := validateSyncSemantics(doc); err == nil || !strings.Contains(err.Error(), "valid configured Git ref") {
		t.Fatalf("expected Git-ref rejection, got %v", err)
	}
}

func TestValidateFailsOnEmptySchemas(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root)
	if err := os.RemoveAll(filepath.Join(root, "schemas")); err != nil {
		t.Fatal(err)
	}
	_, _, err := Validate(root)
	if err == nil || !strings.Contains(err.Error(), "no schema documents found") {
		t.Fatalf("expected empty-schemas error, got: %v", err)
	}
}

func TestValidateFailsOnEmptyExamples(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root)
	if err := os.Remove(filepath.Join(root, "examples", "report.json")); err != nil {
		t.Fatal(err)
	}
	_, _, err := Validate(root)
	if err == nil || !strings.Contains(err.Error(), "no schema-covered example documents found") {
		t.Fatalf("expected empty-examples error, got: %v", err)
	}
}

// TestValidateFailsOnMissingReportTarget guards the fail-closed posture: a
// configured integration report target that disappears must fail the check
// instead of silently dropping out of coverage.
func TestValidateFailsOnMissingReportTarget(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root)
	if err := os.Remove(filepath.Join(root, "integrations", "hermes-capability-report.json")); err != nil {
		t.Fatal(err)
	}
	_, _, err := Validate(root)
	if err == nil {
		t.Fatal("expected an error for the missing report target")
	}
	if !strings.Contains(err.Error(), "hermes-capability-report.json") {
		t.Errorf("error must name the missing target, got: %v", err)
	}
}

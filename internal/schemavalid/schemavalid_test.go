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
	for _, dir := range []string{"schemas", "examples", "integrations"} {
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
targets:
  kanban:
    type: hermes-kanban
    executable: hermes
    capability_report: /etc/caps.json
    required_capabilities: [durable_acceptance]
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
    dispatch:
      target: kanban
      profile: p
      skills: [s]
      mutex_key: m
      latest_state: true
      submission_retry: {max_attempts: 3, initial_backoff: 2s, max_backoff: 2m, multiplier: 2.0, jitter_fraction: 0.2}
      execution_hints: {max_runtime: 30m, max_attempts: 2}
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
	for _, want := range []string{"compiled 2 schema documents", "ok   examples/report.json", "ok   integrations/hermes-capability-report.json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("output missing %q; lines: %v", want, lines)
		}
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

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
	schema := `{"$id":"urn:jjukkumi:schema:hermes-capabilities:v1","type":"object"}`
	if err := os.WriteFile(filepath.Join(root, "schemas", "caps.schema.json"), []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "examples", "report.json"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "integrations", "hermes-capability-report.json"), []byte(`{"b":2}`), 0o644); err != nil {
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
	for _, want := range []string{"compiled 1 schema documents", "ok   examples/report.json", "ok   integrations/hermes-capability-report.json"} {
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

package config

import (
	"strings"
	"testing"
)

// TestE9T7SchemaRejectsNonTcharHeaderNames proves the schema pattern
// (E9-T7, D-023 F3): a header name outside the RFC 9110 tchar set fails
// configuration validation itself — before any sink construction or
// submission attempt — for both header fields, using the same invalid
// list the sink-level grammar test pins, while the equivalent tchar
// names load.
func TestE9T7SchemaRejectsNonTcharHeaderNames(t *testing.T) {
	base := string(e9t6WebhookYAML(t))
	invalid := []string{
		"Bad(Name", "Bad)Name", "Bad,Name", "Bad/Name", `Bad"Name`, `Bad\Name`,
		"Bad Name", "Bad;Name", "Bad=Name", "Bad{Name", "Bad}Name",
		"Bad<Name", "Bad>Name", "Bad?Name", "Bad@Name", "Bad[Name", "Bad]Name",
		"Bäd",
	}
	for _, bad := range invalid {
		changed := strings.Replace(base, "endpoint: https://example.invalid/hook",
			"endpoint: https://example.invalid/hook\n    idempotency_header: "+bad, 1)
		if _, err := Parse([]byte(changed)); err == nil || !strings.Contains(err.Error(), "pattern") {
			t.Fatalf("idempotency_header %q must fail schema validation with a pattern error, got %v", bad, err)
		}
	}
	for _, bad := range []string{"Bad(Name", "Bad,Name", `Bad\Name`} {
		changed := strings.Replace(strings.Replace(base, "type: bearer", "type: header", 1),
			"secret_ref: env:TEST_TOKEN", "secret_ref: env:TEST_TOKEN\n      header_name: "+bad, 1)
		if _, err := Parse([]byte(changed)); err == nil || !strings.Contains(err.Error(), "pattern") {
			t.Fatalf("auth.header_name %q must fail schema validation with a pattern error, got %v", bad, err)
		}
	}
	changed := strings.Replace(base, "endpoint: https://example.invalid/hook",
		"endpoint: https://example.invalid/hook\n    idempotency_header: X-Dedup-Key", 1)
	if _, err := Parse([]byte(changed)); err != nil {
		t.Fatalf("a valid tchar idempotency_header must load: %v", err)
	}
	changed = strings.Replace(strings.Replace(base, "type: bearer", "type: header", 1),
		"secret_ref: env:TEST_TOKEN", "secret_ref: env:TEST_TOKEN\n      header_name: X-Token", 1)
	if _, err := Parse([]byte(changed)); err != nil {
		t.Fatalf("a valid tchar auth.header_name must load: %v", err)
	}
}

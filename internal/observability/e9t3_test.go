package observability

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestE9T3MapValuePathSanitization proves the E8 correction's residual
// (E9-T3, M-20): string-map values ride the same path policy as every
// other value — under the redacted policy an absolute path inside a
// map[string]string never survives into the log line verbatim — and the
// typed map gets the same sensitive-key denylist as map[string]any (the
// round-1 security remediation).
func TestE9T3MapValuePathSanitization(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, LevelInfo, PathsRedacted)
	log.Info("test.map_paths", Correlation{TraceID: "t-e9t3"}, "map values are sanitized", map[string]any{
		"paths": map[string]string{"vault": "/srv/vault/secret-note.md", "kind": "plain-code"},
	})
	line := buf.String()
	if strings.Contains(line, "/srv/vault/secret-note.md") {
		t.Fatalf("a path inside a string map must not survive the redacted policy: %s", line)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &decoded); err != nil {
		t.Fatalf("the line stays valid JSON: %v", err)
	}
	data, _ := decoded["data"].(map[string]any)
	paths, _ := data["paths"].(map[string]any)
	if paths == nil {
		t.Fatalf("the string map must stay a map, got %v", data)
	}
	vault, _ := paths["vault"].(string)
	if !strings.HasPrefix(vault, "path:") {
		t.Fatalf("the path value must render through the path policy, got %q", vault)
	}
	if paths["kind"] != "plain-code" {
		t.Fatalf("non-path values pass through unchanged, got %v", paths["kind"])
	}

	buf.Reset()
	log.Info("test.map_denylist", Correlation{}, "sensitive keys denylist their values", map[string]any{
		"auth": map[string]string{"token": "hunter2", "note": "plain"},
	})
	if strings.Contains(buf.String(), "hunter2") {
		t.Fatalf("a value under a denylisted key must never survive the typed map: %s", buf.String())
	}
	decoded = map[string]any{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &decoded); err != nil {
		t.Fatalf("the line stays valid JSON: %v", err)
	}
	data, _ = decoded["data"].(map[string]any)
	auth, _ := data["auth"].(map[string]any)
	if auth == nil || auth["token"] != "[redacted]" {
		t.Fatalf("the denylisted key must render [redacted], got %v", auth)
	}
	if auth["note"] != "plain" {
		t.Fatalf("ordinary keys keep their values, got %v", auth)
	}
}

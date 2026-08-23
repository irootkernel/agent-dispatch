package observability

import (
	"encoding/json"
	"strings"
	"testing"
)

// decodeLog parses one emitted line.
func decodeLog(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("log line is not JSON: %v: %s", err, line)
	}
	return m
}

// TestLogShapeAndLevels proves OPS-001: every line is one structured
// JSON object with the event name and causal correlation fields, and
// the level filter drops below-threshold events.
func TestLogShapeAndLevels(t *testing.T) {
	var b strings.Builder
	log := New(&b, LevelWarn, PathsRelative)
	log.Info(EventObservationPersisted, Correlation{ObservationID: "obs-1"}, "persisted", nil)
	if b.Len() != 0 {
		t.Fatalf("info below the warn threshold must not emit: %s", b.String())
	}
	log.Warn(EventRouteDirtyMarked, Correlation{RouteID: "wiki", DispatchID: "d-1", ResourceID: "vault"}, "route dirty", map[string]any{"generation": 2})
	line := decodeLog(t, strings.TrimSuffix(b.String(), "\n"))
	if line["event"] != EventRouteDirtyMarked || line["level"] != "warn" {
		t.Fatalf("line = %v", line)
	}
	if line["route_id"] != "wiki" || line["dispatch_id"] != "d-1" || line["resource_id"] != "vault" {
		t.Fatalf("causal IDs missing: %v", line)
	}
	if line["message"] != "route dirty" {
		t.Fatalf("message missing: %v", line)
	}
	data, _ := line["data"].(map[string]any)
	if data["generation"] != float64(2) {
		t.Fatalf("data payload missing: %v", line)
	}
}

// TestRedaction proves SEC-007: credentials, authorization material,
// and note bodies never survive into a log line, whatever the payload
// shape.
func TestRedaction(t *testing.T) {
	var b strings.Builder
	log := New(&b, LevelDebug, PathsFull)
	log.Info(EventSourceReceived, Correlation{TraceID: "t-1"}, "received", map[string]any{
		"secret":        "super-secret-value",
		"Authorization": "Bearer super-secret-value",
		"note_body":     "the user's private note text",
		"token":         "abc",
		"path":          "Inbox/note.md",
		"nested":        []string{"a/b.md", "c.md"},
	})
	m := decodeLog(t, strings.TrimSuffix(b.String(), "\n"))
	raw := b.String()
	for _, leaked := range []string{"super-secret-value", "private note text", "abc"} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("log leaks %q: %s", leaked, raw)
		}
	}
	data, _ := m["data"].(map[string]any)
	if data["secret"] != "[redacted]" || data["note_body"] != "[redacted]" || data["token"] != "[redacted]" {
		t.Fatalf("sensitive keys not redacted: %v", data)
	}
	if data["path"] != "Inbox/note.md" {
		t.Fatalf("full policy must pass ordinary relative paths: %v", data)
	}
}

// TestPathPrivacyPolicies proves retention-and-privacy §4: the redacted
// policy digests path-shaped values while non-path values pass, and the
// other policies pass paths through.
func TestPathPrivacyPolicies(t *testing.T) {
	if got := RenderPath("Inbox/note.md", PathsRedacted); got == "Inbox/note.md" || !strings.HasPrefix(got, "path:") {
		t.Fatalf("redacted policy must digest paths, got %q", got)
	}
	// The digest is stable for the same path and differs across paths.
	same := RenderPath("Inbox/note.md", PathsRedacted)
	if same != RenderPath("Inbox/note.md", PathsRedacted) {
		t.Fatalf("path digest is not stable")
	}
	if same == RenderPath("Inbox/other.md", PathsRedacted) {
		t.Fatalf("distinct paths digest identically")
	}
	if got := RenderPath("dispatch-1", PathsRedacted); got != "dispatch-1" {
		t.Fatalf("non-path values must pass: %q", got)
	}
	if got := RenderPath("Inbox/note.md", PathsRelative); got != "Inbox/note.md" {
		t.Fatalf("relative policy must pass paths: %q", got)
	}
	if got := RenderPath("Inbox/note.md", PathsFull); got != "Inbox/note.md" {
		t.Fatalf("full policy must pass paths: %q", got)
	}
}

// TestDisabledLogger proves a nil writer emits nothing.
func TestDisabledLogger(t *testing.T) {
	log := New(nil, LevelDebug, PathsRelative)
	log.Error(EventDoctorFinding, Correlation{}, "finding", map[string]any{"k": "v"}) // must not panic
}

// TestParseLevelAndPolicy pin the CLI vocabularies with defaults.
func TestParseLevelAndPolicy(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Level
	}{
		{"debug", LevelDebug}, {"info", LevelInfo}, {"", LevelInfo}, {"warn", LevelWarn}, {"error", LevelError},
	} {
		got, err := ParseLevel(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseLevel(%q) = %v, %v", tc.in, got, err)
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Fatalf("unknown level must fail")
	}
	for _, tc := range []struct {
		in   string
		want PathPolicy
	}{
		{"relative", PathsRelative}, {"", PathsRelative}, {"redacted", PathsRedacted}, {"full", PathsFull},
	} {
		got, err := ParsePathPolicy(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParsePathPolicy(%q) = %v, %v", tc.in, got, err)
		}
	}
	if _, err := ParsePathPolicy("partial"); err == nil {
		t.Fatalf("unknown policy must fail")
	}
}

// TestE8T4CredentialRedactionWidened pins M-20: the header, query, and
// bare-JWT credential forms stay masked even when no emitter labeled
// them.
func TestE8T4CredentialRedactionWidened(t *testing.T) {
	adversarial := []string{
		"Authorization: Bearer abc123def456ghi789",
		"Authorization: Basic dXNlcjpwYXNzd29yZA==",
		"Token abc123def456ghi789",
		"https://x.test/cb?client_secret=sekritvalue",
		"https://x.test/cb#token=fragtok",
		"https://x.test/cb?apikey=keyvalue123",
		"https://x.test/cb?password=hunter2222",
		"https://x.test/cb?key=KeyValue999",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N65IhY0c8f7Sg1c8V6q8xU",
	}
	for _, s := range adversarial {
		got := RenderPath(s, PathsRedacted)
		if strings.Contains(got, "sekritvalue") || strings.Contains(got, "fragtok") ||
			strings.Contains(got, "keyvalue123") || strings.Contains(got, "hunter2222") ||
			strings.Contains(got, "KeyValue999") || strings.Contains(got, "dXNlcjpwYXNzd29yZA") ||
			strings.Contains(got, "abc123def456ghi789") || strings.Contains(got, "dozjgNryP4J3") {
			t.Errorf("credential leaked through RenderPath: %q -> %q", s, got)
		}
	}
}

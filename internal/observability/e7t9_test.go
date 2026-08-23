package observability

import (
	"path/filepath"
	"testing"
)

// TestCredentialFragmentRedaction proves the E7-T9/M-26 value-pattern
// redaction: bearer bodies and credential query parameters are masked,
// ordinary paths and IDs are untouched.
func TestCredentialFragmentRedaction(t *testing.T) {
	cases := map[string]string{
		"Bearer abc123def456ghi789":         "Bearer [redacted]",
		"https://x.test/cb?token=sekrit":    "https://x.test/cb?token=[redacted]",
		"https://x.test/cb?access_token=ab": "https://x.test/cb?access_token=[redacted]",
		"api_key=zzz&other=1":               "api_key=[redacted]&other=1",
		filepath.Join("Notes", "a.md"):      filepath.Join("Notes", "a.md"),
		"dispatch-123 accepted":             "dispatch-123 accepted",
	}
	for in, want := range cases {
		if got := RenderPath(in, PathsRelative); got != want {
			t.Errorf("RenderPath(%q) = %q, want %q", in, got, want)
		}
	}
}

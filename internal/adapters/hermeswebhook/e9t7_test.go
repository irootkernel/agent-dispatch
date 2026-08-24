package hermeswebhook

import (
	"errors"
	"testing"
)

// TestE9T7HeaderNameGrammar pins the RFC 9110 tchar allowlist (E9-T7,
// D-023 F3): the separators, quotes, and backslashes the previous scan
// let through — and the wider separator class a partial regression
// could admit — are rejected at sink construction, before any
// submission attempt, for both the authentication header name and the
// idempotency header, and the full tchar set constructs. (The previous
// scan already rejected spaces, control characters, non-ASCII runes,
// and the colon.)
func TestE9T7HeaderNameGrammar(t *testing.T) {
	invalid := []string{
		"Bad(Name", "Bad)Name", "Bad,Name", "Bad/Name", `Bad"Name`, `Bad\Name`,
		"Bad Name", "Bad;Name", "Bad=Name", "Bad{Name", "Bad}Name",
		"Bad<Name", "Bad>Name", "Bad?Name", "Bad@Name", "Bad[Name", "Bad]Name",
		"Bad\tName", "Bäd", "リクエスト",
	}
	for _, name := range invalid {
		opts := Options{TargetID: "hook", AuthType: "header", AuthHeaderName: name, SecretRef: "env:X", Endpoint: "https://example.invalid/hook"}
		_, err := NewSink(opts)
		var configErr *ConfigError
		if err == nil || !errors.As(err, &configErr) {
			t.Fatalf("auth.header_name %q must be rejected as a ConfigError, got %v", name, err)
		}
		opts = Options{TargetID: "hook", AuthType: "bearer", SecretRef: "env:X", Endpoint: "https://example.invalid/hook", IdempotencyHeader: name}
		_, err = NewSink(opts)
		if err == nil || !errors.As(err, &configErr) {
			t.Fatalf("idempotency_header %q must be rejected as a ConfigError, got %v", name, err)
		}
	}
	for _, name := range []string{"X-Request-Id", "a#!$%&b", "X_Own~Path", "A+B.C^D", "t`t", "0123456789ABCDEF"} {
		opts := Options{TargetID: "hook", AuthType: "header", AuthHeaderName: name, SecretRef: "env:X", Endpoint: "https://example.invalid/hook"}
		if _, err := NewSink(opts); err != nil {
			t.Fatalf("a valid tchar auth.header_name %q must construct: %v", name, err)
		}
		opts = Options{TargetID: "hook", AuthType: "bearer", SecretRef: "env:X", Endpoint: "https://example.invalid/hook", IdempotencyHeader: name}
		if _, err := NewSink(opts); err != nil {
			t.Fatalf("a valid tchar idempotency_header %q must construct: %v", name, err)
		}
	}
}

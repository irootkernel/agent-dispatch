package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// E9-T2 regression evidence: the reconciliation symlink posture, the
// Watchman-context maintenance refusal, and the operator-surface gaps.

// TestE9T2EscapingSymlinkNeverRegular proves M-24: every symlink —
// escaping or in-vault — is skipped with a warning and never projects
// as a regular-file fact or enters an automatic task manifest.
func TestE9T2EscapingSymlinkNeverRegular(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	outside := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "target"), filepath.Join(vault, "EscapeLink.md")); err != nil {
		t.Fatal(err)
	}
	// An in-vault symlink to an in-scope markdown file is skipped the
	// same way: the fact set describes regular files only.
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "real.md"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(vault, "Inbox", "real.md"), filepath.Join(vault, "Inbox", "alias.md")); err != nil {
		t.Fatal(err)
	}
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	if code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "manual", "--output", "json"}, &out, &errb); code != 0 {
		t.Fatalf("reconcile: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	for _, name := range []string{"EscapeLink.md", "alias.md"} {
		inSkipped := false
		for _, s := range mustStrings(t, res["skipped"]) {
			if s == name || strings.HasSuffix(s, "/"+name) {
				inSkipped = true
			}
		}
		if !inSkipped {
			t.Fatalf("symlink %s must be reported in the skipped list: %v", name, res)
		}
		for _, a := range mustStrings(t, res["added"]) {
			if a == name {
				t.Fatalf("symlink %s must never appear as an added fact: %v", name, res)
			}
		}
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var manifests int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents i
		WHERE i.request_json LIKE '%EscapeLink.md%' OR i.request_json LIKE '%alias.md%'`).Scan(&manifests); err != nil {
		t.Fatal(err)
	}
	if manifests != 0 {
		t.Fatalf("symlinks must never enter a task manifest, got %d intents", manifests)
	}
}

func mustStrings(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// TestE9T2MaintenanceRefusesWatchmanContext proves M-21/CLI-007: prune
// and vacuum refuse under the Watchman trigger environment at exit 2
// without touching the store.
func TestE9T2MaintenanceRefusesWatchmanContext(t *testing.T) {
	configPath, _ := e4t3Fixture(t)
	// Both guard conditions pin: the trigger name and the watch root
	// (round-2 F006).
	for _, env := range []string{"WATCHMAN_TRIGGER", "WATCHMAN_ROOT"} {
		var pout, perr bytes.Buffer
		t.Setenv(env, "probe-value")
		if code := Run([]string{"maintenance", "prune", "--config", configPath, "--yes"}, &pout, &perr); code != 2 {
			t.Fatalf("prune must refuse under %s at exit 2, got %d: %s", env, code, perr.String())
		}
		os.Unsetenv(env)
	}
	t.Setenv("WATCHMAN_TRIGGER", "agent-dispatch.wiki.abc")
	for _, sub := range []string{"prune", "vacuum"} {
		var out, errb bytes.Buffer
		code := Run([]string{"maintenance", sub, "--config", configPath, "--yes"}, &out, &errb)
		if code != 2 || !strings.Contains(errb.String(), "Watchman trigger context") {
			t.Fatalf("maintenance %s must refuse under WATCHMAN_TRIGGER at exit 2, got %d: %s", sub, code, errb.String())
		}
	}
}

// TestE9T2ConfigShowAcceptsOutputJson proves L-11.
func TestE9T2ConfigShowAcceptsOutputJson(t *testing.T) {
	configPath, _ := e4t3Fixture(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"config", "show", "--config", configPath, "--output", "json"}, &out, &errb); code != 0 {
		t.Fatalf("config show --output json: %s", errb.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Fatalf("config show must still emit JSON: %s", out.String()[:80])
	}
	var out2, errb2 bytes.Buffer
	if code := Run([]string{"config", "show", "--config", configPath, "--output", "yaml"}, &out2, &errb2); code != 2 {
		t.Fatalf("config show --output yaml must be a usage error, got %d", code)
	}
}

// TestE9T2UnknownDispatchAudits proves L-7: a work command against an
// unknown dispatch writes its invalid-receipt audit row.
func TestE9T2UnknownDispatchAudits(t *testing.T) {
	configPath, _ := e4t3Fixture(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", "does-not-exist", "--run-id", "r1"}, &out, &errb); code != 4 {
		t.Fatalf("unknown dispatch must exit 4, got %d: %s", code, errb.String())
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var n int
	if err := store.QueryRow(`SELECT COUNT(*) FROM state_transitions
		WHERE entity_type = 'work_receipt' AND entity_id = 'does-not-exist' AND to_state = 'invalid'`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("the unknown-dispatch rejection must leave an invalid-receipt audit row: %d %v", n, err)
	}
}

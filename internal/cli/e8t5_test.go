package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// E8-T5 regression evidence (H-8, M-18): delete-path containment and the
// widened semantic validation.

// TestE8T5SymlinkEscapeDeleteRejected proves H-8: a crafted stdin delete
// for a path that escapes through a symlinked directory is rejected with
// source_unsafe_path (exit 30) and never recorded as dispatchable — the
// plain delete previously bypassed every containment check.
func TestE8T5SymlinkEscapeDeleteRejected(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(vault, "EscapeDir")); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	setPlanEnv(t, vault, false)
	payload := `[{"name":"EscapeDir/secret.md","exists":false,"new":false,"type":"f"}]`
	withStdin(t, payload, func() {
		code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
		if code != 30 || !strings.Contains(errb.String(), "source_unsafe_path") {
			t.Fatalf("an escaping delete must reject with source_unsafe_path/30, got %d: %s", code, errb.String())
		}
	})
	store := e5t1Store(t, configPath)
	defer store.Close()
	var n int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("an escaping delete must never be recorded as dispatchable: %d %v", n, err)
	}
}

// TestE8T5SemanticValidationWidened proves the M-18 semantic half:
// overlapping resource roots, relative state_dir and resource roots,
// malformed map keys, and a zero max_hash_file_bytes all fail loading.
func TestE8T5SemanticValidationWidened(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	base := string(raw)
	mutations := map[string]func(string) string{
		"overlapping roots": func(s string) string {
			// The nesting resource sits under the same root the fixture
			// registers; the schema requires state_dir, carried over.
			return strings.Replace(s, "  vault-main:", "  vault-nest:\n    type: directory\n    root: "+filepath.Join(vault, "Notes")+"\n    file_scope: markdown\n  vault-main:", 1)
		},
		"relative state_dir": func(s string) string {
			return strings.Replace(s, "state_dir:", "state_dir_relative_do_not_match:", 1) + "\n  state_dir: relative/state\n"
		},
		"relative root": func(s string) string {
			return strings.Replace(s, "    root: "+vault, "    root: relative/vault", 1)
		},
		"malformed map key": func(s string) string {
			return strings.Replace(s, "  vault-main:", "  Vault_Main:", 1)
		},
		"zero hash floor": func(s string) string {
			return strings.Replace(s, "  state_dir:", "  state_dir_placeholder_remove:", 1)[:0] +
				strings.Replace(s, "resources:", "limits:\n  max_hash_file_bytes: 0\nresources:", 1)
		},
	}
	for name, mutate := range mutations {
		mutated := mutate(base)
		if mutated == base {
			t.Fatalf("mutation %q did not apply", name)
		}
		broken := filepath.Join(t.TempDir(), "broken.yaml")
		if err := os.WriteFile(broken, []byte(mutated), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		if code := Run([]string{"config", "validate", "--config", broken}, &out, &errb); code != 3 {
			t.Errorf("%s must fail validation at exit 3, got %d: %s", name, code, errb.String())
		}
	}
}

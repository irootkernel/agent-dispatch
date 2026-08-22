package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenExample is the SOT example configuration, the shared fixture for
// loading, revision, and redaction tests.
func goldenExample(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "examples", "config.yaml"))
	if err != nil {
		t.Fatalf("read docs/examples/config.yaml: %v", err)
	}
	return data
}

func TestParseGoldenExample(t *testing.T) {
	cfg, err := Parse(goldenExample(t))
	if err != nil {
		t.Fatalf("golden example must load: %v", err)
	}
	if cfg.Version != 1 || cfg.Instance.ID != "workstation-main" {
		t.Errorf("unexpected header fields: %+v", cfg.Instance)
	}
	if len(cfg.Resources) != 1 || len(cfg.Targets) != 2 || len(cfg.Routes) != 1 {
		t.Errorf("unexpected collection sizes: %d/%d/%d", len(cfg.Resources), len(cfg.Targets), len(cfg.Routes))
	}
	if cfg.Routes["wiki-maintenance"].Enabled {
		t.Error("example route must stay disabled")
	}
}

func TestParseGoldenWithReplacedPaths(t *testing.T) {
	// Acceptance: the example validates after placeholder paths are
	// replaced with real local ones.
	root := t.TempDir()
	state := filepath.Join(filepath.Dir(root), "agent-dispatch-state")
	text := string(goldenExample(t))
	text = strings.ReplaceAll(text, "/Users/example/Documents/Obsidian/MainVault", root)
	text = strings.ReplaceAll(text, "/Users/example/Library/Application Support/Agent Dispatch", state)
	text = strings.ReplaceAll(text, "/Users/example/.config/agent-dispatch/hermes-capabilities.json", filepath.Join(state, "caps.json"))
	if _, err := Parse([]byte(text)); err != nil {
		t.Fatalf("replaced-path example must validate: %v", err)
	}
}

func TestDuplicateKeysFail(t *testing.T) {
	text := "version: 1\nversion: 1\ninstance:\n  id: a\n  id: b\n"
	if _, err := Parse([]byte(text)); err == nil {
		t.Fatal("duplicate YAML keys must fail")
	}
}

func TestUnknownFieldsFailClosed(t *testing.T) {
	text := strings.Replace(string(minimalYAML(t)), "log_paths: relative\n", "log_paths: relative\nbogus_field: 1\n", 1)
	if _, err := Parse([]byte(text)); err == nil {
		t.Fatal("unknown top-level fields must fail closed")
	}
	nested := strings.Replace(string(minimalYAML(t)), "file_scope: markdown\n", "file_scope: markdown\ntypo: yes\n", 1)
	if _, err := Parse([]byte(nested)); err == nil {
		t.Fatal("unknown nested fields must fail closed")
	}
}

func TestUnknownReferencesFail(t *testing.T) {
	text := strings.Replace(string(minimalYAML(t)), "resource: vault-main", "resource: nope", 1)
	if _, err := Parse([]byte(text)); err == nil || !strings.Contains(err.Error(), "unknown resource") {
		t.Fatalf("unknown resource reference must fail: %v", err)
	}
	text = strings.Replace(string(minimalYAML(t)), "target: hermes-kanban-main", "target: nope", 1)
	if _, err := Parse([]byte(text)); err == nil || !strings.Contains(err.Error(), "unknown target") {
		t.Fatalf("unknown target reference must fail: %v", err)
	}
}

func TestStateDirInsideVaultWarns(t *testing.T) {
	// Spec §3: "Validation warns if it is."
	text := strings.Replace(string(minimalYAML(t)), "state_dir: /var/lib/agent-dispatch", "state_dir: /srv/vault/state", 1)
	cfg, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("state dir inside the vault must warn, not fail: %v", err)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "inside resource") {
		t.Fatalf("expected one inside-vault warning, got %v", cfg.Warnings)
	}
}

func TestMaxBackoffBelowInitialFails(t *testing.T) {
	text := strings.Replace(string(minimalYAML(t)), "max_backoff: 2m", "max_backoff: 1s", 1)
	if _, err := Parse([]byte(text)); err == nil || !strings.Contains(err.Error(), "max_backoff") {
		t.Fatalf("max_backoff < initial_backoff must fail: %v", err)
	}
}

func TestRouteRevisionDeterministicAndSensitive(t *testing.T) {
	cfg, err := Parse(minimalYAML(t))
	if err != nil {
		t.Fatal(err)
	}
	first, ok := RouteRevision(cfg, "r1")
	if !ok {
		t.Fatal("route missing")
	}
	again, _ := RouteRevision(cfg, "r1")
	if first != again {
		t.Fatalf("revision must be deterministic: %s vs %s", first, again)
	}
	if len(first) != 64 {
		t.Fatalf("revision must be a sha256 hex digest, got %d chars", len(first))
	}

	// Display order and comments do not affect the revision: reorder the
	// include list and swap route keys in serialization order.
	reordered := strings.Replace(string(minimalYAML(t)), "include:\n      - \"docs/*.md\"\n      - \"**/*.md\"\n", "include:\n      - \"**/*.md\"\n      - \"docs/*.md\"\n", 1)
	cfg2, err := Parse([]byte(reordered))
	if err != nil {
		t.Fatal(err)
	}
	if rev, _ := RouteRevision(cfg2, "r1"); rev != first {
		t.Error("pattern display order must not change the revision")
	}

	// Behavior-affecting change produces a new revision (POL-007).
	changed := strings.Replace(string(minimalYAML(t)), "hard_limit: 100", "hard_limit: 101", 1)
	cfg3, err := Parse([]byte(changed))
	if err != nil {
		t.Fatal(err)
	}
	if rev, _ := RouteRevision(cfg3, "r1"); rev == first {
		t.Error("changing a behavior-affecting field must change the revision")
	}

	// Excluded fields: state directory and log level stay out.
	excluded := strings.Replace(string(minimalYAML(t)), "state_dir: /var/lib/agent-dispatch", "state_dir: /var/lib/other", 1)
	excluded = strings.Replace(excluded, "log_paths: relative", "log_paths: full", 1)
	cfg4, err := Parse([]byte(excluded))
	if err != nil {
		t.Fatal(err)
	}
	if rev, _ := RouteRevision(cfg4, "r1"); rev != first {
		t.Error("state_dir and log_paths must not affect the revision")
	}
}

func TestNormalizedRedactedDeterministic(t *testing.T) {
	cfg, err := Parse(minimalYAML(t))
	if err != nil {
		t.Fatal(err)
	}
	a, err := cfg.Normalized()
	if err != nil {
		t.Fatal(err)
	}
	b, err := cfg.Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("normalized output must be deterministic")
	}
	if bytes.Contains(a, []byte("resolve")) {
		t.Error("normalized output must not contain resolved secrets")
	}
	// The secret reference identifier may appear; a value never does.
	if !bytes.Contains(a, []byte("env:TEST_TOKEN")) {
		t.Error("secret reference identifier should be visible in normalized output")
	}
}

func TestSchemaDrift(t *testing.T) {
	sot, err := os.ReadFile(filepath.Join("..", "..", "docs", "schemas", "config.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(sot), bytes.TrimSpace(schemaJSON)) {
		t.Fatal("internal/config/schema/config.schema.json drifted from docs/schemas/config.schema.json; copy the SOT schema")
	}
}

func TestWriteExampleRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	cfg := Example("test-instance", dir, filepath.Join(dir, "caps.json"))
	path := filepath.Join(dir, "config.yaml")
	if err := WriteExample(cfg, path); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteExample(cfg, path); err == nil {
		t.Fatal("second write must refuse to overwrite")
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The written example must round-trip through the loader.
	if _, err := Parse(written); err != nil {
		t.Fatalf("written example must load: %v", err)
	}
}

func TestParseSecretRefForms(t *testing.T) {
	ok := []string{"env:HOME", "env:_PRIVATE_1", "file:/etc/agent-dispatch/token", "keychain:agent-dispatch-webhook", "fd:3"}
	for _, ref := range ok {
		if _, err := ParseSecretRef(ref); err != nil {
			t.Errorf("%q must parse: %v", ref, err)
		}
	}
	bad := []string{"", "env:", "env:1X", "file:relative", "keychain:", "fd:0", "fd:03", "fd:-1", "literal-secret", "env:ho me"}
	for _, ref := range bad {
		if _, err := ParseSecretRef(ref); err == nil {
			t.Errorf("%q must fail closed", ref)
		}
	}
	sr, err := ParseSecretRef("file:/etc/agent-dispatch/token")
	if err != nil || sr.Redacted() != "file:/etc/agent-dispatch/token" || sr.Kind != RefFile || sr.Path != "/etc/agent-dispatch/token" {
		t.Errorf("unexpected parse result: %+v %v", sr, err)
	}
}

// minimalYAML is a small valid configuration used by failure-path and
// revision tests.
func minimalYAML(t *testing.T) []byte {
	t.Helper()
	return []byte(`version: 1
instance:
  id: test-instance
  state_dir: /var/lib/agent-dispatch
  log_paths: relative
resources:
  vault-main:
    type: directory
    root: /srv/vault
    file_scope: markdown
    git:
      mode: disabled
targets:
  hermes-kanban-main:
    type: hermes-kanban
    board: agent-dispatch
    executable: hermes
    capability_report: /etc/agent-dispatch/caps.json
    required_capabilities:
      - durable_acceptance
      - submit_idempotency_key
  hermes-webhook-immediate:
    type: hermes-webhook
    endpoint: https://example.invalid/hook
    auth:
      type: bearer
      secret_ref: env:TEST_TOKEN
routes:
  r1:
    enabled: false
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: agent-dispatch.r1.abc123
      include:
        - "docs/*.md"
        - "**/*.md"
      exclude:
        - ".git/**"
    batching:
      automatic_threshold: 25
      hard_limit: 100
      max_manifest_bytes: 262144
    policy:
      protected:
        - "raw/**"
      immutable: []
      bulk_action: quarantine
      overflow_action: reconcile
      fresh_instance_action: reconcile
      unsafe_path_action: quarantine
    dispatch:
      target: hermes-kanban-main
      profile: wiki-maintainer
      skills:
        - llm-wiki
      mutex_key: wiki-publish
      latest_state: true
      submission_retry:
        max_attempts: 3
        initial_backoff: 2s
        max_backoff: 2m
        multiplier: 2.0
        jitter_fraction: 0.2
      execution_hints:
        max_runtime: 30m
        max_attempts: 2
      failure_budget: 2
      active_stale_after: 2h
    reconciliation:
      initial: true
      daily_expected: true
`)
}

func TestNormalizedGolden(t *testing.T) {
	cfg, err := Parse(minimalYAML(t))
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.Normalized()
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "normalized.golden.json")
	if _, err := os.Stat(golden); os.IsNotExist(err) {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("normalized output drifted from golden file:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestParseDurationOverflowFails(t *testing.T) {
	if _, err := parseDuration("9223372036854775807d"); err == nil {
		t.Fatal("overflowing duration must fail")
	}
	if _, err := parseDuration("106751991167300d"); err == nil {
		t.Fatal("duration exceeding int64 nanoseconds must fail")
	}
	d, err := parseDuration("1d")
	if err != nil || d.Nanos != 24*3600*int64(1e9) {
		t.Fatalf("1d parsed wrong: %+v %v", d, err)
	}
}

func TestEnsureStateDirNonDirectoryFails(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := EnsureStateDir(file)
	if err == nil || !IsStateDirPlacementError(err) {
		t.Fatalf("non-directory state path must be a placement error: %v", err)
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("unexpected placement message: %v", err)
	}
	link := filepath.Join(dir, "alink")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	err = EnsureStateDir(link)
	if err == nil || !IsStateDirPlacementError(err) || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlinked state dir must be a placement error naming the link: %v", err)
	}
}

func TestResolvePathPrecedence(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit.yaml")
	envPath := filepath.Join(dir, "env.yaml")
	def := filepath.Join(dir, "default.yaml")
	for _, p := range []string{explicit, envPath, def} {
		if err := os.WriteFile(p, []byte("version: 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AGENT_DISPATCH_CONFIG", envPath)
	if got, ok := ResolvePath("", func() string { return def }); got != envPath || !ok {
		t.Errorf("env precedence: %q %v", got, ok)
	}
	if got, ok := ResolvePath(explicit, func() string { return def }); got != explicit || !ok {
		t.Errorf("explicit precedence: %q %v", got, ok)
	}
	t.Setenv("AGENT_DISPATCH_CONFIG", "")
	if got, ok := ResolvePath("", func() string { return def }); got != def || !ok {
		t.Errorf("default precedence: %q %v", got, ok)
	}
	if _, ok := ResolvePath(filepath.Join(dir, "missing.yaml"), func() string { return def }); ok {
		t.Error("missing explicit path must report not-existing")
	}
}

// TestWebhookAuthShapeSemanticGates pins the two fail-closed webhook
// authentication shapes beyond the schema (configuration-spec §5, §12):
// header authentication requires its header name and bearer
// authentication rejects one. These gates are the only enforcement of
// the bearer shape, so their errors are pinned exactly.
func TestWebhookAuthShapeSemanticGates(t *testing.T) {
	base := func(auth Auth) *Config {
		return &Config{
			Version:  1,
			Instance: Instance{ID: "test"},
			Resources: map[string]Resource{
				"vault": {Type: "directory", Root: "/srv/vault", FileScope: "markdown"},
			},
			Targets: map[string]Target{
				"hook": {
					Type:     "hermes-webhook",
					Endpoint: "https://example.invalid/hook",
					Auth:     &auth,
				},
			},
			Routes: map[string]Route{},
		}
	}
	cases := []struct {
		name    string
		auth    Auth
		wantErr string
	}{
		{"header without name", Auth{Type: "header", SecretRef: "env:HOOK_TOKEN"}, "required when auth.type is header"},
		{"bearer with name", Auth{Type: "bearer", SecretRef: "env:HOOK_TOKEN", HeaderName: "X-Hook"}, "must be empty when auth.type is bearer"},
		{"header with name", Auth{Type: "header", SecretRef: "env:HOOK_TOKEN", HeaderName: "X-Hook"}, ""},
		{"bearer without name", Auth{Type: "bearer", SecretRef: "env:HOOK_TOKEN"}, ""},
	}
	for _, tc := range cases {
		errs, _ := SemanticValidate(base(tc.auth))
		if tc.wantErr == "" {
			for _, err := range errs {
				if strings.Contains(err.Error(), "auth.header_name") {
					t.Fatalf("%s: unexpected auth-shape error: %v", tc.name, err)
				}
			}
			continue
		}
		found := false
		for _, err := range errs {
			if strings.Contains(err.Error(), tc.wantErr) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: missing error %q in %v", tc.name, tc.wantErr, errs)
		}
	}
}

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
)

func TestInitCreatesDisabledExampleAndStateDir(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "agent-dispatch", "config.yaml")
	stateDir := filepath.Join(dir, "state")

	var out, errb bytes.Buffer
	code := Run([]string{"init", "--config", configPath, "--state-dir", stateDir, "--instance-id", "test-main", "--resource-root", filepath.Join(dir, "vault")}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	info, err := os.Stat(configPath)
	if err != nil || info.IsDir() {
		t.Fatalf("config not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config permissions = %o, want 0600 (SEC-008)", perm)
	}
	if info, err := os.Stat(stateDir); err != nil || !info.IsDir() {
		t.Fatalf("state dir not created: %v", err)
	}

	// The written example must round-trip through the strict loader.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.Parse(data); err != nil {
		t.Errorf("written config does not load: %v", err)
	}
	if !bytes.Contains(data, []byte("enabled: false")) {
		t.Error("example route must be disabled")
	}
}

func TestInitRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	args := []string{"init", "--config", configPath, "--state-dir", filepath.Join(filepath.Dir(dir), "agent-dispatch-state"), "--resource-root", dir}
	if code := Run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("first init must succeed")
	}
	before, _ := os.ReadFile(configPath)
	var out, errb bytes.Buffer
	if code := Run(args, &out, &errb); code != 3 {
		t.Fatalf("second init must fail with exit 3, got %d", code)
	}
	after, _ := os.ReadFile(configPath)
	if !bytes.Equal(before, after) {
		t.Error("refused init must not modify the existing config")
	}
	var env ErrorEnvelope
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil || env.Error.Code != "config_invalid" {
		t.Errorf("unexpected error envelope: %v %s", err, errb.String())
	}
}

func TestInitSupportsJSONOutput(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := Run([]string{"init", "--config", filepath.Join(dir, "c.yaml"), "--state-dir", filepath.Join(dir, "s"), "--resource-root", dir, "--output", "json"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	var env struct {
		APIVersion string `json:"api_version"`
		Command    string `json:"command"`
		OK         bool   `json:"ok"`
		Result     struct {
			ConfigPath string `json:"config_path"`
			StateDir   string `json:"state_dir"`
			Enabled    bool   `json:"enabled"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("stdout not JSON envelope: %v (%s)", err, out.String())
	}
	if env.Command != "init" || !env.OK || env.Result.Enabled {
		t.Errorf("unexpected envelope: %+v", env)
	}
}

func TestInitSupportsEqualsFormOutputJSON(t *testing.T) {
	dir := t.TempDir()
	cases := [][]string{
		{"--output=json", "c1.yaml", "s1"},
		{"-o", "c2.yaml", "s2"},
	}
	for k, c := range cases {
		args := []string{"init", "--config", filepath.Join(dir, c[1]), "--state-dir", filepath.Join(dir, c[2]), "--resource-root", dir}
		if c[0] == "--output=json" {
			args = append(args, "--output=json")
		} else {
			args = append(args, "-o", "json")
		}
		var out, errb bytes.Buffer
		if code := Run(args, &out, &errb); code != 0 {
			t.Errorf("case %d: exit %d, stderr %s", k, code, errb.String())
			continue
		}
		var env struct {
			Command string `json:"command"`
			OK      bool   `json:"ok"`
		}
		if err := json.Unmarshal(out.Bytes(), &env); err != nil || env.Command != "init" || !env.OK {
			t.Errorf("case %d: stdout not an init envelope: %s", k, out.String())
		}
	}
}

func TestInitAcceptsHumanOutputValue(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := Run([]string{"init", "--config", filepath.Join(dir, "c.yaml"), "--state-dir", filepath.Join(dir, "s"), "--resource-root", dir, "--output", "human"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if json.Valid(out.Bytes()) {
		t.Errorf("human output must not be a JSON document: %q", out.String())
	}
}

func TestInitRejectsSymlinkedStateDir(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"init", "--config", filepath.Join(dir, "c.yaml"), "--state-dir", link, "--resource-root", dir}, &out, &errb)
	if code != 3 {
		t.Fatalf("symlinked state dir must fail with exit 3, got %d", code)
	}
	var env ErrorEnvelope
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil || env.Error.Code != "state_directory_not_local" {
		t.Errorf("unexpected error envelope: %v %s", err, errb.String())
	}
}

func TestInitStateDirPreparedBeforeConfig(t *testing.T) {
	// A failing configuration write must not leave a state-dir-only or
	// config-only installation: fixing the config failure and rerunning
	// init succeeds.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "sub", "c.yaml")
	args := func() []string {
		return []string{"init", "--config", configPath, "--state-dir", filepath.Join(dir, "s"), "--resource-root", dir}
	}
	var out, errb bytes.Buffer
	if code := Run(args(), &out, &errb); code != 0 {
		t.Fatalf("first init: %d %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "s")); err != nil {
		t.Fatal("state dir must exist")
	}
	// Simulate the historical partial state: config gone, state dir present.
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run(args(), &out, &errb); code != 0 {
		t.Fatalf("init after partial state must succeed: %d %s", code, errb.String())
	}
}

func TestInitWarnsWhenStateDirInsideResourceRoot(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := Run([]string{"init", "--config", filepath.Join(dir, "c.yaml"), "--state-dir", filepath.Join(dir, "vault", "state"), "--resource-root", filepath.Join(dir, "vault")}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !bytes.Contains(errb.Bytes(), []byte("inside the watched resource root")) {
		t.Errorf("expected section-3 warning on stderr, got: %q", errb.String())
	}
}

func TestInitStateDirIOFailureUsesConfigInvalid(t *testing.T) {
	// Root's CAP_DAC_OVERRIDE defeats chmod-based permission
	// expectations; the D-020 Linux record names this test (E8-T6).
	if os.Geteuid() == 0 {
		t.Skip("permission-expectation test defeats CAP_DAC_OVERRIDE under root (D-020)")
	}
	// A file where the state directory belongs is a placement error; a
	// read-only parent produces an IO failure classified config_invalid.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.MkdirAll(filepath.Join(blocker, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(blocker, "sub"), 0o500); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"init", "--config", filepath.Join(dir, "c.yaml"), "--state-dir", filepath.Join(blocker, "sub", "s"), "--resource-root", dir}, &out, &errb)
	if code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
	var env ErrorEnvelope
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
		t.Fatalf("stderr not envelope: %s", errb.String())
	}
	if env.Error.Code != "config_invalid" {
		t.Errorf("IO failure must classify config_invalid, got %q", env.Error.Code)
	}
	_ = out
}

func TestWriteInternalErrorEnvelope(t *testing.T) {
	var buf bytes.Buffer
	WriteInternalError(&buf, "boom sensitive detail")
	var env ErrorEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("not an envelope: %v (%s)", err, buf.String())
	}
	if env.Error.Code != "internal_unclassified" || env.Error.Category != "internal" {
		t.Errorf("unexpected envelope: %+v", env.Error)
	}
	if bytes.Contains(buf.Bytes(), []byte("sensitive")) {
		t.Error("panic cause must not reach diagnostics")
	}
}

func TestInitFlagUsageErrors(t *testing.T) {
	cases := [][]string{
		{"init", "--config"},
		{"init", "--state-dir"},
		{"init", "--instance-id"},
		{"init", "--resource-root"},
		{"init", "--output"},
		{"init", "--output", "yaml"},
		{"init", "--nonsense"},
	}
	for _, args := range cases {
		var out, errb bytes.Buffer
		if code := Run(args, &out, &errb); code != 2 {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
		var env ErrorEnvelope
		if err := json.Unmarshal(errb.Bytes(), &env); err != nil || env.Error.Code != "flag_invalid" {
			t.Errorf("%v: unexpected envelope %v (%s)", args, err, errb.String())
		}
		if out.Len() != 0 {
			t.Errorf("%v: stdout must stay empty", args)
		}
	}
}

func TestInitOutputHumanEqualsForm(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := Run([]string{"init", "--config", filepath.Join(dir, "c.yaml"), "--state-dir", filepath.Join(dir, "s"), "--resource-root", dir, "--output=human"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errb.String())
	}
	if json.Valid(out.Bytes()) {
		t.Errorf("human output must not be a JSON document: %q", out.String())
	}
}

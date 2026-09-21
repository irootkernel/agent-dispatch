package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
)

func enabledSyncConfig(t *testing.T) string {
	t.Helper()
	cfg, err := config.Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Sync.Enabled = true
	cfg.Instance.StateDir = filepath.Join(t.TempDir(), "state")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func syncResult(t *testing.T, args ...string) (int, map[string]any, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(append([]string{"sync"}, args...), &out, &errOut)
	var envelope map[string]any
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatalf("decode stdout: %v: %s", err, out.String())
		}
	}
	return code, envelope, errOut.String()
}

func TestE21T1SyncPauseResumeAndStatus(t *testing.T) {
	configPath := enabledSyncConfig(t)
	code, envelope, stderr := syncResult(t, "status", "--group", "wiki-pair", "--config", configPath, "--output", "json")
	if code != 0 {
		t.Fatalf("initial status: %d %s", code, stderr)
	}
	status := envelope["result"].(map[string]any)
	if status["state"] != "active" || status["control_revision"] != float64(1) {
		t.Fatalf("initial control = %v", status)
	}
	code, envelope, stderr = syncResult(t, "pause", "--group", "wiki-pair", "--expected-control-revision", "1", "--config", configPath, "--output", "json")
	if code != 0 {
		t.Fatalf("pause: %d %s", code, stderr)
	}
	control := envelope["result"].(map[string]any)
	if control["schema_version"] != "agent-dispatch.sync-control/v1" || control["state"] != "paused" || control["revision"] != float64(2) || len(control) != 5 {
		t.Fatalf("pause result = %v", control)
	}
	code, _, stderr = syncResult(t, "resume", "--group", "wiki-pair", "--expected-control-revision", "1", "--config", configPath, "--output", "json")
	if code != 14 || !bytes.Contains([]byte(stderr), []byte("sync_precondition_failed")) {
		t.Fatalf("stale resume: %d %s", code, stderr)
	}
	code, envelope, stderr = syncResult(t, "resume", "--group", "wiki-pair", "--expected-control-revision", "2", "--config", configPath, "--output", "json")
	if code != 0 || envelope["result"].(map[string]any)["state"] != "active" {
		t.Fatalf("resume: %d %v %s", code, envelope, stderr)
	}
}

func TestE21T1EnabledConfigDoesNotActivateReservedGitCommands(t *testing.T) {
	configPath := enabledSyncConfig(t)
	code, _, stderr := syncResult(t, "publish", "--group", "wiki-pair", "--config", configPath, "--output", "json")
	if code != 3 || !bytes.Contains([]byte(stderr), []byte("sync_capability_unavailable")) {
		t.Fatalf("reserved publish: %d %s", code, stderr)
	}
}

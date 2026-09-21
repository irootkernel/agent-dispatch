package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
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
	for _, field := range []string{"latest_publication", "latest_delivery", "latest_import"} {
		if latest, ok := status[field].(map[string]any); !ok || latest["present"] != false {
			t.Fatalf("initial %s status = %v", field, status[field])
		}
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

func TestE21T5StatusSeparatesDurableSyncOutcomes(t *testing.T) {
	configPath := enabledSyncConfig(t)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	revision, ok := config.SyncRevision(cfg)
	if !ok {
		t.Fatal("sync revision unavailable")
	}
	store, err := openStateStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.EnsureSyncControl(requestCtx(), cfg.Sync.GroupID, revision, "2026-09-21T01:00:00Z"); err != nil {
		t.Fatal(err)
	}
	target := "1111111111111111111111111111111111111111"
	jobs := []sqlite.SyncJobInput{
		{JobID: "publication-job-g17", GroupID: cfg.Sync.GroupID, Kind: "publication", LogicalKey: "publication-g17", InitialState: "signed", PayloadJSON: `{"publication_id":"publication-g17","target_commit":"` + target + `"}`, QueueLimit: 10, Now: "2026-09-21T01:00:01Z"},
		{JobID: "delivery-job-g17", GroupID: cfg.Sync.GroupID, Kind: "delivery", LogicalKey: "publication-g17", InitialState: "pending", PayloadJSON: `{"publication_id":"publication-g17","receiver":"node-b","target_commit":"` + target + `"}`, QueueLimit: 10, Now: "2026-09-21T01:00:02Z"},
		{JobID: "import-job-g17", GroupID: cfg.Sync.GroupID, Kind: "import", LogicalKey: target, InitialState: "requested", PayloadJSON: `{"import_id":"import-g17","target_commit":"` + target + `"}`, QueueLimit: 10, Now: "2026-09-21T01:00:03Z"},
	}
	for _, job := range jobs {
		if _, _, err := store.AdmitSyncJob(requestCtx(), job); err != nil {
			t.Fatalf("admit %s: %v", job.Kind, err)
		}
	}
	if _, err := store.Exec(`INSERT INTO sync_journal_entries
		(journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES
		('publication-status-evidence','publication-job-g17',1,'publication','effect_unknown','{"candidate":"2222222222222222222222222222222222222222","remote":"1111111111111111111111111111111111111111","push_state":"ambiguous","reason":"push outcome unknown"}','2026-09-21T01:00:04Z'),
		('import-status-evidence','import-job-g17',1,'import','deferred','{"reason":"observation_unavailable"}','2026-09-21T01:00:05Z')`); err != nil {
		t.Fatal(err)
	}
	code, envelope, stderr := syncResult(t, "status", "--group", cfg.Sync.GroupID, "--config", configPath, "--output", "json")
	if code != 0 {
		t.Fatalf("status: %d %s", code, stderr)
	}
	status := envelope["result"].(map[string]any)
	for field, wantState := range map[string]string{
		"latest_publication": "signed",
		"latest_delivery":    "pending",
		"latest_import":      "requested",
	} {
		latest, ok := status[field].(map[string]any)
		if !ok || latest["present"] != true || latest["state"] != wantState || latest["target_commit"] != target || latest["claimed"] != false || latest["resolved"] != false {
			t.Fatalf("%s = %v", field, status[field])
		}
	}
	if status["latest_publication"].(map[string]any)["publication_id"] != "publication-g17" || status["latest_delivery"].(map[string]any)["receiver"] != "node-b" || status["latest_import"].(map[string]any)["import_id"] != "import-g17" {
		t.Fatalf("typed status details = %v", status)
	}
	publication := status["latest_publication"].(map[string]any)
	if publication["candidate"] != "2222222222222222222222222222222222222222" || publication["remote"] != target || publication["push_state"] != "ambiguous" || publication["reason"] != "push outcome unknown" {
		t.Fatalf("publication recovery evidence = %v", publication)
	}
	if got := status["latest_import"].(map[string]any)["reason"]; got != "observation_unavailable" {
		t.Fatalf("import deferral reason = %v", got)
	}
}

func TestE21T1EnabledConfigDoesNotActivateStillReservedGitCommands(t *testing.T) {
	configPath := enabledSyncConfig(t)
	code, _, stderr := syncResult(t, "verify", "--group", "wiki-pair", "--config", configPath, "--output", "json")
	if code != 3 || !bytes.Contains([]byte(stderr), []byte("sync_capability_unavailable")) {
		t.Fatalf("reserved verify: %d %s", code, stderr)
	}
}

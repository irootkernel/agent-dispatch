package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func e23t3Config(t *testing.T) (*config.Config, string) {
	t.Helper()
	cfg, err := config.Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Instance.StateDir = filepath.Join(t.TempDir(), "state")
	cfg.Sync.Enabled = true
	cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now", "verify-all", "retry", "pause", "resume"}}
	path := filepath.Join(t.TempDir(), "config.yaml")
	e23t3SaveConfig(t, cfg, path)
	return cfg, path
}
func e23t3SaveConfig(t *testing.T, cfg *config.Config, path string) {
	t.Helper()
	raw, err := config.MarshalYAML(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func e23t3Invoke(t *testing.T, path string, args ...string) (int, map[string]any) {
	t.Helper()
	var out, errOut bytes.Buffer
	args = append(args, "--config", path)
	code := runSyncRequestQualified(context.Background(), args, false, []string{"sync-now", "verify-all", "pause"}, &out, &errOut)
	raw := out.Bytes()
	if code != 0 {
		if out.Len() != 0 {
			t.Fatal("error wrote stdout")
		}
		raw = errOut.Bytes()
	} else if errOut.Len() != 0 {
		t.Fatal("success wrote stderr")
	}
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("invalid carrier %q: %v", raw, err)
	}
	e23t3ValidateCarrier(t, envelope)
	return code, envelope
}
func e23t3ValidateCarrier(t *testing.T, value any) {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	compiler.DefaultDraft(jsonschema.Draft2020)
	for _, name := range []string{"result.schema.json", "envelope.schema.json"} {
		raw, err := os.ReadFile("../../docs/contracts/sync-management-v1/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		id := "urn:agent-dispatch:schema:sync-management-result:v1"
		if name == "envelope.schema.json" {
			id = "urn:agent-dispatch:schema:sync-management-envelope:v1"
		}
		if err := compiler.AddResource(id, doc); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := compiler.Compile("urn:agent-dispatch:schema:sync-management-envelope:v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("actual CLI carrier violates frozen schema: %v\n%v", err, value)
	}
}
func e23t3Prepare(t *testing.T, path, group, action string) string {
	t.Helper()
	code, e := e23t3Invoke(t, path, "prepare", "--group", group, "--action", action)
	if code != 0 {
		t.Fatalf("prepare: %d %v", code, e)
	}
	return e["result"].(map[string]any)["request_id"].(string)
}

func TestE23T3FrozenArgvAndRawGlobalClosure(t *testing.T) {
	raw, err := os.ReadFile("../../docs/contracts/sync-management-v1/argv-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Fixtures []struct {
			Name  string
			Valid bool
			Argv  []string
		}
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, f := range doc.Fixtures {
		t.Run(f.Name, func(t *testing.T) {
			_, err := parseManagementFlags(f.Argv)
			if (err == nil) != f.Valid {
				t.Fatalf("valid=%t err=%v argv=%q", f.Valid, err, f.Argv)
			}
		})
	}
	for _, flag := range []string{"--state-dir", "--timeout", "--trace-id", "--log-level"} {
		for _, prefix := range []bool{false, true} {
			var args []string
			if prefix {
				args = []string{"sync", flag, "value", "request", "capabilities", "--group", "wiki"}
			} else {
				args = []string{"sync", "request", "capabilities", "--group", "wiki", flag, "value"}
			}
			var out, errOut bytes.Buffer
			if code := Run(args, &out, &errOut); code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "flag_invalid") {
				t.Fatalf("global flag escaped closed parser: %v => %d %s", args, code, errOut.String())
			}
		}
	}
}

func TestE23T3PublicCapabilitiesAndNonMutatingMissingLookup(t *testing.T) {
	cfg, path := e23t3Config(t)
	var out, errOut bytes.Buffer
	code := Run([]string{"sync", "request", "capabilities", "--group", cfg.Sync.GroupID, "--config", path}, &out, &errOut)
	if code != 0 {
		t.Fatalf("capabilities: %d %s", code, errOut.String())
	}
	var e map[string]any
	if err := json.Unmarshal(out.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	e23t3ValidateCarrier(t, e)
	result := e["result"].(map[string]any)
	if len(result["available_actions"].([]any)) != 5 || result["contract_digest"] != managementContractDigest {
		t.Fatal("qualified runtime capabilities differ")
	}
	out.Reset()
	errOut.Reset()
	code = runSyncRequestQualified(context.Background(), []string{"prepare", "--group", cfg.Sync.GroupID, "--action", "sync-now", "--config", path}, false, []string{}, &out, &errOut)
	if code != 3 || !strings.Contains(errOut.String(), "sync_management_unavailable") {
		t.Fatalf("unqualified preparation: %d %s", code, errOut.String())
	}
	code, e = e23t3Invoke(t, path, "show", "--group", cfg.Sync.GroupID, "--request-id", "mgr_"+strings.Repeat("f", 32))
	if code != 4 || e["error"].(map[string]any)["code"] != "sync_request_not_found" {
		t.Fatalf("missing: %d %v", code, e)
	}
	if _, err := os.Stat(cfg.Instance.StateDir); !os.IsNotExist(err) {
		t.Fatal("inspection or unavailable prepare created state")
	}
	cfg.Sync.Management = nil
	e23t3SaveConfig(t, cfg, path)
	code, e = e23t3Invoke(t, path, "capabilities", "--group", cfg.Sync.GroupID)
	if code != 0 || e["result"].(map[string]any)["enabled"] != false {
		t.Fatal("absent management enabled authority")
	}
}

type e23t3LostWriter struct{}

func (e23t3LostWriter) Write(b []byte) (int, error) { return 0, io.ErrClosedPipe }
func TestE23T3LostResponsesAndDurableDuplicates(t *testing.T) {
	cfg, path := e23t3Config(t)
	args := []string{"prepare", "--group", cfg.Sync.GroupID, "--action", "sync-now", "--config", path}
	var errOut bytes.Buffer
	code := runSyncRequestQualified(context.Background(), args, false, []string{"sync-now"}, e23t3LostWriter{}, &errOut)
	if code != 40 {
		t.Fatalf("lost prepare: %d", code)
	}
	s, err := sqlite.Open(filepath.Join(cfg.Instance.StateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var drafts, acceptances, jobs, controls int
	for query, dst := range map[string]*int{`SELECT COUNT(*) FROM sync_management_requests`: &drafts, `SELECT COUNT(*) FROM sync_management_acceptances`: &acceptances, `SELECT COUNT(*) FROM sync_jobs`: &jobs, `SELECT COUNT(*) FROM sync_controls`: &controls} {
		if err := s.QueryRow(query).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	if drafts != 1 || acceptances != 0 || jobs != 0 || controls != 0 {
		t.Fatalf("lost prepare effects: %d %d %d %d", drafts, acceptances, jobs, controls)
	}
	var id string
	if err := s.QueryRow(`SELECT request_id FROM sync_management_requests`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	args = []string{"submit", "--group", cfg.Sync.GroupID, "--request-id", id, "--config", path}
	errOut.Reset()
	code = runSyncRequestQualified(context.Background(), args, false, []string{"sync-now"}, e23t3LostWriter{}, &errOut)
	if code != 40 {
		t.Fatalf("lost submit: %d %s", code, errOut.String())
	}
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_management_acceptances`).Scan(&acceptances); err != nil || acceptances != 1 {
		t.Fatalf("acceptance lost: %d %v", acceptances, err)
	}
	cfg.Sync.Management.Enabled = false
	e23t3SaveConfig(t, cfg, path)
	code, e := e23t3Invoke(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", id)
	if code != 0 || e["result"].(map[string]any)["request_id"] != id || e["result"].(map[string]any)["binding_current"] != false {
		t.Fatalf("disabled accepted duplicate: %d %v", code, e)
	}
	if effects := e["result"].(map[string]any)["side_effects"].([]any); len(effects) != 0 {
		t.Fatal("duplicate claimed a new effect")
	}
}

func TestE23T3ConcurrentSubmitAndExactLookup(t *testing.T) {
	cfg, path := e23t3Config(t)
	id := e23t3Prepare(t, path, cfg.Sync.GroupID, "sync-now")
	var wg sync.WaitGroup
	type response struct {
		code      int
		errorJSON string
	}
	codes := make(chan response, 8)
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			var out, errOut bytes.Buffer
			code := runSyncRequestQualified(context.Background(), []string{"submit", "--group", cfg.Sync.GroupID, "--request-id", id, "--config", path}, false, []string{"sync-now"}, &out, &errOut)
			codes <- response{code: code, errorJSON: errOut.String()}
		})
	}
	wg.Wait()
	close(codes)
	for result := range codes {
		if result.code == 0 {
			continue
		}
		var carrier map[string]any
		if err := json.Unmarshal([]byte(result.errorJSON), &carrier); err != nil {
			t.Fatalf("concurrent submit: %d %s", result.code, result.errorJSON)
		}
		e23t3ValidateCarrier(t, carrier)
		errorValue := carrier["error"].(map[string]any)
		if result.code != 10 || errorValue["code"] != "sync_request_busy" || errorValue["category"] != "transient_local" || errorValue["retryable"] != false {
			t.Fatalf("concurrent submit: %d %s", result.code, result.errorJSON)
		}
	}
	// After the concurrent writers settle, an explicit same-handle resubmit
	// resolves the allowed transient refusal without creating another action.
	if code, value := e23t3Invoke(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", id); code != 0 || value["result"].(map[string]any)["request_id"] != id {
		t.Fatalf("same-handle resubmit: %d %v", code, value)
	}
	other := e23t3Prepare(t, path, cfg.Sync.GroupID, "sync-now")
	if other == id {
		t.Fatal("random handle reused")
	}
	code, _ := e23t3Invoke(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", other)
	if code != 0 {
		t.Fatal("other submit failed")
	}
	s, err := sqlite.Open(filepath.Join(cfg.Instance.StateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_management_acceptances WHERE request_id=?`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("duplicate acceptance: %d %v", n, err)
	}
	code, e := e23t3Invoke(t, path, "show", "--group", cfg.Sync.GroupID, "--request-id", id)
	if code != 0 || e["result"].(map[string]any)["request_id"] != id {
		t.Fatalf("exact lookup replaced by latest: %d %v", code, e)
	}
}

func TestE23T3ExpiredObsoleteAndBindingRefusal(t *testing.T) {
	cfg, path := e23t3Config(t)
	id := e23t3Prepare(t, path, cfg.Sync.GroupID, "sync-now")
	policy, revision, err := config.ManagementRevisions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.Open(filepath.Join(cfg.Instance.StateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	expired := "mgr_" + strings.Repeat("e", 32)
	_, _, err = s.PrepareManagementRequest(context.Background(), expired, syncmanagement.Binding{GroupID: cfg.Sync.GroupID, InstanceID: cfg.Instance.ID, StateIncarnationID: localSyncIncarnation(cfg), Action: "sync-now", PolicyRevision: policy, ConfigRevision: revision}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	code, e := e23t3Invoke(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", expired)
	if code != 4 || e["error"].(map[string]any)["code"] != "sync_request_expired" {
		t.Fatalf("expiry: %d %v", code, e)
	}
	cfg.Sync.Nodes[1].Endpoint = "https://changed.example.ts.net"
	e23t3SaveConfig(t, cfg, path)
	code, e = e23t3Invoke(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", id)
	if code != 14 || e["error"].(map[string]any)["code"] != "sync_request_binding_changed" {
		t.Fatalf("binding: %d %v", code, e)
	}
	for i := range cfg.Sync.Nodes {
		if cfg.Sync.Nodes[i].InstanceID == cfg.Instance.ID {
			cfg.Sync.Nodes[i].StateIncarnationID = "changed-state-002"
		}
	}
	e23t3SaveConfig(t, cfg, path)
	code, e = e23t3Invoke(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", id)
	if code != 4 || e["error"].(map[string]any)["code"] != "sync_request_obsolete" {
		t.Fatalf("obsolete: %d %v", code, e)
	}
	var count int
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_management_acceptances`).Scan(&count); err != nil || count != 0 {
		t.Fatal("refused drafts were accepted")
	}
	code, e = e23t3Invoke(t, path, "show", "--group", cfg.Sync.GroupID, "--request-id", id)
	if code != 0 || e["result"].(map[string]any)["binding_current"] != false {
		t.Fatal("stale exact lookup refused or restamped")
	}
}

func TestE23T3InvocationDeadlineIncludesMigration(t *testing.T) {
	cfg, path := e23t3Config(t)
	if err := os.MkdirAll(cfg.Instance.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(cfg.Instance.StateDir, StateDBName) + ".migration-lock"
	if err := os.WriteFile(lock, []byte("999999\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var out, errOut bytes.Buffer
	start := time.Now()
	code := runSyncRequestQualified(ctx, []string{"prepare", "--group", cfg.Sync.GroupID, "--action", "sync-now", "--config", path}, false, []string{"sync-now"}, &out, &errOut)
	if code != 10 || !strings.Contains(errOut.String(), "sync_request_deadline") || out.Len() != 0 {
		t.Fatalf("deadline: %d %s", code, errOut.String())
	}
	if time.Since(start) > time.Second {
		t.Fatal("CLI deadline restarted after initialization")
	}
	data, err := os.ReadFile(lock)
	if err != nil || string(data) != "999999\n" {
		t.Fatal("deadline stole another process lock")
	}
}

func TestE23T3BoundedCarrierWriter(t *testing.T) {
	var out bytes.Buffer
	if code := managementError(&out, "sync request show", "internal_unclassified", "internal", strings.Repeat("한", 200)+"\x00", 40, nil); code != 40 {
		t.Fatal(code)
	}
	var e map[string]any
	if err := json.Unmarshal(out.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	e23t3ValidateCarrier(t, e)
	if len(e["error"].(map[string]any)["message"].(string)) > 256 {
		t.Fatal("reason exceeded UTF-8 byte bound")
	}
	if !errors.Is(managementWrite(e23t3ShortWriter{}, map[string]any{"x": 1}), io.ErrShortWrite) {
		t.Fatal("short output write accepted")
	}
}

type e23t3ShortWriter struct{}

func (e23t3ShortWriter) Write(b []byte) (int, error) { return len(b) - 1, nil }

func TestE23T3ReadOnlyExpiryAndControlPreparation(t *testing.T) {
	cfg, path := e23t3Config(t)
	id := e23t3Prepare(t, path, cfg.Sync.GroupID, "pause")
	code, e := e23t3Invoke(t, path, "show", "--group", cfg.Sync.GroupID, "--request-id", id)
	if code != 0 || e["result"].(map[string]any)["bindings"].(map[string]any)["expected_control_revision"] != float64(1) {
		t.Fatalf("control preparation: %d %v", code, e)
	}
	s, err := sqlite.Open(filepath.Join(cfg.Instance.StateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var controls int
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_controls`).Scan(&controls); err != nil || controls != 0 {
		t.Fatal("control preparation initialized control")
	}
	policy, revision, err := config.ManagementRevisions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	expired := "mgr_" + strings.Repeat("d", 32)
	_, _, err = s.PrepareManagementRequest(context.Background(), expired, syncmanagement.Binding{GroupID: cfg.Sync.GroupID, InstanceID: cfg.Instance.ID, StateIncarnationID: localSyncIncarnation(cfg), Action: "sync-now", PolicyRevision: policy, ConfigRevision: revision}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(cfg.Instance.StateDir, StateDBName)
	beforeDB, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeWAL, err := os.ReadFile(statePath + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	code, e = e23t3Invoke(t, path, "show", "--group", cfg.Sync.GroupID, "--request-id", expired)
	if code != 0 || e["result"].(map[string]any)["state"] != "expired" {
		t.Fatalf("derived expiry: %d %v", code, e)
	}
	afterDB, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	afterWAL, err := os.ReadFile(statePath + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeDB, afterDB) || !bytes.Equal(beforeWAL, afterWAL) {
		t.Fatal("lookup mutated database/WAL bytes")
	}
	var state string
	if err := s.QueryRow(`SELECT state FROM sync_management_requests WHERE request_id=?`, expired).Scan(&state); err != nil || state != "prepared" {
		t.Fatal("lookup materialized expiry")
	}
}

func TestE23T3BlockedOutputDeadlineRetainsDraft(t *testing.T) {
	cfg, path := e23t3Config(t)
	initialized, initErr := sqlite.Open(filepath.Join(cfg.Instance.StateDir, StateDBName))
	if initErr != nil {
		t.Fatal(initErr)
	}
	if err := initialized.Migrate(filepath.Join(cfg.Instance.StateDir, "backups")); err != nil {
		initialized.Close()
		t.Fatal(err)
	}
	initialized.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if err := writer.SetWriteDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	for {
		_, err = writer.Write(make([]byte, 4096))
		if err != nil {
			break
		}
	}
	if err := writer.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var errOut bytes.Buffer
	start := time.Now()
	code := runSyncRequestQualified(ctx, []string{"prepare", "--group", cfg.Sync.GroupID, "--action", "sync-now", "--config", path}, false, []string{"sync-now"}, writer, &errOut)
	if code != 40 || time.Since(start) > time.Second || !strings.Contains(errOut.String(), "retain") {
		t.Fatalf("blocked output: %d %s", code, errOut.String())
	}
	s, err := sqlite.Open(filepath.Join(cfg.Instance.StateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_management_requests WHERE state='prepared' AND accepted_at IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("output loss did not retain exactly one inert draft: %d %v", n, err)
	}
}

func TestE23T3BlockedErrorOutputDeadline(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if err := writer.SetWriteDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := writer.Write(make([]byte, 4096)); err != nil {
			break
		}
	}
	if err := writer.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if code := runSyncRequest(ctx, nil, false, io.Discard, writer); code != 40 || time.Since(start) > time.Second {
		t.Fatalf("blocked error output exceeded deadline: %d", code)
	}
}

func TestE23T3ReportedLimitsMatchFrozenPolicy(t *testing.T) {
	raw, err := os.ReadFile("../../docs/contracts/sync-management-v1/policy.json")
	if err != nil {
		t.Fatal(err)
	}
	var policy struct{ Limits map[string]int }
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	actual := managementLimits()
	if len(actual) != len(policy.Limits) {
		t.Fatal("limit set drift")
	}
	for name, want := range policy.Limits {
		if actual[name] != want {
			t.Fatalf("%s: %d != %d", name, actual[name], want)
		}
	}
	raw, err = os.ReadFile("../../docs/contracts/sync-management-v1/bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		Digest string `json:"contract_digest"`
	}
	if err := json.Unmarshal(raw, &bundle); err != nil || bundle.Digest != managementContractDigest {
		t.Fatal("capability digest drift")
	}
}

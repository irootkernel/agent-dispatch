package cli

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	configpkg "github.com/irootkernel/agent-dispatch/internal/config"
)

func TestE20T4SyncCapabilitiesAndDisabledStatus(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"sync", "capabilities", "--output", "json"}, &out, &errOut); code != 0 {
		t.Fatalf("capabilities: %d %s", code, errOut.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	result := envelope["result"].(map[string]any)
	if result["contract_digest"] != configpkg.SyncContractDigest() {
		t.Fatalf("contract digest = %v", result["contract_digest"])
	}
	caps := result["capabilities"].(map[string]any)
	if !reflect.DeepEqual(caps, syncCapabilities()) {
		t.Fatalf("capability truth = %v", caps)
	}
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"sync", "status", "--group", "wiki-pair", "--config", "../../docs/examples/config.yaml", "--output", "json"}, &out, &errOut); code != 0 {
		t.Fatalf("status: %d %s", code, errOut.String())
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	result = envelope["result"].(map[string]any)
	cfg, err := configpkg.Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	revision, _ := configpkg.SyncRevision(cfg)
	wantStatus := map[string]any{"schema_version": "agent-dispatch.sync-status/v1", "group_id": "wiki-pair", "state": "disabled", "reason": "not_configured_or_disabled", "config_revision": revision, "import_acknowledgement_current": false, "side_effects": []any{}}
	if !reflect.DeepEqual(result, wantStatus) {
		t.Fatalf("disabled status = %v", result)
	}
}

func TestE20T4AbsentSyncPreservesOrdinaryConfigShow(t *testing.T) {
	cfg, err := configpkg.Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Sync = nil
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	if err := os.Mkdir(vault, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(vault, "unchanged.md")
	if err := os.WriteFile(marker, []byte("unchanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resource := cfg.Resources["vault-main"]
	resource.Root = vault
	cfg.Resources["vault-main"] = resource
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string]string {
		t.Helper()
		got := map[string]string{}
		if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			if entry.IsDir() {
				got[rel] = "directory"
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			got[rel] = string(body)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return got
	}
	before := snapshot()
	t.Setenv("PATH", t.TempDir())

	var out, errOut bytes.Buffer
	if code := Run([]string{"config", "show", "--config", configPath, "--output", "json"}, &out, &errOut); code != 0 {
		t.Fatalf("ordinary config show: %d %s", code, errOut.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	shown := envelope["result"].(map[string]any)
	if _, present := shown["sync"]; present {
		t.Fatalf("sync-absent configuration gained a sync block: %v", shown["sync"])
	}
	if after := snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("ordinary config show changed fixture files: before=%v after=%v", before, after)
	}
}

func TestE20T4StatusHandlesAbsentStaleAndWrongGroup(t *testing.T) {
	cfg, err := configpkg.Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	writeConfig := func(name string, value *configpkg.Config) string {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	absent := *cfg
	absent.Sync = nil
	digest := "sha256:" + strings.Repeat("0", 64)
	cfg.Sync.ImportAcknowledgement = &configpkg.SyncImportAcknowledgement{
		SchemaVersion: "agent-dispatch.sync-import-acknowledgement/v1", AcknowledgementID: "ack-stale",
		GroupID: cfg.Sync.GroupID, ResourceID: cfg.Sync.Resource, RemoteName: cfg.Sync.RemoteName, RemoteRepositoryDigest: cfg.Sync.RemoteRepositoryDigest,
		ContentRef: cfg.Sync.ContentRef, MembershipRef: cfg.Sync.MembershipRef, ScopeDigest: digest,
		LocalInstanceID: cfg.Sync.LocalInstanceID, StateIncarnationID: "stale-state-001",
		AdministratorKey: cfg.Sync.AdministratorKey, SafetyPolicyDigest: digest,
		ImportBoundsDigest: digest, ConfigRevision: digest,
	}
	for name, path := range map[string]string{"absent": writeConfig("absent.json", &absent), "stale": writeConfig("stale.json", cfg)} {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			group := "wiki-pair"
			if name == "absent" {
				group = "unconfigured-pair"
			}
			if code := Run([]string{"sync", "status", "--group", group, "--config", path, "--output", "json"}, &out, &errOut); code != 0 {
				t.Fatalf("status code=%d stderr=%s", code, errOut.String())
			}
			var envelope map[string]any
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope["result"].(map[string]any)["import_acknowledgement_current"] != false {
				t.Fatalf("status must not claim a current durable acknowledgement: %v", envelope)
			}
		})
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"sync", "status", "--group", "wrong-pair", "--config", "../../docs/examples/config.yaml", "--output", "json"}, &out, &errOut); code != 3 || !bytes.Contains(errOut.Bytes(), []byte("sync_group_not_found")) {
		t.Fatalf("wrong group: code=%d stderr=%s", code, errOut.String())
	}
}

func TestE20T4CapabilitiesMatchProviderCommandVocabulary(t *testing.T) {
	raw, err := os.ReadFile("../../docs/contracts/sync-provider-v1/commands.json")
	if err != nil {
		t.Fatal(err)
	}
	var provider struct {
		Commands []struct {
			Path         string `json:"path"`
			Capability   string `json:"capability"`
			Availability string `json:"availability"`
		} `json:"commands"`
		HelpContract struct {
			OutputModes []string `json:"output_modes"`
		} `json:"help_contract"`
	}
	if err := json.Unmarshal(raw, &provider); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{}
	for _, command := range provider.Commands {
		want[command.Capability] = command.Availability == "implemented" || command.Availability == "implemented_disabled_only"
		parts := strings.Fields(command.Path)
		if len(parts) > 1 && command.Path != "sync capabilities" && command.Path != "sync status" && !reservedSyncCommand(parts[1:]) {
			t.Fatalf("provider command is not registered as reserved: %s", command.Path)
		}
	}
	if !reflect.DeepEqual(syncCapabilities(), want) {
		t.Fatalf("CLI/provider capability drift: got=%v want=%v", syncCapabilities(), want)
	}
	if !reflect.DeepEqual(provider.HelpContract.OutputModes, []string{"json"}) {
		t.Fatalf("sync output modes drifted: %v", provider.HelpContract.OutputModes)
	}
	var out, errOut bytes.Buffer
	if code := Run([]string{"sync", "capabilities", "--output", "human"}, &out, &errOut); code != 2 || out.Len() != 0 {
		t.Fatalf("unsupported human output: code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
}

func TestE20T4ReservedSyncCommandUnavailable(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"sync", "publish", "--group", "wiki-pair"}, &out, &errOut); code != 3 {
		t.Fatalf("publish code = %d, stderr=%s", code, errOut.String())
	}
	if out.Len() != 0 || !bytes.Contains(errOut.Bytes(), []byte("sync_capability_unavailable")) {
		t.Fatalf("unexpected streams out=%q err=%q", out.String(), errOut.String())
	}
	errOut.Reset()
	if code := Run([]string{"sync", "nonsense"}, &out, &errOut); code != 2 {
		t.Fatalf("unknown sync command code = %d, stderr=%s", code, errOut.String())
	}
}

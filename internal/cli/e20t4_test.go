package cli

import (
	"bytes"
	"encoding/json"
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
		GroupID: cfg.Sync.GroupID, ResourceID: cfg.Sync.Resource, RemoteName: cfg.Sync.RemoteName,
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

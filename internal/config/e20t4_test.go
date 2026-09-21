package config

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func cloneE20T4Config(t *testing.T, cfg *Config) *Config {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var clone Config
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return &clone
}

func TestE20T4EmbeddedAcknowledgementMatchesCanonicalSchema(t *testing.T) {
	read := func(path string) map[string]any {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		return document
	}
	canonical := read("../../docs/schemas/sync-import-acknowledgement.schema.json")
	configSchema := read("../../docs/schemas/config.schema.json")
	syncSchema := configSchema["properties"].(map[string]any)["sync"].(map[string]any)
	embedded := syncSchema["properties"].(map[string]any)["cooperative_import_acknowledgement"].(map[string]any)
	canonicalProperties := canonical["properties"].(map[string]any)
	digestDefinition := canonical["$defs"].(map[string]any)["digest"]
	for _, name := range []string{"remote_repository_digest", "scope_digest", "safety_policy_digest", "import_bounds_digest", "config_revision"} {
		canonicalProperties[name] = digestDefinition
	}
	want := map[string]any{
		"type": canonical["type"], "additionalProperties": canonical["additionalProperties"],
		"required": canonical["required"], "properties": canonicalProperties,
	}
	if !reflect.DeepEqual(embedded, want) {
		t.Fatalf("embedded acknowledgement schema drifted: got=%v want=%v", embedded, want)
	}
}

func TestE20T4SyncRevisionAndAcknowledgement(t *testing.T) {
	cfg, err := Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	revision, ok := SyncRevision(cfg)
	if !ok || !strings.HasPrefix(revision, "sha256:") {
		t.Fatalf("revision = %q, %v", revision, ok)
	}
	cfg.Sync.ImportAcknowledgement = &SyncImportAcknowledgement{
		SchemaVersion: "agent-dispatch.sync-import-acknowledgement/v1", AcknowledgementID: "acknowledgement-001", GroupID: cfg.Sync.GroupID,
		ResourceID: cfg.Sync.Resource, RemoteName: cfg.Sync.RemoteName, RemoteRepositoryDigest: cfg.Sync.RemoteRepositoryDigest, ContentRef: cfg.Sync.ContentRef,
		MembershipRef: cfg.Sync.MembershipRef, ScopeDigest: SyncScopeDigest(cfg, cfg.Sync.Resource),
		LocalInstanceID: cfg.Sync.LocalInstanceID, StateIncarnationID: "workstation-main-state-001",
		AdministratorKey: cfg.Sync.AdministratorKey, SafetyPolicyDigest: SyncSafetyPolicyDigest(),
		ImportBoundsDigest: SyncImportBoundsDigest(cfg.Sync.Bounds), ConfigRevision: revision,
	}
	if !SyncAcknowledgementCurrent(cfg, "workstation-main-state-001") {
		t.Fatal("exact acknowledgement must be current")
	}
	peerRotation := cloneE20T4Config(t, cfg)
	peerRotation.Sync.Nodes[1].StateIncarnationID = "node-b-state-002"
	peerRotation.Sync.Nodes[1].PublisherKey = "SHA256:EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE"
	peerRotation.Sync.Nodes[1].Endpoint = "https://node-b-next.example.ts.net"
	if !SyncAcknowledgementCurrent(peerRotation, "workstation-main-state-001") {
		t.Fatal("peer membership movement under the same trust anchor must not stale acknowledgement")
	}
	if SyncAcknowledgementCurrent(cfg, "workstation-main-state-002") {
		t.Fatal("a changed local state incarnation must invalidate acknowledgement")
	}
	localRotation := cloneE20T4Config(t, cfg)
	localRotation.Sync.Nodes[0].StateIncarnationID = "workstation-main-state-002"
	if SyncAcknowledgementCurrent(localRotation, "workstation-main-state-002") {
		t.Fatal("a changed local state incarnation must require a new acknowledgement")
	}
	cfg.Sync.RemoteName = "backup"
	if SyncAcknowledgementCurrent(cfg, "workstation-main-state-001") {
		t.Fatal("guard input change must invalidate acknowledgement")
	}
	cfg.Sync.RemoteName = "origin"
	revision, _ = SyncRevision(cfg)
	cfg.Sync.ImportAcknowledgement.ConfigRevision = revision
	cfg.Sync.ImportAcknowledgement.RemoteName = cfg.Sync.RemoteName
	route := cfg.Routes["wiki-maintenance"]
	route.Policy.Protected = append(route.Policy.Protected, "private/**")
	cfg.Routes["wiki-maintenance"] = route
	if SyncAcknowledgementCurrent(cfg, "workstation-main-state-001") {
		t.Fatal("scope safety-policy change must invalidate acknowledgement")
	}
}

func TestE20T4SyncDigestGoldens(t *testing.T) {
	cfg, err := Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	revision, _ := SyncRevision(cfg)
	want := map[string]string{
		"contract": "sha256:30cf47b1bd854a0271aa9df3e7b37f0cc14cdd86d3f06a65a2cb787c6741131b",
		"scope":    "sha256:1e134a9296b8652b4fd887c8be2c2596caee32ac0523f058d516af8fd4958320",
		"safety":   "sha256:f93ce592981e9d78d7041f8de390c79339746fb16504390e44f85292ce7a6ea7",
		"bounds":   "sha256:d13cc49b2ff321137a8f46a8829c42d0db7591f0a8732a4c5ba2916ab4142f56",
		"config":   "sha256:4c51fc2e6f16977d433729dbe1718ed04ecbaee95901ef1d065c9076ea9f186b",
	}
	got := map[string]string{
		"contract": SyncContractDigest(), "scope": SyncScopeDigest(cfg, cfg.Sync.Resource),
		"safety": SyncSafetyPolicyDigest(), "bounds": SyncImportBoundsDigest(cfg.Sync.Bounds), "config": revision,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sync digest goldens drifted: got=%v want=%v", got, want)
	}
}

func TestE20T4RemoteRepositoryDigestContract(t *testing.T) {
	valid := []struct{ raw, canonical, digest string }{
		{"https://GitHub.com/RootKernel/wiki.git", "https://github.com/RootKernel/wiki", "sha256:9436b709b57944f5c3a29127c41a1a24149159cb81a4e48bf5d0cebc49ed06be"},
		{"ssh://git@GitHub.com:22/RootKernel/wiki/", "ssh://git@github.com/RootKernel/wiki", "sha256:aa989e912c28bec592cc816f21cdafe4efaac13cd23570c2a505028c5c4f7a4f"},
	}
	for _, tc := range valid {
		digest, canonical, err := RemoteRepositoryDigest(tc.raw)
		if err != nil || digest != tc.digest || canonical != tc.canonical {
			t.Errorf("canonicalize %q: digest=%q canonical=%q err=%v", tc.raw, digest, canonical, err)
		}
	}
	for _, raw := range []string{
		"https://alice@github.com/RootKernel/wiki", "ssh://github.com/RootKernel/wiki",
		"ssh://git@@github.com/RootKernel/wiki",
		"ssh://git:secret@github.com/RootKernel/wiki", "git@github.com:RootKernel/wiki.git",
		"https://github.com/RootKernel/../wiki", "https://github.com:8443/RootKernel/wiki",
	} {
		if _, _, err := RemoteRepositoryDigest(raw); err == nil {
			t.Errorf("unsafe remote repository URI accepted: %q", raw)
		}
	}
}

func TestE21T1EnabledSyncKeepsUnavailableEffectsClosed(t *testing.T) {
	raw, err := os.ReadFile("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "sync:\n  enabled: false", "sync:\n  enabled: true", 1))
	if _, err := Parse(raw); err != nil {
		t.Fatalf("enabled sync configuration must be valid once durable control exists: %v", err)
	}
}

func TestE20T4InvalidSyncInputsFailClosed(t *testing.T) {
	raw, err := os.ReadFile("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		old, new, want string
	}{
		"unsafe ref":               {"refs/heads/wiki-sync", "refs/heads/wiki..sync", "safe Git ref"},
		"inline secret":            {"env:SYNC_NODE_A_TO_B", "not-a-secret-reference", "does not match"},
		"unknown resource":         {"resource: vault-main", "resource: missing-vault", "is not defined"},
		"wrong local node":         {"local_instance_id: workstation-main", "local_instance_id: node-b", "must equal instance.id"},
		"public endpoint":          {"node-a.example.ts.net", "public.example.com", "Tailscale HTTPS"},
		"userinfo endpoint":        {"https://node-a.example.ts.net", "https://user:secret@node-a.example.ts.net", "must not contain userinfo"},
		"endpoint port":            {"https://node-a.example.ts.net", "https://node-a.example.ts.net:8443", "must not specify a port"},
		"short fingerprint":        {"SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "SHA256:AAAAAAAAAAAAAAAAAAAA", "does not match"},
		"noncanonical fingerprint": {"SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "SHA256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", "does not match"},
		"invalid dot ref":          {"refs/heads/wiki-sync", "refs/heads/wiki/.hidden", "safe Git ref"},
		"disabled git":             {"mode: optional", "mode: disabled", "must configure git.mode optional"},
		"reused credential":        {"env:SYNC_NODE_B_TO_A", "env:SYNC_NODE_A_TO_B", "separately provisioned"},
		"reused publisher":         {"SHA256:CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCA", "SHA256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA", "must be distinct"},
		"administrator publisher":  {"SHA256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA", "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "must be distinct"},
		"duplicate node":           {"instance_id: node-b", "instance_id: workstation-main", "is duplicated"},
		"duplicate incarnation":    {"state_incarnation_id: node-b-0001", "state_incarnation_id: workstation-main-state-001", "is duplicated"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			mutated := []byte(strings.Replace(string(raw), tc.old, tc.new, 1))
			if _, err := Parse(mutated); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestE20T4WrongSyncMemberCountFailsClosed(t *testing.T) {
	cfg, err := Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Sync.Nodes = cfg.Sync.Nodes[:1]
	errs, _ := SemanticValidate(cfg)
	if len(errs) == 0 || !strings.Contains(errs[len(errs)-1].Error(), "exactly two") {
		t.Fatalf("expected exact two-member error, got %v", errs)
	}
}

func TestE20T4SyncRevisionCoversAcknowledgementInputsAndIgnoresMembershipMovement(t *testing.T) {
	cfg, err := Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	baseline, _ := SyncRevision(cfg)
	membershipOnly := map[string]func(*Config){
		"node order":                func(c *Config) { c.Sync.Nodes[0], c.Sync.Nodes[1] = c.Sync.Nodes[1], c.Sync.Nodes[0] },
		"publisher":                 func(c *Config) { c.Sync.Nodes[1].PublisherKey = "SHA256:EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE" },
		"node identity":             func(c *Config) { c.Sync.Nodes[1].InstanceID = "node-c" },
		"state incarnation":         func(c *Config) { c.Sync.Nodes[1].StateIncarnationID = "node-c-0001" },
		"endpoint":                  func(c *Config) { c.Sync.Nodes[1].Endpoint = "https://node-c.example.ts.net" },
		"credential":                func(c *Config) { c.Sync.Nodes[1].CredentialRef = "env:SYNC_NODE_B_ROTATED" },
		"publisher signing ref":     func(c *Config) { c.Sync.PublisherSigningKeyRef = "env:SYNC_PUBLISHER_ROTATED" },
		"administrator signing ref": func(c *Config) { c.Sync.AdministratorSigningKeyRef = "env:SYNC_ADMIN_ROTATED" },
	}
	for name, mutate := range membershipOnly {
		t.Run(name, func(t *testing.T) {
			candidate := cloneE20T4Config(t, cfg)
			mutate(candidate)
			if got, _ := SyncRevision(candidate); got != baseline {
				t.Fatalf("membership-only mutation changed acknowledgement revision: %s != %s", got, baseline)
			}
		})
	}
	mutations := map[string]func(*Config){
		"group":       func(c *Config) { c.Sync.GroupID = "wiki-pair-next" },
		"resource id": func(c *Config) { c.Sync.Resource = "another-vault" },
		"resource root": func(c *Config) {
			r := c.Resources[c.Sync.Resource]
			r.Root += "-moved"
			c.Resources[c.Sync.Resource] = r
		},
		"file scope": func(c *Config) {
			r := c.Resources[c.Sync.Resource]
			r.FileScope = "changed"
			c.Resources[c.Sync.Resource] = r
		},
		"git mode": func(c *Config) {
			r := c.Resources[c.Sync.Resource]
			r.Git.Mode = "disabled"
			c.Resources[c.Sync.Resource] = r
		},
		"global identity": func(c *Config) { c.Instance.ID = "workstation-next" },
		"local identity":  func(c *Config) { c.Sync.LocalInstanceID = "node-b" },
		"content ref":     func(c *Config) { c.Sync.ContentRef = "refs/heads/wiki-next" },
		"remote repository digest": func(c *Config) {
			c.Sync.RemoteRepositoryDigest = "sha256:8888888888888888888888888888888888888888888888888888888888888888"
		},
		"membership ref":        func(c *Config) { c.Sync.MembershipRef += "-next" },
		"administrator":         func(c *Config) { c.Sync.AdministratorKey = "SHA256:DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDA" },
		"queue bound":           func(c *Config) { c.Sync.Bounds.Queue-- },
		"history bound":         func(c *Config) { c.Sync.Bounds.HistoryCommits-- },
		"subprocess time bound": func(c *Config) { c.Sync.Bounds.SubprocessSeconds-- },
		"subprocess byte bound": func(c *Config) { c.Sync.Bounds.SubprocessBytes-- },
		"include": func(c *Config) {
			r := c.Routes["wiki-maintenance"]
			r.Source.Include = append(r.Source.Include, "notes/**")
			c.Routes["wiki-maintenance"] = r
		},
		"scope": func(c *Config) {
			r := c.Routes["wiki-maintenance"]
			r.Source.Exclude = append(r.Source.Exclude, "drafts/**")
			c.Routes["wiki-maintenance"] = r
		},
		"immutable": func(c *Config) {
			r := c.Routes["wiki-maintenance"]
			r.Policy.Immutable = append(r.Policy.Immutable, "archive/**")
			c.Routes["wiki-maintenance"] = r
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := cloneE20T4Config(t, cfg)
			mutate(candidate)
			if got, _ := SyncRevision(candidate); got == baseline {
				t.Fatalf("guard mutation did not change revision: %s", got)
			}
		})
	}
}

package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/config"
)

func syncFreshnessConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Sync.Enabled = true
	return cfg
}

func syncFreshnessWriteConfig(t *testing.T, path string, cfg *config.Config) {
	t.Helper()
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("regression configuration must be valid: %v", err)
	}
}

func TestSyncFreshnessServiceBindings(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*config.Sync)
		want   string
	}{
		{"unchanged", func(*config.Sync) {}, ""},
		{"reordered nodes", func(s *config.Sync) { s.Nodes[0], s.Nodes[1] = s.Nodes[1], s.Nodes[0] }, ""},
		{"peer identity", func(s *config.Sync) { s.Nodes[1].InstanceID = "node-c" }, "configuration changed"},
		{"local incarnation", func(s *config.Sync) { s.Nodes[0].StateIncarnationID = "workstation-main-state-002" }, "configuration changed"},
		{"peer incarnation", func(s *config.Sync) { s.Nodes[1].StateIncarnationID = "node-b-0002" }, "configuration changed"},
		{"local endpoint", func(s *config.Sync) { s.Nodes[0].Endpoint = "https://node-a.example.ts.net:8448" }, "configuration changed"},
		{"peer endpoint", func(s *config.Sync) { s.Nodes[1].Endpoint = "https://node-b.example.ts.net:8448" }, "configuration changed"},
		{"local publisher key", func(s *config.Sync) { s.Nodes[0].PublisherKey = "SHA256:EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE" }, "configuration changed"},
		{"peer publisher key", func(s *config.Sync) { s.Nodes[1].PublisherKey = "SHA256:EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE" }, "configuration changed"},
		{"outbound credential", func(s *config.Sync) { s.Nodes[0].CredentialRef = "env:SYNC_NODE_A_ROTATED" }, "configuration changed"},
		{"inbound credential", func(s *config.Sync) { s.Nodes[1].CredentialRef = "env:SYNC_NODE_B_ROTATED" }, "configuration changed"},
		{"publisher signer", func(s *config.Sync) { s.PublisherSigningKeyRef = "env:SYNC_PUBLISHER_ROTATED" }, "configuration changed"},
		{"administrator signer", func(s *config.Sync) { s.AdministratorSigningKeyRef = "env:SYNC_ADMIN_ROTATED" }, "configuration changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			loaded, current := syncFreshnessConfig(t), syncFreshnessConfig(t)
			test.mutate(current.Sync)
			path := filepath.Join(t.TempDir(), "config.json")
			syncFreshnessWriteConfig(t, path, current)
			before, _ := config.SyncRevision(loaded)
			after, _ := config.SyncRevision(current)
			if before != after {
				t.Fatal("service binding movement must not broaden acknowledgement revision")
			}
			svc := &peerService{cfg: loaded, configPath: path}
			if got := svc.configurationProblem(); got != test.want {
				t.Fatalf("configurationProblem() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSyncFreshnessVerificationConfiguredPair(t *testing.T) {
	// This closed Git fixture never forwards a command to Git, SSH, or a host.
	// Stable local and remote refs isolate configuration movement from ref drift.
	bin := t.TempDir()
	const script = `#!/bin/sh
while [ "$1" = "-C" ] || [ "$1" = "-c" ]; do shift 2; done
case "$*" in
  'config -z --show-origin --name-only --get-regexp '*) exit 1;;
  'remote get-url --all origin'|'remote get-url --push --all origin')
    printf '%s\n' 'https://github.com/RootKernel/wiki.git';;
  'rev-parse --verify --end-of-options refs/heads/wiki-sync^{commit}')
    printf '%s\n' '2222222222222222222222222222222222222222';;
  'rev-parse --verify --end-of-options refs/agent-dispatch/membership/wiki-pair^{commit}')
    printf '%s\n' '1111111111111111111111111111111111111111';;
  'ls-remote --upload-pack=git-upload-pack --refs https://github.com/RootKernel/wiki.git refs/heads/wiki-sync')
    printf '%s\t%s\n' '2222222222222222222222222222222222222222' 'refs/heads/wiki-sync';;
  'ls-remote --upload-pack=git-upload-pack --refs https://github.com/RootKernel/wiki.git refs/agent-dispatch/membership/wiki-pair')
    printf '%s\t%s\n' '1111111111111111111111111111111111111111' 'refs/agent-dispatch/membership/wiki-pair';;
  *) exit 97;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	client, err := gitlocal.New(t.TempDir(), gitlocal.Limits{Timeout: 5 * time.Second, MaxOutput: 4096})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		mutate    func(*config.Config)
		changed   bool
		uncertain bool
	}{
		{"unchanged", func(*config.Config) {}, false, false},
		{"reordered nodes", func(c *config.Config) { c.Sync.Nodes[0], c.Sync.Nodes[1] = c.Sync.Nodes[1], c.Sync.Nodes[0] }, false, false},
		{"peer identity", func(c *config.Config) { c.Sync.Nodes[1].InstanceID = "node-c" }, true, false},
		{"local incarnation", func(c *config.Config) { c.Sync.Nodes[0].StateIncarnationID = "workstation-main-state-002" }, true, false},
		{"peer incarnation", func(c *config.Config) { c.Sync.Nodes[1].StateIncarnationID = "node-b-0002" }, true, false},
		{"sync removed", func(c *config.Config) { c.Sync = nil }, true, false},
		{"unreadable config", nil, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			loaded, current := syncFreshnessConfig(t), syncFreshnessConfig(t)
			path := filepath.Join(t.TempDir(), "config.json")
			t.Setenv("AGENT_DISPATCH_CONFIG", path)
			if test.mutate != nil {
				test.mutate(current)
				syncFreshnessWriteConfig(t, path, current)
				if current.Sync != nil {
					before, _ := config.SyncRevision(loaded)
					after, _ := config.SyncRevision(current)
					if before != after {
						t.Fatal("pair movement must not broaden acknowledgement revision")
					}
				}
			}
			changed, uncertain := syncVerificationTargetChanged(context.Background(), loaded, client,
				strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("2", 40))
			if changed != test.changed || uncertain != test.uncertain {
				t.Fatalf("target recheck = (%v, %v), want (%v, %v)", changed, uncertain, test.changed, test.uncertain)
			}
			result, _ := finalVerificationDisposition(changed, uncertain, true, true, true)
			want := "complete"
			if test.changed {
				want = "target_changed"
			} else if test.uncertain {
				want = "incomplete"
			}
			if result != want {
				t.Fatalf("verification result = %q, want %q", result, want)
			}
		})
	}
}

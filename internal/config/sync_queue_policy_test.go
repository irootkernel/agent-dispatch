package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSyncQueuePolicyConfig(t *testing.T, cfg *Config) string {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSyncQueueLoadBounds(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, queue := range []int{2, 1000, 1, 0, 1001} {
			t.Run(fmt.Sprintf("enabled=%t/queue=%d", enabled, queue), func(t *testing.T) {
				cfg, err := Load("../../docs/examples/config.yaml")
				if err != nil {
					t.Fatal(err)
				}
				cfg.Sync.Enabled = enabled
				cfg.Sync.Bounds.Queue = queue
				loaded, err := Load(writeSyncQueuePolicyConfig(t, cfg))
				if queue == 2 || queue == 1000 {
					if err != nil {
						t.Fatalf("queue %d must load: %v", queue, err)
					}
					if loaded.Sync.Bounds.Queue != queue || loaded.Sync.Enabled != enabled {
						t.Fatalf("Load changed the configured policy: %+v", loaded.Sync)
					}
				} else if err == nil || !strings.Contains(err.Error(), "queue") {
					t.Fatalf("queue %d must fail loading with a queue error: %v", queue, err)
				}
			})
		}
	}
}

func TestSyncQueueSemanticBounds(t *testing.T) {
	for _, queue := range []int{2, 1000, 1, 0, 1001} {
		t.Run(fmt.Sprintf("queue=%d", queue), func(t *testing.T) {
			cfg, err := Load("../../docs/examples/config.yaml")
			if err != nil {
				t.Fatal(err)
			}
			cfg.Sync.Bounds.Queue = queue
			errs, _ := SemanticValidate(cfg)
			if queue == 2 || queue == 1000 {
				if len(errs) != 0 {
					t.Fatalf("queue %d must pass semantic validation: %v", queue, errs)
				}
				return
			}
			want := fmt.Sprintf("sync.bounds.queue must be between 2 and 1000 (got %d)", queue)
			if len(errs) != 1 || errs[0].Error() != want {
				t.Fatalf("queue %d error = %v, want %q", queue, errs, want)
			}
		})
	}
}

func TestSyncQueueAcknowledgementBoundsRevision(t *testing.T) {
	for _, change := range [][2]int{{2, 3}, {3, 2}, {999, 1000}, {1000, 999}} {
		t.Run(fmt.Sprintf("%d_to_%d", change[0], change[1]), func(t *testing.T) {
			cfg, err := Load("../../docs/examples/config.yaml")
			if err != nil {
				t.Fatal(err)
			}
			cfg.Sync.Enabled = true
			cfg.Sync.Bounds.Queue = change[0]
			revision, ok := SyncRevision(cfg)
			if !ok {
				t.Fatal("sync revision is unavailable")
			}
			incarnation := "workstation-main-state-001"
			cfg.Sync.ImportAcknowledgement = &SyncImportAcknowledgement{
				SchemaVersion:     "agent-dispatch.sync-import-acknowledgement/v1",
				AcknowledgementID: "queue-policy-ack", GroupID: cfg.Sync.GroupID,
				ResourceID: cfg.Sync.Resource, RemoteName: cfg.Sync.RemoteName,
				RemoteRepositoryDigest: cfg.Sync.RemoteRepositoryDigest,
				ContentRef:             cfg.Sync.ContentRef, MembershipRef: cfg.Sync.MembershipRef,
				ScopeDigest:     SyncScopeDigest(cfg, cfg.Sync.Resource),
				LocalInstanceID: cfg.Sync.LocalInstanceID, StateIncarnationID: incarnation,
				AdministratorKey: cfg.Sync.AdministratorKey, SafetyPolicyDigest: SyncSafetyPolicyDigest(),
				ImportBoundsDigest: SyncImportBoundsDigest(cfg.Sync.Bounds), ConfigRevision: revision,
			}
			cfg, err = Load(writeSyncQueuePolicyConfig(t, cfg))
			if err != nil {
				t.Fatal(err)
			}
			if !SyncAcknowledgementCurrent(cfg, incarnation) {
				t.Fatal("acknowledgement must be current before the queue edit")
			}
			oldBounds := cfg.Sync.ImportAcknowledgement.ImportBoundsDigest
			cfg.Sync.Bounds.Queue = change[1]
			cfg, err = Load(writeSyncQueuePolicyConfig(t, cfg))
			if err != nil {
				t.Fatalf("a valid queue edit must load with a stale acknowledgement: %v", err)
			}
			newBounds := SyncImportBoundsDigest(cfg.Sync.Bounds)
			newRevision, ok := SyncRevision(cfg)
			if !ok || newBounds == oldBounds || newRevision == revision {
				t.Fatal("queue edit must change both import bounds digest and configuration revision")
			}
			if SyncAcknowledgementCurrent(cfg, incarnation) {
				t.Fatal("queue edit must invalidate the previous acknowledgement")
			}
			ack := cfg.Sync.ImportAcknowledgement
			ack.ConfigRevision = newRevision
			if SyncAcknowledgementCurrent(cfg, incarnation) {
				t.Fatal("refreshing only the revision must leave the bounds binding stale")
			}
			ack.ConfigRevision = revision
			ack.ImportBoundsDigest = newBounds
			if SyncAcknowledgementCurrent(cfg, incarnation) {
				t.Fatal("refreshing only the bounds digest must leave the revision stale")
			}
			ack.ConfigRevision = newRevision
			if !SyncAcknowledgementCurrent(cfg, incarnation) {
				t.Fatal("refreshing both bindings must restore acknowledgement currentness")
			}
		})
	}
}

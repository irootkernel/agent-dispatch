package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/doctor"
	"github.com/irootkernel/agent-dispatch/internal/config"
)

type syncHealth struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
	Count  int    `json:"count,omitempty"`
}

var syncHealthCategories = []string{"listener", "auth", "membership", "queue", "git", "import", "verification", "activation", "service"}

// syncHealthSnapshot deliberately reports only fixed reason vocabulary and
// counts. Credential values, Git output, note bodies, and subprocess text are
// never diagnostics.
func syncHealthSnapshot(ctx context.Context, cfg *config.Config, configPath string, store *sqlite.Store, control sqlite.SyncControlRow) map[string]syncHealth {
	s := cfg.Sync
	health := map[string]syncHealth{}
	socket := filepath.Join(stateDirOf(cfg), "peer-service", syncPeerSocketName)
	if conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond); err == nil {
		conn.Close()
		health["listener"] = syncHealth{State: "ready", Reason: "local_socket_accepting"}
	} else {
		health["listener"] = syncHealth{State: "unavailable", Reason: "local_socket_not_accepting"}
	}
	health["auth"] = syncHealth{State: "configured", Reason: "credential_resolution_not_probed"}
	if len(s.Nodes) != 2 || s.Nodes[0].CredentialRef == "" || s.Nodes[1].CredentialRef == "" {
		health["auth"] = syncHealth{State: "unavailable", Reason: "directional_credentials_missing"}
	}
	if control.State != "active" || control.MembershipMode != "normal" {
		health["activation"] = syncHealth{State: "blocked", Reason: "control_not_active"}
	} else if revision, _ := config.SyncRevision(cfg); control.ConfigRevision != revision {
		health["activation"] = syncHealth{State: "blocked", Reason: "control_configuration_stale"}
	} else if !config.SyncAcknowledgementCurrent(cfg, localSyncIncarnation(cfg)) {
		health["activation"] = syncHealth{State: "blocked", Reason: "import_acknowledgement_stale"}
	} else {
		health["activation"] = syncHealth{State: "ready", Reason: "current"}
	}
	client, err := membershipGitClient(cfg, s)
	if err != nil {
		health["git"] = syncHealth{State: "unavailable", Reason: "resource_unavailable"}
		health["membership"] = syncHealth{State: "unavailable", Reason: "resource_unavailable"}
	} else {
		if _, err := client.ResolveRef(ctx, s.ContentRef); err != nil {
			health["git"] = syncHealth{State: "unavailable", Reason: "content_ref_unavailable"}
		} else {
			health["git"] = syncHealth{State: "ready", Reason: "content_ref_available"}
		}
		membershipHead, err := client.ResolveRef(ctx, s.MembershipRef)
		if err != nil {
			health["membership"] = syncHealth{State: "unavailable", Reason: "membership_ref_unavailable"}
		} else if control.MembershipMode != "normal" {
			health["membership"] = syncHealth{State: "blocked", Reason: "membership_mode_blocked"}
		} else if _, err := loadConfiguredMembers(ctx, cfg, client, membershipHead); err != nil {
			health["membership"] = syncHealth{State: "unavailable", Reason: "membership_untrusted_or_stale"}
		} else {
			health["membership"] = syncHealth{State: "ready", Reason: "signed_membership_current"}
		}
	}
	queueCount := 0
	queueError := store.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_jobs
		WHERE group_id=? AND resolved_at IS NULL`, s.GroupID).Scan(&queueCount) != nil
	if backlog, err := store.LoadPeerNudgeBacklog(ctx, s.GroupID); err != nil {
		queueError = true
	} else {
		queueCount += backlog.Pending
	}
	switch {
	case queueError:
		health["queue"] = syncHealth{State: "unavailable", Reason: "queue_read_failed"}
	case queueCount > 0:
		health["queue"] = syncHealth{State: "pending", Reason: "work_pending", Count: queueCount}
	default:
		health["queue"] = syncHealth{State: "ready", Reason: "empty"}
	}
	for _, kind := range []string{"import", "verification"} {
		job, err := store.LoadLatestSyncJob(ctx, s.GroupID, kind)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			health[kind] = syncHealth{State: "unknown", Reason: "no_record"}
		case err != nil:
			health[kind] = syncHealth{State: "unavailable", Reason: "record_read_failed"}
		case job.ResolvedAt == "":
			health[kind] = syncHealth{State: "pending", Reason: "latest_unresolved"}
		default:
			health[kind] = syncHealth{State: "recorded", Reason: "latest_resolved"}
		}
	}
	health["service"] = syncServiceHealth(cfg, s.GroupID, configPath)
	return health
}

func syncServiceHealth(cfg *config.Config, group, configPath string) syncHealth {
	d, err := buildSyncServiceDefinition(group, configPath)
	if err != nil {
		return syncHealth{State: "unavailable", Reason: "definition_unavailable"}
	}
	present, matches, _, err := readSyncServiceDefinition(d)
	if err != nil {
		return syncHealth{State: "unavailable", Reason: "definition_unreadable"}
	}
	enabled := cfg.Sync != nil && cfg.Sync.GroupID == group && cfg.Sync.Enabled
	if !present || !matches || !enabled {
		return syncServicePosture(present, matches, false, enabled, nil)
	}
	loaded, loadErr := syncServiceLoadState(d)
	return syncServicePosture(present, matches, loaded, enabled, loadErr)
}

func syncServicePosture(present, matches, loaded, enabled bool, loadErr error) syncHealth {
	if present && !matches {
		return syncHealth{State: "drifted", Reason: "definition_mismatch"}
	}
	if !present {
		if !enabled {
			return syncHealth{State: "disabled", Reason: "definition_absent"}
		}
		return syncHealth{State: "unavailable", Reason: "definition_absent"}
	}
	if !enabled {
		return syncHealth{State: "blocked", Reason: "configured_group_disabled"}
	}
	if loadErr != nil {
		return syncHealth{State: "unavailable", Reason: "load_state_unknown"}
	}
	if !loaded {
		return syncHealth{State: "stopped", Reason: "service_not_loaded"}
	}
	return syncHealth{State: "ready", Reason: "managed_definition_loaded"}
}

func syncDoctorFindings(health map[string]syncHealth) []doctor.Finding {
	var findings []doctor.Finding
	for _, category := range syncHealthCategories {
		row := health[category]
		if row.State != "unavailable" && row.State != "blocked" && row.State != "drifted" && row.State != "stopped" {
			continue
		}
		findings = append(findings, doctor.Finding{
			Code:        "sync_" + category + "_" + row.Reason,
			Severity:    doctor.SeverityWarning,
			Summary:     fmt.Sprintf("sync %s: %s", category, row.Reason),
			Remediation: "inspect 'agent-dispatch sync status --group <id> --output json' and the E22 operations runbook",
		})
	}
	return findings
}

package cli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func runSync(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "sync", "sync requires a subcommand")
	}
	command := syncCommandPath(args)
	switch args[0] {
	case "capabilities":
		if !onlyJSONOutput(args[1:]) {
			return usageError(stderr, "sync capabilities", "only --output json is accepted")
		}
		return writeEnvelope(stdout, "sync capabilities", map[string]any{
			"schema_version": "agent-dispatch.sync-capabilities/v1", "contract_version": "v1",
			"contract_digest": config.SyncContractDigest(),
			"capabilities":    syncCapabilities(),
			"side_effects":    []string{},
		})
	case "status":
		group, configPath, ok := syncStatusFlags(args[1:])
		if !ok {
			return usageError(stderr, "sync status", "status requires --group GROUP and accepts --config PATH --output json")
		}
		cfg, err := config.Load(resolveConfigPath(configPath))
		if err != nil {
			return planErr(stderr, "sync status", "config_invalid", "configuration", err.Error(), 3)
		}
		if cfg.Sync != nil && group != cfg.Sync.GroupID {
			return planErr(stderr, "sync status", "sync_group_not_found", "configuration", fmt.Sprintf("sync group %q is not configured", group), 3)
		}
		revision, _ := config.SyncRevision(cfg)
		if cfg.Sync == nil || !cfg.Sync.Enabled {
			return writeEnvelope(stdout, "sync status", map[string]any{"schema_version": "agent-dispatch.sync-status/v1", "group_id": group, "state": "disabled", "reason": "not_configured_or_disabled", "config_revision": revision, "import_acknowledgement_current": false, "side_effects": []string{}})
		}
		store, concrete, code := openOperatorStore(command, configPath, stderr)
		if code != 0 {
			return code
		}
		defer concrete.Close()
		control, err := store.LoadSyncControl(requestCtx(), group)
		if errors.Is(err, sql.ErrNoRows) {
			control = sqlite.SyncControlRow{GroupID: group, Revision: 1, State: "active", Reason: "none", MembershipMode: "normal", ConfigRevision: revision}
			err = nil
		}
		if err != nil {
			return syncStoreError(stderr, command, err)
		}
		incarnation := localSyncIncarnation(cfg)
		latestPublication, err := latestSyncJobStatus(concrete, group, "publication")
		if err != nil {
			return syncStoreError(stderr, command, err)
		}
		latestDelivery, err := latestSyncJobStatus(concrete, group, "delivery")
		if err != nil {
			return syncStoreError(stderr, command, err)
		}
		latestImport, err := latestSyncJobStatus(concrete, group, "import")
		if err != nil {
			return syncStoreError(stderr, command, err)
		}
		return writeEnvelope(stdout, command, map[string]any{
			"schema_version": "agent-dispatch.sync-status/v1", "group_id": group,
			"state": control.State, "reason": control.Reason, "membership_mode": control.MembershipMode, "control_revision": control.Revision,
			"config_revision": revision, "control_config_current": control.ConfigRevision == revision,
			"import_acknowledgement_current": config.SyncAcknowledgementCurrent(cfg, incarnation),
			"latest_publication":             latestPublication,
			"latest_delivery":                latestDelivery,
			"latest_import":                  latestImport,
			"side_effects":                   []string{},
		})
	case "pause", "resume":
		group, expected, configPath, ok := syncControlFlags(args[1:])
		if !ok {
			return usageError(stderr, command, command+" requires --group GROUP and --expected-control-revision REV; accepts --config PATH --output json")
		}
		cfg, err := config.Load(resolveConfigPath(configPath))
		if err != nil {
			return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
		}
		if cfg.Sync == nil || group != cfg.Sync.GroupID {
			return planErr(stderr, command, "sync_group_not_found", "configuration", fmt.Sprintf("sync group %q is not configured", group), 3)
		}
		if !cfg.Sync.Enabled {
			return planErr(stderr, command, "sync_precondition_failed", "conflict", "sync must be enabled in configuration before control can change", 14)
		}
		revision, _ := config.SyncRevision(cfg)
		store, concrete, code := openOperatorStore(command, configPath, stderr)
		if code != 0 {
			return code
		}
		defer concrete.Close()
		current, err := store.EnsureSyncControl(requestCtx(), group, revision, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return syncStoreError(stderr, command, err)
		}
		if current.State == "blocked" && current.MembershipMode == "blocked_emergency" {
			if current.Revision != expected {
				return syncMembershipError(stderr, command, fmt.Errorf("control revision is %d, expected %d: %w", current.Revision, expected, sqlite.ErrSyncPrecondition), 14)
			}
			writeEnvelope(stdout, command, map[string]any{
				"schema_version": "agent-dispatch.sync-control/v1", "group_id": current.GroupID,
				"revision": current.Revision, "state": current.State, "reason": current.Reason, "membership_mode": current.MembershipMode,
			})
			return 30
		}
		target := "paused"
		if args[0] == "resume" {
			target = "active"
		}
		control, err := store.SetSyncControl(requestCtx(), group, expected, target, revision, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return syncStoreError(stderr, command, err)
		}
		resultCode := writeEnvelope(stdout, command, map[string]any{
			"schema_version": "agent-dispatch.sync-control/v1", "group_id": control.GroupID,
			"revision": control.Revision, "state": control.State, "reason": control.Reason, "membership_mode": control.MembershipMode,
		})
		if control.State == "blocked" && control.MembershipMode == "blocked_emergency" {
			return 30
		}
		return resultCode
	case "membership":
		return runSyncMembership(args[1:], stdout, stderr)
	case "publish":
		return runSyncPublish(args[1:], stdout, stderr)
	case "checkpoint":
		return runSyncCheckpoint(args[1:], stdout, stderr)
	case "reconcile":
		return runSyncReconcile(args[1:], stdout, stderr)
	default:
		if reservedSyncCommand(args) {
			writeErrorWithResult(stderr, command, "sync_capability_unavailable", "configuration", "the requested sync capability is reserved but unavailable in this build", map[string]any{"side_effects": []string{}})
			return 3
		}
		return usageError(stderr, "sync", "unknown or incomplete sync command")
	}
}

type syncStatusEvidence struct {
	Reason     string `json:"reason"`
	Detail     string `json:"detail"`
	Candidate  string `json:"candidate"`
	Remote     string `json:"remote"`
	PushState  string `json:"push_state"`
	Resolution string `json:"resolution"`
	ResolverID string `json:"resolver_id"`
}

// latestSyncJobStatus keeps publication, delivery, and import outcomes
// separate. A malformed retained payload must not hide the durable job head;
// the common fields remain visible and only the optional typed details drop.
func latestSyncJobStatus(store *sqlite.Store, groupID, kind string) (map[string]any, error) {
	row, err := store.LoadLatestSyncJob(requestCtx(), groupID, kind)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"present": false}, nil
	}
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"present":     true,
		"job_id":      row.JobID,
		"logical_key": row.LogicalKey,
		"state":       row.State,
		"updated_at":  row.UpdatedAt,
		"resolved":    row.ResolvedAt != "",
		"fence":       row.Fence,
		"claimed":     row.ClaimOwner != "",
	}
	if row.ResolvedAt != "" {
		result["resolved_at"] = row.ResolvedAt
	}
	var payload map[string]any
	if json.Unmarshal([]byte(row.PayloadJSON), &payload) == nil {
		for _, key := range []string{"publication_id", "import_id", "target_commit", "receiver"} {
			if value, ok := payload[key].(string); ok && value != "" {
				result[key] = value
			}
		}
	}
	journals, journalErr := store.LoadSyncJournals(requestCtx(), row.JobID)
	if journalErr == nil {
		for i := len(journals) - 1; i >= 0; i-- {
			var evidence syncStatusEvidence
			if json.Unmarshal([]byte(journals[i].EvidenceJSON), &evidence) != nil {
				continue
			}
			copySyncStatusField(result, "reason", evidence.Reason)
			copySyncStatusField(result, "detail", evidence.Detail)
			copySyncStatusField(result, "candidate", evidence.Candidate)
			copySyncStatusField(result, "remote", evidence.Remote)
			copySyncStatusField(result, "push_state", evidence.PushState)
			copySyncStatusField(result, "resolution", evidence.Resolution)
			copySyncStatusField(result, "resolver_id", evidence.ResolverID)
		}
	}
	if kind != "import" {
		return result, nil
	}
	record, decodeErr := syncrecords.DecodeImport([]byte(row.PayloadJSON))
	if decodeErr != nil {
		return result, nil
	}
	paths := make([]string, 0, len(record.Paths))
	for _, effect := range record.Paths {
		paths = append(paths, effect.Path)
	}
	reason := record.Reason
	switch row.State {
	case "recovering", "uncertain":
		reason = "partial_effect"
	case "deferred":
		if reason == "none" {
			if journalReason, ok := result["reason"].(string); ok && journalReason != "" {
				reason = journalReason
			}
		}
	default:
		if row.State != "applied" && reason == "none" {
			if journalReason, ok := result["reason"].(string); ok && journalReason != "" {
				reason = journalReason
			}
		}
	}
	result["reason"] = reason
	result["target_paths"] = paths
	return result, nil
}

func copySyncStatusField(result map[string]any, key, value string) {
	if _, exists := result[key]; !exists && value != "" {
		result[key] = value
	}
}

func syncCommandPath(args []string) string {
	if len(args) >= 2 && (args[0] == "membership" || args[0] == "checkpoint" || args[0] == "service") {
		return "sync " + args[0] + " " + args[1]
	}
	return "sync " + args[0]
}

func syncCapabilities() map[string]any {
	return map[string]any{
		"contract_read": true, "status_read": true,
		"publication": true, "reconciliation": true, "pair_verification": false,
		"peer_service": false, "control": true, "membership_plan": true,
		"membership_apply": true, "checkpoint_plan": true, "checkpoint_apply": true,
		"service_render": false, "service_install": false, "service_inspect": false,
		"service_stop": false, "service_disable": false, "service_uninstall": false,
	}
}

type syncPushDisposition uint8

const (
	syncPushConfirmed syncPushDisposition = iota
	syncPushRetryable
	syncPushConflict
	syncPushUnknown
)

func classifySyncPush(state gitlocal.PushState, remote, predecessor, candidate string) syncPushDisposition {
	if state == gitlocal.PushConfirmed || remote == candidate {
		return syncPushConfirmed
	}
	if state == gitlocal.PushNotStarted {
		return syncPushRetryable
	}
	if state == gitlocal.PushRejected {
		if remote == predecessor {
			return syncPushRetryable
		}
		return syncPushConflict
	}
	return syncPushUnknown
}

func syncRetryableError(stderr io.Writer, command string, err error) int {
	writeError(stderr, command, "sync_retryable", "transient_local", err.Error())
	return 10
}

func finishClaimedSyncTrustFailure(stderr io.Writer, command string, store *sqlite.Store, job sqlite.SyncJobRow, owner, state, phase string, cause error, evidence map[string]any) int {
	if evidence == nil {
		evidence = map[string]any{}
	}
	evidence["reason"] = cause.Error()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, state, sqlite.SyncJobKeepUnresolved, sqlite.SyncJournalEntry{
		JournalID: randomSyncID(phase + "-journal"), JobID: job.JobID, Fence: job.Fence, Phase: phase, Outcome: "effect_not_started", EvidenceJSON: mustJSON(evidence), RecordedAt: now,
	}, now); err != nil {
		return syncStoreError(stderr, command, err)
	}
	return syncMembershipError(stderr, command, cause, 30)
}

func reservedSyncCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "verify", "serve":
		return true
	case "service":
		return len(args) >= 2 && (args[1] == "render" || args[1] == "install" || args[1] == "inspect" || args[1] == "stop" || args[1] == "disable" || args[1] == "uninstall")
	default:
		return false
	}
}

func localSyncIncarnation(cfg *config.Config) string {
	if cfg.Sync == nil {
		return ""
	}
	for _, node := range cfg.Sync.Nodes {
		if node.InstanceID == cfg.Sync.LocalInstanceID {
			return node.StateIncarnationID
		}
	}
	return ""
}

func syncControlFlags(args []string) (group string, expected int64, configPath string, ok bool) {
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--group", "--expected-control-revision", "--config":
			flag := args[i]
			if seen[flag] || i+1 >= len(args) {
				return "", 0, "", false
			}
			seen[flag] = true
			i++
			switch flag {
			case "--group":
				group = args[i]
			case "--config":
				configPath = args[i]
			default:
				var err error
				expected, err = strconv.ParseInt(args[i], 10, 64)
				if err != nil || expected < 1 {
					return "", 0, "", false
				}
			}
		case "--output=json":
			if seen["--output"] {
				return "", 0, "", false
			}
			seen["--output"] = true
		case "--output":
			if seen["--output"] || i+1 >= len(args) || args[i+1] != "json" {
				return "", 0, "", false
			}
			seen["--output"] = true
			i++
		default:
			return "", 0, "", false
		}
	}
	return group, expected, configPath, group != "" && expected > 0
}

func syncStoreError(stderr io.Writer, command string, err error) int {
	switch {
	case errors.Is(err, sqlite.ErrSyncPrecondition), errors.Is(err, sqlite.ErrSyncAdmissionConflict), errors.Is(err, sqlite.ErrSyncControlHeld), errors.Is(err, sqlite.ErrSyncQueueFull):
		writeError(stderr, command, "sync_precondition_failed", "conflict", err.Error())
		return 14
	case errors.Is(err, sql.ErrNoRows):
		writeError(stderr, command, "sync_group_not_found", "configuration", err.Error())
		return 3
	default:
		writeError(stderr, command, "sqlite_write_failed", "storage", err.Error())
		return 20
	}
}

func onlyJSONOutput(args []string) bool {
	if len(args) == 0 {
		return true
	}
	return (len(args) == 1 && args[0] == "--output=json") || (len(args) == 2 && args[0] == "--output" && args[1] == "json")
}

func syncStatusFlags(args []string) (group, configPath string, ok bool) {
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--group", "--config":
			flag := args[i]
			if seen[flag] {
				return "", "", false
			}
			seen[flag] = true
			if i+1 >= len(args) {
				return "", "", false
			}
			value := args[i+1]
			i++
			if args[i-1] == "--group" {
				group = value
			} else {
				configPath = value
			}
		case "--output=json":
			if seen["--output"] {
				return "", "", false
			}
			seen["--output"] = true
		case "--output":
			if seen["--output"] {
				return "", "", false
			}
			seen["--output"] = true
			if i+1 >= len(args) || args[i+1] != "json" {
				return "", "", false
			}
			i++
		default:
			return "", "", false
		}
	}
	return group, configPath, group != ""
}

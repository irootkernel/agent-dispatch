package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
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
			control = sqlite.SyncControlRow{GroupID: group, Revision: 1, State: "active", Reason: "none", ConfigRevision: revision}
			err = nil
		}
		if err != nil {
			return syncStoreError(stderr, command, err)
		}
		incarnation := localSyncIncarnation(cfg)
		return writeEnvelope(stdout, command, map[string]any{
			"schema_version": "agent-dispatch.sync-status/v1", "group_id": group,
			"state": control.State, "reason": control.Reason, "control_revision": control.Revision,
			"config_revision": revision, "control_config_current": control.ConfigRevision == revision,
			"import_acknowledgement_current": config.SyncAcknowledgementCurrent(cfg, incarnation),
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
		if _, err := store.EnsureSyncControl(requestCtx(), group, revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return syncStoreError(stderr, command, err)
		}
		target := "paused"
		if args[0] == "resume" {
			target = "active"
		}
		control, err := store.SetSyncControl(requestCtx(), group, expected, target, revision, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return syncStoreError(stderr, command, err)
		}
		return writeEnvelope(stdout, command, map[string]any{
			"schema_version": "agent-dispatch.sync-control/v1", "group_id": control.GroupID,
			"revision": control.Revision, "state": control.State, "reason": control.Reason,
		})
	default:
		if reservedSyncCommand(args) {
			writeErrorWithResult(stderr, command, "sync_capability_unavailable", "configuration", "the requested sync capability is reserved but unavailable in this build", map[string]any{"side_effects": []string{}})
			return 3
		}
		return usageError(stderr, "sync", "unknown or incomplete sync command")
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
		"publication": false, "reconciliation": false, "pair_verification": false,
		"peer_service": false, "control": true, "membership_plan": false,
		"membership_apply": false, "checkpoint_plan": false, "checkpoint_apply": false,
		"service_render": false, "service_install": false, "service_inspect": false,
		"service_stop": false, "service_disable": false, "service_uninstall": false,
	}
}

func reservedSyncCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "publish", "reconcile", "verify", "serve":
		return true
	case "membership", "checkpoint":
		return len(args) >= 2 && (args[1] == "plan" || args[1] == "apply")
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
	case errors.Is(err, sqlite.ErrSyncPrecondition), errors.Is(err, sqlite.ErrSyncAdmissionConflict), errors.Is(err, sqlite.ErrSyncControlHeld):
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

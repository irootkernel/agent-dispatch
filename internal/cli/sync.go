package cli

import (
	"fmt"
	"io"

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
		return writeEnvelope(stdout, "sync status", map[string]any{"schema_version": "agent-dispatch.sync-status/v1", "group_id": group, "state": "disabled", "reason": "not_configured_or_disabled", "config_revision": revision, "import_acknowledgement_current": false, "side_effects": []string{}})
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
		"peer_service": false, "control": false, "membership_plan": false,
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
	case "publish", "reconcile", "verify", "serve", "pause", "resume":
		return true
	case "membership", "checkpoint":
		return len(args) >= 2 && (args[1] == "plan" || args[1] == "apply")
	case "service":
		return len(args) >= 2 && (args[1] == "render" || args[1] == "install" || args[1] == "inspect" || args[1] == "stop" || args[1] == "disable" || args[1] == "uninstall")
	default:
		return false
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

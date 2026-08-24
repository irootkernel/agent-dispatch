package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/app/maintenance"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/observability"
)

// globalLogLevel is the process log level set from the global
// --log-level flag before command dispatch (cli-spec §1). The default
// warn keeps successful one-shot commands quiet on stderr — the
// operational log is diagnostic output (CLI-002), and normal
// transitions are already durable in SQLite.
var globalLogLevel = observability.LevelWarn

// globalTraceID is the process trace id set from the global --trace-id
// flag; it flows into log correlation.
var globalTraceID string

// opsFlags is the parsed flag set of one operational command.
type opsFlags struct {
	values map[string]string
}

func (f *opsFlags) val(name string) string { return f.values[name] }

// parseOpsFlags parses the status/doctor/maintenance flag vocabulary:
// value flags declared per command, the shared boolean flags (each
// accepting its --name=true equals form), and --output json.
func parseOpsFlags(command string, args []string, stderr io.Writer, valueFlags map[string]bool) *opsFlags {
	boolFlags := map[string]bool{"--probe-targets": true, "--dry-run": true, "--yes": true, "--full": true}
	out := &opsFlags{values: map[string]string{}}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value := arg, ""
		hasValue := false
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			name, value, hasValue = arg[:eq], arg[eq+1:], true
		}
		switch {
		case valueFlags[name]:
			if !hasValue {
				if i+1 >= len(args) {
					usageError(stderr, command, fmt.Sprintf("%s requires a value", name))
					return nil
				}
				i++
				value = args[i]
			}
			out.values[name] = value
		case boolFlags[name]:
			if hasValue && value != "true" && value != "false" {
				usageError(stderr, command, fmt.Sprintf("%s accepts true or false", name))
				return nil
			}
			out.values[name] = map[bool]string{true: "true", false: "false"}[value != "false" || !hasValue]
			if hasValue {
				out.values[name] = value
			}
		case name == "--output":
			if !hasValue {
				if i+1 >= len(args) || args[i+1] != "json" {
					usageError(stderr, command, "--output requires 'json'")
					return nil
				}
				i++
				value = "json"
			}
			if value != "json" {
				usageError(stderr, command, "--output requires 'json'")
				return nil
			}
			out.values["--output"] = "json"
		default:
			usageError(stderr, command, fmt.Sprintf("unknown argument %q", arg))
			return nil
		}
	}
	return out
}

// runMaintenance implements the maintenance command tree (cli-spec
// §11, CLI-007): prune is dry-run by default and vacuum requires
// --yes; nothing here reads Watchman input.
func runMaintenance(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "maintenance", "maintenance requires a subcommand: prune, vacuum, integrity, or backup")
	}
	command := "maintenance " + args[0]
	switch args[0] {
	case "prune":
		return runMaintenancePrune(command, args[1:], stdout, stderr)
	case "vacuum":
		return runMaintenanceVacuum(command, args[1:], stdout, stderr)
	case "integrity":
		return runMaintenanceIntegrity(command, args[1:], stdout, stderr)
	case "backup":
		return runMaintenanceBackup(command, args[1:], stdout, stderr)
	default:
		return usageError(stderr, "maintenance", fmt.Sprintf("unknown maintenance subcommand %q", args[0]))
	}
}

// runMaintenanceBackup writes a verified owner-only backup of the
// durable store (runbook §8: the built-in backup path; VACUUM INTO
// plus a quick check — never a bare copy of a live WAL database).
func runMaintenanceBackup(command string, args []string, stdout, stderr io.Writer) int {
	flags := parseOpsFlags(command, args, stderr, map[string]bool{"--config": true, "--output": true})
	if flags == nil {
		return 2
	}
	output := flags.val("--output")
	if output == "" {
		return usageError(stderr, command, "backup requires --output <path> for the backup file")
	}
	// Lstat so a symlink at the target — dangling or not — is itself a
	// conflict: the snapshot must never land through a link the backup
	// path did not create.
	if _, err := os.Lstat(output); err == nil {
		return planErr(stderr, command, "backup_target_exists", "conflict",
			"backup "+output+" already exists; the backup path is never overwritten (runbook s8) — choose a new path", 14)
	}
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	if err := store.Backup(output); err != nil {
		// A failed snapshot leaves nothing behind: a partial file would
		// turn the operator's retry into a spurious conflict.
		_ = os.Remove(output)
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	opsLogger(stderr, cfg).Info(observability.EventMaintenanceBackedUp, observability.Correlation{TraceID: globalTraceID}, "database backup written", map[string]any{"path": output})
	return writeEnvelope(stdout, command, map[string]any{"backup_path": output})
}

// watchmanContextRefusal enforces CLI-007 (E9-T2/M-21): destructive
// maintenance must never run from Watchman input. The managed trigger
// argv is fixed to the dispatch path, so the env vars appearing means
// an operator pasted a maintenance command into a trigger definition —
// refuse at exit 2 before any store is opened.
func watchmanContextRefusal(stderr io.Writer, command string) int {
	if os.Getenv("WATCHMAN_TRIGGER") != "" || os.Getenv("WATCHMAN_ROOT") != "" {
		return usageError(stderr, command,
			"maintenance commands must not run from a Watchman trigger context (WATCHMAN_TRIGGER/WATCHMAN_ROOT are set); run maintenance from an operator shell")
	}
	return 0
}

// runMaintenancePrune plans or executes the retention prune
// (OPS-003/004, retention-and-privacy §3): dry-run by default, --yes
// executes, --before <duration> only narrows the horizons.
func runMaintenancePrune(command string, args []string, stdout, stderr io.Writer) int {
	flags := parseOpsFlags(command, args, stderr, map[string]bool{"--config": true, "--before": true, "--reason": true})
	if flags == nil {
		return 2
	}
	if code := watchmanContextRefusal(stderr, command); code != 0 {
		return code
	}
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	policy, err := maintenance.EffectivePolicy(cfg.Retention)
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	if before := flags.val("--before"); before != "" {
		d, err := config.ParseDuration(before)
		if err != nil {
			return planErr(stderr, command, "config_invalid", "configuration", "before: "+err.Error(), 3)
		}
		policy = policy.ClampBefore(time.Duration(d.Nanos))
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	ctx := requestCtx()
	now := time.Now().UTC()
	cutoffs := sqlite.PruneCutoffs{
		Observations:       dispatch.Timestamp(now.Add(-policy.Observations)),
		Attempts:           dispatch.Timestamp(now.Add(-policy.Attempts)),
		CompletedReceipts:  dispatch.Timestamp(now.Add(-policy.CompletedReceipts)),
		ResolvedQuarantine: dispatch.Timestamp(now.Add(-policy.ResolvedQuarantine)),
	}
	plan, err := store.PlanPrune(ctx, cutoffs)
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	log := opsLogger(stderr, cfg)
	corr := observability.Correlation{TraceID: globalTraceID}
	if flags.val("--dry-run") == "true" && flags.val("--yes") == "true" {
		return usageError(stderr, command, "prune cannot combine --dry-run with --yes; choose one (E8-T4, L-9)")
	}
	if flags.val("--yes") != "true" {
		return writeEnvelope(stdout, command, map[string]any{
			"dry_run": true,
			"policy":  policyMap(policy),
			"plan":    plan,
		})
	}
	counts, err := store.ExecutePrune(ctx, cutoffs, "operator", flags.val("--reason"), dispatch.Timestamp(now))
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	log.Info(observability.EventMaintenancePruned, corr, "retention prune executed", map[string]any{
		"policy": policyMap(policy), "counts": counts,
	})
	return writeEnvelope(stdout, command, map[string]any{
		"dry_run": false,
		"policy":  policyMap(policy),
		"counts":  counts,
	})
}

// runMaintenanceVacuum reclaims database pages after refusing any
// active work (cli-spec §11).
func runMaintenanceVacuum(command string, args []string, stdout, stderr io.Writer) int {
	flags := parseOpsFlags(command, args, stderr, map[string]bool{"--config": true})
	if flags == nil {
		return 2
	}
	if code := watchmanContextRefusal(stderr, command); code != 0 {
		return code
	}
	if flags.val("--dry-run") == "true" {
		return usageError(stderr, command, "vacuum has no dry-run mode; it requires --yes (CLI-007)")
	}
	if flags.val("--yes") != "true" {
		return usageError(stderr, command, "vacuum is destructive maintenance and requires --yes (CLI-007)")
	}
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	ctx := requestCtx()
	active, err := store.HasActiveWork(ctx, dispatch.Timestamp(time.Now()))
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	if active {
		return planErr(stderr, command, "maintenance_active_work", "conflict",
			"vacuum refuses while dispatch intents are submitting or hold unexpired attempt leases; drain or recover them first (cli-spec §11)", 14)
	}
	if err := store.Vacuum(ctx); err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	opsLogger(stderr, cfg).Info(observability.EventMaintenanceVacuumed, observability.Correlation{TraceID: globalTraceID}, "database vacuumed", nil)
	return writeEnvelope(stdout, command, map[string]any{"vacuumed": true})
}

// runMaintenanceIntegrity reports the database integrity check
// (OPS-005 posture; --full switches quick_check to integrity_check).
func runMaintenanceIntegrity(command string, args []string, stdout, stderr io.Writer) int {
	flags := parseOpsFlags(command, args, stderr, map[string]bool{"--config": true})
	if flags == nil {
		return 2
	}
	store, closer, exit := openOperatorStore(command, flags.val("--config"), stderr)
	if exit != 0 {
		return exit
	}
	defer closer.Close()
	full := flags.val("--full") == "true"
	if err := store.IntegrityCheck(full); err != nil {
		return planErr(stderr, command, "sqlite_integrity_failed", "storage", err.Error(), 20)
	}
	version, err := store.SchemaVersion()
	if err != nil {
		return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
	}
	return writeEnvelope(stdout, command, map[string]any{
		"integrity": "ok", "mode": map[bool]string{true: "integrity_check", false: "quick_check"}[full],
		"schema_version": version, "latest_version": store.LatestSchemaVersion(),
	})
}

func policyMap(p maintenance.Policy) map[string]any {
	return map[string]any{
		"observations":        p.Observations.String(),
		"attempts":            p.Attempts.String(),
		"completed_receipts":  p.CompletedReceipts.String(),
		"resolved_quarantine": p.ResolvedQuarantine.String(),
		"unresolved":          "forever (retained until resolution)",
	}
}

// completionCommands derives the completion vocabulary from the
// registered command tree (cli-spec §2), so the two cannot drift.
func completionCommands() []string {
	names := make([]string, 0, len(knownCommands))
	for name := range knownCommands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// runCompletion implements `agent-dispatch completion <bash|zsh>` (E6-T3
// deliverable): one static completion script for the fixed v0.1
// command tree, written to stdout.
func runCompletion(args []string, stdout, stderr io.Writer) int {
	command := "completion"
	if len(args) != 1 || (args[0] != "bash" && args[0] != "zsh") {
		return usageError(stderr, command, "completion requires exactly one shell: bash or zsh")
	}
	words := strings.Join(completionCommands(), " ")
	if args[0] == "bash" {
		fmt.Fprintf(stdout, "_agent-dispatch()\n{\n  local cur\n  cur=${COMP_WORDS[COMP_CWORD]}\n  COMPREPLY=( $(compgen -W \"%s\" -- \"$cur\") )\n}\ncomplete -F _agent-dispatch agent-dispatch\n", words)
		return 0
	}
	fmt.Fprintf(stdout, "#compdef agent-dispatch\n_agent-dispatch() {\n  _arguments '1:command:(%s)'\n}\n_agent-dispatch \"$@\"\n", words)
	return 0
}

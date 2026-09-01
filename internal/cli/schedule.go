// Managed launchd schedule lifecycle of E16-T4 (ADR-0022, v0.1.6 §4,
// CLI-009/CLI-018, OPS-017/OPS-018, SEC-007): `schedule
// render|install|inspect|disable|uninstall --route <id> --platform
// launchd` resolves the actual binary and configuration paths, derives
// the managed label and plist path from the instance ID, route ID, and
// a digest of the configuration absolute path, and invokes one direct
// internal `schedule run` command — never a shell chain. Install is
// idempotent for an identical definition and refuses a different one;
// disable unloads while preserving the plist; uninstall unloads and
// removes only that exact managed plist. After-command recovery runs
// every fifteen minutes; scheduled mode runs daily at 03:00 local time
// by default (`--at HH:MM` overrides).
package cli

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// scheduleLogMaxBytes is the 10 MiB rotation bound; scheduleLogKeep is
// the three-file retention (v0.1.6 §4).
const (
	scheduleLogMaxBytes         = 10 * 1024 * 1024
	scheduleLogKeep             = 3
	afterCommandRecoverySeconds = 900
)

// launchctl invocations are injectable so tests exercise the managed
// lifecycle without a live launchd session.
var launchctlRun = func(args ...string) (string, error) {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	return string(out), err
}

// scheduleLabel derives the managed label from the instance ID, route
// ID, and the digest of the configuration's absolute path (v0.1.6 §4):
// distinct configurations of one route never share a schedule.
func scheduleLabel(instanceID, routeID, absConfigPath string) string {
	sum := sha256.Sum256([]byte(absConfigPath))
	return fmt.Sprintf("xyz.rootkernel.agent-dispatch.%s.%s.%s", instanceID, routeID, hex.EncodeToString(sum[:])[:12])
}

// launchAgentsDir resolves the per-user launchd agent directory; it is
// injectable so the lifecycle tests never touch the real location
// (round-1 F004).
var launchAgentsDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".LaunchAgents"
	}
	return filepath.Join(home, "Library", "LaunchAgents")
}

// schedulePlistPath resolves the managed plist's location.
func schedulePlistPath(label string) string {
	return filepath.Join(launchAgentsDir(), label+".plist")
}

// scheduleDefinition is the rendered managed definition: the launchd
// property list text plus its canonical digest, the resolved binary and
// configuration paths, and the mode-specific timing.
type scheduleDefinition struct {
	RouteID    string
	Label      string
	PlistPath  string
	BinaryPath string
	ConfigPath string
	Mode       string // after-command | scheduled
	Plist      string
	Digest     string
	StdoutPath string
	StderrPath string
	Calendar   string // HH:MM for scheduled mode; empty for interval mode
	Interval   int    // seconds; 0 for calendar mode
}

// renderSchedulePlist renders the launchd definition. The internal
// runner is invoked directly (no shell): the two-key gate and the
// drain's own bounded behavior are the safety boundary (SEC-007).
func renderSchedulePlist(d scheduleDefinition) string {
	startKey := "StartInterval"
	var startXML string
	if d.Interval > 0 {
		startXML = fmt.Sprintf("    <key>%s</key>\n    <integer>%d</integer>\n", startKey, d.Interval)
	} else {
		hour, minute := 3, 0
		if parts := strings.SplitN(d.Calendar, ":", 2); len(parts) == 2 {
			if h, err := strconv.Atoi(parts[0]); err == nil && h >= 0 && h <= 23 {
				hour = h
			}
			if m, err := strconv.Atoi(parts[1]); err == nil && m >= 0 && m <= 59 {
				minute = m
			}
		}
		startXML = fmt.Sprintf("    <key>StartCalendarInterval</key>\n    <dict>\n        <key>Hour</key>\n        <integer>%d</integer>\n        <key>Minute</key>\n        <integer>%d</integer>\n    </dict>\n", hour, minute)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- agent-dispatch managed schedule; safe to remove only through 'schedule uninstall' -->
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>schedule</string>
        <string>run</string>
        <string>--route</string>
        <string>%s</string>
        <string>--config</string>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <false/>
%s    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, d.Label, d.BinaryPath, d.RouteID, d.ConfigPath, startXML, d.StdoutPath, d.StderrPath)
}

// buildScheduleDefinition resolves the managed definition for one route
// from the current executable, the absolute configuration path, and the
// route's effective drain mode, mapping failures to the CLI's error
// classes.
func buildScheduleDefinition(command string, configPath, routeID, at string, stderr io.Writer) (scheduleDefinition, int) {
	def, err := scheduleDefinitionFor(configPath, routeID, at)
	if err != nil {
		if _, usage := err.(*scheduleUsageError); usage {
			return scheduleDefinition{}, usageError(stderr, command, err.Error())
		}
		return scheduleDefinition{}, planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	return def, 0
}

// scheduleUsageError marks a route-declaration usage failure.
type scheduleUsageError struct{ msg string }

func (e *scheduleUsageError) Error() string { return e.msg }

// scheduleDefinitionFor is the error-returning definition core the
// lifecycle surfaces and the posture projection share: the posture's
// definition matching compares the installed bytes against exactly this
// render (round-1 F003/F005).
func scheduleDefinitionFor(configPath, routeID, at string) (scheduleDefinition, error) {
	cfg, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		return scheduleDefinition{}, err
	}
	route, ok := cfg.Routes[routeID]
	if !ok {
		return scheduleDefinition{}, &scheduleUsageError{msg: fmt.Sprintf("route %q is not declared in the configuration", routeID)}
	}
	policy, err := config.EffectiveNotificationDrain(route.Notifications)
	if err != nil {
		return scheduleDefinition{}, err
	}
	absConfig, err := filepath.Abs(resolveConfigPath(configPath))
	if err != nil {
		return scheduleDefinition{}, err
	}
	binaryPath, err := os.Executable()
	if err != nil {
		return scheduleDefinition{}, err
	}
	binaryPath, _ = filepath.EvalSymlinks(binaryPath)
	label := scheduleLabel(cfg.Instance.ID, routeID, absConfig)
	plistPath := schedulePlistPath(label)
	logDir := filepath.Join(stateDirOf(cfg), "logs")
	d := scheduleDefinition{
		Label:      label,
		PlistPath:  plistPath,
		BinaryPath: binaryPath,
		ConfigPath: absConfig,
		Mode:       policy.Mode,
		Calendar:   at,
		StdoutPath: filepath.Join(logDir, "schedule-"+routeID+".out.log"),
		StderrPath: filepath.Join(logDir, "schedule-"+routeID+".err.log"),
	}
	if policy.Mode == config.DrainModeAfterCommand {
		d.Interval = afterCommandRecoverySeconds
	}
	d.RouteID = routeID
	d.Plist = renderSchedulePlist(d)
	sum := sha256.Sum256([]byte(d.Plist))
	d.Digest = "sha256:" + hex.EncodeToString(sum[:])
	return d, nil
}

// stateDirOf resolves the configuration's state directory through the
// shared precedence (the explicit override, the configured value, then
// the platform default).
func stateDirOf(cfg *config.Config) string {
	return resolveStateDirOverride(cfg.Instance.StateDir)
}

// runSchedule implements the `schedule` group.
func runSchedule(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "schedule", "schedule requires a subcommand: render, install, inspect, disable, uninstall, or run")
	}
	sub, rest := args[0], args[1:]
	command := "schedule " + sub
	switch sub {
	case "render", "install", "inspect", "disable", "uninstall":
		return runScheduleLifecycle(command, sub, rest, stdout, stderr)
	case "run":
		return runScheduleRun(command, rest, stdout, stderr)
	default:
		return usageError(stderr, "schedule", fmt.Sprintf("unknown schedule subcommand %q", sub))
	}
}

// runScheduleLifecycle handles the managed-definition surfaces. Only
// --platform launchd exists in v0.1.6.
func runScheduleLifecycle(command, sub string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, map[string]bool{"--config": true, "--route": true, "--platform": true, "--at": true})
	if code != 0 {
		return code
	}
	routeID := flags.val("--route")
	if routeID == "" {
		return usageError(stderr, command, command+" requires --route <id>")
	}
	if platform := flags.val("--platform"); platform != "launchd" {
		return usageError(stderr, command, "--platform must be launchd (the only supported v0.1.6 platform)")
	}
	// A malformed --at is a usage defect, never a silently coerced
	// default (round-1 F008).
	if at := flags.val("--at"); at != "" && !scheduleAtPattern.MatchString(at) {
		return usageError(stderr, command, fmt.Sprintf("--at %q must be HH:MM with hours 00-23 and minutes 00-59", at))
	}
	def, exit := buildScheduleDefinition(command, flags.val("--config"), routeID, flags.val("--at"), stderr)
	if exit != 0 {
		return exit
	}
	switch sub {
	case "render":
		return writeEnvelope(stdout, command, map[string]any{
			"label": def.Label, "plist_path": def.PlistPath, "binary": def.BinaryPath,
			"config": def.ConfigPath, "mode": def.Mode, "digest": def.Digest, "plist": def.Plist,
		})
	case "install":
		return scheduleInstall(command, def, stdout, stderr)
	case "inspect":
		return scheduleInspect(command, def, stdout, stderr)
	case "disable":
		return scheduleDisable(command, def, stdout, stderr)
	default:
		return scheduleUninstall(command, def, stdout, stderr)
	}
}

// writePlistFile writes the definition atomically with owner-only
// permissions, refusing an existing different definition.
func writePlistFile(def scheduleDefinition) error {
	if existing, err := os.ReadFile(def.PlistPath); err == nil {
		if string(existing) == def.Plist {
			return nil // identical definition: idempotent install
		}
		return fmt.Errorf("plist %s exists with a different definition; uninstall it first", def.PlistPath)
	}
	if err := os.MkdirAll(filepath.Dir(def.PlistPath), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(def.PlistPath), ".agent-dispatch-schedule-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(def.Plist); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// The hard link can never replace an existing file (round-1 F009):
	// an occupied path is either the identical definition (idempotent)
	// or refused untouched.
	if err := os.Link(tmpName, def.PlistPath); err != nil {
		if os.IsExist(err) {
			if existing, rerr := os.ReadFile(def.PlistPath); rerr == nil && string(existing) == def.Plist {
				return nil // a racing identical install won the path
			}
			return fmt.Errorf("plist %s exists with a different definition; uninstall it first", def.PlistPath)
		}
		return err
	}
	return nil
}

// scheduleInstall writes the managed plist and bootstraps it with the
// current user's launchd session.
func scheduleInstall(command string, def scheduleDefinition, stdout, stderr io.Writer) int {
	if err := writePlistFile(def); err != nil {
		return planErr(stderr, command, "transition_invalid", "conflict", err.Error(), 14)
	}
	if out, err := launchctlRun("bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), def.PlistPath); err != nil {
		// A second bootstrap over an identical, already-loaded
		// definition is the idempotent posture launchd reports as
		// "already bootstrapped"; anything else fails loudly.
		if !strings.Contains(out, "already bootstrapped") && !strings.Contains(out, "Bootstrap failed: 5") {
			return planErr(stderr, command, "target_response_invalid", "acceptance_unknown", out, 21)
		}
	}
	return writeEnvelope(stdout, command, map[string]any{
		"label": def.Label, "plist_path": def.PlistPath, "digest": def.Digest, "mode": def.Mode, "installed": true,
	})
}

// scheduleInspect reports plist presence, loaded state, and the
// definition-digest match.
func scheduleInspect(command string, def scheduleDefinition, stdout, stderr io.Writer) int {
	installed, installedDigest, present := "", "", false
	if raw, err := os.ReadFile(def.PlistPath); err == nil {
		present = true
		installed = string(raw)
		sum := sha256.Sum256(raw)
		installedDigest = "sha256:" + hex.EncodeToString(sum[:])
	}
	loaded := false
	if out, err := launchctlRun("print", fmt.Sprintf("gui/%d", os.Getuid()), def.Label); err == nil && !strings.Contains(out, "Could not find service") {
		loaded = true
	}
	definitionMatches := present && installed == def.Plist
	return writeEnvelope(stdout, command, map[string]any{
		"label": def.Label, "plist_path": def.PlistPath, "present": present,
		"loaded": loaded, "definition_matches": definitionMatches,
		"expected_digest": def.Digest, "installed_digest": installedDigest,
		"healthy": loaded && definitionMatches,
	})
}

// scheduleDisable unloads the schedule while preserving the plist.
func scheduleDisable(command string, def scheduleDefinition, stdout, stderr io.Writer) int {
	if out, err := launchctlRun("bootout", fmt.Sprintf("gui/%d", os.Getuid()), def.Label); err != nil && !strings.Contains(out, "No such process") && !strings.Contains(out, "not booted") {
		return planErr(stderr, command, "target_response_invalid", "acceptance_unknown", out, 21)
	}
	return writeEnvelope(stdout, command, map[string]any{"label": def.Label, "disabled": true, "plist_preserved": true})
}

// scheduleUninstall unloads and removes only the exact managed plist.
func scheduleUninstall(command string, def scheduleDefinition, stdout, stderr io.Writer) int {
	if _, err := launchctlRun("bootout", fmt.Sprintf("gui/%d", os.Getuid()), def.Label); err != nil {
		// An unloaded label is the idempotent posture.
	}
	if _, err := os.Stat(def.PlistPath); err == nil {
		// Ownership is an exact identity match (round-1 F010): only the
		// byte-identical managed definition is ever removed.
		raw, rerr := os.ReadFile(def.PlistPath)
		if rerr != nil || string(raw) != def.Plist {
			return planErr(stderr, command, "transition_invalid", "conflict", def.PlistPath+" is not the exact current managed definition; refusing to remove it", 14)
		}
		if err := os.Remove(def.PlistPath); err != nil {
			return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
		}
	}
	return writeEnvelope(stdout, command, map[string]any{"label": def.Label, "uninstalled": true, "removed": def.PlistPath})
}

// runScheduleRun is the internal command the plist invokes directly. In
// after-command recovery mode it performs one due-only drain for the
// route (plus the automatic drift evaluation); in scheduled mode it
// runs the scheduled reconciliation first and drains only after a
// healthy (exit 0) pass.
func runScheduleRun(command string, args []string, stdout, stderr io.Writer) int {
	flags, code := parseDispatchesFlags(command, args, stderr, map[string]bool{"--config": true, "--route": true})
	if code != 0 {
		return code
	}
	routeID := flags.val("--route")
	if routeID == "" {
		return usageError(stderr, command, "schedule run requires --route <id>")
	}
	cfg, err := config.Load(resolveConfigPath(flags.val("--config")))
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	route, ok := cfg.Routes[routeID]
	if !ok {
		return usageError(stderr, command, fmt.Sprintf("route %q is not declared in the configuration", routeID))
	}
	policy, err := config.EffectiveNotificationDrain(route.Notifications)
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	rotateScheduleLogs(routeID, stateDirOf(cfg))
	if policy.Mode == config.DrainModeScheduled {
		// The scheduled reconciliation runs first; the drain chains only
		// after a healthy pass (v0.1.6 §4). The reconciliation's own
		// post-commit hook (E16-T3) covers the after-command routes.
		if code := Run([]string{"reconcile", "--route", routeID, "--reason", "scheduled", "--submit", "--config", flags.val("--config")}, stdout, stderr); code != 0 {
			return code
		}
	}
	// The drift evaluation is the registry's automatic surface (E16-T3
	// evidence): it rides the scheduler, never the explicit drain. The
	// runner drains its own route under EITHER automatic mode (round-1
	// F001): after-command recovery drains due work directly, and
	// scheduled mode chains the drain after the healthy reconciliation
	// above.
	store := openDrainStoreForSchedule(flags.val("--config"), stderr)
	if store == nil {
		return 0 // the deferral note already landed on stderr
	}
	autoDrainCfg(command, cfg, store, stderr, map[string]bool{
		config.DrainModeAfterCommand: true, config.DrainModeScheduled: true,
	}, routeID)
	return 0
}

// openDrainStoreForSchedule opens the operator store for the runner's
// drain; an open failure returns a nil-safe dry pass with one bounded
// note.
func openDrainStoreForSchedule(configPath string, stderr io.Writer) afterCommandStore {
	store, _, exit := openOperatorStore("schedule run", configPath, stderr)
	if exit != 0 {
		boundedAfterCommandNote(stderr, "schedule run: state store unavailable; drain deferred to the next recovery")
		return nil
	}
	return store
}

// rotateScheduleLogs rotates the schedule's log pair at 10 MiB keeping
// the latest three files (v0.1.6 §4).
func rotateScheduleLogs(routeID, stateDir string) {
	logDir := filepath.Join(stateDir, "logs")
	for _, name := range []string{"schedule-" + routeID + ".out.log", "schedule-" + routeID + ".err.log"} {
		rotateScheduleLog(filepath.Join(logDir, name))
	}
}

func rotateScheduleLog(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < scheduleLogMaxBytes {
		return
	}
	// age: path -> path.1 ... drop beyond keep.
	for i := scheduleLogKeep - 1; i >= 1; i-- {
		older := fmt.Sprintf("%s.%d", path, i)
		newer := path
		if i > 1 {
			newer = fmt.Sprintf("%s.%d", path, i-1)
		}
		if _, err := os.Stat(newer); err == nil {
			_ = os.Rename(newer, older)
		}
	}
	_ = os.Rename(path, path+".1")
}

// schedulePosture summarizes one route's scheduler posture for status
// and doctor: the expected mode, the installed/loaded/matching
// evidence, and whether the automatic schedule is overdue for
// production enablement.
func schedulePosture(cfg *config.Config, routeID, configPath string) map[string]any {
	route, ok := cfg.Routes[routeID]
	if !ok {
		return nil
	}
	policy, err := config.EffectiveNotificationDrain(route.Notifications)
	if err != nil {
		return nil
	}
	posture := map[string]any{"mode": policy.Mode, "limit": policy.Limit}
	if policy.Mode == config.DrainModeManual {
		posture["expected"] = false
		return posture
	}
	posture["expected"] = true
	abs, aerr := filepath.Abs(configPath)
	if aerr != nil {
		abs = configPath
	}
	label := scheduleLabel(cfg.Instance.ID, routeID, abs)
	def := scheduleDefinition{Label: label, PlistPath: schedulePlistPath(label)}
	present := false
	raw := []byte(nil)
	if r, err := os.ReadFile(def.PlistPath); err == nil {
		raw = r
		present = true
		sum := sha256.Sum256(raw)
		posture["installed_digest"] = "sha256:" + hex.EncodeToString(sum[:])
	}
	loaded := false
	if out, err := launchctlRun("print", fmt.Sprintf("gui/%d", os.Getuid()), label); err == nil && !strings.Contains(out, "Could not find service") {
		loaded = true
	}
	// Definition matching (round-1 F003/F005): the installed bytes must
	// equal the currently rendered managed definition, so a drifted
	// definition (a moved binary, a retargeted configuration, a mode
	// change) is unhealthy even while loaded.
	definitionMatches := false
	if present {
		if def, derr := scheduleDefinitionFor(configPath, routeID, ""); derr == nil {
			definitionMatches = string(raw) == def.Plist
			posture["expected_digest"] = def.Digest
		}
	}
	posture["installed"] = present
	posture["loaded"] = loaded
	posture["definition_matches"] = definitionMatches
	posture["overdue"] = !present || !loaded || !definitionMatches
	posture["healthy"] = present && loaded && definitionMatches
	posture["label"] = label
	return posture
}

// scheduleAtPattern is the closed HH:MM grammar of --at.
var scheduleAtPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// scheduleAtHint renders the preflight remediation's --at clause for
// scheduled mode (the 03:00 default needs no flag).
func scheduleAtHint(posture map[string]any) string {
	if posture["mode"] == config.DrainModeScheduled {
		return " --at 03:00"
	}
	return ""
}

// notificationDrainPosture projects every notification-enabled route's
// delivery and scheduler posture for the status and doctor surfaces
// (E16-T4): mode and limit, the due/backoff pending split, the oldest
// pending age against the configured warning window, the scheduler
// expectation/evidence/overdue state, repeated ambiguous/retryable
// outcomes, and unresolvable sink declarations. No notification payload
// or endpoint material ever enters the projection (v0.1.6 §4).
func notificationDrainPosture(ctx context.Context, cfg *config.Config, store drainQuerySurface, configPath string, stderr io.Writer) map[string]map[string]any {
	out := map[string]map[string]any{}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, routeID := range cfg.SortedRouteIDs() {
		route := cfg.Routes[routeID]
		if route.Notifications == nil || len(route.Notifications.Sinks) == 0 {
			continue
		}
		policy, err := config.EffectiveNotificationDrain(route.Notifications)
		if err != nil {
			continue
		}
		row := map[string]any{"mode": policy.Mode, "limit": policy.Limit}
		var due, backoff int
		if err := store.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_events WHERE route_id = ? AND state = 'pending' AND due_at != '' AND due_at <= ?`, routeID, now).Scan(&due); err == nil {
			if err := store.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_events WHERE route_id = ? AND state = 'pending' AND due_at > ?`, routeID, now).Scan(&backoff); err == nil {
				row["due"] = due
				row["backoff"] = backoff
			}
		}
		var liveClaims int
		if err := store.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_events WHERE route_id = ? AND state = 'pending' AND lease_owner != '' AND lease_expires_at > ?`, routeID, now).Scan(&liveClaims); err == nil {
			row["live_claims"] = liveClaims
		}
		var oldest string
		if err := store.QueryRowContext(ctx, `SELECT MIN(created_at) FROM notification_events WHERE route_id = ? AND state = 'pending'`, routeID).Scan(&oldest); err == nil && oldest != "" {
			row["oldest_pending_at"] = oldest
			if created, perr := time.Parse(time.RFC3339, oldest); perr == nil {
				row["oldest_pending_age"] = time.Since(created).Round(time.Second).String()
				row["pending_overdue"] = time.Since(created) > policy.PendingWarnAfter
			}
		}
		var repeated int
		if err := store.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_events e
			JOIN (SELECT notification_id, MAX(attempt_number) AS last_number FROM notification_attempts GROUP BY notification_id) m ON m.notification_id = e.notification_id
			JOIN notification_attempts last ON last.notification_id = e.notification_id AND last.attempt_number = m.last_number
			WHERE e.route_id = ? AND e.state = 'pending' AND last.outcome IN ('ambiguous','retryable')`, routeID).Scan(&repeated); err == nil {
			row["repeated_retry_outcomes"] = repeated
		}
		unresolvable := 0
		resolver := notificationSinkResolver(cfg, stderr)
		for _, sink := range route.Notifications.Sinks {
			if _, err := resolver(routeID, sinkRefOf(sink)); err != nil {
				unresolvable++
			}
		}
		row["unresolvable_sinks"] = unresolvable
		if posture := schedulePosture(cfg, routeID, configPath); posture != nil {
			row["scheduler_expected"] = posture["expected"]
			row["scheduler_installed"] = posture["installed"]
			row["scheduler_loaded"] = posture["loaded"]
			row["scheduler_overdue"] = posture["overdue"]
		}
		out[routeID] = row
	}
	return out
}

// drainQuerySurface is the read surface the posture projection needs
// (the concrete store's *sql.Row satisfies the scanner shape).
type drainQuerySurface interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// sinkRefOf maps a declared sink onto its reference shape.
func sinkRefOf(sink config.NotificationSink) ports.NotificationSinkRef {
	return ports.NotificationSinkRef{ID: sink.ID, Type: sink.Type}
}

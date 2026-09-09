// Managed systemd schedule lifecycle of E19-T8 (cli-spec §19b, OPS-018,
// CLI-018, SEC-007): `--platform systemd` writes a oneshot `.service`
// and matching `.timer` under the XDG user unit directory, enables the
// timer through `systemctl --user`, and mirrors the launchd lifecycle
// contracts (idempotent install, foreign-definition refusal, disable
// preserves units, uninstall removes only the exact managed pair).
package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// systemctl invocations are injectable so tests exercise the managed
// lifecycle without a live systemd user session. Every call is prefixed
// with `--user` (OPS-018 user units).
var systemctlRun = func(args ...string) (string, error) {
	full := append([]string{"--user"}, args...)
	out, err := exec.Command("systemctl", full...).CombinedOutput()
	return string(out), err
}

// systemdUserDir resolves the per-user systemd unit directory
// (`~/.config/systemd/user/` or `$XDG_CONFIG_HOME/systemd/user`); it is
// injectable so the lifecycle tests never touch the real location.
var systemdUserDir = func() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "systemd", "user")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "systemd", "user")
	}
	return filepath.Join(home, ".config", "systemd", "user")
}

// scheduleServicePath / scheduleTimerPath resolve the managed unit pair.
func scheduleServicePath(label string) string {
	return filepath.Join(systemdUserDir(), label+".service")
}

func scheduleTimerPath(label string) string {
	return filepath.Join(systemdUserDir(), label+".timer")
}

// systemdQuote renders one ExecStart argv token as unit-safe text:
// paths and identifiers with whitespace or unit metacharacters are
// double-quoted with `\`, `"`, and `$` escaped so the unit never
// introduces a shell (SEC-007).
func systemdQuote(s string) string {
	need := s == ""
	for _, r := range s {
		if r <= ' ' || strings.ContainsRune(`"'\$;|&<>()#`, r) {
			need = true
			break
		}
	}
	if !need {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\', '"', '$':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// renderScheduleService renders the oneshot service unit. ExecStart is
// a direct argv list — never a shell chain.
func renderScheduleService(d scheduleDefinition) string {
	execStart := strings.Join([]string{
		systemdQuote(d.BinaryPath),
		"schedule",
		"run",
		"--route",
		systemdQuote(d.RouteID),
		"--config",
		systemdQuote(d.ConfigPath),
	}, " ")
	return fmt.Sprintf(`# agent-dispatch managed schedule; safe to remove only through 'schedule uninstall'
[Unit]
Description=agent-dispatch managed schedule (%s)

[Service]
Type=oneshot
ExecStart=%s
StandardOutput=append:%s
StandardError=append:%s
`, d.Label, execStart, d.StdoutPath, d.StderrPath)
}

// renderScheduleTimer renders the matching timer: after-command recovery
// uses an OnActiveSec/OnUnitActiveSec interval of 900s; scheduled mode
// uses OnCalendar at 03:00 local by default (`--at HH:MM` overrides).
func renderScheduleTimer(d scheduleDefinition) string {
	var timing string
	if d.Interval > 0 {
		timing = fmt.Sprintf("OnActiveSec=%d\nOnUnitActiveSec=%d\n", d.Interval, d.Interval)
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
		timing = fmt.Sprintf("OnCalendar=*-*-* %02d:%02d:00\n", hour, minute)
	}
	return fmt.Sprintf(`# agent-dispatch managed schedule; safe to remove only through 'schedule uninstall'
[Unit]
Description=agent-dispatch managed schedule timer (%s)
Requires=%s.service

[Timer]
%sPersistent=true
Unit=%s.service

[Install]
WantedBy=timers.target
`, d.Label, d.Label, timing, d.Label)
}

// systemdDigest hashes the service+timer pair into the managed digest.
func systemdDigest(service, timer string) string {
	sum := sha256.Sum256([]byte(service + "\n" + timer))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// writeSystemdUnits writes the managed unit pair atomically with
// owner-only permissions, refusing a different definition at either
// path (exit 14 / transition_invalid at the caller).
func writeSystemdUnits(def scheduleDefinition) error {
	pairs := []struct {
		path    string
		content string
	}{
		{def.ServicePath, def.ServiceUnit},
		{def.TimerPath, def.TimerUnit},
	}
	identical := 0
	for _, p := range pairs {
		existing, err := os.ReadFile(p.path)
		if err == nil {
			if string(existing) != p.content {
				return fmt.Errorf("unit %s exists with a different definition; uninstall it first", p.path)
			}
			identical++
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if identical == len(pairs) {
		return nil // both present and byte-identical: idempotent
	}
	if err := os.MkdirAll(filepath.Dir(def.ServicePath), 0o700); err != nil {
		return err
	}
	for _, p := range pairs {
		if existing, err := os.ReadFile(p.path); err == nil && string(existing) == p.content {
			continue
		}
		if err := writeManagedFile(p.path, p.content); err != nil {
			return err
		}
	}
	return nil
}

// writeManagedFile writes content atomically with owner-only mode via a
// temp file + hard link (same non-replace posture as the launchd plist).
func writeManagedFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agent-dispatch-schedule-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(content); err != nil {
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
	if err := os.Link(tmpName, path); err != nil {
		if os.IsExist(err) {
			if existing, rerr := os.ReadFile(path); rerr == nil && string(existing) == content {
				return nil
			}
			return fmt.Errorf("unit %s exists with a different definition; uninstall it first", path)
		}
		return err
	}
	return nil
}

// scheduleInstallSystemd writes the unit pair, reloads the user manager,
// and enables/starts the timer.
func scheduleInstallSystemd(command string, def scheduleDefinition, stdout, stderr io.Writer) int {
	if err := writeSystemdUnits(def); err != nil {
		return planErr(stderr, command, "transition_invalid", "conflict", err.Error(), 14)
	}
	if out, err := systemctlRun("daemon-reload"); err != nil {
		return planErr(stderr, command, "target_response_invalid", "acceptance_unknown", out, 21)
	}
	if out, err := systemctlRun("enable", "--now", def.Label+".timer"); err != nil {
		// An identical already-enabled timer is the idempotent posture.
		if !strings.Contains(out, "already enabled") && !strings.Contains(strings.ToLower(out), "exists") {
			return planErr(stderr, command, "target_response_invalid", "acceptance_unknown", out, 21)
		}
	}
	return writeEnvelope(stdout, command, map[string]any{
		"label": def.Label, "service_path": def.ServicePath, "timer_path": def.TimerPath,
		"digest": def.Digest, "mode": def.Mode, "installed": true,
	})
}

// systemdTimerLoaded reports whether the user timer is enabled and/or
// active in the current session (cli-spec §19b `loaded`).
func systemdTimerLoaded(label string) bool {
	if out, err := systemctlRun("is-enabled", label+".timer"); err == nil {
		switch strings.TrimSpace(out) {
		case "enabled", "enabled-runtime", "static", "indirect":
			return true
		}
	}
	if out, err := systemctlRun("is-active", label+".timer"); err == nil {
		switch strings.TrimSpace(out) {
		case "active", "waiting":
			return true
		}
	}
	return false
}

// scheduleInspectSystemd reports unit presence, loaded state, and the
// definition-digest match for the service+timer pair.
func scheduleInspectSystemd(command string, def scheduleDefinition, stdout, stderr io.Writer) int {
	serviceRaw, servicePresent := readOptionalFile(def.ServicePath)
	timerRaw, timerPresent := readOptionalFile(def.TimerPath)
	present := servicePresent && timerPresent
	installedDigest := ""
	if present {
		installedDigest = systemdDigest(serviceRaw, timerRaw)
	}
	definitionMatches := present && serviceRaw == def.ServiceUnit && timerRaw == def.TimerUnit
	loaded := systemdTimerLoaded(def.Label)
	return writeEnvelope(stdout, command, map[string]any{
		"label": def.Label, "service_path": def.ServicePath, "timer_path": def.TimerPath,
		"present": present, "loaded": loaded, "definition_matches": definitionMatches,
		"expected_digest": def.Digest, "installed_digest": installedDigest,
		"healthy": loaded && definitionMatches,
	})
}

func readOptionalFile(path string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// scheduleDisableSystemd stops/disables the timer while preserving the
// unit files.
func scheduleDisableSystemd(command string, def scheduleDefinition, stdout, stderr io.Writer) int {
	if out, err := systemctlRun("disable", "--now", def.Label+".timer"); err != nil {
		lower := strings.ToLower(out)
		if !strings.Contains(lower, "not loaded") && !strings.Contains(lower, "does not exist") &&
			!strings.Contains(lower, "not found") && !strings.Contains(lower, "no such file") &&
			!strings.Contains(lower, "not enabled") {
			return planErr(stderr, command, "target_response_invalid", "acceptance_unknown", out, 21)
		}
	}
	return writeEnvelope(stdout, command, map[string]any{
		"label": def.Label, "disabled": true, "units_preserved": true,
	})
}

// scheduleUninstallSystemd stops/disables the timer and removes only
// the byte-identical managed service and timer units.
func scheduleUninstallSystemd(command string, def scheduleDefinition, stdout, stderr io.Writer) int {
	_, _ = systemctlRun("disable", "--now", def.Label+".timer")
	removed := []string{}
	for _, pair := range []struct {
		path    string
		content string
	}{
		{def.ServicePath, def.ServiceUnit},
		{def.TimerPath, def.TimerUnit},
	} {
		if _, err := os.Stat(pair.path); err != nil {
			continue
		}
		raw, rerr := os.ReadFile(pair.path)
		if rerr != nil || string(raw) != pair.content {
			return planErr(stderr, command, "transition_invalid", "conflict",
				pair.path+" is not the exact current managed definition; refusing to remove it", 14)
		}
		if err := os.Remove(pair.path); err != nil {
			return planErr(stderr, command, "sqlite_query_failed", "storage", err.Error(), 20)
		}
		removed = append(removed, pair.path)
	}
	if _, err := systemctlRun("daemon-reload"); err != nil {
		// Best-effort: the units are already gone; a stale cache is
		// recoverable by the next install's daemon-reload.
	}
	return writeEnvelope(stdout, command, map[string]any{
		"label": def.Label, "uninstalled": true, "removed": removed,
	})
}

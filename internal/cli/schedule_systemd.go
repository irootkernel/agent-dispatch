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

// systemdQuote renders one ExecStart argv token as unit-safe text. The
// general unit grammar accepts C-style quoting, while ExecStart additionally
// expands $ variables and % specifiers. Doubling them preserves the literal
// bytes without introducing a shell (SEC-007).
func systemdQuote(s string) string {
	return strconv.Quote(strings.NewReplacer("$", "$$", "%", "%%").Replace(s))
}

// systemdQuotedValue renders one complete non-Exec directive value. Dollar
// signs are ordinary bytes there; percent still needs doubling because unit
// specifier expansion applies to path-bearing settings.
func systemdQuotedValue(s string) string {
	return strconv.Quote(strings.ReplaceAll(s, "%", "%%"))
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
StandardOutput=%s
StandardError=%s
`, d.Label, execStart, systemdQuotedValue("append:"+d.StdoutPath), systemdQuotedValue("append:"+d.StderrPath))
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
		return planErr(stderr, command, "target_response_invalid", "acceptance_unknown", out, 21)
	}
	return writeEnvelope(stdout, command, map[string]any{
		"label": def.Label, "service_path": def.ServicePath, "timer_path": def.TimerPath,
		"digest": def.Digest, "mode": def.Mode, "installed": true,
	})
}

// systemdTimerLoaded reports whether the user timer is both enabled and
// active in the current session (cli-spec §19b `loaded`).
func systemdTimerLoaded(label string) bool {
	enabled := false
	if out, err := systemctlRun("is-enabled", label+".timer"); err == nil {
		switch strings.TrimSpace(out) {
		case "enabled", "enabled-runtime", "static", "indirect":
			enabled = true
		}
	}
	active := false
	if out, err := systemctlRun("is-active", label+".timer"); err == nil {
		switch strings.TrimSpace(out) {
		case "active", "waiting":
			active = true
		}
	}
	return enabled && active
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
	if out, err := disableSystemdTimer(def.Label); err != nil {
		return planErr(stderr, command, "target_response_invalid", "acceptance_unknown", out, 21)
	}
	return writeEnvelope(stdout, command, map[string]any{
		"label": def.Label, "disabled": true, "units_preserved": true,
	})
}

// disableSystemdTimer accepts only the idempotent absence states. Any other
// systemctl failure leaves ownership-bearing unit files in place for recovery.
func disableSystemdTimer(label string) (string, error) {
	out, err := systemctlRun("disable", "--now", label+".timer")
	if err == nil {
		return out, nil
	}
	lower := strings.ToLower(out)
	if strings.Contains(lower, "not loaded") || strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "not found") || strings.Contains(lower, "no such file") ||
		strings.Contains(lower, "not enabled") {
		return out, nil
	}
	return out, err
}

// scheduleUninstallSystemd stops/disables the timer and removes only
// the byte-identical managed service and timer units.
func scheduleUninstallSystemd(command string, def scheduleDefinition, stdout, stderr io.Writer) int {
	pairs := []struct {
		path    string
		content string
		present bool
	}{
		{path: def.ServicePath, content: def.ServiceUnit},
		{path: def.TimerPath, content: def.TimerUnit},
	}
	// Establish ownership of the complete pair before any external or file
	// mutation. A conflict at the second path must not disable the timer or
	// leave the first path partially removed.
	for i := range pairs {
		raw, err := os.ReadFile(pairs[i].path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || string(raw) != pairs[i].content {
			return planErr(stderr, command, "transition_invalid", "conflict",
				pairs[i].path+" is not the exact current managed definition; refusing to remove it", 14)
		}
		pairs[i].present = true
	}
	if out, err := disableSystemdTimer(def.Label); err != nil {
		return planErr(stderr, command, "target_response_invalid", "acceptance_unknown", out, 21)
	}
	removed := []string{}
	for _, pair := range pairs {
		if !pair.present {
			continue
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

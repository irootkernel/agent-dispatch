package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/irootkernel/agent-dispatch/internal/config"
)

// A sync group has one managed definition and one executor: sync serve. The
// label binds the group so a second config path cannot install another unit.
type syncServiceDefinition struct {
	group, label, platform, path, configPath, binaryPath, content, digest string
}

var syncServicePlatform = func() string { return runtime.GOOS }

func buildSyncServiceDefinition(group, configPath string) (syncServiceDefinition, error) {
	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return syncServiceDefinition{}, err
	}
	binary, err := os.Executable()
	if err != nil {
		return syncServiceDefinition{}, err
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return syncServiceDefinition{}, err
	}
	platform := syncServicePlatform()
	if platform != "darwin" && platform != "linux" {
		return syncServiceDefinition{}, fmt.Errorf("managed sync service requires macOS or Linux")
	}
	sum := sha256.Sum256([]byte(group))
	d := syncServiceDefinition{
		group: group, label: "xyz.rootkernel.agent-dispatch.sync." + hex.EncodeToString(sum[:])[:16],
		platform: platform, configPath: absConfig, binaryPath: binary,
	}
	if platform == "darwin" {
		logDir := filepath.Join(filepath.Dir(launchAgentsDir()), "Logs", "agent-dispatch")
		d.path = schedulePlistPath(d.label)
		d.content = fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array>
    <string>%s</string><string>sync</string><string>serve</string>
    <string>--group</string><string>%s</string>
    <string>--config</string><string>%s</string>
	<string>--managed</string>
  </array>
  <key>RunAtLoad</key><true/>
	<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
	<key>StandardOutPath</key><string>%s</string>
	<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, xmlEscape(d.label), xmlEscape(binary), xmlEscape(group), xmlEscape(absConfig), xmlEscape(filepath.Join(logDir, d.label+".out.log")), xmlEscape(filepath.Join(logDir, d.label+".err.log")))
	} else {
		d.path = scheduleServicePath(d.label)
		d.content = fmt.Sprintf(`# agent-dispatch managed sync service
[Unit]
Description=agent-dispatch peer and reconciliation service (%s)

[Service]
Type=simple
ExecStart=%s sync serve --group %s --config %s --managed
Restart=on-failure
RestartSec=30

[Install]
WantedBy=default.target
`, d.label, systemdQuote(binary), systemdQuote(group), systemdQuote(absConfig))
	}
	contentSum := sha256.Sum256([]byte(d.content))
	d.digest = "sha256:" + hex.EncodeToString(contentSum[:])
	return d, nil
}

// readSyncServiceDefinition rejects symlinks and non-regular files before
// inspecting bytes. No lifecycle mutation may touch a foreign definition.
func readSyncServiceDefinition(d syncServiceDefinition) (present, matches bool, installedDigest string, err error) {
	info, err := os.Lstat(d.path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, "", nil
	}
	if err != nil {
		return false, false, "", err
	}
	if !info.Mode().IsRegular() {
		return true, false, "", nil
	}
	raw, err := os.ReadFile(d.path)
	if err != nil {
		return true, false, "", err
	}
	sum := sha256.Sum256(raw)
	return true, string(raw) == d.content, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func syncServiceLoadState(d syncServiceDefinition) (bool, error) {
	if d.platform == "linux" {
		out, err := systemctlRun("is-active", d.label+".service")
		if err == nil {
			return strings.TrimSpace(out) == "active", nil
		}
		if strings.TrimSpace(out) == "inactive" || strings.TrimSpace(out) == "failed" || errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	out, err := launchctlRun("print", fmt.Sprintf("gui/%d/%s", os.Getuid(), d.label))
	if err == nil {
		return true, nil
	}
	if strings.Contains(out, "Could not find service") || errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func syncServiceStop(d syncServiceDefinition) error {
	if d.platform == "linux" {
		_, err := systemctlRun("stop", d.label+".service")
		return err
	}
	loaded, err := syncServiceLoadState(d)
	if err != nil {
		return err
	}
	if !loaded {
		return nil
	}
	_, err = launchctlRun("bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), d.label))
	return err
}

func syncServiceDisable(d syncServiceDefinition) error {
	if d.platform == "linux" {
		_, err := systemctlRun("disable", "--now", d.label+".service")
		return err
	}
	if err := syncServiceStop(d); err != nil {
		return err
	}
	_, err := launchctlRun("disable", fmt.Sprintf("gui/%d/%s", os.Getuid(), d.label))
	return err
}

func runSyncService(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "sync service", "service requires render, install, inspect, stop, disable, or uninstall")
	}
	sub := args[0]
	command := "sync service " + sub
	switch sub {
	case "render", "install", "inspect", "stop", "disable", "uninstall":
	default:
		return usageError(stderr, command, "unknown sync service action")
	}
	group, explicitConfig, ok := syncStatusFlags(args[1:])
	if !ok {
		return usageError(stderr, command, "service action requires --group GROUP and accepts --config PATH --output json")
	}
	configPath := resolveConfigPath(explicitConfig)
	cfg, err := config.Load(configPath)
	configErr := err
	if err != nil && (sub == "render" || sub == "install") {
		return planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	if (sub == "install" || sub == "render") && (cfg.Sync == nil || cfg.Sync.GroupID != group) {
		return planErr(stderr, command, "sync_precondition_failed", "conflict", "configured sync group is unavailable", 14)
	}
	if sub == "install" && !cfg.Sync.Enabled {
		return planErr(stderr, command, "sync_precondition_failed", "conflict", "configured sync group must be enabled before install", 14)
	}
	d, err := buildSyncServiceDefinition(group, configPath)
	if err != nil {
		return planErr(stderr, command, "config_invalid", "configuration", "managed service definition could not be built", 3)
	}
	base := map[string]any{"schema_version": "agent-dispatch.sync-service/v1", "group_id": group,
		"platform": d.platform, "label": d.label, "definition_path": d.path, "expected_digest": d.digest}
	if sub == "render" {
		base["definition"] = d.content
		base["binary"] = d.binaryPath
		base["config"] = d.configPath
		base["side_effects"] = []string{}
		return writeEnvelope(stdout, command, base)
	}
	present, matches, digest, err := readSyncServiceDefinition(d)
	if err != nil {
		return planErr(stderr, command, "sync_service_io_failed", "storage", "managed definition could not be inspected", 20)
	}
	if sub == "inspect" {
		loaded, loadErr := syncServiceLoadState(d)
		if loadErr != nil {
			return planErr(stderr, command, "sync_retryable", "transient_local", "managed service load state unavailable", 10)
		}
		base["present"] = present
		base["definition_matches"] = matches
		base["installed_digest"] = digest
		base["loaded"] = loaded
		enabled := cfg != nil && cfg.Sync != nil && cfg.Sync.GroupID == group && cfg.Sync.Enabled
		base["enabled"] = enabled
		base["config_available"] = configErr == nil
		base["healthy"] = syncServicePosture(present, matches, loaded, enabled, nil).State == "ready"
		base["side_effects"] = []string{}
		return writeEnvelope(stdout, command, base)
	}
	if present && !matches {
		return planErr(stderr, command, "sync_precondition_failed", "conflict", "managed definition differs from the current exact definition", 14)
	}
	if sub != "install" && !present {
		loaded, loadErr := syncServiceLoadState(d)
		if loadErr != nil || loaded {
			return planErr(stderr, command, "sync_precondition_failed", "conflict", "definition absent while managed service state is uncertain or loaded", 14)
		}
		base["present"] = false
		base["side_effects"] = []string{}
		return writeEnvelope(stdout, command, base)
	}
	switch sub {
	case "install":
		if d.platform == "darwin" {
			if err := os.MkdirAll(filepath.Join(filepath.Dir(launchAgentsDir()), "Logs", "agent-dispatch"), 0o700); err != nil {
				return planErr(stderr, command, "sync_service_io_failed", "storage", "service log directory could not be created", 20)
			}
		}
		if !present {
			if err := writeManagedFile(d.path, d.content); err != nil {
				return planErr(stderr, command, "sync_service_io_failed", "storage", "managed definition could not be written", 20)
			}
		}
		if d.platform == "linux" {
			if _, err := systemctlRun("daemon-reload"); err != nil {
				return planErr(stderr, command, "sync_retryable", "transient_local", "systemd user daemon reload failed", 10)
			}
			if _, err := systemctlRun("enable", "--now", d.label+".service"); err != nil {
				return planErr(stderr, command, "sync_retryable", "transient_local", "systemd user service start failed", 10)
			}
		} else {
			if _, err := launchctlRun("enable", fmt.Sprintf("gui/%d/%s", os.Getuid(), d.label)); err != nil {
				return planErr(stderr, command, "sync_retryable", "transient_local", "launchd service enable failed", 10)
			}
			loaded, loadErr := syncServiceLoadState(d)
			if loadErr != nil {
				return planErr(stderr, command, "sync_retryable", "transient_local", "launchd service load state unavailable", 10)
			}
			if !loaded {
				if _, err := launchctlRun("bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), d.path); err != nil {
					return planErr(stderr, command, "sync_retryable", "transient_local", "launchd service bootstrap failed", 10)
				}
			} else {
				// A managed executor exits successfully when its group is disabled.
				// A loaded job can therefore be idle after the config is restored.
				// Without -k, kickstart leaves an already-running instance alone.
				if _, err := launchctlRun("kickstart", fmt.Sprintf("gui/%d/%s", os.Getuid(), d.label)); err != nil {
					return planErr(stderr, command, "sync_retryable", "transient_local", "launchd service kickstart failed", 10)
				}
			}
		}
		base["installed"] = true
	case "stop":
		if err := syncServiceStop(d); err != nil {
			return planErr(stderr, command, "sync_retryable", "transient_local", "managed service stop failed", 10)
		}
		base["stopped"] = true
	case "disable":
		if err := syncServiceDisable(d); err != nil {
			return planErr(stderr, command, "sync_retryable", "transient_local", "managed service disable failed", 10)
		}
		base["disabled"] = true
	case "uninstall":
		if err := syncServiceDisable(d); err != nil {
			return planErr(stderr, command, "sync_retryable", "transient_local", "managed service disable failed", 10)
		}
		if err := os.Remove(d.path); err != nil {
			return planErr(stderr, command, "sync_service_io_failed", "storage", "managed definition could not be removed", 20)
		}
		if d.platform == "linux" {
			if _, err := systemctlRun("daemon-reload"); err != nil {
				return planErr(stderr, command, "sync_retryable", "transient_local", "systemd user daemon reload failed", 10)
			}
		}
		base["uninstalled"] = true
		base["state_preserved"] = true
	}
	return writeEnvelope(stdout, command, base)
}

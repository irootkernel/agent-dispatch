package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
)

func TestE22T4ManagedServiceExactDefinitionLifecycle(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			oldPlatform, oldLaunchDir, oldSystemdDir := syncServicePlatform, launchAgentsDir, systemdUserDir
			oldLaunchctl, oldSystemctl := launchctlRun, systemctlRun
			syncServicePlatform = func() string { return platform }
			root := t.TempDir()
			launchAgentsDir = func() string { return filepath.Join(root, "LaunchAgents") }
			systemdUserDir = func() string { return filepath.Join(root, "systemd") }
			loaded := false
			launchctlRun = func(args ...string) (string, error) {
				switch args[0] {
				case "print":
					if loaded {
						return "service = loaded", nil
					}
					return "Could not find service", os.ErrNotExist
				case "bootstrap", "enable":
					loaded = true
				case "bootout", "disable":
					loaded = false
				}
				return "", nil
			}
			systemctlRun = func(args ...string) (string, error) {
				switch args[0] {
				case "is-active":
					if loaded {
						return "active\n", nil
					}
					return "inactive\n", os.ErrNotExist
				case "enable":
					loaded = true
				case "stop", "disable":
					loaded = false
				}
				return "", nil
			}
			t.Cleanup(func() {
				syncServicePlatform, launchAgentsDir, systemdUserDir = oldPlatform, oldLaunchDir, oldSystemdDir
				launchctlRun, systemctlRun = oldLaunchctl, oldSystemctl
			})
			group := svc.cfg.Sync.GroupID
			args := func(action string) []string {
				return []string{"service", action, "--group", group, "--config", svc.configPath, "--output", "json"}
			}
			run := func(action string, want int) string {
				t.Helper()
				var out, errb bytes.Buffer
				if code := runSync(args(action), &out, &errb); code != want {
					t.Fatalf("%s: exit %d, want %d: %s", action, code, want, errb.String())
				}
				return out.String()
			}
			def, err := buildSyncServiceDefinition(svc.cfg.Sync.GroupID, svc.configPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(def.content, "--managed") || !strings.Contains(def.content, svc.configPath) {
				t.Fatal("managed executor argv omitted its config or lifecycle flag")
			}
			if platform == "darwin" {
				for _, part := range []string{"SuccessfulExit</key><false/>", "StandardOutPath", "StandardErrorPath", filepath.Join(root, "Logs", "agent-dispatch")} {
					if !strings.Contains(def.content, part) {
						t.Fatalf("launchd definition omitted %s", part)
					}
				}
			} else if !strings.Contains(def.content, "Restart=on-failure") {
				t.Fatal("systemd definition omitted failure restart policy")
			}
			if !strings.Contains(run("render", 0), def.digest) {
				t.Fatal("render omitted exact definition digest")
			}
			if _, err := os.Stat(def.path); !os.IsNotExist(err) {
				t.Fatalf("render wrote definition: %v", err)
			}
			run("install", 0)
			run("install", 0)
			otherConfig := filepath.Join(root, "other-config.yaml")
			configBytes, err := os.ReadFile(svc.configPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(otherConfig, configBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			other, err := buildSyncServiceDefinition(group, otherConfig)
			if err != nil {
				t.Fatal(err)
			}
			if other.label != def.label || other.path != def.path || other.content == def.content {
				t.Fatal("second config path escaped the one-service group identity")
			}
			var alternateOut, alternateErr bytes.Buffer
			if code := runSync([]string{"service", "install", "--group", group, "--config", otherConfig, "--output", "json"}, &alternateOut, &alternateErr); code != 14 {
				t.Fatalf("alternate config installed another group service: %d %s", code, alternateErr.String())
			}
			inspected := run("inspect", 0)
			for _, field := range []string{`"present":true`, `"definition_matches":true`, `"loaded":true`, `"healthy":true`, def.digest} {
				if !strings.Contains(inspected, field) {
					t.Fatalf("installed inspect omitted %s: %s", field, inspected)
				}
			}
			if err := os.WriteFile(svc.configPath, []byte("invalid config"), 0o600); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(run("inspect", 0), `"config_available":false`) {
				t.Fatal("inspect concealed unreadable configuration")
			}
			var invalidServeErr bytes.Buffer
			if code := runSyncServe([]string{"--group", group, "--config", svc.configPath, "--managed"}, &bytes.Buffer{}, &invalidServeErr); code != 0 {
				t.Fatalf("managed invalid config restarted: %d %s", code, invalidServeErr.String())
			}
			invalidServeErr.Reset()
			if code := runSyncServe([]string{"--group", group, "--config", svc.configPath}, &bytes.Buffer{}, &invalidServeErr); code != 3 {
				t.Fatalf("manual invalid config exit = %d", code)
			}
			run("stop", 0)
			run("disable", 0)
			run("uninstall", 0)
			if err := os.WriteFile(svc.configPath, configBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			run("install", 0)
			run("stop", 0)
			if loaded {
				t.Fatal("stop did not stop the service")
			}
			run("disable", 0)
			if _, err := os.Stat(def.path); err != nil {
				t.Fatalf("disable removed definition: %v", err)
			}
			// Turning sync off must not trap the operator with a service
			// definition that can no longer be inspected or removed.
			svc.cfg.Sync.Enabled = false
			configBytes, err = json.Marshal(svc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(svc.configPath, configBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(run("inspect", 0), `"healthy":false`) {
				t.Fatal("disabled group was reported healthy")
			}
			var statusOut, statusErr bytes.Buffer
			if code := Run([]string{"sync", "status", "--group", group, "--config", svc.configPath, "--output", "json"}, &statusOut, &statusErr); code != 0 || !strings.Contains(statusOut.String(), "configured_group_disabled") {
				t.Fatalf("disabled status omitted installed service: %d %s %s", code, statusOut.String(), statusErr.String())
			}
			statusOut.Reset()
			statusErr.Reset()
			Run([]string{"doctor", "--config", svc.configPath}, &statusOut, &statusErr)
			if !strings.Contains(statusOut.String(), "sync_service_configured_group_disabled") {
				t.Fatalf("doctor omitted disabled installed service: %s %s", statusOut.String(), statusErr.String())
			}
			run("install", 14)
			var serveErr bytes.Buffer
			if code := runSyncServe([]string{"--group", svc.cfg.Sync.GroupID, "--config", svc.configPath, "--managed"}, &bytes.Buffer{}, &serveErr); code != 0 {
				t.Fatalf("managed disabled serve restarted: %d %s", code, serveErr.String())
			}
			serveErr.Reset()
			if code := runSyncServe([]string{"--group", svc.cfg.Sync.GroupID, "--config", svc.configPath}, &bytes.Buffer{}, &serveErr); code != 14 {
				t.Fatalf("manual disabled serve exit = %d", code)
			}
			if err := os.WriteFile(def.path, []byte("foreign"), 0o600); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(run("inspect", 0), `"definition_matches":false`) {
				t.Fatal("inspect concealed foreign definition")
			}
			run("uninstall", 14)
			if raw, err := os.ReadFile(def.path); err != nil || string(raw) != "foreign" {
				t.Fatalf("foreign definition was changed: %q %v", raw, err)
			}
			if err := os.Remove(def.path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(svc.configPath, def.path); err != nil {
				t.Fatal(err)
			}
			run("uninstall", 14)
			if _, err := os.Lstat(def.path); err != nil {
				t.Fatalf("foreign symlink was removed: %v", err)
			}
			if err := os.Remove(def.path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(def.path, []byte(def.content), 0o600); err != nil {
				t.Fatal(err)
			}
			svc.cfg.Sync.GroupID = "replacement-group"
			configBytes, err = json.Marshal(svc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(svc.configPath, configBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(run("inspect", 0), `"healthy":false`) {
				t.Fatal("replaced group was reported healthy")
			}
			if platform == "darwin" {
				loaded = true
				launchctlRun = func(args ...string) (string, error) { return "manager unavailable", os.ErrPermission }
				run("stop", 10)
				if !loaded {
					t.Fatal("manager probe error changed service state")
				}
				launchctlRun = func(args ...string) (string, error) {
					switch args[0] {
					case "print":
						if loaded {
							return "service = loaded", nil
						}
						return "Could not find service", os.ErrNotExist
					case "bootout", "disable":
						loaded = false
					case "bootstrap", "enable":
						loaded = true
					}
					return "", nil
				}
			} else {
				loaded = true
				workingSystemctl := systemctlRun
				systemctlRun = func(...string) (string, error) { return "manager unavailable", os.ErrPermission }
				run("inspect", 10)
				run("disable", 10)
				if !loaded {
					t.Fatal("systemd failure changed service state")
				}
				systemctlRun = workingSystemctl
			}
			run("uninstall", 0)
			svc.cfg.Sync.GroupID = group
			if _, err := os.Stat(def.path); !os.IsNotExist(err) {
				t.Fatalf("managed definition remains: %v", err)
			}
			loaded = true
			run("stop", 14)
			if !loaded {
				t.Fatal("absent definition stop touched a loaded service")
			}
			loaded = false
			originalSync := svc.cfg.Sync
			removedGroup := originalSync.GroupID
			svc.cfg.Sync = nil
			configBytes, err = json.Marshal(svc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(svc.configPath, configBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			run("inspect", 0)
			run("uninstall", 0)
			serveErr.Reset()
			if code := runSyncServe([]string{"--group", removedGroup, "--config", svc.configPath, "--managed"}, &bytes.Buffer{}, &serveErr); code != 0 {
				t.Fatalf("managed removed serve restarted: %d %s", code, serveErr.String())
			}
			if _, err := os.Stat(svc.cfg.Instance.StateDir); err != nil {
				t.Fatalf("uninstall removed state: %v", err)
			}
			svc.cfg.Sync = originalSync
			svc.cfg.Sync.Enabled = true
			configBytes, err = json.Marshal(svc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(svc.configPath, configBytes, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestE22T4LaunchdInstallStartsLoadedIdleService(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	oldPlatform, oldLaunchDir, oldLaunchctl := syncServicePlatform, launchAgentsDir, launchctlRun
	root := t.TempDir()
	syncServicePlatform = func() string { return "darwin" }
	launchAgentsDir = func() string { return filepath.Join(root, "LaunchAgents") }
	t.Cleanup(func() { syncServicePlatform, launchAgentsDir, launchctlRun = oldPlatform, oldLaunchDir, oldLaunchctl })
	def, err := buildSyncServiceDefinition(svc.cfg.Sync.GroupID, svc.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(def.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(def.path, []byte(def.content), 0o600); err != nil {
		t.Fatal(err)
	}
	running, starts := false, 0
	launchctlRun = func(args ...string) (string, error) {
		switch args[0] {
		case "enable":
			return "", nil
		case "print":
			return "service = loaded", nil
		case "kickstart":
			if len(args) != 2 || args[1] != "gui/"+strconv.Itoa(os.Getuid())+"/"+def.label {
				t.Fatalf("unsafe kickstart arguments: %v", args)
			}
			if !running {
				running = true
				starts++
			}
			return "", nil
		default:
			t.Fatalf("unexpected launchctl action: %v", args)
			return "", nil
		}
	}
	args := []string{"service", "install", "--group", svc.cfg.Sync.GroupID, "--config", svc.configPath}
	for i := 0; i < 2; i++ {
		var out, errb bytes.Buffer
		if code := runSync(args, &out, &errb); code != 0 {
			t.Fatalf("install %d: %d %s", i, code, errb.String())
		}
	}
	if !running || starts != 1 {
		t.Fatalf("loaded idle service start count = %d, running = %v", starts, running)
	}
	workingLaunchctl := launchctlRun
	launchctlRun = func(args ...string) (string, error) {
		if args[0] == "kickstart" {
			return "manager unavailable", os.ErrPermission
		}
		return workingLaunchctl(args...)
	}
	var out, errb bytes.Buffer
	if code := runSync(args, &out, &errb); code != 10 || !strings.Contains(errb.String(), `"code":"sync_retryable"`) {
		t.Fatalf("kickstart failure: exit %d, stderr %s", code, errb.String())
	}
}

func TestE22T4DiagnosticsUseBoundedReasons(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	oldPlatform, oldLaunchDir := syncServicePlatform, launchAgentsDir
	syncServicePlatform = func() string { return "darwin" }
	root := t.TempDir()
	launchAgentsDir = func() string { return filepath.Join(root, "LaunchAgents") }
	t.Cleanup(func() { syncServicePlatform, launchAgentsDir = oldPlatform, oldLaunchDir })
	health := syncHealthSnapshot(context.Background(), svc.cfg, svc.configPath, svc.store,
		sqlite.SyncControlRow{GroupID: svc.cfg.Sync.GroupID, State: "paused", MembershipMode: "normal"})
	for _, category := range syncHealthCategories {
		if health[category].State == "" || health[category].Reason == "" {
			t.Fatalf("%s has no bounded state/reason: %+v", category, health[category])
		}
	}
	if len(health) != len(syncHealthCategories) {
		t.Fatalf("health category set drifted: %+v", health)
	}
	if health["activation"].Reason != "control_not_active" || health["service"].Reason != "definition_absent" {
		t.Fatalf("unexpected operator posture: %+v", health)
	}
	raw, err := json.Marshal(health)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{svc.cfg.Sync.Nodes[0].CredentialRef, svc.cfg.Sync.Nodes[1].CredentialRef, svc.cfg.Sync.PublisherSigningKeyRef} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("diagnostics disclosed a credential reference")
		}
	}
	if len(syncDoctorFindings(health)) == 0 {
		t.Fatal("doctor omitted unhealthy sync posture")
	}
	if _, _, err := svc.store.AdmitSyncJob(context.Background(), sqlite.SyncJobInput{
		JobID: "health-verification", GroupID: svc.cfg.Sync.GroupID, Kind: "verification",
		LogicalKey: "health-verification", InitialState: "planned", PayloadJSON: `{}`,
		QueueLimit: 1, Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	health = syncHealthSnapshot(context.Background(), svc.cfg, svc.configPath, svc.store,
		sqlite.SyncControlRow{GroupID: svc.cfg.Sync.GroupID, State: "paused", MembershipMode: "normal"})
	if health["queue"].State != "pending" || health["queue"].Count != 1 {
		t.Fatalf("queue omitted unresolved verification: %+v", health["queue"])
	}
	def, err := buildSyncServiceDefinition(svc.cfg.Sync.GroupID, svc.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(def.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(def.path, []byte(def.content), 0o600); err != nil {
		t.Fatal(err)
	}
	oldLaunchctl := launchctlRun
	launchctlRun = func(...string) (string, error) { return "manager unavailable", os.ErrPermission }
	t.Cleanup(func() { launchctlRun = oldLaunchctl })
	health = syncHealthSnapshot(context.Background(), svc.cfg, svc.configPath, svc.store,
		sqlite.SyncControlRow{GroupID: svc.cfg.Sync.GroupID, State: "paused", MembershipMode: "normal"})
	if health["service"].Reason != "load_state_unknown" {
		t.Fatalf("manager failure reported as stopped: %+v", health["service"])
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"sync", "status", "--group", svc.cfg.Sync.GroupID, "--config", svc.configPath, "--output", "json"}, &out, &errb); code != 0 {
		t.Fatalf("sync status: exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), `"health":`) || !strings.Contains(out.String(), `"credential_resolution_not_probed"`) {
		t.Fatalf("sync status omitted bounded health: %s", out.String())
	}
}

func TestE22T4StatusAndDoctorAgreeWithoutControlRow(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	if _, err := svc.store.ExecContext(context.Background(), `DELETE FROM sync_controls WHERE group_id=?`, svc.cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"sync", "status", "--group", svc.cfg.Sync.GroupID, "--config", svc.configPath, "--output", "json"},
		{"doctor", "--config", svc.configPath},
	} {
		var out, errb bytes.Buffer
		Run(args, &out, &errb)
		if strings.Contains(out.String(), "control_configuration_stale") {
			t.Fatalf("%v fabricated stale control revision: %s", args, out.String())
		}
	}
}

func TestE22T4ManagedLogCapsOpenRegularFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "service-log-")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(scheduleLogMaxBytes); err != nil {
		t.Fatal(err)
	}
	capManagedSyncLog(f)
	if info, err := f.Stat(); err != nil || info.Size() != 0 {
		t.Fatalf("managed log was not capped: %v %v", info, err)
	}
}

func TestE22T4ServiceFailuresKeepRegisteredCodes(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	oldPlatform, oldLaunchDir := syncServicePlatform, launchAgentsDir
	root := t.TempDir()
	syncServicePlatform = func() string { return "unsupported" }
	launchAgentsDir = func() string { return filepath.Join(root, "LaunchAgents") }
	t.Cleanup(func() { syncServicePlatform, launchAgentsDir = oldPlatform, oldLaunchDir })
	args := []string{"service", "render", "--group", svc.cfg.Sync.GroupID, "--config", svc.configPath}
	check := func(action string, wantExit int, wantCode, wantCategory string) {
		t.Helper()
		args[1] = action
		var out, errb bytes.Buffer
		if got := runSync(args, &out, &errb); got != wantExit {
			t.Fatalf("%s exit = %d, want %d: %s", action, got, wantExit, errb.String())
		}
		var envelope ErrorEnvelope
		if err := json.Unmarshal(errb.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Error.Code != wantCode || envelope.Error.Category != wantCategory {
			t.Fatalf("%s error = %+v", action, envelope.Error)
		}
	}
	check("render", 3, "config_invalid", "configuration")
	syncServicePlatform = func() string { return "darwin" }
	if err := os.WriteFile(filepath.Join(root, "Logs"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	check("install", 20, "sync_service_io_failed", "storage")
	var out, errb bytes.Buffer
	if got := runSync([]string{"service", "inspect", "--group", svc.cfg.Sync.GroupID, "--config", ""}, &out, &errb); got != 2 {
		t.Fatalf("explicit empty config exit = %d: %s", got, errb.String())
	}
	out.Reset()
	errb.Reset()
	if got := runSync([]string{"status", "--group", svc.cfg.Sync.GroupID, "--config", ""}, &out, &errb); got != 2 {
		t.Fatalf("status accepted explicit empty config: %d %s", got, errb.String())
	}
}

func TestE22T4UnknownSyncCommandsAreUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"sync", "unknown"}, {"sync", "service", "unknown"}} {
		var out, errb bytes.Buffer
		if code := Run(args, &out, &errb); code != 2 || out.Len() != 0 {
			t.Fatalf("%v: exit %d, stdout %q, stderr %s", args, code, out.String(), errb.String())
		}
		var envelope ErrorEnvelope
		if err := json.Unmarshal(errb.Bytes(), &envelope); err != nil || envelope.Error.Code != "flag_invalid" || envelope.Error.Category != "usage" {
			t.Fatalf("%v: error %+v, decode %v", args, envelope.Error, err)
		}
	}
}

func TestE22T4ServeRejectsInvalidFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"missing group", nil},
		{"missing group value", []string{"--group"}},
		{"empty group value", []string{"--group", ""}},
		{"duplicate group", []string{"--group", "wiki-pair", "--group", "other"}},
		{"missing config value", []string{"--group", "wiki-pair", "--config"}},
		{"empty config value", []string{"--group", "wiki-pair", "--config", ""}},
		{"duplicate config", []string{"--group", "wiki-pair", "--config", "/first", "--config", "/second"}},
		{"duplicate managed", []string{"--group", "wiki-pair", "--managed", "--managed"}},
		{"unknown flag", []string{"--group", "wiki-pair", "--unknown", "value"}},
		{"trailing value", []string{"--group", "wiki-pair", "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := runSyncServe(tc.args, &out, &errb); code != 2 || out.Len() != 0 {
				t.Fatalf("%v: exit %d, stdout %q, stderr %s", tc.args, code, out.String(), errb.String())
			}
			var envelope ErrorEnvelope
			if err := json.Unmarshal(errb.Bytes(), &envelope); err != nil || envelope.Error.Code != "flag_invalid" || envelope.Error.Category != "usage" {
				t.Fatalf("%v: error %+v, decode %v", tc.args, envelope.Error, err)
			}
		})
	}
}

func TestE22T4DoctorReportsDisabledServiceWhenStoreCannotOpen(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	oldPlatform, oldLaunchDir, oldLaunchctl := syncServicePlatform, launchAgentsDir, launchctlRun
	root := t.TempDir()
	syncServicePlatform = func() string { return "darwin" }
	launchAgentsDir = func() string { return filepath.Join(root, "LaunchAgents") }
	launchctlRun = func(...string) (string, error) { return "service = loaded", nil }
	t.Cleanup(func() { syncServicePlatform, launchAgentsDir, launchctlRun = oldPlatform, oldLaunchDir, oldLaunchctl })
	def, err := buildSyncServiceDefinition(svc.cfg.Sync.GroupID, svc.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(def.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(def.path, []byte(def.content), 0o600); err != nil {
		t.Fatal(err)
	}
	svc.cfg.Sync.Enabled = false
	svc.cfg.Instance.StateDir = filepath.Join(root, "occupied-state-dir")
	if err := os.WriteFile(svc.cfg.Instance.StateDir, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(svc.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	Run([]string{"doctor", "--config", svc.configPath}, &out, &errb)
	if !strings.Contains(out.String(), "sync_service_configured_group_disabled") {
		t.Fatalf("doctor omitted disabled installed service with store unavailable: %s %s", out.String(), errb.String())
	}
}

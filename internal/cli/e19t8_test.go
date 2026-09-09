package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// e18t8Env prepares a hermetic systemd user-unit directory and pins the
// host posture platform to systemd for the managed lifecycle tests.
func e18t8Env(t *testing.T, mode string) string {
	t.Helper()
	configPath := e16t4Env(t, mode) // also pins launchAgentsDir / launchd platform
	// Override the e16t4Env launchd pin: these tests exercise systemd.
	hostSchedulePlatform = func() string { return "systemd" }
	dir := filepath.Dir(configPath)
	units := filepath.Join(dir, "systemd-user")
	savedUnits := systemdUserDir
	systemdUserDir = func() string { return units }
	t.Cleanup(func() { systemdUserDir = savedUnits })
	savedCtl := systemctlRun
	systemctlRun = func(args ...string) (string, error) {
		// Default fake: every call succeeds; is-enabled/is-active report
		// the timer as enabled/waiting after enable --now.
		joined := strings.Join(args, " ")
		if strings.HasPrefix(joined, "is-enabled ") {
			return "enabled\n", nil
		}
		if strings.HasPrefix(joined, "is-active ") {
			return "waiting\n", nil
		}
		return "", nil
	}
	t.Cleanup(func() { systemctlRun = savedCtl })
	return configPath
}

func TestE19T8RenderProducesValidSystemdUnits(t *testing.T) {
	for _, tc := range []struct {
		mode     string
		interval bool
	}{
		{"after-command", true},
		{"scheduled", false},
	} {
		configPath := e18t8Env(t, tc.mode)
		var out, errb bytes.Buffer
		markInvocationStart()
		if code := Run([]string{"schedule", "render", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb); code != 0 {
			t.Fatalf("%s render: %d %s", tc.mode, code, errb.String())
		}
		env := decodeEnvelope(t, &out)
		service, _ := env["service"].(string)
		timer, _ := env["timer"].(string)
		if service == "" || timer == "" {
			t.Fatalf("%s: render must carry service and timer text", tc.mode)
		}
		if !strings.Contains(service, "Type=oneshot") {
			t.Fatalf("%s: service must be oneshot: %s", tc.mode, service)
		}
		if !strings.Contains(service, "schedule") || !strings.Contains(service, "run") || !strings.Contains(service, "--route") {
			t.Fatalf("%s: ExecStart must invoke schedule run directly: %s", tc.mode, service)
		}
		if strings.Contains(service, "/bin/sh") || strings.Contains(service, "bash") {
			t.Fatalf("%s: service must never carry a shell chain", tc.mode)
		}
		if tc.interval {
			if !strings.Contains(timer, "OnUnitActiveSec=900") || !strings.Contains(timer, "OnActiveSec=900") {
				t.Fatalf("after-command recovery must use 900s interval: %s", timer)
			}
		} else {
			if !strings.Contains(timer, "OnCalendar=*-*-* 03:00:00") {
				t.Fatalf("scheduled mode must default to 03:00 OnCalendar: %s", timer)
			}
		}
		label, _ := env["label"].(string)
		if !strings.HasPrefix(label, "xyz.rootkernel.agent-dispatch.") {
			t.Fatalf("managed label shape: %q", label)
		}
		if sp, _ := env["service_path"].(string); !strings.HasSuffix(sp, label+".service") {
			t.Fatalf("service path must derive from the label: %v", sp)
		}
		if tp, _ := env["timer_path"].(string); !strings.HasSuffix(tp, label+".timer") {
			t.Fatalf("timer path must derive from the label: %v", tp)
		}
	}
}

func TestE19T8ScheduledAtOverride(t *testing.T) {
	configPath := e18t8Env(t, "scheduled")
	var out, errb bytes.Buffer
	markInvocationStart()
	if code := Run([]string{"schedule", "render", "--route", "wiki", "--platform", "systemd", "--at", "05:45", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("render: %d %s", code, errb.String())
	}
	env := decodeEnvelope(t, &out)
	timer, _ := env["timer"].(string)
	if !strings.Contains(timer, "OnCalendar=*-*-* 05:45:00") {
		t.Fatalf("--at must override OnCalendar: %s", timer)
	}
}

func TestE19T8InstallIdempotentAndConflicting(t *testing.T) {
	configPath := e18t8Env(t, "after-command")
	var out, errb bytes.Buffer
	markInvocationStart()
	if code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("install: %d %s", code, errb.String())
	}
	env := decodeEnvelope(t, &out)
	servicePath, _ := env["service_path"].(string)
	timerPath, _ := env["timer_path"].(string)
	if servicePath == "" || timerPath == "" {
		t.Fatalf("install must report unit paths: %v", env)
	}
	for _, path := range []string{servicePath, timerPath} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("managed unit must be owner-only: %s %v %v", path, info, err)
		}
	}
	rawService, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatal(err)
	}
	// Identical definition: idempotent.
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("idempotent install: %d %s", code, errb.String())
	}
	// Foreign definition at the service path is refused (exit 14).
	if err := os.WriteFile(servicePath, []byte(strings.Replace(string(rawService), "Type=oneshot", "Type=simple", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb)
	if code != 14 || !strings.Contains(errb.String(), "transition_invalid") {
		t.Fatalf("foreign definition must exit 14 transition_invalid, got %d: %s", code, errb.String())
	}
}

func TestE19T8DisablePreservesAndUninstallRemoves(t *testing.T) {
	configPath := e18t8Env(t, "scheduled")
	var out, errb bytes.Buffer
	markInvocationStart()
	if code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("install: %d %s", code, errb.String())
	}
	env := decodeEnvelope(t, &out)
	servicePath := env["service_path"].(string)
	timerPath := env["timer_path"].(string)
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "disable", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("disable: %d %s", code, errb.String())
	}
	for _, path := range []string{servicePath, timerPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("disable must preserve %s", path)
		}
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "uninstall", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("uninstall: %d %s", code, errb.String())
	}
	for _, path := range []string{servicePath, timerPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("uninstall must remove %s", path)
		}
	}
	// Foreign file at the same path is refused.
	if err := os.MkdirAll(filepath.Dir(servicePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(servicePath, []byte("[Service]\nType=oneshot\nExecStart=/bin/true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	code := Run([]string{"schedule", "uninstall", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb)
	if code != 14 {
		t.Fatalf("uninstall must refuse a foreign unit, got %d: %s", code, errb.String())
	}
	if _, err := os.Stat(servicePath); err != nil {
		t.Fatal("the foreign unit must survive the refusal")
	}
}

func TestE19T8InspectReportsHealth(t *testing.T) {
	configPath := e18t8Env(t, "after-command")
	// Absent: force is-enabled/is-active to report missing.
	systemctlRun = func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.HasPrefix(joined, "is-enabled ") || strings.HasPrefix(joined, "is-active ") {
			return "inactive\n", errors.New("not found")
		}
		return "", nil
	}
	var out, errb bytes.Buffer
	markInvocationStart()
	if code := Run([]string{"schedule", "inspect", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("inspect: %d %s", code, errb.String())
	}
	env := decodeEnvelope(t, &out)
	if env["present"] != false || env["healthy"] != false {
		t.Fatalf("an absent schedule is unhealthy: %v", env)
	}
	// Restore the healthy fake and install.
	systemctlRun = func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.HasPrefix(joined, "is-enabled ") {
			return "enabled\n", nil
		}
		if strings.HasPrefix(joined, "is-active ") {
			return "waiting\n", nil
		}
		return "", nil
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("install: %d %s", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "inspect", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("inspect: %d %s", code, errb.String())
	}
	env = decodeEnvelope(t, &out)
	if env["present"] != true || env["loaded"] != true || env["healthy"] != true {
		t.Fatalf("an installed loaded matching schedule is healthy: %v", env)
	}
}

func TestE19T8SystemdQuoteEscapesMetacharacters(t *testing.T) {
	if got := systemdQuote(`/usr/bin/agent-dispatch`); got != `/usr/bin/agent-dispatch` {
		t.Fatalf("plain path stays bare: %q", got)
	}
	if got := systemdQuote(`/tmp/path with spaces/bin`); !strings.HasPrefix(got, `"`) || !strings.Contains(got, `path with spaces`) {
		t.Fatalf("whitespace must quote: %q", got)
	}
	if got := systemdQuote(`/tmp/odd$path`); !strings.Contains(got, `\$`) {
		t.Fatalf("$ must be escaped inside quotes: %q", got)
	}
}

func TestE19T8LaunchdPathStillAccepted(t *testing.T) {
	// Regression: accepting systemd must not break the launchd render.
	configPath := e16t4Env(t, "after-command")
	var out, errb bytes.Buffer
	markInvocationStart()
	if code := Run([]string{"schedule", "render", "--route", "wiki", "--platform", "launchd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("launchd render must still succeed: %d %s", code, errb.String())
	}
	env := decodeEnvelope(t, &out)
	if _, ok := env["plist"].(string); !ok {
		t.Fatalf("launchd render must still carry plist: %v", env)
	}
}

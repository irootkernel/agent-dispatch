package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
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
	// Repeating uninstall after both files are absent is idempotent.
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "uninstall", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("absent uninstall: %d %s", code, errb.String())
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
	var calls []string
	systemctlRun = func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}
	markInvocationStart()
	code := Run([]string{"schedule", "uninstall", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb)
	if code != 14 {
		t.Fatalf("uninstall must refuse a foreign unit, got %d: %s", code, errb.String())
	}
	if _, err := os.Stat(servicePath); err != nil {
		t.Fatal("the foreign unit must survive the refusal")
	}
	if len(calls) != 0 {
		t.Fatalf("foreign ownership refusal must not call systemctl: %v", calls)
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
	if got := systemdQuote(`/usr/bin/agent-dispatch`); got != `"/usr/bin/agent-dispatch"` {
		t.Fatalf("plain path is one quoted argv token: %q", got)
	}
	if got := systemdQuote(`/tmp/path with spaces/bin`); !strings.HasPrefix(got, `"`) || !strings.Contains(got, `path with spaces`) {
		t.Fatalf("whitespace must quote: %q", got)
	}
	got := systemdQuote("/tmp/odd$path/%Z/back\\slash/\"quote\"/line\nfeed")
	for _, want := range []string{"$$path", "%%Z", `back\\slash`, `\"quote\"`, `line\nfeed`} {
		if !strings.Contains(got, want) {
			t.Fatalf("ExecStart token missing %q escape: %q", want, got)
		}
	}
	if strings.Contains(got, `\$`) {
		t.Fatalf("systemd does not accept \\$ as a C-style escape: %q", got)
	}
	value := systemdQuotedValue("append:/tmp/log $cash/%Z file")
	if !strings.Contains(value, "$cash") || strings.Contains(value, "$$cash") || !strings.Contains(value, "%%Z") {
		t.Fatalf("non-Exec values keep dollars literal and escape specifiers: %q", value)
	}
}

func TestE19T8SystemdTimerLoadedRequiresEnabledAndActive(t *testing.T) {
	saved := systemctlRun
	t.Cleanup(func() { systemctlRun = saved })
	cases := []struct {
		name, enabled, active string
		enabledErr, activeErr bool
		want                  bool
	}{
		{name: "healthy", enabled: "enabled\n", active: "waiting\n", want: true},
		{name: "enabled inactive", enabled: "enabled\n", active: "inactive\n"},
		{name: "disabled active", enabled: "disabled\n", active: "active\n"},
		{name: "enabled query error", enabledErr: true, active: "active\n"},
		{name: "active query error", enabled: "enabled\n", activeErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			systemctlRun = func(args ...string) (string, error) {
				switch args[0] {
				case "is-enabled":
					if tc.enabledErr {
						return "", errors.New("is-enabled failed")
					}
					return tc.enabled, nil
				case "is-active":
					if tc.activeErr {
						return "", errors.New("is-active failed")
					}
					return tc.active, nil
				default:
					return "", nil
				}
			}
			if got := systemdTimerLoaded("test"); got != tc.want {
				t.Fatalf("loaded=%v want %v", got, tc.want)
			}
		})
	}
}

func TestE19T8InstallPropagatesEnableFailure(t *testing.T) {
	configPath := e18t8Env(t, "after-command")
	systemctlRun = func(args ...string) (string, error) {
		if args[0] == "enable" {
			return "Failed to enable unit: File exists", errors.New("exit status 1")
		}
		return "", nil
	}
	var out, errb bytes.Buffer
	markInvocationStart()
	code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb)
	if code != 21 || out.Len() != 0 || !strings.Contains(errb.String(), "target_response_invalid") {
		t.Fatalf("enable failure must remain unknown at exit 21, got %d stdout=%s stderr=%s", code, out.String(), errb.String())
	}
}

func TestE19T8UninstallPreflightsCompletePair(t *testing.T) {
	configPath := e18t8Env(t, "after-command")
	var out, errb bytes.Buffer
	markInvocationStart()
	if code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("install: %d %s", code, errb.String())
	}
	env := decodeEnvelope(t, &out)
	servicePath := env["service_path"].(string)
	timerPath := env["timer_path"].(string)
	if err := os.WriteFile(timerPath, []byte("[Timer]\nOnCalendar=never\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	systemctlRun = func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	code := Run([]string{"schedule", "uninstall", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb)
	if code != 14 {
		t.Fatalf("foreign timer must be refused: %d %s", code, errb.String())
	}
	if len(calls) != 0 {
		t.Fatalf("preflight refusal must not call systemctl: %v", calls)
	}
	for _, path := range []string{servicePath, timerPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("preflight refusal changed %s: %v", path, err)
		}
	}
}

func TestE19T8UninstallHandlesMissingMemberAndDisableFailure(t *testing.T) {
	configPath := e18t8Env(t, "after-command")
	var out, errb bytes.Buffer
	markInvocationStart()
	if code := Run([]string{"schedule", "install", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("install: %d %s", code, errb.String())
	}
	env := decodeEnvelope(t, &out)
	servicePath := env["service_path"].(string)
	timerPath := env["timer_path"].(string)
	if err := os.Remove(timerPath); err != nil {
		t.Fatal(err)
	}
	systemctlRun = func(args ...string) (string, error) {
		if args[0] == "disable" {
			return "permission denied", errors.New("exit status 1")
		}
		return "", nil
	}
	out.Reset()
	errb.Reset()
	markInvocationStart()
	code := Run([]string{"schedule", "uninstall", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb)
	if code != 21 {
		t.Fatalf("disable failure must stop uninstall: %d %s", code, errb.String())
	}
	if _, err := os.Stat(servicePath); err != nil {
		t.Fatalf("disable failure removed the validated service: %v", err)
	}
	systemctlRun = func(args ...string) (string, error) { return "", nil }
	out.Reset()
	errb.Reset()
	markInvocationStart()
	if code := Run([]string{"schedule", "uninstall", "--route", "wiki", "--platform", "systemd", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("missing-member uninstall: %d %s", code, errb.String())
	}
	if _, err := os.Stat(servicePath); !os.IsNotExist(err) {
		t.Fatalf("missing-member uninstall did not remove exact survivor: %v", err)
	}
}

func TestE19T8RenderedHostilePathsPassSystemdAnalyze(t *testing.T) {
	tool, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("systemd-analyze not available")
	}
	label := "xyz.rootkernel.agent-dispatch.hostile"
	def := scheduleDefinition{
		Label:      label,
		BinaryPath: "/bin/true",
		RouteID:    `route $cash %Z \"quoted\"`,
		ConfigPath: `/tmp/config $cash %Z \"quoted\".yaml`,
		StdoutPath: "/tmp/output $cash %Z.log",
		StderrPath: "/tmp/error $cash %Z.log",
		Interval:   900,
	}
	service := renderScheduleService(def)
	timer := renderScheduleTimer(def)
	dir := t.TempDir()
	servicePath := filepath.Join(dir, label+".service")
	timerPath := filepath.Join(dir, label+".timer")
	if err := os.WriteFile(servicePath, []byte(service), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(timerPath, []byte(timer), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(tool, "verify", servicePath, timerPath).CombinedOutput(); err != nil {
		t.Fatalf("hostile rendered units failed systemd-analyze: %v: %s\nservice:\n%s", err, out, service)
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

package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestE8T4DoctorReportsUnreadableRoot proves H-4's access probe: a
// chmod-000 resource root produces the error-severity finding and a
// nonzero doctor exit — the stat-only probe passed it silently. Root
// bypasses directory permissions, so the probe cannot be exercised
// under it (E9-T4 confirmation observation).
func TestE8T4DoctorReportsUnreadableRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads through chmod-000 directories; the unreadable-root probe needs an unprivileged run")
	}
	configPath, vault := e4t3Fixture(t)
	if err := os.Chmod(vault, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(vault, 0o755) })
	var out, errb bytes.Buffer
	code := Run([]string{"doctor", "--config", configPath}, &out, &errb)
	if code != 3 || !strings.Contains(errb.String(), "doctor_findings_present") {
		t.Fatalf("an unreadable root must fail doctor at exit 3, got %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "resource_root_not_readable") {
		t.Fatalf("the unreadable-root finding must name the code: %s", out.String())
	}
}

// TestE8T4DoctorNeverFabricatesWatchman pins the round-1 F002 fix: a
// configuration that fails to load produces exactly the configuration
// finding — the unexamined Watchman surface never reports unavailable —
// while a loadable configuration's real probe result stands.
func TestE8T4DoctorNeverFabricatesWatchman(t *testing.T) {
	configPath, _ := e4t3Fixture(t)
	broken := configPath + ".broken"
	if err := os.WriteFile(broken, []byte("version: 1\ninstance:\n  id: x\nresources: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"doctor", "--config", broken}, &out, &errb)
	if code != 3 {
		t.Fatalf("a broken configuration must fail doctor, got %d: %s", code, errb.String())
	}
	if strings.Contains(out.String(), "watchman_unavailable") {
		t.Fatalf("the unexamined Watchman surface must not fabricate a finding: %s", out.String())
	}
	if !strings.Contains(out.String(), "config_invalid") {
		t.Fatalf("the configuration finding must stand: %s", out.String())
	}
}

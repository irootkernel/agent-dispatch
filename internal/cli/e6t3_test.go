package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
)

// TestCompletionEmitsCommandTree proves the E6-T3 completion deliverable:
// one static script per shell naming the v0.1 command tree, and usage
// failures for anything else.
func TestCompletionEmitsCommandTree(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		var out, errb bytes.Buffer
		code := Run([]string{"completion", shell}, &out, &errb)
		if code != 0 {
			t.Fatalf("completion %s failed: %s", shell, errb.String())
		}
		script := out.String()
		for _, name := range []string{"dispatch", "reconcile", "maintenance", "doctor", "status", "watchman"} {
			if !strings.Contains(script, name) {
				t.Fatalf("%s completion lacks %s: %s", shell, name, script)
			}
		}
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"completion", "fish"}, &out, &errb); code != 2 {
		t.Fatalf("unsupported shell must be a usage failure, got %d", code)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"completion"}, &out, &errb); code != 2 {
		t.Fatalf("missing shell must be a usage failure, got %d", code)
	}
}

// TestMaintenanceBackupWritesVerifiedSnapshot proves the backup
// deliverable: an owner-only verified snapshot that refuses to
// overwrite and whose copy is a valid standalone database.
func TestMaintenanceBackupWritesVerifiedSnapshot(t *testing.T) {
	// Root's CAP_DAC_OVERRIDE defeats chmod-based permission
	// expectations; the D-020 Linux record names this test (E8-T6).
	if os.Geteuid() == 0 {
		t.Skip("permission-expectation test defeats CAP_DAC_OVERRIDE under root (D-020)")
	}
	configPath, _ := cliStoreFixture(t)
	dir := t.TempDir()
	backup := filepath.Join(dir, "state-backup.db")
	var out, errb bytes.Buffer
	code := Run([]string{"maintenance", "backup", "--config", configPath, "--output", backup}, &out, &errb)
	if code != 0 {
		t.Fatalf("backup failed: %s", errb.String())
	}
	fi, err := os.Stat(backup)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("backup file missing or empty: %v", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("backup must be owner-only, got %v", fi.Mode().Perm())
	}
	snap, err := sqlite.Open(backup)
	if err != nil {
		t.Fatalf("backup does not open standalone: %v", err)
	}
	defer snap.Close()
	if err := snap.IntegrityCheck(false); err != nil {
		t.Fatalf("backup integrity failed: %v", err)
	}
	out.Reset()
	errb.Reset()
	code = Run([]string{"maintenance", "backup", "--config", configPath, "--output", backup}, &out, &errb)
	if code != 14 || !strings.Contains(errb.String(), "backup_target_exists") {
		t.Fatalf("backup must refuse to overwrite with the stable conflict code, got %d: %s", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	code = Run([]string{"maintenance", "backup", "--config", configPath}, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "--output") {
		t.Fatalf("missing --output must be a usage failure, got %d: %s", code, errb.String())
	}
	// A storage failure (unwritable target directory) leaves nothing
	// behind: the retry is not misclassified as a conflict.
	unwritable := filepath.Join(dir, "sealed", "backup.db")
	if err := os.Mkdir(filepath.Join(dir, "sealed"), 0o000); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	code = Run([]string{"maintenance", "backup", "--config", configPath, "--output", unwritable}, &out, &errb)
	if code != 20 {
		t.Fatalf("unwritable backup target must be a storage failure, got %d: %s", code, errb.String())
	}
	if _, err := os.Lstat(unwritable); err == nil {
		t.Fatalf("failed backup left an artifact behind")
	}
}

// TestCleanHostInstallationScenario proves the macOS clean-host
// acceptance: with a fresh HOME and no prior state, init creates the
// platform-default configuration and owner-only state directory, the
// configuration validates, and the store opens through the default
// paths.
func TestCleanHostInstallationScenario(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("AGENT_DISPATCH_CONFIG", "")
	t.Setenv("AGENT_DISPATCH_STATE_DIR", "")

	var out, errb bytes.Buffer
	code := Run([]string{"init", "--resource-root", filepath.Join(home, "Vault")}, &out, &errb)
	if code != 0 {
		t.Fatalf("init failed: %s", errb.String())
	}
	cfgPath := platformpaths.DefaultConfigPath()
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("default config not created at %s: %v", cfgPath, err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
	stateDir := platformpaths.ResolveStateDir(cfg.Instance.StateDir)
	fi, err := os.Stat(stateDir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("state directory not created: %v", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("state directory must be owner-only, got %v", fi.Mode().Perm())
	}
	store, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatalf("store does not open: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(filepath.Join(stateDir, "backups")); err != nil {
		t.Fatalf("migrations do not apply on a clean host: %v", err)
	}
	before, _ := os.ReadFile(cfgPath)
	out.Reset()
	errb.Reset()
	code = Run([]string{"init"}, &out, &errb)
	// Re-running init on an existing configuration is the documented
	// fail-closed refusal, and the file is preserved byte-for-byte.
	if code != 3 || !strings.Contains(errb.String(), "refuses to overwrite") {
		t.Fatalf("second init must refuse with the stable class, got %d: %s", code, errb.String())
	}
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Fatalf("init overwrote an existing configuration")
	}
}

// TestUninstallExampleRetainsStateAndConfig proves the acceptance line:
// the uninstall procedure has no automatic state or config deletion —
// the explicit flag only prints guidance, and the script's safety
// lines are pinned.
func TestUninstallExampleRetainsStateAndConfig(t *testing.T) {
	raw, err := os.ReadFile("../../docs/examples/scripts/agent-dispatch-uninstall.sh.example")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	for _, must := range []string{
		"watchman remove",
		"RETAINS the",
		"--purge-state",
		"does not delete it automatically",
	} {
		if !strings.Contains(script, must) {
			t.Fatalf("uninstall example lost its safety line %q", must)
		}
	}
	if strings.Contains(script, "rm -rf") {
		t.Fatalf("uninstall example must not recursively delete anything")
	}
}

// TestScheduleExamplesInvokeVerifiedCommands proves the schedule
// examples drive the verified reconcile command shape (OPS-006/007)
// and introduce no daemon. The invocations carry --submit (T2-F001,
// E9-T4): the runbook's automatic-recovery claim relies on the
// scheduled submit leg, and the two-key gate makes it safe before the
// production acknowledgement — a route that is not enabled in
// configuration persists decisions and recovers but submits nothing.
func TestScheduleExamplesInvokeVerifiedCommands(t *testing.T) {
	plist, err := os.ReadFile("../../docs/examples/scripts/agent-dispatch-reconcile.launchd.plist.example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plist), "<string>--submit</string>") {
		t.Fatalf("the launchd invocation must carry --submit")
	}
	if !strings.Contains(string(plist), "reconcile") || !strings.Contains(string(plist), "scheduled") {
		t.Fatalf("schedule artifact does not invoke the scheduled reconciliation: %.80s", plist)
	}
	if strings.Contains(string(plist), "/tmp") {
		t.Fatalf("launchd plist must not log to fixed /tmp paths")
	}
	// The systemd service and timer examples are retired with the
	// Linux packaging surface (D-023, E9-T8); the launchd recipe is
	// the supported scheduling artifact.
	var zout, zerr bytes.Buffer
	if code := Run([]string{"completion", "zsh"}, &zout, &zerr); code != 0 || !strings.HasPrefix(zout.String(), "#compdef agent-dispatch") {
		t.Fatalf("zsh completion lacks its header: %q", zout.String())
	}
}

// TestScheduleExamplesExistForMacOS guards the example set under the
// D-023 macOS-only policy (E9-T8): the launchd recipe and the
// uninstall script; the systemd examples are retired.
func TestScheduleExamplesExistForMacOS(t *testing.T) {
	for _, name := range []string{
		"agent-dispatch-reconcile.launchd.plist.example",
		"agent-dispatch-uninstall.sh.example",
	} {
		if _, err := os.Stat(filepath.Join("../../docs/examples/scripts", name)); err != nil {
			t.Fatalf("scheduling example %s missing: %v", name, err)
		}
	}
	// The retired Linux surface stays retired (D-023): the systemd
	// examples must remain deleted and the uninstall script must not
	// regress into systemctl guidance.
	for _, name := range []string{
		"agent-dispatch-reconcile.service.example",
		"agent-dispatch-reconcile.timer.example",
	} {
		if _, err := os.Stat(filepath.Join("../../docs/examples/scripts", name)); err == nil {
			t.Fatalf("retired systemd example %s must stay deleted under the D-023 macOS-only policy", name)
		}
	}
	body, err := os.ReadFile(filepath.Join("../../docs/examples/scripts", "agent-dispatch-uninstall.sh.example"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "systemctl") {
		t.Fatal("the uninstall script must not reference systemctl under the D-023 macOS-only policy")
	}
}

// TestCompletionMatchesRegisteredTree proves the emitted scripts name
// exactly the registered command tree: the bash word list is parsed
// back out and compared set-for-set with the registry, so an emission
// bug or a registry drift fails here.
func TestCompletionMatchesRegisteredTree(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"completion", "bash"}, &out, &errb); code != 0 {
		t.Fatalf("completion failed: %s", errb.String())
	}
	marker := "compgen -W \""
	start := strings.Index(out.String(), marker)
	if start < 0 {
		t.Fatalf("bash completion lacks the word list: %s", out.String())
	}
	rest := out.String()[start+len(marker):]
	end := strings.Index(rest, "\"")
	if end < 0 {
		t.Fatalf("bash completion word list is unterminated")
	}
	emitted := strings.Fields(rest[:end])
	if len(emitted) != len(knownCommands) {
		t.Fatalf("completion emits %d commands, registry has %d", len(emitted), len(knownCommands))
	}
	for _, name := range emitted {
		if !knownCommands[name] {
			t.Fatalf("completion emits unregistered command %q", name)
		}
	}
}

// TestMaintenanceBackupRefusesSymlinkTarget proves the Lstat guard: a
// symlink at the backup target — dangling or not — is the conflict, so
// the snapshot can never land through a link the backup path did not
// create, and the link itself is untouched.
func TestMaintenanceBackupRefusesSymlinkTarget(t *testing.T) {
	configPath, _ := cliStoreFixture(t)
	dir := t.TempDir()
	real := filepath.Join(dir, "real.db")
	if err := os.WriteFile(real, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"maintenance", "backup", "--config", configPath, "--output", link}, &out, &errb)
	if code != 14 || !strings.Contains(errb.String(), "backup_target_exists") {
		t.Fatalf("symlink target must be the stable conflict, got %d: %s", code, errb.String())
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("the symlink itself must be untouched: %v", err)
	}
	dangling := filepath.Join(dir, "dangling.db")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), dangling); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	code = Run([]string{"maintenance", "backup", "--config", configPath, "--output", dangling}, &out, &errb)
	if code != 14 {
		t.Fatalf("dangling symlink target must also be the conflict, got %d: %s", code, errb.String())
	}
}

// TestMaintenanceBackupLogsBackedUpEvent proves the dedicated
// maintenance.backed_up event carries the operation.
func TestMaintenanceBackupLogsBackedUpEvent(t *testing.T) {
	configPath, _ := cliStoreFixture(t)
	var out, errb bytes.Buffer
	code := Run([]string{"maintenance", "backup", "--config", configPath, "--output", filepath.Join(t.TempDir(), "b.db"), "--log-level", "info"}, &out, &errb)
	if code != 0 {
		t.Fatalf("backup failed: %s", errb.String())
	}
	if !strings.Contains(errb.String(), "maintenance.backed_up") {
		t.Fatalf("backed_up event missing from the log: %s", errb.String())
	}
	if strings.Contains(errb.String(), "maintenance.vacuumed") {
		t.Fatalf("backup must not emit the vacuum event: %s", errb.String())
	}
}

// TestReconcileAcceptsDocumentedOutputOption proves the scheduling
// examples' invocation shape runs: the documented global --output json
// is accepted by the reconcile command (the round-3 high finding).
func TestReconcileAcceptsDocumentedOutputOption(t *testing.T) {
	configPath, _ := cliStoreFixture(t)
	var out, errb bytes.Buffer
	code := Run([]string{"reconcile", "--route", "wiki", "--reason", "manual", "--config", configPath, "--output", "json"}, &out, &errb)
	if code == 2 || strings.Contains(errb.String(), "unknown argument") {
		t.Fatalf("reconcile must accept --output json: %s", errb.String())
	}
}

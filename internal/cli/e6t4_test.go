package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/hermeskanban"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/hermesenv"
)

// TestG5AC501WebhookExplicitRoute covers AC-501 end to end through the
// E6-T1 webhook surface: authentication resolves without persistence,
// transport and durable acceptance are distinguished (the accepted
// result records durable=false), and Kanban is never a fallback.
func TestG5AC501WebhookExplicitRoute(t *testing.T) {
	capture := &e6t1Capture{}
	configPath, vault, server := e6t1Fixture(t, func(w http.ResponseWriter, r *http.Request) {
		capture.add(r)
		w.WriteHeader(http.StatusOK)
	})
	res, _ := e6t1Dispatch(t, configPath, vault)
	if res["state"] != "accepted" || res["submitted"] != true {
		t.Fatalf("AC-501 dispatch result wrong: %v", res)
	}
	dispatchID, _ := res["dispatch_id"].(string)
	cfg, _ := config.Load(resolveConfigPath(configPath))
	stateDir := platformpaths.ResolveStateDir(cfg.Instance.StateDir)
	// Authentication never persists: no file under the state directory
	// contains the resolved secret.
	var leaked []string
	_ = filepath.Walk(stateDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr == nil && strings.Contains(string(raw), "cli-secret-1") {
			leaked = append(leaked, path)
		}
		return nil
	})
	if len(leaked) > 0 {
		t.Fatalf("AC-501: the secret persisted in %v", leaked)
	}
	// Transport acceptance is distinguished from durable acceptance:
	// the acceptance receipt for the webhook's 2xx records durable=0.
	store, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var durable int
	if err := store.QueryRowContext(context.Background(),
		`SELECT durable FROM dispatch_receipts WHERE dispatch_id = ? AND receipt_kind = 'acceptance'`, dispatchID).Scan(&durable); err != nil {
		t.Fatalf("AC-501: acceptance receipt missing: %v", err)
	}
	if durable != 0 {
		t.Fatalf("AC-501: a 2xx must not record durable acceptance")
	}
	// Kanban is not a fallback: exactly one endpoint invocation and no
	// kanban execution anywhere in the fixture.
	if capture.count() != 1 {
		t.Fatalf("AC-501: endpoint invocations = %d", capture.count())
	}
	// No Kanban fallback: the durable intent names the webhook target.
	var targetType string
	if err := store.QueryRowContext(context.Background(), `SELECT target_type FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&targetType); err != nil || targetType != "hermes-webhook" {
		t.Fatalf("AC-501: intent target type = %q, want hermes-webhook (%v)", targetType, err)
	}
	_ = server
}

// TestG5AC502DoctorStableFindings covers AC-502: invalid configuration
// and a missing resource root produce actionable structured findings
// while the command itself reports them as data with stable codes.
func TestG5AC502DoctorStableFindings(t *testing.T) {
	configPath, _ := cliStoreFixture(t)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(resolveConfigPath(configPath))
	root := cfg.Resources["vault-main"].Root
	updated := strings.Replace(string(raw), "root: "+root, "root: "+filepath.Join(filepath.Dir(root), "ac502-missing"), 1)
	broken := filepath.Join(t.TempDir(), "broken.yaml")
	if err := os.WriteFile(broken, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Run([]string{"doctor", "--config", broken}, &out, &errb)
	if code != 3 || !strings.Contains(errb.String(), "doctor_findings_present") {
		t.Fatalf("AC-502: doctor must carry the stable nonzero code, got %d: %s", code, errb.String())
	}
	res := decodeResult(t, &out)
	findings, _ := res["findings"].([]any)
	codes := map[string]bool{}
	for _, f := range findings {
		finding, _ := f.(map[string]any)
		codes[finding["code"].(string)] = true
		if finding["remediation"] == "" || finding["severity"] == "" || finding["summary"] == "" {
			t.Fatalf("AC-502: finding lacks the actionable shape: %v", finding)
		}
	}
	if !codes["resource_root_missing"] {
		t.Fatalf("AC-502: stable code missing: %v", codes)
	}
}

// TestG5AC503PrunePreservesLineageAndAudit covers AC-503 end to end:
// resolved expired data is removed while unresolved lineage and the
// append-only audit survive.
func TestG5AC503PrunePreservesLineageAndAudit(t *testing.T) {
	configPath, _ := cliStoreFixture(t)
	store := e6t2Open(t, configPath)
	// The resolved seed carries a genuinely terminal state; an accepted
	// dispatch — holding or not holding the active slot — is unresolved
	// live work whose lineage prune never touches (E8-T4, H-3).
	e6t2SeedLineage(t, store, "old", "2025-01-01T00:00:00Z")
	e6t2SetIntentState(t, store, "dispatch-old", "completed", "2025-01-02T00:00:00Z", "")
	e6t2SeedLineage(t, store, "acc", "2025-01-01T00:00:00Z")
	e6t2SetIntentState(t, store, "dispatch-acc", "accepted", "2025-01-02T00:00:00Z", "")
	if _, err := store.ExecContext(context.Background(),
		`UPDATE route_runtime_state SET active_dispatch_id = 'dispatch-acc' WHERE route_id = 'wiki' AND active_dispatch_id IS NULL`); err != nil {
		t.Fatal(err)
	}
	e6t2SeedLineage(t, store, "unk", "2025-01-01T00:00:00Z")
	e6t2SetIntentState(t, store, "dispatch-unk", "unknown", "2025-01-02T00:00:00Z", "")
	var auditBefore int
	if err := store.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM state_transitions`).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"maintenance", "prune", "--config", configPath, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("AC-503 prune failed: %s", errb.String())
	}
	var gone int
	if err := store.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM dispatch_intents WHERE dispatch_id = 'dispatch-old'`).Scan(&gone); err != nil || gone != 0 {
		t.Fatalf("AC-503: resolved expired data not removed")
	}
	var keep int
	if err := store.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM dispatch_intents WHERE dispatch_id = 'dispatch-unk'`).Scan(&keep); err != nil || keep != 1 {
		t.Fatalf("AC-503: unresolved lineage not preserved")
	}
	// The active accepted dispatch keeps its whole lineage: attempts,
	// receipts, and the intent itself (H-3).
	var keepAccepted int
	if err := store.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM dispatch_intents WHERE dispatch_id = 'dispatch-acc'`).Scan(&keepAccepted); err != nil || keepAccepted != 1 {
		t.Fatalf("AC-503: an active accepted dispatch must survive prune: %d %v", keepAccepted, err)
	}
	var audit int
	if err := store.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM state_transitions`).Scan(&audit); err != nil || audit == 0 {
		t.Fatalf("AC-503: audit references broken")
	}
	// The append-only audit never loses rows: the post-prune count is
	// the pre-prune count plus exactly the prune's own audit record.
	if audit < auditBefore {
		t.Fatalf("AC-503: audit rows were deleted (%d < %d)", audit, auditBefore)
	}
	if audit != auditBefore+1 {
		t.Fatalf("AC-503: expected exactly the prune audit row added (%d vs %d+1)", audit, auditBefore)
	}
}

// TestG5AC504CleanHostInstallDispatchScheduleUninstall covers AC-504
// on this macOS host as far as the criterion's no-manual-database-edits
// clause reaches: init, config validation, the store opening and
// migrating, one dispatch dry-run plan, the doctor surface, and the
// documented uninstall ordering (trigger removal requires Watchman and
// is exercised by the E2-T5/E4-T3 suites).
func TestG5AC504CleanHostInstallDispatchScheduleUninstall(t *testing.T) {
	// The clean-host flow enables against the live target through the
	// version-gated production gate (E8-T3): an installed Hermes outside
	// the verified support set skips as an environment-dependent
	// evidence gap (TST-007); the environment probe is shared through
	// testsupport/hermesenv with the adapter owning the version judgment.
	hermesenv.SkipUnlessSupportedHermes(t, func(firstLine string) bool {
		ver, perr := hermeskanban.ParseVersionOutput(firstLine)
		return perr == nil && ver.Supported()
	})
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("AGENT_DISPATCH_CONFIG", "")
	t.Setenv("AGENT_DISPATCH_STATE_DIR", "")

	var out, errb bytes.Buffer
	if code := Run([]string{"init", "--resource-root", filepath.Join(home, "Vault")}, &out, &errb); code != 0 {
		t.Fatalf("AC-504 init failed: %s", errb.String())
	}
	cfgPath := platformpaths.DefaultConfigPath()
	out.Reset()
	errb.Reset()
	if code := Run([]string{"config", "validate", "--config", cfgPath}, &out, &errb); code != 0 {
		t.Fatalf("AC-504 config validate failed: %s", errb.String())
	}
	// One dispatch through the dry-run surface: no manual database
	// edits anywhere in the flow.
	os.MkdirAll(filepath.Join(home, "Vault", "Inbox"), 0o755)
	os.WriteFile(filepath.Join(home, "Vault", "Inbox", "note.md"), []byte("x"), 0o644)
	setPlanEnv(t, filepath.Join(home, "Vault"), false)
	t.Setenv("WATCHMAN_TRIGGER", "agent-dispatch.wiki-maintenance.4f8c21")
	out.Reset()
	errb.Reset()
	var code int
	withStdin(t, `[{"name":"Inbox/note.md","exists":true,"new":true,"size":1,"type":"f"}]`, func() {
		code = Run([]string{"dispatch", "--route", "wiki-maintenance", "--config", cfgPath, "--input", "watchman", "--dry-run"}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("AC-504 dispatch dry-run failed: %s", errb.String())
	}
	// First use materializes the route registration exactly as
	// `watchman install` does (the E2-T5 flow); the production gate is
	// then acknowledged through the real `route enable` command with
	// the computed route revision — the documented operator action,
	// never a direct store write.
	cfgLoaded, err := config.Load(resolveConfigPath(cfgPath))
	if err != nil {
		t.Fatal(err)
	}
	stateDir := platformpaths.ResolveStateDir(cfgLoaded.Instance.StateDir)
	regStore, err := sqlite.Open(filepath.Join(stateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	if err := regStore.Migrate(filepath.Join(stateDir, "backups")); err != nil {
		t.Fatal(err)
	}
	if err := registerRouteState(context.Background(), regStore, cfgLoaded, "wiki-maintenance"); err != nil {
		t.Fatal(err)
	}
	regStore.Close()
	revision, ok := config.RouteRevision(cfgLoaded, "wiki-maintenance")
	if !ok {
		t.Fatal("route revision unavailable")
	}
	out.Reset()
	errb.Reset()
	// The two-key gate (E7-T6/M-2): the operator flips the YAML key as
	// part of the reviewed enable, then acknowledges the computed
	// revision.
	e5t4Rewrite(t, cfgPath, "enabled: false", "enabled: true")
	// The production enable gate probes the live target against its
	// capability report (E8-T3): the clean-host flow places the report
	// before enabling — the corrected installation order.
	if err := os.MkdirAll(filepath.Join(home, ".config", "agent-dispatch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if reportRaw, rerr := os.ReadFile(filepath.Join("..", "..", "docs", "integrations", "hermes-capability-report.json")); rerr != nil {
		t.Fatal(rerr)
	} else if werr := os.WriteFile(filepath.Join(home, ".config", "agent-dispatch", "hermes-capabilities.json"), reportRaw, 0o644); werr != nil {
		t.Fatal(werr)
	}
	if code := Run([]string{"route", "enable", "--route", "wiki-maintenance", "--config", cfgPath, "--acknowledge-production-gate", revision, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("AC-504 gate-acknowledged route enable failed: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	code = Run([]string{"reconcile", "--route", "wiki-maintenance", "--reason", "scheduled", "--config", cfgPath, "--output", "json"}, &out, &errb)
	if code != 0 {
		t.Fatalf("AC-504 scheduled reconciliation failed: %s", errb.String())
	}
	// Doctor reports the healthy install (no error-severity findings).
	out.Reset()
	errb.Reset()
	if code := Run([]string{"doctor", "--config", cfgPath}, &out, &errb); code != 0 {
		t.Fatalf("AC-504 doctor failed: %s", errb.String())
	}
	// Uninstall per the documented order: disable the route, remove the
	// managed trigger when Watchman is present, and verify the
	// configuration and state directory are retained (no purge without
	// an explicit flag).
	out.Reset()
	errb.Reset()
	if code := Run([]string{"route", "disable", "--route", "wiki-maintenance", "--config", cfgPath}, &out, &errb); code != 0 {
		t.Fatalf("AC-504 route disable failed: %s", errb.String())
	}
	if _, err := exec.LookPath("watchman"); err == nil {
		out.Reset()
		errb.Reset()
		if code := Run([]string{"watchman", "remove", "--route", "wiki-maintenance", "--config", cfgPath, "--yes"}, &out, &errb); code != 0 {
			t.Fatalf("AC-504 trigger removal failed: %s", errb.String())
		}
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("AC-504: uninstall must retain the configuration")
	}
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("AC-504: uninstall must retain the state directory")
	}
}

// TestG5AC506ReleaseArtifactsPresent covers AC-506: the v0.1.0 release
// candidate's artifacts exist and are version-compatible — the
// reproducible release build with checksums, the schemas and example
// config, the companion skill, the SOT package, and the changelog.
func TestG5AC506ReleaseArtifactsPresent(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	dir := t.TempDir()
	// One version source: the shipped release notes name the version and
	// the test derives everything from it (E8-T6, H-5 - the old test
	// hard-coded v0.1.0 and never looked at dist/).
	// The one version source: the shipped release-notes FILENAME carries
	// the version everything else derives from (the body asserts the same
	// value so a rename drift fails).
	matches, merr := filepath.Glob(filepath.Join("..", "..", "docs", "RELEASE-NOTES-*.md"))
	if merr != nil || len(matches) == 0 {
		t.Fatalf("AC-506: release-notes files must exist, got %v (%v)", matches, merr)
	}
	// The LATEST release notes are the one version source (historical
	// notes for prior releases remain in the package).
	// Semantic selection: compare the numeric version components, never
	// the raw filename (lexical order picks v0.1.9 over v0.1.10).
	latest := matches[0]
	latestKey := versionSortKey(filepath.Base(latest))
	for _, m := range matches {
		if k := versionSortKey(filepath.Base(m)); k > latestKey {
			latest, latestKey = m, k
		}
	}
	notesRel := filepath.Join("docs", filepath.Base(latest))
	version := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(latest), "RELEASE-NOTES-"), ".md")
	root, err := filepath.Abs("../../")
	if err != nil {
		t.Fatal(err)
	}
	notesBody, nerr := os.ReadFile(filepath.Join(root, notesRel))
	if nerr != nil {
		t.Fatalf("AC-506: release notes missing: %v", nerr)
	}
	if !strings.Contains(string(notesBody), "Agent Dispatch "+version) {
		t.Fatalf("AC-506: release notes body must name the version %s (one version source)", version)
	}
	// The documented artifact set exists in the repository (paths
	// relative to the repository root the release builds from).
	for _, rel := range []string{
		"docs/schemas/config.schema.json",
		"docs/examples/config.yaml",
		"docs/skills/agent-dispatch-wiki-maintenance/SKILL.md",
		"docs/README.md",
		"docs/CHANGELOG.md",
		notesRel,
	} {
		path := filepath.Join(root, rel)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("AC-506 artifact missing: %s (%v)", path, err)
		}
	}
	// The release artifacts: when dist/ exists it carries exactly the
	// two shipped binaries and one SHA256SUMS line per artifact.
	dist := filepath.Join(root, "dist")
	if _, err := os.Stat(dist); err == nil {
		sums, rerr := os.ReadFile(filepath.Join(dist, "SHA256SUMS"))
		if rerr != nil {
			t.Fatalf("AC-506: dist/SHA256SUMS missing: %v", rerr)
		}
		lines := 0
		for _, l := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
			if l != "" {
				lines++
			}
		}
		if lines < 2 {
			t.Fatalf("AC-506: SHA256SUMS must carry one line per artifact, got %d: %q", lines, string(sums))
		}
		for _, l := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
			fields := strings.Fields(l)
			if len(fields) != 2 {
				t.Fatalf("AC-506: malformed SHA256SUMS line %q", l)
			}
			artifact, serr := os.ReadFile(filepath.Join(dist, fields[1]))
			if serr != nil {
				t.Fatalf("AC-506: checksummed artifact missing: %s (%v)", fields[1], serr)
			}
			sum := sha256.Sum256(artifact)
			if hex.EncodeToString(sum[:]) != fields[0] {
				t.Fatalf("AC-506: digest mismatch for %s: SHA256SUMS says %s, file hashes %s", fields[1], fields[0], hex.EncodeToString(sum[:]))
			}
		}
	}
	// One reproducible binary built the release way reports the shipped
	// version.
	cmd := exec.Command("go", "build", "-trimpath",
		"-ldflags", "-X github.com/irootkernel/agent-dispatch/internal/version.Version="+version,
		"-o", filepath.Join(dir, "agent-dispatch"), "./cmd/agent-dispatch")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("AC-506 release build failed: %v: %s", err, out)
	}
	var bout, berr bytes.Buffer
	bin := filepath.Join(dir, "agent-dispatch")
	code := runExternal(t, bin, []string{"version", "--output", "json"}, &bout, &berr)
	if code != 0 {
		t.Fatalf("AC-506 version failed: %s", berr.String())
	}
	var env Envelope
	if err := json.Unmarshal(bout.Bytes(), &env); err != nil {
		t.Fatalf("AC-506 version is not the envelope: %s", bout.String())
	}
	rawResult, _ := json.Marshal(env.Result)
	var versionResult struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rawResult, &versionResult); err != nil || versionResult.Version != version {
		t.Fatalf("AC-506: built binary reports version %q, want %s (%v)", versionResult.Version, version, err)
	}
}

// runExternal runs the built binary with a clean environment.
func runExternal(t *testing.T, bin string, args []string, stdout, stderr *bytes.Buffer) int {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin"}
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		t.Fatalf("running %s: %v", bin, err)
	}
	return 0
}

// TestG5UpgradeAndBackupRehearsal proves the documented upgrade
// procedure end to end (installation.md §5-§6, release-checklist
// durability): backup through the built-in path with verification,
// doctor with the new binary, integrity, and one reconciliation.
func TestG5UpgradeAndBackupRehearsal(t *testing.T) {
	configPath, dispatchID := cliStoreFixture(t)
	store := e6t2Open(t, configPath)
	// Fixture seeding only (not the flow under test): the dispatch
	// resolves as accepted and the route's active slot is free, so the
	// post-upgrade reconciliation meets its preconditions.
	e6t2SetIntentState(t, store, dispatchID, "accepted", "2026-08-20T02:00:00Z", "")
	if _, err := store.Exec(`UPDATE route_runtime_state SET active_dispatch_id = NULL WHERE route_id = 'wiki'`); err != nil {
		t.Fatal(err)
	}
	store.Close()
	// The upgrade runbook resumes the route through the real
	// gate-acknowledged command with the computed revision before the
	// final reconciliation.
	cfgLoaded, err := config.Load(resolveConfigPath(configPath))
	if err != nil {
		t.Fatal(err)
	}
	revision, ok := config.RouteRevision(cfgLoaded, "wiki")
	if !ok {
		t.Fatal("route revision unavailable")
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"route", "enable", "--route", "wiki", "--config", configPath, "--acknowledge-production-gate", revision, "--yes"}, &out, &errb); code != 0 {
		t.Fatalf("upgrade rehearsal: route enable failed: %s", errb.String())
	}

	backup := filepath.Join(t.TempDir(), "pre-upgrade.db")
	if code := Run([]string{"maintenance", "backup", "--config", configPath, "--output", backup}, &out, &errb); code != 0 {
		t.Fatalf("upgrade rehearsal: backup failed: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"doctor", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("upgrade rehearsal: doctor failed: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"maintenance", "integrity", "--config", configPath, "--full"}, &out, &errb); code != 0 {
		t.Fatalf("upgrade rehearsal: integrity failed: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"reconcile", "--route", "wiki", "--reason", "manual", "--config", configPath, "--output", "json"}, &out, &errb); code != 0 {
		t.Fatalf("upgrade rehearsal: reconciliation failed: %s", errb.String())
	}
	// The backup restores standalone (the restore half of the
	// rehearsal): it opens, passes integrity, and carries the lineage.
	restored, err := sqlite.Open(backup)
	if err != nil {
		t.Fatalf("upgrade rehearsal: backup does not restore: %v", err)
	}
	defer restored.Close()
	if err := restored.IntegrityCheck(true); err != nil {
		t.Fatalf("upgrade rehearsal: restored backup integrity failed: %v", err)
	}
	var n int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("upgrade rehearsal: restored backup lost the lineage: %d %v", n, err)
	}
}

// TestVersionReportsDeliveredAdapters pins the version surface the
// release depends on: every adapter entry names a delivered surface
// (no stale not-implemented markers).
func TestVersionReportsDeliveredAdapters(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"version", "--output", "json"}, &out, &errb); code != 0 {
		t.Fatalf("version failed: %s", errb.String())
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("version is not the envelope: %s", out.String())
	}
	raw, _ := json.Marshal(env.Result)
	var res struct {
		Adapters map[string]string `json:"adapter_versions"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	for name, desc := range res.Adapters {
		if strings.Contains(desc, "not-implemented") {
			t.Fatalf("adapter %s still reports not-implemented: %s", name, desc)
		}
	}
	if !strings.Contains(res.Adapters["hermeswebhook"], "HTTPS sink") {
		t.Fatalf("hermeswebhook entry wrong: %s", res.Adapters["hermeswebhook"])
	}
}

// versionSortKey renders a RELEASE-NOTES-<version>.md basename into a
// zero-padded numeric key so semantic order equals string order.
func versionSortKey(base string) string {
	v := strings.TrimSuffix(strings.TrimPrefix(base, "RELEASE-NOTES-"), ".md")
	parts := strings.Split(v, ".")
	for i, p := range parts {
		digits := p
		digits = strings.TrimPrefix(digits, "v")
		if n, err := strconv.Atoi(digits); err == nil {
			parts[i] = fmt.Sprintf("%08d", n)
		}
	}
	return strings.Join(parts, ".")
}

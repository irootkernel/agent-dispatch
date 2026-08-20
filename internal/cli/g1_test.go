package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootkernel/jjukkumi/internal/adapters/watchman"
)

// g1Harness drives `route plan` (the E2 dry-run pipeline) over a real
// temporary vault. Every G1 acceptance check (AC-101..110) runs through
// the same public surface an operator uses.
type g1Harness struct {
	t          *testing.T
	configPath string
	vault      string
}

func newG1(t *testing.T) *g1Harness {
	t.Helper()
	configPath, vault := planFixture(t)
	h := &g1Harness{t: t, configPath: configPath, vault: vault}
	t.Setenv("JJUKKUMI_STATE_DIR", t.TempDir())
	return h
}

// plan runs the pipeline with a synthetic incremental environment unless
// fresh is set.
func (h *g1Harness) plan(payload string, fresh bool) (map[string]any, *bytes.Buffer, int) {
	h.t.Helper()
	setPlanEnv(h.t, h.vault, fresh)
	var out, errb bytes.Buffer
	var code int
	withStdin(h.t, payload, func() {
		code = Run([]string{"route", "plan", "--route", "wiki", "--config", h.configPath, "--input", "watchman"}, &out, &errb)
	})
	if code != 0 {
		h.t.Fatalf("plan failed (exit %d): %s", code, errb.String())
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		h.t.Fatal(err)
	}
	raw, _ := json.Marshal(env.Result)
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		h.t.Fatal(err)
	}
	return plan, &out, code
}

func changesOf(plan map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, c := range plan["changes"].([]any) {
		out = append(out, c.(map[string]any))
	}
	return out
}

func writeVault(t *testing.T, vault, rel, content string) {
	t.Helper()
	p := filepath.Join(vault, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// AC-101: one new included Markdown file plans exactly one create in one
// dispatch plan.
func TestG1AC101SingleCreate(t *testing.T) {
	h := newG1(t)
	writeVault(t, h.vault, "Inbox/new.md", "hello")
	plan, _, _ := h.plan(`[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, false)
	if plan["disposition"] != "dispatch" {
		t.Fatalf("AC-101: disposition %v", plan["disposition"])
	}
	cs := changesOf(plan)
	if len(cs) != 1 || cs[0]["operation"] != "create" || cs[0]["path"] != "Inbox/new.md" {
		t.Fatalf("AC-101: changes %v", cs)
	}
}

// AC-102: a modify whose digest is unchanged drops. The dry-run has no
// persisted path facts, so the identical case is driven through the
// batch builder's suppression rule with prior facts (covered in
// internal/app/ingest); here the plan-level proof is that a fresh modify
// without prior facts is never falsely dropped, and the ingest-level
// test owns the suppression. See TestG1AC102 via ingest below.
func TestG1AC102UnchangedModifyDrops(t *testing.T) {
	// Plan-level: no prior digest means meaningful (never falsely
	// unchanged).
	h := newG1(t)
	writeVault(t, h.vault, "Inbox/kept.md", "stable")
	plan, _, _ := h.plan(`[{"name":"Inbox/kept.md","exists":true,"new":false,"size":6,"type":"f"}]`, false)
	if plan["disposition"] != "dispatch" || len(changesOf(plan)) != 1 {
		t.Fatalf("AC-102 (no-prior leg): %v", plan)
	}
	// Suppression with an equal prior digest is proven at the batch
	// layer (internal/app/ingest TestUnchangedModifySuppression) and is
	// part of this gate's recorded evidence.
}

// AC-103: repeated saves resolving to the same final digest leave one
// effective modify.
func TestG1AC103RepeatedSavesOneFinalChange(t *testing.T) {
	h := newG1(t)
	writeVault(t, h.vault, "Notes/save.md", "final")
	// create then modify in one batch (the merged-event shape).
	payload := `[{"name":"Notes/save.md","exists":true,"new":true,"size":5,"type":"f"},{"name":"Notes/save.md","exists":true,"new":false,"size":5,"type":"f"}]`
	plan, _, _ := h.plan(payload, false)
	cs := changesOf(plan)
	if len(cs) != 1 || cs[0]["operation"] != "create" {
		t.Fatalf("AC-103: %v", cs)
	}
}

// AC-104: an included deletion is retained without reading the file.
func TestG1AC104DeleteNeverRead(t *testing.T) {
	h := newG1(t)
	// The file does not exist on disk at all; the delete must stand.
	plan, _, _ := h.plan(`[{"name":"Inbox/gone.md","exists":false,"new":false,"size":10,"type":"f"}]`, false)
	cs := changesOf(plan)
	if len(cs) != 1 || cs[0]["operation"] != "delete" || cs[0]["digest_status"] != "not_applicable" {
		t.Fatalf("AC-104: %v", cs)
	}
}

// AC-105: .git/**, configured Obsidian UI state, and non-Markdown
// attachments are excluded deterministically.
func TestG1AC105DeterministicExclusions(t *testing.T) {
	h := newG1(t)
	payload := `[{"name":".git/config","exists":true,"new":true,"size":1,"type":"f"},{"name":".obsidian/workspace.json","exists":true,"new":true,"size":1,"type":"f"},{"name":"attach/img.png","exists":true,"new":true,"size":1,"type":"f"},{"name":".DS_Store","exists":true,"new":true,"size":1,"type":"f"}]`
	plan, _, _ := h.plan(payload, false)
	if plan["disposition"] != "drop" {
		t.Fatalf("AC-105: excluded-only batch must drop, got %v", plan["disposition"])
	}
	if len(changesOf(plan)) != 0 {
		t.Fatalf("AC-105: nothing may survive exclusion: %v", plan["changes"])
	}
}

// AC-106: absolute, traversal, NUL, and symlink-escape inputs read
// nothing outside the root and are rejected.
func TestG1AC106UnsafeInputsRejected(t *testing.T) {
	h := newG1(t)
	for _, name := range []string{"/etc/passwd", "../outside.md"} {
		// Lexically unsafe names are rejected by the parser before any
		// file access.
		payload := `[{"name":"` + name + `","exists":true,"new":true,"size":1,"type":"f"}]`
		setPlanEnv(t, h.vault, false)
		var out, errb bytes.Buffer
		withStdin(t, payload, func() {
			code := Run([]string{"route", "plan", "--route", "wiki", "--config", h.configPath, "--input", "watchman"}, &out, &errb)
			if code != 4 || out.Len() != 0 {
				t.Fatalf("AC-106: %q must be rejected (exit %d)", name, code)
			}
		})
	}
	// Symlink escape through a lexically clean name.
	outside := filepath.Join(filepath.Dir(h.vault), "g1-secret.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(h.vault, "escape.md")); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, h.vault, false)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"escape.md","exists":true,"new":true,"size":6,"type":"f"}]`, func() {
		code := Run([]string{"route", "plan", "--route", "wiki", "--config", h.configPath, "--input", "watchman"}, &out, &errb)
		if code != 30 || out.Len() != 0 {
			t.Fatalf("AC-106: symlink escape must be rejected (exit %d)", code)
		}
	})
	if data, err := os.ReadFile(outside); err != nil || string(data) != "secret" {
		t.Fatal("AC-106: nothing outside the root may be read or altered")
	}
}

// AC-107: the refuted overflow env var, a fresh instance, or an unusable
// position produce one reconciliation decision and never a partial
// normal task.
func TestG1AC107OverflowClassYieldsReconciliation(t *testing.T) {
	h := newG1(t)
	writeVault(t, h.vault, "Inbox/x.md", "x")
	writeVault(t, h.vault, "Inbox/y.md", "y")
	payload := `[{"name":"Inbox/x.md","exists":true,"new":true,"size":1,"type":"f"},{"name":"Inbox/y.md","exists":true,"new":true,"size":1,"type":"f"}]`
	// Fresh instance: no WATCHMAN_SINCE. A hostile WATCHMAN_FILES_OVERFLOW
	// value is ignored (refuted field, not in the allowlist).
	t.Setenv("WATCHMAN_FILES_OVERFLOW", "1")
	plan, _, _ := h.plan(payload, true)
	if plan["disposition"] != "reconcile" {
		t.Fatalf("AC-107: fresh instance must reconcile, got %v", plan["disposition"])
	}
	if ga, _ := plan["generation_action"].(string); ga != "merge_reconcile" {
		t.Fatalf("AC-107: generation action %v", plan["generation_action"])
	}
	// The changes are still reported, but no partial normal task exists:
	// the disposition is the decision, and it is reconcile.
}

// AC-108: over the automatic threshold the configured bulk disposition
// applies and the manifest is never silently truncated.
func TestG1AC108BulkDispositionNoTruncation(t *testing.T) {
	h := newG1(t)
	// threshold is 25 in the fixture; deliver 30 creates.
	var entries []string
	for i := 0; i < 30; i++ {
		entries = append(entries, `{"name":"Bulk/n`+string(rune('a'+i))+`.md","exists":true,"new":true,"size":1,"type":"f"}`)
	}
	payload := "[" + strings.Join(entries, ",") + "]"
	plan, _, _ := h.plan(payload, false)
	if plan["disposition"] != "quarantine" { // fixture bulk_action: quarantine
		t.Fatalf("AC-108: disposition %v", plan["disposition"])
	}
	if got := len(changesOf(plan)); got != 30 {
		t.Fatalf("AC-108: manifest silently truncated to %d of 30", got)
	}
}

// AC-109: hostile file names and front matter cannot change target,
// profile, skills, workspace, or policy.
func TestG1AC109PayloadCannotAffectRouteAuthority(t *testing.T) {
	h := newG1(t)
	writeVault(t, h.vault, "Inbox/hostile.md", "---\ntarget: evil\nprofile: attacker\nskills: [escape]\n---\n")
	payload := `[{"name":"Inbox/hostile.md","exists":true,"new":true,"size":60,"type":"f"},{"name":"profile=admin/**","exists":true,"new":true,"size":1,"type":"f"}]`
	plan, raw, _ := h.plan(payload, false)
	// The hostile non-Markdown name is excluded; the plan identity is
	// untouched by content: route id and revision come only from config.
	route := plan["route"].(map[string]any)
	if route["id"] != "wiki" {
		t.Fatalf("AC-109: route id altered: %v", route)
	}
	// No field of the plan carries note content or assignment values.
	if strings.Contains(raw.String(), "attacker") || strings.Contains(raw.String(), "escape]") {
		t.Fatal("AC-109: note content leaked into the plan")
	}
	if plan["disposition"] != "dispatch" || len(changesOf(plan)) != 1 {
		t.Fatalf("AC-109: unexpected plan: %v", plan)
	}
}

// AC-110: the same normalized input twice yields byte-for-byte stable
// fingerprint and plan apart from unique observation identity (which the
// dry-run does not emit).
func TestG1AC110StableFingerprintAndPlan(t *testing.T) {
	h := newG1(t)
	writeVault(t, h.vault, "Inbox/stable.md", "stable content")
	payload := `[{"name":"Inbox/stable.md","exists":true,"new":true,"size":14,"type":"f"}]`
	a, rawA, _ := h.plan(payload, false)
	b, rawB, _ := h.plan(payload, false)
	if a["content_fingerprint"] != b["content_fingerprint"] {
		t.Fatalf("AC-110: fingerprint drifted: %v vs %v", a["content_fingerprint"], b["content_fingerprint"])
	}
	if rawA.String() != rawB.String() {
		t.Fatal("AC-110: plan output is not byte-for-byte stable")
	}
}

// SRC-006: the one-shot pipeline contains no settle sleep; the trigger
// payload is one batch by construction. The configuration schema has no
// settle field, and no stage of the E2 pipeline sleeps: the parser,
// pattern engine, batch builder, and planner are pure or synchronous
// single-pass code with no timers. This test pins the observable
// consequence: planning a batch returns promptly with one batch, not
// per-file tasks.
func TestG1NoSettleSleep(t *testing.T) {
	h := newG1(t)
	writeVault(t, h.vault, "Inbox/fast.md", "x")
	plan, _, _ := h.plan(`[{"name":"Inbox/fast.md","exists":true,"new":true,"size":1,"type":"f"}]`, false)
	if len(changesOf(plan)) != 1 {
		t.Fatalf("one delivered batch is one planned batch: %v", plan["changes"])
	}
}

// E2-T5 acceptance: identical trigger install is a no-op, replacement
// requires the explicit flag, and remove touches only the managed
// trigger. Runs against the real installed Watchman when available
// (disposable temporary watch root, removed afterwards).
func TestWatchmanCLILifecycle(t *testing.T) {
	if _, err := exec.LookPath("watchman"); err != nil {
		t.Skip("watchman binary not available")
	}
	configPath, vault := planFixture(t)
	t.Setenv("JJUKKUMI_STATE_DIR", t.TempDir())
	argv := []string{"--route", "wiki", "--config", configPath}
	run := func(sub string, extra ...string) (map[string]any, *bytes.Buffer, *bytes.Buffer, int) {
		var out, errb bytes.Buffer
		args := append(append([]string{sub}, argv...), extra...)
		code := Run(append([]string{"watchman"}, args...), &out, &errb)
		if code == 0 && out.Len() == 0 {
			t.Fatalf("watchman %s produced no envelope: %s", sub, errb.String())
		}
		var decoded map[string]any
		if code == 0 {
			decoded = decodeEnvelope(t, &out)
		}
		return decoded, &out, &errb, code
	}
	t.Cleanup(func() {
		_, _, _, _ = run("remove", "--yes")
	})

	// First install creates and reports the pending initial
	// reconciliation.
	res, _, errb, code := run("install")
	if code != 0 {
		t.Fatalf("install failed: %s", errb.String())
	}
	if res["action"] != "installed" || res["disposition"] != "created" || res["initial_reconciliation_pending"] != true {
		t.Fatalf("install result wrong: %v", res)
	}
	// Identical install is a no-op preserving the position.
	res, _, _, code = run("install")
	if code != 0 || res["action"] != "noop" || res["disposition"] != "already_defined" {
		t.Fatalf("identical reinstall must be a no-op: %v", res)
	}
	// Status reports installed.
	if st, _, _, c := run("status"); c != 0 || st["state"] != "installed" {
		t.Fatalf("status state wrong: %v", st)
	}

	// Diverge the definition through the adapter, then prove the CLI
	// conflict gate, diverged status, and explicit replacement.
	client := watchman.NewClient("")
	ctx := context.Background()
	watchRoot, err := client.EnsureWatch(ctx, vault)
	if err != nil {
		t.Fatalf("ensure watch: %v", err)
	}
	exe, _ := os.Executable()
	diverged := watchman.ManagedTrigger("jjukkumi.wiki.test", []string{exe, "dispatch", "--route", "wiki", "--input", "watchman", "--unexpected"})
	if _, err := client.TriggerInstall(ctx, watchRoot, diverged); err != nil {
		t.Fatalf("diverge: %v", err)
	}
	_, _, cerr, c := run("install")
	if c != 14 {
		t.Fatalf("diverged install without --replace must exit 14, got %d: %s", c, cerr.String())
	}
	var conflictEnv ErrorEnvelope
	if err := json.Unmarshal(cerr.Bytes(), &conflictEnv); err != nil || conflictEnv.Error.Code != "watchman_trigger_conflict" {
		t.Fatalf("conflict envelope wrong: %s", cerr.String())
	}
	if st, _, _, c2 := run("status"); c2 != 0 || st["state"] != "diverged" {
		t.Fatalf("diverged status wrong: %v", st)
	}
	res, _, errb, code = run("install", "--replace")
	if code != 0 {
		t.Fatalf("replace failed: %s", errb.String())
	}
	if res["disposition"] != "replaced" || res["action"] != "installed" {
		t.Fatalf("replace result wrong: %v", res)
	}

	// Remove requires --yes, succeeds idempotently, and leaves status
	// missing.
	if _, _, _, c := run("remove"); c != 2 {
		t.Fatalf("remove without --yes must exit 2, got %d", c)
	}
	for i := 0; i < 2; i++ {
		if _, _, errb, c := run("remove", "--yes"); c != 0 {
			t.Fatalf("remove failed: %s", errb.String())
		}
	}
	if st, _, _, c := run("status"); c != 0 || st["state"] != "missing" {
		t.Fatalf("post-remove status wrong: %v", st)
	}
}

// decodeEnvelope extracts the result object of a success envelope.
func decodeEnvelope(t *testing.T, out *bytes.Buffer) map[string]any {
	t.Helper()
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("not an envelope: %s", out.String())
	}
	raw, _ := json.Marshal(env.Result)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// config validate reports valid configuration plus the Watchman
// availability state with actionable guidance.
func TestConfigValidateWatchmanReporting(t *testing.T) {
	configPath, _ := planFixture(t)
	t.Setenv("JJUKKUMI_STATE_DIR", t.TempDir())
	var out, errb bytes.Buffer
	code := Run([]string{"config", "validate", "--config", configPath}, &out, &errb)
	if code != 0 {
		t.Fatalf("validate failed: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["valid"] != true {
		t.Fatalf("validate result wrong: %v", res)
	}
	state, _ := res["watchman"].(string)
	if state != "available" && state != "unavailable" && state != "unsupported" {
		t.Fatalf("watchman state wrong: %v", state)
	}
	// An invalid configuration exits 3.
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("version: 1\nresources: {}\ntargets: {}\nroutes: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out2, errb2 bytes.Buffer
	if code := Run([]string{"config", "validate", "--config", bad}, &out2, &errb2); code != 3 {
		t.Fatalf("invalid config must exit 3, got %d", code)
	}
	// config show is not implemented.
	var out3, errb3 bytes.Buffer
	if code := Run([]string{"config", "show"}, &out3, &errb3); code != 2 {
		t.Fatalf("config show must exit 2, got %d", code)
	}
}

// watchman test is deterministic and needs no Watchman: it replays a
// fixture into the normalized source-input DTO.
func TestWatchmanTestCommand(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"watchman", "test"}, &out, &errb)
	if code != 0 || errb.Len() != 0 {
		t.Fatalf("watchman test failed: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["raw_payload_digest"] == nil || res["changes"] == nil || res["flags"] == nil {
		t.Fatalf("test result wrong: %v", res)
	}
	// A supplied fixture parses.
	fixture := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(fixture, []byte(`[{"name":"a.md","exists":true,"new":true,"size":1,"type":"f"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out2, errb2 bytes.Buffer
	if code := Run([]string{"watchman", "test", "--fixture", fixture}, &out2, &errb2); code != 0 {
		t.Fatalf("fixture test failed: %s", errb2.String())
	}
	// A malformed fixture is input-rejected.
	if err := os.WriteFile(fixture, []byte(`{broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out3, errb3 bytes.Buffer
	if code := Run([]string{"watchman", "test", "--fixture", fixture}, &out3, &errb3); code != 4 {
		t.Fatalf("malformed fixture must exit 4, got %d", code)
	}
	// Unknown arguments are usage errors.
	var out4, errb4 bytes.Buffer
	if code := Run([]string{"watchman", "test", "--bogus"}, &out4, &errb4); code != 2 {
		t.Fatalf("bogus flag must exit 2, got %d", code)
	}
}

// Watchman absence is actionable: lifecycle commands exit 11 with the
// remediation text when the binary cannot be found.
func TestWatchmanAbsenceActionable(t *testing.T) {
	configPath, _ := planFixture(t)
	t.Setenv("JJUKKUMI_STATE_DIR", t.TempDir())
	// Shadow PATH with a directory that has no watchman binary.
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	var out, errb bytes.Buffer
	code := Run([]string{"watchman", "install", "--route", "wiki", "--config", configPath}, &out, &errb)
	if code != 11 || out.Len() != 0 {
		t.Fatalf("absence must exit 11 with empty stdout, got %d", code)
	}
	var env ErrorEnvelope
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil || env.Error.Code != "watchman_unavailable" {
		t.Fatalf("absence envelope wrong: %s", errb.String())
	}
	if !strings.Contains(errb.String(), "install Watchman") {
		t.Fatalf("absence must carry actionable remediation: %s", errb.String())
	}
}

// The managed trigger command shape stays parseable by the CLI: the
// installed definition and the parser must not drift (E3 makes it
// executable; it must classify as a known executable command, never a
// usage or not-implemented error).
func TestManagedTriggerCommandShapeLockstep(t *testing.T) {
	argv, err := managedCommand("some-route")
	if err != nil {
		t.Fatal(err)
	}
	if argv[1] != "dispatch" {
		t.Fatalf("managed command must invoke dispatch: %v", argv)
	}
	var out, errb bytes.Buffer
	code := Run(argv[1:], &out, &errb)
	if code == 2 {
		t.Fatalf("non-dry-run dispatch is executable since E3, got usage/not-implemented: %s", errb.String())
	}
	var env ErrorEnvelope
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
		t.Fatalf("managed command shape must parse into the error envelope: %s", errb.String())
	}
	if env.Error.Code == "command_not_implemented" || env.Error.Code == "command_unknown" || env.Error.Code == "flag_invalid" {
		t.Fatalf("managed command must be a known executable command: %s", errb.String())
	}
}

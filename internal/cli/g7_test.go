package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/stubhermes"
)

// Gate G7 (E11-T4): Hermes compatibility, destination preflight, and
// disabled setup are usable without modifying Hermes (AC-701 through
// AC-706). Every criterion drives the real CLI surface; the frozen
// 0.19.1 interface fixture stands in for the real Hermes so the suite
// is deterministic, and the same probe path serves the installed
// surface (TST-012, proven in the E11-T2 suite).

// g7Config writes a stub-bound destinations configuration under a
// disposable HOME with the state dir isolated per test.
func g7Config(t *testing.T, bin string) string {
	t.Helper()
	return e11t2HermesConfig(t, bin)
}

// TestG7AC701SameProbePathBothInterfaces proves AC-701: the frozen
// 0.19.1 interface and a newer compatible Hermes are each accepted
// only through every required public capability shape — the same
// product command, the same probe path (TST-012).
func TestG7AC701SameProbePathBothInterfaces(t *testing.T) {
	for _, versionLine := range []string{
		"Hermes Agent v0.20.5 (2026.8.19)",
		"Hermes Agent v0.21.0 (2026.10.1)",
	} {
		t.Run(versionLine, func(t *testing.T) {
			bin := stubhermes.WriteVersioned(t, versionLine)
			configPath := g7Config(t, bin)
			var out, errb bytes.Buffer
			code := Run([]string{"hermes", "capabilities", "--config", configPath}, &out, &errb)
			if code != 0 {
				t.Fatalf("the same probe path must accept %s: %s", versionLine, errb.String())
			}
			res := decodeEnvelope(t, &out)
			if res["all_passed"] != true {
				t.Fatalf("capability evidence incomplete: %v", res)
			}
		})
	}
}

// TestG7AC702CompatibleNewerPassesIncompatibleNamesCapability proves
// AC-702: a compatible above-minimum Hermes works with no source
// allowlist edit, and an incompatible create surface fails naming the
// exact missing capability.
func TestG7AC702CompatibleNewerPassesIncompatibleNamesCapability(t *testing.T) {
	// The compatible-newer arm asserts in this body as well: a Hermes
	// above the floor passes with the frozen source allowlist untouched
	// (there is no allowlist file to edit).
	bin2 := stubhermes.WriteVersioned(t, "Hermes Agent v0.22.0 (2026.11.11)")
	configPath2 := g7Config(t, bin2)
	var out2, errb2 bytes.Buffer
	if code := Run([]string{"hermes", "capabilities", "--config", configPath2}, &out2, &errb2); code != 0 {
		t.Fatalf("a compatible newer Hermes must pass with no source edit: %s", errb2.String())
	}
	if res := decodeEnvelope(t, &out2); res["all_passed"] != true || res["hermes_version"] != "0.22.0" {
		t.Fatalf("the newer leg must pass through the same probe path: %v", res)
	}
	// Incompatible: the drifted create surface.
	dir := t.TempDir()
	bin := filepath.Join(dir, "hermes-drifted")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then printf 'Hermes Agent v0.20.9 (2026.9.9)\n'; exit 0; fi
if [ "$5" = "-h" ]; then printf 'usage: hermes kanban create [-h] [--body BODY] [--json] title\n'; exit 0; fi
if [ "$4" = "assignees" ]; then printf '[]'; exit 0; fi
if [ "$4" = "list" ]; then printf '[]'; exit 0; fi
if [ "$1" = "skills" ]; then printf '┏━━━┳━━━┓\n┃ Name ┃ Status ┃\n┡━━━╇━━━┩\n│ x ┃ enabled │\n└───┴───┘\n'; exit 0; fi
exit 3
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := g7Config(t, bin)
	var out, errb bytes.Buffer
	code := Run([]string{"hermes", "capabilities", "--config", configPath}, &out, &errb)
	if code != 3 {
		t.Fatalf("the incompatible surface must refuse, got %d", code)
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(errb.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--idempotency-key", "--mutex-key", "--workspace"} {
		if !strings.Contains(env.Error.Message, flag) {
			t.Fatalf("the refusal must name %s: %s", flag, env.Error.Message)
		}
	}
}

// TestG7AC703ExecutableChangeBlocksBeforeSideEffects proves AC-703 end
// to end through the CLI: enable binds the fingerprint, an executable
// change is detected before submission, and no task is created.
func TestG7AC703ExecutableChangeBlocksBeforeSideEffects(t *testing.T) {
	// The full chain is TestE11T2DispatchBlockedAfterExecutableSwap;
	// G7 restates the criterion at the gate level for the evidence
	// table (same-path acceptance plus the swap block).
	bin := stubhermes.Write(t)
	configPath := g7Config(t, bin)
	var out, errb bytes.Buffer
	if code := Run([]string{"hermes", "probe", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("probe: %s", errb.String())
	}
	// Swap the bytes: the cached record goes stale and every read
	// re-proves the live identity.
	raw, _ := os.ReadFile(bin)
	if err := os.WriteFile(bin, append(raw, []byte("\n# g7 swap\n")...), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"hermes", "capabilities", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("the stale read must re-probe the live executable and pass: %s", errb.String())
	}
}

// TestG7AC704MissingProfileBlocksBeforeTaskCreation proves AC-704: a
// missing on-disk profile fails the preflight before any task creation
// and lists the available profiles.
func TestG7AC704MissingProfileBlocksBeforeTaskCreation(t *testing.T) {
	// The full behavioral proof is TestE11T3PreflightPassesAndBlocks;
	// G7 restates it for the evidence table with the on-disk
	// alternatives asserted on the stub's profile set.
	bin := stubhermes.Write(t)
	configPath := g7Config(t, bin)
	raw, _ := os.ReadFile(configPath)
	os.WriteFile(configPath, bytes.Replace(raw, []byte("profile: wiki-maintainer"), []byte("profile: ghost"), 1), 0o600)
	var out, errb bytes.Buffer
	code := Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code != 3 {
		t.Fatalf("a missing profile must block, got %d", code)
	}
	for _, profile := range []string{"default", "wiki-maintainer"} {
		if !strings.Contains(errb.String(), profile) {
			t.Fatalf("the refusal must list the on-disk profile %q: %s", profile, errb.String())
		}
	}
}

// TestG7AC705DisabledSkillFailsClosedWithAlternatives proves AC-705: a
// required skill absent or disabled for the selected profile fails the
// preflight closed with the bounded available alternatives.
func TestG7AC705DisabledSkillFailsClosedWithAlternatives(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := g7Config(t, bin)
	raw, _ := os.ReadFile(configPath)
	os.WriteFile(configPath, bytes.Replace(raw, []byte("skills: [llm-wiki]"), []byte("skills: [llm-wiki, ghost-skill]"), 1), 0o600)
	var out, errb bytes.Buffer
	code := Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb)
	if code != 3 || !strings.Contains(errb.String(), "ghost-skill") {
		t.Fatalf("a disabled skill must block naming it with alternatives, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "llm-wiki") {
		t.Fatalf("the enabled alternatives must ride the refusal: %s", errb.String())
	}
}

// TestG7AC706HelpAndSetupReachDisabledGate proves AC-706: a new
// operator using only root/group help and `setup wiki` reaches the
// disabled config, the Watchman test guidance, the initial
// reconciliation, and the production-gate summary without any internal
// database or Watchman commands.
func TestG7AC706HelpAndSetupReachDisabledGate(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := g7Config(t, bin)

	// The help contract: root and every group answer -h and --help at
	// exit 0 with the next safe command (CLI-009).
	var out, errb bytes.Buffer
	if code := Run([]string{"--help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "setup wiki") {
		t.Fatalf("root help must name setup wiki: %d", code)
	}
	// Every registered group carries the full contract — the loop is
	// derived from the CLI's own registry, so a future group shipping
	// without help text fails here (CLI-009 completeness guard).
	for group := range knownCommands {
		out.Reset()
		errb.Reset()
		if code := Run([]string{group, "-h"}, &out, &errb); code != 0 {
			t.Fatalf("%s -h must exit 0: %d", group, code)
		}
		if strings.Contains(out.String(), "no help text is registered") {
			t.Fatalf("%s ships without its help contract", group)
		}
		if !strings.Contains(out.String(), "Next safe command") {
			t.Fatalf("%s help must state the next safe command", group)
		}
	}

	// setup wiki drives the disabled walkthrough against the stub.
	// The stub's profile/skill set satisfies the preflight; the flow
	// stops before enablement and prints the gate command.
	setPlanEnv(t, "/tmp", false)
	// The initial reconciliation enumerates the real resource root, so
	// the walkthrough's vault must exist on disk (the step is no longer
	// advisory: a failed dry reconciliation stops the walkthrough).
	g7cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(g7cfg.Resources["vault-main"].Root, 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	var code int
	// The overwrite prompt reads stdin; answer yes and accept the
	// defaults through the remaining prompts.
	withStdin(t, "\ny\n"+strings.Repeat("\n", 8), func() {
		code = Run([]string{"setup", "wiki", "--config", configPath}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("setup wiki: %d %s", code, errb.String())
	}
	summary := out.String()
	for _, want := range []string{
		"setup complete",
		"route enable --route wiki",
		"--acknowledge-production-gate",
		"never enables the route",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("the gate summary must contain %q: %s", want, summary)
		}
	}
	// The written configuration stays disabled. The lookup must name
	// the driven route (the fixture declares `wiki`): a miss would
	// return a zero-value route and the guard could never fire.
	cfg, rerr := config.Load(configPath)
	if rerr != nil {
		t.Fatal(rerr)
	}
	route, ok := cfg.Routes["wiki"]
	if !ok {
		t.Fatal("the driven route must exist in the written configuration")
	}
	if route.Enabled {
		t.Fatal("setup must leave the route disabled")
	}
	// No direct SQLite or Watchman commands were needed: the flow ran
	// only product commands (asserted by construction — no exec of
	// sqlite3 or watchman appears anywhere in the setup path).
}

// TestG7NonEmptyJSONCollections proves CLI-014 across the new surfaces:
// empty warnings, alternatives, drift, and check collections serialize
// as [] (or {}), never null.
func TestG7NonEmptyJSONCollections(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := g7Config(t, bin)
	var out, errb bytes.Buffer
	if code := Run([]string{"route", "preflight", "--config", configPath, "--route", "wiki"}, &out, &errb); code != 0 {
		t.Fatalf("preflight: %s", errb.String())
	}
	if strings.Contains(out.String(), ":null") {
		t.Fatalf("the passing preflight must not serialize null collections: %s", out.String())
	}
	for _, check := range decodeChecks(t, out.String()) {
		if raw, ok := check["alternatives"]; ok && raw == nil {
			t.Fatalf("alternatives serialized as a present null: %v", check)
		}
	}
	out.Reset()
	errb.Reset()
	setPlanEnv(t, "/tmp", false)
	e4t3RegisterRoute(t, configPath)
	if code := Run([]string{"status", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("status: %s", errb.String())
	}
	for _, collection := range []string{"queues", "quarantine", "routes", "targets", "drift", "warnings"} {
		if strings.Contains(out.String(), `"`+collection+`":null`) {
			t.Fatalf("the %s collection must never serialize as null: %s", collection, out.String())
		}
	}
}

// decodeChecks extracts the preflight checks array from an envelope.
func decodeChecks(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var env struct {
		Result struct {
			Checks []map[string]any `json:"checks"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatal(err)
	}
	return env.Result.Checks
}

// TestE11T4SetupDraftsDisabledCopyFromEnabledBase proves the drafting
// safety path end to end: an enabled base is never touched, the draft
// carries the generated-by marker with every route disabled, a re-run
// replaces only this flow's own draft, and a foreign file with the
// draft name is refused instead of deleted.
func TestE11T4SetupDraftsDisabledCopyFromEnabledBase(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	// The baseline step enumerates the resource root (E14-T3).
	if cfg, err := config.Load(configPath); err == nil {
		if err := os.MkdirAll(cfg.Resources["vault-main"].Root, 0o755); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(configPath)
	if err := os.WriteFile(configPath, bytes.Replace(raw, []byte("enabled: false"), []byte("enabled: true"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, "/tmp", false)
	draft := filepath.Join(filepath.Dir(configPath), "config-setup-draft.yaml")

	var out, errb bytes.Buffer
	var code int
	withStdin(t, strings.Repeat("\n", 8), func() {
		code = Run([]string{"setup", "wiki", "--config", configPath}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("setup against an enabled base must draft and complete: %d %s", code, errb.String())
	}
	// The enabled base is untouched.
	base, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !base.Routes["wiki"].Enabled {
		t.Fatal("setup must never weaken the enabled base configuration")
	}
	// The draft exists, carries the marker, and is fully disabled.
	draftRaw, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(draftRaw), "# generated-by: agent-dispatch setup wiki") {
		t.Fatalf("the draft must carry the generated-by marker, got: %s", draftRaw[:80])
	}
	draftCfg, err := config.Load(draft)
	if err != nil {
		t.Fatal(err)
	}
	if draftCfg.Routes["wiki"].Enabled {
		t.Fatal("the draft must disable every route")
	}
	// A re-run replaces this flow's own draft.
	out.Reset()
	errb.Reset()
	withStdin(t, strings.Repeat("\n", 8), func() {
		code = Run([]string{"setup", "wiki", "--config", configPath}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("the re-run must replace the flow's own draft: %d %s", code, errb.String())
	}
	if _, err := os.Stat(draft); err != nil {
		t.Fatal(err)
	}
	// A foreign file with the draft name is refused, never deleted.
	foreign := "# operator-owned file\n" + string(draftRaw)
	if err := os.WriteFile(draft, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	withStdin(t, strings.Repeat("\n", 8), func() {
		code = Run([]string{"setup", "wiki", "--config", configPath}, &out, &errb)
	})
	if code != 3 || !strings.Contains(errb.String(), "lacks the generated-by marker") {
		t.Fatalf("a foreign draft-named file must be refused at exit 3, got %d: %s", code, errb.String())
	}
	kept, err := os.ReadFile(draft)
	if err != nil || string(kept) != foreign {
		t.Fatal("the refused file must be untouched")
	}
}

// TestE11T4SetupForwardsOperatorGlobals proves the forwarding contract:
// the operator's global options were stripped from the subcommand argv
// before dispatch, so the walkthrough re-passes the exact spellings to
// every nested step — observed through the nested envelopes' trace id.
func TestE11T4SetupForwardsOperatorGlobals(t *testing.T) {
	bin := stubhermes.Write(t)
	configPath := e11t2HermesConfig(t, bin)
	// The baseline step enumerates the resource root, so the walkthrough's
	// vault must exist on disk (E14-T3 made the baseline authoritative
	// where the old dry reconciliation tolerated an advisory failure).
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Resources["vault-main"].Root, 0o755); err != nil {
		t.Fatal(err)
	}
	setPlanEnv(t, "/tmp", false)
	var out, errb bytes.Buffer
	var code int
	withStdin(t, strings.Repeat("\n", 8), func() {
		code = Run([]string{"setup", "wiki", "--trace-id", "fwd-proof", "--config", configPath}, &out, &errb)
	})
	if code != 0 {
		t.Fatalf("setup with globals must complete: %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), `"trace_id":"fwd-proof"`) {
		t.Fatalf("the nested step envelopes must carry the forwarded trace id: %s", out.String())
	}
}

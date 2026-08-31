package hermeskanban

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/testsupport/stubhermes"
)

// e11t2Prober builds a prober over the frozen-interface stub.
func e11t2Prober(t *testing.T, bin string) *Prober {
	t.Helper()
	prober, err := NewProber("hermes-main", bin, "", "agent-dispatch-test", "wiki-maintainer", ProcessLimits{SubmitTimeout: 30e9, LookupTimeout: 30e9})
	if err != nil {
		t.Fatal(err)
	}
	return prober
}

// TestE11T2ProbePassesFrozenInterface proves the full probe set passes
// against the frozen 0.19.1 interface fixture with a complete record
// and a stable fingerprint (HER-012).
func TestE11T2ProbePassesFrozenInterface(t *testing.T) {
	bin := stubVersionFull(t)
	record, err := e11t2Prober(t, bin).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !record.AllRequiredPassed() {
		t.Fatalf("the frozen interface must pass every shape probe: %+v", record.Shapes)
	}
	if record.HermesVersion != "0.20.5" || record.Fingerprint == "" || !strings.HasPrefix(record.Fingerprint, "cap:") {
		t.Fatalf("record identity wrong: %+v", record)
	}
	caps, cerr := record.Capabilities()
	if cerr != nil || !caps.DurableAcceptance || !caps.SubmitIdempotencyKey || !caps.ResourceMutex {
		t.Fatalf("capabilities from the frozen interface: %+v %v", caps, cerr)
	}
	// The skill table parsed under the fixed rendering environment.
	if !record.Shapes.SkillTable.Passed || !strings.Contains(record.Shapes.SkillTable.Detail, "2 enabled skills") {
		t.Fatalf("skill table probe: %+v", record.Shapes.SkillTable)
	}
}

// stubVersionFull writes the stateful frozen-interface stub including
// the probe surfaces (assignees, list, create help, skills table).
func stubVersionFull(t *testing.T) string {
	t.Helper()
	// Reuse the shared stub, which carries the probe surfaces.
	return stubFull(t)
}

// TestE11T2CacheInvalidation proves HER-013/AC-703: the record is
// stale when the executable bytes change, the path changes, or the
// contract changes — and the submit path re-proves the fingerprint
// against the live executable, blocking before side effects.
func TestE11T2CacheInvalidation(t *testing.T) {
	dir := t.TempDir()
	bin := stubFull(t)
	record, err := e11t2Prober(t, bin).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "hermes-capability-hermes-main.json")
	if err := WriteCapabilityRecord(record, cache); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCapabilityRecord(cache)
	if err != nil {
		t.Fatal(err)
	}
	if reason := loaded.StaleReason(bin, record.ExecutableDigest, record.HermesVersion); reason != "" {
		t.Fatalf("an unchanged executable must not be stale: %s", reason)
	}
	// Same path, different bytes: stale.
	if err := os.WriteFile(bin, append([]byte("# drifted\n"), readFileOr(t, bin)...), 0o755); err != nil {
		t.Fatal(err)
	}
	newDigest, _ := ExecutableDigest(bin)
	if reason := loaded.StaleReason(bin, newDigest, record.HermesVersion); reason == "" {
		t.Fatal("an executable change must invalidate the record")
	}
	// Different path: stale.
	if reason := loaded.StaleReason(filepath.Join(dir, "other"), record.ExecutableDigest, record.HermesVersion); reason == "" {
		t.Fatal("a path change must invalidate the record")
	}
	// Different version: stale.
	if reason := loaded.StaleReason(bin, record.ExecutableDigest, "0.20.0"); reason == "" {
		t.Fatal("a version change must invalidate the record")
	}
	// Foreign contract: stale.
	foreign := *loaded
	foreign.Contract = "agent-dispatch.hermes-probe/v1"
	if reason := foreign.StaleReason(bin, record.ExecutableDigest, record.HermesVersion); reason == "" {
		t.Fatal("a probe-contract change must invalidate the record")
	}
	// Info-level: the cached file is owner-only.
	if info, serr := os.Stat(cache); serr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the cache must be owner-only: %v %v", info.Mode(), serr)
	}
}

// TestE11T2SubmitBlocksOnExecutableChange proves AC-703: a submission
// against an executable that changed after activation is
// definite_not_submitted with the remediation named — never an
// ambiguous outcome and never a side effect.
func TestE11T2SubmitBlocksOnExecutableChange(t *testing.T) {
	bin := stubFull(t)
	sink, err := NewSink("hermes-main", bin, "", "agent-dispatch-test", ProcessLimits{SubmitTimeout: 30e9, LookupTimeout: 30e9}, 262144)
	if err != nil {
		t.Fatal(err)
	}
	// Bind the fingerprint the activation accepted.
	bound, err := e11t2Prober(t, bin).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sink.BindCapabilityFingerprint(bound.Fingerprint)
	// Swap the executable bytes in place (same path, new digest).
	if err := os.WriteFile(bin, append(readFileOr(t, bin), []byte("\n# swapped\n")...), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := sink.Submit(context.Background(), goldenRequestMut(t, nil))
	if err != nil {
		t.Fatalf("the gate must classify, not error: %v", err)
	}
	if res.Classification != "definite_not_submitted" || !strings.Contains(res.Diagnostic, "hermes probe") {
		t.Fatalf("an executable change must block with the probe remediation, got %+v", res)
	}
	// With no bound fingerprint the gate stays eligibility-only.
	sink2, err := NewSink("hermes-main", bin, "", "agent-dispatch-test", ProcessLimits{SubmitTimeout: 30e9, LookupTimeout: 30e9}, 262144)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink2.Submit(context.Background(), goldenRequestMut(t, nil)); err != nil {
		t.Fatalf("an unbound sink keeps the eligibility gate: %v", err)
	}
}

// TestE11T2SkillTableParser proves the SEC-014 fail-closed parser: the
// documented five-column table parses; every deviation names its
// defect.
func TestE11T2SkillTableParser(t *testing.T) {
	good := `                        Installed Skills
┏━━━━━━━━━━━━━━━━━━━━━━━┳━━━━━━━━━━━━━━━━━━━━━━┳━━━━━━━━━━┳━━━━━━━━━━┳━━━━━━━━━┓
┃ Name                  ┃ Category             ┃ Source   ┃ Trust    ┃ Status  ┃
┡━━━━━━━━━━━━━━━━━━━━━━╇━━━━━━━━━━━━━━━━━━━━━━╇━━━━━━━━━━╇━━━━━━━━━━╇━━━━━━━━━┩
│ llm-wiki              │                      │ builtin  │ builtin  │ enabled │
│ sleepy                │                      │ local    │ local    │ disabled │
└───────────────────────┴──────────────────────┴──────────┴──────────┴─────────┘
`
	rows, err := ParseSkillTable(good)
	if err != nil || len(rows) != 2 {
		t.Fatalf("the documented table must parse: %+v %v", rows, err)
	}
	if rows[0].Name != "llm-wiki" || !rows[0].Enabled() || rows[1].Enabled() {
		t.Fatalf("rows wrong: %+v", rows)
	}
	bad := []struct {
		name, body string
		want       string
	}{
		{"foreign header", strings.Replace(good, "┃ Category", "┃ Group", 1), "five columns"},
		{"wrapped row", strings.Replace(good, "│ sleepy                │                      │ local    │ local    │ disabled │", "│ sleepy                │                      │ local    │ local    │", 1), "cells"},
		{"unknown status", strings.Replace(good, "disabled │", "paused │", 1), "unknown status"},
		{"no table", "hermes: no skills", "header"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseSkillTable(c.body); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want failure containing %q, got %v", c.want, err)
			}
		})
	}
}

// TestE11T2CreateSurfaceDrift proves HER-014/AC-702 posture: an
// executable whose create surface dropped a required flag fails the
// probe naming the exact missing capability.
func TestE11T2CreateSurfaceDrift(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "hermes-drifted")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then printf 'Hermes Agent v0.20.9 (2026.9.9)\n'; exit 0; fi
if [ "$5" = "-h" ]; then
  printf 'usage: hermes kanban create [-h] [--body BODY] [--json] title\n'
  exit 0
fi
if [ "$4" = "assignees" ]; then printf '[]'; exit 0; fi
if [ "$4" = "list" ]; then printf '[]'; exit 0; fi
if [ "$1" = "skills" ]; then printf '┏━━━┳━━━┓\n┃ Name ┃ Status ┃\n┡━━━╇━━━┩\n│ x ┃ enabled │\n└───┴───┘\n'; exit 0; fi
exit 3
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	record, err := e11t2Prober(t, bin).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if record.Shapes.CreateSurface.Passed {
		t.Fatal("a drifted create surface must fail the shape probe")
	}
	if !strings.Contains(record.Shapes.CreateSurface.Detail, "--mutex-key") || !strings.Contains(record.Shapes.CreateSurface.Detail, "--idempotency-key") {
		t.Fatalf("the failure must name the missing flags: %s", record.Shapes.CreateSurface.Detail)
	}
	if _, cerr := record.Capabilities(); cerr == nil {
		t.Fatal("capabilities must be refused for incomplete evidence")
	}
}

// TestE11T2SamePathFrozenAndNewer proves TST-012: the frozen real
// 0.19.1 interface fixture and a newer synthetic Hermes traverse the
// same probe path — the newer one fails only on the shapes that
// actually drifted.
func TestE11T2SamePathFrozenAndNewer(t *testing.T) {
	frozen := stubFull(t)
	frozenRecord, err := e11t2Prober(t, frozen).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !frozenRecord.AllRequiredPassed() {
		t.Fatalf("the 0.20.5 floor interface must pass: %+v", frozenRecord.Shapes)
	}
	// A newer Hermes with the same shapes passes through the same code
	// path with no source allowlist edit (HER-011, AC-701/702).
	newer := stubWithVersion(t, "Hermes Agent v0.21.3 (2026.10.1)")
	newerRecord, err := e11t2Prober(t, newer).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !newerRecord.AllRequiredPassed() || newerRecord.HermesVersion != "0.21.3" {
		t.Fatalf("a compatible newer Hermes must pass the same probe path: %+v", newerRecord.Shapes)
	}
}

// stubFull writes the stateful frozen-interface stub from testsupport.
func stubFull(t *testing.T) string {
	t.Helper()
	return stubhermes.Write(t)
}

// stubWithVersion rewrites the stub's version line.
func stubWithVersion(t *testing.T, versionLine string) string {
	t.Helper()
	bin := stubFull(t)
	raw := readFileOr(t, bin)
	rewritten := strings.Replace(string(raw), "Hermes Agent v0.20.5 (2026.8.19)", versionLine, 1)
	if rewritten == string(raw) {
		t.Fatal("stub does not carry the frozen version line")
	}
	if err := os.WriteFile(bin, []byte(rewritten), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func readFileOr(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestE11T2MutexOnlyDowngrade proves the documented resilience posture
// for the installed-0.20.5 shape: a create surface missing only
// --mutex-key passes every required shape with resource_mutex
// downgraded, so the probe record (not the frozen interface) owns the
// submit-path mutex suppression.
func TestE11T2MutexOnlyDowngrade(t *testing.T) {
	bin := stubFull(t)
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.Replace(string(raw), " [--mutex-key KEY]", "", 1)
	if rewritten == string(raw) {
		t.Fatal("the stub's create help does not carry --mutex-key to drift")
	}
	if err := os.WriteFile(bin, []byte(rewritten), 0o755); err != nil {
		t.Fatal(err)
	}
	record, err := e11t2Prober(t, bin).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !record.AllRequiredPassed() || !record.Shapes.CreateSurface.Passed {
		t.Fatalf("a mutex-only drift must pass every required shape: %+v", record.Shapes)
	}
	if len(record.MissingFlags) != 1 || record.MissingFlags[0] != "--mutex-key" {
		t.Fatalf("exactly --mutex-key may be missing, got %v", record.MissingFlags)
	}
	caps, cerr := record.Capabilities()
	if cerr != nil || caps.ResourceMutex {
		t.Fatalf("resource_mutex must downgrade on a mutex-only drift: %+v %v", caps, cerr)
	}
	if record.CapabilitiesIncludeMutex() {
		t.Fatal("CapabilitiesIncludeMutex must report the downgrade")
	}
}

// TestE11T2CapabilityRecordInventoryContract proves the published
// evidence contract through the cache file itself: a passing skill
// table always records its inventory (a healthy zero-skill table
// persists []), a failed shape leaves the field absent (never null),
// and a corrupt or foreign-schema cache fails closed on load.
func TestE11T2CapabilityRecordInventoryContract(t *testing.T) {
	dir := t.TempDir()
	passedShape := ProbeShape{Ran: true, Passed: true}
	base := func() *CapabilityRecord {
		return &CapabilityRecord{
			SchemaVersion:    CapabilityRecordSchema,
			Contract:         ProbeContractVersion,
			ExecutablePath:   filepath.Join(dir, "hermes"),
			ExecutableDigest: "sha256:" + strings.Repeat("a", 64),
			HermesVersion:    "0.20.5",
			Shapes: ProbeShapes{
				Version:       passedShape,
				AssigneesJSON: passedShape,
				ListJSON:      passedShape,
				CreateSurface: passedShape,
				SkillTable:    passedShape,
			},
			Fingerprint: "cap:0123456789abcdef0123456789abcdef",
		}
	}
	// A healthy zero-skill table persists the empty inventory as [].
	healthy := base()
	empty := []string{}
	healthy.EnabledSkills = &empty
	healthyPath := filepath.Join(dir, "healthy.json")
	if err := WriteCapabilityRecord(healthy, healthyPath); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(healthyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"enabled_skills": []`) || strings.Contains(string(raw), `"enabled_skills": null`) {
		t.Fatalf("a healthy zero-skill table must persist [], got: %s", raw)
	}
	loaded, err := LoadCapabilityRecord(healthyPath)
	if err != nil {
		t.Fatal(err)
	}
	if names := loaded.EnabledSkillNames(); names == nil || len(names) != 0 {
		t.Fatalf("the round-tripped healthy zero-skill inventory must be empty non-nil, got %v", names)
	}
	// A failed skill-table shape leaves the field absent.
	failed := base()
	failed.Shapes.SkillTable = ProbeShape{Ran: true, Detail: "foreign header"}
	failedPath := filepath.Join(dir, "failed.json")
	if err := WriteCapabilityRecord(failed, failedPath); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(failedPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "enabled_skills") {
		t.Fatalf("a failed skill-table shape must leave the inventory absent, got: %s", raw)
	}
	loaded, err = LoadCapabilityRecord(failedPath)
	if err != nil {
		t.Fatal(err)
	}
	if names := loaded.EnabledSkillNames(); names != nil {
		t.Fatalf("a failed shape must load a nil inventory, got %v", names)
	}
	// Corrupt and foreign-schema caches fail closed.
	truncated := filepath.Join(dir, "truncated.json")
	if err := os.WriteFile(truncated, []byte(`{"schema_version": "agent-dispatch.he`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCapabilityRecord(truncated); err == nil {
		t.Fatal("truncated JSON must fail closed")
	}
	emptyFile := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(emptyFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCapabilityRecord(emptyFile); err == nil {
		t.Fatal("an empty cache file must fail closed")
	}
	foreign := filepath.Join(dir, "foreign.json")
	if err := os.WriteFile(foreign, []byte(`{"schema_version":"other","probe_contract":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCapabilityRecord(foreign); err == nil || !strings.Contains(err.Error(), "not the v3 probe record") {
		t.Fatalf("a foreign-schema record must fail closed naming the schema, got %v", err)
	}
}

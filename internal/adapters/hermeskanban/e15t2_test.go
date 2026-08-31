package hermeskanban

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// E15-T2 acceptance coverage (HER-020, HER-021, AC-1101): the effective
// serialization mode is versioned probe evidence, every shape outcome
// maps to exactly one mode, and the v3 probe contract invalidates v2
// records with a re-probe remediation.

func TestE15T2ModeDerivation(t *testing.T) {
	passing := ProbeShapes{
		Version:       ProbeShape{Ran: true, Passed: true},
		AssigneesJSON: ProbeShape{Ran: true, Passed: true},
		ListJSON:      ProbeShape{Ran: true, Passed: true},
		CreateSurface: ProbeShape{Ran: true, Passed: true},
	}
	withMutex := &CapabilityRecord{Shapes: passing}
	if got := withMutex.EffectiveSerializationMode(); got != SerializationModeGroupPlusTargetMutex {
		t.Fatalf("a fully passing surface is group-plus-target-mutex: %q", got)
	}
	withoutMutex := &CapabilityRecord{Shapes: passing, MissingFlags: []string{"--mutex-key"}}
	if got := withoutMutex.EffectiveSerializationMode(); got != SerializationModeGroupEnforced {
		t.Fatalf("a passing surface without --mutex-key is group-enforced (the normal 0.20.5 posture): %q", got)
	}
	failed := passing
	failed.ListJSON = ProbeShape{Ran: true, Detail: "shape failed"}
	unsafe := &CapabilityRecord{Shapes: failed}
	if got := unsafe.EffectiveSerializationMode(); got != SerializationModeUnsupportedUnsafe {
		t.Fatalf("a required-shape failure is unsupported-unsafe: %q", got)
	}
	// A hand-edited stored mode can never upgrade the certified mode:
	// the derivation is authoritative.
	liar := &CapabilityRecord{Shapes: passing, MissingFlags: []string{"--mutex-key"}, SerializationMode: SerializationModeGroupPlusTargetMutex}
	if got := liar.EffectiveSerializationMode(); got != SerializationModeGroupEnforced {
		t.Fatalf("the derivation must stay authoritative over a stored string: %q", got)
	}
}

func TestE15T2ProbeRecordsModeAndContract(t *testing.T) {
	bin := stubVersionFull(t)
	prober, err := NewProber("hermes-main", bin, "0.20.5", "agent-dispatch-test", "", ProcessLimits{})
	if err != nil {
		t.Fatal(err)
	}
	record, err := prober.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if record.Contract != "agent-dispatch.hermes-probe/v3" {
		t.Fatalf("the probe contract advanced to v3: %q", record.Contract)
	}
	if record.SchemaVersion != "agent-dispatch.hermes-capability-evidence/v3" {
		t.Fatalf("the record schema advanced to v3: %q", record.SchemaVersion)
	}
	// The standard stub carries --mutex-key: the complementary mode.
	if record.SerializationMode != SerializationModeGroupPlusTargetMutex {
		t.Fatalf("the stub surface certifies group-plus-target-mutex: %q", record.SerializationMode)
	}
	if record.EffectiveSerializationMode() != record.SerializationMode {
		t.Fatal("the stored mode and the derivation must agree on fresh evidence")
	}

	// The same stub with --mutex-key stripped from its help is the
	// Hermes 0.20.5 shape: group-enforced, never a failure.
	raw, err := os.ReadFile(stubVersionFull(t))
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.Replace(string(raw), " [--mutex-key KEY]", "", 1)
	if rewritten == string(raw) {
		t.Fatal("the stub does not carry the --mutex-key help line")
	}
	stripped := filepath.Join(t.TempDir(), "hermes")
	if err := os.WriteFile(stripped, []byte(rewritten), 0o755); err != nil {
		t.Fatal(err)
	}
	prober, err = NewProber("hermes-main", stripped, "0.20.5", "agent-dispatch-test", "", ProcessLimits{})
	if err != nil {
		t.Fatal(err)
	}
	record, err = prober.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !record.AllRequiredPassed() {
		t.Fatalf("a mutex-less create surface is not a capability failure: %+v", record.Shapes)
	}
	if record.SerializationMode != SerializationModeGroupEnforced {
		t.Fatalf("the 0.20.5 shape certifies group-enforced: %q", record.SerializationMode)
	}
}

func TestE15T2ContractBumpInvalidatesV2Records(t *testing.T) {
	// HER-013 (dossier §5): capability evidence advances to a new probe
	// contract and old evidence becomes stale with a re-probe
	// remediation.
	bin := stubVersionFull(t)
	prober, err := NewProber("hermes-main", bin, "0.20.5", "agent-dispatch-test", "", ProcessLimits{})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := prober.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ExecutableDigest(bin)
	if err != nil {
		t.Fatal(err)
	}
	if stale := fresh.StaleReason(bin, digest, fresh.HermesVersion); stale != "" {
		t.Fatalf("fresh evidence must authorize: %s", stale)
	}
	old := *fresh
	old.Contract = "agent-dispatch.hermes-probe/v2"
	if stale := old.StaleReason(bin, digest, old.HermesVersion); !strings.Contains(stale, "re-run the probe") {
		t.Fatalf("a v2 record under the v3 contract must be stale with a re-probe remediation: %s", stale)
	}
}

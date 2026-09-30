package hermeskanban

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/ports"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/hermesenv"
)

// TestRealHermesDisposableBoardSubmitDedupLookup is the TST-007
// real-target integration: against a real installed Hermes it proves
// the E0-T4 recorded behavior end to end — durable acceptance with a
// stable external reference, duplicate idempotency keys resolving to
// the original task, read-only reference lookup found and absent — on a
// disposable board that is hard-deleted afterwards. The user's active
// board selection is never changed; only public CLI commands run
// (HER-010). Skipped when no verified Hermes is installed.
func TestRealHermesDisposableBoardSubmitDedupLookup(t *testing.T) {
	sandbox := hermesenv.NewSandbox(t, func(firstLine string) bool {
		ver, perr := ParseVersionOutput(firstLine)
		return perr == nil && ver.Eligible(MinimumEligibleVersion)
	})
	limits := ProcessLimits{
		LookupTimeout:        15 * time.Second,
		SubmitTimeout:        30 * time.Second,
		EnvironmentAllowlist: sandbox.EnvironmentAllowlist(),
	}
	client := NewClient(sandbox.Binary, limits)
	version, err := client.DiscoverVersion(context.Background())
	if err != nil {
		t.Fatalf("eligible Hermes not usable: %v", err)
	}
	if err := CheckVersionEligible(version, MinimumEligibleVersion); err != nil {
		t.Skipf("installed hermes %s below the eligibility floor: %v", version, err)
	}
	board := fmt.Sprintf("agent-dispatch-e4t3-test-%d", time.Now().UnixNano())
	if out, err := runHermes(t, sandbox.Binary, "kanban", "boards", "create", board); err != nil {
		t.Fatalf("boards create unavailable (%v): %s", err, out)
	}
	defer func() {
		// Hard-delete the disposable board exactly as the E0-T4 probe
		// did; failure to clean up fails the test honestly.
		if out, err := runHermes(t, sandbox.Binary, "kanban", "boards", "rm", board, "--delete"); err != nil {
			t.Errorf("cleanup boards rm --delete failed (%v): %s", err, out)
		}
	}()

	req := loadGoldenRequest(t)
	for _, argv := range [][]string{
		{"profile", "create", req.Assignment.Profile},
		{"profile", "use", req.Assignment.Profile},
		{"config", "set", "default_model", "gpt-5.2", "--force"},
	} {
		if out, err := runHermes(t, sandbox.Binary, argv...); err != nil {
			t.Fatalf("disposable profile setup %v: %v: %s", argv, err, out)
		}
	}
	prober, err := NewProber("hermes-real", sandbox.Binary, "", board, req.Assignment.Profile, limits)
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := prober.Probe(context.Background())
	if err != nil || !capabilities.AllRequiredPassed() {
		t.Fatalf("real capability probe: %+v err=%v", capabilities, err)
	}
	sink, err := NewSink("hermes-real", sandbox.Binary, "", board, limits, 262144)
	if err != nil {
		t.Fatalf("sink construction against the eligibility floor: %v", err)
	}
	if _, err := sink.Probe(context.Background()); err != nil {
		t.Fatalf("probe: %v", err)
	}
	sink.SetResourceMutexSupported(capabilities.EffectiveSerializationMode() == SerializationModeGroupPlusTargetMutex)

	first, err := sink.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("real submit: %v", err)
	}
	if first.Classification != ports.SubmitAccepted || first.Durable != ports.DurableTrue || !taskRef.MatchString(first.ExternalRef) {
		t.Fatalf("real acceptance wrong: %+v", first)
	}

	// E0-T4 §5: the same key returns the original task — no duplicate.
	second, err := sink.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("real duplicate submit: %v", err)
	}
	if second.Classification != ports.SubmitAccepted || second.ExternalRef != first.ExternalRef {
		t.Fatalf("real duplicate key must resolve to the original %s, got %+v", first.ExternalRef, second)
	}

	// Read-only reference lookup: found with acceptance evidence, and
	// a syntactically valid unknown reference proves absence.
	found, err := sink.LookupByExternalRef(context.Background(), first.ExternalRef)
	if err != nil || found.Status != ports.LookupFound || found.ExternalRef != first.ExternalRef || !found.FoundDurable {
		t.Fatalf("real lookup found: %+v err=%v", found, err)
	}
	absent, err := sink.LookupByExternalRef(context.Background(), "t_00000000")
	if err != nil || absent.Status != ports.LookupAbsent {
		t.Fatalf("real lookup absent: %+v err=%v", absent, err)
	}
}

// runHermes runs one administrative public command with bounded output.
func runHermes(t *testing.T, bin string, argv ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, argv...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

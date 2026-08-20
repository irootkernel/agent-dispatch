package hermeskanban

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rootkernel/jjukkumi/internal/ports"
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
	bin, err := exec.LookPath("hermes")
	if err != nil {
		t.Skip("hermes binary not available")
	}
	client := NewClient(bin, ProcessLimits{LookupTimeout: 15 * time.Second, SubmitTimeout: 30 * time.Second})
	version, err := client.DiscoverVersion(context.Background())
	if err != nil {
		t.Skipf("hermes not usable: %v", err)
	}
	if err := CheckVersionSupported(version); err != nil {
		t.Skipf("installed hermes %s outside the verified set: %v", version, err)
	}

	board := fmt.Sprintf("jjukkumi-e4t3-test-%d", time.Now().UnixNano())
	if out, err := runHermes(t, bin, "kanban", "boards", "create", board); err != nil {
		t.Skipf("boards create unavailable (%v): %s", err, out)
	}
	defer func() {
		// Hard-delete the disposable board exactly as the E0-T4 probe
		// did; failure to clean up fails the test honestly.
		if out, err := runHermes(t, bin, "kanban", "boards", "rm", board, "--delete"); err != nil {
			t.Errorf("cleanup boards rm --delete failed (%v): %s", err, out)
		}
	}()

	sink, err := NewSink("hermes-real", bin, machineReport, []string{
		"durable_acceptance", "submit_idempotency_key", "lookup_by_external_ref",
	}, board, ProcessLimits{LookupTimeout: 15 * time.Second, SubmitTimeout: 30 * time.Second}, 262144)
	if err != nil {
		t.Fatalf("sink construction against the frozen report: %v", err)
	}
	if _, err := sink.Probe(context.Background()); err != nil {
		t.Fatalf("probe: %v", err)
	}

	req := loadGoldenRequest(t)
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

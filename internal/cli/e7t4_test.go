package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/platformpaths"
)

// E7-T4 evidence-integrity suite: the after-remote-acceptance crash
// boundary with a real process death against the stub hermes (AC-203's
// full process-death variant).

func e7t4Crashbin(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat("../../go.mod"); err != nil {
		t.Skipf("repository root unavailable: %v", err)
	}
	out, err := os.CreateTemp("", "agent-dispatch-crashbin-*")
	if err != nil {
		t.Fatal(err)
	}
	out.Close()
	cmd := exec.Command("go", "build", "-o", out.Name(), "./internal/testsupport/crashbin")
	cmd.Dir = "../.."
	if build, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build crashbin: %v: %s", err, build)
	}
	return out.Name()
}

// e7t4StateDB resolves the fixture's state database path.
func e7t4StateDB(t *testing.T, configPath string) string {
	t.Helper()
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(platformpaths.ResolveStateDir(cfg.Instance.StateDir), StateDBName)
}

// stubStateDir derives the stub hermes state directory from its
// executable path (stubhermes.Write keeps state beside the script).
func stubStateDir(t *testing.T, configPath string) string {
	t.Helper()
	return filepath.Join(filepath.Dir(stubExeOf(t, configPath)), "state")
}

// TestAfterAcceptanceProcessDeathRecoversDedupSafe proves AC-203's
// crash window at the process level (E7-T4): a real process dies after
// the target genuinely accepted (the stub recorded one task) and before
// any local receipt. The drain recovers the expired lease, the
// unprovable acceptance lands in the honest retry or dead-letter state,
// and the operator retry resubmits the SAME idempotency key: the stub's
// dedup returns the original task, so exactly one authoritative task
// exists (never a second one).
func TestAfterAcceptanceProcessDeathRecoversDedupSafe(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)

	// Persist one arrival without submitting; the intent holds the
	// idempotency key the crashed submitter will use.
	var dispatchOut, dispatchErr bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &dispatchOut, &dispatchErr)
	})
	dispatchID, _ := decodeEnvelope(t, &dispatchOut)["dispatch_id"].(string)
	if dispatchID == "" {
		t.Fatal("no-submit dispatch produced no dispatch id")
	}

	// The real crash: the submitter leases, the stub accepts (one task
	// exists on the board), and the process dies before the receipt.
	crash := e7t4Crashbin(t)
	dieOut, err := exec.Command(crash, "submit-die", "--db", e7t4StateDB(t, configPath), "--stub", stubExeOf(t, configPath), "--board", "agent-dispatch-test", "--title", "crash recovery probe", "--dispatch", dispatchID, "--ttl-seconds", "1").CombinedOutput()
	if err != nil {
		t.Fatalf("submit-die: %v: %s", err, dieOut)
	}
	if !strings.Contains(string(dieOut), "hard death after remote acceptance") {
		t.Fatalf("the after-acceptance die path must be the one that ran: %s", dieOut)
	}

	// The one-second lease expires on the wall clock; the drain alone
	// then heals the wedged lease.
	time.Sleep(2500 * time.Millisecond)
	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	if !strings.Contains(out.String(), `"recovered"`) {
		t.Fatalf("the drain must report the recovered lease: %s", out.String())
	}
	healed := e5t1Store(t, configPath)
	defer healed.Close()
	var after string
	if err := healed.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != "retry_wait" && after != "dead_lettered" {
		t.Fatalf("the unprovable acceptance must land in the honest retry or dead-letter state, got %q", after)
	}

	// The operator retry resubmits the SAME idempotency key: the stub
	// dedups back to the original task, so no second task is created.
	if code := Run([]string{"dispatches", "retry", "--config", configPath, dispatchID, "--reason", "operator confirmed the board task"}, &out, &errb); code != 0 {
		t.Fatalf("retry: %s", errb.String())
	}
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain after retry: %s", errb.String())
	}
	var final, external string
	if err := healed.QueryRow(`SELECT state, COALESCE(external_ref, '') FROM dispatch_intents WHERE dispatch_id = ?`, dispatchID).Scan(&final, &external); err != nil {
		t.Fatal(err)
	}
	if final != "accepted" {
		t.Fatalf("the dedup-safe retry must reach acceptance, got %q (drain: %s)", final, out.String())
	}
	if !strings.HasPrefix(external, "t_") {
		t.Fatalf("the accepted retry must carry the original task reference, got %q", external)
	}
	// Exactly one authoritative task exists on the board.
	countRaw, err := os.ReadFile(filepath.Join(stubStateDir(t, configPath), "count"))
	if err != nil {
		t.Fatalf("stub count: %v", err)
	}
	if got := strings.TrimSpace(string(countRaw)); got != "1" {
		t.Fatalf("exactly one task may exist on the board, got %s", got)
	}
}

package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// TestE9T4MigrationLockStealPrevention pins the T2-F002 contract beyond
// the mtime proxy: a concurrent waiter never steals a live holder's
// migration lock (the holder keeps ownership through the wait), the
// lock hands over on release, and a lock whose holder is provably dead
// (mtime past the staleness bound) is stolen and re-acquired.
func TestE9T4MigrationLockStealPrevention(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")

	// A live holder owns the lock.
	release, err := acquireMigrationFileLock(dbPath)
	if err != nil {
		t.Fatalf("holder acquire: %v", err)
	}
	lockPath := dbPath + ".migration-lock"
	holderPID := fmt.Sprint(os.Getpid())
	held, rerr := os.ReadFile(lockPath)
	if rerr != nil || strings.TrimSpace(string(held)) != holderPID {
		t.Fatalf("the holder must own the lock file: %q %v", held, rerr)
	}

	// A concurrent waiter polls but cannot steal the live lock.
	acquired := make(chan struct{})
	var waiterRelease func()
	go func() {
		rel, werr := acquireMigrationFileLock(dbPath)
		if werr != nil {
			t.Errorf("waiter acquire after release: %v", werr)
			return
		}
		waiterRelease = rel
		close(acquired)
	}()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case <-acquired:
			t.Fatal("a live lock must never be handed to a waiter")
		default:
		}
		current, cerr := os.ReadFile(lockPath)
		if cerr != nil || strings.TrimSpace(string(current)) != holderPID {
			t.Fatalf("the live holder's ownership must survive the waiter: %q %v", current, cerr)
		}
		time.Sleep(25 * time.Millisecond)
	}

	// Release hands the lock to the waiting acquirer.
	release()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter must acquire the lock once the holder releases it")
	}
	if waiterRelease != nil {
		waiterRelease()
	}

	// A dead holder's stale lock is stolen and re-acquired.
	if err := os.WriteFile(lockPath, []byte("999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * migrationLockStaleness)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	deadRelease, err := acquireMigrationFileLock(dbPath)
	if err != nil {
		t.Fatalf("a stale lock must be stolen and re-acquired: %v", err)
	}
	deadRelease()
}

// TestE9T4OverBudgetUncertainResolvesThroughReconciliation completes
// the T1-F007 end: the over-budget completion's UNCERTAIN resolution
// (pinned by TestCompleteActiveFollowupBudgetExhausted) is followed by
// the operator reconciliation leg — the retained slot and dirty
// generation resolve through ResolveUncertainReconciliation and the
// route lands IDLE with no second authoritative dispatch.
func TestE9T4OverBudgetUncertainResolvesThroughReconciliation(t *testing.T) {
	s := openTestStore(t)
	seedIntentChain(t, s, "dispatch-1")
	rec0, err := s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRouteRuntimeState(nil, "wiki-maintenance", rec0.Version, func(r *RouteRuntimeStateRecord) {
		r.ActivationState = "enabled"
		r.RouteState = "ACTIVE_DIRTY"
		r.ActiveDispatchID = "dispatch-1"
		r.DirtyGeneration = 1
	}); err != nil {
		t.Fatal(err)
	}
	out, err := s.CompleteActive(context.Background(), ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: "dispatch-1",
		ReceiptRef: "rcpt-work-1", Actor: "hermes-task", Now: now(),
		FollowupGeneration: state.MaxConsecutiveFollowups + 1,
	})
	if err != nil || out.RouteTo != state.RouteUncertain {
		t.Fatalf("setup: the over-budget completion must resolve through UNCERTAIN: %+v %v", out, err)
	}

	// The operator resolution: no work due, so the retained generation
	// collapses and the route lands IDLE through the followup-dropped
	// edge with the slot released and no new dispatch created.
	if err := s.ResolveUncertainReconciliation(context.Background(), "wiki-maintenance", nil, 1, false, "operator", now()); err != nil {
		t.Fatalf("resolving the uncertain route: %v", err)
	}
	rec, err := s.LoadRouteRuntimeState("wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if rec.RouteState != "IDLE" || rec.ActiveDispatchID != "" || rec.DirtyGeneration != 0 || rec.PendingReconcile {
		t.Fatalf("the resolution must land IDLE with the slot and generation cleared: %+v", rec)
	}
	var intents int
	if err := s.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents); err != nil || intents != 1 {
		t.Fatalf("the resolution with no work due must create no dispatch: %d %v", intents, err)
	}
}

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// TestE16T1MigrationV19UpgradesCleanly proves the acceptance set on a
// v18-era database: existing notification identities and attempts
// survive unchanged, migrated pending notifications are immediately
// due, the lease columns start unowned, and the drain-run evidence
// table appears.
func TestE16T1MigrationV19UpgradesCleanly(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/v18.db"
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.migrations = Migrations[:18]
	if err := s.Migrate(dir); err != nil {
		t.Fatal(err)
	}
	// Seed one pending and one delivered notification with an attempt,
	// in the pre-v19 column shape.
	createdAt := "2026-08-20T10:00:00Z"
	for _, row := range []struct {
		id, state string
	}{
		{"ntf-pending-1", "pending"},
		{"ntf-delivered-1", "delivered"},
	} {
		if _, err := s.Exec(`INSERT INTO notification_events
			(notification_id, route_id, event, destination_id, transition, sink_id, sink_type, policy_revision, idempotency_key, payload_json, state, created_at, resolved_at)
			VALUES (?, 'wiki-maintenance', 'reconciliation_required', NULL, 't-' || ?, 'ops-log', 'log', 'policy-rev-1', 'idem-' || ?, '{}', ?, ?, NULL)`,
			row.id, row.id[4:], row.id, row.state, createdAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Exec(`INSERT INTO notification_attempts (attempt_id, notification_id, attempt_number, outcome, error_code, response_digest, started_at, completed_at)
		VALUES ('att-1', 'ntf-delivered-1', 1, 'delivered', NULL, 'dgest', '2026-08-20T10:00:01Z', '2026-08-20T10:00:02Z')`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { upgraded.Close() })
	if err := upgraded.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	version, err := upgraded.SchemaVersion()
	if err != nil || version != MaxSchemaVersion {
		t.Fatalf("the upgraded ledger must reach v19: %d %v", version, err)
	}
	// Identities and attempts survive unchanged.
	var id, idem, state string
	if err := upgraded.QueryRow(`SELECT notification_id, idempotency_key, state FROM notification_events WHERE notification_id = 'ntf-pending-1'`).Scan(&id, &idem, &state); err != nil {
		t.Fatal(err)
	}
	if id != "ntf-pending-1" || idem != "idem-ntf-pending-1" || state != "pending" {
		t.Fatalf("identity drift: %s %s %s", id, idem, state)
	}
	var attempts int
	if err := upgraded.QueryRow(`SELECT COUNT(*) FROM notification_attempts WHERE notification_id = 'ntf-delivered-1'`).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("attempt survival: %d %v", attempts, err)
	}
	// Migrated pending work is immediately due: the backfill sets due_at
	// to created_at, which is in the past.
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	var dueAt string
	if err := upgraded.QueryRow(`SELECT due_at FROM notification_events WHERE notification_id = 'ntf-pending-1'`).Scan(&dueAt); err != nil {
		t.Fatal(err)
	}
	if dueAt != createdAt || dueAt > now {
		t.Fatalf("migrated pending must be immediately due: due_at=%s now=%s", dueAt, now)
	}
	// The lease columns start unowned with token 0 and no expiry.
	var owner string
	var token int
	var expires *string
	if err := upgraded.QueryRow(`SELECT lease_owner, lease_token, lease_expires_at FROM notification_events WHERE notification_id = 'ntf-pending-1'`).Scan(&owner, &token, &expires); err != nil {
		t.Fatal(err)
	}
	if owner != "" || token != 0 || expires != nil {
		t.Fatalf("fresh lease columns must be unowned: %q %d %v", owner, token, expires != nil)
	}
	// The drain-run evidence table exists.
	if _, err := upgraded.Exec(`SELECT 1 FROM drain_runs LIMIT 1`); err != nil {
		t.Fatalf("v19 must create drain_runs: %v", err)
	}
}

// TestE16T1FreshEnqueueIsImmediatelyDue pins that a fresh notification
// intent carries due_at at creation time: only a retryable or ambiguous
// outcome pushes it into the backoff future (E16-T2).
func TestE16T1FreshEnqueueIsImmediatelyDue(t *testing.T) {
	s := openTestStore(t)
	e13t1Policy(s, records.EventReconciliationRequired)
	ctx := context.Background()
	if err := s.MarkPendingReconcile(ctx, "wiki-maintenance", "pos-e16t1", "2026-08-30T09:00:00Z"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Query(`SELECT created_at, due_at FROM notification_events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var createdAt, dueAt string
		if err := rows.Scan(&createdAt, &dueAt); err != nil {
			t.Fatal(err)
		}
		if createdAt != dueAt {
			t.Fatalf("a fresh intent must be immediately due: created=%s due=%s", createdAt, dueAt)
		}
		n++
	}
	if n == 0 {
		t.Fatal("the fixture must enqueue at least one notification")
	}
}

// TestE16T1DrainRunEvidence covers the drain-run record surface: an
// opened pass persists, a completed pass is finalized exactly once, and
// an unknown identity fails loudly.
func TestE16T1DrainRunEvidence(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	in := ports.DrainRunInput{DrainID: "drain-1", RouteID: "wiki-maintenance", Trigger: "manual", Mode: "manual", StartedAt: "2026-08-30T09:00:00Z"}
	if err := s.StartDrainRun(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishDrainRun(ctx, "drain-1", ports.DrainRunCounts{Claimed: 3, Delivered: 2, Refused: 1, RetryScheduled: 0, BudgetExpired: true}, "2026-08-30T09:00:09Z"); err != nil {
		t.Fatal(err)
	}
	var trigger, mode string
	var claimed, delivered, refused, retryScheduled, budget int
	var completedAt *string
	if err := s.QueryRow(`SELECT trigger, mode, claimed, delivered, refused, retry_scheduled, budget_expired, completed_at FROM drain_runs WHERE drain_id = 'drain-1'`).
		Scan(&trigger, &mode, &claimed, &delivered, &refused, &retryScheduled, &budget, &completedAt); err != nil {
		t.Fatal(err)
	}
	if trigger != "manual" || mode != "manual" || claimed != 3 || delivered != 2 || refused != 1 || retryScheduled != 0 || budget != 1 || completedAt == nil {
		t.Fatalf("drain-run evidence drift: trigger=%s mode=%s %d/%d/%d/%d budget=%d completed=%v",
			trigger, mode, claimed, delivered, refused, retryScheduled, budget, completedAt != nil)
	}
	// Completion is idempotent-refusing: the row is finalized once.
	if err := s.FinishDrainRun(ctx, "drain-1", ports.DrainRunCounts{}, "2026-08-30T09:01:00Z"); err == nil {
		t.Fatal("a finalized drain run must not be re-finalized")
	}
	if err := s.FinishDrainRun(ctx, "drain-missing", ports.DrainRunCounts{}, "2026-08-30T09:01:00Z"); err == nil {
		t.Fatal("an unknown drain identity must fail loudly")
	}
}

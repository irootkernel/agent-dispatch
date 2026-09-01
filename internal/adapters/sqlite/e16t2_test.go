package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// e16t2Seed enqueues n pending notifications under the test policy and
// returns their IDs.
func e16t2Seed(t *testing.T, s *Store, n int, createdAt string) []string {
	t.Helper()
	e13t1Policy(s, records.EventReconciliationRequired)
	ctx := context.Background()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if _, err := s.EnqueueRouteNotification(ctx, "wiki-maintenance", records.EventReconciliationRequired, fmt.Sprintf("route:wiki-maintenance:e16t2:%d", i), "", nil, createdAt); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.Query(`SELECT notification_id FROM notification_events ORDER BY notification_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if len(ids) < n {
		t.Fatalf("seed produced %d ids, want at least %d", len(ids), n)
	}
	return ids
}

func e16t2Backoff() ports.NotificationBackoff {
	return ports.NotificationBackoff{Initial: 30 * time.Second, Max: 15 * time.Minute, Multiplier: 2.0, JitterFraction: 0.0}
}

// TestE16T2ConcurrentDrainersClaimDisjointWork pins NTF-011: two
// drainers claiming the same due pool receive disjoint records and the
// union covers the pool.
func TestE16T2ConcurrentDrainersClaimDisjointWork(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	if err := a.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := a.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := a.RegisterRoute(nil, "wiki-maintenance", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := a.InitializeRouteState(nil, "wiki-maintenance"); err != nil {
		t.Fatal(err)
	}
	// Three transitions under the two-sink test policy produce six
	// notifications: drainer A's bounded claim takes the whole pool.
	e16t2Seed(t, a, 3, "2026-08-30T09:00:00Z")
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	ctx := context.Background()
	claimsA, err := a.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 6, Owner: "drainer-a"})
	if err != nil {
		t.Fatal(err)
	}
	claimsB, err := b.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 6, Owner: "drainer-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimsA) != 6 {
		t.Fatalf("drainer A must claim the pool: %d", len(claimsA))
	}
	if len(claimsB) != 0 {
		t.Fatalf("a live lease cannot be re-claimed: %d", len(claimsB))
	}
	seen := map[string]bool{}
	for _, c := range claimsA {
		seen[c.NotificationID] = true
		if c.LeaseToken != 1 {
			t.Fatalf("first claim advances the token to 1, got %d", c.LeaseToken)
		}
	}
	if len(seen) != 6 {
		t.Fatalf("claims must be distinct: %d", len(seen))
	}
}

// TestE16T2StaleOwnerCannotCommitAfterRecovery pins NTF-012: once an
// expired lease is recovered by another drainer (token advanced), the
// stale owner's fenced outcome is refused and records nothing.
func TestE16T2StaleOwnerCannotCommitAfterRecovery(t *testing.T) {
	s := openTestStore(t)
	ids := e16t2Seed(t, s, 2, "2026-08-30T09:00:00Z")
	ctx := context.Background()
	stale, err := s.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 2, Owner: "stale"})
	if err != nil || len(stale) != 2 {
		t.Fatalf("stale claim: %d %v", len(stale), err)
	}
	// Force the lease into expiry so recovery is legal.
	if _, err := s.Exec(`UPDATE notification_events SET lease_expires_at = '2026-08-30T09:00:01Z'`); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 2, Owner: "fresh"})
	if err != nil || len(fresh) != 2 {
		t.Fatalf("recovery claim: %d %v", len(fresh), err)
	}
	for _, c := range fresh {
		if c.LeaseToken != 2 {
			t.Fatalf("recovery must advance the token, got %d", c.LeaseToken)
		}
	}
	// The stale owner's outcome is refused.
	_, err = s.RecordNotificationAttemptFenced(ctx, ports.NotificationAttemptInput{
		NotificationID: ids[0], Outcome: records.NotificationDeliveredOutcome,
		StartedAt: "2026-08-30T09:00:10Z", CompletedAt: "2026-08-30T09:00:11Z",
	}, stale[0], e16t2Backoff())
	if !errors.Is(err, ports.ErrNotificationLeaseLost) {
		t.Fatalf("a stale owner must fail with ErrNotificationLeaseLost, got %v", err)
	}
	var attempts int
	if err := s.QueryRow(`SELECT COUNT(*) FROM notification_attempts WHERE notification_id = ?`, ids[0]).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("the refused outcome must record nothing: %d %v", attempts, err)
	}
	// The recovering owner's outcome commits.
	if _, err := s.RecordNotificationAttemptFenced(ctx, ports.NotificationAttemptInput{
		NotificationID: ids[0], Outcome: records.NotificationDeliveredOutcome,
		StartedAt: "2026-08-30T09:00:12Z", CompletedAt: "2026-08-30T09:00:13Z",
	}, fresh[0], e16t2Backoff()); err != nil {
		t.Fatalf("the recovering owner commits: %v", err)
	}
	var state string
	if err := s.QueryRow(`SELECT state FROM notification_events WHERE notification_id = ?`, ids[0]).Scan(&state); err != nil || state != "delivered" {
		t.Fatalf("recovered delivery resolves: %s %v", state, err)
	}
}

// TestE16T2RetryableOutcomePersistsBackoffDeadline pins NTF-014: a
// retryable outcome persists a due deadline of completedAt plus the
// backoff delay (deterministic under a pinned jitter draw), and the
// notification stays pending under its original identity.
func TestE16T2RetryableOutcomePersistsBackoffDeadline(t *testing.T) {
	s := openTestStore(t)
	ids := e16t2Seed(t, s, 1, "2026-08-30T09:00:00Z")
	notificationJitterDraw = func() float64 { return 1.0 } // +20% at fraction 0.2... fraction set below
	defer func() { notificationJitterDraw = func() float64 { return 0.5 } }()
	notificationJitterDraw = func() float64 { return 0.75 } // +50% of the fraction window
	backoff := ports.NotificationBackoff{Initial: 30 * time.Second, Max: 15 * time.Minute, Multiplier: 2.0, JitterFraction: 0.2}
	ctx := context.Background()
	claims, err := s.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 1, Owner: "drainer"})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim: %d %v", len(claims), err)
	}
	if _, err := s.RecordNotificationAttemptFenced(ctx, ports.NotificationAttemptInput{
		NotificationID: ids[0], Outcome: records.NotificationRetryableOutcome, ErrorCode: "transport",
		StartedAt: "2099-01-01T00:00:10Z", CompletedAt: "2099-01-01T00:00:11Z",
	}, claims[0], backoff); err != nil {
		t.Fatal(err)
	}
	// Attempt 1 delay: 30s * (1 + (0.75*2-1)*0.2) = 30s * 1.1 = 33s.
	var dueAt, state, leaseOwner string
	if err := s.QueryRow(`SELECT due_at, state, lease_owner FROM notification_events WHERE notification_id = ?`, ids[0]).Scan(&dueAt, &state, &leaseOwner); err != nil {
		t.Fatal(err)
	}
	if dueAt != "2099-01-01T00:00:44Z" {
		t.Fatalf("persisted deadline must be completedAt + jittered delay, got %s", dueAt)
	}
	if state != "pending" || leaseOwner != "" {
		t.Fatalf("a retryable outcome stays pending with the lease released: state=%s owner=%q", state, leaseOwner)
	}
	// A future-due notification is not claimable; the deadline governs.
	// (The policy carries two sinks, so a sibling notification may be
	// claimable — the retried identity itself must not be.)
	due, err := s.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 10, Owner: "drainer-2"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range due {
		if c.NotificationID == ids[0] {
			t.Fatalf("future-due work must not be claimed: %s (due %s)", c.NotificationID, c.DueAt)
		}
	}
}

// TestE16T2RetryNotificationBypassesBackoff pins NTF-013: the explicit
// operator retry is the sole bypass — an ambiguous/retryable pending
// record and a refused record both return to pending immediately due.
func TestE16T2RetryNotificationBypassesBackoff(t *testing.T) {
	s := openTestStore(t)
	ids := e16t2Seed(t, s, 2, "2026-08-30T09:00:00Z")
	notificationJitterDraw = func() float64 { return 0.5 }
	defer func() { notificationJitterDraw = func() float64 { return 0.5 } }()
	ctx := context.Background()
	claims, err := s.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 2, Owner: "drainer"})
	if err != nil || len(claims) != 2 {
		t.Fatalf("claim: %d %v", len(claims), err)
	}
	if _, err := s.RecordNotificationAttemptFenced(ctx, ports.NotificationAttemptInput{
		NotificationID: ids[0], Outcome: records.NotificationAmbiguousOutcome,
		StartedAt: "2026-08-30T09:00:10Z", CompletedAt: "2026-08-30T09:00:11Z",
	}, claims[0], e16t2Backoff()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordNotificationAttemptFenced(ctx, ports.NotificationAttemptInput{
		NotificationID: ids[1], Outcome: records.NotificationRefusedOutcome, ErrorCode: "endpoint_rejected",
		StartedAt: "2026-08-30T09:00:10Z", CompletedAt: "2026-08-30T09:00:11Z",
	}, claims[1], e16t2Backoff()); err != nil {
		t.Fatal(err)
	}
	// The retry bypass discards the backoff deadline and re-arms.
	if err := s.RetryNotification(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryNotification(ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		var state string
		var dueAt string
		if err := s.QueryRow(`SELECT state, due_at FROM notification_events WHERE notification_id = ?`, id).Scan(&state, &dueAt); err != nil {
			t.Fatal(err)
		}
		if state != "pending" {
			t.Fatalf("retry must re-arm to pending: %s", state)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if dueAt > now {
			t.Fatalf("retry must make the record immediately due: due=%s now=%s", dueAt, now)
		}
	}
	// Immediately-due work is claimable again.
	due, err := s.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 2, Owner: "drainer"})
	if err != nil || len(due) != 2 {
		t.Fatalf("re-armed work must be claimable: %d %v", len(due), err)
	}
}

// TestE16T2DeliveryNeverTouchesOtherTables pins AC-1208: a full fenced
// drain pass leaves every non-notification table's row count unchanged.
func TestE16T2DeliveryNeverTouchesOtherTables(t *testing.T) {
	s := openTestStore(t)
	e16t2Seed(t, s, 2, "2026-08-30T09:00:00Z")
	before := map[string]int{}
	for _, table := range []string{"dispatch_intents", "dispatch_attempts", "dispatch_receipts", "work_receipts", "policy_decisions", "change_batches", "route_runtime_state", "destination_lane_state"} {
		var n int
		if err := s.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		before[table] = n
	}
	notificationJitterDraw = func() float64 { return 0.5 }
	defer func() { notificationJitterDraw = func() float64 { return 0.5 } }()
	ctx := context.Background()
	claims, err := s.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 2, Owner: "drainer"})
	if err != nil || len(claims) != 2 {
		t.Fatalf("claim: %d %v", len(claims), err)
	}
	for i, claim := range claims {
		outcome := records.NotificationDeliveredOutcome
		if i == 1 {
			outcome = records.NotificationRetryableOutcome
		}
		if _, err := s.RecordNotificationAttemptFenced(ctx, ports.NotificationAttemptInput{
			NotificationID: claim.NotificationID, Outcome: outcome,
			StartedAt: "2026-08-30T09:00:10Z", CompletedAt: "2026-08-30T09:00:11Z",
		}, claim, e16t2Backoff()); err != nil {
			t.Fatal(err)
		}
	}
	for table, n := range before {
		var after int
		if err := s.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != n {
			t.Fatalf("%s changed across a drain pass: %d -> %d", table, n, after)
		}
	}
}

// TestE16T2ReleaseUnstartedClaims pins the budget-expiry posture:
// released claims lose their lease and are immediately claimable again.
func TestE16T2ReleaseUnstartedClaims(t *testing.T) {
	s := openTestStore(t)
	e16t2Seed(t, s, 2, "2026-08-30T09:00:00Z")
	ctx := context.Background()
	claims, err := s.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 2, Owner: "drainer"})
	if err != nil || len(claims) != 2 {
		t.Fatalf("claim: %d %v", len(claims), err)
	}
	if err := s.ReleaseNotificationClaims(ctx, "drainer", claims); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := s.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 2, Owner: "drainer-2"})
	if err != nil || len(reclaimed) != 2 {
		t.Fatalf("released claims must be immediately claimable: %d %v", len(reclaimed), err)
	}
	for _, c := range reclaimed {
		if c.LeaseToken != 2 {
			t.Fatalf("reclaim advances the token, got %d", c.LeaseToken)
		}
	}
}

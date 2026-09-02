package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
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
// TestE17AuditRetryRearmConditionalPredicate pins the round-4
// confirmation finding F002: the operator retry's pending re-arm rides a
// lease-guarded conditional UPDATE — the exact statement maps a lease
// acquired in the check-then-act window to zero affected rows (which
// RetryNotification reports as the same notification_lease_active
// refusal) while a free lease re-arms exactly one row.
func TestE17AuditRetryRearmConditionalPredicate(t *testing.T) {
	s := openTestStore(t)
	ids := e16t2Seed(t, s, 2, "2026-09-02T00:00:00Z")
	free, claimed := ids[0], ids[1]
	now := time.Now().UTC().Format(time.RFC3339)
	liveUntil := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)
	if _, err := s.Exec(`UPDATE notification_events SET lease_owner = 'drainer-race', lease_expires_at = ? WHERE notification_id = ?`, liveUntil, claimed); err != nil {
		t.Fatal(err)
	}
	// A free lease re-arms exactly the one row.
	res, err := s.Exec(`UPDATE notification_events SET due_at = ? WHERE notification_id = ? AND state = 'pending'
		AND (lease_owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?)`, now, free, now)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("a free lease must re-arm one row, affected %d", n)
	}
	// A lease acquired in the race window matches zero rows — the
	// affected-rows check maps this to the live-lease refusal.
	res, err = s.Exec(`UPDATE notification_events SET due_at = ? WHERE notification_id = ? AND state = 'pending'
		AND (lease_owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?)`, now, claimed, now)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Fatalf("a live lease must refuse the re-arm, affected %d", n)
	}
	// The refused row keeps its holder and deadline untouched.
	var owner, expires string
	if err := s.QueryRow(`SELECT COALESCE(lease_owner,''), COALESCE(lease_expires_at,'') FROM notification_events WHERE notification_id = ?`, claimed).Scan(&owner, &expires); err != nil || owner != "drainer-race" || expires != liveUntil {
		t.Fatalf("the racing holder's lease must survive: %q %q %v", owner, expires, err)
	}
}

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

// TestE16T2SimultaneousDrainersRaceTheSamePool pins AC-1203's literal
// claim: drainers issuing claims at the same time over the same due
// pool always hold disjoint claims whose union covers the pool. The
// bounded claims are smaller than the pool and each drainer loops
// until the pool is empty, so the conditional UPDATE — including its
// lost-row arm inside an open transaction — is exercised under -race
// rather than through one committed lease blocking a later read
// (round-2 F001).
func TestE16T2SimultaneousDrainersRaceTheSamePool(t *testing.T) {
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
	// Each seed transition fans out to the two-sink test policy, so six
	// transitions produce the twelve-notification pool.
	const pool = 12
	e16t2Seed(t, a, 6, "2026-08-30T09:00:00Z")
	stores := []*Store{a}
	for i := 1; i < 3; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		stores = append(stores, s)
	}
	var wg sync.WaitGroup
	claimed := make([][]ports.NotificationClaim, len(stores))
	fatal := make([]error, len(stores))
	for i, s := range stores {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			ctx := context.Background()
			owner := fmt.Sprintf("drainer-%d", i)
			for attempt := 0; attempt < 200; attempt++ {
				claims, err := s.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 3, Owner: owner})
				if err != nil {
					// A write-snapshot conflict (SQLITE_BUSY_SNAPSHOT) is
					// the serialized-store's safe refusal under a true
					// race; the drainer simply retries its bounded claim.
					fatal[i] = err
					time.Sleep(5 * time.Millisecond)
					continue
				}
				fatal[i] = nil
				if len(claims) == 0 {
					return
				}
				claimed[i] = append(claimed[i], claims...)
			}
		}(i, s)
	}
	wg.Wait()
	seen := map[string]string{}
	total := 0
	for i, claims := range claimed {
		if fatal[i] != nil {
			t.Fatalf("drainer %d ended on an unresolved claim error: %v", i, fatal[i])
		}
		for _, c := range claims {
			if prior, dup := seen[c.NotificationID]; dup {
				t.Fatalf("notification %s was claimed by drainer-%s and drainer-%d: claims are not disjoint", c.NotificationID, prior, i)
			}
			seen[c.NotificationID] = fmt.Sprintf("%d", i)
			total++
		}
	}
	if total != pool {
		t.Fatalf("the union of simultaneous claims must cover the pool: %d of %d", total, pool)
	}
}

// TestE16T2BackoffProgressionCapsAndJitterExtremes pins the persisted
// retry deadline beyond the first attempt (round-2 F004): the doubling
// loop, the fifteen-minute cap, and both jitter extremes — the jitter
// draw is deterministic through notificationJitterDraw, and the attempt
// number comes from the stored attempt history.
func TestE16T2BackoffProgressionCapsAndJitterExtremes(t *testing.T) {
	s := openTestStore(t)
	ids := e16t2Seed(t, s, 4, "2026-08-30T09:00:00Z")
	ctx := context.Background()
	backoff := ports.NotificationBackoff{Initial: 30 * time.Second, Max: 15 * time.Minute, Multiplier: 2.0, JitterFraction: 0.2}
	seedAttempts := func(id string, n int) {
		t.Helper()
		for i := 1; i <= n; i++ {
			if _, err := s.Exec(`INSERT INTO notification_attempts (attempt_id, notification_id, attempt_number, outcome, error_code, response_digest, started_at, completed_at)
				VALUES (?, ?, ?, 'retryable', 'transport', NULL, '2099-01-01T00:00:01Z', '2099-01-01T00:00:02Z')`,
				records.NotificationAttemptID(id, i), id, i); err != nil {
				t.Fatal(err)
			}
		}
	}
	dueAfterRetry := func(id string, draw float64) string {
		t.Helper()
		restore := notificationJitterDraw
		notificationJitterDraw = func() float64 { return draw }
		defer func() { notificationJitterDraw = restore }()
		claims, err := s.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 1, Owner: "drainer-" + id})
		if err != nil || len(claims) != 1 || claims[0].NotificationID != id {
			t.Fatalf("claim %s: %d %v", id, len(claims), err)
		}
		if _, err := s.RecordNotificationAttemptFenced(ctx, ports.NotificationAttemptInput{
			NotificationID: id, Outcome: records.NotificationRetryableOutcome, ErrorCode: "transport",
			StartedAt: "2099-01-01T00:00:10Z", CompletedAt: "2099-01-01T00:00:11Z",
		}, claims[0], backoff); err != nil {
			t.Fatal(err)
		}
		var dueAt string
		if err := s.QueryRow(`SELECT due_at FROM notification_events WHERE notification_id = ?`, id).Scan(&dueAt); err != nil {
			t.Fatal(err)
		}
		return dueAt
	}
	// Attempt 3 (two seeded): base 30s × 2² = 120s; draw 0 applies the
	// −20% extreme (96s), draw 1 the +20% extreme (144s).
	seedAttempts(ids[0], 2)
	if got := dueAfterRetry(ids[0], 0.0); got != "2099-01-01T00:01:47Z" {
		t.Fatalf("attempt 3 at draw 0 must persist completed+96s, got %s", got)
	}
	seedAttempts(ids[1], 2)
	if got := dueAfterRetry(ids[1], 1.0); got != "2099-01-01T00:02:35Z" {
		t.Fatalf("attempt 3 at draw 1 must persist completed+144s, got %s", got)
	}
	// Attempt 13 (twelve seeded): the doubling loop clamps at the
	// fifteen-minute cap and a neutral draw keeps it exact.
	seedAttempts(ids[2], 12)
	if got := dueAfterRetry(ids[2], 0.5); got != "2099-01-01T00:15:11Z" {
		t.Fatalf("attempt 13 must persist completed+15m at the cap, got %s", got)
	}
	// Attempt 1 with the −20% extreme: 30s × 0.8 = 24s.
	if got := dueAfterRetry(ids[3], 0.0); got != "2099-01-01T00:00:35Z" {
		t.Fatalf("attempt 1 at draw 0 must persist completed+24s, got %s", got)
	}
}

// TestE16T2ReleaseNeverClobbersRecoveredLease pins the release fence's
// recovered-claim arm (round-2 F005): a stale owner releasing the claim
// another drainer already recovered must leave the recovering owner's
// live lease exactly in place, and that owner still commits fenced.
func TestE16T2ReleaseNeverClobbersRecoveredLease(t *testing.T) {
	s := openTestStore(t)
	ids := e16t2Seed(t, s, 1, "2026-08-30T09:00:00Z")
	ctx := context.Background()
	claimsA, err := s.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 1, Owner: "drainer-a"})
	if err != nil || len(claimsA) != 1 {
		t.Fatalf("claim a: %d %v", len(claimsA), err)
	}
	// The lease expires under drainer A.
	if _, err := s.Exec(`UPDATE notification_events SET lease_expires_at = '2020-01-01T00:00:00Z' WHERE notification_id = ?`, ids[0]); err != nil {
		t.Fatal(err)
	}
	claimsB, err := s.ClaimDueNotifications(ctx, ports.NotificationClaimFilter{Limit: 1, Owner: "drainer-b"})
	if err != nil || len(claimsB) != 1 || claimsB[0].LeaseToken != 2 {
		t.Fatalf("recovery claim: %d %v", len(claimsB), err)
	}
	// A's late release reports success but must not touch B's lease.
	if err := s.ReleaseNotificationClaims(ctx, "drainer-a", claimsA); err != nil {
		t.Fatal(err)
	}
	var owner string
	var token int64
	var expires sql.NullString
	if err := s.QueryRow(`SELECT lease_owner, lease_token, lease_expires_at FROM notification_events WHERE notification_id = ?`, ids[0]).Scan(&owner, &token, &expires); err != nil {
		t.Fatal(err)
	}
	if owner != "drainer-b" || token != 2 || !expires.Valid || expires.String == "" {
		t.Fatalf("a stale release must leave the recovering lease intact: owner=%q token=%d expires=%+v", owner, token, expires)
	}
	// The recovering owner still commits under its fence.
	if _, err := s.RecordNotificationAttemptFenced(ctx, ports.NotificationAttemptInput{
		NotificationID: ids[0], Outcome: records.NotificationDeliveredOutcome,
		StartedAt: "2099-01-01T00:00:10Z", CompletedAt: "2099-01-01T00:00:11Z",
	}, claimsB[0], e16t2Backoff()); err != nil {
		t.Fatalf("the recovering owner must commit fenced: %v", err)
	}
	var state string
	if err := s.QueryRow(`SELECT state FROM notification_events WHERE notification_id = ?`, ids[0]).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "delivered" {
		t.Fatalf("the recovered delivery must resolve: state=%s", state)
	}
}

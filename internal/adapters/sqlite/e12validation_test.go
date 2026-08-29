package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E12 cold-validation hardening (round 1 of the second whole-epic
// validation): the store-boundary dangling-reference arm, the stale
// evidence identity, the retry_wait backoff isolation, the v15
// merge-selection evidence upgrade and fail-closed arms, and the blocked
// outcome's lane-untouched contract.

// TestE12ValidationDanglingRevisionFailsClosedAndRollsBack pins the
// second store-boundary arm (E12 epic validation): a child whose
// destination revision is neither supplied beside the fanout nor already
// durable fails closed as the NON-RETRYABLE validation class, and the
// whole intent transaction rolls back — no child, no aggregate, no
// intent row survives.
func TestE12ValidationDanglingRevisionFailsClosedAndRollsBack(t *testing.T) {
	s := openTestStore(t)
	lin := e12t2LaneLineage("dispatch-dangling", "decision-dangling", "batch-dangling", "wiki-primary")
	// The child references a revision that neither the supplied Revisions
	// nor any earlier arrival persisted.
	lin.Intent.Fanout.DestinationRevision = "dst-" + repeat("f", 64)
	lin.Intent.Fanout.Revisions = nil
	err := s.CommitLineage(context.Background(), lin)
	if !errors.Is(err, ports.ErrInvalidFanoutRecord) {
		t.Fatalf("a dangling child revision must fail closed as the invalid-record class: %v", err)
	}
	for _, probe := range []struct{ table, where string }{
		{"child_dispatches", "dispatch_id = 'dispatch-dangling'"},
		{"aggregate_events", "aggregate_id = 'agg-e12t2'"},
		{"dispatch_intents", "dispatch_id = 'dispatch-dangling'"},
	} {
		var n int
		if err := s.QueryRow(`SELECT COUNT(*) FROM ` + probe.table + ` WHERE ` + probe.where).Scan(&n); err != nil || n != 0 {
			t.Fatalf("the failed fanout transaction must roll back %s: %d %v", probe.table, n, err)
		}
	}
}

// TestE12ValidationStaleEvidenceNamesOldestHolder pins the stale evidence
// identity (round 1): the refusal message and the stale audit name the
// lane holding the OLDEST active dispatch — the one whose age the
// eligibility rule measured — never a younger sibling that merely sorts
// first in destination order.
func TestE12ValidationStaleEvidenceNamesOldestHolder(t *testing.T) {
	s := openTestStore(t)
	first, second := e12t2CommitFanout(t, s, "decision-stale-evidence")
	// lane-a (wiki-primary, first in destination order) holds the YOUNGER
	// dispatch; lane-b (wiki-secondary) holds the OLDER one (two hours
	// old, five minutes for the younger).
	if _, err := s.Exec(`UPDATE dispatch_intents SET created_at = ? WHERE dispatch_id = ?`,
		time.Now().UTC().Add(-2*time.Hour).Format(time.RFC3339), second.Intent.DispatchID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE dispatch_intents SET created_at = ? WHERE dispatch_id = ?`,
		time.Now().UTC().Add(-5*time.Minute).Format(time.RFC3339), first.Intent.DispatchID); err != nil {
		t.Fatal(err)
	}
	// Inside a bound that only the OLDER dispatch exceeds: the refusal
	// names the older dispatch, not the destination-order-first younger
	// one.
	eligible, message, err := s.EligibleForStale(context.Background(), "wiki-maintenance", 3*time.Hour)
	if err != nil || eligible {
		t.Fatalf("the route must be inside the bound and refuse: %v %q %v", eligible, message, err)
	}
	if !strings.Contains(message, second.Intent.DispatchID) || strings.Contains(message, first.Intent.DispatchID) {
		t.Fatalf("the refusal must name the oldest holder %s, not %s: %q", second.Intent.DispatchID, first.Intent.DispatchID, message)
	}
	// Crossing the bound: the stale audit's evidence dispatch is the same
	// oldest holder.
	eligible, _, err = s.EligibleForStale(context.Background(), "wiki-maintenance", 30*time.Minute)
	if err != nil || !eligible {
		t.Fatalf("the older dispatch must make the route stale: %v %q %v", eligible, message, err)
	}
	if err := s.MarkRouteStaleWithReason(context.Background(), "wiki-maintenance", "operator", "evidence stale", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("stale the route: %v", err)
	}
	var audit string
	if err := s.QueryRow(`SELECT context_json FROM state_transitions
		WHERE context_json LIKE '%operator_stale%' AND (context_json LIKE '%' || ? || '%')`,
		second.Intent.DispatchID).Scan(&audit); err != nil {
		t.Fatalf("the stale audit must name the oldest holder %s: %v", second.Intent.DispatchID, err)
	}
	if strings.Contains(audit, first.Intent.DispatchID) {
		t.Fatalf("the stale audit must not name the younger holder: %s", audit)
	}
}

// TestE12ValidationRetryWaitBackoffIsolation pins the retry-isolation
// claim on the BACKOFF window (CON-009 beside the slot): a lane's
// retry_wait dispatch cannot reacquire before its next_attempt_at
// elapses, acquires after it does, and the sibling lane leases
// throughout — the backoff never reaches across lanes.
func TestE12ValidationRetryWaitBackoffIsolation(t *testing.T) {
	s := openTestStore(t)
	first, second := e12t2CommitFanout(t, s, "decision-backoff")
	// Move the first lane's dispatch into retry_wait with a future
	// backoff and free its slot (the lease predicate only admits the
	// dispatch when the lane slot is free for it).
	if _, err := s.Exec(`UPDATE destination_lane_state SET active_dispatch_id = NULL, lane_state = 'IDLE'
		WHERE route_id = 'wiki-maintenance' AND destination_id = 'wiki-primary'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE dispatch_intents SET state = 'retry_wait', next_attempt_at = '2030-01-01T00:00:00Z', lease_expires_at = NULL
		WHERE dispatch_id = ?`, first.Intent.DispatchID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: first.Intent.DispatchID, AttemptID: "attempt-backoff-a", Owner: "process-a",
		Now: "2026-08-29T01:00:00Z", LeaseExpiresAt: "2026-08-29T01:01:00Z",
	}); err == nil {
		t.Fatal("a lane in its retry_wait backoff must not reacquire before next_attempt_at")
	}
	// The sibling lane leases throughout the backoff window.
	if acquired, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: second.Intent.DispatchID, AttemptID: "attempt-backoff-b", Owner: "process-b",
		Now: "2026-08-29T01:00:30Z", LeaseExpiresAt: "2026-08-29T01:01:30Z",
	}); err != nil || acquired != "attempt-backoff-b" {
		t.Fatalf("the sibling lane must lease during the other lane's backoff: %q %v", acquired, err)
	}
	var siblingState string
	if err := s.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, first.Intent.DispatchID).Scan(&siblingState); err != nil || siblingState != "retry_wait" {
		t.Fatalf("the backing-off dispatch must be untouched by the sibling's lease: %q %v", siblingState, err)
	}
	// Once the backoff elapses the retrying dispatch acquires again.
	if _, err := s.Exec(`UPDATE dispatch_intents SET next_attempt_at = '2026-08-28T00:00:00Z' WHERE dispatch_id = ?`, first.Intent.DispatchID); err != nil {
		t.Fatal(err)
	}
	if acquired, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: first.Intent.DispatchID, AttemptID: "attempt-backoff-a2", Owner: "process-a",
		Now: "2026-08-29T01:01:00Z", LeaseExpiresAt: "2026-08-29T01:02:00Z",
	}); err != nil || acquired != "attempt-backoff-a2" {
		t.Fatalf("the dispatch must reacquire after its backoff elapses: %q %v", acquired, err)
	}
}

// TestE12ValidationV15UpgradeKeepsLegacyRowsAndRecordsEvidence pins the
// v15 cutover: a v14-era change_batches row upgrades with NULL selection
// evidence (the read leaves SelectedDestinations empty — the per-change
// fallback), and a post-upgrade merge records the occurrence's canonical
// sorted/deduped selection on the shared batch.
func TestE12ValidationV15UpgradeKeepsLegacyRowsAndRecordsEvidence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	old.migrations = Migrations[:14]
	if err := old.Migrate(dir); err != nil {
		t.Fatal(err)
	}
	// The same route envelope the shared test store seeds.
	if err := old.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := old.RegisterRoute(nil, "wiki-maintenance", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", now()); err != nil {
		t.Fatal(err)
	}
	if err := old.InitializeRouteState(nil, "wiki-maintenance"); err != nil {
		t.Fatal(err)
	}
	if err := old.SetRouteActivation(context.Background(), "wiki-maintenance", "enabled", "route-rev-1", "", now()); err != nil {
		t.Fatal(err)
	}
	// A v14-era occurrence committed through the real path: SaveBatch
	// writes no selection evidence when none exists, so the row keeps the
	// legacy unrecorded shape against the v14 schema.
	e12t2CommitFanout(t, old, "decision-v15-legacy")
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(dir); err != nil {
		t.Fatalf("the v14 database must upgrade through v15: %v", err)
	}
	var evidence sql.NullString
	if err := s.QueryRow(`SELECT selected_destinations_json FROM change_batches WHERE batch_id = 'batch-decision-v15-legacy'`).Scan(&evidence); err != nil {
		t.Fatalf("the legacy row must survive the upgrade with its evidence column: %v", err)
	}
	if evidence.Valid && evidence.String != "" {
		t.Fatalf("a v14-era row must keep NULL selection evidence after the upgrade: %q", evidence.String)
	}
	// A post-upgrade merge records the occurrence's full selection in the
	// canonical form (sorted, deduped) on the shared batch. The upgraded
	// occurrence uses fresh identities beside the legacy rows and frees
	// the lanes the legacy occurrence still holds.
	if _, err := s.Exec(`UPDATE destination_lane_state SET active_dispatch_id = NULL, lane_state = 'IDLE' WHERE route_id = 'wiki-maintenance'`); err != nil {
		t.Fatal(err)
	}
	first := e12t2LaneLineage("dispatch-v15-second-x", "decision-v15-evidence", "batch-decision-v15-evidence", "wiki-primary")
	first.Intent.Fanout.AggregateID = "agg-v15-second"
	if err := s.CommitLineage(context.Background(), first); err != nil {
		t.Fatalf("commit first child: %v", err)
	}
	second := e12t2LaneLineage("dispatch-v15-second-y", "decision-v15-evidence", "batch-decision-v15-evidence", "wiki-secondary")
	second.Intent.Fanout.AggregateID = "agg-v15-second"
	if err := s.CommitFanoutChild(context.Background(), second.Intent); err != nil {
		t.Fatalf("commit second child: %v", err)
	}
	if err := s.ActivateDispatch(context.Background(), first.Intent.DispatchID, "test", "2026-08-29T04:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateDispatch(context.Background(), second.Intent.DispatchID, "test", "2026-08-29T04:00:01Z"); err != nil {
		t.Fatal(err)
	}
	merged := e12t2LaneLineage("dispatch-v15-merge", "decision-v15-merge", "batch-v15-merge", "wiki-primary")
	if _, err := s.CommitMergePending(context.Background(), merged, []string{"wiki-primary"}, []string{"wiki-secondary", "wiki-primary", "wiki-primary"}, "test", "2026-08-29T02:00:00Z"); err != nil {
		t.Fatalf("merge with occurrence-level selection evidence: %v", err)
	}
	var stored string
	if err := s.QueryRow(`SELECT selected_destinations_json FROM change_batches WHERE batch_id = 'batch-v15-merge'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != `["wiki-primary","wiki-secondary"]` {
		t.Fatalf("the merge must record the canonical sorted/deduped selection: %s", stored)
	}
	// The read populates the recorded selection for post-v15 batches and
	// leaves it empty for the legacy row (the fallback arm).
	changes, err := s.LoadActiveGenerationChanges(context.Background(), "wiki-maintenance", first.Intent.DispatchID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if c.BatchID != "batch-v15-merge" {
			continue
		}
		if len(c.SelectedDestinations) != 2 || c.SelectedDestinations[0] != "wiki-primary" || c.SelectedDestinations[1] != "wiki-secondary" {
			t.Fatalf("the read must carry the recorded selection: %+v", c.SelectedDestinations)
		}
	}
}

// TestE12ValidationSelectionEvidenceFailsClosed pins the fail-closed arms
// of the v15 evidence: a batch row whose recorded selection is not the
// JSON shape errors the read (never a silent narrowing), and a merge that
// names an absent batch fails closed as the invalid-record class.
func TestE12ValidationSelectionEvidenceFailsClosed(t *testing.T) {
	s := openTestStore(t)
	first, _ := e12t2CommitFanout(t, s, "decision-v15-failclosed")
	merged := e12t2LaneLineage("dispatch-v15-corrupt", "decision-v15-corrupt", "batch-v15-corrupt", "wiki-primary")
	if _, err := s.CommitMergePending(context.Background(), merged, []string{"wiki-primary"}, []string{"wiki-primary"}, "test", "2026-08-29T02:30:00Z"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, err := s.Exec(`UPDATE change_batches SET selected_destinations_json = '{not json' WHERE batch_id = 'batch-v15-corrupt'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadActiveGenerationChanges(context.Background(), "wiki-maintenance", first.Intent.DispatchID); err == nil {
		t.Fatal("a corrupted selection-evidence record must fail the read, not silently narrow it")
	}
}

// TestE12ValidationBlockWorkLeavesLaneUntouched asserts the blocked
// outcome's coordination contract directly (FBK-011): after BlockWork the
// lane row and the dispatch intent are byte-identical to before — no
// completion, no follow-up scheduling, no slot change.
func TestE12ValidationBlockWorkLeavesLaneUntouched(t *testing.T) {
	s := openTestStore(t)
	first, _ := e12t2CommitFanout(t, s, "decision-block-lane")
	seedBegunReceipt(t, s, first.Intent.DispatchID, "run-block-lane")
	var intentStateBefore string
	if err := s.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, first.Intent.DispatchID).Scan(&intentStateBefore); err != nil {
		t.Fatal(err)
	}
	var laneStateBefore string
	var activeBefore, dirtyBefore int
	if err := s.QueryRow(`SELECT lane_state, active_generation, dirty_generation FROM destination_lane_state
		WHERE route_id = 'wiki-maintenance' AND destination_id = 'wiki-primary'`).Scan(&laneStateBefore, &activeBefore, &dirtyBefore); err != nil {
		t.Fatal(err)
	}
	if err := s.BlockWork(context.Background(), ports.WorkReceiptInput{
		ReceiptID: "rcpt-block-lane", DispatchID: first.Intent.DispatchID, RunID: "run-block-lane",
		ResourceID: "vault-main", Status: "blocked", ManualReason: "operator decision required",
		SubmittedAt: "2026-08-29T03:00:00Z", ValidationState: "valid",
	}); err != nil {
		t.Fatalf("blocked transaction: %v", err)
	}
	var laneStateAfter string
	var activeAfter, dirtyAfter int
	if err := s.QueryRow(`SELECT lane_state, active_generation, dirty_generation FROM destination_lane_state
		WHERE route_id = 'wiki-maintenance' AND destination_id = 'wiki-primary'`).Scan(&laneStateAfter, &activeAfter, &dirtyAfter); err != nil {
		t.Fatal(err)
	}
	var activeID string
	if err := s.QueryRow(`SELECT active_dispatch_id FROM destination_lane_state
		WHERE route_id = 'wiki-maintenance' AND destination_id = 'wiki-primary'`).Scan(&activeID); err != nil || activeID != first.Intent.DispatchID {
		t.Fatalf("the blocked lane must keep its active dispatch: %q %v", activeID, err)
	}
	if laneStateBefore != laneStateAfter || activeBefore != activeAfter || dirtyBefore != dirtyAfter {
		t.Fatalf("the blocked outcome must not touch the lane row: %q/%d/%d -> %q/%d/%d",
			laneStateBefore, activeBefore, dirtyBefore, laneStateAfter, activeAfter, dirtyAfter)
	}
	var intentState string
	if err := s.QueryRow(`SELECT state FROM dispatch_intents WHERE dispatch_id = ?`, first.Intent.DispatchID).Scan(&intentState); err != nil || intentState != intentStateBefore {
		t.Fatalf("the blocked dispatch must keep its pre-block state %q: %q %v", intentStateBefore, intentState, err)
	}
	var followups int
	if err := s.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE generation > 1 AND route_id = 'wiki-maintenance'`).Scan(&followups); err != nil || followups != 0 {
		t.Fatalf("the blocked outcome must schedule no follow-up: %d %v", followups, err)
	}
}

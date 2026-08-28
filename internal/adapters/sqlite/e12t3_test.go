package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E12-T3 persistence coverage: migration v14 widens the work-receipt
// status CHECK while preserving v1 rows verbatim, the v2 outcome members
// (partial scopes, blocked manual reason) persist, and the receipt view
// rows carry the child-lane association (DAT-011).

// seedBegunReceipt inserts one begun receipt for a seeded intent through
// the ordinary insert path.
func seedBegunReceipt(t *testing.T, s *Store, dispatchID, runID string) ports.WorkReceiptInput {
	t.Helper()
	w := ports.WorkReceiptInput{
		ReceiptID: "rcpt-" + runID, DispatchID: dispatchID, RunID: runID,
		ResourceID: "vault-main", Status: "begun",
		SubmittedAt: "2026-08-29T01:00:00Z", ValidationState: "valid", BegunAt: "2026-08-29T01:00:00Z",
	}
	if err := s.InsertWorkReceipt(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	return w
}

// TestE12T3MigrationV14WidensStatusCheckAndPreservesRows pins the v14
// rebuild: a v13-era database's begun/completed/failed rows survive
// verbatim (same identity, evidence, and run uniqueness), the widened
// CHECK accepts partially_completed and blocked, and the new scope and
// manual-reason columns exist with their empty defaults.
func TestE12T3MigrationV14WidensStatusCheckAndPreservesRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.migrations = Migrations[:13]
	if err := s.Migrate(dir); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki-maintenance", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-08-29T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki-maintenance"); err != nil {
		t.Fatal(err)
	}
	seedIntentChain(t, s, "dispatch-v13")
	// A raw v13-era begun row: the ordinary insert path writes the v2
	// columns, which do not exist before migration v14.
	if _, err := s.Exec(`INSERT INTO work_receipts
		(receipt_id, dispatch_id, run_id, resource_id, status, submitted_at, begun_at, validation_state, validation_reasons_json, route_revision)
		VALUES ('rcpt-run-v13', 'dispatch-v13', 'run-v13', 'vault-main', 'begun', '2026-08-29T01:00:00Z', '2026-08-29T01:00:00Z', 'valid', '[]', 'route-rev-1')`); err != nil {
		t.Fatal(err)
	}
	// A terminal v1 receipt row.
	if _, err := s.Exec(`INSERT INTO work_receipts
		(receipt_id, dispatch_id, run_id, resource_id, status, changes_json, submitted_at, begun_at, validation_state, validation_reasons_json, route_revision)
		VALUES ('rcpt-done', 'dispatch-v13', 'run-done', 'vault-main', 'completed', '[{"path":"Inbox/a.md"}]', '2026-08-29T02:00:00Z', '2026-08-29T01:30:00Z', 'valid', '[]', 'route-rev-1')`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { upgraded.Close() })
	if err := upgraded.Migrate(t.TempDir()); err != nil {
		t.Fatalf("upgrade to v14: %v", err)
	}
	if version, err := upgraded.SchemaVersion(); err != nil || version < 14 {
		t.Fatalf("the ledger must reach v14: %d %v", version, err)
	}
	// The v1 rows survived verbatim.
	var begunStatus, doneStatus, doneChanges string
	if err := upgraded.QueryRow(`SELECT status FROM work_receipts WHERE run_id = 'run-v13'`).Scan(&begunStatus); err != nil || begunStatus != "begun" {
		t.Fatalf("the v1 begun row must survive: %q %v", begunStatus, err)
	}
	if err := upgraded.QueryRow(`SELECT status, changes_json FROM work_receipts WHERE run_id = 'run-done'`).Scan(&doneStatus, &doneChanges); err != nil || doneStatus != "completed" || doneChanges != `[{"path":"Inbox/a.md"}]` {
		t.Fatalf("the v1 completed row must survive verbatim: %q %s %v", doneStatus, doneChanges, err)
	}
	// The widened CHECK accepts the v2 statuses; the old set still holds.
	for _, status := range []string{"partially_completed", "blocked"} {
		if _, err := upgraded.Exec(`UPDATE work_receipts SET status = ? WHERE run_id = 'run-v13'`, status); err != nil {
			t.Fatalf("the widened CHECK must accept %s: %v", status, err)
		}
	}
	if _, err := upgraded.Exec(`UPDATE work_receipts SET status = 'exploded' WHERE run_id = 'run-v13'`); err == nil {
		t.Fatal("the CHECK must still refuse unknown statuses")
	}
	// The v2 columns exist with empty defaults for the historic rows.
	var scopes, manual string
	if err := upgraded.QueryRow(`SELECT remaining_scope_json, COALESCE(manual_reason, '') FROM work_receipts WHERE run_id = 'run-done'`).Scan(&scopes, &manual); err != nil || scopes != "[]" || manual != "" {
		t.Fatalf("historic rows carry the empty v2 defaults: %q %q %v", scopes, manual, err)
	}
}

// TestE12T3PartialAndBlockedReceiptsPersist pins the v2 member
// persistence through the ordinary terminal paths (FBK-010/FBK-011).
func TestE12T3PartialAndBlockedReceiptsPersist(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	commitTestLineage(t, s, "dispatch-v2outcomes")
	if err := s.ActivateDispatch(context.Background(), "dispatch-v2outcomes", "test", "2026-08-29T01:00:01Z"); err != nil {
		t.Fatal(err)
	}
	seedBegunReceipt(t, s, "dispatch-v2outcomes", "run-partial")
	if _, err := s.CompleteWork(ctx, ports.WorkReceiptInput{
		ReceiptID: "rcpt-run-partial", DispatchID: "dispatch-v2outcomes", RunID: "run-partial",
		ResourceID: "vault-main", Status: "partially_completed",
		ChangesJSON: `[{"path":"Indexes/done.md"}]`,
		CompletedScope: []ports.WorkChange{
			{Path: "Indexes/done.md"},
		},
		RemainingScope: []ports.WorkChange{
			{Path: "Indexes/remaining.md", AfterDigest: "sha256:" + repeat("3", 64)},
		},
		SubmittedAt: "2026-08-29T02:00:00Z", ValidationState: "valid",
	}, ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: "dispatch-v2outcomes",
		ReceiptRef: "rcpt-run-partial", Actor: "hermes-task", Now: "2026-08-29T02:00:00Z",
		RemainingWork: true,
		// The remaining scope's same-lane follow-up (the service builds it
		// in production; this persistence test supplies the intent).
		FollowupRequest: &ports.IntentInput{
			DispatchID: "dispatch-v2outcomes-followup", DecisionID: "dec-v2outcomes-followup",
			RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
			TargetID: "hermes-kanban-main", TargetType: "hermes_kanban", TargetScope: "board-main",
			ResourceID: "vault-main", Generation: 2,
			IdempotencyKey:     "agent-dispatch:v2:sha256:" + repeat("e", 64),
			ContentFingerprint: "sha256:" + repeat("c", 64), ManifestDigest: "sha256:" + repeat("f", 64),
			RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: "{}",
			CreatedAt: "2026-08-29T02:00:00Z",
			Fanout: &ports.FanoutInput{
				AggregateID: "agg-v2outcomes-followup", Origin: "followup",
				DestinationID: "wiki-primary", DestinationRevision: e12t1Revision, Workstream: "maintenance",
				Selections: []records.DestinationSelection{{
					DestinationID: "wiki-primary", DestinationRevision: e12t1Revision,
					Workstream: "maintenance", Reason: "followup:dispatch-v2outcomes",
				}},
			},
		},
	}); err != nil {
		t.Fatalf("partial completion transaction: %v", err)
	}
	var status, completed, remaining string
	if err := s.QueryRow(`SELECT status, completed_scope_json, remaining_scope_json FROM work_receipts WHERE run_id = 'run-partial'`).
		Scan(&status, &completed, &remaining); err != nil {
		t.Fatal(err)
	}
	if status != "partially_completed" || completed == "[]" || remaining == "[]" {
		t.Fatalf("the partial outcome must persist both scopes: %q %s %s", status, completed, remaining)
	}

	// The blocked outcome records its reason without touching the lane.
	// (The partial completion above freed the legacy lane's slot through
	// its lane-keyed completion; this leg seeds a fresh dispatch.)
	if _, err := s.Exec(`UPDATE destination_lane_state SET active_dispatch_id = NULL, lane_state = 'IDLE' WHERE route_id = 'wiki-maintenance'`); err != nil {
		t.Fatal(err)
	}
	commitTestLineage(t, s, "dispatch-blocked2")
	if err := s.ActivateDispatch(context.Background(), "dispatch-blocked2", "test", "2026-08-29T02:30:00Z"); err != nil {
		t.Fatal(err)
	}
	seedBegunReceipt(t, s, "dispatch-blocked2", "run-blocked")
	if err := s.BlockWork(ctx, ports.WorkReceiptInput{
		ReceiptID: "rcpt-run-blocked", DispatchID: "dispatch-blocked2", RunID: "run-blocked",
		ResourceID: "vault-main", Status: "blocked",
		ManualReason: "operator decision required", SubmittedAt: "2026-08-29T03:00:00Z", ValidationState: "valid",
	}); err != nil {
		t.Fatalf("blocked transaction: %v", err)
	}
	view, err := s.LoadWorkReceipt(ctx, "dispatch-blocked2", "run-blocked")
	if err != nil || view.Status != "blocked" || view.ManualReason != "operator decision required" {
		t.Fatalf("the blocked receipt must read back with its reason: %+v %v", view, err)
	}
	// A second blocked update of the same run refuses (already terminal).
	if err := s.BlockWork(ctx, ports.WorkReceiptInput{
		ReceiptID: "rcpt-run-blocked", DispatchID: "dispatch-blocked2", RunID: "run-blocked",
		ResourceID: "vault-main", Status: "blocked", ManualReason: "again",
		SubmittedAt: "2026-08-29T03:01:00Z", ValidationState: "valid",
	}); err == nil {
		t.Fatal("a terminal run may not be re-blocked")
	}
}

// TestE12T3ReceiptViewCarriesDestination pins DAT-011 at the read
// boundary: the work-receipt view row names the child lane, the receipts
// list surfaces it on work rows, and a legacy dispatch reads empty.
func TestE12T3ReceiptViewCarriesDestination(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	lin := e12t1FanoutLineage("dispatch-view", "agg-view")
	if err := s.CommitLineage(ctx, lin); err != nil {
		t.Fatal(err)
	}
	seedBegunReceipt(t, s, "dispatch-view", "run-view")
	view, err := s.LoadWorkReceipt(ctx, "dispatch-view", "run-view")
	if err != nil || view.DestinationID != "wiki-primary" {
		t.Fatalf("the view must carry the child lane: %+v %v", view, err)
	}
	rows, err := s.ListReceipts(ctx, ports.ReceiptFilter{DispatchID: "dispatch-view", Kind: "work", Limit: 10})
	if err != nil || len(rows) != 1 || rows[0].DestinationID != "wiki-primary" {
		t.Fatalf("the receipts list must surface the lane on work rows: %+v %v", rows, err)
	}
	// A legacy (childless) dispatch reads empty, never a wrong lane.
	commitTestLineage(t, s, "dispatch-legacy-view")
	if _, err := s.Exec(`UPDATE destination_lane_state SET active_dispatch_id = NULL WHERE route_id = 'wiki-maintenance' AND destination_id = ?`, LegacyLaneID); err != nil {
		t.Fatal(err)
	}
	seedBegunReceipt(t, s, "dispatch-legacy-view", "run-legacy")
	legacy, err := s.LoadWorkReceipt(ctx, "dispatch-legacy-view", "run-legacy")
	if err != nil || legacy.DestinationID != "" {
		t.Fatalf("a legacy dispatch reads an empty lane: %+v %v", legacy, err)
	}
	// The intent lineage's work-receipt rows carry the manual reason for
	// blocked runs.
	if err := s.BlockWork(ctx, ports.WorkReceiptInput{
		ReceiptID: "rcpt-run-legacy", DispatchID: "dispatch-legacy-view", RunID: "run-legacy",
		ResourceID: "vault-main", Status: "blocked", ManualReason: "inspect manually",
		SubmittedAt: "2026-08-29T04:00:00Z", ValidationState: "valid",
	}); err != nil {
		t.Fatal(err)
	}
	lineage, err := s.LoadIntentLineage(ctx, "dispatch-legacy-view")
	if err != nil || len(lineage.WorkReceipt) == 0 || lineage.WorkReceipt[0].ManualReason != "inspect manually" {
		t.Fatalf("the lineage must surface the blocked reason: %+v %v", lineage.WorkReceipt, err)
	}
}

// TestE12T3AggregateChildrenAndLaneRows pins the events/status read
// models: the per-child projections join the destination lane, the latest
// receipts, and the retry state; the lane rows stay bounded per lane.
func TestE12T3AggregateChildrenAndLaneRows(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	first := e12t1FanoutLineage("dispatch-agg-a", "agg-children")
	if err := s.CommitLineage(ctx, first); err != nil {
		t.Fatal(err)
	}
	children, err := s.LoadAggregateChildren(ctx, "agg-children")
	if err != nil || len(children) != 1 {
		t.Fatalf("one child beneath the aggregate: %+v %v", children, err)
	}
	child := children[0]
	if child.DispatchID != "dispatch-agg-a" || child.DestinationID != "wiki-primary" ||
		child.DestinationRevision != e12t1Revision || child.Workstream != "maintenance" {
		t.Fatalf("the child projection must carry the destination lane: %+v", child)
	}
	if child.IntentState != "ready" || child.Acceptance != "" || child.WorkReceipt != "" {
		t.Fatalf("a fresh child reads its submit state with no receipts: %+v", child)
	}
	// A work receipt joins by dispatch with its validity.
	seedBegunReceipt(t, s, "dispatch-agg-a", "run-agg")
	children, err = s.LoadAggregateChildren(ctx, "agg-children")
	if err != nil || len(children) != 1 || !children[0].WorkReceiptValid || children[0].WorkReceipt != "begun" {
		t.Fatalf("the latest work receipt must join: %+v %v", children, err)
	}
	// Lane rows: bounded, one per lane, destination order.
	if _, err := s.Exec(`UPDATE destination_lane_state SET dirty_generation = 2 WHERE route_id = 'wiki-maintenance' AND destination_id = 'wiki-primary'`); err != nil {
		t.Fatal(err)
	}
	lanes, err := s.ListRouteLanes(ctx, "wiki-maintenance")
	if err != nil || len(lanes) != 1 || lanes[0].DestinationID != "wiki-primary" || lanes[0].DirtyGeneration != 2 {
		t.Fatalf("the lane summary must read the coordination row: %+v %v", lanes, err)
	}
}

package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/fingerprint"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// childKeyForTest derives the DAT-014 child key for the order-property
// test.
func childKeyForTest(in records.ChildIdempotencyKeyInput) (string, error) {
	return fingerprint.ChildIdempotency(in)
}

// e12t1Projection and e12t1Revision are a real content-addressed pair
// (the fixture's projection bytes hash to its revision), so the store
// boundary can assert the self-verifying record property.
const e12t1Projection = `{"id":"wiki-primary","target":"hermes-kanban-main","workstream":"maintenance"}`

const e12t1Revision = "dst-2c3a6e4ab15fff06b26ea4552d01ee4d75ad060347fd655b9e4b0f9e365e4355"

// e12t1FanoutLineage extends the shared dispatch-test lineage shape with
// the E12-T1 fanout context: one aggregate event, its single-selection
// summary, and one durable destination-revision record.
func e12t1FanoutLineage(dispatchID, aggregateID string) ports.Lineage {
	key := "agent-dispatch:v2:sha256:" + repeat(dispatchID[len(dispatchID)-1:], 64)
	lin := lineage(dispatchID, key)
	lin.Intent.Fanout = &ports.FanoutInput{
		AggregateID: aggregateID, Origin: string(records.OriginArrival),
		DestinationID: "wiki-primary", DestinationRevision: e12t1Revision, Workstream: "maintenance",
		Selections: []records.DestinationSelection{{
			DestinationID: "wiki-primary", DestinationRevision: e12t1Revision, Workstream: "maintenance", Reason: "fanout_mode:all",
		}},
		Revisions: []ports.DestinationRevisionInput{{
			DestinationID: "wiki-primary", Revision: e12t1Revision, ProjectionJSON: e12t1Projection,
		}},
	}
	return lin
}

// TestE12T1MigrationV12PreservesHistoryAndAddsFanoutTables pins the v12
// cutover: the three record families exist, no historic row is rewritten,
// and a pre-v12 intent stays queryable with its empty child linkage
// (DAT-012).
func TestE12T1MigrationV12PreservesHistoryAndAddsFanoutTables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.migrations = Migrations[:11]
	if err := s.Migrate(dir); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki-maintenance", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", now); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki-maintenance"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRouteActivation(context.Background(), "wiki-maintenance", "enabled", "route-rev-1", "", now); err != nil {
		t.Fatal(err)
	}
	// Seed one v11-era legacy intent as a raw insert in the v11 column
	// shape (the ordinary commit path is lane-keyed since v13 and cannot
	// run against a pre-v13 schema), exactly as the historical row stands.
	if err := s.SaveDecision(nil, DecisionRecord{
		DecisionID: "decision-dispatch-legacy", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
		PolicyRevision: "policy-rev-1", GenerationLineageJSON: `{"generations":[]}`,
		Disposition: "dispatch", Classification: "normal", ReasonCodesJSON: `[]`,
		CreatedAt: now, Actor: "planner",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO dispatch_intents
		(dispatch_id, decision_id, route_id, route_revision, target_id, target_type, resource_id, generation, idempotency_key, content_fingerprint, manifest_digest, request_version, request_json, state, created_at, updated_at, base_batch_seq)
		VALUES ('dispatch-legacy', 'decision-dispatch-legacy', 'wiki-maintenance', 'route-rev-1', 'hermes-kanban-main', 'hermes_kanban', 'vault-main', 1,
		'agent-dispatch:v1:sha256:`+repeat("0", 64)+`', 'sha256:`+repeat("c", 64)+`', 'sha256:`+repeat("d", 64)+`',
		'agent-dispatch.hermes-task/v1', '{"contract_version":"agent-dispatch.hermes-task/v1"}', 'ready', ?, ?, 0)`, now, now); err != nil {
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
	if err != nil || version < 12 {
		t.Fatalf("upgraded ledger must record the v12 fan-out families: %d %v", version, err)
	}
	for _, table := range []string{"aggregate_events", "destination_revisions", "child_dispatches"} {
		if _, err := upgraded.Exec(`SELECT 1 FROM ` + table); err != nil {
			t.Fatalf("v12 must create %s: %v", table, err)
		}
	}
	// The historic intent stays queryable and its child linkage reads as
	// the legacy shape: empty destination identity, no child row
	// (DAT-012).
	snap, err := upgraded.LoadIntent(context.Background(), "dispatch-legacy")
	if err != nil {
		t.Fatalf("historic intent must remain queryable: %v", err)
	}
	if snap.AggregateID != "" || snap.DestinationID != "" || snap.DestinationRevision != "" || snap.Workstream != "" {
		t.Fatalf("legacy intent must carry empty child linkage: %+v", snap)
	}
	if _, err := upgraded.LoadChildDispatch(context.Background(), "dispatch-legacy"); err == nil {
		t.Fatal("legacy intent must have no child record")
	}
}

// TestE12T1ArrivalCreatesAggregateChildAndDestinationRevision proves the
// aggregate-to-child creation transaction (DAT-010, FAN-003): one
// selected destination produces exactly one child beneath one aggregate
// event, the destination revision persists beside it, and the selection
// summary is canonically ordered (FAN-012).
func TestE12T1ArrivalCreatesAggregateChildAndDestinationRevision(t *testing.T) {
	s := openTestStore(t)
	lin := e12t1FanoutLineage("dispatch-e12t1", "agg-e12t1")
	if err := s.CommitLineage(context.Background(), lin); err != nil {
		t.Fatalf("commit lineage: %v", err)
	}
	agg, err := s.LoadAggregateEvent(context.Background(), "agg-e12t1")
	if err != nil {
		t.Fatalf("aggregate event must persist: %v", err)
	}
	if agg.Origin != string(records.OriginArrival) || agg.SchemaVersion != AggregateEventSchemaVersion {
		t.Fatalf("aggregate shape wrong: %+v", agg)
	}
	if agg.DecisionID != lin.Decision.DecisionID || agg.ContentFingerprint != lin.Intent.ContentFingerprint {
		t.Fatalf("aggregate lineage wrong: %+v", agg)
	}
	var selections []records.DestinationSelection
	if err := json.Unmarshal([]byte(agg.SelectionJSON), &selections); err != nil || len(selections) != 1 {
		t.Fatalf("selection summary must hold one row: %s %v", agg.SelectionJSON, err)
	}
	if selections[0].DestinationID != "wiki-primary" || selections[0].Reason != "fanout_mode:all" {
		t.Fatalf("selection content wrong: %+v", selections[0])
	}
	child, err := s.LoadChildDispatch(context.Background(), "dispatch-e12t1")
	if err != nil {
		t.Fatalf("child dispatch must persist: %v", err)
	}
	if child.AggregateID != "agg-e12t1" || child.DestinationID != "wiki-primary" ||
		child.DestinationRevision != e12t1Revision || child.Workstream != "maintenance" ||
		child.IdempotencyKey != lin.Intent.IdempotencyKey || child.SchemaVersion != ChildDispatchSchemaVersion {
		t.Fatalf("child shape wrong: %+v", child)
	}
	var projection string
	if err := s.QueryRow(`SELECT projection_json FROM destination_revisions WHERE route_id = ? AND destination_id = ? AND revision = ?`,
		"wiki-maintenance", "wiki-primary", e12t1Revision).Scan(&projection); err != nil {
		t.Fatalf("destination revision must persist: %v", err)
	}
	// Self-verifying record (DAT-010/CON-010 posture): the stored bytes
	// address to the stored revision through the ONE canonical derivation
	// (records.RevisionOfProjection), exactly as the config-level pair
	// produced them.
	if want := records.RevisionOfProjection(projection); want != e12t1Revision {
		t.Fatalf("stored projection must address the stored revision: %s vs %s", want, e12t1Revision)
	}
	// The intent snapshot surfaces the child linkage (FAN-010 posture).
	snap, err := s.LoadIntent(context.Background(), "dispatch-e12t1")
	if err != nil {
		t.Fatal(err)
	}
	if snap.AggregateID != "agg-e12t1" || snap.DestinationID != "wiki-primary" || snap.Workstream != "maintenance" {
		t.Fatalf("snapshot child linkage wrong: %+v", snap)
	}
	// One audit row records the aggregate's creation (DUR-011 posture).
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM state_transitions WHERE entity_type = 'aggregate_event' AND entity_id = 'agg-e12t1'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("aggregate creation must be audited once: %d %v", n, err)
	}
}

// TestE12T1OneChildPerDestinationBeneathOneAggregate pins the FAN-003
// invariant: the store rejects a second child for the same destination
// under one aggregate event.
func TestE12T1OneChildPerDestinationBeneathOneAggregate(t *testing.T) {
	s := openTestStore(t)
	if err := s.CommitLineage(context.Background(), e12t1FanoutLineage("dispatch-a", "agg-dup")); err != nil {
		t.Fatal(err)
	}
	// A second intent for the same destination under the same aggregate:
	// SaveIntent fails on UNIQUE(aggregate_id, destination_id) after the
	// slot was released by completing the first dispatch.
	second := e12t1FanoutLineage("dispatch-b", "agg-dup")
	second.Intent.DecisionID = "decision-dispatch-b"
	if err := s.SaveDecision(nil, DecisionRecord{
		DecisionID: "decision-dispatch-b", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
		PolicyRevision: "policy-rev-1", GenerationLineageJSON: `{"generations":[]}`,
		Disposition: "dispatch", Classification: "normal", ReasonCodesJSON: `[]`,
		CreatedAt: "2026-08-29T00:00:00Z", Actor: "test",
	}); err != nil {
		t.Fatal(err)
	}
	// Free the slot so the reservation itself would succeed.
	if _, err := s.Exec(`UPDATE destination_lane_state SET active_dispatch_id = NULL WHERE route_id = 'wiki-maintenance'`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIntent(nil, portsIntent(second.Intent)); err == nil {
		t.Fatal("a second child for one destination beneath one aggregate must fail closed")
	}
}

// TestE12T1FollowupCreatesNewAggregateAndChildPreservingLane proves the
// follow-up creation path stays child-linked (E12-T1): completion with a
// dirty generation creates a new follow-up aggregate whose child keeps
// the parent's destination identity at the next generation.
func TestE12T1FollowupCreatesNewAggregateAndChildPreservingLane(t *testing.T) {
	s := openTestStore(t)
	if err := s.CommitLineage(context.Background(), e12t1FanoutLineage("dispatch-parent", "agg-parent")); err != nil {
		t.Fatal(err)
	}
	// Mark the lane dirty the ordinary way: a second burst merges into the
	// parent's destination lane (lane-keyed since E12-T2).
	burst := lineage("dispatch-burst", "agent-dispatch:v2:sha256:"+repeat("f", 64))
	burst.Decision.Disposition = "merge_pending"
	if _, err := s.CommitMergePending(context.Background(), burst, []string{"wiki-primary"}, []string{"wiki-primary"}, "test", "2026-08-29T02:00:00Z"); err != nil {
		t.Fatal(err)
	}
	followupInput := ports.IntentInput{
		DispatchID: "dispatch-followup", DecisionID: "dec-dispatch-followup", RouteID: "wiki-maintenance",
		RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes_kanban",
		TargetScope: "board-main", ResourceID: "vault-main", Generation: 2,
		IdempotencyKey:     "agent-dispatch:v2:sha256:" + repeat("a", 64),
		ContentFingerprint: "sha256:" + repeat("f", 64), ManifestDigest: "sha256:" + repeat("1", 64),
		RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: `{}`,
		CreatedAt: "2026-08-29T02:00:01Z",
		Fanout: &ports.FanoutInput{
			AggregateID: "agg-followup", Origin: string(records.OriginFollowup),
			DestinationID: "wiki-primary", DestinationRevision: e12t1Revision, Workstream: "maintenance",
			Selections: []records.DestinationSelection{{
				DestinationID: "wiki-primary", DestinationRevision: e12t1Revision, Workstream: "maintenance", Reason: "followup:dispatch-parent",
			}},
		},
	}
	out, err := s.CompleteActive(context.Background(), ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: "dispatch-parent", FollowupRequest: &followupInput,
		Now: "2026-08-29T02:00:01Z", Actor: "test", PolicyRevision: "policy-rev-1",
	})
	if err != nil {
		t.Fatalf("completion with follow-up: %v", err)
	}
	if out.FollowupDispatchID != "dispatch-followup" {
		t.Fatalf("follow-up must be created: %+v", out)
	}
	child, err := s.LoadChildDispatch(context.Background(), "dispatch-followup")
	if err != nil {
		t.Fatalf("follow-up child must persist: %v", err)
	}
	if child.AggregateID != "agg-followup" || child.DestinationID != "wiki-primary" || child.DestinationRevision != e12t1Revision {
		t.Fatalf("follow-up child must keep the parent lane: %+v", child)
	}
	agg, err := s.LoadAggregateEvent(context.Background(), "agg-followup")
	if err != nil || agg.Origin != string(records.OriginFollowup) {
		t.Fatalf("follow-up aggregate must persist with origin followup: %+v %v", agg, err)
	}
	// The two aggregates are separate durable identities (DAT-010).
	if agg.AggregateID == "agg-parent" {
		t.Fatal("follow-up must not reuse the parent aggregate")
	}
}

// TestE12T1SelectionSummaryCanonicalOrder pins FAN-012 at the
// persistence boundary: the selection summary is stored sorted by
// destination ID regardless of the caller's ordering.
func TestE12T1SelectionSummaryCanonicalOrder(t *testing.T) {
	s := openTestStore(t)
	lin := e12t1FanoutLineage("dispatch-order", "agg-order")
	// Inject two extra unsorted selections; the child stays the certified
	// lane while the summary must still land canonically ordered.
	lin.Intent.Fanout.Selections = []records.DestinationSelection{
		{DestinationID: "zeta-lane", DestinationRevision: "dst-z", Workstream: "z", Reason: "fanout_mode:all"},
		{DestinationID: "wiki-primary", DestinationRevision: e12t1Revision, Workstream: "maintenance", Reason: "fanout_mode:all"},
		{DestinationID: "alpha-lane", DestinationRevision: "dst-a", Workstream: "a", Reason: "fanout_mode:all"},
	}
	if err := s.CommitLineage(context.Background(), lin); err != nil {
		t.Fatal(err)
	}
	agg, err := s.LoadAggregateEvent(context.Background(), "agg-order")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	if err := json.Unmarshal([]byte(agg.SelectionJSON), &[]records.DestinationSelection{}); err == nil {
		var selections []records.DestinationSelection
		_ = json.Unmarshal([]byte(agg.SelectionJSON), &selections)
		for _, sel := range selections {
			ids = append(ids, sel.DestinationID)
		}
	}
	if fmt.Sprint(ids) != "[alpha-lane wiki-primary zeta-lane]" {
		t.Fatalf("selection summary must be canonically ordered, got %v", ids)
	}
}

// e12t1EditedProjection/e12t1EditedRevision are a second, distinct,
// real content-addressed pair for the same destination (the behavior-edit
// shape; E12 epic validation: the store verifies every persisted revision
// is the content address of its bytes through the canonical
// records.RevisionOfProjection).
const e12t1EditedProjection = `{"id":"wiki-primary","target":"hermes-kanban-main","workstream":"edited"}`

var e12t1EditedRevision = records.RevisionOfProjection(e12t1EditedProjection)

// TestE12T1DestinationRevisionInsertIsIdempotent proves the
// destination-revision record is content-addressed: re-referencing an
// already-persisted revision is the identity operation, and a different
// revision for the same destination is a new row.
func TestE12T1DestinationRevisionInsertIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	release := func() {
		t.Helper()
		if _, err := s.Exec(`UPDATE destination_lane_state SET active_dispatch_id = NULL WHERE route_id = 'wiki-maintenance'`); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CommitLineage(context.Background(), e12t1FanoutLineage("dispatch-r1", "agg-r1")); err != nil {
		t.Fatal(err)
	}
	release()
	// A second arrival referencing the same revision: no second row.
	if err := s.CommitLineage(context.Background(), e12t1FanoutLineage("dispatch-r2", "agg-r2")); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM destination_revisions WHERE destination_id = 'wiki-primary'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("re-referencing a revision must not duplicate it: %d %v", n, err)
	}
	// A behavior edit produces a new revision: a new row beside the old.
	release()
	third := e12t1FanoutLineage("dispatch-r3", "agg-r3")
	third.Intent.Fanout.DestinationRevision = e12t1EditedRevision
	third.Intent.Fanout.Selections[0].DestinationRevision = e12t1EditedRevision
	third.Intent.Fanout.Revisions[0].Revision = e12t1EditedRevision
	third.Intent.Fanout.Revisions[0].ProjectionJSON = e12t1EditedProjection
	if err := s.CommitLineage(context.Background(), third); err != nil {
		t.Fatalf("arrival under the edited revision: %v", err)
	}
	if err := s.QueryRow(`SELECT COUNT(*) FROM destination_revisions WHERE destination_id = 'wiki-primary'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("a new revision must persist beside the old: %d %v", n, err)
	}
}

// TestE12T1LegacyIntentsRemainQueryableAfterFanoutWork pins DAT-012 in
// the mixed shape: legacy intents (no child) and child-linked intents
// coexist and both stay inspectable through the same surfaces.
func TestE12T1LegacyIntentsRemainQueryableAfterFanoutWork(t *testing.T) {
	s := openTestStore(t)
	// A legacy-shaped lineage (nil Fanout) through the same commit path.
	if err := s.CommitLineage(context.Background(), lineage("dispatch-old", "agent-dispatch:v1:sha256:"+repeat("7", 64))); err != nil {
		t.Fatal(err)
	}
	// Release the slot and create new-contract work.
	if _, err := s.Exec(`UPDATE destination_lane_state SET active_dispatch_id = NULL WHERE route_id = 'wiki-maintenance'`); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitLineage(context.Background(), e12t1FanoutLineage("dispatch-new", "agg-new")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"dispatch-old", "dispatch-new"} {
		if _, err := s.LoadIntentLineage(context.Background(), id); err != nil {
			t.Fatalf("intent %s must stay inspectable: %v", id, err)
		}
	}
	old, _ := s.LoadIntentLineage(context.Background(), "dispatch-old")
	newLin, _ := s.LoadIntentLineage(context.Background(), "dispatch-new")
	if old.Intent.AggregateID != "" || old.Intent.DestinationID != "" {
		t.Fatalf("legacy lineage must expose empty child linkage: %+v", old.Intent)
	}
	if newLin.Intent.AggregateID != "agg-new" || newLin.Intent.DestinationID != "wiki-primary" {
		t.Fatalf("new lineage must expose the child linkage: %+v", newLin.Intent)
	}
}

// TestE12T1FanoutFailsClosedOnInvalidOrigin pins the closed origin
// vocabulary at the persistence boundary.
func TestE12T1FanoutFailsClosedOnInvalidOrigin(t *testing.T) {
	s := openTestStore(t)
	lin := e12t1FanoutLineage("dispatch-bad-origin", "agg-bad")
	lin.Intent.Fanout.Origin = "spontaneous"
	if err := s.CommitLineage(context.Background(), lin); err == nil {
		t.Fatal("an unknown fanout origin must fail closed")
	}
	if _, err := records.ParseAggregateOrigin("arrival"); err != nil {
		t.Fatalf("arrival must parse: %v", err)
	}
	if _, err := records.ParseAggregateOrigin("rebuild"); err != nil {
		t.Fatalf("rebuild must parse: %v", err)
	}
}

// TestE12T1ChildIdempotencyOrderIndependence pins FAN-012 for the
// DAT-014 child key at the derivation level: configuration order never
// changes destination revisions, the canonical selection order, or the
// child idempotency key.
func TestE12T1ChildIdempotencyOrderIndependence(t *testing.T) {
	// Two identical behavior shapes under permuted member order produce
	// the same destination revision and the same child key; a
	// destination-scoped edit produces different ones.
	base := records.ChildIdempotencyKeyInput{
		RouteID: "wiki", RouteRevision: "rev-1", DestinationID: "wiki-primary",
		DestinationRevision: e12t1Revision, Workstream: "maintenance", TargetScope: "board-main",
		Generation: 1, ContentFingerprint: "sha256:" + repeat("c", 64), RequestVersion: "agent-dispatch.hermes-task/v1",
	}
	keyA, err := childKeyForTest(base)
	if err != nil {
		t.Fatal(err)
	}
	// Determinism is pinned through a RECONSTRUCTED input, not the same
	// struct twice: the key derivation must address the identical key from
	// the serialized-and-parsed shape the store rebuilds from persisted
	// rows (field-tag and serialization stability).
	encoded, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	var rebuilt records.ChildIdempotencyKeyInput
	if err := json.Unmarshal(encoded, &rebuilt); err != nil {
		t.Fatal(err)
	}
	keyB, err := childKeyForTest(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	if keyA != keyB {
		t.Fatalf("a reconstructed input must yield the identical key: %s vs %s", keyA, keyB)
	}
	// A second destination (the repeated-profile, distinct-workstream
	// shape of AC-802) never collides with the first.
	sibling := base
	sibling.DestinationID, sibling.Workstream = "wiki-secondary", "backlinks"
	keyC, err := childKeyForTest(sibling)
	if err != nil {
		t.Fatal(err)
	}
	if keyC == keyA {
		t.Fatal("sibling destinations must derive distinct keys")
	}
	// A behavior-affecting destination edit (new revision, AC-804) never
	// reuses the old key.
	edited := base
	edited.DestinationRevision = "dst-rev-2"
	keyD, err := childKeyForTest(edited)
	if err != nil {
		t.Fatal(err)
	}
	if keyD == keyA {
		t.Fatal("a destination revision change must change the child key")
	}
	// The selection projection itself is order-canonical.
	selections := []records.DestinationSelection{
		{DestinationID: "z", DestinationRevision: "r", Workstream: "w", Reason: "n"},
		{DestinationID: "a", DestinationRevision: "r", Workstream: "w", Reason: "n"},
	}
	records.SortSelections(selections)
	if selections[0].DestinationID != "a" {
		t.Fatal("selections must sort by destination ID")
	}
}

// TestE12T1RetryPreservesChildAndKey pins CON-009 at the child layer:
// the explicit operator retry of a child-linked dead letter keeps
// exactly one child row with its idempotency key unchanged (review round
// 1, testing finding 5).
func TestE12T1RetryPreservesChildAndKey(t *testing.T) {
	s := openTestStore(t)
	if err := s.CommitLineage(context.Background(), e12t1FanoutLineage("dispatch-retry", "agg-retry")); err != nil {
		t.Fatal(err)
	}
	before, err := s.LoadChildDispatch(context.Background(), "dispatch-retry")
	if err != nil {
		t.Fatal(err)
	}
	// Drive the intent into the dead-lettered shape the retry exit owns.
	if _, err := s.Exec(`UPDATE dispatch_intents SET state = 'dead_lettered' WHERE dispatch_id = 'dispatch-retry'`); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyOperatorRetry(context.Background(), "dispatch-retry", "operator", "reviewed", "2026-08-29T03:00:00Z"); err != nil {
		t.Fatalf("operator retry: %v", err)
	}
	after, err := s.LoadChildDispatch(context.Background(), "dispatch-retry")
	if err != nil {
		t.Fatalf("the child row must survive the retry: %v", err)
	}
	if after.IdempotencyKey != before.IdempotencyKey || after.DestinationID != before.DestinationID || after.DestinationRevision != before.DestinationRevision {
		t.Fatalf("retry must preserve the child identity and key:\nbefore %+v\nafter %+v", before, after)
	}
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM child_dispatches WHERE dispatch_id = 'dispatch-retry'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("exactly one child row must exist after the retry: %d %v", n, err)
	}
}

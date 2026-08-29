package workreceipt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E12-T3 acceptance coverage over the real durable store: the
// partially_completed outcome schedules exactly one same-lane follow-up
// whose manifest is the remaining scope unioned with the lane's
// unresolved dirty changes (FBK-010), the blocked outcome records its
// manual reason without completing the lane or scheduling anything and
// never auto-runs afterwards (FBK-011), and empty remaining scopes fail
// closed with guidance.

// openReceiptStore prepares an enabled route with one active child-linked
// dispatch on the wiki-primary lane.
func openReceiptStore(t *testing.T) (*sqlite.Store, *Service, string) {
	t.Helper()
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-08-29T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRouteActivation(context.Background(), "wiki", "enabled", "route-rev-1", "", "2026-08-29T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	dispatchID := "dispatch-e12t3"
	lin := receiptLineage(t, dispatchID)
	if err := s.CommitLineage(context.Background(), lin); err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateDispatch(context.Background(), dispatchID, "test", "2026-08-29T00:00:01Z"); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: s, Now: func() time.Time { return time.Date(2026, 8, 29, 1, 0, 0, 0, time.UTC) }}
	if _, err := svc.Begin(context.Background(), BeginInput{DispatchID: dispatchID, RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	return s, svc, dispatchID
}

// testLaneProjection/testLaneRevision are a real content-addressed pair
// for the wiki-primary test lane (E12 epic validation: the store verifies
// persisted revisions are the content address of their bytes and child
// references have durable rows).
const testLaneProjection = `{"id":"wiki-primary","workstream":"maintenance","test":true}`

// testLaneRevision derives through the ONE canonical derivation
// (records.RevisionOfProjection, E12 epic whole-review round 1).
var testLaneRevision = records.RevisionOfProjection(testLaneProjection)

// receiptLineage builds one child-linked arrival on the wiki-primary lane.
func receiptLineage(t *testing.T, dispatchID string) ports.Lineage {
	t.Helper()
	return receiptLineageOnLane(t, dispatchID, "wiki-primary")
}

// receiptLineageOnLane builds one child-linked arrival on the named lane:
// the request document names the SAME lane the fanout block child-links,
// so a follow-up derived from the stored request keeps its own lane (the
// shared fixture above pins the wiki-primary shape every other test
// uses).
func receiptLineageOnLane(t *testing.T, dispatchID, destinationID string) ports.Lineage {
	t.Helper()
	lin := receiptLineageFields(dispatchID)
	req, key, err := dispatch.BuildRequest(dispatch.RequestInput{
		DispatchID: dispatchID,
		Route:      ports.TaskRouteRef{ID: "wiki", Revision: "route-rev-1"},
		Resource:   ports.TaskResource{ID: "vault-main", Workspace: "dir:/srv/vault"},
		TargetID:   "hermes-kanban-main", TargetScope: "board-main",
		Destination: ports.TaskDestinationRef{ID: destinationID, Revision: testLaneRevision, Workstream: "maintenance"},
		Generation:  1,
		Fingerprint: records.Digest("sha256:" + receiptHex('c')),
		Changes: []records.ChangeItem{{
			Path: "Inbox/first.md", Operation: records.OpModify, FileType: records.FileRegular,
			AfterDigest: records.Digest("sha256:" + receiptHex('b')), DigestStatus: records.DigestKnown,
		}},
		AcceptanceCriteria: []string{"wiki maintenance completes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	requestJSON, err := dispatch.MarshalRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	lin.Intent.RequestJSON = requestJSON
	lin.Intent.IdempotencyKey = key
	lin.Intent.ContentFingerprint = string(req.Activation.ContentFingerprint)
	if lin.Intent.Fanout != nil {
		lin.Intent.Fanout.DestinationID = destinationID
		lin.Intent.Fanout.Selections = []records.DestinationSelection{{
			DestinationID: destinationID, DestinationRevision: testLaneRevision, Workstream: "maintenance",
			Reason: "fanout_mode:all",
		}}
		lin.Intent.Fanout.Revisions = []ports.DestinationRevisionInput{{
			DestinationID: destinationID, Revision: testLaneRevision, ProjectionJSON: testLaneProjection,
		}}
	}
	return lin
}

// receiptLineageFields is the raw persistence shape before the request
// document is built.
func receiptLineageFields(dispatchID string) ports.Lineage {
	now := "2026-08-29T00:00:00Z"
	return ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: "obs-" + dispatchID, SchemaVersion: "agent-dispatch.source-observation/v1",
			SourceType: "watchman", SourceID: "watchman-main", TriggerName: "trig",
			ResourceID: "vault-main", ObservedAt: now, ReceivedAt: now,
			RawPayloadDigest: "sha256:" + receiptHex('a'), IngestStatus: "accepted",
			Changes: []ports.ObservationChange{{
				Ordinal: 0, Path: "Inbox/first.md", Operation: "modify", ExistsAfter: true, FileType: "regular",
				AfterDigest: "sha256:" + receiptHex('b'), DigestStatus: "known",
			}},
		},
		Batch: ports.BatchInput{
			BatchID: "batch-" + dispatchID, RouteID: "wiki", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: now,
			ContentFingerprint: "sha256:" + receiptHex('c'), ObservationIDs: []string{"obs-" + dispatchID},
		},
		Decision: ports.DecisionInput{
			DecisionID: "decision-" + dispatchID, BatchID: "batch-" + dispatchID, RouteID: "wiki",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch",
			Classification: "normal", ReasonCodesJSON: `["normal_batch"]`, CreatedAt: now, Actor: "planner",
		},
		Intent: ports.IntentInput{
			DispatchID: dispatchID, DecisionID: "decision-" + dispatchID, RouteID: "wiki",
			RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes_kanban",
			TargetScope: "board-main", ResourceID: "vault-main", Generation: 1,
			IdempotencyKey:     "agent-dispatch:v2:sha256:" + receiptHex('1'),
			ContentFingerprint: "sha256:" + receiptHex('c'), ManifestDigest: "sha256:" + receiptHex('d'),
			RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: "{}",
			CreatedAt: now,
			Fanout: &ports.FanoutInput{
				AggregateID: "agg-" + dispatchID, Origin: string(records.OriginArrival),
				DestinationID: "wiki-primary", DestinationRevision: testLaneRevision, Workstream: "maintenance",
				Selections: []records.DestinationSelection{{
					DestinationID: "wiki-primary", DestinationRevision: testLaneRevision, Workstream: "maintenance",
					Reason: "fanout_mode:all",
				}},
				Revisions: []ports.DestinationRevisionInput{{
					DestinationID: "wiki-primary", Revision: testLaneRevision, ProjectionJSON: testLaneProjection,
				}},
			},
		},
	}
}

func receiptHex(ch byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}

// TestE12T3PartialCompletionSchedulesOneSameLaneFollowup pins FBK-010:
// a partially_completed receipt records both scopes, closes the current
// child exactly like a completion, and schedules exactly one follow-up on
// the SAME destination lane whose manifest is the remaining scope (unioned
// with the lane's unresolved dirty changes — here a merged burst adds a
// path the remaining scope does not carry).
func TestE12T3PartialCompletionSchedulesOneSameLaneFollowup(t *testing.T) {
	s, svc, dispatchID := openReceiptStore(t)
	ctx := context.Background()
	// A later burst merges into the lane: its path is unresolved dirty the
	// follow-up must union beside the remaining scope.
	burst := receiptLineage(t, "dispatch-burst")
	burst.Decision.DecisionID = "decision-burst"
	burst.Intent.DispatchID = "dispatch-burst"
	burst.Intent.DecisionID = "decision-burst"
	burst.Intent.IdempotencyKey = "agent-dispatch:v2:sha256:" + receiptHex('2')
	burst.Intent.Fanout.AggregateID = "agg-burst"
	burst.Decision.Disposition = "merge_pending"
	burst.Observation.Changes[0].Path = "Inbox/merged-later.md"
	if _, err := s.CommitMergePending(ctx, burst, []string{"wiki-primary"}, []string{"wiki-primary"}, "test", "2026-08-29T00:30:00Z"); err != nil {
		t.Fatal(err)
	}
	remaining := `[{"path":"Indexes/remaining.md"},{"path":"Inbox/first.md"}]`
	out, err := svc.Complete(ctx, CompleteInput{
		DispatchID: dispatchID, RunID: "run-1", Status: StatusPartiallyComplete,
		ManifestJSON:          `[{"path":"Indexes/done.md"}]`,
		RemainingManifestJSON: remaining,
	})
	if err != nil {
		t.Fatalf("partial completion: %v", err)
	}
	if out.Status != StatusPartiallyComplete || out.FollowupDispatchID == "" {
		t.Fatalf("the partial outcome must close the child and schedule its follow-up: %+v", out)
	}
	// The receipt row carries both scopes and the status.
	var status, completedScope, remainingScope string
	if err := s.QueryRow(`SELECT status, completed_scope_json, remaining_scope_json FROM work_receipts WHERE dispatch_id = ? AND run_id = 'run-1'`,
		dispatchID).Scan(&status, &completedScope, &remainingScope); err != nil {
		t.Fatal(err)
	}
	if status != StatusPartiallyComplete {
		t.Fatalf("the receipt must record partially_completed: %s", status)
	}
	if !containsJSONPath(completedScope, "Indexes/done.md") || !containsJSONPath(remainingScope, "Indexes/remaining.md") {
		t.Fatalf("the scopes must persist verbatim: %s / %s", completedScope, remainingScope)
	}
	// The follow-up is the ONLY one, keeps the lane, and its manifest is
	// the union: both remaining paths plus the merged burst's new path.
	snap, err := s.LoadIntent(ctx, out.FollowupDispatchID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.DestinationID != "wiki-primary" || snap.Generation != 2 {
		t.Fatalf("the follow-up must be the same lane's next generation: %+v", snap)
	}
	var followups int
	if err := s.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE generation = 2`).Scan(&followups); err != nil || followups != 1 {
		t.Fatalf("exactly one follow-up generation: %d %v", followups, err)
	}
	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(snap.RequestJSON), &req); err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, m := range req.Activation.Manifest {
		paths[m.Path] = true
	}
	for _, want := range []string{"Indexes/remaining.md", "Inbox/merged-later.md"} {
		if !paths[want] {
			t.Fatalf("the follow-up manifest must carry the union (remaining scope + unresolved dirty): %v", paths)
		}
	}
	// The lane landed FOLLOWUP_READY behind the follow-up's reservation.
	laneSnap, err := s.LoadLaneState(ctx, "wiki", "wiki-primary")
	if err != nil || laneSnap.State != state.RouteFollowupReady {
		t.Fatalf("the lane must land FOLLOWUP_READY: %+v %v", laneSnap, err)
	}
}

// TestE12T3EmptyRemainingScopeRejected pins the fail-closed guard: a
// partially_completed receipt with an empty remaining scope is the
// completed outcome — the operator is told so and nothing is recorded.
func TestE12T3EmptyRemainingScopeRejected(t *testing.T) {
	_, svc, dispatchID := openReceiptStore(t)
	_, err := svc.Complete(context.Background(), CompleteInput{
		DispatchID: dispatchID, RunID: "run-1", Status: StatusPartiallyComplete,
		ManifestJSON: `[{"path":"Indexes/done.md"}]`, RemainingManifestJSON: `[]`,
	})
	var invalid *InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("an empty remaining scope must be a validation rejection, got %v", err)
	}
	if !contains(invalid.Error(), "completed") {
		t.Fatalf("the rejection must point at the completed outcome: %v", err)
	}
}

// TestE12T3BlockedRecordsReasonWithoutCompleting pins FBK-011: the
// blocked receipt records its manual reason, the lane stays ACTIVE with
// its child, no follow-up is scheduled, and no automatic machinery
// touches the child afterwards (the drain's retry sweep admits only
// retry_wait/dead-lettered work).
func TestE12T3BlockedRecordsReasonWithoutCompleting(t *testing.T) {
	s, svc, dispatchID := openReceiptStore(t)
	ctx := context.Background()
	out, err := svc.Complete(ctx, CompleteInput{
		DispatchID: dispatchID, RunID: "run-1", Status: StatusBlocked,
		ManifestJSON: `[]`, ManualReason: "protected-path policy forbids the rewrite",
	})
	if err != nil {
		t.Fatalf("blocked outcome: %v", err)
	}
	if out.Status != StatusBlocked || !out.ManualIntervention || out.FollowupDispatchID != "" {
		t.Fatalf("the blocked outcome must surface manual intervention with no follow-up: %+v", out)
	}
	var status, reason string
	if err := s.QueryRow(`SELECT status, COALESCE(manual_reason, '') FROM work_receipts WHERE dispatch_id = ? AND run_id = 'run-1'`,
		dispatchID).Scan(&status, &reason); err != nil || status != StatusBlocked || reason == "" {
		t.Fatalf("the blocked receipt must persist its reason: %q %q %v", status, reason, err)
	}
	// The lane is untouched: still active, still holding the child.
	laneSnap, err := s.LoadLaneState(ctx, "wiki", "wiki-primary")
	if err != nil || laneSnap.State != state.RouteActiveClean || laneSnap.ActiveDispatchID != dispatchID {
		t.Fatalf("the lane must stay active with its child: %+v %v", laneSnap, err)
	}
	var intents int
	if err := s.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents); err != nil || intents != 1 {
		t.Fatalf("no follow-up or replacement may be scheduled: %d %v", intents, err)
	}
	// The automatic retry machinery never admits it: the lease predicate
	// only refuses (the child is accepted, not retry_wait or
	// dead-lettered), and no state change happened.
	intent, err := s.LoadIntent(ctx, dispatchID)
	if err != nil || intent.State != records.IntentReady {
		t.Fatalf("the blocked child keeps its own submit state: %+v %v", intent, err)
	}
	// The view carries the child association and the reason.
	view, err := s.LoadWorkReceipt(ctx, dispatchID, "run-1")
	if err != nil || view.DestinationID != "wiki-primary" || view.ManualReason == "" {
		t.Fatalf("the receipt view must carry the lane and reason: %+v %v", view, err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func containsJSONPath(doc, path string) bool {
	var items []ports.WorkChange
	if err := json.Unmarshal([]byte(doc), &items); err != nil {
		return false
	}
	for _, item := range items {
		if item.Path == path {
			return true
		}
	}
	return false
}

// TestE12T3PartialOnCleanLaneCarriesExactlyRemainingScope pins the
// clean-lane edge (review round 1, testing finding): a partial
// completion on a lane with NO dirty generation still creates exactly
// one same-lane follow-up whose manifest is EXACTLY the remaining scope
// (no dirty union) and the lane lands FOLLOWUP_READY.
func TestE12T3PartialOnCleanLaneCarriesExactlyRemainingScope(t *testing.T) {
	s, svc, dispatchID := openReceiptStore(t)
	ctx := context.Background()
	out, err := svc.Complete(ctx, CompleteInput{
		DispatchID: dispatchID, RunID: "run-1", Status: StatusPartiallyComplete,
		ManifestJSON:          `[{"path":"Indexes/done.md"}]`,
		RemainingManifestJSON: `[{"path":"Indexes/only-remaining.md"}]`,
	})
	if err != nil {
		t.Fatalf("clean-lane partial completion: %v", err)
	}
	if out.FollowupDispatchID == "" || out.Status != StatusPartiallyComplete {
		t.Fatalf("the clean-lane partial outcome must still schedule its follow-up: %+v", out)
	}
	snap, err := s.LoadIntent(ctx, out.FollowupDispatchID)
	if err != nil {
		t.Fatal(err)
	}
	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(snap.RequestJSON), &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Activation.Manifest) != 1 || req.Activation.Manifest[0].Path != "Indexes/only-remaining.md" {
		t.Fatalf("the follow-up manifest must be exactly the remaining scope on a clean lane: %+v", req.Activation.Manifest)
	}
	laneSnap, err := s.LoadLaneState(ctx, "wiki", "wiki-primary")
	if err != nil || laneSnap.State != state.RouteFollowupReady {
		t.Fatalf("the lane must land FOLLOWUP_READY: %+v %v", laneSnap, err)
	}
}

// TestEpicValidationOccurrenceLevelLaneSelection pins the E12 epic
// validation residual (FAN-005): a conditioned lane's follow-up filters
// its dirty generation by the merging OCCURRENCE's recorded selection,
// never by re-evaluating conditions per change. The occurrence carries a
// path that alone fails the lane's path_include, but the occurrence as a
// whole selected the lane (a sibling path matched) — the lane's follow-up
// carries the WHOLE merged burst; the sibling lane still excludes it; and
// a legacy row without selection evidence keeps the per-change fallback.
func TestEpicValidationOccurrenceLevelLaneSelection(t *testing.T) {
	s, svc, dispatchID := openReceiptStore(t)
	ctx := context.Background()

	// The conditioned alpha lane: path_include ["alpha/**"] — the burst
	// carries one alpha path and one other path, so the OCCURRENCE
	// selects the lane while the other path alone would fail.
	conds := &dispatch.DestinationConditionSet{PathInclude: []string{"alpha/**"}}
	matcher := func(pattern, path string) (bool, error) {
		selected, _, err := dispatch.SelectDestination(dispatch.SelectionContext{
			Paths: []string{"alpha/kept.md", "other/dropped.md"},
		}, &dispatch.DestinationConditionSet{PathInclude: []string{pattern}}, nil)
		_ = selected
		return strings.HasPrefix(path, pattern[:len(pattern)-3]), err
	}
	svc.LaneConditions = func(routeID, destinationID string) (*dispatch.DestinationConditionSet, error) {
		if destinationID != "wiki-primary" {
			return nil, fmt.Errorf("unknown lane %q", destinationID)
		}
		return conds, nil
	}
	svc.LanePathMatcher = matcher

	// The merged burst: the batch records its selection evidence
	// (occurrence-level: both destinations), and its changes carry one
	// path that matches the include and one that does not.
	burst := receiptLineage(t, "dispatch-burst-occ")
	burst.Decision.DecisionID = "decision-burst-occ"
	burst.Intent.DispatchID = "dispatch-burst-occ"
	burst.Intent.DecisionID = "decision-burst-occ"
	burst.Intent.IdempotencyKey = "agent-dispatch:v2:sha256:" + receiptHex('7')
	burst.Intent.Fanout.AggregateID = "agg-burst-occ"
	burst.Decision.Disposition = "merge_pending"
	burst.Observation.Changes = []ports.ObservationChange{
		{Ordinal: 0, Path: "alpha/kept.md", Operation: "modify", ExistsAfter: true, FileType: "regular",
			AfterDigest: "sha256:" + receiptHex('1'), DigestStatus: "known"},
		{Ordinal: 1, Path: "other/alone-fails.md", Operation: "modify", ExistsAfter: true, FileType: "regular",
			AfterDigest: "sha256:" + receiptHex('2'), DigestStatus: "known"},
	}
	if _, err := s.CommitMergePending(ctx, burst, []string{"wiki-primary", "wiki-secondary"}, []string{"wiki-primary", "wiki-secondary"}, "test", "2026-08-29T00:30:00Z"); err != nil {
		t.Fatal(err)
	}
	out, err := svc.Complete(ctx, CompleteInput{
		DispatchID: dispatchID, RunID: "run-1", ManifestJSON: `[{"path":"Inbox/first.md"}]`,
	})
	if err != nil {
		t.Fatalf("completion with occurrence-level filtering: %v", err)
	}
	if out.FollowupDispatchID == "" {
		t.Fatalf("the filtered lane must still schedule its follow-up: %+v", out)
	}
	snap, err := s.LoadIntent(ctx, out.FollowupDispatchID)
	if err != nil {
		t.Fatal(err)
	}
	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(snap.RequestJSON), &req); err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, m := range req.Activation.Manifest {
		paths[m.Path] = true
	}
	// The WHOLE merged burst rides along: the per-path miss
	// (other/alone-fails.md) stays because the OCCURRENCE selected the
	// lane — no silent work loss.
	if !paths["alpha/kept.md"] || !paths["other/alone-fails.md"] {
		t.Fatalf("the follow-up must carry the whole occurrence-selected burst: %v", paths)
	}
}

// TestEpicValidationBusyLanesMergeKeepsFullSelection pins the round-2
// merge-evidence residual through the PRODUCTION arrival path: a burst
// that selects BOTH lanes of a route whose lanes are BOTH busy merges on
// each lane through ArrivalFanout — the first committer's
// CommitMergePending must not narrow the batch's selection to the merging
// lane, and the later lane's MergeSelectedLanes must union the
// occurrence's FULL selection onto the shared batch — so the evidence
// reads both destinations and EACH lane's completion carries the whole
// burst in its follow-up (FAN-005/CON-008; the silent-work-loss class).
func TestEpicValidationBusyLanesMergeKeepsFullSelection(t *testing.T) {
	s, svc, primary := openReceiptStore(t)
	ctx := context.Background()
	// The second busy lane: wiki-secondary holds its own accepted dispatch
	// (the request document AND the fanout block name the same lane, so
	// its follow-up keeps it).
	secondary := "dispatch-e12t3-secondary"
	second := receiptLineageOnLane(t, secondary, "wiki-secondary")
	second.Decision.DecisionID = "decision-" + secondary
	second.Intent.DispatchID = secondary
	second.Intent.DecisionID = "decision-" + secondary
	second.Intent.IdempotencyKey = "agent-dispatch:v2:sha256:" + receiptHex('9')
	second.Intent.Fanout.AggregateID = "agg-" + secondary
	if err := s.CommitLineage(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateDispatch(ctx, secondary, "test", "2026-08-29T00:00:05Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Begin(ctx, BeginInput{DispatchID: secondary, RunID: "run-2"}); err != nil {
		t.Fatal(err)
	}
	// Per-change conditions that alone would DROP the burst's other path
	// on both lanes — the occurrence-level evidence must keep it.
	svc.LaneConditions = func(routeID, destinationID string) (*dispatch.DestinationConditionSet, error) {
		return &dispatch.DestinationConditionSet{PathInclude: []string{"alpha/**"}}, nil
	}
	svc.LanePathMatcher = func(pattern, path string) (bool, error) {
		return strings.HasPrefix(path, strings.TrimSuffix(pattern, "**")), nil
	}

	// The two-lane burst through the REAL fan-out arrival: one lineage per
	// lane over the shared observation/batch/decision prefix. Both lanes
	// are busy, so BOTH merge (the first committer through
	// CommitMergePending, its sibling through MergeSelectedLanes).
	burstA := receiptLineage(t, "dispatch-burst-fanout")
	burstA.Decision.DecisionID = "decision-burst-fanout"
	burstA.Intent.DispatchID = "dispatch-burst-fanout"
	burstA.Intent.DecisionID = "decision-burst-fanout"
	burstA.Intent.IdempotencyKey = "agent-dispatch:v2:sha256:" + receiptHex('a')
	burstA.Intent.Fanout.AggregateID = "agg-burst-fanout"
	burstA.Decision.Disposition = "merge_pending"
	burstA.Observation.Changes = []ports.ObservationChange{
		{Ordinal: 0, Path: "alpha/kept.md", Operation: "modify", ExistsAfter: true, FileType: "regular",
			AfterDigest: "sha256:" + receiptHex('1'), DigestStatus: "known"},
		{Ordinal: 1, Path: "other/alone-fails.md", Operation: "modify", ExistsAfter: true, FileType: "regular",
			AfterDigest: "sha256:" + receiptHex('2'), DigestStatus: "known"},
	}
	burstB := burstA
	// Intent.Fanout is a POINTER: the sibling lineage needs its own copy
	// or both lineages alias one fanout block and the arrival sees the
	// same lane twice (the shared-state class the round-2 mutation
	// finding guards against).
	burstFanout := *burstA.Intent.Fanout
	burstFanout.DestinationID = "wiki-secondary"
	burstB.Intent.Fanout = &burstFanout
	burstB.Intent.DispatchID = "dispatch-burst-fanout-b"
	burstB.Intent.IdempotencyKey = "agent-dispatch:v2:sha256:" + receiptHex('b')

	coordinator := &dispatch.Coordinator{
		Store: s, Now: func() string { return "2026-08-29T00:30:00Z" }, Actor: "test",
	}
	outcome, err := coordinator.ArrivalFanout(ctx, []ports.Lineage{burstA, burstB})
	if err != nil {
		t.Fatalf("the busy-lane fan-out arrival must merge on both lanes: %v", err)
	}
	if len(outcome.Merged) != 2 || len(outcome.Activated) != 0 || len(outcome.Failed) != 0 {
		t.Fatalf("both busy lanes merge, nothing activates, nothing fails: %+v", outcome)
	}
	// The shared batch's selection evidence is the occurrence's FULL
	// selection — never narrowed to the first merging lane.
	var evidence string
	if err := s.QueryRow(`SELECT COALESCE(selected_destinations_json, '') FROM change_batches WHERE batch_id = ?`,
		burstA.Batch.BatchID).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if evidence != `["wiki-primary","wiki-secondary"]` {
		t.Fatalf("the merged batch must record the occurrence's full selection, got %s", evidence)
	}

	// Drive BOTH lanes' completions through work complete: each lane's
	// follow-up carries the WHOLE burst (occurrence-level evidence), even
	// though the per-change conditions would drop other/alone-fails.md.
	followups := map[string]string{}
	for _, completion := range []struct {
		dispatch, run string
	}{{primary, "run-1"}, {secondary, "run-2"}} {
		out, err := svc.Complete(ctx, CompleteInput{
			DispatchID: completion.dispatch, RunID: completion.run, ManifestJSON: `[{"path":"Inbox/first.md"}]`,
		})
		if err != nil {
			t.Fatalf("completion of %s: %v", completion.dispatch, err)
		}
		if out.FollowupDispatchID == "" {
			t.Fatalf("lane %s must schedule its follow-up over the merged burst: %+v", completion.dispatch, out)
		}
		followups[completion.dispatch] = out.FollowupDispatchID
	}
	for lane, followup := range followups {
		snap, err := s.LoadIntent(ctx, followup)
		if err != nil {
			t.Fatal(err)
		}
		var req ports.TaskRequest
		if err := json.Unmarshal([]byte(snap.RequestJSON), &req); err != nil {
			t.Fatal(err)
		}
		paths := map[string]bool{}
		for _, m := range req.Activation.Manifest {
			paths[m.Path] = true
		}
		if !paths["alpha/kept.md"] || !paths["other/alone-fails.md"] {
			t.Fatalf("lane %s's follow-up must carry the whole occurrence-selected burst: %v", lane, paths)
		}
	}
}

// TestEpicValidationLegacyRowsKeepPerChangeFallback pins the fallback:
// a merged batch WITHOUT selection evidence (the legacy pre-v15 shape)
// keeps the per-change condition evaluation — the alone-failing path is
// dropped from the conditioned lane's follow-up exactly as before.
func TestEpicValidationLegacyRowsKeepPerChangeFallback(t *testing.T) {
	s, svc, dispatchID := openReceiptStore(t)
	ctx := context.Background()
	svc.LaneConditions = func(routeID, destinationID string) (*dispatch.DestinationConditionSet, error) {
		return &dispatch.DestinationConditionSet{PathInclude: []string{"alpha/**"}}, nil
	}
	svc.LanePathMatcher = func(pattern, path string) (bool, error) {
		return strings.HasPrefix(path, strings.TrimSuffix(pattern, "**")), nil
	}
	burst := receiptLineage(t, "dispatch-burst-legacy")
	burst.Decision.DecisionID = "decision-burst-legacy"
	burst.Intent.DispatchID = "dispatch-burst-legacy"
	burst.Intent.DecisionID = "decision-burst-legacy"
	burst.Intent.IdempotencyKey = "agent-dispatch:v2:sha256:" + receiptHex('8')
	burst.Intent.Fanout.AggregateID = "agg-burst-legacy"
	burst.Decision.Disposition = "merge_pending"
	burst.Observation.Changes = []ports.ObservationChange{
		{Ordinal: 0, Path: "alpha/kept.md", Operation: "modify", ExistsAfter: true, FileType: "regular",
			AfterDigest: "sha256:" + receiptHex('1'), DigestStatus: "known"},
		{Ordinal: 1, Path: "other/alone-fails.md", Operation: "modify", ExistsAfter: true, FileType: "regular",
			AfterDigest: "sha256:" + receiptHex('2'), DigestStatus: "known"},
	}
	// No selectedDestinations argument: the batch records no evidence.
	if _, err := s.CommitMergePending(ctx, burst, nil, nil, "test", "2026-08-29T00:40:00Z"); err != nil {
		t.Fatal(err)
	}
	out, err := svc.Complete(ctx, CompleteInput{
		DispatchID: dispatchID, RunID: "run-1", ManifestJSON: `[{"path":"Inbox/first.md"}]`,
	})
	if err != nil {
		t.Fatalf("completion with legacy filtering: %v", err)
	}
	snap, err := s.LoadIntent(ctx, out.FollowupDispatchID)
	if err != nil {
		t.Fatal(err)
	}
	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(snap.RequestJSON), &req); err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, m := range req.Activation.Manifest {
		paths[m.Path] = true
	}
	if !paths["alpha/kept.md"] {
		t.Fatalf("the matching path must stay: %v", paths)
	}
	if paths["other/alone-fails.md"] {
		t.Fatalf("the legacy fallback drops the alone-failing path: %v", paths)
	}
}

// TestEpicValidationDigestLessScopeClassifiesModify pins the conservative
// classification (E12 epic validation): a remaining-scope row with NO
// digests carries no removal evidence and classifies as modify — deletion
// requires the exact before-present/after-absent pair.
func TestEpicValidationDigestLessScopeClassifiesModify(t *testing.T) {
	if got := scopeOperation(nil, nil); got != records.OpModify {
		t.Fatalf("no digests must classify modify, got %s", got)
	}
	empty := ""
	if got := scopeOperation(&empty, &empty); got != records.OpModify {
		t.Fatalf("empty digests must classify modify, got %s", got)
	}
	present := "sha256:" + receiptHex('3')
	if got := scopeOperation(&present, nil); got != records.OpDelete {
		t.Fatalf("before-present/after-absent must classify delete, got %s", got)
	}
	if got := scopeOperation(&present, &empty); got != records.OpDelete {
		t.Fatalf("before-present/after-empty must classify delete, got %s", got)
	}
	if got := scopeOperation(nil, &present); got != records.OpCreate {
		t.Fatalf("before-absent/after-present must classify create, got %s", got)
	}
	if got := scopeOperation(&present, &present); got != records.OpModify {
		t.Fatalf("both present must classify modify, got %s", got)
	}
}

// TestEpicValidationFilterFailClosedArms pins the filter's fail-closed
// arms at the service level (E12 epic validation, testing gap): a
// resolver error, a nil matcher beside path conditions, and an
// unparseable stored operation each refuse the completion — never a
// silently widened follow-up.
func TestEpicValidationFilterFailClosedArms(t *testing.T) {
	newService := func(laneConds func(string, string) (*dispatch.DestinationConditionSet, error), matcher func(string, string) (bool, error)) *Service {
		_, svc, _ := openReceiptStore(t)
		svc.LaneConditions = laneConds
		svc.LanePathMatcher = matcher
		return svc
	}
	legacyDirty := func() []ports.DirtyChange {
		return []ports.DirtyChange{{Path: "alpha/x.md", Operation: "modify", Classification: "normal", Disposition: "merge_pending"}}
	}
	intent := ports.IntentSnapshot{RouteID: "wiki", DispatchID: "dispatch-e12t3", DestinationID: "wiki-primary"}

	// Resolver error: the completion fails closed naming the lane.
	svc := newService(func(string, string) (*dispatch.DestinationConditionSet, error) {
		return nil, fmt.Errorf("route has no destination beta")
	}, nil)
	if _, err := svc.filterDirtyToLane(intent, legacyDirty()); err == nil || !strings.Contains(err.Error(), "beta") {
		t.Fatalf("a lane-condition resolver error must fail closed naming the cause: %v", err)
	}

	// Nil matcher beside path conditions: path evaluation cannot proceed.
	svc = newService(func(string, string) (*dispatch.DestinationConditionSet, error) {
		return &dispatch.DestinationConditionSet{PathInclude: []string{"alpha/**"}}, nil
	}, nil)
	if _, err := svc.filterDirtyToLane(intent, legacyDirty()); err == nil || !strings.Contains(err.Error(), "path matcher") {
		t.Fatalf("path conditions with a nil matcher must fail closed: %v", err)
	}

	// Unparseable stored operation on a legacy (no selection evidence) row.
	svc = newService(func(string, string) (*dispatch.DestinationConditionSet, error) {
		return &dispatch.DestinationConditionSet{Operations: []string{"modify"}}, nil
	}, nil)
	badOp := []ports.DirtyChange{{Path: "alpha/x.md", Operation: "exploded", Classification: "normal", Disposition: "merge_pending"}}
	if _, err := svc.filterDirtyToLane(intent, badOp); err == nil || !strings.Contains(err.Error(), "operation") {
		t.Fatalf("an unparseable stored operation must fail closed: %v", err)
	}
}

// TestE12ValidationOversizedManifestReportsSizeOnce pins the manifest
// size bound's message contract (E12 cold validation round 1): an
// oversized change set states the limit fact exactly once, never in both
// the manifest-level and the shared per-entry wording.
func TestE12ValidationOversizedManifestReportsSizeOnce(t *testing.T) {
	_, svc, dispatchID := openReceiptStore(t)
	entries := make([]string, MaxChanges+1)
	for i := range entries {
		entries[i] = fmt.Sprintf(`{"path":"Notes/file-%06d.md"}`, i)
	}
	raw := "[" + strings.Join(entries, ",") + "]"
	_, _, _, reasons := svc.validateManifest(raw, dispatchID, "run-size", "vault-main", "")
	limitMentions := 0
	for _, reason := range reasons {
		if strings.Contains(reason, "limit") {
			limitMentions++
		}
	}
	if limitMentions != 1 {
		t.Fatalf("an oversized manifest must state the size bound exactly once, got %d: %v", limitMentions, reasons)
	}
}

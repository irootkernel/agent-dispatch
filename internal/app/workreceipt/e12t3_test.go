package workreceipt

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
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

// receiptLineage builds one child-linked arrival on the wiki-primary lane.
func receiptLineage(t *testing.T, dispatchID string) ports.Lineage {
	t.Helper()
	lin := receiptLineageFields(dispatchID)
	req, key, err := dispatch.BuildRequest(dispatch.RequestInput{
		DispatchID: dispatchID,
		Route:      ports.TaskRouteRef{ID: "wiki", Revision: "route-rev-1"},
		Resource:   ports.TaskResource{ID: "vault-main", Workspace: "dir:/srv/vault"},
		TargetID:   "hermes-kanban-main", TargetScope: "board-main",
		Destination: ports.TaskDestinationRef{ID: "wiki-primary", Revision: "dst-rev-1", Workstream: "maintenance"},
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
				DestinationID: "wiki-primary", DestinationRevision: "dst-rev-1", Workstream: "maintenance",
				Selections: []records.DestinationSelection{{
					DestinationID: "wiki-primary", DestinationRevision: "dst-rev-1", Workstream: "maintenance",
					Reason: "fanout_mode:all",
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
	if _, err := s.CommitMergePending(ctx, burst, []string{"wiki-primary"}, "test", "2026-08-29T00:30:00Z"); err != nil {
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

package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// e12t1LegacyRequestJSON strips the destination block from a built
// request, reproducing the pre-cutover stored-request shape.
func e12t1LegacyRequestJSON(t *testing.T, dispatchID string) string {
	t.Helper()
	req, _, err := BuildRequest(RequestInput{
		DispatchID: dispatchID,
		Route:      ports.TaskRouteRef{ID: "wiki-maintenance", Revision: "route-rev-1"},
		Resource:   ports.TaskResource{ID: "vault-main", Workspace: "dir:/srv/vault"},
		TargetID:   "hermes-kanban-main", Generation: 1,
		Destination: ports.TaskDestinationRef{ID: "wiki-primary", Revision: "dst-rev-1", Workstream: "maintenance"},
		Fingerprint: records.Digest("sha256:" + hex64('c')),
		Changes: []records.ChangeItem{{
			Path: "Inbox/n.md", Operation: records.OpModify, FileType: records.FileRegular,
			AfterDigest: records.Digest("sha256:" + hex64('b')), DigestStatus: records.DigestKnown,
		}},
		AcceptanceCriteria: []string{"wiki maintenance completes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(mustMarshalRequest(t, req)), &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "destination")
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func mustMarshalRequest(t *testing.T, req ports.TaskRequest) string {
	t.Helper()
	raw, err := MarshalRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// snapshotLaneProjection/snapshotLaneRevision are a real content-addressed
// pair for the snapshot-linkage lane (E12 epic validation: the store
// verifies persisted revisions are the content address of their bytes and
// child references have durable rows; the derivation is the canonical
// records.RevisionOfProjection).
const snapshotLaneProjection = `{"id":"wiki-primary","workstream":"maintenance","snapshot":true}`

var snapshotLaneRevision = records.RevisionOfProjection(snapshotLaneProjection)

// liveLaneProjection/liveLaneRevision are the resolver branch's real
// content-addressed pair (E12 epic validation: the store verifies the
// content address of every persisted revision record).
const liveLaneProjection = `{"id":"wiki-primary","workstream":"maintenance","live":true}`

var liveLaneRevision = records.RevisionOfProjection(liveLaneProjection)

// seedLegacyIntent stores a ready intent whose stored request carries no
// destination block. WithFanout additionally child-links it (the snapshot
// linkage lane), so the three DAT-013 resolution branches can be pinned
// independently (review round 1, testing finding 3).
func seedLegacyIntent(t *testing.T, s interface {
	CommitLineage(ctx context.Context, lin ports.Lineage) error
}, dispatchID string, withFanout bool) {
	t.Helper()
	lin := runtimeLineage(t, dispatchID, e12t1LegacyRequestJSON(t, dispatchID))
	if withFanout {
		lin.Intent.Fanout = &ports.FanoutInput{
			AggregateID: "agg-" + dispatchID, Origin: string(records.OriginArrival),
			DestinationID: "wiki-primary", DestinationRevision: snapshotLaneRevision, Workstream: "maintenance",
			Selections: []records.DestinationSelection{{
				DestinationID: "wiki-primary", DestinationRevision: snapshotLaneRevision, Workstream: "maintenance", Reason: "fanout_mode:all",
			}},
			Revisions: []ports.DestinationRevisionInput{{
				DestinationID: "wiki-primary", Revision: snapshotLaneRevision, ProjectionJSON: snapshotLaneProjection,
			}},
		}
	}
	if err := s.CommitLineage(context.Background(), lin); err != nil {
		t.Fatal(err)
	}
}

// TestE12T1RerunPersistsChildAndAggregate proves the rerun creation path
// stays child-linked (review round 1, testing finding 1): the new intent
// lands as one child beneath its own origin `rerun` aggregate while the
// original is superseded in the same transaction.
func TestE12T1RerunPersistsChildAndAggregate(t *testing.T) {
	s := openE3T3Store(t)
	seedReadyIntent(t, s, "dispatch-1")
	op := &OperatorService{Store: s, Now: func() string { return "2026-08-20T01:02:00Z" }}
	summary, err := op.Rerun(context.Background(), "dispatch-1", "operator", "operator wants a fresh run")
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.LoadChildDispatch(context.Background(), summary.DispatchID)
	if err != nil {
		t.Fatalf("the rerun intent must be child-linked: %v", err)
	}
	if child.DestinationID != "wiki-primary" || child.DestinationRevision != testLaneRevision || child.Workstream != "maintenance" {
		t.Fatalf("the rerun child keeps the stored request's lane: %+v", child)
	}
	agg, err := s.LoadAggregateEvent(context.Background(), child.AggregateID)
	if err != nil || agg.Origin != string(records.OriginRerun) {
		t.Fatalf("the rerun aggregate must persist with origin rerun: %+v %v", agg, err)
	}
}

// TestE12T1LaneResolutionBranches pins the shared DAT-013 precedence
// through Rerun (review round 1, testing finding 3): the stored request
// block wins, the snapshot child linkage covers a block-less request, a
// wired resolver covers a bare legacy intent — persisting the referenced
// destination-revision record — and nothing resolving fails closed.
func TestE12T1LaneResolutionBranches(t *testing.T) {
	t.Run("stored request block wins", func(t *testing.T) {
		s := openE3T3Store(t)
		seedReadyIntent(t, s, "dispatch-1")
		op := &OperatorService{
			Store: s, Now: func() string { return "2026-08-20T01:02:00Z" },
			DestinationResolver: func(routeID string) (ports.TaskDestinationRef, string, bool, string) {
				return ports.TaskDestinationRef{ID: "resolver-lane", Revision: liveLaneRevision, Workstream: "live"}, liveLaneProjection, true, ""
			},
		}
		summary, err := op.Rerun(context.Background(), "dispatch-1", "operator", "r")
		if err != nil {
			t.Fatal(err)
		}
		child, _ := s.LoadChildDispatch(context.Background(), summary.DispatchID)
		if child.DestinationID != "wiki-primary" {
			t.Fatalf("the stored request's lane must win over the resolver: %+v", child)
		}
		var live int
		_ = s.QueryRow(`SELECT COUNT(*) FROM destination_revisions WHERE destination_id = 'resolver-lane'`).Scan(&live)
		if live != 0 {
			t.Fatal("a stored-block rerun must not persist the resolver's revision record")
		}
	})
	t.Run("snapshot linkage covers a block-less request", func(t *testing.T) {
		s := openE3T3Store(t)
		seedLegacyIntent(t, s, "dispatch-2", true)
		op := &OperatorService{Store: s, Now: func() string { return "2026-08-20T01:02:00Z" }}
		summary, err := op.Rerun(context.Background(), "dispatch-2", "operator", "r")
		if err != nil {
			t.Fatal(err)
		}
		child, _ := s.LoadChildDispatch(context.Background(), summary.DispatchID)
		if child.DestinationRevision != snapshotLaneRevision {
			t.Fatalf("the snapshot's child linkage must supply the lane: %+v", child)
		}
	})
	t.Run("resolver covers a bare legacy intent and persists the revision", func(t *testing.T) {
		s := openE3T3Store(t)
		seedLegacyIntent(t, s, "dispatch-3", false)
		op := &OperatorService{
			Store: s, Now: func() string { return "2026-08-20T01:02:00Z" },
			DestinationResolver: func(routeID string) (ports.TaskDestinationRef, string, bool, string) {
				return ports.TaskDestinationRef{ID: "wiki-primary", Revision: liveLaneRevision, Workstream: "maintenance"}, liveLaneProjection, true, ""
			},
		}
		summary, err := op.Rerun(context.Background(), "dispatch-3", "operator", "r")
		if err != nil {
			t.Fatal(err)
		}
		child, _ := s.LoadChildDispatch(context.Background(), summary.DispatchID)
		if child.DestinationRevision != liveLaneRevision {
			t.Fatalf("the resolver's live lane must be used: %+v", child)
		}
		var revisions int
		if err := s.QueryRow(`SELECT COUNT(*) FROM destination_revisions WHERE destination_id = 'wiki-primary' AND revision = ?`, liveLaneRevision).Scan(&revisions); err != nil || revisions != 1 {
			t.Fatalf("the live-resolved lane's revision record must persist with the child: %d %v", revisions, err)
		}
	})
	t.Run("nothing resolving fails closed", func(t *testing.T) {
		s := openE3T3Store(t)
		seedLegacyIntent(t, s, "dispatch-4", false)
		op := &OperatorService{Store: s, Now: func() string { return "2026-08-20T01:02:00Z" }}
		_, err := op.Rerun(context.Background(), "dispatch-4", "operator", "r")
		if err == nil || !errors.Is(err, ports.ErrStateNotEligible) {
			t.Fatalf("a bare legacy intent without a resolver must fail closed: %v", err)
		}
	})
}

// TestE12T1RebuildPersistsChildAndAggregate mirrors the rerun test
// through RebuildStale (E12 epic validation, testing gap): a legacy
// stored request whose lane resolves through the live resolver rebuilds
// as one child beneath its own origin `rebuild` aggregate, and the
// live-resolved destination-revision record persists with it (DAT-010).
func TestE12T1RebuildPersistsChildAndAggregate(t *testing.T) {
	s := openE3T3Store(t)
	seedLegacyIntent(t, s, "dispatch-stale", false)
	op := &OperatorService{
		Store: s, Now: func() string { return "2026-08-20T01:02:00Z" },
		RevisionResolver: func(routeID string) (string, bool) { return "route-rev-1", true },
		TargetResolver: func(routeID string) (string, string, string, bool) {
			return "hermes-kanban-main", "hermes_kanban", "board-main", true
		},
		DestinationResolver: func(routeID string) (ports.TaskDestinationRef, string, bool, string) {
			return ports.TaskDestinationRef{ID: "wiki-primary", Revision: liveLaneRevision, Workstream: "maintenance"}, liveLaneProjection, true, ""
		},
	}
	replacement, err := op.RebuildStale(context.Background(), "dispatch-stale", "operator")
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	child, err := s.LoadChildDispatch(context.Background(), replacement)
	if err != nil {
		t.Fatalf("the rebuild intent must be child-linked: %v", err)
	}
	if child.DestinationID != "wiki-primary" || child.DestinationRevision != liveLaneRevision {
		t.Fatalf("the rebuild child must carry the resolved lane: %+v", child)
	}
	agg, err := s.LoadAggregateEvent(context.Background(), child.AggregateID)
	if err != nil || agg.Origin != string(records.OriginRebuild) {
		t.Fatalf("the rebuild aggregate must persist with origin rebuild: %+v %v", agg, err)
	}
	var revisions int
	if err := s.QueryRow(`SELECT COUNT(*) FROM destination_revisions WHERE destination_id = 'wiki-primary' AND revision = ?`, liveLaneRevision).Scan(&revisions); err != nil || revisions != 1 {
		t.Fatalf("the live-resolved lane's revision record must persist with the rebuild child: %d %v", revisions, err)
	}
}

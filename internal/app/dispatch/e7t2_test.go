package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/app/reconcile"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
	"github.com/irootkernel/agent-dispatch/internal/testsupport/fakesink"
)

// E7-T2 round-1 review coverage: the transactional route-slot guard, the
// rerun store backstop, and the dead-lettered rerun supersession edge.

// TestSubmitRefusedWhenSlotHeldByAnother proves the CON-001 guard at
// both layers (E7-T2/B-2): a dispatch whose route slot is held by
// another authoritative dispatch is refused by the runtime pre-check
// and by the lease transaction itself.
func TestSubmitRefusedWhenSlotHeldByAnother(t *testing.T) {
	s := openE3T3Store(t)
	seedReadyIntent(t, s, "dispatch-1")
	op := &OperatorService{Store: s, Now: func() string { return "2026-08-20T01:02:00Z" }}
	rerun, err := op.Rerun(context.Background(), "dispatch-1", "operator", "make the raced shape")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the raced shape directly: the superseded original is put
	// back to ready beside the slot-holding rerun (the store's own
	// writers make this unreachable in normal operation; both guard
	// layers must still hold).
	if _, err := s.Exec(`UPDATE dispatch_intents SET state = 'ready' WHERE dispatch_id = 'dispatch-1'`); err != nil {
		t.Fatal(err)
	}
	rt := &Runtime{Store: s, Sink: fakesink.New("fake-main"), Now: time.Now, LeaseTTL: time.Minute, Actor: "test"}
	_, err = rt.SubmitOnce(context.Background(), "dispatch-1", "p1")
	if err == nil {
		t.Fatal("submitting beside another authoritative dispatch must be refused")
	}
	if !errors.Is(err, ports.ErrStateNotEligible) {
		t.Fatalf("the runtime pre-check must refuse with the state conflict: %v", err)
	}
	// The lease transaction enforces the same predicate without the
	// runtime pre-check (store-level authority).
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-slot", Owner: "p2",
		Now: "2026-08-20T01:00:00Z", LeaseExpiresAt: "2026-08-20T01:01:00Z",
	}); !errors.Is(err, ports.ErrRouteSlotHeld) {
		t.Fatalf("the lease transaction must refuse with the slot conflict: %v", err)
	}
	snap, _ := s.LoadIntent(context.Background(), "dispatch-1")
	if snap.State != records.IntentReady {
		t.Fatalf("the refused dispatch must stay ready: %+v", snap)
	}
	if rerun.State != records.IntentReady {
		t.Fatalf("the slot holder must be untouched: %+v", rerun)
	}
}

// TestRerunDeadLetteredOriginalSuperseded proves the second declared
// supersede edge (E7-T2/B-2): rerunning dead-lettered work moves the
// original to superseded under the reprocess-or-discard reason with the
// takeover audited.
func TestRerunDeadLetteredOriginalSuperseded(t *testing.T) {
	s := openE3T3Store(t)
	seedReadyIntent(t, s, "dispatch-1")
	rt := &Runtime{
		Store: s, Sink: fakesink.New("fake-main", fakesink.Step{Err: errors.New("deadline exceeded after write")}),
		Now: time.Now, LeaseTTL: time.Minute, Actor: "test",
	}
	if _, err := rt.SubmitOnce(context.Background(), "dispatch-1", "p1"); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.LoadIntent(context.Background(), "dispatch-1")
	fake := fakesink.New("fake-main")
	fake.ScriptLookupByIdempotency(snap.IdempotencyKey, fakesink.LookupStep{
		Result: ports.LookupResult{Status: ports.LookupAmbiguous},
	})
	rec := &reconcile.Service{Store: s, Sink: fake, Now: func() string { return time.Now().UTC().Format(time.RFC3339) }}
	if _, err := rec.Reconcile(context.Background(), "dispatch-1", "reconciler", true); err != nil {
		t.Fatal(err)
	}
	dead, _ := s.LoadIntent(context.Background(), "dispatch-1")
	if dead.State != records.IntentDeadLettered {
		t.Fatalf("setup must dead-letter the original: %+v", dead)
	}
	op := &OperatorService{Store: s, Now: func() string { return time.Now().UTC().Format(time.RFC3339) }}
	summary, err := op.Rerun(context.Background(), "dispatch-1", "operator", "redo the dead letter")
	if err != nil {
		t.Fatal(err)
	}
	original, _ := s.LoadIntent(context.Background(), "dispatch-1")
	if original.State != records.IntentSuperseded {
		t.Fatalf("the dead-lettered original must be superseded: %+v", original)
	}
	if summary.State != records.IntentReady || summary.DispatchID == "dispatch-1" {
		t.Fatalf("the rerun must be a fresh ready dispatch: %+v", summary)
	}
}

// TestRerunIntentStoreBackstopRefusesInFlight proves the in-transaction
// backstop (E7-T2/B-2): calling the store's RerunIntent directly on an
// in-flight original is refused even without the operator's pre-check.
func TestRerunIntentStoreBackstopRefusesInFlight(t *testing.T) {
	s := openE3T3Store(t)
	seedReadyIntent(t, s, "dispatch-1")
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-1", Owner: "victim",
		Now: "2026-08-20T01:00:00Z", LeaseExpiresAt: "2026-08-20T09:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.LoadIntent(context.Background(), "dispatch-1")
	if err != nil {
		t.Fatal(err)
	}
	req := rerunRequestFromSnapshot(t, snap)
	if _, err := s.RerunIntent(context.Background(), ports.RerunInput{
		OriginalDispatchID: "dispatch-1", Actor: "operator", Reason: "must refuse",
		New: req,
	}); !errors.Is(err, ports.ErrStateNotEligible) {
		t.Fatalf("the store backstop must refuse an in-flight original: %v", err)
	}
	after, _ := s.LoadIntent(context.Background(), "dispatch-1")
	if after.State != records.IntentSubmitting {
		t.Fatalf("the in-flight original must be untouched: %+v", after)
	}
}

// rerunRequestFromSnapshot builds one valid rerun intent input from a
// stored snapshot, mirroring the operator's construction.
func rerunRequestFromSnapshot(t *testing.T, snap ports.IntentSnapshot) ports.IntentInput {
	t.Helper()
	next, _, err := BuildRequest(RequestInput{
		DispatchID:         snap.DispatchID + "-rerun-1",
		Route:              ports.TaskRouteRef{ID: snap.RouteID, Revision: "route-rev-1"},
		Resource:           ports.TaskResource{ID: snap.ResourceID, Workspace: "dir:/srv/vault"},
		TargetID:           snap.TargetID,
		Generation:         snap.Generation + 1,
		Fingerprint:        records.Digest(snap.ManifestDigest),
		AcceptanceCriteria: WikiAcceptanceCriteria,
	})
	if err != nil {
		t.Fatal(err)
	}
	requestJSON, err := MarshalRequest(next)
	if err != nil {
		t.Fatal(err)
	}
	return ports.IntentInput{
		DispatchID: next.DispatchID, RouteID: snap.RouteID, RouteRevision: "route-rev-1",
		TargetID: snap.TargetID, TargetType: snap.TargetType, TargetScope: snap.TargetScope,
		ResourceID: snap.ResourceID, Generation: next.Activation.Generation,
		IdempotencyKey: next.IdempotencyKey, ContentFingerprint: next.Activation.ContentFingerprint,
		ManifestDigest: snap.ManifestDigest, RequestVersion: RequestContractVersion,
		RequestJSON: requestJSON, CreatedAt: "2026-08-20T01:00:00Z",
	}
}

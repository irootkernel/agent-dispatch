package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// dispatchNow is the fixed clock the dispatch tests run under.
const dispatchNow = "2026-08-20T01:00:00Z"

func lineage(dispatchID, idempotencyKey string) ports.Lineage {
	return ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: "obs-" + dispatchID, SchemaVersion: "agent-dispatch.source-observation/v1",
			SourceType: "watchman", SourceID: "watchman-main", TriggerName: "agent-dispatch-wiki-maintenance",
			ResourceID: "vault-main", ObservedAt: dispatchNow, ReceivedAt: dispatchNow,
			RawPayloadDigest: "sha256:" + repeat("a", 64), IngestStatus: "accepted",
			Changes: []ports.ObservationChange{{
				Ordinal: 0, Path: "Inbox/n.md", Operation: "modify", ExistsAfter: true, FileType: "regular",
				AfterDigest: "sha256:" + repeat("b", 64), DigestStatus: "known",
			}},
		},
		Batch: ports.BatchInput{
			BatchID: "batch-" + dispatchID, RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: dispatchNow,
			ContentFingerprint: "sha256:" + repeat("c", 64), ObservationIDs: []string{"obs-" + dispatchID},
		},
		Decision: ports.DecisionInput{
			DecisionID: "decision-" + dispatchID, BatchID: "batch-" + dispatchID, RouteID: "wiki-maintenance",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch",
			Classification: "normal", ReasonCodesJSON: `["meaningful_markdown_change"]`, CreatedAt: dispatchNow, Actor: "planner",
		},
		Intent: ports.IntentInput{
			DispatchID: dispatchID, DecisionID: "decision-" + dispatchID, RouteID: "wiki-maintenance",
			RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes_kanban",
			ResourceID: "vault-main", Generation: 1, IdempotencyKey: idempotencyKey,
			ContentFingerprint: "sha256:" + repeat("c", 64), ManifestDigest: "sha256:" + repeat("d", 64),
			RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: `{"contract_version":"agent-dispatch.hermes-task/v1"}`,
			CreatedAt: dispatchNow,
		},
	}
}

func repeat(ch string, n int) string {
	out := make([]byte, 0, n)
	for len(out) < n {
		out = append(out, ch[0])
	}
	return string(out)
}

func commitTestLineage(t *testing.T, s *Store, dispatchID string) {
	t.Helper()
	if err := s.CommitLineage(context.Background(), lineage(dispatchID, "agent-dispatch:v1:sha256:"+repeat(dispatchID[len(dispatchID)-1:], 64))); err != nil {
		t.Fatalf("commit lineage: %v", err)
	}
}

// TestCommitLineagePersistsWholeChain proves one transaction persists the
// observation, batch, decision, and intent together (ingestion
// transaction, DUR-002).
func TestCommitLineagePersistsWholeChain(t *testing.T) {
	s := openTestStore(t)
	commitTestLineage(t, s, "dispatch-1")
	snap, err := s.LoadIntent(context.Background(), "dispatch-1")
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != records.IntentReady || snap.IdempotencyKey == "" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	for _, table := range []string{"source_observations", "change_batches", "policy_decisions", "dispatch_intents"} {
		var n int
		if err := s.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s rows = %d, %v", table, n, err)
		}
	}
	var route string
	if err := s.QueryRow(`SELECT active_dispatch_id FROM route_runtime_state WHERE route_id = 'wiki-maintenance'`).Scan(&route); err != nil || route != "dispatch-1" {
		t.Fatalf("route slot must be reserved by the intent: %q %v", route, err)
	}
}

// TestCommitLineageDuplicateIdempotency proves the target/idempotency
// uniqueness constraint is enforced through the port error.
func TestCommitLineageDuplicateIdempotency(t *testing.T) {
	s := openTestStore(t)
	key := "agent-dispatch:v1:sha256:" + repeat("1", 64)
	if err := s.CommitLineage(context.Background(), lineage("dispatch-1", key)); err != nil {
		t.Fatal(err)
	}
	err := s.CommitLineage(context.Background(), lineage("dispatch-2", key))
	if !errors.Is(err, ports.ErrIdempotencyConflict) {
		t.Fatalf("duplicate key must map to ErrIdempotencyConflict: %v", err)
	}
	// The rejected lineage leaves no partial persistence.
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE dispatch_id = 'dispatch-2'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected intent must not persist: %d %v", n, err)
	}
}

// TestCommitLineageRouteSlotHeld proves a second active dispatch for one
// route is refused (one route cannot hold two active dispatch IDs).
func TestCommitLineageRouteSlotHeld(t *testing.T) {
	s := openTestStore(t)
	commitTestLineage(t, s, "dispatch-1")
	second := lineage("dispatch-2", "agent-dispatch:v1:sha256:"+repeat("2", 64))
	err := s.CommitLineage(context.Background(), second)
	if !errors.Is(err, ports.ErrRouteSlotHeld) {
		t.Fatalf("second active dispatch must map to ErrRouteSlotHeld: %v", err)
	}
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM dispatch_intents WHERE dispatch_id = 'dispatch-2'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("refused intent must not persist: %d %v", n, err)
	}
}

// TestAcquireAttemptExclusive proves two competing processes cannot own
// the same attempt: the first conditional write wins and the second
// receives ErrLeaseHeld (DUR-012).
func TestAcquireAttemptExclusive(t *testing.T) {
	s := openTestStore(t)
	commitTestLineage(t, s, "dispatch-1")
	first, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-1", Owner: "process-a",
		Now: dispatchNow, LeaseExpiresAt: "2026-08-20T01:01:00Z",
	})
	if err != nil || first != "attempt-1" {
		t.Fatalf("first acquisition: %q %v", first, err)
	}
	_, err = s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-2", Owner: "process-b",
		Now: dispatchNow, LeaseExpiresAt: "2026-08-20T01:01:00Z",
	})
	if !errors.Is(err, ports.ErrLeaseHeld) {
		t.Fatalf("second acquisition must be refused: %v", err)
	}
	snap, _ := s.LoadIntent(context.Background(), "dispatch-1")
	if snap.State != records.IntentSubmitting || snap.LeaseOwner != "process-a" {
		t.Fatalf("unexpected lease state: %+v", snap)
	}
	var owner string
	if err := s.QueryRow(`SELECT lease_owner FROM dispatch_attempts WHERE attempt_id = 'attempt-1'`).Scan(&owner); err != nil || owner != "process-a" {
		t.Fatalf("attempt row must carry the owner: %q %v", owner, err)
	}
	// After expiry the lease is NOT directly reacquirable: the state
	// machine requires recovery to unknown and reconciliation first
	// (submitting -> submitting does not exist).
	_, err = s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-3", Owner: "process-b",
		Now: "2026-08-20T01:02:00Z", LeaseExpiresAt: "2026-08-20T01:03:00Z",
	})
	if err == nil {
		t.Fatal("an expired submitting lease must recover before re-leasing")
	}
}

// TestAcquireAttemptAuditsTransition proves acquisition appends the
// validated lease transition to the audit history (DUR-011).
func TestAcquireAttemptAuditsTransition(t *testing.T) {
	s := openTestStore(t)
	commitTestLineage(t, s, "dispatch-1")
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-1", Owner: "process-a",
		Now: dispatchNow, LeaseExpiresAt: "2026-08-20T01:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	var from, to string
	if err := s.QueryRow(`SELECT from_state, to_state FROM state_transitions WHERE entity_id = 'dispatch-1' ORDER BY recorded_at DESC LIMIT 1`).Scan(&from, &to); err != nil {
		t.Fatal(err)
	}
	if from != "ready" || to != "submitting" {
		t.Fatalf("audit must record ready -> submitting: %s -> %s", from, to)
	}
}

// TestCompleteAttemptValidatesTransition proves an attempt completion
// whose transition fails the domain guards is rejected atomically.
func TestCompleteAttemptValidatesTransition(t *testing.T) {
	s := openTestStore(t)
	commitTestLineage(t, s, "dispatch-1")
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-1", Owner: "process-a",
		Now: dispatchNow, LeaseExpiresAt: "2026-08-20T01:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	// completed is not reachable from submitting without proven acceptance.
	err := s.CompleteAttempt(context.Background(), ports.AttemptResult{
		AttemptID: "attempt-1", DispatchID: "dispatch-1", Outcome: "accepted", CompletedAt: "2026-08-20T01:00:30Z",
		Transition: ports.AttemptTransition{To: records.IntentCompleted, Reason: state.ReasonExecutionSucceeded},
	})
	var te *state.TransitionError
	if !errors.As(err, &te) {
		t.Fatalf("guard violation must surface the typed transition error: %v", err)
	}
	var completed sql.NullString
	if err := s.QueryRow(`SELECT completed_at FROM dispatch_attempts WHERE attempt_id = 'attempt-1'`).Scan(&completed); err != nil || completed.Valid {
		t.Fatalf("rejected completion must not close the attempt: %v %v", completed, err)
	}
}

// TestCompleteAttemptAccepted proves the accepted path records the
// receipt, closes the attempt, and transitions with audit in one
// transaction.
func TestCompleteAttemptAccepted(t *testing.T) {
	s := openTestStore(t)
	commitTestLineage(t, s, "dispatch-1")
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-1", Owner: "process-a",
		Now: dispatchNow, LeaseExpiresAt: "2026-08-20T01:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	err := s.CompleteAttempt(context.Background(), ports.AttemptResult{
		AttemptID: "attempt-1", DispatchID: "dispatch-1", Outcome: "accepted", CompletedAt: "2026-08-20T01:00:30Z",
		Transition: ports.AttemptTransition{
			To: records.IntentAccepted, Reason: state.ReasonDurableAcceptance,
			Evidence:     state.IntentEvidence{ReceiptRef: "rcpt-attempt-1"},
			TransitionID: "attempt-1:durable_acceptance",
		},
		Receipt: &ports.ReceiptInput{
			ReceiptID: "rcpt-attempt-1", Acceptance: records.AcceptanceAccepted, Durable: true,
			ExternalRef: "task-1", ReceivedAt: "2026-08-20T01:00:30Z",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := s.LoadIntent(context.Background(), "dispatch-1")
	if snap.State != records.IntentAccepted || snap.LeaseOwner != "" {
		t.Fatalf("completion must clear the lease and record acceptance: %+v", snap)
	}
	var acceptance string
	if err := s.QueryRow(`SELECT acceptance_state FROM dispatch_receipts WHERE receipt_id = 'rcpt-attempt-1'`).Scan(&acceptance); err != nil || acceptance != "accepted" {
		t.Fatalf("receipt must persist: %q %v", acceptance, err)
	}
	var outcome string
	if err := s.QueryRow(`SELECT outcome FROM dispatch_attempts WHERE attempt_id = 'attempt-1'`).Scan(&outcome); err != nil || outcome != "accepted" {
		t.Fatalf("attempt outcome: %q %v", outcome, err)
	}
}

// TestRecoverExpiredSubmitting proves an abandoned submitting state
// defaults to unknown with audit evidence and a closed attempt row
// (persistence §5, DUR-010).
func TestRecoverExpiredSubmitting(t *testing.T) {
	s := openTestStore(t)
	commitTestLineage(t, s, "dispatch-1")
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-1", Owner: "process-a",
		Now: dispatchNow, LeaseExpiresAt: "2026-08-20T01:00:30Z",
	}); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.RecoverExpiredSubmitting(context.Background(), "2026-08-20T01:01:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].DispatchID != "dispatch-1" || recovered[0].AttemptID != "attempt-1" {
		t.Fatalf("unexpected recovery list: %+v", recovered)
	}
	snap, _ := s.LoadIntent(context.Background(), "dispatch-1")
	if snap.State != records.IntentUnknown || snap.LeaseOwner != "" {
		t.Fatalf("expired lease must recover to unknown: %+v", snap)
	}
	var outcome, code string
	if err := s.QueryRow(`SELECT outcome, error_code FROM dispatch_attempts WHERE attempt_id = 'attempt-1'`).Scan(&outcome, &code); err != nil || outcome != "unknown" || code != "lease_expired" {
		t.Fatalf("recovered attempt must close unknown: %q %q %v", outcome, code, err)
	}
	var to string
	if err := s.QueryRow(`SELECT to_state FROM state_transitions WHERE entity_id = 'dispatch-1' ORDER BY recorded_at DESC LIMIT 1`).Scan(&to); err != nil || to != "unknown" {
		t.Fatalf("recovery must audit submitting -> unknown: %q %v", to, err)
	}
	// A second recovery pass is a no-op.
	if again, err := s.RecoverExpiredSubmitting(context.Background(), "2026-08-20T01:02:00Z"); err != nil || len(again) != 0 {
		t.Fatalf("second recovery must find nothing: %+v %v", again, err)
	}
}

// TestRecoverLeavesUnexpiredAlone proves an unexpired lease is never
// taken over.
func TestRecoverLeavesUnexpiredAlone(t *testing.T) {
	s := openTestStore(t)
	commitTestLineage(t, s, "dispatch-1")
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: "attempt-1", Owner: "process-a",
		Now: dispatchNow, LeaseExpiresAt: "2026-08-20T02:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.RecoverExpiredSubmitting(context.Background(), "2026-08-20T01:01:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 0 {
		t.Fatalf("unexpired lease must not be recovered: %+v", recovered)
	}
	snap, _ := s.LoadIntent(context.Background(), "dispatch-1")
	if snap.State != records.IntentSubmitting {
		t.Fatalf("unexpired submitting must remain: %+v", snap)
	}
}

// TestLoadIntentNotFound proves the typed not-found error.
func TestLoadIntentNotFound(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.LoadIntent(context.Background(), "missing"); !errors.Is(err, ports.ErrIntentNotFound) {
		t.Fatalf("expected ErrIntentNotFound: %v", err)
	}
}

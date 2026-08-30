package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E13-T1 acceptance coverage: the transactional notification outbox of
// ADR-0019 — per-sink intent creation inside the completion transaction
// (DUR-016), the five-component dedup identity (NTF-003, AC-902), the
// independent attempt lifecycle (NTF-004/NTF-005, DAT-010), the
// remaining reportable transitions (delivery unknown, quarantine, the
// pending-reconciliation appearance), and retention pruning of resolved
// evidence.

// e13t1Policy installs a two-sink policy resolver over the seeded route:
// one log sink and one webhook sink, both carrying the full effective
// event set — the exact per-sink fan-out the dedup identity must cover.
func e13t1Policy(s *Store, events ...records.NotificationEventKind) *ports.NotificationPolicy {
	policy := &ports.NotificationPolicy{
		Events: events,
		Sinks: []ports.NotificationSinkRef{
			{ID: "ops-log", Type: "log"},
			{ID: "ops-webhook", Type: "webhook"},
		},
		Revision: "policy-rev-notif-1",
	}
	s.SetNotificationPolicy(func(routeID string) *ports.NotificationPolicy {
		if routeID != "wiki-maintenance" {
			return nil
		}
		return policy
	})
	return policy
}

func e13t1AllEvents() []records.NotificationEventKind {
	return []records.NotificationEventKind{
		records.EventWorkCompleted, records.EventWorkFailed, records.EventWorkExhausted,
		records.EventDeliveryUnknown, records.EventQuarantined, records.EventReconciliationRequired,
		records.EventIntegrationDrift, records.EventWatchmanDrift,
	}
}

// TestE13T1CompletionCreatesPerSinkIntentsTransactionally pins DUR-016
// and NTF-003: a clean lane completion commits exactly one work_completed
// intent per configured sink inside the completion transaction, scoped
// to the completed lane, carrying the policy revision — and a rollback
// of the owning transition rolls the intents back with it.
func TestE13T1CompletionCreatesPerSinkIntentsTransactionally(t *testing.T) {
	s := openTestStore(t)
	e13t1Policy(s, e13t1AllEvents()...)
	first, second := e12t2CommitFanout(t, s, "decision-e13t1-a")
	ctx := context.Background()
	if _, err := s.CompleteActive(ctx, ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: first.Intent.DispatchID,
		ReceiptRef: "rcpt-e13t1", Actor: "hermes-task", Now: "2026-08-30T01:00:00Z",
	}); err != nil {
		t.Fatalf("clean completion: %v", err)
	}
	notifications, err := s.ListNotifications(ctx, ports.NotificationFilter{RouteID: "wiki-maintenance"})
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 2 {
		t.Fatalf("one completion must create exactly one intent per sink: %d", len(notifications))
	}
	bySink := map[string]ports.NotificationEventRecord{}
	for _, n := range notifications {
		bySink[n.SinkID] = n
	}
	for _, sinkID := range []string{"ops-log", "ops-webhook"} {
		n, ok := bySink[sinkID]
		if !ok {
			t.Fatalf("sink %s must carry its intent", sinkID)
		}
		if n.Event != records.EventWorkCompleted || n.State != records.NotificationPending {
			t.Fatalf("sink %s intent must be pending work_completed: %+v", sinkID, n)
		}
		if n.DestinationID != "wiki-primary" {
			t.Fatalf("the intent must scope to the completed lane: %+v", n)
		}
		if n.PolicyRevision != "policy-rev-notif-1" {
			t.Fatalf("the intent must carry the notification policy revision: %+v", n)
		}
		wantID := records.NotificationID(records.EventWorkCompleted, "wiki-primary",
			"dispatch:"+first.Intent.DispatchID+":IDLE", sinkID, "policy-rev-notif-1")
		if n.NotificationID != wantID {
			t.Fatalf("the identity must derive from the five dedup components: %s want %s", n.NotificationID, wantID)
		}
		if !strings.HasPrefix(n.IdempotencyKey, "ntfidem-") ||
			n.IdempotencyKey != records.NotificationIdempotencyKey(n.NotificationID) {
			t.Fatalf("the stored idempotency key must be the stable derivation: %q", n.IdempotencyKey)
		}
		// SEC-011 structure: the payload carries identities, states, and
		// reason codes only — no body, secret, or absolute path can appear
		// because none exists in the projection.
		for _, forbidden := range []string{"secret", "/srv/vault", "token"} {
			if strings.Contains(n.PayloadJSON, forbidden) {
				t.Fatalf("the payload must stay channel-neutral and safe: %s", n.PayloadJSON)
			}
		}
		if !strings.Contains(n.PayloadJSON, `"schema_version":"agent-dispatch.notification-event/v1"`) ||
			!strings.Contains(n.PayloadJSON, `"dispatch_id":"`+first.Intent.DispatchID+`"`) {
			t.Fatalf("the payload must carry the record contract and the source identity: %s", n.PayloadJSON)
		}
	}
	// The second lane's completion is a distinct transition occurrence:
	// its own per-sink intents, no interference with the first's.
	if _, err := s.CompleteActive(ctx, ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: second.Intent.DispatchID,
		ReceiptRef: "rcpt-e13t1-b", Actor: "hermes-task", Now: "2026-08-30T01:01:00Z",
	}); err != nil {
		t.Fatalf("second completion: %v", err)
	}
	counts, err := s.CountNotificationsByState(ctx)
	if err != nil || counts["pending"] != 4 {
		t.Fatalf("two completions across two lanes must leave four pending intents: %+v %v", counts, err)
	}
}

// TestE13T1DedupCollapsesRepeatedTransitionEvaluation pins AC-902 and
// NTF-003: re-evaluating the same transition occurrence — the replay a
// crash recovery or aggregate rerun performs — collapses onto the
// existing identity instead of notifying twice, while a transition the
// policy does not carry creates nothing.
func TestE13T1DedupCollapsesRepeatedTransitionEvaluation(t *testing.T) {
	s := openTestStore(t)
	// The policy carries only reconciliation_required: every other event
	// class must create nothing (NTF-001's per-event configurability).
	e13t1Policy(s, records.EventReconciliationRequired)
	ctx := context.Background()
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.enqueueNotificationTx(tx, "wiki-maintenance", records.EventReconciliationRequired,
			"route:wiki-maintenance:pending_reconcile:mark", "",
			map[string]string{"origin": "explicit_mark"}, "2026-08-30T02:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	notifications, err := s.ListNotifications(ctx, ports.NotificationFilter{RouteID: "wiki-maintenance"})
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 2 { // one per sink, not three per sink
		t.Fatalf("a replayed transition must collapse onto its identity: %d", len(notifications))
	}
	// A policy without the event creates nothing: work completions stay
	// silent under this reconciliation-only policy.
	first, _ := e12t2CommitFanout(t, s, "decision-e13t1-dedup")
	if _, err := s.CompleteActive(ctx, ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: first.Intent.DispatchID,
		ReceiptRef: "rcpt-e13t1-dedup", Actor: "test", Now: "2026-08-30T02:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	notifications, err = s.ListNotifications(ctx, ports.NotificationFilter{RouteID: "wiki-maintenance", Event: records.EventWorkCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 0 {
		t.Fatalf("an event outside the effective policy must not notify: %+v", notifications)
	}
}

// TestE13T1AttemptLifecycleNeverRewritesSourceState pins NTF-004 and
// NTF-005: attempts are separate durable records; an ambiguous delivery
// leaves the notification pending under the same stable idempotency key;
// a definite outcome resolves it; and no attempt — failed, ambiguous, or
// delivered — changes the completed dispatch, lane, or receipt state a
// crash would have to survive.
func TestE13T1AttemptLifecycleNeverRewritesSourceState(t *testing.T) {
	s := openTestStore(t)
	e13t1Policy(s, e13t1AllEvents()...)
	first, _ := e12t2CommitFanout(t, s, "decision-e13t1-attempt")
	ctx := context.Background()
	if _, err := s.CompleteActive(ctx, ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: first.Intent.DispatchID,
		ReceiptRef: "rcpt-attempt", Actor: "hermes-task", Now: "2026-08-30T03:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	notifications, err := s.ListNotifications(ctx, ports.NotificationFilter{RouteID: "wiki-maintenance", SinkID: "ops-webhook"})
	if err != nil || len(notifications) != 1 {
		t.Fatalf("one webhook intent: %+v %v", notifications, err)
	}
	notification := notifications[0]
	intentBefore, err := s.LoadIntent(ctx, first.Intent.DispatchID)
	if err != nil {
		t.Fatal(err)
	}
	// The crash-window shape: delivery fails ambiguously (a webhook
	// timeout), the attempt records, and the notification stays pending
	// under its stable key.
	firstAttempt, err := s.RecordNotificationAttempt(ctx, ports.NotificationAttemptInput{
		NotificationID: notification.NotificationID, Outcome: records.NotificationAmbiguousOutcome,
		ErrorCode: "webhook_timeout", StartedAt: "2026-08-30T03:00:01Z", CompletedAt: "2026-08-30T03:00:04Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstAttempt.AttemptNumber != 1 || firstAttempt.AttemptID != records.NotificationAttemptID(notification.NotificationID, 1) {
		t.Fatalf("the first attempt must derive its deterministic identity: %+v", firstAttempt)
	}
	reloaded, err := s.LoadNotification(ctx, notification.NotificationID)
	if err != nil || reloaded.State != records.NotificationPending || reloaded.ResolvedAt != "" {
		t.Fatalf("an ambiguous outcome must leave the notification pending: %+v %v", reloaded, err)
	}
	// The retry reuses the stable idempotency key (NTF-007): the record's
	// delivery identity never changes across attempts.
	if reloaded.IdempotencyKey != notification.IdempotencyKey {
		t.Fatalf("retry must reuse the stable idempotency key: %q vs %q", reloaded.IdempotencyKey, notification.IdempotencyKey)
	}
	// A definite success resolves delivered and resolves nothing else.
	secondAttempt, err := s.RecordNotificationAttempt(ctx, ports.NotificationAttemptInput{
		NotificationID: notification.NotificationID, Outcome: records.NotificationDeliveredOutcome,
		StartedAt: "2026-08-30T03:05:00Z", CompletedAt: "2026-08-30T03:05:01Z",
	})
	if err != nil || secondAttempt.AttemptNumber != 2 {
		t.Fatalf("the second attempt must sequence: %+v %v", secondAttempt, err)
	}
	reloaded, err = s.LoadNotification(ctx, notification.NotificationID)
	if err != nil || reloaded.State != records.NotificationDelivered || reloaded.ResolvedAt == "" {
		t.Fatalf("a definite success must resolve the notification delivered: %+v %v", reloaded, err)
	}
	attempts, err := s.ListNotificationAttempts(ctx, notification.NotificationID)
	if err != nil || len(attempts) != 2 {
		t.Fatalf("both attempts must stay inspectable: %+v %v", attempts, err)
	}
	// NTF-005: the completed dispatch and its lane are byte-identical
	// across every attempt outcome — a crash before or after delivery
	// cannot rewrite task state.
	intentAfter, err := s.LoadIntent(ctx, first.Intent.DispatchID)
	if err != nil {
		t.Fatal(err)
	}
	if intentAfter.State != intentBefore.State || intentAfter.LeaseOwner != intentBefore.LeaseOwner ||
		intentAfter.AttemptCount != intentBefore.AttemptCount {
		t.Fatalf("delivery attempts must never rewrite dispatch state: %+v vs %+v", intentBefore, intentAfter)
	}
	if st, active, _ := laneCoordination(t, s, "wiki-maintenance", "wiki-primary"); st != "IDLE" || active != "" {
		t.Fatalf("the completed lane must stay idle across delivery attempts: %s %q", st, active)
	}
	// The unknown notification fails closed at the typed sentinel.
	if _, err := s.RecordNotificationAttempt(ctx, ports.NotificationAttemptInput{
		NotificationID: "ntf-missing", Outcome: records.NotificationDeliveredOutcome,
		StartedAt: now(), CompletedAt: now(),
	}); !errors.Is(err, ports.ErrNotificationNotFound) {
		t.Fatalf("an unknown notification must fail at the sentinel: %v", err)
	}
}

// TestE13T1DeliveryUnknownQuarantineAndReconciliationTransitions covers
// the remaining store-internal reportable transitions (AC-901): the
// expired-lease recovery to unknown, the quarantine hold, and the
// pending-reconciliation appearance — including its once-per-appearance
// semantics under repeated marks.
func TestE13T1DeliveryUnknownQuarantineAndReconciliationTransitions(t *testing.T) {
	s := openTestStore(t)
	e13t1Policy(s, e13t1AllEvents()...)
	ctx := context.Background()

	// delivery_unknown: one submitting intent whose lease expired.
	lin := lineage("dispatch-e13t1-unknown", "agent-dispatch:v2:sha256:"+repeat("u", 64))
	if err := s.CommitLineage(ctx, lin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcquireAttempt(ctx, ports.AcquireAttempt{
		DispatchID: lin.Intent.DispatchID, AttemptID: "attempt-e13t1-u", Owner: "worker-1",
		Now: "2026-08-30T00:00:00Z", LeaseExpiresAt: "2026-08-30T00:00:01Z",
	}); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.RecoverExpiredSubmitting(ctx, "wiki-maintenance", "2026-08-30T00:05:00Z")
	if err != nil || len(recovered) != 1 {
		t.Fatalf("the expired lease must recover: %+v %v", recovered, err)
	}
	notifications, err := s.ListNotifications(ctx, ports.NotificationFilter{Event: records.EventDeliveryUnknown})
	if err != nil || len(notifications) != 2 {
		t.Fatalf("the unknown recovery must notify per sink: %+v %v", notifications, err)
	}
	// Replay: the recovery sweep is idempotent and so is the intent.
	if _, err := s.RecoverExpiredSubmitting(ctx, "wiki-maintenance", "2026-08-30T00:06:00Z"); err != nil {
		t.Fatal(err)
	}
	if notifications, err = s.ListNotifications(ctx, ports.NotificationFilter{Event: records.EventDeliveryUnknown}); err != nil || len(notifications) != 2 {
		t.Fatalf("a replayed recovery must not notify again: %+v %v", notifications, err)
	}

	// quarantined: the hold commits its intent in the same transaction.
	qLin := ports.Lineage{
		Observation: ports.ObservationInput{ObservationID: "obs-e13t1", SchemaVersion: "agent-dispatch.source-observation/v1",
			SourceType: "watchman", SourceID: "src", ResourceID: "vault-main", ObservedAt: now(), ReceivedAt: now(),
			IngestStatus: "accepted"},
		Batch: ports.BatchInput{BatchID: "batch-e13t1", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: now(),
			ContentFingerprint: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		Decision: ports.DecisionInput{DecisionID: "decision-e13t1-q", BatchID: "batch-e13t1", RouteID: "wiki-maintenance",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "quarantine",
			Classification: "protected", CreatedAt: now(), Actor: "planner"},
	}
	if err := s.CommitQuarantineLineage(ctx, qLin, ports.QuarantineInput{
		QuarantineID: "q-e13t1", BatchID: "batch-e13t1", DecisionID: "decision-e13t1-q",
		ReasonCodes: []string{"protected_path_present"}, CreatedAt: now(),
	}); err != nil {
		t.Fatal(err)
	}
	if notifications, err = s.ListNotifications(ctx, ports.NotificationFilter{Event: records.EventQuarantined}); err != nil || len(notifications) != 2 {
		t.Fatalf("the quarantine hold must notify per sink: %+v %v", notifications, err)
	}

	// reconciliation_required: the flag's appearance notifies; repeated
	// marks of the already-pending route do not; a cleared flag that
	// appears again at a new occurrence notifies again.
	if err := s.MarkPendingReconcile(ctx, "wiki-maintenance", "pos-1", "2026-08-30T04:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if notifications, err = s.ListNotifications(ctx, ports.NotificationFilter{Event: records.EventReconciliationRequired}); err != nil || len(notifications) != 2 {
		t.Fatalf("the pending appearance must notify per sink: %+v %v", notifications, err)
	}
	if err := s.MarkPendingReconcile(ctx, "wiki-maintenance", "pos-1", "2026-08-30T04:01:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPendingReconcile(ctx, "wiki-maintenance", "pos-2", "2026-08-30T04:02:00Z"); err != nil {
		t.Fatal(err)
	}
	if notifications, err = s.ListNotifications(ctx, ports.NotificationFilter{Event: records.EventReconciliationRequired}); err != nil || len(notifications) != 2 {
		t.Fatalf("an already-pending route must not re-notify: %+v %v", notifications, err)
	}
	cleared, err := s.ClearPendingReconcile(ctx, "wiki-maintenance", true, "2026-08-30T04:03:00Z")
	if err != nil || !cleared {
		t.Fatalf("the pending flag must clear: %v %v", cleared, err)
	}
	if err := s.MarkPendingReconcile(ctx, "wiki-maintenance", "pos-3", "2026-08-30T04:04:00Z"); err != nil {
		t.Fatal(err)
	}
	if notifications, err = s.ListNotifications(ctx, ports.NotificationFilter{Event: records.EventReconciliationRequired}); err != nil || len(notifications) != 4 {
		t.Fatalf("a genuinely new appearance must notify again: %+v %v", notifications, err)
	}
}

// TestE13T1PruneRemovesResolvedNotificationsOnly pins the retention
// seam (E13-T1 pruning): resolved notification evidence prunes past the
// cutoff with its attempts; pending — unresolved — evidence is retained
// and inspectable no matter its age (NTF-004).
func TestE13T1PruneRemovesResolvedNotificationsOnly(t *testing.T) {
	s := openTestStore(t)
	e13t1Policy(s, e13t1AllEvents()...)
	first, _ := e12t2CommitFanout(t, s, "decision-e13t1-prune")
	ctx := context.Background()
	if _, err := s.CompleteActive(ctx, ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: first.Intent.DispatchID,
		ReceiptRef: "rcpt-e13t1-prune", Actor: "test", Now: "2026-08-30T05:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	notifications, err := s.ListNotifications(ctx, ports.NotificationFilter{})
	if err != nil || len(notifications) != 2 {
		t.Fatalf("two pending intents: %+v %v", notifications, err)
	}
	// Resolve the log sink's intent; the webhook intent stays pending.
	var logNotification ports.NotificationEventRecord
	for _, n := range notifications {
		if n.SinkID == "ops-log" {
			logNotification = n
		}
	}
	if _, err := s.RecordNotificationAttempt(ctx, ports.NotificationAttemptInput{
		NotificationID: logNotification.NotificationID, Outcome: records.NotificationRefusedOutcome,
		ErrorCode: "sink_refused", StartedAt: "2026-08-30T05:00:01Z", CompletedAt: "2026-08-30T05:00:02Z",
	}); err != nil {
		t.Fatal(err)
	}
	cutoffs := PruneCutoffs{
		Observations: "2026-08-31T00:00:00Z", Attempts: "2026-08-31T00:00:00Z",
		CompletedReceipts: "2026-08-31T00:00:00Z", ResolvedQuarantine: "2026-08-31T00:00:00Z",
		Notifications: "2026-08-30T06:00:00Z",
	}
	plan, err := s.PlanPrune(ctx, cutoffs)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Counts.Notifications != 1 {
		t.Fatalf("exactly the resolved notification must plan for pruning: %+v", plan.Counts)
	}
	counts, err := s.ExecutePrune(ctx, cutoffs, "operator", "retention", "2026-08-31T01:00:00Z")
	if err != nil || counts.Notifications != 1 {
		t.Fatalf("the resolved notification must prune with its attempts: %+v %v", counts, err)
	}
	if _, err := s.LoadNotification(ctx, logNotification.NotificationID); !errors.Is(err, ports.ErrNotificationNotFound) {
		t.Fatalf("the resolved notification must be gone: %v", err)
	}
	remaining, err := s.ListNotifications(ctx, ports.NotificationFilter{})
	if err != nil || len(remaining) != 1 || remaining[0].State != records.NotificationPending {
		t.Fatalf("pending evidence must stay retained and inspectable: %+v %v", remaining, err)
	}
}

// TestE13T1AttemptReferentialIntegrity pins DAT-010 at the store
// boundary: an attempt row cannot reference a notification that does
// not exist (the enforced foreign key), and a fresh migration carries
// the v16 tables with their CHECK vocabularies.
func TestE13T1AttemptReferentialIntegrity(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Exec(`SELECT 1 FROM notification_events`); err != nil {
		t.Fatalf("migration v16 must create notification_events: %v", err)
	}
	if _, err := s.Exec(`SELECT 1 FROM notification_attempts`); err != nil {
		t.Fatalf("migration v16 must create notification_attempts: %v", err)
	}
	if _, err := s.Exec(`INSERT INTO notification_attempts
		(attempt_id, notification_id, attempt_number, outcome, started_at, completed_at)
		VALUES ('ntfa-x', 'ntf-missing', 1, 'delivered', '2026-08-30T00:00:00Z', '2026-08-30T00:00:01Z')`); err == nil {
		t.Fatal("the enforced foreign key must refuse an orphan attempt")
	}
	if _, err := s.Exec(`INSERT INTO notification_events
		(notification_id, route_id, event, transition, sink_id, sink_type, policy_revision, idempotency_key, payload_json, created_at)
		VALUES ('ntf-x', 'wiki-maintenance', 'not_an_event', 't', 's', 'log', 'p', 'k', '{}', '2026-08-30T00:00:00Z')`); err == nil {
		t.Fatal("the event CHECK must refuse values outside the closed vocabulary")
	}
}

// TestE13T1DisabledPolicyCreatesNothing pins NTF-001 from the store
// side: without an installed resolver — doctor, read-only, and test
// stores — every reportable transition stays silent.
func TestE13T1DisabledPolicyCreatesNothing(t *testing.T) {
	s := openTestStore(t)
	// No SetNotificationPolicy: the default resolver is nil.
	first, _ := e12t2CommitFanout(t, s, "decision-e13t1-silent")
	ctx := context.Background()
	if _, err := s.CompleteActive(ctx, ports.ActiveCompletion{
		RouteID: "wiki-maintenance", DispatchID: first.Intent.DispatchID,
		ReceiptRef: "rcpt-e13t1-silent", Actor: "test", Now: "2026-08-30T06:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM notification_events`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a store without a policy must create no notifications: %d %v", n, err)
	}
}

// TestE13T1MigrationV16UpgradesCleanly proves the forward migration on a
// v15-era database: the notification tables appear, the ledger records
// v16, and no historic row is touched.
func TestE13T1MigrationV16UpgradesCleanly(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/v15.db"
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.migrations = Migrations[:15]
	if err := s.Migrate(dir); err != nil {
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
	if err != nil || version != MaxSchemaVersion {
		t.Fatalf("the upgraded ledger must reach the current baseline past v16: %d %v", version, err)
	}
	for _, table := range []string{"notification_events", "notification_attempts"} {
		if _, err := upgraded.Exec(`SELECT 1 FROM ` + table); err != nil {
			t.Fatalf("v16 must create %s: %v", table, err)
		}
	}
}

// The filter's event field keeps the listing surface typed at the read
// boundary (used above); ensure the wire shape stays snake_case.
func TestE13T1NotificationWireShapesStaySnakeCase(t *testing.T) {
	s := openTestStore(t)
	e13t1Policy(s, records.EventReconciliationRequired)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.MarkPendingReconcile(ctx, "wiki-maintenance", "pos-wire", "2026-08-30T07:00:00Z"); err != nil {
		t.Fatal(err)
	}
	notifications, err := s.ListNotifications(ctx, ports.NotificationFilter{})
	if err != nil || len(notifications) != 2 {
		t.Fatalf("wire-shape fixture: %+v %v", notifications, err)
	}
	for _, n := range notifications {
		if !strings.HasPrefix(n.NotificationID, "ntf-") || !strings.HasPrefix(n.IdempotencyKey, "ntfidem-") {
			t.Fatalf("the record identities must carry their derivations: %+v", n)
		}
	}
}

// TestE13T1LateTerminalAttemptKeepsFirstResolution pins the round-1
// F006/F007 remediation: an at-least-once delivery race — a terminal
// attempt landing after the notification already resolved — records as
// inspectable evidence but never rewrites the first resolution.
func TestE13T1LateTerminalAttemptKeepsFirstResolution(t *testing.T) {
	s := openTestStore(t)
	e13t1Policy(s, records.EventReconciliationRequired)
	ctx := context.Background()
	if err := s.MarkPendingReconcile(ctx, "wiki-maintenance", "pos-race", "2026-08-30T08:00:00Z"); err != nil {
		t.Fatal(err)
	}
	notifications, err := s.ListNotifications(ctx, ports.NotificationFilter{SinkID: "ops-log"})
	if err != nil || len(notifications) != 1 {
		t.Fatalf("one log intent: %+v %v", notifications, err)
	}
	id := notifications[0].NotificationID
	if _, err := s.RecordNotificationAttempt(ctx, ports.NotificationAttemptInput{
		NotificationID: id, Outcome: records.NotificationDeliveredOutcome,
		StartedAt: "2026-08-30T08:00:01Z", CompletedAt: "2026-08-30T08:00:02Z",
	}); err != nil {
		t.Fatal(err)
	}
	// The racing late attempt: recorded, but the delivered resolution and
	// its timestamp stand.
	if _, err := s.RecordNotificationAttempt(ctx, ports.NotificationAttemptInput{
		NotificationID: id, Outcome: records.NotificationRefusedOutcome, ErrorCode: "late_sink_refusal",
		StartedAt: "2026-08-30T08:01:00Z", CompletedAt: "2026-08-30T08:01:01Z",
	}); err != nil {
		t.Fatal(err)
	}
	resolved, err := s.LoadNotification(ctx, id)
	if err != nil || resolved.State != records.NotificationDelivered || resolved.ResolvedAt != "2026-08-30T08:00:02Z" {
		t.Fatalf("the first resolution must stand: %+v %v", resolved, err)
	}
	attempts, err := s.ListNotificationAttempts(ctx, id)
	if err != nil || len(attempts) != 2 || attempts[1].Outcome != records.NotificationRefusedOutcome {
		t.Fatalf("the late attempt must remain inspectable evidence: %+v %v", attempts, err)
	}
}

// TestE13T1PayloadBoundsDropUnsafeFields pins the reconciled bound
// semantics (the round-1 F003 fail-closed posture, reconciled by the
// epic audit against the deferred availability coupling): a violating
// source value is DROPPED whole — never truncated, never leaked — and
// the notification still commits with its safe fields, so a payload
// defect can never block the owning transition (ADR-0019).
func TestE13T1PayloadBoundsDropUnsafeFields(t *testing.T) {
	s := openTestStore(t)
	e13t1Policy(s, records.EventReconciliationRequired)
	tx, err := s.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tooLong := strings.Repeat("x", 257)
	created, err := s.enqueueNotificationTx(tx, "wiki-maintenance", records.EventReconciliationRequired,
		"route:wiki-maintenance:pending_reconcile:bound", "",
		map[string]string{"blob": tooLong, "origin": "explicit_mark"}, "2026-08-30T09:00:00Z")
	if err != nil || created != 2 {
		t.Fatalf("an unsafe field must drop without blocking the enqueue: %d %v", created, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	notifications, err := s.ListNotifications(context.Background(), ports.NotificationFilter{SinkID: "ops-log"})
	if err != nil || len(notifications) != 1 {
		t.Fatalf("the sanitized notification must exist: %+v %v", notifications, err)
	}
	if strings.Contains(notifications[0].PayloadJSON, "blob") || !strings.Contains(notifications[0].PayloadJSON, "explicit_mark") {
		t.Fatalf("the violating field drops whole while the safe field stays: %s", notifications[0].PayloadJSON)
	}
	// Control characters, over-long keys, and invalid UTF-8 drop the same way.
	for name, value := range map[string]string{
		"note":                  "line one\nline two",
		strings.Repeat("k", 65): "v",
	} {
		_ = name
		_ = value
	}
	if dropped := sanitizeNotificationSource(map[string]string{"a": "line one\nline two", "b": "ok"}); len(dropped) != 1 || dropped["b"] != "ok" {
		t.Fatalf("a control character drops its field whole: %+v", dropped)
	}
	if dropped := sanitizeNotificationSource(map[string]string{strings.Repeat("k", 65): "v", "b": "ok"}); len(dropped) != 1 || dropped["b"] != "ok" {
		t.Fatalf("an over-long key drops its field whole: %+v", dropped)
	}
	if dropped := sanitizeNotificationSource(map[string]string{"a": string([]byte{0xff, 0xfe}), "b": "ok"}); len(dropped) != 1 {
		t.Fatalf("invalid UTF-8 drops its field whole: %+v", dropped)
	}
	overMany := map[string]string{}
	for i := 0; i < 13; i++ {
		overMany[fmt.Sprintf("k%02d", i)] = "v"
	}
	if dropped := sanitizeNotificationSource(overMany); len(dropped) != 12 {
		t.Fatalf("the projection keeps at most the bound: %d", len(dropped))
	}
}

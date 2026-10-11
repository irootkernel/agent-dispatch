package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

var managementT0 = time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)

func managementBinding(action string) syncmanagement.Binding {
	d := "sha256:" + strings.Repeat("a", 64)
	b := syncmanagement.Binding{GroupID: "wiki-pair", InstanceID: "node-a", StateIncarnationID: "epoch-a", Action: action, PolicyRevision: d, ConfigRevision: d}
	if syncmanagement.IsControl(action) {
		revision := int64(1)
		b.ExpectedControlRevision = &revision
	}
	return b
}

func managementHandle(n int) string { return fmt.Sprintf("mgr_%032x", n) }

func prepareManagement(t *testing.T, s *Store, n int, b syncmanagement.Binding) ManagementRequest {
	t.Helper()
	r, reused, err := s.PrepareManagementRequest(context.Background(), managementHandle(n), b, managementT0)
	if err != nil || reused {
		t.Fatalf("prepare: reused=%v %v", reused, err)
	}
	return r
}

func acceptManagement(t *testing.T, s *Store, r ManagementRequest, queue int) ManagementRequest {
	t.Helper()
	out, reused, err := s.AcceptManagementRequest(context.Background(), ManagementAdmission{GroupID: r.GroupID, RequestID: r.RequestID, CurrentBinding: r.Binding, Authorized: true, QueueLimit: queue, ControlSyncRevision: "cfg-1", Clock: func() time.Time { return managementT0 }})
	if err != nil || reused {
		t.Fatalf("accept: reused=%v %v", reused, err)
	}
	return out
}

func claimManagement(t *testing.T, s *Store, r ManagementRequest) ManagementRequest {
	t.Helper()
	out, err := s.ClaimManagementRequest(context.Background(), r.GroupID, r.RequestID, "worker-a", r.Binding, true, managementT0)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func finishManagement(t *testing.T, s *Store, r ManagementRequest) ManagementRequest {
	t.Helper()
	out, err := s.TransitionManagementRequest(context.Background(), r, "finished", &syncmanagement.Outcome{Status: "no_op", Reason: "inspected no pending obligations"}, "inspection-settled", managementT0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Legacy rewind fixtures must remove later additive management tables before
// recreating their historical schema. This is harness-only, never a downgrade.
func dropManagementSchemaForHarness(t *testing.T, s *Store) {
	t.Helper()
	for _, stmt := range []string{"DROP TABLE sync_management_executor", "DELETE FROM schema_migrations WHERE version=29", "DROP TABLE sync_management_jobs", "DROP TABLE sync_management_executions", "DROP TABLE sync_management_acceptances", "DROP TABLE sync_management_requests", "DELETE FROM schema_migrations WHERE version=28"} {
		if _, err := s.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestE23T2DraftIsInertImmutableAndLookupExpiryDoesNotWrite(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	r := prepareManagement(t, s, 1, managementBinding("sync-now"))
	for _, table := range []string{"sync_controls", "sync_jobs", "sync_management_acceptances", "sync_management_executions"} {
		var n int
		if err := s.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("draft changed %s: %d %v", table, n, err)
		}
	}
	for _, offset := range []time.Duration{599 * time.Second, 600 * time.Second, 48 * time.Hour} {
		view, err := s.LoadManagementRequest(ctx, r.GroupID, r.RequestID, managementT0.Add(offset))
		if err != nil {
			t.Fatal(err)
		}
		want := "prepared"
		if offset >= 600*time.Second {
			want = "expired"
		}
		if view.State != want {
			t.Fatalf("expiry at %s: %s", offset, view.State)
		}
	}
	var state string
	var revision int64
	if err := s.QueryRow(`SELECT state,revision FROM sync_management_requests WHERE request_id=?`, r.RequestID).Scan(&state, &revision); err != nil || state != "prepared" || revision != 1 {
		t.Fatalf("lookup wrote state %s/%d: %v", state, revision, err)
	}
	b := r.Binding
	b.Action = "verify-all"
	if _, _, err := s.PrepareManagementRequest(ctx, r.RequestID, b, managementT0); !errors.Is(err, ErrSyncAdmissionConflict) {
		t.Fatalf("changed fingerprint: %v", err)
	}
	if _, err := s.Exec(`UPDATE sync_management_requests SET config_revision='changed' WHERE request_id=?`, r.RequestID); err == nil {
		t.Fatal("binding rewrite admitted")
	}
	if _, _, err := s.AcceptManagementRequest(ctx, ManagementAdmission{GroupID: r.GroupID, RequestID: r.RequestID, CurrentBinding: r.Binding, Authorized: true, QueueLimit: 2, Clock: func() time.Time { return managementT0.Add(600 * time.Second) }}); !errors.Is(err, ErrManagementExpired) {
		t.Fatalf("expired submit: %v", err)
	}
	if _, _, err := s.AcceptManagementRequest(ctx, ManagementAdmission{GroupID: r.GroupID, RequestID: managementHandle(2), Clock: func() time.Time { return managementT0 }}); !errors.Is(err, ErrManagementNotFound) {
		t.Fatalf("missing handle created: %v", err)
	}
}

func TestE23T2ConcurrentPreparationAndAcceptanceAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	const workers = 8
	stores := make([]*Store, workers)
	for i := range stores {
		stores[i], err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer stores[i].Close()
	}
	b := managementBinding("sync-now")
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for _, store := range stores {
		wg.Go(func() {
			_, _, err := store.PrepareManagementRequest(context.Background(), managementHandle(1), b, managementT0)
			errs <- err
		})
	}
	wg.Wait()
	for range workers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for _, store := range stores {
		wg.Go(func() {
			_, _, err := store.AcceptManagementRequest(context.Background(), ManagementAdmission{GroupID: b.GroupID, RequestID: managementHandle(1), CurrentBinding: b, Authorized: true, QueueLimit: 2, Clock: func() time.Time { return managementT0 }})
			errs <- err
		})
	}
	wg.Wait()
	for range workers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"sync_management_requests", "sync_management_acceptances"} {
		var n int
		if err := s.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("duplicate %s: %d %v", table, n, err)
		}
	}
	view, reused, err := s.AcceptManagementRequest(context.Background(), ManagementAdmission{GroupID: b.GroupID, RequestID: managementHandle(1), Authorized: false, QueueLimit: 0, Clock: func() time.Time { return managementT0.Add(time.Hour) }})
	if err != nil || !reused || view.State != "queued" {
		t.Fatalf("duplicate before revoked policy/capacity: %+v %v %v", view, reused, err)
	}
	wrong := b
	wrong.StateIncarnationID = "epoch-restored"
	draft := prepareManagement(t, s, 2, b)
	if _, _, err := s.AcceptManagementRequest(context.Background(), ManagementAdmission{GroupID: b.GroupID, RequestID: draft.RequestID, CurrentBinding: wrong, Authorized: true, QueueLimit: 2, Clock: func() time.Time { return managementT0 }}); !errors.Is(err, ErrManagementUnauthorized) {
		t.Fatalf("restamped incarnation: %v", err)
	}
}

func TestE23T2QueueReserveAndAtomicControlRollback(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 2; n++ {
		acceptManagement(t, s, prepareManagement(t, s, n, managementBinding("sync-now")), 2)
	}
	third := prepareManagement(t, s, 3, managementBinding("sync-now"))
	if _, _, err := s.AcceptManagementRequest(ctx, ManagementAdmission{GroupID: third.GroupID, RequestID: third.RequestID, CurrentBinding: third.Binding, Authorized: true, QueueLimit: 2, Clock: func() time.Time { return managementT0 }}); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("data saturation: %v", err)
	}
	pause := acceptManagement(t, s, prepareManagement(t, s, 4, managementBinding("pause")), 2)
	if pause.State != "finished" || pause.Outcome.Status != "intent_committed" || *pause.Outcome.Quiescence != "unknown" {
		t.Fatalf("control reserve %+v", pause)
	}
	control, err := s.LoadSyncControl(ctx, "wiki-pair")
	if err != nil || control.State != "paused" || control.Revision != 2 {
		t.Fatalf("control result %+v %v", control, err)
	}
	resumeBinding := managementBinding("resume")
	resumeBinding.ExpectedControlRevision = &control.Revision
	resume := prepareManagement(t, s, 5, resumeBinding)
	if _, err := s.Exec(`CREATE TRIGGER fail_management_acceptance BEFORE INSERT ON sync_management_acceptances BEGIN SELECT RAISE(ABORT,'injected admission failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AcceptManagementRequest(ctx, ManagementAdmission{GroupID: resume.GroupID, RequestID: resume.RequestID, CurrentBinding: resume.Binding, Authorized: true, QueueLimit: 2, ControlSyncRevision: "cfg-2", Clock: func() time.Time { return managementT0 }}); err == nil {
		t.Fatal("injected acceptance failure ignored")
	}
	control, err = s.LoadSyncControl(ctx, "wiki-pair")
	if err != nil || control.State != "paused" || control.Revision != 2 || control.ConfigRevision != "cfg-1" {
		t.Fatalf("partial control commit %+v %v", control, err)
	}
	view, err := s.LoadManagementRequest(ctx, resume.GroupID, resume.RequestID, managementT0)
	if err != nil || view.AcceptedAt != nil || view.State != "prepared" {
		t.Fatalf("partial acceptance %+v %v", view, err)
	}
	if _, err := s.Exec(`DROP TRIGGER fail_management_acceptance`); err != nil {
		t.Fatal(err)
	}
	acceptManagement(t, s, resume, 2)
	stale := prepareManagement(t, s, 6, resumeBinding)
	if _, _, err := s.AcceptManagementRequest(ctx, ManagementAdmission{GroupID: stale.GroupID, RequestID: stale.RequestID, CurrentBinding: stale.Binding, Authorized: true, QueueLimit: 2, ControlSyncRevision: "cfg-1", Clock: func() time.Time { return managementT0 }}); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("stale control revision %v", err)
	}
	control, err = s.HoldSyncControl(ctx, "wiki-pair", "trust_failure", "cfg-1", syncT1)
	if err != nil {
		t.Fatal(err)
	}
	held := managementBinding("resume")
	held.ExpectedControlRevision = &control.Revision
	r := prepareManagement(t, s, 7, held)
	if _, _, err := s.AcceptManagementRequest(ctx, ManagementAdmission{GroupID: r.GroupID, RequestID: r.RequestID, CurrentBinding: r.Binding, Authorized: true, QueueLimit: 2, ControlSyncRevision: "cfg-1", Clock: func() time.Time { return managementT0 }}); !errors.Is(err, ErrSyncControlHeld) {
		t.Fatalf("resume cleared safety hold %v", err)
	}
}

func TestE23T2FencesDeadlineAndExplicitRecovery(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	r := claimManagement(t, s, acceptManagement(t, s, prepareManagement(t, s, 1, managementBinding("sync-now")), 2))
	renewed, err := s.RenewManagementClaim(ctx, r, managementT0.Add(10*time.Second))
	if err != nil || renewed.Fence != 1 || renewed.Attempts != 1 || renewed.ExecutionDeadline != r.ExecutionDeadline {
		t.Fatalf("renewal %+v %v", renewed, err)
	}
	if _, err := s.RecoverManagementClaim(ctx, renewed, "queued", nil, "inspection-live", managementT0.Add(11*time.Second)); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("live claim recovered: %v", err)
	}
	if _, err := s.TransitionManagementRequest(ctx, r, "finished", &syncmanagement.Outcome{Status: "no_op"}, "stale-writer", managementT0.Add(11*time.Second)); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("stale revision wrote: %v", err)
	}
	if _, err := s.ClaimManagementRequest(ctx, r.GroupID, r.RequestID, "worker-b", r.Binding, true, managementT0.Add(time.Minute)); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("expiry stole claim %v", err)
	}
	if _, err := s.RecoverManagementClaim(ctx, renewed, "queued", nil, "", managementT0.Add(time.Minute)); err == nil {
		t.Fatal("recovery without inspected basis")
	}
	uncertain := &syncmanagement.Outcome{Status: "uncertain", Reason: "durable effect inspection remains inconclusive"}
	u, err := s.RecoverManagementClaim(ctx, renewed, "uncertain", uncertain, "journal-effect-unknown", managementT0.Add(time.Minute))
	if err != nil || u.State != "uncertain" {
		t.Fatalf("recover %+v %v", u, err)
	}
	if _, err := s.ClaimManagementRequest(ctx, u.GroupID, u.RequestID, "worker-b", u.Binding, true, managementT0.Add(time.Minute)); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("uncertain repeated effect %v", err)
	}
	queued, err := s.TransitionManagementRequest(ctx, u, "queued", nil, "journal-effect-not-started", managementT0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ClaimManagementRequest(ctx, queued.GroupID, queued.RequestID, "worker-b", queued.Binding, true, managementT0.Add(time.Minute))
	if err != nil || second.Fence != 2 || second.Attempts != 2 {
		t.Fatalf("fenced reentry %+v %v", second, err)
	}
	if _, err := s.TransitionManagementRequest(ctx, renewed, "finished", &syncmanagement.Outcome{Status: "no_op"}, "late-worker", managementT0.Add(time.Minute)); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("old owner/fence wrote %v", err)
	}
	for offset := time.Minute + 10*time.Second; offset < 5*time.Minute; offset += 10 * time.Second {
		second, err = s.RenewManagementClaim(ctx, second, managementT0.Add(offset))
		if err != nil {
			t.Fatalf("renew at %s: %v", offset, err)
		}
	}
	if _, err := s.RenewManagementClaim(ctx, second, managementT0.Add(5*time.Minute)); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("execution deadline extended %v", err)
	}
	var count int
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_management_executions WHERE request_id=?`, r.RequestID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("execution records %d %v", count, err)
	}
}

func TestE23T2DraftCapacityAndAttemptCeilings(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for n := 1; n <= 256; n++ {
		prepareManagement(t, s, n, managementBinding("sync-now"))
	}
	if _, _, err := s.PrepareManagementRequest(ctx, managementHandle(257), managementBinding("sync-now"), managementT0.Add(time.Hour)); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("retained expired drafts exceeded capacity: %v", err)
	}
	for n := 1; n <= 16; n++ {
		prepareManagement(t, s, 300+n, managementBinding("pause"))
	}
	if _, _, err := s.PrepareManagementRequest(ctx, managementHandle(317), managementBinding("pause"), managementT0); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("control draft bound: %v", err)
	}
	if n, err := s.PruneManagementRequests(ctx, managementT0.Add(24*time.Hour+10*time.Minute), 0); err != nil || n != 0 {
		t.Fatalf("draft retention boundary n=%d %v", n, err)
	}
	if n, err := s.PruneManagementRequests(ctx, managementT0.Add(25*time.Hour), 0); err != nil || n != 256 {
		t.Fatalf("expired draft prune n=%d %v", n, err)
	}
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	r := acceptManagement(t, s, prepareManagement(t, s, 500, managementBinding("sync-now")), 2)
	for n := 0; n < 20; n++ {
		at := managementT0.Add(time.Duration(n) * time.Second)
		var err error
		r, err = s.ClaimManagementRequest(ctx, r.GroupID, r.RequestID, "worker-a", r.Binding, true, at)
		if err != nil {
			t.Fatal(err)
		}
		r, err = s.TransitionManagementRequest(ctx, r, "queued", nil, "inspection-no-effect", at.Add(time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ClaimManagementRequest(ctx, r.GroupID, r.RequestID, "worker-a", r.Binding, true, managementT0.Add(21*time.Second)); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("attempt budget reset: %v", err)
	}
	view, err := s.LoadManagementRequest(ctx, r.GroupID, r.RequestID, managementT0.Add(time.Hour))
	if err != nil || view.State != "queued" || view.Attempts != 20 {
		t.Fatalf("accepted obligation dropped %+v %v", view, err)
	}
}

func TestE23T2AssociationReservationImmutableTargetsAndRetention(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	r := claimManagement(t, s, acceptManagement(t, s, prepareManagement(t, s, 1, managementBinding("sync-now")), 2))
	importRecord := validE21T4Import(t, false)
	importRaw, _ := json.Marshal(importRecord)
	job, _, err := s.AdmitSyncJob(ctx, SyncJobInput{JobID: importRecord.ImportID, GroupID: importRecord.GroupID, Kind: "import", LogicalKey: importRecord.PlanID, InitialState: "validated", PayloadJSON: string(importRaw), ConfigRevision: "cfg-1", QueueLimit: 1000, Now: syncT0})
	if err != nil {
		t.Fatal(err)
	}
	assoc := ManagementJob{JobID: job.JobID, Kind: job.Kind, AssociationReason: "observed_reconciliation"}
	target := strings.Repeat("b", 40)
	assoc.TargetCommit = &target
	if _, err := s.AssociateManagementJob(ctx, r, assoc, managementT0); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("effect association without reservation: %v", err)
	}
	r, err = s.ReserveManagementAssociations(ctx, r, 1000, managementT0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveManagementAssociations(ctx, r, 1, managementT0); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("1001st reserved association: %v", err)
	}
	r, err = s.AssociateManagementJob(ctx, r, assoc, managementT0)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.AssociateManagementJob(ctx, r, assoc, managementT0)
	if err != nil || duplicate.Revision != r.Revision || duplicate.AssociationReserved != 999 {
		t.Fatalf("association replay %+v %v", duplicate, err)
	}
	changed := assoc
	other := strings.Repeat("c", 40)
	changed.TargetCommit = &other
	if _, err := s.AssociateManagementJob(ctx, r, changed, managementT0); !errors.Is(err, ErrSyncAdmissionConflict) {
		t.Fatalf("association target changed %v", err)
	}
	if _, err := s.Exec(`UPDATE sync_management_jobs SET target_commit=? WHERE request_id=?`, other, r.RequestID); err == nil {
		t.Fatal("immutable association changed")
	}
	r = finishManagement(t, s, r)
	retry := managementBinding("retry")
	retry.TargetRequestID = &r.RequestID
	child := acceptManagement(t, s, prepareManagement(t, s, 2, retry), 2)
	if n, err := s.PruneManagementRequests(ctx, managementT0.Add(400*24*time.Hour), 0); err != nil || n != 0 {
		t.Fatalf("unresolved job/retry pruned parent: %d %v", n, err)
	}
	if _, err := s.Exec(`UPDATE sync_jobs SET state='applied',retain_until_resolved=0,resolved_at=? WHERE job_id=?`, syncT0, job.JobID); err != nil {
		t.Fatal(err)
	}
	cut := PruneCutoffs{CompletedReceipts: "2028-01-01T00:00:00Z"}
	plan, err := s.PlanPrune(ctx, cut)
	if err != nil || plan.Counts.SyncJobs != 0 {
		t.Fatalf("associated job prune plan %+v %v", plan, err)
	}
	counts, err := s.ExecutePrune(ctx, cut, "test", "retention-boundary", syncT3)
	if err != nil || counts.SyncJobs != 0 {
		t.Fatalf("associated job deleted %+v %v", counts, err)
	}
	finishManagement(t, s, claimManagement(t, s, child))
	if n, err := s.PruneManagementRequests(ctx, managementT0.Add(181*24*time.Hour), 400*24*time.Hour); err != nil || n != 0 {
		t.Fatalf("longer configured horizon ignored: %d %v", n, err)
	}
	if n, err := s.PruneManagementRequests(ctx, managementT0.Add(181*24*time.Hour), 0); err != nil || n != 1 {
		t.Fatalf("terminal retry leaf prune: %d %v", n, err)
	}
	if n, err := s.PruneManagementRequests(ctx, managementT0.Add(181*24*time.Hour), 0); err != nil || n != 1 {
		t.Fatalf("resolved parent prune: %d %v", n, err)
	}
	if _, err := s.LoadManagementRequest(ctx, r.GroupID, r.RequestID, managementT0.Add(181*24*time.Hour)); !errors.Is(err, ErrManagementNotFound) {
		t.Fatalf("retired identity reappeared: %v", err)
	}
	counts, err = s.ExecutePrune(ctx, cut, "test", "released-management-reference", syncT3)
	if err != nil || counts.SyncJobs != 1 {
		t.Fatalf("resolved unreferenced job prune %+v %v", counts, err)
	}
}

func TestE23T2TerminalSlotReservationAndControlCapacity(t *testing.T) {
	for _, action := range []string{"sync-now", "pause"} {
		t.Run(action, func(t *testing.T) {
			s := openTestStore(t)
			ctx := context.Background()
			if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
				t.Fatal(err)
			}
			r := acceptManagement(t, s, prepareManagement(t, s, 1, managementBinding(action)), 2)
			if action == "sync-now" {
				r = finishManagement(t, s, claimManagement(t, s, r))
			}
			limit := 10000
			if action == "pause" {
				limit = 1000
			}
			// Bulk valid terminal rows exercise the real admission count at the
			// frozen large boundary without thousands of redundant claim tests.
			_, err := s.Exec(`WITH RECURSIVE n(x) AS (SELECT 2 UNION ALL SELECT x+1 FROM n WHERE x<?)
		 INSERT INTO sync_management_requests(request_id,group_id,instance_id,state_incarnation_id,action,target_request_id,policy_revision,config_revision,expected_control_revision,fingerprint,state,created_at,expires_at,accepted_at,resolved_at,outcome_json)
		 SELECT printf('mgr_%032x',x),group_id,instance_id,state_incarnation_id,action,target_request_id,policy_revision,config_revision,expected_control_revision,fingerprint,state,created_at,expires_at,accepted_at,resolved_at,outcome_json FROM n CROSS JOIN sync_management_requests WHERE request_id=?`, limit-1, r.RequestID)
			if err != nil {
				t.Fatal(err)
			}
			b := managementBinding(action)
			if action == "pause" {
				revision := int64(2)
				b.ExpectedControlRevision = &revision
			}
			draft := prepareManagement(t, s, 20000, b)
			accepted := acceptManagement(t, s, draft, 2)
			if action == "sync-now" && accepted.State != "queued" {
				t.Fatal("terminal reservation did not retain accepted work")
			}
			next := prepareManagement(t, s, 20001, b)
			if _, _, err := s.AcceptManagementRequest(ctx, ManagementAdmission{GroupID: next.GroupID, RequestID: next.RequestID, CurrentBinding: next.Binding, Authorized: true, QueueLimit: 2, ControlSyncRevision: "cfg-1", Clock: func() time.Time { return managementT0 }}); !errors.Is(err, ErrSyncQueueFull) {
				t.Fatalf("terminal reserve overfilled: %v", err)
			}
			view, err := s.LoadManagementRequest(ctx, next.GroupID, next.RequestID, managementT0)
			if err != nil || view.AcceptedAt != nil {
				t.Fatalf("saturated admission persisted receipt %+v %v", view, err)
			}
		})
	}
}

func TestE23T2UpgradePreservesJobsAndOldBinaryRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.migrations = Migrations[:27]
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	before, _, err := s.AdmitSyncJob(ctx, syncJobFixture("historical-job", "historical-key", `{"immutable":"payload"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	upgradeBackups := filepath.Join(t.TempDir(), "upgrade-backups")
	if err := s.Migrate(upgradeBackups); err != nil {
		t.Fatal(err)
	}
	after, err := loadSyncJob(ctx, s.DB, before.JobID)
	if err != nil || after != before {
		t.Fatalf("upgrade rewrote job before=%+v after=%+v %v", before, after, err)
	}
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_management_requests`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("invented historical lineage: %d %v", n, err)
	}
	if _, reused, err := s.AdmitSyncJob(ctx, syncJobFixture("ignored-duplicate-id", "historical-key", `{"immutable":"payload"}`)); err != nil || !reused {
		t.Fatalf("ordinary sync changed after additive migration %v %v", reused, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	old.migrations = Migrations[:27]
	var tooNew *ErrSchemaTooNewType
	if err := old.Migrate(t.TempDir()); !errors.As(err, &tooNew) {
		t.Fatalf("old schema range did not fail closed: %v", err)
	}
	var version int
	if err := old.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 29 {
		t.Fatalf("old reader downgraded upgraded state: %d %v", version, err)
	}
	backups, err := filepath.Glob(filepath.Join(upgradeBackups, "agent-dispatch-v27-to-v29-*.backup"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("compatible pre-upgrade backup: %v %v", backups, err)
	}
	raw, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	restoredPath := filepath.Join(t.TempDir(), "compatible-restored.db")
	if err := os.WriteFile(restoredPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restored.migrations = Migrations[:27]
	if err := restored.Migrate(t.TempDir()); err != nil {
		t.Fatalf("compatible rollback refused: %v", err)
	}
	restoredJob, err := loadSyncJob(ctx, restored.DB, before.JobID)
	if err != nil || restoredJob != before {
		t.Fatalf("rollback rewrote historical job: %+v %v", restoredJob, err)
	}
	if err := restored.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 27 {
		t.Fatalf("restored compatible schema: %d %v", version, err)
	}
}

func TestE23T2CapacityRaceAndCanceledAdmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	const workers = 8
	stores := make([]*Store, workers)
	b := managementBinding("sync-now")
	for i := range stores {
		prepareManagement(t, s, i+1, b)
		stores[i], err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer stores[i].Close()
	}
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i, store := range stores {
		wg.Go(func() {
			_, _, err := store.AcceptManagementRequest(context.Background(), ManagementAdmission{GroupID: b.GroupID, RequestID: managementHandle(i + 1), CurrentBinding: b, Authorized: true, QueueLimit: 2, Clock: func() time.Time { return managementT0 }})
			errs <- err
		})
	}
	wg.Wait()
	accepted := 0
	for range workers {
		err := <-errs
		if err == nil {
			accepted++
		} else if !errors.Is(err, ErrSyncQueueFull) {
			t.Fatal(err)
		}
	}
	if accepted != 2 {
		t.Fatalf("capacity race accepted %d requests", accepted)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.PrepareManagementRequest(ctx, managementHandle(100), b, managementT0); err == nil {
		t.Fatal("canceled preparation committed")
	}
	if _, err := s.LoadManagementRequest(context.Background(), b.GroupID, managementHandle(100), managementT0); !errors.Is(err, ErrManagementNotFound) {
		t.Fatalf("canceled draft survived %v", err)
	}
}

func TestE23T2OneThousandExactAssociationsRemainBounded(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	r := claimManagement(t, s, acceptManagement(t, s, prepareManagement(t, s, 1, managementBinding("sync-now")), 2))
	_, err := s.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1000)
	 INSERT INTO sync_jobs(job_id,group_id,kind,logical_key,request_fingerprint,state,payload_json,created_at,updated_at)
	 SELECT printf('association-job-%04d',x),'wiki-pair','import',printf('association-key-%04d',x),'fixture','requested','{}',?,? FROM n`, syncT0, syncT0)
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.ReserveManagementAssociations(ctx, r, 1000, managementT0)
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 1000; n++ {
		r, err = s.AssociateManagementJob(ctx, r, ManagementJob{JobID: fmt.Sprintf("association-job-%04d", n), Kind: "import", AssociationReason: "observed_reconciliation"}, managementT0)
		if err != nil {
			t.Fatalf("association %d: %v", n, err)
		}
	}
	view, err := s.LoadManagementRequest(ctx, r.GroupID, r.RequestID, managementT0)
	if err != nil || len(view.Jobs) != 1000 || view.AssociationReserved != 0 {
		t.Fatalf("bounded exact lookup jobs=%d reserve=%d %v", len(view.Jobs), view.AssociationReserved, err)
	}
	if _, err := s.ReserveManagementAssociations(ctx, r, 1, managementT0); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("association1001 admitted %v", err)
	}
	for n, j := range view.Jobs {
		if j.JobID != fmt.Sprintf("association-job-%04d", n+1) || j.Attempts != 0 || j.State != "requested" {
			t.Fatalf("lookup invented or truncated job evidence %+v", j)
		}
	}
}

func TestE23T2ManagementRetentionProtectsJournalAndAttributedObservation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	request := claimManagement(t, s, acceptManagement(t, s, prepareManagement(t, s, 1, managementBinding("sync-now")), 2))
	record := validE21T4Import(t, false)
	payload, err := syncrecords.CanonicalImport(record)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := s.AdmitSyncJob(ctx, SyncJobInput{JobID: "management-import", GroupID: "wiki-pair", Kind: "import", LogicalKey: record.ImportID, InitialState: "validated", PayloadJSON: string(payload), ConfigRevision: "cfg-1", QueueLimit: 10, Now: syncT0})
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.ClaimSyncJob(ctx, job.JobID, "import-worker", "cfg-1", syncT0, syncT3)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BeginImportApply(ctx, job.JobID, "import-worker", "vault-main", job.Fence, record.Paths, SyncJournalEntry{JournalID: "management-import-start", JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "started", EvidenceJSON: `{}`, RecordedAt: syncT0}, syncT0); err != nil {
		t.Fatal(err)
	}
	obs := ObservationRecord{ObservationID: "management-import-observation", SchemaVersion: "agent-dispatch.source-observation/v1", SourceType: "watchman", SourceID: "source", TriggerName: "trigger", ResourceID: "vault-main", ObservedAt: syncT0, ReceivedAt: syncT0, RawPayloadDigest: record.Paths[0].After, IngestStatus: "accepted", FlagsJSON: `{}`, ImportAttributions: []ImportAttributionRecord{{JobID: job.JobID, Fence: job.Fence, Path: record.Paths[0].Path, AfterValue: record.Paths[0].After}}}
	if err := s.SaveObservation(nil, obs); err != nil {
		t.Fatal(err)
	}
	request, err = s.ReserveManagementAssociations(ctx, request, 1, managementT0)
	if err != nil {
		t.Fatal(err)
	}
	request, err = s.AssociateManagementJob(ctx, request, ManagementJob{JobID: job.JobID, Kind: "import", AssociationReason: "observed_reconciliation"}, managementT0)
	if err != nil {
		t.Fatal(err)
	}
	finishManagement(t, s, request)
	if n, err := s.PruneManagementRequests(ctx, managementT0.Add(181*24*time.Hour), 0); err != nil || n != 0 {
		t.Fatalf("unresolved import released lineage: %d %v", n, err)
	}
	if err := s.FinishRecoveredImportJob(ctx, job.JobID, "import-worker", "vault-main", job.Fence, 0, SyncJournalEntry{JournalID: "management-import-finish", JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "applied", EvidenceJSON: `{}`, RecordedAt: syncT1}, syncT1); err != nil {
		t.Fatal(err)
	}
	cut := PruneCutoffs{Observations: syncT2, CompletedReceipts: syncT2}
	plan, err := s.PlanPrune(ctx, cut)
	if err != nil || plan.Counts.SyncJobs != 0 || plan.Counts.SyncJournals != 0 || plan.Counts.Observations != 0 {
		t.Fatalf("referenced evidence plan: %+v %v", plan, err)
	}
	counts, err := s.ExecutePrune(ctx, cut, "test", "retain-management-evidence", syncT3)
	if err != nil || counts.SyncJobs != 0 || counts.SyncJournals != 0 || counts.Observations != 0 {
		t.Fatalf("referenced evidence deleted: %+v %v", counts, err)
	}
	for table, want := range map[string]int{"sync_jobs": 1, "sync_journal_entries": 2, "source_observations": 1, "sync_import_effects": len(record.Paths), "sync_import_attributions": 1} {
		var got int
		if err := s.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil || got != want {
			t.Fatalf("retained %s=%d want%d: %v", table, got, want, err)
		}
	}
	if n, err := s.PruneManagementRequests(ctx, managementT0.Add(181*24*time.Hour), 0); err != nil || n != 1 {
		t.Fatalf("resolved management leaf prune: %d %v", n, err)
	}
	plan, err = s.PlanPrune(ctx, cut)
	if err != nil || plan.Counts.SyncJobs != 1 || plan.Counts.SyncJournals != 2 || plan.Counts.Observations != 1 {
		t.Fatalf("released evidence plan: %+v %v", plan, err)
	}
	counts, err = s.ExecutePrune(ctx, cut, "test", "release-management-evidence", syncT3)
	if err != nil || counts.SyncJobs != 1 || counts.SyncJournals != 2 || counts.Observations != 1 {
		t.Fatalf("released evidence prune: %+v %v", counts, err)
	}
	for _, table := range []string{"sync_jobs", "sync_journal_entries", "source_observations", "sync_import_effects", "sync_import_attributions"} {
		var got int
		if err := s.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil || got != 0 {
			t.Fatalf("released %s=%d: %v", table, got, err)
		}
	}
}

func TestE23T2CorruptTerminalTimeCannotDeleteEvidence(t *testing.T) {
	for _, column := range []string{"accepted_at", "resolved_at"} {
		for _, value := range []string{"!invalid", ""} {
			t.Run(column+"/"+value, func(t *testing.T) {
				s := openTestStore(t)
				ctx := context.Background()
				if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
					t.Fatal(err)
				}
				prepareManagement(t, s, 1, managementBinding("sync-now"))
				r := finishManagement(t, s, claimManagement(t, s, acceptManagement(t, s, prepareManagement(t, s, 2, managementBinding("sync-now")), 2)))
				// Simulate on-disk corruption beyond the immutable writer interface.
				if _, err := s.Exec(`DROP TRIGGER management_immutable_binding`); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Exec(`UPDATE sync_management_requests SET `+column+`=? WHERE request_id=?`, value, r.RequestID); err != nil {
					t.Fatal(err)
				}
				if _, err := s.LoadManagementRequest(ctx, r.GroupID, r.RequestID, managementT0); err == nil {
					t.Fatal("corrupt time returned as valid evidence")
				}
				if n, err := s.PruneManagementRequests(ctx, managementT0.Add(181*24*time.Hour), 0); err == nil || n != 0 {
					t.Fatalf("corrupt time pruned evidence: %d %v", n, err)
				}
				for table, want := range map[string]int{"sync_management_requests": 2, "sync_management_acceptances": 1, "sync_management_executions": 1} {
					var got int
					if err := s.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil || got != want {
						t.Fatalf("rollback %s=%d want%d: %v", table, got, want, err)
					}
				}
			})
		}
	}
}

// Compare persisted rows, including claim evidence and ordinary jobs, rather
// than loading a corrupt request through the validation under test.
func managementLedgerSnapshot(t *testing.T, s *Store) string {
	t.Helper()
	ledger := make(map[string][][]any)
	for _, table := range []string{"sync_controls", "sync_jobs", "sync_management_requests", "sync_management_acceptances", "sync_management_executions", "sync_management_jobs"} {
		rows, err := s.Query(`SELECT * FROM ` + table + ` ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			ledger[table] = append(ledger[table], values)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	b, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestE23T2CorruptRequiredTimesRefuseLookupPruneAndRecovery(t *testing.T) {
	for _, column := range []string{"created_at", "expires_at", "claim_expires_at", "execution_deadline"} {
		for _, value := range []string{"", "!invalid"} {
			t.Run(column+"/"+value, func(t *testing.T) {
				s := openTestStore(t)
				ctx := context.Background()
				if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
					t.Fatal(err)
				}
				prepareManagement(t, s, 1, managementBinding("sync-now"))
				r := prepareManagement(t, s, 2, managementBinding("sync-now"))
				running := column == "claim_expires_at" || column == "execution_deadline"
				if running {
					r = claimManagement(t, s, acceptManagement(t, s, r, 2))
				}
				if _, err := s.Exec(`DROP TRIGGER management_immutable_binding`); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Exec(`UPDATE sync_management_requests SET `+column+`=? WHERE request_id=?`, value, r.RequestID); err != nil {
					t.Fatal(err)
				}
				before := managementLedgerSnapshot(t, s)
				if _, err := s.LoadManagementRequest(ctx, r.GroupID, r.RequestID, managementT0); err == nil {
					t.Fatal("corrupt required time returned as valid evidence")
				}
				if running {
					if _, err := s.RecoverManagementClaim(ctx, r, "queued", nil, "inspected-recovery", managementT0.Add(time.Second)); err == nil {
						t.Fatal("corrupt running time authorized early recovery")
					}
					if _, err := s.RecoverManagementClaim(ctx, r, "queued", nil, "inspected-recovery", managementT0.Add(5*time.Minute)); err == nil {
						t.Fatal("corrupt running time authorized expired recovery")
					}
				} else {
					// Empty expiry previously authorized deletion while still fresh.
					if column == "expires_at" && value == "" {
						if n, err := s.PruneManagementRequests(ctx, managementT0, 0); err == nil || n != 0 {
							t.Fatalf("fresh corrupt draft pruned: %d %v", n, err)
						}
					}
					if n, err := s.PruneManagementRequests(ctx, managementT0.Add(181*24*time.Hour), 0); err == nil || n != 0 {
						t.Fatalf("corrupt required time pruned evidence: %d %v", n, err)
					}
				}
				if after := managementLedgerSnapshot(t, s); after != before {
					t.Fatal("corrupt-time refusal changed persisted evidence")
				}
			})
		}
	}
}

func TestE23T2ClaimSafetyHoldsPreserveQueuedEvidence(t *testing.T) {
	for _, hold := range []struct{ state, reason, membership string }{
		{"paused", "operator_pause", "normal"},
		{"blocked", "recovery_required", "normal"},
		{"active", "none", "blocked_emergency"},
	} {
		t.Run(hold.state+"/"+hold.membership, func(t *testing.T) {
			s := openTestStore(t)
			ctx := context.Background()
			if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
				t.Fatal(err)
			}
			r := acceptManagement(t, s, prepareManagement(t, s, 1, managementBinding("sync-now")), 2)
			if _, err := s.Exec(`UPDATE sync_controls SET state=?,reason=?,membership_mode=? WHERE group_id=?`, hold.state, hold.reason, hold.membership, r.GroupID); err != nil {
				t.Fatal(err)
			}
			before := managementLedgerSnapshot(t, s)
			if _, err := s.ClaimManagementRequest(ctx, r.GroupID, r.RequestID, "worker-a", r.Binding, true, managementT0); !errors.Is(err, ErrSyncControlHeld) {
				t.Fatalf("claim ignored safety hold: %v", err)
			}
			if after := managementLedgerSnapshot(t, s); after != before {
				t.Fatal("held claim changed request, attempt, fence or execution evidence")
			}
		})
	}
}

func TestE23T2AssociationScopeAndForeignKeysRefuseWithoutWrites(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, group := range []string{"wiki-pair", "other-pair"} {
		if _, err := s.EnsureSyncControl(ctx, group, "cfg-1", syncT0); err != nil {
			t.Fatal(err)
		}
	}
	r := claimManagement(t, s, acceptManagement(t, s, prepareManagement(t, s, 1, managementBinding("sync-now")), 2))
	r, err := s.ReserveManagementAssociations(ctx, r, 1, managementT0)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := s.AdmitSyncJob(ctx, syncJobFixture("managed-job", "managed-key", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	other := syncJobFixture("other-job", "other-key", `{}`)
	other.GroupID = "other-pair"
	otherJob, _, err := s.AdmitSyncJob(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	before := managementLedgerSnapshot(t, s)
	for _, tc := range []struct {
		job  ManagementJob
		want error
	}{
		{ManagementJob{JobID: otherJob.JobID, Kind: otherJob.Kind, AssociationReason: "observed_reconciliation"}, ErrSyncAdmissionConflict},
		{ManagementJob{JobID: job.JobID, Kind: "import", AssociationReason: "observed_reconciliation"}, ErrSyncAdmissionConflict},
		{ManagementJob{JobID: "missing-job", Kind: job.Kind, AssociationReason: "observed_reconciliation"}, sql.ErrNoRows},
	} {
		if _, err := s.AssociateManagementJob(ctx, r, tc.job, managementT0); !errors.Is(err, tc.want) {
			t.Fatalf("invalid association %+v: %v", tc.job, err)
		}
		if after := managementLedgerSnapshot(t, s); after != before {
			t.Fatal("association refusal changed reservation, revision, job or evidence")
		}
	}
	for _, tc := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO sync_management_acceptances(request_id,accepted_at,fingerprint) VALUES(?,?,?)`, []any{managementHandle(99), r.CreatedAt, r.Fingerprint}},
		{`INSERT INTO sync_management_executions(request_id,fence,owner,started_at,deadline) VALUES(?,1,'worker-a',?,?)`, []any{managementHandle(99), r.CreatedAt, r.ExecutionDeadline}},
		{`INSERT INTO sync_management_jobs(request_id,job_id,kind,association_reason) VALUES(?,?,'publication','observed_reconciliation')`, []any{managementHandle(99), job.JobID}},
		{`INSERT INTO sync_management_jobs(request_id,job_id,kind,association_reason) VALUES(?,?,'publication','observed_reconciliation')`, []any{r.RequestID, "missing-job"}},
	} {
		if _, err := s.Exec(tc.query, tc.args...); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			t.Fatalf("management foreign key did not refuse: %v", err)
		}
		if after := managementLedgerSnapshot(t, s); after != before {
			t.Fatal("foreign-key refusal changed persisted evidence")
		}
	}
}

func TestE23T2CorruptStoredOutcomeRefusesLookupAndPrune(t *testing.T) {
	for _, outcome := range []string{
		`{"schema_version":"unsupported","outcome":{"status":"no_op"}}`,
		`{"schema_version":"agent-dispatch.sync-management/v1","outcome":{"status":"unknown"}}`,
		`{"schema_version":"agent-dispatch.sync-management/v1","outcome":{"status":"intent_committed","control_revision":1,"quiescence":"unknown"}}`,
	} {
		t.Run(outcome, func(t *testing.T) {
			s := openTestStore(t)
			ctx := context.Background()
			if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
				t.Fatal(err)
			}
			prepareManagement(t, s, 1, managementBinding("sync-now"))
			r := claimManagement(t, s, acceptManagement(t, s, prepareManagement(t, s, 2, managementBinding("sync-now")), 2))
			job, _, err := s.AdmitSyncJob(ctx, syncJobFixture("managed-job", "managed-key", `{}`))
			if err != nil {
				t.Fatal(err)
			}
			r, err = s.ReserveManagementAssociations(ctx, r, 1, managementT0)
			if err != nil {
				t.Fatal(err)
			}
			r, err = s.AssociateManagementJob(ctx, r, ManagementJob{JobID: job.JobID, Kind: job.Kind, AssociationReason: "observed_reconciliation"}, managementT0)
			if err != nil {
				t.Fatal(err)
			}
			r = finishManagement(t, s, r)
			if _, err := s.Exec(`UPDATE sync_jobs SET state='published',retain_until_resolved=0,resolved_at=? WHERE job_id=?`, syncT0, job.JobID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Exec(`DROP TRIGGER management_immutable_binding`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Exec(`UPDATE sync_management_requests SET outcome_json=? WHERE request_id=?`, outcome, r.RequestID); err != nil {
				t.Fatal(err)
			}
			before := managementLedgerSnapshot(t, s)
			if _, err := s.LoadManagementRequest(ctx, r.GroupID, r.RequestID, managementT0); err == nil {
				t.Fatal("corrupt stored outcome returned as valid evidence")
			}
			if n, err := s.PruneManagementRequests(ctx, managementT0.Add(181*24*time.Hour), 0); err == nil || n != 0 {
				t.Fatalf("corrupt outcome pruned evidence: %d %v", n, err)
			}
			if after := managementLedgerSnapshot(t, s); after != before {
				t.Fatal("corrupt-outcome refusal changed request, acceptance, execution or association evidence")
			}
		})
	}
}

func TestE23T2QueuedIdentityOrderAndVerifyOutcome(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	if id, err := s.NextQueuedManagementRequest(ctx, "missing"); err != nil || id != "" {
		t.Fatalf("empty queue: %q %v", id, err)
	}
	for offset, n := range []int{3, 1, 2} {
		r := prepareManagement(t, s, n, managementBinding("verify-all"))
		if _, _, err := s.AcceptManagementRequest(ctx, ManagementAdmission{GroupID: r.GroupID, RequestID: r.RequestID, CurrentBinding: r.Binding, Authorized: true, QueueLimit: 3, Clock: func() time.Time { return managementT0.Add(time.Duration(offset) * time.Second) }}); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []int{3, 1, 2} {
		id, err := s.NextQueuedManagementRequest(ctx, "wiki-pair")
		if err != nil || id != managementHandle(n) {
			t.Fatalf("FIFO head=%q want%d: %v", id, n, err)
		}
		r, err := s.ClaimManagementRequest(ctx, "wiki-pair", id, "worker-a", managementBinding("verify-all"), true, managementT0.Add(4*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if next, err := s.NextQueuedManagementRequest(ctx, "wiki-pair"); err != nil || next == id {
			t.Fatalf("running request remained queued: %q %v", next, err)
		}
		if _, err := s.TransitionManagementRequest(ctx, r, "finished", &syncmanagement.Outcome{Status: "no_op"}, "invalid-verify-result", managementT0.Add(5*time.Second)); err == nil {
			t.Fatal("verification finished without permitted outcome")
		}
		view, err := s.LoadManagementRequest(ctx, r.GroupID, id, managementT0.Add(5*time.Second))
		if err != nil || view.State != "running" || view.Revision != r.Revision || view.Outcome != nil {
			t.Fatalf("refusal partially finished: %+v %v", view, err)
		}
		target := strings.Repeat("b", 40)
		verification := "verify-pinned"
		outcome := syncmanagement.Outcome{Status: "complete", TargetCommit: &target, VerificationID: &verification}
		finished, err := s.TransitionManagementRequest(ctx, r, "finished", &outcome, "pinned-verify-evidence", managementT0.Add(5*time.Second))
		if err != nil || finished.State != "finished" || finished.Outcome == nil || finished.Outcome.VerificationID == nil || *finished.Outcome.VerificationID != verification || finished.Outcome.TargetCommit == nil || *finished.Outcome.TargetCommit != target {
			t.Fatalf("pinned verify result: %+v %v", finished, err)
		}
	}
	if id, err := s.NextQueuedManagementRequest(ctx, "wiki-pair"); err != nil || id != "" {
		t.Fatalf("finished requests remained queued: %q %v", id, err)
	}
}

func TestE23T2AdmissionBoundsAndRetryAuthorityRefuseWithoutWrites(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	r := prepareManagement(t, s, 1, managementBinding("sync-now"))
	for _, limit := range []int{1, 1001} {
		if _, _, err := s.AcceptManagementRequest(ctx, ManagementAdmission{GroupID: r.GroupID, RequestID: r.RequestID, CurrentBinding: r.Binding, Authorized: true, QueueLimit: limit, Clock: func() time.Time { return managementT0 }}); err == nil {
			t.Fatalf("queue limit %d accepted", limit)
		}
	}
	retry := managementBinding("retry")
	retry.TargetRequestID = &r.RequestID
	if _, _, err := s.PrepareManagementRequest(ctx, managementHandle(2), retry, managementT0); !errors.Is(err, ErrManagementUnauthorized) {
		t.Fatalf("unaccepted predecessor authorized retry: %v", err)
	}
	acceptManagement(t, s, r, 2)
	retry.StateIncarnationID = "epoch-other"
	if _, _, err := s.PrepareManagementRequest(ctx, managementHandle(2), retry, managementT0); !errors.Is(err, ErrManagementObsolete) {
		t.Fatalf("stale predecessor authorized retry: %v", err)
	}
	var requests, acceptances int
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_management_requests`).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_management_acceptances`).Scan(&acceptances); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || acceptances != 1 {
		t.Fatalf("refusals left partial writes: requests%d acceptances%d", requests, acceptances)
	}
}

func TestE23T2SQLLifecycleMatchesFrozenContract(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/contracts/sync-management-v1/lifecycle.json")
	if err != nil {
		t.Fatal(err)
	}
	var graph struct {
		States      []string            `json:"states"`
		Transitions map[string][]string `json:"transitions"`
	}
	if err := json.Unmarshal(raw, &graph); err != nil {
		t.Fatal(err)
	}
	s := openTestStore(t)
	fields := func(state string) []any {
		var accepted, resolved, owner, lease, deadline, outcome any
		if state != "prepared" && state != "expired" {
			accepted = managementTime(managementT0)
		}
		if state == "finished" {
			resolved = managementTime(managementT0)
			outcome = `{"schema_version":"agent-dispatch.sync-management/v1","outcome":{"status":"no_op"}}`
		}
		if state == "running" {
			owner = "worker-a"
			lease = managementTime(managementT0.Add(30 * time.Second))
			deadline = managementTime(managementT0.Add(240 * time.Second))
		}
		return []any{state, accepted, resolved, owner, lease, deadline, outcome}
	}
	n := 0
	for _, from := range graph.States {
		for _, to := range graph.States {
			n++
			t.Run(from+"/"+to, func(t *testing.T) {
				tx, err := s.Begin()
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				id := managementHandle(n)
				args := append([]any{id}, fields(from)...)
				if _, err := tx.Exec(`INSERT INTO sync_management_requests(request_id,group_id,instance_id,state_incarnation_id,action,policy_revision,config_revision,fingerprint,created_at,expires_at,state,accepted_at,resolved_at,claim_owner,claim_expires_at,execution_deadline,outcome_json) VALUES (?,'wiki-pair','node-a','epoch-a','sync-now','policy','config','fingerprint','2026-10-10T00:00:00Z','2026-10-10T00:10:00Z',?,?,?,?,?,?,?)`, args...); err != nil {
					t.Fatal(err)
				}
				args = append(fields(to), id)
				_, err = tx.Exec(`UPDATE sync_management_requests SET state=?,accepted_at=?,resolved_at=?,claim_owner=?,claim_expires_at=?,execution_deadline=?,outcome_json=? WHERE request_id=?`, args...)
				// A same-state write is permitted by SQL; application transitions still
				// require an actual edge. Every changed state must match the frozen graph.
				want := from == to || slices.Contains(graph.Transitions[from], to)
				if (err == nil) != want {
					t.Fatalf("SQL %s -> %s: %v, permitted%v", from, to, err, want)
				}
			})
		}
	}
}

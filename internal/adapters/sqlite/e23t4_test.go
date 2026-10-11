package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
	"path/filepath"
	"testing"
	"time"
)

func TestE23T4FirstControlAdmissionIsAtomic(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	r := prepareManagement(t, s, 1, managementBinding("pause"))
	if _, err := s.LoadSyncControl(ctx, r.GroupID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("preparation created control: %v", err)
	}
	if _, err := s.Exec(`CREATE TRIGGER fail_acceptance BEFORE INSERT ON sync_management_acceptances BEGIN SELECT RAISE(ABORT,'fixture acceptance failure'); END`); err != nil {
		t.Fatal(err)
	}
	in := ManagementAdmission{GroupID: r.GroupID, RequestID: r.RequestID, CurrentBinding: r.Binding, Authorized: true, QueueLimit: 2, ControlSyncRevision: "cfg-1", Clock: func() time.Time { return managementT0 }}
	if _, _, err := s.AcceptManagementRequest(ctx, in); err == nil {
		t.Fatal("injected acceptance failure succeeded")
	}
	for _, table := range []string{"sync_controls", "state_transitions", "sync_management_acceptances"} {
		var count int
		if err := s.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failure left %s: %d %v", table, count, err)
		}
	}
	retained, err := s.LoadManagementRequest(ctx, r.GroupID, r.RequestID, managementT0)
	if err != nil || retained.State != "prepared" || retained.AcceptedAt != nil || retained.Outcome != nil {
		t.Fatalf("failure changed request: %+v %v", retained, err)
	}
	if _, err := s.Exec(`DROP TRIGGER fail_acceptance`); err != nil {
		t.Fatal(err)
	}
	accepted, duplicate, err := s.AcceptManagementRequest(ctx, in)
	if err != nil || duplicate || accepted.State != "finished" || accepted.Outcome == nil || accepted.Outcome.Status != "intent_committed" || *accepted.Outcome.ControlRevision != 2 || *accepted.Outcome.Quiescence != "unknown" {
		t.Fatalf("first control acceptance: %+v duplicate=%t %v", accepted, duplicate, err)
	}
	control, err := s.LoadSyncControl(ctx, r.GroupID)
	if err != nil || control.State != "paused" || control.Revision != 2 {
		t.Fatalf("control: %+v %v", control, err)
	}
	if _, duplicate, err := s.AcceptManagementRequest(ctx, in); err != nil || !duplicate {
		t.Fatalf("duplicate control: %t %v", duplicate, err)
	}
	control, err = s.LoadSyncControl(ctx, r.GroupID)
	if err != nil || control.Revision != 2 {
		t.Fatalf("duplicate changed control: %+v %v", control, err)
	}
}

func TestE23T4StaleFirstControlRollsBackInitialization(t *testing.T) {
	s := openTestStore(t)
	b := managementBinding("pause")
	wrong := int64(9)
	b.ExpectedControlRevision = &wrong
	r := prepareManagement(t, s, 1, b)
	_, _, err := s.AcceptManagementRequest(context.Background(), ManagementAdmission{GroupID: r.GroupID, RequestID: r.RequestID, CurrentBinding: r.Binding, Authorized: true, QueueLimit: 2, ControlSyncRevision: "cfg-1", Clock: func() time.Time { return managementT0 }})
	if !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("stale admission: %v", err)
	}
	if _, err := s.LoadSyncControl(context.Background(), r.GroupID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale request materialized control: %v", err)
	}
}

func TestE23T4ExecutorEvidenceRequiresBindingOwnerAndFreshness(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	b := managementBinding("sync-now")
	assertAvailability := func(binding syncmanagement.Binding, now time.Time, want string) {
		t.Helper()
		got, err := s.ManagementExecutorAvailability(ctx, binding, now)
		if err != nil || got != want {
			t.Fatalf("availability:%s want%s %v", got, want, err)
		}
	}
	assertAvailability(b, managementT0, "unknown")
	if err := s.StartManagementExecutor(ctx, b, "worker-a", managementT0); err != nil {
		t.Fatal(err)
	}
	assertAvailability(b, managementT0.Add(60*time.Second), "available")
	assertAvailability(b, managementT0.Add(60*time.Second+time.Nanosecond), "unknown")
	assertAvailability(b, managementT0.Add(-time.Nanosecond), "unknown")
	changed := b
	changed.StateIncarnationID = "replacement-state"
	assertAvailability(changed, managementT0, "unknown")
	if err := s.RecordManagementExecutor(ctx, b, "worker-a", "stopped", managementT0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertAvailability(b, managementT0.Add(time.Hour), "stopped")
	if err := s.StartManagementExecutor(ctx, b, "worker-b", managementT0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordManagementExecutor(ctx, b, "worker-a", "stopped", managementT0.Add(time.Hour)); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("old owner stopped successor:%v", err)
	}
	assertAvailability(b, managementT0.Add(time.Hour), "available")
}

func TestE23T4UpgradePreservesLedgerAndReadOnlySchema28Lookup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.migrations = Migrations[:28]
	if err := s.Migrate(filepath.Join(dir, "backups")); err != nil {
		t.Fatal(err)
	}
	request := prepareManagement(t, s, 1, managementBinding("sync-now"))
	read, err := OpenReadOnly(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := read.CheckManagementSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := read.LoadManagementRequest(context.Background(), request.GroupID, request.RequestID, managementT0)
	if err != nil {
		t.Fatal(err)
	}
	availability, err := read.ManagementExecutorAvailability(context.Background(), request.Binding, managementT0)
	if err != nil || availability != "unknown" {
		t.Fatalf("schema28 availability:%s %v", availability, err)
	}
	if err := read.Close(); err != nil {
		t.Fatal(err)
	}
	var checksum string
	if err := s.QueryRow(`SELECT checksum FROM schema_migrations WHERE version=28`).Scan(&checksum); err != nil {
		t.Fatal(err)
	}
	s.migrations = Migrations
	if err := s.Migrate(filepath.Join(dir, "backups")); err != nil {
		t.Fatal(err)
	}
	after, err := s.LoadManagementRequest(context.Background(), request.GroupID, request.RequestID, managementT0)
	if err != nil {
		t.Fatal(err)
	}
	if before.RequestID != after.RequestID || before.Fingerprint != after.Fingerprint || before.Binding.GroupID != after.GroupID || before.State != after.State || before.Revision != after.Revision || after.AcceptedAt != nil || after.Attempts != 0 {
		t.Fatalf("upgrade changed ledger:%+v -> %+v", before, after)
	}
	var installedChecksum string
	if err := s.QueryRow(`SELECT checksum FROM schema_migrations WHERE version=28`).Scan(&installedChecksum); err != nil || installedChecksum != checksum {
		t.Fatalf("installed migration28 changed:%s -> %s %v", checksum, installedChecksum, err)
	}
}

func TestE23T4PublicationAssociationCommitsAtomicallyAfterPause(t *testing.T) {
	for _, owner := range []string{"", "publisher"} {
		for _, failure := range []string{"none", "association-write", "stale-management-fence", "expired-management-claim"} {
			t.Run(owner+"/"+failure, func(t *testing.T) {
				s := openTestStore(t)
				ctx := context.Background()
				if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", managementTime(managementT0)); err != nil {
					t.Fatal(err)
				}
				r := claimManagement(t, s, acceptManagement(t, s, prepareManagement(t, s, 1, managementBinding("sync-now")), 2))
				target := "1111111111111111111111111111111111111111"
				job, _, err := s.AdmitSyncJob(ctx, syncJobFixture("publication-managed", "publication-managed", `{"publication_id":"publication-managed"}`))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.Exec(`UPDATE sync_jobs SET state='signed',fence=1,claim_owner=NULL,claim_expires_at=NULL WHERE job_id=?`, job.JobID); err != nil {
					t.Fatal(err)
				}
				if owner != "" {
					if _, err := s.Exec(`UPDATE sync_jobs SET claim_owner=?,claim_expires_at=? WHERE job_id=?`, owner, managementTime(managementT0.Add(time.Minute)), job.JobID); err != nil {
						t.Fatal(err)
					}
				}
				r, err = s.ReserveManagementAssociations(ctx, r, 2, managementT0)
				if err != nil {
					t.Fatal(err)
				}
				r, err = s.AssociateManagementJob(ctx, r, ManagementJob{JobID: job.JobID, Kind: "publication", TargetCommit: &target, AssociationReason: "already_signed_recovery"}, managementT0)
				if err != nil {
					t.Fatal(err)
				}
				// The content effect was confirmed before local pause. Evidence commit must
				// retain the resulting delivery without authorizing another Git effect.
				if _, err := s.SetSyncControl(ctx, r.GroupID, 1, "paused", "cfg-1", managementTime(managementT0)); err != nil {
					t.Fatal(err)
				}
				if failure == "association-write" {
					if _, err := s.Exec(`CREATE TRIGGER fail_delivery_association BEFORE INSERT ON sync_management_jobs WHEN NEW.kind='delivery' BEGIN SELECT RAISE(ABORT,'association fixture failure'); END`); err != nil {
						t.Fatal(err)
					}
				}
				// This commit validates lease freshness at its writer-side inspection,
				// independently of the supplied publication evidence timestamp.
				lease := time.Now().Add(time.Minute)
				if failure == "expired-management-claim" {
					lease = time.Now().Add(-time.Second)
				}
				if _, err := s.Exec(`UPDATE sync_management_requests SET claim_expires_at=?,execution_deadline=? WHERE request_id=?`, managementTime(lease), managementTime(time.Now().Add(4*time.Minute)), r.RequestID); err != nil {
					t.Fatal(err)
				}
				expected := r
				if failure == "stale-management-fence" {
					expected.Fence++
				}
				nudge := syncrecords.Nudge{SchemaVersion: syncrecords.NudgeSchema, GroupID: r.GroupID, PublicationID: "publication-managed", Sender: "node-a", Receiver: "node-b", MembershipRevision: target, ContentRef: "refs/heads/content", TargetCommit: target}
				raw, err := syncrecords.CanonicalNudge(nudge)
				if err != nil {
					t.Fatal(err)
				}
				now := managementT0.Add(time.Second)
				journal := SyncJournalEntry{JournalID: "managed-published", JobID: job.JobID, Fence: 1, Phase: "publication", Outcome: "published", EvidenceJSON: `{}`, RecordedAt: now.Format(time.RFC3339Nano)}
				delivery := SyncJobInput{JobID: "delivery-managed", GroupID: r.GroupID, Kind: "delivery", LogicalKey: "publication-managed", InitialState: "pending", PayloadJSON: string(raw), QueueLimit: 1000}
				associated, err := s.FinishManagementPublicationJob(ctx, expected, job.JobID, owner, 1, journal, delivery, target, now)
				var state string
				var deliveries, journals int
				if e := s.QueryRow(`SELECT state FROM sync_jobs WHERE job_id=?`, job.JobID).Scan(&state); e != nil {
					t.Fatal(e)
				}
				if e := s.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='delivery'`).Scan(&deliveries); e != nil {
					t.Fatal(e)
				}
				if e := s.QueryRow(`SELECT COUNT(*) FROM sync_journal_entries WHERE journal_id='managed-published'`).Scan(&journals); e != nil {
					t.Fatal(e)
				}
				exact, loadErr := s.LoadManagementRequest(ctx, r.GroupID, r.RequestID, now)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				if failure != "none" {
					if err == nil || state != "signed" || deliveries != 0 || journals != 0 || len(exact.Jobs) != 1 || exact.Revision != r.Revision || exact.AssociationReserved != 1 {
						t.Fatalf("failed transaction leaked state: %s delivery=%d journals=%d request=%+v err=%v", state, deliveries, journals, exact, err)
					}
					return
				}
				if err != nil || state != "published" || deliveries != 1 || journals != 1 || len(exact.Jobs) != 2 || associated.AssociationReserved != 0 || associated.Fence != r.Fence || associated.ClaimOwner != r.ClaimOwner {
					t.Fatalf("atomic completion: %s delivery=%d journals=%d request=%+v err=%v", state, deliveries, journals, exact, err)
				}
				for _, linked := range exact.Jobs {
					if linked.TargetCommit == nil || *linked.TargetCommit != target || linked.AssociationReason != "already_signed_recovery" {
						t.Fatalf("exact attribution: %+v", linked)
					}
				}
				control, err := s.LoadSyncControl(ctx, r.GroupID)
				if err != nil || control.State != "paused" || control.Revision != 2 {
					t.Fatalf("evidence changed control: %+v %v", control, err)
				}
			})
		}
	}
}

func TestE23T4ChildAdmissionAndAssociationAreAtomic(t *testing.T) {
	for _, kind := range []string{"verification", "import", "controller-import"} {
		for _, failure := range []string{"pause", "association-write", "stale-fence", "expired-claim", "missing-reservation", "wrong-target"} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				s := openTestStore(t)
				ctx := context.Background()
				if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", managementTime(managementT0)); err != nil {
					t.Fatal(err)
				}
				action, reason := "verify-all", "verification_target"
				in := SyncJobInput{JobID: "verification-atomic", GroupID: "wiki-pair", Kind: "verification", LogicalKey: "verification-atomic", InitialState: "planned", QueueLimit: 1000, Now: managementTime(managementT0)}
				target := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
				in.PayloadJSON = `{"verification_id":"verification-atomic","group_id":"wiki-pair","phase":"planned","target_commit":"` + target + `"}`
				if kind != "verification" {
					action, reason = "sync-now", "observed_reconciliation"
					record := validE21T4Import(t, kind == "controller-import")
					raw, err := syncrecords.CanonicalImport(record)
					if err != nil {
						t.Fatal(err)
					}
					in.JobID, in.Kind, in.LogicalKey, in.InitialState, in.PayloadJSON = "import-atomic", "import", record.ImportID, "validated", string(raw)
				}
				r := claimManagement(t, s, acceptManagement(t, s, prepareManagement(t, s, 1, managementBinding(action)), 2))
				if failure != "missing-reservation" {
					var err error
					r, err = s.ReserveManagementAssociations(ctx, r, 1, managementT0)
					if err != nil {
						t.Fatal(err)
					}
				}
				lease := time.Now().Add(time.Minute)
				if failure == "expired-claim" {
					lease = time.Now().Add(-time.Second)
				}
				if _, err := s.Exec(`UPDATE sync_management_requests SET claim_expires_at=?,execution_deadline=? WHERE request_id=?`, managementTime(lease), managementTime(time.Now().Add(4*time.Minute)), r.RequestID); err != nil {
					t.Fatal(err)
				}
				expected := r
				if failure == "stale-fence" {
					expected.Fence++
				}
				if failure == "wrong-target" {
					target = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				}
				// Force pause exactly after inserting the job, before association.
				// The transaction must retain both writes or roll both back.
				if _, err := s.Exec(`CREATE TRIGGER pause_at_admission AFTER INSERT ON sync_jobs BEGIN UPDATE sync_controls SET state='paused',reason='operator_pause',revision=revision+1 WHERE group_id=NEW.group_id; END`); err != nil {
					t.Fatal(err)
				}
				if failure == "association-write" {
					if _, err := s.Exec(`CREATE TRIGGER fail_child_association BEFORE INSERT ON sync_management_jobs BEGIN SELECT RAISE(ABORT,'fixture association failure'); END`); err != nil {
						t.Fatal(err)
					}
				}
				job, reused, associated, err := s.AdmitManagementSyncJob(ctx, expected, in, target, reason)
				var jobs int
				if e := s.QueryRow(`SELECT COUNT(*) FROM sync_jobs`).Scan(&jobs); e != nil {
					t.Fatal(e)
				}
				exact, e := s.LoadManagementRequest(ctx, r.GroupID, r.RequestID, time.Now())
				if e != nil {
					t.Fatal(e)
				}
				control, e := s.LoadSyncControl(ctx, r.GroupID)
				if e != nil {
					t.Fatal(e)
				}
				if failure != "pause" {
					if err == nil || jobs != 0 || len(exact.Jobs) != 0 || exact.Revision != r.Revision || exact.AssociationReserved != r.AssociationReserved || control.State != "active" || control.Revision != 1 {
						t.Fatalf("failed admission leaked writes: jobs%d request%+v control%+v err%v", jobs, exact, control, err)
					}
					return
				}
				if err != nil || reused || jobs != 1 || len(exact.Jobs) != 1 || exact.Jobs[0].JobID != job.JobID || exact.Jobs[0].Kind != in.Kind || exact.Jobs[0].TargetCommit == nil || *exact.Jobs[0].TargetCommit != target || exact.Jobs[0].AssociationReason != reason || associated.Revision != r.Revision+1 || associated.AssociationReserved != 0 || control.State != "paused" || control.Revision != 2 {
					t.Fatalf("lost exact admission: jobs%d request%+v control%+v err%v", jobs, exact, control, err)
				}
				// Paused evidence is retained, but cannot authorize a protected job claim.
				if _, err := s.ClaimSyncJob(ctx, job.JobID, "child-owner", "cfg-1", managementTime(time.Now()), managementTime(time.Now().Add(time.Minute))); !errors.Is(err, ErrSyncControlHeld) {
					t.Fatalf("pause allowed child claim: %v", err)
				}
				// Exact reuse needs no new association capacity and never rewrites it.
				again, reused, retained, err := s.AdmitManagementSyncJob(ctx, associated, in, target, reason)
				if err != nil || !reused || again.JobID != job.JobID || retained.Revision != associated.Revision || retained.AssociationReserved != 0 {
					t.Fatalf("repeated admission changed attribution: %+v %+v reused%t %v", again, retained, reused, err)
				}
				lookup, err := s.LoadManagementRequest(ctx, r.GroupID, r.RequestID, time.Now())
				if err != nil || len(lookup.Jobs) != 1 || lookup.Jobs[0].JobID != job.JobID || lookup.Jobs[0].Kind != in.Kind || lookup.Jobs[0].TargetCommit == nil || *lookup.Jobs[0].TargetCommit != target || lookup.Jobs[0].AssociationReason != reason {
					t.Fatalf("repeated admission changed exact lookup: %+v %v", lookup, err)
				}
			})
		}
	}
}

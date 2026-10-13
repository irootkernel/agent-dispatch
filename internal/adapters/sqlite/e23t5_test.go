package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func e23t5Claim(t *testing.T, s *Store) ManagementRequest {
	t.Helper()
	if _, err := s.EnsureSyncControl(context.Background(), "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	return claimManagement(t, s, acceptManagement(t, s, prepareManagement(t, s, 1, managementBinding("sync-now")), 1000))
}

func e23t5ImportObligation(t *testing.T, s *Store, r ManagementRequest) (ManagementRequest, SyncJobRow) {
	t.Helper()
	p := validE21T4Import(t, false)
	raw, _ := json.Marshal(p)
	job, _, err := s.AdmitSyncJob(context.Background(), SyncJobInput{JobID: p.ImportID, GroupID: p.GroupID, Kind: "import", LogicalKey: p.PlanID, InitialState: "validated", PayloadJSON: string(raw), ConfigRevision: "cfg-1", QueueLimit: 1000, Now: syncT0})
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.ReserveManagementAssociations(context.Background(), r, 1, managementT0)
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.AssociateManagementJob(context.Background(), r, ManagementJob{JobID: job.JobID, Kind: "import", TargetCommit: &p.TargetCommit, AssociationReason: "observed_reconciliation"}, managementT0)
	if err != nil {
		t.Fatal(err)
	}
	return r, job
}

func TestE23T5RetryAdmissionFlattensExactObligationsAndPreservesAttempts(t *testing.T) {
	s := openTestStore(t)
	r := e23t5Claim(t, s)
	r, job := e23t5ImportObligation(t, s, r)
	r = finishManagement(t, s, r)
	if _, err := s.Exec(`UPDATE sync_jobs SET attempts=19 WHERE job_id=?`, job.JobID); err != nil {
		t.Fatal(err)
	}
	b := managementBinding("retry")
	b.TargetRequestID = &r.RequestID
	child := acceptManagement(t, s, prepareManagement(t, s, 2, b), 1000)
	view, err := s.LoadManagementRequest(context.Background(), r.GroupID, child.RequestID, managementT0)
	if err != nil || view.Attempts != r.Attempts || len(view.Jobs) != 1 || view.Jobs[0].JobID != job.JobID || view.Jobs[0].Attempts != 19 || view.Jobs[0].AssociationReason != "retry_obligation" {
		t.Fatalf("retry changed identity/budget:%+v %v", view, err)
	}
	child = finishManagement(t, s, claimManagement(t, s, child))
	b.TargetRequestID = &child.RequestID
	grandchild := acceptManagement(t, s, prepareManagement(t, s, 3, b), 1000)
	view2, err := s.LoadManagementRequest(context.Background(), r.GroupID, grandchild.RequestID, managementT0)
	if err != nil || !reflect.DeepEqual(view.Jobs, view2.Jobs) || view2.Attempts != child.Attempts {
		t.Fatalf("flattened retry:%+v %v", view2, err)
	}
	// The twentieth underlying claim is the same identity; the next fails.
	if _, err := s.EnsureSyncControl(context.Background(), r.GroupID, "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimSyncJob(context.Background(), job.JobID, "import-owner", "cfg-1", syncT0, syncT3)
	if err != nil || claimed.Attempts != 20 {
		t.Fatalf("twentieth original attempt:%+v %v", claimed, err)
	}
}

func TestE23T5RetryRechecksEligibilityAtAcceptanceAndRollsBack(t *testing.T) {
	for _, state := range []string{"blocked", "deferred", "resolved_deferred", "exhausted", "held"} {
		t.Run(state, func(t *testing.T) {
			s := openTestStore(t)
			r := e23t5Claim(t, s)
			r, job := e23t5ImportObligation(t, s, r)
			r = finishManagement(t, s, r)
			b := managementBinding("retry")
			b.TargetRequestID = &r.RequestID
			draft := prepareManagement(t, s, 2, b)
			switch state {
			case "resolved_deferred":
				if _, err := s.Exec(`UPDATE sync_jobs SET state='deferred' WHERE job_id=?`, job.JobID); err != nil {
					t.Fatal(err)
				}
				if err := s.ResolveImportDeferral(context.Background(), job.JobID, syncT3); err != nil {
					t.Fatal(err)
				}
			case "exhausted":
				_, _ = s.Exec(`UPDATE sync_jobs SET attempts=20 WHERE job_id=?`, job.JobID)
			case "held":
				_, _ = s.Exec(`UPDATE sync_jobs SET claim_owner='other',claim_expires_at=? WHERE job_id=?`, managementTime(managementT0.Add(time.Minute)), job.JobID)
			default:
				_, _ = s.Exec(`UPDATE sync_jobs SET state=? WHERE job_id=?`, state, job.JobID)
			}
			_, _, err := s.AcceptManagementRequest(context.Background(), ManagementAdmission{GroupID: b.GroupID, RequestID: draft.RequestID, CurrentBinding: b, Authorized: true, QueueLimit: 1000, Clock: func() time.Time { return managementT0 }})
			if !errors.Is(err, ErrManagementNotRetryable) {
				t.Fatalf("ineligible retry:%v", err)
			}
			view, err := s.LoadManagementRequest(context.Background(), b.GroupID, draft.RequestID, managementT0)
			if err != nil || view.State != "prepared" || view.AcceptedAt != nil || len(view.Jobs) != 0 {
				t.Fatalf("partial acceptance:%+v %v", view, err)
			}
		})
	}
}

func TestE23T5SettledRetryReturnsEvidenceWithoutExecution(t *testing.T) {
	s := openTestStore(t)
	r := e23t5Claim(t, s)
	r, job := e23t5ImportObligation(t, s, r)
	r = finishManagement(t, s, r)
	if _, err := s.Exec(`UPDATE sync_jobs SET state='applied',attempts=20,retain_until_resolved=0,resolved_at=? WHERE job_id=?`, syncT3, job.JobID); err != nil {
		t.Fatal(err)
	}
	b := managementBinding("retry")
	b.TargetRequestID = &r.RequestID
	child := acceptManagement(t, s, prepareManagement(t, s, 2, b), 1000)
	if child.State != "finished" || child.Outcome == nil || child.Outcome.Status != "already_settled" || child.Fence != 0 {
		t.Fatalf("settled replay executed:%+v", child)
	}
	var executions int
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_management_executions WHERE request_id=?`, child.RequestID).Scan(&executions); err != nil || executions != 0 {
		t.Fatalf("settled execution:%d %v", executions, err)
	}
}

func TestE23T5RetryBranchesCannotResetRequestExecutionBudget(t *testing.T) {
	s := openTestStore(t)
	r := e23t5Claim(t, s)
	r, _ = e23t5ImportObligation(t, s, r)
	r = finishManagement(t, s, r)
	b := managementBinding("retry")
	b.TargetRequestID = &r.RequestID
	child := acceptManagement(t, s, prepareManagement(t, s, 21, b), 1000)
	for n := 2; n <= 20; n++ {
		child := acceptManagement(t, s, prepareManagement(t, s, n, b), 1000)
		finishManagement(t, s, claimManagement(t, s, child))
	}
	if _, err := s.ClaimManagementRequest(context.Background(), b.GroupID, child.RequestID, "retry-worker", b, true, managementT0); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("fork reset aggregate budget:%v", err)
	}
	if _, _, err := s.PrepareManagementRequest(context.Background(), managementHandle(22), b, managementT0); !errors.Is(err, ErrManagementNotRetryable) {
		t.Fatalf("exhausted lineage admitted fresh retry:%v", err)
	}
}

func TestE23T5ManagementPrunePlanExecutionAndStrictBoundary(t *testing.T) {
	s := openTestStore(t)
	r := prepareManagement(t, s, 1, managementBinding("sync-now"))
	cut := PruneCutoffs{ManagementDrafts: r.ExpiresAt, ManagementTerminal: managementTime(managementT0.Add(-180 * 24 * time.Hour))}
	plan, err := s.PlanPrune(context.Background(), cut)
	if err != nil || plan.Counts.ManagementRequests != 0 {
		t.Fatalf("expiry boundary:%+v %v", plan, err)
	}
	cut.ManagementDrafts = managementTime(mustManagementTime(r.ExpiresAt).Add(time.Nanosecond))
	plan, err = s.PlanPrune(context.Background(), cut)
	if err != nil || plan.Counts.ManagementRequests != 1 {
		t.Fatalf("draft plan:%+v %v", plan, err)
	}
	if _, err := s.LoadManagementRequest(context.Background(), r.GroupID, r.RequestID, managementT0); err != nil {
		t.Fatalf("dry-run deleted draft:%v", err)
	}
	counts, err := s.ExecutePrune(context.Background(), cut, "test", "expired draft", syncT3)
	if err != nil || counts.ManagementRequests != plan.Counts.ManagementRequests {
		t.Fatalf("execution differs:%+v %v", counts, err)
	}
	if _, err := s.LoadManagementRequest(context.Background(), r.GroupID, r.RequestID, managementT0); !errors.Is(err, ErrManagementNotFound) {
		t.Fatalf("pruned handle resurrected:%v", err)
	}
}

func TestE23T5RecordedPreSignaturePublicationIsNotRetryable(t *testing.T) {
	s := openTestStore(t)
	r := e23t5Claim(t, s)
	job, _, err := s.AdmitSyncJob(context.Background(), syncJobFixture("pre-signature", "pre-signature", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.ReserveManagementAssociations(context.Background(), r, 1, managementT0)
	if err != nil {
		t.Fatal(err)
	}
	target := validE21T4Import(t, false).TargetCommit
	r, err = s.AssociateManagementJob(context.Background(), r, ManagementJob{JobID: job.JobID, Kind: job.Kind, TargetCommit: &target, AssociationReason: "observed_reconciliation"}, managementT0)
	if err != nil {
		t.Fatal(err)
	}
	r = finishManagement(t, s, r)
	b := managementBinding("retry")
	b.TargetRequestID = &r.RequestID
	if _, _, err := s.PrepareManagementRequest(context.Background(), managementHandle(2), b, managementT0); !errors.Is(err, ErrManagementNotRetryable) {
		t.Fatalf("recorded pre-signature work eligible:%v", err)
	}
	var attempts int
	if err := s.QueryRow(`SELECT attempts FROM sync_jobs WHERE job_id=?`, job.JobID).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("pre-signature attempted:%d %v", attempts, err)
	}
}

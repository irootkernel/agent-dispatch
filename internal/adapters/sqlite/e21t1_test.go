package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

const (
	syncT0 = "2026-09-21T00:00:00Z"
	syncT1 = "2026-09-21T00:01:00Z"
	syncT2 = "2026-09-21T00:02:00Z"
	syncT3 = "2026-09-21T00:03:00Z"
)

func syncJobFixture(id, key, payload string) SyncJobInput {
	return SyncJobInput{
		JobID: id, GroupID: "wiki-pair", Kind: "publication", LogicalKey: key,
		InitialState: "eligible", PayloadJSON: payload,
		ConfigRevision: "cfg-1", QueueLimit: 1000, Now: syncT0,
	}
}

func TestE21T1AdmissionIsIdempotentAndFingerprintBound(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	first, reused, err := s.AdmitSyncJob(ctx, syncJobFixture("job-1", "cause-1", `{}`))
	if err != nil || reused {
		t.Fatalf("first admission: reused=%v err=%v", reused, err)
	}
	second, reused, err := s.AdmitSyncJob(ctx, syncJobFixture("job-other", "cause-1", `{ }`))
	if err != nil || !reused || second.JobID != first.JobID {
		t.Fatalf("same request must reuse %q: row=%+v reused=%v err=%v", first.JobID, second, reused, err)
	}
	if _, _, err := s.AdmitSyncJob(ctx, syncJobFixture("job-2", "cause-1", `{"changed":true}`)); !errors.Is(err, ErrSyncAdmissionConflict) {
		t.Fatalf("changed request must conflict: %v", err)
	}
}

func TestE21T1ExpiredLeaseNeedsRecoveryAndRejectsStaleWriter(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.AdmitSyncJob(ctx, syncJobFixture("job-1", "cause-1", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimSyncJob(ctx, job.JobID, "worker-a", "cfg-1", syncT0, syncT1)
	if err != nil || claim.Fence != 1 {
		t.Fatalf("first claim: %+v %v", claim, err)
	}
	if _, err := s.ClaimSyncJob(ctx, job.JobID, "worker-b", "cfg-1", syncT2, syncT3); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("expiry alone must not permit takeover: %v", err)
	}
	recovery := SyncJournalEntry{JournalID: "journal-recovery", JobID: job.JobID, Fence: 1, Phase: "claim_recovery", Outcome: "effect_not_started", EvidenceJSON: `{"probe":"no-effect"}`, RecordedAt: syncT2}
	if err := s.ReconcileExpiredSyncClaim(ctx, job.JobID, 1, "effect_not_started", recovery, syncT2); err != nil {
		t.Fatal(err)
	}
	claim2, err := s.ClaimSyncJob(ctx, job.JobID, "worker-b", "cfg-1", syncT2, syncT3)
	if err != nil || claim2.Fence != 2 {
		t.Fatalf("recovered claim: %+v %v", claim2, err)
	}
	stale := SyncJournalEntry{JournalID: "journal-stale", JobID: job.JobID, Fence: 1, Phase: "publication", Outcome: "ok", EvidenceJSON: `{}`, RecordedAt: syncT2}
	if err := s.AppendSyncJournal(ctx, stale, "worker-a"); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("stale owner must not append evidence: %v", err)
	}
}

func TestE21T1PauseResumeIsRevisionFencedAndPreservesBlocks(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	control, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := s.SetSyncControl(ctx, "wiki-pair", control.Revision, "paused", "cfg-2", syncT1)
	if err != nil || paused.State != "paused" || paused.ConfigRevision != "cfg-1" {
		t.Fatalf("pause must preserve validated policy binding: %+v %v", paused, err)
	}
	if _, err := s.SetSyncControl(ctx, "wiki-pair", control.Revision, "active", "cfg-2", syncT2); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("stale resume must fail: %v", err)
	}
	active, err := s.SetSyncControl(ctx, "wiki-pair", paused.Revision, "active", "cfg-2", syncT2)
	if err != nil || active.State != "active" || active.ConfigRevision != "cfg-2" {
		t.Fatalf("resume must bind current policy: %+v %v", active, err)
	}
	revalidated, err := s.SetSyncControl(ctx, "wiki-pair", active.Revision, "active", "cfg-3", syncT3)
	if err != nil || revalidated.Revision != active.Revision+1 || revalidated.ConfigRevision != "cfg-3" {
		t.Fatalf("active resume must rebind changed policy: %+v %v", revalidated, err)
	}
	if _, err := s.Exec(`UPDATE sync_controls SET state='blocked', reason='conflict', revision=revision+1 WHERE group_id='wiki-pair'`); err != nil {
		t.Fatal(err)
	}
	blocked, err := s.LoadSyncControl(ctx, "wiki-pair")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSyncControl(ctx, "wiki-pair", blocked.Revision, "active", "cfg-2", syncT3); !errors.Is(err, ErrSyncControlHeld) {
		t.Fatalf("resume must not clear conflict: %v", err)
	}
}

func TestE21T1ConcurrentAdmissionCreatesOneLogicalJob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnsureSyncControl(context.Background(), "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	stores := []*Store{a, b}
	ids := []string{"job-a", "job-b"}
	results := make([]SyncJobRow, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _, errs[i] = stores[i].AdmitSyncJob(context.Background(), syncJobFixture(ids[i], "same-cause", `{}`))
		}(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].JobID != results[1].JobID {
		t.Fatalf("concurrent reuse: rows=%+v errs=%v", results, errs)
	}
	var count int
	if err := a.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE logical_key='same-cause'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("logical rows=%d err=%v", count, err)
	}
}

func TestE21T1ResolvedRetentionIsChildrenFirstAndUnresolvedIsPreserved(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.AdmitSyncJob(ctx, syncJobFixture("job-resolved", "cause-resolved", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimSyncJob(ctx, job.JobID, "worker", "cfg-1", syncT0, syncT3)
	if err != nil {
		t.Fatal(err)
	}
	terminal := SyncJournalEntry{JournalID: "journal-terminal", JobID: job.JobID, Fence: claim.Fence, Phase: "publication", Outcome: "published", EvidenceJSON: `{}`, RecordedAt: syncT1}
	if err := s.FinishSyncJob(ctx, job.JobID, "worker", claim.Fence, "published", true, terminal, syncT1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitSyncJob(ctx, syncJobFixture("job-pending", "cause-pending", `{"pending":true}`)); err != nil {
		t.Fatal(err)
	}
	cutoffs := PruneCutoffs{CompletedReceipts: syncT2}
	plan, err := s.PlanPrune(ctx, cutoffs)
	if err != nil || plan.Counts.SyncJobs != 1 || plan.Counts.SyncJournals != 1 {
		t.Fatalf("prune plan=%+v err=%v", plan.Counts, err)
	}
	counts, err := s.ExecutePrune(ctx, cutoffs, "operator", "retention", syncT3)
	if err != nil || counts.SyncJobs != 1 || counts.SyncJournals != 1 {
		t.Fatalf("prune counts=%+v err=%v", counts, err)
	}
	var remaining int
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_jobs`).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("remaining jobs=%d err=%v", remaining, err)
	}
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_journal_entries WHERE job_id='job-resolved'`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("resolved journal rows=%d err=%v", remaining, err)
	}
}

func TestE21T2MembershipEmergencyBlocksEffectsUntilSignedPairRecovery(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	input := SyncJobInput{JobID: "membership-emergency", GroupID: "wiki-pair", Kind: "membership", LogicalKey: "plan-emergency", InitialState: "planned", PayloadJSON: `{}`, ConfigRevision: "cfg-1", QueueLimit: 1000, Now: syncT0}
	job, _, err := s.AdmitSyncJob(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimSyncAdministrationJob(ctx, job.JobID, "admin", "cfg-1", syncT0, syncT3)
	if err != nil {
		t.Fatal(err)
	}
	control, err := s.FinishMembershipJob(ctx, job.JobID, "admin", claim.Fence, "blocked_emergency", SyncJournalEntry{JournalID: "membership-emergency-applied", JobID: job.JobID, Fence: claim.Fence, Phase: "membership", Outcome: "applied", EvidenceJSON: `{}`, RecordedAt: syncT1}, "cfg-1", syncT1)
	if err != nil || control.State != "blocked" || control.Reason != "membership_emergency" {
		t.Fatalf("emergency control=%+v err=%v", control, err)
	}
	publication := syncJobFixture("publication-blocked", "publication-blocked", `{}`)
	publication.Now = syncT1
	pubJob, _, err := s.AdmitSyncJob(ctx, publication)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimSyncJob(ctx, pubJob.JobID, "publisher", "cfg-1", syncT1, syncT3); !errors.Is(err, ErrSyncControlHeld) {
		t.Fatalf("publication crossed emergency block: %v", err)
	}
	recoveryInput := input
	recoveryInput.JobID, recoveryInput.LogicalKey, recoveryInput.Now = "membership-recovery", "plan-recovery", syncT1
	recovery, _, err := s.AdmitSyncJob(ctx, recoveryInput)
	if err != nil {
		t.Fatal(err)
	}
	recoveryClaim, err := s.ClaimSyncAdministrationJob(ctx, recovery.JobID, "admin", "cfg-1", syncT1, syncT3)
	if err != nil {
		t.Fatalf("membership repair must cross its own hold: %v", err)
	}
	control, err = s.FinishMembershipJob(ctx, recovery.JobID, "admin", recoveryClaim.Fence, "normal", SyncJournalEntry{JournalID: "membership-recovery-applied", JobID: recovery.JobID, Fence: recoveryClaim.Fence, Phase: "membership", Outcome: "applied", EvidenceJSON: `{}`, RecordedAt: syncT2}, "cfg-1", syncT2)
	if err != nil || control.State != "active" || control.Reason != "none" {
		t.Fatalf("pair recovery control=%+v err=%v", control, err)
	}
}

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

func TestE21T1QueueAndAttemptBoundsFailClosed(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	first := syncJobFixture("job-bound-first", "cause-bound-first", `{}`)
	first.QueueLimit = 1
	job, _, err := s.AdmitSyncJob(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	second := syncJobFixture("job-bound-second", "cause-bound-second", `{}`)
	second.QueueLimit = 1
	if _, _, err := s.AdmitSyncJob(ctx, second); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("queue exhaustion must fail closed: %v", err)
	}
	if _, err := s.Exec(`UPDATE sync_jobs SET attempts=20 WHERE job_id=?`, job.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimSyncJob(ctx, job.JobID, "worker-bound", "cfg-1", syncT0, syncT3); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("attempt exhaustion must fail closed: %v", err)
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

func TestE21T1SyncJobTransitionsCannotSkipOrMoveBackward(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.AdmitSyncJob(ctx, syncJobFixture("job-transition", "cause-transition", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.ClaimSyncJob(ctx, job.JobID, "worker-transition", "cfg-1", syncT0, syncT3)
	if err != nil {
		t.Fatal(err)
	}
	journal := SyncJournalEntry{JournalID: "transition-skip", JobID: job.JobID, Fence: job.Fence, Phase: "publication", Outcome: "published", EvidenceJSON: `{}`, RecordedAt: syncT1}
	if err := s.FinishSyncJob(ctx, job.JobID, "worker-transition", job.Fence, "published", true, journal, syncT1); err == nil {
		t.Fatal("eligible publication must not skip prepared and signed states")
	}
	prepared := SyncJournalEntry{JournalID: "transition-prepared", JobID: job.JobID, Fence: job.Fence, Phase: "publication", Outcome: "prepared", EvidenceJSON: `{}`, RecordedAt: syncT1}
	if err := s.AdvanceSyncJob(ctx, job.JobID, "worker-transition", job.Fence, "prepared", prepared, syncT1); err != nil {
		t.Fatal(err)
	}
	backward := SyncJournalEntry{JournalID: "transition-backward", JobID: job.JobID, Fence: job.Fence, Phase: "publication", Outcome: "prepared", EvidenceJSON: `{}`, RecordedAt: syncT2}
	if err := s.AdvanceSyncJob(ctx, job.JobID, "worker-transition", job.Fence, "eligible", backward, syncT2); err == nil {
		t.Fatal("prepared publication must not move backward to eligible")
	}
}

func TestE21T1ClaimExpiryUsesTimeOrderNotTimestampTextOrder(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	first, _, err := s.AdmitSyncJob(ctx, syncJobFixture("job-fractional-first", "cause-fractional-first", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	secondInput := syncJobFixture("job-fractional-second", "cause-fractional-second", `{}`)
	second, _, err := s.AdmitSyncJob(ctx, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	firstClaim, err := s.ClaimSyncJob(ctx, first.JobID, "worker-a", "cfg-1", "2026-09-21T00:00:00Z", "2026-09-21T00:00:00.1Z")
	if err != nil {
		t.Fatal(err)
	}
	advance := SyncJournalEntry{JournalID: "fractional-advance", JobID: first.JobID, Fence: firstClaim.Fence, Phase: "publication", Outcome: "prepared", EvidenceJSON: `{}`, RecordedAt: "2026-09-21T00:00:00Z"}
	if err := s.AdvanceSyncJob(ctx, first.JobID, "worker-a", firstClaim.Fence, "prepared", advance, advance.RecordedAt); err != nil {
		t.Fatalf("live fractional claim must remain usable despite RFC3339 text ordering: %v", err)
	}
	if _, err := s.ClaimSyncJob(ctx, second.JobID, "worker-b", "cfg-1", "2026-09-21T00:00:00Z", syncT1); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("live fractional claim must block sibling despite RFC3339 text ordering: %v", err)
	}
	if _, err := s.ClaimSyncJob(ctx, second.JobID, "worker-b", "cfg-1", "2026-09-21T00:00:00.2Z", syncT1); err != nil {
		t.Fatalf("expired fractional sibling claim must not block: %v", err)
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
	prepared := SyncJournalEntry{JournalID: "journal-retention-prepared", JobID: job.JobID, Fence: claim.Fence, Phase: "publication", Outcome: "prepared", EvidenceJSON: `{}`, RecordedAt: syncT0}
	if err := s.AdvanceSyncJob(ctx, job.JobID, "worker", claim.Fence, "prepared", prepared, syncT0); err != nil {
		t.Fatal(err)
	}
	signed := SyncJournalEntry{JournalID: "journal-retention-signed", JobID: job.JobID, Fence: claim.Fence, Phase: "publication", Outcome: "signed", EvidenceJSON: `{}`, RecordedAt: syncT0}
	if err := s.AdvanceSyncJob(ctx, job.JobID, "worker", claim.Fence, "signed", signed, syncT0); err != nil {
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
	if err != nil || plan.Counts.SyncJobs != 1 || plan.Counts.SyncJournals != 3 {
		t.Fatalf("prune plan=%+v err=%v", plan.Counts, err)
	}
	counts, err := s.ExecutePrune(ctx, cutoffs, "operator", "retention", syncT3)
	if err != nil || counts.SyncJobs != 1 || counts.SyncJournals != 3 {
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

func TestE21T2FetchedMembershipModeReconcilesProtectedEffectHold(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	emergencyRevision := "1111111111111111111111111111111111111111"
	blocked, err := s.ReconcileAdoptedMembership(ctx, "wiki-pair", "blocked_emergency", emergencyRevision, "cfg-1", syncT1)
	if err != nil || blocked.State != "blocked" || blocked.Reason != "membership_emergency" {
		t.Fatalf("emergency posture=%+v err=%v", blocked, err)
	}
	job, _, err := s.AdmitSyncJob(ctx, syncJobFixture("publication-emergency", "publication-emergency", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimSyncJob(ctx, job.JobID, "publisher", "cfg-1", syncT1, syncT3); !errors.Is(err, ErrSyncControlHeld) {
		t.Fatalf("protected effect crossed fetched emergency membership: %v", err)
	}
	normalRevision := "2222222222222222222222222222222222222222"
	active, err := s.ReconcileAdoptedMembership(ctx, "wiki-pair", "normal", normalRevision, "cfg-1", syncT2)
	if err != nil || active.State != "active" || active.Reason != "none" {
		t.Fatalf("normal replacement posture=%+v err=%v", active, err)
	}
}

func TestE21BlockedJobsRemainUntilRecoveryEvidence(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	control, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldSyncControl(ctx, "wiki-pair", "conflict", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.AdmitSyncJob(ctx, syncJobFixture("publication-blocked-retained", "publication-blocked-retained", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE sync_jobs SET state='blocked',fence=1,retain_until_resolved=1,resolved_at=NULL,updated_at=? WHERE job_id=?`, syncT0, job.JobID); err != nil {
		t.Fatal(err)
	}
	cutoffs := PruneCutoffs{CompletedReceipts: syncT2}
	plan, err := s.PlanPrune(ctx, cutoffs)
	if err != nil || plan.Counts.SyncJobs != 0 {
		t.Fatalf("unresolved block must not prune: plan=%+v err=%v", plan.Counts, err)
	}
	blocked, err := s.LoadSyncControl(ctx, "wiki-pair")
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Revision == control.Revision {
		t.Fatal("safety hold did not advance control revision")
	}
	if _, err := s.ReconcileSyncControlCheckpoint(ctx, "wiki-pair", blocked.Revision, "cfg-1", "checkpoint-commit", syncT1); err != nil {
		t.Fatal(err)
	}
	var retain int
	var resolved string
	if err := s.QueryRow(`SELECT retain_until_resolved,COALESCE(resolved_at,'') FROM sync_jobs WHERE job_id=?`, job.JobID).Scan(&retain, &resolved); err != nil || retain != 0 || resolved != syncT1 {
		t.Fatalf("reconciled job retain=%d resolved=%q err=%v", retain, resolved, err)
	}
	plan, err = s.PlanPrune(ctx, cutoffs)
	if err != nil || plan.Counts.SyncJobs != 1 || plan.Counts.SyncJournals != 1 {
		t.Fatalf("reconciled block must become prunable: plan=%+v err=%v", plan.Counts, err)
	}
}

func TestE21Migration23ReopensLegacyBlockedObligations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.AdmitSyncJob(ctx, syncJobFixture("legacy-blocked", "legacy-blocked", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE sync_jobs SET state='blocked',fence=1,retain_until_resolved=0,resolved_at=? WHERE job_id=?`, syncT0, job.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`DELETE FROM schema_migrations WHERE version=23`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	var retain int
	var resolved string
	if err := s.QueryRow(`SELECT retain_until_resolved,COALESCE(resolved_at,'') FROM sync_jobs WHERE job_id=?`, job.JobID).Scan(&retain, &resolved); err != nil || retain != 1 || resolved != "" {
		t.Fatalf("migrated blocked obligation retain=%d resolved=%q err=%v", retain, resolved, err)
	}
}

func TestE21T3PublicationAndPeerDeliveryCommitAtomically(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.AdmitSyncJob(ctx, syncJobFixture("publication-atomic", "publication-atomic", `{"publication_id":"publication-atomic"}`))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimSyncJob(ctx, job.JobID, "publisher", "cfg-1", syncT0, syncT3)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceSyncJob(ctx, job.JobID, "publisher", claim.Fence, "prepared", SyncJournalEntry{JournalID: "publication-prepared", JobID: job.JobID, Fence: claim.Fence, Phase: "publication", Outcome: "prepared", EvidenceJSON: `{}`, RecordedAt: syncT0}, syncT0); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceSyncJob(ctx, job.JobID, "publisher", claim.Fence, "signed", SyncJournalEntry{JournalID: "publication-signed", JobID: job.JobID, Fence: claim.Fence, Phase: "publication", Outcome: "signed", EvidenceJSON: `{}`, RecordedAt: syncT0}, syncT0); err != nil {
		t.Fatal(err)
	}
	delivery := SyncJobInput{JobID: "delivery-atomic", GroupID: "wiki-pair", Kind: "delivery", LogicalKey: "publication-atomic", InitialState: "pending", PayloadJSON: `{"target_commit":"1111111111111111111111111111111111111111"}`, QueueLimit: 1000, Now: syncT1}
	row, err := s.FinishPublicationJob(ctx, job.JobID, "publisher", claim.Fence, SyncJournalEntry{JournalID: "publication-published", JobID: job.JobID, Fence: claim.Fence, Phase: "publication", Outcome: "published", EvidenceJSON: `{"candidate":"1111111111111111111111111111111111111111"}`, RecordedAt: syncT1}, delivery, syncT1)
	if err != nil {
		t.Fatal(err)
	}
	if row.Kind != "delivery" || row.State != "pending" {
		t.Fatalf("delivery=%+v", row)
	}
	var publicationState, deliveryState string
	if err := s.QueryRow(`SELECT state FROM sync_jobs WHERE job_id='publication-atomic'`).Scan(&publicationState); err != nil {
		t.Fatal(err)
	}
	if err := s.QueryRow(`SELECT state FROM sync_jobs WHERE job_id='delivery-atomic'`).Scan(&deliveryState); err != nil {
		t.Fatal(err)
	}
	if publicationState != "published" || deliveryState != "pending" {
		t.Fatalf("publication=%s delivery=%s", publicationState, deliveryState)
	}
}

func TestE21T3PublicationEligibilityBindsIdleRouteReceiptAndFacts(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.SaveDecision(nil, DecisionRecord{DecisionID: "decision-publish", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", GenerationLineageJSON: `{"generation":1}`, Disposition: "dispatch", Classification: "normal", ReasonCodesJSON: "[]", CreatedAt: syncT0, Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIntent(nil, IntentRecord{DispatchID: "dispatch-publish", DecisionID: "decision-publish", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes-kanban", ResourceID: "vault-main", Generation: 1, IdempotencyKey: "idem-publish", ContentFingerprint: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ManifestDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", RequestVersion: "v1", RequestJSON: "{}", CreatedAt: syncT0}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWorkReceipt(nil, WorkReceiptRecord{ReceiptID: "receipt-publish", DispatchID: "dispatch-publish", RunID: "run-publish", ResourceID: "vault-main", Status: "completed", ChangesJSON: "[]", CompletedScopeJSON: "[]", RemainingScopeJSON: "[]", SubmittedAt: syncT1, ValidationState: "valid", ValidationReasonsJSON: "[]", BegunAt: syncT0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE route_runtime_state SET route_state='IDLE',active_dispatch_id=NULL,pending_reconcile=0 WHERE route_id='wiki-maintenance'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE resources SET observation_revision=1 WHERE resource_id='vault-main'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO path_facts(resource_id,path,digest,"exists",observed_at) VALUES('vault-main','note.md','sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc',1,?)`, syncT1); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadPublicationEligibility(ctx, "vault-main")
	if err != nil {
		t.Fatal(err)
	}
	if got.SourceRevision != 1 || len(got.ReceiptIDs) != 1 || got.ReceiptIDs[0] != "receipt-publish" || got.PathDigests["note.md"] == "" {
		t.Fatalf("eligibility=%+v", got)
	}
	if _, err := s.Exec(`UPDATE route_runtime_state SET route_state='ACTIVE_DIRTY' WHERE route_id='wiki-maintenance'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadPublicationEligibility(ctx, "vault-main"); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("dirty route eligible: %v", err)
	}
}

func TestE21T3PublicationEligibilityIgnoresDisabledRoute(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.Exec(`INSERT INTO routes(route_id,revision,policy_revision,resource_id,target_id,definition,updated_at) VALUES('disabled-route','disabled-rev','disabled-policy','vault-main','hermes-kanban-main','{}',?)`, syncT0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO route_runtime_state(route_id,activation_state,route_state,pending_reconcile) VALUES('disabled-route','disabled','IDLE',0)`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDecision(nil, DecisionRecord{DecisionID: "decision-enabled", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", GenerationLineageJSON: `{"generation":1}`, Disposition: "dispatch", Classification: "normal", ReasonCodesJSON: "[]", CreatedAt: syncT0, Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIntent(nil, IntentRecord{DispatchID: "dispatch-enabled", DecisionID: "decision-enabled", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes-kanban", ResourceID: "vault-main", Generation: 1, IdempotencyKey: "idem-enabled", ContentFingerprint: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ManifestDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", RequestVersion: "v1", RequestJSON: "{}", CreatedAt: syncT0}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWorkReceipt(nil, WorkReceiptRecord{ReceiptID: "receipt-enabled", DispatchID: "dispatch-enabled", RunID: "run-enabled", ResourceID: "vault-main", Status: "completed", ChangesJSON: "[]", CompletedScopeJSON: "[]", RemainingScopeJSON: "[]", SubmittedAt: syncT1, ValidationState: "valid", ValidationReasonsJSON: "[]", BegunAt: syncT0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE route_runtime_state SET route_state='IDLE',pending_reconcile=0 WHERE route_id='wiki-maintenance'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE resources SET observation_revision=1 WHERE resource_id='vault-main'`); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadPublicationEligibility(ctx, "vault-main")
	if err != nil || len(got.ReceiptIDs) != 1 || got.ReceiptIDs[0] != "receipt-enabled" {
		t.Fatalf("disabled route affected eligibility: got=%+v err=%v", got, err)
	}
}

func TestE21T3PausedControlRejectsNewPublicationAdmission(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	control, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSyncControl(ctx, "wiki-pair", control.Revision, "paused", "cfg-1", syncT1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE resources SET observation_revision=1 WHERE resource_id='vault-main'`); err != nil {
		t.Fatal(err)
	}
	in := syncJobFixture("paused-publication", "paused-publication", `{}`)
	in.PublicationResourceID = "vault-main"
	in.ExpectedSourceRevision = 1
	if _, _, err := s.AdmitSyncJob(ctx, in); !errors.Is(err, ErrSyncControlHeld) {
		t.Fatalf("paused publication admission was not rejected: %v", err)
	}
	var count int
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE job_id='paused-publication'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("paused admission persisted a job: count=%d err=%v", count, err)
	}
}

func TestE21T3ConfirmedCandidateRecoveryReusesPublication(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.AdmitSyncJob(ctx, syncJobFixture("publication-recovery", "publication-recovery", `{"publication_id":"publication-recovery"}`))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimSyncJob(ctx, job.JobID, "publisher", "cfg-1", syncT0, syncT3)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceSyncJob(ctx, job.JobID, "publisher", claim.Fence, "prepared", SyncJournalEntry{JournalID: "publication-prepared", JobID: job.JobID, Fence: claim.Fence, Phase: "publication", Outcome: "prepared", EvidenceJSON: `{}`, RecordedAt: syncT0}, syncT0); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceSyncJob(ctx, job.JobID, "publisher", claim.Fence, "signed", SyncJournalEntry{JournalID: "publication-signed", JobID: job.JobID, Fence: claim.Fence, Phase: "publication", Outcome: "signed", EvidenceJSON: `{"candidate":"1111111111111111111111111111111111111111"}`, RecordedAt: syncT1}, syncT1); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishSyncJob(ctx, job.JobID, "publisher", claim.Fence, "uncertain", false, SyncJournalEntry{JournalID: "publication-unknown", JobID: job.JobID, Fence: claim.Fence, Phase: "publication", Outcome: "effect_unknown", EvidenceJSON: `{}`, RecordedAt: syncT2}, syncT2); err != nil {
		t.Fatal(err)
	}
	delivery := SyncJobInput{JobID: "delivery-recovery", GroupID: "wiki-pair", Kind: "delivery", LogicalKey: "publication-recovery", InitialState: "pending", PayloadJSON: `{"target_commit":"1111111111111111111111111111111111111111"}`, QueueLimit: 1000, Now: syncT3}
	if _, err := s.FinishRecoveredPublicationJob(ctx, job.JobID, claim.Fence, SyncJournalEntry{JournalID: "publication-recovered", JobID: job.JobID, Fence: claim.Fence, Phase: "publication", Outcome: "published", EvidenceJSON: `{"candidate":"1111111111111111111111111111111111111111"}`, RecordedAt: syncT3}, delivery, syncT3); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := s.QueryRow(`SELECT state FROM sync_jobs WHERE job_id='publication-recovery'`).Scan(&state); err != nil || state != "published" {
		t.Fatalf("state=%s err=%v", state, err)
	}
}

func TestE21T3CheckpointRecoverySettlesConfirmedCandidate(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
	in := SyncJobInput{JobID: "checkpoint-recovery", GroupID: "wiki-pair", Kind: "checkpoint", LogicalKey: "checkpoint-plan-recovery", InitialState: "planned", PayloadJSON: `{"plan_id":"checkpoint-plan-recovery"}`, ConfigRevision: "cfg-1", QueueLimit: 1000, Now: syncT0}
	job, _, err := s.AdmitSyncJob(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimSyncAdministrationJob(ctx, job.JobID, "administrator", "cfg-1", syncT0, syncT1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceSyncJob(ctx, job.JobID, "administrator", claim.Fence, "applying", SyncJournalEntry{JournalID: "checkpoint-signed", JobID: job.JobID, Fence: claim.Fence, Phase: "checkpoint", Outcome: "signed", EvidenceJSON: `{"candidate":"1111111111111111111111111111111111111111"}`, RecordedAt: syncT0}, syncT0); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRecoveredCheckpointJob(ctx, job.JobID, claim.Fence, SyncJournalEntry{JournalID: "checkpoint-recovered", JobID: job.JobID, Fence: claim.Fence, Phase: "checkpoint", Outcome: "applied", EvidenceJSON: `{"candidate":"1111111111111111111111111111111111111111"}`, RecordedAt: syncT2}, syncT2); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := s.QueryRow(`SELECT state FROM sync_jobs WHERE job_id='checkpoint-recovery'`).Scan(&state); err != nil || state != "applied" {
		t.Fatalf("state=%s err=%v", state, err)
	}
}

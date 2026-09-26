package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSyncVerificationCommitsOutcomeAtomicallyAndRetainsInterruptedPlan(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg", syncT0); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"verification-plan", "verification-complete"} {
		if _, _, err := s.AdmitSyncJob(ctx, SyncJobInput{JobID: id, GroupID: "wiki-pair", Kind: "verification", LogicalKey: id, InitialState: "planned", PayloadJSON: `{}`, QueueLimit: 2, Now: syncT0}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.FinishSyncVerification(ctx, "verification-complete", "expired", `{}`, syncT1); err == nil {
		t.Fatal("unexpired verification was finished as expired")
	}
	if err := s.FinishSyncVerification(ctx, "verification-complete", "complete", `{"result":"complete"}`, syncT1); err != nil {
		t.Fatal(err)
	}
	unfinished, found, err := s.FindSyncJob(ctx, "wiki-pair", "verification", "verification-plan")
	if err != nil || !found || unfinished.State != "planned" || unfinished.ResolvedAt != "" {
		t.Fatalf("interrupted plan was lost: %+v found=%v err=%v", unfinished, found, err)
	}
	completed, found, err := s.FindSyncJob(ctx, "wiki-pair", "verification", "verification-complete")
	if err != nil || !found || completed.State != "complete" || completed.Fence != 1 || completed.ResolvedAt != syncT1 {
		t.Fatalf("finished verification did not persist: %+v found=%v err=%v", completed, found, err)
	}
	journals, err := s.LoadSyncJournals(ctx, completed.JobID)
	if err != nil || len(journals) != 3 || journals[0].Outcome != "started" || journals[2].EvidenceJSON != `{"result":"complete"}` {
		t.Fatalf("verification journal: %+v %v", journals, err)
	}
	if err := s.FinishSyncVerification(ctx, "verification-complete", "complete", `{}`, syncT2); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("stale completion was accepted: %v", err)
	}
}

func TestInterruptedVerificationExpiresOnlyAfterBound(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg", syncT0); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old-plan", "boundary-plan", "fresh-plan"} {
		now := syncT0
		if id == "boundary-plan" {
			now = time.Date(2026, 9, 21, 0, 0, 1, 0, time.UTC).Format(time.RFC3339Nano)
		}
		if id == "fresh-plan" {
			now = syncT1
		}
		if _, _, err := s.AdmitSyncJob(ctx, SyncJobInput{JobID: id, GroupID: "wiki-pair", Kind: "verification", LogicalKey: id, InitialState: "planned", PayloadJSON: `{}`, QueueLimit: 3, Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ExpireInterruptedSyncVerifications(ctx, "wiki-pair", time.Date(2026, 9, 21, 0, 5, 1, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	old, _, _ := s.FindSyncJob(ctx, "wiki-pair", "verification", "old-plan")
	boundary, _, _ := s.FindSyncJob(ctx, "wiki-pair", "verification", "boundary-plan")
	fresh, _, _ := s.FindSyncJob(ctx, "wiki-pair", "verification", "fresh-plan")
	if old.State != "expired" || old.ResolvedAt == "" || boundary.State != "planned" || boundary.ResolvedAt != "" || fresh.State != "planned" || fresh.ResolvedAt != "" {
		t.Fatalf("expiry boundary: old=%+v boundary=%+v fresh=%+v", old, boundary, fresh)
	}
	journals, err := s.LoadSyncJournals(ctx, old.JobID)
	if err != nil || len(journals) != 1 || journals[0].Outcome != "failed" || journals[0].EvidenceJSON != `{"reason":"interrupted_observation_expired"}` {
		t.Fatalf("expired verification journal: %+v %v", journals, err)
	}
}

func TestIncompleteVerificationRetainedUntilSuperseded(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg", syncT0); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, result, at string }{
		{"first", "incomplete", syncT0},
		{"second", "complete", syncT1},
	} {
		if _, _, err := s.AdmitSyncJob(ctx, SyncJobInput{JobID: tc.id, GroupID: "wiki-pair", Kind: "verification", LogicalKey: tc.id, InitialState: "planned", PayloadJSON: `{}`, QueueLimit: 1, Now: tc.at}); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishSyncVerification(ctx, tc.id, tc.result, `{}`, tc.at); err != nil {
			t.Fatal(err)
		}
		first, found, err := s.FindSyncJob(ctx, "wiki-pair", "verification", "first")
		if err != nil || !found || first.ResolvedAt == "" || first.RetainUntilResolved != (tc.result == "incomplete") {
			t.Fatalf("retention after %s: %+v found=%v err=%v", tc.result, first, found, err)
		}
	}
}

func TestLatestSyncJobOrdersFractionalTimestampChronologically(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "cfg", syncT0); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, at string }{
		{"z-older", "2026-09-21T00:00:00.1Z"},
		{"a-newer", "2026-09-21T00:00:00.15Z"},
	} {
		if _, _, err := s.AdmitSyncJob(ctx, SyncJobInput{JobID: tc.id, GroupID: "wiki-pair", Kind: "verification", LogicalKey: tc.id, InitialState: "planned", PayloadJSON: `{}`, QueueLimit: 2, Now: tc.at}); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := s.LoadLatestSyncJob(ctx, "wiki-pair", "verification")
	if err != nil || latest.JobID != "a-newer" {
		t.Fatalf("latest verification = %+v, err=%v", latest, err)
	}
}

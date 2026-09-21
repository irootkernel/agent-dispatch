package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func TestE21T4ImportEffectsFinishAndAttributeExactlyOnce(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := "2026-09-21T09:00:00Z"
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "revision-1", now); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.AdmitSyncJob(ctx, SyncJobInput{JobID: "import-job-1", GroupID: "wiki-pair", Kind: "import", LogicalKey: "import-1", InitialState: "validated", PayloadJSON: `{"import":"one"}`, ConfigRevision: "revision-1", QueueLimit: 10, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.ClaimSyncJob(ctx, job.JobID, "owner-1", "revision-1", now, "2026-09-21T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	after := "sha256:" + strings.Repeat("a", 64)
	effects := []syncrecords.ImportPath{{Path: "Inbox/a.md", Before: "absent", After: after}}
	if err := s.BeginImportApply(ctx, job.JobID, "owner-1", "vault-main", job.Fence, effects, SyncJournalEntry{JournalID: "journal-start", JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "started", EvidenceJSON: `{}`, RecordedAt: now}, now); err != nil {
		t.Fatal(err)
	}
	pending, err := s.LoadPendingImportEffects(ctx, "vault-main")
	if err != nil || len(pending) != 1 || pending[0].After != after {
		t.Fatalf("applying effect must be visible across process loss: pending=%+v err=%v", pending, err)
	}
	obs := ObservationRecord{ObservationID: "observation-import-1", SchemaVersion: "agent-dispatch.source-observation/v1", SourceType: "watchman", SourceID: "source", TriggerName: "trigger", ResourceID: "vault-main", ObservedAt: now, ReceivedAt: now, RawPayloadDigest: after, IngestStatus: "accepted", FlagsJSON: `{}`, ImportAttributions: []ImportAttributionRecord{{JobID: job.JobID, Fence: job.Fence, Path: "Inbox/a.md", AfterValue: after}}}
	if err := s.SaveObservation(nil, obs); err != nil {
		t.Fatal(err)
	}
	// An unrelated observation may advance the resource revision while the
	// original importer process is absent. Proven all-after recovery must retain
	// that observation and finish from the new revision.
	if _, err := s.Exec(`UPDATE resources SET observation_revision=1 WHERE resource_id='vault-main'`); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRecoveredImportJob(ctx, job.JobID, "owner-1", "vault-main", job.Fence, 0, SyncJournalEntry{JournalID: "journal-finish", JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "applied", EvidenceJSON: `{}`, RecordedAt: now}, now); err != nil {
		t.Fatal(err)
	}
	pending, err = s.LoadPendingImportEffects(ctx, "vault-main")
	if err != nil || len(pending) != 0 {
		t.Fatalf("effect must be consumed once: %+v %v", pending, err)
	}
}

func TestE21T4DeferredImportLeavesNoActiveQueueObligation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "revision-1", syncT0); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.AdmitSyncJob(ctx, SyncJobInput{JobID: "import-deferred", GroupID: "wiki-pair", Kind: "import", LogicalKey: "import-deferred", InitialState: "deferred", PayloadJSON: `{}`, ConfigRevision: "revision-1", QueueLimit: 1, Now: syncT0})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveImportDeferral(ctx, job.JobID, syncT1); err != nil {
		t.Fatal(err)
	}
	var unresolved int
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE job_id=? AND resolved_at IS NULL`, job.JobID).Scan(&unresolved); err != nil || unresolved != 0 {
		t.Fatalf("deferred import leaked into active queue: count=%d err=%v", unresolved, err)
	}
	latest, err := s.LoadLatestSyncJob(ctx, "wiki-pair", "import")
	if err != nil || latest.JobID != job.JobID || latest.State != "deferred" || latest.ResolvedAt == "" {
		t.Fatalf("resolved deferral must remain visible to status: row=%+v err=%v", latest, err)
	}
}

func TestE21T4OnlyNewestEffectCanAttributeAndDuplicateFailsClosed(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "revision-1", syncT0); err != nil {
		t.Fatal(err)
	}
	afterValues := []string{"sha256:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("b", 64)}
	beforeValues := []string{"absent", afterValues[0]}
	for i := range afterValues {
		jobID := "import-newest-" + string(rune('1'+i))
		job, _, err := s.AdmitSyncJob(ctx, SyncJobInput{JobID: jobID, GroupID: "wiki-pair", Kind: "import", LogicalKey: jobID, InitialState: "validated", PayloadJSON: `{}`, ConfigRevision: "revision-1", QueueLimit: 10, Now: []string{syncT0, syncT1}[i]})
		if err != nil {
			t.Fatal(err)
		}
		owner := "owner-newest-" + string(rune('1'+i))
		job, err = s.ClaimSyncJob(ctx, job.JobID, owner, "revision-1", []string{syncT0, syncT1}[i], syncT3)
		if err != nil {
			t.Fatal(err)
		}
		effects := []syncrecords.ImportPath{{Path: "Inbox/a.md", Before: beforeValues[i], After: afterValues[i]}}
		started := []string{syncT0, syncT1}[i]
		if err := s.BeginImportApply(ctx, job.JobID, owner, "vault-main", job.Fence, effects, SyncJournalEntry{JournalID: "start-" + jobID, JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "started", EvidenceJSON: `{}`, RecordedAt: started}, started); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishImportJob(ctx, job.JobID, owner, "vault-main", job.Fence, int64(i), SyncJournalEntry{JournalID: "finish-" + jobID, JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "applied", EvidenceJSON: `{}`, RecordedAt: started}, started); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := s.LoadPendingImportEffects(ctx, "vault-main")
	if err != nil || len(pending) != 1 || pending[0].JobID != "import-newest-2" {
		t.Fatalf("only newest effect may attribute: %+v err=%v", pending, err)
	}
	obs := ObservationRecord{ObservationID: "observation-newest", SchemaVersion: "agent-dispatch.source-observation/v1", SourceType: "watchman", SourceID: "source", TriggerName: "trigger", ResourceID: "vault-main", ObservedAt: syncT2, ReceivedAt: syncT2, RawPayloadDigest: afterValues[1], IngestStatus: "accepted", FlagsJSON: `{}`, ImportAttributions: []ImportAttributionRecord{{JobID: pending[0].JobID, Fence: pending[0].Fence, Path: pending[0].Path, AfterValue: pending[0].After}}}
	if err := s.SaveObservation(nil, obs); err != nil {
		t.Fatal(err)
	}
	tx, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	duplicate := obs
	duplicate.ObservationID = "observation-newest-duplicate"
	if err := s.SaveObservation(tx, duplicate); err == nil {
		t.Fatal("duplicate import attribution must fail closed")
	}
	_ = tx.Rollback()
}

func TestE21T4ResourceWriterIdleFenceTracksActiveDispatch(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	idle, err := s.ResourceWritersIdle(ctx, "vault-main")
	if err != nil || !idle {
		t.Fatalf("fresh resource must be idle: idle=%v err=%v", idle, err)
	}
	if err := s.SaveDecision(nil, DecisionRecord{DecisionID: "decision-import-idle", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", GenerationLineageJSON: `{}`, Disposition: "dispatch", Classification: "normal", ReasonCodesJSON: "[]", CreatedAt: syncT0, Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIntent(nil, IntentRecord{DispatchID: "dispatch-import-active", DecisionID: "decision-import-idle", RouteID: "wiki-maintenance", RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes-kanban", ResourceID: "vault-main", Generation: 1, IdempotencyKey: "idem-import-active", ContentFingerprint: "sha256:" + strings.Repeat("c", 64), ManifestDigest: "sha256:" + strings.Repeat("d", 64), RequestVersion: "v1", RequestJSON: `{}`, CreatedAt: syncT0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE route_runtime_state SET active_dispatch_id='dispatch-import-active' WHERE route_id='wiki-maintenance'`); err != nil {
		t.Fatal(err)
	}
	idle, err = s.ResourceWritersIdle(ctx, "vault-main")
	if err != nil || idle {
		t.Fatalf("active dispatch must fence import: idle=%v err=%v", idle, err)
	}
}

func TestE21T4DeferredValidatedImportCanReopenAfterFenceClears(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.EnsureSyncControl(ctx, "wiki-pair", "revision-1", syncT0); err != nil {
		t.Fatal(err)
	}
	record := validE21T4Import(t, false)
	payload, _ := syncrecords.CanonicalImport(record)
	job, _, err := s.AdmitSyncJob(ctx, SyncJobInput{JobID: "import-reopen", GroupID: "wiki-pair", Kind: "import", LogicalKey: record.ImportID, InitialState: "validated", PayloadJSON: string(payload), ConfigRevision: "revision-1", QueueLimit: 10, Now: syncT0})
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.ClaimSyncJob(ctx, job.JobID, "owner-reopen", "revision-1", syncT0, syncT3)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishSyncJob(ctx, job.JobID, "owner-reopen", job.Fence, "deferred", true, SyncJournalEntry{JournalID: "journal-deferred", JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "deferred", EvidenceJSON: `{}`, RecordedAt: syncT1}, syncT1); err != nil {
		t.Fatal(err)
	}
	reopened, err := s.ReopenDeferredImport(ctx, job.JobID, "revision-1", SyncJournalEntry{JournalID: "journal-reopen", JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_not_started", EvidenceJSON: `{}`, RecordedAt: syncT2}, syncT2)
	if err != nil || reopened.State != "validated" || reopened.ResolvedAt != "" {
		t.Fatalf("reopened=%+v err=%v", reopened, err)
	}
}

func TestE21T4ControllerOnlyImportHasDurableRecoveryBoundary(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	control, err := s.EnsureSyncControl(ctx, "wiki-pair", "revision-1", syncT0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.HoldSyncControl(ctx, "wiki-pair", "conflict", "revision-1", syncT0); err != nil {
		t.Fatal(err)
	}
	record := validE21T4Import(t, true)
	payload, _ := syncrecords.CanonicalImport(record)
	job, _, err := s.AdmitSyncJob(ctx, SyncJobInput{JobID: "import-controller", GroupID: "wiki-pair", Kind: "import", LogicalKey: record.ImportID, InitialState: "validated", PayloadJSON: string(payload), ConfigRevision: control.ConfigRevision, QueueLimit: 10, Now: syncT0})
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.ClaimSyncAdministrationJob(ctx, job.JobID, "owner-controller", "revision-1", syncT0, syncT1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BeginControllerImportApply(ctx, job.JobID, "owner-controller", job.Fence, SyncJournalEntry{JournalID: "journal-controller-start", JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "started", EvidenceJSON: `{}`, RecordedAt: syncT0}, syncT0); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRecoveredControllerImportJob(ctx, job.JobID, job.Fence, SyncJournalEntry{JournalID: "journal-controller-finish", JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "applied", EvidenceJSON: `{}`, RecordedAt: syncT2}, syncT2); err != nil {
		t.Fatal(err)
	}
	finished, found, err := s.FindSyncJob(ctx, "wiki-pair", "import", record.ImportID)
	if err != nil || !found || finished.State != "applied" || finished.ResolvedAt == "" {
		t.Fatalf("finished=%+v found=%v err=%v", finished, found, err)
	}
}

func validE21T4Import(t *testing.T, controllerOnly bool) syncrecords.Import {
	t.Helper()
	paths := []syncrecords.ImportPath{{Path: "Inbox/a.md", Before: "absent", After: "sha256:" + strings.Repeat("e", 64)}}
	if controllerOnly {
		paths = nil
	}
	record, err := syncrecords.NewImport(syncrecords.ImportBinding{
		GroupID: "wiki-pair", FromCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40),
		MembershipRevision: strings.Repeat("c", 40), AcknowledgementID: "acknowledgement-1",
		ResourceObservationRevision: 1, ExpectedGitStateDigest: "sha256:" + strings.Repeat("d", 64),
		HistoryEvidenceID: "history-evidence-1", CaseMode: "sensitive", ControllerOnly: controllerOnly, State: "validated", Reason: "none",
	}, paths)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/adapters/resourceguard"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/syncimport"
	"github.com/irootkernel/agent-dispatch/internal/app/syncmembership"
	"github.com/irootkernel/agent-dispatch/internal/app/syncpublication"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func runSyncReconcile(args []string, stdout, stderr io.Writer) int {
	const command = "sync reconcile"
	v, ok := parseClosedSyncFlags(args, map[string]bool{"--group": true})
	if !ok || v["--group"] == "" {
		return usageError(stderr, command, "reconcile requires --group GROUP and accepts --output json")
	}
	cfg, s, code := loadMembershipConfig(command, v["--group"], false, stderr)
	if code != 0 {
		return code
	}
	if !s.Enabled {
		return planErr(stderr, command, "sync_precondition_failed", "conflict", "sync must be enabled before reconciliation", 14)
	}
	revision, _ := config.SyncRevision(cfg)
	_, store, code := openOperatorStore(command, "", stderr)
	if code != 0 {
		return code
	}
	defer store.Close()
	now := time.Now().UTC()
	control, err := store.EnsureSyncControl(requestCtx(), s.GroupID, revision, now.Format(time.RFC3339Nano))
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	guard, err := resourceguard.Acquire(stateDirOf(cfg), s.Resource)
	if err != nil {
		return reconcileEnvelope(stdout, "deferred", "resource_busy", map[string]any{"detail": err.Error()})
	}
	defer guard.Close()
	client, err := membershipGitClient(cfg, s)
	if err != nil {
		return syncMembershipError(stderr, command, err, 3)
	}

	// Fetch into controller-owned tracking refs. A non-fast-forward rewrite of
	// either remote history is refused by Git before the live content ref moves.
	membershipTracking := "refs/agent-dispatch/sync/membership/" + s.GroupID
	if err := client.Fetch(requestCtx(), s.RemoteName, s.MembershipRef, membershipTracking, s.RemoteRepositoryDigest); err != nil {
		if errors.Is(err, gitlocal.ErrFetchRewrite) {
			return blockReconcile(stdout, stderr, store, s.GroupID, revision, "trust_failure", "membership_stale", err)
		}
		return reconcileEnvelopeCode(stdout, "deferred", "git_unstable", map[string]any{"detail": "membership fetch failed: " + err.Error()}, 10)
	}
	remoteMembership, err := client.ResolveRef(requestCtx(), membershipTracking)
	if err != nil {
		return blockReconcile(stdout, stderr, store, s.GroupID, revision, "trust_failure", "membership_stale", err)
	}
	localMembership, err := client.ResolveRef(requestCtx(), s.MembershipRef)
	if err != nil {
		return blockReconcile(stdout, stderr, store, s.GroupID, revision, "trust_failure", "membership_stale", err)
	}
	remoteHistory, err := syncmembership.LoadHistory(requestCtx(), client, remoteMembership, membershipBinding(cfg, s), s.Bounds.HistoryCommits)
	if err != nil {
		return blockReconcile(stdout, stderr, store, s.GroupID, revision, "trust_failure", "trust_failed", err)
	}
	remoteMembershipRecord, ok := remoteHistory.Current()
	if !ok {
		return blockReconcile(stdout, stderr, store, s.GroupID, revision, "trust_failure", "trust_failed", errors.New("verified membership history has no current revision"))
	}
	if remoteMembership != localMembership {
		relation, compareErr := client.Compare(requestCtx(), localMembership, remoteMembership)
		if compareErr != nil || relation != gitlocal.RelationBehind {
			if compareErr == nil {
				compareErr = errors.New("approved remote membership is not a signed fast-forward of the locally adopted revision")
			}
			return blockReconcile(stdout, stderr, store, s.GroupID, revision, "trust_failure", "membership_stale", compareErr)
		}
		if remoteMembershipRecord.Mode == "blocked_emergency" {
			control, err = store.ReconcileAdoptedMembership(requestCtx(), s.GroupID, remoteMembershipRecord.Mode, remoteMembership, revision, time.Now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				return syncStoreError(stderr, command, err)
			}
		}
		if err := client.UpdateRefExpected(requestCtx(), s.MembershipRef, remoteMembership, localMembership); err != nil {
			return blockReconcile(stdout, stderr, store, s.GroupID, revision, "trust_failure", "membership_stale", err)
		}
		if remoteMembershipRecord.Mode == "normal" {
			control, err = store.ReconcileAdoptedMembership(requestCtx(), s.GroupID, remoteMembershipRecord.Mode, remoteMembership, revision, time.Now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				return syncStoreError(stderr, command, err)
			}
		}
		return reconcileEnvelope(stdout, "membership_adopted", "none", map[string]any{
			"previous_membership_revision": localMembership,
			"membership_revision":          remoteMembership,
			"side_effects":                 []string{"local_membership_ref_updated"},
		})
	}
	control, err = store.ReconcileAdoptedMembership(requestCtx(), s.GroupID, remoteMembershipRecord.Mode, remoteMembership, revision, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	if control.State == "blocked" && control.Reason == "membership_emergency" {
		return reconcileEnvelopeCode(stdout, "blocked", "membership_emergency", map[string]any{"membership_revision": remoteMembership, "detail": "the verified membership revision blocks protected effects"}, 30)
	}

	contentTracking := "refs/agent-dispatch/sync/content/" + s.GroupID
	if err := client.Fetch(requestCtx(), s.RemoteName, s.ContentRef, contentTracking, s.RemoteRepositoryDigest); err != nil {
		if errors.Is(err, gitlocal.ErrFetchRewrite) {
			return blockReconcile(stdout, stderr, store, s.GroupID, revision, "trust_failure", "trust_failed", err)
		}
		return reconcileEnvelopeCode(stdout, "deferred", "git_unstable", map[string]any{"detail": "content fetch failed: " + err.Error()}, 10)
	}
	target, err := client.ResolveRef(requestCtx(), contentTracking)
	if err != nil {
		return syncMembershipError(stderr, command, err, 30)
	}
	from, err := client.ResolveRef(requestCtx(), s.ContentRef)
	if err != nil {
		return syncMembershipError(stderr, command, err, 14)
	}
	if handled, result := recoverPendingImport(stdout, stderr, cfg, s, revision, store, client, remoteMembership, target); handled {
		return result
	}
	relation, err := client.Compare(requestCtx(), from, target)
	if err != nil {
		return syncMembershipError(stderr, command, err, 30)
	}
	if relation == gitlocal.RelationAhead || relation == gitlocal.RelationDiverged {
		return blockReconcile(stdout, stderr, store, s.GroupID, revision, "conflict", "history_diverged", errors.New("local and approved remote content history are not fast-forward compatible"))
	}
	if relation == gitlocal.RelationEqual {
		if control.State == "blocked" && (control.Reason == "conflict" || control.Reason == "trust_failure" || control.Reason == "recovery_required") {
			if err := verifyContentHead(client, remoteHistory, cfg, s, target); err != nil || !contentHeadAddsCheckpoint(client, target) {
				return reconcileEnvelopeCode(stdout, "blocked", "history_uncovered", map[string]any{"target_commit": target, "detail": "the equal head is not the exact verified administrator checkpoint required to clear the hold"}, 14)
			}
			if _, err := store.ReconcileSyncControlCheckpoint(requestCtx(), s.GroupID, control.Revision, revision, target, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return syncStoreError(stderr, command, err)
			}
			return reconcileEnvelope(stdout, "no_change", "none", map[string]any{"target_commit": target, "checkpoint_reconciled": true})
		}
		return reconcileEnvelope(stdout, "no_change", "none", map[string]any{"target_commit": target})
	}

	commits, err := client.FirstParentRange(requestCtx(), from, target, s.Bounds.HistoryCommits)
	if err != nil {
		reason := "history_uncovered"
		if errors.Is(err, gitlocal.ErrHistoryBound) {
			reason = "bound_exhausted"
		}
		return blockReconcile(stdout, stderr, store, s.GroupID, revision, "trust_failure", reason, err)
	}
	if err := verifyImportBase(client, remoteHistory, cfg, s, from, s.Bounds.HistoryCommits); err != nil {
		reason := "history_uncovered"
		if errors.Is(err, gitlocal.ErrHistoryBound) {
			reason = "bound_exhausted"
		}
		return blockReconcile(stdout, stderr, store, s.GroupID, revision, "trust_failure", reason, err)
	}
	for _, commit := range commits {
		if err := verifyContentHead(client, remoteHistory, cfg, s, commit); err != nil {
			return blockReconcile(stdout, stderr, store, s.GroupID, revision, "trust_failure", "trust_failed", err)
		}
	}
	beforeFiles, err := client.ReadContentFiles(requestCtx(), from)
	if err != nil {
		return syncMembershipError(stderr, command, err, 30)
	}
	afterFiles, err := client.ReadContentFiles(requestCtx(), target)
	if err != nil {
		return syncMembershipError(stderr, command, err, 30)
	}
	if err := syncpublication.ValidateFiles(cfg, s.Resource, afterFiles); err != nil {
		return blockReconcile(stdout, stderr, store, s.GroupID, revision, "trust_failure", "trust_failed", err)
	}
	effects, writes, deletions := syncimport.Diff(beforeFiles, afterFiles)
	if len(effects) > 1000 {
		return blockReconcile(stdout, stderr, store, s.GroupID, revision, "trust_failure", "bound_exhausted", errors.New("import path set exceeds 1000 effects"))
	}
	observationRevision, err := store.ObservationRevision(requestCtx(), s.Resource)
	if err != nil || observationRevision < 1 {
		detail := "resource has no stable maintained observation revision"
		if err != nil {
			detail += ": " + err.Error()
		}
		return reconcileEnvelope(stdout, "deferred", "observation_unavailable", map[string]any{"detail": detail})
	}
	state, err := client.InspectImport(requestCtx(), s.ContentRef)
	if err != nil {
		return reconcileEnvelope(stdout, "deferred", "git_unstable", map[string]any{"detail": err.Error()})
	}
	writersIdle, err := store.ResourceWritersIdle(requestCtx(), s.Resource)
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	targetPaths := make([]string, 0, len(effects))
	for _, effect := range effects {
		targetPaths = append(targetPaths, effect.Path)
	}
	overlap, collisions := gitlocal.ImportOverlap(targetPaths, state.DirtyPaths, state.UntrackedPaths)
	reason := "none"
	reasonDetail := ""
	if !writersIdle {
		reason = "resource_busy"
		reasonDetail = "participating maintenance writers are still active"
	} else if state.ActiveOperation != "" {
		reason = "git_unstable"
		reasonDetail = "active Git state: " + state.ActiveOperation
	} else if len(collisions) > 0 {
		reason = "untracked_collision"
	} else if len(overlap) > 0 {
		reason = "local_overlap"
	} else if !config.SyncAcknowledgementCurrent(cfg, localSyncIncarnation(cfg)) || control.ConfigRevision != revision {
		reason = "acknowledgement_stale"
		reasonDetail = "the cooperative import acknowledgement or control configuration binding is stale"
	} else if control.State == "paused" {
		return reconcileEnvelope(stdout, "deferred", "operator_pause", map[string]any{"target_commit": target, "detail": "sync is paused; run sync resume after review"})
	} else if control.State == "blocked" && !(len(effects) == 0 && (control.Reason == "conflict" || control.Reason == "trust_failure" || control.Reason == "recovery_required")) {
		return reconcileEnvelopeCode(stdout, "blocked", control.Reason, map[string]any{"target_commit": target, "detail": "the existing sync safety hold must be resolved before Markdown import"}, 14)
	}
	if len(effects) == 0 {
		if reason != "none" {
			if reasonDetail == "" {
				reasonDetail = "controller-only advance is waiting for the same import safety fences"
			}
			return reconcileEnvelope(stdout, "deferred", reason, map[string]any{"target_commit": target, "detail": reasonDetail})
		}
		if control.State == "blocked" && !contentHeadAddsCheckpoint(client, target) {
			return reconcileEnvelopeCode(stdout, "blocked", "history_uncovered", map[string]any{"target_commit": target, "detail": "controller-only history does not add the exact administrator checkpoint required to clear the hold"}, 14)
		}
		ackID := "acknowledgement-stale"
		if s.ImportAcknowledgement != nil && s.ImportAcknowledgement.AcknowledgementID != "" {
			ackID = s.ImportAcknowledgement.AcknowledgementID
		}
		record, err := syncrecords.NewImport(syncrecords.ImportBinding{
			GroupID: s.GroupID, FromCommit: from, TargetCommit: target,
			MembershipRevision: remoteMembership, AcknowledgementID: ackID,
			ResourceObservationRevision: observationRevision, ExpectedGitStateDigest: state.Digest,
			HistoryEvidenceID: reconcileEvidenceID(from, target, remoteMembership, commits), CaseMode: config.CaseMode(),
			ControllerOnly: true, State: "validated", Reason: "none",
		}, nil)
		if err != nil {
			return syncMembershipError(stderr, command, err, 14)
		}
		payload, _ := syncrecords.CanonicalImport(record)
		job, reused, err := store.AdmitSyncJob(requestCtx(), sqlite.SyncJobInput{JobID: randomSyncID("import-job"), GroupID: s.GroupID, Kind: "import", LogicalKey: record.ImportID, InitialState: "validated", PayloadJSON: string(payload), ConfigRevision: revision, QueueLimit: s.Bounds.Queue, Now: time.Now().UTC().Format(time.RFC3339Nano)})
		if err != nil {
			return syncStoreError(stderr, command, err)
		}
		if reused {
			if job.State == "applied" {
				return reconcileEnvelope(stdout, "applied", "none", map[string]any{"import_id": record.ImportID, "target_commit": target, "controller_only": true, "idempotent": true})
			}
			if job.ClaimOwner != "" {
				return reconcileEnvelopeCode(stdout, "blocked", "partial_effect", map[string]any{"import_id": record.ImportID, "detail": "an earlier controller-only import still owns or requires recovery"}, 14)
			}
			if job.State == "deferred" {
				reopenedAt := time.Now().UTC().Format(time.RFC3339Nano)
				job, err = store.ReopenDeferredImport(requestCtx(), job.JobID, revision, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_not_started", EvidenceJSON: mustJSON(map[string]any{"reason": "retry_after_deferred_fence", "controller_only": true}), RecordedAt: reopenedAt}, reopenedAt)
				if err != nil {
					return syncStoreError(stderr, command, err)
				}
			}
		}
		owner := randomSyncID("import-owner")
		claimAt := time.Now().UTC()
		if control.State == "blocked" {
			job, err = store.ClaimSyncAdministrationJob(requestCtx(), job.JobID, owner, revision, claimAt.Format(time.RFC3339Nano), claimAt.Add(time.Duration(s.Bounds.SubprocessSeconds*4+60)*time.Second).Format(time.RFC3339Nano))
		} else {
			job, err = store.ClaimSyncJob(requestCtx(), job.JobID, owner, revision, claimAt.Format(time.RFC3339Nano), claimAt.Add(time.Duration(s.Bounds.SubprocessSeconds*4+60)*time.Second).Format(time.RFC3339Nano))
		}
		if err != nil {
			return syncStoreError(stderr, command, err)
		}
		stateNow, stateErr := client.InspectImport(requestCtx(), s.ContentRef)
		remoteNow, remoteErr := client.RemoteRef(requestCtx(), s.RemoteName, s.ContentRef, s.RemoteRepositoryDigest)
		membershipNow, membershipErr := client.RemoteRef(requestCtx(), s.RemoteName, s.MembershipRef, s.RemoteRepositoryDigest)
		idleNow, idleErr := store.ResourceWritersIdle(requestCtx(), s.Resource)
		if stateErr != nil || remoteErr != nil || membershipErr != nil || idleErr != nil || !idleNow || stateNow.Digest != state.Digest || remoteNow != target || membershipNow != remoteMembership || !config.SyncAcknowledgementCurrent(cfg, localSyncIncarnation(cfg)) {
			fenceReason := "git_unstable"
			if idleErr == nil && !idleNow {
				fenceReason = "resource_busy"
			}
			return finishImportDisposition(stdout, stderr, store, job, owner, "deferred", "deferred", "a controller-only pre-apply fence changed", fenceReason)
		}
		started := time.Now().UTC().Format(time.RFC3339Nano)
		if err := store.BeginControllerImportApply(requestCtx(), job.JobID, owner, job.Fence, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "started", EvidenceJSON: mustJSON(map[string]any{"import_id": record.ImportID, "plan_id": record.PlanID, "from_commit": from, "target_commit": target, "git_state": state.Digest, "controller_only": true}), RecordedAt: started}, started); err != nil {
			return syncStoreError(stderr, command, err)
		}
		if err := client.ApplyImportIndex(requestCtx(), s.ContentRef, from, target, nil, nil); err != nil {
			return finishControllerImport(stdout, stderr, store, client, job, owner, s.ContentRef, from, target, state.Digest, err)
		}
		appliedState, inspectErr := client.InspectImport(requestCtx(), s.ContentRef)
		refNow, refErr := client.ResolveRef(requestCtx(), s.ContentRef)
		if inspectErr != nil || refErr != nil || appliedState.ActiveOperation != "" || refNow != target {
			return finishControllerImport(stdout, stderr, store, client, job, owner, s.ContentRef, from, target, state.Digest, errors.New("controller-only import did not reach its exact target"))
		}
		terminal := time.Now().UTC().Format(time.RFC3339Nano)
		if err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, "applied", true, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "applied", EvidenceJSON: mustJSON(map[string]any{"import_id": record.ImportID, "target_commit": target, "controller_only": true}), RecordedAt: terminal}, terminal); err != nil {
			return syncStoreError(stderr, command, err)
		}
		checkpointReconciled := false
		if control.State == "blocked" {
			if _, err := store.ReconcileSyncControlCheckpoint(requestCtx(), s.GroupID, control.Revision, revision, target, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return syncStoreError(stderr, command, err)
			}
			checkpointReconciled = true
		}
		return reconcileEnvelope(stdout, "applied", "none", map[string]any{"import_id": record.ImportID, "from_commit": from, "target_commit": target, "paths": 0, "controller_only": true, "checkpoint_reconciled": checkpointReconciled, "idempotent": reused, "side_effects": []string{"controller_records_updated", "index_updated", "local_content_ref_updated", "state_committed"}})
	}
	ackID := "acknowledgement-stale"
	if s.ImportAcknowledgement != nil && s.ImportAcknowledgement.AcknowledgementID != "" {
		ackID = s.ImportAcknowledgement.AcknowledgementID
	}
	historyID := reconcileEvidenceID(from, target, remoteMembership, commits)
	recordState := "validated"
	if reason != "none" {
		recordState = "deferred"
	}
	record, err := syncrecords.NewImport(syncrecords.ImportBinding{
		GroupID: s.GroupID, FromCommit: from, TargetCommit: target,
		MembershipRevision: remoteMembership, AcknowledgementID: ackID,
		ResourceObservationRevision: observationRevision, ExpectedGitStateDigest: state.Digest,
		HistoryEvidenceID: historyID, CaseMode: config.CaseMode(), State: recordState, Reason: reason,
	}, effects)
	if err != nil {
		return syncMembershipError(stderr, command, err, 14)
	}
	payload, _ := syncrecords.CanonicalImport(record)
	admissionRevision := revision
	if reason == "acknowledgement_stale" && control.ConfigRevision != revision {
		admissionRevision = control.ConfigRevision
	}
	job, reused, err := store.AdmitSyncJob(requestCtx(), sqlite.SyncJobInput{JobID: randomSyncID("import-job"), GroupID: s.GroupID, Kind: "import", LogicalKey: record.ImportID, InitialState: recordState, PayloadJSON: string(payload), ConfigRevision: admissionRevision, QueueLimit: s.Bounds.Queue, Now: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	if reason != "none" {
		if !reused {
			if err := store.ResolveImportDeferral(requestCtx(), job.JobID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return syncStoreError(stderr, command, err)
			}
		} else if job.State != "deferred" {
			return syncStoreError(stderr, command, sqlite.ErrSyncPrecondition)
		}
		extra := map[string]any{"import_id": record.ImportID, "target_commit": target, "overlap": overlap, "collisions": collisions, "idempotent": reused}
		if reasonDetail != "" {
			extra["detail"] = reasonDetail
		}
		return reconcileEnvelope(stdout, "deferred", reason, extra)
	}
	if reused {
		if job.State == "applied" {
			return reconcileEnvelope(stdout, "applied", "none", map[string]any{"import_id": record.ImportID, "target_commit": target, "idempotent": true})
		}
		if job.State == "deferred" && job.ClaimOwner == "" {
			reopenedAt := time.Now().UTC().Format(time.RFC3339Nano)
			job, err = store.ReopenDeferredImport(requestCtx(), job.JobID, revision, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_not_started", EvidenceJSON: mustJSON(map[string]any{"reason": "retry_after_deferred_fence"}), RecordedAt: reopenedAt}, reopenedAt)
			if err != nil {
				return syncStoreError(stderr, command, err)
			}
		}
		if job.ClaimOwner != "" {
			return reconcileEnvelopeCode(stdout, "blocked", "partial_effect", map[string]any{"import_id": record.ImportID, "detail": "an earlier import attempt still owns or requires recovery"}, 14)
		}
	}
	owner := randomSyncID("import-owner")
	claimAt := time.Now().UTC()
	job, err = store.ClaimSyncJob(requestCtx(), job.JobID, owner, revision, claimAt.Format(time.RFC3339Nano), claimAt.Add(time.Duration(s.Bounds.SubprocessSeconds*4+60)*time.Second).Format(time.RFC3339Nano))
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	// Recheck every fence immediately before the durable pre-apply boundary.
	stateNow, stateErr := client.InspectImport(requestCtx(), s.ContentRef)
	remoteNow, remoteErr := client.RemoteRef(requestCtx(), s.RemoteName, s.ContentRef, s.RemoteRepositoryDigest)
	membershipNow, membershipErr := client.RemoteRef(requestCtx(), s.RemoteName, s.MembershipRef, s.RemoteRepositoryDigest)
	observationNow, observationErr := store.ObservationRevision(requestCtx(), s.Resource)
	idleNow, idleErr := store.ResourceWritersIdle(requestCtx(), s.Resource)
	if stateErr != nil || remoteErr != nil || membershipErr != nil || observationErr != nil || idleErr != nil || !idleNow || stateNow.Digest != state.Digest || remoteNow != target || membershipNow != remoteMembership || observationNow != observationRevision || !config.SyncAcknowledgementCurrent(cfg, localSyncIncarnation(cfg)) {
		fenceReason := "git_unstable"
		if idleErr == nil && !idleNow {
			fenceReason = "resource_busy"
		}
		return finishImportDisposition(stdout, stderr, store, job, owner, "deferred", "deferred", "a pre-apply fence changed", fenceReason)
	}
	preapplyAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.BeginImportApply(requestCtx(), job.JobID, owner, s.Resource, job.Fence, effects, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "started", EvidenceJSON: mustJSON(map[string]any{"import_id": record.ImportID, "plan_id": record.PlanID, "from_commit": from, "target_commit": target, "git_state": state.Digest, "observation_revision": observationRevision}), RecordedAt: preapplyAt}, preapplyAt); err != nil {
		return syncStoreError(stderr, command, err)
	}
	resourceRoot := cfg.Resources[s.Resource].Root
	if err := syncimport.Apply(resourceRoot, effects, writes); err != nil {
		return finishImportRecovering(stdout, stderr, store, client, job, owner, resourceRoot, s.ContentRef, from, target, effects, err)
	}
	if err := client.ApplyImportIndex(requestCtx(), s.ContentRef, from, target, writes, deletions); err != nil {
		return finishImportRecovering(stdout, stderr, store, client, job, owner, resourceRoot, s.ContentRef, from, target, effects, err)
	}
	effectState, err := syncimport.Inspect(resourceRoot, effects)
	refNow, refErr := client.ResolveRef(requestCtx(), s.ContentRef)
	if err != nil || effectState != syncimport.AllAfter || refErr != nil || refNow != target {
		return finishImportRecovering(stdout, stderr, store, client, job, owner, resourceRoot, s.ContentRef, from, target, effects, errors.New("post-apply file or ref state is not exact"))
	}
	terminal := time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.FinishImportJob(requestCtx(), job.JobID, owner, s.Resource, job.Fence, observationRevision, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "applied", EvidenceJSON: mustJSON(map[string]any{"import_id": record.ImportID, "target_commit": target, "effect_count": len(effects)}), RecordedAt: terminal}, terminal); err != nil {
		recoveredAt := time.Now().UTC().Format(time.RFC3339Nano)
		if recoveredErr := store.FinishRecoveredImportJob(requestCtx(), job.JobID, owner, s.Resource, job.Fence, observationRevision, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "applied", EvidenceJSON: mustJSON(map[string]any{"import_id": record.ImportID, "target_commit": target, "effect_count": len(effects), "advanced_observation_recovery": true}), RecordedAt: recoveredAt}, recoveredAt); recoveredErr != nil {
			return finishImportRecovering(stdout, stderr, store, client, job, owner, resourceRoot, s.ContentRef, from, target, effects, errors.Join(err, recoveredErr))
		}
	}
	return reconcileEnvelope(stdout, "applied", "none", map[string]any{"import_id": record.ImportID, "from_commit": from, "target_commit": target, "paths": len(effects), "idempotent": false, "side_effects": []string{"live_tree_updated", "index_updated", "local_content_ref_updated", "import_effects_published", "path_facts_updated", "state_committed"}})
}

func reconcileEvidenceID(from, target, membership string, commits []string) string {
	values := append([]string{from, target, membership}, commits...)
	h := sha256.New()
	for _, value := range values {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	return "history-evidence-" + hex.EncodeToString(h.Sum(nil))[:32]
}

func recoverPendingImport(stdout, stderr io.Writer, cfg *config.Config, s *config.Sync, revision string, store *sqlite.Store, client *gitlocal.Client, membership, remoteTarget string) (bool, int) {
	jobs, err := store.LoadUnresolvedSyncJobs(requestCtx(), s.GroupID, "import")
	if err != nil {
		return true, syncStoreError(stderr, "sync reconcile", err)
	}
	for _, job := range jobs {
		if job.State == "deferred" || job.State == "blocked" || job.State == "requested" || job.State == "fetched" || (job.State == "validated" && job.ClaimOwner == "") {
			continue
		}
		if job.State != "validated" && job.State != "applying" && job.State != "recovering" && job.State != "uncertain" {
			continue
		}
		record, decodeErr := syncrecords.DecodeImport([]byte(job.PayloadJSON))
		if decodeErr != nil || record.TargetCommit != remoteTarget || record.MembershipRevision != membership {
			_, _ = store.HoldSyncControl(requestCtx(), s.GroupID, "recovery_required", revision, time.Now().UTC().Format(time.RFC3339Nano))
			return true, reconcileEnvelopeCode(stdout, "uncertain", "partial_effect", map[string]any{"import_job_id": job.JobID, "detail": "stored recovery plan no longer matches the approved remote"}, 13)
		}
		if job.ClaimOwner != "" {
			expires, _ := time.Parse(time.RFC3339Nano, job.ClaimExpiresAt)
			if expires.After(time.Now().UTC()) {
				return true, reconcileEnvelopeCode(stdout, "blocked", "partial_effect", map[string]any{"import_job_id": job.JobID, "detail": "an earlier import attempt still has a live claim"}, 14)
			}
		}
		if record.ControllerOnly {
			stateNow, stateErr := client.InspectImport(requestCtx(), s.ContentRef)
			refNow, refErr := client.ResolveRef(requestCtx(), s.ContentRef)
			if stateErr == nil && refErr == nil && stateNow.ActiveOperation == "" && refNow == record.TargetCommit {
				now := time.Now().UTC().Format(time.RFC3339Nano)
				if err := store.FinishRecoveredControllerImportJob(requestCtx(), job.JobID, job.Fence, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "applied", EvidenceJSON: mustJSON(map[string]any{"controller_only": true, "recovered": true, "target_commit": record.TargetCommit}), RecordedAt: now}, now); err != nil {
					return true, syncStoreError(stderr, "sync reconcile", err)
				}
				checkpointReconciled := false
				if control, loadErr := store.LoadSyncControl(requestCtx(), s.GroupID); loadErr == nil && control.State == "blocked" && contentHeadAddsCheckpoint(client, record.TargetCommit) {
					if _, reconcileErr := store.ReconcileSyncControlCheckpoint(requestCtx(), s.GroupID, control.Revision, revision, record.TargetCommit, now); reconcileErr != nil {
						return true, syncStoreError(stderr, "sync reconcile", reconcileErr)
					}
					checkpointReconciled = true
				}
				return true, reconcileEnvelope(stdout, "recovered", "none", map[string]any{"import_id": record.ImportID, "target_commit": record.TargetCommit, "controller_only": true, "checkpoint_reconciled": checkpointReconciled, "side_effects": []string{"controller_state_confirmed", "state_committed"}})
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			if stateErr == nil && refErr == nil && stateNow.ActiveOperation == "" && stateNow.Digest == record.ExpectedGitStateDigest && refNow == record.FromCommit {
				if err := store.PrepareControllerImportRecovery(requestCtx(), job.JobID, job.Fence, "validated", sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_not_started", EvidenceJSON: mustJSON(map[string]any{"controller_only": true, "ref": refNow, "git_state": stateNow.Digest}), RecordedAt: now}, now); err != nil {
					return true, syncStoreError(stderr, "sync reconcile", err)
				}
				return false, 0
			}
			if job.State != "uncertain" {
				if err := store.PrepareControllerImportRecovery(requestCtx(), job.JobID, job.Fence, "uncertain", sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_unknown", EvidenceJSON: mustJSON(map[string]any{"controller_only": true, "ref": refNow, "active_operation": stateNow.ActiveOperation}), RecordedAt: now}, now); err != nil {
					return true, syncStoreError(stderr, "sync reconcile", err)
				}
			}
			_, _ = store.HoldSyncControl(requestCtx(), s.GroupID, "recovery_required", revision, now)
			return true, reconcileEnvelopeCode(stdout, "uncertain", "partial_effect", map[string]any{"import_job_id": job.JobID, "detail": "controller-only recovery found an unexplained index, worktree, or ref state"}, 13)
		}
		if job.State == "uncertain" {
			_, _ = store.HoldSyncControl(requestCtx(), s.GroupID, "recovery_required", revision, time.Now().UTC().Format(time.RFC3339Nano))
			return true, reconcileEnvelopeCode(stdout, "uncertain", "partial_effect", map[string]any{"import_job_id": job.JobID, "detail": "an earlier import has unresolved partial effects"}, 13)
		}
		root := cfg.Resources[s.Resource].Root
		effectState, inspectErr := syncimport.Inspect(root, record.Paths)
		refNow, refErr := client.ResolveRef(requestCtx(), s.ContentRef)
		if inspectErr != nil || refErr != nil || (refNow != record.FromCommit && refNow != record.TargetCommit) {
			effectState = syncimport.Unexpected
		}
		if job.State == "validated" {
			now := time.Now().UTC().Format(time.RFC3339Nano)
			outcome := "effect_unknown"
			if effectState == syncimport.AllBefore && refNow == record.FromCommit {
				stateNow, stateErr := client.InspectImport(requestCtx(), s.ContentRef)
				if stateErr == nil && stateNow.Digest == record.ExpectedGitStateDigest {
					outcome = "effect_not_started"
				}
			}
			if err := store.ReconcileExpiredSyncClaim(requestCtx(), job.JobID, job.Fence, outcome, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: outcome, EvidenceJSON: mustJSON(map[string]any{"path_state": effectState, "ref": refNow}), RecordedAt: now}, now); err != nil {
				return true, syncStoreError(stderr, "sync reconcile", err)
			}
			if outcome == "effect_not_started" {
				return false, 0
			}
			_, _ = store.HoldSyncControl(requestCtx(), s.GroupID, "recovery_required", revision, now)
			return true, reconcileEnvelopeCode(stdout, "uncertain", "partial_effect", map[string]any{"import_job_id": job.JobID, "detail": "an expired pre-apply claim no longer matches its no-effect fence"}, 13)
		}
		if effectState == syncimport.Unexpected {
			if job.ClaimOwner != "" {
				now := time.Now().UTC().Format(time.RFC3339Nano)
				if err := store.PrepareImportRecovery(requestCtx(), job.JobID, job.Fence, "uncertain", sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_unknown", EvidenceJSON: mustJSON(map[string]any{"path_state": effectState, "ref": refNow}), RecordedAt: now}, now); err != nil {
					return true, syncStoreError(stderr, "sync reconcile", err)
				}
			}
			_, _ = store.HoldSyncControl(requestCtx(), s.GroupID, "recovery_required", revision, time.Now().UTC().Format(time.RFC3339Nano))
			return true, reconcileEnvelopeCode(stdout, "uncertain", "partial_effect", map[string]any{"import_job_id": job.JobID, "detail": "import recovery found independent or unexplained path state"}, 13)
		}
		stateNow, stateErr := client.InspectImport(requestCtx(), s.ContentRef)
		if effectState == syncimport.AllBefore && refNow == record.FromCommit && stateErr == nil && stateNow.Digest == record.ExpectedGitStateDigest {
			if job.ClaimOwner != "" {
				now := time.Now().UTC().Format(time.RFC3339Nano)
				if err := store.PrepareImportRecovery(requestCtx(), job.JobID, job.Fence, "validated", sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_not_started", EvidenceJSON: mustJSON(map[string]any{"path_state": effectState, "ref": refNow}), RecordedAt: now}, now); err != nil {
					return true, syncStoreError(stderr, "sync reconcile", err)
				}
			}
			return false, 0
		}
		if job.ClaimOwner != "" {
			now := time.Now().UTC().Format(time.RFC3339Nano)
			if err := store.PrepareImportRecovery(requestCtx(), job.JobID, job.Fence, "recovering", sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_unknown", EvidenceJSON: mustJSON(map[string]any{"path_state": effectState, "ref": refNow, "recoverable": true}), RecordedAt: now}, now); err != nil {
				return true, syncStoreError(stderr, "sync reconcile", err)
			}
			job.ClaimOwner = ""
		}
		if !config.SyncAcknowledgementCurrent(cfg, localSyncIncarnation(cfg)) {
			return true, reconcileEnvelope(stdout, "deferred", "acknowledgement_stale", map[string]any{"import_job_id": job.JobID, "detail": "recovery requires the current cooperative import acknowledgement"})
		}
		idle, idleErr := store.ResourceWritersIdle(requestCtx(), s.Resource)
		recoveryGit, gitErr := client.InspectImport(requestCtx(), s.ContentRef)
		if idleErr != nil || gitErr != nil || !idle || recoveryGit.ActiveOperation != "" {
			detail := "recovery is waiting for idle participating writers and stable Git state"
			if recoveryGit.ActiveOperation != "" {
				detail = "recovery is waiting for Git state: " + recoveryGit.ActiveOperation
			}
			reason := "git_unstable"
			if idleErr == nil && !idle {
				reason = "resource_busy"
			}
			return true, reconcileEnvelope(stdout, "deferred", reason, map[string]any{"import_job_id": job.JobID, "detail": detail})
		}
		owner := randomSyncID("import-recovery-owner")
		claimAt := time.Now().UTC()
		claimed, err := store.ClaimSyncJob(requestCtx(), job.JobID, owner, revision, claimAt.Format(time.RFC3339Nano), claimAt.Add(time.Duration(s.Bounds.SubprocessSeconds*4+60)*time.Second).Format(time.RFC3339Nano))
		if err != nil {
			return true, syncStoreError(stderr, "sync reconcile", err)
		}
		targetFiles, err := client.ReadContentFiles(requestCtx(), record.TargetCommit)
		if err != nil {
			return true, finishImportUncertain(stdout, stderr, store, claimed, owner, err)
		}
		writes := map[string][]byte{}
		var deletions []string
		for _, effect := range record.Paths {
			if effect.After == "absent" {
				deletions = append(deletions, effect.Path)
				continue
			}
			raw, ok := targetFiles[effect.Path]
			if !ok || syncimport.Digest(raw) != effect.After {
				return true, finishImportUncertain(stdout, stderr, store, claimed, owner, errors.New("recovery target bytes do not match the stored import plan"))
			}
			writes[effect.Path] = raw
		}
		started := time.Now().UTC().Format(time.RFC3339Nano)
		if err := store.BeginImportApply(requestCtx(), claimed.JobID, owner, s.Resource, claimed.Fence, record.Paths, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: claimed.JobID, Fence: claimed.Fence, Phase: "import", Outcome: "started", EvidenceJSON: mustJSON(map[string]any{"recovery_of_fence": job.Fence, "path_state": effectState}), RecordedAt: started}, started); err != nil {
			return true, syncStoreError(stderr, "sync reconcile", err)
		}
		if err := syncimport.Apply(root, record.Paths, writes); err != nil {
			return true, finishImportUncertain(stdout, stderr, store, claimed, owner, err)
		}
		if err := client.ApplyImportIndex(requestCtx(), s.ContentRef, record.FromCommit, record.TargetCommit, writes, deletions); err != nil {
			return true, finishImportUncertain(stdout, stderr, store, claimed, owner, err)
		}
		if state, err := syncimport.Inspect(root, record.Paths); err != nil || state != syncimport.AllAfter {
			return true, finishImportUncertain(stdout, stderr, store, claimed, owner, errors.New("recovered import did not reach its exact target effects"))
		}
		terminal := time.Now().UTC().Format(time.RFC3339Nano)
		if err := store.FinishRecoveredImportJob(requestCtx(), claimed.JobID, owner, s.Resource, claimed.Fence, record.ResourceObservationRevision, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: claimed.JobID, Fence: claimed.Fence, Phase: "import", Outcome: "applied", EvidenceJSON: mustJSON(map[string]any{"recovered": true, "target_commit": record.TargetCommit}), RecordedAt: terminal}, terminal); err != nil {
			return true, finishImportUncertain(stdout, stderr, store, claimed, owner, err)
		}
		return true, reconcileEnvelope(stdout, "recovered", "none", map[string]any{"import_id": record.ImportID, "target_commit": record.TargetCommit, "side_effects": []string{"partial_import_reconciled", "import_effects_published", "path_facts_updated", "state_committed"}})
	}
	return false, 0
}

func contentHeadAddsCheckpoint(client *gitlocal.Client, head string) bool {
	parents, err := client.CommitParents(requestCtx(), head)
	if err != nil || len(parents) != 1 {
		return false
	}
	current, err := client.ReadTreePrefix(requestCtx(), head, ".agent-dispatch-sync/checkpoints/")
	if err != nil {
		return false
	}
	prior, err := client.ReadTreePrefix(requestCtx(), parents[0], ".agent-dispatch-sync/checkpoints/")
	if err != nil {
		return false
	}
	return len(current) == len(prior)+1
}

func verifyImportBase(client *gitlocal.Client, history syncmembership.History, cfg *config.Config, s *config.Sync, from string, limit int) error {
	current := from
	for inspected := 0; inspected < limit; inspected++ {
		if err := verifyContentHead(client, history, cfg, s, current); err != nil {
			return err
		}
		if contentHeadAddsCheckpoint(client, current) {
			return nil
		}
		parents, err := client.CommitParents(requestCtx(), current)
		if err != nil || len(parents) != 1 {
			return errors.New("import base is not descended from a linear administrator checkpoint")
		}
		current = parents[0]
	}
	return gitlocal.ErrHistoryBound
}

func blockReconcile(stdout, stderr io.Writer, store *sqlite.Store, group, revision, controlReason, resultReason string, cause error) int {
	_, err := store.HoldSyncControl(requestCtx(), group, controlReason, revision, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return syncStoreError(stderr, "sync reconcile", err)
	}
	exit := 30
	if controlReason == "conflict" {
		exit = 14
	}
	return reconcileEnvelopeCode(stdout, "blocked", resultReason, map[string]any{"detail": cause.Error()}, exit)
}

func finishImportDisposition(stdout, stderr io.Writer, store *sqlite.Store, job sqlite.SyncJobRow, owner, state, outcome, detail, reason string) int {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, state, state == "deferred", sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: outcome, EvidenceJSON: mustJSON(map[string]any{"detail": detail, "reason": reason}), RecordedAt: now}, now); err != nil {
		return syncStoreError(stderr, "sync reconcile", err)
	}
	return reconcileEnvelope(stdout, state, reason, map[string]any{"detail": detail})
}

func finishControllerImport(stdout, stderr io.Writer, store *sqlite.Store, client *gitlocal.Client, job sqlite.SyncJobRow, owner, contentRef, from, target, expectedDigest string, cause error) int {
	state, inspectErr := client.InspectImport(requestCtx(), contentRef)
	ref, refErr := client.ResolveRef(requestCtx(), contentRef)
	if inspectErr == nil && refErr == nil && state.ActiveOperation == "" && ref == target {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, "applied", true, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "applied", EvidenceJSON: mustJSON(map[string]any{"controller_only": true, "target_commit": target, "recovered_after_error": cause.Error()}), RecordedAt: now}, now); err != nil {
			return syncStoreError(stderr, "sync reconcile", err)
		}
		return reconcileEnvelope(stdout, "applied", "none", map[string]any{"import_job_id": job.JobID, "target_commit": target, "controller_only": true, "recovered": true})
	}
	if inspectErr == nil && refErr == nil && state.ActiveOperation == "" && state.Digest == expectedDigest && ref == from {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, "validated", false, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "effect_not_started", EvidenceJSON: mustJSON(map[string]any{"controller_only": true, "detail": cause.Error(), "ref": ref}), RecordedAt: now}, now); err != nil {
			return syncStoreError(stderr, "sync reconcile", err)
		}
		return reconcileEnvelopeCode(stdout, "deferred", "git_unstable", map[string]any{"import_job_id": job.JobID, "detail": cause.Error(), "ref": ref, "effect_not_started": true}, 10)
	}
	return finishImportUncertain(stdout, stderr, store, job, owner, cause)
}

func finishImportUncertain(stdout, stderr io.Writer, store *sqlite.Store, job sqlite.SyncJobRow, owner string, cause error) int {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, "uncertain", false, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "effect_unknown", EvidenceJSON: mustJSON(map[string]any{"detail": cause.Error()}), RecordedAt: now}, now); err != nil {
		return syncStoreError(stderr, "sync reconcile", err)
	}
	control, loadErr := store.LoadSyncControl(requestCtx(), job.GroupID)
	if loadErr != nil {
		return syncStoreError(stderr, "sync reconcile", loadErr)
	}
	if _, err := store.HoldSyncControl(requestCtx(), job.GroupID, "recovery_required", control.ConfigRevision, now); err != nil {
		return syncStoreError(stderr, "sync reconcile", err)
	}
	return reconcileEnvelopeCode(stdout, "uncertain", "partial_effect", map[string]any{"detail": cause.Error()}, 13)
}

func finishImportRecovering(stdout, stderr io.Writer, store *sqlite.Store, client *gitlocal.Client, job sqlite.SyncJobRow, owner, root, contentRef, from, target string, effects []syncrecords.ImportPath, cause error) int {
	effectState, inspectErr := syncimport.Inspect(root, effects)
	refNow, refErr := client.ResolveRef(requestCtx(), contentRef)
	if inspectErr != nil || refErr != nil || effectState == syncimport.Unexpected || (refNow != from && refNow != target) {
		return finishImportUncertain(stdout, stderr, store, job, owner, cause)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, "recovering", false, sqlite.SyncJournalEntry{JournalID: randomSyncID("import-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "effect_unknown", EvidenceJSON: mustJSON(map[string]any{"detail": cause.Error(), "path_state": effectState, "ref": refNow}), RecordedAt: now}, now); err != nil {
		return syncStoreError(stderr, "sync reconcile", err)
	}
	return reconcileEnvelopeCode(stdout, "recovering", "partial_effect", map[string]any{"import_job_id": job.JobID, "detail": cause.Error(), "path_state": effectState, "ref": refNow}, 13)
}

func reconcileEnvelope(stdout io.Writer, state, reason string, extra map[string]any) int {
	result := map[string]any{"schema_version": "agent-dispatch.sync-reconcile-result/v1", "state": state, "reason": reason}
	for key, value := range extra {
		result[key] = value
	}
	if _, ok := result["side_effects"]; !ok {
		result["side_effects"] = []string{}
	}
	return writeEnvelope(stdout, "sync reconcile", result)
}

func reconcileEnvelopeCode(stdout io.Writer, state, reason string, extra map[string]any, exit int) int {
	if code := reconcileEnvelope(stdout, state, reason, extra); code != 0 {
		return code
	}
	return exit
}

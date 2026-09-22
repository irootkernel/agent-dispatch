package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/adapters/secretresolver"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/syncmembership"
	"github.com/irootkernel/agent-dispatch/internal/app/syncpublication"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func runSyncCheckpoint(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "sync checkpoint", "checkpoint requires plan or apply")
	}
	switch args[0] {
	case "plan":
		return runSyncCheckpointPlan(args[1:], stdout, stderr)
	case "apply":
		return runSyncCheckpointApply(args[1:], stdout, stderr)
	default:
		return usageError(stderr, "sync checkpoint", "checkpoint requires plan or apply")
	}
}

func runSyncCheckpointPlan(args []string, stdout, stderr io.Writer) int {
	const command = "sync checkpoint plan"
	v, ok := parseClosedSyncFlags(args, map[string]bool{"--group": true, "--target-commit": true, "--kind": true})
	if !ok || v["--group"] == "" || v["--target-commit"] == "" || v["--kind"] == "" {
		return usageError(stderr, command, "plan requires --group GROUP --target-commit OID --kind KIND and accepts --output json")
	}
	cfg, s, code := loadMembershipConfig(command, v["--group"], false, stderr)
	if code != 0 {
		return code
	}
	client, err := membershipGitClient(cfg, s)
	if err != nil {
		return syncMembershipError(stderr, command, err, 3)
	}
	membership, predecessor, digest, err := checkpointBindings(client, cfg, s, v["--target-commit"])
	if err != nil {
		return checkpointBindingError(stderr, command, err)
	}
	if v["--kind"] == "initial_baseline" {
		if v["--target-commit"] != predecessor {
			return syncMembershipError(stderr, command, errors.New("initial baseline must bind the current content predecessor"), 14)
		}
		has, inspectErr := client.TreeHasPrefix(requestCtx(), predecessor, ".agent-dispatch-sync/")
		if inspectErr != nil {
			return syncMembershipError(stderr, command, inspectErr, 14)
		}
		if has {
			return syncMembershipError(stderr, command, errors.New("initial baseline checkpoint already exists"), 14)
		}
	}
	plan, err := syncrecords.NewCheckpointPlan(s.GroupID, v["--kind"], v["--target-commit"], digest, config.SyncScopeDigest(cfg, s.Resource), config.SyncContractDigest(), membership, predecessor, s.AdministratorKey)
	if err != nil {
		return syncMembershipError(stderr, command, err, 14)
	}
	return writeEnvelope(stdout, command, map[string]any{"schema_version": "agent-dispatch.sync-checkpoint-plan-result/v1", "plan": plan, "side_effects": []string{}})
}

func runSyncCheckpointApply(args []string, stdout, stderr io.Writer) int {
	const command = "sync checkpoint apply"
	v, ok := parseClosedSyncFlags(args, map[string]bool{"--group": true, "--plan": true})
	if !ok || v["--group"] == "" || v["--plan"] == "" {
		return usageError(stderr, command, "apply requires --group GROUP --plan FILE and accepts --output json")
	}
	cfg, s, code := loadMembershipConfig(command, v["--group"], true, stderr)
	if code != 0 {
		return code
	}
	raw, err := readMembershipPlanFile(v["--plan"])
	if err != nil {
		writeError(stderr, command, "sync_payload_invalid", "input_rejected", err.Error())
		return 4
	}
	plan, err := syncrecords.DecodeCheckpointPlan(raw)
	if err != nil {
		writeError(stderr, command, "sync_payload_invalid", "input_rejected", err.Error())
		return 4
	}
	if plan.GroupID != s.GroupID {
		return syncMembershipError(stderr, command, errors.New("checkpoint plan group does not match command"), 14)
	}
	client, err := membershipGitClient(cfg, s)
	if err != nil {
		return syncMembershipError(stderr, command, err, 3)
	}
	configRevision, _ := config.SyncRevision(cfg)
	now := time.Now().UTC()
	_, store, openCode := openOperatorStore(command, "", stderr)
	if openCode != 0 {
		return openCode
	}
	defer store.Close()
	control, err := store.EnsureSyncControl(requestCtx(), s.GroupID, configRevision, now.Format(time.RFC3339Nano))
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	if !checkpointControlMatches(plan.ProposedCheckpoint.Kind, control) {
		return syncMembershipError(stderr, command, fmt.Errorf("checkpoint kind %s does not match sync control state %s/%s", plan.ProposedCheckpoint.Kind, control.State, control.Reason), 14)
	}
	if recovered, result := recoverCheckpointApply(stdout, stderr, store, client, cfg, s, plan); recovered {
		return result
	}
	membership, predecessor, digest, err := checkpointBindings(client, cfg, s, plan.ProposedCheckpoint.TargetCommit)
	if err != nil {
		return checkpointBindingError(stderr, command, err)
	}
	if plan.ProposedCheckpoint.Kind == "initial_baseline" {
		if plan.ProposedCheckpoint.TargetCommit != predecessor {
			return syncMembershipError(stderr, command, errors.New("initial baseline must bind the current content predecessor"), 14)
		}
		has, inspectErr := client.TreeHasPrefix(requestCtx(), predecessor, ".agent-dispatch-sync/")
		if inspectErr != nil {
			return syncMembershipError(stderr, command, inspectErr, 14)
		}
		if has {
			return syncMembershipError(stderr, command, errors.New("initial baseline checkpoint already exists"), 14)
		}
	}
	rebuilt, err := syncrecords.NewCheckpointPlan(s.GroupID, plan.ProposedCheckpoint.Kind, plan.ProposedCheckpoint.TargetCommit, digest, config.SyncScopeDigest(cfg, s.Resource), config.SyncContractDigest(), membership, predecessor, s.AdministratorKey)
	if err != nil || !reflect.DeepEqual(rebuilt, plan) {
		return syncMembershipError(stderr, command, errors.New("checkpoint plan no longer matches current membership, predecessor, and target snapshot"), 14)
	}
	canonical, _ := syncrecords.CanonicalCheckpointPlan(plan)
	job, reused, err := store.AdmitSyncJob(requestCtx(), sqlite.SyncJobInput{JobID: randomSyncID("checkpoint-job"), GroupID: s.GroupID, Kind: "checkpoint", LogicalKey: plan.PlanID, InitialState: "planned", PayloadJSON: string(canonical), ConfigRevision: configRevision, QueueLimit: s.Bounds.Queue, Now: now.Format(time.RFC3339Nano)})
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	journals, err := store.LoadSyncJournals(requestCtx(), job.JobID)
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	if job.State == "applied" {
		return checkpointResult(stdout, plan, journalCandidate(journals), true)
	}
	if reused && job.ClaimOwner != "" {
		return syncMembershipError(stderr, command, errors.New("checkpoint is still claimed by another invocation"), 14)
	}
	owner := randomSyncID("checkpoint-owner")
	claimAt := time.Now().UTC()
	job, err = store.ClaimSyncAdministrationJob(requestCtx(), job.JobID, owner, configRevision, claimAt.Format(time.RFC3339Nano), claimAt.Add(time.Duration(s.Bounds.SubprocessSeconds*5+60)*time.Second).Format(time.RFC3339Nano))
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	candidate := journalCandidate(journals)
	if candidate == "" {
		cpRaw, _ := syncrecords.CanonicalCheckpoint(plan.ProposedCheckpoint)
		targetFiles, readErr := client.ReadContentFiles(requestCtx(), plan.ProposedCheckpoint.TargetCommit)
		if readErr != nil {
			return finishCheckpointFailure(stderr, store, job, owner, "planned", "effect_not_started", readErr.Error())
		}
		predecessorFiles, readErr := client.ReadContentFiles(requestCtx(), predecessor)
		if readErr != nil {
			return finishCheckpointFailure(stderr, store, job, owner, "planned", "effect_not_started", readErr.Error())
		}
		if policyErr := syncpublication.ValidateTransition(cfg, s.Resource, predecessorFiles, targetFiles); policyErr != nil {
			return finishCheckpointFailure(stderr, store, job, owner, "planned", "effect_not_started", "checkpoint target "+policyErr.Error())
		}
		files := cloneFiles(targetFiles)
		files[".agent-dispatch-sync/checkpoints/"+plan.ProposedCheckpoint.CheckpointID+".json"] = cpRaw
		files[".agent-dispatch-sync/checkpoint-plans/"+plan.PlanID+".json"] = canonical
		tree, treeErr := client.SnapshotTreeChanges(requestCtx(), predecessor, files, missingPaths(predecessorFiles, targetFiles))
		if treeErr != nil {
			return finishCheckpointFailure(stderr, store, job, owner, "planned", "effect_not_started", treeErr.Error())
		}
		ref, parseErr := config.ParseSecretRef(s.AdministratorSigningKeyRef)
		if parseErr != nil {
			return finishCheckpointFailure(stderr, store, job, owner, "planned", "effect_not_started", "administrator signing key reference is unavailable")
		}
		privateKey, resolveErr := secretresolver.Resolve(requestCtx(), ref)
		if resolveErr != nil {
			return finishCheckpointFailure(stderr, store, job, owner, "planned", "effect_not_started", "administrator signing key could not be resolved")
		}
		candidate, err = client.CreateSignedContentCommit(requestCtx(), tree, []byte(privateKey), predecessor, claimAt, "administrator", "Apply "+plan.ProposedCheckpoint.CheckpointID)
		privateKey = ""
		if err != nil {
			return finishCheckpointFailure(stderr, store, job, owner, "planned", "effect_not_started", "checkpoint signing failed")
		}
		if err := client.VerifySSHSignature(requestCtx(), candidate, s.AdministratorKey); err != nil {
			return finishCheckpointFailure(stderr, store, job, owner, "planned", "effect_not_started", "checkpoint administrator signature was not pinned")
		}
		signedAt := time.Now().UTC().Format(time.RFC3339Nano)
		if err := store.AdvanceSyncJob(requestCtx(), job.JobID, owner, job.Fence, "applying", sqlite.SyncJournalEntry{JournalID: randomSyncID("checkpoint-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "checkpoint", Outcome: "signed", EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "plan_id": plan.PlanID}), RecordedAt: signedAt}, signedAt); err != nil {
			return syncStoreError(stderr, command, err)
		}
	} else if err := client.VerifySSHSignature(requestCtx(), candidate, s.AdministratorKey); err != nil {
		return finishCheckpointFailure(stderr, store, job, owner, "blocked", "blocked", "reused checkpoint candidate signature was not pinned")
	}
	remoteNow, remoteErr := client.RemoteRef(requestCtx(), s.RemoteName, s.ContentRef, s.RemoteRepositoryDigest)
	if remoteErr != nil || remoteNow != candidate {
		push := client.PushFastForward(requestCtx(), s.RemoteName, s.ContentRef, candidate, predecessor, s.RemoteRepositoryDigest)
		switch classifySyncPush(push.State, push.RemoteOID, predecessor, candidate, push.Underlying) {
		case syncPushTrust:
			return finishClaimedSyncTrustFailure(stderr, command, store, job, owner, "applying", "checkpoint", push.Underlying, map[string]any{"candidate": candidate})
		case syncPushRetryable:
			return finishCheckpointRetryable(stderr, store, job, owner, candidate, push.RemoteOID, push.State)
		case syncPushConflict:
			held, err := store.HoldSyncControl(requestCtx(), s.GroupID, "conflict", configRevision, time.Now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				return syncStoreError(stderr, command, err)
			}
			if held.Reason != "conflict" {
				return finishCheckpointBlockedByHold(stdout, stderr, store, job, owner, plan, candidate, held, push.State)
			}
			return finishCheckpointFailure(stderr, store, job, owner, "blocked", "blocked", fmt.Sprintf("checkpoint push %s; review history and create a conflict-resolution checkpoint plan", push.State))
		case syncPushUnknown:
			return finishCheckpointFailure(stderr, store, job, owner, "uncertain", "effect_unknown", fmt.Sprintf("checkpoint push %s", push.State))
		}
	}
	if err := client.UpdateRefExpected(requestCtx(), s.ContentRef, candidate, predecessor); err != nil {
		local, _ := client.ResolveRef(requestCtx(), s.ContentRef)
		if local != candidate {
			return finishCheckpointFailure(stderr, store, job, owner, "uncertain", "effect_unknown", "remote confirmed but local content ref update failed")
		}
	}
	terminal := time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, "applied", sqlite.SyncJobResolve, sqlite.SyncJournalEntry{JournalID: randomSyncID("checkpoint-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "checkpoint", Outcome: "applied", EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "remote": candidate, "push_state": gitlocal.PushConfirmed, "reason": "none", "plan_id": plan.PlanID}), RecordedAt: terminal}, terminal); err != nil {
		return syncStoreError(stderr, command, err)
	}
	return checkpointResult(stdout, plan, candidate, false)
}

func finishCheckpointBlockedByHold(stdout, stderr io.Writer, store *sqlite.Store, job sqlite.SyncJobRow, owner string, plan syncrecords.CheckpointPlan, candidate string, control sqlite.SyncControlRow, pushState gitlocal.PushState) int {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, "blocked", sqlite.SyncJobKeepUnresolved, sqlite.SyncJournalEntry{JournalID: randomSyncID("checkpoint-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "checkpoint", Outcome: "blocked", EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "push_state": pushState, "reason": control.Reason}), RecordedAt: now}, now); err != nil {
		return syncStoreError(stderr, "sync checkpoint apply", err)
	}
	return checkpointBlockedResult(stdout, plan, candidate, control)
}

// recoverCheckpointApply converges an already signed checkpoint after process
// loss. It either settles the remote-confirmed candidate or records that the
// remote stayed at the reviewed predecessor so the same candidate can resume.
func recoverCheckpointApply(stdout, stderr io.Writer, store *sqlite.Store, client *gitlocal.Client, cfg *config.Config, s *config.Sync, plan syncrecords.CheckpointPlan) (bool, int) {
	job, found, err := store.FindSyncJob(requestCtx(), s.GroupID, "checkpoint", plan.PlanID)
	if err != nil {
		return true, syncStoreError(stderr, "sync checkpoint apply", err)
	}
	if !found {
		return false, 0
	}
	stored, err := syncrecords.DecodeCheckpointPlan([]byte(job.PayloadJSON))
	if err != nil || !reflect.DeepEqual(stored, plan) {
		return true, syncMembershipError(stderr, "sync checkpoint apply", errors.New("stored checkpoint plan does not match the reviewed plan"), 14)
	}
	if job.State == "blocked" && job.ResolvedAt != "" {
		return true, syncMembershipError(stderr, "sync checkpoint apply", errors.New("checkpoint job was already discharged by newer recovery evidence"), 14)
	}
	journals, err := store.LoadSyncJournals(requestCtx(), job.JobID)
	if err != nil {
		return true, syncStoreError(stderr, "sync checkpoint apply", err)
	}
	candidate := journalCandidate(journals)
	remote, err := client.RemoteRef(requestCtx(), s.RemoteName, s.ContentRef, s.RemoteRepositoryDigest)
	if err != nil {
		if errors.Is(err, gitlocal.ErrRemoteBinding) {
			return true, syncMembershipError(stderr, "sync checkpoint apply", err, 30)
		}
		return true, syncRetryableError(stderr, "sync checkpoint apply", fmt.Errorf("checkpoint recovery could not measure the approved remote content ref: %w", err))
	}
	now := time.Now().UTC()
	if job.State == "applied" {
		local, localErr := client.ResolveRef(requestCtx(), s.ContentRef)
		if candidate == "" || remote != candidate || localErr != nil || local != candidate {
			return true, syncMembershipError(stderr, "sync checkpoint apply", errors.New("applied checkpoint no longer matches the local and approved remote content refs"), 14)
		}
		if verifyErr := client.VerifySSHSignature(requestCtx(), candidate, s.AdministratorKey); verifyErr != nil {
			return true, syncMembershipError(stderr, "sync checkpoint apply", verifyErr, 30)
		}
		return true, checkpointResult(stdout, plan, candidate, true)
	}
	claimExpired := false
	if job.ClaimOwner != "" {
		expires, _ := time.Parse(time.RFC3339Nano, job.ClaimExpiresAt)
		if expires.After(now) {
			return true, syncMembershipError(stderr, "sync checkpoint apply", errors.New("checkpoint is still claimed by another invocation"), 14)
		}
		claimExpired = true
	}
	if candidate != "" && remote == candidate {
		membership, membershipErr := client.ResolveRef(requestCtx(), s.MembershipRef)
		if membershipErr != nil || membership != plan.ExpectedMembershipRevision {
			return true, syncMembershipError(stderr, "sync checkpoint apply", errors.New("checkpoint recovery membership revision is no longer current"), 30)
		}
		remoteMembership, remoteErr := client.RemoteRef(requestCtx(), s.RemoteName, s.MembershipRef, s.RemoteRepositoryDigest)
		if remoteErr != nil {
			if errors.Is(remoteErr, gitlocal.ErrRemoteBinding) {
				return true, syncMembershipError(stderr, "sync checkpoint apply", remoteErr, 30)
			}
			return true, syncRetryableError(stderr, "sync checkpoint apply", fmt.Errorf("membership ref could not be measured on the approved remote: %w", remoteErr))
		}
		if remoteMembership != membership {
			return true, syncMembershipError(stderr, "sync checkpoint apply", errors.New("membership ref is not current on the approved remote"), 30)
		}
		history, historyErr := syncmembership.LoadHistory(requestCtx(), client, membership, membershipBinding(cfg, s), s.Bounds.HistoryCommits)
		if historyErr != nil {
			return true, syncMembershipError(stderr, "sync checkpoint apply", historyErr, 30)
		}
		if verifyErr := verifyContentHead(client, history, cfg, s, candidate); verifyErr != nil {
			return true, syncMembershipError(stderr, "sync checkpoint apply", verifyErr, 30)
		}
		local, localErr := client.ResolveRef(requestCtx(), s.ContentRef)
		if localErr != nil {
			return true, syncMembershipError(stderr, "sync checkpoint apply", localErr, 14)
		}
		if local != candidate {
			if local != plan.ExpectedContentPredecessor {
				return true, syncMembershipError(stderr, "sync checkpoint apply", errors.New("local content ref moved during checkpoint recovery"), 14)
			}
			if updateErr := client.UpdateRefExpected(requestCtx(), s.ContentRef, candidate, plan.ExpectedContentPredecessor); updateErr != nil {
				return true, syncMembershipError(stderr, "sync checkpoint apply", fmt.Errorf("remote checkpoint is confirmed but the local content ref did not move: %w", updateErr), 13)
			}
		}
		nowText := now.Format(time.RFC3339Nano)
		if finishErr := store.FinishRecoveredCheckpointJob(requestCtx(), job.JobID, job.Fence, sqlite.SyncJournalEntry{JournalID: randomSyncID("checkpoint-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "checkpoint", Outcome: "applied", EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "remote": candidate, "push_state": gitlocal.PushConfirmed, "reason": "none", "plan_id": plan.PlanID, "recovered": true}), RecordedAt: nowText}, nowText); finishErr != nil {
			return true, syncStoreError(stderr, "sync checkpoint apply", finishErr)
		}
		return true, writeEnvelope(stdout, "sync checkpoint apply", map[string]any{"schema_version": "agent-dispatch.sync-checkpoint-apply-result/v1", "state": "applied", "plan_id": plan.PlanID, "checkpoint_id": plan.ProposedCheckpoint.CheckpointID, "content_revision": candidate, "recovered": true, "idempotent": true, "side_effects": []string{"local_content_ref_reconciled", "state_committed"}})
	}
	if remote != plan.ExpectedContentPredecessor {
		nowText := now.Format(time.RFC3339Nano)
		evidence := map[string]any{"candidate": candidate, "remote": remote, "base": plan.ExpectedContentPredecessor, "reason": "remote predecessor moved during checkpoint recovery"}
		if err := store.BlockSyncJobAfterRemoteMove(requestCtx(), job.JobID, job.Fence, sqlite.SyncJournalEntry{JournalID: randomSyncID("checkpoint-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "blocked", EvidenceJSON: mustJSON(evidence), RecordedAt: nowText}, nowText); err != nil {
			return true, syncStoreError(stderr, "sync checkpoint apply", err)
		}
		revision, _ := config.SyncRevision(cfg)
		held, holdErr := store.HoldSyncControl(requestCtx(), s.GroupID, "conflict", revision, nowText)
		if holdErr != nil {
			return true, syncStoreError(stderr, "sync checkpoint apply", holdErr)
		}
		if held.Reason != "conflict" {
			return true, checkpointBlockedResult(stdout, plan, candidate, held)
		}
		return true, syncMembershipError(stderr, "sync checkpoint apply", errors.New("checkpoint recovery found a moved remote content revision; review history and create a new conflict-resolution checkpoint plan"), 14)
	}
	nowText := now.Format(time.RFC3339Nano)
	journal := sqlite.SyncJournalEntry{JournalID: randomSyncID("checkpoint-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_not_started", EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "remote": remote, "plan_id": plan.PlanID}), RecordedAt: nowText}
	if job.State == "uncertain" {
		if err := store.ReconcileUncertainCheckpoint(requestCtx(), job.JobID, job.Fence, journal, nowText); err != nil {
			return true, syncStoreError(stderr, "sync checkpoint apply", err)
		}
	} else if job.State == "blocked" {
		if candidate == "" {
			return true, syncMembershipError(stderr, "sync checkpoint apply", errors.New("blocked checkpoint has no signed candidate to retry"), 14)
		}
		if err := client.VerifySSHSignature(requestCtx(), candidate, s.AdministratorKey); err != nil {
			return true, syncMembershipError(stderr, "sync checkpoint apply", err, 30)
		}
		if err := store.ReopenRejectedCheckpoint(requestCtx(), job.JobID, job.Fence, journal, nowText); err != nil {
			return true, syncStoreError(stderr, "sync checkpoint apply", err)
		}
	} else if claimExpired {
		if err := store.ReconcileExpiredSyncClaim(requestCtx(), job.JobID, job.Fence, "effect_not_started", journal, nowText); err != nil {
			return true, syncStoreError(stderr, "sync checkpoint apply", err)
		}
	}
	return false, 0
}

func checkpointBlockedResult(stdout io.Writer, plan syncrecords.CheckpointPlan, candidate string, control sqlite.SyncControlRow) int {
	if code := writeEnvelope(stdout, "sync checkpoint apply", map[string]any{
		"schema_version": "agent-dispatch.sync-checkpoint-apply-result/v1", "state": "blocked", "reason": control.Reason,
		"plan_id": plan.PlanID, "candidate_commit": candidate, "membership_mode": control.MembershipMode,
		"detail": "the remote move was recorded without replacing the existing stronger sync safety hold", "side_effects": []string{"state_committed"},
	}); code != 0 {
		return code
	}
	return syncControlHoldExitCode(control.MembershipMode, control.Reason)
}

func checkpointBindings(client *gitlocal.Client, cfg *config.Config, s *config.Sync, target string) (string, string, string, error) {
	ctx := requestCtx()
	membership, err := client.ResolveRef(ctx, s.MembershipRef)
	if err != nil {
		return "", "", "", fmt.Errorf("checkpoint requires a verified local membership ref: %w: %v", syncrecords.ErrInvalidRecord, err)
	}
	remoteMembership, err := client.RemoteRef(ctx, s.RemoteName, s.MembershipRef, s.RemoteRepositoryDigest)
	if err != nil {
		return "", "", "", fmt.Errorf("membership ref could not be measured on the approved remote: %w", err)
	}
	if remoteMembership != membership {
		return "", "", "", fmt.Errorf("membership ref is not current on the approved remote: %w", gitlocal.ErrPushRejected)
	}
	if _, err := syncmembership.LoadHistory(ctx, client, membership, membershipBinding(cfg, s), s.Bounds.HistoryCommits); err != nil {
		return "", "", "", fmt.Errorf("membership history is not trusted: %w: %v", syncrecords.ErrInvalidRecord, err)
	}
	predecessor, err := client.ResolveRef(ctx, s.ContentRef)
	if err != nil {
		return "", "", "", fmt.Errorf("checkpoint requires an existing content predecessor: %w: %v", gitlocal.ErrPushRejected, err)
	}
	remotePredecessor, err := client.RemoteRef(ctx, s.RemoteName, s.ContentRef, s.RemoteRepositoryDigest)
	if err != nil {
		return "", "", "", fmt.Errorf("content predecessor could not be measured on the approved remote: %w", err)
	}
	if remotePredecessor != predecessor {
		return "", "", "", fmt.Errorf("content predecessor is not current on the approved remote: %w", gitlocal.ErrPushRejected)
	}
	files, err := client.ReadContentFiles(ctx, target)
	if err != nil {
		return "", "", "", fmt.Errorf("checkpoint target is not readable: %w: %v", gitlocal.ErrPushRejected, err)
	}
	digest, err := snapshotDigestFromFiles(files)
	if err != nil {
		return "", "", "", err
	}
	return membership, predecessor, digest, nil
}

func checkpointBindingError(stderr io.Writer, command string, err error) int {
	switch {
	case errors.Is(err, gitlocal.ErrRemoteBinding), errors.Is(err, syncrecords.ErrInvalidRecord):
		return syncMembershipError(stderr, command, err, 30)
	case errors.Is(err, gitlocal.ErrPushRejected):
		return syncMembershipError(stderr, command, err, 14)
	default:
		return syncRetryableError(stderr, command, err)
	}
}

func snapshotDigestFromFiles(files map[string][]byte) (string, error) {
	records := make([]syncrecords.SnapshotFile, 0, len(files))
	for p, b := range files {
		sum := sha256.Sum256(b)
		records = append(records, syncrecords.SnapshotFile{Path: p, Digest: "sha256:" + hex.EncodeToString(sum[:])})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	return syncrecords.SnapshotDigest(records)
}

func checkpointControlMatches(kind string, control sqlite.SyncControlRow) bool {
	switch kind {
	case "initial_baseline":
		return control.State == "active"
	case "conflict_resolution":
		return control.State == "blocked" && control.Reason == "conflict"
	case "history_bound_exhausted":
		return control.State == "blocked" && (control.Reason == "trust_failure" || control.Reason == "recovery_required")
	default:
		return false
	}
}
func finishCheckpointFailure(stderr io.Writer, store *sqlite.Store, job sqlite.SyncJobRow, owner, state, outcome, reason string) int {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, state, sqlite.SyncJobKeepUnresolved, sqlite.SyncJournalEntry{JournalID: randomSyncID("checkpoint-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "checkpoint", Outcome: outcome, EvidenceJSON: mustJSON(map[string]any{"reason": reason}), RecordedAt: now}, now)
	if err != nil {
		return syncStoreError(stderr, "sync checkpoint apply", err)
	}
	code := 14
	if state == "uncertain" {
		code = 13
	}
	writeError(stderr, "sync checkpoint apply", map[bool]string{true: "sync_effect_unknown", false: "sync_precondition_failed"}[state == "uncertain"], map[bool]string{true: "acceptance_unknown", false: "conflict"}[state == "uncertain"], reason)
	return code
}

func finishCheckpointRetryable(stderr io.Writer, store *sqlite.Store, job sqlite.SyncJobRow, owner, candidate, remote string, pushState gitlocal.PushState) int {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	reason := "checkpoint push was rejected before the approved remote moved; rerun sync checkpoint apply"
	if pushState == gitlocal.PushNotStarted {
		reason = "checkpoint remote measurement failed before push started; rerun sync checkpoint apply"
	}
	if err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, "applying", sqlite.SyncJobKeepUnresolved, sqlite.SyncJournalEntry{
		JournalID: randomSyncID("checkpoint-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "checkpoint", Outcome: "effect_not_started",
		EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "remote": remote, "push_state": pushState, "reason": reason}), RecordedAt: now,
	}, now); err != nil {
		return syncStoreError(stderr, "sync checkpoint apply", err)
	}
	writeError(stderr, "sync checkpoint apply", "sync_retryable", "transient_local", reason)
	return 10
}
func checkpointResult(stdout io.Writer, p syncrecords.CheckpointPlan, candidate string, idempotent bool) int {
	return writeEnvelope(stdout, "sync checkpoint apply", map[string]any{"schema_version": "agent-dispatch.sync-checkpoint-apply-result/v1", "state": "applied", "plan_id": p.PlanID, "checkpoint_id": p.ProposedCheckpoint.CheckpointID, "content_revision": candidate, "idempotent": idempotent, "side_effects": func() []string {
		if idempotent {
			return []string{}
		}
		return []string{"git_objects_written", "remote_content_ref_updated", "local_content_ref_updated", "state_committed"}
	}()})
}

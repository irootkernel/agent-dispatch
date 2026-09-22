package cli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/adapters/secretresolver"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/syncmembership"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func runSyncMembership(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "sync membership", "membership requires plan or apply")
	}
	switch args[0] {
	case "plan":
		return runSyncMembershipPlan(args[1:], stdout, stderr)
	case "apply":
		return runSyncMembershipApply(args[1:], stdout, stderr)
	default:
		return usageError(stderr, "sync membership", "membership requires plan or apply")
	}
}

func runSyncMembershipPlan(args []string, stdout, stderr io.Writer) int {
	const command = "sync membership plan"
	group, change, instance, ok := syncMembershipPlanFlags(args)
	if !ok {
		return usageError(stderr, command, "plan requires --group GROUP --change KIND; non-bootstrap changes also require --instance INSTANCE; accepts --output json")
	}
	cfg, syncCfg, code := loadMembershipConfig(command, group, false, stderr)
	if code != 0 {
		return code
	}
	client, err := membershipGitClient(cfg, syncCfg)
	if err != nil {
		return syncMembershipError(stderr, command, err, 3)
	}
	binding := membershipBinding(cfg, syncCfg)
	var current *syncrecords.Membership
	var predecessor *string
	head, err := client.ResolveRef(requestCtx(), syncCfg.MembershipRef)
	if err == nil {
		history, loadErr := syncmembership.LoadHistory(requestCtx(), client, head, binding, syncCfg.Bounds.HistoryCommits)
		if loadErr != nil {
			return syncMembershipError(stderr, command, loadErr, 14)
		}
		member, found := history.Current()
		if !found {
			return syncMembershipError(stderr, command, errors.New("membership head has no verified record"), 14)
		}
		current, predecessor = &member, &head
	} else if !errors.Is(err, gitlocal.ErrMissingRef) {
		return syncMembershipError(stderr, command, err, 14)
	}
	plan, err := syncrecords.NewPlan(change, instance, current, predecessor, configuredMembers(syncCfg), binding)
	if err != nil {
		return syncMembershipError(stderr, command, err, 14)
	}
	return writeEnvelope(stdout, command, map[string]any{
		"schema_version": "agent-dispatch.sync-membership-plan-result/v1",
		"plan":           plan, "side_effects": []string{},
	})
}

func runSyncMembershipApply(args []string, stdout, stderr io.Writer) int {
	const command = "sync membership apply"
	group, planPath, expected, ok := syncMembershipApplyFlags(args)
	if !ok {
		return usageError(stderr, command, "apply requires --group GROUP --plan FILE --expected-membership-predecessor OID|none and accepts --output json")
	}
	cfg, syncCfg, code := loadMembershipConfig(command, group, true, stderr)
	if code != 0 {
		return code
	}
	raw, err := readMembershipPlanFile(planPath)
	if err != nil {
		writeError(stderr, command, "sync_payload_invalid", "input_rejected", err.Error())
		return 4
	}
	plan, err := syncrecords.DecodePlan(raw)
	if err != nil {
		writeError(stderr, command, "sync_payload_invalid", "input_rejected", err.Error())
		return 4
	}
	if plan.GroupID != group || !expectedPredecessorMatches(plan.ExpectedPredecessor, expected) {
		return syncMembershipError(stderr, command, errors.New("plan group or expected predecessor does not match the reviewed command"), 14)
	}
	canonicalPlan, _ := syncrecords.CanonicalPlan(plan)
	configRevision, _ := config.SyncRevision(cfg)
	now := time.Now().UTC()
	nowText := now.Format(time.RFC3339Nano)
	_, concrete, openCode := openOperatorStore(command, "", stderr)
	if openCode != 0 {
		return openCode
	}
	defer concrete.Close()
	if _, err := concrete.EnsureSyncControl(requestCtx(), group, configRevision, nowText); err != nil {
		return syncStoreError(stderr, command, err)
	}
	job, reused, err := concrete.AdmitSyncJob(requestCtx(), sqlite.SyncJobInput{
		JobID: randomSyncID("membership-job"), GroupID: group, Kind: "membership", LogicalKey: plan.PlanID,
		InitialState: "planned", PayloadJSON: string(canonicalPlan), ConfigRevision: configRevision,
		QueueLimit: syncCfg.Bounds.Queue, Now: nowText,
	})
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	client, err := membershipGitClient(cfg, syncCfg)
	if err != nil {
		return syncMembershipError(stderr, command, err, 3)
	}
	binding := membershipBinding(cfg, syncCfg)
	if reused {
		if job.State == "applied" {
			head, headErr := client.ResolveRef(requestCtx(), syncCfg.MembershipRef)
			if headErr == nil {
				_, historyErr := syncmembership.LoadHistory(requestCtx(), client, head, binding, syncCfg.Bounds.HistoryCommits)
				_, embeddedRaw, readErr := client.ReadMembershipCommit(requestCtx(), head)
				embedded, decodeErr := syncrecords.DecodePlan(embeddedRaw)
				if historyErr == nil && readErr == nil && decodeErr == nil && embedded.PlanID == plan.PlanID {
					control, controlErr := concrete.LoadSyncControl(requestCtx(), group)
					if controlErr != nil {
						return syncStoreError(stderr, command, controlErr)
					}
					return membershipApplyResult(stdout, command, plan, head, control)
				}
			}
		}
		if recovered, result := recoverConfirmedMembership(stdout, stderr, concrete, client, syncCfg, plan, job, configRevision); recovered {
			return result
		}
		if job.State == "blocked" || job.State == "uncertain" {
			if job.ResolvedAt != "" {
				return syncMembershipError(stderr, command, errors.New("membership job was already discharged by newer recovery evidence"), 14)
			}
			journals, loadErr := concrete.LoadSyncJournals(requestCtx(), job.JobID)
			if loadErr != nil {
				return syncStoreError(stderr, command, loadErr)
			}
			candidate := journalCandidate(journals)
			remote, remoteErr := client.RemoteRef(requestCtx(), syncCfg.RemoteName, syncCfg.MembershipRef, syncCfg.RemoteRepositoryDigest)
			if errors.Is(remoteErr, gitlocal.ErrRemoteBinding) {
				return syncMembershipError(stderr, command, remoteErr, 30)
			}
			if remoteErr != nil && !errors.Is(remoteErr, gitlocal.ErrMissingRef) {
				return syncRetryableError(stderr, command, fmt.Errorf("membership recovery could not measure the approved remote ref: %w", remoteErr))
			}
			remoteUnchanged := plan.ExpectedPredecessor == nil && errors.Is(remoteErr, gitlocal.ErrMissingRef)
			if plan.ExpectedPredecessor != nil {
				remoteUnchanged = remoteErr == nil && remote == *plan.ExpectedPredecessor
			}
			if candidate == "" || !remoteUnchanged {
				return syncMembershipError(stderr, command, errors.New("membership recovery requires the exact signed candidate or predecessor on the approved remote"), 14)
			}
			if err := client.VerifySSHSignature(requestCtx(), candidate, syncCfg.AdministratorKey); err != nil {
				return syncMembershipError(stderr, command, err, 30)
			}
			recoveryNow := time.Now().UTC().Format(time.RFC3339Nano)
			if err := concrete.ReopenRejectedMembership(requestCtx(), job.JobID, job.Fence, sqlite.SyncJournalEntry{
				JournalID: randomSyncID("membership-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_not_started",
				EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "remote": remote, "remote_unchanged": true}), RecordedAt: recoveryNow,
			}, recoveryNow); err != nil {
				return syncStoreError(stderr, command, err)
			}
			job.State, job.ClaimOwner = "planned", ""
		}
		if job.State == "planned" && job.ClaimOwner != "" {
			expires, _ := time.Parse(time.RFC3339Nano, job.ClaimExpiresAt)
			if expires.After(time.Now().UTC()) {
				return syncMembershipError(stderr, command, fmt.Errorf("membership plan is still claimed by another invocation"), 14)
			}
			journals, loadErr := concrete.LoadSyncJournals(requestCtx(), job.JobID)
			if loadErr != nil {
				return syncStoreError(stderr, command, loadErr)
			}
			candidate := journalCandidate(journals)
			remote, remoteErr := client.RemoteRef(requestCtx(), syncCfg.RemoteName, syncCfg.MembershipRef, syncCfg.RemoteRepositoryDigest)
			if errors.Is(remoteErr, gitlocal.ErrRemoteBinding) {
				return syncMembershipError(stderr, command, remoteErr, 30)
			}
			if remoteErr != nil && !errors.Is(remoteErr, gitlocal.ErrMissingRef) {
				return syncRetryableError(stderr, command, fmt.Errorf("expired membership recovery could not measure the approved remote ref: %w", remoteErr))
			}
			remoteUnchanged := plan.ExpectedPredecessor == nil && errors.Is(remoteErr, gitlocal.ErrMissingRef)
			if plan.ExpectedPredecessor != nil {
				remoteUnchanged = remoteErr == nil && remote == *plan.ExpectedPredecessor
			}
			if !remoteUnchanged {
				return syncMembershipError(stderr, command, fmt.Errorf("expired membership claim requires remote confirmation recovery for candidate %s", candidate), 14)
			}
			recoveryNow := time.Now().UTC().Format(time.RFC3339Nano)
			if err := concrete.ReconcileExpiredSyncClaim(requestCtx(), job.JobID, job.Fence, "effect_not_started", sqlite.SyncJournalEntry{JournalID: randomSyncID("membership-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_not_started", EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "remote_unchanged": true, "explicit_cli_reentry": true}), RecordedAt: recoveryNow}, recoveryNow); err != nil {
				return syncStoreError(stderr, command, err)
			}
			job.ClaimOwner = ""
		}
		if job.State != "planned" || job.ClaimOwner != "" {
			return syncMembershipError(stderr, command, fmt.Errorf("membership plan already has durable state %s and requires reconciliation", job.State), 14)
		}
	}
	var current *syncrecords.Membership
	localHead, headErr := client.ResolveRef(requestCtx(), syncCfg.MembershipRef)
	if plan.ExpectedPredecessor == nil {
		if !errors.Is(headErr, gitlocal.ErrMissingRef) {
			return finishMembershipBeforeEffect(stderr, concrete, job, "bootstrap requires an absent membership ref", false)
		}
	} else {
		if headErr != nil || localHead != *plan.ExpectedPredecessor {
			return finishMembershipBeforeEffect(stderr, concrete, job, "local membership predecessor changed", false)
		}
		history, loadErr := syncmembership.LoadHistory(requestCtx(), client, localHead, binding, syncCfg.Bounds.HistoryCommits)
		if loadErr != nil {
			return finishMembershipBeforeEffect(stderr, concrete, job, loadErr.Error(), false)
		}
		member, found := history.Current()
		if !found {
			return finishMembershipBeforeEffect(stderr, concrete, job, "current membership is unavailable", false)
		}
		current = &member
	}
	rebuilt, err := syncrecords.RebuildPlan(plan, current, configuredMembers(syncCfg), binding)
	if err != nil || !reflect.DeepEqual(rebuilt, plan) {
		return finishMembershipBeforeEffect(stderr, concrete, job, "plan no longer matches current configuration and membership", false)
	}
	remoteHead, remoteErr := client.RemoteRef(requestCtx(), syncCfg.RemoteName, syncCfg.MembershipRef, syncCfg.RemoteRepositoryDigest)
	if errors.Is(remoteErr, gitlocal.ErrRemoteBinding) {
		return syncMembershipError(stderr, command, remoteErr, 30)
	}
	if plan.ExpectedPredecessor == nil {
		if remoteErr != nil && !errors.Is(remoteErr, gitlocal.ErrMissingRef) {
			return finishMembershipBeforeEffect(stderr, concrete, job, "remote membership absence could not be proven", true)
		}
		if remoteErr == nil {
			return finishMembershipBeforeEffect(stderr, concrete, job, "remote membership root already exists", false)
		}
	} else if remoteErr != nil {
		return finishMembershipBeforeEffect(stderr, concrete, job, "remote membership predecessor could not be inspected", true)
	} else if remoteHead != *plan.ExpectedPredecessor {
		return finishMembershipBeforeEffect(stderr, concrete, job, "remote membership predecessor changed", false)
	}
	owner := randomSyncID("membership-owner")
	claimNow := time.Now().UTC()
	claimNowText := claimNow.Format(time.RFC3339Nano)
	expires := claimNow.Add(time.Duration(syncCfg.Bounds.SubprocessSeconds*4+30) * time.Second).Format(time.RFC3339Nano)
	job, err = concrete.ClaimSyncAdministrationJob(requestCtx(), job.JobID, owner, configRevision, claimNowText, expires)
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	predecessor := ""
	if plan.ExpectedPredecessor != nil {
		predecessor = *plan.ExpectedPredecessor
	}
	journals, err := concrete.LoadSyncJournals(requestCtx(), job.JobID)
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	candidate := journalCandidate(journals)
	if candidate == "" {
		ref, parseErr := config.ParseSecretRef(syncCfg.AdministratorSigningKeyRef)
		if parseErr != nil {
			return finishClaimedMembership(stderr, concrete, job, owner, "planned", "effect_not_started", map[string]any{"reason": "administrator signing key reference is unavailable"}, time.Now().UTC())
		}
		privateKey, resolveErr := secretresolver.Resolve(requestCtx(), ref)
		if resolveErr != nil {
			return finishClaimedMembership(stderr, concrete, job, owner, "planned", "effect_not_started", map[string]any{"reason": "administrator signing key could not be resolved"}, time.Now().UTC())
		}
		documentRaw, _ := syncrecords.CanonicalMembership(plan.ProposedMembership)
		candidate, err = client.CreateSignedMembershipCommit(requestCtx(), documentRaw, canonicalPlan, []byte(privateKey), predecessor, claimNow)
		privateKey = ""
		if err != nil {
			return finishClaimedMembership(stderr, concrete, job, owner, "planned", "effect_not_started", map[string]any{"reason": "signed candidate creation failed"}, time.Now().UTC())
		}
		journalNow := time.Now().UTC().Format(time.RFC3339Nano)
		if err := concrete.AppendSyncJournal(requestCtx(), sqlite.SyncJournalEntry{
			JournalID: randomSyncID("membership-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "membership", Outcome: "signed",
			EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "plan_id": plan.PlanID}), RecordedAt: journalNow,
		}, owner); err != nil {
			return syncStoreError(stderr, command, err)
		}
	}
	if err := client.VerifySSHSignature(requestCtx(), candidate, syncCfg.AdministratorKey); err != nil {
		return finishClaimedMembership(stderr, concrete, job, owner, "planned", "effect_not_started", map[string]any{"reason": "candidate administrator signature was not pinned"}, time.Now().UTC())
	}
	push := client.PushFastForward(requestCtx(), syncCfg.RemoteName, syncCfg.MembershipRef, candidate, predecessor, syncCfg.RemoteRepositoryDigest)
	terminalNow := time.Now().UTC()
	switch classifySyncPush(push.State, push.RemoteOID, predecessor, candidate, push.Underlying) {
	case syncPushTrust:
		return finishClaimedSyncTrustFailure(stderr, command, concrete, job, owner, "planned", "membership", push.Underlying, map[string]any{"candidate": candidate})
	case syncPushConfirmed:
		if plan.ProposedMembership.Mode == "blocked_emergency" {
			if _, err := concrete.ReconcileAdoptedMembership(requestCtx(), syncCfg.GroupID, plan.ProposedMembership.Mode, candidate, configRevision, terminalNow.Format(time.RFC3339Nano)); err != nil {
				return syncStoreError(stderr, command, err)
			}
		}
		if err := client.UpdateRefExpected(requestCtx(), syncCfg.MembershipRef, candidate, predecessor); err != nil {
			return finishClaimedMembership(stderr, concrete, job, owner, "uncertain", "effect_unknown", map[string]any{"candidate": candidate, "remote": push.RemoteOID, "reason": "remote confirmed but local ref update failed"}, terminalNow)
		}
		terminalText := terminalNow.Format(time.RFC3339Nano)
		control, err := concrete.FinishMembershipJob(requestCtx(), job.JobID, owner, job.Fence, plan.ProposedMembership.Mode, sqlite.SyncJournalEntry{
			JournalID: randomSyncID("membership-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "membership", Outcome: "applied",
			EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "remote": push.RemoteOID, "push_state": gitlocal.PushConfirmed, "reason": "none", "plan_id": plan.PlanID}), RecordedAt: terminalText,
		}, configRevision, terminalText)
		if err != nil {
			return syncStoreError(stderr, command, err)
		}
		resultCode := writeEnvelope(stdout, command, map[string]any{
			"schema_version": "agent-dispatch.sync-membership-apply-result/v1", "state": "applied", "plan_id": plan.PlanID,
			"membership_revision": candidate, "membership_mode": control.MembershipMode, "control_state": control.State, "control_reason": control.Reason,
			"idempotent": false, "side_effects": []string{"git_objects_written", "remote_membership_ref_updated", "local_membership_ref_updated", "state_committed"},
		})
		return membershipApplyExitCode(plan.ProposedMembership.Mode, control, resultCode)
	case syncPushRetryable:
		reason := "membership push was rejected before the approved remote moved; rerun sync membership apply"
		return finishClaimedMembershipRetryable(stderr, concrete, job, owner, map[string]any{"candidate": candidate, "remote": push.RemoteOID, "push_state": push.State, "reason": reason}, terminalNow)
	case syncPushConflict:
		return finishClaimedMembership(stderr, concrete, job, owner, "blocked", "blocked", map[string]any{"candidate": candidate, "remote": push.RemoteOID, "reason": "non-force push rejected"}, terminalNow)
	default:
		return finishClaimedMembership(stderr, concrete, job, owner, "uncertain", "effect_unknown", map[string]any{"candidate": candidate, "remote": push.RemoteOID, "reason": "push outcome ambiguous"}, terminalNow)
	}
}

func recoverConfirmedMembership(stdout, stderr io.Writer, store *sqlite.Store, client *gitlocal.Client, syncCfg *config.Sync, plan syncrecords.MembershipPlan, job sqlite.SyncJobRow, configRevision string) (bool, int) {
	if job.State != "planned" && job.State != "uncertain" && job.State != "blocked" {
		return false, 0
	}
	journals, err := store.LoadSyncJournals(requestCtx(), job.JobID)
	if err != nil {
		return true, syncStoreError(stderr, "sync membership apply", err)
	}
	candidate := journalCandidate(journals)
	if candidate == "" {
		return false, 0
	}
	remote, remoteErr := client.RemoteRef(requestCtx(), syncCfg.RemoteName, syncCfg.MembershipRef, syncCfg.RemoteRepositoryDigest)
	if errors.Is(remoteErr, gitlocal.ErrRemoteBinding) {
		return true, syncMembershipError(stderr, "sync membership apply", remoteErr, 30)
	}
	if remoteErr != nil && !errors.Is(remoteErr, gitlocal.ErrMissingRef) {
		return true, syncRetryableError(stderr, "sync membership apply", fmt.Errorf("membership recovery could not measure the approved remote ref: %w", remoteErr))
	}
	if remoteErr != nil || remote != candidate {
		return false, 0
	}
	if err := client.VerifySSHSignature(requestCtx(), candidate, syncCfg.AdministratorKey); err != nil {
		return true, syncMembershipError(stderr, "sync membership apply", err, 30)
	}
	predecessor := ""
	if plan.ExpectedPredecessor != nil {
		predecessor = *plan.ExpectedPredecessor
	}
	local, localErr := client.ResolveRef(requestCtx(), syncCfg.MembershipRef)
	if localErr != nil && !errors.Is(localErr, gitlocal.ErrMissingRef) {
		return true, syncMembershipError(stderr, "sync membership apply", localErr, 14)
	}
	if local != candidate {
		if local != predecessor {
			return true, syncMembershipError(stderr, "sync membership apply", errors.New("local membership ref moved during recovery"), 14)
		}
		if plan.ProposedMembership.Mode == "blocked_emergency" {
			if _, err := store.ReconcileAdoptedMembership(requestCtx(), syncCfg.GroupID, plan.ProposedMembership.Mode, candidate, configRevision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return true, syncStoreError(stderr, "sync membership apply", err)
			}
		}
		if err := client.UpdateRefExpected(requestCtx(), syncCfg.MembershipRef, candidate, predecessor); err != nil {
			message := fmt.Errorf("remote membership is confirmed but the local membership ref did not move: %w", err)
			if plan.ProposedMembership.Mode == "blocked_emergency" {
				return true, syncMembershipError(stderr, "sync membership apply", fmt.Errorf("membership_adoption_incomplete: %w", message), 30)
			}
			return true, syncMembershipError(stderr, "sync membership apply", message, 13)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	control, err := store.FinishRecoveredMembershipJob(requestCtx(), job.JobID, job.Fence, plan.ProposedMembership.Mode, sqlite.SyncJournalEntry{
		JournalID: randomSyncID("membership-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "membership", Outcome: "applied",
		EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "remote": candidate, "push_state": gitlocal.PushConfirmed, "reason": "none", "plan_id": plan.PlanID, "recovered": true}), RecordedAt: now,
	}, configRevision, now)
	if err != nil {
		return true, syncStoreError(stderr, "sync membership apply", err)
	}
	code := writeEnvelope(stdout, "sync membership apply", map[string]any{
		"schema_version": "agent-dispatch.sync-membership-apply-result/v1", "state": "applied", "plan_id": plan.PlanID,
		"membership_revision": candidate, "membership_mode": control.MembershipMode, "control_state": control.State, "control_reason": control.Reason,
		"recovered": true, "idempotent": true, "side_effects": []string{"local_membership_ref_reconciled", "state_committed"},
	})
	return true, membershipApplyExitCode(plan.ProposedMembership.Mode, control, code)
}

func finishClaimedMembershipRetryable(stderr io.Writer, store *sqlite.Store, job sqlite.SyncJobRow, owner string, evidence map[string]any, now time.Time) int {
	nowText := now.UTC().Format(time.RFC3339Nano)
	if err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, "planned", sqlite.SyncJobKeepUnresolved, sqlite.SyncJournalEntry{
		JournalID: randomSyncID("membership-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "membership", Outcome: "effect_not_started",
		EvidenceJSON: mustJSON(evidence), RecordedAt: nowText,
	}, nowText); err != nil {
		return syncStoreError(stderr, "sync membership apply", err)
	}
	writeError(stderr, "sync membership apply", "sync_retryable", "transient_local", fmt.Sprint(evidence["reason"]))
	return 10
}

func membershipApplyResult(stdout io.Writer, command string, plan syncrecords.MembershipPlan, revision string, control sqlite.SyncControlRow) int {
	code := writeEnvelope(stdout, command, map[string]any{
		"schema_version": "agent-dispatch.sync-membership-apply-result/v1", "state": "applied", "plan_id": plan.PlanID,
		"membership_revision": revision, "membership_mode": control.MembershipMode, "control_state": control.State,
		"control_reason": control.Reason, "idempotent": true, "side_effects": []string{},
	})
	return membershipApplyExitCode(plan.ProposedMembership.Mode, control, code)
}

func membershipApplyExitCode(candidateMode string, control sqlite.SyncControlRow, successCode int) int {
	if candidateMode == "blocked_emergency" {
		return 30
	}
	if control.State == "blocked" {
		return syncControlHoldExitCode(control.MembershipMode, control.Reason)
	}
	return successCode
}

func finishMembershipBeforeEffect(stderr io.Writer, store *sqlite.Store, job sqlite.SyncJobRow, reason string, retryable bool) int {
	now := time.Now().UTC()
	owner := randomSyncID("membership-owner")
	configRevision := ""
	if control, err := store.LoadSyncControl(requestCtx(), job.GroupID); err == nil {
		configRevision = control.ConfigRevision
	}
	claimed, err := store.ClaimSyncAdministrationJob(requestCtx(), job.JobID, owner, configRevision, now.Format(time.RFC3339Nano), now.Add(2*time.Minute).Format(time.RFC3339Nano))
	if err == nil {
		if retryable {
			return finishClaimedMembershipRetryable(stderr, store, claimed, owner, map[string]any{"reason": reason}, now)
		}
		return finishClaimedMembership(stderr, store, claimed, owner, "blocked", "effect_not_started", map[string]any{"reason": reason}, now)
	}
	return syncStoreError(stderr, "sync membership apply", err)
}

func finishClaimedMembership(stderr io.Writer, store *sqlite.Store, job sqlite.SyncJobRow, owner, state, outcome string, evidence map[string]any, now time.Time) int {
	nowText := now.UTC().Format(time.RFC3339Nano)
	err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, state, sqlite.SyncJobKeepUnresolved, sqlite.SyncJournalEntry{
		JournalID: randomSyncID("membership-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "membership", Outcome: outcome,
		EvidenceJSON: mustJSON(evidence), RecordedAt: nowText,
	}, nowText)
	if err != nil {
		return syncStoreError(stderr, "sync membership apply", err)
	}
	code := 14
	errorCode, category := "sync_precondition_failed", "conflict"
	if state == "uncertain" {
		code, errorCode, category = 13, "sync_effect_unknown", "acceptance_unknown"
	}
	writeError(stderr, "sync membership apply", errorCode, category, fmt.Sprint(evidence["reason"]))
	return code
}

func loadMembershipConfig(command, group string, requireEnabled bool, stderr io.Writer) (*config.Config, *config.Sync, int) {
	cfg, err := config.Load(resolveConfigPath(""))
	if err != nil {
		return nil, nil, planErr(stderr, command, "config_invalid", "configuration", err.Error(), 3)
	}
	if cfg.Sync == nil || cfg.Sync.GroupID != group {
		return nil, nil, planErr(stderr, command, "sync_group_not_found", "configuration", fmt.Sprintf("sync group %q is not configured", group), 3)
	}
	if requireEnabled && !cfg.Sync.Enabled {
		return nil, nil, planErr(stderr, command, "sync_precondition_failed", "conflict", "sync must be enabled before signed apply", 14)
	}
	if requireEnabled && cfg.Sync.AdministratorSigningKeyRef == "" {
		return nil, nil, planErr(stderr, command, "config_invalid", "configuration", "administrator_signing_key_ref is required for signed apply", 3)
	}
	return cfg, cfg.Sync, 0
}

func membershipGitClient(cfg *config.Config, syncCfg *config.Sync) (*gitlocal.Client, error) {
	resource, ok := cfg.Resources[syncCfg.Resource]
	if !ok {
		return nil, fmt.Errorf("configured sync resource is missing")
	}
	return gitlocal.New(resource.Root, gitlocal.Limits{Timeout: time.Duration(syncCfg.Bounds.SubprocessSeconds) * time.Second, MaxOutput: syncCfg.Bounds.SubprocessBytes})
}

func membershipBinding(cfg *config.Config, syncCfg *config.Sync) syncrecords.Binding {
	return syncrecords.Binding{GroupID: syncCfg.GroupID, ContentRef: syncCfg.ContentRef, ContentBinding: syncCfg.RemoteRepositoryDigest, ContractDigest: config.SyncContractDigest(), AdministratorKey: syncCfg.AdministratorKey}
}

func configuredMembers(syncCfg *config.Sync) []syncrecords.ActiveMember {
	out := make([]syncrecords.ActiveMember, 0, len(syncCfg.Nodes))
	for _, node := range syncCfg.Nodes {
		out = append(out, syncrecords.ActiveMember{InstanceID: node.InstanceID, StateIncarnationID: node.StateIncarnationID, PublisherKey: node.PublisherKey, Endpoint: node.Endpoint})
	}
	return out
}

func readMembershipPlanFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > syncrecords.MaxRecordBytes {
		return nil, fmt.Errorf("plan must be a regular file no larger than %d bytes", syncrecords.MaxRecordBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open plan: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("plan file changed while it was being opened")
	}
	raw, err := io.ReadAll(io.LimitReader(file, syncrecords.MaxRecordBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}
	if len(raw) > syncrecords.MaxRecordBytes {
		return nil, fmt.Errorf("plan exceeds %d bytes", syncrecords.MaxRecordBytes)
	}
	return raw, nil
}

func expectedPredecessorMatches(expected *string, value string) bool {
	if expected == nil {
		return value == "none"
	}
	return value == *expected
}

func randomSyncID(prefix string) string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(value[:])
}

func mustJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }

func syncMembershipError(stderr io.Writer, command string, err error, fallback int) int {
	code, errorCode, category := fallback, "sync_precondition_failed", "conflict"
	switch fallback {
	case 3:
		errorCode, category = "config_invalid", "configuration"
	case 13:
		errorCode, category = "sync_effect_unknown", "acceptance_unknown"
	case 30:
		errorCode, category = "sync_trust_failed", "security"
	}
	switch {
	case errors.Is(err, gitlocal.ErrInvalidSignature), errors.Is(err, gitlocal.ErrRemoteBinding), errors.Is(err, syncrecords.ErrInvalidRecord):
		code, errorCode, category = 30, "sync_trust_failed", "security"
	case errors.Is(err, syncrecords.ErrInvalidTransition), errors.Is(err, gitlocal.ErrPushRejected):
		code, errorCode = 14, "sync_precondition_failed"
	}
	writeError(stderr, command, errorCode, category, err.Error())
	return code
}

func syncMembershipPlanFlags(args []string) (group, change, instance string, ok bool) {
	values, valid := parseClosedSyncFlags(args, map[string]bool{"--group": true, "--change": true, "--instance": true})
	if !valid {
		return "", "", "", false
	}
	group, change, instance = values["--group"], values["--change"], values["--instance"]
	return group, change, instance, group != "" && change != "" && ((change == "bootstrap" && instance == "") || (change != "bootstrap" && instance != ""))
}

func syncMembershipApplyFlags(args []string) (group, plan, expected string, ok bool) {
	values, valid := parseClosedSyncFlags(args, map[string]bool{"--group": true, "--plan": true, "--expected-membership-predecessor": true})
	if !valid {
		return "", "", "", false
	}
	group, plan, expected = values["--group"], values["--plan"], values["--expected-membership-predecessor"]
	return group, plan, expected, group != "" && plan != "" && expected != ""
}

func parseClosedSyncFlags(args []string, allowed map[string]bool) (map[string]string, bool) {
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if flag == "--output=json" {
			if _, exists := values["--output"]; exists {
				return nil, false
			}
			values["--output"] = "json"
			continue
		}
		if flag == "--output" {
			if _, exists := values[flag]; exists || i+1 >= len(args) || args[i+1] != "json" {
				return nil, false
			}
			values[flag] = "json"
			i++
			continue
		}
		if !allowed[flag] || values[flag] != "" || i+1 >= len(args) {
			return nil, false
		}
		values[flag] = args[i+1]
		i++
	}
	return values, true
}

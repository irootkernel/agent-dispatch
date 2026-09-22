package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/adapters/resourceguard"
	"github.com/irootkernel/agent-dispatch/internal/adapters/secretresolver"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/syncmembership"
	"github.com/irootkernel/agent-dispatch/internal/app/syncpublication"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func runSyncPublish(args []string, stdout, stderr io.Writer) int {
	const command = "sync publish"
	values, ok := parseClosedSyncFlags(args, map[string]bool{"--group": true, "--expected-config-revision": true})
	if !ok || values["--group"] == "" || values["--expected-config-revision"] == "" {
		return usageError(stderr, command, "publish requires --group GROUP --expected-config-revision REV and accepts --output json")
	}
	cfg, syncCfg, code := loadMembershipConfig(command, values["--group"], false, stderr)
	if code != 0 {
		return code
	}
	if !syncCfg.Enabled || syncCfg.PublisherSigningKeyRef == "" {
		return planErr(stderr, command, "sync_precondition_failed", "conflict", "sync must be enabled with publisher_signing_key_ref before publication", 14)
	}
	revision, _ := config.SyncRevision(cfg)
	if revision != values["--expected-config-revision"] {
		return planErr(stderr, command, "sync_precondition_failed", "conflict", "configuration revision does not match the reviewed command", 14)
	}
	_, store, openCode := openOperatorStore(command, "", stderr)
	if openCode != 0 {
		return openCode
	}
	defer store.Close()
	now := time.Now().UTC()
	if _, err := store.EnsureSyncControl(requestCtx(), syncCfg.GroupID, revision, now.Format(time.RFC3339Nano)); err != nil {
		return syncStoreError(stderr, command, err)
	}
	guard, err := resourceguard.Acquire(stateDirOf(cfg), syncCfg.Resource)
	if err != nil {
		return syncMembershipError(stderr, command, err, 14)
	}
	defer guard.Close()
	client, err := membershipGitClient(cfg, syncCfg)
	if err != nil {
		return syncMembershipError(stderr, command, err, 3)
	}
	membershipHead, err := client.ResolveRef(requestCtx(), syncCfg.MembershipRef)
	if err != nil {
		return syncMembershipError(stderr, command, err, 30)
	}
	remoteMembership, err := client.RemoteRef(requestCtx(), syncCfg.RemoteName, syncCfg.MembershipRef, syncCfg.RemoteRepositoryDigest)
	if err != nil || remoteMembership != membershipHead {
		return syncMembershipError(stderr, command, fmt.Errorf("membership ref is not current on the approved remote"), 30)
	}
	history, err := syncmembership.LoadHistory(requestCtx(), client, membershipHead, membershipBinding(cfg, syncCfg), syncCfg.Bounds.HistoryCommits)
	if err != nil {
		return syncMembershipError(stderr, command, err, 30)
	}
	member, peer, ok := publicationMembers(history, syncCfg.LocalInstanceID)
	if !ok {
		return syncMembershipError(stderr, command, fmt.Errorf("local publisher is not active in current membership"), 30)
	}
	if err := history.AuthorizePublisher(membershipHead, member.PublisherKey, false, false); err != nil {
		return syncMembershipError(stderr, command, err, 30)
	}
	if recovered, result := recoverConfirmedPublication(stdout, stderr, store, client, cfg, syncCfg, history, membershipHead, member, peer); recovered {
		return result
	}
	eligible, err := store.LoadPublicationEligibility(requestCtx(), syncCfg.Resource)
	if err != nil {
		if errors.Is(err, sqlite.ErrSyncPrecondition) {
			return writeEnvelope(stdout, command, map[string]any{"schema_version": "agent-dispatch.sync-publish-result/v1", "state": "no_eligible_snapshot", "reason": err.Error(), "side_effects": []string{}})
		}
		return syncStoreError(stderr, command, err)
	}
	base, err := client.ResolveRef(requestCtx(), syncCfg.ContentRef)
	if err != nil {
		return syncMembershipError(stderr, command, fmt.Errorf("content ref requires an administrator checkpoint before publication: %w", err), 14)
	}
	remoteBase, err := client.RemoteRef(requestCtx(), syncCfg.RemoteName, syncCfg.ContentRef, syncCfg.RemoteRepositoryDigest)
	if err != nil || remoteBase != base {
		return syncMembershipError(stderr, command, fmt.Errorf("content predecessor is not current on the approved remote"), 14)
	}
	needsCheckpoint, err := contentHeadNeedsInitialCheckpoint(client, base)
	if err != nil {
		return syncMembershipError(stderr, command, err, 30)
	}
	if needsCheckpoint {
		return syncMembershipError(stderr, command, errors.New("content ref requires sync checkpoint plan --kind initial_baseline before publication"), 14)
	}
	if err := verifyContentHead(client, history, cfg, syncCfg, base); err != nil {
		return syncMembershipError(stderr, command, err, 30)
	}
	snapshot, err := syncpublication.Capture(cfg, syncCfg.Resource)
	if err != nil {
		return syncMembershipError(stderr, command, err, 14)
	}
	confirmedEligibility, err := store.LoadPublicationEligibility(requestCtx(), syncCfg.Resource)
	factsMatch := false
	if err == nil {
		factsMatch, err = syncpublication.MatchesObservedFacts(cfg, syncCfg.Resource, snapshot.Records, confirmedEligibility.PathDigests)
	}
	if err != nil || confirmedEligibility.SourceRevision != eligible.SourceRevision || !equalStrings(confirmedEligibility.ReceiptIDs, eligible.ReceiptIDs) || !factsMatch {
		return writeEnvelope(stdout, command, map[string]any{"schema_version": "agent-dispatch.sync-publish-result/v1", "state": "no_eligible_snapshot", "reason": "maintenance_evidence_changed_or_did_not_match", "side_effects": []string{}})
	}
	baseFiles, err := client.ReadContentFiles(requestCtx(), base)
	if err != nil {
		return syncMembershipError(stderr, command, err, 14)
	}
	if contentEqual(baseFiles, snapshot.Files) {
		return writeEnvelope(stdout, command, map[string]any{"schema_version": "agent-dispatch.sync-publish-result/v1", "state": "no_content_change", "base_commit": base, "side_effects": []string{}})
	}
	publication, err := syncrecords.NewPublication(syncrecords.PublicationBinding{GroupID: syncCfg.GroupID, Publisher: member.InstanceID, StateIncarnationID: member.StateIncarnationID, MembershipRevision: membershipHead, ContentRef: syncCfg.ContentRef, BaseCommit: base, ScopeDigest: config.SyncScopeDigest(cfg, syncCfg.Resource), ContractDigest: config.SyncContractDigest(), SourceRevision: eligible.SourceRevision, ReceiptIDs: eligible.ReceiptIDs}, snapshot.Records)
	if err != nil {
		return syncMembershipError(stderr, command, err, 14)
	}
	payload, _ := syncrecords.CanonicalPublication(publication)
	job, reused, err := store.AdmitSyncJob(requestCtx(), sqlite.SyncJobInput{JobID: randomSyncID("publication-job"), GroupID: syncCfg.GroupID, Kind: "publication", LogicalKey: publication.PublicationID, InitialState: "eligible", PayloadJSON: string(payload), ConfigRevision: revision, QueueLimit: syncCfg.Bounds.Queue, Now: now.Format(time.RFC3339Nano), PublicationResourceID: syncCfg.Resource, ExpectedSourceRevision: eligible.SourceRevision})
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	journals, err := store.LoadSyncJournals(requestCtx(), job.JobID)
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	if job.State == "published" {
		candidate := journalCandidate(journals)
		return publicationResult(stdout, publication, candidate, true)
	}
	if job.State == "uncertain" {
		recoveryNow := time.Now().UTC().Format(time.RFC3339Nano)
		if err := store.ReconcileUncertainPublication(requestCtx(), job.JobID, job.Fence, sqlite.SyncJournalEntry{JournalID: randomSyncID("publication-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_not_started", EvidenceJSON: mustJSON(map[string]any{"candidate": journalCandidate(journals), "remote": remoteBase}), RecordedAt: recoveryNow}, recoveryNow); err != nil {
			return syncStoreError(stderr, command, err)
		}
		job.State = "signed"
	}
	if reused && job.ClaimOwner != "" {
		expires, _ := time.Parse(time.RFC3339Nano, job.ClaimExpiresAt)
		if expires.After(time.Now().UTC()) {
			return syncMembershipError(stderr, command, fmt.Errorf("publication is still claimed by another invocation"), 14)
		}
		recoveryNow := time.Now().UTC().Format(time.RFC3339Nano)
		if err := store.ReconcileExpiredSyncClaim(requestCtx(), job.JobID, job.Fence, "effect_not_started", sqlite.SyncJournalEntry{JournalID: randomSyncID("publication-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_not_started", EvidenceJSON: mustJSON(map[string]any{"explicit_cli_reentry": true}), RecordedAt: recoveryNow}, recoveryNow); err != nil {
			return syncStoreError(stderr, command, err)
		}
	}
	owner := randomSyncID("publication-owner")
	claimNow := time.Now().UTC()
	job, err = store.ClaimSyncJob(requestCtx(), job.JobID, owner, revision, claimNow.Format(time.RFC3339Nano), claimNow.Add(time.Duration(syncCfg.Bounds.SubprocessSeconds*6+60)*time.Second).Format(time.RFC3339Nano))
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	candidate := journalCandidate(journals)
	if candidate == "" {
		tree, commitTime := journalPreparedCandidate(journals)
		if tree == "" {
			files := cloneFiles(snapshot.Files)
			files[".agent-dispatch-sync/publications/"+publication.PublicationID+".json"] = payload
			deletions := missingPaths(baseFiles, snapshot.Files)
			var treeErr error
			tree, treeErr = client.SnapshotTreeChanges(requestCtx(), base, files, deletions)
			if treeErr != nil {
				return finishPublicationFailure(stderr, store, job, owner, "eligible", "effect_not_started", treeErr.Error())
			}
			commitTime = claimNow
			preparedAt := time.Now().UTC().Format(time.RFC3339Nano)
			if err := store.AdvanceSyncJob(requestCtx(), job.JobID, owner, job.Fence, "prepared", sqlite.SyncJournalEntry{JournalID: randomSyncID("publication-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "publication", Outcome: "prepared", EvidenceJSON: mustJSON(map[string]any{"tree": tree, "commit_time": commitTime.Format(time.RFC3339), "publication_id": publication.PublicationID}), RecordedAt: preparedAt}, preparedAt); err != nil {
				return syncStoreError(stderr, command, err)
			}
		}
		ref, parseErr := config.ParseSecretRef(syncCfg.PublisherSigningKeyRef)
		if parseErr != nil {
			return finishPublicationFailure(stderr, store, job, owner, "prepared", "effect_not_started", "publisher signing key reference is unavailable")
		}
		privateKey, resolveErr := secretresolver.Resolve(requestCtx(), ref)
		if resolveErr != nil {
			return finishPublicationFailure(stderr, store, job, owner, "prepared", "effect_not_started", "publisher signing key could not be resolved")
		}
		candidate, err = client.CreateSignedContentCommit(requestCtx(), tree, []byte(privateKey), base, commitTime, "publisher", "Publish "+publication.PublicationID)
		privateKey = ""
		if err != nil {
			return finishPublicationFailure(stderr, store, job, owner, "prepared", "effect_not_started", "signed candidate creation failed")
		}
		if err := client.VerifySSHSignature(requestCtx(), candidate, member.PublisherKey); err != nil {
			return finishPublicationFailure(stderr, store, job, owner, "blocked", "blocked", "candidate publisher signature was not pinned")
		}
		signedAt := time.Now().UTC().Format(time.RFC3339Nano)
		if err := store.AdvanceSyncJob(requestCtx(), job.JobID, owner, job.Fence, "signed", sqlite.SyncJournalEntry{JournalID: randomSyncID("publication-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "publication", Outcome: "signed", EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "publication_id": publication.PublicationID}), RecordedAt: signedAt}, signedAt); err != nil {
			return syncStoreError(stderr, command, err)
		}
	}
	remoteNow, remoteErr := client.RemoteRef(requestCtx(), syncCfg.RemoteName, syncCfg.ContentRef, syncCfg.RemoteRepositoryDigest)
	confirmed := remoteErr == nil && remoteNow == candidate
	if !confirmed {
		push := client.PushFastForward(requestCtx(), syncCfg.RemoteName, syncCfg.ContentRef, candidate, base, syncCfg.RemoteRepositoryDigest)
		switch classifySyncPush(push.State, push.RemoteOID, base, candidate) {
		case syncPushRetryable:
			evidence := map[string]any{"candidate": candidate, "remote": push.RemoteOID, "push_state": push.State}
			return finishPublicationRetryable(stderr, store, job, owner, evidence, "content push was rejected before the approved remote moved; rerun sync publish")
		case syncPushConflict:
			evidence := map[string]any{"candidate": candidate, "remote": push.RemoteOID, "push_state": push.State}
			if _, err := store.HoldSyncControl(requestCtx(), syncCfg.GroupID, "conflict", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return syncStoreError(stderr, command, err)
			}
			return finishPublicationFailureWithEvidence(stderr, store, job, owner, "blocked", "blocked", "content fast-forward lost; resolve ordinary Git history, then run sync checkpoint plan --kind conflict_resolution", evidence)
		case syncPushUnknown:
			evidence := map[string]any{"candidate": candidate, "remote": push.RemoteOID, "push_state": push.State}
			return finishPublicationFailureWithEvidence(stderr, store, job, owner, "uncertain", "effect_unknown", "content push outcome is ambiguous; rerun sync publish for remote confirmation", evidence)
		}
	}
	if err := client.UpdateRefExpected(requestCtx(), syncCfg.ContentRef, candidate, base); err != nil {
		local, _ := client.ResolveRef(requestCtx(), syncCfg.ContentRef)
		if local != candidate {
			return finishPublicationFailure(stderr, store, job, owner, "uncertain", "effect_unknown", "remote confirmed but local content ref update failed")
		}
	}
	nudge := syncrecords.Nudge{SchemaVersion: syncrecords.NudgeSchema, GroupID: syncCfg.GroupID, PublicationID: publication.PublicationID, Sender: member.InstanceID, Receiver: peer.InstanceID, MembershipRevision: membershipHead, ContentRef: syncCfg.ContentRef, TargetCommit: candidate}
	nudgeRaw, err := syncrecords.CanonicalNudge(nudge)
	if err != nil {
		return finishPublicationFailure(stderr, store, job, owner, "blocked", "blocked", err.Error())
	}
	terminal := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = store.FinishPublicationJob(requestCtx(), job.JobID, owner, job.Fence, sqlite.SyncJournalEntry{JournalID: randomSyncID("publication-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "publication", Outcome: "published", EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "remote": candidate, "publication_id": publication.PublicationID}), RecordedAt: terminal}, sqlite.SyncJobInput{JobID: randomSyncID("delivery-job"), GroupID: syncCfg.GroupID, Kind: "delivery", LogicalKey: publication.PublicationID, InitialState: "pending", PayloadJSON: string(nudgeRaw), QueueLimit: syncCfg.Bounds.Queue, Now: terminal}, terminal)
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	return publicationResult(stdout, publication, candidate, false)
}

func publicationMembers(h syncmembership.History, local string) (syncrecords.ActiveMember, syncrecords.ActiveMember, bool) {
	m, ok := h.Current()
	if !ok || len(m.ActiveMembers) != 2 {
		return syncrecords.ActiveMember{}, syncrecords.ActiveMember{}, false
	}
	var self, peer syncrecords.ActiveMember
	for _, v := range m.ActiveMembers {
		if v.InstanceID == local {
			self = v
		} else {
			peer = v
		}
	}
	return self, peer, self.InstanceID != "" && peer.InstanceID != ""
}

// recoverConfirmedPublication closes the narrow crash window after a signed
// candidate reached the remote but before SQLite and the local ref committed.
// It never signs or creates another commit.
func recoverConfirmedPublication(stdout, stderr io.Writer, store *sqlite.Store, client *gitlocal.Client, cfg *config.Config, s *config.Sync, history syncmembership.History, membership string, self, peer syncrecords.ActiveMember) (bool, int) {
	jobs, err := store.LoadUnresolvedSyncJobs(requestCtx(), s.GroupID, "publication")
	if err != nil {
		return true, syncStoreError(stderr, "sync publish", err)
	}
	if len(jobs) == 0 {
		return false, 0
	}
	revision, _ := config.SyncRevision(cfg)
	remote, remoteErr := client.RemoteRef(requestCtx(), s.RemoteName, s.ContentRef, s.RemoteRepositoryDigest)
	if remoteErr != nil {
		if errors.Is(remoteErr, gitlocal.ErrMissingRef) {
			return true, syncMembershipError(stderr, "sync publish", errors.New("content ref requires an administrator checkpoint before publication recovery"), 14)
		}
		return true, syncMembershipError(stderr, "sync publish", errors.New("publication recovery could not measure the approved remote content ref"), 13)
	}
	for _, job := range jobs {
		journals, loadErr := store.LoadSyncJournals(requestCtx(), job.JobID)
		if loadErr != nil {
			return true, syncStoreError(stderr, "sync publish", loadErr)
		}
		candidate := journalCandidate(journals)
		if candidate == "" {
			continue
		}
		publication, decodeErr := syncrecords.DecodePublication([]byte(job.PayloadJSON))
		if decodeErr != nil || publication.MembershipRevision != membership || publication.Publisher != self.InstanceID || publication.StateIncarnationID != self.StateIncarnationID {
			continue
		}
		recoveryOwner := ""
		if candidate != remote {
			if remote != publication.BaseCommit || (job.State != "signed" && job.State != "uncertain") {
				continue
			}
			now := time.Now().UTC()
			if job.ClaimOwner != "" {
				expires, _ := time.Parse(time.RFC3339Nano, job.ClaimExpiresAt)
				if expires.After(now) {
					return true, syncMembershipError(stderr, "sync publish", errors.New("pending publication still has an unexpired claim"), 14)
				}
				nowText := now.Format(time.RFC3339Nano)
				if err := store.ReconcileExpiredSyncClaim(requestCtx(), job.JobID, job.Fence, "effect_not_started", sqlite.SyncJournalEntry{JournalID: randomSyncID("publication-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_not_started", EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "remote": remote}), RecordedAt: nowText}, nowText); err != nil {
					return true, syncStoreError(stderr, "sync publish", err)
				}
				job.ClaimOwner = ""
			}
			if job.State == "uncertain" {
				nowText := now.Format(time.RFC3339Nano)
				if err := store.ReconcileUncertainPublication(requestCtx(), job.JobID, job.Fence, sqlite.SyncJournalEntry{JournalID: randomSyncID("publication-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_not_started", EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "remote": remote}), RecordedAt: nowText}, nowText); err != nil {
					return true, syncStoreError(stderr, "sync publish", err)
				}
			}
			// Validate the exact signed candidate before any recovery push. A
			// malformed retained object must never be allowed to move the remote.
			if verifyErr := verifyContentHead(client, history, cfg, s, candidate); verifyErr != nil {
				return true, syncMembershipError(stderr, "sync publish", verifyErr, 30)
			}
			owner := randomSyncID("publication-owner")
			claimed, claimErr := store.ClaimSyncJob(requestCtx(), job.JobID, owner, revision, now.Format(time.RFC3339Nano), now.Add(time.Duration(s.Bounds.SubprocessSeconds*4+60)*time.Second).Format(time.RFC3339Nano))
			if claimErr != nil {
				return true, syncStoreError(stderr, "sync publish", claimErr)
			}
			push := client.PushFastForward(requestCtx(), s.RemoteName, s.ContentRef, candidate, publication.BaseCommit, s.RemoteRepositoryDigest)
			switch classifySyncPush(push.State, push.RemoteOID, publication.BaseCommit, candidate) {
			case syncPushRetryable:
				evidence := map[string]any{"candidate": candidate, "remote": push.RemoteOID, "push_state": push.State, "recovered": true}
				return true, finishPublicationRetryable(stderr, store, claimed, owner, evidence, "content push was rejected before the approved remote moved; rerun sync publish")
			case syncPushConflict:
				evidence := map[string]any{"candidate": candidate, "remote": push.RemoteOID, "push_state": push.State, "recovered": true}
				if _, err := store.HoldSyncControl(requestCtx(), s.GroupID, "conflict", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
					return true, syncStoreError(stderr, "sync publish", err)
				}
				return true, finishPublicationFailureWithEvidence(stderr, store, claimed, owner, "blocked", "blocked", "content fast-forward lost during publication recovery", evidence)
			case syncPushUnknown:
				evidence := map[string]any{"candidate": candidate, "remote": push.RemoteOID, "push_state": push.State, "recovered": true}
				return true, finishPublicationFailureWithEvidence(stderr, store, claimed, owner, "uncertain", "effect_unknown", "content push outcome remains ambiguous", evidence)
			}
			remote = candidate
			job = claimed
			recoveryOwner = owner
		}
		if verifyErr := verifyContentHead(client, history, cfg, s, candidate); verifyErr != nil {
			return true, syncMembershipError(stderr, "sync publish", verifyErr, 30)
		}
		now := time.Now().UTC()
		if job.ClaimOwner != "" {
			expires, _ := time.Parse(time.RFC3339Nano, job.ClaimExpiresAt)
			if expires.After(now) {
				return true, syncMembershipError(stderr, "sync publish", errors.New("confirmed publication still has an unexpired claim"), 14)
			}
		}
		local, localErr := client.ResolveRef(requestCtx(), s.ContentRef)
		if localErr != nil {
			return true, syncMembershipError(stderr, "sync publish", localErr, 14)
		}
		if local != candidate {
			if local != publication.BaseCommit {
				return true, syncMembershipError(stderr, "sync publish", errors.New("local content ref moved during publication recovery"), 14)
			}
			if err := client.UpdateRefExpected(requestCtx(), s.ContentRef, candidate, publication.BaseCommit); err != nil {
				return true, syncMembershipError(stderr, "sync publish", err, 14)
			}
		}
		nudge := syncrecords.Nudge{SchemaVersion: syncrecords.NudgeSchema, GroupID: s.GroupID, PublicationID: publication.PublicationID, Sender: self.InstanceID, Receiver: peer.InstanceID, MembershipRevision: membership, ContentRef: s.ContentRef, TargetCommit: candidate}
		raw, encodeErr := syncrecords.CanonicalNudge(nudge)
		if encodeErr != nil {
			return true, syncMembershipError(stderr, "sync publish", encodeErr, 14)
		}
		nowText := now.Format(time.RFC3339Nano)
		delivery := sqlite.SyncJobInput{JobID: randomSyncID("delivery-job"), GroupID: s.GroupID, Kind: "delivery", LogicalKey: publication.PublicationID, InitialState: "pending", PayloadJSON: string(raw), QueueLimit: s.Bounds.Queue, Now: nowText}
		journal := sqlite.SyncJournalEntry{JournalID: randomSyncID("publication-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "publication", Outcome: "published", EvidenceJSON: mustJSON(map[string]any{"candidate": candidate, "remote": candidate, "publication_id": publication.PublicationID, "recovered": true}), RecordedAt: nowText}
		if recoveryOwner == "" {
			_, err = store.FinishRecoveredPublicationJob(requestCtx(), job.JobID, job.Fence, journal, delivery, nowText)
		} else {
			_, err = store.FinishPublicationJob(requestCtx(), job.JobID, recoveryOwner, job.Fence, journal, delivery, nowText)
		}
		if err != nil {
			return true, syncStoreError(stderr, "sync publish", err)
		}
		return true, writeEnvelope(stdout, "sync publish", map[string]any{"schema_version": "agent-dispatch.sync-publish-result/v1", "state": "published", "publication_id": publication.PublicationID, "candidate_commit": candidate, "remote_commit": candidate, "recovered": true, "idempotent": true, "side_effects": []string{"local_content_ref_reconciled", "peer_delivery_admitted", "state_committed"}})
	}
	return false, 0
}
func contentEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for p, v := range a {
		if !bytes.Equal(v, b[p]) {
			return false
		}
	}
	return true
}
func cloneFiles(in map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(in)+1)
	for p, v := range in {
		out[p] = append([]byte(nil), v...)
	}
	return out
}
func missingPaths(base, current map[string][]byte) []string {
	var out []string
	for p := range base {
		if _, ok := current[p]; !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contentHeadNeedsInitialCheckpoint(client *gitlocal.Client, head string) (bool, error) {
	hasPublication, err := client.TreeHasPrefix(requestCtx(), head, ".agent-dispatch-sync/publications/")
	if err != nil {
		return false, err
	}
	hasCheckpoint, err := client.TreeHasPrefix(requestCtx(), head, ".agent-dispatch-sync/checkpoints/")
	if err != nil {
		return false, err
	}
	if hasPublication || hasCheckpoint {
		return false, nil
	}
	parents, err := client.CommitParents(requestCtx(), head)
	if err != nil {
		return false, err
	}
	if len(parents) != 1 {
		return true, nil
	}
	parentPublication, err := client.TreeHasPrefix(requestCtx(), parents[0], ".agent-dispatch-sync/publications/")
	if err != nil {
		return false, err
	}
	parentCheckpoint, err := client.TreeHasPrefix(requestCtx(), parents[0], ".agent-dispatch-sync/checkpoints/")
	if err != nil {
		return false, err
	}
	// A controller record disappearing from a child is a trust failure handled
	// by verifyContentHead, not an onboarding precondition.
	return !parentPublication && !parentCheckpoint, nil
}

func verifyContentHead(client *gitlocal.Client, history syncmembership.History, cfg *config.Config, s *config.Sync, head string) error {
	parents, err := client.CommitParents(requestCtx(), head)
	if err != nil {
		return err
	}
	if len(parents) != 1 {
		return fmt.Errorf("content head is not a linear signed publication or checkpoint")
	}
	parent := parents[0]
	currentPublications, err := client.ReadTreePrefix(requestCtx(), head, ".agent-dispatch-sync/publications/")
	if err != nil {
		return err
	}
	parentPublications, err := client.ReadTreePrefix(requestCtx(), parent, ".agent-dispatch-sync/publications/")
	if err != nil {
		return err
	}
	newPublications, err := newControllerRecords(currentPublications, parentPublications)
	if err != nil {
		return err
	}
	currentCheckpoints, err := client.ReadTreePrefix(requestCtx(), head, ".agent-dispatch-sync/checkpoints/")
	if err != nil {
		return err
	}
	parentCheckpoints, err := client.ReadTreePrefix(requestCtx(), parent, ".agent-dispatch-sync/checkpoints/")
	if err != nil {
		return err
	}
	newCheckpoints, err := newControllerRecords(currentCheckpoints, parentCheckpoints)
	if err != nil {
		return err
	}
	currentPlans, err := client.ReadTreePrefix(requestCtx(), head, ".agent-dispatch-sync/checkpoint-plans/")
	if err != nil {
		return err
	}
	parentPlans, err := client.ReadTreePrefix(requestCtx(), parent, ".agent-dispatch-sync/checkpoint-plans/")
	if err != nil {
		return err
	}
	newPlans, err := newControllerRecords(currentPlans, parentPlans)
	if err != nil {
		return err
	}
	files, err := client.ReadContentFiles(requestCtx(), head)
	if err != nil {
		return err
	}
	digest, err := snapshotDigestFromFiles(files)
	if err != nil {
		return err
	}
	switch {
	case len(newPublications) == 1 && len(newCheckpoints) == 0 && len(newPlans) == 0:
		var path string
		var raw []byte
		for recordPath, value := range newPublications {
			path = recordPath
			raw = value
		}
		publication, decodeErr := syncrecords.DecodePublication(raw)
		if decodeErr != nil {
			return decodeErr
		}
		if path != ".agent-dispatch-sync/publications/"+publication.PublicationID+".json" {
			return fmt.Errorf("publication manifest path does not match its identity")
		}
		if publication.GroupID != s.GroupID || publication.ContentRef != s.ContentRef || publication.BaseCommit != parent || publication.SnapshotDigest != digest || publication.ScopeDigest != config.SyncScopeDigest(cfg, s.Resource) || publication.ContractDigest != config.SyncContractDigest() {
			return fmt.Errorf("content publication binding does not match its commit")
		}
		membership, ok := history.Revisions[publication.MembershipRevision]
		if !ok {
			return fmt.Errorf("publication membership revision is not verified")
		}
		key := ""
		for _, candidate := range membership.ActiveMembers {
			if candidate.InstanceID == publication.Publisher && candidate.StateIncarnationID == publication.StateIncarnationID {
				key = candidate.PublisherKey
				break
			}
		}
		if key == "" {
			return fmt.Errorf("publication publisher identity is not active at its membership revision")
		}
		if err := history.AuthorizePublisher(publication.MembershipRevision, key, false, false); err != nil {
			return err
		}
		return client.VerifySSHSignature(requestCtx(), head, key)
	case len(newCheckpoints) == 1 && len(newPublications) == 0 && len(newPlans) == 1:
		var path string
		var raw []byte
		for recordPath, value := range newCheckpoints {
			path = recordPath
			raw = value
		}
		checkpoint, decodeErr := syncrecords.DecodeCheckpoint(raw)
		if decodeErr != nil {
			return decodeErr
		}
		if path != ".agent-dispatch-sync/checkpoints/"+checkpoint.CheckpointID+".json" {
			return fmt.Errorf("checkpoint manifest path does not match its identity")
		}
		var planPath string
		var planRaw []byte
		for recordPath, value := range newPlans {
			planPath, planRaw = recordPath, value
		}
		plan, planErr := syncrecords.DecodeCheckpointPlan(planRaw)
		if planErr != nil {
			return planErr
		}
		if planPath != ".agent-dispatch-sync/checkpoint-plans/"+plan.PlanID+".json" || plan.ProposedCheckpoint != checkpoint {
			return fmt.Errorf("checkpoint plan does not match its signed checkpoint")
		}
		if checkpoint.GroupID != s.GroupID || checkpoint.SnapshotDigest != digest || checkpoint.ScopeDigest != config.SyncScopeDigest(cfg, s.Resource) || checkpoint.ContractDigest != config.SyncContractDigest() || checkpoint.AdministratorKey != s.AdministratorKey {
			return fmt.Errorf("content checkpoint binding does not match its commit")
		}
		if _, ok := history.Revisions[checkpoint.MembershipRevision]; !ok {
			return fmt.Errorf("checkpoint membership revision is not verified")
		}
		return client.VerifySSHSignature(requestCtx(), head, s.AdministratorKey)
	default:
		return fmt.Errorf("content head must add exactly one publication or checkpoint record")
	}
}

func newControllerRecords(current, parent map[string][]byte) (map[string][]byte, error) {
	out := map[string][]byte{}
	for path, raw := range current {
		prior, exists := parent[path]
		if !exists {
			out[path] = raw
		} else if !bytes.Equal(prior, raw) {
			return nil, fmt.Errorf("controller record %q was rewritten", path)
		}
	}
	for path := range parent {
		if _, exists := current[path]; !exists {
			return nil, fmt.Errorf("controller record %q was removed", path)
		}
	}
	return out, nil
}

func journalCandidate(entries []sqlite.SyncJournalEntry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		var v struct {
			Candidate string `json:"candidate"`
		}
		if json.Unmarshal([]byte(entries[i].EvidenceJSON), &v) == nil && v.Candidate != "" {
			return v.Candidate
		}
	}
	return ""
}

func journalPreparedCandidate(entries []sqlite.SyncJournalEntry) (string, time.Time) {
	for i := len(entries) - 1; i >= 0; i-- {
		var v struct {
			Tree       string `json:"tree"`
			CommitTime string `json:"commit_time"`
		}
		if json.Unmarshal([]byte(entries[i].EvidenceJSON), &v) != nil || v.Tree == "" || v.CommitTime == "" {
			continue
		}
		commitTime, err := time.Parse(time.RFC3339, v.CommitTime)
		if err == nil {
			return v.Tree, commitTime
		}
	}
	return "", time.Time{}
}

func finishPublicationFailure(stderr io.Writer, store *sqlite.Store, job sqlite.SyncJobRow, owner, state, outcome, reason string) int {
	return finishPublicationFailureWithEvidence(stderr, store, job, owner, state, outcome, reason, nil)
}

func finishPublicationFailureWithEvidence(stderr io.Writer, store *sqlite.Store, job sqlite.SyncJobRow, owner, state, outcome, reason string, evidence map[string]any) int {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if evidence == nil {
		evidence = map[string]any{}
	}
	evidence["reason"] = reason
	err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, state, sqlite.SyncJobKeepUnresolved, sqlite.SyncJournalEntry{JournalID: randomSyncID("publication-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "publication", Outcome: outcome, EvidenceJSON: mustJSON(evidence), RecordedAt: now}, now)
	if err != nil {
		return syncStoreError(stderr, "sync publish", err)
	}
	code, errorCode, category := 14, "sync_precondition_failed", "conflict"
	if state == "uncertain" {
		code, errorCode, category = 13, "sync_effect_unknown", "acceptance_unknown"
	}
	writeError(stderr, "sync publish", errorCode, category, reason)
	return code
}

func finishPublicationRetryable(stderr io.Writer, store *sqlite.Store, job sqlite.SyncJobRow, owner string, evidence map[string]any, reason string) int {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	evidence["reason"] = reason
	if err := store.FinishSyncJob(requestCtx(), job.JobID, owner, job.Fence, "signed", sqlite.SyncJobKeepUnresolved, sqlite.SyncJournalEntry{JournalID: randomSyncID("publication-journal"), JobID: job.JobID, Fence: job.Fence, Phase: "publication", Outcome: "effect_not_started", EvidenceJSON: mustJSON(evidence), RecordedAt: now}, now); err != nil {
		return syncStoreError(stderr, "sync publish", err)
	}
	writeError(stderr, "sync publish", "sync_retryable", "transient_local", reason)
	return 10
}
func publicationResult(stdout io.Writer, p syncrecords.Publication, candidate string, idempotent bool) int {
	return writeEnvelope(stdout, "sync publish", map[string]any{"schema_version": "agent-dispatch.sync-publish-result/v1", "state": "published", "publication_id": p.PublicationID, "candidate_commit": candidate, "remote_commit": candidate, "idempotent": idempotent, "side_effects": func() []string {
		if idempotent {
			return []string{}
		}
		return []string{"git_objects_written", "remote_content_ref_updated", "local_content_ref_updated", "peer_delivery_admitted", "state_committed"}
	}()})
}

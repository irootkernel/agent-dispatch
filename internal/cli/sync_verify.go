package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/adapters/secretresolver"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

const verificationCommandDeadline = 4 * time.Minute

func runSyncVerify(args []string, stdout, stderr io.Writer) int {
	httpClient := peerHTTPClient()
	defer httpClient.CloseIdleConnections()
	return runSyncVerifyWithHTTP(args, stdout, stderr, httpClient)
}

func runSyncVerifyWithHTTP(args []string, stdout, stderr io.Writer, httpClient *http.Client) int {
	const command = "sync verify"
	flags, ok := parseClosedSyncFlags(args, map[string]bool{"--group": true})
	if !ok || flags["--group"] == "" {
		return usageError(stderr, command, "verify requires --group GROUP and accepts --output json")
	}
	cfg, s, code := loadMembershipConfig(command, flags["--group"], false, stderr)
	if code != 0 {
		return code
	}
	if !s.Enabled {
		return planErr(stderr, command, "sync_precondition_failed", "conflict", "sync must be enabled before verification", 14)
	}
	_, store, code := openOperatorStore(command, "", stderr)
	if code != 0 {
		return code
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(requestCtx(), verificationCommandDeadline)
	defer cancel()
	revision, _ := config.SyncRevision(cfg)
	if _, err := store.EnsureSyncControl(ctx, s.GroupID, revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return syncStoreError(stderr, command, err)
	}
	if err := store.ExpireInterruptedSyncVerifications(ctx, s.GroupID, time.Now()); err != nil {
		return syncStoreError(stderr, command, err)
	}
	client, err := membershipGitClient(cfg, s)
	if err != nil {
		return syncMembershipError(stderr, command, err, 3)
	}
	service := &peerService{cfg: cfg}
	membership, pair, err := service.currentMembers(ctx)
	if err != nil {
		return syncMembershipError(stderr, command, err, 30)
	}
	remoteMembership, err := client.RemoteRef(ctx, s.RemoteName, s.MembershipRef, s.RemoteRepositoryDigest)
	if err != nil {
		return syncVerificationRefError(stderr, command, err)
	}
	if remoteMembership != membership {
		return syncMembershipError(stderr, command, errors.New("local membership revision is not current on the approved remote"), 30)
	}
	target, err := client.RemoteRef(ctx, s.RemoteName, s.ContentRef, s.RemoteRepositoryDigest)
	if err != nil {
		return syncVerificationRefError(stderr, command, err)
	}
	localTarget, err := client.ResolveRef(ctx, s.ContentRef)
	if err != nil {
		return syncVerificationRefError(stderr, command, err)
	}
	verification := syncrecords.Verification{
		SchemaVersion: syncrecords.VerificationSchema, VerificationID: randomSyncID("verification"),
		GroupID: s.GroupID, MembershipRevision: membership, ContentRef: s.ContentRef,
		TargetCommit: target, ScopeDigest: config.SyncScopeDigest(cfg, s.Resource),
		ContractDigest: config.SyncContractDigest(), Phase: "finished", Result: "incomplete",
		ExpectedNodes: []syncrecords.ExpectedNode{
			{InstanceID: pair[1].InstanceID, StateIncarnationID: pair[1].StateIncarnationID},
			{InstanceID: pair[0].InstanceID, StateIncarnationID: pair[0].StateIncarnationID},
		},
		Nodes: []syncrecords.VerificationNode{}, Fence: 1, RetainUntilResolved: true,
	}
	// Admission precedes network observations. An interrupted attempt stays
	// visible as a planned verification rather than a completed pair claim.
	// Admission is an internal intent, not a finished verification record.
	planned := map[string]any{
		"verification_id":     verification.VerificationID,
		"group_id":            verification.GroupID,
		"membership_revision": verification.MembershipRevision,
		"target_commit":       verification.TargetCommit,
		"phase":               "planned",
	}
	job, _, err := store.AdmitSyncJob(ctx, sqlite.SyncJobInput{
		JobID: verification.VerificationID, GroupID: s.GroupID, Kind: "verification",
		LogicalKey: verification.VerificationID, InitialState: "planned",
		PayloadJSON: mustJSON(planned), ConfigRevision: "", QueueLimit: s.Bounds.Queue,
		Now: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return syncStoreError(stderr, command, err)
	}
	selfNonce, peerNonce := randomSyncID("nonce"), randomSyncID("nonce")
	peerRequest := syncVerificationRequest(verification, pair[1].InstanceID, pair[0].InstanceID, peerNonce)
	peer, err := querySyncPeer(ctx, cfg, httpClient, pair[0].Endpoint, peerRequest)
	var reasons []string
	if err != nil {
		reasons = append(reasons, "peer_unavailable")
	}
	selfRequest := syncVerificationRequest(verification, pair[0].InstanceID, pair[1].InstanceID, selfNonce)
	local := observeSyncPeer(ctx, cfg, store, client, selfRequest)
	if syncResponseMatches(local, selfRequest, pair[1]) {
		verification.Nodes = append(verification.Nodes, syncrecords.VerificationNodeFromStatus(local))
		if local.State != "applied" || local.GovernedDirty || local.PendingWork || !local.MembershipCurrent || local.Uncertain {
			reasons = append(reasons, "local_incomplete")
		}
	} else {
		reasons = append(reasons, "local_binding_mismatch")
	}
	if err == nil && syncResponseMatches(peer, peerRequest, pair[0]) {
		verification.Nodes = append(verification.Nodes, syncrecords.VerificationNodeFromStatus(peer))
		if peer.State != "applied" || peer.GovernedDirty || peer.PendingWork || !peer.MembershipCurrent || peer.Uncertain || peer.EvidenceAgeSeconds > syncrecords.MaxEvidenceAgeSeconds {
			reasons = append(reasons, "peer_incomplete")
		}
	} else if err == nil {
		reasons = append(reasons, "peer_binding_mismatch")
	}
	changed, uncertain := syncVerificationTargetChanged(ctx, cfg, client, membership, target, localTarget)
	if changed {
		verification.Result = "target_changed"
		reasons = append(reasons, "target_changed")
	} else if uncertain {
		reasons = append(reasons, "target_recheck_unavailable")
	} else if verification.CleanPair() {
		finalLocal := observeSyncPeer(ctx, cfg, store, client, selfRequest)
		if syncResponseMatches(finalLocal, selfRequest, pair[1]) {
			verification.Nodes[0] = syncrecords.VerificationNodeFromStatus(finalLocal)
		}
		finalChanged, finalUncertain := syncVerificationTargetChanged(ctx, cfg, client, membership, target, localTarget)
		control, controlErr := store.LoadSyncControl(ctx, s.GroupID)
		currentRevision, _ := config.SyncRevision(cfg)
		controlCurrent := controlErr == nil && control.State == "active" && control.MembershipMode == "normal" && control.ConfigRevision == currentRevision
		result, reason := finalVerificationDisposition(finalChanged, uncertain || finalUncertain,
			controlCurrent, syncResponseMatches(finalLocal, selfRequest, pair[1]), verification.CleanPair())
		verification.Result = result
		if reason != "" {
			reasons = append(reasons, reason)
		} else {
			verification.RetainUntilResolved = false
		}
	}
	refreshVerificationAges(&verification, time.Now())
	if verification.Result == "complete" && !verification.CleanPair() {
		verification.Result = "incomplete"
		verification.RetainUntilResolved = true
		reasons = append(reasons, "peer_incomplete")
	}
	if err := verification.Validate(); err != nil {
		return syncMembershipError(stderr, command, err, 30)
	}
	if err := store.FinishSyncVerification(ctx, job.JobID, verification.Result, mustJSON(map[string]any{"verification": verification, "reason_codes": reasons}), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return syncStoreError(stderr, command, err)
	}
	return writeEnvelopeWithWarnings(stdout, command, verification, reasons)
}

func finalVerificationDisposition(changed, uncertain, controlCurrent, localMatches, cleanPair bool) (result, reason string) {
	switch {
	case changed:
		return "target_changed", "target_changed"
	case uncertain:
		return "incomplete", "target_recheck_unavailable"
	case !controlCurrent || !localMatches || !cleanPair:
		return "incomplete", "local_changed_during_verification"
	default:
		return "complete", ""
	}
}

func syncVerificationRefError(stderr io.Writer, command string, err error) int {
	if errors.Is(err, gitlocal.ErrRemoteBinding) || errors.Is(err, gitlocal.ErrMissingRef) {
		return syncMembershipError(stderr, command, err, 30)
	}
	return syncRetryableError(stderr, command, err)
}

func syncVerificationRequest(v syncrecords.Verification, sender, receiver, nonce string) syncrecords.StatusRequest {
	return syncrecords.StatusRequest{SchemaVersion: syncrecords.StatusRequestSchema, GroupID: v.GroupID,
		Sender: sender, Receiver: receiver, MembershipRevision: v.MembershipRevision,
		ContentRef: v.ContentRef, TargetCommit: v.TargetCommit, ScopeDigest: v.ScopeDigest,
		ContractDigest: v.ContractDigest, Nonce: nonce}
}

func syncResponseMatches(r syncrecords.StatusResponse, request syncrecords.StatusRequest, member syncrecords.ActiveMember) bool {
	return r.Validate() == nil && r.GroupID == request.GroupID && r.Responder == request.Receiver &&
		r.StateIncarnationID == member.StateIncarnationID && r.MembershipRevision == request.MembershipRevision &&
		r.ContentRef == request.ContentRef && r.TargetCommit == request.TargetCommit &&
		r.ScopeDigest == request.ScopeDigest && r.ContractDigest == request.ContractDigest && r.Nonce == request.Nonce
}

func refreshVerificationAges(v *syncrecords.Verification, now time.Time) {
	for i := range v.Nodes {
		node := &v.Nodes[i]
		elapsed := now.Sub(time.Unix(0, node.EvidenceGeneration))
		if elapsed > 0 {
			age := syncrecords.EvidenceAgeSecondsSince(elapsed)
			if age > node.EvidenceAgeSeconds {
				node.EvidenceAgeSeconds = age
			}
		}
		node.EvidenceFresh = node.EvidenceAgeSeconds <= syncrecords.MaxEvidenceAgeSeconds
	}
}

func syncVerificationTargetChanged(ctx context.Context, cfg *config.Config, client *gitlocal.Client, membership, target, localTarget string) (bool, bool) {
	s := cfg.Sync
	localMembership, err := client.ResolveRef(ctx, s.MembershipRef)
	if err != nil {
		return false, true
	}
	if localMembership != membership {
		return true, false
	}
	localContent, err := client.ResolveRef(ctx, s.ContentRef)
	if err != nil {
		return false, true
	}
	if localContent != localTarget {
		return true, false
	}
	remoteMembership, err := client.RemoteRef(ctx, s.RemoteName, s.MembershipRef, s.RemoteRepositoryDigest)
	if err != nil {
		return false, true
	}
	if remoteMembership != membership {
		return true, false
	}
	remoteTarget, err := client.RemoteRef(ctx, s.RemoteName, s.ContentRef, s.RemoteRepositoryDigest)
	if err != nil {
		return false, true
	}
	if remoteTarget != target {
		return true, false
	}
	currentCfg, err := config.Load(resolveConfigPath(""))
	if err != nil {
		return false, true
	}
	oldRevision, _ := config.SyncRevision(cfg)
	currentRevision, _ := config.SyncRevision(currentCfg)
	if oldRevision != currentRevision || currentCfg.Sync == nil {
		return true, false
	}
	return syncVerificationPairChanged(s.Nodes, currentCfg.Sync.Nodes), false
}

// SYN-013 pins identities and incarnations independently of the cooperative
// acknowledgement revision, which deliberately excludes roster movement.
func syncVerificationPairChanged(pinned, current []config.SyncNode) bool {
	if len(pinned) != len(current) {
		return true
	}
	for _, node := range pinned {
		found := false
		for _, candidate := range current {
			if node.InstanceID == candidate.InstanceID && node.StateIncarnationID == candidate.StateIncarnationID {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}

func querySyncPeer(ctx context.Context, cfg *config.Config, client *http.Client, rawEndpoint string, request syncrecords.StatusRequest) (syncrecords.StatusResponse, error) {
	var empty syncrecords.StatusResponse
	var local *config.SyncNode
	for i := range cfg.Sync.Nodes {
		if cfg.Sync.Nodes[i].InstanceID == cfg.Sync.LocalInstanceID {
			local = &cfg.Sync.Nodes[i]
		}
	}
	if local == nil {
		return empty, fmt.Errorf("local peer credential is not configured")
	}
	ref, err := config.ParseSecretRef(local.CredentialRef)
	if err != nil {
		return empty, err
	}
	credential, err := secretresolver.Resolve(ctx, ref)
	if err != nil {
		return empty, err
	}
	endpoint, err := syncrecords.ParseTailnetEndpoint(rawEndpoint)
	if err != nil {
		return empty, fmt.Errorf("configured peer endpoint is invalid")
	}
	endpoint.Path = "/v1/sync/status"
	body, err := json.Marshal(request)
	if err != nil {
		return empty, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return empty, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+credential)
	response, err := client.Do(httpRequest)
	if err != nil {
		return empty, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return empty, fmt.Errorf("peer status unavailable")
	}
	limited := io.LimitReader(response.Body, syncrecords.MaxPeerStatusBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return empty, err
	}
	result, err := syncrecords.DecodeStatusResponse(raw)
	if err != nil {
		return empty, err
	}
	// Generation is the responder's observation time in Unix nanoseconds.
	// Preserve any reported cache age and increase it for elapsed wall time.
	observedAt := time.Unix(0, result.EvidenceGeneration)
	if observedAt.After(time.Now().Add(5*time.Second)) || result.EvidenceGeneration < 1_000_000_000_000_000 {
		return empty, fmt.Errorf("peer observation generation is invalid")
	}
	elapsed := time.Since(observedAt)
	if elapsed > 0 {
		age := syncrecords.EvidenceAgeSecondsSince(elapsed)
		if age > result.EvidenceAgeSeconds {
			result.EvidenceAgeSeconds = age
		}
	}
	return result, nil
}

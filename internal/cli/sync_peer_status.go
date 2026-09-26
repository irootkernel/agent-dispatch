package cli

import (
	"context"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/syncpublication"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

// observeSyncPeer reads local facts for one request. Every invocation measures
// Git and SQLite anew; the request nonce cannot refresh a cached observation.
func observeSyncPeer(ctx context.Context, cfg *config.Config, store *sqlite.Store, client *gitlocal.Client, request syncrecords.StatusRequest) syncrecords.StatusResponse {
	s := cfg.Sync
	started := time.Now()
	response := syncrecords.StatusResponse{
		SchemaVersion: syncrecords.StatusResponseSchema, GroupID: s.GroupID,
		Responder: s.LocalInstanceID, StateIncarnationID: localSyncIncarnation(cfg),
		MembershipRevision: request.MembershipRevision, ContentRef: s.ContentRef,
		ScopeDigest: config.SyncScopeDigest(cfg, s.Resource), ContractDigest: config.SyncContractDigest(),
		Nonce: request.Nonce, EvidenceGeneration: started.UnixNano(),
		State: "unknown", GovernedDirty: true, PendingWork: true, Uncertain: true,
	}
	content, err := client.ResolveRef(ctx, s.ContentRef)
	if err != nil {
		return response
	}
	response.TargetCommit = content
	membership, err := client.ResolveRef(ctx, s.MembershipRef)
	if err != nil {
		return response
	}
	response.MembershipRevision = membership
	response.MembershipCurrent = membership == request.MembershipRevision
	state, err := client.InspectWorkingPaths(ctx)
	if err != nil || state.Head != content || state.CheckedOutRef != s.ContentRef || state.ActiveOperation != "" {
		return response
	}
	response.GovernedDirty = false
	for _, path := range append(state.DirtyPaths, state.UntrackedPaths...) {
		if syncpublication.ReservedMetadataPath(path) {
			response.GovernedDirty = true
			return response
		}
		governed, err := syncpublication.ClassifyLocalPath(cfg, s.Resource, path)
		if err != nil {
			response.GovernedDirty = true
			return response
		}
		if governed {
			response.GovernedDirty = true
			break
		}
	}
	pending := false
	for _, kind := range []string{"publication", "delivery", "import"} {
		jobs, err := store.LoadUnresolvedSyncJobs(ctx, s.GroupID, kind)
		if err != nil {
			return response
		}
		pending = pending || len(jobs) > 0
	}
	inbox, err := store.LoadPeerNudgeBacklog(ctx, s.GroupID)
	if err != nil {
		return response
	}
	writersIdle, err := store.ResourceWritersIdle(ctx, s.Resource)
	if err != nil {
		return response
	}
	control, err := store.LoadSyncControl(ctx, s.GroupID)
	if err != nil {
		return response
	}
	response.PendingWork = pending || inbox.Pending > 0 || inbox.Failed > 0 || !writersIdle
	if control.State == "blocked" || control.MembershipMode != "normal" {
		response.State = "blocked"
		return response
	}
	if control.State == "paused" {
		response.State = "paused"
		return response
	}
	if control.State != "active" || !response.MembershipCurrent || control.ConfigRevision == "" {
		return response
	}
	currentRevision, _ := config.SyncRevision(cfg)
	if control.ConfigRevision != currentRevision {
		return response
	}
	response.Uncertain = false
	switch {
	case response.GovernedDirty:
		response.State = "local_dirty"
	case content != request.TargetCommit:
		response.State = "behind"
	default:
		response.State = "applied"
	}
	response.EvidenceAgeSeconds = syncrecords.EvidenceAgeSecondsSince(time.Since(started))
	return response
}

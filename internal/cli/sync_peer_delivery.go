package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/secretresolver"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

// The peer transport follows no redirects or ambient proxy configuration.
// The configured .ts.net origin supplies TLS identity and the only destination.
func peerHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			DialContext:  (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}).DialContext,
			MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: 15 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (s *peerService) deliveryLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	client := peerHTTPClient()
	defer client.CloseIdleConnections()
	for {
		if err := s.deliverPending(ctx, client); err != nil {
			// The jobs remain durable. Report a bounded, non-sensitive reason
			// because the listener can keep serving after a worker failure.
			s.warn("peer delivery worker deferred")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *peerService) deliverPending(ctx context.Context, client *http.Client) error {
	if reason := s.configurationProblem(); reason != "" {
		s.warn(reason)
		return fmt.Errorf("service %s", reason)
	}
	jobs, err := s.store.LoadUnresolvedSyncJobs(ctx, s.cfg.Sync.GroupID, "delivery")
	if err != nil {
		return err
	}
	revision, _ := config.SyncRevision(s.cfg)
	for _, job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if job.ClaimOwner != "" {
			expiry, parseErr := time.Parse(time.RFC3339Nano, job.ClaimExpiresAt)
			if parseErr != nil || time.Now().Before(expiry) {
				continue
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			journal := sqlite.SyncJournalEntry{JournalID: randomSyncID("delivery-recovery"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_unknown", EvidenceJSON: `{"reason":"process_lost_after_delivery_claim"}`, RecordedAt: now}
			if err := s.store.ReconcileExpiredSyncClaim(ctx, job.JobID, job.Fence, "effect_unknown", journal, now); err != nil {
				return err
			}
			continue
		}
		if job.State == "unknown" {
			// A replay is safe because the receiving inbox admits the same
			// logical publication and exact fingerprint idempotently.
			now := time.Now().UTC().Format(time.RFC3339Nano)
			journal := sqlite.SyncJournalEntry{JournalID: randomSyncID("delivery-recovery"), JobID: job.JobID, Fence: job.Fence, Phase: "claim_recovery", Outcome: "effect_unknown", EvidenceJSON: `{"reason":"retry_same_idempotent_nudge"}`, RecordedAt: now}
			if err := s.store.PrepareUnknownDeliveryRetry(ctx, job.JobID, job.Fence, journal, now); err != nil {
				return err
			}
			continue
		}
		if job.State != "pending" && job.State != "retryable" {
			continue
		}
		if !deliveryDue(job, time.Now()) {
			continue
		}
		now := time.Now().UTC()
		owner := randomSyncID("delivery-worker")
		claimed, err := s.store.ClaimSyncJob(ctx, job.JobID, owner, revision, now.Format(time.RFC3339Nano), now.Add(30*time.Second).Format(time.RFC3339Nano))
		if err != nil {
			if errors.Is(err, sqlite.ErrSyncControlHeld) {
				s.warn("sync control prevents delivery; inspect sync status and run sync reconcile if configuration binding is stale")
			}
			continue
		}
		state, outcome, status := s.sendNudge(ctx, client, claimed)
		terminal := time.Now().UTC().Format(time.RFC3339Nano)
		disposition := sqlite.SyncJobKeepUnresolved
		if state == "accepted" || state == "refused" {
			disposition = sqlite.SyncJobResolve
		}
		journal := sqlite.SyncJournalEntry{
			JournalID: randomSyncID("delivery-journal"), JobID: claimed.JobID, Fence: claimed.Fence,
			Phase: "delivery", Outcome: outcome,
			EvidenceJSON: mustJSON(map[string]any{"http_status": status, "publication_id": claimed.LogicalKey}), RecordedAt: terminal,
		}
		if err := s.store.FinishSyncJob(ctx, claimed.JobID, owner, claimed.Fence, state, disposition, journal, terminal); err != nil {
			return err
		}
	}
	return nil
}

func deliveryDue(job sqlite.SyncJobRow, now time.Time) bool {
	if job.Attempts == 0 {
		return true
	}
	last, err := time.Parse(time.RFC3339Nano, job.UpdatedAt)
	if err != nil {
		return false
	}
	delay := 10 * time.Second
	for i := 1; i < job.Attempts && delay < 5*time.Minute; i++ {
		delay *= 2
	}
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	return !now.Before(last.Add(delay))
}

func (s *peerService) sendNudge(ctx context.Context, client *http.Client, job sqlite.SyncJobRow) (state, outcome string, httpStatus int) {
	nudge, err := syncrecords.DecodeNudge([]byte(job.PayloadJSON))
	if err != nil || nudge.GroupID != s.cfg.Sync.GroupID || nudge.Sender != s.cfg.Sync.LocalInstanceID || nudge.ContentRef != s.cfg.Sync.ContentRef {
		return "refused", "refused", 0
	}
	head, members, err := s.members(ctx)
	if err != nil {
		return "retryable", "retryable", 0
	}
	if nudge.MembershipRevision != head || nudge.Receiver != members[0].InstanceID {
		return "refused", "refused", 0
	}
	var self, peer *config.SyncNode
	for i := range s.cfg.Sync.Nodes {
		node := &s.cfg.Sync.Nodes[i]
		if node.InstanceID == nudge.Sender {
			self = node
		}
		if node.InstanceID == nudge.Receiver {
			peer = node
		}
	}
	if self == nil || peer == nil || peer.Endpoint != members[0].Endpoint {
		return "refused", "refused", 0
	}
	ref, err := config.ParseSecretRef(self.CredentialRef)
	if err != nil {
		return "refused", "refused", 0
	}
	credential, err := secretresolver.Resolve(ctx, ref)
	if err != nil {
		return "retryable", "retryable", 0
	}
	endpoint, err := syncrecords.ParseTailnetEndpoint(peer.Endpoint)
	if err != nil {
		return "refused", "refused", 0
	}
	endpoint.Path = "/v1/sync/nudges"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewBufferString(job.PayloadJSON))
	if err != nil {
		return "refused", "refused", 0
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+credential)
	response, err := client.Do(request)
	if err != nil {
		// The peer may have committed before the response was lost. Retain
		// uncertainty and retry the identical logical request later.
		return "unknown", "effect_unknown", 0
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	switch response.StatusCode {
	case http.StatusAccepted:
		return "accepted", "accepted", response.StatusCode
	case http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		return "retryable", "retryable", response.StatusCode
	default:
		if response.StatusCode >= 500 {
			return "retryable", "retryable", response.StatusCode
		}
		return "refused", "refused", response.StatusCode
	}
}

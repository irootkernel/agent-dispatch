package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

// The inbox wakes the same guarded reconcile path as the operator command.
// Its pending rows remain after a deferral or process loss. Periodic remote
// inspection independent of inbox delivery is installed by E22-T2.
func (s *peerService) inboxLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		s.processInbox(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}

func (s *peerService) processInbox(ctx context.Context) {
	if reason := s.configurationProblem(); reason != "" {
		s.warn(reason)
		return
	}
	control, err := s.store.LoadSyncControl(ctx, s.cfg.Sync.GroupID)
	if err != nil {
		s.warn("sync control unavailable")
		return
	}
	if control.State == "paused" {
		return
	}
	rows, err := s.store.LoadPendingPeerNudges(ctx, s.cfg.Sync.GroupID, s.cfg.Sync.Bounds.Queue)
	if err != nil {
		s.warn("peer inbox read failed")
		return
	}
	if len(rows) == 0 {
		return
	}
	settled, reconciledTarget := s.runReconcile(ctx)
	if !settled {
		for _, row := range rows {
			_ = s.store.RecordPeerNudgeFailure(ctx, row.GroupID, row.PublicationID, row.Fingerprint, "reconcile_failed")
		}
		s.warn("peer inbox reconcile deferred")
		return
	}
	client, err := membershipGitClient(s.cfg, s.cfg.Sync)
	if err != nil {
		s.warn("peer inbox local Git unavailable")
		return
	}
	local, err := client.ResolveRef(ctx, s.cfg.Sync.ContentRef)
	if err != nil || local != reconciledTarget {
		for _, row := range rows {
			_ = s.store.RecordPeerNudgeFailure(ctx, row.GroupID, row.PublicationID, row.Fingerprint, "local_ref_unavailable")
		}
		s.warn("peer inbox local ref is not reconciled")
		return
	}
	s.settleInboxRows(ctx, client, local, rows)
}

// Reconcile in a cancellable child of the service, preserving the operator
// command's guard and outcome contract. Cancellation kills its Git children
// with the command so a stopped service cannot leave an import running.
func (s *peerService) runReconcile(ctx context.Context) (bool, string) {
	if s.reconcile != nil {
		return s.reconcile(ctx)
	}
	executable, err := os.Executable()
	if err != nil {
		return false, ""
	}
	cmd := exec.CommandContext(ctx, executable, "sync", "reconcile", "--state-dir="+stateDirOf(s.cfg), "--group", s.cfg.Sync.GroupID)
	cmd.Env = append(os.Environ(), "AGENT_DISPATCH_CONFIG="+s.configPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.Stderr = io.Discard
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return false, ""
	}
	return reconcileSettled(stdout.Bytes())
}

func (s *peerService) settleInboxRows(ctx context.Context, client *gitlocal.Client, local string, rows []sqlite.PeerNudgeRow) {
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		nudge, err := syncrecords.DecodeNudge([]byte(row.PayloadJSON))
		if err != nil {
			if s.store.ResolvePeerNudge(ctx, row.GroupID, row.PublicationID, row.Fingerprint, "invalid_payload", time.Now().UTC().Format(time.RFC3339Nano)) != nil {
				s.warn("peer inbox resolution failed")
			}
			continue
		}
		if nudge.GroupID != s.cfg.Sync.GroupID || nudge.Receiver != s.cfg.Sync.LocalInstanceID {
			if s.store.ResolvePeerNudge(ctx, row.GroupID, row.PublicationID, row.Fingerprint, "obsolete_binding", time.Now().UTC().Format(time.RFC3339Nano)) != nil {
				s.warn("peer inbox resolution failed")
			}
			continue
		}
		covered, err := nudgeCoveredByLocalHistory(ctx, client, local, nudge.TargetCommit, s.cfg.Sync.Bounds.HistoryCommits)
		if err != nil {
			_ = s.store.RecordPeerNudgeFailure(ctx, row.GroupID, row.PublicationID, row.Fingerprint, "coverage_unavailable")
			s.warn("peer inbox coverage unavailable")
			continue
		}
		resolution := "covered"
		if !covered {
			// The approved remote head was just reconciled. Retain the hint
			// as superseded without claiming its target was imported.
			resolution = "superseded"
		}
		if err := s.store.ResolvePeerNudge(ctx, row.GroupID, row.PublicationID, row.Fingerprint, resolution, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			s.warn("peer inbox resolution failed")
		}
	}
}

// Walk only from the configured local ref through discovered parents. The
// peer-supplied target is compared as data and never passed to Git as an
// object selector. A non-linear history retains the inbox row; a target past
// the scan bound can be superseded only after the approved remote reconciles.
func nudgeCoveredByLocalHistory(ctx context.Context, client *gitlocal.Client, local, target string, bound int) (bool, error) {
	current := local
	for i := 0; i < bound; i++ {
		if current == target {
			return true, nil
		}
		parents, err := client.CommitParents(ctx, current)
		if err != nil {
			return false, err
		}
		if len(parents) == 0 {
			return false, nil
		}
		if len(parents) != 1 {
			return false, fmt.Errorf("local sync history is not first-parent linear")
		}
		current = parents[0]
	}
	return false, nil
}

func reconcileSettled(raw []byte) (bool, string) {
	var envelope struct {
		Result struct {
			State        string `json:"state"`
			TargetCommit string `json:"target_commit"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return false, ""
	}
	switch envelope.Result.State {
	case "no_change", "applied", "recovered":
		return envelope.Result.TargetCommit != "", envelope.Result.TargetCommit
	default:
		return false, ""
	}
}

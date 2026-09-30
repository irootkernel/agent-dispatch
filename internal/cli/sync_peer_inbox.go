package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

// One worker serializes inbox wakes and configured-ref inspection. The
// persisted schedule prevents restarts or failed nudges from spinning while
// a healthy wake can still shorten the ordinary periodic interval.
func (s *peerService) inboxLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	wake := true // startup uncertainty requires a configured-ref inspection
	for {
		s.recoverScheduled(ctx, wake)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
			wake = true
		case <-ticker.C:
			wake = false
		}
	}
}

func (s *peerService) recoverScheduled(ctx context.Context, wake bool) {
	if !s.recoveryAllowed(ctx) {
		return
	}
	attempt := randomSyncID("recovery")
	due, err := s.store.ReserveSyncRecovery(ctx, s.cfg.Sync.GroupID, attempt, time.Now(), wake)
	if err != nil {
		s.warn("recovery schedule unavailable")
		return
	}
	if !due {
		return
	}
	if pending, _, err := s.store.PreSignaturePublications(ctx, s.cfg.Sync.GroupID, time.Now()); err == nil && pending != 0 {
		s.warn("pre-signature publication awaits explicit sync publish re-entry")
	}
	rows, err := s.store.LoadPendingPeerNudges(ctx, s.cfg.Sync.GroupID, s.cfg.Sync.Bounds.Queue)
	settled, reason := false, sqlite.SyncRecoveryReasonReconcileFailed
	if err == nil {
		if len(rows) != 0 {
			settled, reason = s.processInboxRows(ctx, rows)
		} else {
			settled, _ = s.runReconcile(ctx)
		}
	} else {
		reason = sqlite.SyncRecoveryReasonInboxUnavailable
		s.warn("peer inbox read failed")
	}
	if err := s.store.CompleteSyncRecovery(ctx, s.cfg.Sync.GroupID, attempt, time.Now(), settled, reason); err != nil && ctx.Err() == nil {
		s.warn("recovery schedule update failed")
	}
	if !settled && ctx.Err() == nil {
		s.warn("configured-ref reconciliation deferred")
	}
}

func (s *peerService) recoveryAllowed(ctx context.Context) bool {
	if reason := s.configurationProblem(); reason != "" {
		s.warn(reason)
		return false
	}
	control, err := s.store.LoadSyncControl(ctx, s.cfg.Sync.GroupID)
	if err != nil {
		s.warn("sync control unavailable")
		return false
	}
	revision, _ := config.SyncRevision(s.cfg)
	if control.ConfigRevision != revision {
		s.warn("sync control configuration binding is stale; run sync reconcile")
		return false
	}
	if control.State == "paused" {
		return false
	}
	return true
}

func (s *peerService) processInboxRows(ctx context.Context, rows []sqlite.PeerNudgeRow) (bool, string) {
	settled, reconciledTarget := s.runReconcile(ctx)
	if !settled {
		for _, row := range rows {
			_ = s.store.RecordPeerNudgeFailure(ctx, row.GroupID, row.PublicationID, row.Fingerprint, sqlite.SyncRecoveryReasonReconcileFailed)
		}
		s.warn("peer inbox reconcile deferred")
		return false, sqlite.SyncRecoveryReasonReconcileFailed
	}
	client, err := membershipGitClient(s.cfg, s.cfg.Sync)
	if err != nil {
		s.warn("peer inbox local Git unavailable")
		return false, sqlite.SyncRecoveryReasonLocalRefUnavailable
	}
	local, err := client.ResolveRef(ctx, s.cfg.Sync.ContentRef)
	if err != nil || local != reconciledTarget {
		for _, row := range rows {
			_ = s.store.RecordPeerNudgeFailure(ctx, row.GroupID, row.PublicationID, row.Fingerprint, sqlite.SyncRecoveryReasonLocalRefUnavailable)
		}
		s.warn("peer inbox local ref is not reconciled")
		return false, sqlite.SyncRecoveryReasonLocalRefUnavailable
	}
	if !s.settleInboxRows(ctx, client, local, rows) {
		return false, sqlite.SyncRecoveryReasonInboxUnsettled
	}
	return true, sqlite.SyncRecoveryReasonNone
}

// Reconcile in a cancellable child of the service, preserving the operator
// command's guard and outcome contract. Cancellation asks the child to stop
// and waits for its Git groups before reporting successful service shutdown.
func (s *peerService) runReconcile(ctx context.Context) (bool, string) {
	if s.reconcile != nil {
		return s.reconcile(ctx)
	}
	executable, err := os.Executable()
	if err != nil {
		return false, ""
	}
	// A full bounded history can outlive any single claim lease. Let the
	// operator command's per-operation limits bound its work, while service
	// cancellation still reaches the child and each Git operation's context.
	for attempt := 0; attempt < 2; attempt++ {
		cmd := exec.CommandContext(ctx, executable, "sync", "reconcile", "--state-dir="+stateDirOf(s.cfg), "--group", s.cfg.Sync.GroupID)
		cmd.Env = append(os.Environ(), "AGENT_DISPATCH_CONFIG="+s.configPath)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			// Reconciliation cancels and waits for Git's separate process
			// groups before exiting; killing only this group cannot clean up
			// those descendants.
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		cmd.WaitDelay = 5 * time.Second
		cmd.Stderr = io.Discard
		stdout := boundedReconcileOutput{limit: s.cfg.Sync.Bounds.SubprocessBytes}
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			var exitErr *exec.ExitError
			if ctx.Err() != nil && (errors.Is(err, exec.ErrWaitDelay) ||
				(errors.As(err, &exitErr) && exitErr.ProcessState.Sys().(syscall.WaitStatus).Signal() == syscall.SIGKILL)) {
				s.reconcileShutdownErr = errors.New("reconciliation did not reach its shutdown boundary")
			}
			return false, ""
		}
		state, target := parseReconcileResult(stdout.buffer.Bytes())
		if state == "membership_adopted" {
			continue // a normal adoption needs a content pass before inbox settlement
		}
		return settledReconcileResult(state, target)
	}
	return false, ""
}

type boundedReconcileOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedReconcileOutput) Write(p []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if len(p) > remaining {
		if remaining > 0 {
			_, _ = b.buffer.Write(p[:remaining])
		}
		return max(remaining, 0), errors.New("reconcile output exceeds configured limit")
	}
	return b.buffer.Write(p)
}

func (s *peerService) settleInboxRows(ctx context.Context, client *gitlocal.Client, local string, rows []sqlite.PeerNudgeRow) bool {
	settled := true
	history := localHistoryCoverage{client: client, current: local, remaining: s.cfg.Sync.Bounds.HistoryCommits, covered: make(map[string]bool)}
	for _, row := range rows {
		if ctx.Err() != nil {
			return false
		}
		nudge, err := syncrecords.DecodeNudge([]byte(row.PayloadJSON))
		if err != nil {
			if s.store.ResolvePeerNudge(ctx, row.GroupID, row.PublicationID, row.Fingerprint, "invalid_payload", time.Now().UTC().Format(time.RFC3339Nano)) != nil {
				s.warn("peer inbox resolution failed")
				settled = false
			}
			continue
		}
		if nudge.GroupID != s.cfg.Sync.GroupID || nudge.Receiver != s.cfg.Sync.LocalInstanceID {
			if s.store.ResolvePeerNudge(ctx, row.GroupID, row.PublicationID, row.Fingerprint, "obsolete_binding", time.Now().UTC().Format(time.RFC3339Nano)) != nil {
				s.warn("peer inbox resolution failed")
				settled = false
			}
			continue
		}
		covered, err := history.contains(ctx, nudge.TargetCommit)
		if err != nil {
			_ = s.store.RecordPeerNudgeFailure(ctx, row.GroupID, row.PublicationID, row.Fingerprint, "coverage_unavailable")
			s.warn("peer inbox coverage unavailable")
			settled = false
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
			settled = false
		}
	}
	return settled
}

// Reuse the same bounded local history across every row in one inbox pass.
// A peer-supplied target is compared as data and never passed to Git.
type localHistoryCoverage struct {
	client    *gitlocal.Client
	current   string
	remaining int
	covered   map[string]bool
	advance   bool
	done      bool
	err       error
}

func (h *localHistoryCoverage) contains(ctx context.Context, target string) (bool, error) {
	if h.covered[target] {
		return true, nil
	}
	for !h.done && h.remaining > 0 {
		if h.advance {
			h.advance = false
		} else {
			current := h.current
			h.covered[current] = true
			h.remaining--
			if current == target {
				h.advance = true
				return true, nil
			}
		}
		current := h.current
		parents, err := h.client.CommitParents(ctx, current)
		if err != nil {
			h.err = err
			h.done = true
			break
		}
		if len(parents) == 0 {
			h.done = true
			break
		}
		if len(parents) != 1 {
			h.err = fmt.Errorf("local sync history is not first-parent linear")
			h.done = true
			break
		}
		h.current = parents[0]
	}
	return false, h.err
}

func parseReconcileResult(raw []byte) (string, string) {
	var envelope struct {
		Result struct {
			State        string `json:"state"`
			TargetCommit string `json:"target_commit"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return "", ""
	}
	return envelope.Result.State, envelope.Result.TargetCommit
}

func settledReconcileResult(state, target string) (bool, string) {
	switch state {
	case "no_change", "applied", "recovered":
		return target != "", target
	default:
		return false, ""
	}
}

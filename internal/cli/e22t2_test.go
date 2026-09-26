package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
)

func TestE22T2PublicationRecoveryResultMapping(t *testing.T) {
	for _, tc := range []struct {
		name, state, reason, control string
		input, exit                  int
		claimPending, unrelatedClaim bool
	}{
		{name: "transient", state: "deferred", reason: "publication_recovery_pending", control: "active", input: 10, exit: 10},
		{name: "unknown", state: "blocked", reason: "recovery_required", control: "blocked", input: 13, exit: 13},
		{name: "trust", state: "blocked", reason: "trust_failure", control: "blocked", input: 30, exit: 30},
		{name: "claimed", state: "deferred", reason: "publication_recovery_pending", control: "active", input: 14, exit: 10, claimPending: true},
		{name: "precondition", state: "failed", reason: "publication_recovery_precondition", control: "active", input: 14, exit: 14, unrelatedClaim: true},
		{name: "storage", state: "failed", reason: "publication_recovery_error", control: "active", input: 20, exit: 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, closeStore := e22t1Service(t)
			defer closeStore()
			revision, _ := config.SyncRevision(svc.cfg)
			if tc.unrelatedClaim {
				_, err := svc.store.ExecContext(context.Background(), `INSERT INTO sync_jobs(job_id,group_id,kind,logical_key,request_fingerprint,state,payload_json,claim_owner,claim_expires_at,created_at,updated_at) VALUES ('other-publisher',?,'publication','other','sha256:other','prepared','{}','publisher',?, ?, ?)`, svc.cfg.Sync.GroupID, time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
				if err != nil {
					t.Fatal(err)
				}
			}
			inner := []byte(`{"error":{"code":"sqlite_write_failed","category":"storage","message":"test write failure"}}`)
			var out, stderr bytes.Buffer
			code := reconcilePublicationRecoveryResult(&out, &stderr, svc.store, svc.cfg.Sync.GroupID, revision, tc.input, tc.claimPending, inner)
			if code != tc.exit || !bytes.Contains(out.Bytes(), []byte(`"state":"`+tc.state+`"`)) || !bytes.Contains(out.Bytes(), []byte(`"reason":"`+tc.reason+`"`)) {
				t.Fatalf("mapping %d: code=%d out=%s err=%s", tc.input, code, out.String(), stderr.String())
			}
			control, err := svc.store.LoadSyncControl(context.Background(), svc.cfg.Sync.GroupID)
			if err != nil || control.State != tc.control {
				t.Fatalf("mapping %d control=%+v err=%v", tc.input, control, err)
			}
			if tc.input == 20 && (!bytes.Contains(stderr.Bytes(), []byte(`"command":"sync reconcile"`)) || bytes.Contains(stderr.Bytes(), []byte(`"command":"sync publish"`))) {
				t.Fatalf("storage diagnostic command: %s", stderr.String())
			}
		})
	}
}

func TestE22T2MembershipAdoptionImmediatelyChecksContent(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	marker := filepath.Join(t.TempDir(), "adopted")
	t.Setenv("AGENT_DISPATCH_E22_RECONCILE_HELPER", "adopt-once")
	t.Setenv("AGENT_DISPATCH_E22_RECONCILE_RECORD", marker)
	settled, target := svc.runReconcile(context.Background())
	if !settled || target != strings.Repeat("a", 40) {
		t.Fatalf("normal membership adoption did not continue to content: settled=%v target=%q", settled, target)
	}
}

func TestE22T2HealthyNudgeWakesAfterStartupPass(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	svc.wake = make(chan struct{}, 1)
	called := make(chan struct{}, 2)
	svc.reconcile = func(context.Context) (bool, string) {
		called <- struct{}{}
		return true, "approved-head"
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { svc.inboxLoop(ctx); close(done) }()
	select {
	case <-called: // startup pass
	case <-time.After(2 * time.Second):
		t.Fatal("startup reconcile did not run")
	}
	svc.wake <- struct{}{}
	select {
	case <-called: // a second pass before the periodic timer is due
	case <-time.After(2 * time.Second):
		t.Fatal("admitted wake did not start a second reconcile")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("recovery worker did not stop")
	}
}

func TestE22T2ConfiguredRefRecoveryRunsWithoutNudge(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	var initialOut, initialErr bytes.Buffer
	if code := Run([]string{"sync", "status", "--group", svc.cfg.Sync.GroupID, "--output", "json"}, &initialOut, &initialErr); code != 0 {
		t.Fatalf("initial sync status: %d %s", code, initialErr.String())
	}
	var initial struct {
		Result struct {
			RecoverySchedule    any `json:"recovery_schedule"`
			PreSignaturePending int `json:"pre_signature_publications_pending"`
		} `json:"result"`
	}
	if err := json.Unmarshal(initialOut.Bytes(), &initial); err != nil || initial.Result.RecoverySchedule != nil || initial.Result.PreSignaturePending != 0 {
		t.Fatalf("initial recovery status: %+v err=%v", initial.Result, err)
	}
	called := 0
	svc.reconcile = func(context.Context) (bool, string) {
		called++
		return true, "approved-head"
	}
	svc.recoverScheduled(context.Background(), true)
	if called != 1 {
		t.Fatalf("startup did not inspect Git without an inbox hint: %d calls", called)
	}
	state, found, err := svc.store.LoadSyncRecoverySchedule(context.Background(), svc.cfg.Sync.GroupID)
	if err != nil || !found || state.LastSuccessAt == "" || state.ConsecutiveFailures != 0 {
		t.Fatalf("successful configured-ref inspection was not persisted: %+v found=%v err=%v", state, found, err)
	}
	svc.recoverScheduled(context.Background(), false)
	if called != 1 {
		t.Fatal("ordinary tick bypassed the periodic due time")
	}
	if _, err := svc.store.ExecContext(context.Background(), `UPDATE sync_recovery_schedule SET next_due_at=? WHERE group_id=?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), svc.cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	svc.recoverScheduled(context.Background(), false)
	if called != 2 {
		t.Fatal("due periodic inspection did not run")
	}
	if _, err := svc.store.ExecContext(context.Background(), `INSERT INTO sync_jobs(job_id,group_id,kind,logical_key,request_fingerprint,state,payload_json,created_at,updated_at) VALUES ('stranded',?,'publication','older-publication','sha256:older','prepared','{}','2026-09-01T00:00:00Z','2026-09-01T00:00:00Z')`, svc.cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.ExecContext(context.Background(), `INSERT INTO sync_jobs(job_id,group_id,kind,logical_key,request_fingerprint,state,payload_json,created_at,updated_at) VALUES ('eligible',?,'publication','eligible-publication','sha256:eligible','eligible','{}','2026-08-31T00:00:00Z','2026-08-31T00:00:00Z'),('signed',?,'publication','signed-publication','sha256:signed','signed','{}','2026-08-01T00:00:00Z','2026-08-01T00:00:00Z')`, svc.cfg.Sync.GroupID, svc.cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.ExecContext(context.Background(), `INSERT INTO sync_jobs(job_id,group_id,kind,logical_key,request_fingerprint,state,payload_json,claim_owner,claim_expires_at,created_at,updated_at) VALUES ('active',?,'publication','active-publication','sha256:active','prepared','{}','publisher',?,'2026-08-01T00:00:00Z','2026-08-01T00:00:00Z')`, svc.cfg.Sync.GroupID, time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.ExecContext(context.Background(), `INSERT INTO sync_jobs(job_id,group_id,kind,logical_key,request_fingerprint,state,payload_json,claim_owner,claim_expires_at,created_at,updated_at) VALUES ('expired',?,'publication','expired-publication','sha256:expired','prepared','{}','former-publisher',?,'2026-08-30T00:00:00Z','2026-08-30T00:00:00Z')`, svc.cfg.Sync.GroupID, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	var warning bytes.Buffer
	svc.stderr = &warning
	e22t1RunScheduledInbox(t, svc)
	if !strings.Contains(warning.String(), "pre-signature publication awaits explicit sync publish re-entry") {
		t.Fatalf("missing stranded-publication warning: %s", warning.String())
	}
	var out, stderr bytes.Buffer
	if code := Run([]string{"sync", "status", "--group", svc.cfg.Sync.GroupID, "--output", "json"}, &out, &stderr); code != 0 {
		t.Fatalf("sync status: %d %s", code, stderr.String())
	}
	var status struct {
		Result struct {
			PreSignaturePending int    `json:"pre_signature_publications_pending"`
			OldestPreSignature  string `json:"pre_signature_oldest_created_at"`
			RecoverySchedule    struct {
				ConsecutiveFailures int    `json:"consecutive_failures"`
				NextDueAt           string `json:"next_due_at"`
				LastAttemptAt       string `json:"last_attempt_at"`
				LastSuccessAt       string `json:"last_success_at"`
				LastReason          string `json:"last_reason"`
			} `json:"recovery_schedule"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	_, nextErr := time.Parse(time.RFC3339Nano, status.Result.RecoverySchedule.NextDueAt)
	_, attemptErr := time.Parse(time.RFC3339Nano, status.Result.RecoverySchedule.LastAttemptAt)
	if nextErr != nil || attemptErr != nil || status.Result.RecoverySchedule.LastSuccessAt == "" || status.Result.RecoverySchedule.LastReason != "none" || status.Result.RecoverySchedule.ConsecutiveFailures != 0 || status.Result.PreSignaturePending != 3 || status.Result.OldestPreSignature != "2026-08-30T00:00:00Z" {
		t.Fatalf("recovery status: %+v next_err=%v attempt_err=%v", status.Result, nextErr, attemptErr)
	}
}

func TestE22T2PausedControlDoesNotReserveRecovery(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	if _, err := svc.store.ExecContext(context.Background(), `UPDATE sync_controls SET state='paused',reason='operator_pause' WHERE group_id=?`, svc.cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	svc.reconcile = func(context.Context) (bool, string) {
		t.Fatal("paused service attempted reconciliation")
		return false, ""
	}
	svc.recoverScheduled(context.Background(), true)
	if _, found, err := svc.store.LoadSyncRecoverySchedule(context.Background(), svc.cfg.Sync.GroupID); err != nil || found {
		t.Fatalf("paused control reserved recovery: found=%v err=%v", found, err)
	}
}

func TestE22T2RecoveryStoreFailuresRemainVisible(t *testing.T) {
	t.Run("inbox read", func(t *testing.T) {
		svc, closeStore := e22t1Service(t)
		defer closeStore()
		var warnings bytes.Buffer
		svc.stderr = &warnings
		if _, err := svc.store.ExecContext(context.Background(), `DROP TABLE sync_peer_nudges`); err != nil {
			t.Fatal(err)
		}
		svc.reconcile = func(context.Context) (bool, string) {
			t.Fatal("inbox read failure must not report a successful pass")
			return false, ""
		}
		svc.recoverScheduled(context.Background(), true)
		schedule, found, err := svc.store.LoadSyncRecoverySchedule(context.Background(), svc.cfg.Sync.GroupID)
		if err != nil || !found || schedule.LastReason != sqlite.SyncRecoveryReasonInboxUnavailable || schedule.ConsecutiveFailures != 1 || !strings.Contains(warnings.String(), "peer inbox read failed") {
			t.Fatalf("inbox failure: schedule=%+v found=%v err=%v warnings=%q", schedule, found, err, warnings.String())
		}
	})
	t.Run("schedule completion", func(t *testing.T) {
		svc, closeStore := e22t1Service(t)
		defer closeStore()
		var warnings bytes.Buffer
		svc.stderr = &warnings
		svc.reconcile = func(context.Context) (bool, string) {
			if _, err := svc.store.ExecContext(context.Background(), `DROP TABLE sync_recovery_schedule`); err != nil {
				t.Fatal(err)
			}
			return true, "approved-head"
		}
		svc.recoverScheduled(context.Background(), true)
		if !strings.Contains(warnings.String(), "recovery schedule update failed") {
			t.Fatalf("schedule completion failure was silent: %q", warnings.String())
		}
	})
}

func TestE22T2OfflineRetrySurvivesWakeAndCatchesUpAfterReconnect(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	calls := 0
	svc.reconcile = func(context.Context) (bool, string) {
		calls++
		if calls == 1 {
			return false, ""
		}
		return true, "approved-head"
	}
	svc.recoverScheduled(context.Background(), true)
	svc.recoverScheduled(context.Background(), true)
	if calls != 1 {
		t.Fatalf("repeated wake retried offline Git before persisted due time: %d calls", calls)
	}
	// An offline interval longer than the ordinary timer does not erase the
	// obligation. Simulate the due time after reconnection without sleeping.
	if _, err := svc.store.ExecContext(context.Background(), `UPDATE sync_recovery_schedule SET next_due_at='2026-08-01T00:00:00Z' WHERE group_id=?`, svc.cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	svc.recoverScheduled(context.Background(), false)
	if calls != 2 {
		t.Fatalf("reconnected peer did not inspect configured Git refs: %d calls", calls)
	}
	state, found, err := svc.store.LoadSyncRecoverySchedule(context.Background(), svc.cfg.Sync.GroupID)
	if err != nil || !found || state.ConsecutiveFailures != 0 || state.LastSuccessAt == "" {
		t.Fatalf("offline obligation did not settle: %+v found=%v err=%v", state, found, err)
	}
}

func TestE22T2StaleControlDoesNotStartServiceRecovery(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	var warnings bytes.Buffer
	svc.stderr = &warnings
	if _, err := svc.store.ExecContext(context.Background(), `UPDATE sync_controls SET config_revision='older-configuration' WHERE group_id=?`, svc.cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	svc.reconcile = func(context.Context) (bool, string) {
		t.Fatal("service rebound stale control without operator reconciliation")
		return false, ""
	}
	svc.recoverScheduled(context.Background(), true)
	if _, found, err := svc.store.LoadSyncRecoverySchedule(context.Background(), svc.cfg.Sync.GroupID); err != nil || found {
		t.Fatalf("stale control created a recovery attempt: found=%v err=%v", found, err)
	}
	if !strings.Contains(warnings.String(), "configuration binding is stale; run sync reconcile") {
		t.Fatalf("stale control warning omitted operator remedy: %q", warnings.String())
	}
}

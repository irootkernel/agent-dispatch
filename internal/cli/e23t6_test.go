package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func TestE23T6LocalInstanceRebindRetainsOriginalHandles(t *testing.T) {
	f := newResumeFixture(t)
	f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now"}}
	f.writeConfig(t)
	accepted := e23t5Accepted(t, f.cfg, f.path, "sync-now")
	code, draft := e23t5Public(t, f.path, "prepare", "--group", f.cfg.Sync.GroupID, "--action", "sync-now")
	if code != 0 {
		t.Fatal(draft)
	}
	id := draft["result"].(map[string]any)["request_id"].(string)
	original := f.cfg.Instance.ID
	incarnation := localSyncIncarnation(f.cfg)
	// Keep a valid two-node config and the same incarnation while changing
	// only the local node identity, rather than testing config parse refusal.
	f.cfg.Instance.ID = "replacement-local-node"
	f.cfg.Sync.LocalInstanceID = f.cfg.Instance.ID
	for i := range f.cfg.Sync.Nodes {
		if f.cfg.Sync.Nodes[i].InstanceID == original {
			f.cfg.Sync.Nodes[i].InstanceID = f.cfg.Instance.ID
		}
	}
	f.writeConfig(t)
	if current, err := config.Load(f.path); err != nil || localSyncIncarnation(current) != incarnation {
		t.Fatalf("fixture did not isolate node identity: %v", err)
	}
	code, result := e23t5Public(t, f.path, "submit", "--group", f.cfg.Sync.GroupID, "--request-id", id)
	if code != 4 || result["error"].(map[string]any)["code"] != "sync_request_obsolete" {
		t.Fatalf("rebound draft gained acceptance: %d %v", code, result)
	}
	service := &peerService{cfg: f.cfg, configPath: f.path, store: f.store}
	service.dispatchManagement(context.Background())
	retained, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, accepted, time.Now())
	if err != nil || retained.State != "blocked" || retained.InstanceID != original || retained.StateIncarnationID != incarnation || retained.Attempts != 0 || len(retained.Jobs) != 0 {
		t.Fatalf("accepted rebound handle gained effects: %+v %v", retained, err)
	}
	for _, handle := range []string{id, accepted} {
		code, result = e23t5Public(t, f.path, "show", "--group", f.cfg.Sync.GroupID, "--request-id", handle)
		if code != 0 || result["result"].(map[string]any)["request_id"] != handle || result["result"].(map[string]any)["bindings"].(map[string]any)["instance_id"] != original {
			t.Fatalf("rebind lost exact historical identity: %d %v", code, result)
		}
	}
}

func TestE23T6ReconcileResultCeilingRejectsTruncatedSuccess(t *testing.T) {
	target := strings.Repeat("a", 40)
	var empty bytes.Buffer
	if code := reconcileEnvelope(&empty, "no_change", "none", map[string]any{"target_commit": target, "detail": ""}); code != 0 {
		t.Fatal("fixture producer failed")
	}
	for _, overflow := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("overflow-%d", overflow), func(t *testing.T) {
			size := syncmanagement.ResultBytes - empty.Len()
			size += overflow
			out := boundedReconcileOutput{limit: syncmanagement.ResultBytes}
			code := reconcileEnvelope(&out, "no_change", "none", map[string]any{"target_commit": target, "detail": strings.Repeat("x", size)})
			state, pinned := parseReconcileResult(out.buffer.Bytes())
			settled, _ := settledReconcileResult(state, pinned)
			if out.buffer.Len() != syncmanagement.ResultBytes {
				t.Fatalf("capture exceeded or missed its ceiling: %d", out.buffer.Len())
			}
			if overflow > 0 {
				// Dropping only the newline can leave parseable JSON. The
				// producer must still fail, so dispatch cannot finish it.
				if code == 0 || overflow > 1 && (settled || pinned != "") {
					t.Fatalf("truncated result settled a target: %d %s %s", code, state, pinned)
				}
			} else if code != 0 || !settled || pinned != target {
				t.Fatalf("exact-bound result rejected: %d %s %s", code, state, pinned)
			}
		})
	}
}

func TestE23T6PublicFrozenArgvRejectsAuthorityAndSecretInputs(t *testing.T) {
	cfg, path := e23t3Config(t)
	cfg.Sync.GroupID = "wiki"
	e23t3SaveConfig(t, cfg, path)
	t.Setenv("AGENT_DISPATCH_CONFIG", path)
	store, err := openStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	revision, _ := config.SyncRevision(cfg)
	if _, err := store.EnsureSyncControl(context.Background(), "wiki", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	const secret = "fixture-secret-must-not-be-emitted"
	t.Setenv("SYNC_PUBLISHER_SIGNING_KEY", secret)
	t.Setenv("SYNC_NODE_A_TO_B", secret)
	raw, err := os.ReadFile("../../docs/contracts/sync-management-v1/argv-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Fixtures []struct {
			Name  string
			Argv  []string
			Valid bool
		}
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	invoke := func(args []string) (int, map[string]any) {
		t.Helper()
		var out, stderr bytes.Buffer
		code := Run(args, &out, &stderr)
		if strings.Contains(out.String()+stderr.String(), secret) {
			t.Fatal("request carrier leaked supplied secret")
		}
		raw := out.Bytes()
		if code != 0 {
			if out.Len() != 0 {
				t.Fatal("refusal wrote success output")
			}
			raw = stderr.Bytes()
		} else if stderr.Len() != 0 {
			t.Fatal("success wrote error output")
		}
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		e23t3ValidateCarrier(t, value)
		return code, value
	}
	for _, fixture := range fixtures.Fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			args := append([]string(nil), fixture.Argv...)
			for i := range args {
				if args[i] == "/trusted/config.yaml" {
					args[i] = path
				}
				if args[i] == "forbidden" {
					args[i] = secret
				}
			}
			code, value := invoke(append([]string{"sync", "request"}, args...))
			if !fixture.Valid {
				if code != 2 || value["error"].(map[string]any)["code"] != "flag_invalid" {
					t.Fatalf("closed argv escaped: code=%d", code)
				}
			} else {
				wantCode := 0
				if args[0] == "show" || args[0] == "submit" || fixture.Name == "argv-prepare-retry" {
					wantCode = 4 // The frozen handles do not exist in this isolated ledger.
				}
				if code != wantCode {
					t.Fatalf("valid frozen argv outcome: code=%d want=%d carrier=%v", code, wantCode, value)
				}
			}
		})
	}
	for _, flag := range []string{"--actor", "--confirmation", "--credential", "--profile", "--executable", "--note-body", "--force"} {
		code, value := invoke([]string{"sync", "request", "prepare", "--group", "wiki", "--action", "sync-now", flag, secret, "--config", path})
		if code != 2 || value["error"].(map[string]any)["code"] != "flag_invalid" {
			t.Fatalf("caller authority flag accepted: %s code=%d", flag, code)
		}
	}
	for _, action := range []string{"publish", "membership", "checkpoint", "service", "remote-pause", "tailscale"} {
		code, value := invoke([]string{"sync", "request", "prepare", "--group", "wiki", "--action", action, "--config", path})
		if code != 2 || value["error"].(map[string]any)["code"] != "flag_invalid" {
			t.Fatalf("forbidden action accepted: %s code=%d", action, code)
		}
	}
	for _, flag := range []string{"--state-dir", "--timeout", "--log-level", "--trace-id"} {
		code, value := invoke([]string{"sync", flag, secret, "request", "capabilities", "--group", "wiki", "--config", path})
		if code != 2 || value["error"].(map[string]any)["code"] != "flag_invalid" {
			t.Fatalf("legacy global flag bypassed closure: %s code=%d", flag, code)
		}
	}
	for _, query := range []string{"SELECT COUNT(*) FROM sync_management_requests WHERE accepted_at IS NOT NULL", "SELECT COUNT(*) FROM sync_jobs"} {
		var count int
		if err := store.QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("inert/forbidden argv caused executable state: count=%d err=%v", count, err)
		}
	}
	control, err := store.LoadSyncControl(context.Background(), "wiki")
	if err != nil || control.State != "active" || control.Revision != 1 {
		t.Fatalf("preparation changed control: %+v %v", control, err)
	}
}

func TestE23T6ManagementDisabledPreservesOperatorControl(t *testing.T) {
	for _, absent := range []bool{true, false} {
		t.Run(fmt.Sprintf("absent-%t", absent), func(t *testing.T) {
			f := newResumeFixture(t)
			if !absent {
				f.cfg.Sync.Management = &config.SyncManagement{Enabled: false, Actions: []string{"pause", "resume", "sync-now"}}
			}
			f.writeConfig(t)
			code, value := e23t5Public(t, f.path, "prepare", "--group", f.cfg.Sync.GroupID, "--action", "pause")
			if code != 3 || value["error"].(map[string]any)["code"] != "sync_management_disabled" {
				t.Fatalf("disabled management gained authority: %d %v", code, value)
			}
			code, value, stderr := syncResult(t, "pause", "--group", f.cfg.Sync.GroupID, "--expected-control-revision", "1", "--config", f.path)
			if code != 0 || value["result"].(map[string]any)["state"] != "paused" {
				t.Fatalf("ordinary operator control regressed: %d %v %s", code, value, stderr)
			}
			var requests int
			if err := f.store.QueryRow(`SELECT COUNT(*) FROM sync_management_requests`).Scan(&requests); err != nil || requests != 0 {
				t.Fatalf("operator control manufactured a management request: %d %v", requests, err)
			}
		})
	}
}

// Remote operations answer only the two configured refs in this private fixture.
// Local signature verification and ref operations still use the real Git binary.
func e23t6RemoteContent(t *testing.T, f *resumeFixture, allowFetch bool) string {
	t.Helper()
	target := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef)
	path := filepath.Join(filepath.Dir(f.remoteHead), "bin", "git")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	condition := fmt.Sprintf("    test \"$last\" = '%s' || exit 2", f.cfg.Sync.MembershipRef)
	if !strings.Contains(string(raw), condition) {
		t.Fatal("remote fixture shape changed")
	}
	reply := fmt.Sprintf("    if test \"$last\" = '%s'; then printf '%%s\\t%%s\\n' '%s' \"$last\"; exit 0; fi\n", f.cfg.Sync.ContentRef, target)
	changed := strings.Replace(string(raw), condition, reply+condition, 1)
	if allowFetch {
		fetch := fmt.Sprintf(`  *" fetch "*)
    for last do :; done
    ref=${last#+}
    from=${ref%%:*}
    to=${ref#*:}
    case "$from" in '%s'|'%s') exec '%s' -C '%s' update-ref "$to" "$from";; *) exit 97;; esac;;
`, f.cfg.Sync.MembershipRef, f.cfg.Sync.ContentRef, f.git, f.repo)
		changed = strings.Replace(changed, `  *" push "*|*" fetch "*|*" clone "*) exit 97;;`, fetch+`  *" push "*|*" clone "*) exit 97;;`, 1)
	}
	if err := os.WriteFile(path, []byte(changed), 0700); err != nil {
		t.Fatal(err)
	}
	return target
}

func TestE23T6VerificationStallRetainsPlannedThenExpiredEvidence(t *testing.T) {
	for _, stalled := range []bool{false, true} {
		t.Run(fmt.Sprintf("stalled-%t", stalled), func(t *testing.T) {
			f := newResumeFixture(t)
			e23t6RemoteContent(t, f, false)
			t.Setenv("SYNC_NODE_A_TO_B", "fixture-outbound-secret")
			ctx := context.Background()
			if stalled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
			}
			called := false
			client := &http.Client{Transport: e22t3RoundTrip(func(r *http.Request) (*http.Response, error) {
				called = true
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > verificationCommandDeadline {
					t.Fatal("production verification deadline did not reach transport")
				}
				if stalled {
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				return nil, context.DeadlineExceeded
			})}
			var out, stderr bytes.Buffer
			started := time.Now()
			code := verifyConfiguredSync(ctx, f.cfg, f.store, client, &out, &stderr)
			if !called || time.Since(started) > 10*time.Second {
				t.Fatal("injected transport deadline did not bound real verification")
			}
			if !stalled {
				var envelope struct{ Result syncrecords.Verification }
				if code != 0 || json.Unmarshal(out.Bytes(), &envelope) != nil || envelope.Result.Result != "incomplete" || envelope.Result.Validate() != nil {
					t.Fatalf("transport failure claimed pair completion: code=%d", code)
				}
				return
			}
			var errorEnvelope struct{ Error struct{ Code string } }
			if code != 20 || out.Len() != 0 || json.Unmarshal(stderr.Bytes(), &errorEnvelope) != nil || errorEnvelope.Error.Code != "sqlite_write_failed" || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Fatalf("cancelled verification returned false success: code=%d", code)
			}
			code, status, errText := syncResult(t, "status", "--group", f.cfg.Sync.GroupID, "--config", f.path)
			if code != 0 || status["result"].(map[string]any)["latest_verification"].(map[string]any)["state"] != "planned" {
				t.Fatalf("interrupted verification disappeared: %d %v %s", code, status, errText)
			}
			if err := f.store.ExpireInterruptedSyncVerifications(context.Background(), f.cfg.Sync.GroupID, time.Now().Add(6*time.Minute)); err != nil {
				t.Fatal(err)
			}
			code, status, errText = syncResult(t, "status", "--group", f.cfg.Sync.GroupID, "--config", f.path)
			if code != 0 || status["result"].(map[string]any)["latest_verification"].(map[string]any)["state"] != "expired" {
				t.Fatalf("expired verification invented a result: %d %v %s", code, status, errText)
			}
			var jobs int
			if err := f.store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='verification'`).Scan(&jobs); err != nil || jobs != 1 {
				t.Fatalf("deadline/expiry duplicated observation: %d %v", jobs, err)
			}
		})
	}
}

func TestE23T6PublicVerificationSaturationAndStaleMembershipRefuseBeforeObservation(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprintf("stale-%t", stale), func(t *testing.T) {
			f := newResumeFixture(t)
			f.cfg.Sync.Bounds.Queue = 2
			f.writeConfig(t)
			e23t6RemoteContent(t, f, false)
			if stale {
				if err := os.WriteFile(f.remoteHead, []byte(strings.Repeat("a", 40)), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				for i := range 2 {
					id := fmt.Sprintf("existing-%d", i)
					if _, _, err := f.store.AdmitSyncJob(context.Background(), sqlite.SyncJobInput{JobID: id, GroupID: f.cfg.Sync.GroupID, Kind: "verification", LogicalKey: id, InitialState: "planned", PayloadJSON: `{}`, QueueLimit: 2, Now: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
						t.Fatal(err)
					}
				}
			}
			before, err := f.store.LoadUnresolvedSyncJobs(context.Background(), f.cfg.Sync.GroupID, "verification")
			if err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			client := &http.Client{Transport: e22t3RoundTrip(func(*http.Request) (*http.Response, error) {
				t.Fatal("refused verification contacted a peer")
				return nil, errors.New("unexpected observation")
			})}
			code := runSyncVerifyWithHTTP([]string{"--group", f.cfg.Sync.GroupID}, &out, &stderr, client)
			wantCode, wantError := 14, "sync_precondition_failed"
			if stale {
				wantCode, wantError = 30, "sync_trust_failed"
			}
			var value struct{ Error struct{ Code string } }
			if code != wantCode || out.Len() != 0 || json.Unmarshal(stderr.Bytes(), &value) != nil || value.Error.Code != wantError {
				t.Fatalf("incorrect bounded operator refusal: code=%d", code)
			}
			after, err := f.store.LoadUnresolvedSyncJobs(context.Background(), f.cfg.Sync.GroupID, "verification")
			if err != nil || !bytes.Equal([]byte(mustJSON(before)), []byte(mustJSON(after))) {
				t.Fatalf("refusal changed retained obligations: %v", err)
			}
			code, status, errText := syncResult(t, "status", "--group", f.cfg.Sync.GroupID, "--config", f.path)
			if code != 0 {
				t.Fatalf("operator lookup lost queue pressure: %d %v %s", code, status, errText)
			}
			queue := status["result"].(map[string]any)["health"].(map[string]any)["queue"].(map[string]any)
			if !stale && (queue["state"] != "pending" || queue["count"] != float64(2)) {
				t.Fatalf("operator lookup lost retained queue pressure: %v", queue)
			}
		})
	}
}

func TestE23T6ExpiredDeliveryClaimRetainsExactReplayIdentity(t *testing.T) {
	svc, closeStore := e22t1Service(t)
	defer closeStore()
	t.Setenv("SYNC_NODE_A_TO_B", "fixture-outbound-credential")
	nudge := e22t1Nudge()
	nudge.Sender, nudge.Receiver = nudge.Receiver, nudge.Sender
	raw, err := syncrecords.CanonicalNudge(nudge)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	if _, _, err := svc.store.AdmitSyncJob(ctx, sqlite.SyncJobInput{JobID: "expired-delivery", GroupID: svc.cfg.Sync.GroupID, Kind: "delivery", LogicalKey: nudge.PublicationID, InitialState: "pending", PayloadJSON: string(raw), QueueLimit: 2, Now: now.Add(-2 * time.Minute).Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	revision, _ := config.SyncRevision(svc.cfg)
	if _, err := svc.store.ClaimSyncJob(ctx, "expired-delivery", "lost-owner", revision, now.Add(-time.Minute).Format(time.RFC3339Nano), now.Add(-30*time.Second).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	original, err := svc.store.LoadLatestSyncJob(ctx, svc.cfg.Sync.GroupID, "delivery")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &http.Client{Transport: e22t1RoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		wire, err := io.ReadAll(r.Body)
		if err != nil || string(wire) != original.PayloadJSON {
			t.Fatalf("replay changed wire identity: %v", err)
		}
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}
	for _, state := range []string{"unknown", "retryable"} {
		if err := svc.deliverPending(ctx, client); err != nil {
			t.Fatal(err)
		}
		job, err := svc.store.LoadLatestSyncJob(ctx, svc.cfg.Sync.GroupID, "delivery")
		if err != nil || calls != 0 || job.State != state || job.JobID != "expired-delivery" || job.PayloadJSON != original.PayloadJSON || job.LogicalKey != nudge.PublicationID || job.Attempts != 1 || job.Fence != 1 || job.ResolvedAt != "" || job.ClaimOwner != "" {
			t.Fatalf("lost claim invented an effect or identity: %+v calls=%d err=%v", job, calls, err)
		}
	}
	var journals int
	if err := svc.store.QueryRow(`SELECT COUNT(*) FROM sync_journal_entries WHERE job_id=? AND phase='claim_recovery' AND outcome='effect_unknown'`, "expired-delivery").Scan(&journals); err != nil || journals != 2 {
		t.Fatalf("missing uncertainty provenance: %d %v", journals, err)
	}
	// Advance only this private ledger's retry clock; no production backoff changes.
	if _, err := svc.store.Exec(`UPDATE sync_jobs SET updated_at=? WHERE job_id=?`, now.Add(-time.Minute).Format(time.RFC3339Nano), "expired-delivery"); err != nil {
		t.Fatal(err)
	}
	if err := svc.deliverPending(ctx, client); err != nil {
		t.Fatal(err)
	}
	job, err := svc.store.LoadLatestSyncJob(ctx, svc.cfg.Sync.GroupID, "delivery")
	if err != nil || calls != 1 || job.State != "accepted" || job.ResolvedAt == "" || job.Attempts != 2 || job.Fence != 2 || job.JobID != "expired-delivery" || job.PayloadJSON != original.PayloadJSON || job.LogicalKey != nudge.PublicationID {
		t.Fatalf("same-identity replay did not settle: %+v calls=%d err=%v", job, calls, err)
	}
}

func TestE23T6PublicServeRefusesSignerMembershipAndSocketHazards(t *testing.T) {
	for _, hazard := range []string{"open-signer-fd", "missing-membership", "occupied-socket"} {
		t.Run(hazard, func(t *testing.T) {
			f := newResumeFixture(t)
			wantCode, wantError := 30, "sync_trust_failed"
			var occupied string
			switch hazard {
			case "open-signer-fd":
				signer, err := os.CreateTemp(t.TempDir(), "signer")
				if err != nil {
					t.Fatal(err)
				}
				defer signer.Close()
				if _, err := signer.WriteString("fixture-private-signing-material"); err != nil {
					t.Fatal(err)
				}
				f.cfg.Sync.PublisherSigningKeyRef = fmt.Sprintf("fd:%d", signer.Fd())
				f.writeConfig(t)
			case "missing-membership":
				gitTestRun(t, f.git, f.repo, "update-ref", "-d", f.cfg.Sync.MembershipRef)
			case "occupied-socket":
				dir := filepath.Join(f.cfg.Instance.StateDir, "peer-service")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				occupied = filepath.Join(dir, syncPeerSocketName)
				if err := os.WriteFile(occupied, []byte("fixture-occupied"), 0600); err != nil {
					t.Fatal(err)
				}
				wantCode, wantError = 14, "sync_precondition_failed"
			}
			var out, stderr bytes.Buffer
			code := Run([]string{"sync", "serve", "--group", f.cfg.Sync.GroupID, "--config", f.path}, &out, &stderr)
			var value struct{ Error struct{ Code string } }
			if code != wantCode || out.Len() != 0 || json.Unmarshal(stderr.Bytes(), &value) != nil || value.Error.Code != wantError || strings.Contains(stderr.String(), "fixture-private-signing-material") {
				t.Fatalf("startup hazard escaped closed refusal: code=%d error=%s", code, stderr.String())
			}
			if occupied != "" {
				raw, err := os.ReadFile(occupied)
				if err != nil || string(raw) != "fixture-occupied" {
					t.Fatal("startup replaced occupied socket path")
				}
			}
			var jobs int
			if err := f.store.QueryRow(`SELECT COUNT(*) FROM sync_jobs`).Scan(&jobs); err != nil || jobs != 0 {
				t.Fatalf("refused startup executed work: %d %v", jobs, err)
			}
		})
	}
}

func TestE23T6ActualReconcileCarrierDoesNotSettleSafetyHold(t *testing.T) {
	f := newResumeFixture(t)
	target := e23t6RemoteContent(t, f, true)
	invoke := func() (int, string, string) {
		t.Helper()
		var out, stderr bytes.Buffer
		code := Run([]string{"sync", "reconcile", "--group", f.cfg.Sync.GroupID}, &out, &stderr)
		if stderr.Len() != 0 {
			t.Fatalf("reconcile error: %d %s", code, stderr.String())
		}
		state, parsedTarget := parseReconcileResult(out.Bytes())
		if code != 0 && code != 10 {
			t.Fatalf("unexpected reconcile carrier: %d %s", code, out.String())
		}
		return code, state, parsedTarget
	}
	code, state, parsedTarget := invoke()
	settled, settledTarget := settledReconcileResult(state, parsedTarget)
	if code != 0 || state != "no_change" || !settled || settledTarget != target {
		t.Fatalf("actual producer/consumer lost reconciled target: %d %s %s", code, state, parsedTarget)
	}
	code, _, stderr := syncResult(t, "pause", "--group", f.cfg.Sync.GroupID, "--expected-control-revision", "1", "--config", f.path)
	if code != 0 {
		t.Fatalf("pause: %d %s", code, stderr)
	}
	code, state, parsedTarget = invoke()
	settled, _ = settledReconcileResult(state, parsedTarget)
	if code != 0 || state != "deferred" || parsedTarget != target || settled {
		t.Fatalf("actual safety hold falsely settled: %d %s %s", code, state, parsedTarget)
	}
}

func TestE23T6InboxObsoleteBindingAndUnavailableLocalGitPreserveEvidence(t *testing.T) {
	for _, obsolete := range []bool{true, false} {
		t.Run(fmt.Sprintf("obsolete-%t", obsolete), func(t *testing.T) {
			f := newResumeFixture(t)
			target := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef)
			nudge := syncrecords.Nudge{SchemaVersion: syncrecords.NudgeSchema, GroupID: f.cfg.Sync.GroupID, PublicationID: "retained-inbox", Sender: f.cfg.Sync.Nodes[1].InstanceID, Receiver: f.cfg.Sync.LocalInstanceID, MembershipRevision: gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.MembershipRef), ContentRef: f.cfg.Sync.ContentRef, TargetCommit: target}
			if obsolete {
				nudge.Receiver = "former-local-node"
			}
			raw, err := syncrecords.CanonicalNudge(nudge)
			if err != nil {
				t.Fatal(err)
			}
			fingerprint := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
			ctx := context.Background()
			if _, err := f.store.AdmitPeerNudge(ctx, sqlite.PeerNudgeInput{GroupID: nudge.GroupID, PublicationID: nudge.PublicationID, Fingerprint: fingerprint, PayloadJSON: string(raw), ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano), QueueLimit: 2}); err != nil {
				t.Fatal(err)
			}
			rows, err := f.store.LoadPendingPeerNudges(ctx, nudge.GroupID, 2)
			if err != nil || len(rows) != 1 {
				t.Fatalf("inbox admission: %v %v", rows, err)
			}
			svc := &peerService{cfg: f.cfg, configPath: f.path, store: f.store}
			if obsolete {
				client, err := membershipGitClient(f.cfg, f.cfg.Sync)
				if err != nil {
					t.Fatal(err)
				}
				if !svc.settleInboxRows(ctx, client, target, rows) {
					t.Fatal("obsolete binding did not retain a resolution")
				}
				var resolution, payload string
				if err := f.store.QueryRow(`SELECT resolution,payload_json FROM sync_peer_nudges WHERE publication_id=? AND processed_at IS NOT NULL`, nudge.PublicationID).Scan(&resolution, &payload); err != nil || resolution != "obsolete_binding" || payload != string(raw) {
					t.Fatalf("obsolete binding claimed an import or lost payload: %s %v", resolution, err)
				}
			} else {
				// The reconcile seam reports its measured target; the real local Git
				// boundary must still reject an unavailable configured resource.
				svc.reconcile = func(context.Context) (bool, string) { return true, target }
				resource := f.cfg.Resources[f.cfg.Sync.Resource]
				resource.Root = filepath.Join(t.TempDir(), "missing-resource")
				f.cfg.Resources[f.cfg.Sync.Resource] = resource
				settled, reason := svc.processInboxRows(ctx, rows)
				if settled || reason != sqlite.SyncRecoveryReasonLocalRefUnavailable {
					t.Fatalf("local Git failure claimed settlement: %t %s", settled, reason)
				}
				retained, err := f.store.LoadPendingPeerNudges(ctx, nudge.GroupID, 2)
				if err != nil || !bytes.Equal([]byte(mustJSON(rows)), []byte(mustJSON(retained))) {
					t.Fatalf("local Git failure lost obligation: %v", err)
				}
			}
			var imports int
			if err := f.store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='import'`).Scan(&imports); err != nil || imports != 0 {
				t.Fatalf("inbox bookkeeping invented import: %d %v", imports, err)
			}
		})
	}
}

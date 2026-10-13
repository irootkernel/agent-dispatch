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
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/resourceguard"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func e23t5Public(t *testing.T, path string, args ...string) (int, map[string]any) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(append(append([]string{"sync", "request"}, args...), "--config", path), &out, &errOut)
	raw := out.Bytes()
	if code != 0 {
		raw = errOut.Bytes()
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("public request %d:%s %v", code, raw, err)
	}
	e23t3ValidateCarrier(t, v)
	return code, v
}

func e23t5Accepted(t *testing.T, cfg *config.Config, path, action string) string {
	t.Helper()
	code, v := e23t5Public(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", action)
	if code != 0 {
		t.Fatal(v)
	}
	id := v["result"].(map[string]any)["request_id"].(string)
	if code, v = e23t5Public(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", id); code != 0 {
		t.Fatal(v)
	}
	return id
}

func TestE23T5ExpiredEmptyClaimRecoversWithoutTreatingTimeAsEffectProof(t *testing.T) {
	for _, mode := range []string{"authorized", "revoked", "rebound"} {
		t.Run(mode, func(t *testing.T) {
			cfg, path := e23t3Config(t)
			id := e23t5Accepted(t, cfg, path, "sync-now")
			store, err := sqlite.Open(filepath.Join(cfg.Instance.StateDir, StateDBName))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			revision, _ := config.SyncRevision(cfg)
			if _, err := store.EnsureSyncControl(context.Background(), cfg.Sync.GroupID, revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			b, _ := managementBindingForConfig(cfg)
			claimed, err := store.ClaimManagementRequest(context.Background(), cfg.Sync.GroupID, id, "lost-worker", b, true, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			service := &peerService{cfg: cfg, configPath: path, store: store}
			service.recoverManagement(context.Background())
			live, _ := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, id, time.Now())
			if live.State != "running" || live.Revision != claimed.Revision {
				t.Fatal("recovery stole a live claim")
			}
			if mode == "revoked" {
				cfg.Sync.Management.Enabled = false
				e23t3SaveConfig(t, cfg, path)
			}
			if mode == "rebound" {
				cfg.Sync.Nodes[0].StateIncarnationID = "replacement-incarnation"
				e23t3SaveConfig(t, cfg, path)
			}
			if _, err := store.Exec(`UPDATE sync_management_requests SET claim_expires_at=? WHERE request_id=?`, time.Now().Add(-time.Minute).UTC().Format("2006-01-02T15:04:05.000000000Z"), id); err != nil {
				t.Fatal(err)
			}
			service.recoverManagement(context.Background())
			r, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, id, time.Now())
			want := "queued"
			if mode != "authorized" {
				want = "blocked"
			}
			if err != nil || r.State != want || r.Attempts != 1 || r.Fence != 1 || r.AcceptedAt == nil || r.ClaimOwner != "" || len(r.Jobs) != 0 {
				t.Fatalf("recovered claim:%+v %v", r, err)
			}
			if err := (&managementExecution{request: claimed, service: service}).authorized(context.Background()); err == nil {
				t.Fatal("lost worker retained effect authority")
			}
			if code, v := e23t5Public(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", id); code != 0 || v["result"].(map[string]any)["request_id"] != id {
				t.Fatalf("accepted replay:%d %v", code, v)
			}
		})
	}
}

func TestE23T5EmptyHoldRecovery(t *testing.T) {
	for _, action := range []string{"sync-now", "verify-all"} {
		for _, mode := range []string{"resumed", "revoked", "rebound", "safety", "exhausted"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				f := newResumeFixture(t)
				f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now", "verify-all", "pause", "resume"}}
				f.writeConfig(t)
				id := e23t5Accepted(t, f.cfg, f.path, action)
				e23t5Accepted(t, f.cfg, f.path, "pause")
				service := &peerService{cfg: f.cfg, configPath: f.path, store: f.store}
				service.dispatchManagement(context.Background())
				blocked, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
				if err != nil || blocked.State != "blocked" || blocked.AcceptedAt == nil || blocked.Attempts != 0 || blocked.Fence != 0 || len(blocked.Jobs) != 0 {
					t.Fatalf("paused dispatch:%+v %v", blocked, err)
				}
				service.recoverManagement(context.Background())
				held, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
				if err != nil || held.State != "blocked" || held.Revision != blocked.Revision || held.Attempts != 0 {
					t.Fatalf("recovery cleared pause:%+v %v", held, err)
				}
				e23t5Accepted(t, f.cfg, f.path, "resume")
				switch mode {
				case "revoked":
					f.cfg.Sync.Management.Enabled = false
					f.writeConfig(t)
				case "rebound":
					f.cfg.Sync.Bounds.Queue--
					f.writeConfig(t)
				case "safety":
					if _, err := f.store.Exec(`UPDATE sync_controls SET membership_mode='blocked_emergency' WHERE group_id=?`, f.cfg.Sync.GroupID); err != nil {
						t.Fatal(err)
					}
				case "exhausted":
					if _, err := f.store.Exec(`UPDATE sync_management_requests SET attempts=? WHERE request_id=?`, syncmanagement.AttemptLimit, id); err != nil {
						t.Fatal(err)
					}
				}
				service.recoverManagement(context.Background())
				got, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
				want, attempts := "blocked", 0
				if mode == "resumed" {
					want = "queued"
				}
				if mode == "exhausted" {
					attempts = syncmanagement.AttemptLimit
				}
				if err != nil || got.State != want || got.RequestID != id || got.Fingerprint != blocked.Fingerprint || got.AcceptedAt == nil || *got.AcceptedAt != *blocked.AcceptedAt || got.Attempts != attempts || got.Fence != 0 || got.ClaimOwner != "" || len(got.Jobs) != 0 {
					t.Fatalf("hold recovery:%+v %v", got, err)
				}
				if mode != "resumed" {
					return
				}
				if got.Outcome != nil || got.Revision != blocked.Revision+1 {
					t.Fatalf("requeue retained old hold:%+v", got)
				}
				if _, err := f.store.TransitionManagementRequest(context.Background(), blocked, "queued", nil, "stale-hold-inspection", time.Now()); !errors.Is(err, sqlite.ErrSyncPrecondition) {
					t.Fatalf("stale hold revision accepted:%v", err)
				}
				if code, v := e23t5Public(t, f.path, "submit", "--group", f.cfg.Sync.GroupID, "--request-id", id); code != 0 || v["result"].(map[string]any)["request_id"] != id || v["result"].(map[string]any)["state"] != "queued" {
					t.Fatalf("accepted duplicate changed recovered identity:%d %v", code, v)
				}
				binding, err := managementBindingForConfig(f.cfg)
				if err != nil {
					t.Fatal(err)
				}
				claimed, err := f.store.ClaimManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, "resumed-worker", binding, true, time.Now())
				if err != nil || claimed.State != "running" || claimed.Attempts != 1 || claimed.Fence != 1 || claimed.RequestID != id {
					t.Fatalf("same request did not regain bounded execution:%+v %v", claimed, err)
				}
			})
		}
	}
}

func TestE23T5DeferredImportHoldYieldsToSettledRecovery(t *testing.T) {
	f := newResumeFixture(t)
	f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now", "retry"}}
	f.writeConfig(t)
	ids := []string{e23t5Accepted(t, f.cfg, f.path, "sync-now"), e23t5Accepted(t, f.cfg, f.path, "sync-now")}
	slices.Sort(ids)
	ctx := context.Background()
	binding, err := managementBindingForConfig(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	revision, _ := config.SyncRevision(f.cfg)
	jobs := make([]string, 2)
	for i, id := range ids {
		r, err := f.store.ClaimManagementRequest(ctx, f.cfg.Sync.GroupID, id, fmt.Sprintf("worker-%d", i), binding, true, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		target := strings.Repeat(string(rune('b'+i)), 40)
		p, err := syncrecords.NewImport(syncrecords.ImportBinding{GroupID: f.cfg.Sync.GroupID, FromCommit: strings.Repeat("a", 40), TargetCommit: target, MembershipRevision: strings.Repeat("d", 40), AcknowledgementID: "fixture-ack", ResourceObservationRevision: 1, ExpectedGitStateDigest: "sha256:" + strings.Repeat("e", 64), HistoryEvidenceID: "fixture-history", CaseMode: config.CaseMode(), State: "validated", Reason: "none"}, []syncrecords.ImportPath{{Path: "note.md", Before: "absent", After: "sha256:" + strings.Repeat("f", 64)}})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := syncrecords.CanonicalImport(p)
		if err != nil {
			t.Fatal(err)
		}
		job, _, err := f.store.AdmitSyncJob(ctx, sqlite.SyncJobInput{JobID: p.ImportID, GroupID: p.GroupID, Kind: "import", LogicalKey: p.PlanID, InitialState: "validated", PayloadJSON: string(raw), ConfigRevision: revision, QueueLimit: 1000, Now: time.Now().UTC().Format(time.RFC3339Nano)})
		if err != nil {
			t.Fatal(err)
		}
		jobs[i] = job.JobID
		r, err = f.store.ReserveManagementAssociations(ctx, r, 1, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		r, err = f.store.AssociateManagementJob(ctx, r, sqlite.ManagementJob{JobID: job.JobID, Kind: "import", TargetCommit: &target, AssociationReason: "observed_reconciliation"}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err := f.store.Exec(`UPDATE sync_jobs SET state='deferred' WHERE job_id=?`, job.JobID); err != nil {
				t.Fatal(err)
			}
			if err := f.store.ResolveImportDeferral(ctx, job.JobID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		} else if _, err := f.store.Exec(`UPDATE sync_jobs SET state='applied',resolved_at=?,retain_until_resolved=0 WHERE job_id=?`, time.Now().UTC().Format(time.RFC3339Nano), job.JobID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.TransitionManagementRequest(ctx, r, "blocked", &syncmanagement.Outcome{Status: "blocked", Reason: "recorded import awaits recovery inspection"}, "fixture-import-inspection", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	service := &peerService{cfg: f.cfg, configPath: f.path, store: f.store}
	service.recoverManagement(ctx)
	held, err := f.store.LoadManagementRequest(ctx, f.cfg.Sync.GroupID, ids[0], time.Now())
	if err != nil || held.State != "blocked" || held.Outcome == nil || held.Outcome.Status != "blocked" || held.Attempts != 1 || len(held.Jobs) != 1 || held.Jobs[0].State != "deferred" || held.Jobs[0].JobID != jobs[0] {
		t.Fatalf("deferral falsely settled: %+v %v", held, err)
	}
	service.recoverManagement(ctx)
	settled, err := f.store.LoadManagementRequest(ctx, f.cfg.Sync.GroupID, ids[1], time.Now())
	if err != nil || settled.State != "finished" || settled.Outcome == nil || settled.Outcome.Status != "already_settled" || settled.Attempts != 1 || len(settled.Jobs) != 1 || settled.Jobs[0].JobID != jobs[1] {
		t.Fatalf("retained hold starved later recovery: %+v %v", settled, err)
	}
	// Ordinary effect recovery can later apply the original plan. Management
	// observes that durable head without reopening the safety-held import itself.
	if _, err := f.store.Exec(`UPDATE sync_jobs SET state='applied' WHERE job_id=?`, jobs[0]); err != nil {
		t.Fatal(err)
	}
	service.recoverManagement(ctx)
	settled, err = f.store.LoadManagementRequest(ctx, f.cfg.Sync.GroupID, ids[0], time.Now())
	if err != nil || settled.State != "finished" || settled.Outcome == nil || settled.Outcome.Status != "already_settled" || settled.Attempts != 1 || settled.Jobs[0].JobID != jobs[0] {
		t.Fatalf("applied original obligation did not settle: %+v %v", settled, err)
	}
}

func TestE23T5VerificationJournalSurvivesLostManagementCompletion(t *testing.T) {
	f := newResumeFixture(t)
	f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"verify-all", "retry"}}
	f.writeConfig(t)
	target := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef)
	bin := filepath.Join(filepath.Dir(f.remoteHead), "bin", "git")
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	condition := fmt.Sprintf("    test \"$last\" = '%s' || exit 2", f.cfg.Sync.MembershipRef)
	reply := fmt.Sprintf("    if test \"$last\" = '%s'; then printf '%%s\\t%%s\\n' '%s' \"$last\"; exit 0; fi\n", f.cfg.Sync.ContentRef, target)
	if !strings.Contains(string(raw), condition) {
		t.Fatal("remote fixture shape changed")
	}
	if err := os.WriteFile(bin, []byte(strings.Replace(string(raw), condition, reply+condition, 1)), 0700); err != nil {
		t.Fatal(err)
	}
	id := e23t5Accepted(t, f.cfg, f.path, "verify-all")
	if _, err := f.store.Exec(`CREATE TRIGGER lose_management_completion BEFORE UPDATE OF state ON sync_management_requests WHEN NEW.state='finished' BEGIN SELECT RAISE(ABORT,'injected process loss before management result'); END`); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYNC_NODE_A_TO_B", "outbound-secret")
	observations := 0
	client := &http.Client{Transport: e22t3RoundTrip(func(r *http.Request) (*http.Response, error) {
		observations++
		var q syncrecords.StatusRequest
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Fatal(err)
		}
		response := syncrecords.StatusResponse{SchemaVersion: syncrecords.StatusResponseSchema, GroupID: q.GroupID, Responder: q.Receiver, StateIncarnationID: f.cfg.Sync.Nodes[1].StateIncarnationID, MembershipRevision: q.MembershipRevision, ContentRef: q.ContentRef, TargetCommit: q.TargetCommit, ScopeDigest: q.ScopeDigest, ContractDigest: q.ContractDigest, Nonce: q.Nonce, EvidenceGeneration: time.Now().UnixNano(), State: "applied", MembershipCurrent: true}
		raw, _ := json.Marshal(response)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: http.Header{}}, nil
	})}
	service := &peerService{cfg: f.cfg, configPath: f.path, store: f.store}
	service.members = service.currentMembers
	service.dispatchManagementWithHTTP(context.Background(), client)
	r, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
	if err != nil || r.State != "running" || len(r.Jobs) != 1 || r.Jobs[0].State != "complete" || observations != 1 {
		t.Fatalf("lost result boundary:%+v %d %v", r, observations, err)
	}
	jobID := r.Jobs[0].JobID
	if _, err := f.store.Exec(`DROP TRIGGER lose_management_completion`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Exec(`UPDATE sync_management_requests SET claim_expires_at=? WHERE request_id=?`, time.Now().Add(-time.Minute).UTC().Format("2006-01-02T15:04:05.000000000Z"), id); err != nil {
		t.Fatal(err)
	}
	service.recoverManagement(context.Background())
	r, err = f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
	if err != nil || r.State != "finished" || r.Outcome == nil || r.Outcome.Status != "complete" || r.Outcome.VerificationID == nil || *r.Outcome.VerificationID != jobID || r.Outcome.TargetCommit == nil || *r.Outcome.TargetCommit != target || r.Attempts != 1 {
		t.Fatalf("journal result recovery:%+v %v", r, err)
	}
	code, v := e23t5Public(t, f.path, "prepare", "--group", f.cfg.Sync.GroupID, "--action", "retry", "--target-request-id", id)
	if code != 0 {
		t.Fatal(v)
	}
	retry := v["result"].(map[string]any)["request_id"].(string)
	code, v = e23t5Public(t, f.path, "submit", "--group", f.cfg.Sync.GroupID, "--request-id", retry)
	if code != 0 || v["result"].(map[string]any)["outcome"].(map[string]any)["status"] != "already_settled" {
		t.Fatalf("settled retry:%d %v", code, v)
	}
	service.dispatchManagementWithHTTP(context.Background(), client)
	if observations != 1 {
		t.Fatal("settled retry repeated verification")
	}
}

func TestE23T5SupportedRestoreRefusesObsoleteDraftAndRetainsAcceptedEvidence(t *testing.T) {
	cfg, path := e23t3Config(t)
	accepted := e23t5Accepted(t, cfg, path, "sync-now")
	code, v := e23t5Public(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "sync-now")
	if code != 0 {
		t.Fatal(v)
	}
	draft := v["result"].(map[string]any)["request_id"].(string)
	db := filepath.Join(cfg.Instance.StateDir, StateDBName)
	s, err := sqlite.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "restore.db")
	if err := s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// The documented stopped-writer restore uses disabled management and a
	// fresh configured incarnation before effects. This fixture restores only
	// its own verified standalone snapshot into a new disposable state root.
	cfg.Sync.Management.Enabled = false
	cfg.Sync.Nodes[0].StateIncarnationID = "restored-incarnation"
	cfg.Instance.StateDir = t.TempDir()
	raw, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Instance.StateDir, StateDBName), raw, 0600); err != nil {
		t.Fatal(err)
	}
	e23t3SaveConfig(t, cfg, path)
	code, v = e23t5Public(t, path, "show", "--group", cfg.Sync.GroupID, "--request-id", accepted)
	if code != 0 || v["result"].(map[string]any)["binding_current"] != false || v["result"].(map[string]any)["accepted_at"] == nil {
		t.Fatalf("restored accepted lookup:%d %v", code, v)
	}
	cfg.Sync.Management.Enabled = true
	e23t3SaveConfig(t, cfg, path)
	code, v = e23t5Public(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", draft)
	if code != 4 || v["error"].(map[string]any)["code"] != "sync_request_obsolete" {
		t.Fatalf("obsolete draft:%d %v", code, v)
	}
	code, v = e23t5Public(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", "mgr_"+strings.Repeat("f", 32))
	if code != 4 || v["error"].(map[string]any)["code"] != "sync_request_not_found" {
		t.Fatalf("missing restored handle:%d %v", code, v)
	}
	read, err := sqlite.OpenReadOnly(context.Background(), filepath.Join(cfg.Instance.StateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	r, err := read.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, draft, time.Now())
	if err != nil || r.AcceptedAt != nil || r.State != "prepared" || r.Attempts != 0 {
		t.Fatalf("obsolete draft executed:%+v %v", r, err)
	}
	if _, err := read.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, "mgr_"+strings.Repeat("f", 32), time.Now()); !errors.Is(err, sqlite.ErrManagementNotFound) {
		t.Fatal(err)
	}
}

func TestE23T5PublicRetryRefusesUnrecordedWork(t *testing.T) {
	cfg, path := e23t3Config(t)
	id := e23t5Accepted(t, cfg, path, "sync-now")
	code, v := e23t5Public(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "retry", "--target-request-id", id)
	if code != 14 || v["error"].(map[string]any)["code"] != "sync_request_not_retryable" {
		t.Fatalf("unrecorded retry:%d %v", code, v)
	}
	if v["error"].(map[string]any)["retryable"] != false {
		t.Fatal("adapter automatic retry advertised")
	}
}

func TestE23T5InterruptedPlannedVerificationExpiresWithoutNewObservation(t *testing.T) {
	cfg, path := e23t3Config(t)
	id := e23t5Accepted(t, cfg, path, "verify-all")
	s, err := sqlite.Open(filepath.Join(cfg.Instance.StateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	revision, _ := config.SyncRevision(cfg)
	now := time.Now()
	then := now.Add(-6 * time.Minute)
	if _, err := s.EnsureSyncControl(context.Background(), cfg.Sync.GroupID, revision, then.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	b, _ := managementBindingForConfig(cfg)
	r, err := s.ClaimManagementRequest(context.Background(), cfg.Sync.GroupID, id, "interrupted-verifier", b, true, then)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := s.AdmitSyncJob(context.Background(), sqlite.SyncJobInput{JobID: "planned-verification", GroupID: cfg.Sync.GroupID, Kind: "verification", LogicalKey: "planned-verification", InitialState: "planned", PayloadJSON: `{"phase":"planned"}`, QueueLimit: 1000, Now: then.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.ReserveManagementAssociations(context.Background(), r, 1, then)
	if err != nil {
		t.Fatal(err)
	}
	target := strings.Repeat("a", 40)
	if _, err := s.AssociateManagementJob(context.Background(), r, sqlite.ManagementJob{JobID: job.JobID, Kind: job.Kind, TargetCommit: &target, AssociationReason: "verification_target"}, then); err != nil {
		t.Fatal(err)
	}
	service := &peerService{cfg: cfg, configPath: path, store: s}
	service.recoverManagement(context.Background())
	r, err = s.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, id, now)
	if err != nil || r.State != "finished" || r.Outcome == nil || r.Outcome.Status != "expired" || r.Outcome.TargetCommit != nil || r.Outcome.VerificationID != nil || len(r.Jobs) != 1 || r.Jobs[0].JobID != job.JobID || r.Jobs[0].State != "expired" {
		t.Fatalf("interrupted planned verification:%+v %v", r, err)
	}
	service.recoverManagement(context.Background())
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='verification'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("expiry invented another observation:%d %v", n, err)
	}
}

func TestE23T5PublicRetryDeliversOnlyEligibleRecordedJobs(t *testing.T) {
	f := newResumeFixture(t)
	f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now", "retry"}}
	f.writeConfig(t)
	revision, _ := config.SyncRevision(f.cfg)
	if _, err := f.store.SetSyncControl(context.Background(), f.cfg.Sync.GroupID, 1, "active", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	id := e23t5Accepted(t, f.cfg, f.path, "sync-now")
	b, _ := managementBindingForConfig(f.cfg)
	r, err := f.store.ClaimManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, "original-worker", b, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	target := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef)
	membership := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.MembershipRef)
	for _, name := range []string{"eligible", "exhausted", "unrecorded"} {
		p := syncrecords.Nudge{SchemaVersion: syncrecords.NudgeSchema, GroupID: f.cfg.Sync.GroupID, PublicationID: "publication-" + name, Sender: f.cfg.Sync.LocalInstanceID, Receiver: f.cfg.Sync.Nodes[1].InstanceID, MembershipRevision: membership, ContentRef: f.cfg.Sync.ContentRef, TargetCommit: target}
		raw, _ := json.Marshal(p)
		job, _, err := f.store.AdmitSyncJob(context.Background(), sqlite.SyncJobInput{JobID: "delivery-" + name, GroupID: p.GroupID, Kind: "delivery", LogicalKey: p.PublicationID, InitialState: "pending", PayloadJSON: string(raw), ConfigRevision: revision, QueueLimit: 1000, Now: time.Now().UTC().Format(time.RFC3339Nano)})
		if err != nil {
			t.Fatal(err)
		}
		if name == "exhausted" {
			if _, err := f.store.Exec(`UPDATE sync_jobs SET attempts=20 WHERE job_id=?`, job.JobID); err != nil {
				t.Fatal(err)
			}
		}
		if name == "unrecorded" {
			continue
		}
		r, err = f.store.ReserveManagementAssociations(context.Background(), r, 1, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		r, err = f.store.AssociateManagementJob(context.Background(), r, sqlite.ManagementJob{JobID: job.JobID, Kind: job.Kind, TargetCommit: &target, AssociationReason: "observed_reconciliation"}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.TransitionManagementRequest(context.Background(), r, "uncertain", &syncmanagement.Outcome{Status: "uncertain", Reason: "injected original worker interruption"}, "fixture-recorded-obligations", time.Now()); err != nil {
		t.Fatal(err)
	}
	code, v := e23t5Public(t, f.path, "prepare", "--group", f.cfg.Sync.GroupID, "--action", "retry", "--target-request-id", id)
	if code != 0 {
		t.Fatal(v)
	}
	retry := v["result"].(map[string]any)["request_id"].(string)
	if code, v = e23t5Public(t, f.path, "submit", "--group", f.cfg.Sync.GroupID, "--request-id", retry); code != 0 {
		t.Fatal(v)
	}
	if jobs := v["result"].(map[string]any)["jobs"].([]any); len(jobs) != 2 {
		t.Fatalf("retry submit omitted recorded obligations:%v", jobs)
	}
	t.Setenv("SYNC_NODE_A_TO_B", "outbound-secret")
	calls := 0
	client := &http.Client{Transport: e22t3RoundTrip(func(q *http.Request) (*http.Response, error) {
		calls++
		var p syncrecords.Nudge
		if err := json.NewDecoder(q.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if p.PublicationID != "publication-eligible" || p.TargetCommit != target {
			t.Fatalf("retry escaped recorded obligation:%+v", p)
		}
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
	})}
	service := &peerService{cfg: f.cfg, configPath: f.path, store: f.store}
	service.members = service.currentMembers
	service.dispatchManagementWithHTTP(context.Background(), client)
	got, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, retry, time.Now())
	if err != nil || got.State != "finished" || got.Outcome == nil || got.Outcome.Status != "incomplete" || len(got.Jobs) != 2 || calls != 1 {
		t.Fatalf("mixed bounded retry:%+v calls%d %v", got, calls, err)
	}
	for _, j := range got.Jobs {
		if j.AssociationReason != "retry_obligation" || j.TargetCommit == nil || *j.TargetCommit != target {
			t.Fatalf("retry association changed:%+v", j)
		}
		if j.JobID == "delivery-exhausted" && (j.Attempts != 20 || j.State != "pending") {
			t.Fatalf("retry reset exhausted obligation:%+v", j)
		}
		if j.JobID == "delivery-eligible" && (j.Attempts != 1 || j.State != "accepted") {
			t.Fatalf("retry failed eligible obligation:%+v", j)
		}
	}
	var attempts int
	if err := f.store.QueryRow(`SELECT attempts FROM sync_jobs WHERE job_id='delivery-unrecorded'`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("unrecorded job executed:%d %v", attempts, err)
	}
	service.dispatchManagementWithHTTP(context.Background(), client)
	if calls != 1 {
		t.Fatal("finished retry repeated effect")
	}
}

func TestE23T5MaintenanceBeforeCannotShortenManagementRetention(t *testing.T) {
	cfg, path := e23t3Config(t)
	horizon := "400d"
	cfg.Retention = &config.Retention{CompletedReceipts: &horizon}
	e23t3SaveConfig(t, cfg, path)
	id := e23t5Accepted(t, cfg, path, "sync-now")
	s, err := sqlite.Open(filepath.Join(cfg.Instance.StateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	revision, _ := config.SyncRevision(cfg)
	if _, err := s.EnsureSyncControl(context.Background(), cfg.Sync.GroupID, revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	b, _ := managementBindingForConfig(cfg)
	r, err := s.ClaimManagementRequest(context.Background(), cfg.Sync.GroupID, id, "retention-worker", b, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionManagementRequest(context.Background(), r, "finished", &syncmanagement.Outcome{Status: "no_op", Reason: "fixture inspected no admitted effect"}, "retention-inspection", time.Now()); err != nil {
		t.Fatal(err)
	}
	// A terminal row's timestamp cannot be rewritten. Make a second accepted
	// historical fixture through the same typed clock/transition primitives.
	id2, _ := syncmanagement.NewHandle()
	b.Action = "sync-now"
	then := time.Now().Add(-201 * 24 * time.Hour)
	_, _, err = s.PrepareManagementRequest(context.Background(), id2, b, then)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.AcceptManagementRequest(context.Background(), sqlite.ManagementAdmission{GroupID: b.GroupID, RequestID: id2, CurrentBinding: b, Authorized: true, QueueLimit: 1000, Clock: func() time.Time { return then }})
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.ClaimManagementRequest(context.Background(), b.GroupID, id2, "historical-worker", b, true, then)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionManagementRequest(context.Background(), r, "finished", &syncmanagement.Outcome{Status: "no_op", Reason: "historical fixture inspected no effect"}, "historical-inspection", then); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Run([]string{"maintenance", "prune", "--config", path, "--before", "1d"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("maintenance dry-run:%d %s", code, &errOut)
	}
	result := decodeResult(t, &out)
	cutoff := result["plan"].(map[string]any)["cutoffs"].(map[string]any)["management_terminal"].(string)
	cut, err := time.Parse(time.RFC3339Nano, cutoff)
	if err != nil || time.Since(cut) < 399*24*time.Hour {
		t.Fatalf("shortened horizon:%s %v", cutoff, err)
	}
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"maintenance", "prune", "--config", path, "--before", "1d", "--yes"}, &out, &errOut); code != 0 {
		t.Fatalf("maintenance execution:%d %s", code, &errOut)
	}
	if _, err := s.LoadManagementRequest(context.Background(), b.GroupID, id2, time.Now()); err != nil {
		t.Fatalf("longer configured evidence pruned:%v", err)
	}
}

// Extend the disposable signed-publication fixture with a replay whose remote
// effect survived but whose local publication completion was interrupted.
func assertE23T5SignedRetry(t *testing.T, cfg *config.Config, path string, store *sqlite.Store, git, repo string, predecessor sqlite.ManagementRequest, jobID, base, target string) {
	t.Helper()
	if _, err := store.Exec(`UPDATE sync_jobs SET state='signed',retain_until_resolved=1,resolved_at=NULL,claim_owner=NULL,claim_expires_at=NULL WHERE job_id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, git, repo, "update-ref", cfg.Sync.ContentRef, base, target)
	code, v := e23t5Public(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "retry", "--target-request-id", predecessor.RequestID)
	if code != 0 {
		t.Fatal(v)
	}
	id := v["result"].(map[string]any)["request_id"].(string)
	if code, v = e23t5Public(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", id); code != 0 {
		t.Fatal(v)
	}
	t.Setenv("SYNC_NODE_A_TO_B", "outbound-secret")
	calls := 0
	client := &http.Client{Transport: e22t3RoundTrip(func(q *http.Request) (*http.Response, error) {
		calls++
		var p syncrecords.Nudge
		if err := json.NewDecoder(q.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if p.TargetCommit != target {
			t.Fatalf("signed retry moved target:%+v", p)
		}
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
	})}
	service := &peerService{cfg: cfg, configPath: path, store: store}
	service.members = service.currentMembers
	service.dispatchManagementWithHTTP(context.Background(), client)
	r, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, id, time.Now())
	if err != nil || r.State != "finished" || r.Outcome == nil || r.Outcome.Status != "already_settled" || len(r.Jobs) != 2 || calls != 1 {
		t.Fatalf("signed retry result:%+v calls%d %v", r, calls, err)
	}
	for _, j := range r.Jobs {
		if j.AssociationReason != "retry_obligation" || j.TargetCommit == nil || *j.TargetCommit != target {
			t.Fatalf("signed retry association:%+v", j)
		}
		found := false
		for _, original := range predecessor.Jobs {
			if original.JobID == j.JobID && original.Kind == j.Kind {
				found = true
			}
		}
		if !found {
			t.Fatalf("signed retry invented underlying identity:%+v", j)
		}
	}
	if got := gitTestOutput(t, git, repo, "rev-parse", cfg.Sync.ContentRef); got != target {
		t.Fatalf("signed retry target:%s", got)
	}
	service.dispatchManagementWithHTTP(context.Background(), client)
	service.recoverManagement(context.Background())
	if calls != 1 {
		t.Fatal("settled signed retry repeated delivery")
	}
}

func assertE23T5RetryStoredImport(t *testing.T, cfg *config.Config, path string, store *sqlite.Store, target, phase string) {
	t.Helper()
	id := e23t5Accepted(t, cfg, path, "sync-now")
	if _, err := store.Exec(`CREATE TRIGGER e23t5_import_claim_loss BEFORE UPDATE OF claim_owner ON sync_jobs WHEN NEW.kind='import' AND NEW.claim_owner<>'' BEGIN SELECT RAISE(ABORT,'injected import claim loss'); END`); err != nil {
		t.Fatal(err)
	}
	service := &peerService{cfg: cfg, configPath: path, store: store}
	service.dispatchManagement(context.Background())
	if _, err := store.Exec(`DROP TRIGGER e23t5_import_claim_loss`); err != nil {
		t.Fatal(err)
	}
	original, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, id, time.Now())
	if err != nil || len(original.Jobs) != 1 || original.Jobs[0].Kind != "import" || original.Jobs[0].State != "validated" || original.Jobs[0].Attempts != 0 {
		t.Fatalf("admitted before claim:%+v %v", original, err)
	}
	jobID := original.Jobs[0].JobID
	var payload string
	var importsBefore int
	if err := store.QueryRow(`SELECT payload_json FROM sync_jobs WHERE job_id=?`, jobID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='import'`).Scan(&importsBefore); err != nil {
		t.Fatal(err)
	}
	record, err := syncrecords.DecodeImport([]byte(payload))
	if err != nil || record.TargetCommit != target || record.ControllerOnly {
		t.Fatalf("stored plan:%+v %v", record, err)
	}
	expectedAttempts := 1
	if phase != "admission" {
		revision, _ := config.SyncRevision(cfg)
		now := time.Now().UTC()
		claimed, err := store.ClaimSyncJob(context.Background(), jobID, "lost-import-worker", revision, now.Format(time.RFC3339Nano), now.Add(time.Minute).Format(time.RFC3339Nano))
		if err != nil {
			t.Fatal(err)
		}
		if phase == "preapply" {
			at := now.Format(time.RFC3339Nano)
			err = store.BeginImportApply(context.Background(), jobID, "lost-import-worker", cfg.Sync.Resource, claimed.Fence, record.Paths, sqlite.SyncJournalEntry{JournalID: randomSyncID("injected-preapply"), JobID: jobID, Fence: claimed.Fence, Phase: "import", Outcome: "started", EvidenceJSON: `{"injected":"loss_before_file_effect"}`, RecordedAt: at}, at)
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := store.Exec(`UPDATE sync_jobs SET claim_expires_at=? WHERE job_id=?`, now.Add(-time.Minute).Format(time.RFC3339Nano), jobID); err != nil {
			t.Fatal(err)
		}
		expectedAttempts = 2
	}
	code, v := e23t5Public(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "retry", "--target-request-id", id)
	if code != 0 {
		t.Fatalf("retry prepare:%d %v", code, v)
	}
	retry := v["result"].(map[string]any)["request_id"].(string)
	if code, v = e23t5Public(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", retry); code != 0 {
		t.Fatal(v)
	}
	service.dispatchManagement(context.Background())
	got, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, retry, time.Now())
	if err != nil || got.State != "finished" || got.Outcome == nil || got.Outcome.Status != "already_settled" || len(got.Jobs) != 1 {
		t.Fatalf("stored import replay:%+v %v", got, err)
	}
	j := got.Jobs[0]
	if j.JobID != jobID || j.State != "applied" || j.Attempts != expectedAttempts || j.TargetCommit == nil || *j.TargetCommit != target || j.AssociationReason != "retry_obligation" {
		t.Fatalf("replay changed identity/target/budget:%+v", j)
	}
	var afterPayload string
	var importsAfter int
	if err := store.QueryRow(`SELECT payload_json FROM sync_jobs WHERE job_id=?`, jobID).Scan(&afterPayload); err != nil {
		t.Fatal(err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='import'`).Scan(&importsAfter); err != nil {
		t.Fatal(err)
	}
	if payload != afterPayload || importsBefore != importsAfter {
		t.Fatal("retry admitted or rewrote an import plan")
	}
	service.dispatchManagement(context.Background())
	repeated, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, retry, time.Now())
	if err != nil || repeated.Jobs[0].Attempts != expectedAttempts || repeated.Revision != got.Revision {
		t.Fatalf("completed replay repeated effects:%+v %v", repeated, err)
	}
	if code, v = e23t5Public(t, path, "show", "--group", cfg.Sync.GroupID, "--request-id", id); code != 0 {
		t.Fatal(v)
	}
}

func TestE23T5AcceptanceTransactionLossRetainsExactDraft(t *testing.T) {
	for _, boundary := range []string{"BEFORE", "AFTER"} {
		t.Run(boundary, func(t *testing.T) {
			cfg, path := e23t3Config(t)
			code, v := e23t5Public(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "sync-now")
			if code != 0 {
				t.Fatal(v)
			}
			id := v["result"].(map[string]any)["request_id"].(string)
			store, err := sqlite.Open(filepath.Join(cfg.Instance.StateDir, StateDBName))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			draft, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, id, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			trigger := fmt.Sprintf(`CREATE TRIGGER e23t5_acceptance_loss %s INSERT ON sync_management_acceptances BEGIN SELECT RAISE(ABORT,'injected acceptance loss'); END`, boundary)
			if _, err := store.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			if code, v = e23t5Public(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", id); code != 20 || v["error"].(map[string]any)["code"] != "sqlite_write_failed" {
				t.Fatalf("acceptance failure:%d %v", code, v)
			}
			got, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, id, time.Now())
			if err != nil || got.State != "prepared" || got.AcceptedAt != nil || got.Revision != draft.Revision || got.Fingerprint != draft.Fingerprint || got.Attempts != 0 || len(got.Jobs) != 0 {
				t.Fatalf("partial acceptance escaped:%+v %v", got, err)
			}
			for _, table := range []string{"sync_management_acceptances", "sync_management_executions", "sync_jobs"} {
				var n int
				if err := store.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
					t.Fatalf("partial transaction %s:%d %v", table, n, err)
				}
			}
			if _, err := store.Exec(`DROP TRIGGER e23t5_acceptance_loss`); err != nil {
				t.Fatal(err)
			}
			if code, v = e23t5Public(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", id); code != 0 {
				t.Fatal(v)
			}
			accepted, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, id, time.Now())
			if err != nil || accepted.AcceptedAt == nil || accepted.State != "queued" || accepted.Fingerprint != draft.Fingerprint {
				t.Fatalf("exact resubmit:%+v %v", accepted, err)
			}
			if code, v = e23t5Public(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", id); code != 0 {
				t.Fatal(v)
			}
			var n int
			if err := store.QueryRow(`SELECT COUNT(*) FROM sync_management_acceptances WHERE request_id=?`, id).Scan(&n); err != nil || n != 1 {
				t.Fatalf("duplicate acceptance:%d %v", n, err)
			}
		})
	}
}

func e23t5RecordedDelivery(t *testing.T, cfg *config.Config, path string) (*sqlite.Store, string, string, string) {
	t.Helper()
	id := e23t5Accepted(t, cfg, path, "sync-now")
	store, err := sqlite.Open(filepath.Join(cfg.Instance.StateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	revision, _ := config.SyncRevision(cfg)
	if _, err := store.EnsureSyncControl(context.Background(), cfg.Sync.GroupID, revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	b, _ := managementBindingForConfig(cfg)
	r, err := store.ClaimManagementRequest(context.Background(), cfg.Sync.GroupID, id, "interrupted-worker", b, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	target := strings.Repeat("a", 40)
	nudge := syncrecords.Nudge{SchemaVersion: syncrecords.NudgeSchema, GroupID: cfg.Sync.GroupID, PublicationID: "recorded-delivery", Sender: cfg.Sync.LocalInstanceID, Receiver: cfg.Sync.Nodes[1].InstanceID, MembershipRevision: strings.Repeat("b", 40), ContentRef: cfg.Sync.ContentRef, TargetCommit: target}
	raw, err := json.Marshal(nudge)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := store.AdmitSyncJob(context.Background(), sqlite.SyncJobInput{JobID: "recorded-job", GroupID: cfg.Sync.GroupID, Kind: "delivery", LogicalKey: nudge.PublicationID, InitialState: "pending", PayloadJSON: string(raw), ConfigRevision: revision, QueueLimit: 1000, Now: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	r, err = store.ReserveManagementAssociations(context.Background(), r, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r, err = store.AssociateManagementJob(context.Background(), r, sqlite.ManagementJob{JobID: job.JobID, Kind: job.Kind, TargetCommit: &target, AssociationReason: "observed_reconciliation"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionManagementRequest(context.Background(), r, "uncertain", &syncmanagement.Outcome{Status: "uncertain", Reason: "injected worker loss"}, "fixture-recorded-delivery", time.Now()); err != nil {
		t.Fatal(err)
	}
	return store, id, job.JobID, target
}

func TestE23T5PublicRetryRefusalMatrixPreservesPredecessor(t *testing.T) {
	for _, mode := range []string{"revoked", "stale", "obsolete", "wrong-request", "wrong-group", "exhausted"} {
		t.Run(mode, func(t *testing.T) {
			cfg, path := e23t3Config(t)
			store, id, jobID, _ := e23t5RecordedDelivery(t, cfg, path)
			targetID, group := id, cfg.Sync.GroupID
			wantExit, wantCode := 14, "sync_request_binding_changed"
			switch mode {
			case "revoked":
				cfg.Sync.Management.Enabled = false
				e23t3SaveConfig(t, cfg, path)
				wantExit, wantCode = 3, "sync_management_disabled"
			case "stale":
				cfg.Sync.Bounds.Queue--
				e23t3SaveConfig(t, cfg, path)
			case "obsolete":
				for i := range cfg.Sync.Nodes {
					if cfg.Sync.Nodes[i].InstanceID == cfg.Sync.LocalInstanceID {
						cfg.Sync.Nodes[i].StateIncarnationID = "new-incarnation"
					}
				}
				e23t3SaveConfig(t, cfg, path)
				wantExit, wantCode = 4, "sync_request_obsolete"
			case "wrong-request":
				targetID = "mgr_" + strings.Repeat("f", 32)
				wantExit, wantCode = 4, "sync_request_not_found"
			case "wrong-group":
				group = "unknown-group"
				wantExit, wantCode = 3, "sync_group_not_found"
			case "exhausted":
				if _, err := store.Exec(`UPDATE sync_jobs SET attempts=20 WHERE job_id=?`, jobID); err != nil {
					t.Fatal(err)
				}
				wantCode = "sync_request_not_retryable"
			}
			before, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, id, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			counts := func() []int {
				t.Helper()
				out := []int{}
				for _, table := range []string{"sync_management_requests", "sync_management_acceptances", "sync_jobs"} {
					var n int
					if err := store.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
						t.Fatal(err)
					}
					out = append(out, n)
				}
				return out
			}
			beforeCounts := counts()
			code, v := e23t5Public(t, path, "prepare", "--group", group, "--action", "retry", "--target-request-id", targetID)
			if code != wantExit {
				t.Fatalf("refusal exit:%d %v", code, v)
			}
			e := v["error"].(map[string]any)
			if e["code"] != wantCode || e["retryable"] != false {
				t.Fatalf("exact refusal:%v", e)
			}
			after, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, id, time.Now())
			if err != nil || after.Fingerprint != before.Fingerprint || after.Revision != before.Revision || after.AcceptedAt == nil || len(after.Jobs) != 1 || after.Jobs[0].Attempts != before.Jobs[0].Attempts || fmt.Sprint(counts()) != fmt.Sprint(beforeCounts) {
				t.Fatalf("refusal changed durable evidence:%+v %v", after, err)
			}
		})
	}
}

func TestE23T5AcceptedRetryRevocationBeforeDispatchRetainsEvidence(t *testing.T) {
	for _, mode := range []string{"revoked", "rebound"} {
		t.Run(mode, func(t *testing.T) {
			cfg, path := e23t3Config(t)
			store, id, jobID, target := e23t5RecordedDelivery(t, cfg, path)
			code, v := e23t5Public(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "retry", "--target-request-id", id)
			if code != 0 {
				t.Fatal(v)
			}
			retry := v["result"].(map[string]any)["request_id"].(string)
			if code, v = e23t5Public(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", retry); code != 0 {
				t.Fatal(v)
			}
			if mode == "revoked" {
				cfg.Sync.Management.Enabled = false
			} else {
				cfg.Sync.Bounds.Queue--
			}
			e23t3SaveConfig(t, cfg, path)
			service := &peerService{cfg: cfg, configPath: path, store: store}
			service.dispatchManagement(context.Background())
			got, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, retry, time.Now())
			if err != nil || got.State != "blocked" || got.AcceptedAt == nil || got.Attempts != 1 || got.Fence != 0 || got.Outcome == nil || got.Outcome.Status != "blocked" || len(got.Jobs) != 1 || got.Jobs[0].JobID != jobID || got.Jobs[0].Attempts != 0 || got.Jobs[0].State != "pending" || got.Jobs[0].TargetCommit == nil || *got.Jobs[0].TargetCommit != target {
				t.Fatalf("post-acceptance authority:%+v %v", got, err)
			}
			if mode == "revoked" {
				cfg.Sync.Management.Enabled = true
			} else {
				cfg.Sync.Bounds.Queue++
			}
			e23t3SaveConfig(t, cfg, path)
			service.recoverManagement(context.Background())
			retained, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, retry, time.Now())
			if err != nil || retained.State != "blocked" || retained.Revision != got.Revision || retained.Attempts != got.Attempts || len(retained.Jobs) != 1 || retained.Jobs[0].JobID != jobID || retained.Jobs[0].Attempts != 0 || retained.Jobs[0].TargetCommit == nil || *retained.Jobs[0].TargetCommit != target {
				t.Fatalf("associated hold was requeued without effect inspection:%+v %v", retained, err)
			}
			if code, v = e23t5Public(t, path, "show", "--group", cfg.Sync.GroupID, "--request-id", retry); code != 0 {
				t.Fatal(v)
			}
		})
	}
}

func TestE23T5DeliveryReceiptBeforeJournalCompletionReplaysOneLogicalEffect(t *testing.T) {
	cfg, path := e23t3Config(t)
	store, id, jobID, target := e23t5RecordedDelivery(t, cfg, path)
	receiver, err := sqlite.Open(filepath.Join(t.TempDir(), "receiver.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	if err := receiver.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	code, v := e23t5Public(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "retry", "--target-request-id", id)
	if code != 0 {
		t.Fatal(v)
	}
	retry := v["result"].(map[string]any)["request_id"].(string)
	if code, v = e23t5Public(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", retry); code != 0 {
		t.Fatal(v)
	}
	t.Setenv("SYNC_NODE_A_TO_B", "fixture-outbound-secret")
	service := &peerService{cfg: cfg, configPath: path, store: store}
	service.members = func(context.Context) (string, [2]syncrecords.ActiveMember, error) {
		return strings.Repeat("b", 40), [2]syncrecords.ActiveMember{{InstanceID: cfg.Sync.Nodes[1].InstanceID, Endpoint: cfg.Sync.Nodes[1].Endpoint}}, nil
	}
	calls, logicalEffects := 0, 0
	var firstPayload string
	client := &http.Client{Transport: e22t3RoundTrip(func(q *http.Request) (*http.Response, error) {
		calls++
		raw, err := io.ReadAll(q.Body)
		if err != nil {
			t.Fatal(err)
		}
		nudge, err := syncrecords.DecodeNudge(raw)
		if err != nil || nudge.PublicationID != "recorded-delivery" || nudge.TargetCommit != target {
			t.Fatalf("delivery identity:%+v %v", nudge, err)
		}
		if firstPayload == "" {
			firstPayload = string(raw)
		} else if firstPayload != string(raw) {
			t.Fatal("replay changed nudge bytes")
		}
		duplicate, err := receiver.AdmitPeerNudge(context.Background(), sqlite.PeerNudgeInput{GroupID: nudge.GroupID, PublicationID: nudge.PublicationID, Fingerprint: "sha256:" + fmt.Sprintf("%x", sha256.Sum256(raw)), PayloadJSON: string(raw), ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano), QueueLimit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		if !duplicate {
			logicalEffects++
		}
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
	})}
	if _, err := store.Exec(`CREATE TRIGGER e23t5_delivery_finish_loss BEFORE UPDATE OF state ON sync_jobs WHEN NEW.job_id='recorded-job' AND NEW.state='accepted' BEGIN SELECT RAISE(ABORT,'injected journal completion loss'); END`); err != nil {
		t.Fatal(err)
	}
	service.dispatchManagementWithHTTP(context.Background(), client)
	lost, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, retry, time.Now())
	if err != nil || lost.State != "uncertain" || calls != 1 || logicalEffects != 1 {
		t.Fatalf("effect-before-journal:%+v calls%d effects%d %v", lost, calls, logicalEffects, err)
	}
	job, err := store.LoadLatestSyncJob(context.Background(), cfg.Sync.GroupID, "delivery")
	if err != nil || job.JobID != jobID || job.Attempts != 1 || job.ClaimOwner == "" || job.ResolvedAt != "" {
		t.Fatalf("lost journal claim:%+v %v", job, err)
	}
	service.recoverManagement(context.Background())
	live, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, retry, time.Now())
	if err != nil || live.State != "uncertain" || calls != 1 {
		t.Fatalf("age inferred no effect:%+v %v", live, err)
	}
	if _, err := store.Exec(`DROP TRIGGER e23t5_delivery_finish_loss`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`UPDATE sync_jobs SET claim_expires_at=? WHERE job_id=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), jobID); err != nil {
		t.Fatal(err)
	}
	if err := service.deliverPending(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	job, err = store.LoadLatestSyncJob(context.Background(), cfg.Sync.GroupID, "delivery")
	if err != nil || job.State != "unknown" || job.Attempts != 1 || job.ResolvedAt != "" || calls != 1 {
		t.Fatalf("expired effect remains unknown:%+v %v", job, err)
	}
	code, v = e23t5Public(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "retry", "--target-request-id", retry)
	if code != 0 {
		t.Fatal(v)
	}
	child := v["result"].(map[string]any)["request_id"].(string)
	if code, v = e23t5Public(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", child); code != 0 {
		t.Fatal(v)
	}
	service.dispatchManagementWithHTTP(context.Background(), client)
	job, err = store.LoadLatestSyncJob(context.Background(), cfg.Sync.GroupID, "delivery")
	if err != nil || job.State != "retryable" || job.Attempts != 1 || calls != 1 {
		t.Fatalf("explicit replay preparation:%+v %v", job, err)
	}
	if _, err := store.Exec(`UPDATE sync_jobs SET updated_at=? WHERE job_id=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), jobID); err != nil {
		t.Fatal(err)
	}
	if err := service.deliverPending(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	job, err = store.LoadLatestSyncJob(context.Background(), cfg.Sync.GroupID, "delivery")
	if err != nil || job.State != "accepted" || job.Attempts != 2 || job.Fence != 2 || job.LogicalKey != "recorded-delivery" || job.ResolvedAt == "" || calls != 2 || logicalEffects != 1 {
		t.Fatalf("idempotent receiver replay:%+v calls%d effects%d %v", job, calls, logicalEffects, err)
	}
	var receipts int
	if err := receiver.QueryRow(`SELECT COUNT(*) FROM sync_peer_nudges`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("logical receipts:%d %v", receipts, err)
	}
	service.recoverManagement(context.Background())
	code, v = e23t5Public(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "retry", "--target-request-id", retry)
	if code != 0 {
		t.Fatal(v)
	}
	settled := v["result"].(map[string]any)["request_id"].(string)
	if code, v = e23t5Public(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", settled); code != 0 {
		t.Fatal(v)
	}
	done, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, settled, time.Now())
	if err != nil || done.State != "finished" || done.Attempts != lost.Attempts || done.Fence != 0 || done.Outcome == nil || done.Outcome.Status != "already_settled" || len(done.Jobs) != 1 || done.Jobs[0].JobID != jobID || done.Jobs[0].TargetCommit == nil || *done.Jobs[0].TargetCommit != target {
		t.Fatalf("periodic settlement lookup:%+v %v", done, err)
	}
	service.dispatchManagementWithHTTP(context.Background(), client)
	if calls != 2 || logicalEffects != 1 {
		t.Fatal("settled replay duplicated receiver effect")
	}
}

func TestE23T5PublicRetryRefusesControlAndUnacceptedPredecessors(t *testing.T) {
	for _, mode := range []string{"control", "unaccepted"} {
		t.Run(mode, func(t *testing.T) {
			cfg, path := e23t3Config(t)
			var id string
			wantCode := "sync_request_not_retryable"
			if mode == "control" {
				id = e23t5Accepted(t, cfg, path, "pause")
			} else {
				code, value := e23t5Public(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "sync-now")
				if code != 0 {
					t.Fatal(value)
				}
				id = value["result"].(map[string]any)["request_id"].(string)
				wantCode = "sync_request_binding_changed"
			}
			store, err := sqlite.Open(filepath.Join(cfg.Instance.StateDir, StateDBName))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			before, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, id, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			code, value := e23t5Public(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "retry", "--target-request-id", id)
			if code != 14 || value["error"].(map[string]any)["code"] != wantCode || value["error"].(map[string]any)["retryable"] != false {
				t.Fatalf("retry refusal:%d %v", code, value)
			}
			after, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, id, time.Now())
			if err != nil || after.Revision != before.Revision || after.Fingerprint != before.Fingerprint {
				t.Fatalf("refusal changed predecessor:%+v %v", after, err)
			}
			var requests, jobs int
			if err := store.QueryRow(`SELECT COUNT(*) FROM sync_management_requests`).Scan(&requests); err != nil {
				t.Fatal(err)
			}
			if err := store.QueryRow(`SELECT COUNT(*) FROM sync_jobs`).Scan(&jobs); err != nil || jobs != 0 || requests != 1 {
				t.Fatalf("refusal created work:%d/%d %v", requests, jobs, err)
			}
		})
	}
}

func TestE23T5OperatorAdoptionHoldsRequireSpecificRemoval(t *testing.T) {
	for _, kind := range []string{"membership", "membership_normal", "checkpoint", "checkpoint_signed", "checkpoint_unsigned", "checkpoint_ancestor"} {
		t.Run(kind, func(t *testing.T) {
			holdKind := strings.Split(kind, "_")[0]
			f := newResumeFixture(t)
			f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now"}}
			f.writeConfig(t)
			revision, _ := config.SyncRevision(f.cfg)
			if _, err := f.store.SetSyncControl(context.Background(), f.cfg.Sync.GroupID, 1, "active", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			membership := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.MembershipRef)
			base := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef)
			target := base
			adoptedHead := ""
			if holdKind == "membership" {
				client, err := membershipGitClient(f.cfg, f.cfg.Sync)
				if err != nil {
					t.Fatal(err)
				}
				raw, _, err := client.ReadMembershipCommit(context.Background(), membership)
				if err != nil {
					t.Fatal(err)
				}
				current, err := syncrecords.DecodeMembership(raw)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "membership_normal" {
					desired := configuredMembers(f.cfg.Sync)
					for i := range desired {
						if desired[i].InstanceID == f.cfg.Sync.Nodes[1].InstanceID {
							desired[i].Endpoint = "https://shifted-node.fixture.ts.net:8443"
						}
					}
					plan, err := syncrecords.NewPlan("endpoint_update", f.cfg.Sync.Nodes[1].InstanceID, &current, &membership, desired, membershipBinding(f.cfg, f.cfg.Sync))
					if err != nil {
						t.Fatal(err)
					}
					shifted := f.signMembership(t, client, plan, membership)
					normal, err := syncrecords.NewPlan("endpoint_update", f.cfg.Sync.Nodes[1].InstanceID, &plan.ProposedMembership, &shifted, configuredMembers(f.cfg.Sync), membershipBinding(f.cfg, f.cfg.Sync))
					if err != nil {
						t.Fatal(err)
					}
					f.signMembership(t, client, normal, shifted)
				} else {
					plan, err := syncrecords.NewPlan("emergency_revocation", f.cfg.Sync.Nodes[1].InstanceID, &current, &membership, configuredMembers(f.cfg.Sync), membershipBinding(f.cfg, f.cfg.Sync))
					if err != nil {
						t.Fatal(err)
					}
					f.signMembership(t, client, plan, membership)
				}
				gitTestRun(t, f.git, f.repo, "update-ref", f.cfg.Sync.MembershipRef, membership)
			} else {
				if kind != "checkpoint" {
					client, err := membershipGitClient(f.cfg, f.cfg.Sync)
					if err != nil {
						t.Fatal(err)
					}
					digest, err := snapshotDigestFromFiles(map[string][]byte{"note.md": []byte("base\n")})
					if err != nil {
						t.Fatal(err)
					}
					checkpointCommit := func(parent, checkpointKind string) string {
						t.Helper()
						plan, err := syncrecords.NewCheckpointPlan(f.cfg.Sync.GroupID, checkpointKind, parent, digest, config.SyncScopeDigest(f.cfg, f.cfg.Sync.Resource), config.SyncContractDigest(), membership, parent, f.cfg.Sync.AdministratorKey)
						if err != nil {
							t.Fatal(err)
						}
						checkpoint, err := syncrecords.CanonicalCheckpoint(plan.ProposedCheckpoint)
						if err != nil {
							t.Fatal(err)
						}
						proposal, err := syncrecords.CanonicalCheckpointPlan(plan)
						if err != nil {
							t.Fatal(err)
						}
						tree, err := client.SnapshotTree(context.Background(), parent, map[string][]byte{".agent-dispatch-sync/checkpoints/" + plan.ProposedCheckpoint.CheckpointID + ".json": checkpoint, ".agent-dispatch-sync/checkpoint-plans/" + plan.PlanID + ".json": proposal})
						if err != nil {
							t.Fatal(err)
						}
						commit, err := client.CreateSignedContentCommit(context.Background(), tree, f.adminKey, parent, time.Now(), "administrator", "Reviewed fixture checkpoint")
						if err != nil {
							t.Fatal(err)
						}
						return commit
					}
					target = checkpointCommit(base, "initial_baseline")
					if kind == "checkpoint_unsigned" {
						if err := client.VerifySSHSignature(context.Background(), target, f.cfg.Sync.AdministratorKey); err != nil {
							t.Fatal(err)
						}
						tree := gitTestOutput(t, f.git, f.repo, "rev-parse", target+"^{tree}")
						// Keep the valid records and predecessor; remove only the Git signature.
						target = gitTestOutput(t, f.git, f.repo, "-c", "commit.gpgsign=false", "commit-tree", tree, "-p", base, "-m", "Unsigned fixture checkpoint")
						if err := client.VerifySSHSignature(context.Background(), target, f.cfg.Sync.AdministratorKey); err == nil {
							t.Fatal("unsigned fixture retained a signature")
						}
					} else if kind == "checkpoint_ancestor" {
						adoptedHead = checkpointCommit(target, "conflict_resolution")
					}
				} else {
					checkpoint := filepath.Join(f.repo, ".agent-dispatch-sync", "checkpoints", "operator-only.json")
					if err := os.MkdirAll(filepath.Dir(checkpoint), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(checkpoint, []byte("{}\n"), 0600); err != nil {
						t.Fatal(err)
					}
					gitTestRun(t, f.git, f.repo, "add", ".agent-dispatch-sync")
					gitTestRun(t, f.git, f.repo, "commit", "-q", "-m", "operator checkpoint fixture")
					target = gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD")
					gitTestRun(t, f.git, f.repo, "reset", "--hard", base)
				}
			}
			if adoptedHead == "" {
				adoptedHead = target
			}
			binGit := filepath.Join(filepath.Dir(f.remoteHead), "bin", "git")
			raw, err := os.ReadFile(binGit)
			if err != nil {
				t.Fatal(err)
			}
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
			tracking := fmt.Sprintf("#!/bin/sh\ncase \" $* \" in\n *\" fetch \"*)\n for last do :; done\n case \"$last\" in *membership*) source=$(cat %s);; *) source=%s;; esac\n destination=${last#*:}\n exec %s -C %s update-ref \"$destination\" \"$source\";;\n *\" ls-remote \"*%s*) printf '%%s\\t%%s\\n' %s %s; exit 0;;\nesac\n", quote(f.remoteHead), quote(target), quote(f.git), quote(f.repo), quote(f.cfg.Sync.ContentRef), quote(target), quote(f.cfg.Sync.ContentRef))
			if err := os.WriteFile(binGit, []byte(tracking+strings.TrimPrefix(string(raw), "#!/bin/sh\n")), 0700); err != nil {
				t.Fatal(err)
			}
			code, value := e23t4Request(t, f.path, "prepare", "--group", f.cfg.Sync.GroupID, "--action", "sync-now")
			if code != 0 {
				t.Fatal(value)
			}
			id := value["result"].(map[string]any)["request_id"].(string)
			if code, value = e23t4Request(t, f.path, "submit", "--group", f.cfg.Sync.GroupID, "--request-id", id); code != 0 {
				t.Fatal(value)
			}
			service := &peerService{cfg: f.cfg, configPath: f.path, store: f.store}
			service.dispatchManagement(context.Background())
			request, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
			if err != nil || request.State != "blocked" || request.Outcome == nil || request.Outcome.Status != "blocked" || len(request.Jobs) != 0 {
				t.Fatalf("operator-only adoption:%+v %v", request, err)
			}
			if request.Outcome.Reason != holdKind+"_adoption_requires_operator" {
				t.Fatalf("operator-only reason lost: %+v", request.Outcome)
			}
			if holdKind == "checkpoint" && (request.Outcome.TargetCommit == nil || *request.Outcome.TargetCommit != target) {
				t.Fatal("known checkpoint target lost")
			}
			assertHeld := func(contentHead, membershipHead string) {
				t.Helper()
				held, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
				if err != nil || held.State != "blocked" || held.Revision != request.Revision || held.Attempts != request.Attempts || held.Fence != request.Fence || held.Fingerprint != request.Fingerprint || held.AcceptedAt == nil || *held.AcceptedAt != *request.AcceptedAt || len(held.Jobs) != 0 || !reflect.DeepEqual(held.Outcome, request.Outcome) {
					t.Fatalf("standing operator hold changed:%+v %v", held, err)
				}
				if gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef) != contentHead || gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.MembershipRef) != membershipHead {
					t.Fatal("management recovery changed adopted refs")
				}
			}
			for pass := range 3 {
				if pass == 1 {
					f.cfg.Sync.Management.Enabled = false
					f.writeConfig(t)
				}
				service.recoverManagement(context.Background())
				service.dispatchManagement(context.Background())
				assertHeld(base, membership)
				f.cfg.Sync.Management.Enabled = true
				f.writeConfig(t)
			}
			if got := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.MembershipRef); got != membership {
				t.Fatalf("management adopted membership:%s", got)
			}
			if got := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef); got != base {
				t.Fatalf("management adopted checkpoint:%s", got)
			}
			if kind == "checkpoint_unsigned" {
				gitTestRun(t, f.git, f.repo, "reset", "--hard", target)
				service.recoverManagement(context.Background())
				assertHeld(target, membership)
			}
			if kind == "membership_normal" || kind == "checkpoint_signed" || kind == "checkpoint_ancestor" {
				// Fixture setup represents an already completed, explicit operator
				// adoption. Management recovery may inspect it, never perform it.
				if kind == "membership_normal" {
					gitTestRun(t, f.git, f.repo, "update-ref", f.cfg.Sync.MembershipRef, strings.TrimSpace(string(mustRead(t, f.remoteHead))))
				} else {
					gitTestRun(t, f.git, f.repo, "reset", "--hard", adoptedHead)
				}
				currentMembership := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.MembershipRef)
				guard, err := resourceguard.Acquire(stateDirOf(f.cfg), f.cfg.Sync.Resource)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { guard.Close() })
				service.recoverManagement(context.Background())
				assertHeld(adoptedHead, currentMembership)
				if err := guard.Close(); err != nil {
					t.Fatal(err)
				}
				service.recoverManagement(context.Background())
				queued, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
				if err != nil || queued.State != "queued" || queued.Revision != request.Revision+1 || queued.Attempts != request.Attempts || queued.Fence != request.Fence || queued.Fingerprint != request.Fingerprint || queued.AcceptedAt == nil || *queued.AcceptedAt != *request.AcceptedAt || queued.Outcome != nil || len(queued.Jobs) != 0 {
					t.Fatalf("confirmed operator adoption did not resume the exact request:%+v %v", queued, err)
				}
			}

		})
	}
}

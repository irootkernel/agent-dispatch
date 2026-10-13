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
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/resourceguard"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncmanagement"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func TestE23T4EffectBoundaryRejectsStaleDurableClaim(t *testing.T) {
	f := newResumeFixture(t)
	f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now"}}
	f.writeConfig(t)
	revision, _ := config.SyncRevision(f.cfg)
	if _, err := f.store.SetSyncControl(context.Background(), f.cfg.Sync.GroupID, 1, "active", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
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
	binding, _ := managementBindingForConfig(f.cfg)
	claimed, err := f.store.ClaimManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, "original-worker", binding, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e := &managementExecution{request: claimed, service: &peerService{cfg: f.cfg, configPath: f.path, store: f.store}}
	ctx := context.WithValue(context.Background(), managementExecutionKey{}, e)
	if err := managementEffectBoundary(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.TransitionManagementRequest(context.Background(), claimed, "uncertain", &syncmanagement.Outcome{Status: "uncertain", Reason: "separate exact claim inspection"}, "management-execution-observation", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := managementEffectBoundary(ctx); !errors.Is(err, sqlite.ErrSyncPrecondition) {
		t.Fatalf("stale in-memory owner retained effect authority:%v", err)
	}
}

func TestE23T4ManagementCannotAdoptMembershipOrCheckpoint(t *testing.T) {
	for _, kind := range []string{"membership", "checkpoint"} {
		t.Run(kind, func(t *testing.T) {
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
			if kind == "membership" {
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
				plan, err := syncrecords.NewPlan("emergency_revocation", f.cfg.Sync.Nodes[1].InstanceID, &current, &membership, configuredMembers(f.cfg.Sync), membershipBinding(f.cfg, f.cfg.Sync))
				if err != nil {
					t.Fatal(err)
				}
				f.signMembership(t, client, plan, membership)
				gitTestRun(t, f.git, f.repo, "update-ref", f.cfg.Sync.MembershipRef, membership)
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
			if request.Outcome.Reason != kind+"_adoption_requires_operator" {
				t.Fatalf("operator-only reason lost: %+v", request.Outcome)
			}
			if kind == "checkpoint" && (request.Outcome.TargetCommit == nil || *request.Outcome.TargetCommit != target) {
				t.Fatal("known checkpoint target lost")
			}
			if got := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.MembershipRef); got != membership {
				t.Fatalf("management adopted membership:%s", got)
			}
			if got := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef); got != base {
				t.Fatalf("management adopted checkpoint:%s", got)
			}
		})
	}
}

func init() {
	if len(os.Args) == 6 && os.Args[1] == "e23-management-submit-helper" {
		var out, errOut bytes.Buffer
		code := runSyncRequestQualified(context.Background(), []string{"submit", "--config", os.Args[2], "--group", os.Args[3], "--request-id", os.Args[4]}, false, []string{"sync-now"}, &out, &errOut)
		if code != 0 {
			os.Stderr.Write(errOut.Bytes())
			os.Exit(code)
		}
		if os.WriteFile(os.Args[5], out.Bytes(), 0600) != nil {
			os.Exit(2)
		}
		time.Sleep(time.Hour)
		os.Exit(0)
	}
}

func TestE23T4AcceptedWorkSurvivesSubmitterDeath(t *testing.T) {
	f := newResumeFixture(t)
	f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now"}}
	f.writeConfig(t)
	revision, _ := config.SyncRevision(f.cfg)
	if _, err := f.store.SetSyncControl(context.Background(), f.cfg.Sync.GroupID, 1, "active", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	code, value := e23t4Request(t, f.path, "prepare", "--group", f.cfg.Sync.GroupID, "--action", "sync-now")
	if code != 0 {
		t.Fatal(value)
	}
	id := value["result"].(map[string]any)["request_id"].(string)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(t.TempDir(), "accepted.json")
	cmd := exec.Command(executable, "e23-management-submit-helper", f.path, f.cfg.Sync.GroupID, id, receipt)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	until := time.Now().Add(8 * time.Second)
	for time.Now().Before(until) {
		if _, err := os.Stat(receipt); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(receipt); err != nil {
		t.Fatal("submitter did not publish its acceptance receipt")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("submitter was not killed after acceptance")
	}
	request, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
	if err != nil || request.State != "queued" || request.AcceptedAt == nil || request.Attempts != 0 {
		t.Fatalf("durable acceptance:%+v %v", request, err)
	}
	service := &peerService{cfg: f.cfg, configPath: f.path, store: f.store}
	service.dispatchManagement(context.Background())
	request, err = f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
	if err != nil || request.State != "blocked" || request.Attempts != 1 || request.Outcome == nil || request.Outcome.Reason != "git_unstable" {
		t.Fatalf("independent worker did not execute durable work:%+v %v", request, err)
	}
}

func TestE23T4ManagementClaimRenewsAndCancelsGitGroup(t *testing.T) {
	f := newResumeFixture(t)
	f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now"}}
	f.writeConfig(t)
	revision, _ := config.SyncRevision(f.cfg)
	if _, err := f.store.SetSyncControl(context.Background(), f.cfg.Sync.GroupID, 1, "active", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
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
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "git.pid")
	// Exercise the interval between marker creation and PID publication.
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	binGit := filepath.Join(filepath.Dir(f.remoteHead), "bin", "git")
	raw, err := os.ReadFile(binGit)
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	blockingFetch := fmt.Sprintf("#!/bin/sh\ncase \" $* \" in *\" fetch \"*) exec %s sync-shutdown-git-helper %s;; esac\n", quote(executable), quote(marker))
	if err := os.WriteFile(binGit, []byte(blockingFetch+strings.TrimPrefix(string(raw), "#!/bin/sh\n")), 0700); err != nil {
		t.Fatal(err)
	}
	service := &peerService{cfg: f.cfg, configPath: f.path, store: f.store, wake: make(chan struct{}, 1), reconcile: func(context.Context) (bool, string) { return true, strings.Repeat("a", 40) }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { service.inboxLoop(ctx); close(done) }()
	until := time.Now().Add(5 * time.Second)
	var pid int
	for time.Now().Before(until) {
		if raw, err := os.ReadFile(marker); err == nil {
			pid, _ = strconv.Atoi(string(raw))
			if pid > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid <= 0 {
		t.Fatal("management Git child did not start")
	}
	if group, err := syscall.Getpgid(pid); err != nil || group != pid {
		t.Fatalf("management Git group=%d pid=%d err=%v", group, pid, err)
	}
	first, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
	if err != nil || first.State != "running" {
		t.Fatalf("initial claim:%+v %v", first, err)
	}
	renewed := false
	until = time.Now().Add(12 * time.Second)
	for time.Now().Before(until) {
		current, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if current.Revision > first.Revision && current.ClaimExpiresAt > first.ClaimExpiresAt {
			if current.ClaimOwner != first.ClaimOwner || current.Fence != first.Fence || current.ExecutionDeadline != first.ExecutionDeadline || current.Attempts != 1 {
				t.Fatalf("renewal changed execution identity:%+v -> %+v", first, current)
			}
			renewed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !renewed {
		t.Fatal("live management claim did not renew")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("management worker did not join its cancelled Git process")
	}
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("management Git process still alive:%v", err)
	}
	request, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
	if err != nil || request.State != "uncertain" || request.Outcome == nil || request.Outcome.Status != "uncertain" {
		t.Fatalf("cancelled execution claimed settlement:%+v %v", request, err)
	}
	binding, _ := managementBindingForConfig(f.cfg)
	availability, err := f.store.ManagementExecutorAvailability(context.Background(), binding, time.Now())
	if err != nil || availability != "stopped" {
		t.Fatalf("shutdown evidence:%s %v", availability, err)
	}
}

func e23t4Request(t *testing.T, path string, args ...string) (int, map[string]any) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runSyncRequestQualified(context.Background(), append(args, "--config", path), false, []string{"pause", "resume", "sync-now", "verify-all"}, &out, &errOut)
	raw := out.Bytes()
	if code != 0 {
		raw = errOut.Bytes()
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("request code%d: %s %v", code, raw, err)
	}
	e23t3ValidateCarrier(t, value)
	return code, value
}

func TestE23T4LocalControlsShareOperatorResumeSafety(t *testing.T) {
	for _, name := range []string{"healthy", "held", "emergency", "busy", "stale", "trust", "transport", "storage", "merge", "index_lock", "foreign_branch", "detached_head", "writer"} {
		t.Run(name, func(t *testing.T) {
			f := newResumeFixture(t)
			f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"pause", "resume"}}
			f.writeConfig(t)
			revision, _ := config.SyncRevision(f.cfg)
			paused, err := f.store.SetSyncControl(context.Background(), f.cfg.Sync.GroupID, 1, "paused", revision, time.Now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				t.Fatal(err)
			}
			code, value := e23t4Request(t, f.path, "prepare", "--group", f.cfg.Sync.GroupID, "--action", "resume")
			if code != 0 {
				t.Fatalf("prepare%d %v", code, value)
			}
			id := value["result"].(map[string]any)["request_id"].(string)
			switch name {
			case "held":
				_, err = f.store.HoldSyncControl(context.Background(), f.cfg.Sync.GroupID, "trust_failure", revision, time.Now().UTC().Format(time.RFC3339Nano))
			case "emergency":
				_, err = f.store.Exec(`UPDATE sync_controls SET membership_mode='blocked_emergency' WHERE group_id=?`, f.cfg.Sync.GroupID)
			case "busy":
				guard, guardErr := resourceguard.Acquire(stateDirOf(f.cfg), f.cfg.Sync.Resource)
				err = guardErr
				if err == nil {
					defer guard.Close()
				}
			case "stale":
				_, err = f.store.SetSyncControl(context.Background(), f.cfg.Sync.GroupID, paused.Revision, "active", revision, time.Now().UTC().Format(time.RFC3339Nano))
			case "trust":
				err = os.WriteFile(f.remoteHead, []byte(strings.Repeat("a", 40)), 0600)
			case "transport":
				err = os.WriteFile(f.transportFailure, []byte("unavailable"), 0600)
			case "storage":
				_, err = f.store.Exec(`DROP TABLE destination_lane_state`)
			case "merge":
				head := gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD")
				err = os.WriteFile(filepath.Join(f.repo, ".git", "MERGE_HEAD"), []byte(head+"\n"), 0600)
			case "index_lock":
				err = os.WriteFile(filepath.Join(f.repo, ".git", "index.lock"), nil, 0600)
			case "foreign_branch":
				gitTestRun(t, f.git, f.repo, "checkout", "-q", "-b", "foreign")
			case "detached_head":
				gitTestRun(t, f.git, f.repo, "checkout", "-q", "--detach")
			case "writer":
				resource := f.cfg.Resources[f.cfg.Sync.Resource]
				if err := f.store.RegisterResource(nil, f.cfg.Sync.Resource, "resource-resume-writer", resource.Root, resource.Root, resource.FileScope, resource.Git.Mode); err != nil {
					t.Fatal(err)
				}
				found := false
				for routeID, route := range f.cfg.Routes {
					if route.Source.Resource != f.cfg.Sync.Resource {
						continue
					}
					routeRevision, _ := config.RouteRevision(f.cfg, routeID)
					if err := f.store.RegisterRoute(nil, routeID, routeRevision, config.PolicyRevision(route), f.cfg.Sync.Resource, route.Destinations[0].Target, "{}", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
						t.Fatal(err)
					}
					if err := f.store.InitializeRouteState(nil, routeID); err != nil {
						t.Fatal(err)
					}
					seedE21T3Eligibility(t, f.cfg, f.path, []byte("base\n"))
					_, err = f.store.Exec(`UPDATE route_runtime_state SET active_dispatch_id=? WHERE route_id=?`, "dispatch-"+routeID, routeID)
					found = true
					break
				}
				if !found {
					t.Fatal("fixture has no maintained resource writer")
				}
				if idle, inspectErr := f.store.ResourceWritersIdle(context.Background(), f.cfg.Sync.Resource); inspectErr != nil || idle {
					t.Fatalf("writer fixture is not active:%v %v", idle, inspectErr)
				}
				// The receipt fixture rewrites unchanged bytes; settle its stat cache
				// before checking that resume itself leaves the index unchanged.
				gitTestRun(t, f.git, f.repo, "update-index", "--refresh")
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := f.store.LoadSyncControl(context.Background(), f.cfg.Sync.GroupID)
			if err != nil {
				t.Fatal(err)
			}
			head := gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD")
			index := mustRead(t, filepath.Join(f.repo, ".git", "index"))
			code, value = e23t4Request(t, f.path, "submit", "--group", f.cfg.Sync.GroupID, "--request-id", id)
			after, err := f.store.LoadSyncControl(context.Background(), f.cfg.Sync.GroupID)
			if err != nil {
				t.Fatal(err)
			}
			if head != gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD") || !bytes.Equal(index, mustRead(t, filepath.Join(f.repo, ".git", "index"))) {
				t.Fatal("resume changed HEAD or the Git index")
			}
			request, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if name == "healthy" {
				if code != 0 || after.State != "active" || after.Revision != before.Revision+1 || request.AcceptedAt == nil || request.Outcome == nil || request.Outcome.Status != "intent_committed" || *request.Outcome.Quiescence != "unknown" {
					t.Fatalf("healthy:%d %v %+v %+v", code, value, after, request)
				}
			} else {
				wantCode, wantError := 14, "sync_request_conflict"
				if name == "trust" {
					wantCode, wantError = 30, "sync_trust_failed"
				} else if name == "transport" {
					wantCode, wantError = 10, "sync_request_busy"
				} else if name == "storage" {
					wantCode, wantError = 20, "sqlite_write_failed"
				}
				if code != wantCode {
					t.Fatalf("resume refusal code:%d want%d envelope%v", code, wantCode, value)
				}
				wantMessage := "resume requires idle Git state and resource writers; resolve the local guard and repeat this handle"
				switch name {
				case "held", "stale":
					wantMessage = fmt.Sprintf("control revision is %d, expected %d; inspect sync status, complete any required operator recovery, then prepare a new request", before.Revision, paused.Revision)
				case "emergency":
					wantMessage = "membership emergency remains; inspect sync status and adopt a reviewed normal membership replacement through the operator CLI before preparing another request"
				case "busy":
					wantMessage = "resource is busy; retain the same handle"
				case "trust":
					wantMessage = "resume trust checks failed; review signed membership and the approved remote before repeating this handle"
				case "transport":
					wantMessage = "resume could not inspect the approved remote; restore connectivity and repeat this handle"
				case "storage":
					wantMessage = "resume could not inspect local storage; repair the store and repeat this handle"
				}
				message := value["error"].(map[string]any)["message"].(string)
				if message != wantMessage || strings.Contains(message, f.repo) || strings.Contains(message, f.path) || len([]byte(message)) > 256 {
					t.Fatalf("resume diagnostic lost its closed safe action:%q want%q", message, wantMessage)
				}
				if code != wantCode || value["error"].(map[string]any)["code"] != wantError || after != before || request.AcceptedAt != nil || request.State != "prepared" {
					t.Fatalf("safety:%d %v %+v -> %+v request%+v", code, value, before, after, request)
				}
			}
		})
	}
}

func TestE23T4WorkerVerificationHasExactResultAndOwnMetadataExemption(t *testing.T) {
	for _, name := range []string{"healthy", "paused_dispatch", "pending", "pending_verification", "dirty", "stale", "uncertain", "moving", "capacity", "association_capacity", "pause_admission"} {
		t.Run(name, func(t *testing.T) {
			f := newResumeFixture(t)
			f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"verify-all", "sync-now"}}
			if name == "paused_dispatch" {
				f.cfg.Sync.Management.Actions = append(f.cfg.Sync.Management.Actions, "pause", "resume")
			}
			if name == "capacity" {
				f.cfg.Sync.Bounds.Queue = 2
			}
			f.writeConfig(t)
			if name == "capacity" {
				revision, _ := config.SyncRevision(f.cfg)
				if _, err := f.store.SetSyncControl(context.Background(), f.cfg.Sync.GroupID, 1, "active", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
					t.Fatal(err)
				}
			}
			target := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef)
			binGit := filepath.Join(filepath.Dir(f.remoteHead), "bin", "git")
			raw, err := os.ReadFile(binGit)
			if err != nil {
				t.Fatal(err)
			}
			condition := fmt.Sprintf("    test \"$last\" = '%s' || exit 2", f.cfg.Sync.MembershipRef)
			// The fixture's remote branch answers only the two configured refs.
			contentReply := fmt.Sprintf("    if test \"$last\" = '%s'; then printf '%%s\\t%%s\\n' '%s' \"$last\"; exit 0; fi\n", f.cfg.Sync.ContentRef, target)
			if !strings.Contains(string(raw), condition) {
				t.Fatal("remote fixture shape changed")
			}
			if err := os.WriteFile(binGit, []byte(strings.Replace(string(raw), condition, contentReply+condition, 1)), 0700); err != nil {
				t.Fatal(err)
			}
			code, value := e23t4Request(t, f.path, "prepare", "--group", f.cfg.Sync.GroupID, "--action", "verify-all")
			if code != 0 {
				t.Fatalf("prepare%d %v", code, value)
			}
			id := value["result"].(map[string]any)["request_id"].(string)
			code, value = e23t4Request(t, f.path, "submit", "--group", f.cfg.Sync.GroupID, "--request-id", id)
			if code != 0 {
				t.Fatalf("submit%d %v", code, value)
			}
			if name == "pending" {
				code, other := e23t4Request(t, f.path, "prepare", "--group", f.cfg.Sync.GroupID, "--action", "sync-now")
				if code != 0 {
					t.Fatal(other)
				}
				code, other = e23t4Request(t, f.path, "submit", "--group", f.cfg.Sync.GroupID, "--request-id", other["result"].(map[string]any)["request_id"].(string))
				if code != 0 {
					t.Fatal(other)
				}
			}
			if name == "pending_verification" {
				otherID := e23t5Accepted(t, f.cfg, f.path, "verify-all")
				binding, err := managementBindingForConfig(f.cfg)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.ClaimManagementRequest(context.Background(), f.cfg.Sync.GroupID, otherID, "other-verifier", binding, true, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			if name == "dirty" {
				if err := os.WriteFile(filepath.Join(f.repo, "note.md"), []byte("dirty\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if name == "capacity" {
				for i := range 2 {
					jobID := fmt.Sprintf("existing-verification-%d", i)
					if _, _, err := f.store.AdmitSyncJob(context.Background(), sqlite.SyncJobInput{JobID: jobID, GroupID: f.cfg.Sync.GroupID, Kind: "verification", LogicalKey: jobID, InitialState: "planned", PayloadJSON: `{}`, QueueLimit: 2, Now: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if name == "association_capacity" {
				if _, err := f.store.Exec(`CREATE TRIGGER saturate_associations AFTER UPDATE OF state ON sync_management_requests WHEN NEW.state='running' BEGIN UPDATE sync_management_requests SET association_reserved=1000 WHERE request_id=NEW.request_id; END`); err != nil {
					t.Fatal(err)
				}
			}
			if name == "pause_admission" {
				if _, err := f.store.Exec(`CREATE TRIGGER pause_at_verification_admission AFTER INSERT ON sync_jobs WHEN NEW.kind='verification' BEGIN UPDATE sync_controls SET state='paused',reason='operator_pause',revision=revision+1 WHERE group_id=NEW.group_id; END`); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("SYNC_NODE_A_TO_B", "outbound-secret")
			transport := e22t3RoundTrip(func(r *http.Request) (*http.Response, error) {
				if name == "capacity" || name == "association_capacity" || name == "pause_admission" {
					t.Fatal("pre-observation hold started a peer observation")
				}
				var request syncrecords.StatusRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				response := syncrecords.StatusResponse{SchemaVersion: syncrecords.StatusResponseSchema, GroupID: request.GroupID, Responder: request.Receiver, StateIncarnationID: f.cfg.Sync.Nodes[1].StateIncarnationID, MembershipRevision: request.MembershipRevision, ContentRef: request.ContentRef, TargetCommit: request.TargetCommit, ScopeDigest: request.ScopeDigest, ContractDigest: request.ContractDigest, Nonce: request.Nonce, EvidenceGeneration: time.Now().UnixNano(), State: "applied", MembershipCurrent: true}
				if name == "stale" {
					response.EvidenceGeneration = time.Now().Add(-301 * time.Second).UnixNano()
				}
				if name == "uncertain" {
					response.Uncertain = true
				}
				if name == "moving" {
					gitTestRun(t, f.git, f.repo, "update-ref", f.cfg.Sync.ContentRef, gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.MembershipRef))
				}
				raw, _ := json.Marshal(response)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: http.Header{}}, nil
			})
			service := &peerService{cfg: f.cfg, configPath: f.path, store: f.store}
			if name == "paused_dispatch" {
				e23t5Accepted(t, f.cfg, f.path, "pause")
				service.dispatchManagementWithHTTP(context.Background(), &http.Client{Transport: e22t3RoundTrip(func(*http.Request) (*http.Response, error) {
					t.Fatal("paused dispatch started a peer observation")
					return nil, nil
				})})
				held, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
				if err != nil || held.State != "blocked" || held.Attempts != 0 || len(held.Jobs) != 0 {
					t.Fatalf("paused request admitted effects:%+v %v", held, err)
				}
				e23t5Accepted(t, f.cfg, f.path, "resume")
				service.recoverManagement(context.Background())
			}
			service.dispatchManagementWithHTTP(context.Background(), &http.Client{Transport: transport})
			request, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if name == "pause_admission" {
				if request.State != "blocked" || request.Outcome == nil || request.Outcome.Status != "blocked" || request.Outcome.Reason != "local control held before verification observations" || request.Outcome.TargetCommit == nil || *request.Outcome.TargetCommit != target || len(request.Jobs) != 1 || request.Jobs[0].Kind != "verification" || request.Jobs[0].State != "planned" || request.Jobs[0].TargetCommit == nil || *request.Jobs[0].TargetCommit != target || request.Jobs[0].AssociationReason != "verification_target" || request.AssociationReserved != 0 {
					t.Fatalf("pause lost exact pre-observation job: %+v", request)
				}
				var jobs int
				if err := f.store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='verification'`).Scan(&jobs); err != nil || jobs != 1 {
					t.Fatalf("pause duplicated verification: %d %v", jobs, err)
				}
				control, err := f.store.LoadSyncControl(context.Background(), f.cfg.Sync.GroupID)
				if err != nil || control.State != "paused" {
					t.Fatalf("verification cleared pause: %+v %v", control, err)
				}
				return
			}
			if name == "capacity" || name == "association_capacity" {
				if request.State != "blocked" || request.Outcome == nil || request.Outcome.Status != "blocked" || !strings.Contains(request.Outcome.Reason, "capacity exhausted") || request.Outcome.VerificationID != nil || len(request.Jobs) != 0 || request.AssociationReserved != 0 {
					t.Fatalf("known hold must remain blocked:%+v", request)
				}
				var jobs int
				if err := f.store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='verification'`).Scan(&jobs); err != nil {
					t.Fatal(err)
				}
				wantJobs := 0
				if name == "capacity" {
					wantJobs = 2
				}
				if jobs != wantJobs {
					t.Fatalf("capacity admission created a verification job: %d want%d", jobs, wantJobs)
				}
				return
			}
			want := "incomplete"
			if name == "healthy" || name == "paused_dispatch" {
				want = "complete"
			}
			if name == "moving" {
				want = "target_changed"
			}
			if request.State != "finished" || request.Outcome == nil || request.Outcome.Status != want || request.Outcome.TargetCommit == nil || *request.Outcome.TargetCommit != target || request.Outcome.VerificationID == nil || len(request.Jobs) != 1 || request.Jobs[0].JobID != *request.Outcome.VerificationID || request.Jobs[0].State != want || request.Jobs[0].Kind != "verification" {
				t.Fatalf("exact verification:%+v", request)
			}
			if name == "paused_dispatch" && (request.RequestID != id || request.Attempts != 1 || request.Jobs[0].Attempts != 1) {
				t.Fatalf("resumed verification changed identity or budget:%+v", request)
			}
			code, value = e23t4Request(t, f.path, "show", "--group", f.cfg.Sync.GroupID, "--request-id", id)
			if code != 0 {
				t.Fatalf("show%d %v", code, value)
			}
		})
	}
}

func TestE23T4ExistingWorkerFairnessAndStoppedEvidence(t *testing.T) {
	f := newResumeFixture(t)
	f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now", "pause", "resume"}}
	f.cfg.Sync.Bounds.Queue = 2
	f.writeConfig(t)
	revision, _ := config.SyncRevision(f.cfg)
	if _, err := f.store.SetSyncControl(context.Background(), f.cfg.Sync.GroupID, 1, "active", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for range 2 {
		code, value := e23t4Request(t, f.path, "prepare", "--group", f.cfg.Sync.GroupID, "--action", "sync-now")
		if code != 0 {
			t.Fatal(value)
		}
		id := value["result"].(map[string]any)["request_id"].(string)
		ids = append(ids, id)
		code, value = e23t4Request(t, f.path, "submit", "--group", f.cfg.Sync.GroupID, "--request-id", id)
		if code != 0 {
			t.Fatal(value)
		}
	}
	target := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef)
	nudge := syncrecords.Nudge{SchemaVersion: syncrecords.NudgeSchema, GroupID: f.cfg.Sync.GroupID, PublicationID: "fairness-nudge", Sender: f.cfg.Sync.Nodes[1].InstanceID, Receiver: f.cfg.Sync.LocalInstanceID, MembershipRevision: gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.MembershipRef), ContentRef: f.cfg.Sync.ContentRef, TargetCommit: target}
	raw, err := syncrecords.CanonicalNudge(nudge)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AdmitPeerNudge(context.Background(), sqlite.PeerNudgeInput{GroupID: nudge.GroupID, PublicationID: nudge.PublicationID, Fingerprint: fmt.Sprintf("sha256:%x", sha256.Sum256(raw)), PayloadJSON: string(raw), ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano), QueueLimit: 2}); err != nil {
		t.Fatal(err)
	}
	periodic := make(chan struct{}, 2)
	service := &peerService{cfg: f.cfg, configPath: f.path, store: f.store, wake: make(chan struct{}, 1), reconcile: func(context.Context) (bool, string) {
		periodic <- struct{}{}
		return true, target
	}}
	binding, err := managementBindingForConfig(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { service.inboxLoop(ctx); close(done) }()
	awaitPass := func() {
		t.Helper()
		select {
		case <-periodic:
		case <-time.After(3 * time.Second):
			t.Fatal("periodic recovery was starved")
		}
	}
	awaitDispatch := func(index int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			request, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, ids[index], time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if request.State == "blocked" && request.Outcome != nil && request.Outcome.Reason == "git_unstable" {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("one management dispatch did not settle its observed failure")
	}
	awaitPass()
	awaitDispatch(0)
	var resolution string
	if err := f.store.QueryRow(`SELECT resolution FROM sync_peer_nudges WHERE publication_id=? AND processed_at IS NOT NULL`, nudge.PublicationID).Scan(&resolution); err != nil || resolution != "covered" {
		t.Fatalf("queued management starved peer recovery: %s %v", resolution, err)
	}
	second, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, ids[1], time.Now())
	if err != nil || second.State != "queued" {
		t.Fatalf("startup dispatched more than one request:%+v %v", second, err)
	}
	availability, err := f.store.ManagementExecutorAvailability(context.Background(), binding, time.Now())
	if err != nil || availability != "available" {
		t.Fatalf("live worker:%s %v", availability, err)
	}
	for _, action := range []string{"pause", "resume"} {
		code, value := e23t4Request(t, f.path, "prepare", "--group", f.cfg.Sync.GroupID, "--action", action)
		if code != 0 {
			t.Fatalf("reserved control prepare:%d %v", code, value)
		}
		code, value = e23t4Request(t, f.path, "submit", "--group", f.cfg.Sync.GroupID, "--request-id", value["result"].(map[string]any)["request_id"].(string))
		if code != 0 {
			t.Fatalf("reserved control submit:%d %v", code, value)
		}
		if action == "pause" {
			request, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, ids[1], time.Now())
			if err != nil || request.State != "queued" {
				t.Fatalf("pause changed pending request:%+v %v", request, err)
			}
			availability, err := f.store.ManagementExecutorAvailability(context.Background(), binding, time.Now())
			if err != nil || availability != "available" {
				t.Fatalf("paused worker:%s %v", availability, err)
			}
		}
	}
	service.wake <- struct{}{}
	awaitPass()
	awaitDispatch(1)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker shutdown exceeded its cancellation boundary")
	}
	availability, err = f.store.ManagementExecutorAvailability(context.Background(), binding, time.Now())
	if err != nil || availability != "stopped" {
		t.Fatalf("stopped worker:%s %v", availability, err)
	}
	for _, id := range ids {
		code, value := e23t4Request(t, f.path, "show", "--group", f.cfg.Sync.GroupID, "--request-id", id)
		if code != 0 || value["result"].(map[string]any)["executor"] != "stopped" {
			t.Fatalf("exact stopped lookup:%d %v", code, value)
		}
	}
}

func TestE23T4StaleWorkerBindingDoesNotHoldTheQueuedHead(t *testing.T) {
	f := newResumeFixture(t)
	f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now"}}
	f.writeConfig(t)
	startup, err := config.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.Sync.Management.Actions = append(f.cfg.Sync.Management.Actions, "verify-all")
	f.writeConfig(t)
	first := e23t5Accepted(t, f.cfg, f.path, "sync-now")
	second := e23t5Accepted(t, f.cfg, f.path, "sync-now")
	before, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, first, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	service := &peerService{cfg: startup, configPath: f.path, store: f.store}
	service.dispatchManagement(context.Background())
	held, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, first, time.Now())
	if err != nil || held.State != "blocked" || held.Outcome == nil || held.Outcome.Status != "blocked" || held.Attempts != 0 || held.Fence != 0 || len(held.Jobs) != 0 || held.Fingerprint != before.Fingerprint || held.AcceptedAt == nil || *held.AcceptedAt != *before.AcceptedAt {
		t.Fatalf("stale worker lost its refusal: %+v %v", held, err)
	}
	next, err := f.store.NextQueuedManagementRequest(context.Background(), f.cfg.Sync.GroupID)
	if err != nil || next != second {
		t.Fatalf("refused head starved later work: %s %v", next, err)
	}
	service.cfg = f.cfg
	service.dispatchManagement(context.Background())
	progressed, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, second, time.Now())
	if err != nil || progressed.State == "queued" || progressed.Attempts != 1 {
		t.Fatalf("current worker did not dispatch later work: %+v %v", progressed, err)
	}
}

// Continue the existing signed publication fixture after its original recovery
// assertions. The management path must reuse the signed identity without keys.
func assertE23T4ManagedSignedRecovery(t *testing.T, cfg *config.Config, path string, store *sqlite.Store, git, repo, jobID, base, target string) {
	t.Helper()
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	originalManagement := cfg.Sync.Management
	defer func() {
		cfg.Sync.Management = originalManagement
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
	}()
	for _, key := range []string{"E21T3_ADMIN", "E21T3_PUBLISHER"} {
		old, found := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if found {
				if err := os.Setenv(key, old); err != nil {
					t.Fatal(err)
				}
			}
		}()
	}
	cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now", "retry"}}
	e23t3SaveConfig(t, cfg, path)
	if _, err := store.Exec(`UPDATE sync_jobs SET state='signed',retain_until_resolved=1,resolved_at=NULL,claim_owner=NULL,claim_expires_at=NULL WHERE job_id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, git, repo, "update-ref", cfg.Sync.ContentRef, base, target)
	code, value := e23t4Request(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "sync-now")
	if code != 0 {
		t.Fatalf("managed prepare%d %v", code, value)
	}
	id := value["result"].(map[string]any)["request_id"].(string)
	code, value = e23t4Request(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", id)
	if code != 0 {
		t.Fatalf("managed submit%d %v", code, value)
	}
	service := &peerService{cfg: cfg, configPath: path, store: store}
	service.dispatchManagement(context.Background())
	request, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, id, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if request.State != "finished" || request.Outcome == nil || request.Outcome.Status != "incomplete" || request.Outcome.TargetCommit == nil || *request.Outcome.TargetCommit != target || request.Attempts != 1 || len(request.Jobs) != 2 {
		t.Fatalf("managed signed recovery:%+v", request)
	}
	seen := map[string]bool{}
	for _, job := range request.Jobs {
		seen[job.Kind] = true
		if job.AssociationReason != "already_signed_recovery" || job.TargetCommit == nil || *job.TargetCommit != target {
			t.Fatalf("recovery attribution:%+v", job)
		}
	}
	if !seen["publication"] || !seen["delivery"] || gitTestOutput(t, git, repo, "rev-parse", cfg.Sync.ContentRef) != target {
		t.Fatalf("signed recovery identities:%+v", request.Jobs)
	}
	assertE23T5SignedRetry(t, cfg, path, store, git, repo, request, jobID, base, target)
}

func assertE23T4ManagedPeerImport(t *testing.T, cfg *config.Config, path, contentState, repo string, peerKey []byte) {
	t.Helper()
	for _, key := range []string{"E21T3_ADMIN", "E21T3_PUBLISHER"} {
		old, found := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if found {
				if err := os.Setenv(key, old); err != nil {
					t.Fatal(err)
				}
			}
		}()
	}
	original := cfg.Sync.Management
	cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now"}}
	defer func() {
		cfg.Sync.Management = original
		e23t3SaveConfig(t, cfg, path)
	}()
	originalQueue := cfg.Sync.Bounds.Queue
	cfg.Sync.Bounds.Queue = 8
	for _, mode := range []string{"capacity-import", "capacity-controller", "capacity-import-prior", "capacity-controller-prior"} {
		assertPublishedNodePeerRoundTrip(t, cfg, path, contentState, repo, peerKey, mode)
	}
	cfg.Sync.Bounds.Queue = originalQueue
	assertPublishedNodePeerRoundTrip(t, cfg, path, contentState, repo, peerKey, "management")
	assertPublishedNodePeerRoundTrip(t, cfg, path, contentState, repo, peerKey, "parallel")
	cfg.Sync.Management.Actions = []string{"sync-now", "retry"}
	// Retain earlier capacity/claim evidence while admitting three retry cohorts.
	cfg.Sync.Bounds.Queue = 16
	for i, phase := range []string{"admission", "claim", "preapply"} {
		t.Run("stored-import-retry-"+phase, func(t *testing.T) {
			t.Setenv("E23T5_IMPORT_LOSS", phase)
			mode := "capacity-import"
			if i == 1 {
				mode = "management"
			}
			assertPublishedNodePeerRoundTrip(t, cfg, path, contentState, repo, peerKey, mode)
		})
	}
	cfg.Sync.Bounds.Queue = originalQueue
}

func assertE23T4DispatchPeerImport(t *testing.T, cfg *config.Config, path string, store *sqlite.Store, target string) {
	t.Helper()
	if phase := os.Getenv("E23T5_IMPORT_LOSS"); phase != "" {
		assertE23T5RetryStoredImport(t, cfg, path, store, target, phase)
		return
	}
	code, value := e23t4Request(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "sync-now")
	if code != 0 {
		t.Fatal(value)
	}
	id := value["result"].(map[string]any)["request_id"].(string)
	if code, value = e23t4Request(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", id); code != 0 {
		t.Fatal(value)
	}
	service := &peerService{cfg: cfg, configPath: path, store: store}
	service.dispatchManagement(context.Background())
	request, err := store.LoadManagementRequest(context.Background(), cfg.Sync.GroupID, id, time.Now())
	if err != nil || request.State != "finished" || request.Outcome == nil || request.Outcome.Status != "already_settled" || request.Outcome.TargetCommit == nil || *request.Outcome.TargetCommit != target || len(request.Jobs) != 1 {
		t.Fatalf("managed peer import:%+v %v", request, err)
	}
	job := request.Jobs[0]
	if job.Kind != "import" || job.State != "applied" || job.AssociationReason != "observed_reconciliation" || job.TargetCommit == nil || *job.TargetCommit != target {
		t.Fatalf("managed import association:%+v", job)
	}
	code, value = e23t4Request(t, path, "show", "--group", cfg.Sync.GroupID, "--request-id", id)
	if code != 0 {
		t.Fatalf("managed import lookup:%d %v", code, value)
	}
}

// Exercise the shared guarded path under actual periodic/management contention.
// The callback below runs real reconciliation; it fabricates no result or effect.
func assertE23T4ParallelPeerImport(t *testing.T, cfg *config.Config, path string, store *sqlite.Store, target string) {
	t.Helper()
	priorGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	entered, release := filepath.Join(dir, "entered"), filepath.Join(dir, "release")
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'" }
	wrapper := fmt.Sprintf(`#!/bin/sh
case " $* " in
 *" fetch "*"%s:refs/agent-dispatch/sync/content/"*)
  if mkdir %s 2>/dev/null; then
   : > %s
   while ! test -e %s; do sleep 0.01; done
  fi;;
esac
exec %s "$@"
`, cfg.Sync.ContentRef, quote(filepath.Join(dir, "claimed")), quote(entered), quote(release), quote(priorGit))
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	prepare := func() string {
		t.Helper()
		code, value := e23t4Request(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "sync-now")
		if code != 0 {
			t.Fatal(value)
		}
		return value["result"].(map[string]any)["request_id"].(string)
	}
	first := prepare()
	if code, value := e23t4Request(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", first); code != 0 {
		t.Fatal(value)
	}
	// Cover the barrier, concurrent public replays and actual import within
	// the declared worker execution budget.
	ctx, cancel := context.WithTimeout(context.Background(), syncmanagement.ExecutionSeconds*time.Second)
	service := &peerService{cfg: cfg, configPath: path, store: store, wake: make(chan struct{}, 1)}
	firstDone := make(chan struct{})
	go func() { defer close(firstDone); service.dispatchManagement(ctx) }()
	loopStarted := false
	loopDone := make(chan struct{})
	defer func() {
		_ = os.WriteFile(release, []byte("release"), 0600)
		cancel()
		select {
		case <-firstDone:
		case <-time.After(8 * time.Second):
			t.Error("management execution did not join")
		}
		if loopStarted {
			select {
			case <-loopDone:
			case <-time.After(8 * time.Second):
				t.Error("existing worker did not join")
			}
		}
	}()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(entered); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("management did not reach content-fetch barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	running, err := store.LoadManagementRequest(ctx, cfg.Sync.GroupID, first, time.Now())
	if err != nil || running.State != "running" || running.Attempts != 1 || running.Fence != 1 || running.ClaimOwner == "" {
		t.Fatalf("barrier claim: %+v %v", running, err)
	}
	second := prepare()
	// Repeated acceptance races with an independent request while execution owns
	// the resource guard. Every duplicate must retain its one original receipt.
	var wg sync.WaitGroup
	failures := make(chan string, 7)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, value := e23t4Request(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", first)
			if code != 0 {
				failures <- fmt.Sprintf("duplicate: %d %v", code, value)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		code, value := e23t4Request(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", second)
		if code != 0 {
			failures <- fmt.Sprintf("second: %d %v", code, value)
		}
	}()
	wg.Wait()
	close(failures)
	for message := range failures {
		t.Error(message)
	}
	passes := make(chan string, 4)
	service.reconcile = func(passCtx context.Context) (bool, string) {
		var out, stderr bytes.Buffer
		code := reconcileConfiguredSync(passCtx, cfg, store, &out, &stderr)
		state, pinned := parseReconcileResult(out.Bytes())
		passes <- state
		if code != 0 {
			return false, ""
		}
		return settledReconcileResult(state, pinned)
	}
	loopStarted = true
	go func() { defer close(loopDone); service.inboxLoop(ctx) }()
	select {
	case state := <-passes:
		if state != "deferred" {
			t.Fatalf("parallel periodic result=%s", state)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("periodic reconciliation was starved")
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		r, err := store.LoadManagementRequest(ctx, cfg.Sync.GroupID, second, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if r.State == "blocked" {
			if r.Attempts != 1 || r.Fence != 1 || r.Outcome == nil || r.Outcome.Status != "blocked" || len(r.Jobs) != 0 {
				t.Fatalf("competing guarded request:%+v", r)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bounded second dispatch did not settle: %+v", r)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(release, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstDone:
	case <-ctx.Done():
		diagnosticCtx, diagnosticCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer diagnosticCancel()
		r, requestErr := store.LoadManagementRequest(diagnosticCtx, cfg.Sync.GroupID, first, time.Now())
		jobs, jobsErr := store.LoadUnresolvedSyncJobs(diagnosticCtx, cfg.Sync.GroupID, "import")
		for _, job := range jobs {
			t.Logf("pending import: id=%s state=%s attempts=%d fence=%d claimed=%t", job.JobID, job.State, job.Attempts, job.Fence, job.ClaimOwner != "")
		}
		t.Fatalf("guarded management import did not finish: context=%v request_state=%s attempts=%d fence=%d request_error=%v jobs_error=%v", ctx.Err(), r.State, r.Attempts, r.Fence, requestErr, jobsErr)
	}
	exact, err := store.LoadManagementRequest(ctx, cfg.Sync.GroupID, first, time.Now())
	if err != nil || exact.State != "finished" || exact.Attempts != 1 || exact.Fence != running.Fence || exact.Outcome == nil || exact.Outcome.Status != "already_settled" || exact.Outcome.TargetCommit == nil || *exact.Outcome.TargetCommit != target || len(exact.Jobs) != 1 {
		t.Fatalf("parallel exact import:%+v %v", exact, err)
	}
	job := exact.Jobs[0]
	if job.Kind != "import" || job.State != "applied" || job.Attempts != 1 || job.AssociationReason != "observed_reconciliation" || job.TargetCommit == nil || *job.TargetCommit != target {
		t.Fatalf("parallel attribution:%+v", job)
	}
	var acceptances, executions, imports int
	if err := store.QueryRow(`SELECT COUNT(*) FROM sync_management_acceptances WHERE request_id=?`, first).Scan(&acceptances); err != nil {
		t.Fatal(err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM sync_management_executions WHERE request_id=?`, first).Scan(&executions); err != nil {
		t.Fatal(err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='import' AND job_id=?`, job.JobID).Scan(&imports); err != nil {
		t.Fatal(err)
	}
	if acceptances != 1 || executions != 1 || imports != 1 {
		t.Fatalf("duplicates created work: acceptances=%d executions=%d imports=%d", acceptances, executions, imports)
	}
	// A subsequent real periodic pass can observe the settled target without
	// rewriting either request's immutable job evidence or executing it again.
	var out, stderr bytes.Buffer
	if code := reconcileConfiguredSync(ctx, cfg, store, &out, &stderr); code != 0 {
		t.Fatalf("periodic settled observation:%d %s %s", code, out.String(), stderr.String())
	}
	state, pinned := parseReconcileResult(out.Bytes())
	if settled, got := settledReconcileResult(state, pinned); !settled || got != target {
		t.Fatalf("periodic target:%s %s", state, pinned)
	}
	retained, err := store.LoadManagementRequest(ctx, cfg.Sync.GroupID, first, time.Now())
	if err != nil || retained.Revision != exact.Revision || len(retained.Jobs) != 1 || retained.Jobs[0].JobID != job.JobID || retained.Attempts != 1 {
		t.Fatalf("periodic observation rewrote attribution:%+v %v", retained, err)
	}
	if code, value := e23t4Request(t, path, "show", "--group", cfg.Sync.GroupID, "--request-id", first); code != 0 {
		t.Fatalf("parallel exact lookup:%d %v", code, value)
	}
}

func assertE23T4ImportCapacityHold(t *testing.T, cfg *config.Config, path string, store *sqlite.Store, base, target string, prior bool) {
	t.Helper()
	ctx := context.Background()
	var pending int
	if err := store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE group_id=? AND resolved_at IS NULL`, cfg.Sync.GroupID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	var jobs []string
	for i := pending; i < cfg.Sync.Bounds.Queue; i++ {
		jobs = append(jobs, randomSyncID("capacity-verification"))
	}
	if len(jobs) == 0 {
		t.Fatal("capacity fixture needs a free slot for its retained prior obligation")
	}
	for _, id := range jobs {
		if _, _, err := store.AdmitSyncJob(ctx, sqlite.SyncJobInput{JobID: id, GroupID: cfg.Sync.GroupID, Kind: "verification", LogicalKey: id, InitialState: "planned", PayloadJSON: `{}`, QueueLimit: cfg.Sync.Bounds.Queue, Now: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	code, value := e23t4Request(t, path, "prepare", "--group", cfg.Sync.GroupID, "--action", "sync-now")
	if code != 0 {
		t.Fatal(value)
	}
	id := value["result"].(map[string]any)["request_id"].(string)
	if code, value = e23t4Request(t, path, "submit", "--group", cfg.Sync.GroupID, "--request-id", id); code != 0 {
		t.Fatal(value)
	}
	if prior {
		// Retained earlier obligations prohibit inferring that the whole action
		// had no effect merely from this attempted child's admission refusal.
		if _, err := store.Exec(`INSERT INTO sync_management_jobs(request_id,job_id,kind,target_commit,association_reason) VALUES(?,?,'verification',?,'verification_target')`, id, jobs[0], base); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(filepath.Join(cfg.Resources[cfg.Sync.Resource].Root, "note.md"))
	if err != nil {
		t.Fatal(err)
	}
	service := &peerService{cfg: cfg, configPath: path, store: store}
	service.dispatchManagement(ctx)
	request, err := store.LoadManagementRequest(ctx, cfg.Sync.GroupID, id, time.Now())
	if err != nil || request.Outcome == nil {
		t.Fatalf("capacity result:%+v %v", request, err)
	}
	if prior {
		if request.State != "uncertain" || request.Outcome.Status != "uncertain" || request.Outcome.TargetCommit != nil || len(request.Jobs) != 1 {
			t.Fatalf("earlier obligation lost uncertainty:%+v", request)
		}
	} else if request.State != "blocked" || request.Outcome.Status != "blocked" || request.Outcome.TargetCommit == nil || *request.Outcome.TargetCommit != target || !strings.Contains(request.Outcome.Reason, "capacity exhausted before import effects") || len(request.Jobs) != 0 {
		t.Fatalf("proven import admission hold:%+v", request)
	}
	var imports int
	if err := store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE group_id=? AND kind='import' AND json_extract(payload_json,'$.target_commit')=?`, cfg.Sync.GroupID, target).Scan(&imports); err != nil || imports != 0 {
		t.Fatalf("capacity created child:%d %v", imports, err)
	}
	if got := gitTestOutput(t, "git", cfg.Resources[cfg.Sync.Resource].Root, "rev-parse", cfg.Sync.ContentRef); got != base {
		t.Fatalf("capacity moved content ref:%s want%s", got, base)
	}
	after, err := os.ReadFile(filepath.Join(cfg.Resources[cfg.Sync.Resource].Root, "note.md"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("capacity changed governed content:%q -> %q %v", before, after, err)
	}
	for _, job := range jobs {
		if _, err := store.Exec(`UPDATE sync_jobs SET state='finished',resolved_at=? WHERE job_id=?`, time.Now().UTC().Format(time.RFC3339Nano), job); err != nil {
			t.Fatal(err)
		}
	}
}

func TestE23T4CapabilitiesPreservesPreManagementState(t *testing.T) {
	for _, mode := range []string{"absent", "disabled", "enabled", "corrupt", "future"} {
		t.Run(mode, func(t *testing.T) {
			cfg, path := e23t3Config(t)
			if mode == "absent" {
				cfg.Sync.Management = nil
			} else if mode == "disabled" {
				cfg.Sync.Management.Enabled = false
			}
			e23t3SaveConfig(t, cfg, path)
			db := filepath.Join(cfg.Instance.StateDir, StateDBName)
			store, err := sqlite.Open(db)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.EnsureLedgerForHarness(); err != nil {
				t.Fatal(err)
			}
			for _, migration := range sqlite.Migrations[:27] {
				if err := store.ApplyMigrationForHarness(migration); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "corrupt" {
				_, err = store.Exec(`UPDATE schema_migrations SET checksum='corrupt' WHERE version=27`)
			} else if mode == "future" {
				_, err = store.Exec(`UPDATE schema_migrations SET version=30 WHERE version=27`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(db)
			if err != nil {
				t.Fatal(err)
			}
			var out, errOut bytes.Buffer
			code := Run([]string{"sync", "request", "capabilities", "--group", cfg.Sync.GroupID, "--config", path}, &out, &errOut)
			if mode == "corrupt" || mode == "future" {
				if code == 0 || out.Len() != 0 {
					t.Fatalf("invalid history accepted:%d %s %s", code, &out, &errOut)
				}
			} else {
				var envelope map[string]any
				if err := json.Unmarshal(out.Bytes(), &envelope); err != nil || code != 0 || errOut.Len() != 0 {
					t.Fatalf("capabilities:%d %s %s %v", code, &out, &errOut, err)
				}
				e23t3ValidateCarrier(t, envelope)
				result := envelope["result"].(map[string]any)
				actions := 0
				if mode == "enabled" {
					actions = 5
				}
				if result["executor"] != "unknown" || len(result["available_actions"].([]any)) != actions || len(result["side_effects"].([]any)) != 0 || result["enabled"] != (mode == "enabled") {
					t.Fatalf("pre-management capability:%v", result)
				}
			}
			after, err := os.ReadFile(db)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("capabilities mutated schema27:%v", err)
			}
			read, err := sqlite.OpenReadOnly(context.Background(), db)
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			if err := read.CheckManagementSchema(context.Background()); err == nil {
				t.Fatal("request lookup no longer requires the management ledger")
			}
		})
	}
}

func TestE23T4WorkerPreservesKnownPreEffectResult(t *testing.T) {
	for _, mode := range []string{"no_op", "git_unstable", "trust", "transport", "storage"} {
		t.Run(mode, func(t *testing.T) {
			f := newResumeFixture(t)
			f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now", "verify-all"}}
			f.writeConfig(t)
			revision, _ := config.SyncRevision(f.cfg)
			if _, err := f.store.SetSyncControl(context.Background(), f.cfg.Sync.GroupID, 1, "active", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			action := "sync-now"
			if mode == "trust" || mode == "transport" || mode == "storage" {
				action = "verify-all"
			}
			id := e23t5Accepted(t, f.cfg, f.path, action)
			before, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			target := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef)
			if mode == "no_op" {
				bin := filepath.Join(filepath.Dir(f.remoteHead), "bin", "git")
				raw, err := os.ReadFile(bin)
				if err != nil {
					t.Fatal(err)
				}
				quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
				prefix := fmt.Sprintf("#!/bin/sh\ncase \" $* \" in\n *\" fetch \"*) for last do :; done; case \"$last\" in *membership*) source=$(cat %s);; *) source=%s;; esac; destination=${last#*:}; exec %s -C %s update-ref \"$destination\" \"$source\";;\n *\" ls-remote \"*%s*) printf '%%s\\t%%s\\n' %s %s; exit 0;;\nesac\n", quote(f.remoteHead), quote(target), quote(f.git), quote(f.repo), quote(f.cfg.Sync.ContentRef), quote(target), quote(f.cfg.Sync.ContentRef))
				if err := os.WriteFile(bin, []byte(prefix+strings.TrimPrefix(string(raw), "#!/bin/sh\n")), 0700); err != nil {
					t.Fatal(err)
				}
			} else if mode == "trust" {
				if err := os.WriteFile(f.remoteHead, []byte(strings.Repeat("a", 40)), 0600); err != nil {
					t.Fatal(err)
				}
			} else if mode == "transport" {
				if err := os.WriteFile(f.transportFailure, []byte("unavailable"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if mode == "storage" {
				if _, err := f.store.Exec(`CREATE TRIGGER refuse_control_metadata BEFORE INSERT ON sync_controls BEGIN SELECT RAISE(FAIL,'fixture storage failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			service := &peerService{cfg: f.cfg, configPath: f.path, store: f.store}
			service.dispatchManagement(context.Background())
			r, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
			if err != nil || r.Attempts != 1 || r.Fence != 1 || len(r.Jobs) != 0 || r.AcceptedAt == nil || *r.AcceptedAt != *before.AcceptedAt || r.Fingerprint != before.Fingerprint || r.Outcome == nil {
				t.Fatalf("immutable bounded result:%+v %v", r, err)
			}
			state, status, reason := "blocked", "blocked", "git_unstable"
			if mode == "no_op" {
				state, status, reason = "finished", "no_op", "guarded reconciliation observed the configured target"
			}
			if mode == "trust" {
				state, status, reason = "finished", "failed", "sync_trust_failed"
			}
			if mode == "transport" {
				reason = "sync_retryable"
			} else if mode == "storage" {
				reason = "sqlite_write_failed"
			}
			if r.State != state || r.Outcome.Status != status || r.Outcome.Reason != reason || r.Outcome.VerificationID != nil {
				t.Fatalf("known result lost:%+v", r)
			}
			if mode == "no_op" {
				if r.Outcome.TargetCommit == nil || *r.Outcome.TargetCommit != target {
					t.Fatal("no-op target lost")
				}
			} else if r.Outcome.TargetCommit != nil {
				t.Fatal("pre-pin refusal invented a target")
			}
			if gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef) != target {
				t.Fatal("pre-effect result moved content")
			}
		})
	}
}

func TestE23T4ExhaustedLineageYieldsQueuedHead(t *testing.T) {
	f := newResumeFixture(t)
	f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"sync-now", "retry"}}
	f.writeConfig(t)
	ctx := context.Background()
	revision, _ := config.SyncRevision(f.cfg)
	if _, err := f.store.SetSyncControl(ctx, f.cfg.Sync.GroupID, 1, "active", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	root := e23t5Accepted(t, f.cfg, f.path, "sync-now")
	binding, err := managementBindingForConfig(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.store.ClaimManagementRequest(ctx, f.cfg.Sync.GroupID, root, "budget-worker", binding, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	target := strings.Repeat("b", 40)
	plan, err := syncrecords.NewImport(syncrecords.ImportBinding{GroupID: f.cfg.Sync.GroupID, FromCommit: strings.Repeat("a", 40), TargetCommit: target, MembershipRevision: strings.Repeat("d", 40), AcknowledgementID: "fixture-ack", ResourceObservationRevision: 1, ExpectedGitStateDigest: "sha256:" + strings.Repeat("e", 64), HistoryEvidenceID: "fixture-history", CaseMode: config.CaseMode(), State: "validated", Reason: "none"}, []syncrecords.ImportPath{{Path: "note.md", Before: "absent", After: "sha256:" + strings.Repeat("f", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := syncrecords.CanonicalImport(plan)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := f.store.AdmitSyncJob(ctx, sqlite.SyncJobInput{JobID: plan.ImportID, GroupID: plan.GroupID, Kind: "import", LogicalKey: plan.PlanID, InitialState: "validated", PayloadJSON: string(raw), ConfigRevision: revision, QueueLimit: 1000, Now: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	r, err = f.store.ReserveManagementAssociations(ctx, r, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r, err = f.store.AssociateManagementJob(ctx, r, sqlite.ManagementJob{JobID: job.JobID, Kind: "import", TargetCommit: &target, AssociationReason: "observed_reconciliation"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r, err = f.store.TransitionManagementRequest(ctx, r, "blocked", &syncmanagement.Outcome{Status: "blocked", Reason: "fixture obligation"}, "fixture-budget-inspection", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	code, v := e23t5Public(t, f.path, "prepare", "--group", f.cfg.Sync.GroupID, "--action", "retry", "--target-request-id", root)
	if code != 0 {
		t.Fatal(v)
	}
	child := v["result"].(map[string]any)["request_id"].(string)
	if code, v = e23t5Public(t, f.path, "submit", "--group", f.cfg.Sync.GroupID, "--request-id", child); code != 0 {
		t.Fatal(v)
	}
	for n := 1; n < syncmanagement.AttemptLimit; n++ {
		if _, err := f.store.TransitionManagementRequest(ctx, r, "queued", nil, "fixture-budget-inspection", time.Now()); err != nil {
			t.Fatal(err)
		}
		r, err = f.store.ClaimManagementRequest(ctx, f.cfg.Sync.GroupID, root, "budget-worker", binding, true, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		r, err = f.store.TransitionManagementRequest(ctx, r, "blocked", &syncmanagement.Outcome{Status: "blocked", Reason: "fixture obligation"}, "fixture-budget-inspection", time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.store.TransitionManagementRequest(ctx, r, "finished", &syncmanagement.Outcome{Status: "failed", Reason: "operator review required"}, "fixture-budget-inspection", time.Now()); err != nil {
		t.Fatal(err)
	}
	later := e23t5Accepted(t, f.cfg, f.path, "sync-now")
	// Retry drafts inherit the predecessor attempt count. Put later work at
	// the same scheduling priority so the exhausted lineage is the head.
	laterClaim, err := f.store.ClaimManagementRequest(ctx, f.cfg.Sync.GroupID, later, "later-worker", binding, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.TransitionManagementRequest(ctx, laterClaim, "queued", nil, "fixture-budget-inspection", time.Now()); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.LoadManagementRequest(ctx, f.cfg.Sync.GroupID, child, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	service := &peerService{cfg: f.cfg, configPath: f.path, store: f.store}
	service.dispatchManagement(ctx)
	held, err := f.store.LoadManagementRequest(ctx, f.cfg.Sync.GroupID, child, time.Now())
	if err != nil || held.State != "blocked" || held.Attempts != before.Attempts || held.Fence != before.Fence || held.Outcome == nil || held.Outcome.Reason != "request lineage exhausted its execution budget" || held.Fingerprint != before.Fingerprint || held.AcceptedAt == nil || *held.AcceptedAt != *before.AcceptedAt || len(held.Jobs) != 1 || held.Jobs[0].JobID != before.Jobs[0].JobID || held.Jobs[0].State != before.Jobs[0].State || held.Jobs[0].Attempts != before.Jobs[0].Attempts || held.Jobs[0].TargetCommit == nil || *held.Jobs[0].TargetCommit != target {
		t.Fatalf("exhausted lineage:%+v %v", held, err)
	}
	next, err := f.store.NextQueuedManagementRequest(ctx, f.cfg.Sync.GroupID)
	if err != nil || next != later {
		t.Fatalf("exhausted head starved later work:%s %v", next, err)
	}
	service.dispatchManagement(ctx)
	progressed, err := f.store.LoadManagementRequest(ctx, f.cfg.Sync.GroupID, later, time.Now())
	if err != nil || progressed.State == "queued" || progressed.Attempts != laterClaim.Attempts+1 {
		t.Fatalf("later work did not progress:%+v %v", progressed, err)
	}
	original, err := f.store.LoadManagementRequest(ctx, f.cfg.Sync.GroupID, root, time.Now())
	if err != nil || original.Attempts != syncmanagement.AttemptLimit || original.Jobs[0].Attempts != before.Jobs[0].Attempts {
		t.Fatalf("refusal consumed budget or effects:%+v %v", original, err)
	}
}

func TestE23T4SharedRefusalRequiresTypedCompleteDiagnosis(t *testing.T) {
	for _, raw := range []string{`{`, `{"ok":false,"command":"sync verify","error":{"code":"sync_trust_failed","category":"storage"}}`, `{"ok":false,"command":"sync reconcile","error":{"code":"sync_trust_failed","category":"security"}}`, `{"ok":false,"command":"sync verify","error":{"code":"/private/secret","category":"security"}}`} {
		if state, reason := managementSharedRefusal("verify-all", 30, []byte(raw)); state != "" || reason != "" {
			t.Fatalf("untyped refusal became certainty:%s %s", state, reason)
		}
	}
	var out bytes.Buffer
	writeError(&out, "sync verify", "sync_trust_failed", "security", "/private/secret remote text")
	if state, reason := managementSharedRefusal("verify-all", 30, out.Bytes()); state != "finished" || reason != "sync_trust_failed" {
		t.Fatal("typed diagnosis was lost")
	}
	bounded := boundedReconcileOutput{limit: out.Len()}
	if _, err := bounded.Write(out.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := bounded.Write([]byte(" ")); err == nil || !bounded.overflow {
		t.Fatal("complete prefix hid oversized output")
	}
}

func TestE23T4KnownRefusalKeepsAmbiguousWorkerEvidence(t *testing.T) {
	for _, kind := range []string{"overflow", "associated"} {
		t.Run(kind, func(t *testing.T) {
			f := newResumeFixture(t)
			f.cfg.Sync.Management = &config.SyncManagement{Enabled: true, Actions: []string{"verify-all"}}
			f.writeConfig(t)
			revision, _ := config.SyncRevision(f.cfg)
			if _, err := f.store.SetSyncControl(context.Background(), f.cfg.Sync.GroupID, 1, "active", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			target := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef)
			if kind == "associated" {
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
				if _, err := f.store.Exec(`CREATE TRIGGER refuse_verification_finish BEFORE UPDATE OF state ON sync_jobs WHEN OLD.kind='verification' AND NEW.state='collecting' BEGIN SELECT RAISE(FAIL,'fixture-private-finalization'); END`); err != nil {
					t.Fatal(err)
				}
			} else if kind == "overflow" {
				message := "fixture-private-storage"
				create := func(text string) {
					t.Helper()
					if _, err := f.store.Exec("CREATE TRIGGER refuse_control_metadata BEFORE INSERT ON sync_controls BEGIN SELECT RAISE(FAIL,'" + strings.ReplaceAll(text, "'", "''") + "'); END"); err != nil {
						t.Fatal(err)
					}
				}
				create(message)
				_, err := f.store.EnsureSyncControl(context.Background(), f.cfg.Sync.GroupID, revision, time.Now().UTC().Format(time.RFC3339Nano))
				if err == nil {
					t.Fatal("storage fixture did not refuse")
				}
				var probe bytes.Buffer
				if syncStoreError(&probe, "sync verify", err) != 20 {
					t.Fatal("storage fixture has wrong class")
				}
				message += strings.Repeat("x", syncmanagement.ResultBytes+1-probe.Len())
				if _, err := f.store.Exec(`DROP TRIGGER refuse_control_metadata`); err != nil {
					t.Fatal(err)
				}
				create(message)
				_, err = f.store.EnsureSyncControl(context.Background(), f.cfg.Sync.GroupID, revision, time.Now().UTC().Format(time.RFC3339Nano))
				probe.Reset()
				syncStoreError(&probe, "sync verify", err)
				if probe.Len() != syncmanagement.ResultBytes+1 || !json.Valid(probe.Bytes()[:syncmanagement.ResultBytes]) {
					t.Fatalf("overflow fixture must retain a complete typed JSON prefix: %d", probe.Len())
				}
			}
			id := e23t5Accepted(t, f.cfg, f.path, "verify-all")
			before, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("SYNC_NODE_A_TO_B", "fixture-peer-credential")
			observations := 0
			client := &http.Client{Transport: e22t3RoundTrip(func(r *http.Request) (*http.Response, error) {
				if kind != "associated" {
					t.Fatal("pre-effect refusal started a peer observation")
				}
				observations++
				var request syncrecords.StatusRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				response := syncrecords.StatusResponse{SchemaVersion: syncrecords.StatusResponseSchema, GroupID: request.GroupID, Responder: request.Receiver, StateIncarnationID: f.cfg.Sync.Nodes[1].StateIncarnationID, MembershipRevision: request.MembershipRevision, ContentRef: request.ContentRef, TargetCommit: request.TargetCommit, ScopeDigest: request.ScopeDigest, ContractDigest: request.ContractDigest, Nonce: request.Nonce, EvidenceGeneration: time.Now().UnixNano(), State: "applied", MembershipCurrent: true}
				raw, _ := json.Marshal(response)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: http.Header{}}, nil
			})}
			service := &peerService{cfg: f.cfg, configPath: f.path, store: f.store}
			service.dispatchManagementWithHTTP(context.Background(), client)
			after, err := f.store.LoadManagementRequest(context.Background(), f.cfg.Sync.GroupID, id, time.Now())
			if err != nil || after.Attempts != 1 || after.Fence != 1 || after.AcceptedAt == nil || *after.AcceptedAt != *before.AcceptedAt || after.Fingerprint != before.Fingerprint || after.Outcome == nil {
				t.Fatalf("fault changed immutable acceptance or budget:%+v %v", after, err)
			}
			state, reason := "uncertain", "execution requires exact journal inspection"
			if after.State != state || after.Outcome.Status != state || after.Outcome.Reason != reason || after.Outcome.TargetCommit != nil || after.Outcome.VerificationID != nil {
				t.Fatalf("fault claimed an unsupported result:%+v", after)
			}
			if kind == "associated" {
				if observations == 0 || len(after.Jobs) != 1 || after.Jobs[0].Kind != "verification" || after.Jobs[0].State != "planned" || after.Jobs[0].Attempts != 0 || after.Jobs[0].TargetCommit == nil || *after.Jobs[0].TargetCommit != target || after.Jobs[0].AssociationReason != "verification_target" {
					t.Fatalf("finalization refusal discarded the actual observation journal:%+v observations%d", after, observations)
				}
			} else if len(after.Jobs) != 0 {
				t.Fatalf("pre-effect fault invented an association:%+v", after.Jobs)
			}
			if gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef) != target {
				t.Fatal("refusal changed content")
			}
		})
	}
}

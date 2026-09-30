package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/adapters/resourceguard"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

type resumeFixture struct {
	cfg                                           *config.Config
	path, repo, git, remoteHead, transportFailure string
	store                                         *sqlite.Store
	adminKey                                      []byte
}

func newResumeFixture(t *testing.T) *resumeFixture {
	t.Helper()
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	if err := os.Mkdir(homeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", homeDir)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("SSH_AUTH_SOCK", "")
	// Install both service-manager stubs before any status command can run.
	oldLaunchDir, oldSystemdDir := launchAgentsDir, systemdUserDir
	oldLaunchctl, oldSystemctl := launchctlRun, systemctlRun
	launchAgentsDir = func() string { return filepath.Join(homeDir, "LaunchAgents") }
	systemdUserDir = func() string { return filepath.Join(homeDir, "systemd") }
	launchctlRun = func(...string) (string, error) { return "fixture service absent", os.ErrNotExist }
	systemctlRun = func(...string) (string, error) { return "fixture service absent", os.ErrNotExist }
	t.Cleanup(func() {
		launchAgentsDir, systemdUserDir = oldLaunchDir, oldSystemdDir
		launchctlRun, systemctlRun = oldLaunchctl, oldSystemctl
	})
	cfg, err := config.Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "vault")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, realGit, repo, "init", "-q", "-b", strings.TrimPrefix(cfg.Sync.ContentRef, "refs/heads/"))
	gitTestRun(t, realGit, repo, "config", "user.name", "Resume test")
	gitTestRun(t, realGit, repo, "config", "user.email", "resume@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, realGit, repo, "add", "note.md")
	gitTestRun(t, realGit, repo, "commit", "-q", "-m", "base")
	adminKey, fingerprint := e21t3Key(t, dir, "admin")
	resource := cfg.Resources[cfg.Sync.Resource]
	resource.Root = repo
	cfg.Resources[cfg.Sync.Resource] = resource
	cfg.Instance.StateDir = filepath.Join(dir, "state")
	cfg.Sync.Enabled = true
	cfg.Sync.AdministratorKey = fingerprint
	cfg.Sync.ImportAcknowledgement = nil
	remoteURL := "ssh://git@example.invalid/resume"
	cfg.Sync.RemoteRepositoryDigest, _, err = config.RemoteRepositoryDigest(remoteURL)
	if err != nil {
		t.Fatal(err)
	}
	f := &resumeFixture{cfg: cfg, path: filepath.Join(dir, "config.json"), repo: repo, git: realGit, remoteHead: filepath.Join(dir, "remote-head"), transportFailure: filepath.Join(dir, "transport-failure"), adminKey: adminKey}
	f.writeConfig(t)
	t.Setenv("AGENT_DISPATCH_CONFIG", f.path)
	client, err := membershipGitClient(cfg, cfg.Sync)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := syncrecords.NewPlan("bootstrap", "", nil, nil, configuredMembers(cfg.Sync), membershipBinding(cfg, cfg.Sync))
	if err != nil {
		t.Fatal(err)
	}
	f.signMembership(t, client, plan, "")
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	// All remote commands terminate here; only local Git commands reach Git.
	script := fmt.Sprintf(`#!/bin/sh
case " $* " in
  *" remote get-url "*) printf '%%s\n' '%s'; exit 0;;
  *" ls-remote "*)
    test ! -e '%s' || exit 1
    for last do :; done
    test "$last" = '%s' || exit 2
    test -s '%s' && printf '%%s\t%%s\n' "$(cat '%s')" "$last"
    exit 0;;
  *" push "*|*" fetch "*|*" clone "*) exit 97;;
esac
exec '%s' "$@"
`, remoteURL, f.transportFailure, cfg.Sync.MembershipRef, f.remoteHead, f.remoteHead, realGit)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f.store, err = openStateStore(f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.store.Close() })
	revision, _ := config.SyncRevision(cfg)
	if _, err := f.store.EnsureSyncControl(requestCtx(), cfg.Sync.GroupID, revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *resumeFixture) writeConfig(t *testing.T) {
	t.Helper()
	raw, err := json.Marshal(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *resumeFixture) signMembership(t *testing.T, client *gitlocal.Client, plan syncrecords.MembershipPlan, predecessor string) string {
	t.Helper()
	document, err := syncrecords.CanonicalMembership(plan.ProposedMembership)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := syncrecords.CanonicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	head, err := client.CreateSignedMembershipCommit(requestCtx(), document, raw, f.adminKey, predecessor, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, f.git, f.repo, "update-ref", f.cfg.Sync.MembershipRef, head)
	if err := os.WriteFile(f.remoteHead, []byte(head), 0o600); err != nil {
		t.Fatal(err)
	}
	return head
}

func TestResumeRegressionRejectsUnsafeActivation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, *resumeFixture)
		code   int
	}{
		{"missing_membership", func(t *testing.T, f *resumeFixture) {
			gitTestRun(t, f.git, f.repo, "update-ref", "-d", f.cfg.Sync.MembershipRef)
		}, 30},
		{"unsigned_membership", func(t *testing.T, f *resumeFixture) {
			head := gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD")
			gitTestRun(t, f.git, f.repo, "update-ref", f.cfg.Sync.MembershipRef, head)
			if err := os.WriteFile(f.remoteHead, []byte(head), 0o600); err != nil {
				t.Fatal(err)
			}
		}, 30},
		{"stale_remote_membership", func(t *testing.T, f *resumeFixture) {
			if err := os.WriteFile(f.remoteHead, []byte(strings.Repeat("1", 40)), 0o600); err != nil {
				t.Fatal(err)
			}
		}, 30},
		{"remote_unavailable", func(t *testing.T, f *resumeFixture) {
			if err := os.WriteFile(f.transportFailure, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}, 10},
		{"endpoint_changed", func(t *testing.T, f *resumeFixture) {
			f.cfg.Sync.Nodes[0].Endpoint = "https://changed.example.ts.net"
			f.writeConfig(t)
		}, 30},
		{"incarnation_changed", func(t *testing.T, f *resumeFixture) {
			f.cfg.Sync.Nodes[0].StateIncarnationID = "new-incarnation"
			f.writeConfig(t)
		}, 30},
		{"publisher_key_changed", func(t *testing.T, f *resumeFixture) {
			_, key := e21t3Key(t, t.TempDir(), "new-publisher")
			f.cfg.Sync.Nodes[0].PublisherKey = key
			f.writeConfig(t)
		}, 30},
		{"administrator_anchor_changed", func(t *testing.T, f *resumeFixture) {
			_, key := e21t3Key(t, t.TempDir(), "new-admin")
			f.cfg.Sync.AdministratorKey = key
			f.writeConfig(t)
		}, 30},
		{"signed_local_revocation", func(t *testing.T, f *resumeFixture) {
			client, err := membershipGitClient(f.cfg, f.cfg.Sync)
			if err != nil {
				t.Fatal(err)
			}
			head := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.MembershipRef)
			raw, _, err := client.ReadMembershipCommit(requestCtx(), head)
			if err != nil {
				t.Fatal(err)
			}
			current, err := syncrecords.DecodeMembership(raw)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := syncrecords.NewPlan("emergency_revocation", f.cfg.Sync.LocalInstanceID, &current, &head, configuredMembers(f.cfg.Sync), membershipBinding(f.cfg, f.cfg.Sync))
			if err != nil {
				t.Fatal(err)
			}
			f.signMembership(t, client, plan, head)
		}, 30},
		{"disabled", func(t *testing.T, f *resumeFixture) { f.cfg.Sync.Enabled = false; f.writeConfig(t) }, 14},
		{"foreign_branch", func(t *testing.T, f *resumeFixture) { gitTestRun(t, f.git, f.repo, "checkout", "-q", "-b", "foreign") }, 14},
		{"detached_head", func(t *testing.T, f *resumeFixture) { gitTestRun(t, f.git, f.repo, "checkout", "-q", "--detach") }, 14},
		{"merge_in_progress", func(t *testing.T, f *resumeFixture) {
			head := gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD")
			if err := os.WriteFile(filepath.Join(f.repo, ".git", "MERGE_HEAD"), []byte(head+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, 14},
		{"index_lock", func(t *testing.T, f *resumeFixture) {
			if err := os.WriteFile(filepath.Join(f.repo, ".git", "index.lock"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}, 14},
		{"resource_busy", func(t *testing.T, f *resumeFixture) {
			guard, err := resourceguard.Acquire(stateDirOf(f.cfg), f.cfg.Sync.Resource)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { guard.Close() })
		}, 14},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newResumeFixture(t)
			code, _, stderr := syncResult(t, "pause", "--group", f.cfg.Sync.GroupID, "--expected-control-revision", "1", "--config", f.path)
			if code != 0 {
				t.Fatalf("pause: %d %s", code, stderr)
			}
			tc.change(t, f)
			before, err := f.store.LoadSyncControl(requestCtx(), f.cfg.Sync.GroupID)
			if err != nil {
				t.Fatal(err)
			}
			index := mustRead(t, filepath.Join(f.repo, ".git", "index"))
			head := gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD")
			code, envelope, stderr := syncResult(t, "resume", "--group", f.cfg.Sync.GroupID, "--expected-control-revision", "2", "--config", f.path)
			after, err := f.store.LoadSyncControl(requestCtx(), f.cfg.Sync.GroupID)
			if err != nil {
				t.Fatal(err)
			}
			if code != tc.code || after != before {
				t.Fatalf("resume: code=%d want=%d before=%+v after=%+v out=%v err=%s", code, tc.code, before, after, envelope, stderr)
			}
			if string(index) != string(mustRead(t, filepath.Join(f.repo, ".git", "index"))) || head != gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD") {
				t.Fatal("resume changed index or HEAD")
			}
		})
	}
}

func TestResumeRegressionRebindsConfigWithoutImportAcknowledgement(t *testing.T) {
	f := newResumeFixture(t)
	oldRevision, _ := config.SyncRevision(f.cfg)
	control, err := f.store.SetSyncControl(requestCtx(), f.cfg.Sync.GroupID, 1, "paused", oldRevision, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.Sync.Bounds.HistoryCommits--
	f.writeConfig(t)
	revision, _ := config.SyncRevision(f.cfg)
	if revision == oldRevision {
		t.Fatal("fixture did not change configuration revision")
	}
	// Ordinary maintenance edits remain publishable after activation.
	note := filepath.Join(f.repo, "note.md")
	if err := os.WriteFile(note, []byte("staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, f.git, f.repo, "add", "note.md")
	if err := os.WriteFile(note, []byte("unstaged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	indexTree := gitTestOutput(t, f.git, f.repo, "write-tree")
	head := gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD")
	code, out, stderr := syncResult(t, "resume", "--group", f.cfg.Sync.GroupID, "--expected-control-revision", strconv.FormatInt(control.Revision, 10), "--config", f.path)
	if code != 0 {
		t.Fatalf("resume: %d out=%v err=%s", code, out, stderr)
	}
	control, err = f.store.LoadSyncControl(requestCtx(), f.cfg.Sync.GroupID)
	if err != nil {
		t.Fatal(err)
	}
	if control.State != "active" || control.ConfigRevision != revision {
		t.Fatalf("configuration not rebound: %+v", control)
	}
	if string(mustRead(t, note)) != "unstaged\n" || gitTestOutput(t, f.git, f.repo, "write-tree") != indexTree || gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD") != head {
		t.Fatal("resume changed local maintenance edits")
	}
	// An already-active control must still pass current safety checks.
	gitTestRun(t, f.git, f.repo, "checkout", "-q", "-b", "foreign")
	code, _, stderr = syncResult(t, "resume", "--group", f.cfg.Sync.GroupID, "--expected-control-revision", strconv.FormatInt(control.Revision, 10), "--config", f.path)
	if code != 14 {
		t.Fatalf("active unsafe resume: %d %s", code, stderr)
	}
	after, err := f.store.LoadSyncControl(requestCtx(), f.cfg.Sync.GroupID)
	if err != nil || after != control {
		t.Fatalf("unsafe idempotent resume changed control: %+v %v", after, err)
	}
}

func TestResumeRegressionPreservesControlSemantics(t *testing.T) {
	for _, posture := range []string{"normal", "emergency", "conflict", "trust_failure", "recovery_required"} {
		t.Run(posture, func(t *testing.T) {
			f := newResumeFixture(t)
			revision, _ := config.SyncRevision(f.cfg)
			control, err := f.store.SetSyncControl(requestCtx(), f.cfg.Sync.GroupID, 1, "paused", revision, time.Now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if posture == "emergency" {
				control, err = f.store.ReconcileAdoptedMembership(requestCtx(), f.cfg.Sync.GroupID, "blocked_emergency", strings.Repeat("1", 40), revision, time.Now().UTC().Format(time.RFC3339Nano))
				want = 30
			} else if posture != "normal" {
				control, err = f.store.HoldSyncControl(requestCtx(), f.cfg.Sync.GroupID, posture, revision, time.Now().UTC().Format(time.RFC3339Nano))
				want = 14
			}
			if err != nil {
				t.Fatal(err)
			}
			code, _, stderr := syncResult(t, "resume", "--group", f.cfg.Sync.GroupID, "--expected-control-revision", strconv.FormatInt(control.Revision-1, 10), "--config", f.path)
			if code != 14 {
				t.Fatalf("stale resume: %d %s", code, stderr)
			}
			code, out, stderr := syncResult(t, "resume", "--group", f.cfg.Sync.GroupID, "--expected-control-revision", strconv.FormatInt(control.Revision, 10), "--config", f.path)
			if code != want {
				t.Fatalf("resume: %d want=%d out=%v err=%s", code, want, out, stderr)
			}
			after, err := f.store.LoadSyncControl(requestCtx(), f.cfg.Sync.GroupID)
			if err != nil {
				t.Fatal(err)
			}
			switch posture {
			case "normal":
				if after.State != "active" || after.ConfigRevision != revision || config.SyncAcknowledgementCurrent(f.cfg, localSyncIncarnation(f.cfg)) {
					t.Fatalf("unacknowledged activation: %+v", after)
				}
			case "emergency":
				if after.State != "blocked" || after.Reason != "membership_emergency" || after.MembershipMode != "blocked_emergency" {
					t.Fatalf("lost emergency: %+v", after)
				}
			default:
				if after != control {
					t.Fatalf("hold changed: before=%+v after=%+v", control, after)
				}
			}
		})
	}
}

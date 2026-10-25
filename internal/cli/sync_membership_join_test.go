package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

// Use the signed candidate produced by the publication test, a separate Git
// checkout, and fresh local state. Only the network transport is substituted;
// fetch transfers the actual membership and content objects between fixtures.
func assertWorkReceiptPublicationPeerImport(t *testing.T, cfg *config.Config, configPath, sourceRepo, git string, publication syncrecords.Publication, target string) {
	t.Helper()
	raw := mustRead(t, configPath)
	var peer config.Config
	if err := json.Unmarshal(raw, &peer); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "peer")
	gitTestRun(t, git, dir, "clone", "-q", "--no-local", sourceRepo, repo)
	gitTestRun(t, git, repo, "checkout", "-q", "-B", strings.TrimPrefix(peer.Sync.ContentRef, "refs/heads/"), publication.BaseCommit)
	resource := peer.Resources[peer.Sync.Resource]
	resource.Root = repo
	peer.Resources[peer.Sync.Resource] = resource
	for _, node := range peer.Sync.Nodes {
		if node.InstanceID != cfg.Sync.LocalInstanceID {
			peer.Instance.ID = node.InstanceID
			peer.Sync.LocalInstanceID = node.InstanceID
			break
		}
	}
	peer.Instance.StateDir = filepath.Join(dir, "state")
	peer.Sync.AdministratorSigningKeyRef = "env:SYNC_JOIN_ADMINISTRATOR"
	peer.Sync.PublisherSigningKeyRef = "env:SYNC_JOIN_PUBLISHER"
	t.Setenv("SYNC_JOIN_ADMINISTRATOR", "")
	t.Setenv("SYNC_JOIN_PUBLISHER", "")
	revision, _ := config.SyncRevision(&peer)
	peer.Sync.ImportAcknowledgement = &config.SyncImportAcknowledgement{
		SchemaVersion: "agent-dispatch.sync-import-acknowledgement/v1", AcknowledgementID: "acknowledgement-first-participation",
		GroupID: peer.Sync.GroupID, ResourceID: peer.Sync.Resource, RemoteName: peer.Sync.RemoteName,
		RemoteRepositoryDigest: peer.Sync.RemoteRepositoryDigest, ContentRef: peer.Sync.ContentRef, MembershipRef: peer.Sync.MembershipRef,
		ScopeDigest: config.SyncScopeDigest(&peer, peer.Sync.Resource), LocalInstanceID: peer.Sync.LocalInstanceID,
		StateIncarnationID: localSyncIncarnation(&peer), AdministratorKey: peer.Sync.AdministratorKey,
		SafetyPolicyDigest: config.SyncSafetyPolicyDigest(), ImportBoundsDigest: config.SyncImportBoundsDigest(peer.Sync.Bounds), ConfigRevision: revision,
	}
	path := filepath.Join(dir, "peer.json")
	syncFreshnessWriteConfig(t, path, &peer)
	// A new node still needs the normal setup baseline before importing.
	for routeID, route := range peer.Routes {
		if route.Source.Resource != peer.Sync.Resource {
			continue
		}
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"reconcile", "--route", routeID, "--reason", "initial", "--baseline-only", "--config", path}, &stdout, &stderr); code != 0 {
			t.Fatalf("peer observation baseline: exit=%d out=%s err=%s", code, stdout.String(), stderr.String())
		}
	}
	prepareE21T3State(t, &peer, path)

	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := fmt.Sprintf(`#!/bin/sh
case " $* " in
  *" remote get-url "*) printf '%%s\n' 'ssh://git@example.com/repo'; exit 0;;
  *" ls-remote "*|*" fetch "*)
    for last do :; done
    source=${last%%%%:*}; destination=${last#*:}
    case "$source" in
      %s) oid=%s;;
      %s) oid=%s;;
      *) exit 97;;
    esac
    case " $* " in
      *" ls-remote "*) printf '%%s\t%%s\n' "$oid" "$source";;
      *" fetch "*)
        GIT_ALLOW_PROTOCOL=file
        export GIT_ALLOW_PROTOCOL
        exec %s -C %s fetch --no-tags --no-write-fetch-head %s "$oid:$destination";;
    esac
    exit 0;;
  *" push "*|*" clone "*) exit 97;;
esac
exec %s "$@"
`, quote(peer.Sync.MembershipRef), quote(publication.MembershipRevision), quote(peer.Sync.ContentRef), quote(target), quote(git), quote(repo), quote(sourceRepo), quote(git))
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AGENT_DISPATCH_CONFIG", path)
	client, err := membershipGitClient(&peer, peer.Sync)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ResolveRef(requestCtx(), peer.Sync.MembershipRef); !errors.Is(err, gitlocal.ErrMissingRef) {
		t.Fatalf("peer must start without a membership ref: %v", err)
	}
	for _, state := range []string{"membership_adopted", "applied", "no_change"} {
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"sync", "reconcile", "--group", peer.Sync.GroupID}, &stdout, &stderr); code != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"state":"`+state+`"`)) {
			t.Fatalf("first-participation %s: exit=%d out=%s err=%s", state, code, stdout.String(), stderr.String())
		}
	}
	if got := gitTestOutput(t, git, repo, "rev-parse", peer.Sync.MembershipRef); got != publication.MembershipRevision {
		t.Fatalf("adopted membership=%s want=%s", got, publication.MembershipRevision)
	}
	if got := gitTestOutput(t, git, repo, "rev-parse", peer.Sync.ContentRef); got != target {
		t.Fatalf("peer target=%s want=%s", got, target)
	}
	if got := string(mustRead(t, filepath.Join(repo, "note.md"))); got != "changed\n" {
		t.Fatalf("peer did not import maintained content: %q", got)
	}
	store, err := openStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	control, err := store.LoadSyncControl(requestCtx(), peer.Sync.GroupID)
	if err != nil || control.State != "active" || control.Reason != "none" {
		t.Fatalf("first participation created a control hold: %+v %v", control, err)
	}
}

func TestSyncFirstMembershipPreservesTrustAndManagementBoundaries(t *testing.T) {
	for _, scenario := range []string{"unpinned_administrator", "management_request", "existing_trust_hold", "emergency_membership"} {
		t.Run(scenario, func(t *testing.T) {
			f := newResumeFixture(t)
			client, err := membershipGitClient(f.cfg, f.cfg.Sync)
			if err != nil {
				t.Fatal(err)
			}
			head := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.MembershipRef)
			revision, _ := config.SyncRevision(f.cfg)
			wantCode, wantReason := 30, "trust_failure"
			ctx := context.Background()
			switch scenario {
			case "unpinned_administrator":
				_, f.cfg.Sync.AdministratorKey = e21t3Key(t, t.TempDir(), "other-administrator")
				wantReason = "trust_failed"
			case "management_request":
				ctx = context.WithValue(ctx, managementExecutionKey{}, &managementExecution{})
				wantCode, wantReason = 14, "membership_adoption_requires_operator"
			case "existing_trust_hold":
				if _, err := f.store.HoldSyncControl(ctx, f.cfg.Sync.GroupID, "trust_failure", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
					t.Fatal(err)
				}
			case "emergency_membership":
				raw, _, err := client.ReadMembershipCommit(ctx, head)
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
				head = f.signMembership(t, client, plan, head)
				wantReason = "membership_emergency"
			}
			gitTestRun(t, f.git, f.repo, "update-ref", "-d", f.cfg.Sync.MembershipRef)
			// The existing fixture denies content transport. These cases must
			// settle at the membership boundary without reaching content effects.
			transport, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			script := fmt.Sprintf(`#!/bin/sh
case " $* " in
  *" fetch "*)
    for last do :; done
    test "$last" = '%s:refs/agent-dispatch/sync/membership/%s' || exit 97
    exec '%s' -C '%s' update-ref "${last#*:}" '%s';;
esac
exec '%s' "$@"
`, f.cfg.Sync.MembershipRef, f.cfg.Sync.GroupID, f.git, f.repo, head, transport)
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			var stdout, stderr bytes.Buffer
			code := reconcileConfiguredSync(ctx, f.cfg, f.store, &stdout, &stderr)
			if code != wantCode || !bytes.Contains(stdout.Bytes(), []byte(`"reason":"`+wantReason+`"`)) {
				t.Fatalf("first membership %s: exit=%d out=%s err=%s", scenario, code, stdout.String(), stderr.String())
			}
			adopted, err := client.ResolveRef(ctx, f.cfg.Sync.MembershipRef)
			control, controlErr := f.store.LoadSyncControl(ctx, f.cfg.Sync.GroupID)
			if controlErr != nil {
				t.Fatal(controlErr)
			}
			if scenario == "management_request" || scenario == "unpinned_administrator" {
				if !errors.Is(err, gitlocal.ErrMissingRef) {
					t.Fatalf("refusal installed membership: %s %v", adopted, err)
				}
				if scenario == "management_request" && control.State != "active" {
					t.Fatalf("management refusal created a trust hold: %+v", control)
				}
				if scenario == "unpinned_administrator" && (control.State != "blocked" || control.Reason != "trust_failure") {
					t.Fatalf("invalid administrator did not create a trust hold: %+v", control)
				}
			} else if err != nil || adopted != head || control.State != "blocked" || control.Reason != wantReason {
				t.Fatalf("adoption cleared a required hold: ref=%s control=%+v err=%v", adopted, control, err)
			}
			if scenario == "emergency_membership" && control.MembershipMode != "blocked_emergency" {
				t.Fatalf("adoption lost emergency posture: %+v", control)
			}
		})
	}
}

package gitlocal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/config"
)

func TestRestrictedGitInspectCompareAndMissingRef(t *testing.T) {
	repo := initRepository(t)
	client, err := New(repo, Limits{Timeout: 5 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	state, err := client.Inspect(ctx)
	if err != nil || state.State != "clean" {
		t.Fatalf("clean inspect: %+v %v", state, err)
	}
	if _, err := client.ResolveRef(ctx, "refs/heads/missing"); !errors.Is(err, ErrMissingRef) {
		t.Fatalf("missing ref: %v", err)
	}
	first := gitOutput(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err = client.Inspect(ctx)
	if err != nil || state.State != "dirty" {
		t.Fatalf("dirty inspect: %+v %v", state, err)
	}
	gitRun(t, repo, "add", "note.md")
	gitRun(t, repo, "commit", "-m", "second")
	second := gitOutput(t, repo, "rev-parse", "HEAD")
	if relation, err := client.Compare(ctx, first, second); err != nil || relation != RelationBehind {
		t.Fatalf("behind: %s %v", relation, err)
	}
	if relation, err := client.Compare(ctx, second, first); err != nil || relation != RelationAhead {
		t.Fatalf("ahead: %s %v", relation, err)
	}
	gitRun(t, repo, "checkout", "-b", "other", first)
	if err := os.WriteFile(filepath.Join(repo, "other.md"), []byte("other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "other.md")
	gitRun(t, repo, "commit", "-m", "other")
	other := gitOutput(t, repo, "rev-parse", "HEAD")
	if relation, err := client.Compare(ctx, second, other); err != nil || relation != RelationDiverged {
		t.Fatalf("diverged: %s %v", relation, err)
	}
}

func TestImportIndexPreservesDisjointDirtyPath(t *testing.T) {
	repo := initRepository(t)
	if err := os.WriteFile(filepath.Join(repo, "local.md"), []byte("base-local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "staged.md"), []byte("base-staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "local.md", "staged.md")
	gitRun(t, repo, "commit", "-m", "local base")
	from := gitOutput(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("remote\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "note.md")
	gitRun(t, repo, "commit", "-m", "remote target")
	target := gitOutput(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "reset", "--hard", from)
	if err := os.WriteFile(filepath.Join(repo, "local.md"), []byte("local edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "staged.md"), []byte("staged edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "staged.md")
	stagedBefore := gitOutput(t, repo, "rev-parse", ":staged.md")
	client, err := New(repo, Limits{Timeout: 5 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.InspectImport(context.Background(), "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	overlap, collisions := ImportOverlap([]string{"note.md"}, state.DirtyPaths, state.UntrackedPaths)
	if len(overlap) != 0 || len(collisions) != 0 {
		t.Fatalf("disjoint edit rejected: overlap=%v collisions=%v", overlap, collisions)
	}
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("remote\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.ApplyImportIndex(context.Background(), "refs/heads/main", from, target, map[string][]byte{"note.md": []byte("remote\n")}, nil); err != nil {
		t.Fatal(err)
	}
	if got := gitOutput(t, repo, "rev-parse", "refs/heads/main"); got != target {
		t.Fatalf("content ref=%s target=%s", got, target)
	}
	if raw, _ := os.ReadFile(filepath.Join(repo, "local.md")); string(raw) != "local edit\n" {
		t.Fatalf("disjoint working edit changed: %q", raw)
	}
	if stagedAfter := gitOutput(t, repo, "rev-parse", ":staged.md"); stagedAfter != stagedBefore {
		t.Fatalf("disjoint staged edit changed: %s -> %s", stagedBefore, stagedAfter)
	}
	status := gitOutput(t, repo, "status", "--porcelain=v1")
	if !strings.Contains(status, "local.md") || strings.Contains(status, "note.md") {
		t.Fatalf("unexpected status after import: %q", status)
	}
	commits, err := client.FirstParentRange(context.Background(), from, target, 2)
	if err != nil || len(commits) != 1 || commits[0] != target {
		t.Fatalf("range=%v err=%v", commits, err)
	}
}

func TestImportIndexTreatsMarkdownPathAsLiteral(t *testing.T) {
	repo := initRepository(t)
	for path, body := range map[string]string{"draft*.md": "original\n", "draft1.md": "neighbor\n"} {
		if err := os.WriteFile(filepath.Join(repo, path), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, repo, "add", "--", ":(literal)draft*.md", "draft1.md")
	gitRun(t, repo, "commit", "-m", "literal path base")
	from := gitOutput(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "draft*.md"), []byte("remote\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "--", ":(literal)draft*.md")
	gitRun(t, repo, "commit", "-m", "literal path target")
	target := gitOutput(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "reset", "--hard", from)
	if err := os.WriteFile(filepath.Join(repo, "draft*.md"), []byte("remote\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := New(repo, Limits{Timeout: 5 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ApplyImportIndex(context.Background(), "refs/heads/main", from, target, map[string][]byte{"draft*.md": []byte("remote\n")}, nil); err != nil {
		t.Fatal(err)
	}
	if got := gitOutput(t, repo, "rev-parse", "refs/heads/main"); got != target {
		t.Fatalf("content ref=%s target=%s", got, target)
	}
	if got := gitOutput(t, repo, "rev-parse", ":draft*.md"); got != gitOutput(t, repo, "rev-parse", target+":draft*.md") {
		t.Fatalf("literal path index does not match target: %s", got)
	}
	if got := gitOutput(t, repo, "status", "--porcelain=v1"); got != "" {
		t.Fatalf("unexpected status after import: %q", got)
	}
}

func TestAdvanceContentRefKeepsLateMarkdownEditDirtyWithoutControllerResidue(t *testing.T) {
	repo := initRepository(t)
	from := gitOutput(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("published\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	controllerDir := filepath.Join(repo, ".agent-dispatch-sync", "publications")
	if err := os.MkdirAll(controllerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controllerDir, "publication.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "note.md", ".agent-dispatch-sync/publications/publication.json")
	gitRun(t, repo, "commit", "-m", "published target")
	target := gitOutput(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "reset", "--hard", from)
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("late edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := New(repo, Limits{Timeout: 5 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.AdvanceContentRef(context.Background(), "refs/heads/main", from, target); err != nil {
		t.Fatal(err)
	}
	state, err := client.InspectImport(context.Background(), "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveOperation != "" {
		t.Fatalf("local publication left an active Git operation: %+v", state)
	}
	if len(state.DirtyPaths) != 1 || state.DirtyPaths[0] != "note.md" {
		t.Fatalf("late edit was not preserved as the only dirty path: %+v", state)
	}
	status := gitOutput(t, repo, "status", "--porcelain=v1")
	if !strings.Contains(status, "note.md") || strings.Contains(status, ".agent-dispatch-sync/") {
		t.Fatalf("unexpected status after local content advance: %q", status)
	}
	if raw, err := os.ReadFile(filepath.Join(controllerDir, "publication.json")); err != nil || string(raw) != "{}\n" {
		t.Fatalf("controller record was not materialized: %q err=%v", raw, err)
	}
	if err := client.AdvanceContentRef(context.Background(), "refs/heads/main", from, target); err != nil {
		t.Fatalf("exact after-image retry failed: %v", err)
	}
}

func TestAdvanceContentRefRefusesLocalCollisions(t *testing.T) {
	for _, scenario := range []string{"staged_note", "controller_collision", "tracked_controller_edit", "other_branch", "detached_head"} {
		t.Run(scenario, func(t *testing.T) {
			repo := initRepository(t)
			controllerPath := filepath.Join(repo, ".agent-dispatch-sync", "publications", "publication.json")
			existingControllerPath := filepath.Join(repo, ".agent-dispatch-sync", "publications", "existing.json")
			if err := os.MkdirAll(filepath.Dir(controllerPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(existingControllerPath, []byte("existing record\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			gitRun(t, repo, "add", ".agent-dispatch-sync/publications/existing.json")
			gitRun(t, repo, "commit", "-m", "existing controller")
			from := gitOutput(t, repo, "rev-parse", "HEAD")
			if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("published\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(controllerPath, []byte("published record\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			gitRun(t, repo, "add", "note.md", ".agent-dispatch-sync/publications/publication.json")
			gitRun(t, repo, "commit", "-m", "candidate")
			target := gitOutput(t, repo, "rev-parse", "HEAD")
			gitRun(t, repo, "reset", "--hard", from)
			client, err := New(repo, Limits{Timeout: 5 * time.Second, MaxOutput: 64 << 10})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if err := client.CheckAdvanceContentRef(ctx, "refs/heads/main", from, target); err != nil {
				t.Fatalf("clean candidate failed the pre-push check: %v", err)
			}
			wantNote := ""
			wantController := ""
			switch scenario {
			case "staged_note":
				wantNote = "new staged edit\n"
				if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte(wantNote), 0o600); err != nil {
					t.Fatal(err)
				}
				gitRun(t, repo, "add", "note.md")
			case "controller_collision":
				wantController = "local record\n"
				if err := os.MkdirAll(filepath.Dir(controllerPath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(controllerPath, []byte(wantController), 0o600); err != nil {
					t.Fatal(err)
				}
			case "tracked_controller_edit":
				controllerPath = existingControllerPath
				wantController = "edited existing record\n"
				if err := os.WriteFile(controllerPath, []byte(wantController), 0o600); err != nil {
					t.Fatal(err)
				}
			case "other_branch":
				gitRun(t, repo, "checkout", "-b", "other")
			case "detached_head":
				gitRun(t, repo, "checkout", "--detach", from)
			}
			if err := client.CheckAdvanceContentRef(ctx, "refs/heads/main", from, target); err == nil {
				t.Fatal("unsafe candidate passed the pre-push check")
			}
			indexBefore := gitOutput(t, repo, "write-tree")
			if err := client.AdvanceContentRef(ctx, "refs/heads/main", from, target); err == nil {
				t.Fatal("unsafe candidate advanced local content")
			}
			if got := gitOutput(t, repo, "write-tree"); got != indexBefore {
				t.Fatalf("index changed: %s -> %s", indexBefore, got)
			}
			if got := gitOutput(t, repo, "rev-parse", "refs/heads/main"); got != from {
				t.Fatalf("content ref changed: %s", got)
			}
			if wantNote != "" {
				if raw, err := os.ReadFile(filepath.Join(repo, "note.md")); err != nil || string(raw) != wantNote {
					t.Fatalf("staged note changed: %q %v", raw, err)
				}
			}
			if wantController != "" {
				if raw, err := os.ReadFile(controllerPath); err != nil || string(raw) != wantController {
					t.Fatalf("local controller changed: %q %v", raw, err)
				}
			}
		})
	}
}

func TestImportInspectionIncludesIgnoredCollisionAndCaseAlias(t *testing.T) {
	repo := initRepository(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".gitignore")
	gitRun(t, repo, "commit", "-m", "ignore local files")
	if err := os.MkdirAll(filepath.Join(repo, "ignored"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "ignored", "Note.md"), []byte("local only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := New(repo, Limits{Timeout: 5 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.InspectImport(context.Background(), "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	_, collisions := ImportOverlap([]string{"ignored/note.md"}, state.DirtyPaths, state.UntrackedPaths)
	if len(collisions) != 1 || collisions[0] != "ignored/Note.md" {
		t.Fatalf("ignored case alias must be an overwrite collision: %+v", collisions)
	}
}

func TestSignedMembershipUsesPinnedEd25519AndClosedTree(t *testing.T) {
	repo := initRepository(t)
	marker := filepath.Join(t.TempDir(), "hostile-ran")
	hostile := filepath.Join(t.TempDir(), "hostile")
	if err := os.WriteFile(hostile, []byte("#!/bin/sh\n: > "+marker+"\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "config", "gpg.ssh.program", hostile)
	gitRun(t, repo, "config", "core.hooksPath", filepath.Dir(hostile))
	key := filepath.Join(t.TempDir(), "admin")
	cmd := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	privateKey, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	fingerprintFields := strings.Fields(commandOutput(t, "ssh-keygen", "-lf", key+".pub", "-E", "sha256"))
	if len(fingerprintFields) < 2 {
		t.Fatal("ssh-keygen did not print a fingerprint")
	}
	client, err := New(repo, Limits{Timeout: 10 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	oid, err := client.CreateSignedMembershipCommit(context.Background(), []byte(`{"membership":true}`), []byte(`{"plan":true}`), privateKey, "", time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("hostile Git program or hook ran: %v", err)
	}
	if err := client.VerifySSHSignature(context.Background(), oid, fingerprintFields[1]); err != nil {
		t.Fatal(err)
	}
	if err := client.VerifySSHSignature(context.Background(), oid, "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("unpinned signature accepted: %v", err)
	}
	document, plan, err := client.ReadMembershipCommit(context.Background(), oid)
	if err != nil {
		t.Fatal(err)
	}
	if string(document) != `{"membership":true}` || string(plan) != `{"plan":true}` {
		t.Fatalf("wrong membership tree: %s %s", document, plan)
	}
}

func TestSignedContentSnapshotPreservesLateWorkingTreeEdit(t *testing.T) {
	repo := initRepository(t)
	base := gitOutput(t, repo, "rev-parse", "HEAD")
	key := filepath.Join(t.TempDir(), "publisher")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	privateKey, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := strings.Fields(commandOutput(t, "ssh-keygen", "-lf", key+".pub", "-E", "sha256"))[1]
	client, err := New(repo, Limits{Timeout: 10 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := client.SnapshotTreeChanges(context.Background(), base, map[string][]byte{"note.md": []byte("frozen\n"), ".agent-dispatch-sync/publications/publication-a.json": []byte("{}")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("late\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate, err := client.CreateSignedContentCommit(context.Background(), tree, privateKey, base, time.Unix(1_700_000_001, 0), "publisher", "Publish publication-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.VerifySSHSignature(context.Background(), candidate, fingerprint); err != nil {
		t.Fatal(err)
	}
	raw, err := client.ReadFile(context.Background(), candidate, "note.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "frozen\n" {
		t.Fatalf("candidate captured %q", raw)
	}
	live, err := os.ReadFile(filepath.Join(repo, "note.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(live) != "late\n" {
		t.Fatalf("working tree overwritten: %q", live)
	}
}

func TestRemoteBindingRejectsSeparatePushURLBeforeNetwork(t *testing.T) {
	repo := initRepository(t)
	gitRun(t, repo, "remote", "add", "origin", "ssh://git@example.com/repo")
	gitRun(t, repo, "remote", "set-url", "--add", "--push", "origin", "ssh://git@evil.example/repo")
	digest, _, err := config.RemoteRepositoryDigest("ssh://git@example.com/repo")
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(repo, Limits{Timeout: 2 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.RemoteRef(context.Background(), "origin", "refs/heads/main", digest)
	if err == nil || !strings.Contains(err.Error(), "identical fetch and push URL") {
		t.Fatalf("hostile push URL accepted: %v", err)
	}
}

func TestRemoteBindingRejectsRepositoryURLRewrites(t *testing.T) {
	repo := initRepository(t)
	gitRun(t, repo, "remote", "add", "origin", "ssh://git@example.com/repo")
	gitRun(t, repo, "config", "url.ssh://git@mirror.example/.insteadOf", "ssh://git@example.com/")
	gitRun(t, repo, "config", "url.ssh://git@evil.example/.insteadOf", "ssh://git@mirror.example/")
	digest, _, err := config.RemoteRepositoryDigest("ssh://git@mirror.example/repo")
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(repo, Limits{Timeout: 2 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RemoteRef(context.Background(), "origin", "refs/heads/main", digest); err == nil || !strings.Contains(err.Error(), "URL rewrite") {
		t.Fatalf("repository-local URL rewrite was accepted: %v", err)
	}
}

func TestRemoteBindingRejectsRepositoryHTTPTransportOverrides(t *testing.T) {
	repo := initRepository(t)
	gitRun(t, repo, "remote", "add", "origin", "https://example.com/repo.git")
	gitRun(t, repo, "config", "http.https://example.com/.sslVerify", "false")
	digest, _, err := config.RemoteRepositoryDigest("https://example.com/repo.git")
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(repo, Limits{Timeout: 2 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RemoteRef(context.Background(), "origin", "refs/heads/main", digest); err == nil || !errors.Is(err, ErrRemoteBinding) || !strings.Contains(err.Error(), "HTTP transport override") {
		t.Fatalf("repository HTTP transport override was accepted: %v", err)
	}
}

func TestRemoteBindingRejectsURLRewriteKeyContainingSpace(t *testing.T) {
	repo := initRepository(t)
	gitRun(t, repo, "remote", "add", "origin", "ssh://git@example.com/repo")
	gitRun(t, repo, "config", "url.ssh://git@evil.example/a b.insteadOf", "ssh://git@example.com/")
	digest, _, err := config.RemoteRepositoryDigest("ssh://git@example.com/repo")
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(repo, Limits{Timeout: 2 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RemoteRef(context.Background(), "origin", "refs/heads/main", digest); err == nil || !strings.Contains(err.Error(), "URL rewrite") {
		t.Fatalf("URL rewrite with whitespace in its key was accepted: %v", err)
	}
}

func TestRemoteBindingRejectsWorktreePushURLRewrite(t *testing.T) {
	repo := initRepository(t)
	gitRun(t, repo, "config", "extensions.worktreeConfig", "true")
	gitRun(t, repo, "remote", "add", "origin", "ssh://git@example.com/repo")
	gitRun(t, repo, "config", "--worktree", "url.ssh://git@evil.example/.pushInsteadOf", "ssh://git@example.com/")
	digest, _, err := config.RemoteRepositoryDigest("ssh://git@example.com/repo")
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(repo, Limits{Timeout: 2 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RemoteRef(context.Background(), "origin", "refs/heads/main", digest); err == nil || !strings.Contains(err.Error(), "URL rewrite") {
		t.Fatalf("worktree pushInsteadOf rewrite was accepted: %v", err)
	}
}

func TestPushPreservesRemoteBindingFailure(t *testing.T) {
	repo := initRepository(t)
	gitRun(t, repo, "remote", "add", "origin", "ssh://git@example.com/repo")
	gitRun(t, repo, "config", "url.ssh://git@evil.example/.insteadOf", "ssh://git@example.com/")
	digest, _, err := config.RemoteRepositoryDigest("ssh://git@example.com/repo")
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(repo, Limits{Timeout: 2 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	head := gitOutput(t, repo, "rev-parse", "HEAD")
	result := client.PushFastForward(context.Background(), "origin", "refs/agent-dispatch/content/wiki-pair", head, head, digest)
	if !errors.Is(result.Underlying, ErrRemoteBinding) {
		t.Fatalf("push binding error = %+v", result)
	}
	if result.State != PushRejected {
		t.Fatalf("binding failure state=%s want=%s", result.State, PushRejected)
	}
}

func TestUpdateRefExpectedRejectsSymbolicRef(t *testing.T) {
	repo := initRepository(t)
	head := gitOutput(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "symbolic-ref", "refs/heads/sync", "refs/heads/main")
	client, err := New(repo, Limits{Timeout: 2 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.UpdateRefExpected(context.Background(), "refs/heads/sync", head, head); err == nil || !strings.Contains(err.Error(), "symbolic ref") {
		t.Fatalf("symbolic expected-old ref was accepted: %v", err)
	}
}

func TestUpdateRefExpectedCreatesMissingRef(t *testing.T) {
	repo := initRepository(t)
	head := gitOutput(t, repo, "rev-parse", "HEAD")
	client, err := New(repo, Limits{Timeout: 2 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	const ref = "refs/agent-dispatch/membership/wiki-pair"
	if err := client.UpdateRefExpected(context.Background(), ref, head, ""); err != nil {
		t.Fatalf("create missing ref: %v", err)
	}
	if got := gitOutput(t, repo, "rev-parse", "--verify", ref); got != head {
		t.Fatalf("created ref = %s, want %s", got, head)
	}
}

func TestRestrictedRunnerBoundsOutputAndKillsProcessGroup(t *testing.T) {
	repo := initRepository(t)
	script := filepath.Join(t.TempDir(), "fake-git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncase \"$*\" in\n  *flood*) yes x | head -c 8192;;\n  *) sleep 10 & wait;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	client := &Client{root: repo, git: script, ssh: "/usr/bin/ssh", sshKeygen: "/usr/bin/ssh-keygen", timeout: 30 * time.Second, maxOutput: 1024}
	if _, _, err := client.run(context.Background(), nil, nil, "flood"); !errors.Is(err, ErrOutputBound) {
		t.Fatalf("output bound: %v", err)
	}
	client.timeout = 150 * time.Millisecond
	started := time.Now()
	if _, _, err := client.run(context.Background(), nil, nil, "sleep"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("timeout: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("process group cleanup took %s", elapsed)
	}
}

func TestPushTimeoutWithUnprovableRemoteHeadIsAmbiguous(t *testing.T) {
	repo := initRepository(t)
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	script := filepath.Join(dir, "fake-git")
	expected := "1111111111111111111111111111111111111111"
	candidate := "2222222222222222222222222222222222222222"
	body := "#!/bin/sh\ncase \"$*\" in\n" +
		"  *config*--name-only*--get-regexp*) exit 1;;\n" +
		"  *\"remote get-url --all origin\"*) echo ssh://git@example.com/repo;;\n" +
		"  *\"remote get-url --push --all origin\"*) echo ssh://git@example.com/repo;;\n" +
		"  *ls-remote*\"refs/agent-dispatch/membership/wiki-pair\"*) if test -f " + count + "; then exit 1; else : > " + count + "; echo '" + expected + " refs/agent-dispatch/membership/wiki-pair'; fi;;\n" +
		"  *push*--porcelain*) exit 1;;\n" +
		"  *) exit 2;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, _, err := config.RemoteRepositoryDigest("ssh://git@example.com/repo")
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{root: repo, git: script, ssh: "/usr/bin/ssh", sshKeygen: "/usr/bin/ssh-keygen", timeout: 10 * time.Second, maxOutput: 64 << 10}
	result := client.PushFastForward(context.Background(), "origin", "refs/agent-dispatch/membership/wiki-pair", candidate, expected, digest)
	if result.State != "ambiguous" || !errors.Is(result.Underlying, ErrPushAmbiguous) {
		t.Fatalf("push result=%+v", result)
	}
}

func TestPushRemoteMeasurementFailureDoesNotStartPush(t *testing.T) {
	repo := initRepository(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-git")
	expected := "1111111111111111111111111111111111111111"
	candidate := "2222222222222222222222222222222222222222"
	body := "#!/bin/sh\ncase \"$*\" in\n" +
		"  *config*--name-only*--get-regexp*) exit 1;;\n" +
		"  *\"remote get-url --all origin\"*) echo ssh://git@example.com/repo;;\n" +
		"  *\"remote get-url --push --all origin\"*) echo ssh://git@example.com/repo;;\n" +
		"  *ls-remote*) exit 2;;\n" +
		"  *push*) exit 99;;\n" +
		"  *) exit 2;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, _, err := config.RemoteRepositoryDigest("ssh://git@example.com/repo")
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{root: repo, git: script, ssh: "/usr/bin/ssh", sshKeygen: "/usr/bin/ssh-keygen", timeout: 10 * time.Second, maxOutput: 64 << 10}
	result := client.PushFastForward(context.Background(), "origin", "refs/agent-dispatch/content/wiki-pair", candidate, expected, digest)
	if result.State != PushNotStarted || result.Underlying == nil {
		t.Fatalf("push result=%+v", result)
	}
}

func TestPushAlreadyAtCandidateIsConfirmed(t *testing.T) {
	repo := initRepository(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-git")
	expected := "1111111111111111111111111111111111111111"
	candidate := "2222222222222222222222222222222222222222"
	body := "#!/bin/sh\ncase \"$*\" in\n" +
		"  *config*--name-only*--get-regexp*) exit 1;;\n" +
		"  *\"remote get-url --all origin\"*) echo ssh://git@example.com/repo;;\n" +
		"  *\"remote get-url --push --all origin\"*) echo ssh://git@example.com/repo;;\n" +
		"  *ls-remote*) echo '" + candidate + " refs/agent-dispatch/content/wiki-pair';;\n" +
		"  *push*) exit 99;;\n" +
		"  *) exit 2;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, _, err := config.RemoteRepositoryDigest("ssh://git@example.com/repo")
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{root: repo, git: script, ssh: "/usr/bin/ssh", sshKeygen: "/usr/bin/ssh-keygen", timeout: 10 * time.Second, maxOutput: 64 << 10}
	result := client.PushFastForward(context.Background(), "origin", "refs/agent-dispatch/content/wiki-pair", candidate, expected, digest)
	if result.State != PushConfirmed || result.RemoteOID != candidate {
		t.Fatalf("push result=%+v", result)
	}
}

func TestG17TwoWritersPreserveBothHistoriesAfterFastForwardLoss(t *testing.T) {
	repo := initRepository(t)
	base := gitOutput(t, repo, "rev-parse", "HEAD")
	key := filepath.Join(t.TempDir(), "publisher")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	privateKey, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	real, err := New(repo, Limits{Timeout: 10 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	makeCandidate := func(body, manifest, message string, when time.Time) string {
		t.Helper()
		tree, err := real.SnapshotTreeChanges(context.Background(), base, map[string][]byte{
			"note.md": []byte(body),
			".agent-dispatch-sync/publications/" + manifest + ".json": []byte("{}"),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := real.CreateSignedContentCommit(context.Background(), tree, privateKey, base, when, "publisher", message)
		if err != nil {
			t.Fatal(err)
		}
		return candidate
	}
	winner := makeCandidate("writer-a\n", "publication-a", "Publish A", time.Unix(1_700_000_001, 0))
	loser := makeCandidate("writer-b\n", "publication-b", "Publish B", time.Unix(1_700_000_002, 0))
	if winner == loser {
		t.Fatal("independent writers produced the same candidate")
	}

	dir := t.TempDir()
	remoteHead := filepath.Join(dir, "remote-head")
	if err := os.WriteFile(remoteHead, []byte(base+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	remoteURL := "ssh://git@example.com/repo"
	ref := "refs/agent-dispatch/content/wiki-pair"
	script := filepath.Join(dir, "git")
	body := fmt.Sprintf(`#!/bin/sh
case " $* " in
  *config*--name-only*--get-regexp*) exit 1;;
  *" remote get-url --all origin "*) echo %s; exit 0;;
  *" remote get-url --push --all origin "*) echo %s; exit 0;;
  *" ls-remote "*) oid=$(sed -n '1p' %s); printf '%%s\t%s\n' "$oid"; exit 0;;
  *" push "*) for last do :; done; oid=${last%%%%:*}; printf '%%s\n' "$oid" > %s; exit 0;;
esac
exit 2
`, remoteURL, remoteURL, remoteHead, ref, remoteHead)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Fatal(err)
	}
	sshKeygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{root: repo, git: script, ssh: ssh, sshKeygen: sshKeygen, timeout: 10 * time.Second, maxOutput: 64 << 10}
	digest, _, err := config.RemoteRepositoryDigest(remoteURL)
	if err != nil {
		t.Fatal(err)
	}
	if result := client.PushFastForward(context.Background(), "origin", ref, winner, base, digest); result.State != PushConfirmed {
		t.Fatalf("winner push = %+v", result)
	}
	result := client.PushFastForward(context.Background(), "origin", ref, loser, base, digest)
	if result.State != PushRejected || result.RemoteOID != winner || !errors.Is(result.Underlying, ErrPushRejected) {
		t.Fatalf("losing push = %+v", result)
	}
	for name, candidate := range map[string]string{"winner": winner, "loser": loser} {
		if got := gitOutput(t, repo, "cat-file", "-t", candidate); got != "commit" {
			t.Fatalf("%s candidate was not preserved: %q", name, got)
		}
		parents, err := real.CommitParents(context.Background(), candidate)
		if err != nil || len(parents) != 1 || parents[0] != base {
			t.Fatalf("%s history = %v, %v", name, parents, err)
		}
	}
}

func TestReadContentFilesRejectsUnknownControllerPath(t *testing.T) {
	repo := initRepository(t)
	if err := os.MkdirAll(filepath.Join(repo, ".agent-dispatch-sync", "unknown"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".agent-dispatch-sync", "unknown", "payload.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".agent-dispatch-sync/unknown/payload.json")
	gitRun(t, repo, "commit", "-q", "-m", "unknown controller")
	client, err := New(repo, Limits{Timeout: 5 * time.Second, MaxOutput: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	head := gitOutput(t, repo, "rev-parse", "HEAD")
	if _, err := client.ReadContentFiles(context.Background(), head); err == nil || !strings.Contains(err.Error(), "unsupported controller path") {
		t.Fatalf("unknown controller path accepted: %v", err)
	}
}

func TestFetchRewriteClassificationDoesNotConfuseTransportFailure(t *testing.T) {
	if !fetchRejectedRewrite([]byte(" ! [rejected] main -> tracking (non-fast-forward)")) {
		t.Fatal("non-fast-forward fetch rejection must be classified as a history rewrite")
	}
	if fetchRejectedRewrite([]byte("fatal: Could not resolve hostname example.invalid")) {
		t.Fatal("transport failure must remain retryable and must not become a trust hold")
	}
	if !fetchMissingRef([]byte("fatal: couldn't find remote ref refs/heads/wiki-sync")) {
		t.Fatal("deleted configured ref must be classified separately from transport failure")
	}
	if fetchMissingRef([]byte("fatal: Could not resolve hostname example.invalid")) {
		t.Fatal("offline remote must not look like a deleted ref")
	}
}

func initRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "config", "user.name", "Test")
	gitRun(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "note.md")
	gitRun(t, repo, "commit", "-q", "-m", "first")
	return repo
}

func gitRun(t *testing.T, repo string, args ...string) { t.Helper(); _ = gitOutput(t, repo, args...) }
func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", repo}, args...)
	return commandOutput(t, "git", full...)
}
func commandOutput(t *testing.T, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_AUTHOR_DATE=1700000000 +0000", "GIT_COMMITTER_DATE=1700000000 +0000")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

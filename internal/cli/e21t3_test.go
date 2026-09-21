package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func TestE21T3PublishSignsFrozenSnapshotAndAdmitsDelivery(t *testing.T) {
	cfg, err := config.Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "vault")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, realGit, repo, "init", "-q", "-b", "main")
	gitTestRun(t, realGit, repo, "config", "user.name", "Test")
	gitTestRun(t, realGit, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, realGit, repo, "add", "note.md")
	gitTestRun(t, realGit, repo, "commit", "-q", "-m", "base")
	base := gitTestOutput(t, realGit, repo, "rev-parse", "HEAD")
	adminKey, adminFingerprint := e21t3Key(t, dir, "admin")
	publisherKey, publisherFingerprint := e21t3Key(t, dir, "publisher")
	remoteURL := "ssh://git@example.com/repo"
	remoteDigest, _, err := config.RemoteRepositoryDigest(remoteURL)
	if err != nil {
		t.Fatal(err)
	}
	resource := cfg.Resources[cfg.Sync.Resource]
	resource.Root = repo
	cfg.Resources[cfg.Sync.Resource] = resource
	cfg.Instance.StateDir = filepath.Join(dir, "state")
	cfg.Sync.Enabled = true
	cfg.Sync.AdministratorKey = adminFingerprint
	cfg.Sync.AdministratorSigningKeyRef = "env:E21T3_ADMIN"
	cfg.Sync.PublisherSigningKeyRef = "env:E21T3_PUBLISHER"
	cfg.Sync.RemoteRepositoryDigest = remoteDigest
	cfg.Sync.Bounds.SubprocessSeconds = 30
	cfg.Sync.Nodes[0].PublisherKey = publisherFingerprint
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_DISPATCH_CONFIG", configPath)
	t.Setenv("E21T3_ADMIN", string(adminKey))
	t.Setenv("E21T3_PUBLISHER", string(publisherKey))
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	membershipState := filepath.Join(dir, "membership-head")
	contentState := filepath.Join(dir, "content-head")
	if err := os.WriteFile(contentState, []byte(base+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(binDir, "git")
	script := fmt.Sprintf(`#!/bin/sh
case " $* " in
  *" remote get-url --all origin "*) echo %s; exit 0;;
  *" remote get-url --push --all origin "*) echo %s; exit 0;;
  *" ls-remote "*)
    for last do :; done
    case "$last" in
      %s) state=%s;;
      %s) state=%s;;
      *) exit 0;;
    esac
    if test -s "$state"; then oid=$(sed -n '1p' "$state"); printf '%%s\t%%s\n' "$oid" "$last"; fi
    exit 0;;
  *" push "*)
    for last do :; done
    oid=${last%%%%:*}; ref=${last#*:}
    case "$ref" in
      %s) state=%s;;
      %s) state=%s;;
      *) exit 2;;
    esac
    printf '%%s\n' "$oid" > "$state"; exit 0;;
esac
exec %s "$@"
`, remoteURL, remoteURL, cfg.Sync.MembershipRef, membershipState, cfg.Sync.ContentRef, contentState, cfg.Sync.MembershipRef, membershipState, cfg.Sync.ContentRef, contentState, realGit)
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	gitTestRun(t, realGit, repo, "update-ref", cfg.Sync.ContentRef, base)
	var planOut, planErr bytes.Buffer
	if code := Run([]string{"sync", "membership", "plan", "--group", cfg.Sync.GroupID, "--change", "bootstrap", "--output", "json"}, &planOut, &planErr); code != 0 {
		t.Fatalf("membership plan: %d %s", code, planErr.String())
	}
	var planEnvelope struct {
		Result struct {
			Plan syncrecords.MembershipPlan `json:"plan"`
		} `json:"result"`
	}
	if err := json.Unmarshal(planOut.Bytes(), &planEnvelope); err != nil {
		t.Fatal(err)
	}
	planRaw, _ := json.Marshal(planEnvelope.Result.Plan)
	planPath := filepath.Join(dir, "membership-plan.json")
	if err := os.WriteFile(planPath, planRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	var applyOut, applyErr bytes.Buffer
	if code := Run([]string{"sync", "membership", "apply", "--group", cfg.Sync.GroupID, "--plan", planPath, "--expected-membership-predecessor", "none", "--output", "json"}, &applyOut, &applyErr); code != 0 {
		t.Fatalf("membership apply: %d %s", code, applyErr.String())
	}
	var checkpointPlanOut, checkpointPlanErr bytes.Buffer
	if code := Run([]string{"sync", "checkpoint", "plan", "--group", cfg.Sync.GroupID, "--target-commit", base, "--kind", "initial_baseline", "--output", "json"}, &checkpointPlanOut, &checkpointPlanErr); code != 0 {
		t.Fatalf("checkpoint plan: %d %s", code, checkpointPlanErr.String())
	}
	var checkpointEnvelope struct {
		Result struct {
			Plan syncrecords.CheckpointPlan `json:"plan"`
		} `json:"result"`
	}
	if err := json.Unmarshal(checkpointPlanOut.Bytes(), &checkpointEnvelope); err != nil {
		t.Fatal(err)
	}
	checkpointRaw, _ := json.Marshal(checkpointEnvelope.Result.Plan)
	checkpointPath := filepath.Join(dir, "checkpoint-plan.json")
	if err := os.WriteFile(checkpointPath, checkpointRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	var checkpointOut, checkpointErr bytes.Buffer
	if code := Run([]string{"sync", "checkpoint", "apply", "--group", cfg.Sync.GroupID, "--plan", checkpointPath, "--output", "json"}, &checkpointOut, &checkpointErr); code != 0 {
		t.Fatalf("checkpoint apply: %d %s", code, checkpointErr.String())
	}
	seedE21T3Eligibility(t, cfg, configPath, []byte("changed\n"))
	revision, _ := config.SyncRevision(cfg)
	var publishOut, publishErr bytes.Buffer
	if code := Run([]string{"sync", "publish", "--group", cfg.Sync.GroupID, "--expected-config-revision", revision, "--output", "json"}, &publishOut, &publishErr); code != 0 {
		t.Fatalf("publish: %d %s", code, publishErr.String())
	}
	var published struct {
		Result struct {
			State           string `json:"state"`
			PublicationID   string `json:"publication_id"`
			CandidateCommit string `json:"candidate_commit"`
		} `json:"result"`
	}
	if err := json.Unmarshal(publishOut.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	if published.Result.State != "published" || published.Result.PublicationID == "" || published.Result.CandidateCommit == "" {
		t.Fatalf("result=%s", publishOut.String())
	}
	if got := strings.TrimSpace(string(mustRead(t, contentState))); got != published.Result.CandidateCommit {
		t.Fatalf("remote=%s candidate=%s", got, published.Result.CandidateCommit)
	}
	candidateRaw := gitTestOutput(t, realGit, repo, "show", published.Result.CandidateCommit+":note.md")
	if candidateRaw != "changed" {
		t.Fatalf("candidate note=%q", candidateRaw)
	}
	store, err := openStateStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var deliveries int
	if err := store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='delivery' AND state='pending'`).Scan(&deliveries); err != nil || deliveries != 1 {
		t.Fatalf("deliveries=%d err=%v", deliveries, err)
	}
	var noOpOut, noOpErr bytes.Buffer
	if code := Run([]string{"sync", "publish", "--group", cfg.Sync.GroupID, "--expected-config-revision", revision, "--output", "json"}, &noOpOut, &noOpErr); code != 0 || !bytes.Contains(noOpOut.Bytes(), []byte(`"state":"no_content_change"`)) {
		t.Fatalf("no-op: %d out=%s err=%s", code, noOpOut.String(), noOpErr.String())
	}
}

func seedE21T3Eligibility(t *testing.T, cfg *config.Config, configPath string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cfg.Resources[cfg.Sync.Resource].Root, "note.md"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := openStateStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	resource := cfg.Resources[cfg.Sync.Resource]
	if err := store.RegisterResource(nil, cfg.Sync.Resource, "resource-e21t3", resource.Root, resource.Root, resource.FileScope, resource.Git.Mode); err != nil {
		t.Fatal(err)
	}
	for routeID, route := range cfg.Routes {
		if route.Source.Resource != cfg.Sync.Resource {
			continue
		}
		routeRevision, _ := config.RouteRevision(cfg, routeID)
		if err := store.RegisterRoute(nil, routeID, routeRevision, config.PolicyRevision(route), cfg.Sync.Resource, route.Destinations[0].Target, "{}", "2026-09-21T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if err := store.InitializeRouteState(nil, routeID); err != nil {
			t.Fatal(err)
		}
		if err := store.SetRouteActivation(requestCtx(), routeID, "enabled", routeRevision, "", "2026-09-21T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
		decisionID := "decision-" + routeID
		dispatchID := "dispatch-" + routeID
		if err := store.SaveDecision(nil, sqlite.DecisionRecord{DecisionID: decisionID, RouteID: routeID, RouteRevision: routeRevision, PolicyRevision: config.PolicyRevision(route), GenerationLineageJSON: `{"generation":1}`, Disposition: "dispatch", Classification: "normal", ReasonCodesJSON: "[]", CreatedAt: "2026-09-21T00:00:00Z", Actor: "test"}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveIntent(nil, sqlite.IntentRecord{DispatchID: dispatchID, DecisionID: decisionID, RouteID: routeID, RouteRevision: routeRevision, TargetID: route.Destinations[0].Target, TargetType: "hermes-kanban", TargetScope: route.Destinations[0].ID, ResourceID: cfg.Sync.Resource, Generation: 1, IdempotencyKey: "idem-" + routeID, ContentFingerprint: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ManifestDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", RequestVersion: "v1", RequestJSON: "{}", CreatedAt: "2026-09-21T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveWorkReceipt(nil, sqlite.WorkReceiptRecord{ReceiptID: "receipt-" + routeID, DispatchID: dispatchID, RunID: "run-" + routeID, ResourceID: cfg.Sync.Resource, Status: "completed", ChangesJSON: "[]", CompletedScopeJSON: "[]", RemainingScopeJSON: "[]", SubmittedAt: "2026-09-21T00:01:00Z", ValidationState: "valid", ValidationReasonsJSON: "[]", BegunAt: "2026-09-21T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Exec(`UPDATE route_runtime_state SET route_state='IDLE',active_dispatch_id=NULL,pending_reconcile=0 WHERE route_id=?`, routeID); err != nil {
			t.Fatal(err)
		}
	}
	sum := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if _, err := store.Exec(`UPDATE resources SET observation_revision=1 WHERE resource_id=?`, cfg.Sync.Resource); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`INSERT INTO path_facts(resource_id,path,digest,"exists",observed_at) VALUES(?,?,?,1,'2026-09-21T00:01:00Z')`, cfg.Sync.Resource, "note.md", digest); err != nil {
		t.Fatal(err)
	}
}
func e21t3Key(t *testing.T, dir, name string) ([]byte, string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", path).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v %s", err, out)
	}
	private := mustRead(t, path)
	fields := strings.Fields(string(commandBytes(t, "ssh-keygen", "-lf", path+".pub", "-E", "sha256")))
	return private, fields[1]
}
func gitTestRun(t *testing.T, git, repo string, args ...string) {
	t.Helper()
	_ = gitTestOutput(t, git, repo, args...)
}
func gitTestOutput(t *testing.T, git, repo string, args ...string) string {
	t.Helper()
	all := append([]string{"-C", repo}, args...)
	return strings.TrimSpace(string(commandBytes(t, git, all...)))
}
func commandBytes(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_AUTHOR_DATE=1700000000 +0000", "GIT_COMMITTER_DATE=1700000000 +0000")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, out)
	}
	return out
}
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

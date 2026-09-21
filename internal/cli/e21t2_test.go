package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func TestE21T2MembershipBootstrapPlanIsSideEffectFree(t *testing.T) {
	cfg, err := config.Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "vault")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo, "init", "-q", "-b", "main")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	resource := cfg.Resources[cfg.Sync.Resource]
	resource.Root = repo
	cfg.Resources[cfg.Sync.Resource] = resource
	cfg.Instance.StateDir = filepath.Join(dir, "state")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_DISPATCH_CONFIG", configPath)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"sync", "membership", "plan", "--group", cfg.Sync.GroupID, "--change", "bootstrap", "--output", "json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("plan code=%d stderr=%s", code, stderr.String())
	}
	var envelope struct {
		Result struct {
			Plan        syncrecords.MembershipPlan `json:"plan"`
			SideEffects []string                   `json:"side_effects"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Result.Plan.ChangeKind != "bootstrap" || envelope.Result.Plan.PlanID == "" || len(envelope.Result.SideEffects) != 0 {
		t.Fatalf("unexpected plan result: %+v", envelope.Result)
	}
	if _, err := os.Stat(cfg.Instance.StateDir); !os.IsNotExist(err) {
		t.Fatalf("plan created local state: %v", err)
	}
	status := exec.Command("git", "-C", repo, "status", "--porcelain")
	if out, err := status.CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("plan changed repository: %v %q", err, out)
	}
}

func TestE21T2MembershipFlagsRequireExplicitTargetAndReviewedPredecessor(t *testing.T) {
	for _, args := range [][]string{
		{"sync", "membership", "plan", "--group", "wiki-pair", "--change", "retirement", "--output", "json"},
		{"sync", "membership", "plan", "--group", "wiki-pair", "--change", "bootstrap", "--instance", "node-a", "--output", "json"},
		{"sync", "membership", "apply", "--group", "wiki-pair", "--plan", "plan.json", "--output", "json"},
	} {
		var stdout, stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr); code != 2 || stdout.Len() != 0 {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestE21T2MembershipApplySignsPushesAndCommitsDurableEvidence(t *testing.T) {
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
	if out, err := exec.Command(realGit, "-C", repo, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	key := filepath.Join(dir, "admin")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	privateKey, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	fingerprintOut, err := exec.Command("ssh-keygen", "-lf", key+".pub", "-E", "sha256").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	fingerprintFields := strings.Fields(string(fingerprintOut))
	if len(fingerprintFields) < 2 {
		t.Fatal("missing administrator fingerprint")
	}
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
	cfg.Sync.AdministratorKey = fingerprintFields[1]
	cfg.Sync.AdministratorSigningKeyRef = "env:E21_ADMIN_KEY"
	cfg.Sync.RemoteRepositoryDigest = remoteDigest
	cfg.Sync.Bounds.SubprocessSeconds = 30
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_DISPATCH_CONFIG", configPath)
	t.Setenv("E21_ADMIN_KEY", string(privateKey))

	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	remoteState := filepath.Join(dir, "remote-head")
	wrapper := filepath.Join(binDir, "git")
	script := fmt.Sprintf(`#!/bin/sh
case " $* " in
  *" remote get-url --all origin "*) echo %s; exit 0;;
  *" remote get-url --push --all origin "*) echo %s; exit 0;;
  *" ls-remote "*)
    if test -s %s; then oid=$(sed -n '1p' %s); printf '%%s\t%%s\n' "$oid" 'refs/agent-dispatch/membership/wiki-pair'; fi
    exit 0;;
  *" push "*)
    for last do :; done
    oid=${last%%%%:*}
    printf '%%s\n' "$oid" > %s
    exit 0;;
esac
exec %s "$@"
`, remoteURL, remoteURL, remoteState, remoteState, remoteState, realGit)
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var planOut, planErr bytes.Buffer
	if code := Run([]string{"sync", "membership", "plan", "--group", cfg.Sync.GroupID, "--change", "bootstrap", "--output", "json"}, &planOut, &planErr); code != 0 {
		t.Fatalf("plan code=%d stderr=%s", code, planErr.String())
	}
	var planned struct {
		Result struct {
			Plan syncrecords.MembershipPlan `json:"plan"`
		} `json:"result"`
	}
	if err := json.Unmarshal(planOut.Bytes(), &planned); err != nil {
		t.Fatal(err)
	}
	planRaw, err := json.Marshal(planned.Result.Plan)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(dir, "membership-plan.json")
	if err := os.WriteFile(planPath, planRaw, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"sync", "membership", "apply", "--group", cfg.Sync.GroupID, "--plan", planPath, "--expected-membership-predecessor", "none", "--output", "json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("apply code=%d stderr=%s", code, stderr.String())
	}
	if bytes.Contains(stdout.Bytes(), privateKey) || bytes.Contains(stderr.Bytes(), privateKey) {
		t.Fatal("private key leaked to command output")
	}
	var applied struct {
		Result struct {
			State              string `json:"state"`
			MembershipRevision string `json:"membership_revision"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	if applied.Result.State != "applied" || len(applied.Result.MembershipRevision) != 40 {
		t.Fatalf("apply result=%+v", applied.Result)
	}
	localHead, err := exec.Command(realGit, "-C", repo, "rev-parse", cfg.Sync.MembershipRef).CombinedOutput()
	if err != nil || strings.TrimSpace(string(localHead)) != applied.Result.MembershipRevision {
		t.Fatalf("local membership ref=%q err=%v", localHead, err)
	}
	database, err := os.ReadFile(filepath.Join(cfg.Instance.StateDir, StateDBName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(database, privateKey) {
		t.Fatal("private key leaked to durable state")
	}
}

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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func TestE21T3PreparedJournalReusesDeterministicCommitInputs(t *testing.T) {
	wantTime := time.Date(2026, 9, 21, 1, 2, 3, 0, time.UTC)
	entries := []sqlite.SyncJournalEntry{
		{EvidenceJSON: `{}`},
		{EvidenceJSON: `{"tree":"1111111111111111111111111111111111111111","commit_time":"2026-09-21T01:02:03Z"}`},
		{EvidenceJSON: `{"tree":"bad","commit_time":"not-a-time"}`},
	}
	tree, commitTime := journalPreparedCandidate(entries)
	if tree != "1111111111111111111111111111111111111111" || !commitTime.Equal(wantTime) {
		t.Fatalf("prepared candidate = %q %s", tree, commitTime)
	}
}

func TestE21MembershipApplyExitCodeMatchesDurableControl(t *testing.T) {
	tests := []struct {
		name          string
		candidateMode string
		control       sqlite.SyncControlRow
		want          int
	}{
		{name: "normal active", candidateMode: "normal", control: sqlite.SyncControlRow{State: "active", Reason: "none", MembershipMode: "normal"}, want: 0},
		{name: "normal preserves conflict", candidateMode: "normal", control: sqlite.SyncControlRow{State: "blocked", Reason: "conflict", MembershipMode: "normal"}, want: 14},
		{name: "normal preserves trust hold", candidateMode: "normal", control: sqlite.SyncControlRow{State: "blocked", Reason: "trust_failure", MembershipMode: "normal"}, want: 30},
		{name: "emergency remains trust exit", candidateMode: "blocked_emergency", control: sqlite.SyncControlRow{State: "paused", Reason: "operator_pause", MembershipMode: "blocked_emergency"}, want: 30},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := membershipApplyExitCode(test.candidateMode, test.control, 0); got != test.want {
				t.Fatalf("exit=%d want=%d", got, test.want)
			}
		})
	}
}

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
	pushReject := filepath.Join(dir, "push-reject-same-base")
	pushConflict := filepath.Join(dir, "content-push-conflict")
	conflictProbe := filepath.Join(dir, "content-push-conflict-probed")
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
      %s) state=%s; conflict=%s; probe=%s;;
      *) exit 0;;
    esac
    source_state="$state"
    if test -n "$conflict" && test -s "$conflict"; then
      if test -e "$probe"; then source_state="$conflict"; else : > "$probe"; fi
    fi
    if test -s "$source_state"; then oid=$(sed -n '1p' "$source_state"); printf '%%s\t%%s\n' "$oid" "$last"; fi
    exit 0;;
  *" fetch "*)
    for last do :; done
    source=${last%%%%:*}; destination=${last#*:}
    case "$source" in
      %s) state=%s;;
      %s) state=%s;;
      *) exit 2;;
    esac
    oid=$(sed -n '1p' "$state")
    exec %s -C %s update-ref "$destination" "$oid";;
  *" push "*)
    for last do :; done
    oid=${last%%%%:*}; ref=${last#*:}
    case "$ref" in
      %s) state=%s;;
      %s) state=%s;;
      *) exit 2;;
    esac
	if test -e '%s'; then exit 1; fi
    printf '%%s\n' "$oid" > "$state"; exit 0;;
esac
exec %s "$@"
`, remoteURL, remoteURL, cfg.Sync.MembershipRef, membershipState, cfg.Sync.ContentRef, contentState, pushConflict, conflictProbe, cfg.Sync.MembershipRef, membershipState, cfg.Sync.ContentRef, contentState, realGit, repo, cfg.Sync.MembershipRef, membershipState, cfg.Sync.ContentRef, contentState, pushReject, realGit)
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
	if err := os.WriteFile(pushReject, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var rejectedMembershipOut, rejectedMembershipErr bytes.Buffer
	if code := Run([]string{"sync", "membership", "apply", "--group", cfg.Sync.GroupID, "--plan", planPath, "--expected-membership-predecessor", "none", "--output", "json"}, &rejectedMembershipOut, &rejectedMembershipErr); code != 10 || !bytes.Contains(rejectedMembershipErr.Bytes(), []byte(`"code":"sync_retryable"`)) {
		t.Fatalf("same-base membership rejection: %d out=%s err=%s", code, rejectedMembershipOut.String(), rejectedMembershipErr.String())
	}
	if err := os.Remove(pushReject); err != nil {
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
	if err := os.WriteFile(pushReject, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var rejectedCheckpointOut, rejectedCheckpointErr bytes.Buffer
	if code := Run([]string{"sync", "checkpoint", "apply", "--group", cfg.Sync.GroupID, "--plan", checkpointPath, "--output", "json"}, &rejectedCheckpointOut, &rejectedCheckpointErr); code != 10 || !bytes.Contains(rejectedCheckpointErr.Bytes(), []byte(`"code":"sync_retryable"`)) {
		t.Fatalf("same-base checkpoint rejection: %d out=%s err=%s", code, rejectedCheckpointOut.String(), rejectedCheckpointErr.String())
	}
	if err := os.Remove(pushReject); err != nil {
		t.Fatal(err)
	}
	var checkpointOut, checkpointErr bytes.Buffer
	if code := Run([]string{"sync", "checkpoint", "apply", "--group", cfg.Sync.GroupID, "--plan", checkpointPath, "--output", "json"}, &checkpointOut, &checkpointErr); code != 0 {
		t.Fatalf("checkpoint apply: %d %s", code, checkpointErr.String())
	}
	prepareE21T3State(t, cfg, configPath)
	revision, _ := config.SyncRevision(cfg)
	contentBeforeIneligible := strings.TrimSpace(string(mustRead(t, contentState)))
	var ineligibleOut, ineligibleErr bytes.Buffer
	if code := Run([]string{"sync", "publish", "--group", cfg.Sync.GroupID, "--expected-config-revision", revision, "--output", "json"}, &ineligibleOut, &ineligibleErr); code != 0 || !bytes.Contains(ineligibleOut.Bytes(), []byte(`"state":"no_eligible_snapshot"`)) {
		t.Fatalf("ineligible publish: %d out=%s err=%s", code, ineligibleOut.String(), ineligibleErr.String())
	}
	ineligibleStore, err := openStateStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var ineligibleJobs int
	if err := ineligibleStore.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='publication'`).Scan(&ineligibleJobs); err != nil {
		ineligibleStore.Close()
		t.Fatal(err)
	}
	ineligibleStore.Close()
	if ineligibleJobs != 0 || strings.TrimSpace(string(mustRead(t, contentState))) != contentBeforeIneligible {
		t.Fatalf("ineligible publish mutated state: jobs=%d remote=%s", ineligibleJobs, strings.TrimSpace(string(mustRead(t, contentState))))
	}
	seedE21T3Eligibility(t, cfg, configPath, []byte("changed\n"))
	if err := os.WriteFile(pushReject, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var rejectedPublishOut, rejectedPublishErr bytes.Buffer
	if code := Run([]string{"sync", "publish", "--group", cfg.Sync.GroupID, "--expected-config-revision", revision, "--output", "json"}, &rejectedPublishOut, &rejectedPublishErr); code != 10 || !bytes.Contains(rejectedPublishErr.Bytes(), []byte(`"code":"sync_retryable"`)) {
		t.Fatalf("same-base publication rejection: %d out=%s err=%s", code, rejectedPublishOut.String(), rejectedPublishErr.String())
	}
	if err := os.Remove(pushReject); err != nil {
		t.Fatal(err)
	}
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
	var publicationJobID, publicationPayload string
	if err := store.QueryRow(`SELECT job_id,payload_json FROM sync_jobs WHERE kind='publication'`).Scan(&publicationJobID, &publicationPayload); err != nil {
		t.Fatal(err)
	}
	publicationRecord, err := syncrecords.DecodePublication([]byte(publicationPayload))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`DELETE FROM sync_jobs WHERE kind='delivery'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`UPDATE sync_jobs SET state='signed',retain_until_resolved=1,resolved_at=NULL WHERE job_id=?`, publicationJobID); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, realGit, repo, "update-ref", cfg.Sync.ContentRef, publicationRecord.BaseCommit, published.Result.CandidateCommit)
	var recoveryOut, recoveryErr bytes.Buffer
	if code := Run([]string{"sync", "publish", "--group", cfg.Sync.GroupID, "--expected-config-revision", revision, "--output", "json"}, &recoveryOut, &recoveryErr); code != 0 {
		t.Fatalf("confirmed publication recovery: %d out=%s err=%s", code, recoveryOut.String(), recoveryErr.String())
	}
	var recovered struct {
		Result struct {
			PublicationID   string `json:"publication_id"`
			CandidateCommit string `json:"candidate_commit"`
			Recovered       bool   `json:"recovered"`
			Idempotent      bool   `json:"idempotent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(recoveryOut.Bytes(), &recovered); err != nil {
		t.Fatal(err)
	}
	if !recovered.Result.Recovered || !recovered.Result.Idempotent || recovered.Result.PublicationID != published.Result.PublicationID || recovered.Result.CandidateCommit != published.Result.CandidateCommit {
		t.Fatalf("recovery changed publication identity: %s", recoveryOut.String())
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='delivery' AND state='pending'`).Scan(&deliveries); err != nil || deliveries != 1 {
		t.Fatalf("recovered deliveries=%d err=%v", deliveries, err)
	}
	var noOpOut, noOpErr bytes.Buffer
	if code := Run([]string{"sync", "publish", "--group", cfg.Sync.GroupID, "--expected-config-revision", revision, "--output", "json"}, &noOpOut, &noOpErr); code != 0 || !bytes.Contains(noOpOut.Bytes(), []byte(`"state":"no_content_change"`)) {
		t.Fatalf("no-op: %d out=%s err=%s", code, noOpOut.String(), noOpErr.String())
	}
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("conflict\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	conflictDigest := sha256.Sum256([]byte("conflict\n"))
	if _, err := store.Exec(`UPDATE resources SET observation_revision=observation_revision+1 WHERE resource_id=?`, cfg.Sync.Resource); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`UPDATE path_facts SET digest=?,observed_at='2026-09-21T00:02:00Z' WHERE resource_id=? AND path='note.md'`, "sha256:"+hex.EncodeToString(conflictDigest[:]), cfg.Sync.Resource); err != nil {
		t.Fatal(err)
	}
	alternateTree := gitTestOutput(t, realGit, repo, "rev-parse", published.Result.CandidateCommit+"^{tree}")
	alternate := strings.TrimSpace(string(commandBytesWithInput(t, []byte("alternate\n"), realGit, "-C", repo, "commit-tree", alternateTree, "-p", published.Result.CandidateCommit)))
	if err := os.WriteFile(pushConflict, []byte(alternate+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var conflictOut, conflictErr bytes.Buffer
	if code := Run([]string{"sync", "publish", "--group", cfg.Sync.GroupID, "--expected-config-revision", revision, "--output", "json"}, &conflictOut, &conflictErr); code != 14 {
		t.Fatalf("losing fast-forward: %d out=%s err=%s", code, conflictOut.String(), conflictErr.String())
	}
	control, err := store.LoadSyncControl(requestCtx(), cfg.Sync.GroupID)
	if err != nil || control.State != "blocked" || control.Reason != "conflict" {
		t.Fatalf("losing fast-forward control=%+v err=%v", control, err)
	}
	var blockedJobs int
	if err := store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='publication' AND state='blocked' AND resolved_at IS NULL AND retain_until_resolved=1`).Scan(&blockedJobs); err != nil || blockedJobs != 1 {
		t.Fatalf("blocked publication obligations=%d err=%v", blockedJobs, err)
	}
	var blockedCandidate string
	if err := store.QueryRow(`SELECT json_extract(evidence_json,'$.candidate') FROM sync_journal_entries WHERE job_id=(SELECT job_id FROM sync_jobs WHERE kind='publication' AND state='blocked') AND outcome='signed' ORDER BY recorded_at DESC LIMIT 1`).Scan(&blockedCandidate); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, realGit, repo, "cat-file", "-e", blockedCandidate+"^{commit}")
	if err := os.Remove(pushConflict); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(conflictProbe); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contentState, []byte(published.Result.CandidateCommit+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`UPDATE sync_controls SET revision=revision+1,state='active',reason='none' WHERE group_id=?`, cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}

	normalMembership := gitTestOutput(t, realGit, repo, "rev-parse", cfg.Sync.MembershipRef)
	emergencyMembership := e21t3ApplyMembershipChange(t, cfg, dir, "retirement", cfg.Sync.Nodes[1].InstanceID, normalMembership)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = openStateStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Exec(`UPDATE sync_controls SET revision=revision+1,state='paused',reason='operator_pause' WHERE group_id=?`, cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, realGit, repo, "update-ref", cfg.Sync.MembershipRef, normalMembership, emergencyMembership)
	contentBeforeEmergency := gitTestOutput(t, realGit, repo, "rev-parse", cfg.Sync.ContentRef)
	var adoptOut, adoptErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &adoptOut, &adoptErr); code != 30 || !bytes.Contains(adoptOut.Bytes(), []byte(`"state":"membership_adopted"`)) || !bytes.Contains(adoptOut.Bytes(), []byte(`"control_reason":"operator_pause"`)) || !bytes.Contains(adoptOut.Bytes(), []byte(`"membership_mode":"blocked_emergency"`)) {
		t.Fatalf("emergency membership adoption: %d out=%s err=%s", code, adoptOut.String(), adoptErr.String())
	}
	control, err = store.LoadSyncControl(requestCtx(), cfg.Sync.GroupID)
	if err != nil || control.State != "paused" || control.Reason != "operator_pause" || control.MembershipMode != "blocked_emergency" {
		t.Fatalf("emergency membership did not preserve pause and latch posture: control=%+v err=%v", control, err)
	}
	if got := gitTestOutput(t, realGit, repo, "rev-parse", cfg.Sync.ContentRef); got != contentBeforeEmergency {
		t.Fatalf("emergency adoption moved content: before=%s after=%s", contentBeforeEmergency, got)
	}
	var pausedOut, pausedErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &pausedOut, &pausedErr); code != 0 || !bytes.Contains(pausedOut.Bytes(), []byte(`"state":"deferred"`)) || !bytes.Contains(pausedOut.Bytes(), []byte(`"reason":"operator_pause"`)) || !bytes.Contains(pausedOut.Bytes(), []byte(`"membership_mode":"blocked_emergency"`)) {
		t.Fatalf("paused emergency reconcile: %d out=%s err=%s", code, pausedOut.String(), pausedErr.String())
	}
	var resumeOut, resumeErr bytes.Buffer
	if code := Run([]string{"sync", "resume", "--group", cfg.Sync.GroupID, "--expected-control-revision", strconv.FormatInt(control.Revision, 10), "--output", "json"}, &resumeOut, &resumeErr); code != 30 || !bytes.Contains(resumeOut.Bytes(), []byte(`"state":"blocked"`)) || !bytes.Contains(resumeOut.Bytes(), []byte(`"reason":"membership_emergency"`)) {
		t.Fatalf("resume crossed emergency membership: %d out=%s err=%s", code, resumeOut.String(), resumeErr.String())
	}
	var blockedOut, blockedErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &blockedOut, &blockedErr); code != 30 || !bytes.Contains(blockedOut.Bytes(), []byte(`"reason":"membership_emergency"`)) {
		t.Fatalf("emergency reconcile fence: %d out=%s err=%s", code, blockedOut.String(), blockedErr.String())
	}

	_, replacementFingerprint := e21t3Key(t, dir, "replacement-publisher")
	cfg.Sync.Nodes[1].PublisherKey = replacementFingerprint
	cfg.Sync.Nodes[1].StateIncarnationID = "node-b-0002"
	updatedConfig, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, updatedConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	normalRestoration := e21t3ApplyMembershipChange(t, cfg, dir, "replacement", cfg.Sync.Nodes[1].InstanceID, emergencyMembership)
	if _, err := store.Exec(`UPDATE sync_controls SET revision=revision+1,state='blocked',reason='membership_emergency',membership_mode='blocked_emergency' WHERE group_id=?`, cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	if got := gitTestOutput(t, realGit, repo, "rev-parse", cfg.Sync.MembershipRef); got != normalRestoration {
		t.Fatalf("normal restoration ref=%s want=%s", got, normalRestoration)
	}
	var clearedOut, clearedErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &clearedOut, &clearedErr); code != 0 {
		t.Fatalf("equal-ref normal membership did not clear stale posture: %d out=%s err=%s", code, clearedOut.String(), clearedErr.String())
	}
	control, err = store.LoadSyncControl(requestCtx(), cfg.Sync.GroupID)
	if err != nil || control.State != "active" || control.Reason != "none" || control.MembershipMode != "normal" {
		t.Fatalf("normal equal-ref reconcile control=%+v err=%v", control, err)
	}

}

func e21t3ApplyMembershipChange(t *testing.T, cfg *config.Config, dir, change, instance, predecessor string) string {
	t.Helper()
	var planOut, planErr bytes.Buffer
	if code := Run([]string{"sync", "membership", "plan", "--group", cfg.Sync.GroupID, "--change", change, "--instance", instance, "--output", "json"}, &planOut, &planErr); code != 0 {
		t.Fatalf("membership %s plan: %d %s", change, code, planErr.String())
	}
	var envelope struct {
		Result struct {
			Plan syncrecords.MembershipPlan `json:"plan"`
		} `json:"result"`
	}
	if err := json.Unmarshal(planOut.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(envelope.Result.Plan)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "membership-"+change+".json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var applyOut, applyErr bytes.Buffer
	if code := Run([]string{"sync", "membership", "apply", "--group", cfg.Sync.GroupID, "--plan", path, "--expected-membership-predecessor", predecessor, "--output", "json"}, &applyOut, &applyErr); code != 0 && !(change == "retirement" && code == 30) {
		t.Fatalf("membership %s apply: %d out=%s err=%s", change, code, applyOut.String(), applyErr.String())
	}
	var applied struct {
		Result struct {
			MembershipRevision string `json:"membership_revision"`
		} `json:"result"`
	}
	if err := json.Unmarshal(applyOut.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	return applied.Result.MembershipRevision
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
	for routeID, route := range cfg.Routes {
		if route.Source.Resource != cfg.Sync.Resource {
			continue
		}
		routeRevision, _ := config.RouteRevision(cfg, routeID)
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

func prepareE21T3State(t *testing.T, cfg *config.Config, configPath string) {
	t.Helper()
	store, err := openStateStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	resource := cfg.Resources[cfg.Sync.Resource]
	if err := store.RegisterResource(nil, cfg.Sync.Resource, "resource-e21t3", resource.Root, resource.Root, resource.FileScope, resource.Git.Mode); err != nil {
		t.Fatal(err)
	}
	revision, _ := config.SyncRevision(cfg)
	if _, err := store.EnsureSyncControl(requestCtx(), cfg.Sync.GroupID, revision, "2026-09-21T00:00:00Z"); err != nil {
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
func commandBytesWithInput(t *testing.T, input []byte, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Stdin = bytes.NewReader(input)
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

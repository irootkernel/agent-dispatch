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

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/syncmembership"
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
	gitTestRun(t, realGit, repo, "checkout", "-q", "-b", strings.TrimPrefix(cfg.Sync.ContentRef, "refs/heads/"), base)
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
	gitTestRun(t, realGit, repo, "checkout", "-q", "-b", "other-checkpoint")
	var wrongCheckpointOut, wrongCheckpointErr bytes.Buffer
	if code := Run([]string{"sync", "checkpoint", "apply", "--group", cfg.Sync.GroupID, "--plan", checkpointPath, "--output", "json"}, &wrongCheckpointOut, &wrongCheckpointErr); code != 14 || strings.TrimSpace(string(mustRead(t, contentState))) != base {
		t.Fatalf("wrong-branch checkpoint: %d out=%s err=%s", code, wrongCheckpointOut.String(), wrongCheckpointErr.String())
	}
	gitTestRun(t, realGit, repo, "checkout", "-q", strings.TrimPrefix(cfg.Sync.ContentRef, "refs/heads/"))
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
	assertE21ContentVerificationRejectsSnapshotMismatch(t, cfg, repo, publisherKey)
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
	gitTestRun(t, realGit, repo, "checkout", "-q", "-b", "other-publication")
	var wrongPublishOut, wrongPublishErr bytes.Buffer
	if code := Run([]string{"sync", "publish", "--group", cfg.Sync.GroupID, "--expected-config-revision", revision, "--output", "json"}, &wrongPublishOut, &wrongPublishErr); code != 14 || strings.TrimSpace(string(mustRead(t, contentState))) != contentBeforeIneligible {
		t.Fatalf("wrong-branch publication: %d out=%s err=%s", code, wrongPublishOut.String(), wrongPublishErr.String())
	}
	gitTestRun(t, realGit, repo, "checkout", "-q", strings.TrimPrefix(cfg.Sync.ContentRef, "refs/heads/"))
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
	postPublishStatus := gitTestOutput(t, realGit, repo, "status", "--porcelain=v1")
	if strings.Contains(postPublishStatus, ".agent-dispatch-sync/") {
		t.Fatalf("publication left controller metadata dirty: %q", postPublishStatus)
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
	// A service has no publisher signing key. Re-strand the same confirmed
	// candidate and prove the operator reconcile path can finish it alone.
	if _, err := store.Exec(`DELETE FROM sync_jobs WHERE kind='delivery'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`UPDATE sync_jobs SET state='signed',retain_until_resolved=1,resolved_at=NULL WHERE job_id=?`, publicationJobID); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, realGit, repo, "update-ref", cfg.Sync.ContentRef, publicationRecord.BaseCommit, published.Result.CandidateCommit)
	t.Setenv("E21T3_PUBLISHER", "")
	faultBin := filepath.Join(dir, "recovery-fault-bin")
	if err := os.Mkdir(faultBin, 0o700); err != nil {
		t.Fatal(err)
	}
	remoteFailure := filepath.Join(dir, "fail-recovery-remote")
	remoteCount := filepath.Join(dir, "recovery-remote-count")
	advanceFailure := filepath.Join(dir, "fail-local-advance")
	fetchFailure := filepath.Join(dir, "fail-content-fetch")
	membershipFetchFailure := filepath.Join(dir, "fail-membership-fetch")
	faultWrapper := `#!/bin/sh
case " $* " in
  *" ls-remote "*)
    for last do :; done
    if test "$last" = __CONTENT_REF__ && test -e __REMOTE_FAILURE__; then
      count=0
      if test -s __REMOTE_COUNT__; then count=$(cat __REMOTE_COUNT__); fi
      count=$((count + 1))
      printf '%s\n' "$count" > __REMOTE_COUNT__
      if test "$count" -eq 2; then exit 1; fi
    fi;;
  *" fetch "*"__CONTENT_REF_PATTERN__:refs/agent-dispatch/sync/content/"*)
    if test -e __FETCH_FAILURE__; then
      printf '%s\n' "remote: fatal: couldn't find remote ref" >&2
      exit 1
    fi;;
  *" fetch "*"__MEMBERSHIP_REF_PATTERN__:refs/agent-dispatch/sync/membership/"*)
    if test -e __MEMBERSHIP_FETCH_FAILURE__; then
      printf '%s\n' "remote: fatal: couldn't find remote ref" >&2
      exit 1
    fi;;
  *" update-ref "*)
    previous=
    for arg do
      if test "$previous" = update-ref && test "$arg" = __CONTENT_REF__ && test -e __ADVANCE_FAILURE__; then exit 1; fi
      previous=$arg
    done;;
esac
exec __BASE_WRAPPER__ "$@"
`
	shellQuote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	faultWrapper = strings.NewReplacer(
		"__CONTENT_REF_PATTERN__", cfg.Sync.ContentRef,
		"__MEMBERSHIP_REF_PATTERN__", cfg.Sync.MembershipRef,
		"__CONTENT_REF__", shellQuote(cfg.Sync.ContentRef),
		"__REMOTE_FAILURE__", shellQuote(remoteFailure),
		"__REMOTE_COUNT__", shellQuote(remoteCount),
		"__ADVANCE_FAILURE__", shellQuote(advanceFailure),
		"__FETCH_FAILURE__", shellQuote(fetchFailure),
		"__MEMBERSHIP_FETCH_FAILURE__", shellQuote(membershipFetchFailure),
		"__BASE_WRAPPER__", shellQuote(wrapper),
	).Replace(faultWrapper)
	if err := os.WriteFile(filepath.Join(faultBin, "git"), []byte(faultWrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", faultBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := store.Exec(`UPDATE sync_jobs SET claim_owner='live-publisher',claim_expires_at=? WHERE job_id=?`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), publicationJobID); err != nil {
		t.Fatal(err)
	}
	var claimedOut, claimedErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &claimedOut, &claimedErr); code != 10 || !bytes.Contains(claimedOut.Bytes(), []byte(`"state":"deferred"`)) || !bytes.Contains(claimedOut.Bytes(), []byte(`"reason":"publication_recovery_pending"`)) || claimedErr.Len() != 0 {
		t.Fatalf("live publication claim: %d out=%s err=%s", code, claimedOut.String(), claimedErr.String())
	}
	if _, err := store.Exec(`UPDATE sync_jobs SET claim_owner=NULL,claim_expires_at=NULL WHERE job_id=?`, publicationJobID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(remoteFailure, []byte("fail second content probe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var retryOut, retryErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &retryOut, &retryErr); code != 10 || !bytes.Contains(retryOut.Bytes(), []byte(`"state":"deferred"`)) || !bytes.Contains(retryOut.Bytes(), []byte(`"reason":"publication_recovery_pending"`)) || retryErr.Len() != 0 {
		t.Fatalf("transient signed-publication recovery: %d out=%s err=%s", code, retryOut.String(), retryErr.String())
	}
	if err := os.Remove(remoteFailure); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.Exec(`INSERT INTO sync_jobs(job_id,group_id,kind,logical_key,request_fingerprint,state,payload_json,claim_owner,claim_expires_at,created_at,updated_at) VALUES ('unrelated-live-publisher',?,'publication','unrelated','sha256:unrelated','eligible','{}','other-publisher',?,?,?)`, cfg.Sync.GroupID, now.Add(time.Hour).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	baseTree := gitTestOutput(t, realGit, repo, "rev-parse", publicationRecord.BaseCommit+"^{tree}")
	otherLocal := strings.TrimSpace(string(commandBytesWithInput(t, []byte("other local head\n"), realGit, "-C", repo, "commit-tree", baseTree, "-p", publicationRecord.BaseCommit)))
	gitTestRun(t, realGit, repo, "update-ref", cfg.Sync.ContentRef, otherLocal, publicationRecord.BaseCommit)
	var unrelatedOut, unrelatedErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &unrelatedOut, &unrelatedErr); code != 14 || !bytes.Contains(unrelatedOut.Bytes(), []byte(`"state":"failed"`)) || !bytes.Contains(unrelatedOut.Bytes(), []byte(`"reason":"publication_recovery_precondition"`)) {
		t.Fatalf("unrelated live claim masked a recovery precondition: %d out=%s err=%s", code, unrelatedOut.String(), unrelatedErr.String())
	}
	control, err := store.LoadSyncControl(requestCtx(), cfg.Sync.GroupID)
	if err != nil || control.State != "active" {
		t.Fatalf("unrelated live claim changed control: %+v err=%v", control, err)
	}
	gitTestRun(t, realGit, repo, "update-ref", cfg.Sync.ContentRef, publicationRecord.BaseCommit, otherLocal)
	if _, err := store.Exec(`DELETE FROM sync_jobs WHERE job_id='unrelated-live-publisher'`); err != nil {
		t.Fatal(err)
	}
	var fence int
	if err := store.QueryRow(`SELECT fence FROM sync_jobs WHERE job_id=?`, publicationJobID).Scan(&fence); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`INSERT INTO sync_journal_entries(journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES ('e22-invalid-candidate',?,?,'claim_recovery','signed',?,?)`, publicationJobID, fence, mustJSON(map[string]any{"candidate": strings.Repeat("f", 40)}), time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	var invalidOut, invalidErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &invalidOut, &invalidErr); code != 14 || !bytes.Contains(invalidOut.Bytes(), []byte(`"state":"blocked"`)) || !bytes.Contains(invalidOut.Bytes(), []byte(`"reason":"conflict"`)) || invalidErr.Len() != 0 {
		t.Fatalf("moved signed-publication candidate: %d out=%s err=%s", code, invalidOut.String(), invalidErr.String())
	}
	if _, err := store.Exec(`UPDATE sync_controls SET state='active',reason='none' WHERE group_id=?`, cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`UPDATE sync_jobs SET state='signed' WHERE job_id=?`, publicationJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`INSERT INTO sync_journal_entries(journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES ('e22-restored-candidate',?,?,'claim_recovery','signed',?,?)`, publicationJobID, fence, mustJSON(map[string]any{"candidate": published.Result.CandidateCommit}), time.Now().Add(2*time.Second).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	badCandidate := strings.Repeat("f", 40)
	if _, err := store.Exec(`INSERT INTO sync_journal_entries(journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES ('e22-trust-candidate',?,?,'claim_recovery','signed',?,?)`, publicationJobID, fence, mustJSON(map[string]any{"candidate": badCandidate}), time.Now().Add(3*time.Second).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contentState, []byte(badCandidate+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var trustOut, trustErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &trustOut, &trustErr); code != 30 || !bytes.Contains(trustOut.Bytes(), []byte(`"state":"blocked"`)) || !bytes.Contains(trustOut.Bytes(), []byte(`"reason":"trust_failure"`)) || trustErr.Len() != 0 {
		t.Fatalf("untrusted signed-publication recovery: %d out=%s err=%s", code, trustOut.String(), trustErr.String())
	}
	if err := os.WriteFile(contentState, []byte(published.Result.CandidateCommit+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`UPDATE sync_controls SET state='active',reason='none' WHERE group_id=?`, cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`INSERT INTO sync_journal_entries(journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES ('e22-after-trust-candidate',?,?,'claim_recovery','signed',?,?)`, publicationJobID, fence, mustJSON(map[string]any{"candidate": published.Result.CandidateCommit}), time.Now().Add(4*time.Second).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(advanceFailure, []byte("fail local advance\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var uncertainOut, uncertainErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &uncertainOut, &uncertainErr); code != 13 || !bytes.Contains(uncertainOut.Bytes(), []byte(`"state":"blocked"`)) || !bytes.Contains(uncertainOut.Bytes(), []byte(`"reason":"recovery_required"`)) || uncertainErr.Len() != 0 {
		t.Fatalf("uncertain signed-publication recovery: %d out=%s err=%s", code, uncertainOut.String(), uncertainErr.String())
	}
	if err := os.Remove(advanceFailure); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`UPDATE sync_controls SET state='active',reason='none' WHERE group_id=?`, cfg.Sync.GroupID); err != nil {
		t.Fatal(err)
	}
	var serviceRecoveryOut, serviceRecoveryErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &serviceRecoveryOut, &serviceRecoveryErr); code != 0 || !bytes.Contains(serviceRecoveryOut.Bytes(), []byte(`"state":"no_change"`)) {
		t.Fatalf("signer-free reconcile recovery: %d out=%s err=%s", code, serviceRecoveryOut.String(), serviceRecoveryErr.String())
	}
	if got := gitTestOutput(t, realGit, repo, "rev-parse", cfg.Sync.ContentRef); got != published.Result.CandidateCommit {
		t.Fatalf("reconcile did not restore local content ref: %s", got)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='delivery' AND state='pending'`).Scan(&deliveries); err != nil || deliveries != 1 {
		t.Fatalf("reconcile did not retain publication delivery: %d err=%v", deliveries, err)
	}
	t.Setenv("E21T3_PUBLISHER", string(publisherKey))
	var noOpOut, noOpErr bytes.Buffer
	if code := Run([]string{"sync", "publish", "--group", cfg.Sync.GroupID, "--expected-config-revision", revision, "--output", "json"}, &noOpOut, &noOpErr); code != 0 || !bytes.Contains(noOpOut.Bytes(), []byte(`"state":"no_content_change"`)) {
		t.Fatalf("no-op: %d out=%s err=%s", code, noOpOut.String(), noOpErr.String())
	}
	if err := os.WriteFile(membershipFetchFailure, []byte("spoof missing membership ref\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var membershipSpoofOut, membershipSpoofErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &membershipSpoofOut, &membershipSpoofErr); code != 10 || !bytes.Contains(membershipSpoofOut.Bytes(), []byte(`"state":"deferred"`)) || !bytes.Contains(membershipSpoofOut.Bytes(), []byte(`"reason":"git_unstable"`)) {
		t.Fatalf("unconfirmed membership missing-ref diagnostic created a hold: %d out=%s err=%s", code, membershipSpoofOut.String(), membershipSpoofErr.String())
	}
	control, err = store.LoadSyncControl(requestCtx(), cfg.Sync.GroupID)
	if err != nil || control.State != "active" {
		t.Fatalf("unconfirmed membership missing-ref diagnostic changed control: %+v err=%v", control, err)
	}
	if err := os.Remove(membershipFetchFailure); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fetchFailure, []byte("spoof missing ref\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var spoofOut, spoofErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &spoofOut, &spoofErr); code != 10 || !bytes.Contains(spoofOut.Bytes(), []byte(`"state":"deferred"`)) || !bytes.Contains(spoofOut.Bytes(), []byte(`"reason":"git_unstable"`)) {
		t.Fatalf("unconfirmed missing-ref diagnostic created a hold: %d out=%s err=%s", code, spoofOut.String(), spoofErr.String())
	}
	control, err = store.LoadSyncControl(requestCtx(), cfg.Sync.GroupID)
	if err != nil || control.State != "active" {
		t.Fatalf("unconfirmed missing-ref diagnostic changed control: %+v err=%v", control, err)
	}
	if err := os.Remove(fetchFailure); err != nil {
		t.Fatal(err)
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
	control, err = store.LoadSyncControl(requestCtx(), cfg.Sync.GroupID)
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

	replacementKey, replacementFingerprint := e21t3Key(t, dir, "replacement-publisher")
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
	assertE21PublishedNodeCanApplyPeerRoundTrip(t, cfg, configPath, contentState, repo, replacementKey)
	// Rewinding the remote after a successful import must leave the local
	// content ref unchanged and arm a conflict hold.
	if err := os.WriteFile(contentState, []byte(base+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	localBeforeRewrite := gitTestOutput(t, realGit, repo, "rev-parse", cfg.Sync.ContentRef)
	var rewriteOut, rewriteErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &rewriteOut, &rewriteErr); code != 14 || !bytes.Contains(rewriteOut.Bytes(), []byte(`"state":"blocked"`)) || !bytes.Contains(rewriteOut.Bytes(), []byte(`"control_reason":"conflict"`)) {
		t.Fatalf("remote history rewrite was not held: %d out=%s err=%s", code, rewriteOut.String(), rewriteErr.String())
	}
	if got := gitTestOutput(t, realGit, repo, "rev-parse", cfg.Sync.ContentRef); got != localBeforeRewrite {
		t.Fatalf("remote rewrite moved the protected local ref: %s -> %s", localBeforeRewrite, got)
	}
	// A deleted configured ref is a stronger trust failure, not an ordinary
	// offline transport failure. It must remain visible across a prior hold.
	if err := os.WriteFile(contentState, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var missingOut, missingErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &missingOut, &missingErr); code != 30 || !bytes.Contains(missingOut.Bytes(), []byte(`"state":"blocked"`)) || !bytes.Contains(missingOut.Bytes(), []byte(`"control_reason":"trust_failure"`)) {
		t.Fatalf("deleted configured ref was not held: %d out=%s err=%s", code, missingOut.String(), missingErr.String())
	}
	if err := os.WriteFile(membershipState, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	localMembershipBeforeDeletion := gitTestOutput(t, realGit, repo, "rev-parse", cfg.Sync.MembershipRef)
	var missingMembershipOut, missingMembershipErr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &missingMembershipOut, &missingMembershipErr); code != 30 || !bytes.Contains(missingMembershipOut.Bytes(), []byte(`"state":"blocked"`)) || !bytes.Contains(missingMembershipOut.Bytes(), []byte(`"control_reason":"trust_failure"`)) {
		t.Fatalf("deleted membership ref was not held: %d out=%s err=%s", code, missingMembershipOut.String(), missingMembershipErr.String())
	}
	if got := gitTestOutput(t, realGit, repo, "rev-parse", cfg.Sync.MembershipRef); got != localMembershipBeforeDeletion {
		t.Fatalf("deleted remote membership moved local ref: %s -> %s", localMembershipBeforeDeletion, got)
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

func assertE21ContentVerificationRejectsSnapshotMismatch(t *testing.T, cfg *config.Config, repo string, publisherKey []byte) {
	t.Helper()
	client, err := gitlocal.New(repo, gitlocal.Limits{Timeout: 30 * time.Second, MaxOutput: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	membership, err := client.ResolveRef(requestCtx(), cfg.Sync.MembershipRef)
	if err != nil {
		t.Fatal(err)
	}
	history, err := syncmembership.LoadHistory(requestCtx(), client, membership, membershipBinding(cfg, cfg.Sync), cfg.Sync.Bounds.HistoryCommits)
	if err != nil {
		t.Fatal(err)
	}
	current, ok := history.Current()
	if !ok {
		t.Fatal("membership history has no current record")
	}
	var publisher syncrecords.ActiveMember
	for _, member := range current.ActiveMembers {
		if member.InstanceID == cfg.Sync.LocalInstanceID {
			publisher = member
			break
		}
	}
	if publisher.InstanceID == "" {
		t.Fatal("local publisher is not active")
	}
	base, err := client.ResolveRef(requestCtx(), cfg.Sync.ContentRef)
	if err != nil {
		t.Fatal(err)
	}
	baseFiles, err := client.ReadContentFiles(requestCtx(), base)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := syncrecords.NewPublication(syncrecords.PublicationBinding{
		GroupID: cfg.Sync.GroupID, Publisher: publisher.InstanceID, StateIncarnationID: publisher.StateIncarnationID,
		MembershipRevision: membership, ContentRef: cfg.Sync.ContentRef, BaseCommit: base,
		ScopeDigest: config.SyncScopeDigest(cfg, cfg.Sync.Resource), ContractDigest: config.SyncContractDigest(),
		SourceRevision: 1, ReceiptIDs: []string{"receipt-forged-binding"},
	}, syncrecords.SnapshotFiles(baseFiles))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := syncrecords.CanonicalPublication(publication)
	if err != nil {
		t.Fatal(err)
	}
	forgedFiles := cloneFiles(baseFiles)
	forgedFiles["note.md"] = []byte("forged snapshot bytes\n")
	forgedFiles[".agent-dispatch-sync/publications/"+publication.PublicationID+".json"] = raw
	tree, err := client.SnapshotTreeChanges(requestCtx(), base, forgedFiles, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := client.CreateSignedContentCommit(requestCtx(), tree, publisherKey, base, time.Unix(1_700_000_100, 0), "publisher", "forged snapshot binding")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyContentHead(client, history, cfg, cfg.Sync, candidate); err == nil {
		t.Fatal("content verification accepted a publication whose snapshot digest did not bind its tree")
	}
}

func assertE21PublishedNodeCanApplyPeerRoundTrip(t *testing.T, cfg *config.Config, configPath, contentState, repo string, peerKey []byte) {
	t.Helper()
	client, err := gitlocal.New(repo, gitlocal.Limits{Timeout: 30 * time.Second, MaxOutput: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	base, err := client.ResolveRef(requestCtx(), cfg.Sync.ContentRef)
	if err != nil {
		t.Fatal(err)
	}
	baseFiles, err := client.ReadContentFiles(requestCtx(), base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "note.md"), baseFiles["note.md"], 0o600); err != nil {
		t.Fatal(err)
	}
	membership, err := client.ResolveRef(requestCtx(), cfg.Sync.MembershipRef)
	if err != nil {
		t.Fatal(err)
	}
	history, err := syncmembership.LoadHistory(requestCtx(), client, membership, membershipBinding(cfg, cfg.Sync), cfg.Sync.Bounds.HistoryCommits)
	if err != nil {
		t.Fatal(err)
	}
	current, ok := history.Current()
	if !ok {
		t.Fatal("membership history has no current record")
	}
	var peer syncrecords.ActiveMember
	for _, member := range current.ActiveMembers {
		if member.InstanceID != cfg.Sync.LocalInstanceID {
			peer = member
			break
		}
	}
	if peer.InstanceID == "" {
		t.Fatal("peer publisher is not active")
	}
	// Several publications accumulate while this node is offline. One
	// configured-ref reconcile must verify every signed first-parent commit,
	// including publications for which no nudge was received.
	target := base
	for i := 1; i <= 4; i++ {
		targetFiles := cloneFiles(baseFiles)
		targetFiles["note.md"] = []byte(fmt.Sprintf("peer round trip %d\n", i))
		publication, err := syncrecords.NewPublication(syncrecords.PublicationBinding{
			GroupID: cfg.Sync.GroupID, Publisher: peer.InstanceID, StateIncarnationID: peer.StateIncarnationID,
			MembershipRevision: membership, ContentRef: cfg.Sync.ContentRef, BaseCommit: target,
			ScopeDigest: config.SyncScopeDigest(cfg, cfg.Sync.Resource), ContractDigest: config.SyncContractDigest(),
			SourceRevision: int64(i + 1), ReceiptIDs: []string{fmt.Sprintf("receipt-peer-round-trip-%d", i)},
		}, syncrecords.SnapshotFiles(targetFiles))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := syncrecords.CanonicalPublication(publication)
		if err != nil {
			t.Fatal(err)
		}
		treeFiles := cloneFiles(targetFiles)
		treeFiles[".agent-dispatch-sync/publications/"+publication.PublicationID+".json"] = raw
		tree, err := client.SnapshotTreeChanges(requestCtx(), target, treeFiles, nil)
		if err != nil {
			t.Fatal(err)
		}
		target, err = client.CreateSignedContentCommit(requestCtx(), tree, peerKey, target, time.Unix(int64(1_700_000_200+i), 0), "publisher", "peer round trip")
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(contentState, []byte(target+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	revision, ok := config.SyncRevision(cfg)
	if !ok {
		t.Fatal("sync revision unavailable")
	}
	incarnation := localSyncIncarnation(cfg)
	cfg.Sync.ImportAcknowledgement = &config.SyncImportAcknowledgement{
		SchemaVersion: "agent-dispatch.sync-import-acknowledgement/v1", AcknowledgementID: "acknowledgement-round-trip",
		GroupID: cfg.Sync.GroupID, ResourceID: cfg.Sync.Resource, RemoteName: cfg.Sync.RemoteName,
		RemoteRepositoryDigest: cfg.Sync.RemoteRepositoryDigest, ContentRef: cfg.Sync.ContentRef, MembershipRef: cfg.Sync.MembershipRef,
		ScopeDigest: config.SyncScopeDigest(cfg, cfg.Sync.Resource), LocalInstanceID: cfg.Sync.LocalInstanceID,
		StateIncarnationID: incarnation, AdministratorKey: cfg.Sync.AdministratorKey,
		SafetyPolicyDigest: config.SyncSafetyPolicyDigest(), ImportBoundsDigest: config.SyncImportBoundsDigest(cfg.Sync.Bounds), ConfigRevision: revision,
	}
	updated, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := openStateStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`UPDATE destination_lane_state SET active_dispatch_id=NULL WHERE route_id IN (SELECT route_id FROM routes WHERE resource_id=?)`, cfg.Sync.Resource); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Run([]string{"sync", "reconcile", "--group", cfg.Sync.GroupID, "--output", "json"}, &out, &stderr); code != 0 || !bytes.Contains(out.Bytes(), []byte(`"state":"applied"`)) {
		t.Fatalf("peer round-trip reconcile: %d out=%s err=%s", code, out.String(), stderr.String())
	}
	if got := gitTestOutput(t, "git", repo, "rev-parse", cfg.Sync.ContentRef); got != target {
		t.Fatalf("round-trip content ref=%s want=%s", got, target)
	}
	if raw, err := os.ReadFile(filepath.Join(repo, "note.md")); err != nil || string(raw) != "peer round trip 4\n" {
		t.Fatalf("round-trip content=%q err=%v", raw, err)
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

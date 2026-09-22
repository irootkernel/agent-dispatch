package cli

import (
	"bytes"
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
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/syncimport"
	"github.com/irootkernel/agent-dispatch/internal/app/syncmembership"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func enabledSyncConfig(t *testing.T) string {
	t.Helper()
	cfg, err := config.Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Sync.Enabled = true
	cfg.Instance.StateDir = filepath.Join(t.TempDir(), "state")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func syncResult(t *testing.T, args ...string) (int, map[string]any, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(append([]string{"sync"}, args...), &out, &errOut)
	var envelope map[string]any
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatalf("decode stdout: %v: %s", err, out.String())
		}
	}
	return code, envelope, errOut.String()
}

func TestE21T1SyncPauseResumeAndStatus(t *testing.T) {
	configPath := enabledSyncConfig(t)
	code, envelope, stderr := syncResult(t, "status", "--group", "wiki-pair", "--config", configPath, "--output", "json")
	if code != 0 {
		t.Fatalf("initial status: %d %s", code, stderr)
	}
	status := envelope["result"].(map[string]any)
	if status["state"] != "active" || status["control_revision"] != float64(1) {
		t.Fatalf("initial control = %v", status)
	}
	for _, field := range []string{"latest_publication", "latest_delivery", "latest_import"} {
		if latest, ok := status[field].(map[string]any); !ok || latest["present"] != false {
			t.Fatalf("initial %s status = %v", field, status[field])
		}
	}
	code, envelope, stderr = syncResult(t, "pause", "--group", "wiki-pair", "--expected-control-revision", "1", "--config", configPath, "--output", "json")
	if code != 0 {
		t.Fatalf("pause: %d %s", code, stderr)
	}
	control := envelope["result"].(map[string]any)
	if control["schema_version"] != "agent-dispatch.sync-control/v1" || control["state"] != "paused" || control["revision"] != float64(2) || control["membership_mode"] != "normal" || len(control) != 6 {
		t.Fatalf("pause result = %v", control)
	}
	code, _, stderr = syncResult(t, "resume", "--group", "wiki-pair", "--expected-control-revision", "1", "--config", configPath, "--output", "json")
	if code != 14 || !bytes.Contains([]byte(stderr), []byte("sync_precondition_failed")) {
		t.Fatalf("stale resume: %d %s", code, stderr)
	}
	code, envelope, stderr = syncResult(t, "resume", "--group", "wiki-pair", "--expected-control-revision", "2", "--config", configPath, "--output", "json")
	if code != 0 || envelope["result"].(map[string]any)["state"] != "active" {
		t.Fatalf("resume: %d %v %s", code, envelope, stderr)
	}
}

func TestE21T5StatusSeparatesDurableSyncOutcomes(t *testing.T) {
	configPath := enabledSyncConfig(t)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	revision, ok := config.SyncRevision(cfg)
	if !ok {
		t.Fatal("sync revision unavailable")
	}
	store, err := openStateStore(configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.EnsureSyncControl(requestCtx(), cfg.Sync.GroupID, revision, "2026-09-21T01:00:00Z"); err != nil {
		t.Fatal(err)
	}
	target := "1111111111111111111111111111111111111111"
	jobs := []sqlite.SyncJobInput{
		{JobID: "publication-job-g17", GroupID: cfg.Sync.GroupID, Kind: "publication", LogicalKey: "publication-g17", InitialState: "signed", PayloadJSON: `{"publication_id":"publication-g17","target_commit":"` + target + `"}`, QueueLimit: 10, Now: "2026-09-21T01:00:01Z"},
		{JobID: "delivery-job-g17", GroupID: cfg.Sync.GroupID, Kind: "delivery", LogicalKey: "publication-g17", InitialState: "pending", PayloadJSON: `{"publication_id":"publication-g17","receiver":"node-b","target_commit":"` + target + `"}`, QueueLimit: 10, Now: "2026-09-21T01:00:02Z"},
		{JobID: "import-job-g17", GroupID: cfg.Sync.GroupID, Kind: "import", LogicalKey: target, InitialState: "requested", PayloadJSON: `{"import_id":"import-g17","target_commit":"` + target + `"}`, QueueLimit: 10, Now: "2026-09-21T01:00:03Z"},
	}
	for _, job := range jobs {
		if _, _, err := store.AdmitSyncJob(requestCtx(), job); err != nil {
			t.Fatalf("admit %s: %v", job.Kind, err)
		}
	}
	if _, err := store.Exec(`INSERT INTO sync_journal_entries
		(journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES
		('publication-status-evidence','publication-job-g17',1,'publication','effect_unknown','{"candidate":"2222222222222222222222222222222222222222","remote":"1111111111111111111111111111111111111111","push_state":"ambiguous","reason":"push outcome unknown"}','2026-09-21T01:00:04Z'),
		('import-status-evidence','import-job-g17',1,'import','deferred','{"reason":"observation_unavailable"}','2026-09-21T01:00:05Z'),
		('publication-resolution-evidence','publication-job-g17',1,'publication','ok','{"resolution":"checkpoint_reconciled","resolver_id":"checkpoint-1"}','2026-09-21T01:00:03Z'),
		('publication-reopen-evidence','publication-job-g17',1,'claim_recovery','effect_not_started','{"candidate":"2222222222222222222222222222222222222222","remote":"1111111111111111111111111111111111111111"}','2026-09-21T01:00:05Z')`); err != nil {
		t.Fatal(err)
	}
	code, envelope, stderr := syncResult(t, "status", "--group", cfg.Sync.GroupID, "--config", configPath, "--output", "json")
	if code != 0 {
		t.Fatalf("status: %d %s", code, stderr)
	}
	status := envelope["result"].(map[string]any)
	for field, wantState := range map[string]string{
		"latest_publication": "signed",
		"latest_delivery":    "pending",
		"latest_import":      "requested",
	} {
		latest, ok := status[field].(map[string]any)
		if !ok || latest["present"] != true || latest["state"] != wantState || latest["target_commit"] != target || latest["claimed"] != false || latest["resolved"] != false {
			t.Fatalf("%s = %v", field, status[field])
		}
	}
	if status["latest_publication"].(map[string]any)["publication_id"] != "publication-g17" || status["latest_delivery"].(map[string]any)["receiver"] != "node-b" || status["latest_import"].(map[string]any)["import_id"] != "import-g17" {
		t.Fatalf("typed status details = %v", status)
	}
	publication := status["latest_publication"].(map[string]any)
	if publication["candidate"] != "2222222222222222222222222222222222222222" || publication["remote"] != target || publication["push_state"] != "ambiguous" || publication["reason"] != "push outcome unknown" {
		t.Fatalf("publication recovery evidence = %v", publication)
	}
	if publication["resolution"] != "checkpoint_reconciled" || publication["resolver_id"] != "checkpoint-1" {
		t.Fatalf("publication resolution evidence = %v", publication)
	}
	if got := status["latest_import"].(map[string]any)["reason"]; got != "observation_unavailable" {
		t.Fatalf("import deferral reason = %v", got)
	}
	if _, err := store.Exec(`INSERT INTO sync_journal_entries
		(journal_id,job_id,fence,phase,outcome,evidence_json,recorded_at) VALUES
		('publication-confirmed-evidence','publication-job-g17',2,'publication','published','{"candidate":"2222222222222222222222222222222222222222","remote":"2222222222222222222222222222222222222222","push_state":"confirmed","reason":"none"}','2026-09-21T01:00:06Z');
		UPDATE sync_jobs SET state='published',updated_at='2026-09-21T01:00:06Z' WHERE job_id='publication-job-g17'`); err != nil {
		t.Fatal(err)
	}
	code, envelope, stderr = syncResult(t, "status", "--group", cfg.Sync.GroupID, "--config", configPath, "--output", "json")
	if code != 0 {
		t.Fatalf("published status: %d %s", code, stderr)
	}
	publication = envelope["result"].(map[string]any)["latest_publication"].(map[string]any)
	if publication["state"] != "published" || publication["push_state"] != "confirmed" || publication["reason"] != "none" || publication["remote"] != "2222222222222222222222222222222222222222" {
		t.Fatalf("terminal publication retained stale retry evidence: %v", publication)
	}
}

func TestE21T1EnabledConfigDoesNotActivateStillReservedGitCommands(t *testing.T) {
	configPath := enabledSyncConfig(t)
	code, _, stderr := syncResult(t, "verify", "--group", "wiki-pair", "--config", configPath, "--output", "json")
	if code != 3 || !bytes.Contains([]byte(stderr), []byte("sync_capability_unavailable")) {
		t.Fatalf("reserved verify: %d %s", code, stderr)
	}
}

func TestE21SameBasePushRejectionIsRetryable(t *testing.T) {
	base := "1111111111111111111111111111111111111111"
	candidate := "2222222222222222222222222222222222222222"
	if got := classifySyncPush(gitlocal.PushRejected, base, base, candidate, nil); got != syncPushRetryable {
		t.Fatalf("same-base rejection = %v", got)
	}
	if got := classifySyncPush(gitlocal.PushRejected, "3333333333333333333333333333333333333333", base, candidate, nil); got != syncPushConflict {
		t.Fatalf("changed-base rejection = %v", got)
	}
	if got := classifySyncPush(gitlocal.PushNotStarted, "", base, candidate, nil); got != syncPushRetryable {
		t.Fatalf("pre-push measurement failure = %v", got)
	}
	if got := classifySyncPush(gitlocal.PushAmbiguous, candidate, base, candidate, nil); got != syncPushConfirmed {
		t.Fatalf("remote-confirmed candidate = %v", got)
	}
	if got := classifySyncPush(gitlocal.PushRejected, "", base, candidate, gitlocal.ErrRemoteBinding); got != syncPushTrust {
		t.Fatalf("remote binding disposition=%v", got)
	}
}

func TestE21CheckpointBindingErrorsKeepDistinctExitClasses(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code int
		want string
	}{
		{name: "measurement", err: errors.New("ls-remote unavailable"), code: 10, want: `"code":"sync_retryable"`},
		{name: "predecessor", err: fmt.Errorf("moved: %w", gitlocal.ErrPushRejected), code: 14, want: `"code":"sync_precondition_failed"`},
		{name: "history", err: fmt.Errorf("history: %w", syncrecords.ErrInvalidRecord), code: 30, want: `"code":"sync_trust_failed"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if got := checkpointBindingError(&stderr, "sync checkpoint plan", tc.err); got != tc.code || !bytes.Contains(stderr.Bytes(), []byte(tc.want)) {
				t.Fatalf("classification code=%d stderr=%s", got, stderr.String())
			}
		})
	}
}

func TestE21ControllerNoEffectRetryReportsValidatedIdentity(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, gitPath, repo, "init", "-q", "-b", "main")
	gitTestRun(t, gitPath, repo, "config", "user.name", "Test")
	gitTestRun(t, gitPath, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, gitPath, repo, "add", "note.md")
	gitTestRun(t, gitPath, repo, "commit", "-q", "-m", "base")
	head := gitTestOutput(t, gitPath, repo, "rev-parse", "HEAD")
	client, err := gitlocal.New(repo, gitlocal.Limits{Timeout: 30 * time.Second, MaxOutput: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.InspectImport(requestCtx(), "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureSyncControl(requestCtx(), "wiki-pair", "cfg-1", "2026-09-21T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	job, _, err := store.AdmitSyncJob(requestCtx(), sqlite.SyncJobInput{JobID: "controller-no-effect", GroupID: "wiki-pair", Kind: "import", LogicalKey: "controller-no-effect", InitialState: "applying", PayloadJSON: `{}`, ConfigRevision: "cfg-1", QueueLimit: 10, Now: "2026-09-21T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.ClaimSyncAdministrationJob(requestCtx(), job.JobID, "controller-owner", "cfg-1", "2026-09-21T00:00:00Z", "2099-09-21T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := finishControllerImport(&stdout, &stderr, store, client, job, "controller-owner", "refs/heads/main", head, "2222222222222222222222222222222222222222", state.Digest, os.ErrInvalid)
	if code != 10 || !bytes.Contains(stdout.Bytes(), []byte(`"state":"validated"`)) || !bytes.Contains(stdout.Bytes(), []byte(`"reason":"effect_not_started"`)) {
		t.Fatalf("controller retry: code=%d out=%s err=%s", code, stdout.String(), stderr.String())
	}
	stored, found, err := store.FindSyncJob(requestCtx(), "wiki-pair", "import", "controller-no-effect")
	if err != nil || !found || stored.State != "validated" || stored.ResolvedAt != "" {
		t.Fatalf("controller retry job=%+v found=%v err=%v", stored, found, err)
	}
}

func TestE21PublicationBlockedResultPreservesRecoveryReason(t *testing.T) {
	var stdout bytes.Buffer
	code := publicationBlockedResult(&stdout, syncrecords.Publication{PublicationID: "publication-1"}, "candidate-1", sqlite.SyncControlRow{State: "blocked", Reason: "recovery_required", MembershipMode: "normal"}, false)
	if code != 30 {
		t.Fatalf("code=%d output=%s", code, stdout.String())
	}
	for _, want := range []string{`"state":"blocked"`, `"reason":"recovery_required"`, `"candidate_commit":"candidate-1"`} {
		if !bytes.Contains(stdout.Bytes(), []byte(want)) {
			t.Fatalf("output missing %s: %s", want, stdout.String())
		}
	}
}

func TestE21PublicationBlockedBeforeCandidateDoesNotClaimOneWasPreserved(t *testing.T) {
	var stdout bytes.Buffer
	code := publicationBlockedResult(&stdout, syncrecords.Publication{}, "", sqlite.SyncControlRow{State: "blocked", Reason: "recovery_required", MembershipMode: "normal"}, false)
	if code != 30 {
		t.Fatalf("code=%d output=%s", code, stdout.String())
	}
	for _, unwanted := range []string{`"publication_id"`, `"candidate_commit"`, `preserved publication candidate`} {
		if bytes.Contains(stdout.Bytes(), []byte(unwanted)) {
			t.Fatalf("output unexpectedly contains %s: %s", unwanted, stdout.String())
		}
	}
}

func TestE21CheckpointBlockedResultPreservesRecoveryReason(t *testing.T) {
	var stdout bytes.Buffer
	code := checkpointBlockedResult(&stdout, syncrecords.CheckpointPlan{PlanID: "checkpoint-1"}, "candidate-1", sqlite.SyncControlRow{State: "blocked", Reason: "trust_failure", MembershipMode: "normal"})
	if code != 30 {
		t.Fatalf("code=%d output=%s", code, stdout.String())
	}
	for _, want := range []string{`"state":"blocked"`, `"reason":"trust_failure"`, `"plan_id":"checkpoint-1"`, `"candidate_commit":"candidate-1"`} {
		if !bytes.Contains(stdout.Bytes(), []byte(want)) {
			t.Fatalf("output missing %s: %s", want, stdout.String())
		}
	}
}

func TestE21LiveImportClaimResultIncludesDurableControlContext(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureSyncControl(requestCtx(), "wiki-pair", "cfg-1", "2026-09-21T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.HoldSyncControl(requestCtx(), "wiki-pair", "trust_failure", "cfg-1", "2026-09-21T00:00:01Z"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := liveImportClaimResult(&stdout, &stderr, store, sqlite.SyncJobRow{JobID: "import-live", GroupID: "wiki-pair", State: "applying"}, "still claimed")
	if code != 14 {
		t.Fatalf("code=%d output=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{`"import_state":"applying"`, `"control_reason":"trust_failure"`, `"membership_mode":"normal"`} {
		if !bytes.Contains(stdout.Bytes(), []byte(want)) {
			t.Fatalf("output missing %s: %s", want, stdout.String())
		}
	}
}

func TestE21PartialEffectResultPreservesStrongerHoldWithoutChangingIt(t *testing.T) {
	var stdout bytes.Buffer
	control := sqlite.SyncControlRow{State: "blocked", Reason: "trust_failure", MembershipMode: "normal"}
	code := importPartialEffectResult(&stdout, sqlite.SyncJobRow{JobID: "import-partial", State: "recovering"}, control, "coherent partial state")
	if code != 13 {
		t.Fatalf("code=%d output=%s", code, stdout.String())
	}
	for _, want := range []string{`"state":"uncertain"`, `"reason":"partial_effect"`, `"import_state":"recovering"`, `"control_reason":"trust_failure"`} {
		if !bytes.Contains(stdout.Bytes(), []byte(want)) {
			t.Fatalf("output missing %s: %s", want, stdout.String())
		}
	}
}

func TestE21BlockReconcileUsesEmergencyMembershipExitClass(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureSyncControl(requestCtx(), "wiki-pair", "cfg-1", "2026-09-21T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReconcileAdoptedMembership(requestCtx(), "wiki-pair", "blocked_emergency", "membership-emergency", "cfg-1", "2026-09-21T00:00:01Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.HoldSyncControl(requestCtx(), "wiki-pair", "conflict", "cfg-1", "2026-09-21T00:00:02Z"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := blockReconcile(&stdout, &stderr, store, "wiki-pair", "cfg-1", "conflict", "history_diverged", errors.New("diverged history"))
	if code != 30 || stderr.Len() != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{`"reason":"history_diverged"`, `"control_reason":"conflict"`, `"membership_mode":"blocked_emergency"`} {
		if !bytes.Contains(stdout.Bytes(), []byte(want)) {
			t.Fatalf("output missing %s: %s", want, stdout.String())
		}
	}
}

func TestE21PartialEffectGateExcludesValidatedNoEffectFence(t *testing.T) {
	for _, state := range []string{"applying", "recovering", "uncertain"} {
		if !importStateMayHavePartialEffect(state) {
			t.Fatalf("state %s must stop at the partial-effect gate", state)
		}
	}
	for _, state := range []string{"requested", "fetched", "validated", "applied", "blocked", "deferred"} {
		if importStateMayHavePartialEffect(state) {
			t.Fatalf("state %s must retain its own recovery path", state)
		}
	}
}

func TestE21RecoverPendingImportConvergesCoherentPartialState(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, gitPath, repo, "init", "-q", "-b", "main")
	gitTestRun(t, gitPath, repo, "config", "user.name", "Test")
	gitTestRun(t, gitPath, repo, "config", "user.email", "test@example.invalid")
	beforeBytes := []byte("before\n")
	afterBytes := []byte("after\n")
	beforeOther := []byte("other-before\n")
	afterOther := []byte("other-after\n")
	if err := os.WriteFile(filepath.Join(repo, "note.md"), beforeBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "other.md"), beforeOther, 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, gitPath, repo, "add", "note.md", "other.md")
	gitTestRun(t, gitPath, repo, "commit", "-q", "-m", "before")
	from := gitTestOutput(t, gitPath, repo, "rev-parse", "HEAD")
	client, err := gitlocal.New(repo, gitlocal.Limits{Timeout: 30 * time.Second, MaxOutput: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	baseState, err := client.InspectImport(requestCtx(), "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "note.md"), afterBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "other.md"), afterOther, 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, gitPath, repo, "add", "note.md", "other.md")
	gitTestRun(t, gitPath, repo, "commit", "-q", "-m", "after")
	target := gitTestOutput(t, gitPath, repo, "rev-parse", "HEAD")
	gitTestRun(t, gitPath, repo, "reset", "--hard", from)
	if err := os.WriteFile(filepath.Join(repo, "note.md"), afterBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load("../../docs/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Sync.ContentRef = "refs/heads/main"
	resource := cfg.Resources[cfg.Sync.Resource]
	resource.Root = repo
	cfg.Resources[cfg.Sync.Resource] = resource
	revision, ok := config.SyncRevision(cfg)
	if !ok {
		t.Fatal("sync revision unavailable")
	}
	incarnation := localSyncIncarnation(cfg)
	cfg.Sync.ImportAcknowledgement = &config.SyncImportAcknowledgement{
		SchemaVersion: "agent-dispatch.sync-import-acknowledgement/v1", AcknowledgementID: "acknowledgement-test",
		GroupID: cfg.Sync.GroupID, ResourceID: cfg.Sync.Resource, RemoteName: cfg.Sync.RemoteName,
		RemoteRepositoryDigest: cfg.Sync.RemoteRepositoryDigest, ContentRef: cfg.Sync.ContentRef, MembershipRef: cfg.Sync.MembershipRef,
		ScopeDigest: config.SyncScopeDigest(cfg, cfg.Sync.Resource), LocalInstanceID: cfg.Sync.LocalInstanceID,
		StateIncarnationID: incarnation, AdministratorKey: cfg.Sync.AdministratorKey,
		SafetyPolicyDigest: config.SyncSafetyPolicyDigest(), ImportBoundsDigest: config.SyncImportBoundsDigest(cfg.Sync.Bounds), ConfigRevision: revision,
	}
	if !config.SyncAcknowledgementCurrent(cfg, incarnation) {
		t.Fatal("test acknowledgement is stale")
	}
	membership := strings.Repeat("3", 40)
	record, err := syncrecords.NewImport(syncrecords.ImportBinding{
		GroupID: cfg.Sync.GroupID, FromCommit: from, TargetCommit: target, MembershipRevision: membership,
		AcknowledgementID: cfg.Sync.ImportAcknowledgement.AcknowledgementID, ResourceObservationRevision: 1,
		ExpectedGitStateDigest: baseState.Digest, HistoryEvidenceID: "history-evidence-test", CaseMode: config.CaseMode(), State: "validated", Reason: "none",
	}, []syncrecords.ImportPath{
		{Path: "note.md", Before: syncimport.Digest(beforeBytes), After: syncimport.Digest(afterBytes)},
		{Path: "other.md", Before: syncimport.Digest(beforeOther), After: syncimport.Digest(afterOther)},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := syncrecords.CanonicalImport(record)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(dir); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterResource(nil, cfg.Sync.Resource, "resource-revision", repo, repo, "markdown", "enabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(`UPDATE resources SET observation_revision=1 WHERE resource_id=?`, cfg.Sync.Resource); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureSyncControl(requestCtx(), cfg.Sync.GroupID, revision, "2026-09-21T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	job, _, err := store.AdmitSyncJob(requestCtx(), sqlite.SyncJobInput{JobID: "recover-coherent-partial", GroupID: cfg.Sync.GroupID, Kind: "import", LogicalKey: record.ImportID, InitialState: "recovering", PayloadJSON: string(payload), ConfigRevision: revision, QueueLimit: 10, Now: "2026-09-21T00:00:01Z"})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	handled, code := recoverPendingImport(&stdout, &stderr, cfg, cfg.Sync, revision, store, client, syncmembership.History{}, membership, target)
	if !handled || code != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"state":"recovered"`)) {
		t.Fatalf("recovery handled=%v code=%d out=%s err=%s", handled, code, stdout.String(), stderr.String())
	}
	stored, found, err := store.FindSyncJob(requestCtx(), cfg.Sync.GroupID, "import", record.ImportID)
	if err != nil || !found || stored.JobID != job.JobID || stored.State != "applied" || stored.ResolvedAt == "" {
		t.Fatalf("stored recovery=%+v found=%v err=%v", stored, found, err)
	}
	if got := gitTestOutput(t, gitPath, repo, "rev-parse", cfg.Sync.ContentRef); got != target {
		t.Fatalf("content ref=%s want=%s", got, target)
	}
	if raw, err := os.ReadFile(filepath.Join(repo, "other.md")); err != nil || !bytes.Equal(raw, afterOther) {
		t.Fatalf("mixed recovery did not converge the remaining effect: raw=%q err=%v", raw, err)
	}

	gitTestRun(t, gitPath, repo, "reset", "--hard", from)
	if err := os.WriteFile(filepath.Join(repo, "unrelated.tmp"), []byte("local-only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unclaimedRecord, err := syncrecords.NewImport(syncrecords.ImportBinding{
		GroupID: cfg.Sync.GroupID, FromCommit: from, TargetCommit: target, MembershipRevision: membership,
		AcknowledgementID: cfg.Sync.ImportAcknowledgement.AcknowledgementID, ResourceObservationRevision: 2,
		ExpectedGitStateDigest: baseState.Digest, HistoryEvidenceID: "history-evidence-unclaimed", CaseMode: config.CaseMode(), State: "validated", Reason: "none",
	}, []syncrecords.ImportPath{{Path: "note.md", Before: syncimport.Digest(beforeBytes), After: syncimport.Digest(afterBytes)}})
	if err != nil {
		t.Fatal(err)
	}
	unclaimedPayload, err := syncrecords.CanonicalImport(unclaimedRecord)
	if err != nil {
		t.Fatal(err)
	}
	unclaimedJob, _, err := store.AdmitSyncJob(requestCtx(), sqlite.SyncJobInput{JobID: "validated-unclaimed", GroupID: cfg.Sync.GroupID, Kind: "import", LogicalKey: unclaimedRecord.ImportID, InitialState: "validated", PayloadJSON: string(unclaimedPayload), ConfigRevision: revision, QueueLimit: 10, Now: "2026-09-21T00:00:02Z"})
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	handled, code = recoverPendingImport(&stdout, &stderr, cfg, cfg.Sync, revision, store, client, syncmembership.History{}, membership, target)
	if handled || code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("unclaimed current plan handled=%v code=%d out=%s err=%s", handled, code, stdout.String(), stderr.String())
	}
	stored, found, err = store.FindSyncJob(requestCtx(), cfg.Sync.GroupID, "import", unclaimedRecord.ImportID)
	if err != nil || !found || stored.JobID != unclaimedJob.JobID || stored.State != "validated" || stored.Fence != 0 {
		t.Fatalf("unclaimed current plan=%+v found=%v err=%v", stored, found, err)
	}
	journals, err := store.LoadSyncJournals(requestCtx(), unclaimedJob.JobID)
	if err != nil || len(journals) != 0 {
		t.Fatalf("current-plan journals=%+v err=%v", journals, err)
	}

	stdout.Reset()
	stderr.Reset()
	handled, code = recoverPendingImport(&stdout, &stderr, cfg, cfg.Sync, revision, store, client, syncmembership.History{}, membership, strings.Repeat("4", 40))
	if handled || code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("unclaimed obsolete plan handled=%v code=%d out=%s err=%s", handled, code, stdout.String(), stderr.String())
	}
	stored, found, err = store.FindSyncJob(requestCtx(), cfg.Sync.GroupID, "import", unclaimedRecord.ImportID)
	if err != nil || !found || stored.State != "deferred" || stored.Fence != 1 || stored.ResolvedAt == "" {
		t.Fatalf("unclaimed obsolete plan=%+v found=%v err=%v", stored, found, err)
	}
	journals, err = store.LoadSyncJournals(requestCtx(), unclaimedJob.JobID)
	if err != nil || len(journals) != 1 || journals[0].Outcome != "effect_not_started" || !strings.Contains(journals[0].EvidenceJSON, `"basis":"durable_state_invariant"`) {
		t.Fatalf("obsolete-plan journals=%+v err=%v", journals, err)
	}
}

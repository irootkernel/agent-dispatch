package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/gitlocal"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/syncmembership"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func TestControllerImportRecoveryAfterIndexInstallation(t *testing.T) {
	for _, condition := range []string{"exact", "recovery_hold", "uncertain", "foreign_controller", "index_lock", "paused", "stale_acknowledgement"} {
		t.Run(condition, func(t *testing.T) {
			f := newResumeFixture(t)
			cfg, s := f.cfg, f.cfg.Sync
			client, err := membershipGitClient(cfg, s)
			if err != nil {
				t.Fatal(err)
			}
			from := gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD")
			controller := filepath.Join(f.repo, ".agent-dispatch-sync", "publications", "publication-recovery.json")
			if err := os.MkdirAll(filepath.Dir(controller), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(controller, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			gitTestRun(t, f.git, f.repo, "add", ".agent-dispatch-sync")
			gitTestRun(t, f.git, f.repo, "commit", "-q", "-m", "controller-only advance")
			target := gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD")
			gitTestRun(t, f.git, f.repo, "reset", "--hard", from)
			local := filepath.Join(f.repo, "local.md")
			if err := os.WriteFile(local, []byte("staged local\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			gitTestRun(t, f.git, f.repo, "add", "local.md")
			if err := os.WriteFile(local, []byte("unstaged local\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			localIndex := gitTestOutput(t, f.git, f.repo, "ls-files", "--stage", "local.md")
			before, err := client.InspectImport(requestCtx(), s.ContentRef)
			if err != nil {
				t.Fatal(err)
			}
			revision, _ := config.SyncRevision(cfg)
			s.ImportAcknowledgement = &config.SyncImportAcknowledgement{
				SchemaVersion: "agent-dispatch.sync-import-acknowledgement/v1", AcknowledgementID: "acknowledgement-controller",
				GroupID: s.GroupID, ResourceID: s.Resource, RemoteName: s.RemoteName,
				RemoteRepositoryDigest: s.RemoteRepositoryDigest, ContentRef: s.ContentRef, MembershipRef: s.MembershipRef,
				ScopeDigest: config.SyncScopeDigest(cfg, s.Resource), LocalInstanceID: s.LocalInstanceID,
				StateIncarnationID: localSyncIncarnation(cfg), AdministratorKey: s.AdministratorKey,
				SafetyPolicyDigest: config.SyncSafetyPolicyDigest(), ImportBoundsDigest: config.SyncImportBoundsDigest(s.Bounds), ConfigRevision: revision,
			}
			membership := strings.Repeat("3", 40)
			record, err := syncrecords.NewImport(syncrecords.ImportBinding{
				GroupID: s.GroupID, FromCommit: from, TargetCommit: target, MembershipRevision: membership,
				AcknowledgementID: s.ImportAcknowledgement.AcknowledgementID, ResourceObservationRevision: 1,
				ExpectedGitStateDigest: before.Digest, HistoryEvidenceID: "history-evidence-controller",
				CaseMode: config.CaseMode(), ControllerOnly: true, State: "validated", Reason: "none",
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := syncrecords.CanonicalImport(record)
			job, _, err := f.store.AdmitSyncJob(requestCtx(), sqlite.SyncJobInput{JobID: "controller-import", GroupID: s.GroupID, Kind: "import", LogicalKey: record.ImportID, InitialState: "validated", PayloadJSON: string(payload), ConfigRevision: revision, QueueLimit: 10, Now: "2026-09-21T00:00:00Z"})
			if err != nil {
				t.Fatal(err)
			}
			job, err = f.store.ClaimSyncJob(requestCtx(), job.JobID, "interrupted-owner", revision, "2026-09-21T00:00:01Z", "2026-09-21T00:00:03Z")
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.BeginControllerImportApply(requestCtx(), job.JobID, job.ClaimOwner, job.Fence, sqlite.SyncJournalEntry{JournalID: "controller-start", JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "started", EvidenceJSON: `{}`, RecordedAt: "2026-09-21T00:00:02Z"}, "2026-09-21T00:00:02Z"); err != nil {
				t.Fatal(err)
			}
			faultDir := t.TempDir()
			fault := fmt.Sprintf("#!/bin/sh\nfor arg in \"$@\"; do\n if [ \"$arg\" = update-ref ]; then exit 1; fi\ndone\nexec %q \"$@\"\n", f.git)
			if err := os.WriteFile(filepath.Join(faultDir, "git"), []byte(fault), 0o700); err != nil {
				t.Fatal(err)
			}
			originalPath := os.Getenv("PATH")
			t.Setenv("PATH", faultDir+string(os.PathListSeparator)+originalPath)
			faultClient, err := gitlocal.New(f.repo, gitlocal.Limits{Timeout: 30 * time.Second, MaxOutput: 1 << 20})
			if err != nil {
				t.Fatal(err)
			}
			if err := faultClient.ApplyImportIndex(requestCtx(), s.ContentRef, from, target, nil, nil); err == nil {
				t.Fatal("expected interruption after controller/index installation")
			}
			t.Setenv("PATH", originalPath)
			if err := client.CheckAdvanceContentRef(requestCtx(), s.ContentRef, from, target); err != nil {
				t.Fatal(err)
			}
			if condition == "uncertain" {
				at := "2026-09-21T00:00:02.5Z"
				if err := f.store.FinishSyncJob(requestCtx(), job.JobID, job.ClaimOwner, job.Fence, "uncertain", sqlite.SyncJobKeepUnresolved, sqlite.SyncJournalEntry{JournalID: "controller-uncertain", JobID: job.JobID, Fence: job.Fence, Phase: "import", Outcome: "effect_unknown", EvidenceJSON: `{}`, RecordedAt: at}, at); err != nil {
					t.Fatal(err)
				}
			}
			if condition == "recovery_hold" || condition == "uncertain" {
				if _, err := f.store.Exec(`UPDATE sync_controls SET state='blocked',reason='recovery_required' WHERE group_id=?`, s.GroupID); err != nil {
					t.Fatal(err)
				}
			} else if condition == "foreign_controller" {
				if err := os.WriteFile(filepath.Join(filepath.Dir(controller), "foreign.json"), []byte("local-only\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				gitTestRun(t, f.git, f.repo, "add", ".agent-dispatch-sync/publications/foreign.json")
			} else if condition == "index_lock" {
				if err := os.WriteFile(filepath.Join(f.repo, ".git", "index.lock"), []byte("existing lock"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if condition == "paused" {
				if _, err := f.store.Exec(`UPDATE sync_controls SET state='paused',reason='operator_pause' WHERE group_id=?`, s.GroupID); err != nil {
					t.Fatal(err)
				}
			} else if condition == "stale_acknowledgement" {
				s.ImportAcknowledgement = nil
			}
			indexBefore := gitTestOutput(t, f.git, f.repo, "ls-files", "--stage")
			var out, stderr bytes.Buffer
			handled, code := recoverPendingImport(&out, &stderr, cfg, s, revision, f.store, client, syncmembership.History{}, membership, target)
			if !handled {
				t.Fatal("recovery did not handle the interrupted controller import")
			}
			stored, found, err := f.store.FindSyncJob(requestCtx(), s.GroupID, "import", record.ImportID)
			if err != nil || !found {
				t.Fatalf("job: %+v %v", stored, err)
			}
			if condition == "exact" || condition == "recovery_hold" || condition == "uncertain" {
				wantCode := 0
				if condition != "exact" {
					wantCode = 30
				}
				if code != wantCode || stored.State != "applied" || stored.ResolvedAt == "" || stored.Fence <= job.Fence || gitTestOutput(t, f.git, f.repo, "rev-parse", s.ContentRef) != target {
					t.Fatalf("recovery code=%d job=%+v out=%s err=%s", code, stored, out.String(), stderr.String())
				}
				control, err := f.store.LoadSyncControl(requestCtx(), s.GroupID)
				if err != nil || (condition != "exact" && (control.State != "blocked" || control.Reason != "recovery_required")) {
					t.Fatalf("hold changed: %+v %v", control, err)
				}
			} else {
				wantCode, wantReason := 13, "partial_effect"
				if condition == "paused" {
					wantCode, wantReason = 0, "operator_pause"
				} else if condition == "stale_acknowledgement" {
					wantCode, wantReason = 0, "acknowledgement_stale"
				}
				if code != wantCode || !bytes.Contains(out.Bytes(), []byte(`"reason":"`+wantReason+`"`)) || stderr.Len() != 0 || stored.ResolvedAt != "" || gitTestOutput(t, f.git, f.repo, "rev-parse", s.ContentRef) != from || gitTestOutput(t, f.git, f.repo, "ls-files", "--stage") != indexBefore {
					t.Fatalf("unsafe recovery code=%d job=%+v out=%s err=%s", code, stored, out.String(), stderr.String())
				}
			}
			if got := gitTestOutput(t, f.git, f.repo, "ls-files", "--stage", "local.md"); got != localIndex {
				t.Fatal("recovery changed unrelated staged entries")
			}
			if raw, err := os.ReadFile(local); err != nil || string(raw) != "unstaged local\n" {
				t.Fatalf("recovery changed unrelated live bytes: %q %v", raw, err)
			}
		})
	}
}

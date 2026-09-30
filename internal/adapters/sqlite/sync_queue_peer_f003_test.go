package sqlite

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

func peerF003Job(id, kind, state string, limit int) SyncJobInput {
	in := syncJobFixture(id, id, `{}`)
	in.Kind, in.InitialState, in.QueueLimit = kind, state, limit
	return in
}

func peerF003Nudge(id string, limit int) PeerNudgeInput {
	in := peerNudge(id)
	in.GroupID, in.QueueLimit = "wiki-pair", limit
	return in
}

func peerF003Control(t *testing.T, s *Store, group string) {
	t.Helper()
	if _, err := s.EnsureSyncControl(context.Background(), group, "cfg-1", syncT0); err != nil {
		t.Fatal(err)
	}
}

func peerF003Counts(t *testing.T, s *Store, jobs, nudges int) {
	t.Helper()
	var gotJobs, gotNudges int
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE group_id='wiki-pair' AND resolved_at IS NULL`).Scan(&gotJobs); err != nil {
		t.Fatal(err)
	}
	if err := s.QueryRow(`SELECT COUNT(*) FROM sync_peer_nudges WHERE group_id='wiki-pair' AND processed_at IS NULL`).Scan(&gotNudges); err != nil {
		t.Fatal(err)
	}
	if gotJobs != jobs || gotNudges != nudges {
		t.Fatalf("pending jobs/nudges = %d/%d, want %d/%d", gotJobs, gotNudges, jobs, nudges)
	}
}

func TestPeerF003QueueAdmissionBothDirections(t *testing.T) {
	ctx := context.Background()
	for kind, state := range map[string]string{"publication": "eligible", "import": "validated", "verification": "planned"} {
		for _, jobFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/job-first=%v", kind, jobFirst), func(t *testing.T) {
				s := openTestStore(t)
				peerF003Control(t, s, "wiki-pair")
				job := peerF003Job("job-1", kind, state, 2)
				nudge := peerF003Nudge("pub-1", 2)
				if jobFirst {
					if _, _, err := s.AdmitSyncJob(ctx, job); err != nil {
						t.Fatal(err)
					}
					if _, _, err := s.AdmitSyncJob(ctx, peerF003Job("job-fill", kind, state, 2)); err != nil {
						t.Fatal(err)
					}
					if _, err := s.AdmitPeerNudge(ctx, nudge); !errors.Is(err, ErrSyncQueueFull) {
						t.Fatalf("job must block nudge admission: %v", err)
					}
					peerF003Counts(t, s, 2, 0)
				} else {
					if _, err := s.AdmitPeerNudge(ctx, nudge); err != nil {
						t.Fatal(err)
					}
					if _, _, err := s.AdmitSyncJob(ctx, peerF003Job("job-fill", kind, state, 2)); err != nil {
						t.Fatal(err)
					}
					if _, _, err := s.AdmitSyncJob(ctx, job); !errors.Is(err, ErrSyncQueueFull) {
						t.Fatalf("nudge must block job admission: %v", err)
					}
					peerF003Counts(t, s, 1, 1)
				}
			})
		}
	}
}

func TestPeerF003QueueReplayAtMixedBound(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	peerF003Control(t, s, "wiki-pair")
	job := peerF003Job("job-1", "publication", "eligible", 3)
	nudge := peerF003Nudge("pub-1", 3)
	if _, _, err := s.AdmitSyncJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdmitPeerNudge(ctx, nudge); err != nil {
		t.Fatal(err)
	}
	// Both exact replays must work even after the configured limit shrinks.
	job.QueueLimit, nudge.QueueLimit = 2, 2
	job.JobID, job.PayloadJSON = "replacement-id", `{ }`
	if row, reused, err := s.AdmitSyncJob(ctx, job); err != nil || !reused || row.JobID != "job-1" {
		t.Fatalf("job replay: row=%+v reused=%v err=%v", row, reused, err)
	}
	if duplicate, err := s.AdmitPeerNudge(ctx, nudge); err != nil || !duplicate {
		t.Fatalf("nudge replay: duplicate=%v err=%v", duplicate, err)
	}
	job.PayloadJSON = `{"changed":true}`
	if _, _, err := s.AdmitSyncJob(ctx, job); !errors.Is(err, ErrSyncAdmissionConflict) {
		t.Fatalf("job conflict must precede capacity check: %v", err)
	}
	nudge.PayloadJSON = `{"changed":true}`
	if _, err := s.AdmitPeerNudge(ctx, nudge); !errors.Is(err, ErrSyncAdmissionConflict) {
		t.Fatalf("nudge conflict must precede capacity check: %v", err)
	}
	if _, _, err := s.AdmitSyncJob(ctx, peerF003Job("job-2", "verification", "planned", 2)); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("new job at mixed bound: %v", err)
	}
	if _, err := s.AdmitPeerNudge(ctx, peerF003Nudge("pub-2", 2)); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("new nudge at mixed bound: %v", err)
	}
	peerF003Counts(t, s, 1, 1)
	row, err := loadSyncJob(ctx, s.DB, "job-1")
	if err != nil || row.PayloadJSON != `{}` || row.ResolvedAt != "" {
		t.Fatalf("original job must survive refusals: %+v %v", row, err)
	}
	rows, err := s.LoadPendingPeerNudges(ctx, "wiki-pair", 2)
	if err != nil || len(rows) != 1 || rows[0].PayloadJSON != peerF003Nudge("pub-1", 2).PayloadJSON {
		t.Fatalf("original nudge must survive refusals: %+v %v", rows, err)
	}
}

func TestPeerF003QueueScopeAndResolvedHistory(t *testing.T) {
	ctx := context.Background()
	for _, jobNext := range []bool{true, false} {
		t.Run(fmt.Sprintf("job-next=%v", jobNext), func(t *testing.T) {
			s := openTestStore(t)
			for _, group := range []string{"wiki-pair", "other-pair"} {
				peerF003Control(t, s, group)
				job := peerF003Job("job-"+group, "verification", "planned", 2)
				nudge := peerF003Nudge("pub-"+group, 2)
				job.GroupID, nudge.GroupID = group, group
				if _, _, err := s.AdmitSyncJob(ctx, job); err != nil {
					t.Fatal(err)
				}
				if _, err := s.AdmitPeerNudge(ctx, nudge); err != nil {
					t.Fatal(err)
				}
			}
			// Seed completed job history; admission must use resolution, not row count.
			if _, err := s.Exec(`UPDATE sync_jobs SET state='incomplete',resolved_at=? WHERE job_id='job-wiki-pair'`, syncT1); err != nil {
				t.Fatal(err)
			}
			nudge := peerF003Nudge("pub-wiki-pair", 2)
			if err := s.ResolvePeerNudge(ctx, nudge.GroupID, nudge.PublicationID, nudge.Fingerprint, "covered", syncT1); err != nil {
				t.Fatal(err)
			}
			if jobNext {
				if _, _, err := s.AdmitSyncJob(ctx, peerF003Job("job-new", "import", "validated", 1)); err != nil {
					t.Fatalf("resolved history and other group must not block job: %v", err)
				}
				peerF003Counts(t, s, 1, 0)
			} else {
				if _, err := s.AdmitPeerNudge(ctx, peerF003Nudge("pub-new", 2)); err != nil {
					t.Fatalf("resolved history and other group must not block nudge: %v", err)
				}
				peerF003Counts(t, s, 0, 1)
			}
		})
	}
}

func TestPeerF003QueueConcurrentMixedAdmissions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Close() })
	if err := first.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	peerF003Control(t, first, "wiki-pair")
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Close() })
	start := make(chan struct{})
	type result struct {
		id  int
		job bool
		err error
	}
	results := make(chan result, 12)
	for i := 0; i < cap(results); i++ {
		go func() {
			<-start
			// Each connection competes to admit both kinds of obligation.
			s := []*Store{first, second}[(i/2)%2]
			r := result{id: i, job: i%2 == 0}
			if r.job {
				_, _, r.err = s.AdmitSyncJob(ctx, peerF003Job(fmt.Sprintf("job-%d", i), "verification", "planned", 3))
			} else {
				_, r.err = s.AdmitPeerNudge(ctx, peerF003Nudge(fmt.Sprintf("pub-%d", i), 3))
			}
			results <- r
		}()
	}
	close(start)
	jobs, nudges, full := 0, 0, 0
	var accepted []result
	for i := 0; i < cap(results); i++ {
		r := <-results
		switch {
		case r.err == nil:
			accepted = append(accepted, r)
			if r.job {
				jobs++
			} else {
				nudges++
			}
		case errors.Is(r.err, ErrSyncQueueFull):
			full++
		default:
			t.Errorf("concurrent admission: %v", r.err)
		}
	}
	if jobs+nudges != 3 || full != 9 {
		t.Fatalf("accepted jobs/nudges=%d/%d full=%d, want 3 total and 9 full", jobs, nudges, full)
	}
	peerF003Counts(t, first, jobs, nudges)
	for _, r := range accepted {
		if r.job {
			row, err := loadSyncJob(ctx, first.DB, fmt.Sprintf("job-%d", r.id))
			if err != nil || row.ResolvedAt != "" || row.PayloadJSON != `{}` {
				t.Fatalf("accepted job lost or changed: %+v %v", row, err)
			}
		} else {
			in := peerF003Nudge(fmt.Sprintf("pub-%d", r.id), 3)
			if duplicate, err := second.AdmitPeerNudge(ctx, in); err != nil || !duplicate {
				t.Fatalf("accepted nudge replay: duplicate=%v err=%v", duplicate, err)
			}
		}
	}
}

func TestPeerF003QueuePublicationDeliveryReplacementAtMixedBound(t *testing.T) {
	ctx := context.Background()
	for _, recovered := range []bool{false, true} {
		t.Run(fmt.Sprintf("recovered=%v", recovered), func(t *testing.T) {
			s := openTestStore(t)
			peerF003Control(t, s, "wiki-pair")
			job := peerF003Job("publication", "publication", "eligible", 2)
			if _, _, err := s.AdmitSyncJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AdmitPeerNudge(ctx, peerF003Nudge("inbox", 2)); err != nil {
				t.Fatal(err)
			}
			claim, err := s.ClaimSyncJob(ctx, job.JobID, "publisher", "cfg-1", syncT0, syncT3)
			if err != nil {
				t.Fatal(err)
			}
			journal := func(outcome, at string) SyncJournalEntry {
				return SyncJournalEntry{JournalID: "journal-" + outcome, JobID: job.JobID, Fence: claim.Fence,
					Phase: "publication", Outcome: outcome, EvidenceJSON: `{}`, RecordedAt: at}
			}
			for _, state := range []string{"prepared", "signed"} {
				if err := s.AdvanceSyncJob(ctx, job.JobID, "publisher", claim.Fence, state, journal(state, syncT0), syncT0); err != nil {
					t.Fatal(err)
				}
			}
			delivery := SyncJobInput{JobID: "delivery", GroupID: job.GroupID, Kind: "delivery", LogicalKey: job.LogicalKey,
				InitialState: "pending", PayloadJSON: `{}`, QueueLimit: 2, Now: syncT1}
			if recovered {
				if err := s.FinishSyncJob(ctx, job.JobID, "publisher", claim.Fence, "uncertain", SyncJobKeepUnresolved, journal("effect_unknown", syncT1), syncT1); err != nil {
					t.Fatal(err)
				}
				_, err = s.FinishRecoveredPublicationJob(ctx, job.JobID, claim.Fence, journal("published", syncT2), delivery, syncT2)
			} else {
				_, err = s.FinishPublicationJob(ctx, job.JobID, "publisher", claim.Fence, journal("published", syncT1), delivery, syncT1)
			}
			if err != nil {
				t.Fatalf("one-for-one replacement at aggregate bound: %v", err)
			}
			peerF003Counts(t, s, 1, 1)
			publication, err := loadSyncJob(ctx, s.DB, job.JobID)
			if err != nil || publication.State != "published" || publication.ResolvedAt == "" {
				t.Fatalf("publication completion: %+v %v", publication, err)
			}
			successor, err := loadSyncJob(ctx, s.DB, delivery.JobID)
			if err != nil || successor.State != "pending" || successor.ResolvedAt != "" {
				t.Fatalf("delivery obligation: %+v %v", successor, err)
			}
		})
	}
}

// Pre-policy inboxes and lowered bounds may already be saturated. Keep their
// replay evidence and refuse new work until the operator restores capacity.
func TestPeerF003QueueLegacyFullInboxPreservesObligations(t *testing.T) {
	ctx := context.Background()
	for _, limit := range []int{2, 3} {
		t.Run(fmt.Sprintf("queue=%d", limit), func(t *testing.T) {
			s := openTestStore(t)
			peerF003Control(t, s, "wiki-pair")
			const from = "1111111111111111111111111111111111111111"
			const target = "2222222222222222222222222222222222222222"
			const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			var inputs []PeerNudgeInput
			for i := 0; i < limit; i++ {
				nudge := syncrecords.Nudge{
					SchemaVersion: syncrecords.NudgeSchema, GroupID: "wiki-pair",
					PublicationID: fmt.Sprintf("publication-%d", i), Sender: "node-a", Receiver: "node-b",
					MembershipRevision: from, ContentRef: "refs/heads/wiki-sync", TargetCommit: target,
				}
				payload, err := syncrecords.CanonicalNudge(nudge)
				if err != nil {
					t.Fatal(err)
				}
				in := PeerNudgeInput{GroupID: nudge.GroupID, PublicationID: nudge.PublicationID,
					Fingerprint: fmt.Sprintf("sha256:%x", sha256.Sum256(payload)), PayloadJSON: string(payload),
					QueueLimit: limit, ReceivedAt: syncT0}
				// Seed a pre-policy inbox; current admission must never create it.
				if _, err := s.Exec(`INSERT INTO sync_peer_nudges
					(group_id,publication_id,request_fingerprint,payload_json,received_at)
					VALUES (?,?,?,?,?)`, in.GroupID, in.PublicationID, in.Fingerprint, in.PayloadJSON, in.ReceivedAt); err != nil {
					t.Fatal(err)
				}
				inputs = append(inputs, in)
			}
			record, err := syncrecords.NewImport(syncrecords.ImportBinding{
				GroupID: "wiki-pair", FromCommit: from, TargetCommit: target, MembershipRevision: from,
				AcknowledgementID: "acknowledgement-current", ResourceObservationRevision: 1,
				ExpectedGitStateDigest: digest, HistoryEvidenceID: "history-verified", CaseMode: "sensitive",
				State: "validated", Reason: "none",
			}, []syncrecords.ImportPath{{Path: "note.md", Before: "absent", After: digest}})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := syncrecords.CanonicalImport(record)
			if err != nil {
				t.Fatal(err)
			}
			in := peerF003Job("import-job", "import", "validated", limit)
			in.LogicalKey, in.PayloadJSON = record.ImportID, string(payload)
			started, err := time.Parse(time.RFC3339Nano, syncT0)
			if err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 3; attempt++ {
				// Advance past failure backoff deterministically, without sleeping.
				at := started.Add(time.Duration(attempt) * 10 * time.Minute)
				attemptID := fmt.Sprintf("recovery-%d", attempt)
				if due, err := s.ReserveSyncRecovery(ctx, in.GroupID, attemptID, at, true); err != nil || !due {
					t.Fatalf("reserve recovery: due=%v err=%v", due, err)
				}
				in.JobID, in.Now = fmt.Sprintf("import-job-%d", attempt), at.Format(time.RFC3339Nano)
				if _, reused, err := s.AdmitSyncJob(ctx, in); !errors.Is(err, ErrSyncQueueFull) || reused {
					t.Fatalf("import reservation at full inbox: reused=%v err=%v", reused, err)
				}
				for _, nudge := range inputs {
					if err := s.RecordPeerNudgeFailure(ctx, nudge.GroupID, nudge.PublicationID, nudge.Fingerprint, SyncRecoveryReasonReconcileFailed); err != nil {
						t.Fatal(err)
					}
				}
				if err := s.CompleteSyncRecovery(ctx, in.GroupID, attemptID, at, false, SyncRecoveryReasonReconcileFailed); err != nil {
					t.Fatal(err)
				}
				if _, found, err := s.FindSyncJob(ctx, in.GroupID, "import", record.ImportID); err != nil || found {
					t.Fatalf("refused import must not exist: found=%v err=%v", found, err)
				}
				peerF003Counts(t, s, 0, limit)
				backlog, err := s.LoadPeerNudgeBacklog(ctx, in.GroupID)
				if err != nil || backlog.Pending != limit || backlog.Failed != limit || backlog.Retained != limit || backlog.OldestReason != SyncRecoveryReasonReconcileFailed {
					t.Fatalf("cycle must retain the full inbox with visible failure: %+v %v", backlog, err)
				}
				t.Logf("attempt=%d import_present=false pending_nudges=%d retained_nudges=%d reason=%s", attempt+1, backlog.Pending, backlog.Retained, backlog.OldestReason)
			}
			for _, nudge := range inputs {
				var original string
				if err := s.QueryRow(`SELECT payload_json FROM sync_peer_nudges WHERE group_id=? AND publication_id=? AND processed_at IS NULL AND resolution='pending'`, nudge.GroupID, nudge.PublicationID).Scan(&original); err != nil || original != nudge.PayloadJSON {
					t.Fatalf("hint must retain original payload and pending historical resolution: payload=%s err=%v", original, err)
				}
				if duplicate, err := s.AdmitPeerNudge(ctx, nudge); err != nil || !duplicate {
					t.Fatalf("retained replay after failed recovery: duplicate=%v err=%v", duplicate, err)
				}
			}
			schedule, found, err := s.LoadSyncRecoverySchedule(ctx, in.GroupID)
			if err != nil || !found || schedule.ConsecutiveFailures != 3 || schedule.LastSuccessAt != "" || schedule.LastReason != SyncRecoveryReasonReconcileFailed {
				t.Fatalf("retry schedule must not claim progress: %+v found=%v err=%v", schedule, found, err)
			}
		})
	}
}

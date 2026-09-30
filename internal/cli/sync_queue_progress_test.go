package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/domain/syncrecords"
)

// Exercise real signed history, Git import, and inbox settlement. The only
// remote transport is a fixture that measures refs and exposes local objects.
func TestSyncQueuePolicyInboxImportsBeforeSettlingNudge(t *testing.T) {
	testSyncQueueInboxImport(t, syncImportFixtureOptions{})
}

func TestSyncQueuePolicyDeferredImportReopensAfterWriterIdle(t *testing.T) {
	testSyncQueueInboxImport(t, syncImportFixtureOptions{deferForWriter: true})
}

func TestSyncImportDefersUntrackedAncestorBeforeEffects(t *testing.T) {
	testSyncQueueInboxImport(t, syncImportFixtureOptions{ancestorCollision: true})
}

func TestSyncImportAdministratorCheckpointCoversOrdinaryHistory(t *testing.T) {
	for _, history := range []string{"checkpoint", "reviewed_snapshot", "publication_after_checkpoint", "uncovered_publication", "unsigned_tail", "wrong_target", "wrong_predecessor", "wrong_signer", "history_bound"} {
		t.Run(history, func(t *testing.T) {
			testSyncQueueInboxImport(t, syncImportFixtureOptions{history: history})
		})
	}
}

type syncImportFixtureOptions struct {
	deferForWriter, ancestorCollision bool
	history                           string
}

func testSyncQueueInboxImport(t *testing.T, options syncImportFixtureOptions) {
	t.Helper()
	deferForWriter := options.deferForWriter
	f := newResumeFixture(t)
	f.cfg.Sync.Bounds.Queue = 2
	if options.history == "history_bound" {
		f.cfg.Sync.Bounds.HistoryCommits = 1
	}
	peerKey, peerFingerprint := e21t3Key(t, t.TempDir(), "queue-peer")
	f.cfg.Sync.Nodes[1].PublisherKey = peerFingerprint
	client, err := membershipGitClient(f.cfg, f.cfg.Sync)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := syncrecords.NewPlan("bootstrap", "", nil, nil, configuredMembers(f.cfg.Sync), membershipBinding(f.cfg, f.cfg.Sync))
	if err != nil {
		t.Fatal(err)
	}
	membership := f.signMembership(t, client, plan, "")
	initial := gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD")
	before := map[string][]byte{"note.md": []byte("base\n")}
	digest, err := snapshotDigestFromFiles(before)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := syncrecords.NewCheckpointPlan(f.cfg.Sync.GroupID, "initial_baseline", initial, digest,
		config.SyncScopeDigest(f.cfg, f.cfg.Sync.Resource), config.SyncContractDigest(), membership, initial, f.cfg.Sync.AdministratorKey)
	if err != nil {
		t.Fatal(err)
	}
	cpRaw, err := syncrecords.CanonicalCheckpoint(cp.ProposedCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	planRaw, err := syncrecords.CanonicalCheckpointPlan(cp)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := client.SnapshotTree(context.Background(), initial, map[string][]byte{
		".agent-dispatch-sync/checkpoints/" + cp.ProposedCheckpoint.CheckpointID + ".json": cpRaw,
		".agent-dispatch-sync/checkpoint-plans/" + cp.PlanID + ".json":                     planRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := client.CreateSignedContentCommit(context.Background(), tree, f.adminKey, initial, time.Now(), "administrator", "Queue test baseline")
	if err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, f.git, f.repo, "reset", "--hard", base)
	after := map[string][]byte{"note.md": []byte("imported\n")}
	if options.ancestorCollision {
		after["zblocked/leaf.md"] = []byte("remote child\n")
	}
	peer := f.cfg.Sync.Nodes[1]
	publication, err := syncrecords.NewPublication(syncrecords.PublicationBinding{
		GroupID: f.cfg.Sync.GroupID, Publisher: peer.InstanceID, StateIncarnationID: peer.StateIncarnationID,
		MembershipRevision: membership, ContentRef: f.cfg.Sync.ContentRef, BaseCommit: base,
		ScopeDigest: config.SyncScopeDigest(f.cfg, f.cfg.Sync.Resource), ContractDigest: config.SyncContractDigest(),
		SourceRevision: 1, ReceiptIDs: []string{"receipt-queue"},
	}, syncrecords.SnapshotFiles(after))
	if err != nil {
		t.Fatal(err)
	}
	pubRaw, err := syncrecords.CanonicalPublication(publication)
	if err != nil {
		t.Fatal(err)
	}
	publicationWrites := map[string][]byte{
		"note.md": after["note.md"], ".agent-dispatch-sync/publications/" + publication.PublicationID + ".json": pubRaw,
	}
	if options.ancestorCollision {
		publicationWrites["zblocked/leaf.md"] = after["zblocked/leaf.md"]
	}
	tree, err = client.SnapshotTree(context.Background(), base, publicationWrites)
	if err != nil {
		t.Fatal(err)
	}
	target, err := client.CreateSignedContentCommit(context.Background(), tree, peerKey, base, time.Now(), "publisher", "Queue test publication")
	if err != nil {
		t.Fatal(err)
	}
	if options.history != "" {
		tree, err = client.SnapshotTree(context.Background(), base, after)
		if err != nil {
			t.Fatal(err)
		}
		ordinary := gitTestOutput(t, f.git, f.repo, "-c", "commit.gpgsign=false", "commit-tree", tree, "-p", base, "-m", "Ordinary reviewed resolution")
		checkpointTarget := ordinary
		if options.history == "wrong_target" {
			checkpointTarget = base
		} else if options.history == "reviewed_snapshot" {
			checkpointTarget = target
		}
		digest, err := snapshotDigestFromFiles(after)
		if err != nil {
			t.Fatal(err)
		}
		predecessor := ordinary
		if options.history == "wrong_predecessor" {
			predecessor = base
		}
		resolution, err := syncrecords.NewCheckpointPlan(f.cfg.Sync.GroupID, "conflict_resolution", checkpointTarget, digest,
			config.SyncScopeDigest(f.cfg, f.cfg.Sync.Resource), config.SyncContractDigest(), membership, predecessor, f.cfg.Sync.AdministratorKey)
		if err != nil {
			t.Fatal(err)
		}
		checkpointRaw, err := syncrecords.CanonicalCheckpoint(resolution.ProposedCheckpoint)
		if err != nil {
			t.Fatal(err)
		}
		resolutionRaw, err := syncrecords.CanonicalCheckpointPlan(resolution)
		if err != nil {
			t.Fatal(err)
		}
		tree, err = client.SnapshotTree(context.Background(), ordinary, map[string][]byte{
			".agent-dispatch-sync/checkpoints/" + resolution.ProposedCheckpoint.CheckpointID + ".json": checkpointRaw,
			".agent-dispatch-sync/checkpoint-plans/" + resolution.PlanID + ".json":                     resolutionRaw,
		})
		if err != nil {
			t.Fatal(err)
		}
		key := f.adminKey
		if options.history == "wrong_signer" {
			key = peerKey
		}
		target, err = client.CreateSignedContentCommit(context.Background(), tree, key, ordinary, time.Now(), "administrator", "Reviewed resolution checkpoint")
		if err != nil {
			t.Fatal(err)
		}
		if options.history == "unsigned_tail" {
			tree, err = client.SnapshotTree(context.Background(), target, map[string][]byte{"note.md": []byte("uncovered\n")})
			if err != nil {
				t.Fatal(err)
			}
			target = gitTestOutput(t, f.git, f.repo, "-c", "commit.gpgsign=false", "commit-tree", tree, "-p", target, "-m", "Uncovered tail")
		} else if options.history == "publication_after_checkpoint" || options.history == "uncovered_publication" {
			parent := target
			if options.history == "uncovered_publication" {
				parent = ordinary
			}
			after["note.md"] = []byte("published after resolution\n")
			publication, err = syncrecords.NewPublication(syncrecords.PublicationBinding{
				GroupID: f.cfg.Sync.GroupID, Publisher: peer.InstanceID, StateIncarnationID: peer.StateIncarnationID,
				MembershipRevision: membership, ContentRef: f.cfg.Sync.ContentRef, BaseCommit: parent,
				ScopeDigest: config.SyncScopeDigest(f.cfg, f.cfg.Sync.Resource), ContractDigest: config.SyncContractDigest(),
				SourceRevision: 2, ReceiptIDs: []string{"receipt-after-resolution"},
			}, syncrecords.SnapshotFiles(after))
			if err != nil {
				t.Fatal(err)
			}
			pubRaw, err = syncrecords.CanonicalPublication(publication)
			if err != nil {
				t.Fatal(err)
			}
			tree, err = client.SnapshotTree(context.Background(), parent, map[string][]byte{
				"note.md": after["note.md"], ".agent-dispatch-sync/publications/" + publication.PublicationID + ".json": pubRaw,
			})
			if err != nil {
				t.Fatal(err)
			}
			target, err = client.CreateSignedContentCommit(context.Background(), tree, peerKey, parent, time.Now(), "publisher", "Publication after resolution")
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	revision, _ := config.SyncRevision(f.cfg)
	f.cfg.Sync.ImportAcknowledgement = &config.SyncImportAcknowledgement{
		SchemaVersion: "agent-dispatch.sync-import-acknowledgement/v1", AcknowledgementID: "acknowledgement-queue",
		GroupID: f.cfg.Sync.GroupID, ResourceID: f.cfg.Sync.Resource, RemoteName: f.cfg.Sync.RemoteName,
		RemoteRepositoryDigest: f.cfg.Sync.RemoteRepositoryDigest, ContentRef: f.cfg.Sync.ContentRef, MembershipRef: f.cfg.Sync.MembershipRef,
		ScopeDigest: config.SyncScopeDigest(f.cfg, f.cfg.Sync.Resource), LocalInstanceID: f.cfg.Sync.LocalInstanceID,
		StateIncarnationID: localSyncIncarnation(f.cfg), AdministratorKey: f.cfg.Sync.AdministratorKey,
		SafetyPolicyDigest: config.SyncSafetyPolicyDigest(), ImportBoundsDigest: config.SyncImportBoundsDigest(f.cfg.Sync.Bounds), ConfigRevision: revision,
	}
	f.writeConfig(t)
	if err := f.store.RegisterResource(nil, f.cfg.Sync.Resource, "resource-queue", f.repo, f.repo, "markdown", "enabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Exec(`UPDATE resources SET observation_revision=1 WHERE resource_id=?`, f.cfg.Sync.Resource); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SetSyncControl(context.Background(), f.cfg.Sync.GroupID, 1, "active", revision, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := fmt.Sprintf(`#!/bin/sh
case " $* " in
  *" remote get-url "*) printf '%%s\n' 'ssh://git@example.invalid/resume'; exit 0;;
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
      *" fetch "*) exec %s -C %s update-ref "$destination" "$oid";;
    esac
    exit 0;;
  *" push "*|*" clone "*) exit 97;;
esac
exec %s "$@"
`, quote(f.cfg.Sync.MembershipRef), quote(membership), quote(f.cfg.Sync.ContentRef), quote(target), quote(f.git), quote(f.repo), quote(f.git))
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SYNC_NODE_B_TO_A", "test-inbound-credential")
	if options.ancestorCollision || options.history != "" {
		if options.ancestorCollision {
			if err := os.WriteFile(filepath.Join(f.repo, "zblocked"), []byte("local blocker\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		gitTestRun(t, f.git, f.repo, "update-index", "--refresh")
		indexBefore := mustRead(t, filepath.Join(f.repo, ".git", "index"))
		var stdout, stderr bytes.Buffer
		code := Run([]string{"sync", "reconcile", "--group", f.cfg.Sync.GroupID}, &stdout, &stderr)
		if options.history == "checkpoint" || options.history == "reviewed_snapshot" || options.history == "publication_after_checkpoint" {
			if code != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"state":"applied"`)) {
				t.Fatalf("checkpoint catch-up: %d out=%s err=%s", code, stdout.String(), stderr.String())
			}
			if got := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef); got != target {
				t.Fatalf("checkpoint target not imported: %s", got)
			}
			if got := mustRead(t, filepath.Join(f.repo, "note.md")); !bytes.Equal(got, after["note.md"]) {
				t.Fatalf("checkpoint snapshot not imported: %q", got)
			}
			return
		}
		wantCode, reason := 30, "trust_failed"
		if options.ancestorCollision {
			wantCode, reason = 0, "untracked_collision"
		} else if options.history == "history_bound" {
			reason = "bound_exhausted"
		}
		if code != wantCode || !bytes.Contains(stdout.Bytes(), []byte(`"reason":"`+reason+`"`)) {
			t.Fatalf("unsafe import: %d out=%s err=%s", code, stdout.String(), stderr.String())
		}
		if got := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef); got != base {
			t.Fatalf("ref changed before admission: %s", got)
		}
		if !bytes.Equal(indexBefore, mustRead(t, filepath.Join(f.repo, ".git", "index"))) || !bytes.Equal(before["note.md"], mustRead(t, filepath.Join(f.repo, "note.md"))) {
			t.Fatal("refused import changed index or Markdown")
		}
		if options.ancestorCollision {
			if got := string(mustRead(t, filepath.Join(f.repo, "zblocked"))); got != "local blocker\n" {
				t.Fatalf("local blocker changed: %q", got)
			}
			var applied int
			if err := f.store.QueryRow(`SELECT COUNT(*) FROM sync_import_effects WHERE applied_at IS NOT NULL`).Scan(&applied); err != nil || applied != 0 {
				t.Fatalf("admission refusal applied effects: %d %v", applied, err)
			}
			var fence int
			var state string
			if err := f.store.QueryRow(`SELECT fence,state FROM sync_jobs WHERE kind='import'`).Scan(&fence, &state); err != nil || fence != 0 || state != "deferred" {
				t.Fatalf("admission refusal claimed import: %d %s %v", fence, state, err)
			}
		}
		return
	}
	deferredJobID := ""
	if deferForWriter {
		routeID := ""
		for id, route := range f.cfg.Routes {
			if route.Source.Resource == f.cfg.Sync.Resource {
				revision, _ := config.RouteRevision(f.cfg, id)
				if err := f.store.RegisterRoute(nil, id, revision, config.PolicyRevision(route), f.cfg.Sync.Resource, route.Destinations[0].Target, "{}", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
					t.Fatal(err)
				}
				if err := f.store.InitializeRouteState(nil, id); err != nil {
					t.Fatal(err)
				}
				routeID = id
				break
			}
		}
		if routeID == "" {
			t.Fatal("fixture has no writer route")
		}
		seedE21T3Eligibility(t, f.cfg, f.path, before["note.md"])
		if _, err := f.store.Exec(`UPDATE destination_lane_state SET active_dispatch_id=NULL WHERE route_id=?`, routeID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.Exec(`UPDATE route_runtime_state SET active_dispatch_id=? WHERE route_id=?`, "dispatch-"+routeID, routeID); err != nil {
			t.Fatal(err)
		}
		headBefore := gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD")
		gitTestRun(t, f.git, f.repo, "update-index", "--refresh")
		indexBefore := mustRead(t, filepath.Join(f.repo, ".git", "index"))
		for attempt := 0; attempt < 2; attempt++ {
			var stdout, stderr bytes.Buffer
			if code := Run([]string{"sync", "reconcile", "--group", f.cfg.Sync.GroupID}, &stdout, &stderr); code != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"reason":"resource_busy"`)) {
				t.Fatalf("writer deferral: %d out=%s err=%s", code, stdout.String(), stderr.String())
			}
			var fence, attempts int
			var state, resolved, payload string
			if err := f.store.QueryRow(`SELECT job_id,state,fence,attempts,resolved_at,payload_json FROM sync_jobs WHERE kind='import'`).Scan(&deferredJobID, &state, &fence, &attempts, &resolved, &payload); err != nil {
				t.Fatal(err)
			}
			if state != "deferred" || fence != 0 || attempts != 0 || resolved == "" {
				t.Fatalf("admission deferral claimed or unresolved: %s %d %d %q", state, fence, attempts, resolved)
			}
			record, err := syncrecords.DecodeImport([]byte(payload))
			if err != nil || record.State != "validated" || record.Reason != "none" {
				t.Fatalf("immutable import changed: %+v %v", record, err)
			}
			if got := gitTestOutput(t, f.git, f.repo, "rev-parse", "HEAD"); got != headBefore {
				t.Fatalf("deferred HEAD moved: %s", got)
			}
			if !bytes.Equal(indexBefore, mustRead(t, filepath.Join(f.repo, ".git", "index"))) || !bytes.Equal(before["note.md"], mustRead(t, filepath.Join(f.repo, "note.md"))) {
				t.Fatal("deferral changed index or Markdown")
			}
		}
		if _, err := f.store.Exec(`UPDATE route_runtime_state SET active_dispatch_id=NULL WHERE route_id=?`, routeID); err != nil {
			t.Fatal(err)
		}
		if idle, err := f.store.ResourceWritersIdle(context.Background(), f.cfg.Sync.Resource); err != nil || !idle {
			t.Fatalf("writer did not become idle: %v %v", idle, err)
		}
	}
	svc := &peerService{
		cfg: f.cfg, configPath: f.path, store: f.store,
		slots: make(chan struct{}, 2), rate: map[string]peerRate{},
	}
	svc.members = svc.currentMembers
	nudge := e22t1Nudge()
	nudge.PublicationID, nudge.MembershipRevision, nudge.TargetCommit = publication.PublicationID, membership, target
	raw, err := syncrecords.CanonicalNudge(nudge)
	if err != nil {
		t.Fatal(err)
	}
	if response := e22t1Request(t, svc, "/v1/sync/nudges", raw, "test-inbound-credential", nil); response.Code != http.StatusAccepted {
		t.Fatalf("admission: %d %s", response.Code, response.Body.String())
	}
	extra := nudge
	extra.PublicationID = "publication-extra"
	extraRaw, _ := syncrecords.CanonicalNudge(extra)
	if response := e22t1Request(t, svc, "/v1/sync/nudges", extraRaw, "test-inbound-credential", nil); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("extra hint must leave the work slot free: %d %s", response.Code, response.Body.String())
	}
	svc.reconcile = func(context.Context) (bool, string) {
		backlog, err := f.store.LoadPeerNudgeBacklog(context.Background(), f.cfg.Sync.GroupID)
		if err != nil || backlog.Pending != 1 || backlog.Retained != 1 {
			t.Fatalf("hint must remain pending before import: %+v %v", backlog, err)
		}
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"sync", "reconcile", "--group", f.cfg.Sync.GroupID}, &stdout, &stderr); code != 0 {
			t.Fatalf("reconcile: %d out=%s err=%s", code, stdout.String(), stderr.String())
		}
		state, target := parseReconcileResult(stdout.Bytes())
		t.Logf("reconcile result: %s", stdout.String())
		return settledReconcileResult(state, target)
	}
	rows, err := f.store.LoadPendingPeerNudges(context.Background(), f.cfg.Sync.GroupID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if settled, reason := svc.processInboxRows(context.Background(), rows); !settled {
		t.Fatalf("inbox did not settle after import: %s", reason)
	}
	if got := string(mustRead(t, filepath.Join(f.repo, "note.md"))); got != "imported\n" {
		t.Fatalf("Markdown not imported: %q", got)
	}
	if got := gitTestOutput(t, f.git, f.repo, "rev-parse", f.cfg.Sync.ContentRef); got != target {
		t.Fatalf("content ref=%s want=%s", got, target)
	}
	var resolution string
	if err := f.store.QueryRow(`SELECT resolution FROM sync_peer_nudges WHERE publication_id=?`, publication.PublicationID).Scan(&resolution); err != nil || resolution != "covered" {
		t.Fatalf("historical resolution=%q err=%v", resolution, err)
	}
	backlog, err := f.store.LoadPeerNudgeBacklog(context.Background(), f.cfg.Sync.GroupID)
	if err != nil || backlog.Pending != 0 || backlog.Retained != 1 {
		t.Fatalf("reconciled hint evidence: %+v %v", backlog, err)
	}
	if response := e22t1Request(t, svc, "/v1/sync/nudges", raw, "test-inbound-credential", nil); response.Code != http.StatusAccepted {
		t.Fatalf("settled replay: %d", response.Code)
	}
	var imports int
	if err := f.store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='import' AND state='applied' AND resolved_at IS NOT NULL`).Scan(&imports); err != nil || imports != 1 {
		t.Fatalf("durable applied import count=%d err=%v", imports, err)
	}
	if deferForWriter {
		var jobID string
		var fence, attempts int
		if err := f.store.QueryRow(`SELECT job_id,fence,attempts FROM sync_jobs WHERE kind='import' AND state='applied'`).Scan(&jobID, &fence, &attempts); err != nil {
			t.Fatal(err)
		}
		if jobID != deferredJobID || fence != 1 || attempts != 1 {
			t.Fatalf("resume must reuse job and obtain its first claim: %s fence=%d attempts=%d", jobID, fence, attempts)
		}
		var recoveryJournals, totalImports int
		if err := f.store.QueryRow(`SELECT COUNT(*) FROM sync_journal_entries WHERE job_id=? AND phase='claim_recovery'`, jobID).Scan(&recoveryJournals); err != nil || recoveryJournals != 0 {
			t.Fatalf("unclaimed deferral invented a recovery journal: %d %v", recoveryJournals, err)
		}
		if err := f.store.QueryRow(`SELECT COUNT(*) FROM sync_jobs WHERE kind='import'`).Scan(&totalImports); err != nil || totalImports != 1 {
			t.Fatalf("deferral replay duplicated the import: %d %v", totalImports, err)
		}
	}
	t.Logf("queue=2: reserved import completed; content_ref=%s; nudge covered; fingerprint=sha256:%x", target, sha256.Sum256(raw))
}

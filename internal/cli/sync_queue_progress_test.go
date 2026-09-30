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
	f := newResumeFixture(t)
	f.cfg.Sync.Bounds.Queue = 2
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
	tree, err = client.SnapshotTree(context.Background(), base, map[string][]byte{
		"note.md": after["note.md"], ".agent-dispatch-sync/publications/" + publication.PublicationID + ".json": pubRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := client.CreateSignedContentCommit(context.Background(), tree, peerKey, base, time.Now(), "publisher", "Queue test publication")
	if err != nil {
		t.Fatal(err)
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
	t.Logf("queue=2: reserved import completed; content_ref=%s; nudge covered; fingerprint=sha256:%x", target, sha256.Sum256(raw))
}

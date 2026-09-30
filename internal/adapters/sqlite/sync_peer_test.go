package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func peerNudge(id string) PeerNudgeInput {
	return PeerNudgeInput{
		GroupID: "pair-a", PublicationID: id, Fingerprint: "sha256:" + id,
		PayloadJSON: `{"publication_id":"` + id + `"}`, QueueLimit: 3, ReceivedAt: syncT0,
	}
}

func TestPeerNudgeAdmissionIdentityAndBound(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	first := peerNudge("pub-1")
	if duplicate, err := s.AdmitPeerNudge(ctx, first); err != nil || duplicate {
		t.Fatalf("first admission: duplicate=%v err=%v", duplicate, err)
	}
	if duplicate, err := s.AdmitPeerNudge(ctx, first); err != nil || !duplicate {
		t.Fatalf("exact replay: duplicate=%v err=%v", duplicate, err)
	}
	changed := first
	changed.Fingerprint = "sha256:changed"
	if _, err := s.AdmitPeerNudge(ctx, changed); !errors.Is(err, ErrSyncAdmissionConflict) {
		t.Fatalf("same publication with a different fingerprint: %v", err)
	}
	changed = first
	changed.PayloadJSON = `{"publication_id":"pub-1","changed":true}`
	if _, err := s.AdmitPeerNudge(ctx, changed); !errors.Is(err, ErrSyncAdmissionConflict) {
		t.Fatalf("same fingerprint with different exact payload: %v", err)
	}
	if _, err := s.AdmitPeerNudge(ctx, peerNudge("pub-2")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdmitPeerNudge(ctx, peerNudge("pub-3")); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("third pending nudge must fail: %v", err)
	}
	if duplicate, err := s.AdmitPeerNudge(ctx, first); err != nil || !duplicate {
		t.Fatalf("replay at bound: duplicate=%v err=%v", duplicate, err)
	}
	other := peerNudge("pub-3")
	other.GroupID = "pair-b"
	if _, err := s.AdmitPeerNudge(ctx, other); err != nil {
		t.Fatalf("other group must have its own bound: %v", err)
	}
	rows, err := s.LoadPendingPeerNudges(ctx, "pair-a", 1)
	if err != nil || len(rows) != 1 || rows[0].PublicationID != first.PublicationID || rows[0].PayloadJSON != first.PayloadJSON {
		t.Fatalf("bounded FIFO pending load: %+v %v", rows, err)
	}
	if err := s.RecordPeerNudgeFailure(ctx, first.GroupID, first.PublicationID, first.Fingerprint, "reconcile_failed"); err != nil {
		t.Fatal(err)
	}
	backlog, err := s.LoadPeerNudgeBacklog(ctx, first.GroupID)
	if err != nil || backlog.Pending != 2 || backlog.Failed != 1 || backlog.OldestPendingAt != syncT0 || backlog.OldestReason != "reconcile_failed" {
		t.Fatalf("visible retained backlog: %+v %v", backlog, err)
	}
}

func TestPeerNudgeBacklogUsesSequenceForTimestampAndReason(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	first := peerNudge("pub-old")
	first.ReceivedAt = "2026-09-25T01:00:00Z"
	second := peerNudge("pub-new")
	second.ReceivedAt = "2026-09-25T01:00:00.9Z"
	for _, nudge := range []PeerNudgeInput{first, second} {
		if _, err := s.AdmitPeerNudge(ctx, nudge); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordPeerNudgeFailure(ctx, first.GroupID, first.PublicationID, first.Fingerprint, "reconcile_failed"); err != nil {
		t.Fatal(err)
	}
	backlog, err := s.LoadPeerNudgeBacklog(ctx, first.GroupID)
	if err != nil || backlog.OldestPendingAt != first.ReceivedAt || backlog.OldestReason != "reconcile_failed" {
		t.Fatalf("oldest pending row: %+v %v", backlog, err)
	}
}

func TestPeerNudgeRetainedLedgerBoundKeepsExactReplay(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	first := peerNudge("pub-first")
	first.QueueLimit = 2
	if _, err := s.admitPeerNudge(ctx, first, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolvePeerNudge(ctx, first.GroupID, first.PublicationID, first.Fingerprint, "covered", syncT1); err != nil {
		t.Fatal(err)
	}
	second := peerNudge("pub-second")
	second.QueueLimit = 2
	if _, err := s.admitPeerNudge(ctx, second, 2); err != nil {
		t.Fatal(err)
	}
	third := peerNudge("pub-third")
	third.QueueLimit = 2
	if _, err := s.admitPeerNudge(ctx, third, 2); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("new identity after retained bound: %v", err)
	}
	if duplicate, err := s.admitPeerNudge(ctx, first, 2); err != nil || !duplicate {
		t.Fatalf("exact retained replay: duplicate=%v err=%v", duplicate, err)
	}
	backlog, err := s.LoadPeerNudgeBacklog(ctx, first.GroupID)
	if err != nil || backlog.Retained != 2 || backlog.Pending != 1 {
		t.Fatalf("bounded retained ledger: %+v %v", backlog, err)
	}
	third.GroupID = "other-group"
	if _, err := s.admitPeerNudge(ctx, third, 2); err != nil {
		t.Fatalf("other group should have separate bound: %v", err)
	}
}

func TestPeerNudgeRecoveryAndFencedCompletion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	first := peerNudge("pub-1")
	first.QueueLimit = 2
	if _, err := s.AdmitPeerNudge(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.LoadPendingPeerNudges(ctx, first.GroupID, 1)
	if err != nil || len(rows) != 1 || rows[0].Fingerprint != first.Fingerprint {
		t.Fatalf("crash recovery: %+v %v", rows, err)
	}
	if err := s.MarkPeerNudgeProcessed(ctx, first.GroupID, first.PublicationID, "sha256:stale", syncT1); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("stale fingerprint must not complete: %v", err)
	}
	if err := s.MarkPeerNudgeProcessed(ctx, first.GroupID, first.PublicationID, first.Fingerprint, syncT1); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPeerNudgeProcessed(ctx, first.GroupID, first.PublicationID, first.Fingerprint, syncT2); !errors.Is(err, ErrSyncPrecondition) {
		t.Fatalf("repeat completion must be fenced: %v", err)
	}
	rows, err = s.LoadPendingPeerNudges(ctx, first.GroupID, 1)
	if err != nil || len(rows) != 0 {
		t.Fatalf("completed nudge remains pending: %+v %v", rows, err)
	}
	if duplicate, err := s.AdmitPeerNudge(ctx, first); err != nil || !duplicate {
		t.Fatalf("processed replay must retain identity: duplicate=%v err=%v", duplicate, err)
	}
	second := peerNudge("pub-2")
	second.QueueLimit = 2
	if duplicate, err := s.AdmitPeerNudge(ctx, second); err != nil || duplicate {
		t.Fatalf("processed row must free pending capacity: duplicate=%v err=%v", duplicate, err)
	}
}

func TestPeerNudgeCommitFailureDoesNotAdmit(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	// The deferred foreign key lets INSERT succeed and makes COMMIT fail.
	for _, stmt := range []string{
		`CREATE TABLE peer_commit_parent (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE peer_commit_child (id INTEGER REFERENCES peer_commit_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
		`CREATE TRIGGER peer_commit_guard AFTER INSERT ON sync_peer_nudges BEGIN INSERT INTO peer_commit_child(id) VALUES (1); END`,
	} {
		if _, err := s.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if duplicate, err := s.AdmitPeerNudge(ctx, peerNudge("pub-failed")); err == nil || duplicate {
		t.Fatalf("failed commit must not report admission: duplicate=%v err=%v", duplicate, err)
	}
	rows, err := s.LoadPendingPeerNudges(ctx, "pair-a", 2)
	if err != nil || len(rows) != 0 {
		t.Fatalf("failed transaction left an obligation: %+v %v", rows, err)
	}
	if _, err := s.Exec(`DROP TRIGGER peer_commit_guard`); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := s.AdmitPeerNudge(ctx, peerNudge("pub-failed")); err != nil || duplicate {
		t.Fatalf("retry after rollback: duplicate=%v err=%v", duplicate, err)
	}
}

func TestPeerNudgeMigrationFromV25(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.migrations = Migrations[:25]
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if version, err := s.SchemaVersion(); err != nil || version != MaxSchemaVersion {
		t.Fatalf("v25 upgrade yielded schema %d: %v", version, err)
	}
	if _, err := s.AdmitPeerNudge(context.Background(), peerNudge("pub-after-upgrade")); err != nil {
		t.Fatalf("upgraded inbox unusable: %v", err)
	}
}

func TestPeerNudgeConcurrentAdmissionKeepsBound(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, store := range []*Store{first, second} {
		wg.Add(1)
		go func(i int, store *Store) {
			defer wg.Done()
			<-start
			in := peerNudge([]string{"pub-a", "pub-b"}[i])
			in.QueueLimit = 2
			_, err := store.AdmitPeerNudge(ctx, in)
			results <- err
		}(i, store)
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, full := 0, 0
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrSyncQueueFull):
			full++
		default:
			t.Fatalf("unexpected concurrent admission error: %v", err)
		}
	}
	rows, err := first.LoadPendingPeerNudges(ctx, "pair-a", 2)
	if err != nil || accepted != 1 || full != 1 || len(rows) != 1 {
		t.Fatalf("concurrent bound accepted=%d full=%d pending=%d err=%v", accepted, full, len(rows), err)
	}
}

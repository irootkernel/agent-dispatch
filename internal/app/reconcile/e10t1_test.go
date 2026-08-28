package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/localfs"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/domain/ids"
	"github.com/irootkernel/agent-dispatch/internal/domain/policy"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// e10t1OpenStore opens a migrated store whose registered resource root is
// a real disposable directory the test controls.
func e10t1OpenStore(t *testing.T, root string) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResource(nil, "vault-main", "res-rev-1", root, root, "markdown", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRoute(nil, "wiki-maintenance", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeRouteState(nil, "wiki-maintenance"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRouteActivation(context.Background(), "wiki-maintenance", "enabled", "route-rev-1", "", "2026-08-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	return s
}

func e10t1Service(t *testing.T, store FullStore, root string, maxHash int64) *FullService {
	t.Helper()
	resolver, err := localfs.NewResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := policy.NewEngine([]string{"**/*.md"}, nil, nil, nil, policy.CaseSensitive)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("c", 64)
	builds := 0
	return &FullService{
		Store: store, Resolver: resolver, Engine: engine,
		ResourceID: "vault-main", FileScope: "markdown", MaxHash: maxHash,
		Now: time.Now,
		IntentBuilder: func(routeID, reason, decisionID string, changes []records.ChangeItem) (ports.IntentInput, error) {
			builds++
			return ports.IntentInput{
				DispatchID: fmt.Sprintf("disp-reconcile-e10t1-%d-%s", builds, ids.RandomSuffix()),
				DecisionID: decisionID, RouteID: routeID, RouteRevision: "route-rev-1",
				TargetID: "hermes-kanban-main", TargetType: "hermes_kanban",
				ResourceID: "vault-main", Generation: 1,
				IdempotencyKey:     "agent-dispatch:v1:" + digest,
				ContentFingerprint: digest, ManifestDigest: "sha256:" + strings.Repeat("d", 64),
				RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: "{}",
				CreatedAt: "2026-08-26T00:00:00Z",
			}, nil
		},
	}
}

// e10t1SeedFacts stores the initial snapshot through the fence so later
// runs compare against a current baseline.
func e10t1SeedFacts(t *testing.T, s *sqlite.Store, facts []ports.PathFact) int64 {
	t.Helper()
	rev, err := s.ObservationRevision(context.Background(), "vault-main")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePathFacts(context.Background(), "vault-main", rev, facts, "2026-08-26T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	next, err := s.ObservationRevision(context.Background(), "vault-main")
	if err != nil {
		t.Fatal(err)
	}
	return next
}

// fenceTrapStore wraps the real store so the snapshot replacement — the
// transaction that must fence — is preceded by one ordinary ingestion
// mutation committed on a second connection, exactly as a Watchman
// arrival landing inside the enumeration window would land. The trap
// makes the interleaving deterministic without slowing the walk.
type fenceTrapStore struct {
	*sqlite.Store
	mutate func() error
}

func (f *fenceTrapStore) ReplacePathFacts(ctx context.Context, resourceID string, expectedRevision int64, facts []ports.PathFact, observedAt string) error {
	if f.mutate != nil {
		if err := f.mutate(); err != nil {
			return err
		}
		f.mutate = nil
	}
	return f.Store.ReplacePathFacts(ctx, resourceID, expectedRevision, facts, observedAt)
}

// e10t1LineageFor builds one ordinary dispatch lineage recording a fact
// for path, used as the concurrent writer inside the fence window.
func e10t1LineageFor(suffix, path, digest string) ports.Lineage {
	now := "2026-08-26T00:00:0" + suffix + "Z"
	d := "sha256:" + strings.Repeat(digest, 64)[:64]
	return ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: "obs-e10t1-" + suffix, SchemaVersion: "agent-dispatch.source-observation/v1",
			SourceType: "watchman", SourceID: "watchman-main", TriggerName: "trig", ResourceID: "vault-main",
			ObservedAt: now, ReceivedAt: now, RawPayloadDigest: d, IngestStatus: "accepted",
			Changes: []ports.ObservationChange{
				{Ordinal: 1, Path: path, Operation: "create", ExistsAfter: true, FileType: "regular", AfterDigest: d, DigestStatus: "known"},
			},
		},
		Batch: ports.BatchInput{
			BatchID: "batch-e10t1-" + suffix, RouteID: "wiki-maintenance", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: now, ContentFingerprint: d,
			ObservationIDs: []string{"obs-e10t1-" + suffix},
		},
		Decision: ports.DecisionInput{
			DecisionID: "decision-e10t1-" + suffix, BatchID: "batch-e10t1-" + suffix, RouteID: "wiki-maintenance",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch",
			Classification: "normal", CreatedAt: now, Actor: "planner",
		},
		Intent: ports.IntentInput{
			DispatchID: "dispatch-e10t1-" + suffix, DecisionID: "decision-e10t1-" + suffix, RouteID: "wiki-maintenance",
			RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes_kanban",
			ResourceID: "vault-main", Generation: 1, IdempotencyKey: "agent-dispatch:v1:" + d,
			ContentFingerprint: d, ManifestDigest: d,
			RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: "{}", CreatedAt: now,
		},
	}
}

// TestE10T1ConcurrentFactUpdateSurvivesFullReconciliation pins AC-604 /
// DUR-015 / TST-011: an ordinary path-fact update committing inside the
// reconciliation's enumeration window makes the stale snapshot refuse as
// a typed concurrent change — the newer fact survives untouched, the
// snapshot is not stored, and exactly one due reconciliation generation
// remains for the retry.
func TestE10T1ConcurrentFactUpdateSurvivesFullReconciliation(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Inbox", "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := e10t1OpenStore(t, root)
	sum := sha256.Sum256([]byte("a"))
	e10t1SeedFacts(t, s, []ports.PathFact{{Path: "Inbox/a.md", Digest: "sha256:" + hex.EncodeToString(sum[:]), Exists: true}})

	// The concurrent writer: a second store connection over the same
	// database file commits one ordinary ingestion fact for a brand-new
	// path inside the fence window (the trap fires immediately before
	// the fenced replacement).
	writer, err := sqlite.Open(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	trap := &fenceTrapStore{Store: s, mutate: func() error {
		if _, err := writer.Exec(`UPDATE route_runtime_state SET active_dispatch_id = NULL, active_generation = 0 WHERE route_id = 'wiki-maintenance'`); err != nil {
			return err
		}
		return writer.CommitLineage(context.Background(), e10t1LineageFor("1", "Inbox/newer.md", "n"))
	}}

	service := e10t1Service(t, trap, root, 1<<20)
	out, err := service.Run(context.Background(), "wiki-maintenance", "manual")
	if err != nil {
		t.Fatalf("the fenced refusal is a typed outcome, never an error: %v", err)
	}
	if !out.ConcurrentChange || out.SnapshotStored || !out.PendingReconcile {
		t.Fatalf("the concurrent-change outcome must be typed with one due generation: %+v", out)
	}
	facts, err := s.LoadPathFacts(context.Background(), "vault-main")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := facts["Inbox/newer.md"]; !ok {
		t.Fatalf("the newer fact must survive the refused replacement: %v", facts)
	}
	if fact, ok := facts["Inbox/newer.md"]; !ok || fact.Digest == "" {
		t.Fatalf("the surviving fact must carry its digest: %v", fact)
	}
	snap, err := s.LoadRouteState(context.Background(), "wiki-maintenance")
	if err != nil || !snap.PendingReconcile {
		t.Fatalf("one due reconciliation generation must remain: %+v %v", snap, err)
	}
	rev, err := s.ObservationRevision(context.Background(), "vault-main")
	if err != nil || rev != 2 { // seed replacement + the concurrent lineage
		t.Fatalf("only the real mutations may advance the revision: %d %v", rev, err)
	}
}

// TestE10T1UncontestedRunStoresSnapshotAndAdvances pins the unfenced
// baseline (DUR-014 positive arm): with no concurrent mutation the
// replacement stores, advances the revision exactly once, and reports no
// concurrent-change evidence.
func TestE10T1UncontestedRunStoresSnapshotAndAdvances(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Inbox", "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := e10t1OpenStore(t, root)
	sum := sha256.Sum256([]byte("a"))
	before := e10t1SeedFacts(t, s, []ports.PathFact{{Path: "Inbox/a.md", Digest: "sha256:" + hex.EncodeToString(sum[:]), Exists: true}})

	service := e10t1Service(t, s, root, 1<<20)
	out, err := service.Run(context.Background(), "wiki-maintenance", "manual")
	if err != nil {
		t.Fatalf("uncontested run: %v", err)
	}
	if out.ConcurrentChange || !out.SnapshotStored {
		t.Fatalf("an uncontested run must store the snapshot without conflict evidence: %+v", out)
	}
	after, err := s.ObservationRevision(context.Background(), "vault-main")
	if err != nil || after != before+1 {
		t.Fatalf("the stored snapshot must advance the revision exactly once: %d -> %d %v", before, after, err)
	}
}

// e10t1CountingReader is an endless byte stream that counts every byte
// consumed, proving the read bound by observation rather than timing.
type e10t1CountingReader struct {
	mu sync.Mutex
	n  int64
}

func (r *e10t1CountingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	r.mu.Lock()
	r.n += int64(len(p))
	r.mu.Unlock()
	return len(p), nil
}

func (r *e10t1CountingReader) count() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// TestE10T1BoundedReadNeverExceedsMaxPlusOne pins the OPS-012 read bound
// itself: against an endless stream the reconciliation hasher consumes
// exactly max+1 bytes and reports the over-bound refusal — the growing
// file can never push the read past the configured bound.
func TestE10T1BoundedReadNeverExceedsMaxPlusOne(t *testing.T) {
	const max int64 = 1024
	r := &e10t1CountingReader{}
	digest, n, over, err := records.SumBounded(io.Reader(r), max)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "" || n != max+1 || !over {
		t.Fatalf("an over-bound stream must consume exactly max+1 bytes and keep the digest unknown: digest %q n %d over %v", digest, n, over)
	}
	if consumed := r.count(); consumed != max+1 {
		t.Fatalf("the reader must never be pulled past max+1 bytes: consumed %d", consumed)
	}
	// A stream within the bound produces its digest.
	within := strings.NewReader("hello")
	digest, n, over, err = records.SumBounded(within, 16)
	if err != nil || n != 5 || over || !strings.HasPrefix(digest.String(), "sha256:") {
		t.Fatalf("an in-bound stream must hash: %q %d %v", digest, n, err)
	}
}

// TestE10T1StableOverBoundFileIsQuarantineEvidence pins the OPS-012
// quarantine arm end to end: a stable file beyond the configured bound
// is reported as explicit quarantine evidence with its digest unknown,
// never silently hashed or dropped.
func TestE10T1StableOverBoundFileIsQuarantineEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 64)
	if err := os.WriteFile(filepath.Join(root, "Inbox", "big.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	s := e10t1OpenStore(t, root)
	e10t1SeedFacts(t, s, []ports.PathFact{{Path: "Inbox/big.md", Digest: "", Exists: true}})
	service := e10t1Service(t, s, root, 16) // the bound sits far below the file

	digest, outcome := service.hash("Inbox/big.md")
	if digest != "" || outcome != hashOverBound {
		t.Fatalf("a stable over-bound file must classify as over-bound with no digest: %q %v", digest, outcome)
	}

	out, err := service.Run(context.Background(), "wiki-maintenance", "initial")
	if err != nil {
		t.Fatalf("reconcile with an over-bound file: %v", err)
	}
	if len(out.QuarantinedOverBound) != 1 || out.QuarantinedOverBound[0] != "Inbox/big.md" {
		t.Fatalf("the over-bound file must be explicit quarantine evidence: %+v", out.QuarantinedOverBound)
	}
	if len(out.UnstableAfterRetry) != 0 {
		t.Fatalf("a stable over-bound file is not instability evidence: %+v", out.UnstableAfterRetry)
	}
	facts, err := s.LoadPathFacts(context.Background(), "vault-main")
	if err != nil {
		t.Fatal(err)
	}
	if fact, ok := facts["Inbox/big.md"]; !ok || fact.Digest != "" || !fact.Exists {
		t.Fatalf("the quarantined file's fact must exist with an unknown digest: %+v", fact)
	}
}

// TestE10T1GrowingFileRaceStaysBoundedAndExplicit pins TST-011's race
// arm: a goroutine appending to an in-scope file while full
// reconciliation enumerates it can produce only the three honest
// outcomes — a stable digest, quarantine evidence, or instability
// evidence — and the stored fact never claims a digest for bytes beyond
// the bound. The assertions hold under every interleaving.
func TestE10T1GrowingFileRaceStaysBoundedAndExplicit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "Inbox", "growing.md")
	const max = 512 * 1024
	if err := os.WriteFile(target, make([]byte, max), 0o644); err != nil {
		t.Fatal(err)
	}
	s := e10t1OpenStore(t, root)

	stop := make(chan struct{})
	appended := make(chan struct{})
	go func() {
		defer close(appended)
		f, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		chunk := make([]byte, 64*1024)
		for i := 0; i < 200; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := f.Write(chunk); err != nil {
				return
			}
		}
	}()

	service := e10t1Service(t, s, root, max)
	out, err := service.Run(context.Background(), "wiki-maintenance", "initial")
	close(stop)
	<-appended
	if err != nil {
		t.Fatalf("the growing-file race must not fail the run: %v", err)
	}
	facts, err := s.LoadPathFacts(context.Background(), "vault-main")
	if err != nil {
		t.Fatal(err)
	}
	fact, ok := facts["Inbox/growing.md"]
	if !ok || !fact.Exists {
		t.Fatalf("the growing file must keep its fact: %+v", fact)
	}
	quarantined := containsPath(out.QuarantinedOverBound, "Inbox/growing.md")
	unstable := containsPath(out.UnstableAfterRetry, "Inbox/growing.md")
	switch {
	case fact.Digest != "":
		if quarantined || unstable {
			t.Fatalf("a digested file must not carry quarantine or instability evidence: %+v", out)
		}
	case quarantined && !unstable:
		// stable over-bound: quarantine evidence, digest unknown
	case unstable:
		// twice-unstable: explicit reconciliation evidence, digest unknown
	default:
		t.Fatalf("an unknown digest must carry explicit evidence: %+v", out)
	}
}

// containsPath reports list membership (the shared helper name in ingest
// is private to that package).
func containsPath(list []string, path string) bool {
	for _, p := range list {
		if p == path {
			return true
		}
	}
	return false
}

// TestE10T1HashStableRejectsChangedFile pins the ADR-0018 stability
// predicate directly: identical size and mtime hold; either changing
// refuses the digest.
func TestE10T1HashStableRejectsChangedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "f.md")
	if err := os.WriteFile(path, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !statsStable(before, after) {
		t.Fatal("an untouched file must be stable")
	}
	changed := fakeInfo{FileInfo: before, size: before.Size() + 1}
	if statsStable(before, changed) {
		t.Fatal("a size change must refuse the digest")
	}
	moved := fakeInfo{FileInfo: before, mtime: before.ModTime().Add(time.Nanosecond)}
	if statsStable(before, moved) {
		t.Fatal("an mtime change must refuse the digest")
	}
}

// fakeInfo overrides the two stability-relevant fields of a real stat.
type fakeInfo struct {
	os.FileInfo
	size  int64
	mtime time.Time
}

func (f fakeInfo) Size() int64        { return f.size }
func (f fakeInfo) ModTime() time.Time { return f.mtime }

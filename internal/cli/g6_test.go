package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/adapters/watchman"
	"github.com/irootkernel/agent-dispatch/internal/config"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Gate G6 (E10-T3): the source and reconciliation integrity gate. Every
// acceptance criterion drives the real CLI surface: the exact-root
// effective binding, exclusions, and removal proof over a disposable
// real-Watchman tree (AC-601 through AC-603, AC-601 amended by D-028),
// the fenced reconciliation and bounded hashing (AC-604/605), and the
// fresh-database migration leg. The per-criterion evidence table lives
// in docs/VALIDATION.md under Gate G6; the deterministic conflict and
// bound proofs live at the store and service levels and are named by
// each criterion below.

// The gate shares the e18t1 lifecycle helpers (runWatchmanArgs drives one
// watchman subcommand; bindingOf decodes the binding member) so the two
// surfaces cannot drift apart.

// TestG6AC601EffectiveBindingReported pins AC-601 (amended by D-028)
// against the real Watchman: a configured resource root installs as its
// own watch root, and status reports the configured root, the same root
// as the actual root, the schema-vestigial relative root ".", and the
// trigger identity on an unconstrained trigger — the same binding
// watchman test reports without server contact.
func TestG6AC601EffectiveBindingReported(t *testing.T) {
	if _, err := exec.LookPath("watchman"); err != nil {
		t.Skip("watchman binary not available")
	}
	configPath, _, vault := watchmanFixture(t)
	client := watchman.NewClient("")
	ctx := context.Background()
	t.Cleanup(func() {
		_, _, _, _ = runWatchmanArgs(t, configPath, "remove", "--yes")
		_ = client.WatchDelete(context.Background(), vault)
	})
	res, _, errb, code := runWatchmanArgs(t, configPath, "install")
	if code != 0 {
		t.Fatalf("install: %s", errb.String())
	}
	binding := bindingOf(t, res)
	if watchman.CanonicalRoot(binding.ActualRoot) != watchman.CanonicalRoot(vault) || binding.RelativeRoot != "." ||
		binding.ConfiguredRoot != vault || binding.TriggerName != "agent-dispatch.wiki.e18t1" {
		t.Fatalf("AC-601: the four binding values must be reported with the exact root: %+v", binding)
	}
	defs, err := client.TriggerList(ctx, vault)
	if err != nil {
		t.Fatal(err)
	}
	def, ok := watchman.FindTrigger(defs, "agent-dispatch.wiki.e18t1")
	if !ok || def.RelativeRoot != "" {
		t.Fatalf("AC-601: the trigger must never be subtree-constrained: %+v", def)
	}
	st, _, errb, code := runWatchmanArgs(t, configPath, "status")
	if code != 0 {
		t.Fatalf("status: %s", errb.String())
	}
	if st["state"] != "installed" || bindingOf(t, st) != binding {
		t.Fatalf("AC-601: status must report the same effective binding installed: %v", st)
	}
	te, _, errb, code := runWatchmanArgs(t, configPath, "test", "--route", "wiki", "--config", configPath)
	if code != 0 {
		t.Fatalf("test --route: %s", errb.String())
	}
	if bindingOf(t, te) != binding {
		t.Fatalf("AC-601: test must resolve the same effective binding: %v", te)
	}
}

// TestG6AC602OutOfRootAndExcludedCreateNoRecords pins AC-602 at the
// gate level: an excluded burst through the exact-root environment
// creates no child task and never hashes, while an in-scope burst
// creates exactly one.
func TestG6AC602OutOfRootAndExcludedCreateNoRecords(t *testing.T) {
	configPath, _, vault := watchmanFixture(t)
	t.Setenv("WATCHMAN_TRIGGER", "agent-dispatch.wiki.e18t1")
	t.Setenv("WATCHMAN_ROOT", vault)
	t.Setenv("WATCHMAN_SINCE", "c:1:2:3:3")
	t.Setenv("WATCHMAN_CLOCK", "c:1:2:3:4")
	g6RegisterEnabled(t, configPath)

	// The excluded-only burst: exact directory and file glob, both
	// configured-root-relative.
	excluded := `[{"name":"Secrets/keep.md","exists":true,"new":true,"size":6,"type":"f"},` +
		`{"name":"Inbox/noise-1.md","exists":true,"new":true,"size":4,"type":"f"}]`
	var out, errb bytes.Buffer
	withStdin(t, excluded, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("excluded burst: %s", errb.String())
	}
	if res := decodeEnvelope(t, &out); res["disposition"] != "drop" {
		t.Fatalf("AC-602: the excluded burst must drop: %v", res)
	}
	// The in-scope burst under the same exact-root environment flows.
	out.Reset()
	errb.Reset()
	withStdin(t, `[{"name":"Inbox/flow.md","exists":true,"new":true,"size":4,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("in-scope burst: %s", errb.String())
	}
	if res := decodeEnvelope(t, &out); res["dispatch_id"] == nil {
		t.Fatalf("AC-602: the in-scope burst must create its task: %v", res)
	}
	_, store, exit := openOperatorStore("g6", configPath, &bytes.Buffer{})
	if exit != 0 {
		t.Fatal("store open failed")
	}
	defer store.Close()
	var intents, hashed int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents); err != nil || intents != 1 {
		t.Fatalf("AC-602: exactly the in-scope task may exist: %d %v", intents, err)
	}
	if err := store.QueryRow(`SELECT COUNT(*) FROM observation_changes WHERE after_digest IS NOT NULL AND path LIKE 'Secrets/%'`).Scan(&hashed); err != nil || hashed != 0 {
		t.Fatalf("AC-602: the excluded path must never be hashed: %d %v", hashed, err)
	}
}

// g6RegisterEnabled materializes the durable route registration and the
// enabled acknowledgement exactly as first use does; D-028 needs no
// persisted binding for dispatch, so none is written here.
func g6RegisterEnabled(t *testing.T, configPath string) {
	t.Helper()
	_, store, exit := openOperatorStore("g6", configPath, &bytes.Buffer{})
	if exit != 0 {
		t.Fatal("store open failed")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerRouteState(requestCtx(), store, cfg, "wiki"); err != nil {
		t.Fatal(err)
	}
	rev, ok := config.RouteRevision(cfg, "wiki")
	if !ok {
		t.Fatal("route revision unavailable")
	}
	if err := store.SetRouteActivation(context.Background(), "wiki", "enabled", rev, "", "2026-08-26T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	store.Close()
}

// TestG6AC603RemoveProvesAbsenceEverywhere pins AC-603 with the watch
// topology changed: the managed trigger is additionally planted on a
// second watched root, and a successful remove proves absence on every
// applicable root by re-listing.
func TestG6AC603RemoveProvesAbsenceEverywhere(t *testing.T) {
	if _, err := exec.LookPath("watchman"); err != nil {
		t.Skip("watchman binary not available")
	}
	configPath, _, vault := watchmanFixture(t)
	client := watchman.NewClient("")
	ctx := context.Background()
	actual, err := client.EnsureWatch(ctx, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.WatchDelete(context.Background(), actual) })
	if _, _, errb, code := runWatchmanArgs(t, configPath, "install"); code != 0 {
		t.Fatalf("install: %s", errb.String())
	}
	second := t.TempDir()
	if err := os.WriteFile(filepath.Join(second, "park.md"), []byte("park"), 0o644); err != nil {
		t.Fatal(err)
	}
	secondRoot, err := client.EnsureWatch(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.WatchDelete(context.Background(), secondRoot) })
	stray := watchman.ManagedTrigger("agent-dispatch.wiki.e18t1", []string{"/bin/true"})
	if _, err := client.TriggerInstall(ctx, secondRoot, stray); err != nil {
		t.Fatalf("plant stray: %v", err)
	}
	res, _, errb, code := runWatchmanArgs(t, configPath, "remove", "--yes")
	if code != 0 {
		t.Fatalf("AC-603 remove: %s", errb.String())
	}
	list, _ := res["proof"].([]any)
	if len(list) < 2 {
		t.Fatalf("AC-603: the proof must cover every watched root: %v", res)
	}
	covered := map[string]bool{}
	for _, entry := range list {
		if m, ok := entry.(map[string]any); ok {
			if m["present"] != false {
				t.Fatalf("AC-603: the proof must show absence: %v", entry)
			}
			covered[m["watch_root"].(string)] = true
		}
	}
	if !covered[filepath.Clean(actual)] || !covered[filepath.Clean(secondRoot)] {
		t.Fatalf("AC-603: the proof must cover the exact root and the changed-topology root: %v", covered)
	}
	for _, root := range []string{actual, secondRoot} {
		defs, err := client.TriggerList(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		if _, present := watchman.FindTrigger(defs, "agent-dispatch.wiki.e18t1"); present {
			t.Fatalf("AC-603: the managed trigger must be absent on %s", root)
		}
	}
}

// TestG6AC604FencedReconciliation pins AC-604 through the CLI surface:
// an ordinary ingestion fact committed while a full reconciliation
// enumerates never loses — either the run refuses as a typed concurrent
// change leaving one due generation, or it stores a snapshot that still
// carries the newer fact after the next clean reconciliation. The
// deterministic interleaving is pinned by
// TestE10T1ConcurrentFactUpdateSurvivesFullReconciliation.
func TestG6AC604FencedReconciliation(t *testing.T) {
	configPath, _, vault := watchmanFixture(t)
	g6RegisterEnabled(t, configPath)
	// A vault wide enough to give the concurrent writer a real window.
	dir := filepath.Join(vault, "Inbox", "g6")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 120; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%03d.md", i)), []byte("g6"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Baseline reconciliation stores the initial snapshot.
	if _, errb, code := g6Reconcile(t, configPath); code != 0 {
		t.Fatalf("baseline reconcile: %s", errb.String())
	}
	// The concurrent writer commits one ordinary ingestion fact while
	// the gated reconciliation enumerates.
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-done // released right before the reconciliation starts
		// The concurrent ordinary ingestion: a real file on disk plus its
		// durable fact, exactly as a Watchman arrival lands.
		if err := os.WriteFile(filepath.Join(vault, "Inbox", "g6", "newer.md"), []byte("newer"), 0o644); err != nil {
			return
		}
		_, store, exit := openOperatorStore("g6", configPath, &bytes.Buffer{})
		if exit != 0 {
			return
		}
		defer store.Close()
		_ = store.CommitLineage(context.Background(), g6Lineage("1", "Inbox/g6/newer.md"))
	}()
	close(done)
	res, errb, code := g6Reconcile(t, configPath)
	if code != 0 {
		t.Fatalf("fenced reconcile: %s", errb.String())
	}
	wg.Wait()
	if res["concurrent_change"] == true {
		// The refused arm: one due reconciliation generation remains.
		if res["snapshot_stored"] != false || res["pending_reconcile"] != true {
			t.Fatalf("AC-604: the refusal must leave one due generation: %v", res)
		}
	}
	// Either arm: the next clean reconciliation stores a snapshot that
	// carries the newer fact — newer knowledge never loses.
	final, errb, code := g6Reconcile(t, configPath)
	if code != 0 {
		t.Fatalf("final reconcile: %s", errb.String())
	}
	if final["snapshot_stored"] != true {
		t.Fatalf("AC-604: the retry must store the snapshot: %v", final)
	}
	_, store, exit := openOperatorStore("g6", configPath, &bytes.Buffer{})
	if exit != 0 {
		t.Fatal("store open failed")
	}
	defer store.Close()
	var count int
	if err := store.QueryRow(`SELECT COUNT(*) FROM path_facts WHERE path = 'Inbox/g6/newer.md'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("AC-604: the concurrent fact must survive every arm: %d %v", count, err)
	}
}

// g6Reconcile runs one full reconciliation through the CLI.
func g6Reconcile(t *testing.T, configPath string) (map[string]any, *bytes.Buffer, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run([]string{"reconcile", "--route", "wiki", "--config", configPath, "--reason", "manual"}, &out, &errb)
	var decoded map[string]any
	if code == 0 {
		decoded = decodeEnvelope(t, &out)
	}
	return decoded, &errb, code
}

// g6Lineage builds one ordinary dispatch lineage recording a fact for
// path (the gate's concurrent writer).
func g6Lineage(suffix, path string) ports.Lineage {
	now := "2026-08-26T0" + suffix + ":00:00Z"
	digest := "sha256:" + strings.Repeat("g", 64)
	return ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: "obs-g6-" + suffix, SchemaVersion: "agent-dispatch.source-observation/v1",
			SourceType: "watchman", SourceID: "watchman-main", TriggerName: "agent-dispatch.wiki.e18t1", ResourceID: "vault-main",
			ObservedAt: now, ReceivedAt: now, RawPayloadDigest: digest, IngestStatus: "accepted",
			Changes: []ports.ObservationChange{
				{Ordinal: 1, Path: path, Operation: "create", ExistsAfter: true, FileType: "regular", AfterDigest: digest, DigestStatus: "known"},
			},
		},
		Batch: ports.BatchInput{
			BatchID: "batch-g6-" + suffix, RouteID: "wiki", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: now, ContentFingerprint: digest,
			ObservationIDs: []string{"obs-g6-" + suffix},
		},
		Decision: ports.DecisionInput{
			DecisionID: "decision-g6-" + suffix, BatchID: "batch-g6-" + suffix, RouteID: "wiki",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1", Disposition: "dispatch",
			Classification: "normal", CreatedAt: now, Actor: "planner",
		},
		Intent: ports.IntentInput{
			DispatchID: "dispatch-g6-" + suffix, DecisionID: "decision-g6-" + suffix, RouteID: "wiki",
			RouteRevision: "route-rev-1", TargetID: "hermes-main", TargetType: "hermes_kanban",
			ResourceID: "vault-main", Generation: 1, IdempotencyKey: "agent-dispatch:v1:" + digest,
			ContentFingerprint: digest, ManifestDigest: digest,
			RequestVersion: "agent-dispatch.hermes-task/v1", RequestJSON: "{}", CreatedAt: now,
		},
	}
}

// TestG6AC605BoundedHashingEvidence pins AC-605 through the CLI: a
// stable file beyond the bound is explicit quarantine evidence with an
// unknown digest, and a file growing during reconciliation produces
// only a stable digest, quarantine evidence, or instability evidence —
// never an unbounded read. The exact max+1 bound is pinned by
// TestE10T1BoundedReadNeverExceedsMaxPlusOne.
func TestG6AC605BoundedHashingEvidence(t *testing.T) {
	configPath, _, vault := watchmanFixture(t)
	g6RegisterEnabled(t, configPath)
	// Rewrite the fixture config with a 16-byte hash bound.
	e10t1SetHashLimit(t, configPath, 16)
	big := make([]byte, 64)
	if err := os.WriteFile(filepath.Join(vault, "Inbox", "big.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	// The growing file starts inside the bound so the growth — not the
	// stat precheck — is what pushes the read past it.
	growing := filepath.Join(vault, "Inbox", "growing.md")
	if err := os.WriteFile(growing, make([]byte, 8), 0o644); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	appended := make(chan struct{})
	go func() {
		defer close(appended)
		f, err := os.OpenFile(growing, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		chunk := make([]byte, 512)
		for i := 0; i < 300; i++ {
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
	res, errb, code := g6Reconcile(t, configPath)
	close(stop)
	<-appended
	if code != 0 {
		t.Fatalf("AC-605 reconcile: %s", errb.String())
	}
	if !listedAs(res, "quarantined_over_bound", "Inbox/big.md") {
		t.Fatalf("AC-605: the stable over-bound file must be quarantine evidence: %v", res)
	}
	_, store, exit := openOperatorStore("g6", configPath, &bytes.Buffer{})
	if exit != 0 {
		t.Fatal("store open failed")
	}
	defer store.Close()
	var digest int
	if err := store.QueryRow(`SELECT COUNT(*) FROM path_facts WHERE path = 'Inbox/big.md' AND digest IS NULL`).Scan(&digest); err != nil || digest != 1 {
		t.Fatalf("AC-605: the over-bound digest must stay unknown: %d %v", digest, err)
	}
	// The growing file's postcondition is exact (AC-605): either its
	// digest was accepted from a stable bounded read — and then it
	// appears in no evidence list — or its digest stayed unknown — and
	// then it must appear in exactly one evidence list.
	var growingDigest int
	if err := store.QueryRow(`SELECT COUNT(*) FROM path_facts WHERE path = 'Inbox/growing.md' AND digest IS NOT NULL`).Scan(&growingDigest); err != nil {
		t.Fatal(err)
	}
	switch growingDigest {
	case 1:
		if listedAs(res, "quarantined_over_bound", "Inbox/growing.md") || listedAs(res, "unstable_after_retry", "Inbox/growing.md") {
			t.Fatalf("AC-605: a digested file must carry no evidence flags: %v", res)
		}
	case 0:
		if !listedAs(res, "quarantined_over_bound", "Inbox/growing.md") && !listedAs(res, "unstable_after_retry", "Inbox/growing.md") {
			t.Fatalf("AC-605: an unknown digest must carry explicit evidence: %v", res)
		}
	default:
		t.Fatalf("AC-605: exactly one growing-file fact may exist: %d", growingDigest)
	}
}

// listedAs reports whether the path appears in the named evidence list.
func listedAs(res map[string]any, key, path string) bool {
	list, ok := res[key].([]any)
	if !ok {
		return false
	}
	for _, p := range list {
		if p == path {
			return true
		}
	}
	return false
}

// TestG6FreshDatabaseMigration pins the gate's fresh-database leg: a
// brand-new state directory migrates to the current baseline on first
// CLI use, the version surface reports the compact product identity, and
// the fresh and fully-migrated schemas are identical (pinned at the
// store level by TestFreshAndMigratedSchemasIdentical; the interrupted
// upgrade window by TestE10T1MigrationV8BackfillAndIntegrity).
func TestG6FreshDatabaseMigration(t *testing.T) {
	configPath, _, _ := watchmanFixture(t)
	// The fixture's state dir is fresh: the first operator surface
	// (registration, then reconciliation) migrates it on first open.
	g6RegisterEnabled(t, configPath)
	if _, errb, code := g6Reconcile(t, configPath); code != 0 {
		t.Fatalf("fresh-boot reconcile: %s", errb.String())
	}
	_, store, exit := openOperatorStore("g6", configPath, &bytes.Buffer{})
	if exit != 0 {
		t.Fatal("store open failed")
	}
	defer store.Close()
	schemaVersion, err := store.SchemaVersion()
	if err != nil || schemaVersion != sqlite.MaxSchemaVersion {
		t.Fatalf("G6: the fresh database must sit at the current baseline: %d %v", schemaVersion, err)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"version", "--json"}, &out, &errb); code != 0 {
		t.Fatalf("version: %s", errb.String())
	}
	if out.String() != "{\"name\":\"agent-dispatch\",\"version\":\"v0.1.0-dev\"}\n" {
		t.Fatalf("G6: the version surface must report the compact product identity: %s", out.String())
	}
}

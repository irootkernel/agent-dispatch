// Command crashbin is the E3-T5 crash-injection and multi-process test
// harness (TST-004, TST-005): it performs durable operations against a
// real SQLite file and dies hard at injected windows, or competes for
// one attempt lease across processes. It is test infrastructure compiled
// and executed by the G2 acceptance tests and never installed.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/rootkernel/jjukkumi/internal/adapters/sqlite"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: crashbin <command> [flags]")
		os.Exit(64)
	}
	switch os.Args[1] {
	case "seed":
		// seed --db PATH: create the schema, route, and one ready intent.
		err := seed(flag("db"))
		exitWith(err)
	case "commit":
		// commit --db PATH --stage mid-transaction|after-commit: die hard
		// inside the uncommitted ingestion transaction, or immediately
		// after it commits (AC-201, AC-202).
		stage := flag("stage")
		err := commitStage(flag("db"), stage)
		exitWith(err)
	case "arrive":
		// arrive --db PATH --tag N: one simultaneous one-shot arrival
		// competing for the route slot (TST-005): exactly one process
		// commits an intent; the losers observe the held slot.
		err := arrive(flag("db"), flag("tag"))
		exitWith(err)
	case "lease":
		// lease --db PATH --owner O --ttl-seconds N [--die]: acquire the
		// attempt lease; with --die, exit hard immediately after the
		// transaction commits (crash after lease, before the target call).
		die := flag("die") == "true"
		err := acquire(flag("db"), flag("owner"), flag("ttl-seconds"), die)
		exitWith(err)
	case "acquire-race":
		// acquire-race --db PATH --owner O: one simultaneous one-shot
		// competitor (AC-204); exit 0 only for the single lease winner.
		err := acquire(flag("db"), flag("owner"), "60", false)
		exitWith(err)
	case "migrate-partial":
		// migrate-partial --db PATH --steps N: apply N migrations then die
		// hard (AC-207 interruption window).
		err := migratePartial(flag("db"), flag("steps"))
		exitWith(err)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(64)
	}
}

func flag(name string) string {
	for i := 2; i < len(os.Args)-1; i++ {
		if os.Args[i] == "--"+name {
			return os.Args[i+1]
		}
	}
	return ""
}

func exitWith(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "crashbin: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func open(db string) (*sqlite.Store, error) {
	return sqlite.Open(db)
}

func baseLineage(now string) ports.Lineage {
	return ports.Lineage{
		Observation: ports.ObservationInput{
			ObservationID: "obs-1", SchemaVersion: "jjukkumi.source-observation/v1",
			SourceType: "watchman", SourceID: "watchman-main", TriggerName: "trig",
			ResourceID: "vault-main", ObservedAt: now, ReceivedAt: now,
			RawPayloadDigest: "sha256:" + rep('a'), IngestStatus: "accepted",
		},
		Batch: ports.BatchInput{
			BatchID: "batch-1", RouteID: "wiki", RouteRevision: "route-rev-1",
			ResourceID: "vault-main", CreatedAt: now,
			ContentFingerprint: "sha256:" + rep('c'), ObservationIDs: []string{"obs-1"},
		},
		Decision: ports.DecisionInput{
			DecisionID: "decision-1", BatchID: "batch-1", RouteID: "wiki",
			RouteRevision: "route-rev-1", PolicyRevision: "policy-rev-1",
			Disposition: "dispatch", Classification: "normal", CreatedAt: now, Actor: "planner",
		},
		Intent: ports.IntentInput{
			DispatchID: "dispatch-1", DecisionID: "decision-1", RouteID: "wiki",
			RouteRevision: "route-rev-1", TargetID: "hermes-kanban-main", TargetType: "hermes_kanban",
			ResourceID: "vault-main", Generation: 1, IdempotencyKey: "jjukkumi:v1:sha256:" + rep('1'),
			ContentFingerprint: "sha256:" + rep('c'), ManifestDigest: "sha256:" + rep('d'),
			RequestVersion: "jjukkumi.hermes-task/v1", RequestJSON: "{}", CreatedAt: now,
		},
	}
}

func rep(ch byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}

func seed(db string) error {
	s, err := open(db)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Migrate(os.TempDir()); err != nil {
		return err
	}
	if err := s.RegisterResource(nil, "vault-main", "res-rev-1", "/srv/vault", "/srv/vault", "markdown", "disabled"); err != nil {
		return err
	}
	if err := s.RegisterRoute(nil, "wiki", "route-rev-1", "policy-rev-1", "vault-main", "hermes-kanban-main", "{}", "2026-08-20T00:00:00Z"); err != nil {
		return err
	}
	if err := s.InitializeRouteState(nil, "wiki"); err != nil {
		return err
	}
	if err := s.SetRouteActivation(context.Background(), "wiki", "enabled", "route-rev-1", "2026-08-20T00:00:00Z"); err != nil {
		return err
	}
	return nil
}

// commitStage realizes the two ingestion crash windows: mid-transaction
// opens the ingestion transaction, writes the observation, batch, and
// decision, then dies hard with the transaction uncommitted; after-commit
// dies immediately after the full lineage commits.
func commitStage(db, stage string) error {
	s, err := open(db)
	if err != nil {
		return err
	}
	defer s.Close()
	lin := baseLineage("2026-08-20T01:00:00Z")
	switch stage {
	case "mid-transaction":
		tx, err := s.BeginTx(context.Background(), nil)
		if err != nil {
			return err
		}
		// Dying with the transaction open: WAL recovery on the next open
		// must discard every uncommitted row (AC-201).
		if err := s.SaveObservation(tx, portsObservationFor(lin)); err != nil {
			return err
		}
		if err := s.SaveBatch(tx, lin.Batch.BatchID, lin.Batch.RouteID, lin.Batch.RouteRevision,
			lin.Batch.ResourceID, lin.Batch.CreatedAt, lin.Batch.ContentFingerprint, lin.Batch.ObservationIDs); err != nil {
			return err
		}
		if err := s.SaveDecision(tx, decisionFor(lin)); err != nil {
			return err
		}
		os.Exit(0)
	case "after-commit":
		if err := s.CommitLineage(context.Background(), lin); err != nil {
			return err
		}
		os.Exit(0)
	default:
		return fmt.Errorf("unknown stage %q", stage)
	}
	return nil
}

// arrive commits one uniquely tagged lineage competing for the route
// slot (TST-005): the single slot winner exits 0.
func arrive(db, tag string) error {
	s, err := open(db)
	if err != nil {
		return err
	}
	defer s.Close()
	lin := baseLineage("2026-08-20T01:00:00Z")
	lin.Observation.ObservationID = "obs-" + tag
	lin.Batch.BatchID = "batch-" + tag
	lin.Batch.ObservationIDs = []string{"obs-" + tag}
	lin.Decision.DecisionID = "decision-" + tag
	lin.Decision.BatchID = "batch-" + tag
	lin.Intent.DispatchID = "dispatch-" + tag
	lin.Intent.DecisionID = "decision-" + tag
	lin.Intent.IdempotencyKey = "jjukkumi:v1:sha256:" + rep('0')[:63] + tag[:1]
	return s.CommitLineage(context.Background(), lin)
}

// portsObservationFor and decisionFor mirror the adapter's private
// converters for the harness's direct transaction use.
func portsObservationFor(lin ports.Lineage) sqlite.ObservationRecord {
	rec := sqlite.ObservationRecord{
		ObservationID:    lin.Observation.ObservationID,
		SchemaVersion:    lin.Observation.SchemaVersion,
		SourceType:       lin.Observation.SourceType,
		SourceID:         lin.Observation.SourceID,
		SourceEventKey:   lin.Observation.SourceEventKey,
		TriggerName:      lin.Observation.TriggerName,
		ResourceID:       lin.Observation.ResourceID,
		ObservedAt:       lin.Observation.ObservedAt,
		ReceivedAt:       lin.Observation.ReceivedAt,
		RawPayloadDigest: lin.Observation.RawPayloadDigest,
		IngestStatus:     lin.Observation.IngestStatus,
		FlagsJSON:        lin.Observation.FlagsJSON,
	}
	for _, c := range lin.Observation.Changes {
		rec.Changes = append(rec.Changes, sqlite.ChangeRecord{
			Ordinal: c.Ordinal, Path: c.Path, Operation: c.Operation, ExistsAfter: c.ExistsAfter,
			FileType: c.FileType, BeforeDigest: c.BeforeDigest, AfterDigest: c.AfterDigest, DigestStatus: c.DigestStatus,
		})
	}
	return rec
}

func decisionFor(lin ports.Lineage) sqlite.DecisionRecord {
	return sqlite.DecisionRecord{
		DecisionID: lin.Decision.DecisionID, BatchID: lin.Decision.BatchID, RouteID: lin.Decision.RouteID,
		RouteRevision: lin.Decision.RouteRevision, PolicyRevision: lin.Decision.PolicyRevision,
		Disposition: lin.Decision.Disposition, Classification: lin.Decision.Classification,
		ReasonCodesJSON: lin.Decision.ReasonCodesJSON, CreatedAt: lin.Decision.CreatedAt, Actor: lin.Decision.Actor,
		GenerationLineageJSON: lin.Decision.GenerationLineageJSON,
	}
}

func acquire(db, owner, ttl string, die bool) error {
	s, err := open(db)
	if err != nil {
		return err
	}
	defer s.Close()
	seconds, err := strconv.Atoi(ttl)
	if err != nil || seconds <= 0 {
		return fmt.Errorf("invalid ttl %q", ttl)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := s.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: "dispatch-1", AttemptID: fmt.Sprintf("attempt-%s", owner), Owner: owner,
		Now: now.Format(time.RFC3339), LeaseExpiresAt: now.Add(time.Duration(seconds) * time.Second).Format(time.RFC3339),
	}); err != nil {
		return err
	}
	if die {
		// Hard exit with the lease transaction committed and no attempt
		// completion: the crash-after-lease window.
		os.Exit(0)
	}
	return nil
}

func migratePartial(db, steps string) error {
	s, err := open(db)
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(steps)
	if err != nil || n < 0 {
		return fmt.Errorf("invalid steps %q", steps)
	}
	list := sqlite.Migrations
	if n > len(list) {
		n = len(list)
	}
	for i := 0; i < n; i++ {
		if err := s.ApplyMigrationForHarness(list[i]); err != nil {
			return err
		}
	}
	// Die hard mid-migration-sequence: the ledger holds exactly the
	// applied prefix.
	os.Exit(0)
	return nil
}

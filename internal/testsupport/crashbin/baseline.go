package main

// The E14-T2 baseline crash window (TST-004, AC-1004): run one real
// baseline-only reconciliation through the production service against a
// real SQLite file, and — for the die-before-write window — die hard
// after the enumeration, immediately before the observation-fenced
// transaction. The durable state after the crash is either the previous
// baseline or nothing (never a partial snapshot), and a rerun converges.

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/localfs"
	"github.com/irootkernel/agent-dispatch/internal/adapters/sqlite"
	"github.com/irootkernel/agent-dispatch/internal/app/reconcile"
	"github.com/irootkernel/agent-dispatch/internal/domain/policy"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// dieBeforeWriteBaseline wraps the store so the service's durable commit
// becomes a hard exit: every read, guard, and enumeration the service
// performed happened, and nothing was written.
type dieBeforeWriteBaseline struct {
	*sqlite.Store
}

func (d *dieBeforeWriteBaseline) ReplacePathFactsWithBaseline(ctx context.Context, expectedRevision int64, facts []ports.PathFact, baseline ports.RouteBaselineInput, register *ports.ResourceRegistrationInput, observedAt string) error {
	fmt.Fprintf(os.Stderr, "crashbin: dying before the baseline transaction (resource %s, %d facts)\n", baseline.ResourceID, len(facts))
	os.Exit(86)
	return nil
}

// baselineRun runs one baseline-only reconciliation through the real
// service. window "full" completes it; "die-before-write" dies hard
// immediately before the fenced transaction.
func baselineRun(db, vault, route, resource, window string) error {
	if route == "" {
		route = "wiki"
	}
	if resource == "" {
		resource = "vault-main"
	}
	s, err := open(db)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Migrate(os.TempDir()); err != nil {
		return err
	}
	resolver, err := localfs.NewResolver(vault)
	if err != nil {
		return err
	}
	engine, err := policy.NewEngine([]string{"**/*.md"}, nil, nil, nil, policy.CaseSensitive)
	if err != nil {
		return err
	}
	var store ports.BaselineStore = s
	if window == "die-before-write" {
		store = &dieBeforeWriteBaseline{s}
	}
	service := &reconcile.BaselineService{
		Store: store, Resolver: resolver, Engine: engine,
		FileScope: "markdown", MaxHash: 8 << 20,
		ResourceID: resource, RouteID: route,
		RouteRevision: "route-rev-e14t2", PolicyRevision: "policy-rev-e14t2",
		ResourceRegistration: &ports.ResourceRegistrationInput{
			ResourceID: resource, Revision: "route-rev-e14t2",
			Root: vault, CanonicalRoot: vault, FileScope: "markdown", GitMode: "disabled",
		},
		Now: time.Now,
	}
	result, err := service.Run(context.Background(), route, "initial")
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "baseline: stored=%v revision=%d facts=%d\n", result.SnapshotStored, result.ObservationRevision, result.FactCount)
	return nil
}

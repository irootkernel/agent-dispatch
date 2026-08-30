package ports

import (
	"context"

	"github.com/irootkernel/agent-dispatch/internal/domain/state"
)

// Baseline-only reconciliation surface (E14-T2, ADR-0020, DUR-017,
// CLI-017): establish or refresh the initial path facts of a route that
// stays disabled. The operation is one observation-fenced transaction
// writing exactly the snapshot, the route baseline record, and — on a
// clean host — the trusted resource row.

// RouteBaselineInput is the durable evidence record of one baseline-only
// reconciliation: the observation revision the snapshot committed at,
// the bounded fact count and canonical snapshot digest, the route and
// policy revisions the evidence names, and the reason and timestamp of
// establishment. One row per route; a rerun replaces it atomically.
type RouteBaselineInput struct {
	RouteID             string
	ResourceID          string
	ObservationRevision int64
	FactCount           int
	SnapshotSHA256      string
	RouteRevision       string
	PolicyRevision      string
	Reason              string
	EstablishedAt       string
}

// ResourceRegistrationInput is the trusted resource materialization a
// baseline performs inside its fenced transaction when the resource row
// does not exist yet (the clean-host posture). An existing row is never
// rewritten by it.
type ResourceRegistrationInput struct {
	ResourceID    string
	Revision      string
	Root          string
	CanonicalRoot string
	FileScope     string
	GitMode       string
}

// BaselineStore is the durable surface baseline-only reconciliation
// needs beside the shared enumeration walker. Implementations commit
// the snapshot and the baseline record in one transaction fenced on the
// pre-enumeration observation revision and write no decision, intent,
// receipt, route runtime state, or notification row.
type BaselineStore interface {
	ObservationRevision(ctx context.Context, resourceID string) (int64, error)
	LoadPathFacts(ctx context.Context, resourceID string) (map[string]PathFact, error)
	// LoadRouteState reads the route snapshot for the disabled-state
	// guards; callers establish existence first (RouteRuntimeStatePresent)
	// because a missing row is the clean-host posture, not an error.
	LoadRouteState(ctx context.Context, routeID string) (state.RouteSnapshot, error)
	RouteRuntimeStatePresent(ctx context.Context, routeID string) (bool, error)
	// ReplacePathFactsWithBaseline commits the snapshot, the baseline
	// record, and the optional clean-host resource registration in one
	// observation-fenced transaction.
	ReplacePathFactsWithBaseline(ctx context.Context, expectedRevision int64, facts []PathFact, baseline RouteBaselineInput, register *ResourceRegistrationInput, observedAt string) error
}

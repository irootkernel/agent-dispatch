package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// RouteBaselineRecord is the persisted shape of one route baseline
// (E14-T2, DUR-017, ADR-0020): the snapshot and this record commit
// atomically inside the observation-fenced transaction, and the row
// replaces itself on every rerun so a crashed attempt converges without
// duplicate work.
type RouteBaselineRecord struct {
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

// ReplacePathFactsWithBaseline stores one baseline-only snapshot and its
// route baseline record through the observation fence (E14-T2,
// DUR-014/DUR-015/DUR-017): the disabled-activation re-check, the
// optional clean-host resource registration, the revision advancement,
// the fact replacement, and the baseline row commit in one transaction,
// and the transaction runs only while the resource's observation
// revision still equals the revision the baseline observed before
// enumerating. A revision moved by a newer durable path-fact mutation
// refuses with ports.ErrObservationConflict; a route whose runtime
// activation flipped away from disabled inside the enumeration window
// refuses with ports.ErrStateNotEligible — both leave the stored facts
// and the previous baseline untouched. The method writes nothing else:
// no policy decision, dispatch intent, receipt, route runtime state, or
// notification row is touched.
func (s *Store) ReplacePathFactsWithBaseline(ctx context.Context, expectedRevision int64, facts []ports.PathFact, baseline ports.RouteBaselineInput, register *ports.ResourceRegistrationInput, observedAt string) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The in-transaction re-check of the runtime half of the two-key
	// gate: the pre-enumeration guard can go stale across the (long)
	// walk, so the commit itself refuses an activation flipped to
	// enabled or paused inside the window (CLI-017, ADR-0020).
	var activation string
	switch err := tx.QueryRow(`SELECT activation_state FROM route_runtime_state WHERE route_id = ?`, baseline.RouteID).Scan(&activation); {
	case errors.Is(err, sql.ErrNoRows):
		// The clean-host posture: no runtime row, nothing to re-check.
	case err != nil:
		return err
	default:
		if activation != "disabled" {
			return fmt.Errorf("%w: route %s runtime activation state is %q inside the baseline transaction; the newer state stands",
				ports.ErrStateNotEligible, baseline.RouteID, activation)
		}
	}
	if register != nil {
		// INSERT ... ON CONFLICT DO NOTHING keeps the clean-host path
		// self-sufficient without ever rewriting a trusted registration.
		if _, err := tx.Exec(`INSERT INTO resources (resource_id, revision, root, canonical_root, file_scope, git_mode) VALUES (?,?,?,?,?,?)
			ON CONFLICT(resource_id) DO NOTHING`,
			register.ResourceID, register.Revision, register.Root, register.CanonicalRoot, register.FileScope, register.GitMode); err != nil {
			return err
		}
	}
	res, err := tx.Exec(`UPDATE resources SET observation_revision = observation_revision + 1 WHERE resource_id = ? AND observation_revision = ?`,
		baseline.ResourceID, expectedRevision)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var current int64
		switch err := tx.QueryRow(`SELECT observation_revision FROM resources WHERE resource_id = ?`, baseline.ResourceID).Scan(&current); {
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("%w: resource %s", ports.ErrResourceNotFound, baseline.ResourceID)
		case err != nil:
			return err
		}
		return fmt.Errorf("%w: resource %s observation revision is %d, expected %d",
			ports.ErrObservationConflict, baseline.ResourceID, current, expectedRevision)
	}
	if _, err := tx.Exec(`DELETE FROM path_facts WHERE resource_id = ?`, baseline.ResourceID); err != nil {
		return err
	}
	for _, f := range facts {
		if _, err := tx.Exec(`INSERT INTO path_facts (resource_id, path, digest, "exists", observed_at) VALUES (?,?,?,?,?)`,
			baseline.ResourceID, f.Path, nullString(f.Digest), f.Exists, observedAt); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO route_baselines (route_id, resource_id, observation_revision, fact_count, snapshot_sha256, route_revision, policy_revision, reason, established_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(route_id) DO UPDATE SET
			resource_id=excluded.resource_id,
			observation_revision=excluded.observation_revision,
			fact_count=excluded.fact_count,
			snapshot_sha256=excluded.snapshot_sha256,
			route_revision=excluded.route_revision,
			policy_revision=excluded.policy_revision,
			reason=excluded.reason,
			established_at=excluded.established_at`,
		baseline.RouteID, baseline.ResourceID, baseline.ObservationRevision, baseline.FactCount, baseline.SnapshotSHA256,
		baseline.RouteRevision, baseline.PolicyRevision, baseline.Reason, baseline.EstablishedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// LoadRouteBaseline returns the stored baseline record for one route,
// or nil when none exists.
func (s *Store) LoadRouteBaseline(ctx context.Context, routeID string) (*RouteBaselineRecord, error) {
	var rec RouteBaselineRecord
	err := s.QueryRowContext(ctx, `SELECT route_id, resource_id, observation_revision, fact_count, snapshot_sha256, route_revision, policy_revision, reason, established_at
		FROM route_baselines WHERE route_id = ?`, routeID).Scan(
		&rec.RouteID, &rec.ResourceID, &rec.ObservationRevision, &rec.FactCount, &rec.SnapshotSHA256,
		&rec.RouteRevision, &rec.PolicyRevision, &rec.Reason, &rec.EstablishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// RouteRuntimeStatePresent reports whether a route runtime row exists.
// A clean host has none: baseline-only reconciliation treats that
// posture as disabled rather than refusing it (ADR-0020).
func (s *Store) RouteRuntimeStatePresent(ctx context.Context, routeID string) (bool, error) {
	var one int
	err := s.QueryRowContext(ctx, `SELECT 1 FROM route_runtime_state WHERE route_id = ?`, routeID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

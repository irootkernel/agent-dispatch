// Baseline-only reconciliation (E14-T2, ADR-0020, CLI-017, DUR-017):
// establish or refresh the initial path facts of a route that remains
// disabled — enumerate the bounded snapshot under the same containment
// defense as full reconciliation and commit it together with the route
// baseline record in one observation-fenced transaction. The operation
// creates no policy decision, dispatch intent, Hermes task, production
// acknowledgement, or notification, has no submit path, and refuses any
// non-disabled runtime state.
package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/adapters/localfs"
	"github.com/irootkernel/agent-dispatch/internal/domain/ids"
	"github.com/irootkernel/agent-dispatch/internal/domain/policy"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// BaselineResult reports one baseline-only reconciliation outcome.
type BaselineResult struct {
	RouteID string `json:"route_id"`
	Reason  string `json:"reason"`
	// Baseline marks the operation kind in the envelope (CLI-017).
	Baseline bool `json:"baseline_only"`
	// Enumerated is the in-scope file count the walk observed; Compared
	// is the previously stored fact count (zero on a clean host).
	Enumerated int      `json:"enumerated"`
	Compared   int      `json:"compared"`
	Skipped    []string `json:"skipped,omitempty"`
	// QuarantinedOverBound and UnstableAfterRetry carry the same explicit
	// hashing evidence full reconciliation reports (OPS-012).
	QuarantinedOverBound []string `json:"quarantined_over_bound,omitempty"`
	UnstableAfterRetry   []string `json:"unstable_after_retry,omitempty"`
	// ObservationRevision is the revision the committed snapshot advanced
	// the resource to; the previous baseline is replaced atomically.
	ObservationRevision int64  `json:"observation_revision"`
	FactCount           int    `json:"fact_count"`
	SnapshotSHA256      string `json:"snapshot_sha256"`
	EstablishedAt       string `json:"established_at"`
	SnapshotStored      bool   `json:"snapshot_stored"`
	// ConcurrentChange reports the observation-fence refusal (DUR-015):
	// nothing was stored, the newer facts stand, and a rerun converges.
	ConcurrentChange bool `json:"concurrent_change,omitempty"`
}

// BaselineService performs baseline-only reconciliation for one route
// that is disabled in both halves of the production gate.
type BaselineService struct {
	Store      ports.BaselineStore
	Resolver   *localfs.Resolver
	Engine     *policy.Engine
	FileScope  string
	MaxHash    int64
	ResourceID string
	RouteID    string
	// RouteRevision and PolicyRevision name the exact configuration the
	// baseline evidence records (never placeholders).
	RouteRevision  string
	PolicyRevision string
	// ConfigEnabled is the YAML half of the two-key gate: a route whose
	// configuration key is on fails closed before anything is read.
	ConfigEnabled bool
	// ResourceRegistration carries the trusted clean-host materialization
	// the fenced transaction performs when no resource row exists yet.
	ResourceRegistration *ports.ResourceRegistrationInput
	Now                  func() time.Time
}

// enumerate delegates to the shared bounded scope walker (see
// scopeWalker): full reconciliation and baseline-only reconciliation
// enumerate the resource identically.
func (s *BaselineService) enumerate() (facts []ports.PathFact, skippedPrefixes, overBound, unstable []string, err error) {
	return scopeWalker{Resolver: s.Resolver, Engine: s.Engine, FileScope: s.FileScope, MaxHash: s.MaxHash}.enumerate()
}

// Run enumerates the bounded snapshot and commits it with the route
// baseline record under the observation fence. Guards refuse before any
// durable write: a non-disabled runtime activation state and an
// uncertain or quarantined route state all fail closed with
// ports.ErrStateNotEligible (CLI-017). The operation writes nothing but
// the snapshot, the baseline record, and — on a clean host — the
// resource row: no decision, intent, task, acknowledgement, or
// notification.
func (s *BaselineService) Run(ctx context.Context, routeID, reason string) (BaselineResult, error) {
	if !Reasons[reason] {
		return BaselineResult{}, fmt.Errorf("unknown reconcile reason %q", reason)
	}
	// The YAML half of the two-key gate (CLI-017, ADR-0020): the
	// operation is documented for disabled routes only, and an enabled
	// configuration key fails closed before anything is read or written.
	if s.ConfigEnabled {
		return BaselineResult{}, fmt.Errorf("%w: route %s is enabled in configuration; baseline-only reconciliation is a disabled-route operation",
			ports.ErrStateNotEligible, routeID)
	}
	// The runtime half of the two-key gate (CLI-017): a clean host has
	// no route row and is the operation's home posture; any materialized
	// row must be disabled and free of an uncertain or quarantined hold.
	if present, err := s.Store.RouteRuntimeStatePresent(ctx, routeID); err != nil {
		return BaselineResult{}, ports.WrapStore(err)
	} else if present {
		snap, err := s.Store.LoadRouteState(ctx, routeID)
		if err != nil {
			return BaselineResult{}, ports.WrapStore(err)
		}
		if snap.ActivationState != "disabled" {
			return BaselineResult{}, fmt.Errorf("%w: route %s runtime activation state is %q; baseline-only reconciliation is a disabled-route operation",
				ports.ErrStateNotEligible, routeID, snap.ActivationState)
		}
		if snap.State == state.RouteUncertain || snap.State == state.RouteQuarantined {
			return BaselineResult{}, fmt.Errorf("%w: route %s state is %s; resolve the hold before establishing a baseline",
				ports.ErrStateNotEligible, routeID, snap.State)
		}
	}
	// The observation revision is read before the enumeration walk
	// (DUR-014): any durable path-fact mutation landing inside the
	// window advances it, and the fenced commit below then refuses as a
	// typed concurrent change instead of overwriting the newer facts. A
	// missing resource row (clean host) starts from revision zero.
	observedRevision := int64(0)
	if rev, err := s.Store.ObservationRevision(ctx, s.ResourceID); err == nil {
		observedRevision = rev
	} else if !errors.Is(err, ports.ErrResourceNotFound) {
		return BaselineResult{}, ports.WrapStore(err)
	}
	current, skipped, overBound, unstable, err := s.enumerate()
	if err != nil {
		return BaselineResult{}, err
	}
	sort.Strings(skipped)
	sort.Strings(overBound)
	sort.Strings(unstable)
	stored, err := s.Store.LoadPathFacts(ctx, s.ResourceID)
	if err != nil {
		return BaselineResult{}, ports.WrapStore(err)
	}
	// The snapshot keeps the stored facts of unreadable subtrees exactly
	// as full reconciliation does: their last observed truth stands.
	snapshot := current
	if len(skipped) > 0 {
		currentMap := map[string]ports.PathFact{}
		for _, f := range current {
			currentMap[f.Path] = f
		}
		for path, fact := range stored {
			if _, ok := currentMap[path]; !ok && underSkippedPrefix(skipped, path) {
				snapshot = append(snapshot, fact)
			}
		}
	}
	now := ids.CanonicalTimestamp(s.now())
	baseline := ports.RouteBaselineInput{
		RouteID: routeID, ResourceID: s.ResourceID,
		ObservationRevision: observedRevision + 1,
		FactCount:           len(snapshot),
		SnapshotSHA256:      snapshotDigest(snapshot),
		RouteRevision:       s.routeRevision(),
		PolicyRevision:      s.policyRevision(),
		Reason:              reason, EstablishedAt: now,
	}
	if err := s.Store.ReplacePathFactsWithBaseline(ctx, observedRevision, snapshot, baseline, s.ResourceRegistration, now); err != nil {
		if errors.Is(err, ports.ErrObservationConflict) {
			// The fence refused the replacement: the newer facts stand,
			// the previous baseline is intact, and the outcome is typed —
			// one rerun converges (DUR-015, AC-1004).
			return BaselineResult{
				RouteID: routeID, Reason: reason, Baseline: true,
				Enumerated: len(current), Compared: len(stored), Skipped: skipped,
				QuarantinedOverBound: overBound, UnstableAfterRetry: unstable,
				SnapshotStored: false, ConcurrentChange: true,
			}, nil
		}
		if errors.Is(err, ports.ErrStateNotEligible) {
			// The in-transaction activation re-check refused the commit:
			// the newer route state stands and the refusal is a typed
			// state conflict, never a storage failure.
			return BaselineResult{}, err
		}
		return BaselineResult{}, ports.WrapStore(err)
	}
	return BaselineResult{
		RouteID: routeID, Reason: reason, Baseline: true,
		Enumerated: len(current), Compared: len(stored), Skipped: skipped,
		QuarantinedOverBound: overBound, UnstableAfterRetry: unstable,
		ObservationRevision: baseline.ObservationRevision, FactCount: baseline.FactCount,
		SnapshotSHA256: baseline.SnapshotSHA256, EstablishedAt: now,
		SnapshotStored: true,
	}, nil
}

// snapshotDigest derives the canonical baseline digest: the sorted
// path-and-digest projection of the complete bounded snapshot encoded as
// a JSON array, whose string escaping makes the framing unambiguous —
// two enumerations of the same content produce the same evidence, and
// no filename (which may legally contain tabs or newlines on the
// supported platform) can forge another snapshot's projection.
func snapshotDigest(facts []ports.PathFact) string {
	ordered := make([]ports.PathFact, len(facts))
	copy(ordered, facts)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	projection := make([][2]string, len(ordered))
	for i, f := range ordered {
		projection[i] = [2]string{f.Path, f.Digest}
	}
	raw, err := json.Marshal(projection)
	if err != nil {
		// A path-and-digest pair of strings always marshals; the failure
		// is unreachable in practice and must never silently produce a
		// digest.
		panic(fmt.Sprintf("baseline snapshot projection: %v", err))
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (s *BaselineService) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

func (s *BaselineService) routeRevision() string {
	if s.RouteRevision != "" {
		return s.RouteRevision
	}
	return "unknown"
}

func (s *BaselineService) policyRevision() string {
	if s.PolicyRevision != "" {
		return s.PolicyRevision
	}
	return "unknown"
}

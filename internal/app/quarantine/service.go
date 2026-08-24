// Package quarantine implements the operator hold surface (E5-T4,
// PTH-008, CLI-006): protected and bulk holds are durable and visible,
// release creates a replacement reconciliation decision with full
// actor, reason, previous-decision, and new-decision lineage, and
// discard resolves without task creation while preserving the audit
// trail. The hold itself never dispatches work (POL-005).
package quarantine

import (
	"context"
	"strings"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Store is the durable surface the hold service needs.
type Store interface {
	ports.QuarantineStore
	// ReleaseQuarantineWithRevision records the caller-computed current
	// revision and policy digest into the replacement decision (epic
	// audit round-1 F001; E9-T3, L-18).
	ReleaseQuarantineWithRevision(ctx context.Context, quarantineID, actor, reason, routeRevision, policyRevision, now string) (ports.QuarantineRecord, error)
}

// Service applies the operator resolution semantics.
type Service struct {
	Store Store
	// Now renders the canonical resolution timestamp.
	Now func() string
	// RouteRevision is the caller-computed CURRENT route revision
	// recorded into the replacement decision (E8-T5, M-13, epic audit
	// round-1 F001: never a derivation over historical intents). Empty
	// keeps the quarantined decision's own revision.
	RouteRevision string
	// PolicyRevision is the caller-computed independent policy digest
	// recorded into the replacement decision (E9-T3, L-18): never a
	// route revision echo. Empty keeps the quarantined decision's own
	// policy revision.
	PolicyRevision string
}

// Release resolves one held item into a replacement reconciliation
// decision; the route's pending reconciliation generation absorbs it.
// Typed domain outcomes pass through; durable-store failures surface as
// the typed store error so the CLI boundary never relabels them.
func (s *Service) Release(ctx context.Context, quarantineID, actor, reason string) (ports.QuarantineRecord, error) {
	if strings.TrimSpace(reason) == "" {
		return ports.QuarantineRecord{}, ports.ErrReasonRequired
	}
	var rec ports.QuarantineRecord
	var err error
	if s.RouteRevision != "" {
		rec, err = s.Store.ReleaseQuarantineWithRevision(ctx, quarantineID, actor, reason, s.RouteRevision, s.PolicyRevision, s.Now())
	} else {
		rec, err = s.Store.ReleaseQuarantine(ctx, quarantineID, actor, reason, s.Now())
	}
	if ports.IsQuarantineDomainOutcome(err) {
		return rec, err
	}
	return rec, ports.WrapStore(err)
}

// Discard resolves one held item without task creation.
func (s *Service) Discard(ctx context.Context, quarantineID, actor, reason string) (ports.QuarantineRecord, error) {
	if strings.TrimSpace(reason) == "" {
		return ports.QuarantineRecord{}, ports.ErrReasonRequired
	}
	rec, err := s.Store.DiscardQuarantine(ctx, quarantineID, actor, reason, s.Now())
	if ports.IsQuarantineDomainOutcome(err) {
		return rec, err
	}
	return rec, ports.WrapStore(err)
}

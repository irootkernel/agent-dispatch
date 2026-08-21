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

	"github.com/rootkernel/jjukkumi/internal/ports"
)

// Store is the durable surface the hold service needs.
type Store interface {
	ports.QuarantineStore
}

// Service applies the operator resolution semantics.
type Service struct {
	Store Store
	// Now renders the canonical resolution timestamp.
	Now func() string
}

// Release resolves one held item into a replacement reconciliation
// decision; the route's pending reconciliation generation absorbs it.
// Typed domain outcomes pass through; durable-store failures surface as
// the typed store error so the CLI boundary never relabels them.
func (s *Service) Release(ctx context.Context, quarantineID, actor, reason string) (ports.QuarantineRecord, error) {
	if strings.TrimSpace(reason) == "" {
		return ports.QuarantineRecord{}, ports.ErrReasonRequired
	}
	rec, err := s.Store.ReleaseQuarantine(ctx, quarantineID, actor, reason, s.Now())
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

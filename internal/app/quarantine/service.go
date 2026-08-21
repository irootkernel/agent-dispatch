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
func (s *Service) Release(ctx context.Context, quarantineID, actor, reason string) (ports.QuarantineRecord, error) {
	if strings.TrimSpace(reason) == "" {
		return ports.QuarantineRecord{}, ports.ErrReasonRequired
	}
	return s.Store.ReleaseQuarantine(ctx, quarantineID, actor, reason, s.Now())
}

// Discard resolves one held item without task creation.
func (s *Service) Discard(ctx context.Context, quarantineID, actor, reason string) (ports.QuarantineRecord, error) {
	if strings.TrimSpace(reason) == "" {
		return ports.QuarantineRecord{}, ports.ErrReasonRequired
	}
	return s.Store.DiscardQuarantine(ctx, quarantineID, actor, reason, s.Now())
}

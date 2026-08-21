package ports

import (
	"context"
	"errors"
)

// Quarantine and full-reconciliation surface (E5-T4, PTH-008, POL-005,
// SRC-005, OPS-006): protected and bulk holds are durable and
// operator-visible, overflow and fresh-instance signals never dispatch
// partial ordinary work, and full reconciliation collapses into one
// pending generation.

// The closed policy vocabulary (POL-002/POL-006) as typed constants so
// no layer re-embeds it in ad-hoc strings.
const (
	DispositionDrop         = "drop"
	DispositionDispatch     = "dispatch"
	DispositionMerge        = "merge_pending"
	DispositionQuarantine   = "quarantine"
	DispositionReconcile    = "reconcile"
	ClassificationNormal    = "normal"
	ClassificationProtected = "protected"
	ClassificationBulk      = "bulk"
	ClassificationOverflow  = "overflow"
)

// ErrQuarantineNotFound reports no quarantine item for the ID.
var ErrQuarantineNotFound = errors.New("quarantine item not found")

// ErrQuarantineNotHeld reports a resolution attempt against an item that
// is no longer held.
var ErrQuarantineNotHeld = errors.New("quarantine item is not held")

// IsQuarantineDomainOutcome reports whether err is one of the typed
// quarantine domain outcomes — the single classification every layer
// shares, so a future domain refusal cannot be silently relabeled as
// storage by missing one layer's enumeration.
func IsQuarantineDomainOutcome(err error) bool {
	return errors.Is(err, ErrQuarantineNotFound) || errors.Is(err, ErrQuarantineNotHeld)
}

// QuarantineInput is one durable hold created with its policy decision.
type QuarantineInput struct {
	QuarantineID string
	BatchID      string
	DecisionID   string
	ReasonCodes  []string
	CreatedAt    string
}

// QuarantineRecord is the operator-visible hold projection.
type QuarantineRecord struct {
	QuarantineID          string   `json:"quarantine_id"`
	BatchID               string   `json:"batch_id,omitempty"`
	DecisionID            string   `json:"decision_id"`
	ReasonCodes           []string `json:"reason_codes"`
	State                 string   `json:"state"`
	CreatedAt             string   `json:"created_at"`
	ResolvedAt            string   `json:"resolved_at,omitempty"`
	ResolvedBy            string   `json:"resolved_by,omitempty"`
	ResolutionReason      string   `json:"resolution_reason,omitempty"`
	ReplacementDecisionID string   `json:"replacement_decision_id,omitempty"`
}

// QuarantineFilter narrows the list surface.
type QuarantineFilter struct {
	RouteID string
	State   string // held | released | discarded | superseded
	Limit   int
}

// QuarantineStore is the durable quarantine surface.
type QuarantineStore interface {
	// CommitQuarantineLineage persists one quarantine-classified arrival
	// — observation, batch, decision, and the hold — with no dispatch
	// intent (PTH-008: protected paths never enter a task manifest).
	CommitQuarantineLineage(ctx context.Context, lin Lineage, item QuarantineInput) error
	// CommitDropLineage persists a dropped arrival (no intent, no hold).
	CommitDropLineage(ctx context.Context, lin Lineage) error
	// CommitReconcileLineage persists one reconcile-classified arrival
	// and marks the route's single pending reconciliation generation
	// (SRC-005: never a partial ordinary dispatch).
	CommitReconcileLineage(ctx context.Context, lin Lineage, sourcePosition string) error
	// ListQuarantine returns holds matching the filter, newest first.
	ListQuarantine(ctx context.Context, filter QuarantineFilter) ([]QuarantineRecord, error)
	// LoadQuarantine returns one hold with its decision context.
	LoadQuarantine(ctx context.Context, quarantineID string) (QuarantineRecord, error)
	// ReleaseQuarantine resolves one held item by creating the
	// replacement reconciliation decision (operator actor, reason, and
	// supersedes lineage recorded, CLI-006).
	ReleaseQuarantine(ctx context.Context, quarantineID, actor, reason, now string) (QuarantineRecord, error)
	// DiscardQuarantine resolves one held item without task creation,
	// preserving the audit lineage.
	DiscardQuarantine(ctx context.Context, quarantineID, actor, reason, now string) (QuarantineRecord, error)
	// MarkPendingReconcile idempotently marks the route's pending
	// reconciliation generation (repeated reconciliations collapse).
	MarkPendingReconcile(ctx context.Context, routeID, sourcePosition, now string) error
	// ClearPendingReconcile resolves the pending generation when a full
	// reconciliation on an idle route proved no work remains. The clear
	// is conditional on the flag the reconciliation observed: a signal
	// another actor marked inside the window is never wiped (cleared
	// reports the miss).
	ClearPendingReconcile(ctx context.Context, routeID string, expected bool, now string) (cleared bool, err error)
	// ReplacePathFacts stores one full-scope path-fact snapshot.
	ReplacePathFacts(ctx context.Context, resourceID string, facts []PathFact, observedAt string) error
	// LoadPathFacts returns the stored full-scope snapshot.
	LoadPathFacts(ctx context.Context, resourceID string) (map[string]PathFact, error)
	// CommitReconcileDecision persists one batch-less reconciliation
	// decision (generation lineage, POL-006 disposition "reconcile").
	CommitReconcileDecision(ctx context.Context, decision DecisionInput) error
	// CommitReconcileIntent persists and activates one latest-state
	// reconciliation intent for an idle route (SRC-005: exactly one,
	// never a partial ordinary batch).
	CommitReconcileIntent(ctx context.Context, intent IntentInput, actor, now string) error
}

// PathFact is one enumerated path's current fact (reconciliation
// comparison unit).
type PathFact struct {
	Path       string
	Digest     string
	Exists     bool
	ObservedAt string
}

// ErrReasonRequired reports an operator resolution without a reason
// (CLI-006: actor and reason are mandatory lineage).
var ErrReasonRequired = errors.New("an operator reason is required")

package ports

import (
	"context"
	"errors"
)

// Work-receipt persistence surface (E5-T1, FBK-005): one durable row per
// (dispatch, run) pair — inserted by `work begin` and updated by
// `work complete` / `work fail` — plus the atomic completion transaction
// that schedules at most one follow-up with the receipt update.

// ErrWorkReceiptNotFound reports no work-receipt row for the dispatch and
// run pair.
var ErrWorkReceiptNotFound = errors.New("work receipt not found")

// ErrRunNotBeginnable reports the run already recorded a receipt (the
// UNIQUE(dispatch_id, run_id) intent enforced before insert).
var ErrRunAlreadyRecorded = errors.New("run already recorded for the dispatch")

// ErrRunNotBegun reports a terminal receipt for a run that never began
// (v0.1 has no completion-only policy, feedback-loop §4).
var ErrRunNotBegun = errors.New("run has no begun receipt")

// WorkChange is one bounded scope item of a work receipt (E12-T3,
// FBK-010): the v1 changes-manifest item shape — a relative path with
// optional before/after digests, never note bodies.
type WorkChange struct {
	Path         string `json:"path"`
	BeforeDigest string `json:"before_digest,omitempty"`
	AfterDigest  string `json:"after_digest,omitempty"`
}

// WorkReceiptInput is the persistence shape of one work receipt.
type WorkReceiptInput struct {
	ReceiptID             string
	DispatchID            string
	RunID                 string
	ResourceID            string
	Status                string // begun | completed | partially_completed | blocked | failed
	FailureCode           string
	ExternalTaskID        string
	BaseRevision          string
	ResultRevision        string
	ChangesJSON           string
	SubmittedAt           string
	ValidationState       string // valid | invalid
	ValidationReasonsJSON string
	// BegunAt preserves the run's begin timestamp across terminal
	// updates (migration v4).
	BegunAt string
	// CompletedScope and RemainingScope carry the partially_completed
	// outcome's bounded scopes (E12-T3, FBK-010); the store persists them
	// as their JSON columns from migration v14.
	CompletedScope []WorkChange
	RemainingScope []WorkChange
	// ManualReason is the blocked outcome's non-empty operator reason
	// (FBK-011).
	ManualReason string
}

// WorkReceiptView is the durable read model of one run's receipt.
type WorkReceiptView struct {
	ReceiptID             string
	DispatchID            string
	RunID                 string
	Status                string
	FailureCode           string
	ValidationState       string
	ValidationReasonsJSON string
	SubmittedAt           string
	// BegunAt is the run's begin timestamp (the attribution window).
	BegunAt string
	// DestinationID names the child lane the receipt's dispatch belongs
	// to (E12-T3, DAT-011): the receipt-to-child association is explicit
	// on every view row; empty for a pre-cutover legacy dispatch.
	DestinationID string
	// ManualReason is the blocked outcome's operator reason (FBK-011).
	ManualReason string
}

// WorkReceiptStore is the durable work-receipt surface.
type WorkReceiptStore interface {
	// LoadWorkReceipt returns the run's receipt row.
	LoadWorkReceipt(ctx context.Context, dispatchID, runID string) (WorkReceiptView, error)
	// InsertWorkReceipt persists a new begun or audited-invalid receipt
	// (one row per dispatch/run pair).
	InsertWorkReceipt(ctx context.Context, w WorkReceiptInput) error
	// CompleteWork applies the terminal receipt update and the completion
	// transaction (route transition, follow-up scheduling) atomically; the
	// row must currently be a begun receipt of the same run.
	CompleteWork(ctx context.Context, w WorkReceiptInput, req ActiveCompletion) (FollowupCreated, error)
	// BlockWork records the blocked outcome (E12-T3, FBK-011): the run's
	// begun receipt becomes blocked with its manual reason, and NO lane
	// completion or follow-up scheduling happens — the child stays active
	// awaiting operator resolution. The row must currently be the begun
	// receipt of the same run.
	BlockWork(ctx context.Context, w WorkReceiptInput) error
	// FailureBudgetRemaining returns the route's remaining consecutive
	// failure budget: the configured budget minus failed work completions
	// recorded since the route's last completed work receipt.
	FailureBudgetRemaining(ctx context.Context, routeID string, budget int) (int, error)
	// AuditWorkReceipt appends one work-receipt audit transition (invalid
	// receipts and lifecycle evidence; FBK-003 retention).
	AuditWorkReceipt(ctx context.Context, transitionID, dispatchID, fromState, toState, recordedAt, contextJSON string) error
	// LoadActiveGenerationChanges returns every observation change merged
	// into the route's current active generation (batches recorded since
	// the active dispatch was created), oldest first — the exact set a
	// completion receipt may partially suppress (E5-T3, FBK-001).
	LoadActiveGenerationChanges(ctx context.Context, routeID, dispatchID string) ([]DirtyChange, error)
	// AuditAttribution appends the exact-suppression decision evidence
	// for one completion receipt (AC-403: the decision is audited).
	AuditAttribution(ctx context.Context, transitionID, dispatchID, recordedAt, contextJSON string) error
}

// DirtyChange is one observed change of the active dirty generation.
type DirtyChange struct {
	Path         string
	Operation    string
	BeforeDigest string
	AfterDigest  string
	DigestStatus string
	ObservedAt   string
	BatchID      string
	// Classification and Disposition are the merging decision's recorded
	// class and outcome for the change's batch (E12-T2: a lane's follow-up
	// re-evaluates its destination's structural conditions per change, and
	// the policy-outcome and classification classes read them).
	Classification string
	Disposition    string
	// SelectedDestinations is the merging occurrence's recorded
	// destination-selection summary (E12 epic validation, migration v15):
	// occurrence-level FAN-005 semantics — when the completing lane is in
	// the selection, the change stays regardless of any per-path
	// condition miss. Empty means unrecorded (legacy rows): the filter
	// falls back to per-change evaluation.
	SelectedDestinations []string
}

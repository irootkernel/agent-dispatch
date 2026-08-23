// Package dispatch implements the structural policy planner (E2-T4,
// POL-001..008): a pure, deterministic, side-effect-free evaluation of a
// normalized source batch against one route policy snapshot into a
// versioned dispatch plan with machine-readable reason codes. The planner
// never inspects note semantics (POL-003), never performs I/O, and a
// payload can never request a disposition (processing-pipeline §5).
package dispatch

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/irootkernel/agent-dispatch/internal/app/ingest"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// PlanSchemaVersion is the dispatch plan contract version
// (docs/schemas/dispatch-plan.schema.json).
const PlanSchemaVersion = "agent-dispatch.dispatch-plan/v1"

// Reason codes (POL-006, machine-readable).
const (
	ReasonNoMeaningfulChanges = "no_meaningful_changes"
	ReasonProtectedPath       = "protected_path_present"
	ReasonImmutablePath       = "immutable_path_present"
	ReasonBatchOverHardLimit  = "batch_over_hard_limit"
	ReasonManifestOverLimit   = "manifest_over_limit"
	ReasonOverThreshold       = "over_automatic_threshold"
	ReasonOverflowSignal      = "overflow_signal"
	ReasonFreshInstance       = "fresh_instance"
	ReasonActiveDispatch      = "active_dispatch_exists"
	ReasonNormalBatch         = "normal_batch"
)

// RoutePolicy is the behavior-affecting route snapshot the plan is
// evaluated against (POL-007/POL-008): one computed route revision plus
// the batching and policy actions from trusted configuration.
type RoutePolicy struct {
	RouteID              string
	RouteRevision        string
	ResourceID           string
	AutomaticThreshold   int
	HardLimit            int
	MaxManifestBytes     int
	BulkAction           string // quarantine | reconcile
	OverflowAction       string // quarantine | reconcile
	FreshInstanceAction  string // quarantine | reconcile
	RequiredCapabilities []string
}

// Input is one evaluation request. ActiveDispatchExists models the
// route-local unresolved-dispatch fact (precedence 7); dry-run callers
// without state pass false.
type Input struct {
	Batch                *ingest.Result
	Flags                records.SourceFlags
	ActiveDispatchExists bool
}

// PlanChange is the canonical change projection inside a plan
// (source-observation $defs.change).
type PlanChange struct {
	Path         string `json:"path"`
	Operation    string `json:"operation"`
	ExistsAfter  bool   `json:"exists_after"`
	FileType     string `json:"file_type"`
	BeforeDigest string `json:"before_digest,omitempty"`
	AfterDigest  string `json:"after_digest,omitempty"`
	DigestStatus string `json:"digest_status"`
}

// Plan is the versioned dispatch plan (dispatch-plan v1). Field order is
// irrelevant to the schema; determinism comes from sorted changes,
// classification, reason codes, and capabilities.
type Plan struct {
	SchemaVersion        string       `json:"schema_version"`
	Route                RouteRef     `json:"route"`
	ResourceID           string       `json:"resource_id"`
	Changes              []PlanChange `json:"changes"`
	ContentFingerprint   string       `json:"content_fingerprint"`
	Classification       []string     `json:"classification"`
	Disposition          string       `json:"disposition"`
	ReasonCodes          []string     `json:"reason_codes"`
	RequiredCapabilities []string     `json:"required_capabilities"`
	GenerationAction     string       `json:"generation_action"`
}

// RouteRef identifies the route and revision the plan was evaluated
// against (POL-008: revalidate before any side effect, SEC-010).
type RouteRef struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

// ManifestBytes estimates the serialized manifest size of the change
// list deterministically: per change, the path and digest text plus a
// fixed per-entry overhead envelope. It is a bound check, not a byte
// copy of the final request rendering.
func ManifestBytes(changes []records.ChangeItem) int {
	total := 0
	for _, c := range changes {
		// Marshal the actual plan projection so the bound covers key
		// names, punctuation, escaping, and enum text exactly.
		raw, err := json.Marshal(projectChanges([]records.ChangeItem{c}))
		if err != nil {
			// Cannot happen for this value type; inflate worst case.
			total += 512 + len(c.Path)*6
			continue
		}
		total += len(raw) + 2 // commas/brackets
	}
	return total
}

// Evaluate plans one batch against one route policy snapshot following
// the disposition precedence of processing-pipeline §5. It is pure: the
// same input and policy always produce the same plan (POL-001).
func Evaluate(policy RoutePolicy, in Input) (*Plan, error) {
	if in.Batch == nil {
		return nil, fmt.Errorf("nil batch")
	}
	if policy.HardLimit <= 0 || policy.AutomaticThreshold <= 0 || policy.MaxManifestBytes <= 0 {
		return nil, fmt.Errorf("route policy limits must be positive")
	}
	plan := &Plan{
		SchemaVersion:        PlanSchemaVersion,
		Route:                RouteRef{ID: policy.RouteID, Revision: policy.RouteRevision},
		ResourceID:           policy.ResourceID,
		ContentFingerprint:   string(in.Batch.Fingerprint),
		RequiredCapabilities: sortedCopy(policy.RequiredCapabilities),
		Changes:              projectChanges(in.Batch.Changes),
	}
	finish := func(disposition records.Disposition, generation records.GenerationAction) *Plan {
		propagateDrops(plan, in.Batch)
		return finishPlan(plan, disposition, generation)
	}

	// Structural classification only (POL-002, POL-003): a non-empty
	// batch is normal until a precedence rule labels it otherwise;
	// protected or immutable status was fixed by the pattern engine.
	classification := map[records.Classification]bool{}
	if len(in.Batch.Changes) > 0 {
		classification[records.ClassNormal] = true
	}

	// Precedence 2: overflow-class signal never yields partial dispatch.
	// Each signal follows its own route action; if both fire, the
	// overflow action decides.
	if in.Flags.Overflow || in.Flags.FreshInstance {
		action := policy.FreshInstanceAction
		if in.Flags.Overflow {
			classification[records.ClassOverflow] = true
			plan.ReasonCodes = append(plan.ReasonCodes, ReasonOverflowSignal)
			action = policy.OverflowAction
		}
		if in.Flags.FreshInstance {
			classification[records.ClassOverflow] = true
			plan.ReasonCodes = append(plan.ReasonCodes, ReasonFreshInstance)
		}
		// The overflow class outranks the protected hold, but the hold
		// stays visible: the reconciliation generation carries the
		// protected reason so the operator sees why the next pass
		// quarantines (the protected path itself never enters an
		// automatic task — PTH-008 clause 2 holds, E8-T5/M-14).
		if len(in.Batch.Protected) > 0 {
			classification[records.ClassProtected] = true
			plan.ReasonCodes = append(plan.ReasonCodes, ReasonProtectedPath)
		}
		plan.Classification = classifyList(classification)
		return finish(actionOr(action, records.DispositionReconcile), records.GenMergeReconcile), nil
	}

	// Precedence 3: no meaningful changes.
	if len(in.Batch.Changes) == 0 {
		plan.ReasonCodes = append(plan.ReasonCodes, ReasonNoMeaningfulChanges)
		plan.Classification = classifyList(classification)
		return finish(records.DispositionDrop, records.GenNone), nil
	}

	// Precedence 4: protected or immutable paths quarantine (PTH-008).
	if len(in.Batch.Protected) > 0 {
		classification[records.ClassProtected] = true
		plan.ReasonCodes = append(plan.ReasonCodes, ReasonProtectedPath)
		plan.Classification = classifyList(classification)
		return finish(records.DispositionQuarantine, records.GenNone), nil
	}
	if len(in.Batch.Immutable) > 0 {
		classification[records.ClassProtected] = true
		plan.ReasonCodes = append(plan.ReasonCodes, ReasonImmutablePath)
		plan.Classification = classifyList(classification)
		return finish(records.DispositionQuarantine, records.GenNone), nil
	}

	// Precedence 5: hard limit quarantines.
	count := len(in.Batch.Changes)
	if count > policy.HardLimit {
		classification[records.ClassBulk] = true
		plan.ReasonCodes = append(plan.ReasonCodes, ReasonBatchOverHardLimit)
		plan.Classification = classifyList(classification)
		return finish(records.DispositionQuarantine, records.GenNone), nil
	}

	// Serialized payload bound (POL-004).
	if ManifestBytes(in.Batch.Changes) > policy.MaxManifestBytes {
		classification[records.ClassBulk] = true
		plan.ReasonCodes = append(plan.ReasonCodes, ReasonManifestOverLimit)
		plan.Classification = classifyList(classification)
		return finish(records.DispositionQuarantine, records.GenNone), nil
	}

	// Precedence 6: over automatic threshold follows route policy.
	if count > policy.AutomaticThreshold {
		classification[records.ClassBulk] = true
		plan.ReasonCodes = append(plan.ReasonCodes, ReasonOverThreshold)
		plan.Classification = classifyList(classification)
		return finish(actionOr(policy.BulkAction, records.DispositionQuarantine), records.GenMergeReconcile), nil
	}

	// Precedence 7: unresolved active dispatch merges into one pending
	// generation instead of a second normal dispatch.
	if in.ActiveDispatchExists {
		plan.ReasonCodes = append(plan.ReasonCodes, ReasonActiveDispatch)
		plan.Classification = classifyList(classification)
		return finish(records.DispositionMergePending, records.GenIncrementDirty), nil
	}

	// Precedence 8: normal bounded batch dispatches.
	plan.ReasonCodes = append(plan.ReasonCodes, ReasonNormalBatch)
	plan.Classification = classifyList(classification)
	return finish(records.DispositionDispatch, records.GenCreateIfIdle), nil
}

func finishPlan(plan *Plan, disposition records.Disposition, generation records.GenerationAction) *Plan {
	plan.Disposition = string(disposition)
	plan.GenerationAction = string(generation)
	if plan.Classification == nil {
		plan.Classification = []string{}
	}
	if plan.ReasonCodes == nil {
		plan.ReasonCodes = []string{}
	}
	return plan
}

// propagateDrops surfaces the batch's suppressed paths in the plan's
// reason codes (AC-102, E7-T3/H-2): an unchanged or metadata-only modify
// suppressed by the durable path facts must be visible in the plan and
// the persisted decision, never silently discarded.
func propagateDrops(plan *Plan, batch *ingest.Result) {
	if batch == nil {
		return
	}
	seen := make(map[string]bool, len(batch.Dropped))
	for _, d := range batch.Dropped {
		seen[d.Reason] = true
	}
	for _, reason := range []string{ingest.ReasonUnchangedModify, ingest.ReasonCreateDeleteNever} {
		if seen[reason] && !containsReason(plan.ReasonCodes, reason) {
			plan.ReasonCodes = append(plan.ReasonCodes, reason)
		}
	}
}

func containsReason(codes []string, reason string) bool {
	for _, c := range codes {
		if c == reason {
			return true
		}
	}
	return false
}

func actionOr(action string, fallback records.Disposition) records.Disposition {
	switch records.Disposition(action) {
	case records.DispositionQuarantine, records.DispositionReconcile:
		return records.Disposition(action)
	default:
		return fallback
	}
}

func classifyList(set map[records.Classification]bool) []string {
	order := []records.Classification{
		records.ClassMalformed, records.ClassOverflow, records.ClassProtected,
		records.ClassBulk, records.ClassStale, records.ClassUnknown, records.ClassNormal,
	}
	var out []string
	for _, c := range order {
		if set[c] {
			out = append(out, string(c))
		}
	}
	return out
}

func projectChanges(changes []records.ChangeItem) []PlanChange {
	out := make([]PlanChange, len(changes))
	for i, c := range changes {
		out[i] = PlanChange{
			Path:         c.Path,
			Operation:    string(c.Operation),
			ExistsAfter:  c.ExistsAfter,
			FileType:     string(c.FileType),
			BeforeDigest: string(c.BeforeDigest),
			AfterDigest:  string(c.AfterDigest),
			DigestStatus: string(c.DigestStatus),
		}
	}
	return out
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

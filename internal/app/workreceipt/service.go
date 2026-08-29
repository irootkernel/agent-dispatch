// Package workreceipt implements the cooperative work-receipt service
// (E5-T1, FBK-005): plugin-free begin, complete, and fail receipts from
// a Hermes task, validated against the durable dispatch lineage and the
// work-receipt schema before anything is persisted. An invalid receipt is
// audited and rejected, never silently applied (FBK-002, FBK-003).
package workreceipt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/irootkernel/agent-dispatch/internal/adapters/localfs"
	"github.com/irootkernel/agent-dispatch/internal/app/dispatch"
	"github.com/irootkernel/agent-dispatch/internal/domain/ids"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/domain/state"
	"github.com/irootkernel/agent-dispatch/internal/observability"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// SchemaVersion is the receipt document version this service accepts
// (docs/schemas/work-receipt.schema.json). v2 (E12-T3, FBK-009) widens
// the outcome vocabulary; v1 documents remain valid inputs (the stored
// history stays readable, DAT-009).
const SchemaVersion = "agent-dispatch.work-receipt/v2"

// The closed v2 outcome set after a run begins (FBK-009): the completed,
// partially_completed (FBK-010), blocked (FBK-011), and failed outcomes
// the CLI's `work complete --status` / `work fail` surfaces record. The
// values are the domain's WorkStatus declarations — the one live
// vocabulary (review round 1, maintainability finding); no parallel
// string set exists.
const (
	StatusCompleted         = string(records.WorkCompleted)
	StatusPartiallyComplete = string(records.WorkPartiallyComplete)
	StatusBlocked           = string(records.WorkBlocked)
	StatusBegun             = string(records.WorkBegan)
	StatusFailed            = string(records.WorkFailed)
)

// maxManualReasonCharacters bounds the blocked outcome's manual reason in
// characters (FBK-011): the schema's 200-code-point limit, counted in
// runes so multi-byte operator text is bounded exactly as documented.
const maxManualReasonCharacters = 200

// Echo bounds for rejection reasons that quote untrusted submission
// members into the append-only audit trail (review round 1, security
// finding): statuses and identifiers are truncated before any write.
const (
	statusEchoBound = 64
	idEchoBound     = 256
)

// boundEcho renders one untrusted echo clearly truncated at the bound.
func boundEcho(text string, bound int) string {
	runes := []rune(text)
	if len(runes) <= bound {
		return text
	}
	return string(runes[:bound]) + "...(truncated)"
}

// sameChangeEntries compares two scope sets for content equality in
// canonical order (the document-vs-flag conflict check; order-sensitive
// inputs are already canonicalized by validation).
func sameChangeEntries(a, b []changeEntry) bool {
	if len(a) != len(b) {
		return false
	}
	// Compare by VALUE over a canonical order (E12 epic validation): the
	// entries carry optional-digest POINTERS, so struct comparison
	// compares addresses and can never match two equal scopes; canonical
	// sorting makes the comparison order-insensitive as submitted scopes
	// legitimately differ in order.
	key := func(entries []changeEntry) []string {
		keys := make([]string, 0, len(entries))
		for _, e := range entries {
			before, after := "", ""
			if e.BeforeDigest != nil {
				before = *e.BeforeDigest
			}
			if e.AfterDigest != nil {
				after = *e.AfterDigest
			}
			keys = append(keys, e.Path+"\x00"+before+"\x00"+after)
		}
		sort.Strings(keys)
		return keys
	}
	left, right := key(a), key(b)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// Limits bound one receipt submission (SEC-009); the change count and
// digest shapes mirror the schema, the byte caps bound the untrusted
// input before parsing.
const (
	MaxChanges       = 1000
	MaxManifestBytes = 1 << 20
	MaxIDBytes       = 256
	MaxDetailBytes   = 200
)

// failureCodes is the closed v0.1 failure-code set.
var failureCodes = map[string]bool{"agent_error": true, "canceled": true, "timeout": true, "environment_error": true}

// docKeys and changeKeys are the exact key spellings the published
// schema admits (additionalProperties false at the document and change
// levels both): Go's decoder matches struct tags case-insensitively, so
// exact-key comparison is done here, never by the decoder alone.
var docKeys = map[string]bool{
	"schema_version": true, "dispatch_id": true, "external_task_id": true, "run_id": true,
	"resource_id": true, "status": true, "base_revision": true, "result_revision": true,
	"submitted_at": true, "changes": true, "failure_code": true,
	// The v2 outcome members (E12-T3, FBK-009 through FBK-011).
	"completed_scope": true, "remaining_scope": true, "manual_reason": true,
}

var changeKeys = map[string]bool{"path": true, "before_digest": true, "after_digest": true}

// receiptDoc is the full work-receipt document shape; decoding is strict
// so a note body or any unknown field is rejected, never absorbed.
type receiptDoc struct {
	SchemaVersion  *string        `json:"schema_version"`
	DispatchID     *string        `json:"dispatch_id"`
	ExternalTaskID *string        `json:"external_task_id"`
	RunID          *string        `json:"run_id"`
	ResourceID     *string        `json:"resource_id"`
	Status         *string        `json:"status"`
	BaseRevision   *string        `json:"base_revision"`
	ResultRevision *string        `json:"result_revision"`
	SubmittedAt    *string        `json:"submitted_at"`
	Changes        *[]changeEntry `json:"changes"`
	FailureCode    *string        `json:"failure_code"`
	// The v2 outcome members (E12-T3, FBK-009 through FBK-011): a v2
	// document carries its own outcome and the members that outcome
	// requires, and the submission routes through them (review round 1).
	CompletedScope *[]changeEntry `json:"completed_scope"`
	RemainingScope *[]changeEntry `json:"remaining_scope"`
	ManualReason   *string        `json:"manual_reason"`
}

// manifestDocument is the parsed full-document form beside its change
// entries (E12-T3 review round 1): zeroed when the submission was the
// bare changes array, and carrying the v2 outcome members when it was a
// v2 document.
type manifestDocument struct {
	IsV2           bool
	SchemaVersion  string
	Status         string
	CompletedScope []changeEntry
	RemainingScope []changeEntry
	ManualReason   string
}

// changeEntry is one manifest row: only a relative path and before/after
// digests (no note bodies, cli-spec §7).
type changeEntry struct {
	Path         string  `json:"path"`
	BeforeDigest *string `json:"before_digest"`
	AfterDigest  *string `json:"after_digest"`
}

// Store is the durable surface the service needs.
type Store interface {
	ports.DispatchStore
	ports.RouteCoordinationStore
	ports.WorkReceiptStore
	// LoadPathFacts returns the stored full-scope path-fact snapshot
	// (declared as the single method rather than embedding the whole
	// quarantine surface; the sqlite store satisfies it structurally).
	LoadPathFacts(ctx context.Context, resourceID string) (map[string]ports.PathFact, error)
}

// Service validates and records work receipts.
type Service struct {
	Store Store
	// Now is the wall clock; tests inject a deterministic one.
	Now func() time.Time
	// Resolver enforces path containment against the resource root
	// (SEC-002); nil restricts validation to the lexical rules.
	Resolver *localfs.Resolver
	// FailureBudget is the route's configured consecutive-failure budget.
	FailureBudget int
	// OutsideScope reports whether a path is outside the route's
	// effective scope (excluded or not admitted); nil means every path is
	// in scope. Receipt paths it rejects never count as extra provenance
	// (E8-T1, H-1.1).
	OutsideScope func(path string) bool
	// Log emits the declarative work.* lifecycle events at the receipt
	// boundaries (E9-T3/L-17); nil skips emission. TraceID rides the
	// causal correlation (the CLI owns the request's trace).
	Log     *observability.Logger
	TraceID string
	// PolicyRevision is the independent policy digest of the live route
	// (config.PolicyRevision): the follow-up decision the store may
	// create records it instead of a route revision echo (E9-T3, L-18).
	PolicyRevision string
	// DestinationResolver supplies the live destination lane of a route
	// (E12-T1): completing pre-cutover legacy work that still owes a
	// follow-up resolves the current certified lane so the follow-up
	// child-links under the destinations[] contract instead of failing
	// the whole receipt. Nil disables the resolution and legacy
	// completions fail closed with guidance.
	DestinationResolver dispatch.DestinationLaneResolver
	// LaneConditions resolves one destination's structural selection
	// conditions (E12-T2, CON-008): the follow-up projection filters the
	// dirty generation to the changes the completing dispatch's lane
	// selects, so one lane's follow-up can never incorporate a sibling
	// lane's conditioned-out work. Nil disables the filtering (legacy
	// and test paths); a non-nil resolver that fails for a child-linked
	// dispatch fails the completion closed.
	LaneConditions func(routeID, destinationID string) (*dispatch.DestinationConditionSet, error)
	// LanePathMatcher is the per-pattern path matcher the lane filter
	// evaluates path conditions through (the CLI wires the pattern
	// engine's matcher, the same delegate the dispatch surface uses).
	// Path conditions with a nil matcher fail the completion closed.
	LanePathMatcher func(pattern, path string) (bool, error)
}

// InvalidError reports a receipt rejected by validation; Reasons are the
// bounded audit reasons persisted with the rejected evidence.
type InvalidError struct {
	Reasons []string
}

func (e *InvalidError) Error() string {
	return "work receipt rejected: " + strings.Join(e.Reasons, "; ")
}

// BeginInput is the `work begin` submission.
type BeginInput struct {
	DispatchID     string
	RunID          string
	ExternalTaskID string
	BaseRevision   string
}

// CompleteInput is the `work complete` submission.
type CompleteInput struct {
	DispatchID     string
	RunID          string
	ResultRevision string
	// ManifestJSON is the raw manifest document: either the full
	// work-receipt object or its bare changes array.
	ManifestJSON string
	// Status is the outcome (E12-T3, FBK-009): completed (the default),
	// partially_completed (FBK-010), or blocked (FBK-011). The failed
	// outcome stays on `work fail`.
	Status string
	// RemainingManifestJSON is the partially_completed outcome's remaining
	// scope (required, non-empty, bounded like the manifest).
	RemainingManifestJSON string
	// ManualReason is the blocked outcome's non-empty operator reason
	// (required for blocked).
	ManualReason string
}

// FailInput is the `work fail` submission.
type FailInput struct {
	DispatchID  string
	RunID       string
	FailureCode string
	Detail      string
}

// Result reports one accepted receipt.
type Result struct {
	ReceiptID          string `json:"receipt_id"`
	DispatchID         string `json:"dispatch_id"`
	RunID              string `json:"run_id"`
	Status             string `json:"status"`
	RouteState         string `json:"route_state,omitempty"`
	FollowupDispatchID string `json:"followup_dispatch_id,omitempty"`
	DirtyGeneration    int    `json:"dirty_generation"`
	FailureBudgetLeft  int    `json:"failure_budget_remaining,omitempty"`
	// SuppressedPaths lists exactly-verified self-generated changes and
	// SelfChangeSuppressed reports the whole generation cleared (E5-T3).
	SuppressedPaths      []string `json:"suppressed_paths,omitempty"`
	SelfChangeSuppressed bool     `json:"self_change_suppressed,omitempty"`
	// ManualIntervention reports the blocked outcome (FBK-011): the lane
	// stays active awaiting operator resolution; nothing auto-runs.
	ManualIntervention bool `json:"manual_intervention,omitempty"`
	// AuditWarning reports a failed post-commit attribution audit
	// append (the completion stands; the evidence gap is visible).
	AuditWarning error `json:"-"`
}

// Begin validates and records one begun run (FBK-005).
func (s *Service) Begin(ctx context.Context, in BeginInput) (Result, error) {
	intent, snap, reasons, err := s.validateLineage(ctx, in.DispatchID, in.RunID, in.ExternalTaskID)
	if err != nil {
		return Result{}, err
	}
	replayReasons, err := s.validateNewRun(ctx, in.DispatchID, in.RunID)
	if err != nil {
		return Result{}, err
	}
	reasons = append(reasons, replayReasons...)
	if len(reasons) > 0 {
		s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: reasons})
		return Result{}, &InvalidError{Reasons: reasons}
	}
	w := ports.WorkReceiptInput{
		ReceiptID:       s.receiptID(in.DispatchID),
		DispatchID:      in.DispatchID,
		RunID:           in.RunID,
		ResourceID:      intent.ResourceID,
		Status:          "begun",
		ExternalTaskID:  in.ExternalTaskID,
		BaseRevision:    in.BaseRevision,
		SubmittedAt:     s.timestamp(),
		ValidationState: "valid",
		BegunAt:         s.timestamp(),
	}
	if err := s.Store.InsertWorkReceipt(ctx, w); err != nil {
		return Result{}, ports.WrapStore(err)
	}
	s.logEvent(observability.EventWorkBegun, in.DispatchID, in.RunID)
	return Result{ReceiptID: w.ReceiptID, DispatchID: in.DispatchID, RunID: in.RunID, Status: "begun", RouteState: string(snap.State)}, nil
}

// logEvent emits one work.* lifecycle event at the receipt boundaries
// when the operational logger is wired (E9-T3/L-17); nil skips
// emission. The causal identity (trace, dispatch, run) rides the
// correlation fields.
func (s *Service) logEvent(event, dispatchID, runID string) {
	if s.Log == nil {
		return
	}
	s.Log.Log(observability.LevelInfo, event,
		observability.Correlation{TraceID: s.TraceID, DispatchID: dispatchID, RunID: runID},
		"work receipt lifecycle", map[string]any{"dispatch_id": dispatchID, "run_id": runID})
}

// Complete validates one completion manifest, records the terminal
// receipt, and applies the completion transaction: when dirty work or a
// pending reconciliation remains, exactly one latest-state follow-up is
// scheduled atomically with the receipt update (CON-003).
func (s *Service) Complete(ctx context.Context, in CompleteInput) (Result, error) {
	// The outcome vocabulary is closed (FBK-009): completed is the
	// default; partially_completed and blocked carry their own required
	// members; failed stays on `work fail`. A v2 full document names its
	// own outcome and routes through it (review round 1): the flag and
	// the document must agree.
	requestedStatus := in.Status
	if requestedStatus == "" {
		requestedStatus = StatusCompleted
	}
	if _, err := records.ParseWorkStatus(requestedStatus); err != nil || requestedStatus == StatusBegun || requestedStatus == StatusFailed {
		reason := fmt.Sprintf("status %q is not a work complete outcome (completed, partially_completed, blocked; failed belongs to work fail)", boundEcho(in.Status, statusEchoBound))
		s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{reason}})
		return Result{}, &InvalidError{Reasons: []string{reason}}
	}
	intent, snap, reasons, err := s.validateLineage(ctx, in.DispatchID, in.RunID, "")
	if err != nil {
		return Result{}, err
	}
	if len(reasons) > 0 {
		s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: reasons})
		return Result{}, &InvalidError{Reasons: reasons}
	}
	// A blocked run may have changed nothing: the empty manifest is the
	// honest scope when no document was submitted (FBK-011).
	manifestJSON := in.ManifestJSON
	if manifestJSON == "" {
		manifestJSON = "[]"
	}
	changes, resultRevision, document, manifestReasons := s.validateManifest(manifestJSON, in.DispatchID, in.RunID, intent.ResourceID, intent.ExternalRef)
	if len(manifestReasons) > 0 {
		s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: manifestReasons})
		return Result{}, &InvalidError{Reasons: manifestReasons}
	}
	if in.ResultRevision != "" {
		resultRevision = in.ResultRevision
	}
	// Reconcile the outcome: a v2 full document's status is authoritative
	// for the document's own members; an explicit --status must agree with
	// it (a conflict is a validation error, review round 1).
	status := requestedStatus
	if document.IsV2 && document.Status != "" {
		if in.Status != "" && in.Status != document.Status {
			reason := fmt.Sprintf("the document status %q conflicts with --status %q; submit one outcome", document.Status, boundEcho(in.Status, statusEchoBound))
			s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{reason}})
			return Result{}, &InvalidError{Reasons: []string{reason}}
		}
		status = document.Status
	}
	// --manual-reason belongs to the blocked outcome only (E12 epic
	// validation): beside any other outcome it was silently discarded. The
	// guard validates against the EFFECTIVE outcome — the reconciled
	// document status first, the flag status otherwise (E12 epic
	// whole-review round 1) — so a blocked DOCUMENT carrying its matching
	// flag reason is the legal doubled form, never a misleading rejection
	// that names the flag default the document already overrode. The
	// message names exactly the blocked outcome (round 3): the rule admits
	// one status, so the rejection names it, never a wider list.
	if strings.TrimSpace(in.ManualReason) != "" && status != StatusBlocked {
		reason := fmt.Sprintf("--manual-reason belongs to the blocked outcome only (carry a manual reason just with --status blocked); got status %q", boundEcho(status, statusEchoBound))
		s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{reason}})
		return Result{}, &InvalidError{Reasons: []string{reason}}
	}
	manualReason := strings.TrimSpace(in.ManualReason)
	if document.IsV2 && strings.TrimSpace(document.ManualReason) != "" {
		if manualReason != "" && manualReason != strings.TrimSpace(document.ManualReason) {
			reason := "the document manual_reason conflicts with --manual-reason; submit one manual reason"
			s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{reason}})
			return Result{}, &InvalidError{Reasons: []string{reason}}
		}
		// The document form trims like the flag form (E12 epic
		// validation): a whitespace-only manual_reason is empty.
		manualReason = strings.TrimSpace(document.ManualReason)
		// A document manual_reason beside any outcome other than blocked
		// rejects exactly like the flag form (E12 epic whole-review round
		// 2): the effective outcome already reconciled above, so a manual
		// reason riding a completed or partially_completed document is a
		// shape error naming the status — never silently discarded.
		if status != StatusBlocked {
			reason := fmt.Sprintf("the document manual_reason belongs to the blocked outcome only (carry a manual reason just with a blocked document); got status %q", boundEcho(status, statusEchoBound))
			s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{reason}})
			return Result{}, &InvalidError{Reasons: []string{reason}}
		}
	}
	if status == StatusBlocked {
		// A blocked outcome takes its manual reason — never a completion
		// manifest: a bare change array (or v1 document) beside a blocked
		// outcome is a shape error, and a v2 blocked document carrying a
		// NON-EMPTY change set rejects the same way (E12 epic validation:
		// blocked takes the reason, not a change set — dropped changes
		// were silently discarded evidence). The empty-changes v2 blocked
		// document remains the shipped example form.
		if len(changes) > 0 {
			reason := "a blocked receipt takes its manual reason, not a change set; submit the changes through the resolving work complete"
			s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{reason}})
			return Result{}, &InvalidError{Reasons: []string{reason}}
		}
		if !document.IsV2 && strings.TrimSpace(in.ManifestJSON) != "" && strings.TrimSpace(in.ManifestJSON) != "[]" {
			reason := "a blocked receipt takes --manual-reason only; submit the completion manifest through the resolving work complete"
			s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{reason}})
			return Result{}, &InvalidError{Reasons: []string{reason}}
		}
		if manualReason == "" {
			reason := "a blocked receipt requires a non-empty manual reason (--manual-reason or the document's manual_reason)"
			s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{reason}})
			return Result{}, &InvalidError{Reasons: []string{reason}}
		}
		if utf8.RuneCountInString(manualReason) > maxManualReasonCharacters {
			reason := fmt.Sprintf("manual_reason exceeds %d characters", maxManualReasonCharacters)
			s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{reason}})
			return Result{}, &InvalidError{Reasons: []string{reason}}
		}
	}
	// The begun receipt anchors the attribution window; a completion
	// without one is rejected by the atomic update too, but the matcher
	// needs its timestamp before any mutation.
	begun, err := s.Store.LoadWorkReceipt(ctx, in.DispatchID, in.RunID)
	if errors.Is(err, ports.ErrWorkReceiptNotFound) {
		reason := fmt.Sprintf("run %s has no begun receipt for dispatch %s", boundEcho(in.RunID, idEchoBound), boundEcho(in.DispatchID, idEchoBound))
		s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{reason}})
		return Result{}, &InvalidError{Reasons: []string{reason}}
	}
	if err != nil {
		return Result{}, ports.WrapStore(err)
	}
	// The blocked outcome (FBK-011): the receipt records its manual
	// reason and NOTHING else happens — the lane stays active with its
	// child, no completion, no follow-up, nothing auto-runs (the automatic
	// retry machinery only touches retry_wait/dead_lettered states).
	if status == StatusBlocked {
		in.ManualReason = manualReason
		return s.completeBlocked(ctx, intent, snap, in)
	}
	// The partially_completed outcome (FBK-010): the remaining scope is
	// required, bounded, and non-empty (an empty scope is the completed
	// outcome — the operator is told so). The scope comes from
	// --remaining-manifest or the v2 document's remaining_scope; both
	// present with different content is an explicit conflict.
	var remainingScope []records.ChangeItem
	// The v2 document's completed_scope is authoritative when present
	// (E12 epic validation): persist it on the receipt row exactly as the
	// flag path persists its scope, never validating-then-dropping it.
	completedScope := changes
	if document.IsV2 && len(document.CompletedScope) > 0 {
		// Subset cross-check (E12 epic whole-review round 1): the completed
		// scope must name only paths the document's own changes report — a
		// scope claiming paths the change set never carried is unauditable
		// completion evidence, so it rejects with a bounded error.
		manifestPaths := make(map[string]bool, len(changes))
		for _, c := range changes {
			manifestPaths[c.Path] = true
		}
		for _, entry := range document.CompletedScope {
			if !manifestPaths[entry.Path] {
				reason := fmt.Sprintf("the document completed_scope names path %q absent from its changes; the completed scope must be a subset of the reported changes", boundEcho(entry.Path, idEchoBound))
				s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{reason}})
				return Result{}, &InvalidError{Reasons: []string{reason}}
			}
		}
		completedScope = document.CompletedScope
	}
	if status == StatusPartiallyComplete {
		remainingEntries := document.RemainingScope
		if strings.TrimSpace(in.RemainingManifestJSON) != "" {
			flagRemaining, _, flagDoc, flagReasons := s.validateManifest(in.RemainingManifestJSON, in.DispatchID, in.RunID, intent.ResourceID, intent.ExternalRef)
			if len(flagReasons) > 0 {
				s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: flagReasons})
				return Result{}, &InvalidError{Reasons: flagReasons}
			}
			if len(document.RemainingScope) > 0 && !sameChangeEntries(flagRemaining, document.RemainingScope) {
				reason := "the document remaining_scope conflicts with --remaining-manifest; submit one remaining scope"
				s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{reason}})
				return Result{}, &InvalidError{Reasons: []string{reason}}
			}
			if flagDoc.IsV2 && len(flagDoc.RemainingScope) > 0 {
				remainingEntries = flagDoc.RemainingScope
			} else {
				remainingEntries = flagRemaining
			}
		}
		if len(remainingEntries) == 0 {
			reason := "the remaining scope of a partially_completed receipt must not be empty; submit --status completed when no work remains"
			s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{reason}})
			return Result{}, &InvalidError{Reasons: []string{reason}}
		}
		for _, entry := range remainingEntries {
			remainingScope = append(remainingScope, records.ChangeItem{
				Path: entry.Path, Operation: scopeOperation(entry.BeforeDigest, entry.AfterDigest),
				BeforeDigest: derefDigest(entry.BeforeDigest), AfterDigest: derefDigest(entry.AfterDigest),
			})
		}
	}
	changesJSON, _ := json.Marshal(changes)
	w := ports.WorkReceiptInput{
		ReceiptID:       s.receiptID(in.DispatchID),
		DispatchID:      in.DispatchID,
		RunID:           in.RunID,
		ResourceID:      intent.ResourceID,
		Status:          status,
		ResultRevision:  resultRevision,
		ChangesJSON:     string(changesJSON),
		SubmittedAt:     s.timestamp(),
		ValidationState: "valid",
		// The partial outcome's scopes persist beside the receipt
		// (migration v14): the completed scope is the document's own when
		// it carried one, else the manifest above; the remaining scope is
		// exactly what the follow-up will carry.
		CompletedScope: workScopeOf(entriesToItems(completedScope)),
		RemainingScope: workScopeOf(remainingScope),
	}
	// Exact self-change attribution (E5-t3): match the receipt against
	// the dirty generation; only a fully verified generation may clear
	// the route without a follow-up, and the decision is always audited.
	// The generation is scoped to the completing dispatch's lane first
	// (E12-T2, CON-008): the matcher and the follow-up projection see
	// only this lane's changes.
	dirty, err := s.Store.LoadActiveGenerationChanges(ctx, intent.RouteID, in.DispatchID)
	if err != nil {
		return Result{}, ports.WrapStore(err)
	}
	dirty, err = s.filterDirtyToLane(intent, dirty)
	if err != nil {
		return Result{}, err
	}
	evidence := ReceiptEvidence{
		ReceiptID: w.ReceiptID, DispatchID: in.DispatchID, RunID: in.RunID,
		ResourceID: intent.ResourceID, BegunAt: begun.BegunAt, CompletedAt: w.SubmittedAt,
		Changes: changes, OutsideScope: s.OutsideScope,
	}
	factsUnavailable := false
	if facts, ferr := s.Store.LoadPathFacts(ctx, intent.ResourceID); ferr == nil {
		evidence.FactDigest = func(path string) (string, bool) {
			f, ok := facts[path]
			return f.Digest, ok && f.Exists
		}
	} else {
		// A storage failure degrades attribution conservatively (identical
		// rewrites classify as extra provenance); the decision document
		// records the degradation so the audit trail can explain it
		// (E8-T1 round-1 F008).
		factsUnavailable = true
	}
	decision := Match(evidence, dirty)
	decision.FactsUnavailable = factsUnavailable
	var auditErr error
	out, err := s.applyCompletion(ctx, intent, snap, w, ports.ActiveCompletion{
		RouteID: intent.RouteID, DispatchID: in.DispatchID, Failed: false,
		ReceiptRef: w.ReceiptID,
		// A partial completion always owes the remaining scope: the
		// receipt never clears the lane as fully suppressed (FBK-010).
		DirtySuppressed: decision.FullySuppressed && status != StatusPartiallyComplete,
		FenceGeneration: true, ExpectedDirtyGeneration: snap.DirtyGeneration,
		RemainingWork: status == StatusPartiallyComplete,
	}, &decision, dirty, remainingScope)
	if err != nil {
		return out, err
	}
	// The decision evidence is recorded only after its receipt
	// committed: the audit never references an unpersisted receipt
	// (E5 audit F002).
	auditErr = s.Store.AuditAttribution(ctx, "attr-"+w.ReceiptID, in.DispatchID, w.SubmittedAt, decision.ContextJSON())
	out.SuppressedPaths = decision.SuppressedPaths
	// Report suppression only when a dirty generation actually
	// cleared (a clean route suppresses nothing, E5 audit F010).
	out.SelfChangeSuppressed = decision.FullySuppressed && snap.DirtyGeneration > 0
	// The completion already committed: a failed audit append is
	// surfaced, never silently discarded (E5 audit round 7).
	out.AuditWarning = auditErr
	if status == StatusPartiallyComplete {
		s.logEvent(observability.EventWorkPartiallyCompleted, in.DispatchID, in.RunID)
	} else {
		s.logEvent(observability.EventWorkCompleted, in.DispatchID, in.RunID)
	}
	return out, nil
}

// Fail validates one cooperative failure and applies the completion
// transaction: the failure never erases dirty route state, and the
// remaining failure budget decides follow-up versus operator resolution
// (feedback-loop §7).
func (s *Service) Fail(ctx context.Context, in FailInput) (Result, error) {
	var reasons []string
	if !failureCodes[in.FailureCode] {
		reasons = append(reasons, fmt.Sprintf("failure_code %q is outside the closed set (agent_error, canceled, timeout, environment_error)", in.FailureCode))
	}
	if len(in.Detail) > MaxDetailBytes {
		reasons = append(reasons, fmt.Sprintf("detail exceeds %d bytes", MaxDetailBytes))
	}
	intent, snap, lineageReasons, err := s.validateLineage(ctx, in.DispatchID, in.RunID, "")
	if err != nil {
		return Result{}, err
	}
	reasons = append(reasons, lineageReasons...)
	if len(reasons) > 0 {
		s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: reasons})
		return Result{}, &InvalidError{Reasons: reasons}
	}
	w := ports.WorkReceiptInput{
		ReceiptID:       s.receiptID(in.DispatchID),
		DispatchID:      in.DispatchID,
		RunID:           in.RunID,
		ResourceID:      intent.ResourceID,
		Status:          "failed",
		FailureCode:     in.FailureCode,
		SubmittedAt:     s.timestamp(),
		ValidationState: "valid",
	}
	budget, err := s.Store.FailureBudgetRemaining(ctx, intent.RouteID, s.FailureBudget)
	if err != nil {
		return Result{}, ports.WrapStore(err)
	}
	dirty, err := s.Store.LoadActiveGenerationChanges(ctx, intent.RouteID, in.DispatchID)
	if err != nil {
		return Result{}, ports.WrapStore(err)
	}
	// The failure path's follow-up carries the same lane-scoped
	// generation (E12-T2, CON-008).
	dirty, err = s.filterDirtyToLane(intent, dirty)
	if err != nil {
		return Result{}, err
	}
	// The same generation fence as Complete: a burst merging between this
	// snapshot and the transaction must not be silently dropped from the
	// follow-up's coverage (E8-T1 round-1 F003).
	return s.applyCompletion(ctx, intent, snap, w, ports.ActiveCompletion{
		RouteID: intent.RouteID, DispatchID: in.DispatchID, Failed: true,
		FailureBudgetRemaining: budget, ReceiptRef: w.ReceiptID, Actor: "hermes-task",
		FenceGeneration: true, ExpectedDirtyGeneration: snap.DirtyGeneration,
	}, nil, dirty, nil)
}

// completeBlocked records the blocked outcome (E12-T3, FBK-011): the
// begun receipt becomes blocked with its manual reason and the child
// keeps its lane — no completion transaction, no follow-up, no state
// change. Resolution is operator-only: a later `work complete`/`work
// fail` for the same dispatch (a fresh run), or a rerun.
func (s *Service) completeBlocked(ctx context.Context, intent ports.IntentSnapshot, snap state.RouteSnapshot, in CompleteInput) (Result, error) {
	if _, err := s.Store.LoadWorkReceipt(ctx, in.DispatchID, in.RunID); errors.Is(err, ports.ErrWorkReceiptNotFound) {
		s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{fmt.Sprintf("run %s has no begun receipt for dispatch %s", in.RunID, in.DispatchID)}})
		return Result{}, &InvalidError{Reasons: []string{fmt.Sprintf("run %s has no begun receipt for dispatch %s", in.RunID, in.DispatchID)}}
	} else if err != nil {
		return Result{}, ports.WrapStore(err)
	}
	w := ports.WorkReceiptInput{
		ReceiptID:       s.receiptID(in.DispatchID),
		DispatchID:      in.DispatchID,
		RunID:           in.RunID,
		ResourceID:      intent.ResourceID,
		Status:          StatusBlocked,
		SubmittedAt:     s.timestamp(),
		ValidationState: "valid",
		ManualReason:    strings.TrimSpace(in.ManualReason),
	}
	if err := s.Store.BlockWork(ctx, w); err != nil {
		return Result{}, ports.WrapStore(err)
	}
	s.logEvent(observability.EventWorkBlocked, in.DispatchID, in.RunID)
	return Result{
		ReceiptID: w.ReceiptID, DispatchID: in.DispatchID, RunID: in.RunID,
		Status: StatusBlocked, RouteState: string(snap.State), ManualIntervention: true,
	}, nil
}

// scopeOperation derives a scope item's change operation from its
// optional digests (FBK-010, E12 epic validation): the manifest item
// shape carries no operation member, and the follow-up's fingerprint
// projection needs one. Deletion evidence demands the exact
// before-present/after-absent pair; creation demands
// before-absent/after-present. A row with NO digests carries neither
// piece of evidence and classifies as modify — the conservative default
// that never fabricates removal (or creation) evidence.
func scopeOperation(before, after *string) records.Operation {
	beforePresent := before != nil && *before != ""
	afterPresent := after != nil && *after != ""
	switch {
	case beforePresent && !afterPresent:
		return records.OpDelete
	case !beforePresent && afterPresent:
		return records.OpCreate
	default:
		return records.OpModify
	}
}

// derefDigest renders an optional manifest digest pointer as the plain
// digest value (nil stays empty).
func derefDigest(d *string) records.Digest {
	if d == nil {
		return ""
	}
	return records.Digest(*d)
}

// entriesToItems projects validated receipt entries onto the canonical
// change-item shape (digests stay optional; the manifest item members
// only).
func entriesToItems(entries []changeEntry) []records.ChangeItem {
	out := make([]records.ChangeItem, 0, len(entries))
	for _, e := range entries {
		out = append(out, records.ChangeItem{Path: e.Path, BeforeDigest: derefDigest(e.BeforeDigest), AfterDigest: derefDigest(e.AfterDigest)})
	}
	return out
}

// workScopeOf projects receipt change entries onto the durable
// WorkChange scope shape (the v1 manifest item members only, FBK-010).
func workScopeOf(changes []records.ChangeItem) []ports.WorkChange {
	if len(changes) == 0 {
		return nil
	}
	out := make([]ports.WorkChange, 0, len(changes))
	for _, c := range changes {
		out = append(out, ports.WorkChange{Path: c.Path, BeforeDigest: string(c.BeforeDigest), AfterDigest: string(c.AfterDigest)})
	}
	return out
}

// filterDirtyToLane scopes one dirty generation to the completing
// dispatch's destination lane (E12-T2, CON-008; E12 epic validation).
// The PRIMARY rule is occurrence-level (FAN-005): a change whose merging
// batch recorded its destination selection stays whenever that selection
// contains the completing lane — the same occurrence-level OR the
// arrival evaluation used, so a path that alone fails a path_include is
// never silently dropped from a lane the occurrence selected. The
// per-change condition evaluation (the path, its operation, and the
// merging decision's classification and disposition) is the FALLBACK for
// rows without recorded selection evidence — legacy batches and any
// writer that predates migration v15. A pre-contract dispatch (empty
// destination identity) keeps the route-scoped generation; a
// child-linked dispatch whose lane conditions cannot be resolved fails
// the completion closed, never silently widens the follow-up.
func (s *Service) filterDirtyToLane(intent ports.IntentSnapshot, dirty []ports.DirtyChange) ([]ports.DirtyChange, error) {
	if intent.DestinationID == "" || s.LaneConditions == nil {
		return dirty, nil
	}
	conds, err := s.LaneConditions(intent.RouteID, intent.DestinationID)
	if err != nil {
		return nil, fmt.Errorf("resolving the destination %q lane conditions of dispatch %s: %w", intent.DestinationID, intent.DispatchID, err)
	}
	if conds == nil {
		// The destination selects unconditionally: the whole generation is
		// this lane's (fanout_mode all over an unconditioned destination).
		return dirty, nil
	}
	out := make([]ports.DirtyChange, 0, len(dirty))
	for _, c := range dirty {
		if len(c.SelectedDestinations) > 0 {
			// Occurrence-level evidence (migration v15): the merging batch
			// recorded the occurrence's selection — keep the change exactly
			// when that selection contains this lane, never re-evaluating
			// per path.
			for _, selected := range c.SelectedDestinations {
				if selected == intent.DestinationID {
					out = append(out, c)
					break
				}
			}
			continue
		}
		// Legacy fallback: no recorded selection — per-change evaluation.
		operation, opErr := records.ParseOperation(c.Operation)
		if opErr != nil {
			return nil, fmt.Errorf("dirty change %q operation %q: %v", c.Path, c.Operation, opErr)
		}
		selected, _, selErr := dispatch.SelectDestination(dispatch.SelectionContext{
			Paths:          []string{c.Path},
			Operations:     []records.Operation{operation},
			Classification: c.Classification,
			Disposition:    c.Disposition,
		}, conds, s.LanePathMatcher)
		if selErr != nil {
			return nil, fmt.Errorf("evaluating the destination %q lane conditions for %q: %v", intent.DestinationID, c.Path, selErr)
		}
		if selected {
			out = append(out, c)
		}
	}
	return out, nil
}

// applyCompletion builds the follow-up request when the route needs one
// and applies the atomic receipt-plus-completion transaction. The
// follow-up manifest carries the unresolved paths of the dirty
// generation (latest observation per path) so the chain stays bounded to
// outstanding work (M-10); a consecutive-follow-up chain past
// state.MaxConsecutiveFollowups schedules no follow-up and resolves the
// route through UNCERTAIN instead (E8-T1, H-1.1).
func (s *Service) applyCompletion(ctx context.Context, intent ports.IntentSnapshot, snap state.RouteSnapshot, w ports.WorkReceiptInput, base ports.ActiveCompletion, decision *AttributionDecision, dirty []ports.DirtyChange, remainingScope []records.ChangeItem) (Result, error) {
	base.FollowupRequest = nil
	base.PolicyRevision = s.PolicyRevision
	base.FollowupGeneration = intent.Generation + 1
	// A partial completion always schedules its same-lane follow-up for
	// the remaining scope (FBK-010): the store's decision point sees the
	// owed work through RemainingWork, and the manifest below unions the
	// remaining scope with the lane's unresolved dirty changes.
	storeNeedsFollowup := (snap.DirtyGeneration > 0 && !base.DirtySuppressed) || snap.PendingReconcile || base.RemainingWork
	overBudget := storeNeedsFollowup && base.FollowupGeneration > state.MaxConsecutiveFollowups
	// The store is the single decision point (it re-reads the fenced
	// snapshot): build the follow-up exactly when the store would take the
	// FOLLOWUP_READY edge. A failed completion with an exhausted failure
	// budget and any completion over the consecutive follow-up bound both
	// resolve through UNCERTAIN and schedule nothing (E8-T1 round-1 F002).
	failureExhausted := base.Failed && base.FailureBudgetRemaining <= 0
	if (storeNeedsFollowup || base.Failed) && !failureExhausted && !overBudget {
		followup, err := s.buildFollowup(intent, decision, dirty, remainingScope)
		if err != nil {
			return Result{}, fmt.Errorf("building the follow-up request: %w", err)
		}
		base.FollowupRequest = &followup
	}
	dirtyLineage, _ := json.Marshal(map[string]any{
		"route_id": intent.RouteID, "dirty_generation": snap.DirtyGeneration,
		"parent_dispatch_id": intent.DispatchID, "lineage_kind": "followup",
	})
	base.DirtyLineageJSON = string(dirtyLineage)
	base.Now = s.timestamp()
	created, err := s.Store.CompleteWork(ctx, w, base)
	if err != nil {
		return Result{}, err
	}
	return Result{
		ReceiptID: w.ReceiptID, DispatchID: w.DispatchID, RunID: w.RunID, Status: w.Status,
		RouteState: string(created.RouteTo), FollowupDispatchID: created.FollowupDispatchID,
		DirtyGeneration: created.DirtyGeneration, FailureBudgetLeft: base.FailureBudgetRemaining,
	}, nil
}

// buildFollowup derives the latest-state follow-up intent from the
// completed dispatch's stored request (CON-004: evidence, not
// snapshots). The manifest is the dirty generation's unresolved paths —
// a nil decision (failure or no attribution) keeps every observed path —
// each as its latest observation; with nothing unresolved the parent
// manifest remains the best available description (a pending
// reconciliation with no observed changes).
func (s *Service) buildFollowup(original ports.IntentSnapshot, decision *AttributionDecision, dirty []ports.DirtyChange, remainingScope []records.ChangeItem) (ports.IntentInput, error) {
	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(original.RequestJSON), &req); err != nil {
		return ports.IntentInput{}, fmt.Errorf("stored request is not the task contract shape: %w", err)
	}
	// The destination lane resolves through the shared DAT-013 precedence
	// inside BuildFollowupRequest: a legacy completion's follow-up
	// child-links under the live certified lane (persisting the referenced
	// destination-revision record) instead of wedging the receipt behind a
	// contract the parent predates.
	items := make([]records.ChangeItem, 0, len(req.Activation.Manifest))
	for _, m := range req.Activation.Manifest {
		op, err := records.ParseOperation(m.Operation)
		if err != nil {
			return ports.IntentInput{}, fmt.Errorf("stored manifest operation %q: %v", m.Operation, err)
		}
		items = append(items, records.ChangeItem{Path: m.Path, Operation: op, BeforeDigest: records.Digest(m.BeforeDigest), AfterDigest: records.Digest(m.AfterDigest)})
	}
	if unresolved := unresolvedManifest(decision, dirty); len(unresolved) > 0 {
		items = unresolved
	}
	if len(remainingScope) > 0 {
		// The partial outcome's follow-up manifest is the union of the
		// remaining scope and the lane's UNRESOLVED dirty changes
		// (FBK-010): the remaining scope entries lead, dirty changes only
		// add paths the scope does not already carry, and the
		// parent-manifest fallback never rides along — with nothing
		// unresolved the manifest is exactly the remaining scope (review
		// round 1, testing finding).
		items = unionScopes(remainingScope, unresolvedManifest(decision, dirty))
	}
	return dispatch.BuildFollowupRequest(original, items, req.Activation.Flags, s.DestinationResolver)
}

// unionScopes merges the remaining scope with the unresolved dirty
// projection (FBK-010): scope entries win their paths, dirty entries add
// only new paths, and the result keeps deterministic scope-then-dirty
// order.
func unionScopes(scope, unresolved []records.ChangeItem) []records.ChangeItem {
	seen := make(map[string]bool, len(scope))
	out := make([]records.ChangeItem, 0, len(scope)+len(unresolved))
	for _, item := range scope {
		seen[item.Path] = true
		out = append(out, item)
	}
	for _, item := range unresolved {
		if seen[item.Path] {
			continue
		}
		out = append(out, item)
	}
	return out
}

// unresolvedManifest projects the dirty generation's unresolved paths
// into the follow-up manifest (latest observation per path; the same
// collapse the matcher applies).
func unresolvedManifest(decision *AttributionDecision, dirty []ports.DirtyChange) []records.ChangeItem {
	if decision == nil {
		return dirtyManifest(dirty, nil)
	}
	unresolved := make(map[string]bool, len(decision.Unresolved))
	for _, d := range decision.Unresolved {
		unresolved[d.Path] = true
	}
	return dirtyManifest(dirty, unresolved)
}

// dirtyManifest collapses the dirty changes to the latest observation
// per path, keeping only the selected paths (nil keeps every path).
func dirtyManifest(dirty []ports.DirtyChange, keep map[string]bool) []records.ChangeItem {
	latest := map[string]ports.DirtyChange{}
	for _, c := range dirty {
		if keep != nil && !keep[c.Path] {
			continue
		}
		prev, ok := latest[c.Path]
		if !ok || c.ObservedAt >= prev.ObservedAt {
			latest[c.Path] = c
		}
	}
	out := make([]records.ChangeItem, 0, len(latest))
	for _, c := range latest {
		op, err := records.ParseOperation(c.Operation)
		if err != nil {
			continue
		}
		out = append(out, records.ChangeItem{
			Path: c.Path, Operation: op,
			BeforeDigest: records.Digest(c.BeforeDigest), AfterDigest: records.Digest(c.AfterDigest),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// validateLineage checks the dispatch/resource/task/run lineage rules
// (feedback-loop §4). It returns the intent and route snapshot when every
// check passes, and the rejection reasons otherwise. A non-nil error is a
// durable-store failure: it is never a rejection reason, because a
// transient read failure is not evidence against the receipt.
func (s *Service) validateLineage(ctx context.Context, dispatchID, runID, externalTaskID string) (ports.IntentSnapshot, state.RouteSnapshot, []string, error) {
	var reasons []string
	if len(dispatchID) == 0 || len(dispatchID) > MaxIDBytes {
		return ports.IntentSnapshot{}, state.RouteSnapshot{}, []string{"dispatch_id is required and bounded"}, nil
	}
	if len(runID) == 0 || len(runID) > MaxIDBytes {
		return ports.IntentSnapshot{}, state.RouteSnapshot{}, []string{"run_id is required and bounded"}, nil
	}
	intent, err := s.Store.LoadIntent(ctx, dispatchID)
	if err != nil {
		if errors.Is(err, ports.ErrIntentNotFound) {
			return ports.IntentSnapshot{}, state.RouteSnapshot{}, []string{fmt.Sprintf("dispatch %s does not exist", dispatchID)}, nil
		}
		return ports.IntentSnapshot{}, state.RouteSnapshot{}, nil, ports.WrapStore(err)
	}
	// The work-receipt surface admits the dispatch's LANE as active
	// (E12-T2, CON-007): the merged lane snapshot carries the route
	// envelope beside the lane's own slot and dirty generation.
	snap, err := s.Store.LoadIntentLane(ctx, dispatchID)
	if err != nil {
		return ports.IntentSnapshot{}, state.RouteSnapshot{}, nil, ports.WrapStore(err)
	}
	if !snap.State.IsActive() || snap.ActiveDispatchID != dispatchID {
		reasons = append(reasons, fmt.Sprintf("dispatch %s is not the active dispatch of its lane on route %s (state %s, active %q)", dispatchID, intent.RouteID, snap.State, snap.ActiveDispatchID))
	}
	if externalTaskID != "" && intent.ExternalRef == "" {
		reasons = append(reasons, fmt.Sprintf("dispatch %s has no accepted task reference, so task %q cannot be verified", dispatchID, externalTaskID))
	}
	if externalTaskID != "" && intent.ExternalRef != "" && externalTaskID != intent.ExternalRef {
		reasons = append(reasons, fmt.Sprintf("external task %q does not match the accepted task %q", externalTaskID, intent.ExternalRef))
	}
	if len(reasons) > 0 {
		return ports.IntentSnapshot{}, state.RouteSnapshot{}, reasons, nil
	}
	return intent, snap, nil, nil
}

// validateNewRun additionally rejects a run identifier that already
// recorded a receipt; it gates `work begin` (the terminal commands expect
// the begun row and update it atomically). A non-nil error is a
// durable-store failure, never rejection evidence.
func (s *Service) validateNewRun(ctx context.Context, dispatchID, runID string) ([]string, error) {
	if _, err := s.Store.LoadWorkReceipt(ctx, dispatchID, runID); err == nil {
		return []string{fmt.Sprintf("run %s is already recorded for dispatch %s", runID, dispatchID)}, nil
	} else if !errors.Is(err, ports.ErrWorkReceiptNotFound) {
		return nil, ports.WrapStore(err)
	}
	return nil, nil
}

// validateManifest validates the raw manifest document against the
// work-receipt rules: full-document receipts re-verify their identity
// fields, and every change entry carries a normalized contained relative
// path and well-formed digests (SEC-002, SEC-009).
func (s *Service) validateManifest(raw, dispatchID, runID, resourceID, externalRef string) ([]changeEntry, string, manifestDocument, []string) {
	if len(raw) > MaxManifestBytes {
		return nil, "", manifestDocument{}, []string{fmt.Sprintf("manifest exceeds %d bytes", MaxManifestBytes)}
	}
	trimmed := strings.TrimSpace(raw)
	// Exactly one JSON value, with exact key spellings: the decoder
	// stops at the first value and matches tags case-insensitively, so
	// schema equivalence needs an explicit trailing check and exact-key
	// comparison at both levels.
	dec := json.NewDecoder(strings.NewReader(raw))
	var top json.RawMessage
	if err := dec.Decode(&top); err != nil {
		return nil, "", manifestDocument{}, []string{fmt.Sprintf("manifest is not one JSON value: %v", err)}
	}
	if dec.More() {
		return nil, "", manifestDocument{}, []string{"manifest must carry exactly one JSON value"}
	}
	var entries []changeEntry
	var resultRevision string
	var doc manifestDocument
	var reasons []string
	if strings.HasPrefix(trimmed, "[") {
		var rawItems []map[string]json.RawMessage
		if err := json.Unmarshal(top, &rawItems); err != nil {
			return nil, "", manifestDocument{}, []string{fmt.Sprintf("manifest is not a change array: %v", err)}
		}
		for _, item := range rawItems {
			for k := range item {
				if !changeKeys[k] {
					reasons = append(reasons, fmt.Sprintf("change key %q is not part of the schema", k))
				}
			}
		}
		if len(reasons) == 0 {
			if err := json.Unmarshal(top, &entries); err != nil {
				return nil, "", manifestDocument{}, []string{fmt.Sprintf("manifest is not a change array: %v", err)}
			}
		}
	} else {
		var rawDoc map[string]json.RawMessage
		if err := json.Unmarshal(top, &rawDoc); err != nil {
			return nil, "", manifestDocument{}, []string{fmt.Sprintf("manifest is not a work-receipt document: %v", err)}
		}
		for k := range rawDoc {
			if !docKeys[k] {
				reasons = append(reasons, fmt.Sprintf("document key %q is not part of the schema", k))
			}
		}
		if len(reasons) > 0 {
			return nil, "", manifestDocument{}, reasons
		}
		var parsed receiptDoc
		if err := json.Unmarshal(top, &parsed); err != nil {
			return nil, "", manifestDocument{}, []string{fmt.Sprintf("manifest is not a work-receipt document: %v", err)}
		}
		doc.IsV2 = parsed.SchemaVersion != nil && *parsed.SchemaVersion == SchemaVersion
		if parsed.SchemaVersion != nil {
			doc.SchemaVersion = *parsed.SchemaVersion
		}
		if parsed.Status != nil {
			doc.Status = *parsed.Status
		}
		if parsed.ManualReason != nil {
			doc.ManualReason = *parsed.ManualReason
		}
		if parsed.CompletedScope != nil {
			doc.CompletedScope = *parsed.CompletedScope
		}
		if parsed.RemainingScope != nil {
			doc.RemainingScope = *parsed.RemainingScope
		}
		// The document version is the closed two-value set (E12-T3,
		// FBK-009): v2 names the four-outcome vocabulary and v1 documents
		// remain valid submissions — the stored history stays readable
		// (DAT-009 posture applies to major versions, and v2 is minor).
		if parsed.SchemaVersion == nil || (*parsed.SchemaVersion != SchemaVersion && *parsed.SchemaVersion != "agent-dispatch.work-receipt/v1") {
			reasons = append(reasons, "schema_version must be "+SchemaVersion+" or agent-dispatch.work-receipt/v1")
		}
		// The document form is schema-equivalent (docs/schemas/
		// work-receipt.schema.json): its required fields must be present,
		// with the values cross-checked against the invoked lineage.
		if parsed.DispatchID == nil {
			reasons = append(reasons, "dispatch_id is required in the document form")
		}
		if parsed.RunID == nil {
			reasons = append(reasons, "run_id is required in the document form")
		}
		if parsed.ResourceID == nil {
			reasons = append(reasons, "resource_id is required in the document form")
		}
		if parsed.Status == nil {
			reasons = append(reasons, "status is required in the document form")
		}
		if parsed.SubmittedAt == nil {
			reasons = append(reasons, "submitted_at is required in the document form")
		}
		if parsed.Changes == nil {
			reasons = append(reasons, "changes is required in the document form")
		}
		if parsed.DispatchID != nil && *parsed.DispatchID != dispatchID {
			reasons = append(reasons, fmt.Sprintf("dispatch_id %q does not match the invoked dispatch %q", *parsed.DispatchID, dispatchID))
		}
		if parsed.RunID != nil && *parsed.RunID != runID {
			reasons = append(reasons, fmt.Sprintf("run_id %q does not match the invoked run %q", *parsed.RunID, runID))
		}
		if parsed.ResourceID != nil && resourceID != "" && *parsed.ResourceID != resourceID {
			reasons = append(reasons, fmt.Sprintf("resource_id %q does not match the dispatch resource %q", *parsed.ResourceID, resourceID))
		}
		if parsed.ExternalTaskID != nil && *parsed.ExternalTaskID != "" && externalRef != "" && *parsed.ExternalTaskID != externalRef {
			reasons = append(reasons, fmt.Sprintf("external task %q does not match the accepted task %q", *parsed.ExternalTaskID, externalRef))
		}
		// A v2 document names its own outcome (E12-T3 review round 1):
		// completed, partially_completed, and blocked route through their
		// branches; a v1 document keeps the completed-only contract.
		// begun/failed never arrive here (failed belongs to `work fail`).
		if doc.IsV2 {
			if parsed.Status == nil {
				reasons = append(reasons, "status is required in the document form")
			} else if _, err := records.ParseWorkStatus(doc.Status); err != nil || doc.Status == string(records.WorkBegan) || doc.Status == string(records.WorkFailed) {
				reasons = append(reasons, fmt.Sprintf("a completion document must carry a work complete outcome (completed, partially_completed, blocked), got %q", doc.Status))
			}
		} else if doc.Status != string(records.WorkCompleted) {
			reasons = append(reasons, fmt.Sprintf("a completion manifest must carry status completed, got %q", doc.Status))
		}
		if parsed.SubmittedAt != nil {
			if _, err := time.Parse(time.RFC3339, *parsed.SubmittedAt); err != nil {
				reasons = append(reasons, "submitted_at is not an RFC 3339 timestamp")
			}
		}
		if parsed.FailureCode != nil && *parsed.FailureCode != "" {
			reasons = append(reasons, "a completion manifest must not carry a failure code")
		}
		if parsed.ResultRevision != nil {
			resultRevision = *parsed.ResultRevision
		}
		if parsed.Changes != nil {
			entries = *parsed.Changes
		}
	}
	if len(entries) > MaxChanges {
		reasons = append(reasons, fmt.Sprintf("manifest carries %d changes (limit %d)", len(entries), MaxChanges))
	}
	reasons = append(reasons, s.validateChangeEntries(entries)...)
	if doc.IsV2 {
		// The v2 outcome scopes validate under the same per-entry rules
		// as the manifest (paths, digests, bounds; E12-T3 review round 1).
		reasons = append(reasons, s.validateChangeEntries(doc.CompletedScope)...)
		reasons = append(reasons, s.validateChangeEntries(doc.RemainingScope)...)
	}
	if len(reasons) > 0 {
		return nil, "", manifestDocument{}, reasons
	}
	return entries, resultRevision, doc, nil
}

// validateChangeEntries applies the shared per-entry rules to one change
// set: canonical relative paths, uniqueness, containment, and digest
// shapes (the manifest and the v2 outcome scopes share them).
func (s *Service) validateChangeEntries(entries []changeEntry) []string {
	var reasons []string
	seen := map[string]bool{}
	for _, c := range entries {
		normalized, err := records.NormalizePath(c.Path)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("path %q is not a normalized relative path: %v", c.Path, err))
			continue
		}
		if normalized != c.Path {
			reasons = append(reasons, fmt.Sprintf("path %q is not in canonical form (%q)", c.Path, normalized))
		}
		if seen[normalized] {
			reasons = append(reasons, fmt.Sprintf("path %q appears more than once", normalized))
		}
		seen[normalized] = true
		if s.Resolver != nil {
			if _, err := s.Resolver.Resolve(normalized); err != nil {
				reasons = append(reasons, fmt.Sprintf("path %q fails containment: %v", normalized, err))
			}
		}
		for _, probe := range []struct {
			label  string
			digest *string
		}{{"before_digest", c.BeforeDigest}, {"after_digest", c.AfterDigest}} {
			if probe.digest == nil || *probe.digest == "" {
				continue
			}
			if _, err := records.ParseDigest(*probe.digest); err != nil {
				reasons = append(reasons, fmt.Sprintf("%s of %q: %v", probe.label, normalized, err))
			}
		}
	}
	if len(entries) > MaxChanges {
		reasons = append(reasons, fmt.Sprintf("change set carries %d entries (limit %d)", len(entries), MaxChanges))
	}
	return reasons
}

// AuditUnknownDispatch records the work-command rejection of an unknown
// dispatch in the same append-only audit history (E9-T2/L-7; round-1
// F002: one shape, owned here rather than the CLI layer).
func (s *Service) AuditUnknownDispatch(ctx context.Context, dispatchID string) {
	s.auditInvalid(ctx, dispatchID, "", &InvalidError{Reasons: []string{fmt.Sprintf("dispatch %s does not exist", dispatchID)}})
}

// auditInvalid records one rejected submission in the append-only audit
// history (FBK-003: invalid provenance is retained evidence, never a
// deletion). An audit failure never masks the validation error.
func (s *Service) auditInvalid(ctx context.Context, dispatchID, runID string, invalid *InvalidError) {
	// The append-only audit trail quotes untrusted submission members:
	// every reason is bounded before the write (review round 1, security
	// finding — the operator-facing error keeps its full text).
	bounded := make([]string, 0, len(invalid.Reasons))
	for _, reason := range invalid.Reasons {
		bounded = append(bounded, boundEcho(reason, 300))
	}
	reasons, _ := json.Marshal(bounded)
	now := s.timestamp()
	auditDoc, _ := json.Marshal(map[string]any{"run_id": runID, "reasons": json.RawMessage(string(reasons))})
	if s.Log != nil {
		s.Log.Warn(observability.EventWorkReceiptInvalid,
			observability.Correlation{TraceID: s.TraceID, DispatchID: dispatchID, RunID: runID},
			"work receipt rejected", map[string]any{"dispatch_id": dispatchID, "run_id": runID, "reasons": invalid.Reasons})
	}
	_ = s.Store.AuditWorkReceipt(ctx, "wr-"+dispatchID+"-"+runID+"-invalid-"+now+"-"+ids.RandomSuffix(),
		dispatchID, "", "invalid", now, string(auditDoc))
}

func (s *Service) receiptID(dispatchID string) string {
	return "rcpt-work-" + dispatchID + "-" + s.Now().UTC().Format("20060102T150405.000000000") + "-" + ids.RandomSuffix()
}

func (s *Service) timestamp() string {
	return ids.CanonicalTimestamp(s.Now())
}

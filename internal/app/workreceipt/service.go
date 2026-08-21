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
	"strings"
	"time"

	"github.com/rootkernel/jjukkumi/internal/adapters/localfs"
	"github.com/rootkernel/jjukkumi/internal/app/dispatch"
	"github.com/rootkernel/jjukkumi/internal/domain/ids"
	"github.com/rootkernel/jjukkumi/internal/domain/records"
	"github.com/rootkernel/jjukkumi/internal/domain/state"
	"github.com/rootkernel/jjukkumi/internal/ports"
)

// SchemaVersion is the receipt document version this service accepts
// (docs/schemas/work-receipt.schema.json).
const SchemaVersion = "jjukkumi.work-receipt/v1"

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
	// AuditWarning reports a failed post-commit attribution audit
	// append (the completion stands; the evidence gap is visible).
	AuditWarning error `json:"-"`
}

// Begin validates and records one begun run (FBK-005).
func (s *Service) Begin(ctx context.Context, in BeginInput) (Result, error) {
	intent, snap, reasons := s.validateLineage(ctx, in.DispatchID, in.RunID, in.ExternalTaskID)
	reasons = append(reasons, s.validateNewRun(ctx, in.DispatchID, in.RunID)...)
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
		return Result{}, err
	}
	return Result{ReceiptID: w.ReceiptID, DispatchID: in.DispatchID, RunID: in.RunID, Status: "begun", RouteState: string(snap.State)}, nil
}

// Complete validates one completion manifest, records the terminal
// receipt, and applies the completion transaction: when dirty work or a
// pending reconciliation remains, exactly one latest-state follow-up is
// scheduled atomically with the receipt update (CON-003).
func (s *Service) Complete(ctx context.Context, in CompleteInput) (Result, error) {
	intent, snap, reasons := s.validateLineage(ctx, in.DispatchID, in.RunID, "")
	if len(reasons) > 0 {
		s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: reasons})
		return Result{}, &InvalidError{Reasons: reasons}
	}
	changes, resultRevision, manifestReasons := s.validateManifest(in.ManifestJSON, in.DispatchID, in.RunID, intent.ResourceID, intent.ExternalRef)
	if len(manifestReasons) > 0 {
		s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: manifestReasons})
		return Result{}, &InvalidError{Reasons: manifestReasons}
	}
	if in.ResultRevision != "" {
		resultRevision = in.ResultRevision
	}
	// The begun receipt anchors the attribution window; a completion
	// without one is rejected by the atomic update too, but the matcher
	// needs its timestamp before any mutation.
	begun, err := s.Store.LoadWorkReceipt(ctx, in.DispatchID, in.RunID)
	if errors.Is(err, ports.ErrWorkReceiptNotFound) {
		s.auditInvalid(ctx, in.DispatchID, in.RunID, &InvalidError{Reasons: []string{fmt.Sprintf("run %s has no begun receipt for dispatch %s", in.RunID, in.DispatchID)}})
		return Result{}, &InvalidError{Reasons: []string{fmt.Sprintf("run %s has no begun receipt for dispatch %s", in.RunID, in.DispatchID)}}
	}
	if err != nil {
		return Result{}, err
	}
	changesJSON, _ := json.Marshal(changes)
	w := ports.WorkReceiptInput{
		ReceiptID:       s.receiptID(in.DispatchID),
		DispatchID:      in.DispatchID,
		RunID:           in.RunID,
		ResourceID:      intent.ResourceID,
		Status:          "completed",
		ResultRevision:  resultRevision,
		ChangesJSON:     string(changesJSON),
		SubmittedAt:     s.timestamp(),
		ValidationState: "valid",
	}
	// Exact self-change attribution (E5-T3): match the receipt against
	// the dirty generation; only a fully verified generation may clear
	// the route without a follow-up, and the decision is always audited.
	dirty, err := s.Store.LoadActiveGenerationChanges(ctx, intent.RouteID, in.DispatchID)
	if err != nil {
		return Result{}, err
	}
	decision := Match(ReceiptEvidence{
		ReceiptID: w.ReceiptID, DispatchID: in.DispatchID, RunID: in.RunID,
		ResourceID: intent.ResourceID, BegunAt: begun.BegunAt, CompletedAt: w.SubmittedAt,
		Changes: changes,
	}, dirty)
	_ = dirty // the matcher owns the vacuous-window refusal
	var auditErr error
	out, err := s.applyCompletion(ctx, intent, snap, w, ports.ActiveCompletion{
		RouteID: intent.RouteID, DispatchID: in.DispatchID, Failed: false,
		ReceiptRef: w.ReceiptID, Actor: "hermes-task", DirtySuppressed: decision.FullySuppressed,
		ExpectedDirtyGeneration: snap.DirtyGeneration,
	})
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
	intent, snap, lineageReasons := s.validateLineage(ctx, in.DispatchID, in.RunID, "")
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
		return Result{}, err
	}
	return s.applyCompletion(ctx, intent, snap, w, ports.ActiveCompletion{
		RouteID: intent.RouteID, DispatchID: in.DispatchID, Failed: true,
		FailureBudgetRemaining: budget, ReceiptRef: w.ReceiptID, Actor: "hermes-task",
	})
}

// applyCompletion builds the follow-up request when the route needs one
// and applies the atomic receipt-plus-completion transaction.
func (s *Service) applyCompletion(ctx context.Context, intent ports.IntentSnapshot, snap state.RouteSnapshot, w ports.WorkReceiptInput, base ports.ActiveCompletion) (Result, error) {
	base.FollowupRequest = nil
	// A failure with remaining budget always warrants its one
	// follow-up, even on a clean generation (the route would otherwise
	// sit in FOLLOWUP_READY with nothing following).
	if (snap.DirtyGeneration > 0 && !base.DirtySuppressed) || snap.PendingReconcile || base.Failed {
		followup, err := s.buildFollowup(intent)
		if err != nil {
			return Result{}, fmt.Errorf("building the follow-up request: %w", err)
		}
		base.FollowupRequest = &followup
	}
	base.DirtyLineageJSON = fmt.Sprintf(`{"route_id":%q,"dirty_generation":%d}`, intent.RouteID, snap.DirtyGeneration)
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
// completed dispatch's stored request (CON-004: evidence, not snapshots).
func (s *Service) buildFollowup(original ports.IntentSnapshot) (ports.IntentInput, error) {
	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(original.RequestJSON), &req); err != nil {
		return ports.IntentInput{}, fmt.Errorf("stored request is not the task contract shape: %w", err)
	}
	items := make([]records.ChangeItem, 0, len(req.Activation.Manifest))
	for _, m := range req.Activation.Manifest {
		op, err := records.ParseOperation(m.Operation)
		if err != nil {
			return ports.IntentInput{}, fmt.Errorf("stored manifest operation %q: %v", m.Operation, err)
		}
		items = append(items, records.ChangeItem{Path: m.Path, Operation: op, BeforeDigest: records.Digest(m.BeforeDigest), AfterDigest: records.Digest(m.AfterDigest)})
	}
	return dispatch.BuildFollowupRequest(original, items, req.Activation.Flags)
}

// validateLineage checks the dispatch/resource/task/run lineage rules
// (feedback-loop §4). It returns the intent and route snapshot when every
// check passes, and the rejection reasons otherwise.
func (s *Service) validateLineage(ctx context.Context, dispatchID, runID, externalTaskID string) (ports.IntentSnapshot, state.RouteSnapshot, []string) {
	var reasons []string
	if len(dispatchID) == 0 || len(dispatchID) > MaxIDBytes {
		return ports.IntentSnapshot{}, state.RouteSnapshot{}, []string{"dispatch_id is required and bounded"}
	}
	if len(runID) == 0 || len(runID) > MaxIDBytes {
		return ports.IntentSnapshot{}, state.RouteSnapshot{}, []string{"run_id is required and bounded"}
	}
	intent, err := s.Store.LoadIntent(ctx, dispatchID)
	if err != nil {
		if errors.Is(err, ports.ErrIntentNotFound) {
			return ports.IntentSnapshot{}, state.RouteSnapshot{}, []string{fmt.Sprintf("dispatch %s does not exist", dispatchID)}
		}
		return ports.IntentSnapshot{}, state.RouteSnapshot{}, []string{fmt.Sprintf("dispatch %s could not be read: %v", dispatchID, err)}
	}
	snap, err := s.Store.LoadRouteState(ctx, intent.RouteID)
	if err != nil {
		return ports.IntentSnapshot{}, state.RouteSnapshot{}, []string{fmt.Sprintf("route %s state could not be read: %v", intent.RouteID, err)}
	}
	if !snap.State.IsActive() || snap.ActiveDispatchID != dispatchID {
		reasons = append(reasons, fmt.Sprintf("dispatch %s is not the active dispatch of route %s (state %s, active %q)", dispatchID, intent.RouteID, snap.State, snap.ActiveDispatchID))
	}
	if externalTaskID != "" && intent.ExternalRef == "" {
		reasons = append(reasons, fmt.Sprintf("dispatch %s has no accepted task reference, so task %q cannot be verified", dispatchID, externalTaskID))
	}
	if externalTaskID != "" && intent.ExternalRef != "" && externalTaskID != intent.ExternalRef {
		reasons = append(reasons, fmt.Sprintf("external task %q does not match the accepted task %q", externalTaskID, intent.ExternalRef))
	}
	if len(reasons) > 0 {
		return ports.IntentSnapshot{}, state.RouteSnapshot{}, reasons
	}
	return intent, snap, nil
}

// validateNewRun additionally rejects a run identifier that already
// recorded a receipt; it gates `work begin` (the terminal commands expect
// the begun row and update it atomically).
func (s *Service) validateNewRun(ctx context.Context, dispatchID, runID string) []string {
	if _, err := s.Store.LoadWorkReceipt(ctx, dispatchID, runID); err == nil {
		return []string{fmt.Sprintf("run %s is already recorded for dispatch %s", runID, dispatchID)}
	} else if !errors.Is(err, ports.ErrWorkReceiptNotFound) {
		return []string{fmt.Sprintf("run %s could not be checked: %v", runID, err)}
	}
	return nil
}

// validateManifest validates the raw manifest document against the
// work-receipt rules: full-document receipts re-verify their identity
// fields, and every change entry carries a normalized contained relative
// path and well-formed digests (SEC-002, SEC-009).
func (s *Service) validateManifest(raw, dispatchID, runID, resourceID, externalRef string) ([]changeEntry, string, []string) {
	if len(raw) > MaxManifestBytes {
		return nil, "", []string{fmt.Sprintf("manifest exceeds %d bytes", MaxManifestBytes)}
	}
	trimmed := strings.TrimSpace(raw)
	var entries []changeEntry
	var resultRevision string
	var reasons []string
	if strings.HasPrefix(trimmed, "[") {
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&entries); err != nil {
			return nil, "", []string{fmt.Sprintf("manifest is not a change array: %v", err)}
		}
	} else {
		var doc receiptDoc
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&doc); err != nil {
			return nil, "", []string{fmt.Sprintf("manifest is not a work-receipt document: %v", err)}
		}
		if doc.SchemaVersion == nil || *doc.SchemaVersion != SchemaVersion {
			reasons = append(reasons, "schema_version must be "+SchemaVersion)
		}
		if doc.DispatchID != nil && *doc.DispatchID != dispatchID {
			reasons = append(reasons, fmt.Sprintf("dispatch_id %q does not match the invoked dispatch %q", *doc.DispatchID, dispatchID))
		}
		if doc.RunID != nil && *doc.RunID != runID {
			reasons = append(reasons, fmt.Sprintf("run_id %q does not match the invoked run %q", *doc.RunID, runID))
		}
		if doc.ResourceID != nil && resourceID != "" && *doc.ResourceID != resourceID {
			reasons = append(reasons, fmt.Sprintf("resource_id %q does not match the dispatch resource %q", *doc.ResourceID, resourceID))
		}
		if doc.ExternalTaskID != nil && *doc.ExternalTaskID != "" && externalRef != "" && *doc.ExternalTaskID != externalRef {
			reasons = append(reasons, fmt.Sprintf("external task %q does not match the accepted task %q", *doc.ExternalTaskID, externalRef))
		}
		if doc.Status != nil && *doc.Status != "completed" {
			reasons = append(reasons, fmt.Sprintf("a completion manifest must carry status completed, got %q", *doc.Status))
		}
		if doc.SubmittedAt != nil {
			if _, err := time.Parse(time.RFC3339, *doc.SubmittedAt); err != nil {
				reasons = append(reasons, "submitted_at is not an RFC 3339 timestamp")
			}
		}
		if doc.FailureCode != nil && *doc.FailureCode != "" {
			reasons = append(reasons, "a completion manifest must not carry a failure code")
		}
		if doc.ResultRevision != nil {
			resultRevision = *doc.ResultRevision
		}
		if doc.Changes != nil {
			entries = *doc.Changes
		}
	}
	if len(entries) > MaxChanges {
		reasons = append(reasons, fmt.Sprintf("manifest carries %d changes (limit %d)", len(entries), MaxChanges))
	}
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
	if len(reasons) > 0 {
		return nil, "", reasons
	}
	return entries, resultRevision, nil
}

// auditInvalid records one rejected submission in the append-only audit
// history (FBK-003: invalid provenance is retained evidence, never a
// deletion). An audit failure never masks the validation error.
func (s *Service) auditInvalid(ctx context.Context, dispatchID, runID string, invalid *InvalidError) {
	reasons, _ := json.Marshal(invalid.Reasons)
	now := s.timestamp()
	auditDoc, _ := json.Marshal(map[string]any{"run_id": runID, "reasons": json.RawMessage(string(reasons))})
	_ = s.Store.AuditWorkReceipt(ctx, "wr-"+dispatchID+"-"+runID+"-invalid-"+now+"-"+ids.RandomSuffix(),
		dispatchID, "", "invalid", now, string(auditDoc))
}

func (s *Service) receiptID(dispatchID string) string {
	return "rcpt-work-" + dispatchID + "-" + s.Now().UTC().Format("20060102T150405.000000000") + "-" + ids.RandomSuffix()
}

func (s *Service) timestamp() string {
	return s.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
}

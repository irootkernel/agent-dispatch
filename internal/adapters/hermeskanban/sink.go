package hermeskanban

import (
	"io/fs"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/observability"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Sink is the durable ports.Sink implementation for one configured
// Hermes Kanban target (E4-T3): it renders the immutable logical request
// through the E4-T2 renderer onto the E4-T1 typed transport and maps
// every outcome onto the sink-adapter contract classification.
//
// Construction is the gate: NewSink loads and validates the frozen
// capability report against the configured requirements, and the caller
// must complete Probe (version discovery and gating) before any
// submission so an unsupported Hermes version fails route validation
// before a task is submitted (HER-002/HER-005).
type Sink struct {
	adapter    *Adapter
	board      string
	renderOpts RenderOptions
	reportPath string
	targetID   string
	required   []string
	// Log and TraceID make mutex suppression operator-visible: when
	// rendering drops a configured mutex key the target cannot honor,
	// Submit emits one dispatch.mutex_suppressed warning instead of
	// failing silently (E9-T3, T3-F007). A nil Log keeps the sink quiet.
	Log     *observability.Logger
	TraceID string
}

// Compile-time contract check.
var _ ports.Sink = (*Sink)(nil)

// NewSink builds the sink for one target. The board slug is the operator
// created public board; the manifest bound is the route's
// batching.max_manifest_bytes enforced again at rendering.
func NewSink(targetID, executable, reportPath string, required []string, board string, limits ProcessLimits, maxManifestBytes int64) (*Sink, error) {
	if board == "" {
		return nil, fmt.Errorf("target %s: hermes-kanban requires the operator-created board slug", targetID)
	}
	if maxManifestBytes <= 0 {
		return nil, fmt.Errorf("target %s: the sink requires a positive manifest byte bound", targetID)
	}
	caps, err := loadValidatedCaps(targetID, reportPath, required)
	if err != nil {
		return nil, err
	}
	return &Sink{
		adapter: New(targetID, executable, reportPath, required, limits),
		board:   board,
		renderOpts: RenderOptions{
			MaxManifestBytes: maxManifestBytes,
			// resource_mutex is consulted before --mutex-key is ever sent
			// (E8-T3, M-6): a target that does not honor the flag never
			// receives it.
			ResourceMutexSupported: caps.ResourceMutex,
		},
		reportPath: reportPath,
		targetID:   targetID,
		required:   append([]string(nil), required...),
	}, nil
}

// ID implements ports.Sink.
func (s *Sink) ID() string { return s.adapter.ID() }

// Type implements ports.Sink; no fallback to another type exists
// (DUR-008).
func (s *Sink) Type() ports.SinkType { return ports.SinkHermesKanban }

// Probe implements ports.Sink: the read-only version gate and
// capability validation the caller must complete before submissions.
func (s *Sink) Probe(ctx context.Context) (ports.Capabilities, error) {
	return s.adapter.Probe(ctx)
}

// Submit implements ports.Sink. The submission is the public create with
// the request's idempotency key transmitted verbatim, so it is
// idempotent by the verified dedup behavior: a duplicate submission
// resolves to the original task and never creates a second one
// (E0-T4 §5, AC-302). Classification follows the frozen error model:
// only provable pre-invocation failures are definite_not_submitted;
// timeouts, excessive output, malformed output, and unrecognized
// failures after possible submission are unknown (DUR-005), and no other
// target is ever invoked (DUR-008).
func (s *Sink) Submit(ctx context.Context, req ports.TaskRequest) (ports.SubmitResult, error) {
	// The durability capabilities are re-read per submission so a
	// swapped report cannot diverge from the validated snapshot; a
	// missing durability capability is provably pre-invocation, so it
	// is a definite failure, never an ambiguous outcome (HER-005).
	caps, err := loadValidatedCaps(s.targetID, s.reportPath, s.required)
	if err != nil {
		return definiteNotSubmitted(err.Error()), nil
	}
	if !caps.DurableAcceptance || !caps.SubmitIdempotencyKey {
		return definiteNotSubmitted(fmt.Sprintf("target %s: durable submission requires durable_acceptance and submit_idempotency_key; use a documented reduced-guarantee ADR instead of emulating them (HER-005)", s.targetID)), nil
	}
	rendered, err := Render(req, s.renderOpts)
	if err != nil {
		// Rendering refusals are definite pre-submit failures: nothing
		// was sent and the request itself is re-plannable.
		var tooLarge *ManifestTooLargeError
		if errors.As(err, &tooLarge) {
			return definiteNotSubmitted("manifest bound exceeded: " + tooLarge.Error()), nil
		}
		return definiteNotSubmitted(err.Error()), nil
	}
	if rendered.SuppressedMutex {
		// The mutex key is configuration intent the target cannot honor:
		// the drop is warned, never silent, and the key value itself is
		// not logged (E9-T3, T3-F007).
		s.Log.Warn(observability.EventDispatchMutexSuppressed, observability.Correlation{
			TraceID: s.TraceID, DispatchID: req.DispatchID,
			RouteID: req.Route.ID, RouteRevision: req.Route.Revision,
			ResourceID: req.Resource.ID, TargetID: s.targetID,
		}, "configured mutex key dropped: the target capability report lacks resource_mutex, so the task is submitted without mutual exclusion", nil)
	}
	task, err := s.adapter.client.Create(ctx, s.board, rendered.CreateOptions)
	if err != nil {
		return s.classifyCreateFailure(err), nil
	}
	payload := acceptanceEvidenceOf(task)
	return ports.SubmitResult{
		Classification:    ports.SubmitAccepted,
		Durable:           ports.DurableTrue,
		ExternalRef:       task.ID,
		TargetObservedAt:  epochToTimestamp(task.CreatedAt),
		StructuredPayload: boundedPayload(payload),
	}, nil
}

// classifyCreateFailure maps the typed transport errors onto the
// contract classification. Definite failures are those provably before
// any write: the executable is missing, the argument array was rejected,
// or the board does not exist. Everything else may have reached Hermes,
// so it is unknown (DUR-005).
func (s *Sink) classifyCreateFailure(err error) ports.SubmitResult {
	var missing *ExecutableMissingError
	var rejected *ArgumentRejectedError
	var board *UnknownBoardError
	var pathErr *fs.PathError
	switch {
	case errors.As(err, &missing), errors.As(err, &rejected), errors.As(err, &board):
		return definiteNotSubmitted(err.Error())
	case errors.As(err, &pathErr) && pathErr.Op == "fork/exec":
		// The child never started (the argv-length family: macOS ARG_MAX,
		// Linux MAX_ARG_STRLEN): the failure is provably pre-invocation,
		// so it is definite not-submitted, never an unknown dead-letter
		// of provably unsubmitted work (E8-T2, M-5).
		return definiteNotSubmitted(err.Error())
	default:
		return ports.SubmitResult{
			Classification: ports.SubmitUnknown,
			Durable:        ports.DurableUnknown,
			Diagnostic:     truncate(err.Error(), diagnosticBound),
		}
	}
}

// LookupByIdempotencyKey implements ports.Sink. The public Hermes CLI
// has no read-only query by idempotency key (E0-T4 §5: the dedup create
// is the only key mechanism), so the read-only lookup is unsupported and
// is never emulated: key-based reconciliation runs through the
// idempotent dedup submission itself, where a returned original proves
// acceptance and a created task proves prior absence safely.
func (s *Sink) LookupByIdempotencyKey(ctx context.Context, key string) (ports.LookupResult, error) {
	return ports.LookupResult{}, fmt.Errorf("%w: the public Hermes CLI has no read-only query by idempotency key; reconcile by external reference or resubmit the same key (E0-T4 §5)", ports.ErrCapabilityUnsupported)
}

// LookupByExternalRef implements ports.Sink through the read-only public
// show: a typed record proves durable acceptance; the frozen exit-1
// `no such task` behavior is the deterministic absence proof (DUR-006);
// any transport failure proves nothing and reports ambiguous.
func (s *Sink) LookupByExternalRef(ctx context.Context, ref string) (ports.LookupResult, error) {
	caps, err := loadValidatedCaps(s.targetID, s.reportPath, s.required)
	if err != nil {
		return ports.LookupResult{}, err
	}
	if !caps.LookupByExternalRef {
		return ports.LookupResult{}, fmt.Errorf("%w: lookup by external reference", ports.ErrCapabilityUnsupported)
	}
	task, err := s.adapter.client.Show(ctx, s.board, ref)
	if err != nil {
		var absent *UnknownTaskError
		if errors.As(err, &absent) {
			return ports.LookupResult{Status: ports.LookupAbsent}, nil
		}
		return ports.LookupResult{}, err
	}
	return ports.LookupResult{
		Status:           ports.LookupFound,
		Acceptance:       records.AcceptanceAccepted,
		ExternalRef:      task.ID,
		FoundDurable:     true,
		TargetObservedAt: epochToTimestamp(task.CreatedAt),
	}, nil
}

// loadValidatedCaps reads the frozen report and validates it against
// the configured requirements; construction and every invocation share
// it so no cached snapshot can diverge from the report on disk.
func loadValidatedCaps(targetID, reportPath string, required []string) (ports.Capabilities, error) {
	report, err := LoadReport(reportPath)
	if err != nil {
		return ports.Capabilities{}, fmt.Errorf("target %s: %w", targetID, err)
	}
	caps := report.PortCapabilities()
	if err := ValidateRequired(targetID, caps, required); err != nil {
		return caps, fmt.Errorf("target %s: %w", targetID, err)
	}
	return caps, nil
}

// definiteNotSubmitted builds the provable pre-invocation failure.
func definiteNotSubmitted(diagnostic string) ports.SubmitResult {
	return ports.SubmitResult{
		Classification: ports.SubmitDefiniteNotSubmitted,
		Durable:        ports.DurableFalse,
		Diagnostic:     truncate(diagnostic, diagnosticBound),
	}
}

// epochToTimestamp renders the public created_at epoch as the canonical
// UTC RFC 3339 second-precision form.
func epochToTimestamp(epoch int64) string {
	if epoch <= 0 {
		return ""
	}
	return time.Unix(epoch, 0).UTC().Format(time.RFC3339)
}

// acceptanceEvidence is the bounded, always-valid-JSON acceptance
// projection persisted as structured evidence: identity, status, and
// assignment echoes only — never the task body, whose echoed size is
// unbounded (SEC-009; the full record stays in the target).
type acceptanceEvidence struct {
	ID            string   `json:"id"`
	Status        string   `json:"status"`
	CreatedAt     int64    `json:"created_at"`
	Assignee      *string  `json:"assignee"`
	MutexKey      *string  `json:"mutex_key"`
	Skills        []string `json:"skills"`
	WorkspaceKind *string  `json:"workspace_kind"`
}

// acceptanceEvidenceOf projects one accepted task record onto the
// bounded evidence document.
func acceptanceEvidenceOf(task TaskRecord) []byte {
	raw, _ := json.Marshal(acceptanceEvidence{
		ID:            task.ID,
		Status:        task.Status,
		CreatedAt:     task.CreatedAt,
		Assignee:      task.Assignee,
		MutexKey:      task.MutexKey,
		Skills:        task.Skills,
		WorkspaceKind: task.WorkspaceKind,
	})
	return raw
}

// boundedPayload keeps the recorded structured evidence bounded; a
// projection at the bound is re-marshaled from the typed record so the
// persisted evidence is never invalid JSON (no mid-document truncation).
func boundedPayload(p []byte) []byte {
	const max = 4096
	if len(p) <= max {
		return p
	}
	return []byte(`{"truncated":true,"bytes":` + fmt.Sprint(len(p)) + `}`)
}

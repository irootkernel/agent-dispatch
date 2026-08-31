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
	targetID   string
	// capabilityFingerprint, when non-empty, is the activation-bound
	// capability-evidence fingerprint (HER-018): Submit re-proves the
	// live fingerprint against it before any side effect, so an
	// executable or probe-contract change blocks submission (AC-703).
	capabilityFingerprint string
	// Log and TraceID make mutex suppression operator-visible: when
	// rendering drops a configured mutex key the target cannot honor,
	// Submit emits one dispatch.mutex_suppressed warning instead of
	// failing silently (E9-T3, T3-F007). A nil Log keeps the sink quiet.
	Log     *observability.Logger
	TraceID string
}

// BindCapabilityFingerprint records the activation-bound capability
// evidence fingerprint the submit path re-proves (HER-018). An empty
// fingerprint keeps the eligibility-only gate.
func (s *Sink) BindCapabilityFingerprint(fingerprint string) {
	s.capabilityFingerprint = fingerprint
}

// Compile-time contract check.
var _ ports.Sink = (*Sink)(nil)

// NewSink builds the sink for one target. The board slug is the operator
// created public board; the manifest bound is the route's
// batching.max_manifest_bytes enforced again at rendering. Since the
// v0.1.5 cutover the construction gate is the declared minimum-version
// eligibility (HER-011); the capability-shape probe that re-establishes
// per-target capability truth — including resource_mutex — arrives with
// E11-T2 (HER-012).
func NewSink(targetID, executable, minimumVersion string, board string, limits ProcessLimits, maxManifestBytes int64) (*Sink, error) {
	if board == "" {
		return nil, fmt.Errorf("target %s: hermes-kanban requires the operator-created board slug", targetID)
	}
	if maxManifestBytes <= 0 {
		return nil, fmt.Errorf("target %s: the sink requires a positive manifest byte bound", targetID)
	}
	adapter, err := New(targetID, executable, minimumVersion, limits)
	if err != nil {
		return nil, err
	}
	return &Sink{
		adapter: adapter,
		board:   board,
		renderOpts: RenderOptions{
			MaxManifestBytes: maxManifestBytes,
			// resource_mutex is consulted before --mutex-key is ever sent
			// (E8-T3, M-6): a target that does not honor the flag never
			// receives it. The default is the frozen 0.20.5 runtime-
			// verified interface; the CLI replaces it with the fresh
			// per-executable probe truth through SetResourceMutexSupported
			// (E11-T2) whenever the capability record is current.
			ResourceMutexSupported: true,
		},
		targetID: targetID,
	}, nil
}

// SetResourceMutexSupported overrides the resource-mutex posture from
// the fresh per-executable capability record: a probed create surface
// missing --mutex-key downgrades resource_mutex, so the renderer
// suppresses the key for a target that cannot honor it (E8-T3, M-6).
func (s *Sink) SetResourceMutexSupported(supported bool) {
	s.renderOpts.ResourceMutexSupported = supported
}

// liveFingerprint recomputes the capability-evidence fingerprint for
// the executable as it stands now: the digest and version halves of the
// record identity, which is exactly what an executable swap changes.
func (s *Sink) liveFingerprint(ctx context.Context) (string, error) {
	digest, err := ExecutableDigest(s.adapter.client.runner.executable)
	if err != nil {
		return "", err
	}
	version, err := s.adapter.client.DiscoverVersion(ctx)
	if err != nil {
		return "", err
	}
	return DeriveFingerprint(CapabilityRecordSchema, ProbeContractVersion,
		s.adapter.client.runner.executable, digest, version.String()), nil
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
	// Eligibility is re-proven per submission so an executable swapped
	// since construction cannot carry an earlier eligibility into a side
	// effect; a below-floor version is provably pre-invocation, so it is
	// a definite failure, never an ambiguous outcome (HER-011).
	if _, err := s.adapter.Probe(ctx); err != nil {
		return definiteNotSubmitted(err.Error()), nil
	}
	// The activation-bound capability fingerprint is re-proved against
	// the live executable identity (HER-018, AC-703): an executable,
	// version, or probe-contract change since the acknowledgement is
	// provably pre-invocation and blocks the submission with the
	// remediation named.
	if s.capabilityFingerprint != "" {
		live, err := s.liveFingerprint(ctx)
		if err != nil || live != s.capabilityFingerprint {
			detail := "the capability evidence changed since activation; re-run 'agent-dispatch hermes probe' and re-acknowledge the route"
			if err != nil {
				detail = fmt.Sprintf("re-proving the capability evidence failed: %v; %s", err, detail)
			}
			return definiteNotSubmitted(detail), nil
		}
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
// any transport failure proves nothing and reports ambiguous. The
// lookup_by_external_ref capability's interim truth source is the frozen
// 0.20.5 runtime-verified interface plus the eligibility probe (E11-T2's
// capability probe restores per-executable shape proof).
func (s *Sink) LookupByExternalRef(ctx context.Context, ref string) (ports.LookupResult, error) {
	if _, err := s.adapter.Probe(ctx); err != nil {
		return ports.LookupResult{}, err
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

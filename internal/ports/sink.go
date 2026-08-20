package ports

import (
	"context"
	"errors"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
)

// SinkType names one explicit public target interface.
type SinkType string

const (
	SinkHermesKanban  SinkType = "hermes_kanban"
	SinkHermesWebhook SinkType = "hermes_webhook"
	SinkFake          SinkType = "fake"
)

// Capabilities is the versioned target capability set tied to target
// version evidence (sink-adapter-contract.md §3).
type Capabilities struct {
	DurableAcceptance      bool
	SubmitIdempotencyKey   bool
	LookupByIdempotencyKey bool
	LookupByExternalRef    bool
	ResourceMutex          bool
	ExecutionStatus        bool
	Cancellation           bool
	ResultReceipt          bool
	MaximumRequestBytes    int64
}

// SubmitClassification is the adapter's error-classified submit outcome
// (sink-adapter-contract.md §4-§5). Only the adapter can distinguish
// definite pre-submit failure from an ambiguous outcome; when proof is
// unavailable the classification is unknown (DUR-005).
type SubmitClassification string

const (
	SubmitAccepted             SubmitClassification = "accepted"
	SubmitRejected             SubmitClassification = "rejected"
	SubmitDefiniteNotSubmitted SubmitClassification = "definite_not_submitted"
	SubmitUnknown              SubmitClassification = "unknown"
)

// DurableStatus is the tri-state durability of an acceptance.
type DurableStatus string

const (
	DurableTrue    DurableStatus = "true"
	DurableFalse   DurableStatus = "false"
	DurableUnknown DurableStatus = "unknown"
)

// SubmitResult reports what one submit invocation proved. Only
// Classification accepted with Durable true satisfies the durable
// acceptance requirement.
type SubmitResult struct {
	Classification   SubmitClassification
	Durable          DurableStatus
	ExternalRef      string
	TargetObservedAt string
	// StructuredPayload is the bounded structured response evidence.
	StructuredPayload []byte
	// Diagnostic is redacted free-form context.
	Diagnostic string
}

// LookupStatus is the lookup outcome axis.
type LookupStatus string

const (
	LookupFound     LookupStatus = "found"
	LookupAbsent    LookupStatus = "absent"
	LookupAmbiguous LookupStatus = "ambiguous"
)

// LookupResult reports what a target lookup proved about a previously
// submitted task.
type LookupResult struct {
	Status      LookupStatus
	Acceptance  records.AcceptanceState
	ExternalRef string
	// FoundDurable reports target-durable acceptance when Status is found.
	FoundDurable     bool
	TargetObservedAt string
}

// ExecutionProjection is the portable target execution status of one
// submitted task.
type ExecutionProjection struct {
	State            records.ExecutionState
	ExternalRef      string
	TargetObservedAt string
}

// ErrCapabilityUnsupported is returned by sink methods the target does
// not support; it is never emulated (sink-adapter-contract.md §2).
var ErrCapabilityUnsupported = errors.New("sink capability unsupported")

// TaskRequest is the immutable logical dispatch request
// (hermes-task-contract.md §2, schemas/hermes-task-request.schema.json).
// The core constructs it and owns the idempotency key; the adapter
// transmits both verbatim and never rewrites them.
type TaskRequest struct {
	ContractVersion    string              `json:"contract_version"`
	DispatchID         string              `json:"dispatch_id"`
	IdempotencyKey     string              `json:"idempotency_key"`
	Route              TaskRouteRef        `json:"route"`
	Resource           TaskResource        `json:"resource"`
	Assignment         *TaskAssignment     `json:"assignment,omitempty"`
	ExecutionHints     *TaskExecutionHints `json:"execution_hints,omitempty"`
	Activation         TaskActivation      `json:"activation"`
	AcceptanceCriteria []string            `json:"acceptance_criteria"`
}

// TaskRouteRef identifies the route revision the request was planned for.
type TaskRouteRef struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

// TaskResource identifies the resource and its workspace root.
type TaskResource struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
}

// TaskAssignment is the optional assignment block.
type TaskAssignment struct {
	Profile  string   `json:"profile"`
	Skills   []string `json:"skills"`
	MutexKey string   `json:"mutex_key,omitempty"`
}

// TaskExecutionHints bounds execution.
type TaskExecutionHints struct {
	MaxRuntimeSeconds int64 `json:"max_runtime_seconds,omitempty"`
	MaxAttempts       int64 `json:"max_attempts,omitempty"`
}

// TaskActivation is the latest-state activation evidence (ADR-0008).
type TaskActivation struct {
	Mode               string             `json:"mode"`
	Generation         int64              `json:"generation"`
	ContentFingerprint string             `json:"content_fingerprint"`
	Manifest           []TaskManifestItem `json:"manifest"`
	Flags              []string           `json:"flags"`
}

// TaskManifestItem is one relative-path activation manifest entry.
type TaskManifestItem struct {
	Path         string `json:"path"`
	Operation    string `json:"operation"`
	BeforeDigest string `json:"before_digest,omitempty"`
	AfterDigest  string `json:"after_digest,omitempty"`
}

// Sink maps an immutable logical dispatch request to one explicit public
// target interface and returns acceptance evidence. It does not decide
// whether a dispatch should exist.
type Sink interface {
	ID() string
	Type() SinkType
	Probe(ctx context.Context) (Capabilities, error)
	Submit(ctx context.Context, req TaskRequest) (SubmitResult, error)
	LookupByIdempotencyKey(ctx context.Context, key string) (LookupResult, error)
	LookupByExternalRef(ctx context.Context, ref string) (LookupResult, error)
	GetExecution(ctx context.Context, ref string) (ExecutionProjection, error)
}

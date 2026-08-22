// Package fakesink provides the fake Hermes sink required by TST-006: a
// scripted in-memory target that simulates accepted, rejected,
// timeout-before-accept, timeout-after-accept, malformed response,
// duplicate idempotency, unavailable lookup, and status progression
// without any external process. It is test infrastructure and never
// wired into production composition.
package fakesink

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Step scripts one Submit response. Exactly one field group applies:
// a delayed step sleeps (respecting the context deadline) before or
// after recording the write, producing the timeout windows TST-006
// requires; a malformed step returns an unknown classification with the
// raw unparseable payload.
type Step struct {
	Result ports.SubmitResult
	// Err is returned instead of a result (for example a context error
	// surfacing as an ambiguous outcome).
	Err error
	// DelayBeforeRecord sleeps before the write is recorded; a context
	// cancellation during it simulates timeout before possible acceptance.
	DelayBeforeRecord time.Duration
	// DelayAfterRecord sleeps after the write is recorded; a context
	// cancellation during it simulates timeout after possible acceptance.
	DelayAfterRecord time.Duration
	// Malformed marks the response payload as unparseable target output
	// (DUR-005: invalid response after possible submission is unknown).
	Malformed bool
	// Hook runs while the write is durably recorded but before Submit
	// returns, letting tests observe mid-flight durable state.
	Hook func(ctx context.Context, submitted Submitted)
}

// Submitted is one recorded target invocation.
type Submitted struct {
	Request ports.TaskRequest
	At      time.Time
}

// ExecutionStep scripts one GetExecution response for the status
// progression scenario.
type ExecutionStep struct {
	Projection ports.ExecutionProjection
	Err        error
}

// LookupStep scripts lookup behavior for one key or ref.
type LookupStep struct {
	Result ports.LookupResult
	// Err makes the lookup unavailable (typed target error).
	Err error
}

// Sink is the fake target. It is safe for concurrent use.
type Sink struct {
	ID_           string
	Capabilities_ ports.Capabilities

	mu          sync.Mutex
	steps       []Step
	pos         int
	submitted   []Submitted
	byKey       map[string]ports.SubmitResult
	lookupByKey map[string]LookupStep
	lookupByRef map[string]LookupStep
	execution   map[string][]ExecutionStep
	hook        func(ctx context.Context, submitted Submitted) error
}

// Compile-time contract check.
var _ ports.Sink = (*Sink)(nil)

// New creates a fake sink with durable-acceptance capabilities by
// default and one scripted response per Submit.
func New(id string, steps ...Step) *Sink {
	return &Sink{
		ID_: id,
		Capabilities_: ports.Capabilities{
			DurableAcceptance:      true,
			SubmitIdempotencyKey:   true,
			LookupByIdempotencyKey: true,
			LookupByExternalRef:    true,
			ExecutionStatus:        true,
			MaximumRequestBytes:    262144,
		},
		steps:       append([]Step(nil), steps...),
		byKey:       map[string]ports.SubmitResult{},
		lookupByKey: map[string]LookupStep{},
		lookupByRef: map[string]LookupStep{},
		execution:   map[string][]ExecutionStep{},
	}
}

// SetSubmitHook installs a hook invoked inside every submit after the
// write is recorded; a non-nil error aborts the submit with that error.
func (s *Sink) SetSubmitHook(hook func(ctx context.Context, submitted Submitted) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hook = hook
}

// ScriptLookupByIdempotency scripts the lookup result for one key.
func (s *Sink) ScriptLookupByIdempotency(key string, step LookupStep) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookupByKey[key] = step
}

// ScriptLookupByExternalRef scripts the lookup result for one ref.
func (s *Sink) ScriptLookupByExternalRef(ref string, step LookupStep) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookupByRef[ref] = step
}

// ScriptExecution scripts the status progression for one ref.
func (s *Sink) ScriptExecution(ref string, steps ...ExecutionStep) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.execution[ref] = append([]ExecutionStep(nil), steps...)
}

// Submissions returns every recorded invocation in order.
func (s *Sink) Submissions() []Submitted {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Submitted(nil), s.submitted...)
}

// ID implements ports.Sink.
func (s *Sink) ID() string { return s.ID_ }

// Type implements ports.Sink.
func (s *Sink) Type() ports.SinkType { return ports.SinkFake }

// Probe implements ports.Sink.
func (s *Sink) Probe(ctx context.Context) (ports.Capabilities, error) {
	return s.Capabilities_, nil
}

// Submit implements ports.Sink. Duplicate idempotency keys replay the
// first recorded result, mirroring target-side deduplication.
func (s *Sink) Submit(ctx context.Context, req ports.TaskRequest) (ports.SubmitResult, error) {
	s.mu.Lock()
	step, ok := s.nextStepLocked()
	if !ok {
		s.mu.Unlock()
		return ports.SubmitResult{}, fmt.Errorf("fakesink: no scripted step for submit %s", req.DispatchID)
	}
	if prior, seen := s.byKey[req.IdempotencyKey]; seen {
		record := Submitted{Request: req, At: time.Now()}
		s.submitted = append(s.submitted, record)
		hook := s.hook
		s.mu.Unlock()
		if hook != nil {
			if err := hook(ctx, record); err != nil {
				return ports.SubmitResult{}, err
			}
		}
		return prior, nil
	}
	s.mu.Unlock()

	if step.DelayBeforeRecord > 0 {
		if err := sleepCtx(ctx, step.DelayBeforeRecord); err != nil {
			return ports.SubmitResult{}, err
		}
	}

	s.mu.Lock()
	record := Submitted{Request: req, At: time.Now()}
	s.submitted = append(s.submitted, record)
	if req.IdempotencyKey != "" {
		if _, seen := s.byKey[req.IdempotencyKey]; !seen {
			s.byKey[req.IdempotencyKey] = step.Result
		}
	}
	hook := s.hook
	s.mu.Unlock()

	if step.Hook != nil {
		step.Hook(ctx, record)
	}
	if hook != nil {
		if err := hook(ctx, record); err != nil {
			return ports.SubmitResult{}, err
		}
	}

	if step.DelayAfterRecord > 0 {
		if err := sleepCtx(ctx, step.DelayAfterRecord); err != nil {
			return ports.SubmitResult{}, err
		}
	}
	if step.Malformed {
		return ports.SubmitResult{
			Classification:    ports.SubmitUnknown,
			Durable:           ports.DurableUnknown,
			StructuredPayload: []byte(`{"status": "ok", "task_i`),
			Diagnostic:        "malformed response payload after possible submission",
		}, nil
	}
	if step.Err != nil {
		return ports.SubmitResult{}, step.Err
	}
	return step.Result, nil
}

// LookupByIdempotencyKey implements ports.Sink.
func (s *Sink) LookupByIdempotencyKey(ctx context.Context, key string) (ports.LookupResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	step, ok := s.lookupByKey[key]
	if !ok {
		if _, submitted := s.byKey[key]; submitted {
			return ports.LookupResult{Status: ports.LookupFound}, nil
		}
		return ports.LookupResult{Status: ports.LookupAbsent}, nil
	}
	if step.Err != nil {
		return ports.LookupResult{}, step.Err
	}
	return step.Result, nil
}

// LookupByExternalRef implements ports.Sink.
func (s *Sink) LookupByExternalRef(ctx context.Context, ref string) (ports.LookupResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	step, ok := s.lookupByRef[ref]
	if !ok {
		return ports.LookupResult{}, fmt.Errorf("%w: lookup by external ref", ports.ErrCapabilityUnsupported)
	}
	if step.Err != nil {
		return ports.LookupResult{}, step.Err
	}
	return step.Result, nil
}

// GetExecution implements ports.Sink, draining the scripted status
// progression one step per call.
func (s *Sink) GetExecution(ctx context.Context, ref string) (ports.ExecutionProjection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	steps := s.execution[ref]
	if len(steps) == 0 {
		return ports.ExecutionProjection{}, fmt.Errorf("%w: execution status", ports.ErrCapabilityUnsupported)
	}
	step := steps[0]
	s.execution[ref] = steps[1:]
	if step.Err != nil {
		return ports.ExecutionProjection{}, step.Err
	}
	return step.Projection, nil
}

func (s *Sink) nextStepLocked() (Step, bool) {
	if s.pos >= len(s.steps) {
		return Step{}, false
	}
	step := s.steps[s.pos]
	s.pos++
	return step, true
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Accepted builds a durable accepted result.
func Accepted(externalRef string) ports.SubmitResult {
	return ports.SubmitResult{
		Classification: ports.SubmitAccepted,
		Durable:        ports.DurableTrue,
		ExternalRef:    externalRef,
	}
}

// Rejected builds a definite rejection.
func Rejected() ports.SubmitResult {
	return ports.SubmitResult{
		Classification: ports.SubmitRejected,
		Durable:        ports.DurableTrue,
	}
}

// NotSubmitted builds a definite pre-submit transport failure.
func NotSubmitted() ports.SubmitResult {
	return ports.SubmitResult{
		Classification: ports.SubmitDefiniteNotSubmitted,
		Durable:        ports.DurableFalse,
		Diagnostic:     "connection failed before request bytes were sent",
	}
}

// FoundAccepted builds a lookup result proving acceptance.
func FoundAccepted(externalRef string) ports.LookupResult {
	return ports.LookupResult{
		Status:       ports.LookupFound,
		Acceptance:   records.AcceptanceAccepted,
		ExternalRef:  externalRef,
		FoundDurable: true,
	}
}

// FoundRejected builds a lookup result proving non-acceptance.
func FoundRejected() ports.LookupResult {
	return ports.LookupResult{
		Status:     ports.LookupFound,
		Acceptance: records.AcceptanceRejected,
	}
}

# Sink Adapter Contract

## 1. Purpose

A sink adapter maps an immutable logical dispatch request to one explicit public target interface and returns evidence about acceptance. It does not decide whether a dispatch should exist.

## 2. Port

Conceptual Go interface:

```go
type Sink interface {
    ID() string
    Type() SinkType
    Probe(ctx context.Context) (Capabilities, error)
    Submit(ctx context.Context, req TaskRequest) (SubmitResult, error)
    LookupByIdempotencyKey(ctx context.Context, key string) (LookupResult, error)
    LookupByExternalRef(ctx context.Context, ref string) (LookupResult, error)
    GetExecution(ctx context.Context, ref string) (ExecutionProjection, error)
}
```

Unsupported methods return a typed `capability_unsupported` error and are not emulated.

## 3. Capabilities

```json
{
  "capabilities": {
    "durable_acceptance": true,
    "submit_idempotency_key": true,
    "lookup_by_idempotency_key": false,
    "lookup_by_external_ref": true,
    "resource_mutex": true,
    "execution_status": true,
    "cancellation": true,
    "result_receipt": true
  },
  "limits": {
    "maximum_request_bytes": 262144
  }
}
```

Capabilities are versioned and tied to target version evidence.

## 4. SubmitResult

```text
SubmitResult {
  classification        accepted | rejected | definite_not_submitted | unknown
  durable               true | false | unknown
  external_ref          optional
  target_observed_at    optional
  structured_payload    bounded
  diagnostic            redacted
}
```

Only `accepted` with `durable=true` satisfies the primary Kanban durable-acceptance requirement.

## 5. Error Classification

The adapter must distinguish:

- local validation before invocation;
- executable not found before invocation;
- definite connection failure before request bytes are sent, when provable;
- target rejection with structured evidence;
- ambiguous timeout or termination;
- malformed output after possible acceptance;
- capability mismatch.

When proof is unavailable, return unknown.

## 6. Process Invocation Rules

- no shell;
- fixed or verified executable;
- argv array;
- controlled environment;
- bounded stdin/stdout/stderr;
- deadline;
- process-group cleanup;
- no parsing of localized human text;
- exact adapter tests for each supported target version.

## 7. Idempotency

The core creates the key. The adapter transmits it through the target's verified public field or header. It must not rewrite the key.

If the target lacks idempotency submission but supports deterministic lookup before create, this may be documented as a reduced guarantee. A pre-check alone does not eliminate the post-create crash window.

## 8. Mutex

The route mutex is a capability request, not a string embedded into an untrusted prompt. If the target supports a machine mutex field, the adapter maps it. Local route serialization remains mandatory even when target mutex is absent.

## 9. Conformance Test Suite

Every sink adapter must pass tests for:

- accepted durable;
- accepted non-durable;
- definite rejection;
- definite pre-submit failure;
- timeout before known write;
- timeout after possible write;
- malformed output;
- duplicate idempotency key;
- lookup found, absent, ambiguous, and unsupported;
- excessive output;
- secret redaction;
- cancellation of timed-out child process.

## 10. v0.1.5 Notification Sink Contract

Notification sinks consume `notification-event/v1`, return a definite success,
definite refusal, ambiguous outcome, or retryable pre-delivery failure, and
never mutate dispatch state. Every attempt uses the stable notification
idempotency key. The initial implementations are the structured stderr log sink and
authenticated HTTPS webhook; channel-specific adapters must preserve this
contract and cannot become dispatch fallbacks.

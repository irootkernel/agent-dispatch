# Implementation Guide

## 1. Engineering Priorities

Implement in this order of concern:

1. correctness of authority boundaries;
2. no silent event loss;
3. no blind duplicate privileged work;
4. deterministic behavior and inspectability;
5. security and bounded resource use;
6. operational simplicity;
7. extensibility.

Do not optimize for adapter count or generalized plugin architecture before v0.1 acceptance.

## 2. Recommended Go Baseline

Use the Go standard library wherever practical. The following dependency roles are approved in principle, with exact modules and versions pinned in E1-T1:

| Role | Preferred approach |
|---|---|
| CLI command tree | A mature Go CLI library or a thin internal command router; do not duplicate business rules in handlers. |
| YAML | YAML v3 parser with duplicate-key detection. |
| SQLite | Pure-Go driver preferred for reproducible darwin/arm64 builds (the only supported platform, D-023); CGO requires an explicit ADR (hosted CI is not used, D-017). |
| Recursive glob | Library with deterministic `**` semantics across platforms. |
| UUIDv7 | Small pinned library behind an `IDGenerator` port. |
| JSON Schema | Pinned Draft 2020-12 validator for schemas and examples. |
| Logging | Standard `log/slog` with JSON handler. |
| Diff in tests | `go-cmp` or equivalent test-only dependency. |

No framework may become an authority boundary. Domain code must remain ordinary Go.

## 3. Dependency Direction

```text
cmd/cli -> application -> domain
             |             ^
             v             |
          ports/interfaces |
             ^             |
             |             |
       adapters/storage/fs/source/sink
```

Rules:

- `domain` imports only the standard library and small pure value dependencies if unavoidable.
- `application` imports domain and ports, not concrete adapters.
- adapters import ports/domain but do not call each other.
- CLI constructs dependencies and calls application services.
- storage DTOs do not leak into domain APIs.
- Hermes DTOs do not leak outside the Hermes adapter.

Add an architecture test or import-lint rule if useful.

## 4. Domain Modeling

Prefer explicit types:

```go
type RouteID string
type DispatchID string
type RelativePath string
type Digest string
type DispatchState string
```

Constructors validate invariants. Avoid passing raw strings for IDs, paths, states, and digests.

State transitions belong in methods or transition services that return a new state and typed transition event. Repositories persist validated transitions; they do not decide them.

## 5. Application Services

Recommended services:

```text
PlanWatchmanInput
PersistPlannedObservation
CreateDispatchIntent
AttemptDispatch
ReconcileUnknownDispatch
MergeDirtyGeneration
CompleteWorkReceipt
CreateFullReconciliation
ReleaseQuarantine
SetRouteActivation
PruneRetention
RunDoctor
```

Each service has one transaction boundary documented in code. Do not create a generic `ProcessEverything` service. `SetRouteActivation` implements `route enable`/`route disable`, including the acknowledged-revision gate, and lives in `application/maintenance`.

## 6. Transaction Pattern

Use explicit transaction functions:

```go
func (s *Service) PersistPlan(ctx context.Context, plan Plan) (Result, error) {
    return s.store.WithTx(ctx, func(tx ports.Tx) (Result, error) {
        // insert immutable evidence
        // validate current route state
        // create decision and intent or dirty generation
        // append transitions
    })
}
```

Never invoke Hermes, Watchman, Git, hash large files, or wait on network/process I/O inside the transaction.

After commit, attempt submission. A crash between commit and submit is recoverable. A crash after remote acceptance but before receipt is unknown and must be reconciled.

## 7. Time and Randomness

Inject:

- clock;
- ID generator;
- jitter source;
- filesystem abstraction for unit tests where justified;
- process runner;
- target adapter.

Production uses real implementations. Tests never depend on wall-clock sleeps for backoff or lease expiry.

## 8. Canonicalization

Do not canonicalize arbitrary structs. Build dedicated projection structs with only SOT fields. Sort changes explicitly. Validate UTF-8 and path normalization before projection.

Golden tests must include:

- reordered map input;
- reordered source file facts;
- different observation IDs and times;
- retry attempt changes;
- route revision changes;
- rerun generation changes;
- Unicode path cases.

## 9. Filesystem Safety

- Keep the trusted canonical root open or re-resolve it at controlled points.
- Normalize relative path first, then enforce containment.
- Avoid `filepath.Join(root, untrusted)` as the only security check.
- Re-check after symlink resolution immediately before opening.
- Open regular files only.
- Bound file size before and during read.
- Stream SHA-256; never load a large note entirely merely to hash.
- Handle deletion as metadata without file open.

Platform-specific secure-open code may be isolated behind an adapter and tested with race fixtures.

## 10. Watchman Parser

Use a bounded decoder. Reject multiple top-level JSON values unless the verified trigger contract requires streaming values. Preserve raw bytes only long enough to compute a digest.

Treat environment values as bounded input and parse booleans strictly. Never read the entire process environment into stored diagnostics.

## 11. External Process Runner

Build one reusable safe runner with:

```text
executable
argv[]
working directory
environment allowlist
stdin bytes or reader
stdout/stderr limits
deadline
process group termination
```

The runner returns process facts. The Hermes adapter classifies semantic outcomes. A generic command sink is not introduced.

## 12. Hermes Adapter

Implementation is gated by E0-T4. Keep all command names, flags, and version-specific parsing in the adapter.

Recommended layout:

```text
hermeskanban/
  adapter.go
  capabilities.go
  request_mapper.go
  response_vX.go
  errors.go
  fixtures/
```

Never infer acceptance from a localized phrase. If public output is not machine-readable, stop and record the blocker rather than implementing brittle regex logic.

## 13. CLI Design

CLI handlers:

1. parse flags;
2. load dependencies;
3. call one application service;
4. render human or JSON output;
5. map typed error to stable exit code.

Do not put SQL, path policy, state transitions, or Hermes parsing in command handlers.

## 14. Logging

Use structured attributes. Pass causal IDs through `context.Context` or an explicit operation context. Do not generate a new trace ID in every layer.

All errors are wrapped with context while preserving typed classification through `errors.Is`/`errors.As` or a project error type.

## 15. Concurrency

The product is process-concurrent but not internally parallel by default. Avoid unnecessary goroutines. SQLite constraints and leases coordinate processes.

When process I/O requires concurrent stdout/stderr draining, encapsulate it in the runner and prove bounded buffers and cancellation.

## 16. Defensive Limits

Centralize defaults and resolved limits. Enforce at ingress and again before target submission. Do not silently truncate evidence that affects correctness. When a limit is exceeded, produce a structural policy decision.

## 17. Review Checklist for Every Task

- Does this code assign semantic authority to Agent Dispatch?
- Can a payload modify target/profile/skills/workspace?
- Is an external side effect preceded by a committed intent?
- Is ambiguity represented as unknown?
- Can two processes duplicate the side effect?
- Are note bodies or secrets logged or stored?
- Is a later roadmap feature being introduced prematurely?
- Are tests deterministic and crash/concurrency relevant?

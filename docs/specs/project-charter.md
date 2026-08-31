# Project Charter

## 1. Product Identity

**Name:** Agent Dispatch  
**Tagline:** Sense. Catch. Route.  
**Category:** Local-first event-ingress and activation gateway  
**First authoritative runtime:** Hermes  
**First governed resource:** One Obsidian Markdown vault

## 2. Problem

Native watchers can report that files changed, and agent runtimes can execute work, but the boundary between them remains unsafe and operationally incomplete. A direct watcher-to-agent command does not adequately handle editor save noise, duplicate source delivery, restart recovery, target downtime, ambiguous acceptance, bulk changes, protected paths, concurrent work, recursive agent edits, or an auditable causal record.

Agent Dispatch supplies that missing boundary.

## 3. Vision

An operator can declare:

> When meaningful changes occur in this governed vault, create one effective, durable, reviewable work request per selected destination/workstream under a fixed route policy, with enough evidence to deduplicate, reconcile, retry, aggregate, notify, and audit the work safely.

For the first use case, the work request asks Hermes to evaluate the **latest state** of the Obsidian vault from an LLM Wiki perspective and update or review indexing, referencing, and grouping according to the configured Hermes skill and runtime permissions.

## 4. Responsibility Boundary

### Agent Dispatch owns

- source ingestion and source-specific validation;
- trusted resource and route resolution;
- deterministic path containment and filtering;
- content-change confirmation by metadata and optional hashing;
- structural classification such as protected, bulk, overflow, malformed, or stale;
- coalescing into a bounded change batch;
- immutable identity, fingerprint, and idempotency derivation;
- durable local spooling before side effects;
- submission retries and ambiguous-delivery reconciliation;
- destination-lane single-active-work coordination and aggregate event status;
- quarantine and operator-visible failure;
- delivery receipts, provenance receipts, durable notification attempts, retention, and audit records.

### Hermes owns

- agent profile resolution and runtime lifecycle;
- semantic interpretation of note contents;
- the LLM Wiki maintenance workflow;
- tool permissions and final authorization to edit;
- human approval inside Hermes, when configured;
- execution retries and model budgets;
- work success, failure, cancellation, and result semantics;
- the authoritative task history after durable acceptance.

### Agent Dispatch does not own

- note editing;
- semantic indexing or grouping algorithms;
- a general workflow engine;
- distributed orchestration;
- Hermes internals;
- an approval user interface;
- a Hermes plugin in v0.1.

## 5. Primary User Story

1. A user creates, modifies, moves, or deletes one or more Markdown notes in an Obsidian vault.
2. Watchman emits a settled trigger batch.
3. Agent Dispatch validates the source, resource, paths, and source position.
4. Agent Dispatch ignores non-meaningful changes, persists meaningful observations, and creates or extends one route generation.
5. For every selected destination whose lane has no unresolved task, Agent Dispatch durably creates one child dispatch intent and submits one Hermes Kanban task.
6. Hermes executes the configured LLM Wiki maintenance skill against the latest vault state.
7. If Hermes changes the vault, a bundled Agent Dispatch work-receipt CLI may record the run and changed paths without modifying Hermes core.
8. Changes observed while work is active are retained as a dirty generation.
9. When active work completes, Agent Dispatch creates at most one follow-up for that destination lane if the vault became dirty.
10. Aggregate status and configured notifications expose completion, failure, quarantine, drift, and manual intervention without changing task outcomes.
11. Unknown delivery or attribution never causes silent deletion or blind duplicate submission.

## 6. Goals

1. One meaningful burst of Markdown changes results in one effective Hermes maintenance request.
2. Work survives Agent Dispatch process termination, machine restart, and temporary Hermes unavailability.
3. Duplicate source delivery does not create duplicate accepted work when the Hermes interface supports idempotency or lookup.
4. Agent-generated changes do not recurse indefinitely.
5. Mixed human and agent changes are never incorrectly discarded as self-generated.
6. Protected, oversized, overflow, and structurally uncertain changes fail visibly and conservatively.
7. Operators can inspect why an event was ignored, merged, quarantined, dispatched, retried, or reconciled.
8. The core remains independent of Hermes implementation details through a sink port and public interface adapter.
9. One event can coordinate independent indexing, referencing, or grouping workstreams for different or repeated Hermes profiles.
10. Setup, preflight, status, and notifications make routine operation possible without direct SQLite or Watchman administration.

## 7. Non-Goals for v0.1

- Watching attachments, PDFs, images, Canvas files, or arbitrary binary files.
- Semantically deciding whether a note belongs in a topic or index.
- Requiring Git for basic operation.
- Reconstructing an immutable historical snapshot of note content.
- Multi-host coordination or a shared network database.
- A long-running Agent Dispatch daemon.
- Multi-vault production certification.
- Generic subprocess execution supplied by arbitrary configuration.
- Automatic failover between Hermes Kanban and webhook.
- A Hermes plugin or Hermes source-code change.
- An MCP server. It is a candidate for a later release.

## 8. Success Definition for v0.1

Agent Dispatch v0.1 is complete when all release acceptance cases pass on macOS (darwin/arm64, the only supported platform under the D-023 policy, E9-T8 — the earlier "and a supported Linux environment" clause is superseded, with the D-020 linux/arm64 verification standing as history), a real Obsidian vault can be wired to Watchman, Hermes Kanban receives one durable task per effective route generation, restart and ambiguity tests do not silently lose work, and feedback-loop tests demonstrate bounded follow-up behavior.

## 8.1 Success Definition for v0.1.5

v0.1.5 is complete only when a nested configured Wiki is scoped correctly,
reconciliation cannot erase newer facts, Hermes compatibility is
capability-probed without modifying Hermes, profiles and skills preflight, one
event can fan out to independent destination lanes, bounded receipts distinguish
completed/partial/blocked/failed work, and configured notifications are durable
and retryable. Gates G6 through G9 are cumulative and all closed: G6-G8
evidenced through E10-E12 and G9 evidenced with E13 on 2026-08-30.

## 9. Product Constraints

- Local-first operation.
- Go implementation.
- YAML operator configuration.
- SQLite local state on a local filesystem.
- Structured JSON at machine boundaries.
- No shell interpolation of event-derived data.
- No note body persisted by default.
- One globally active roadmap task during development.

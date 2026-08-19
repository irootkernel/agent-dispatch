# JJUKKUMI: Project Vision and Architecture Outline

> **Status:** Discussion draft v0.1
>  **Purpose:** Provide a concise but complete design brief for review by other AI agents and human collaborators.
>  **Tagline:** **Sense. Catch. Route.**

## 1. Executive Summary

**JJUKKUMI** is a local-first, general-purpose **event-to-agent bridge**. It observes events from sources such as Watchman, Git, webhooks, cron, process exits, and message queues; converts them into a canonical event envelope; applies filtering, batching, risk, and authorization policies; and dispatches durable requests to Hermes or other AI-agent runtimes.

The project is not intended to make semantic decisions or edit user content by itself. JJUKKUMI is the control-plane layer between low-level events and privileged, potentially expensive, nondeterministic agent work:

```
event source
  → normalize
  → settle and batch
  → classify and authorize
  → deduplicate
  → dispatch durably
  → observe execution
  → retain receipt

```

The name refers to the Korean small octopus: multiple arms represent independent source and sink adapters, suckers represent subscriptions, and the central body represents the shared policy and routing core.

## 2. Problem Statement

Existing tools usually solve only one segment of the path:

- filesystem watcher → shell command
- local file event → workflow node
- webhook → vendor-specific agent run
- MCP server → tools and resources for an already-running client
- agent CLI → one immediate execution

A reusable event-to-agent system must also handle noisy filesystem events, atomic saves, recrawls, retries, idempotency, feedback loops, prompt injection, cost limits, protected paths, concurrent writers, runtime downtime, execution receipts, and heterogeneous agent invocation contracts.

JJUKKUMI aims to provide this missing integration and governance layer without replacing native watchers, workflow engines, or agent runtimes.

## 3. Vision

Enable operators to express a durable rule such as:

> “When meaningful changes occur in this governed workspace, create one effective, reviewable unit of work for the designated agent, under the correct policy, with enough evidence to deduplicate, reconcile, retry, audit, or replay it safely.”

JJUKKUMI should eventually support this pattern for local knowledge bases, software repositories, data pipelines, content operations, monitoring systems, and multi-agent workspaces.

## 4. Primary Goals

1. **Unify heterogeneous event sources.** Convert source-specific payloads into one versioned event model.
2. **Route to heterogeneous agent targets.** Support Hermes Kanban, Hermes webhooks, generic HTTP, agent CLIs, and operator-defined commands through adapters.
3. **Prefer durable activation.** Preserve work across bridge, gateway, and agent restarts when the target supports durable tasks.
4. **Prevent duplicate or recursive execution.** Use event fingerprints, idempotency keys, mutexes, receipts, and transaction-aware feedback-loop controls.
5. **Keep sensing separate from reasoning.** The bridge detects, classifies, and routes; the receiving agent interprets content and performs domain work.
6. **Enforce least privilege.** Bind every route to explicit roots, event types, sinks, budgets, toolsets, and approval thresholds.
7. **Remain local-first and inspectable.** Use human-readable configuration, deterministic preprocessing, structured logs, and replayable immutable event IDs.
8. **Make failure visible.** Never silently drop, duplicate, or ambiguously fail over agent work.

## 5. Non-Goals

JJUKKUMI is not intended to:

- replace Watchman, FSEvents, inotify, or other native event sources;
- become a general business workflow engine such as Temporal, n8n, or Airflow;
- perform open-ended content interpretation inside the bridge process;
- promise true exactly-once execution across process and network boundaries;
- allow changed file content to redefine route instructions or permissions;
- directly edit governed documents, source trees, or canonical knowledge;
- silently switch between delivery sinks when execution status is ambiguous;
- store secrets, confidential payloads, or agent chain-of-thought in event records.

## 6. Representative Use Cases

### 6.1 Governed knowledge base maintenance

A Watchman trigger detects meaningful changes in a Markdown vault. JJUKKUMI batches atomic-save noise, checks path policy and Git state, then creates a serialized Hermes Kanban task for the designated Wiki maintainer.

### 6.2 Repository review and repair

A Git hook or CI webhook reports a changed branch or failed test. JJUKKUMI routes a bounded task to a coding agent with the correct repository workspace, skill, branch policy, and acceptance criteria.

### 6.3 Cross-agent notification

An external agent publishes an artifact or receipt. JJUKKUMI validates the event metadata and creates a follow-up request for another agent without treating the artifact body as trusted instructions.

### 6.4 Operational response

A process exits, a health probe changes state, or a queue emits an alert. JJUKKUMI applies rate limits and severity policy before creating an investigation task or sending a stateless notification.

## 7. Design Principles

- **Adapters at the edges, policy at the center.**
- **Structured data over prompt interpolation.**
- **At-least-once delivery plus idempotent consumption.**
- **Immutable event identity and replayable history.**
- **Explicit failure over silent best-effort behavior.**
- **Deterministic preprocessing before LLM invocation.**
- **No automatic trust transfer from source authentication to payload content.**
- **Dry-run before side effects; reconciliation after uncertainty.**
- **Capability discovery before claiming an integration is available.**

## 8. High-Level Architecture

```
Sources                         JJUKKUMI Core                       Sinks

Watchman ─────┐            ┌─ ingestion adapters              ┌─ Hermes Kanban CLI
Git hooks ────┤            ├─ canonical envelope              ├─ Hermes webhook
Webhooks ─────┤            ├─ settle/coalesce                 ├─ generic HTTP
Cron/timers ──┼───────────→├─ path and risk policy──────────→├─ agent CLI
Processes ────┤            ├─ fingerprint/dedup               ├─ command adapter
Queues ───────┤            ├─ retry/circuit breaker           └─ audit-only sink
stdin/files ──┘            └─ receipt and replay store

```

### 8.1 Source adapters

Initial source adapters should include Watchman JSON input and a generic webhook. Later adapters may include Git hooks, cron/timers, process supervision, queues, RSS/URL change monitors, and platform-specific event systems.

### 8.2 Core pipeline

Every event passes through:

1. schema validation;
2. source normalization;
3. settle window and coalescing;
4. ignore and containment rules;
5. optional content hash and Git-diff confirmation;
6. risk and bulk-change classification;
7. route selection and authorization;
8. event fingerprint generation;
9. idempotency and mutex assignment;
10. sink dispatch;
11. acknowledgement and execution receipt tracking;
12. replay, reconciliation, or dead-letter handling when required.

### 8.3 Sink adapters

Each sink adapter must declare capabilities such as durable tasks, idempotency, mutexes, retries, acknowledgement semantics, cancellation, result retrieval, and authentication requirements. Unsupported capabilities must be visible rather than emulated silently.

## 9. Canonical Event Envelope

```
{
  "spec_version": "1.0",
  "event_id": "sha256:<fingerprint>",
  "source": {
    "type": "watchman",
    "root": "/absolute/approved/root",
    "cursor": "<source-specific-position>"
  },
  "changes": [
    {
      "path": "relative/path.md",
      "operation": "create|modify|delete|rename",
      "before_sha256": null,
      "after_sha256": "<optional-hash>"
    }
  ],
  "observed_at": "2026-08-19T01:57:26+09:00",
  "risk": "normal|protected|bulk|unknown",
  "origin": "human|agent|system|unknown",
  "route_hint": null
}

```

The envelope carries observations, not authority. File content and external payloads remain untrusted data.

## 10. Hermes Integration Strategy

Hermes is the first supported agent runtime.

### Preferred durable path: Kanban

Use the Hermes Kanban CLI when work must survive restart, preserve task history, serialize a writer, or support review and retries. A dispatched task should include:

- assigned profile;
- `workspace=dir:<approved-root>`;
- required skills;
- content-derived idempotency key;
- resource-specific mutex key;
- bounded runtime and retry policy;
- acceptance criteria and requested receipt.

### Immediate path: webhook

Use Hermes webhooks for stateless notifications, manual control-plane requests, or explicitly immediate executions. Webhook delivery and Kanban creation must not act as automatic failover alternatives for the same event unless a shared deduplication contract is proven.

### Other runtimes

Generic adapters may call another agent CLI or authenticated HTTP endpoint. Subprocess adapters must pass structured event data over stdin and use an argv array; they must never construct shell commands by interpolating event or file content.

## 11. Delivery, State, and Failure Semantics

- Delivery guarantee: **at least once**.
- Duplicate defense: stable event fingerprints and sink-side idempotency.
- Concurrency defense: route- or resource-specific mutexes.
- Retry policy: bounded exponential backoff with a circuit breaker.
- Ambiguous result: mark `needs-reconciliation`; do not assume success or failure.
- Overflow or lost cursor: schedule one full reconciliation.
- Replay: retain the original event ID and do not duplicate accepted work.
- Audit trail: record actor, route, sink, timestamps, fingerprints, state transitions, and receipts-not hidden reasoning.

A lightweight local SQLite store may be introduced when durable spooling, replay, multi-source cursors, or sink downtime cannot be delegated to the target runtime.

## 12. Feedback-Loop Control

Agent outputs may modify the same tree that caused the run. JJUKKUMI should defend in layers:

1. coalesce atomic-save sequences;
2. compare observed events with hashes and Git diff;
3. serialize governed writers;
4. retain event fingerprints and execution receipts;
5. identify agent-produced commits or transactions;
6. hold events observed during a run;
7. drop exact self-generated results only after verification;
8. re-enqueue mixed human and agent changes;
9. request full reconciliation whenever attribution is uncertain.

Generated control files must not be ignored forever, because external corruption of those files still matters.

## 13. Security and Policy Model

Every route should declare:

- allowed roots and path containment rules;
- include and ignore patterns;
- protected and immutable paths;
- accepted event types and maximum batch size;
- destination agent, workspace, and allowed toolsets;
- token, cost, frequency, and runtime budgets;
- approval requirements for destructive or high-impact work;
- secret references resolved outside payloads;
- receipt retention and redaction policy.

JJUKKUMI must treat changed files, webhook bodies, URLs, commit messages, and agent artifacts as potentially hostile input. Operator-owned route instructions and payload data must remain separate.

## 14. Execution Modes

### Mode A: one-shot trigger bridge

The first implementation should be a one-shot CLI invoked by a Watchman trigger. Watchman already supplies daemon lifecycle, settle-aware triggers, incremental clocks, deletion records, trigger persistence, serialized trigger execution, and JSON input.

```
Watchman daemon → jjukkumi dispatch --route wiki → Hermes Kanban

```

### Mode B: managed daemon

Add `jjukkumi daemon` only when multiple concurrent sources, durable local spooling, global budgets, sink fan-out, a control API, or long-lived subscriptions justify another background process.

### Mode C: optional Hermes plugin

A future plugin may expose status, pause/resume, route management, receipts, and manual replay from Hermes. The plugin should remain a management surface; core sensing and routing should not depend on the lifecycle of a particular agent process.

## 15. Configuration Sketch

```
version: 1
routes:
  wiki-maintenance:
    source:
      type: watchman
      root: /Users/example/vault
      include: ["**/*.md"]
      exclude: [".git/**", ".obsidian/workspace*.json"]
    batching:
      settle_seconds: 8
      max_changes: 100
    policy:
      protected: ["raw/**", "canon/**"]
      bulk_threshold: 25
      approval_on: [protected, bulk, unknown]
    sink:
      type: hermes-kanban
      profile: wiki-maintainer
      workspace: "dir:/Users/example/vault"
      skills: [llm-wiki]
      mutex_key: wiki-publish
      max_runtime: 30m
      max_retries: 2

```

## 16. Proposed CLI Surface

```
jjukkumi init
jjukkumi validate [config|event]
jjukkumi source list|test
jjukkumi sink list|test
jjukkumi route list|show|test
jjukkumi dispatch --route <name> [--dry-run]
jjukkumi watch --route <name>
jjukkumi daemon start|status|pause|resume
jjukkumi receipts list|show
jjukkumi replay <event-id>
jjukkumi reconcile <route>
jjukkumi doctor

```

## 17. Delivery Plan

### Phase 0 - Design review

Validate product scope, event schema, route policy, adapter contract, state model, threat model, naming, and CLI ergonomics with independent reviewers.

### Phase 1 - Watchman dry-run prototype

- Read Watchman trigger JSON from stdin.
- Normalize and validate events.
- Coalesce changes and apply ignore rules.
- Produce event fingerprints and a dispatch plan.
- Perform no agent invocation.
- Test atomic saves, repeated saves, deletes, renames, bulk copies, and recrawls.

### Phase 2 - Hermes Kanban adapter

- Create durable tasks through the Hermes Kanban CLI.
- Set workspace, profile, skills, idempotency, mutex, runtime, and retries.
- Verify gateway downtime, duplicate dispatch, receipt retrieval, and bounded failure behavior.

### Phase 3 - Generic delivery adapters

- Add authenticated webhook and safe subprocess adapters.
- Define a sink capability matrix.
- Add dead-letter inspection, replay, and reconciliation.

### Phase 4 - Managed daemon and multi-source support

- Add local durable spool and source cursors if needed.
- Add Git, timer, process, and queue sources.
- Add global rate and cost budgets plus status API.

### Phase 5 - Management surfaces

- Optional Hermes plugin or local dashboard.
- Route editing, health, pause/resume, receipts, replay, and policy diagnostics.

## 18. Initial Acceptance Criteria

Before enabling automatic agent writes, the system should demonstrate:

1. one meaningful file change creates one task;
2. repeated editor saves coalesce into one task;
3. bulk changes form one bounded batch or require approval;
4. agent-produced edits do not recurse indefinitely;
5. restart causes reconciliation rather than silent loss;
6. target-runtime downtime does not lose or ambiguously duplicate work;
7. protected-path changes block without rewriting protected content;
8. concurrent batches preserve single-writer semantics;
9. retries stop at the configured limit;
10. mixed human and agent edits are detected and re-evaluated;
11. watcher overflow creates one full-reconciliation request;
12. replay preserves event identity and accepted work is not duplicated.

## 19. Open Design Questions

1. Should the initial release support only Watchman trigger mode, or include a daemon from the start?
2. What is the minimum canonical event schema that remains useful across files, Git, webhooks, and queues?
3. Which route policies belong in the generic core versus runtime-specific adapters?
4. Should SQLite be mandatory or introduced only when durable local spooling is needed?
5. How should agent-origin attribution be represented without trusting commit messages alone?
6. What receipt states are portable across Kanban, webhooks, and one-shot CLIs?
7. How should secrets be referenced and resolved without entering event records or logs?
8. What plugin interface should third-party source and sink adapters implement?
9. Which integrations are required for a credible v1.0 release?
10. Should the brand use the colloquial Korean spelling `쭈꾸미` while documenting the standard species spelling `주꾸미`?

## 20. Requested Review

Reviewers are asked to challenge, not merely summarize, this proposal. Please return:

- missing use cases or incorrect assumptions;
- architecture risks and unnecessary complexity;
- security, privacy, and prompt-injection gaps;
- failure modes involving duplication, loss, recursion, or concurrency;
- suggested event-envelope and adapter-contract changes;
- MVP scope reductions;
- competing tools or standards that should be adopted instead of rebuilt;
- a recommended sequence for prototype, validation, and release;
- an overall verdict: `proceed`, `proceed with changes`, or `do not proceed`.

## 21. Related Wiki Pages

- [multi-agent-artifact-governance](multi-agent-artifact-governance) - durable tasks, artifacts, handoffs, receipts, and promotion boundaries
- [llm-wiki-operating-strategy](llm-wiki-operating-strategy) - governance, approval, and maintenance rules for the first target workspace
- [knowledge-provenance](knowledge-provenance) - evidence, integrity, and trust boundaries
- [knowledge-lifecycle](knowledge-lifecycle) - controlled movement from temporary material to maintained knowledge

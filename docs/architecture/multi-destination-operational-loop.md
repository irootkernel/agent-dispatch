# v0.1.5 Multi-Destination Operational Loop

> **Status:** Approved target design; implementation is Planned in E10-E13.
> **Shipped baseline:** v0.1.4 remains current until gate G9 closes.

## 1. Outcome and Boundaries

v0.1.5 turns the existing durable single-task path into a complete operational
loop without making Agent Dispatch an agent runtime:

```text
configured resource
  -> effective Watchman binding
  -> observation and reconciliation fence
  -> aggregate event
  -> selected destination children
  -> independent Hermes acceptance and execution projections
  -> bounded worker receipts
  -> aggregate status and notification outbox
```

Hermes remains authoritative for task execution. Agent Dispatch does not modify
Hermes, read its private storage, install a plugin, interpret Wiki semantics, or
write governed documents. Paths, filenames, manifests, and worker artifacts
remain untrusted data.

## 2. Effective Watch Binding

One managed binding contains four identities:

| Field | Meaning |
|---|---|
| configured resource root | Trusted logical boundary used by policy and path containment |
| actual Watchman root | Root returned by `watch-project`, which may be an ancestor |
| effective relative root | Configured root relative to the actual Watchman root |
| trigger name | Stable Agent Dispatch-owned trigger identity |

Installation persists this binding and installs `relative_root` (or an
equivalent subtree expression). Status, test, and removal use the same resolver.
Removal checks the persisted root and every current Watchman root for the exact
managed trigger name and succeeds only when none remains. Include and exclude
patterns always evaluate relative to the configured resource root. Exclusion
precedes reads, hashing, batching, fan-out, task rendering, and notifications.

## 3. Reconciliation Fence

Every path-fact mutation advances a resource observation revision. Full
reconciliation captures revision `N` before enumeration and may replace the
snapshot only in a transaction that still observes `N`. A mismatch preserves
all newer facts, records a typed concurrent-change outcome, and leaves one due
reconciliation generation.

Hashing reads at most `max_hash_file_bytes + 1` (the configured hash bound, `limits.max_hash_file_bytes`). A stable over-bound file is
quarantined without hashing. A file whose identity or size changes during the
bounded read is retried once; a second instability is quarantined and requests
reconciliation. No conflict or unstable-file path deletes newer evidence.

## 4. Aggregate Event and Destination Lanes

An aggregate event records one normalized source/policy outcome. Each selected
destination creates an independent child dispatch. The serialization unit is
`(route_id, destination_id)`, not the whole route: one child may be running,
retrying, or blocked without preventing siblings from progressing.

Destination conditions are closed structural predicates over path, operation,
classification, and policy outcome. Values within one predicate are OR; present
predicate classes are AND. Absent conditions select the destination. Semantic
content is never evaluated. The MVP accepts only `fanout_mode: all`.

The child idempotency projection contains the contract version, route ID and
revision, source generation and content fingerprint, destination ID and
revision, workstream, and target scope. Retry preserves the child and key.
Behavior-affecting destination changes create a new destination revision and
cannot reuse an incompatible accepted child.

## 5. Hermes Capability and Preflight

Hermes versions below 0.19.1 are ineligible. Later versions have no fixed upper
bound but are not trusted by version alone. The probe executes only public CLI
commands with an allowlisted environment, closed stdin, bounded output, a
controlled working directory, and a deadline.

- Version and Kanban operations use their public output contracts.
- Profiles come from `hermes kanban assignees --json`; `on_disk` must be true.
- Enabled skills come from
  `hermes -p <profile> skills list --enabled-only` with color disabled and a
  fixed wide output. A strict parser accepts only the known complete table
  shape; truncation, duplicate names, unknown rows, or drift fail closed.

Capability evidence is keyed by executable absolute path and SHA-256, reported
version, and probe-contract version. Route activation binds both the computed
route revision and capability-evidence fingerprint. Executable or response
shape drift blocks submission until a fresh probe, preflight, and explicit
production acknowledgement succeed.

## 6. Lifecycle and Completion

Aggregate event, child dispatch acceptance, execution projection, and work
receipt remain separate records. Human status may summarize them but never
collapses them into one authoritative enum.

`work-receipt/v2` permits:

| Result | Consequence |
|---|---|
| `completed` | Child completes after exact bounded evidence validation |
| `partially_completed` | Current child closes; bounded remaining scope becomes one follow-up generation |
| `blocked` | Child enters manual-intervention-required; no automatic retry |
| `failed` | Existing failure budget decides retry or terminal failure |

Kanban acceptance is never completion. A terminal Hermes status without a
valid attributable receipt is completion-evidence-missing and requires
reconciliation or manual intervention according to policy.

## 7. Notification Outbox

A reportable state transition and its notification intent commit in the same
SQLite transaction. Delivery happens after commit and never changes dispatch
outcome. The deduplication projection contains event ID, optional destination
ID, transition, sink ID, and notification-policy revision.

The release ships structured stdout/log and authenticated HTTPS webhook sinks.
Webhook requests carry a stable notification idempotency key, never follow
redirects or ambient proxies, resolve secrets only at send time, and bound
payload, response, and execution time. Payloads contain identities, states,
reason codes, safe relative paths when policy permits, and digests—not document
contents or resolved credentials.

## 8. Setup and Rollback

`setup wiki` produces disabled configuration, verifies external capabilities,
installs and tests the Watchman binding, runs initial reconciliation, and stops
at a production-gate summary. Enabling still requires the exact computed route
revision and explicit confirmation.

The v0.1.5 config contract is a clean `version: 1` cutover to
`destinations[]`; legacy `dispatch` is rejected with regeneration guidance.
SQLite migration is forward-only and preserves historic records under a
synthetic legacy destination. Any unresolved legacy work blocks enablement.
Rollback preserves the upgraded database separately and restores the verified
pre-migration backup with the v0.1.4 binary and configuration.

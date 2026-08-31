# ADR-0022: Post-Commit Bounded Notification Draining

**Status:** Accepted
**Decision:** D-027
**Target:** v0.1.6

## Context

The v0.1.5 outbox is durable and state-independent, but ordinary operation
requires a separate remembered `notifications drain`. Pending operational
signals can therefore remain undelivered indefinitely.

## Decision

Add per-route `manual`, `after-command`, and `scheduled` drain modes.
After-command delivery starts only after a successful source-state transaction
commits and performs one ten-second, item-bounded, lease-protected pass. A
managed fifteen-minute launchd recovery schedule is a production prerequisite
for after-command mode. Automatic retry uses durable exponential backoff with
one persisted symmetric-jitter deadline. Manual drain also selects due work;
explicit retry alone makes selected work immediately due.
Claims carry fencing tokens and a stale owner cannot commit after ownership is
recovered. Delivery results remain notification evidence only and cannot alter
source state or its successful exit status. Scheduled mode uses an inspectable
managed launchd lifecycle with deterministic identity, a direct internal
runner, definition-drift refusal, and bounded rotation; no resident daemon is
introduced.

## Consequences

Watchman one-shot dispatch and later work-completion commands can advance
notifications normally. Crashes retain pending work and the recovery schedule
guarantees another opportunity without requiring a new source command.
Concurrent drainers claim disjoint records, fenced at-least-once retries keep
the stable notification identity, and all affected routes share one fair
ten-second budget per successful core command. Failed core commands defer to
the fallback. Scheduler and stalled-delivery posture become visible to status
and doctor without changing core stdout, JSON, or exit behavior.

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
commits and performs one bounded, lease-protected pass. Delivery results remain
notification evidence only and cannot alter source state or its successful
exit status. Scheduled mode uses an inspectable generated launchd recipe; no
resident daemon is introduced.

## Consequences

Watchman one-shot dispatch and later work-completion commands can advance
notifications normally. Crashes retain pending work, concurrent drainers claim
disjoint records, and at-least-once retries keep the stable notification
identity. Scheduler and stalled-delivery posture become visible to status and
doctor.

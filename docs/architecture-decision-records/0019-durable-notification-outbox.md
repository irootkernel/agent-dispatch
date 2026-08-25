# ADR-0019: Durable Notification Outbox

- **Status:** Accepted for v0.1.5
- **Date:** 2026-08-25
- **Extends:** ADR-0005 and ADR-0006

## Context

Operators need completion and failure visibility, but notification delivery is
neither Hermes task acceptance nor Wiki work completion. Coupling those states
would corrupt the underlying outcome when a webhook or local sink fails.

## Decision

Create a notification intent in the same transaction as each configured state
transition and deliver it after commit. Notification attempts and outcomes are
separate durable records. Retry is at-least-once with a stable idempotency key;
notification failure never changes event or child dispatch state.

## Consequences

Webhook and structured-log sinks share a channel-neutral contract. Duplicate
intent creation is prevented locally, ambiguous delivery remains visible, and
future channels can be added without changing dispatch state semantics.

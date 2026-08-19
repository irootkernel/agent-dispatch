# ADR-0010: Do Not Automatically Fail Over Between Kanban and Webhook

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

Kanban and webhook may have independent deduplication and acceptance semantics. If Kanban acceptance is ambiguous, webhook fallback can create duplicate work.

## Decision

A route selects one explicit target. Hermes webhook is not an automatic fallback for Hermes Kanban or vice versa.

## Consequences

- Unknown delivery remains visible and requires reconciliation.
- Webhook is suitable for explicit immediate routes only.
- Future failover requires a proven shared idempotency contract and a new ADR.

## Rejected Alternatives

- Automatic fallback on timeout: rejected as duplicate-prone.
- Best-effort broadcast to both: rejected as incompatible with one effective task goal.

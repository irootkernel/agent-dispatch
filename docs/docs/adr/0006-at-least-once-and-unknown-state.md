# ADR-0006: Use At-Least-Once Delivery and a First-Class Unknown State

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

Exactly-once execution cannot be proven across a local process and an external runtime. A timeout can occur after remote acceptance but before local receipt.

## Decision

JJUKKUMI provides at-least-once delivery. It uses stable idempotency keys when supported and records ambiguous outcomes as `unknown`. Unknown work is reconciled before retry.

## Consequences

- Duplicate defense depends partly on target public capabilities.
- Blind retry is prohibited after ambiguity.
- Acceptance and execution are modeled separately.
- Dead letter is preferable to silent guessing.

## Rejected Alternatives

- Exactly-once claim: rejected as false.
- Treat timeout as failure: rejected because it can duplicate accepted work.
- Never retry: rejected because definite pre-submit failures are recoverable.

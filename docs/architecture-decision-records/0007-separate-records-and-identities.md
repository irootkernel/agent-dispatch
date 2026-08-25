# ADR-0007: Separate Canonical Records and Identity Concepts

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

The discussion draft used one event ID derived from a fingerprint while also mixing source evidence, policy classification, origin, and route hints.

## Decision

Use separate immutable records for source observation, change batch, policy decision, dispatch intent, attempt, acceptance receipt, and work receipt. Use distinct observation IDs, fingerprints, idempotency keys, and attempt IDs.

## Consequences

- Causal history is explicit.
- Policy can evolve without rewriting source evidence.
- Retry and rerun semantics are distinguishable.
- More tables and contracts are required.

## Rejected Alternatives

- One envelope for the whole lifecycle: rejected because authority and identity semantics become ambiguous.
- Content hash as event ID: rejected because repeated equal content can be separate events.

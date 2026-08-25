# ADR-0016: Aggregate Events and Per-Destination Lanes

- **Status:** Accepted for v0.1.5
- **Date:** 2026-08-25
- **Supersedes:** ADR-0009 for v0.1.5 concurrency scope

## Context

ADR-0009 serializes one unresolved authoritative task per route. One source
event must now create independent workstreams for different or repeated Hermes
profiles without one child blocking every sibling.

## Decision

Persist one aggregate event and one child dispatch per selected destination.
The single-active-task and dirty-generation invariant applies independently to
each `(route_id, destination_id)` lane. Aggregate status is a projection over
children, not a replacement state machine. Destination identity and revision
join child idempotency and lineage.

## Consequences

Sibling submission, retry, completion, and failure are isolated. Repeated
changes collapse once per destination lane. Existing route-wide records remain
historically queryable after migration but cannot drive new v0.1.5 work.

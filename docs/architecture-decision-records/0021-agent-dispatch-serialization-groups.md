# ADR-0021: Agent Dispatch Serialization Groups

**Status:** Accepted
**Decision:** D-027
**Target:** v0.1.6

## Context

Hermes Agent v0.20.5 retains the durable delivery surfaces Agent Dispatch
needs but does not expose `--mutex-key`. Per-destination lanes prevent same-lane
parallelism, but do not serialize different destinations that govern the same
resource.

## Decision

Treat target mutex support as optional and enforce one active child per stable
Agent Dispatch serialization group in the shared SQLite state. Destination
lanes retain dirty generations and bounded follow-up semantics. Different
groups governing one resource require an explicit behavior-affecting route
acknowledgement before concurrency is permitted. Target-side mutexes complement
but do not replace the local slot.

## Consequences

Hermes v0.20.5 is compatible under a named local guarantee. Shared groups
serialize across destinations; independent acknowledged groups may progress
concurrently. The guarantee is state-database scoped and is never described as
cross-instance global single-writer safety.

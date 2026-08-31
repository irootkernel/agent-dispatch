# ADR-0021: Agent Dispatch Serialization Groups

**Status:** Accepted
**Decision:** D-027
**Target:** v0.1.6

## Context

Hermes Agent v0.20.5 is the minimum supported release. It retains the durable
delivery surfaces Agent Dispatch needs but does not expose `--mutex-key`.
Per-destination lanes prevent same-lane parallelism, but do not serialize
different destinations that govern the same resource.

## Decision

Enforce one active child per stable Agent Dispatch serialization group in the
shared SQLite state, regardless of target capability. Resolve the group from an
explicit `serialization_group`, a deprecated `mutex_key` alias, or a safe
`resource:<resource_id>` default. Explicit values use one bounded ASCII
grammar. Equal dual fields are accepted with a deprecation warning; conflicting
values fail validation. Destination lanes retain dirty generations and
oldest-waiter-first bounded follow-up semantics. Different groups governing one
resource require `allow_cross_group_concurrency: true` on every involved route
before concurrency is permitted. A probed target-side mutex complements but
never replaces the local slot.

## Consequences

Hermes 0.20.5 and probe-compatible later releases are supported under a named
local guarantee. Shared groups
serialize across destinations; independent acknowledged groups may progress
concurrently. The guarantee is state-database scoped and is never described as
cross-instance global single-writer safety. A migration-time collision among
preserved active children selects no arbitrary holder and blocks new group work
without rewriting history. Existing completion and safe recovery exits remain;
the sole survivor becomes holder, or the oldest dirty lane is promoted when no
active child remains.

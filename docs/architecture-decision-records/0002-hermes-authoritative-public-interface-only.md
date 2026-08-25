# ADR-0002: Hermes Is Authoritative and Only Public Interfaces May Be Used

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

Hermes is the first runtime and owns Kanban work and agent execution. Tight coupling to its internals would make Agent Dispatch fragile and would turn this project into a Hermes modification project.

## Decision

Hermes is authoritative. Agent Dispatch integrates only through verified public CLI or webhook interfaces. It does not modify Hermes core, access Hermes internal storage, or require a plugin in v0.1.

## Consequences

- E0-T4 must verify real public capabilities before adapter work.
- Missing durable acceptance or lookup can block the roadmap.
- The adapter contains all version-specific mapping.
- Agent Dispatch guarantees cannot exceed the public contract.

## Rejected Alternatives

- Direct Hermes database access: rejected for coupling and authority violations.
- Hermes source patch: rejected because it changes project ownership.
- Mandatory plugin: rejected because sensing and routing must survive Hermes lifecycle independently.

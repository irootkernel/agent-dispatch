# ADR-0004: Use Go, YAML, and SQLite

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

The project needs a portable local CLI, safe process execution, deterministic domain logic, concurrent short-lived processes, and an inspectable configuration and state store.

## Decision

Use Go for implementation, YAML for operator configuration, JSON for machine contracts, and SQLite for local durable state.

## Consequences

- Toolchain and dependencies are pinned.
- A pure-Go SQLite driver is preferred for portability.
- SQLite must remain on a local filesystem.
- Schema and config versions evolve independently.

## Rejected Alternatives

- Rust: viable but not selected for this project.
- Embedded key-value store: rejected because relational constraints and audit queries are important.
- JSON files as queue: rejected because crash-safe multi-process transitions would be difficult.

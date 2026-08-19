# ADR-0008: Use Latest-State Processing for Obsidian Maintenance

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

A Hermes task may start after more vault changes occur. Persisting full note snapshots would increase privacy, storage, and replay complexity.

## Decision

The change manifest is activation evidence. Hermes processes the current vault state at execution time. JJUKKUMI does not preserve full historical note content by default.

## Consequences

- A stale path does not force restoration of old content.
- Reconciliation aligns naturally with current state.
- Exact historical replay is not promised.
- Git commit/revision may enrich evidence when available.

## Rejected Alternatives

- Snapshot-bound notes for v0.1: rejected due to scope and privacy cost.
- Process each file event independently: rejected because Wiki maintenance is cross-document and stateful.

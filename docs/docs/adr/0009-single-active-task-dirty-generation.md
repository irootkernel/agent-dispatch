# ADR-0009: Allow One Active Maintenance Task and Collapse Later Changes

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

Obsidian save bursts and Hermes-generated edits can create many activations. Parallel Wiki maintainers can conflict and amplify costs.

## Decision

Each route has at most one unresolved Hermes maintenance task. Later meaningful changes are persisted as a dirty generation. Completion creates at most one follow-up latest-state task.

## Consequences

- Local route state is required even when Hermes offers mutexes.
- Work may be slightly delayed but remains bounded.
- Multiple bursts collapse into one follow-up.
- Stale active tasks require reconciliation rather than timeout-based failure.

## Rejected Alternatives

- Task per file: rejected for cost and cross-document inconsistency.
- Parallel tasks with only Git conflict handling: rejected because semantic and cost conflicts remain.

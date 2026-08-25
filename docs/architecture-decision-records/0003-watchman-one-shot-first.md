# ADR-0003: Use Watchman One-Shot Trigger Mode First

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

The first use case is local filesystem observation. Watchman already supplies a daemon, settled trigger batches, source positions, and trigger persistence.

## Decision

v0.1 uses a short-lived `agent-dispatch dispatch` process invoked by a Watchman trigger. Agent Dispatch does not implement a managed daemon.

## Consequences

- SQLite coordinates independent processes.
- No second settle sleep is added.
- Daily reconciliation uses external platform scheduling.
- Long-lived subscriptions are deferred.

## Rejected Alternatives

- Agent Dispatch daemon from the start: rejected as unnecessary operational and concurrency complexity.
- Native platform watcher implementation: rejected because it duplicates Watchman.

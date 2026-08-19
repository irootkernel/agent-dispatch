# ADR-0015: A Future Hermes Plugin Is Optional and Management-Only

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

An in-Hermes view of JJUKKUMI status, quarantine, and receipts could be convenient, but plugin lifecycle coupling must not affect sensing or dispatch correctness.

## Decision

A Hermes plugin is future work. If introduced, it may expose status, pause/resume, route inspection, receipts, quarantine, and explicit manual actions through JJUKKUMI public APIs. It must not host the core watcher, policy engine, SQLite authority, or dispatch correctness logic.

## Consequences

- v0.1 contains no plugin.
- Core behavior works while Hermes is stopped.
- The plugin requires authenticated JJUKKUMI management APIs or MCP and a new security review.
- Removing the plugin cannot lose source or dispatch state.

## Rejected Alternatives

- Plugin as the core process: rejected due to lifecycle and authority coupling.
- Plugin-specific state store: rejected because it would split authority.

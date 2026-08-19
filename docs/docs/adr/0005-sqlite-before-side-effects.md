# ADR-0005: Commit a SQLite Dispatch Intent Before External Side Effects

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

A process can terminate after receiving an event but before or during Hermes submission. Delegating all durability to Hermes leaves a loss window before target acceptance.

## Decision

SQLite is mandatory before the first target side effect. Observation, decision, and immutable dispatch intent commit before invoking Hermes.

## Consequences

- Crash before submit leaves recoverable ready work.
- Crash after possible remote acceptance still requires an unknown state and lookup.
- No external call occurs inside a database transaction.
- Migration and database integrity become release-critical.

## Rejected Alternatives

- Add SQLite later: rejected because the first real dispatch already has a loss window.
- Depend only on Watchman trigger retry: rejected because Watchman is not the authoritative task spool.

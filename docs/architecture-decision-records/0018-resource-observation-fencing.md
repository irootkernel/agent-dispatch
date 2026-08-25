# ADR-0018: Resource Observation Fencing

- **Status:** Accepted for v0.1.5
- **Date:** 2026-08-25
- **Extends:** ADR-0005 and ADR-0008

## Context

A full filesystem enumeration can overlap newer Watchman ingestion. Replacing
all path facts from the older enumeration would erase newer durable knowledge.
Files can also grow while reconciliation hashes them.

## Decision

Every resource owns a monotonic observation revision. Reconciliation captures
the revision before enumeration and replaces facts only through an atomic
compare-and-swap transaction. A mismatch preserves current facts and schedules
one new reconciliation. Hashing reads `max_file_bytes + 1` at most and checks
file stability before accepting the digest.

## Consequences

Reconciliation becomes retryable rather than destructive under concurrency.
Over-bound and repeatedly unstable files become explicit quarantine or
reconciliation evidence; no path can trigger an unbounded snapshot read.

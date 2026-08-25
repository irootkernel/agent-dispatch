# ADR-0013: Keep Policy Structural and Quarantine Unsafe Cases

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

Agent Dispatch must govern activation without becoming a semantic LLM layer. Protected, bulk, overflow, and malformed events still require conservative handling.

## Decision

Policy is deterministic and structural. It may drop, dispatch, merge pending, quarantine, or reconcile. Protected and unsafe paths default to quarantine; overflow and fresh instance default to reconciliation.

## Consequences

- No LLM is called inside Agent Dispatch.
- Operator release creates new decision lineage.
- Hermes owns semantic and execution approval.
- Large or uncertain input is never silently truncated into ordinary work.

## Rejected Alternatives

- LLM risk classifier: rejected for nondeterminism and prompt injection exposure.
- Automatically ignore protected changes: rejected because external corruption still matters.

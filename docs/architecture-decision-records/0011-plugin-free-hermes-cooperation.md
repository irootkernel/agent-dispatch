# ADR-0011: Use a Companion CLI and Skill for Hermes Work Receipts

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

Feedback-loop attribution benefits from run and changed-path receipts, but Hermes core and plugin development are out of scope.

## Decision

Agent Dispatch exposes `work begin`, `work complete`, and `work fail` CLI commands and ships an optional Hermes companion skill. Hermes agents may use them without a plugin.

## Consequences

- Provenance can be stronger when the skill is used.
- Missing receipts are handled conservatively.
- The skill cannot grant permissions or determine semantic success.
- An MCP interface may later expose the same port.

## Rejected Alternatives

- Trust commit messages: rejected as forgeable and incomplete.
- Ignore all events during a Hermes run: rejected because human edits would be lost.
- Build a plugin now: rejected by scope and lifecycle independence.

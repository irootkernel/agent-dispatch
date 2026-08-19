# ADR-0012: Certify v0.1 for Markdown in One Obsidian Vault

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

The broader draft included many sources, file types, and runtimes. The first business value is LLM Wiki maintenance for an Obsidian vault.

## Decision

v0.1 certification covers Markdown create, modify, and delete evidence in one vault, one route, and one primary Hermes Kanban target. Data structures remain named and extensible.

## Consequences

- Attachments and additional vaults are deferred.
- Rename pairing is optional; delete plus create is sufficient.
- The release can deeply test crash and feedback behavior.
- Multiple configured routes are not production-certified until future work.

## Rejected Alternatives

- General-purpose adapters in v0.1: rejected because they dilute correctness work.
- Binary attachment indexing: rejected because content extraction and privacy require separate design.

# ADR-0014: Git Is Optional Supporting Evidence

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

Git can provide revisions and diffs, but an Obsidian vault may not be a repository and commit messages cannot prove agent origin by themselves.

## Decision

Basic operation does not require Git. When available, Git revisions and diffs may enrich receipts and reconciliation. Git is never the sole provenance trust anchor.

## Consequences

- The product works for non-Git vaults.
- Exact suppression still requires validated path/digest receipts.
- Git adapter stays behind a port.
- Snapshot-bound replay remains out of scope.

## Rejected Alternatives

- Require Git: rejected because it imposes unrelated workflow policy.
- Ignore Git entirely: rejected because it is useful evidence when present.

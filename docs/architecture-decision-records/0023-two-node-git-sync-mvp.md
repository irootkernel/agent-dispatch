# ADR-0023: Two-node Git sync MVP

- **Status:** Accepted
- **Date:** 2026-09-17
- **Decision:** D-030

## Context

Agent Dispatch currently finishes Wiki maintenance on one machine. The source
request proposed a general N-member replication system and two later Plugin
epics. The immediate product need is narrower: keep one Markdown vault current
between a MacBook and one Oracle Cloud Linux node without weakening the local
maintenance and receipt boundaries.

## Decision

The v0.2.0 target supports one sync group, one governed Git working copy, one
configured content ref, and exactly two active nodes. Each node can publish,
receive a nudge, import, reconcile, and verify the pair. Git is the content
history authority. SQLite state, credentials, journals, locks, and local
Hermes or Obsidian configuration remain private to each node.

Publication is explicit. `sync publish` consumes an eligible, validated
maintenance snapshot, creates an SSH-signed commit, performs a fast-forward
push, confirms the remote outcome, and then records the peer nudge obligation.
No maintenance completion automatically publishes.

The peer nudge is a wake-up hint. The receiver resolves its configured remote,
refs, scope, and peer identity and obtains content from Git. Periodic
reconciliation remains mandatory so a lost nudge cannot prevent catch-up.

Only `.md` and `.markdown` content is admitted. The controller metadata subtree
is excluded from semantic maintenance. Attachments and additional nodes require
a later roadmap admission.

## Consequences

- The CLI and implementation may use list-shaped records where that avoids a
  later storage migration, but v0.2.0 must reject a third active member.
- The current Plugin remains an inspection-only product and is not changed by
  E20 through E22. Its accepted Agent Dispatch range is `>=0.1.6,<0.2.0`, so
  it must fail closed against v0.2.0 until a separate Plugin admission replaces
  that range. The obsolete Plugin handoff reference to Dispatch E26 grants no
  authority; Plugin work resumes only after a later explicit admission.
- The product does not claim Dropbox-style file synchronization, general
  distributed coordination, or refreshed memory in already-running Hermes
  sessions.

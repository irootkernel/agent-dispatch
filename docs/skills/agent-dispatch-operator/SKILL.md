---
name: agent-dispatch-operator
version: 1.0.0
author: Agent Dispatch
license: MIT
platforms: [macos]
metadata:
  hermes:
    tags: [Agent Dispatch, operator, setup, preflight, evidence]
---

# Agent Dispatch Operator Skill

Guide an operator through a safe Agent Dispatch setup and daily
operation. This skill changes neither Hermes core nor production
state: every mutating action stays an explicit operator command.

## When to use

- First-time setup of a vault-to-Hermes route (`setup wiki`).
- Pre-enablement verification (`route preflight`).
- Daily inspection and drift resolution (`status`, `doctor`).
- Evidence walkthroughs (`receipts`, `dispatches show`).

## Setup walkthrough

1. `agent-dispatch setup wiki` — the guided disabled flow. It writes a
   disabled configuration, probes Hermes (`hermes probe`), preflights
   the destination (`route preflight`), checks the Watchman binding,
   and runs the initial reconciliation. It stops before enablement and
   prints the exact production-gate command.
2. Review the written configuration and the printed revision.
3. Enable explicitly (never implicit):
   `agent-dispatch route enable --route <id> --acknowledge-production-gate <revision> --yes`.

## Daily operation

- `agent-dispatch status` — routes, queues, quarantine, targets, and
  the five drift classes (capability, profile, skill, watchman,
  reconciliation).
- `agent-dispatch doctor --probe-targets` — the findings examination.
- `agent-dispatch dispatches list --state unknown` then
  `dispatches drain` — delivery uncertainty resolution.
- `agent-dispatch receipts list --dispatch <id>` — acceptance and
  execution evidence.

## Safety rules

- Never enable a route without reading the computed revision the
  preflight and setup printed.
- Never resolve drift by editing SQLite directly; use the CLI exits.
- Hermes is only touched through its public CLI; nothing here writes
  Hermes private state.

## Compatibility

Agent Dispatch v0.1.5 with Hermes 0.19.1 or newer (compatibility is
probed; there is no maximum). Install the matching agent-dispatch
binary first; this skill assumes it on PATH.

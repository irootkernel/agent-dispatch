---
name: agent-dispatch-operator
version: 2.0.0
author: Agent Dispatch
license: MIT
platforms: [macos]
metadata:
  hermes:
    tags: [Agent Dispatch, operator, setup, preflight, evidence, notifications]
---

# Agent Dispatch Operator Skill

Guide an operator through a safe Agent Dispatch setup and daily
operation. This skill changes neither Hermes core nor production
state: every mutating action stays an explicit operator command.

## When to use

- First-time setup of a vault-to-Hermes route (`setup wiki`).
- Pre-enablement verification (`route preflight`).
- Daily inspection and drift resolution (`status`, `doctor`).
- Notification delivery operations (`notifications drain`, `list`,
  `retry`, `test`).
- Evidence walkthroughs (`receipts`, `dispatches show`, `events show`).

## Setup walkthrough

1. `agent-dispatch setup wiki` — the guided disabled flow. It writes a
   disabled configuration, probes Hermes (`hermes probe`), preflights
   the destination (`route preflight`), checks the Watchman binding,
   and establishes the initial baseline
   (`reconcile --reason initial --baseline-only` — route-scoped,
   disabled-only, and safely rerunnable, so a walkthrough interrupted
   at any non-production step simply reruns). With multiple routes
   pass `--route <id>` (or answer the interactive choice); a
   non-interactive multi-route run refuses instead of choosing. It
   stops before enablement and prints the five-state production-gate
   summary (configuration enabled, runtime activation, Watchman
   binding, initial baseline, production acknowledgement) followed by
   the exact enable command.
2. Review the written configuration and the printed revision. Declare
   the notification policy beside the destinations: the `notifications`
   block names each sink (a structured log sink, or an HTTPS webhook
   with its `secret_ref`); an omitted event list selects the default
   set (work completed, exhausted failure, unknown delivery, quarantine,
   reconciliation required, integration drift, Watchman drift).
3. Enable explicitly (never implicit):
   `agent-dispatch route enable --route <id> --acknowledge-production-gate <revision> --yes`.

## Daily operation

- `agent-dispatch status` — routes, queues, quarantine, targets, the
  five drift classes (capability, profile, skill, watchman,
  reconciliation), and the notification delivery counts; pending
  notifications carry their own warning.
- `agent-dispatch doctor --probe-targets` — the findings examination.
- `agent-dispatch dispatches list --state unknown` then
  `dispatches drain` — delivery uncertainty resolution.
- `agent-dispatch receipts list --dispatch <id>` and
  `events show <aggregate-id>` — acceptance, execution, and
  completion-evidence inspection.
- `agent-dispatch notifications drain` — deliver the pending
  notification intents: one bounded attempt each, oldest first, with
  the scheduled recipe chaining it after reconciliation.

## Notification operations

- Delivery outcomes are data, never failures: a refused, ambiguous, or
  retryable notification stays inspectable (`notifications list
  --state pending`) and never changes the dispatched work.
- `notifications test --route <id> --sink <id>` proves a sink's
  transport (HTTPS, authentication, idempotency header) without
  creating a notification, source event, or Hermes task.
- A refused notification that should have delivered is retried
  explicitly (`notifications retry <notification-id>`) after fixing
  the sink; the retry presents the same idempotency identity the
  endpoint saw before.
- Ambiguous deliveries retry on the next drain under the same stable
  identity — at-least-once, never silently dropped.

## Safety rules

- Never enable a route without reading the computed revision the
  preflight and setup printed.
- Never resolve drift by editing SQLite directly; use the CLI exits.
- Hermes is only touched through its public CLI; nothing here writes
  Hermes private state.
- Notification endpoints and credentials are trusted configuration:
  never resolve or paste a secret; sinks are selected by configuration,
  never by event or worker data.
- Disabling a route or removing the managed Watchman trigger preserves
  history: prefer `route disable` and `watchman remove` over deleting
  state.

## Compatibility

Agent Dispatch v0.1.5 (notifications, multi-destination fan-out, and
work-receipt/v2 included) with Hermes 0.20.5 or newer (compatibility is
probed; there is no maximum). Install the matching agent-dispatch
binary first; this skill assumes it on PATH.

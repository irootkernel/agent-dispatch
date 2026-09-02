---
name: agent-dispatch-operator
version: 2.1.0
author: Agent Dispatch
license: MIT
platforms: [macos]
metadata:
  hermes:
    tags: [Agent Dispatch, operator, setup, preflight, evidence, notifications, schedule]
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
- Managed drain schedule lifecycle (`schedule render`, `install`,
  `inspect`, `disable`, `uninstall`).
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
   reconciliation required, integration drift, Watchman drift). The
   optional `drain` block selects how pending notification work
   progresses — `manual` (the default when omitted), `after-command`,
   or `scheduled`; an automatic mode requires the managed schedule of
   the next step before the route may be enabled.
3. Enable explicitly (never implicit):
   `agent-dispatch route enable --route <id> --acknowledge-production-gate <revision> --yes`.
   For an automatic drain mode, install the managed schedule FIRST —
   preflight warns while it is missing (with the exact install command)
   and enablement refuses without it:
   `agent-dispatch schedule install --route <id> --platform launchd`
   (`schedule inspect` proves installed, loaded, and
   definition-matching; `schedule disable`/`uninstall` preserve
   configuration, state, and history).

## Daily operation

- `agent-dispatch status` — routes, queues, quarantine, targets, the
  five drift classes (capability, profile, skill, watchman,
  reconciliation), the notification delivery counts, and — for
  notification-enabled routes — the drain posture (mode and limit, the
  due/backoff split, oldest pending age, repeated retry outcomes,
  unresolvable sinks, and the scheduler expectation for automatic
  modes); pending notifications carry their own warning.
- `agent-dispatch doctor --probe-targets` — the findings examination
  (including the drain posture findings).
- `agent-dispatch dispatches list --state unknown` then
  `dispatches drain` — delivery uncertainty resolution.
- `agent-dispatch receipts list --dispatch <id>` and
  `events show <aggregate-id>` — acceptance, execution, and
  completion-evidence inspection.
- `agent-dispatch notifications drain` — deliver the DUE notification
  intents: one bounded lease-safe attempt each, oldest first. The
  explicit drain never evaluates drift; drift notifications ride the
  managed scheduled runner.

## Notification operations

- Delivery outcomes are data, never failures: a refused, ambiguous, or
  retryable notification stays inspectable (`notifications list
  --state pending`) and never changes the dispatched work.
- `notifications test --route <id> --sink <id>` proves a sink's
  transport (HTTPS, authentication, idempotency header) without
  creating a notification, source event, or Hermes task.
- A notification that should have delivered is retried explicitly
  (`notifications retry <notification-id>`) after fixing the sink: the
  retry is the sole operator bypass — it re-arms one ambiguous,
  retryable, or refused record to immediately-due pending and presents
  the same idempotency identity the endpoint saw before.
- Ambiguous and retryable deliveries retry on the next drain under the
  same stable identity with a persisted jittered backoff deadline —
  at-least-once, never silently dropped.

## Managed drain schedule

- `schedule render --route <id> --platform launchd` reviews the exact
  managed definition (label, plist, binary, digest) without touching
  launchd; `schedule install` writes and loads it idempotently,
  refusing a different definition at the same path.
- `after-command` recovery runs every fifteen minutes; `scheduled`
  mode reconciles first at 03:00 local by default (`--at HH:MM`
  overrides) and chains the drain only after a healthy pass.
- `schedule inspect` is the health check: `healthy` requires the plist
  present, loaded, and byte-identical to the rendered definition. A
  drifted schedule (moved binary, retargeted config) is unhealthy even
  while loaded — uninstall and reinstall to converge.
- Logs rotate at 10 MiB with three files retained under the state
  directory; `disable`/`uninstall` never touch configuration, SQLite
  state, or notification history.

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

Agent Dispatch v0.1.6 or newer (automatic notification draining and the
managed launchd schedule included; a v0.1.5 binary lacks the `schedule`
surface and the automatic drain modes) with Hermes 0.20.5 or newer
(compatibility is probed; there is no maximum). Install the matching
agent-dispatch binary first; this skill assumes it on PATH.

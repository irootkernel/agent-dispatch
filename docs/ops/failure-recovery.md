# Failure and Recovery Guide

For the operator of a local macOS arm64 installation. Before intervention,
identify the binary version, configuration, route, and affected causal IDs.
Inspect `status`, `doctor`, and the relevant dispatch or notification first.
The host owner authorizes changes; maintainers own unexplained defects.
Keep the same explicit `--config` path for diagnosis and resolution.

| Symptom | Likely state | Safe action | Forbidden action |
|---|---|---|---|
| Watchman command exits nonzero after a change | input rejected, storage failure, target problem, or unknown | inspect logs and `status`; use causal ID | manually fire webhook as fallback |
| Hermes CLI timed out | `unknown` if request may have been sent | lookup by idempotency key/task ID | blind retry |
| Hermes definitely unavailable before invocation | `retry_wait` or command error | allow bounded retry or drain later | change idempotency key |
| Duplicate Hermes task appears | target capability/adapter defect | block route, preserve both refs, investigate | delete local lineage to hide it |
| Database busy | competing one-shot process | bounded wait/retry; inspect stale lease | disable locking/constraints |
| Database integrity fails | corruption | stop submission, restore verified backup, preserve corrupt copy | create a new empty DB silently |
| Watchman overflow | pending reconciliation | run/allow one full reconciliation | dispatch partial list |
| Fresh Watchman instance | pending reconciliation | establish current-state baseline | treat all files as independent tasks |
| Route remains active too long | stale/uncertain execution | public Hermes lookup or operator resolution | auto-mark failed by timeout alone |
| Agent edits trigger more events | active dirty generation | retain and wait for receipt/completion | ignore all events during run |
| Mixed human and agent edits | dirty unresolved | one follow-up latest-state task | suppress whole batch |
| Work receipt digest mismatch | invalid/incomplete receipt | retain dirty state and audit | trust task identity alone |
| Protected path changed | quarantine | operator review/release or discard | include in normal automatic task |
| Config changed with ready intent | superseded/reprocess | revalidate and create new decision lineage | mutate immutable request |
| Capability evidence mismatch | stale or changed executable capabilities | run `agent-dispatch hermes probe`, then route preflight | hand-edit the probe cache or bypass production checks |
| Secret resolution fails | no side effect | correct reference and retry | log secret value for debugging |

## Recovery Principles

1. Preserve evidence before intervention.
2. Do not convert uncertainty into failure for convenience.
3. Do not create a new idempotency key unless the operator intends a new work request.
4. Reconciliation observes authoritative current state; it does not rewrite history.
5. Database repair never uses Hermes internal storage as a substitute source of truth.
6. A redundant bounded follow-up is safer than silent loss, but parallel unbounded tasks are not.

## Multi-Destination Recovery

- **Watch binding drift:** disable the route, inspect configured/actual roots,
  replace only after review, test, then re-enable with the current revision.
- **Reconciliation conflict:** keep the newer facts and drain the single due
  reconciliation; never force snapshot replacement.
- **Capability/profile/skill drift:** run `hermes probe` (the probe
  always re-probes; `--refresh` belongs to `hermes capabilities`) and
  `route preflight`; explicit route re-acknowledgement is required.
- **Partial work:** inspect completed/remaining scope and allow only the bounded
  lane follow-up. **Blocked work:** resolve manually; do not retry blindly.
- **Notification failure:** retry the notification ID only. Do not rerun a
  successful child task to obtain another notification.

## Absolute Watch-Root Binding

Current E18 source binds the configured absolute resource root itself. Inspect
`agent-dispatch watchman status --route <id>` before installation or replacement.
If an ancestor watch prevents an exact-root watch, keep the route disabled and
inspect the shared watch topology with the host owner. Follow the command's
unwatch guidance only after checking other tools that use that root; Agent
Dispatch does not automatically remove shared watch roots.

After the owner resolves the topology, reinstall the managed trigger (use
`--replace` only for a reviewed differing definition), inspect the exact-root
binding, and recheck preflight. Re-acknowledge the current revision when required.
`watchman test` normalizes fixture input without contacting the server; verify
actual event delivery using an authorized disposable edit and its task lineage.
If repair fails, leave submissions disabled and preserve the old binding facts
for diagnosis rather than falling back to an ancestor binding.

## Verify and Escalate

After recovery, re-run `doctor` and inspect the affected dispatch or notification.
Confirm the expected target task, receipt, or pending state by causal ID; do not
interpret notification success as worker completion. Re-enable automatic work
only after the relevant findings and ambiguity are resolved.

If integrity checks fail, stop commands and schedules and follow the
[backup restoration procedure](installation.md#6-backup). Preserve both the
failed database and the matching backup. Escalate unresolved delivery uncertainty,
duplicate tasks, or contract defects to repository maintainers with redacted
version/configuration metadata and causal IDs. The operator handles local
permissions and Watchman/launchd ownership; the Hermes owner handles profiles,
skills, and board access. Do not share credentials, note bodies, or raw secrets.

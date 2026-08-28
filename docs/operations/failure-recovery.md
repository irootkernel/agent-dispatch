# Failure and Recovery Guide

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
| Capability report version mismatch | invalid target | rerun E0-T4-style probe/update adapter | bypass probe in production trigger |
| Secret resolution fails | no side effect | correct reference and retry | log secret value for debugging |

## Recovery Principles

1. Preserve evidence before intervention.
2. Do not convert uncertainty into failure for convenience.
3. Do not create a new idempotency key unless the operator intends a new work request.
4. Reconciliation observes authoritative current state; it does not rewrite history.
5. Database repair never uses Hermes internal storage as a substitute source of truth.
6. A redundant bounded follow-up is safer than silent loss, but parallel unbounded tasks are not.

## 8. v0.1.5 Recovery Additions

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

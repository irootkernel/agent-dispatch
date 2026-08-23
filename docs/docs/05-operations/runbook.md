# Operations Runbook

## 1. Initial Deployment Sequence

1. Install the verified Go-built Agent Dispatch binary.
2. Confirm `agent-dispatch version --output json`.
3. Create a disabled config with `agent-dispatch init`.
4. Set the Obsidian vault as a named resource.
5. Generate or install the verified Hermes capability report from E0-T4 procedures.
6. Run `agent-dispatch config validate --probe-targets`.
7. Run `agent-dispatch doctor --probe-targets`.
8. Run fixture-based `route plan`.
9. Install the Watchman trigger while the route remains disabled or no-submit according to implementation policy.
10. Run an initial full reconciliation in dry-run/no-submit mode.
11. Use a disposable vault and Hermes task space to pass the automatic-write gate.
12. Set config `enabled: true`, then explicitly activate the computed route revision with `agent-dispatch route enable --route wiki-maintenance --acknowledge-production-gate <computed-route-revision> --yes` (the acknowledgement must equal the revision `route show` computes; any other value is refused).
13. Install daily scheduled reconciliation.

## 2. Routine Inspection

```text
agent-dispatch status --output json
agent-dispatch doctor --output json
agent-dispatch dispatches list --state unknown
agent-dispatch dispatches list --state dead_lettered
agent-dispatch quarantine list
agent-dispatch receipts list --route wiki-maintenance
```

Investigate any unknown dispatch before retrying.

## 3. Normal Change Flow

A normal trigger invocation should:

- exit 0 for a drop/no-op;
- persist and submit one task if route is idle;
- persist and merge dirty generation if route is active;
- produce stable causal IDs in logs.

No operator action is required unless target acceptance becomes unknown, the item is quarantined, or active work becomes stale.

## 4. Unknown Dispatch Recovery

1. Inspect lineage:

   ```text
   agent-dispatch dispatches show <dispatch-id>
   ```

2. Run target reconciliation if supported:

   ```text
   agent-dispatch reconcile --route <route-id> --reason delivery
   ```

3. If lookup finds the Hermes task, record acceptance and do not retry.
4. If lookup proves absence, use `dispatches retry`.
5. If neither can be proven, keep dead-lettered/unknown and resolve manually. Do not send via webhook.

## 5. Stale Active Task

1. Inspect public Hermes status and Agent Dispatch receipts.
2. If Hermes proves terminal, refresh projection.
3. If a work receipt exists, validate it.
4. If status is unavailable, do not auto-fail based on time alone.
5. Resolve through explicit operator action or retain uncertainty.
6. Ensure dirty generation remains preserved.

## 6. Quarantine

For protected, unsafe, or oversized input:

```text
agent-dispatch quarantine show <id>
```

Options:

- fix configuration or vault condition, then `release --reason ... --yes`;
- request a full reconciliation instead;
- `discard --reason ... --yes` only when the event is intentionally irrelevant.

Release creates new lineage. It does not edit the original observation.

## 7. Full Reconciliation

Run for:

- initial baseline;
- Watchman overflow/fresh instance;
- lost source position;
- scheduled daily check;
- stale route uncertainty;
- operator suspicion of missed events.

```text
agent-dispatch reconcile --route wiki-maintenance --reason manual --submit
```

If active work exists, reconciliation merges into the pending dirty generation rather than creating parallel work.

## 8. Database Backup

Before upgrade or risky maintenance:

1. stop manual drain/reconciliation commands;
2. allow current short-lived command to finish;
3. verify no unexpired attempt lease, or record its state;
4. use the built-in backup or SQLite online backup path;
5. verify backup integrity;
6. retain config and capability report alongside backup metadata.

Copying a live WAL database without its WAL/SHM or checkpoint procedure is not a valid backup.

## 9. Upgrade

1. back up database and config;
2. validate new binary version/schema range;
3. run `doctor` with new binary without submitting work;
4. run migration;
5. verify integrity and capability compatibility;
6. inspect trigger command path and scheduled jobs;
7. resume route;
8. run one reconciliation.

## 10. Uninstall

Uninstall order:

1. disable route;
2. remove managed Watchman trigger;
3. remove scheduled reconciliation units;
4. remove binary;
5. retain config and SQLite by default;
6. delete the state directory manually after backup; v0.1 has no purge command, so this discards dedup and reconciliation history.

## 11. Delivery-Failure Operator Exits (E8-T2)

- **Expired submitting lease (process died mid-submit).** The next
  Watchman trigger, the scheduled `reconcile --submit`, and
  `dispatches drain` all run the recovery sweep at their head: the
  wedged intent moves to `unknown` and is reconciled automatically. No
  manual database edit is ever required; run `dispatches drain` only to
  force the sweep immediately.
- **Definite target rejection.** A dispatch the target provably refused
  (rejected receipt) dead-letters through the declared edge at rejection
  time: the record, attempts, and receipt stay inspectable through
  `dispatches show`, and the route slot is freed by `dispatches discard`
  (or the work recreated with `dispatches rerun`). A rejected dispatch no
  longer holds the slot with no exit.
- **Budget-exhausted retry_wait.** A dispatch whose submission backoff
  budget is exhausted stays in `retry_wait` and the drain skips it.
  `dispatches retry <dispatch-id>` is the documented exit: it makes the
  dispatch due and resets its attempt budget in one audited transaction
  (`explicit_retry_reset` in the transition history), so the next drain
  submits it under a fresh budget.
- **Startup uncertainty.** After an unclean shutdown or an unexplained
  gap in the structured log, run `reconcile --route <id> --reason
  startup` (OPS-006): the reconciliation rebuilds the latest-state
  comparison from the enumerated vault, collapses any observed drift
  into the single pending generation, and never assumes partial
  delivery. The `startup` reason distinguishes this operator decision
  from a manual re-evaluation in the audit history.
- **Over-budget follow-up chain (UNCERTAIN).** A route whose consecutive
  follow-up chain passed `MaxConsecutiveFollowups` resolves through
  UNCERTAIN instead of scheduling another generation: run
  `reconcile --route <id> --reason manual` (or wait for the scheduled
  reconciliation) to resolve the route from latest state.

## 12. Incident Data Collection

Collect:

- binary/version output;
- redacted normalized config;
- doctor output;
- dispatch lineage JSON;
- relevant structured logs;
- Hermes capability report and public task reference;
- database integrity result.

Do not collect note bodies or secrets unless the operator deliberately handles them outside the standard support bundle.

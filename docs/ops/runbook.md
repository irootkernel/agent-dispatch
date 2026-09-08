# Operations Runbook

This runbook targets the operator of a local macOS arm64 instance. Identify the
binary version, configuration path, route, and intended Hermes board before acting.
The installation owner authorizes state changes; repository maintainers own defect
escalation. Read-only inspection comes first, and uncertain delivery remains
uncertain until public target evidence resolves it. Use the same `--config` path
throughout; examples below use the default configuration and `wiki-maintenance`.

## 1. Initial Deployment Sequence

1. Install and verify the binary; prepare the existing vault and Hermes board,
   profile, and requested skills. Set the target's absolute executable path and
   explicit minimum version (at least 0.20.5).
2. Run `agent-dispatch setup wiki` against the intended disabled configuration.
   Correct findings and rerun using the configuration path it prints. Setup
   records the initial disabled baseline; it never activates a production route.
3. Install and inspect the Watchman trigger using `watchman install` and
   `watchman status`. The configured absolute vault root must itself be watched.
4. Review, install, and inspect the schedule appropriate to the drain mode.
   [Installation §4](installation.md#4-daily-reconciliation-scheduling-ops-006-ops-007)
   distinguishes daily reconciliation from the after-command drain recovery job.
5. Complete the route's automatic-write trial in a disposable vault/board before
   enabling production. Fixture normalization alone is not a live trigger test.
6. Set configuration `enabled: true`; run `config validate --probe-targets`,
   `route preflight`, and `route show` for the final route revision. Explicitly
   activate that revision with `route enable --route wiki-maintenance
   --acknowledge-production-gate <computed-route-revision> --yes`.
7. Run `status` and `doctor`; inspect trigger and schedule state. Verify an
   authorized eligible edit reaches the intended task and receipt flow. If a
   check fails, disable new submissions, retain evidence, and follow recovery.

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
6. retain the configuration beside the backup metadata.

Copying a live WAL database without its WAL/SHM or checkpoint procedure is not a valid backup.

## 9. Upgrade

1. back up database and config;
2. validate the new binary identity and run doctor for schema compatibility;
3. run `doctor` with new binary without submitting work;
4. run migration;
5. verify integrity and capability compatibility;
6. inspect trigger command path and scheduled jobs;
7. resume route;
8. run one reconciliation.

### 9a. v0.1.6 serialization upgrade (migration v18, E15)

The migration is additive and configuration-independent; the first
reconciliation after the upgrade materializes the serialization-group
topology from the current configuration. The upgrade sequence:

1. verified pre-migration backup (§8) — required before the migration
   runs (it creates one automatically; keep it);
2. `hermes probe --target <id>` — the probe contract advanced to v3, so
   every cached record is stale until re-probed; the fresh record
   certifies one effective serialization mode
   (`agent-dispatch-group-enforced` on a Hermes without `--mutex-key`,
   `agent-dispatch-group-plus-target-mutex` with it);
3. declare every target floor explicitly — an omitted
   `minimum_version` now fails closed; `hermes set-minimum-version
   <target> 0.20.5` remediates a legacy below-floor or omitted-floor
   document and reports each affected route's changed revision;
4. `route preflight --route <id>` for every route — a below-floor
   installation, an unknown profile or skill, and an unacknowledged
   cross-group topology all refuse here, before any submission;
5. `route enable --route <id> --acknowledge-production-gate <revision>
   --yes` — every route whose revision changed (a serialization edit,
   the acknowledgement flag, or a floor change) pauses until the fresh
   acknowledgement;
6. run one reconciliation per route — it materializes the group
   membership and reports any preserved `serialization_conflict` (two
   pre-upgrade active children resolving to one group select no
   arbitrary holder; let them reach their terminal outcomes through
   `work complete`/`work fail`, and the group resolves atomically to
   its sole survivor or its oldest waiting lane).

Rollback restores the pre-upgrade database, the previous binary, and
the compatible configuration together; no down migration exists (OPS-015).

### 9b. v0.1.6 notification drain upgrade (migrations v19-v20, E16/E17)

The migrations are additive: v19 adds due deadlines, lease columns, and the
drain-run evidence table to the notification outbox, and v20 adds the
managed schedule's durable `--at` timing store; neither rewrites any historic
row, and every existing notification identity and attempt survives
unchanged. Pending notifications backfill their due time to the creation
timestamp, so migrated pending work is immediately due and the next
explicit `notifications drain` picks it up. An omitted `notifications.drain`
block keeps the v0.1.5 manual behavior and the exact route revision. The
upgrade sequence:

1. verified pre-migration backup (§8) — required before the migration
   runs (it creates one automatically; keep it);
2. run the migration (any command that opens the state store);
3. run `notifications drain` once to clear the migrated
   immediately-due pending work — the drain selects due work for every
   route in one bounded pass (it takes no route filter; a route-scoped
   invocation is not expressible, §cli-spec 18);
4. only when enabling automatic draining: declare the `drain` block,
   install the managed schedule for the route
   (`schedule install --route <id> --platform launchd`, cli-spec §19),
   and re-run `route preflight` — a declared block whose effective
   policy differs from the manual default changes the route revision,
   the preflight schedule check warns with the exact install command
   while the schedule is missing, and `route enable` refuses without
   the installed, loaded, definition-matching schedule, so production
   acknowledgement pauses until
   `route enable --route <id> --acknowledge-production-gate <revision>
   --yes` re-acknowledges it.

Rollback follows the shared v0.1.6 procedure: restore the pre-upgrade
database, the previous binary, and the compatible configuration together;
no down migration exists (OPS-015).

## 10. Uninstall

Uninstall order:

1. disable route;
2. remove managed Watchman trigger;
3. remove scheduled reconciliation units;
4. remove binary;
5. retain config and SQLite by default;
6. verify the exact trigger and jobs are absent. Permanent state erasure is a
   separate operator decision, not a required uninstall step; it discards dedup
   and reconciliation history.

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
  (or the work recreated with `dispatches rerun`). In the rare case the
  dead-letter transition itself fails (a logged warning on the submit
  outcome), the dispatch stays in `rejected` holding the slot — there is
  no automatic retry of the edge, and the drain does not pick up
  rejected work; the operator resolves it by forcing the route to
  UNCERTAIN with `route stale --reason <why>` (valid once the dispatch
  passes `active_stale_after`) and then reconciling, or by inspecting
  `dispatches show` and correcting the underlying target condition
  before rerunning.
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
- the frozen Hermes interface evidence (`docs/integrations/hermes-capability-report.json`) and public task reference;
- database integrity result.

Do not collect note bodies or secrets unless the operator deliberately handles them outside the standard support bundle.

## 14. Multi-Destination Operational Flow

1. Run `setup wiki` (pass `--route <id>` when several routes are
   declared); review the disabled config and effective Watchman binding.
2. Run `hermes probe` and `route preflight`; resolve every missing profile,
   skill, capability, sink, and trigger finding.
3. Confirm the walkthrough's baseline step established the initial
   baseline (`reconcile --reason initial --baseline-only` ran inside it;
   it may be rerun standalone at any time while the route stays
   disabled) and inspect aggregate status.
4. Enable only with the exact route revision and capability-evidence
   fingerprint shown by the production gate; the walkthrough's summary
   names all five gate states and never enables anything itself.
5. Use `events show` for parent/child state and `notifications list` for sink
   state; retry them independently.

Capability or executable drift pauses delivery. Watchman drift is repaired by
an explicit replace after status review. A reconciliation fence conflict is
normal retryable evidence, not data loss. Partial work follows the remaining
scope; blocked work stays manual. Notification failure never justifies retrying
or rewriting an otherwise successful Hermes task.

Notification delivery posture: a pending notification whose sink declaration
no longer resolves records a retryable `sink_unresolvable` attempt on every
drain and stays pending — the operator exit is to restore a declaration for
that sink id (a log sink is enough) and let the next drain resolve or refuse
it, never to touch the notification tables directly. Run one drain pass at a
time: two overlapping passes (for example a manual drain over the scheduled
one) can surface a storage-conflict exit while both passes' deliveries stay
idempotent under the stable key, and the interrupted pass simply re-runs.

For rollback, disable submissions and the relevant triggers/schedules, preserve
the failed database, and restore the verified pre-upgrade database/configuration
with its matching binary. Run integrity checks and doctor before resuming; never
point an older binary at an upgraded database. Version-specific procedures in
§9a/§9b retain their named migration scope.

## Success, Recovery, and Escalation

After intervention, confirm `doctor` findings are resolved or explicitly explained,
inspect the affected dispatch/notification/receipt by ID, and verify the expected
Watchman and schedule definitions before resuming automatic submissions. A healthy
command exit is not proof that Hermes completed the work.

If an intervention fails, leave new submissions disabled, preserve causal IDs and
backups, and avoid retries that could duplicate uncertain work. Database restore
must follow [installation §6](installation.md#6-backup). The host operator handles
local paths, permissions, and scheduling; Hermes administrators handle target access;
repository maintainers handle integrity, contract, and unexplained state-machine
failures. Provide the redacted incident data from §12, never secret values or note bodies.

# Operations Runbook

This runbook targets the operator of a local macOS arm64 or Linux instance. Identify the
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
agent-dispatch sync status --group <group-id> --output json
```

Investigate any unknown dispatch before retrying. For an enabled sync group,
also inspect `state`, `reason`, and the separate
`latest_publication`, `latest_delivery`, and `latest_import` outcomes. An
unresolved `uncertain`, `recovering`, or `recovery_required` outcome is not a
successful synchronization; retain its evidence and use the matching row in
[failure recovery](failure-recovery.md).

## 3. Normal Change Flow

A normal trigger invocation should:

- exit 0 for a drop/no-op;
- persist and submit one task if route is idle;
- persist and merge dirty generation if route is active;
- produce stable causal IDs in logs.

No operator action is required unless target acceptance becomes unknown, the item is quarantined, or active work becomes stale.

### 3a. Normal Manual Sync Flow

Sync is opt-in and manual in E21. After membership bootstrap and the initial
administrator checkpoint, the publishing node runs `sync publish`; the peer
runs `sync reconcile`; each node then inspects `sync status`. Publication does
not require a cooperative-import acknowledgement, but live-tree import does.
The repository worktree must have the configured `content_ref` checked out on
both nodes; a detached or different `HEAD` fails closed.

After both nodes have reconciled, run
`sync verify --group <group-id> --output json` on either node. Require
`result: complete` and two collected nodes before
claiming fresh pair convergence. `sync status` keeps `latest_delivery` and
`latest_verification` separate and lists both expected incarnations even when
one peer is offline. An `incomplete` result with `peer_unavailable` requires
checking that node's service and approved HTTPS route; `local_incomplete` or
`peer_incomplete` requires inspecting dirtiness, pending work, membership, and
control on that node. `local_binding_mismatch` calls for checking the local ref,
incarnation, and configuration; `peer_binding_mismatch` calls for checking the
peer's membership and incarnation. For `target_recheck_unavailable`, restore
the approved Git remote or local ref read and retry. For
`local_changed_during_verification`, inspect local work and control, let them
settle, then retry. `target_changed` requires a new verification after the
configured refs settle. An interrupted `planned` result remains visible until
the next `sync verify` expires it after five minutes. Do not treat a previous
complete result as current
after either ref, incarnation, or governed working copy changes.

When an operator starts `sync serve`, its worker inspects configured refs on
startup and every five minutes even if every nudge is lost. The same guarded
reconcile command processes a successful nudge wake. `sync status` reports the
durable `recovery_schedule`; repeated failures delay retries by 30 seconds up
to five minutes and survive service restart. The reported failure count stops
at 16. A paused group waits without claiming recovery. A stale sync control
configuration binding also suspends recovery until explicit `sync reconcile`
rebinds it. In `recovery_schedule.last_reason`, `reconcile_failed` means the
configured-ref pass did not settle; inspect the reconcile result and control.
`inbox_unavailable` means the worker could not read its local inbox; inspect
SQLite health. `local_ref_unavailable` means the local content ref could not
be confirmed after reconciliation; inspect that ref and the peer inbox.
`inbox_unsettled` means at least one nudge could not be settled; inspect its
retained reason and retry after resolving the cause. The managed service
definition is operated through `sync service`.

Queue configuration and recovery:

- `sync.bounds.queue` accepts integers from 2 to 1000. An existing value of 1
  now fails configuration loading and requires an explicit edit to at least 2;
  the CLI does not rewrite it automatically.
- New admission limits pending nudges to `sync.bounds.queue - 1` and combined
  unresolved jobs plus pending nudges to `sync.bounds.queue`. Existing logical
  identities replay before capacity checks. A nudge remains pending until
  guarded reconciliation establishes its resolution; never settle it early
  to free capacity.
- Existing saturated queues and obligations above a lowered limit are retained.
  Inspect `sync status`, the peer inbox, and unresolved job reasons. If safe,
  increase the configured queue within the hard ceiling of 1000 to provide
  capacity for recovery. Changing the queue changes `import_bounds_digest`
  and the acknowledgement configuration revision. Refresh the node's
  cooperative-import acknowledgement using the procedure below before
  automatic live-tree import, run `sync reconcile` to refresh the control
  binding, and restart `sync serve` to load the configuration.
- A full queue at 1000 requires an operator recovery decision based on the
  retained obligations and their causes. Do not purge state, delete replay
  evidence, or drop obligations to manufacture capacity. These limits do not
  guarantee progress for every combination of unresolved jobs and nudges.

Before enabling import on a node:

1. Start from that node's normalized, validated configuration and its current
   state-incarnation ID. Do not copy another node's acknowledgement.
2. Have the configuration producer compute the canonical `scope_digest`,
   `safety_policy_digest`, `import_bounds_digest`, and acknowledgement
   `config_revision` defined in the sync contract's acknowledgement-digest
   section. The paired example configuration and record provide checked vectors
   in `docs/examples/config.yaml` and
   `docs/examples/sync-import-acknowledgement.json`.
3. Review the complete versioned record, give it a new `acknowledgement_id`,
   and place it at `sync.cooperative_import_acknowledgement`. Run
   `config validate`, then `sync status --group <group-id> --output json` and
   require both acknowledgement and control-configuration currentness before
   `sync reconcile` may apply live-tree effects.
4. Repeat this review after any bound local resource, remote/ref, scope, local
   identity, administrator anchor, import-bound, safety-policy, or state-
   incarnation change. When a bound configuration value changes, inspect the
   current `control_revision` in `sync status`, then run
   `sync resume --group <group-id> --expected-control-revision <revision> --config <path>`
   using that configuration. Require both
   `import_acknowledgement_current` and `control_config_current` in a fresh
   status result before importing. A blocked control still requires its named
   recovery procedure; `sync resume` cannot clear a safety hold.

Never invent or shorten a digest projection. If no trusted configuration
producer can supply the exact values, leave import unacknowledged and allow
`sync reconcile` to fail closed.

### 3b. Managed Sync Service

On each node, run `sync service render --group <group-id> --config <path> --output json`
and confirm its binary, configuration path, group, and digest.
Use `sync service install` with the same flags, then `sync service inspect` and
require `definition_matches` and `loaded`. The macOS definition is a launchd
user agent; the Linux definition is a systemd user service. The service runs
the same `sync serve` and guarded reconcile path as explicit commands. It
uses `~/Library/Logs/agent-dispatch/<label>.out.log` and `.err.log` on macOS;
the managed executor truncates an open stderr log at 10 MiB before its next
write. Systemd logs are available through the user journal on Linux.

Stop or disable the service before changing its sync configuration. If the
configuration changes first, a running executor suspends peer work; a newly
launched managed executor exits cleanly. The exact definition can still be
inspected, disabled, or uninstalled with the original group and config path,
even if the configuration is unreadable or changes to another group. Restore
the configuration before reinstalling. On macOS, install kickstarts an already
loaded job without terminating a running executor; this also starts a job that
exited cleanly while sync was disabled. When changing groups, uninstall the old
group's exact definition before installing the new group. `inspect` reports
manager load state; confirm `sync status` reports `health.listener` ready to
establish that the executor is serving. The service does not configure
Tailscale or start an HTTPS proxy; review that route before expecting remote
peer requests. Keep `state_dir` short enough for its
`peer-service/http.sock` Unix socket path; on Linux a long disposable path
caused `sync serve` to fail with `bind: invalid argument` before any listener
was available.

For a private Serve route, select a reviewed HTTPS port on each tailnet node,
install port-capable binaries on both nodes, then configure and sign the
membership endpoints with that port. Keep each configured endpoint string
identical to its signed membership value, including explicit port, trailing
slash, and host spelling. Confirm the
route is restricted to the tailnet with Funnel disabled. Proxy to the
owner-only Unix socket where the installed Tailscale service can reach it.
On macOS, if the Tailscale network extension returns HTTP 502 when proxying
directly to the socket, provision an operator-owned bridge bound only to
`127.0.0.1`, forward that bridge to the socket, and point the private Serve
route at the loopback port. Confirm TLS and `sync verify` in both directions
before relying on the route. Keep the bridge under operator supervision;
Agent Dispatch does not install or manage it. Other local processes can reach
the loopback listener while it runs; peer authentication still applies, but
the Unix socket's filesystem permissions no longer guard that hop. The bridge
forwards HTTP request headers, including the bearer credential, and bodies in
cleartext over the local loopback hop; restrict access to the host and stop the
bridge when the service is not needed.

For a planned interruption, use `sync service stop` to keep its definition or
`sync service disable` to stop automatic start. Inspect pending publication,
delivery, import, and verification work before restarting with `install`.
`sync service uninstall` removes only the current matching definition and
preserves SQLite state and evidence. A drifted or foreign definition is
refused; identify its owner and review it before changing anything. `sync status`
exposes bounded listener, auth, membership, queue, Git, import,
verification, activation, and service posture under `health`; `doctor` reports
unhealthy categories. `credential_resolution_not_probed` means the status
command did not resolve secrets, so verify live authentication separately.

### 3c. Sync State and Trust Maintenance

- **Upgrade:** Stop the managed service, back up state and configuration, and
  uninstall the old exact definition with the old binary and config path.
  Install the reviewed binary, inspect its rendered definition, and install it.
  Validate config, inspect status and doctor, reconcile, then verify the pair. An ordinary
  restart retains the state incarnation and all pending obligations.
- **Backup and restore:** Stop the service and participating writers before
  taking a consistent SQLite and repository backup. Preserve the old copy for
  diagnosis. A destructive reset or restore must use a new configured local
  `state_incarnation_id`, a fresh cooperative-import acknowledgement, and a
  reviewed `sync membership plan --change incarnation_registration --instance <local-id>`
  followed by `sync membership apply` with the exact predecessor.
  Do not start the restored node as its old incarnation or copy the other
  node's acknowledgement. Reconcile and verify both nodes after registration.
- **Publisher-key rotation:** Stop the service and pause new protected work.
  Generate the new key outside the service. Update the local publisher key
  and state incarnation in config, then
  review `sync membership plan --change key_rotation --instance <local-id>`
  and apply its exact predecessor. Refresh the import acknowledgement and
  verify both nodes before retiring the previous key.
- **Peer credential rotation:** Stop the service and pause with the current
  control revision. Then
  provision distinct directional secret references on both nodes, update
  configuration and membership endpoint/key declarations when they change,
  validate, resume with the new configuration revision, then test fresh
  verification. The service never prints credential values.
- **Bootstrap or re-registration:** Review the two configured identities and
  incarnations. Use `sync membership plan --change bootstrap` only for the
  initial `none` predecessor; use `incarnation_registration` and the exact
  non-null predecessor after a reset. Apply the reviewed plan, establish an
  initial signed checkpoint with `sync checkpoint plan --kind initial_baseline`
  and `sync checkpoint apply`, and reconcile. Never adopt an unsigned tip.
- **Conflict or history hold:** Pause affected work and preserve both Git
  histories, index/worktree facts, SQLite journals, and the exact control
  reason. Resolve the content and obtain administrator review. Use
  `sync checkpoint plan --kind conflict_resolution` or
  `--kind history_bound_exhausted` for the actual hold, apply the reviewed
  plan, reconcile, and verify. Never use force push, reset, or an implicit
  trust expansion.

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
   (`schedule install --route <id> --platform launchd|systemd` as
   appropriate for the host, cli-spec §19),
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

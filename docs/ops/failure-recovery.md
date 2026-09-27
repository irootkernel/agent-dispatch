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
| `sync reconcile` reports `deferred` | `operator_pause`, `acknowledgement_stale`, `resource_busy`, `observation_unavailable`, `git_unstable`, `local_overlap`, or `untracked_collision` names the unsatisfied fence | preserve the named paths and evidence, resolve only the reported fence, then rerun reconcile; when `operator_pause` also reports `membership_mode: blocked_emergency`, adopt a reviewed normal membership replacement before `sync resume` | stash, clean, reset, overwrite local files, or resume an emergency posture before replacing membership |
| `sync reconcile` reports `publication_recovery_pending` at exit 10 | an already-signed publication has a live claim or retryable failure | inspect the unresolved publication and journals in `sync status`; allow the claim owner to finish or resolve the transient cause, then retry | remove the job by hand or bypass its claim |
| `sync serve` warns that sync control configuration binding is stale | the configured sync revision differs from the durable control revision; startup, periodic, and nudge recovery remain suspended across restarts | run explicit `sync reconcile` under the reviewed configuration to rebind control, then inspect `sync status` | repeatedly restart the service or discard pending nudges |
| `sync status` reports a recovery schedule with failures | configured-ref inspection was deferred; its next due time and bounded reason survive service restart | inspect `recovery_schedule`, the group control and unresolved jobs; correct the reported cause and allow the next retry or run explicit `sync reconcile` | delete the schedule row or repeatedly restart to evade backoff |
| `sync verify` is `incomplete` or `target_changed` | a peer is offline or stale, local work is pending or dirty, or a pinned ref moved during collection | inspect both expected nodes, warning codes, current refs and `latest_verification`; reconcile or restore service, then run a new verification | treat equal commit IDs or an old complete record as fresh convergence |
| `sync service inspect` reports a mismatched definition | another definition occupies the managed path, or the executable or configuration path in the definition changed | inspect the owner and rendered bytes; after review, stop and remove the exact old managed definition before installing the new one | overwrite or remove a foreign definition |
| `sync status` health reports a missing listener, stale activation, or unavailable membership ref | the service is stopped, the import acknowledgement is stale, or membership has not been adopted | inspect the fixed health reason and `doctor`; restore the exact service definition, refresh acknowledgement or follow the reviewed membership procedure | infer pair readiness from a loaded unit or old complete verification |
| restored or reset sync state has an old configured incarnation | durable local identity may have been replaced while peers still trust the previous instance | keep the service stopped, choose a fresh incarnation, review and apply `sync membership plan --change incarnation_registration`, refresh acknowledgement, then reconcile and verify | restart with the retired incarnation or copy another node's state |
| `sync reconcile` reports `failed` with `publication_recovery_precondition` or `publication_recovery_error` | an already-signed publication could not complete recovery because of a publisher precondition or store error; no new history hold was created | inspect the publication journal, control, and reported error; correct that cause, then rerun reconcile | treat exit 14 alone as proof of divergent history or clear the unresolved job |
| `sync reconcile` blocks after a configured remote ref disappears or is rewritten | approved content or membership history is no longer a safe fast-forward | preserve both histories, restore the approved ref or prepare the applicable reviewed administrator checkpoint, then reconcile | force-fetch, reset, or silently adopt the replacement history |
| `pre_signature_publications_pending` is nonzero | a publication has no live publisher claim and still needs signing; the service cannot resolve its key | inspect the oldest time and unresolved logical identity, then re-enter explicit `sync publish` under the reviewed configuration; escalate a stranded older identity for operator review | ask the service to sign or remove the unresolved row by hand |
| `sync reconcile` exits 14 with `blocked` | divergent history, live protected claim, or missing exact checkpoint; a `failed` result at 14 follows the publication-recovery row above | resolve with ordinary Git, then use reviewed `sync checkpoint plan`, `sync checkpoint apply`, and reconcile | merge, rebase, force-push, or clear the control row by hand |
| `sync reconcile` or `sync resume` exits 30 with `membership_mode: blocked_emergency` | an emergency membership is the adopted posture; resume has re-armed `membership_emergency`, or reconcile is reporting a blocked emergency after any stronger hold was resolved | review and adopt a normal `sync membership plan` / `apply` replacement; an exact checkpoint may resolve a stronger conflict, trust, or recovery hold but preserves the emergency posture | clear the control row by hand or assume a checkpoint cleared emergency membership |
| `sync reconcile` exits 30 with `membership_adoption_incomplete` / `membership_emergency` | emergency posture was durably armed but the local membership ref did not move | repair the reported local ref precondition and rerun `sync reconcile`; then adopt a reviewed normal membership replacement | use a content checkpoint or clear either posture or ref by hand |
| `sync reconcile` exits 30 with `membership_adopted` while `membership_mode` is `blocked_emergency` | the emergency revision was adopted while an operator pause or stronger hold remains visible | preserve that hold; after resolving it, adopt a reviewed normal membership replacement | infer that the emergency posture cleared because the visible reason is not `membership_emergency` |
| a sync command exits 30 with `sync_trust_failed` before a blocked result | the configured remote URL, repository digest, URL rewrite, or repository HTTP transport override cannot be proved safe | correct or remove the repository/worktree remote or transport override, then rerun the same command | use a checkpoint to bypass remote-binding proof |
| `sync reconcile` exits 30 with result reason `trust_failed`, `membership_stale`, `history_uncovered`, or `bound_exhausted` and `control_reason: trust_failure` | signature, membership, or covered-history trust failure | inspect the pinned membership and signed history; after review use checkpoint kind `history_bound_exhausted` | accept an unsigned tip, widen trust implicitly, or use `conflict_resolution` |
| `sync reconcile`, `sync publish`, or checkpoint recovery exits 30 with `recovery_required` or `trust_failure` | an earlier protected effect or trust failure remains unresolved; remote-move recording preserves the stronger hold and any signed candidate | inspect the import journal and exact Git/file state, then after review use checkpoint kind `history_bound_exhausted`; the verified checkpoint may linearly cover and retire or complete an older stored import plan | classify the hold as a signature failure, retry the publication through it, use `conflict_resolution`, or clear the control row by hand |
| `sync reconcile` exits 13 with `uncertain` and `import_state: applying`, `recovering`, or `uncertain` | file, index, ref, and SQLite effects cannot yet be proven complete | stop participating writers, preserve the import journal and both Git/file states, then rerun only after inspection or prepare an administrator checkpoint | infer success from equal bytes or lease expiry alone |
| `sync publish` loses its approved fast-forward | another signed child moved the remote from the same base | preserve both signed commits, resolve ordinary Git history, then use reviewed `sync checkpoint plan --kind conflict_resolution`, `sync checkpoint apply`, and reconcile | retry with force, merge, rebase, or discard either signed history |

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

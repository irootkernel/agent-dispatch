# Hermes v0.20.5 Compatibility Gate Evidence (E15-T4, G11)

> **Status:** Verified 2026-08-31 (E15-T4)
> **Hermes version:** `Hermes Agent v0.20.5 (2026.8.19)` — the installed
> public CLI at `/Users/draccoon/.local/bin/hermes`, unmodified (BND-003).
> **Environment:** fully disposable — a throwaway `$HOME` (own
> `~/.hermes` state), a disposable board `agent-dispatch-g11` created and
> populated only through the public CLI, a disposable profile
> `wiki-maintainer` created through `hermes profile create` and registered
> on the board's assignee surface through `hermes config set`, a
> disposable vault, and a disposable Agent Dispatch state directory. No
> production route, board, profile, or vault was touched; no worker was
> executed (task execution requires operator credentials and is outside
> this gate). The Agent Dispatch binary is the reviewed build of this
> commit.

## 1. Probe and capability certification (AC-1101, HER-020)

`hermes probe` (probe contract `agent-dispatch.hermes-probe/v3`) against
the real executable:

- every required shape probe passed; the only missing create flag is
  exactly `--mutex-key`;
- the record certifies effective serialization mode
  **`agent-dispatch-group-enforced`** — the normal posture of the 0.20.5
  floor, not a failure — fingerprint `cap:35583a58b0753f09b906a8b943bac0d5`;
- `hermes capabilities` reports the same mode with `resource_mutex:
  false`, agreeing with the probe.

`route preflight` on a route whose destinations share the explicit group
`wiki-publish` (with a second route in the same group and a third in
`side-publish`, every involved route acknowledging cross-group
concurrency) passes (exit 0): profile on-disk through the board's
public assignee surface, the required skill `llm-wiki` enabled for the
profile through the public skill table, the serialization check naming
the certified mode as a warn — local enforcement with the flag
suppressed — never a compatibility failure.

## 2. Real submission and renderer suppression (AC-1101, HER-021)

`reconcile --reason initial --submit` delivered a real task onto the
disposable board (durable acceptance, `submitted_state: accepted`), and
the independent-group demonstration delivered a second real task. Both
created task objects — read back through the public
`kanban list --json` — contain **no `mutex_key` field at all**: the
renderer never sends the unsupported flag. The task request carried the
destination's effective serialization group in its assignment block
(`side-publish` for the independent lane), where the complementary
target mutex would be rendered exactly when a later eligible Hermes
exposes the flag.

## 3. Shared-group exclusion, burst merging, independent concurrency
> (AC-1102 through AC-1104)

With the `wiki-publish` group held by the active reconciliation child
on route `wiki-maintenance`:

- a three-file burst on the holding route merged into the lane's dirty
  generation (`disposition: merge_pending`, no submission, no second
  child);
- an arrival on the other `wiki-publish` lane (route `nightly-audit`)
  merged the same way — a different route cannot bypass the group slot;
- an arrival on the `side-publish` group (route `side-index`) submitted
  immediately and durably — acknowledged independent groups progress
  concurrently.

The durable group table after the demonstrations holds exactly one
holder per group — `wiki-publish` held by the reconciliation child,
`side-publish` held by the independent child — and one active lane per
holder. This walkthrough also **exposed and closed a real defect**: the
reconciliation child's activation bypassed the group gate
(`CommitReconcileIntent` applied the lane activation without acquiring
the slot), allowing a reconcile child to run beside the group holder's
active child; the gate now refuses the whole commit while the group is
occupied (pinned by `TestE15T4ReconcileChildRespectsOccupiedGroup`).

## 3a. Defects the reopened real-environment gate exposed and closed

Re-opening the real-Hermes environment tests for the 0.20.5 baseline
(their verified set had pinned the retired baseline, so the legs had
been skipping as TST-007 gaps) exposed three product defects, each
fixed and pinned in this task:

1. **The reconciliation child bypassed the group slot.**
   `CommitReconcileIntent` applied its lane activation without the
   group gate, letting a reconcile child run beside the group holder's
   active child — visible in the walkthrough's durable state. The gate
   now refuses the whole commit while the group is occupied (pinned by
   `TestE15T4ReconcileChildRespectsOccupiedGroup`).
2. **A downtime re-acknowledgement could never submit again after an
   executable swap.** A partial probe (the executable answers the
   version gate, delivery surfaces fail) refused the enable outright,
   and the preserved capability binding kept naming the retired
   executable. The enable now treats a partial probe as target
   downtime (AC-303's liveness posture), keeps the stored binding only
   when the previous cached evidence names the SAME executable, and
   retires it explicitly on a freshly acknowledged swap
   (`CapabilityFingerprintClear`; the same-executable outage preserve
   rule of E11-T2 is unchanged).
3. **Post-cutover dead letters wedged recovery re-acknowledgement.**
   The DAT-013 enable gate counted ANY unresolved intent under a
   different revision, so the documented recovery order
   (re-acknowledge, then retry the dead letter) refused. The gate now
   keys on the actual legacy marker — the absence of a
   destinations-contract child row (migration v12) — and post-cutover
   residue resolves through the retry/discard exits after the
   re-acknowledgement (the enable-blocked pin now marks the legacy
   shape by that marker).

## 4. Below-floor refusal and floor restoration (AC-1106/AC-1107
> posture)

`hermes set-minimum-version hermes-main 0.21.0` raised the target floor
above the installed 0.20.5 (naming all three affected routes with their
before/after revisions and the owed re-probe, preflight, and
re-acknowledgement). `route preflight` then refused (exit 3,
`config_capability_missing`) — an installed version below the configured
floor fails before any side effect, and nothing was submitted. Restoring
the floor to 0.20.5 returned the preflight to green through the same
atomic helper. The synthetic legs of AC-1106 — 0.20.4 failing before
side effects and a later target-mutex Hermes adding the complementary
mutex — are pinned by the frozen-interface suites
(`TestE15T2BelowFloorSettingFailsClosed`, `TestE11T2SamePathFrozenAndNewer`,
`TestE15T2ProbeRecordsModeAndContract`, and the suppression test
`TestE11T2SubmitSuppressesMutexKeyForDriftedSurface`), which the gate
inherits as the same probe path (TST-012).

## 5. Baseline retirement

Every exact previous-baseline reference is removed from the current
tracked files — current contracts, historical narratives, fixtures, and
release notes now name the 0.20.5 baseline or a historical phrasing
without the exact retired version. Git history and existing tags are
not rewritten.

## 6. Boundary

No Hermes core file, private storage, or plugin was read beyond the
public CLI surface or modified at all; no worker executed; the
disposable board, profile, home, vault, and state directory were
discarded after the walkthrough. The full-machine `make verify` gate is
green on the committed tree.

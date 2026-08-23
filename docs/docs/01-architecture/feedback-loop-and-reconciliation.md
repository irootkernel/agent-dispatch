# Feedback Loop and Reconciliation

## 1. Problem

Hermes may edit the same Obsidian vault that activated it. Those edits are observed by Watchman and can recursively activate more work. At the same time, a human may edit the vault during the Hermes run. Agent Dispatch must limit recursion without discarding human work.

## 2. Core Strategy

1. One unresolved maintenance task per route.
2. Every later relevant observation is persisted.
3. Later observations increment one dirty generation instead of creating parallel tasks.
4. A cooperating Hermes task reports a bounded work receipt.
5. Exact receipt matches may suppress exact self-generated changes.
6. Mixed or uncertain changes remain dirty.
7. Completion creates at most one follow-up task for latest state.

## 3. Sequence

```mermaid
sequenceDiagram
    participant U as User
    participant W as Watchman
    participant J as Agent Dispatch
    participant H as Hermes

    U->>W: edits note A
    W->>J: batch A
    J->>H: Kanban task D1
    H->>J: work begin D1 / run R1
    H->>W: edits index I and note A
    U->>W: edits note B
    W->>J: batch I,A,B
    J->>J: persist and mark dirty generation
    H->>J: work complete R1 with manifest I,A
    J->>J: verify exact subset; B remains human/unknown
    J->>H: at most one follow-up D2 for latest state
```

The algorithm never suppresses `B` merely because it occurred during run `R1`.

## 4. Work Receipt Validation

A completion receipt is valid only if:

- dispatch exists and belongs to the route;
- dispatch is the active or recently active dispatch;
- resource ID matches;
- external task ID, when available, matches the accepted receipt;
- run ID is unique within the dispatch;
- every path is normalized and contained;
- every digest has the correct format;
- receipt size and change count are within limits;
- completed receipt follows a begin receipt, unless route policy explicitly permits completion-only mode.

A valid receipt still does not prove an observed change is self-generated until path and after-digest evidence match.

## 5. Exact Suppression Algorithm

For each observed dirty change after `work begin`:

```text
if observed path and after_digest exactly match one completed receipt item
and no contradictory observation for that path exists after receipt completion
and the resource and dispatch lineage match:
    mark change as verified_self_generated
else:
    retain change as unresolved
```

A receipt path with no corresponding observed change is recorded as unresolved `receipt_extra_path` — unless it is immaterial to the route: a path outside the effective scope (excluded by the pattern engine or the resource's file scope), or a byte-identical rewrite whose reported after-digest equals the durable path fact, is recorded as `receipt_immaterial_path` and does not block full suppression (E8-T1). Every other claimed-provenance path without a durable observation is not an exact match and blocks full suppression. The remaining unresolved outcomes are `receipt_missing_path` (an observed path the receipt does not cover), `digest_mismatch`, `digest_unverified` (either side lacks a known digest), and `observed_before_run` (a temporal-window demotion: an observation before the run began cannot be the run's output). Multiple observations of one path collapse to the latest before matching.

A batch is fully suppressible only when every meaningful change is verified self-generated and there is no pending reconciliation flag.

If even one path is unresolved, route remains dirty. The follow-up task requests a full latest-state check and carries the bounded manifest of unresolved paths observed for the dirty generation (the delivered E8-T1 behavior; the follow-up content fingerprint is recomputed over that manifest).

## 6. No Receipt or Invalid Receipt

When Hermes does not use the companion skill:

- active work can still be serialized;
- changes are retained;
- completion may be learned through public Hermes status if supported;
- self-change suppression is unavailable;
- Agent Dispatch creates at most one conservative follow-up if dirty.

This may produce one redundant maintenance task, which is preferred over silent loss.

## 7. Active Work Completion

Completion evidence can be, in descending preference:

1. validated `agent-dispatch work complete` receipt;
2. public Hermes execution status plus an optional result receipt;
3. explicit operator resolution;
4. timeout-based uncertainty, which does not imply completion.

If active work is completed:

- no dirty generation: clear active route state;
- dirty generation but every change exact-suppressed: clear active route state;
- unresolved dirty changes: create one follow-up intent and make it active after acceptance;
- pending reconciliation: the generation collapses into the single follow-up intent (the follow-up is the full-reconciliation vehicle under the CON-003 bound); an idle route's `reconcile` command may also schedule the intent directly.

If active work fails or is canceled (cooperative `work fail`, or a verified terminal target status):

- failure never erases dirty state; the activating changes remain unprocessed;
- while the route's consecutive-failure budget remains (the route's configured `failure_budget`): create one bounded follow-up intent for latest state, under the same collapse bound as completion, and make it active after acceptance;
- the consecutive follow-up chain itself is bounded (`MaxConsecutiveFollowups`, E8-T1): a completion whose follow-up would carry a generation beyond the budget schedules no follow-up and resolves the route through `UNCERTAIN` for operator reconciliation, like failure-budget exhaustion; the chain bound dominates — a failure-path completion over the bound also resolves through `UNCERTAIN` rather than scheduling another generation;
- when the budget is exhausted: mark the route uncertain and require operator resolution.

Follow-up and reconciliation decisions reference the dirty generation lineage rather than a single batch, because a dirty generation may accumulate changes across many batches and scheduled reconciliation may have no batch at all.

## 8. Stale Active Work

A task that remains unresolved beyond `active_stale_after` is not automatically considered failed. `doctor` and `reconcile` report it. If public Hermes lookup can prove terminal status, update projection. Otherwise require operator resolution or preserve uncertainty.

## 9. Reconciliation Types

| Type | Trigger | Result |
|---|---|---|
| Initial reconciliation | first installation | one initial reconciliation plan rather than per-file tasks |
| Source reconciliation | overflow, fresh instance, lost cursor | enumerate current Markdown scope and compare path facts |
| Delivery reconciliation | unknown submit | query Hermes by idempotency key or task ID |
| Route reconciliation | stale active state or configuration drift | compare local route state with public Hermes state |
| Content reconciliation | scheduled daily or manual | one latest-state maintenance generation |

The `reconcile --reason` CLI values map to these types: `initial`, `scheduled`, `overflow`, `fresh-instance`, `lost-cursor` (lost cursor), `manual`, `delivery` (unknown submit), and `stale-active` (stale active state or configuration drift).

## 10. Distinct Operator Operations

- `retry <dispatch-id>`: same request and idempotency key.
- `reprocess <batch-id>`: new decision under current policy.
- `rerun <dispatch-id>`: new intentional work request and key.
- `reconcile --route <id>`: new current-state assessment. This is also the operator exit from UNCERTAIN: a reconciliation of an uncertain route applies the `UNCERTAIN -> FOLLOWUP_READY` transition under `reconciliation_resolved` (or lands IDLE through the follow-up-dropped edge when no work is due), releasing the stale dispatch's slot and collapsing its retained generation (persistence §6).
- `quarantine release <id>`: explicit operator decision creating new lineage.

No generic `replay` command exists.

# SOT Changelog

## 1.0.7 - 2026-08-21

E5 post-closeout review remediations (no contract surface changes; behavior corrections under the existing contracts):

- attribution: a receipt path the generation never observed is recorded as unresolved `receipt_extra_path` and blocks full suppression — AC-404's extra-paths clause is enforced (`feedback-loop-and-reconciliation.md` §5 vocabulary updated);
- route machine: the declared UNCERTAIN exits are now reachable — full reconciliation is the operator resolution (`UNCERTAIN -> FOLLOWUP_READY` under `reconciliation_resolved`, landing IDLE through `followup_dropped_after_reconciliation` when no work is due; budget exhaustion previously left the route permanently wedged);
- completion transaction: an in-transaction follow-up need the caller never prepared refuses as an optimistic-concurrency conflict instead of dropping the pending reconciliation signal into a follow-up-less FOLLOWUP_READY;
- receipt validation: the document form enforces every schema-required field (`docs/schemas/work-receipt.schema.json`), and durable-store failures during lineage or replay validation classify as storage (exit 20) instead of being written as invalid-receipt audit evidence;
- reconciliation: stored path facts under an unreadable subtree are retained in the replaced snapshot (not merely excluded from the removal diff), completing the round-20 phantom-removal fix;
- records: the follow-up decision records the planned route revision as its policy revision with encoder-produced reason codes (the `"current"` placeholder is gone); the quarantine error boundary classifies store failures as storage and defects as internal instead of relabeling everything storage;
- docs: `processing-pipeline.md` §5 rule 1 no longer carries the refuted class-5 exit wording (error-model §4 boundary notes are authoritative).

Second review round (same date, advisory findings adopted):

- the lost UNCERTAIN-resolution race reports `transition_invalid` (exit 14) instead of a storage failure (`classifyResolutionError`, `TestClassifyResolutionError`), and `quarantine show` wraps durable-read failures as storage instead of internal defects;
- the intent-builder contract is uniform (due work without a builder fails loudly on every eligible route state, never silently drops);
- a no-work UNCERTAIN resolution no longer resurrects its pending generation between two transactions;
- the quarantine domain-outcome classification is shared through `ports.IsQuarantineDomainOutcome`;
- tests pin the quarantine/receipt error-boundary arms (`TestResolutionErrorClassification`, `TestQuarantineErrClassification`, `TestWorkReceiptErrClassification`), the attribution decision document's input-order determinism (`TestMatchDecisionDocumentDeterministic`), the follow-up decision's recorded revisions and reason content, and the single-winner concurrent resolution (`TestConcurrentUncertainResolutionSingleWinner`);
- feedback-loop §10 names `reconcile` as the operator exit from UNCERTAIN, and the full-reconciliation result contract's `reconcile_dispatch_id` condition covers the uncertain-resolution case.

Third review round (same date, convergence):

- the UNCERTAIN resolution is fenced like the completion path: the dirty generation and pending flag observed before the enumeration window must still hold inside the resolution transaction, so a merge or release landing mid-reconciliation refuses as `transition_invalid` instead of being silently absorbed;
- the resolution marks (work due) or clears (no work) its pending generation atomically in the same transaction, removing the second-transaction window; every lost-race arm of the reconcile surface — eligibility, the fence, a held slot — reports `transition_invalid` (exit 14), and `MarkPendingReconcile`/`CommitReconcileDecision` failures classify as storage;
- the reconciliation intent's evidence manifest no longer asserts deletes under unreadable subtrees (matching the removal-diff and snapshot posture);
- `quarantine show` reads share `ports.IsQuarantineDomainOutcome`, the unused resolution method is dropped from the CLI `storeOp` interface, and `Coordinator.Completion`'s unprepared-followup refusal is a typed conflict;
- new coverage: the fence (`TestResolveUncertainReconciliation`), the no-work CLI resolution (`TestUncertainNoWorkResolutionLandsIdle`), the manifest exclusion (`TestReconcileUnreadableSubtreeKeepsStoredFacts`), the builder contract (`TestIntentBuilderRequired`), and the reconcile/read error arms (`TestReconcileErrClassification`, `TestWrapQuarantineReadError`).

Fourth review round (convergence; the logic and security roles returned zero findings):

- the generation-conflict sentinel is shared (`ports.ErrGenerationConflict`, aliased by the adapter) and adopted at every boundary, the idempotency conflict joins the reconcile conflict arms, and the conditional `ClearPendingReconcile` never wipes a pending signal another actor marked inside the window (`TestClearPendingReconcileConditional`) while the idle no-work reconciliation resolves its own observed flag in one transaction;
- the coordinator's typed unprepared-followup refusal is pinned (`TestCompletionRefusesUnpreparedFollowupTyped`) together with the work-command conflict arms, and the document form rejects unknown top-level keys exactly as the published schema does (`doc unknown top field`).

Fifth review round (closure; security clean, logic clean except the residual below):

- receipt validation is exact against the published schema: exact key spellings at the document and change levels (Go's case-insensitive tag matching can no longer absorb a `Dispatch_ID`), and exactly one JSON value — trailing content is malformed input, never silently ignored (`doc case-variant key`, `item case-variant key`, `trailing json value`); the resolution fence's pending-only arm is pinned;
- accepted residual, recorded honestly: `pending_reconcile` is a bare boolean, so a mark folded into an already-true flag inside one enumeration window (an ABA interleaving) can be cleared with the stale signal — bounded to one delayed follow-up, self-healing on the next reconciliation; fully closing it needs a versioned pending signal (a schema change deferred with the finding).

## 1.0.6 - 2026-08-21

E5 contract changes:

- route state machine: the new edges `ACTIVE_DIRTY -> IDLE` (exact suppression, receipt-evidence guarded) and `ACTIVE_CLEAN -> FOLLOWUP_READY` (completion with a pending reconciliation generation) with their documented reasons (`persistence-and-state-machines.md` §6);
- canonical records: the quarantine record, batch record, decision record, and full-reconciliation result JSON shapes are defined (`canonical-record-contracts.md` §7);
- error registry: `work_receipt_invalid` is emitted by the receipt CLI, `quarantine_not_found` joins the usage class (exit 4), and `quarantine_release_denied` documents the denied re-release; the structural holds are successful trigger outcomes (exit 0 with an explicit disposition envelope) and `unsafe_path_quarantined` (class 5) is reserved for a future durable-evidence policy;
- cli-spec: `route enable` requires the computed route revision acknowledgement value; the quarantine command surface (state filter, non-interactive requirements, release semantics) and the reconcile command match the shipped behavior;
- work-receipt schema: the persisted run keeps its begin timestamp (migration v4) so attribution windows survive terminal updates.

## 1.0.5 - 2026-08-20

E2 errata:

- extended the closed error-code registry with the Watchman lifecycle surface: `watchman_unavailable` and `watchman_version_unsupported` (`target_unavailable`, exit 11) and `watchman_trigger_conflict` (`conflict`, exit 14);
- recorded in architecture `watchman-integration.md` §2/§7 that `WATCHMAN_FILES_OVERFLOW` was refuted by the E0-T5 probe and the allowlist includes `WATCHMAN_SOCK`;
- the AC-102 drop reason is emitted as `unchanged_content` (matching the acceptance text) and the E2-T3 same-path coalescing rules are qualified for persisted-prior paths (a create observed over a prior digest is conservatively a modify).

## 1.0.4 - 2026-08-20

Implementation bootstrap errata (E1-T1):

- extended the closed error-code registry with the usage class (exit 2) required by the first real CLI surface: `command_unknown`, `command_not_implemented`, and `flag_invalid`, plus `internal_unclassified` (exit 40) for the panic-recovery path;
- repository-layout now records the importlint-enforced dependency rules and that SOT schemas/examples remain under `docs/schemas` and `docs/examples` until generated schemas exist.

## 1.0.3 - 2026-08-19

Design-review errata on 1.0.2 (see `docs/00-sot/decision-log.md`):

- the route failure budget is an explicit `failure_budget` field (1 through 10, required, revision-affecting), ending the `execution_hints.max_attempts` overload introduced in D-009; the three budgets are documented together in configuration-spec §9 (D-013);
- completed the partially applied 1.0.2 fixes: no remaining "trusted environment" label in the architecture overview, `SinkCapabilities` no longer embeds `maximum_request_bytes`, and `work-receipt.schema.json` enforces the closed failure-code set with a required non-null code on failed receipts (D-014).

## 1.0.2 - 2026-08-19

Multi-agent design-review errata (see `docs/00-sot/decision-log.md`):

- made policy-driven unsafe-path quarantine expressible in the closed error registry via `unsafe_path_quarantined` (exit class 5) (D-007);
- clarified decision lineage: a policy decision references either an immutable batch or a durable generation lineage, resolving the follow-up and reconciliation conflict with invariant 1 (D-008);
- specified route behavior when accepted work fails or is canceled: one bounded follow-up while the retry budget remains, operator resolution on exhaustion; added the `work fail` synopsis with a closed failure-code set and a quarantine-release exit edge in the route state machine (D-009);
- added E0-T5 (Watchman public-interface verification and fixture baseline) so E2-T1 builds on frozen evidence, assigned `init` to E1-T2 and route-management commands to E3-T3; the roadmap now has 33 tasks (D-010);
- documented that scheduled reconciliation adds `--submit` only after the production gate, and declared trigger-driven retry liveness intentional (D-011);
- bundled consistency errata across terminology, contracts, examples, and operations docs (D-012).

## 1.0.1 - 2026-08-19

Design-review errata and post-baseline decisions (see `docs/00-sot/decision-log.md`):

- made the Hermes logical task request single-sourced in the task contract, added `hermes-task-request.schema.json`, and constrained `dispatch-intent.request` with a `$ref` to it (D-001);
- enumerated error categories 1:1 with exit-code classes, closed the error code registry with per-code category/exit mapping, assigned the adapter error codes, and defined exit code 1 as never emitted with panic recovery to exit 40 (D-002);
- replaced the manually curated traceability matrix with output generated by `scripts/generate-traceability.py` (D-003);
- kept E3-T3 as a single task, to re-evaluate at E3 entry (D-004);
- fixed verified cross-document inconsistencies: `lost-cursor` reconcile reason, initial reconciliation type, `test/` in the repository tree, `SetRouteActivation` service placement, conditional `go test` in DoD, trigger-name convention in the config example, and JSON-output-contract deliverables for E3-T3/E5-T4 (D-005);
- kept the E5/E6 detail freeze with an explicit amendment path (D-006);
- added `dispatch-intent` and `dispatch-receipt` examples so every schema has a validating example.

## 1.0.0 - 2026-08-19

Initial approved SOT baseline.

Major resolutions from the discussion draft:

- narrowed the primary product identity from a broad control plane to an event-ingress and activation gateway;
- made Hermes the authoritative runtime;
- prohibited Hermes core modification, internal database access, and a Hermes plugin in v0.1;
- selected Watchman one-shot trigger mode as the initial execution model;
- selected Go, YAML, and SQLite as the implementation stack;
- made SQLite mandatory before the first external side effect;
- separated observations, batches, policy decisions, dispatch intents, attempts, and receipts;
- separated event identity, content fingerprint, idempotency key, and attempt identity;
- selected latest-state processing for the Obsidian vault use case;
- limited the certified v0.1 scope to Markdown changes in one vault and one primary Hermes Kanban target;
- added a route-level single-active-work rule with durable dirty-generation tracking;
- defined retry, reconciliation, reprocessing, rerun, and quarantine as distinct operations;
- scheduled the Hermes webhook adapter after the durable Kanban MVP;
- deferred daemon mode, MCP, multi-vault certification, generic adapters, and a Hermes plugin.

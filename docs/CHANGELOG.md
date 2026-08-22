# SOT Changelog

## 1.0.12 - 2026-08-22

E6 epic validation audit and closeout:

- every member-task hardening deferral was revalidated against its native Mulgae authority and the valid findings were remediated: the secretresolver fd cache serializes wrapper creation and reads seek-state-free through ReadAt with the deadline-bounded pipe one-shot fallback (the race-loser finalizer can no longer close the shared descriptor; concurrent-first-resolution and post-GC tests pin it), the webhook construction gate refuses idempotency-header collisions with the authentication and transport headers (four-case refusal test), the prune dry-run plan mirrors the executed cascade through one shared CTE chain (freed-chain dry-run test), doctor opens the store unmigrated so migration_pending is observable (downgrade regression test), and the documented global --state-dir and --timeout options are implemented with fail-closed validation, per-invocation reset, and store-context bounding (behavior tests);
- three whole-epic review rounds converged to r_01a027d2 returning reports_only with zero structured findings; the residual low/info items are the documented v0.1.0 hardening posture (release notes) or were fixed in place (the lock-name comment, the unreachable init branch, the timeout claim wording);
- the roadmap records the E6 epic Completed with the closeout narrative; the v0.1 sequence is complete (33/33 tasks, gates G0-G5 closed).

## 1.0.11 - 2026-08-22

E6-T4: v0.1.0 verification and release:

- the executable G5 acceptance suite closes the gate: AC-501 (webhook auth without persistence, transport-vs-durable distinction, no Kanban fallback), AC-502 (doctor's stable actionable findings), AC-503 (prune removes resolved expired data while unresolved lineage and the audit survive), AC-504 (the clean-host macOS install→validate→dry-run dispatch→gate-acknowledged enable→scheduled reconciliation→doctor flow without manual database edits), AC-505 (the Linux CI leg of make verify), and AC-506 (the release-way build with its version envelope and the full artifact set);
- the upgrade-and-backup rehearsal is executable: built-in backup with verification, doctor, full integrity, one reconciliation, and a standalone restore that carries the lineage;
- docs/VALIDATION.md gains the Gate G5 evidence table (G0–G5 now closed) and docs/RELEASE-NOTES-v0.1.0.md ships as the release notes artifact;
- the requirement traceability matrix regenerates with every requirement resolved to its owning and verifying tasks (33 tasks, 15 groups);
- compatibility is frozen and reported: config version 1, schema range 1-4, record payload versions, adapter profiles hermes 0.19.1 and watchman 2026.07.27.00;
- the roadmap records all 33 tasks Completed with the v0.1 sequence complete; the deferred future work stays apart (no partially enabled feature), the Hermes plugin remains absent, and production enablement stays the explicit computed-revision operator action.

Review round 1 remediations (all roles, reports_only):

- `agent-dispatch version` reports the delivered webhook adapter (its static-declaration HTTPS sink posture) instead of the stale "not-implemented" entry;
- doctor now satisfies AC-502's stable-nonzero contract: error-severity findings keep the structured stdout result while the command emits the new stable `doctor_findings_present` code (configuration, exit 3) — registry and cli-spec updated, and the affected tests pinned to the new shape;
- the AC-504 evidence runs the real production gate (`route enable --acknowledge-production-gate <computed> --yes`) instead of a direct store write, adds the documented uninstall ordering (route disable, trigger removal under Watchman, configuration and state retention), and the upgrade rehearsal enables the route through the same command with the computed revision;
- the AC-503 audit assertion proves monotonicity (exactly the prune's own audit row is added, nothing deleted), AC-501 asserts the durable intent's webhook target type, and AC-506 asserts the built binary reports v0.1.0 and lists the release-notes artifact;
- the Gate G5 header names all four evidence files, and the installation guide documents the restore procedure the release notes reference.

Round 2 remediations (all roles, reports_only):

- the doctor emission tail is unified (one writer computes the error-finding count in both branches), the config-load failure produces exactly the configuration finding set with nothing fabricated, and the doc comment states the nonzero contract;
- the version command's adapter map is regression-pinned (no not-implemented markers; the hermeswebhook entry names its delivered HTTPS-sink posture);
- the completeness test sandbox resets the XDG variables too.

## 1.0.10 - 2026-08-22

E6-T3: packaging and scheduled reconciliation (SCP-008, OPS-006, OPS-007, OPS-009):

- `make release VERSION=v0.1.0` builds byte-reproducible cross-platform binaries (darwin/arm64, linux/amd64) with `-trimpath`, the full release commit hash, and the commit's committer date as the build time, and emits a portable `LC_ALL=C`-sorted `SHA256SUMS` over the binaries under `dist/`;
- the scheduling examples exist and are validated: the launchd LaunchAgent plist (`plutil -lint` on macOS), the systemd --user service and timer (`systemd-analyze verify` on Linux), and the uninstall script (`sh -n`), wired as `make schedule-check` inside `make verify` so each CI platform lints its own artifact (SCP-008, where possible); both schedules invoke the verified `reconcile --reason scheduled` one-shot shape with no daemon, omitting `--submit` before the production gate;
- `agent-dispatch completion bash|zsh` emits the static v0.1 command-tree completion, completing the registered CLI tree;
- `agent-dispatch maintenance backup --output <path>` writes the runbook §8 built-in backup: an owner-only `VACUUM INTO` snapshot with a post-write quick check that refuses to overwrite (cli-spec §11 updated);
- `docs/docs/05-operations/installation.md` documents the install, platform config/state paths (macOS and XDG Linux), first-use clean-host scenario, daily scheduling, upgrade, backup, and uninstall procedures;
- the uninstall example follows runbook §10 and retains SQLite and configuration by design — `--purge-state` only prints the manual backup guidance; the acceptance lines are pinned by tests (clean-host init through the default paths with owner-only perms and idempotent refusal, backup standalone-open and integrity, schedule shapes, no recursive deletion);
- the Linux CI leg of `make verify` (existing ubuntu-latest matrix) validates the binary, configuration, SQLite, and — with this change — the systemd unit syntax.

Review round 1 remediations (all roles, reports_only):

- the release ships byte-reproducible binaries with checksums over the binaries themselves (Go `-trimpath` plus the commit-pinned version, commit, and build time; verified by two consecutive `make release` runs producing identical SHA-256 digests) — the earlier tar archives embedded machine-specific metadata and a stale-checksum edge, so the archive layer is gone, `make clean` removes `dist/`, and the checksum list sorts under `LC_ALL=C`;
- `maintenance backup` refuses an existing target with the new stable `backup_target_exists` code (conflict, exit 14) before opening the store, logs the dedicated `maintenance.backed_up` event instead of the vacuum event, and its failure shapes are test-pinned alongside the missing-flag usage;
- the completion vocabulary derives from the registered command registry at run time (the duplicated list is gone), the no-op zsh transform is removed, and the registry-equals-vocabulary invariant is pinned by a test;
- the cli-spec §2 command tree lists `maintenance backup`; the launchd example drops its fixed `/tmp` error path; the bare-command completeness test regained its teeth (each registered command must reach its own handler with its own error class, never `command_not_implemented` or `command_unknown`).

Review round 2 remediations (all roles, reports_only):

- the release documentation matches the remediated shape everywhere: the installation guide and the changelog headline describe the byte-reproducible binaries with binary-level checksums (no tar layer), the full commit hash is stamped for builder-independent bytes, and the checksum verification line runs from `dist/` as written;
- the backup target guard uses `Lstat`, so a symlink at the target — dangling or not — is itself the `backup_target_exists` conflict and the snapshot can never land through a link the backup path did not create; a failed snapshot removes its partial file so the operator's retry is not misclassified as a conflict; the storage-failure branch and the no-artifact-on-failure property are test-pinned;
- the completion invariant test parses the emitted bash word list back out and compares it set-for-set with the registry (the earlier derivation-based test was tautological); the completeness test sandboxes `HOME` so a bare `init` cannot touch the developer's configuration, allows exactly the three legitimately-succeeding bare commands, and excludes `command_unknown` fallthrough;
- the launchd example's fixed `/tmp` path removal is pinned by a regression assertion; `make schedule-check` lost its dead success flag; the stale dispatcher comments now describe the fully implemented tree.

Round 3 remediations (authorized extra round; all roles, reports_only):

- the scheduling examples' invocation actually runs: the documented global `--output json` option is accepted across the dispatches command family (reconcile included — the high-severity finding that would have failed every scheduled run), pinned by a test driving the example's exact arguments;
- the uninstall example no longer passes the undocumented `--yes` to `route disable`;
- the Lstat symlink guard, the `maintenance.backed_up` event, the zsh completion header, and the launchd `/tmp` exclusion all gained real assertions;
- `make release` warns when git metadata is absent instead of silently stamping `unknown`.

## 1.0.9 - 2026-08-22

E6-T2: doctor, status, retention, and operational observability (OPS-001..005, OPS-008, CLI-004, CLI-007, SEC-007):

- the structured operational log exists (`internal/observability`): one JSON object per line on stderr, the stable §3 event-name vocabulary, the §2 causal correlation fields, the §4 level semantics, and the path-privacy policy (relative, redacted with stable digests, full); note bodies, credentials, and authorization material never survive redaction, and the default warn level keeps successful one-shot commands quiet (CLI-002). The global `--log-level` and `--trace-id` options shape it, and the dispatch runtime emits the attempt-started and classified-outcome lifecycle events with causal IDs;
- `agent-dispatch status` reports route active/dirty state with queue counts, unresolved delivery, quarantine, the oldest unresolved record, the database size, and the offline target capability summary (the webhook's static declaration and the kanban frozen report), warning on dirty routes, pending reconciliation, held quarantine, and delivery uncertainty;
- `agent-dispatch doctor` examines configuration validity, resource roots (existence, readability, owner-only posture), the durable store (open failure, WAL journal mode, quick/integrity check, schema currency), Watchman presence and version, per-target construction gates, secret-reference resolvability without printing values, stale attempt leases, unknown and dead-lettered dispatches, stale active routes, and never-run daily reconciliation — findings carry stable code, severity, summary, details, and remediation, are data on stdout (exit 0), and emit doctor.finding log events at their severities;
- the retention planner and prune are implemented (`internal/app/maintenance` policy resolution with the OPS-003 defaults and configured overrides; `maintenance prune` computes per-class cutoffs children-first): dry-run by default, `--yes` executes in one transaction with foreign keys enforced, `--before` only narrows horizons, unresolved lineages (unknown, retry_wait, dead_lettered, submitting, reconciling) survive intact with their ancestry, state-transition audit rows are never pruned, and the actor, policy cutoffs, and counts are recorded in the append-only audit;
- `maintenance vacuum` requires `--yes` (CLI-007) and refuses with the new stable `maintenance_active_work` code (conflict, exit 14) while any intent is submitting or holds an unexpired lease; `maintenance integrity` reports the check mode and schema currency;
- stale findings are first-class: expired attempt leases, stale active routes against active_stale_after, unknown/dead-lettered counts, and overdue reconciliation surface in both doctor and status;
- the runbook's routine inspection commands (`status`, `doctor`, `doctor --probe-targets`) now exist and are exercised end to end by the CLI suite.

Review round 1 remediations (all roles, reports_only):

- `doctor --integrity full` is reachable (the flag parses as a value option), `--output=json` and boolean equals-forms parse for every ops command, `vacuum --dry-run` is rejected as the contradiction it is, and an invalid `--log-level` fails closed instead of being silently ignored;
- the prune execution guards every foreign-key referencer the plan can meet: held or unresolved quarantine blocks its decision and batch, work receipts outside retention block their intent, and the route's active slot never loses its dispatch; the dry-run plan now mirrors the execution's cascade (decisions freed by pruned intents free batches, which free observations), so the counts cannot diverge;
- `maintenance prune --reason` is recorded in the append-only audit beside the actor, cutoffs, and counts;
- the kanban capability-report path is passed through verbatim like every other consumer (the divergent config-directory resolution rule is gone);
- `doctor --probe-targets` no longer duplicates a target's offline gate failure, a configuration failure no longer fabricates `sqlite_open_failed`, the stale-active age comes from the last completed attempt (lease expiry as fallback), and overdue daily reconciliation (over 25 hours) joins the never-run finding;
- the unknown-delivery lifecycle event logs at WARN per observability §4 (recoverable uncertainty), and log redaction recurses into nested map and list payloads;
- tests pin the retention policy resolution with `--before` narrowing, every doctor finding code with severity and remediation, the prune audit row with actor and reason, plan-versus-execution consistency, held-quarantine lineage preservation, the doctor option surface, and the fail-closed log level;
- docs: configuration-spec §10 states that v0.1 prune resolves the instance-level policy (route-level retention is reserved), and retention-and-privacy §4 names the instance-level `log_paths` policy.

## 1.0.8 - 2026-08-22

E6-T1: the explicit Hermes webhook adapter (contract addition, WHK-001..005, SEC-006, SEC-007):

- the `hermes-webhook` target is wired end to end: `resolveSink` constructs the adapter after its fail-closed gates (https endpoint, bearer/header authentication shape, valid header names, HER-005 required-capability validation against the static declaration), and the unwired-adapter error is gone;
- the capability declaration is static and offline, derived from the frozen E0-T4 §9 evidence (inbound-only receiving platform, not enabled): `durable_acceptance` false — a 2xx is transport acceptance only, never durable (WHK-004) — `submit_idempotency_key` true (the core's key transmitted verbatim under the configured header, default `Idempotency-Key`, WHK-005), every lookup and projection unsupported and never emulated, `maximum_request_bytes` 262144 enforced before transmission;
- the structured HTTP client is the repository's first: one end-to-end submit deadline (default 30s), TLS 1.2+ with system roots and no bypass, no proxy, redirects disabled (an unfollowed 3xx is a definite routing rejection), and a conservative response mapping — definite refusal statuses (400/401/403/404/405/406/410/413/414/415/422 and 3xx) reject; 408/409/429/5xx stay unknown; transport failures provably before transmission (resolution, dial, handshake) are definite non-submission, everything after possible transmission unknown;
- authentication secrets resolve immediately before each submission through the new `secretresolver` adapter (env, file, fd, and the controlled macOS keychain lookup; argv-only, no environment, bounded) and never enter SQLite or logs (SEC-006); every captured response byte is redacted against the resolved secret (SEC-007);
- webhook intents record the endpoint URL as their durable target scope (the analog of the kanban board) at every intent-construction site, so drain reconciliation re-verifies the accepting identity;
- `config validate --probe-targets` reports webhook targets with their declared capabilities and no endpoint network I/O; unknown webhook dispatches dead-letter through drain (no lookup exists) and never fall over to another sink (WHK-002, DUR-008);
- config schema: the `hermesWebhook` target accepts optional `required_capabilities` (mirroring hermes-kanban), and semantic validation enforces the authentication shape (`header` requires `header_name`, `bearer` rejects it);
- docs: hermes-integration §10 carries the declaration table and response mapping; configuration-spec §5 documents the webhook fields and defaults; the frozen capability report gains the append-only E6-T1 derivation note.

Review round 1 remediations (all roles, reports_only):

- `fd:` secret references now survive repeated resolution in one process (the WHK-005 retry posture): the read rewinds a seekable descriptor and loops to EOF so a chunked writer cannot silently truncate the credential, while the descriptor stays open because the launching process owns it;
- every secret kind enforces the 64 KiB bound, and the keychain subprocess output is bounded to it (the comment's "bounded output" claim is now true) with the controlled invocation pinned by a stub test (argv, no inherited environment, bounded failure detail);
- a webhook `submit_timeout` that is schema-pattern-valid but unparseable (int64 overflow) is a configuration failure (exit 3) at dispatch time, matching `config validate --probe-targets`;
- endpoints embedding URL userinfo are rejected at construction (the userinfo would otherwise persist verbatim as the durable target scope and could surface as a Basic authorization header — SEC-006);
- a resolved secret containing characters invalid in a header value is refused before transmission, and transport-failure diagnostics are redacted against the resolved secret (SEC-007);
- a response body that dies mid-stream marks its captured evidence truncated — partial evidence is never presented as complete;
- tests pin the semantic auth-shape gates (the only bearer-shape enforcement), the redirect single-delivery guarantee, the mid-body truncation marker, the userinfo and negative-timeout construction gates, and the probe surface's config_error and capability_mismatch exits;
- docs: the secretresolver package comment no longer claims to be a skeleton, README's project status records E6-T1 complete, and the capability-report note names the public interface report instead of "this report".

Review round 2 remediations (all roles, reports_only):

- `fd:` references cache one `*os.File` wrapper per descriptor for the process lifetime — a transient `os.NewFile` wrapper would be finalized closed by the runtime after a GC cycle, nondeterministically closing the launching process's descriptor (and a later fd-number reuse could resolve an unrelated stream as the credential); the survival is pinned by a GC-forcing test;
- the keychain lookup captures stderr separately from stdout: keychain notices no longer concatenate into the resolved credential (a corrupted token would have surfaced as an undiagnosable permanent 401), and the separation is pinned by a stub that writes to both streams;
- the response-body capture never fabricates adapter text as endpoint evidence: a read error (zero-byte or mid-stream) leaves the body empty or partial, marks the capture truncated, and records the redacted read error explicitly; the oversize capture takes the same shape;
- `file:` and `fd:` reads run under the caller's context with a 10-second default deadline and the 64 KiB bound applied during the read, so a wedged or oversized source can neither hang nor over-allocate a submission; a consumed pipe-backed descriptor reports its one-shot cause distinctly;
- the 64 KiB bound, the dispatch-surface exit-3 timeout classification, transport-diagnostic redaction, the zero-byte capture shape, and the rerun path's endpoint target scope all gained pinning tests (the round-1 test gaps);
- the webhook probe surfaces a Probe failure as config_error instead of reporting empty capabilities.

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

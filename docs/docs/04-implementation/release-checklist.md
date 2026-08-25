# v0.1.4 Release Checklist

> Operated 2026-08-25 for the reopened E9 remediation release (D-023,
> closed by D-024). The v0.1.1-era basis (33/45 task counts, the open
> SCP-008 exception) was finding F5 of the 2026-08-25 review; this pass
> rewrites the checklist against the current 60-task, macOS-only,
> toolchain-enforced state. The earlier v0.1.1 record stands as history.

## SOT and Roadmap

- [x] Every roadmap task E0-T1 through E9-T9 is Completed (verified 2026-08-25; 10 epics, 60 tasks, 60/60).
- [x] No task is In Progress, In Review, or Blocked (epic E9 is re-closed by D-024; the roadmap's current epic is None).
- [x] Required specification and implementation are reconciled (every MUST is implemented, carries a recorded supersession (SCP-008 under D-023), or stands as a maintained exception).
- [x] Traceability contains evidence for every MUST (regenerated for 60 tasks; the D-023 finding dispositions are recorded in D-023/D-024).
- [x] Accepted ADRs match the implementation (per-task Mulgae rounds and the epic validation audit own the residual drift).
- [x] Future work is not partially enabled (the deferred list stands apart).

## Build and Supply Chain

- [x] Go toolchain and dependencies are pinned (go 1.26.6; staticcheck as a tool dependency, SCP-005); `make verify` and `make release` enforce the exact pin through `go-version-check` before any build step (E9-T7).
- [x] Clean reproducible builds pass on macOS (darwin/arm64 — the only supported platform under the D-023 policy; the earlier Linux-build line is superseded history).
- [x] Binaries and checksums are generated (make release; the byte-reproducibility double build is verified for v0.1.4 by E9-T9).
- [x] Dependency/license review is complete (dependency-licenses.md, all 39 modules in the build graph: MIT, BSD, Apache, and MPL-2.0 tool-chain only, all compatible).
- [x] Build version, commit, and schema ranges are embedded (`version --output json`).

## Tests

- [x] Unit, component, integration, race, multi-process, and crash tests pass (make verify including -race on darwin/arm64, the only supported platform).
- [x] JSON Schemas parse and examples validate (make schema-validation, 12 schemas).
- [x] All G0-G5 acceptance scenarios pass (re-verified on the reopened delta 2026-08-25; AC-505's Linux-host scenario is superseded by D-023 with its D-020 closure standing as history).
- [x] Real Watchman test passes (2026.07.27.00).
- [x] Real disposable Hermes Kanban test passes (0.19.1, disposable boards; under an installed Hermes outside the verified set the real-environment legs skip as TST-007 evidence gaps — E9-T6).
- [x] Webhook fake/contract tests pass (TLS conformance suite).
- [x] No production vault was used for destructive tests.

## Security and Privacy

- [x] Path traversal and symlink escape tests pass (G1 AC-106).
- [x] No shell interpolation exists (argv-only runners).
- [x] Secret redaction tests pass (key and value-pattern coverage, E7-T9).
- [x] Note body is absent from SQLite and normal logs.
- [x] Config and state permissions are documented and checked (owner-only state; permissive file secrets fail closed).
- [x] Hermes adapter uses only public interfaces.
- [x] No Hermes plugin or internal DB access exists.

## Durability

- [x] SQLite settings are verified at runtime (pragma checks at open).
- [x] Database backup/restore rehearsal passes (TestG5UpgradeAndBackupRehearsal).
- [x] Migration interruption test passes (before, between, and inside units, E7-T4).
- [x] Remote-acceptance crash window reconciles safely (dedup-safe recovery, E7-T4).
- [x] Concurrent one-shot processes cannot duplicate attempt ownership (AC-204).
- [x] Unknown acceptance never triggers webhook fallback.

## Feedback Loop

- [x] One active route task invariant passes (transactional slot enforcement, E7-T2).
- [x] Dirty bursts collapse into at most one follow-up (product-path activation, E7-T2).
- [x] Exact work receipt suppression passes.
- [x] Mixed human/agent change remains dirty.
- [x] Missing receipt is conservative and bounded.
- [x] Protected and overflow cases do not enter ordinary automatic tasks.

## Operations

- [x] `doctor`, `status`, inspection, retry, reprocess, rerun, discard, reconcile, quarantine, and maintenance commands work (E7-T5/E7-T7 completed the surface).
- [x] Retention dry-run and prune preserve unresolved lineage (and begun receipts while their dispatch is unresolved or holds the active slot; begun receipts prune with a terminal lineage past retention, E9 epic validation round-1 F001).
- [x] Watchman install/status/remove is idempotent.
- [x] The `launchd` scheduled reconciliation example and the uninstall script are tested (make schedule-check on macOS; the systemd examples are retired under D-023).
- [x] Upgrade and uninstall procedures are documented.

## Artifacts

- [x] Binary archives and checksums (make release, SHA256SUMS).
- [x] SOT documentation (manifest-verified).
- [x] Schemas and examples.
- [x] Default disabled config (two-key gate enforced, E7-T6).
- [x] Verified Hermes capability report template and compatibility documentation.
- [x] Hermes companion skill.
- [x] Changelog and release notes (the v0.1.1 notes disclose the Linux exception — historical; releases from v0.1.4 on are darwin/arm64-only under D-023).
- [x] Acceptance reports (docs/VALIDATION.md; the compliance-review findings live in the decision log's D-017/D-020/D-023 records).

## Planned v0.1.5 Addendum

The checked items above are v0.1.4 release history. E13-T4 may check the items
below only after G6 through G9 carry executable evidence:

- [ ] Effective nested Watchman binding and complete managed-trigger removal.
- [ ] Reconciliation fence and bounded-growing-file evidence.
- [ ] Frozen real Hermes 0.19.1 plus installed newer-version probe evidence,
      with no Hermes source/private-state modification.
- [ ] Destination config/schema migration, multi-profile/workstream fan-out,
      independent retry, and work-receipt/v2 evidence.
- [ ] Notification outbox, webhook/log sinks, dedup, retry, and redaction.
- [ ] Versioned operator and worker skills plus isolated operational walkthrough.
- [ ] SOT/roadmap/VALIDATION/release-note truth synchronized at 75/75.
- [ ] Two byte-identical darwin/arm64 builds and local v0.1.5 tag; no push or
      production activation.

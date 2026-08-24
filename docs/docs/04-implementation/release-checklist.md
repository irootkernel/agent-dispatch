# v0.1.1 Release Checklist

> Operated 2026-08-23 for the E7 remediation release (D-017). The v0.1.0
> checklist was never operated; its unchecked state was finding M-31.
> This pass checks every item against the v0.1.1 state with notes where
> the honest answer is narrower than the item's literal text.

## SOT and Roadmap

- [x] Every roadmap task E0-T1 through E6-T4 is Completed (verified 2026-08-23; the E7 tasks are tracked separately).
- [x] No task is In Progress, In Review, or Blocked (the E7 remediation sequence is the active work and is tracked in the task index).
- [x] Required specification and implementation are reconciled (E7-T1 restored the documentation truth; every MUST is implemented or carries the recorded SCP-008 exception).
- [x] Traceability contains evidence for every MUST (regenerated for 45 tasks; the MUST-closure matrix is recorded in VALIDATION by E7-T12).
- [x] Accepted ADRs match the implementation (per-task Mulgae rounds and the epic validation audit own the residual drift).
- [x] Future work is not partially enabled (the deferred list stands apart).

## Build and Supply Chain

- [x] Go toolchain and dependencies are pinned (go 1.26.6; staticcheck as a tool dependency, SCP-005).
- [x] Clean reproducible builds pass on macOS (darwin/arm64); Linux builds are reproducible but unverified at runtime (the recorded SCP-008 exception, D-017).
- [x] Binaries and checksums are generated (make release; the byte-reproducibility double build is re-verified by E7-T12).
- [x] Dependency/license review is complete (dependency-licenses.md, all 39 modules in the build graph: MIT, BSD, Apache, and MPL-2.0 tool-chain only, all compatible).
- [x] Build version, commit, and schema ranges are embedded (`version --output json`).

## Tests

- [x] Unit, component, integration, race, multi-process, and crash tests pass (make verify including -race on darwin/arm64; the Linux leg carries the recorded exception).
- [x] JSON Schemas parse and examples validate (make schema-validation, 12 schemas).
- [x] All G0-G5 acceptance scenarios pass (re-verified 2026-08-23; AC-505 carries the recorded Linux exception).
- [x] Real Watchman test passes (2026.07.27.00).
- [x] Real disposable Hermes Kanban test passes (0.19.1, disposable boards).
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
- [x] `launchd` and `systemd --user` scheduled reconciliation examples are tested (make schedule-check on each platform; systemd verified where the tool exists).
- [x] Upgrade and uninstall procedures are documented.

## Artifacts

- [x] Binary archives and checksums (make release, SHA256SUMS).
- [x] SOT documentation (manifest-verified).
- [x] Schemas and examples.
- [x] Default disabled config (two-key gate enforced, E7-T6).
- [x] Verified Hermes capability report template and compatibility documentation.
- [x] Hermes companion skill.
- [x] Changelog and release notes (the v0.1.1 notes disclose the Linux exception).
- [x] Acceptance reports (docs/VALIDATION.md; the archived compliance review under docs/reports/).

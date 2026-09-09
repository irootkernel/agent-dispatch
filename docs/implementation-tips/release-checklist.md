# v0.1.6 Release Checklist (Historical Record)

This is evidence for the dated v0.1.6 release below, not a checklist already
passed by the current HEAD. Use the [release guide](release-guide.md) for a new
candidate. Original checked items retain their historical scope.

> Operated 2026-09-02 for the E17 release of the v0.1.6 operational
> follow-up (D-027). This pass rewrites the checklist against the
> current 89-task state: the checked sections below name the standing
> release verities, each carried by the named gate evidence; the
> v0.1.4/v0.1.5 records stand as history beneath them.

## SOT and Roadmap

- [x] Every roadmap task E0-T1 through E17-T3 is Completed (verified 2026-09-02; 18 epics, 89 tasks, 89/89 — the roadmap closes).
- [x] No task is In Progress, In Review, or Blocked.
- [x] Required specification and implementation are reconciled (E17-T1 synchronized the public operator contract; every MUST is implemented or carries a recorded supersession/exception).
- [x] Traceability contains evidence for every MUST (regenerated for 89 tasks through `make traceability`).
- [x] Accepted ADRs match the implementation (ADR-0020 through ADR-0022 deliver their contracts; per-task Mulgae rounds and the epic validation audit own the residual drift).
- [x] Future work is not partially enabled (the deferred list stands apart).

## Build and Supply Chain

- [x] Go toolchain and dependencies are pinned (go 1.26.6; staticcheck as a tool dependency, SCP-005); `make verify` and `make release` enforce the exact pin through `go-version-check` before any build step.
- [x] Clean reproducible builds pass on each supported platform (`darwin/arm64`, `linux/amd64`, and `linux/arm64` under the D-029 policy; three-arch `make release` packaging delivered by E19-T4).
- [x] Binaries and checksums are generated with the byte-reproducibility double build verified for v0.1.6 (two consecutive `make release VERSION=v0.1.6` builds byte-identical on the tagged implementation tree; the published SHA256SUMS carries the final artifact digest).
- [x] Dependency/license review is complete (dependency-licenses.md; no new dependencies since the v0.1.5 review).
- [x] Build metadata remains embedded while the public identity is compact (`version`: `agent-dispatch 0.1.6`; `version --json`: exactly `name` and `version: v0.1.6`; schema range 1-20 remains internally drift-checked).

## Tests

- [x] Unit, component, integration, race, multi-process, and crash tests pass (`make verify` including -race on darwin/arm64, fully green on the final tree).
- [x] JSON Schemas parse and examples validate (make schema-validation, 18 schemas).
- [x] All G0-G13 acceptance scenarios pass (the G10-G12 suites re-ran green in the E17-T2 cold validation; G13's rows live in VALIDATION.md).
- [x] Real Watchman test passes (2026.07.27.00).
- [x] Real disposable Hermes Kanban test passes (0.20.5; the E17-T2 cold validation additionally walked the real launchd and trigger surfaces on disposable state).
- [x] Webhook fake/contract tests pass (TLS conformance suite).
- [x] No production vault, board, route, or profile was used for any test.

## Security and Privacy

- [x] Path traversal and symlink escape tests pass (G1 AC-106).
- [x] No shell interpolation exists (argv-only runners; the managed plist invokes the internal runner directly with XML-escaped interpolation).
- [x] Secret redaction tests pass (key and value-pattern coverage).
- [x] Note body is absent from SQLite, normal logs, and notification payloads (the E17-T2 webhook legs verified the sanitized posture).
- [x] Config and state permissions are documented and checked (owner-only state; permissive file secrets fail closed).
- [x] Hermes adapter uses only public interfaces; no Hermes plugin or internal DB access exists (re-verified by the cold validation's boundary).

## Durability

- [x] SQLite settings are verified at runtime (pragma checks at open).
- [x] Database backup/restore rehearsal passes (TestG5UpgradeAndBackupRehearsal).
- [x] Migration interruption tests pass through the v20 ledger (the heal harnesses updated with the E17-T2 batch).
- [x] Remote-acceptance crash window reconciles safely; the notification drain crash recovery is proven at the store level and re-verified in the real environment (kill -9 mid-attempt, fenced recovery after lease expiry).
- [x] Concurrent one-shot processes cannot duplicate attempt ownership or notification claims (disjoint leases, fenced outcomes).
- [x] Unknown acceptance never triggers webhook fallback.

## Feedback Loop

- [x] One active route task invariant passes per serialization group (group-held exclusion re-verified in the real environment).
- [x] Dirty bursts collapse into the lane's generation; acknowledged independent groups run concurrently.
- [x] Exact work receipt suppression passes; the four-outcome receipt drives per-lane completion.
- [x] Protected and overflow cases do not enter ordinary automatic tasks.

## Operations

- [x] The full operator surface works (the E17-T1 contract truth and the E17-T2 real-environment walkthrough).
- [x] Retention dry-run and prune preserve unresolved lineage.
- [x] Watchman install/status/remove is idempotent (real-trigger lifecycle re-verified).
- [x] The managed launchd schedule lifecycle works on the real session (install/inspect/disable/uninstall with the durable `--at` timing; `make schedule-check` lints the shipped recipe).
- [x] Upgrade and rollback procedures are documented (runbook §9a/§9b through migrations v18-v20).

## Artifacts

- [x] Binary archives and checksums (make release, SHA256SUMS).
- [x] SOT documentation (manifest-verified; SOT 1.3.0).
- [x] Schemas and examples.
- [x] Default disabled config (two-key gate enforced).
- [x] Verified Hermes capability evidence and compatibility documentation (probe contract v3).
- [x] Versioned skills (operator 2.1.0 / worker 1.2.0, agreeing on v0.1.6).
- [x] Changelog and release notes (v0.1.6).
- [x] Acceptance reports (docs/VALIDATION.md including gate G13; the cold-validation evidence record).

## v0.1.4 Record (history)

The v0.1.4 checklist basis (operated 2026-08-25 for the reopened E9
remediation release, D-023 closed by D-024; the v0.1.1-era 33/45 basis
was finding F5 of the 2026-08-25 review) is preserved by Git history;
its checked items are subsumed by the standing sections above.

## v0.1.5 Addendum (closed, history)

- [x] Effective nested Watchman binding and complete managed-trigger removal (E10).
- [x] Reconciliation fence and bounded-growing-file evidence (E10).
- [x] Frozen real Hermes 0.20.5 plus installed newer-version probe evidence (E11).
- [x] Destination config/schema migration, multi-profile/workstream fan-out, and work-receipt/v2 evidence (E11/E12).
- [x] Notification outbox, webhook/log sinks, dedup, retry, and redaction (E13).
- [x] Versioned operator and worker skills plus isolated operational walkthrough (E13-T3).
- [x] SOT/roadmap/VALIDATION/release-note truth synchronized at 75/75 (SOT 1.1.18).
- [x] Two byte-identical darwin/arm64 builds; published 2026-08-30 (candidate re-cut from the post-validation final tree, `main` fast-forwarded, the `v0.1.5` tag pushed, the hosted Release created with artifact and SHA256SUMS; no production activation).

## v0.1.6 Addendum (closed)

The G10 through G13 evidence E17 closed the v0.1.6 release with:

- [x] Route-correct rerunnable setup, disabled baseline-only
      reconciliation, and the five-state gate (E14, G10).
- [x] Certified serialization modes, local serialization groups, the
      explicit 0.20.5 floor, and the real-Hermes G11 walkthrough (E15).
- [x] Drain policies, lease-safe delivery, post-commit after-command
      draining, and the managed launchd scheduler (E16, G12).
- [x] Documentation truth, the cold validation with its
      real-environment evidence record and three closed real defects,
      and the reproducible release proof (E17, G13).
- [x] SOT/roadmap/VALIDATION/release-note truth synchronized at 89/89
      (this release; SOT 1.3.0).
- [x] Two byte-identical darwin/arm64 builds from the tagged
      implementation tree, including every audit remediation and the
      compact version interface; no production activation.

# v0.1 Release Checklist

## SOT and Roadmap

- [ ] Every roadmap task E0-T1 through E6-T4 is Completed.
- [ ] No task is In Progress, In Review, or Blocked.
- [ ] Required specification and implementation are reconciled.
- [ ] Traceability contains evidence for every MUST.
- [ ] Accepted ADRs match the implementation.
- [ ] Future work is not partially enabled.

## Build and Supply Chain

- [ ] Go toolchain and dependencies are pinned.
- [ ] Clean reproducible builds pass on macOS and Linux.
- [ ] Binaries and checksums are generated.
- [ ] Dependency/license review is complete.
- [ ] Build version, commit, and schema ranges are embedded.

## Tests

- [ ] Unit, component, integration, race, multi-process, and crash tests pass.
- [ ] JSON Schemas parse and examples validate.
- [ ] All G0-G5 acceptance scenarios pass.
- [ ] Real Watchman test passes.
- [ ] Real disposable Hermes Kanban test passes.
- [ ] Webhook fake/contract tests pass.
- [ ] No production vault was used for destructive tests.

## Security and Privacy

- [ ] Path traversal and symlink escape tests pass.
- [ ] No shell interpolation exists.
- [ ] Secret redaction tests pass.
- [ ] Note body is absent from SQLite and normal logs.
- [ ] Config and state permissions are documented and checked.
- [ ] Hermes adapter uses only public interfaces.
- [ ] No Hermes plugin or internal DB access exists.

## Durability

- [ ] SQLite settings are verified at runtime.
- [ ] Database backup/restore rehearsal passes.
- [ ] Migration interruption test passes.
- [ ] Remote-acceptance crash window reconciles safely.
- [ ] Concurrent one-shot processes cannot duplicate attempt ownership.
- [ ] Unknown acceptance never triggers webhook fallback.

## Feedback Loop

- [ ] One active route task invariant passes.
- [ ] Dirty bursts collapse into at most one follow-up.
- [ ] Exact work receipt suppression passes.
- [ ] Mixed human/agent change remains dirty.
- [ ] Missing receipt is conservative and bounded.
- [ ] Protected and overflow cases do not enter ordinary automatic tasks.

## Operations

- [ ] `doctor`, `status`, inspection, retry, reprocess, rerun, reconcile, quarantine, and maintenance commands work.
- [ ] Retention dry-run and prune preserve unresolved lineage.
- [ ] Watchman install/status/remove is idempotent.
- [ ] `launchd` and `systemd --user` scheduled reconciliation examples are tested.
- [ ] Upgrade and uninstall procedures are documented.

## Artifacts

- [ ] Binary archives and checksums.
- [ ] SOT documentation.
- [ ] Schemas and examples.
- [ ] Default disabled config.
- [ ] Verified Hermes capability report template and compatibility documentation.
- [ ] Hermes companion skill.
- [ ] Changelog and release notes.
- [ ] Acceptance reports.

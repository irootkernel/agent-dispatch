# Changelog

This file records concise shipped outcomes and pending changes.

## Unreleased

## v0.1.8 - 2026-09-10

### Added

- Support `darwin/arm64`, `linux/amd64`, and `linux/arm64` with matching release
  artifacts and managed systemd user schedules (D-029, E19, G15).

### Changed

- Use XDG config/state paths and env/file/fd secrets on Linux, keep keychain
  macOS-only, and accept the official Linux Watchman version-output dialect.

### Fixed

- Render systemd execution arguments and log paths safely when they contain
  dollar signs, percent specifiers, whitespace, quotes, or backslashes.
- Require systemd timers to be both enabled and active, propagate failed enable
  operations, and preflight both managed units before uninstall side effects.

## v0.1.7 - 2026-09-09

### Changed

- Report `v0.1.7` by default and include the `v` prefix in both human and JSON version output.
- Reorganize the README for users and `docs/` for contributors and operators.
- Consolidate product release history in root `CHANGELOG.md` and keep specification
  history separately in `docs/SOT-CHANGELOG.md`.

### Fixed

- Accept Hermes Git-install version output with provenance decorations.
- Bind Watchman triggers to the configured absolute vault root and reject ancestor
  bindings with recovery guidance. Existing ancestor-bound installations require
  an explicit re-binding before resuming delivery.

## v0.1.6 - 2026-09-02

### Added

- Add automatic notification draining with manual, after-command, and scheduled
  policies, persistent retry backoff, and lease-safe concurrent delivery.
- Add managed launchd schedules with render, install, inspect, disable, and uninstall
  commands; require a matching schedule before enabling automatic draining.
- Add local serialization groups and capability-driven support for Hermes 0.20.5
  or newer, including installations without the optional mutex flag.

### Changed

- Simplify `version` and `version --json` to product name and version; remove the
  former detailed `version --output json` interface.
- Advance the database through schema 20 with forward-only migrations. Upgrades
  require backup, fresh capability probes, preflight, and route acknowledgement;
  rollback restores the matching database, configuration, and binary together.

### Fixed

- Make guided setup route-specific and rerunnable, record the disabled initial
  baseline, and show the five activation states before explicit enablement.
- Fence notification outcomes by lease ownership and keep draining failures from
  changing the successful command's output or exit status.
- Correct launchd invocation and refuse dispatch on an unregistered route cleanly.
- Document absolute Hermes executable paths for Watchman's minimal environment.

## v0.1.5 - 2026-08-30

### Added

- Add guided wiki setup and public Hermes capability, profile, and skill preflight.
- Add multiple destinations per source event with independent workstreams, retries,
  and completion tracking on one Hermes board.
- Add work receipt v2 with completed, partial, blocked, and failed outcomes.
- Add durable log/webhook notifications with inspection, testing, retry, and drain
  commands, plus operator and worker skill updates.

### Changed

- Replace single-target route configuration with `destinations[]` and include
  destination behavior in route revisions.

### Fixed

- Fence reconciliation against newer observations and bound reads of growing files.
- Scope destination exclusions to the configured resource and preserve pending
  notification delivery under stable idempotency keys.

## v0.1.4 - 2026-08-25

### Changed

- Restrict supported releases to macOS arm64 and retire Linux artifacts and systemd
  scheduling examples.
- Enforce Go 1.26.6 before verification or release builds.

### Fixed

- Include webhook authentication, idempotency, capability, and reconciliation settings
  in route revisions. Existing routes require one fresh acknowledgement after upgrade.
- Require capability evidence at route enablement, including when the target
  executable is unreachable.
- Reject invalid webhook header names during configuration validation.

## v0.1.3 - 2026-08-24

### Changed

- Upgrade `golang.org/x/text` to v0.41.0 to address GO-2026-5970.
- Require fresh route acknowledgement after upgrade as revisions gain target
  transport settings.

### Fixed

- Align dispatch inspection records with their schemas and backfill route revisions
  during database migration.
- Skip symlinks during reconciliation and retain recoverable per-file failures.
- Reject maintenance under Watchman, enforce secret-file ownership, and strengthen
  secret redaction and work-lifecycle diagnostics.
- Preserve in-flight lineage during retention and prune completed work receipts
  without leaving maintenance stuck.
- Harden scheduled submission, crash recovery, and repeated verification behavior.

## v0.1.2 - 2026-08-23

### Changed

- Require one fresh route acknowledgement after upgrade; resource and target changes
  now pause submission until the updated revision is acknowledged.

### Fixed

- Keep edits arriving during completion eligible for bounded follow-up work, with
  fresh identities and generation fences that prevent re-importing prior changes.
- Recover expired submissions through Watchman and scheduled reconciliation; allow
  explicit retry to reset an exhausted attempt budget.
- Preserve active dispatch lineage during retention, create owner-only state files,
  and return actionable doctor findings and failure codes.
- Reject escaping paths, validate resource overlap and configuration bounds, and
  bind managed triggers to the selected configuration.
- Heartbeat migration locks and strengthen secret redaction.

## v0.1.1 - 2026-08-23

### Added

- Add the MIT license and dependency license inventory.

### Fixed

- Recover interrupted submissions through draining and prevent reruns from
  duplicating in-flight work.
- Promote and submit follow-up generations without manual intervention.
- Revalidate stale dispatch plans, retain file digests for unchanged-edit suppression,
  and expose complete causal lineage and inspection filters.
- Harden migration interruption, post-acceptance crash recovery, and concurrency
  handling across the dispatch lifecycle.

## v0.1.0 - 2026-08-22

### Added

- Observe Markdown vault changes through Watchman and deliver bounded maintenance
  tasks to Hermes Kanban or an explicitly configured webhook through one-shot commands.
- Add durable SQLite dispatch, retry, receipt, quarantine, and reconciliation state.
- Add protected-path policies, cooperative self-change suppression, diagnostics,
  retention, integrity checks, and database backup.
- Publish reproducible binaries and checksums, scheduling examples, configuration
  and record schemas, and a Hermes companion skill. Initial artifacts included
  macOS arm64 and Linux amd64; Linux runtime verification was incomplete.

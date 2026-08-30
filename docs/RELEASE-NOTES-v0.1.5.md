# Agent Dispatch v0.1.5

Published 2026-08-30. v0.1.5 supersedes v0.1.4 as the latest release
and ships the approved v0.1.5 plan (D-025): source and reconciliation
integrity, Hermes capability-driven preflight and operator setup, the
multi-destination lifecycle, durable notifications, and the release
proof. The candidate was cut from the post-validation final tree, the
`v0.1.5` tag names that tree on the published `main`, and the hosted
Release carries the artifact and its checksums with
`docs/VALIDATION.md` as the evidence. No production activation is part
of this release.

## Source and Reconciliation Integrity (E10)

- **Resource observation fence and bounded reconciliation reads.**
  Reconciliation enumerates the vault under a durable observation
  revision: a purge or concurrent reconciliation that advances the
  revision refuses the stale enumeration instead of restoring a
  superseded snapshot, and the growing-file read bound keeps the
  enumeration finite.
- **Effective Watchman binding and route-relative exclusions.** The
  managed trigger binds the effective watched root (nested watches
  resolve to their real ancestor), and per-destination exclusion
  conditions evaluate route-relative against that root.

## Hermes Preflight and Operator Setup (E11)

- **Config v1 destinations cutover.** The `destinations[]` contract
  (target, profile, skills, workstream, workspace, mutex, execution
  hints, selection conditions) replaces the single-target route shape
  through a forward migration; the route revision covers every
  behavior-affecting field including the destination projections.
- **Capability-driven preflight.** `hermes probe` records the frozen
  0.19.1 interface and the installed newer Hermes through one bounded
  public-interface probe set (TST-012); `route preflight` validates the
  declared profiles and skills against that evidence and the submit
  path re-proves the executable identity before any side effect.
- **Discoverable setup.** `setup wiki` writes a disabled configuration,
  probes, preflights, checks the Watchman binding, runs the initial
  reconciliation, and stops before enablement with the exact production
  gate printed (CLI-012).

## Multi-Destination Lifecycle (E12)

- **One aggregate, many lanes.** One source occurrence fans out to one
  child dispatch per selected destination beneath one aggregate event,
  each child carrying its own destination revision, workstream, and
  DAT-014 idempotency identity; lanes hold independent slots, dirty
  generations, and retry chains (CON-007/CON-008, FAN-002/FAN-003).
- **Work receipt v2.** The four-outcome cooperative receipt
  (`completed`, `partially_completed` with both scopes, `blocked` with
  its manual reason, `failed`) drives per-lane completion; acceptance
  without a valid attributable terminal receipt is never reported
  completed (FBK-009..FBK-012, AC-806).

## Notifications (E13)

- **Transactional outbox (ADR-0019).** Every reportable transition —
  work outcomes, unknown delivery, quarantine, the pending
  reconciliation appearance, and the drain-evaluated integration and
  Watchman drift — commits its notification intent in the same SQLite
  transaction; delivery happens after commit and never rewrites the
  transitioned state. The notification identity is the five-component
  projection (event, optional destination, transition occurrence, sink,
  notification-policy revision), so replays and reruns collapse onto
  the existing notification instead of duplicating it (NTF-001..005,
  NTF-007, AC-901/AC-902).
- **Sinks and operator surface.** The structured log sink emits each
  stored notification-event/v1 payload as a stderr JSON line; the HTTPS
  webhook sink enforces https-only endpoints, send-time secret
  resolution redacted from every diagnostic, a dedicated stable
  idempotency header, redirect refusal without ambient proxies, and
  bounded payload, response, and time (SEC-011..013). `notifications
  test|list|retry|drain` ship: the probe creates nothing (NTF-008),
  the drain evaluates the drift classes once per appearance and
  performs one bounded attempt per pending notification with outcomes
  as data, and the explicit retry re-arms a refused notification under
  its stable identity. `status` projects the delivery counts and the
  scheduled recipe chains the drain after reconciliation (CLI-013).
- **Versioned skills.** The operator skill (2.0.0) carries the v0.1.5
  compatibility declaration and the notification operational guidance;
  the wiki-maintenance worker skill (1.2.0) adds the workstream-scope
  and exclusion discipline beside its untrusted-manifest, latest-state,
  and work-receipt/v2 rules.

## Artifacts

- `agent-dispatch-v0.1.5-darwin-arm64` — the single supported platform
  under the D-023 macOS-only policy (darwin/arm64), built twice from
  the release commit with the pinned Go 1.26.6 toolchain; the two
  builds are byte-identical and `dist/SHA256SUMS` records the digest.
- The versioned skills and the checked-in schemas and examples are
  manifest-verified (`make manifest-check`) and schema-validated on
  every `make verify` run.

## Known state

- The real-Hermes walkthrough legs (G8/G9) are skip-guarded under the
  documented TST-007 posture: they execute against an installed
  supported Hermes on disposable boards and skip with an explicit
  environment-dependent evidence-gap message otherwise; every gate
  criterion is proven deterministically in `docs/VALIDATION.md`.
- Notification delivery is at-least-once under the stable idempotency
  identity: an ambiguous or retryable delivery stays pending for the
  next drain and is inspectable through `notifications list --state
  pending`; a permanently retryable notification has no automatic
  attempt ceiling (the operator surface owns the retry decision: a sink
  id that no longer resolves is restored — a log sink declaration is
  enough — so the next drain resolves or refuses its pending
  notifications).

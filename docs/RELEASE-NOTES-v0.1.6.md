# Agent Dispatch v0.1.6

Published 2026-09-02. v0.1.6 supersedes v0.1.5 as the latest release
and ships the approved v0.1.6 operational follow-up (D-027): route
correct and rerunnable guided setup, Hermes v0.20.5 compatibility
through Agent Dispatch serialization groups, and bounded automatic
notification draining — closed with documentation truth, a real
environment cold validation, and the reproducible release proof. The
candidate was cut from the post-validation final tree, the `v0.1.6` tag
names that tree on the published `main`, and the hosted Release carries
the artifact and its checksums with `docs/VALIDATION.md` and
`docs/integrations/e17t2-cold-validation-evidence.md` as the evidence.
No production activation is part of this release.

## Guided Setup and Disabled Baseline (E14)

- **Route-correct, rerunnable setup.** `setup wiki` selects one route
  explicitly (`--route`, single-route auto-selection, or a clearly
  rendered interactive choice — never a silent default), propagates the
  selected route to every route-scoped step, printed Watchman command,
  and the final enable command, and converges across clean-host,
  installed, materialized, and interrupted postures (ADR-0020,
  AC-1001/AC-1003).
- **Disabled baseline-only reconciliation.** `reconcile --reason
  initial --baseline-only` records the bounded snapshot and the route
  baseline in one observation-fenced transaction while the route stays
  disabled in both halves of the production gate; it creates no
  decision, dispatch, task, acknowledgement, or notification, refuses
  every other state at exit 14, and a crashed attempt converges on the
  next run (CLI-017, DUR-017).
- **Five-state production gate.** The setup summary reports the
  configuration enabled state, runtime activation, Watchman binding,
  initial baseline, and production acknowledgement as distinct states
  and prints — never executes — the exact enable command (OPS-016,
  AC-1005).

## Hermes v0.20.5 Compatibility and Serialization Groups (E15)

- **Certified serialization modes.** The capability probe (contract v3)
  certifies exactly one effective mode per target —
  `agent-dispatch-group-enforced` (the normal posture of a 0.20.5
  Hermes without `--mutex-key`), `agent-dispatch-group-plus-target-mutex`,
  or `unsupported-unsafe` — and every surface from preflight to the
  submit-time revalidation agrees on it; the renderer never sends the
  unsupported flag (HER-019 through HER-021, AC-1101).
- **Local serialization groups.** Every destination resolves one
  effective group (explicit `serialization_group`, the deprecated
  `mutex_key` alias, or the resource-derived default), global within
  one state database: at most one active child holds a group, bursts
  merge into the lane's dirty generation, completion promotes the
  oldest first-dirty lane, and acknowledged independent groups run
  concurrently (ADR-0021, CON-011 through CON-014, AC-1102 through
  AC-1104).
- **Explicit version floor.** 0.20.5 is the product floor with no
  maximum; an omitted or below-floor setting fails closed and
  `hermes set-minimum-version` updates one target atomically, naming
  every affected route's owed re-probe, preflight, and
  re-acknowledgement (HER-011, AC-1106/AC-1107).

## Automatic Durable Notification Draining (E16)

- **Bounded drain policies.** Each route declares `manual`,
  `after-command`, or `scheduled` draining with an explicit default
  envelope (limit 100, preserve-pending, 1h pending warning, 30s/15m/
  2.0/0.2 retry); an omitted block keeps the exact v0.1.5 manual
  behavior and route revision, and the effective policy carries its own
  inspectable revision (ADR-0022, NTF-010).
- **Lease-safe delivery.** Concurrent drainers claim disjoint due work
  atomically, outcomes record under the claim's fencing token (a stale
  owner records nothing), ambiguous and retryable outcomes persist one
  jittered backoff deadline shared by every process, and the explicit
  retry is the sole operator bypass — re-arming an ambiguous,
  retryable, or refused record, refusing a live lease at exit 14
  (NTF-011 through NTF-013, AC-1203/AC-1209).
- **Post-commit after-command draining.** A successful registered
  command drains the existing due work of every affected after-command
  route under one ten-second invocation-wide budget with deterministic
  one-item route rounds — silent on success, never changing the core
  command's stdout, JSON, or exit status; a failed core command never
  auto-drains (NTF-014/NTF-016, AC-1211).
- **Managed launchd schedules.** `schedule render|install|inspect|
  disable|uninstall --platform launchd` derives the managed identity
  from the instance, route, and configuration-path digest, invokes the
  internal runner directly (never a shell chain), installs idempotently
  refusing different definitions, and rotates logs; production
  enablement of an automatic mode requires the installed, loaded,
  definition-matching schedule, and the scheduled mode reconciles
  before draining (CLI-018, OPS-017/OPS-018, AC-1210).

## Documentation Truth, Cold Validation, and Release Proof (E17)

- **The public operator contract states the shipped behavior.** The
  CLI, configuration, installation, runbook, and skill surfaces
  document the due-only drain truth, the managed schedule lifecycle
  with the durable `--at` timing, the latest drain evidence in the
  status and doctor posture, and the retry semantics; the operator
  skill ships at 2.1.0.
- **The real environment validated the whole result.** The cold
  validation walked disposable state against the installed Hermes
  v0.20.5, Watchman, and real launchd — clean-host setup and rerun,
  five real board tasks with the mutex flag suppressed, a real
  `launchctl kickstart` firing of the managed runner, completion →
  outbox → automatic delivery, transport timeout, the ten-second
  budget, kill -9 crash recovery through lease expiry, and both
  serialization groups held concurrently — and closed three real
  defects on the way (the launchctl target form, the clean refusal of
  a dispatch on a never-registered route, and the Watchman-trigger
  executable-path guidance).
- **Schema 1-20, forward-only.** Migrations v18-v20 are additive and
  identity-preserving: serialization-group topology, notification
  drain leases and drain-run evidence, and the managed schedule's
  timing overrides; the documented upgrade runs the pre-migration
  backup, re-probes, preflights, and re-acknowledges the changed
  revisions, and rollback restores the verified pre-upgrade database,
  binary, and configuration together (OPS-009/OPS-015, AC-1304).

## Artifacts

- `agent-dispatch-v0.1.6-darwin-arm64` — the single supported platform
  under the D-023 macOS-only policy (darwin/arm64), built twice from
  the final implementation tree with the pinned Go 1.26.6 toolchain; the two builds
  are byte-identical and `dist/SHA256SUMS` records the digest
  (`ea0eb3cf17820baa2d9ce76b8a5539dca6f3c3130e5501f7b2bba08dd702deae`
  on the final implementation tree — commit `24e07c4`, carrying every
  audit remediation; only documentation commits follow it. The hosted
  artifact re-cuts from the tagged release commit under the publication
  authority, re-verifying byte-identity at that tree).
- The versioned skills and the checked-in schemas and examples are
  manifest-verified (`make manifest-check`) and schema-validated on
  every `make verify` run. Before publication the exact shipping
  artifact re-ran the real-Hermes, real-launchd surfaces on disposable
  state (probe, preflight, schedule lifecycle with the durable `--at`,
  a real board submission through automatic delivery) — see the
  cold-validation evidence's final-artifact re-verification section.

## Known state

- The real-Hermes walkthrough legs keep the documented TST-007
  skip-guard posture; the E17 cold validation additionally recorded a
  sanitized real-environment transcript set
  (`docs/integrations/e17t2-cold-validation-evidence.md`) with its
  known-limitations record.
- Notification delivery stays at-least-once under the stable
  idempotency identity; an unreachable webhook endpoint leaves its
  notifications pending under the persisted backoff until the operator
  retries or corrects the endpoint.
- Under the real Watchman trigger's minimal environment a PATH-relative
  `hermes` executable does not resolve: production configurations
  declare the absolute path, and the arrival parks durably in
  `retry_wait` with its documented recovery exits.
- The separately requested general `make verify` remediation remains
  outside this release's scope.

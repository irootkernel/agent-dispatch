# Agent Dispatch v0.1.4

Released 2026-08-25 from the tagged tree. v0.1.4 supersedes v0.1.3 as
the tagged latest release and ships the 2026-08-25 external MVP
compliance review remediation (D-023, closed by D-024).

## Remediation

- **Submission-gate integrity (D-023 F1, High).** The computed route
  revision now covers the webhook delivery-evidence surface — the
  authentication type, secret reference, auth header name, idempotency
  header, lookup timeout, and capability-report path — and the route's
  reconciliation flags. Changing how a dispatch authenticates,
  deduplicates, or reconciles now pauses the acknowledged route until
  it is re-acknowledged, exactly like any other behavior change.
  **Operator action required once:** every route enabled under v0.1.3
  or earlier must be re-enabled with a fresh
  `route enable --acknowledge-production-gate <new-computed-revision>`
  after upgrading; until then the route persists observations and
  refuses submission with the re-acknowledge guidance.
- **Capability evidence at enable (D-023 F2).** `route enable` treats
  the capability report as mandatory evidence whether or not the target
  executable is reachable: a missing, unreadable, or
  unsupported-version report refuses at exit 3. An unreachable target
  remains a warning deferred to the submit path's run-time gate; only
  freshness against the installed binary rides the probe.
- **Header grammar (D-023 F3).** Webhook `auth.header_name` and
  `idempotency_header` must consist solely of RFC 9110 token
  characters; configuration validation rejects anything else before a
  submission attempt. Configurations that previously loaded with
  separator characters in these names now fail validation with the
  field and value named.
- **Enforced toolchain pin (D-023 F4).** `make verify` and
  `make release` now require the exact go.mod-pinned toolchain
  (Go 1.26.6) before any build step, including under `make -j`.
- **macOS-only support policy (D-023 F6).** darwin/arm64 is the only
  supported platform. The Linux build output, the systemd scheduling
  examples, and the Linux documentation surface are retired; SCP-008's
  and AC-505's Linux clauses are superseded by D-023 with the earlier
  verification records standing as history.
- **Documentation truth (D-023 F5).** The SOT package's status
  surfaces (README, VALIDATION, roadmap, release checklist) agree with
  the decision record: one SOT version, one shipped release, one epic
  lifecycle state, one 60-task count.

## Artifacts

- One statically linked binary: `agent-dispatch-v0.1.4-darwin-arm64`
  (byte-reproducible; built twice with identical SHA-256) plus
  `SHA256SUMS`. There is no Linux artifact under the D-023 policy.

## Known state

- The runtime-verified Hermes set remains exactly 0.19.1. On hosts
  with a newer installed Hermes (for example 0.20.5), the
  real-environment test legs skip as TST-007 evidence gaps and the
  enable gate refuses reports recording unsupported versions; widening
  the set is a fresh probe task, not a configuration override.
- The TST-008 automatic-write gate remains disabled in shipped
  defaults, as disclosed with v0.1.3.

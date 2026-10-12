# E23-T7 Exact Management Provider Handoff

This handoff identifies the v0.3.0 qualification candidate for Plugin TASK-028.
It grants no Plugin admission, production activation, installation or publication.
[D-031](../specs/decision-log.md#d-031---2026-10-12---arm-scoped-e23-completion-amd64-qualification-deferred)
permits ARM-scoped E23 completion using native Darwin arm64/Linux arm64 full
gates and paired public CLI evidence. Both native qualifications are complete
on the exact product source and binaries below. Their temporary services and
credentials have been removed. The required member and whole-Epic reviews
remain completion gates; admission requires the committed handoff package.

Linux amd64 is reproducibly built but unverified. Its unfinished full gate is
owned by [DF-010](../deferred-feedback/010-e23-amd64-qualification.md) before a
future three-platform release or amd64 management admission. ARM-scoped E23
completion does not close that requirement or qualify native amd64 Plugin use.

## Provider identity

| Field | Required value |
|---|---|
| Product version | `v0.3.0` |
| Product source commit | `6786088bc7b6540b34f25c9a398e644dae6a4547` |
| Management contract | `sha256:77dba7fac82cf1e627ad29ab89057c18d4d5c2c380251112579e551d1122cf39` |
| Base-sync contract | `sha256:30cf47b1bd854a0271aa9df3e7b37f0cc14cdd86d3f06a65a2cb787c6741131b` |
| Toolchain | Go `1.27.1` |
| SQLite compatibility | Schemas 1 through 29; management ledger 28 and worker availability 29 |
| Public CLI envelope | `agent-dispatch.cli/v1` |
| Management result | `agent-dispatch.sync-management/v1` |

The product source commit identifies the compiled implementation. The later
handoff documentation commit does not replace it. `version --json` exposes
name and version, with no commit field. Verify the full source identity through
this handoff and the exact binary SHA.

| Artifact | SHA-256 | Qualification |
|---|---|---|
| `agent-dispatch-v0.3.0-darwin-arm64` | `0d51e0afce19ae880bfb33211c5d06b81370f3ca9b1c19ce35e254a74016c9f7` | Native qualified |
| `agent-dispatch-v0.3.0-linux-arm64` | `d2ea2388b6474c60dec541d4a7d9ac974b39df28f710894a853b844e5dbc56e4` | Native qualified |
| `agent-dispatch-v0.3.0-linux-amd64` | `1bc58fc7a9cca839c5e7ead10365ee2286a2e446e00be52b0ae5fd1472aa6ebe` | Built only; unverified, DF-010 |

Two builds on native Darwin arm64 and two on native Linux arm64 produced this
same three-platform checksum list. Each used the exact Git archive, `make
release`, `VERSION=v0.3.0`, the full `RELEASE_COMMIT` above, that commit's
`%cI` value as `COMMIT_DATE`, `-trimpath` and `CGO_ENABLED=0`.
Build into a disposable `DIST_DIR`; rebuilding does not publish or install.

The separately frozen operating pair remains Core v0.2.0 source
`ffc8715d030ed3978e2c34d700798815975917d9` and inspection Plugin v0.2.0 source
`a060d1363e6861fc113b928f238a39d2f4c11e7d`. Its artifact pins are in the
[frozen-pair table](../contracts/sync-management-contract.md#frozen-release-pair-and-development-isolation).
It does not implement management and was not replaced during qualification.

The handoff evidence belongs in one `external-handoff` package under
`evidence/aquarium/E23-T7/external-handoff/<checksum-manifest-sha256>/`.
Its `manifest.json` binds the native checksum file's SHA and source commit
and indexes the three-artifact `SHA256SUMS` and a selected public-interface projection.
Opaque identities, timestamps and machine paths are removed from that
projection. It is evidence of selected native fields, not a complete wire
fixture or a request receipt. Full carriers and identity equalities were
inspected separately during qualification. Admission requires the committed
package; review and verified promotion precede the member completion commit.

## Public admission checks

TASK-028's qualified platform scope is Darwin arm64/Linux arm64 only. It must
verify the selected artifact's exact version and SHA before
amending the Plugin allowlist. A version range alone does not admit a provider.
Use only the public CLI and frozen contracts:

1. Inspect `version --json`, then inspect `sync request capabilities --group
   <group> --config <trusted-config>`. Check the management digest, policy,
   configured and available actions, executor availability and limits.
2. Validate actual carriers against the frozen result and envelope schemas.
   Resolve the configuration and input schemas through the same bundle.
3. Prepare an allowed action, retain the exact returned handle, submit it,
   then show that handle. Check its bindings, acceptance, state, associated
   jobs, targets and outcome. Acceptance does not establish completion.
4. Recover a missing caller response through exact lookup or same-handle
   resubmission. Keep caller uncertainty separate from Core's durable evidence.
   Do not replace the result with the group's latest verification.
5. Inspect action failures and retained obligations using the published error
   mapping. Retry only the recorded eligible obligations. A settled predecessor
   returns `already_settled` without another effect; a control result reports
   `intent_committed` and its separate quiescence value.

The [management bundle](../contracts/sync-management-v1/bundle.json) identifies
the exact 12 artifacts, including four schemas, policy, lifecycle, errors,
positive/negative fixtures, frozen argv, traceability and digest vectors.
Its checker validates 231 JSON fixtures, 69 argv fixtures and seven digest
vectors. [G19's deterministic matrix](../VALIDATION.md#g19-deterministic-qualification-basis-e23-t6)
maps crash, ambiguity, revocation, saturation, expiry, pruning and restore
behavior to the implementation tests. These contract fixtures are separate
from fresh runtime-output qualification.

The [base-sync bundle](../contracts/sync-provider-v1/bundle.json) and
[management contract](../contracts/sync-management-contract.md) retain their
independent identities. Do not alter either bundle to turn qualification
output into a normative fixture or widen a consumer pin.

## Qualification coverage and limits

The paired run used native Darwin arm64 and native Linux arm64, the binaries
above, disposable signed Git repositories and SQLite state, strict SSH host
verification, private HTTPS peer routes and native launchd/systemd user services.
The public CLI created signed membership and an initial administrator checkpoint.
No Plugin implementation or direct fixture database mutation supplied the run.

| Public behavior | Qualified result |
|---|---|
| Capabilities and service availability | Five enabled actions; matching loaded native definitions, accepting local sockets and available executors were inspected separately. |
| `sync-now` | A request accepted without an executor survived startup and finished `no_op` at the measured configured target. No new publication or signing was requested. |
| `verify-all` | The native managed executor finished a request with its exact verification job and target; ordinary `sync verify` returned fresh, clean two-node `complete` results in both directions. |
| Local `pause` and `resume` | Atomic control results returned `intent_committed`; the paused pair stayed incomplete and the resumed pair became complete. Quiescence remained `unknown`, with no claim that pause proved an idle worker. |
| `retry` | A settled predecessor retained the same verification job, target and job-attempt count and returned `already_settled`. Native eligible delivery replay was not exercised; T5/T6 deterministic cases cover that branch. |
| Service interruption | A stopped executor retained a queued request with zero attempts. Native restart finished that same handle and preserved its acceptance; resubmission added no effect. |
| Lost caller response | Closing the submitter's output reader caused SIGPIPE after durable acceptance. Exact lookup retained the queued handle; native restart completed one attempt and one verification job, and duplicate submission had no effect. |
| Exact historical lookup | An older request kept its original job and verification identity after newer successful verifications. |
| Uninstall | Stop, disable and state-preserving uninstall removed the matching definitions. Accepted request/job evidence remained available afterward. |

All 47 captured management carriers validated against the frozen envelope
and result schemas. Decoded public output was inspected for fixture secrets.
Temporary service definitions, private peer routes, bridges, transport agents,
owned credential files and the added SSH authorization/host-key lines were
removed. Existing routes, SSH file contents and agent keys were preserved.

Native Darwin arm64 and Linux arm64 passed `make verify` on the exact archived
source. Linux amd64 emulation has not yet passed the complete gate. The first
attempt failed optional Watchman startup; the next reached the package's
30-minute unit-test limit. Container-local source/cache storage allowed the
unit suite to pass, but the race suite reached the same limit. The final run
with two CPUs and `GOMAXPROCS=2` passed the unit suite in 1563.124 seconds and
failed the CLI race package at 1800.120 seconds, with `make verify` exit 2.
The timeout occurred while an existing G9 notification test was running;
it does not establish an individual assertion failure or race violation.
The source, checks and 30-minute timeout remained unchanged. The full amd64 SCP-008 gate
remains open in DF-010. D-031 excludes it from this ARM-scoped E23 completion;
future amd64 qualification requires a qualified host or separately authorized
correction to the canonical verification gate.
Native Darwin supplies real Watchman integration and launchd lint; native
Linux supplies systemd lint. Emulated amd64 evidence grants no native amd64
Plugin support.

## Operations and consumer ownership

Keep development and production separate by explicit config, state directory,
resource repository and sync group. Use the same native manager HOME for
service lifecycle and status inspection. Keep administrator/publisher signing
keys out of managed processes and transport agents; provide command-only
signer references to explicit protected CLI calls. Peer transport credentials
remain distinct. No operating v0.2.0 database or executable is a development
fixture.

Follow the [runbook](../ops/runbook.md#3c-sync-state-and-trust-maintenance): stop
services and writers, take a verified state/config/repository backup, inspect
the new binary and definition, then reconcile and verify the pair after upgrade.
Schema 29 is additive; an older executable cannot perform a downward migration.
Rollback restores a compatible stopped-service backup. A destructive restore
requires a fresh state incarnation, refreshed import acknowledgement and a
reviewed membership re-registration. Old drafts cannot execute as the retired
incarnation; accepted historical handles remain available for exact lookup.

Plugin TASK-028 is the next consumer admission gate under separate Plugin
authority. TASK-029 starts only after that scope amendment is approved.
EPIC-008 remains Deferred in the Plugin roadmap; EPIC-009's native amd64
inspection qualification remains independent. This candidate is unreleased.

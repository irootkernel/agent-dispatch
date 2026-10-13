# E23-T7 Exact Management Provider Handoff

This handoff identifies the v0.3.0 qualification candidate for Plugin TASK-028.
It grants no Plugin admission, production activation, installation or publication.
[D-031](../specs/decision-log.md#d-031---2026-10-12---arm-scoped-e23-completion-amd64-qualification-deferred)
permits ARM-scoped E23 completion using native Darwin arm64/Linux arm64 full
gates and paired public CLI evidence. The product source and binaries below
have native ARM paired qualification.
The exact source passed the native full gates described below. Temporary services
and credentials were removed. TASK-028 also requires a completed whole-Epic
independent review of this corrected candidate and its committed handoff package.

The selected source passed the native Linux amd64 full gate.
[DF-010](../deferred-feedback/010-e23-amd64-qualification.md) preserves the earlier
qualification of its original source and the requirement to requalify changed inputs.
Native paired management and service qualification remains ARM-only. This
follow-up does not widen Plugin admission or qualify native amd64 Plugin use;
new release inputs require applicable requalification.

## Provider identity

| Field | Required value |
|---|---|
| Product version | `v0.3.0` |
| Product source commit | `47cdc6b8ac6efb39fb6d7e6cb827bb178332996b` |
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
| `agent-dispatch-v0.3.0-darwin-arm64` | `c43e20f23d22705d9bb958dc23c3104085cc0c05c24a11fa5382a0c1848b1b41` | Native qualified |
| `agent-dispatch-v0.3.0-linux-arm64` | `e1476833dfa8e80737bb99646ff787900fa33bcacd12238ab0e94786f410aae0` | Native qualified |
| `agent-dispatch-v0.3.0-linux-amd64` | `4d42dc3a689a512bb9a4c2bdb900982c5d750d061cb7fe5dd73c83313d91e764` | Native full gate and version execution qualified; paired management unqualified |

Independent builds on native Darwin arm64 and native Linux arm64 produced
the same three-platform checksum list. Each used the exact Git archive, `make
release`, `VERSION=v0.3.0`, the full `RELEASE_COMMIT` above, that commit's
`%cI` value as `COMMIT_DATE`, `-trimpath` and `CGO_ENABLED=0`.
Build into a disposable `DIST_DIR`; rebuilding does not publish or install.

The separately frozen operating pair remains Core v0.2.0 source
`ffc8715d030ed3978e2c34d700798815975917d9` and inspection Plugin v0.2.0 source
`a060d1363e6861fc113b928f238a39d2f4c11e7d`. Its artifact pins are in the
[frozen-pair table](../contracts/sync-management-contract.md#frozen-release-pair-and-development-isolation).
It does not implement management and was not replaced during qualification.

Admission requires one committed `external-handoff` package for this product
source under `evidence/aquarium/E23-T7/external-handoff/`.
The native checksum file target is
`sha256:d7a1aed771fbc551e96e4efc3426653f65d3d5d890d187f5f2e61baebedc149f`.
The package's `manifest.json` binds that exact file target and capture-time Git
HEAD and indexes `SHA256SUMS` and selected native public-interface fields.
Verify the manifest and payload digests, the source identity and every artifact
SHA above. A missing or mismatched package cannot admit the provider.

The public projection omits opaque identities, timestamps, credentials and
machine paths. It contains selected native fields and is neither a complete
wire fixture nor a request receipt. Full carriers and exact identity equalities
were inspected separately. Promotion follows operationally complete native
review and receives commit-boundary validation. Earlier committed packages
remain immutable historical evidence and cannot admit this source.

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
The run reused private signed membership and an administrator checkpoint
previously created through the public CLI. The corrected binaries supplied the
new management, recovery and verification results. No Plugin implementation,
new publication, signing operation or direct fixture database mutation supplied
this run.

| Public behavior | Qualified result |
|---|---|
| Capabilities and service availability | Five enabled actions; matching loaded native definitions, accepting local sockets and available executors were inspected separately. |
| `sync-now` | A request accepted without an executor survived startup and finished `no_op` at the measured configured target. No new publication or signing was requested. |
| `verify-all` | The native managed executor finished a request with its exact verification job and target; ordinary `sync verify` returned fresh, clean two-node `complete` results in both directions. |
| Local `pause` and `resume` | Atomic control results returned `intent_committed`; the paused pair stayed incomplete and the resumed pair became complete. On both native nodes, `sync-now` and `verify-all` dispatched while paused became blocked with zero jobs and attempts; resume completed the original handles with unchanged bindings, acceptance and budgets. Reconciliation finished `no_op` with no job; verification finished `complete` with one job and one attempt. Quiescence remained `unknown`. |
| `retry` | A settled predecessor retained the same verification job, target and job-attempt count and returned `already_settled`. Native eligible delivery replay was not exercised; T5/T6 deterministic cases cover that branch. |
| Service interruption | A stopped executor retained a queued request with zero attempts. Native restart finished that same handle and preserved its acceptance; resubmission added no effect. |
| Lost caller response | Closing the submitter's output reader caused SIGPIPE after durable acceptance. Exact lookup retained the queued handle; native restart completed one attempt and one verification job, and duplicate submission had no effect. |
| Exact historical lookup | An older request kept its original job and verification identity after newer successful verifications. |
| Uninstall | Stop, disable and state-preserving uninstall removed the matching definitions. Accepted request/job evidence remained available afterward. |

All 393 captured management carriers validated against the frozen envelope
and result schemas. Decoded public output was inspected for fixture secrets.
Temporary service definitions, private peer routes, bridges, owned peer
credential files, the temporary Mac binary copy and the added Linux SSH
authorization/host-key lines were removed. No new SSH agent was started.
Existing routes, SSH file contents and agent keys were preserved.

Native Darwin arm64, Linux arm64 and Linux amd64 passed default `make verify`
on the exact Git archive of the product source above with Go `1.27.1`.
The unit and race targets retained their 30-minute deadlines; no test-selection
or deadline override was used. Native Darwin supplied launchd lint; native
Linux supplied systemd lint. The other platform's manager lint was skipped on
each host. Native Darwin had real Watchman and the installed Hermes executable;
Linux arm64 had no real Watchman or Hermes on its verification PATH.
Non-verbose gate output does not establish individual optional-test skip counts.

Native Linux amd64 used real Watchman from an owned tool directory. Its first
attempt failed before unit execution because the client tried to create its
installation's default state directory. Later attempts supplied private
client state, pid and log defaults while retaining the runner's later explicit
instance paths. A subsequent attempt was interrupted when the disposable VM
session restarted, so its partial unit/race output does not qualify the source.
The final attempt used an owned persistent tool/source directory and a session
heartbeat; the default gate passed. Repository source, test selection and
deadlines did not change.
The coordinator directly inspected the native terminal result and its source
and log digests. Native `version --json` executed the exact amd64 artifact and
returned `v0.3.0`; paired management and service qualification remains ARM-only.

The paired run's first Mac service could not finish an external-volume open.
The same binary ran from an owned internal fixture copy. Its existing import
acknowledgement was rebound to the copied resource's public configuration
revision. The original failed-start handle was settled through the public
worker before the clean pair was checked. Both Mac paused actions were then
confirmed from a healthy acknowledgement baseline; neither confirmation
changed the configuration between pause and completion. Failed attempts remain
separate from the successful native matrix; no fixture database was edited to
force a result.
The final native services, routes, bridges and credentials were removed, and
Linux SSH file contents were restored to their pre-run digests.

Later handoff and manifest edits change only canonical documentation and
promoted evidence. Their focused documentation and contract checks do not
pretend to rerun the full gates on those later bytes. Changed product, test,
build or contract inputs require fresh applicable qualification. D-031 keeps
its original ARM completion boundary; DF-010 keeps its historical exact-source
resolution. Neither record qualifies a changed source automatically.

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

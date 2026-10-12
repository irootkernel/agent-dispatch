# Constrained Sync Management Request Contract

## Status and ownership

This is E23's frozen implementation contract for the v0.3.0 successor,
selected by E23-T1 on 2026-10-09. Runtime delivery and G19 qualification remain
separate requirements; the v0.2.0 executable does not implement this family.
The successor requirements are `SMR-001` through `SMR-015` in
[required-spec.md](../specs/required-spec.md#20-post-v020-constrained-sync-management).
The [roadmap](../roadmap/roadmap.md#e23-constrained-sync-management-request-provider)
owns status and links to the canonical delivery outcomes. [ADR-0026](../architecture-decision-records/0026-constrained-sync-management-requests.md)
records the accepted architectural decision.

The first provider release is v0.3.0. Use a separately versioned
`agent-dispatch.sync-management/v1` contract and independent capability digest.
Do not revise the frozen `sync-provider-v1` bundle to advertise these commands.
The [management v1 bundle](sync-management-v1/bundle.json) owns the exact
command, policy, lifecycle, schema, fixture, and error descriptors. Run
`make management-contract-check` to check their independent identity.

## Scope and authority

Management is local to one configured group and its local state incarnation.
It is disabled by default, independently of ordinary sync enablement. An
operator-owned configuration selects the allowed actions. Core checks that
policy at preparation, submission, and each execution/recovery boundary.
Already-accepted work does not retain permission after the policy is revoked;
it pauses before new effects while keeping its result and recovery evidence.
Read-only lookup remains available through the trusted local CLI when new
management submissions are disabled.

The first actions are:

| Action | Allowed effect | Excluded effect |
|---|---|---|
| `sync-now` | Request the existing guarded configured-ref reconciliation, import, and already-signed recovery path | Create or sign a publication; treat maintenance completion as publication permission |
| `verify-all` | Run fresh verification of the exact configured two-node pair and record its actual target and evidence | Add nodes, infer completion from latest status, or modify content to make verification pass |
| `retry` | Ask Core to recover only the retry-eligible obligations linked to a specified earlier management request | Recreate the original action against a new target, retry arbitrary jobs, reset budgets, or clear a safety hold |
| `pause` | Commit local operator-pause intent under the expected control revision | Stop the peer's service or claim that an in-flight effect stopped immediately |
| `resume` | Revalidate and lift the local operator pause under the expected control revision | Clear conflict, revocation, trust failure, recovery-required, or membership-emergency state |

There is no publication/signing action, membership/checkpoint mutation, service
installation, remote peer control, force option, arbitrary Git operation,
endpoint selection, or note-body input. Paths, binaries, credentials, refs,
profiles, and transport settings come only from trusted local configuration.
A request ID identifies a record; it is not an authorization token. A claimed
actor, model-generated `confirmed` value, or Hermes message text grants no
privilege. The security boundary assumes an operator-controlled local account
and configuration, not isolation from a hostile process with the same UID.

## Request handle before submission

Use a small preparation step so a caller learns a durable handle before a
management action can execute. This avoids requiring a Plugin database or an
unverified Hermes call-ID persistence guarantee.

The frozen command family is shown for successor implementation. These commands
must not appear as available in v0.2.0 help, examples, or capabilities:

```text
agent-dispatch sync request capabilities --group GROUP --config PATH --output json
agent-dispatch sync request prepare --group GROUP --action ACTION [--target-request-id ID] --config PATH --output json
agent-dispatch sync request submit --group GROUP --request-id ID --config PATH --output json
agent-dispatch sync request show --group GROUP --request-id ID --config PATH --output json
```

All commands accept the trusted config path consistently. All inputs are
closed. `--target-request-id` is required only for `retry`; other actions
reject it. Core derives revision preconditions at preparation and returns
them for inspection. Submission accepts the handle, not replacement action
parameters. Both `--flag value` and `--flag=value` are accepted. Duplicate
flags, positionals, unknown flags, empty values, and control bytes are refused.
Output defaults to JSON; any other output format is refused.

`prepare` validates the policy and local bindings and creates a bounded,
expiring immutable request draft. It may perform bounded local inspection
and a local SQLite write, but cannot enqueue executable work, change sync
control, sign, contact a remote, or mutate Git content or refs. Preparation
is not a per-request human approval mechanism. Operator preauthorization is
the first-release policy; a distinct human approval system is out of scope.
A lost preparation response can leave an unsubmitted draft, which expires
without effects. Creating a replacement draft is safe only when no earlier
submission could have happened.

The handle binds the group, local instance and state incarnation, action,
optional predecessor request, authorization-policy revision, relevant config
binding, and expected control revision for pause/resume. The immutable
fingerprint covers these fields. Any changed binding requires explicit new
preparation, not silent restamping during submit or retry. Data-plane work
rechecks membership, import acknowledgement, resource safety, and configured
remote identity through the existing execution path. Verification pins its
actual target at execution, rather than pretending preparation observed the
remote head.

`submit` looks up a prepared handle, rechecks its bindings and authorization,
and commits acceptance before making work executable. Duplicate submissions
return the same request and outcome; concurrent submitters cannot create two
executions. It never upserts a missing handle. Unknown, expired, retired,
wrong-group, or obsolete-incarnation handles cannot initiate work. Changing
an action or precondition under an existing handle is a conflict.

For `sync-now`, `verify-all`, and `retry`, submission performs no remote,
Git-content, signing, or long-running execution. The existing Core sync
executor takes responsibility after durable acceptance. Loss of the caller
or CLI process cannot discard accepted work. When the service is stopped,
the response distinguishes accepted pending work from executor availability;
it never starts or installs the service implicitly.

For local `pause` and `resume`, acceptance, the revision-checked control
transition, and its request outcome commit in one SQLite transaction, using
the same control rules as the operator CLI. These small control transactions
do not wait behind an import or verification. A bounded control admission
reserve remains usable when data-plane request capacity is full. Resume also
works when the group is paused; neither operation needs the sync worker to
be running. Pause completion means control intent committed, with separately
reported quiescence evidence. It does not mean all in-flight effects have
already stopped. The prepare/submit transaction boundaries must never permit
an accepted receipt without its corresponding control result.

## Lookup, outcomes, and uncertain responses

`show` is a bounded read by exact request ID. It neither starts recovery nor
fetches, verifies, submits, or creates missing records. It reports the bound
action and revisions, lifecycle, executor availability, associated execution
identities, attempts, and bounded reasons. Results must come from that
request's durable associations, never the group's latest job projection.

The lifecycle distinguishes unsubmitted `prepared` and `expired`
records, accepted `queued` and `running` work, recoverable or held work, and
resolved results, with the closed graph below. Query success says that the
record was read. Acceptance, publication, delivery acknowledgement, import,
and complete verification each require their own evidence.
A finished verification with `incomplete` or `target_changed` is a valid
verification result, not an adapter failure or fresh convergence claim.

A caller timeout or malformed response after submit is an unknown submission
outcome. The caller retains the handle and uses `show`, then may resubmit the
same handle only as permitted by the contract. It must not prepare a new
action automatically. Core can already have finished the request while the
caller is uncertain. Do not overwrite a known Core result with the caller's
transport uncertainty.

Lookup must distinguish a retained record, an expired/retired handle where
known, and absence under the current local incarnation. Absence is not proof
of no historical effect. After retention or state restoration, missing
records cannot be reconstructed by submitting the old handle.

A `retry` is a new management request linked to an existing request, not a
new underlying publication/import identity. Only the earlier request's
verified retry-eligible obligations are considered. If background recovery
already settled them, return that evidence without repeating effects. A
request with no recoverable obligations refuses or reports a documented
no-op. Pre-signature publication, arbitrary existing jobs without management
lineage, exhausted attempts, revoked policy, stale bindings, and safety holds
require operator action. Queueing another retry cannot reset the underlying
logical job's attempt budget or change its immutable target.

## Execution and recovery

Core owns the request ledger, execution claims, request-to-job associations,
results, and recovery. Reuse the existing SQLite and sync worker, with shared
application helpers where CLI orchestration currently embeds logic. Do not
introduce a general-purpose job framework, a second sync daemon or scheduler,
or Plugin-owned durable state. A request can associate with several existing
jobs during one reconciliation, but each association must record why that
job belongs to the request. A concurrent periodic pass may satisfy an
obligation; the result credits observed durable evidence rather than falsely
claiming exclusive execution.

Execution uses claims and fencing before effects. Restart and expired-claim
recovery inspect existing journals and effect evidence before retrying.
Timeout and lease expiry are not no-effect proofs. The worker provides
bounded fairness between management requests, peer inbox work, and periodic
recovery, preserving the existing one-protected-effect-per-group boundary.
A management request cannot starve missed-nudge catch-up.

Read-only verification orchestration must not count its own request metadata
as content-affecting pending work and make every verification incomplete.
Only the exact harmless bookkeeping for the current verification is excluded;
unrelated content-affecting work, uncertainty, dirtiness, or stale membership
still prevents a `complete` result. This is an internal provenance rule, not
a caller-controlled exemption in the peer protocol.

The existing signing-key isolation remains intact for the service and every
management execution path. Management never resolves a publisher or
administrator private key. Ordinary explicit `sync publish` and signed
checkpoint/membership recovery retain their existing authority.

## Limits, retention, and local state upgrades

The numeric ceilings below apply to preparation lifetime, retained
unsubmitted drafts, queued/unresolved requests, the independent control
reserve, lookup size, associated-job evidence, execution deadlines, retry
attempts, and retained terminal records. Do not expose an uncapped list or
serialize every historical job into one tool result. Existing sync job,
subprocess, concurrency, and retry ceilings remain upper bounds for effects.
Preparation/acceptance must fit inside the five-second call bound;
long work stays outside that call. Verify this with an injected short CLI
deadline, not by increasing the Plugin timeout to hide coupling.

Unused drafts expire. Accepted unresolved obligations do not disappear through
age pruning. Resolved management evidence remains long enough to inspect its
linked job outcomes, following the configured completed-receipt horizon
(default 180 days) unless a reviewed successor contract specifies otherwise.
Pruning must preserve referential integrity, unresolved retry lineage, and
request-to-effect evidence. Saturation produces an explicit bounded refusal
before acceptance. Lookup and reserved local control remain usable.

The migration is additive and preserves existing sync jobs and ordinary
v0.2.0 behavior when management is absent or disabled. Do not invent
management requests for historical jobs. An older binary must refuse an
unsupported upgraded schema. Rollback uses the repository's stopped-service,
compatible-backup procedure; it is not an old executable opening new state.
Supported destructive restore requires management disabled and a new local
state incarnation through the existing identity recovery procedure before
new management effects. A restored prepared request from an old incarnation
must never become executable. Unannounced copying of an older live database
by a same-UID process is outside this supported recovery contract.

## Frozen release pair and development isolation

The current pair was published on 2026-10-01. These public release identities
remain independently usable while v0.3.0 is developed:

| Provider | Published identity |
|---|---|
| [Core v0.2.0](https://github.com/irootkernel/agent-dispatch/releases/tag/v0.2.0) | Source `ffc8715d030ed3978e2c34d700798815975917d9`; base contract `sha256:30cf47b1bd854a0271aa9df3e7b37f0cc14cdd86d3f06a65a2cb787c6741131b` |
| Core Darwin arm64 | Binary SHA256 `7f9e68f7033ed7750f1d6912d510f617d2a06fd58f5f18400b73d68280604508` |
| Core Linux amd64 | Binary SHA256 `a749ad083c2028af3b0d500c95d448f5a90af80c5c6e9a70fe950b011393efb2` |
| Core Linux arm64 | Binary SHA256 `9ca8f030b7763f05c229c5792836ea86ad3f2a8cc97912e47dda8f8a687d2cec` |
| [Inspection Plugin v0.2.0](https://github.com/irootkernel/agent-dispatch-plugin/releases/tag/v0.2.0) | Source `a060d1363e6861fc113b928f238a39d2f4c11e7d`; source archive SHA256 `10c69ed73dc320d37c77e0918784107d5b51e3430e005dae0290cbe41dd3eb9a` |

The published Core `VALIDATION.json` records `final_verify_verdict: INCOMPLETE`:
the aggregate race leg reached its 600-second limit and later gates were not
run in that release qualification. Publication does not turn that result into
passing verification or waive E23's fresh gates.

T1 adds only contract artifacts and developer verification. It keeps runtime
commands, runtime version defaults, configuration parsing, and the frozen
`sync-provider-v1` artifact set unchanged. T2 onward use disposable state,
repositories, credentials, refs, and service fixtures. No installed binary,
production configuration, real vault, or Plugin allowance changes as a result
of contract freeze. v0.3.0 release, installation, and activation need their
own subsequent authority.

## Exact bindings and wire records

Handles match `^mgr_[0-9a-f]{32}$`: the suffix is 16 cryptographically random
bytes, generated by Core. Group IDs match `^[a-z][a-z0-9-]{0,62}$`. Local
instance, incarnation, verification, and job IDs retain the existing ASCII
grammar `^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`. Digests use `sha256:` followed
by 64 lowercase hexadecimal digits. Git commit IDs contain exactly 40 or 64
lowercase hexadecimal digits. These are whole-string matches; control bytes,
path traversal, whitespace, aliases, and uppercase digest prefixes are refused.
Timestamps use RFC3339 UTC. Raw JSON rejects duplicate keys at every depth,
trailing documents, invalid UTF-8, and more than 64 nested containers before schema
validation. All objects are closed and property names are case-sensitive.

The successor configuration block `sync.management` has exactly `enabled`
(boolean) and `actions` (unique array of the five action names). Both fields
are required when the block is present; an absent block means disabled with
an empty allowlist. Enabled with no actions is invalid. Ordinary `sync.enabled`
also has to be true for new management effects. The operator config selector
is `--config`, then `AGENT_DISPATCH_CONFIG`, then the platform default. No
management command accepts `--state-dir` or a caller-defined policy. Argv fixtures
start at the phase token; runtime conformance tests prepend `sync request`.
Flag values refuse Unicode control characters (C0, DEL, and C1).

Policy revision hashes `{enabled, actions}` with actions sorted lexically.
Config revision hashes `{sync_revision, sync_enabled, bounds, nodes}`:
`sync_revision` is the existing cooperative-import `SyncRevision`; `bounds`
is the complete existing `SyncBounds`; nodes sort by `instance_id` and project
exactly `instance_id`, `state_incarnation_id`, `endpoint`, `publisher_key`,
and `credential_ref`. The credential reference is hashed without resolving
its secret. Private signing-key references and secret values are neither
resolved nor returned. Management does not change the existing acknowledgement
projection. The config revision additionally binds endpoint and membership
changes that the acknowledgement intentionally excludes.

The immutable request fingerprint hashes exactly `group_id`, `instance_id`,
`state_incarnation_id`, `action`, nullable `target_request_id`, `policy_revision`,
`config_revision`, and nullable `expected_control_revision`. Control actions
require a positive expected control revision; other actions carry null.
Retry requires a predecessor handle; other actions carry null. Preparation
records creation/expiry time separately. Submission never rewrites these
bindings. Duplicate accepted submissions return existing evidence before new
capacity or policy checks, while execution still needs current authority.
Lookup after incarnation/policy/config movement returns the retained record
with `binding_current: false`, allowing no new effects under that record.

Revision/fingerprint digests hash domain UTF-8, NUL, and compact canonical JSON
UTF-8 with recursively sorted object keys, exact integers, no HTML escaping,
and no final newline. U+2028 and U+2029 use lowercase JSON Unicode escapes,
including when HTML escaping is disabled. The domains and golden vectors are in
[policy.json](sync-management-v1/policy.json) and
[digest-vectors.json](sync-management-v1/digest-vectors.json). Array order is
preserved except for the specified action/node sorting. A vector's `input`, when
present, is the unsorted projection; `value` is the sorted projection to encode.

The outer carrier stays `agent-dispatch.cli/v1` in `api_version`. Success
contains `command`, `ok: true`, `result`, `warnings`, and `trace_id`; errors
contain `command`, `ok: false`, `error`, and `trace_id`, with an optional
bounded request view in `result`. Omit `result` when no view is available;
an explicit null is invalid. Successful records go to stdout; error
records go to stderr. The inner `schema_version` is
`agent-dispatch.sync-management/v1`. Exact fields, discriminators, conditional
constraints, and error/exit pairs are in the closed
[result schema](sync-management-v1/result.schema.json),
[envelope schema](sync-management-v1/envelope.schema.json), and
[error registry](sync-management-v1/errors.json).

Wire conformance also requires the semantic UTF-8 byte limits in
[policy.json](sync-management-v1/policy.json). JSON Schema `maxLength` counts
code points and supplies a structural bound; the checker and runtime consumers
must separately enforce 256 UTF-8 bytes for reason, message, and remediation.
Only envelope fixtures carry `exit_code`; other fixtures test document validity.

A successful lookup or accepted submit exits 0 even when the action is pending,
blocked, uncertain, or finished with an incomplete result. Error categories
retain the existing exit taxonomy: usage 2, configuration 3, input rejection 4,
transient local 10, acceptance unknown 13, conflict 14, storage 20, security 30,
internal 40. Output failure exits 40 without undoing committed work. Management
error records set `retryable: false`; this forbids an adapter's automatic new
action. Exact lookup and explicitly permitted same-handle resubmission remain
the response-loss recovery procedure.

Results expose only their own bounded job associations, attempts, claim fence,
immutable bindings, acceptance time, and outcome. `side_effects` describes the
current CLI call: preparation writes a draft; a newly accepted submit writes
acceptance and, for controls, local intent; duplicate submit and lookup do not
claim a new write. It does not summarize background effects. `executor` is
`available` only with a worker heartbeat no older than 60 seconds, `stopped`
with explicit stopped-service evidence, and otherwise `unknown`. Availability
is an observation, not a completion promise. Disabled or implementation-gated
actions cannot appear in `available_actions`.

## Closed lifecycle and outcome meaning

| Current state | Allowed next state | Required basis |
|---|---|---|
| `prepared` | `expired`, `queued`, `finished` | Expiry without acceptance; durable async admission; or atomic local control/result |
| `expired` | None | An unaccepted draft's lifetime ended |
| `queued` | `running`, `blocked` | Fenced worker claim or a known authority/safety hold |
| `running` | `queued`, `blocked`, `uncertain`, `finished` | Evidence-backed recovery, hold, unresolved possible effect, or persisted result |
| `blocked` | `queued`, `uncertain`, `finished` | Revalidated removal of the specific hold, effect evidence, or settled obligation |
| `uncertain` | `queued`, `blocked`, `finished` | Durable effect/journal evidence establishes the appropriate next state |
| `finished` | None | A retained action result; no automatic re-execution |

Expiry is derived for unaccepted drafts during lookup without writing. Accepted
work is not turned into draft expiry. Claims increase the fence and request
attempt count; renewal retains the fence. Expired claims require effect/journal
inspection before reclaiming. Known holds use `blocked`; ambiguous effects use
`uncertain`. Neither state is success, and neither lease age nor caller timeout
is sufficient to queue a repeated effect.

Control results use `intent_committed`, a positive resulting control revision,
and `quiescence: observed|not_observed|unknown`. Pause never infers quiescence
from intent alone. Resume performs the existing bounded safety/remote checks
outside SQLite while retaining the nonblocking resource guard through the
atomic commit. Guard contention, safety failure, stale revision, or the call
deadline refuses before acceptance. Pause requires no remote check. Both
controls use their own draft, unresolved, and terminal capacity reserves and
work with the sync worker stopped or paused.

Data outcomes keep control revision and quiescence null. `verify-all` reports
`complete`, `incomplete`, or `target_changed` only with a stored verification
identity and pinned target. `expired` or `failed` can describe failure before
target pinning and must not invent a target/verification identity. Execution
deadline expiry differs from the unaccepted draft's `expired` lifecycle state.
`complete` requires the existing fresh exact-pair proof. Only this verification's
own harmless bookkeeping can be excluded from pending-work admission.

Each request/job association records an immutable job ID, kind, pinned target,
and reason: `observed_reconciliation`, `already_signed_recovery`,
`verification_target`, or `retry_obligation`. Existing jobs can satisfy work
through a concurrent periodic pass; association records do not claim exclusive
execution. Job IDs in one result are distinct; semantic validation supplements
the closed schema. Reserve association capacity before starting effects; a pass that
would exceed it remains visibly blocked before another effect.

Retry follows the predecessor's recorded obligations, including flattened
retry lineage, without changing job identities or targets. Fully settled
obligations produce `already_settled`. No recoverable obligations refuse with
`sync_request_not_retryable`. A mixed set may recover eligible obligations,
retain held/exhausted obligations, and finish `incomplete`; it never clears
their holds or resets the aggregate 20-attempt job budget. Pre-signature work
is never eligible. Retry against a stale or obsolete predecessor is refused.

## Tested limits and independent bundle identity

| Limit | Frozen value |
|---|---|
| Unaccepted preparation lifetime | 600 seconds |
| Expired unsubmitted draft retention | 24 hours after expiry |
| Prepared drafts | 256 data, 16 control |
| Accepted unresolved requests | Configured `sync.bounds.queue` (2 through 1000) data; 16 control |
| Retained terminal records | 10,000 data; 1,000 control |
| Job associations per request | 1,000 |
| JSON result/envelope | 1,048,576 UTF-8 bytes |
| Reason/message/remediation text | 256 UTF-8 bytes |
| Prepare/submit/show/capabilities deadline | 5 seconds |
| One claimed execution deadline | 240 seconds; existing subprocess bounds also apply |
| Claim lease / renewal | 30 seconds / every 10 seconds |
| Worker availability freshness | 60 seconds |
| Management shutdown | 30 seconds |
| Cumulative request and underlying-job attempts | 20 each; retry never resets either identity's count |
| Terminal retention | At least 180 days, or the longer configured completed-receipt horizon |
| Fair dispatch | At most one management request per existing recovery pass |

Data/control counts are separate; request metadata is not counted again as
an underlying sync job. Existing sync-job/peer admission limits still apply
to effects. To ensure retained evidence fits, reserve a terminal slot for
every accepted unresolved request: admission requires unresolved plus retained
terminal records below that class's terminal limit. A full terminal reserve
refuses before acceptance; it never requires deleting young or unresolved
evidence. Prune accepted terminal records only after the retention horizon
and after all request/job/retry references are resolved. Keep unresolved
lineage even when this causes bounded refusal. Expired unaccepted drafts may
be pruned after their separate 24-hour horizon.

The existing periodic/peer recovery pass runs before the next management
dispatch. One long management action still obeys existing resource guards,
subprocess bounds, cancellation, and the one-protected-effect rule. Neither
a new scheduler nor increasing Plugin timeouts implements fairness. Runtime
tests in T2 through T6 must exercise the frozen storage, time, and fairness
bounds; T1 checks the limit descriptors, wire boundaries, and rejection fixtures
without claiming those runtime gates passed.

The management digest starts with `agent-dispatch.sync-management.bundle/v1`
and NUL. For each ordered `artifacts` entry in `bundle.json`, hash its UTF-8
path length (uint64 big-endian), path bytes, file byte length (uint64 big-endian),
and exact file bytes. `bundle.json` is excluded to avoid a circular digest.
`SHA256SUMS` covers `bundle.json` and every artifact in that order. No unlisted
files or symlinks are allowed. The checker pins the reviewed digest independently
of the base provider. After freeze, a semantic or byte identity change needs a
reviewed successor bundle; public runtime-output qualification fixtures remain
separate evidence rather than rewriting these contract fixtures.

[traceability.json](sync-management-v1/traceability.json) maps every SMR to its
canonical Task owners and G19 cases. This is planning/contract coverage; only
fresh execution evidence can establish completion of those later gates.

## Provider handoff and validation boundary

E23 is complete only when the frozen provider works through its public CLI
without a Plugin implementation. G19 covers concurrent submission, lost
responses, caller death, worker restart, effect-boundary crash recovery,
exact lookup despite newer requests, revoked authorization, stale revisions,
paused/stopped executors, saturation, expiry, pruning, and restore.

Retain Core's `SCP-008` three-platform deterministic verification requirement.
The first paired management handoff qualifies native Darwin arm64 and Linux
arm64 with disposable repositories and native service managers. Emulated
amd64 verification is labelled as such and grants no native Plugin support
claim. Plugin EPIC-009 remains independently responsible for its v0.2.0
Linux amd64 inspection qualification.

The handoff contains the full accepted source commit, selected product
version, per-platform binary SHA values, management and base-sync contract
digests, schemas, positive/negative and crash fixtures, expected errors,
upgrade notes, and gate evidence. It must identify the frozen v0.2.0 paired
release separately from the successor. No candidate is silently admitted by
version range alone. Plugin TASK-028 owns its later scope amendment and
allowlist update; TASK-029 must not execute mutations before that admission.

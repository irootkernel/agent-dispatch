# Constrained Sync Management Request Contract

## Status and ownership

This is the planning contract for E23, requested on 2026-09-30. It is not an
implemented command reference, a change to the v0.2.0 provider bundle, or
permission to enable management in an installed instance. The normative
successor requirements are `SMR-001` through `SMR-015` in
[required-spec.md](../specs/required-spec.md#20-post-v020-constrained-sync-management).
[E23's dossier](../todo/TODO-SYNC-MANAGEMENT.md) maps delivery, and the
[roadmap](../roadmap/roadmap.md#e23-constrained-sync-management-request-provider)
alone owns status. [ADR-0026](../architecture-decision-records/0026-constrained-sync-management-requests.md)
is Proposed until E23-T1 freezes the implementation contract.

The first provider release must be later than v0.2.0; its version is selected
at E23-T1, not inferred from this document. Use a separately versioned
`agent-dispatch.sync-management/v1` contract and independent capability digest.
Do not revise the frozen `sync-provider-v1` bundle to advertise these commands.
Before implementation, E23-T1 must turn the design below into exact command,
configuration, result, error, schema, fixture, and checksum authorities.

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

The proposed command family is shown for contract design only. These commands
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
parameters. E23-T1 freezes exact option spelling, ID grammar, limits, error
carriers, and exit codes before runtime implementation.

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
action and revisions, lifecycle, executor availability, linked execution
identities, attempts, and bounded reasons. Results must come from that
request's durable associations, never the group's latest job projection.

The planned lifecycle distinguishes unsubmitted `prepared` and `expired`
records, accepted `queued` and `running` work, recoverable or held work, and
resolved results. E23-T1 freezes a closed state graph with explicit
`blocked` and `uncertain` meanings. A wrapper saying the query succeeded does
not say the action succeeded. Likewise, an accepted request is not a
publication, delivery acknowledgement, import, or complete verification.
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

E23-T1 must freeze tested numeric ceilings for preparation lifetime, retained
unsubmitted drafts, queued/unresolved requests, the independent control
reserve, lookup size, associated-job evidence, execution deadlines, retry
attempts, and retained terminal records. Do not expose an uncapped list or
serialize every historical job into one tool result. Existing sync job,
subprocess, concurrency, and retry ceilings remain upper bounds for effects.
Preparation/acceptance should fit inside the Plugin's normal bounded call;
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

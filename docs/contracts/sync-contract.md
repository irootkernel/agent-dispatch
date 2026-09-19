# Two-Node Wiki Sync Contract

This contract reserves the v0.2.0 Agent Dispatch sync surface. E20 freezes the
schemas and implements truthful capability reporting. E21 and E22 implement
the behavior. Until the relevant capability is implemented, commands must
return the closed unavailable result without a Git, network, filesystem, or
activation side effect.

## Scope

One sync group binds exactly two active node identities, one resource root, one
approved Git remote, one content ref, one membership ref, and the Markdown file
scope. Route-protected and immutable paths remain outside that scope; a
publication or import candidate touching one fails closed before content
mutation. Configuration and local state choose every path, executable,
endpoint, ref, credential reference, and trust anchor. Event or peer payloads
cannot override them.

## Command identities

```text
agent-dispatch sync capabilities --output json
agent-dispatch sync status --group GROUP --output json
agent-dispatch sync publish --group GROUP --expected-config-revision REV --output json
agent-dispatch sync reconcile --group GROUP --output json
agent-dispatch sync verify --group GROUP --output json
agent-dispatch sync serve --group GROUP
agent-dispatch sync pause --group GROUP --expected-control-revision REV --output json
agent-dispatch sync resume --group GROUP --expected-control-revision REV --output json
agent-dispatch sync membership plan|apply --group GROUP --output json
agent-dispatch sync checkpoint plan|apply --group GROUP --output json
agent-dispatch sync service render|install|inspect|stop|disable|uninstall --group GROUP --output json
```

Reserved command identities remain registered in the CLI tree and return the
closed unavailable result until their owning task implements them.

`sync publish` is the only ordinary publication entrypoint. It requires an
eligible maintenance snapshot but not cooperative-import acknowledgement. The
command holds the resource guard through snapshot capture, persists publication
intent before effects, and resolves the node publisher key only in the explicit
CLI process. A pre-signature interruption requires an explicit re-entry under
the same logical identity. The service may recover an already-signed commit's
push, confirmation, and nudge work but cannot create or sign a new commit. A
confirmed remote publication and accepted nudge are separate outcomes.

`sync reconcile` fetches configured refs, resolves ambiguous prior effects,
and schedules eligible import or delivery work. It cannot select an arbitrary
remote, ref, path, executable, profile, or force option. After an operator
resolves a conflict, reconciliation clears only a block covered by the exact
administrator-signed checkpoint; it never adopts uncovered history by itself.

`sync verify` pins the group, membership revision, content ref, target commit,
scope digest, contract digest, both node identities, and their incarnations.
It reports historical delivery separately from fresh pair convergence.

`sync serve` hosts the authenticated nudge and fresh-status endpoints and runs
bounded periodic reconciliation. HTTP handlers validate and persist requests;
application services perform Git and filesystem effects after admission.

`pause` prevents new protected effects and lets in-flight work reach a safe
boundary. `resume` revalidates configuration, membership, activation, and local
safety. Neither command clears a conflict or revocation.

`sync membership plan|apply` is the two-phase administrator surface for initial
bootstrap, endpoint/key update, replacement, retirement, revocation, and
incarnation re-registration. The plan uses a closed change-kind enum. Apply
requires the same plan identity, expected membership predecessor, separate
administrator key role, and non-force ref update.

`sync checkpoint plan|apply` creates the administrator-signed content adoption
evidence used for an initial baseline, reviewed conflict resolution, or visible
bounded-history exhaustion. It binds the exact target commit, governed snapshot,
scope and contract digests, and membership revision.

`sync service` owns the managed launchd/systemd user-service definition. Install
starts the exact rendered definition, stop preserves it, disable stops and
disables it, and uninstall removes only the matching managed definition while
preserving local state and evidence.

## Peer protocol

The v0.2.0 protocol exposes authenticated nudge reception and bounded fresh
status observation. A nudge identifies the schema, group, publication, sender,
receiver, membership revision, content ref, and full target object ID. HTTP
202 means the inbox transaction committed. It does not mean that Git fetch or
content application completed.

Fresh status uses a request-correlated nonce and binds the membership revision,
content ref, target commit, scope digest, and contract digest requested by the
verifier. The response identifies the responder and state incarnation and
reports governed dirtiness, pending work, membership currentness, and
uncertainty. Cached evidence retains its original age and generation; echoing a
new nonce does not make cached evidence fresh. Verification carries each
response's `evidence_age_seconds`; `evidence_fresh` is true exactly when that
age is at most 300 seconds, and `complete` requires the bound for both nodes.

Requests reject unknown fields, duplicate-field ambiguity, oversized input,
wrong group or receiver, revoked identities, unsupported schemas, invalid
credentials, and unapproved redirects. Peer messages never contain note bodies
or arbitrary commands. The listener is loopback or tailnet-only, never enables
Funnel or public binding, never changes Tailscale configuration, and
verifies the configured peer endpoint and certificate.

## Outcomes and recovery

Publication preparation, remote publication, nudge acceptance, local import,
historical delivery, and fresh pair convergence are separate durable milestones. Every external effect
has a stable logical identity, bounded attempts, claim ownership, a fencing
generation, and an explicit ambiguous outcome.

The service recovers already-signed publication, delivery, and import work at
startup and during periodic reconciliation. Duplicate or reordered nudges are
normal. Retries reuse the original logical identity. Conflict, trust failure,
pre-signature publication work, and unsafe local state require operator action
and are not retryable transport failures.

## Versioned records and closed states

The canonical v1 record schemas are `sync-membership`,
`sync-membership-plan`, `sync-checkpoint`, `sync-checkpoint-plan`,
`sync-import-acknowledgement`, `sync-publication`, `sync-delivery`,
`sync-import`, `sync-control`, and `sync-verification`. Unknown fields,
versions, states, and reasons are invalid.
Full Git object IDs are lowercase 40- or 64-hex values; abbreviated IDs and
wall-clock time are never causal authority.

| Record | State progression | Terminal or held result |
|---|---|---|
| publication | `eligible -> prepared -> signed -> push_pending -> published` | `blocked` or `uncertain` retains the obligation |
| delivery | `pending -> attempted -> accepted` | `retryable`, `unknown`, or `refused`; HTTP 202 proves only `accepted` |
| import | `requested -> fetched -> validated -> applying -> applied` | `deferred`, `blocked`, `recovering`, or `uncertain` |
| control | `active <-> paused` | `blocked` requires explicit recovery evidence |
| verification | `planned -> collecting -> finished` | `complete`, `incomplete`, `target_changed`, `blocked`, or `expired` |

Publication `published`, delivery `accepted`, import `applied`, and verification
`complete` are deliberately non-interchangeable. State changes persist the
stable logical record ID, attempt count, claim owner where applicable, and a
monotonic fence before an external effect. Retries reuse the logical ID and a
new fence; lease expiry, process loss, or timeout alone cannot advance state.

Membership history binds the group, predecessor, pinned
administrator key, and node identity plus positive incarnation. Normal mode
has exactly two active members. Emergency revocation may produce a blocked
zero- or one-member roster while retired and revoked entries remain auditable.
A membership apply with a predecessor other than the reviewed plan predecessor
is `sync_precondition_failed`; an evidence record naming an older incarnation
is `sync_identity_obsolete`. The document does not embed its own Git object ID:
`membership_revision` always means the full object ID of the signed Git commit
containing the validated document. This avoids a self-referential payload while
retaining an explicit predecessor chain.

The membership plan binds a closed change kind, plan identity, expected
predecessor, proposed membership, and administrator key. Apply accepts only the
same plan whose group, predecessor, and administrator binding still match; the
shared semantic validator rejects duplicate active/historical identities and a
stale plan predecessor. The checkpoint plan likewise binds the group,
administrator, membership revision, content predecessor, and exact proposed
checkpoint.

Checkpoint evidence binds the exact target commit, governed snapshot, scope,
contract, and membership revision. It has only the reasons
`initial_baseline`, `conflict_resolution`, and `history_bound_exhausted` and
does not authorize a different target or uncovered history.

The cooperative-import acknowledgement is a distinct versioned record over
the group, resource, configured remote name, normalized credential-free remote
repository digest, refs, scope digest, local instance
and state incarnation, pinned administrator key, safety-policy digest, import
bounds digest, and normalized configuration revision. Any changed local input
changes that revision and makes the prior acknowledgement ineligible for live
application; membership movement under the same trust policy affects the
verification target but does not by itself rewrite the acknowledgement. E21
must recompute the repository digest from the actual Git remote immediately
before each protected effect; retargeting the same remote name invalidates it.

## Bounds and retention

The v1 contract caps active members at two, historical membership entries at
1,000, import paths at 1,000, publication and delivery attempts at 20, peer
payloads at 256 KiB, retained-record pages at 100 items, Git history inspection
at 1,000 commits, subprocess output at 1 MiB per stream, and subprocess runtime
at 120 seconds. Sync work queues hold at most 1,000 obligations per group,
service concurrency is one protected effect per group plus two peer reads, and
graceful shutdown has 30 seconds to reach a safe boundary before leaving the
obligation pending. Runtime configuration may lower but not raise these
ceilings.

Resolved publication, delivery, import, and verification records are retained
for at least 180 days. Membership and checkpoint evidence is retained for the
life of the group. Any `blocked`, `recovering`, `uncertain`, uncovered-history,
or unresolved predecessor obligation is exempt from age pruning until a newer
durable record explicitly resolves and references it. Exhaustion remains a
visible held result; it never truncates a node pair, drops work, or reports
convergence.

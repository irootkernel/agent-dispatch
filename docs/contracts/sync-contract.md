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
agent-dispatch sync membership plan --group GROUP --change KIND [--instance INSTANCE] --output json
agent-dispatch sync membership apply --group GROUP --plan FILE --expected-membership-predecessor OID|none --output json
agent-dispatch sync checkpoint plan --group GROUP --target-commit OID --kind KIND --output json
agent-dispatch sync checkpoint apply --group GROUP --plan FILE --output json
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
The plan takes node endpoint, publisher key, and positive
`state_incarnation_id` values from the validated local sync block. This makes
bootstrap constructible without peer discovery; the operator must update the
declared incarnation before an `incarnation_registration` plan. Non-bootstrap
plans require a non-null full predecessor object ID.
`--instance` is forbidden for bootstrap and required for every other change;
it names the member whose endpoint, key, incarnation, retirement, or revocation
is reviewed. For pair restoration after a blocked emergency it names the new
configured member. `--plan` is a regular, non-symlink strict-JSON file bounded
at 256 KiB. Its `plan_id` is the domain-separated SHA-256 digest of the
canonical plan with an empty self field. The signing secret resolves to
OpenSSH Ed25519 private-key bytes.

The membership `content_binding` is exactly the configured
`remote_repository_digest`; `content_ref` binds the governed branch within
that repository. Each membership revision contains exactly
`.agent-dispatch-sync/membership.json` and
`.agent-dispatch-sync/membership-plan.json` as regular non-executable files.
Any other tree entry, merge parent, unknown or duplicate JSON field, unpinned
signature, stale parent, or unauthorized transition fails closed.
Publisher-key rotation also requires a fresh state incarnation, so the retired
key and the replacement key can never authorize the same incarnation.

`sync checkpoint plan|apply` creates the administrator-signed content adoption
evidence used for an initial baseline, reviewed conflict resolution, or visible
bounded-history exhaustion. It binds the exact target commit, governed snapshot,
scope and contract digests, and membership revision. Apply journals the signed
candidate before push. After process loss, explicit re-entry probes the approved
remote: a confirmed candidate is verified and settled without signing again;
an unchanged predecessor records no-effect recovery evidence and resumes the
same candidate.

`sync reconcile --group <id>` is the guarded import surface. It fetches into
controller-owned tracking refs, verifies every first-parent content commit,
persists the exact effect set before mutation, updates only affected worktree
and index paths, advances the configured content ref with expected-old fencing,
and commits path facts plus import provenance atomically. Schema v22 stores the
effects separately from work receipts; the first exact Watchman observation
consumes an effect, while mismatches and later edits remain ordinary dirty work.
A content advance with no governed Markdown changes is still a durable import:
its immutable record sets `controller_only`, carries no path effects, and
journals the pre-apply boundary so exact ref/index/worktree inspection can
settle or retry it after process loss. Transient fetch failures defer without
creating a trust hold; a proven non-fast-forward fetch rejection remains a
history/trust failure.

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
versions, states, and reasons are invalid. `sync-publication` is the immutable
manifest stored in the signed commit: its record state is always `prepared`.
The lifecycle below belongs to the durable SQLite job/status projection and is
not embedded back into that signed manifest.
Full Git object IDs are lowercase 40- or 64-hex values; abbreviated IDs and
wall-clock time are never causal authority.
Journal order uses the explicit v24 sequence. Newest import-effect selection
uses the explicit v25 sync-job sequence and fence, never a hidden SQLite
`rowid` or timestamp.

| Record | State progression | Terminal or held result |
|---|---|---|
| publication job | `eligible -> prepared -> signed -> push_pending -> published` | `blocked` or `uncertain` retains the obligation |
| delivery | `pending -> attempted -> accepted` | `retryable`, `unknown`, or `refused`; HTTP 202 proves only `accepted` |
| import | `requested -> fetched -> validated -> applying -> applied` | `deferred`, `blocked`, `recovering`, or `uncertain` |
| control | `active <-> paused` | `blocked` requires explicit recovery evidence; `membership_mode` records the separate adopted posture in every canonical control record |
| verification | `planned -> collecting -> finished` | `complete`, `incomplete`, `target_changed`, `blocked`, or `expired` |

The generic transition graph keeps `blocked` terminal. Exact remote evidence
may settle a blocked publication, checkpoint, or membership through its
kind-specific recovery transaction without opening a general blocked-to-active
edge. A normal membership replacement resolves membership jobs only. An exact
administrator checkpoint resolves publication, checkpoint, and import jobs;
each affected job receives its own resolver identifier in the journal.
Emergency membership posture remains durable while an operator pause or a
stronger conflict, trust, or recovery reason is visible. Resume and exact
checkpoint reconciliation consult that posture and produce
`blocked`/`membership_emergency`; only an adopted normal membership clears it.

`expected_nodes` always fixes the exact two-node target. Verification `nodes`
contains collected evidence only: it may contain zero, one, or two distinct
expected nodes while `planned` or `collecting`, and `complete` requires exactly
two. Both expected and observed node identities and state-incarnation IDs are
pairwise distinct. Unobserved nodes are never represented by fabricated
evidence.

Publication `published`, delivery `accepted`, import `applied`, and verification
`complete` are deliberately non-interchangeable. State changes persist the
stable logical record ID, attempt count, claim owner where applicable, and a
monotonic fence before an external effect. Retries reuse the logical ID and a
new fence; lease expiry, process loss, or timeout alone cannot advance state.
The provider `results.json` file freezes semantic outcome fragments, not the
literal CLI wire envelope. CLI responses wrap those fragments in
`agent-dispatch.cli/v1` and may add target identity fields; the error registry
owns error code, category, and exit status. Verification completion explicitly
does not prove historical delivery, which remains a separate delivery-record
query and report section.

Membership history binds the group, predecessor, pinned
administrator key, and node identity plus positive incarnation. Normal mode
has exactly two active members. Emergency revocation may produce a blocked
zero- or one-member roster while retired and revoked entries remain auditable.
A prior incarnation retained after a destructive reset uses the closed
`incarnation_rotation` historical reason; it may retain the same stable
instance and publisher key while its incarnation remains globally unique.
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

Each content publication preserves prior controller metadata and adds exactly
one regular non-executable manifest at
`.agent-dispatch-sync/publications/<publication_id>.json`. The prepared
manifest omits the candidate object ID to avoid self-reference; durable journal
evidence binds the resulting signed commit. Checkpoint applies add the exact
reviewed records at `.agent-dispatch-sync/checkpoints/<checkpoint_id>.json`
and `.agent-dispatch-sync/checkpoint-plans/<plan_id>.json`. These controller
paths are excluded from the governed Markdown snapshot digest.

The cooperative-import acknowledgement is a distinct versioned record over
the group, resource, configured remote name, normalized credential-free remote
repository digest, refs, scope digest, local instance
and state incarnation, pinned administrator key, safety-policy digest, import
bounds digest, and normalized acknowledgement-configuration revision. Any
changed SYN-009 local input changes that revision and makes the prior
acknowledgement ineligible for live application. The peer roster, publisher
keys, endpoints, peer credentials, and signing-key references are membership
or publication inputs, not acknowledgement inputs: movement under the same
administrator trust policy affects the verification target but does not by
itself rewrite the acknowledgement. E21
must recompute the repository digest from the actual Git remote immediately
before each protected effect; retargeting the same remote name invalidates it.

Every sync digest other than the repository identity uses the same byte
framing: `sha256(domain || NUL || canonical_json || LF)`. `canonical_json` is
UTF-8, compact JSON over the contract's closed projection: object keys are
lexically sorted by Unicode code point; strings use JSON escaping; integers
use unsigned base-10 without leading zeros; booleans are lowercase; arrays
retain only the explicitly defined order below; floats and nulls are absent.
This restricted form is the complete canonicalization rule, not a reference
to producer-specific map iteration or pretty printing.

All input strings must be valid UTF-8 and retain their exact Unicode scalar
sequence; this digest layer performs no Unicode normalization. Object keys and
the scope projection's pattern arrays use ascending unsigned UTF-8 byte order
(equivalent to code-point order for valid UTF-8). JSON strings escape quote and
backslash, use `\b`, `\f`, `\n`, `\r`, and `\t` for those controls, use
lowercase `\u00xx` for other U+0000 through U+001F controls, and must escape
`<`, `>`, `&`, U+2028, and U+2029 as lowercase `\u003c`, `\u003e`, `\u0026`,
`\u2028`, and `\u2029`. Every other non-ASCII scalar is emitted directly as
UTF-8. Alternative but JSON-equivalent escaping is not canonical.
`sync-provider-v1/digest-vectors.json` freezes an independently checked vector
covering `<`, direct non-ASCII UTF-8, U+2028 escaping, and array order.

| Digest | Domain and canonical projection |
|---|---|
| `contract_digest` | Domain `agent-dispatch.sync-contract/v1`; object `{"schema_version":"agent-dispatch.sync-contract/v1"}`. After the v0.2.0 contract is released, a semantic change requires a new version. Before that first release, incompatible v1 draft changes are recorded in the current SOT changelog and frozen together by the provider artifact-set golden. |
| `scope_digest` | Domain `agent-dispatch.sync-scope/v1`; array of route objects with `route_id`, sorted `include`, `exclude`, `protected`, and `immutable` arrays. Routes are ordered by `route_id`. |
| `safety_policy_digest` | Domain `agent-dispatch.sync-safety-policy/v1`; the closed boolean SYN-010 policy object implemented by `syncSafetyPolicy`. |
| `import_bounds_digest` | Domain `agent-dispatch.sync-import-bounds/v1`; object with `history_commits`, `queue`, `subprocess_bytes`, and `subprocess_seconds`. |
| `config_revision` | Domain `agent-dispatch.sync-acknowledgement-config/v1`; object with that `schema_version`, group/resource IDs, complete resource shape, remote name and repository digest, content/membership refs, local/global instance IDs, administrator key, and the three digests above. It excludes enabled state, acknowledgement bytes, peer roster, endpoints, publisher keys, peer credentials, and signing-key references. |

The safety-policy projection is exactly the following closed object; every
value is `true`:

```json
{"defer_unproven_disjointness":true,"durable_preapply_journal":true,"exact_import_suppression":true,"fast_forward_only":true,"participating_writer_idle":true,"preserve_disjoint_changes":true,"preserve_ignored_files":true,"preserve_out_of_scope":true,"preserve_watchman_evidence":true,"reject_git_instability":true,"reject_overlapping_changes":true,"reject_untracked_overwrite":true,"resource_writer_guard":true}
```

The complete resource subobject in `config_revision` has exactly `type`,
`root`, `file_scope`, and optional `git`; when present, `git` has exactly
`mode`. The remaining projection keys are exactly `schema_version`, `group_id`,
`resource_id`, `resource`, `remote_name`, `remote_repository_digest`,
`content_ref`, `membership_ref`, `local_instance_id`, `global_instance_id`,
`administrator_key`, `scope_digest`, `safety_policy_digest`, and
`import_bounds_digest`. No unspecified configuration field participates.

For `docs/examples/config.yaml`, the frozen digest vectors are:

| Value | Digest |
|---|---|
| contract | `sha256:30cf47b1bd854a0271aa9df3e7b37f0cc14cdd86d3f06a65a2cb787c6741131b` |
| scope | `sha256:1e134a9296b8652b4fd887c8be2c2596caee32ac0523f058d516af8fd4958320` |
| safety policy | `sha256:f93ce592981e9d78d7041f8de390c79339746fb16504390e44f85292ce7a6ea7` |
| import bounds | `sha256:d13cc49b2ff321137a8f46a8829c42d0db7591f0a8732a4c5ba2916ab4142f56` |
| acknowledgement configuration | `sha256:4c51fc2e6f16977d433729dbe1718ed04ecbaee95901ef1d065c9076ea9f186b` |

`docs/examples/sync-import-acknowledgement.json` is the paired record for that
configuration and therefore uses `vault-main`, `workstation-main`, and
`workstation-main-state-001` with the exact digests above.

The repository identity digest is `sha256(domain || NUL || canonical || LF)`,
where `domain` is the ASCII string `agent-dispatch.remote-repository/v1`.
`canonical` is an absolute `https` or `ssh` URI only: lowercase scheme and
host, no query, fragment, percent encoding, dot segment, or non-default port.
HTTPS forbids all userinfo. SSH requires one non-empty username, preserves it,
requires the ASCII grammar `[A-Za-z0-9._-]{1,64}`, and forbids a password or
additional `@`. Default ports are removed;
the path is NFC-normalized, begins with exactly one slash, removes one trailing
slash and a terminal `.git`, and otherwise preserves case. SCP-like syntax is
rejected rather than guessed. For example:

| Input | Canonical | Digest |
|---|---|---|
| `https://GitHub.com/RootKernel/wiki.git` | `https://github.com/RootKernel/wiki` | `sha256:9436b709b57944f5c3a29127c41a1a24149159cb81a4e48bf5d0cebc49ed06be` |
| `ssh://git@GitHub.com:22/RootKernel/wiki/` | `ssh://git@github.com/RootKernel/wiki` | `sha256:aa989e912c28bec592cc816f21cdafe4efaac13cd23570c2a505028c5c4f7a4f` |

`https://alice@github.com/RootKernel/wiki`, `ssh://github.com/RootKernel/wiki`,
`ssh://git@@github.com/RootKernel/wiki`, and every URI with a password are
rejected rather than canonicalized.

An import record's `case_mode` is derived from the current resource path
policy and is rechecked with the acknowledgement and resource revision before
application. It is never an alias-safety selector: every import path set is
conservatively unique under NFC normalization plus Unicode case folding on
all hosts. A record that declares `sensitive` therefore still rejects paths
such as `Wiki/Index.md` and `wiki/index.md`; a caller cannot weaken cross-node
path identity by choosing the field.

## Bounds and retention

The v1 contract caps active members at two, historical membership entries at
1,000, import paths at 1,000, publication, delivery, and import attempts at 20, peer
payloads at 256 KiB, retained-record pages at 100 items, Git history inspection
at 1,000 commits, subprocess output at 1 MiB per stream, and subprocess runtime
at 120 seconds. Sync work queues hold at most 1,000 obligations per group,
service concurrency is one protected effect per group plus two peer reads, and
graceful shutdown has 30 seconds to reach a safe boundary before leaving the
obligation pending. Runtime configuration may lower but not raise these
ceilings.

Resolved publication, delivery, import, and verification jobs use the
configured completed-receipt retention horizon, which defaults to 180 days.
An observation consumed as exact import attribution follows that linked job's
retention constraint as well as the ordinary observation horizon: it remains
until both cutoffs have elapsed, regardless of which configured horizon is
longer. Pruning
removes the job-owned journal, effect, and attribution children before the
released observation, in one transaction.
Membership and checkpoint evidence is retained for the
life of the group. Any `blocked`, `recovering`, `uncertain`, uncovered-history,
or unresolved predecessor obligation is exempt from age pruning until a newer
durable record explicitly resolves and references it. Exhaustion remains a
visible held result; it never truncates a node pair, drops work, or reports
convergence.

The canonical import document is immutable pre-apply evidence. Its identity is
computed from the commits, membership, acknowledgement, observation, Git-state,
history, case mode, controller marker, and exact path effects. The canonical
payload keeps the initial `validated` / `none` disposition; state, reason,
attempts, claim, fence, and retention are mutable SQLite job projections and do
not change the import identity. A deferred job may later be resolved or reopened
under that same logical key without rewriting the canonical payload.

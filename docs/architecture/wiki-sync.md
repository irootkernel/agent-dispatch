# Two-Node Wiki Sync Architecture

> **Status:** Partially implemented for v0.2.0 under D-030. E21 provides
> qualified durable jobs and control, restricted Git and membership, explicit
> signed publication, signed checkpoints, guarded local import, and exact
> attribution. E22-T1 adds authenticated peer admission; E22-T2 adds periodic
> recovery. Pair verification, managed service lifecycle, and release remain
> unavailable.

Agent Dispatch extends the existing maintenance loop with an explicit Git
publication step and a peer import loop. Hermes still owns Wiki semantics.
Agent Dispatch owns durable intent, Git effects, peer delivery, import
attribution, and pair-status evidence.

```text
local change -> Watchman -> maintenance lanes -> validated receipt
                                                   |
                                             sync publish
                                                   v
                                      signed commit and push
                                                   |
                                          durable peer nudge
                                                   |
                             explicit sync reconcile (implemented)
                                                   v
                                  fetch and validate -> guarded import -> receipt

peer service -> durable inbox -> the same reconciliation/import path
             ^
             +---- periodic configured-ref inspection
```

## Boundaries

The E21 application helpers live beside the existing dispatch, work-receipt,
notification, and reconciliation services, while the explicit CLI currently
owns their bounded orchestration. E22 must extract or reuse that orchestration
before adding a service so the service and manual commands cannot diverge.
They reuse the local SQLite store and resource coordination rules. A restricted Git adapter owns
fixed-argument subprocess execution, delimiter-safe parsing, SSH signature
verification, explicit refspecs, deadlines, and output limits. The peer HTTP
adapter owns authentication, parsing, size limits, and response mapping only.

The service uses the same guarded reconciliation path as the manual CLI command
and must not create an alternate Git or recovery path. Local SQLite state is
never placed in Git or on a network filesystem.

Membership administration is a separate signed Git history. The normal roster
has exactly two active nodes, but historical retired/revoked entries remain
auditable and an emergency revocation can place the group in a blocked
fewer-than-two state. Administrator and publisher keys are distinct roles.
Membership and content-checkpoint changes use reviewed two-phase plan/apply
commands with expected predecessors and no force update.
The content binding is the configured canonical remote-repository digest; the
content ref separately names the governed branch. A membership commit has one
parent at most and exactly two fixed regular files: the canonical membership
document and the reviewed canonical plan. Runtime history verification checks
the complete bounded ancestry and the pinned administrator signature on every
revision before trusting the current roster.
The document carries its predecessor but never its own object ID. The full Git
object ID of the signed commit containing it is the `membership_revision` used
by publication, checkpoint, status, and verification records.

## Publication

An eligible publication binds maintained source inputs, required destination
receipt evidence, the frozen Markdown snapshot, base commit, scope and
contract digests, publisher identity, membership revision, and cause identity.
The signed commit contains a minimal manifest under
`.agent-dispatch-sync/publications/`. The manifest excludes that subtree from
its governed-content digest and does not try to contain its own final commit
ID.

Snapshot capture holds the resource guard and uses a private index or equivalent
immutable-object method. It never runs broad `git add -A` over a mutable live
tree. Edits that arrive after the frozen snapshot remain local and dirty. The
publisher key is available only to the explicit `sync publish` process.
Explicit re-entry of `sync publish` may recover an already-signed candidate.
No service signs a publication unattended.

## Peer service

`sync serve` binds an owner-only Unix socket at
`<state_dir>/peer-service/http.sock` and leaves the Tailscale HTTPS route under
operator control. It accepts only the configured peer identity and directional
credential after checking the current locally signed membership. Strict JSON
and HTTP bounds precede an idempotent SQLite inbox transaction. HTTP 202 means
that transaction committed. A separate worker wakes the existing guarded
`sync reconcile` path; the nudge never supplies its remote, ref, path, or
executable. Delivery jobs use the configured `.ts.net` HTTPS origin with
certificate verification, no ambient proxy, and no redirects. A lost response
retains an unknown delivery attempt and replays the same logical request under
a new fence. The service has no signing-key resolution path and refuses startup
when its process can access a configured signer reference.
The inbox worker reconciles the approved remote once for each pending batch,
then records each hint separately. A target covered by the local bounded
history is `covered`; a stale or unrelated target is `superseded` only after
the current approved remote head has reconciled, without claiming that hint's
target was imported. Invalid persisted payloads retain an `invalid_payload`
record. Deferred rows retain a reason and cannot block later rows. Enabled
`sync status` exposes pending and failed counts, the oldest pending timestamp,
and the oldest row's bounded reason. Configuration drift and worker failures
produce rate-limited service warnings. After a configuration revision changes,
the operator runs `sync reconcile` to refresh the guarded control revision and
restarts `sync serve` to load the new configuration. Until both actions succeed,
peer admission and delivery remain deferred.
The inbox retains at most 100,000 logical nudge identities per group.
Exhaustion refuses new nudges while the sender retains its delivery obligation;
`sync status` reports the retained count and limit for operator diagnosis.

## Import and attribution

An import plan records the from and target commits, validated membership and
publication evidence, resource observation revision, per-path before and after
digests or absence, and expected Git writer state before live files change.
The resource guard serializes participating Watchman planning, validation,
application, and the path-fact/effect commit. Import refuses changes overlapping target paths,
untracked overwrite collisions, or unstable Git/index/ref state. Proven
disjoint edits and out-of-scope or ignored files remain untouched and dirty.
Route-protected and immutable paths are outside the synchronized content scope;
a publication or import candidate touching one fails closed before content
mutation.
An advance that changes only controller-owned metadata uses the same durable
import job, fence, and pre-apply journal with `controller_only: true` and an
empty effect set. Recovery classifies its exact ref/index/worktree state before
retrying or settling; it never infers completion from the absence of Markdown
effects.
Without a current cooperative-import acknowledgement, reconciliation may fetch
and validate but defers live-tree application. Publication and import are
fast-forward-only: Agent Dispatch never merges, rebases, stashes, force-pushes,
resets, or cleans automatically, and conflict recovery proceeds through the
checkpoint plan/apply and reconcile workflow.
The acknowledgement binds a normalized credential-free remote repository
identity digest as well as the remote name. The Git adapter must resolve and
recheck that identity immediately before a protected effect so a remote
retarget cannot preserve currentness accidentally.

Watchman remains active, but its dispatch process waits on the same resource
guard before reading files or loading facts. Import completion updates path facts
and publishes immutable suppression evidence under the observation fence. A crash between file,
ref, index, and SQLite transitions enters recovery or uncertainty; it does not
infer success from partial state.

The implemented importer records immutable per-path before/after evidence in
`sync_import_effects` before live mutation. An exact Watchman observation may
consume the newest effect while its job is applying, recovering, uncertain, or
completed; this closes the process-loss window after filesystem/Git mutation
without treating a contradictory value as imported. The
`sync_import_attributions` row is committed with that source observation.
Older evidence cannot resurface after a later import, so a contradictory or
late same-path edit stays dirty.

The initial trusted state and any reviewed conflict resolution use an
administrator-signed adoption checkpoint. Every commit from the accepted
checkpoint to an imported target must be covered by a verified publication or
checkpoint. Previously unseen history from a rotated or revoked publisher key,
or history beyond the configured inspection bound, stops for a new checkpoint;
commit timestamps cannot restore trust.

## Liveness and verification

Nudges reduce latency but are not required for correctness. The peer worker
inspects configured Git refs at startup and every five minutes, using the same
guarded `sync reconcile` command as inbox wakes and operators. A successful
nudge wake may advance that timer. A failed run records exponential retry delay
from 30 seconds to five minutes in SQLite; a restart or repeated wake cannot
bypass that backoff. Each admitted nudge keeps its own durable identity and
resolution, even when one reconcile covers a batch. The worker also recovers
already-signed publication work without resolving a publisher signing key.
A pre-signature `eligible` or `prepared` obligation without a live publisher
claim remains for explicit `sync publish` re-entry. `sync status` reports its
count and oldest creation time, and the service warns without attempting to
sign it.

Deleted configured refs and non-fast-forward rewrites create a trust hold for
administrator review.

An offline peer does not block local publication, but fresh pair convergence
remains incomplete until both nodes answer with current authenticated evidence.
The peer listener binds an owner-only local Unix socket. The service does not
enable Funnel or public exposure and does not modify Tailscale configuration.

Verification rechecks membership and content refs before completion. A changed
target produces `target_changed` rather than a false success. Equal commit IDs
are insufficient when either node has governed local dirtiness, pending work,
or uncertain evidence.
Each status observation retains its measured age. Evidence is fresh only at
300 seconds or less; verification persists that age and derives the boolean
freshness flag from the same bound.

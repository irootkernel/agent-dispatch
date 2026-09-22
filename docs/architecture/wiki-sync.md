# Two-Node Wiki Sync Architecture

> **Status:** Partially implemented for v0.2.0 under D-030. E21 provides
> qualified durable jobs and control, restricted Git and membership, explicit
> signed publication, signed checkpoints, guarded local import, and exact
> attribution. Peer service, pair verification, and release remain unavailable.

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

future: peer service -> inbox -> the same reconciliation/import path
             ^
             +---- periodic reconciliation
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

The future service must use the same application methods as manual CLI commands
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
No peer service performs that recovery until E22, and a future service cannot
sign unattended.

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

Nudges reduce latency but are not required for correctness. Startup and
periodic reconciliation inspect configured Git refs and recover missed work.
An offline peer does not block local publication, but fresh pair convergence
remains incomplete until both nodes answer with current authenticated evidence.
The listener binds only to a loopback or tailnet-only endpoint, does not enable
Funnel or public exposure, and does not modify Tailscale configuration.

Verification rechecks membership and content refs before completion. A changed
target produces `target_changed` rather than a false success. Equal commit IDs
are insufficient when either node has governed local dirtiness, pending work,
or uncertain evidence.
Each status observation retains its measured age. Evidence is fresh only at
300 seconds or less; verification persists that age and derives the boolean
freshness flag from the same bound.

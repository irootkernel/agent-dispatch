# Two-Node Wiki Sync Architecture

> **Status:** Planned for v0.2.0 under D-030; no sync runtime is implemented in
> the shipped v0.1.8 baseline.

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
                                                   v
peer service -> inbox -> fetch and validate -> guarded import -> receipt
      ^                                      |
      +---- periodic reconciliation --------+
```

## Boundaries

The sync application services live beside the existing dispatch,
work-receipt, notification, and reconciliation services. They reuse the local
SQLite store and resource coordination rules. A restricted Git adapter owns
fixed-argument subprocess execution, delimiter-safe parsing, SSH signature
verification, explicit refspecs, deadlines, and output limits. The peer HTTP
adapter owns authentication, parsing, size limits, and response mapping only.

The service uses the same application methods as manual CLI commands. It does
not create an alternate Git or recovery path. Local SQLite state is never
placed in Git or on a network filesystem.

Membership administration is a separate signed Git history. The normal roster
has exactly two active nodes, but historical retired/revoked entries remain
auditable and an emergency revocation can place the group in a blocked
fewer-than-two state. Administrator and publisher keys are distinct roles.
Membership and content-checkpoint changes use reviewed two-phase plan/apply
commands with expected predecessors and no force update.
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
publisher key is available only to the explicit `sync publish` process. The
service may recover an already-signed candidate but cannot sign unattended.

## Import and attribution

An import plan records the from and target commits, validated membership and
publication evidence, resource observation revision, per-path before and after
digests or absence, and expected Git writer state before live files change.
The resource guard covers validation, application, path-fact update, and
observation attribution. Import refuses changes overlapping target paths,
untracked overwrite collisions, or unstable Git/index/ref state. Proven
disjoint edits and out-of-scope or ignored files remain untouched and dirty.
Route-protected and immutable paths are outside the synchronized content scope;
a publication or import candidate touching one fails closed before content
mutation.
Without a current cooperative-import acknowledgement, reconciliation may fetch
and validate but defers live-tree application. Publication and import are
fast-forward-only: Agent Dispatch never merges, rebases, stashes, force-pushes,
resets, or cleans automatically, and conflict recovery proceeds through the
checkpoint plan/apply and reconcile workflow.
The acknowledgement binds a normalized credential-free remote repository
identity digest as well as the remote name. The Git adapter must resolve and
recheck that identity immediately before a protected effect so a remote
retarget cannot preserve currentness accidentally.

Watchman continues recording observations. Import completion updates path facts
and suppression evidence under the observation fence. A crash between file,
ref, index, and SQLite transitions enters recovery or uncertainty; it does not
infer success from partial state.

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

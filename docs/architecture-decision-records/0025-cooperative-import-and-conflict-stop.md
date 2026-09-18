# ADR-0025: Cooperative import and conflict stop

- **Status:** Accepted
- **Date:** 2026-09-17
- **Decision:** D-030
- **Supersedes:** ADR-0001's note-mutation prohibition for the validated sync-import boundary only

## Context

Agent Dispatch can coordinate its own maintenance and sync writers, but it
cannot lock Obsidian or another editor. Git, the filesystem, and SQLite also do
not share one atomic transaction. Import therefore needs an explicit operating
condition and durable recovery evidence.

## Decision

Sync is disabled by default. A group may apply remote content automatically
only after the operator records a cooperative-import acknowledgement for the
exact resource, remote, refs, scope digest, local identity, pinned administrator
trust anchor, and safety policy, meaning the SYN-010 guard set together with the
configured import bounds. Changing any of those inputs invalidates the
acknowledgement. An authorized membership update under the same pinned trust
policy invalidates verification targets but not this acknowledgement.

Without a current acknowledgement, reconciliation may fetch and validate but
must defer live-tree application; publication does not require this
acknowledgement. With acknowledgement, an import may apply only when the
resource guard is held, participating Agent Dispatch writers are idle, no
staged or unstaged change overlaps an imported path, no untracked path would be
overwritten, Git has no operation or index/ref instability, and a durable
pre-apply journal records exact before and target state. Proven-disjoint local
changes and out-of-scope or ignored files remain untouched and dirty. If
disjointness cannot be proven, application defers.

The first trusted content state is an administrator-signed adoption checkpoint
over an exact commit, governed snapshot digest, scope and contract digests, and
membership revision. Every commit from the accepted checkpoint to an import
target must be covered by a verified publication or a later administrator
checkpoint; a signed tip cannot certify an uncovered ancestor. Bounded-history
exhaustion stops visibly and requires a new explicit checkpoint.

Watchman ingestion remains active during import. The controller suppresses
only observations that exact durable import evidence proves, including
deletions. Unrelated, contradictory, late, or uncertain observations remain
eligible for normal maintenance.

Publication and import are fast-forward-only. Divergence, unsafe local state,
remote history rewrite, or an ambiguous apply result stops automatic progress
with inspectable blocked or uncertain evidence. Agent Dispatch never merges,
rebases, stashes, force-pushes, resets, or cleans to manufacture success. The
operator resolves conflicts with ordinary Git procedures, creates an
administrator-signed checkpoint through `sync checkpoint plan|apply`, and then
runs `sync reconcile` to validate the new trusted base and clear only the
matching administrative block. Reconciliation alone cannot adopt unsigned or
uncovered history.

This decision narrowly supersedes ADR-0001's prohibition on Agent Dispatch note
mutation for the validated, journaled sync-import boundary only. Import
attribution uses a separate durable import record and never fabricates a Hermes
work receipt.

## Consequences

- Pair verification is observational. It distinguishes historical delivery
  from a fresh report that both nodes are at the same target with no governed
  dirtiness, pending work, or unresolved uncertainty.
- A timeout or lease expiry does not prove an external process stopped. A new
  writer requires termination or reconciliation evidence.
- Content synchronization does not claim that a local search index or an
  already-running Hermes session has refreshed.

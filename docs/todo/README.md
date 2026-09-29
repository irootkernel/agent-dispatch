# TODO and Future Work

This directory owns future epic-sized candidates that have not entered the
roadmap. Those candidate items have no roadmap identity or lifecycle status.

This directory may also hold temporary execution dossiers for roadmap epics whose
cross-task integration needs one bounded map. The roadmap remains the only
lifecycle authority for those epics.

Future work is not permission to partially implement these features during
roadmap tasks. Each priority candidate requires a new roadmap, requirement
changes, and where noted a new ADR/security review.

## Adopted execution dossiers

| Epic | Dossier | Scope |
|---|---|---|
| E23 | [Constrained sync management](TODO-SYNC-MANAGEMENT.md) | Planned successor provider after the current v0.2.0 paired release; Plugin EPIC-008 consumes its qualified contract |

E23's status and task order belong to the roadmap. This planning entry does
not start runtime work or change current release/production authority.

## Priority Candidates

| Candidate | Value | Entry condition | Key risk |
|---|---|---|---|
| MCP server for status and work receipts | Easier Hermes/agent tool use | v0.1 receipt CLI stable; authentication model approved | network/listener trust and confused deputy |
| Multi-vault certification | Govern multiple knowledge bases | one-vault state and retention proven; global limits defined | cross-route concurrency and operational complexity |
| General managed daemon beyond sync | Long-lived sources, timers, API, global backoff | the E22 sync service is delivered and another use case cannot reuse it | duplicate scheduler and lifecycle complexity |
| Inbound webhook source | External events | authentication, replay protection, body retention policy | replay, payload trust, public exposure |
| Git/CI source | Repository automation | source-event model proven generic | branch/worktree authority |
| Timer/process/queue/RSS sources | Operational activation | daemon or external scheduler design approved | broad workflow-engine scope |
| Generic HTTP target | Other runtimes | sink contract proven by two explicit adapters | vague durable acceptance |
| Safe generic agent CLI target | Runtime portability | executable allowlist and public machine contract required | command injection and weak receipts |
| Attachment indexing | PDFs/images/assets | content extraction, size, privacy, and semantic ownership designed | sensitive payload storage and cost |
| Snapshot-bound mode | Historical reproducibility | immutable artifact store and retention approved | storage/privacy and replay semantics |
| Shared remote operational state | Distributed deployment beyond Git-backed content and membership | local SQLite limitation reached and broker/workflow comparison completed | consensus, leases, duplicate side effects |
| N-member Wiki sync | More than the admitted two-node group | E22 two-node evidence accepted; membership, credential, fairness, and all-member bounds redesigned | credential growth, target completeness, and verification cost |
| Multiple sync groups or governed working copies | More resources under sync governance | E22 two-node evidence accepted; a successor to D-030 admits ownership and isolation rules | cross-group locking, configuration authority, and failure isolation |
| Attachment, binary, LFS, submodule, or symlink sync | Extend beyond the admitted Markdown scope | E22 two-node evidence accepted; a successor to D-030 defines content and path safety | data loss, storage bounds, and unsafe repository features |
| Automatic conflict merge or semantic resolution | Reduce operator conflict work | E22 conflict-stop evidence accepted; a successor to D-030 admits a deterministic resolution authority | silent content loss and split-brain history |
| Additional Plugin management beyond the E23/EPIC-008 action set | Broader Hermes operations | E23's constrained provider and a separate Plugin scope decision | version skew, lifecycle coupling, and split authority |
| Local dashboard | Operator convenience | stable status/management API exists | unnecessary daemon/network surface |
| Hermes management plugin | Native Hermes UI | public authenticated Agent Dispatch management API or MCP stable | lifecycle coupling and split authority |

## Future Hermes Plugin Constraints

Plugin EPIC-007 inspection is completed in `agent-dispatch-plugin`. Core E23
now owns the successor constrained sync management provider; Plugin EPIC-008
remains Deferred until E23-T7 and its own TASK-028 admission. The broader
possibilities below are not the admitted E23 action set and do not authorize
release/discard, publication, or other extra mutations.

A future plugin may:

- display route health and active/dirty state;
- list receipts, unknown dispatches, dead letters, and quarantine;
- issue explicit pause/resume, reconcile, retry, release, or discard requests;
- link Hermes task IDs to Agent Dispatch lineage.

It may not:

- own Watchman subscriptions;
- replace the SQLite state authority;
- access Agent Dispatch state by directly reading its database;
- make silent policy changes;
- automatically retry unknown delivery;
- become required for Agent Dispatch correctness.

## Daemon Entry Criteria

A daemon is justified only when at least one proven requirement cannot be met cleanly by Watchman triggers, SQLite, public Hermes interfaces, and external scheduling. Examples:

- a long-lived authenticated inbound source;
- global scheduling across many routes;
- persistent status API or MCP server;
- continuous target health probing;
- message queue subscription.

The daemon must reuse the same application services and SQLite state machines. It must not create a second implementation path.

## Multi-Vault Entry Criteria

Before certifying multiple vaults:

- define global vs per-route budgets;
- define fairness and starvation behavior;
- define state directory and database sizing;
- test overlapping or nested roots;
- define whether mutexes are per resource, route, or target;
- test simultaneous Watchman trigger processes for different routes;
- update retention and doctor output.

## MCP Entry Criteria

MCP should expose existing application ports rather than duplicate logic. Candidate tools:

```text
agent-dispatch_status
agent-dispatch_dispatch_show
agent-dispatch_work_begin
agent-dispatch_work_complete
agent-dispatch_work_fail
agent-dispatch_reconcile
agent-dispatch_quarantine_release
```

Authentication, local transport, permission scopes, prompt-injection boundaries, and destructive-action confirmation require an ADR before implementation.

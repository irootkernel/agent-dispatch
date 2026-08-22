# Future Work

Future work is not permission to partially implement these features during v0.1 tasks. Each item requires a new roadmap, requirement changes, and where noted a new ADR/security review.

## Priority Candidates

| Candidate | Value | Entry condition | Key risk |
|---|---|---|---|
| MCP server for status and work receipts | Easier Hermes/agent tool use | v0.1 receipt CLI stable; authentication model approved | network/listener trust and confused deputy |
| Multi-vault certification | Govern multiple knowledge bases | one-vault state and retention proven; global limits defined | cross-route concurrency and operational complexity |
| Managed daemon | Long-lived sources, timers, API, global backoff | one-shot limits are demonstrated by real need | duplicate scheduler and lifecycle complexity |
| Inbound webhook source | External events | authentication, replay protection, body retention policy | replay, payload trust, public exposure |
| Git/CI source | Repository automation | source-event model proven generic | branch/worktree authority |
| Timer/process/queue/RSS sources | Operational activation | daemon or external scheduler design approved | broad workflow-engine scope |
| Generic HTTP target | Other runtimes | sink contract proven by two explicit adapters | vague durable acceptance |
| Safe generic agent CLI target | Runtime portability | executable allowlist and public machine contract required | command injection and weak receipts |
| Attachment indexing | PDFs/images/assets | content extraction, size, privacy, and semantic ownership designed | sensitive payload storage and cost |
| Snapshot-bound mode | Historical reproducibility | immutable artifact store and retention approved | storage/privacy and replay semantics |
| Remote or multi-host state | Distributed deployment | SQLite limitation reached and broker/workflow comparison completed | consensus, leases, duplicate side effects |
| Local dashboard | Operator convenience | stable status/management API exists | unnecessary daemon/network surface |
| Hermes management plugin | Native Hermes UI | public authenticated Agent Dispatch management API or MCP stable | lifecycle coupling and split authority |

## Future Hermes Plugin Constraints

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

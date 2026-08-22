# Decision Resolution from Discussion Draft

This document records how the approved SOT resolves the discussion draft and later design review.

| Topic | Final decision |
|---|---|
| Product identity | Event-ingress and activation gateway. Avoid claiming ownership of agent execution control. |
| Authoritative runtime | Hermes. |
| Primary use case | Watch one Obsidian vault and request LLM Wiki indexing, referencing, and grouping maintenance in Hermes. |
| Hermes modification | Prohibited in v0.1. Use public CLI/webhook only. No internal database access. |
| Hermes plugin | Deferred future management surface; never a core dependency. |
| Allowed companion integration | Agent Dispatch CLI and Hermes companion skill in v0.1; MCP considered later. |
| Primary target | Hermes Kanban public interface. |
| Secondary target | Hermes webhook after the Kanban production gate. No automatic failover. |
| First source | Watchman one-shot trigger. |
| Daemon | Deferred until long-lived subscriptions or global scheduling justify it. |
| Persistence | SQLite mandatory before the first side effect. |
| Delivery guarantee | At-least-once with idempotent consumption when supported. |
| Processing time model | Latest-state. The event is evidence, not an immutable content snapshot. |
| Initial file scope | Markdown only. |
| Initial vault scope | One certified vault and route; configuration remains extensible. |
| Meaningful modification | Effective digest changed. Metadata-only updates are ignored. |
| Rename | Correctness relies on create/delete evidence; paired rename is optional derived evidence. |
| Git | Optional enrichment, not mandatory and not sole provenance. |
| Concurrent work | One unresolved Hermes maintenance task per route. Later changes become one durable dirty generation. |
| Approval | Agent Dispatch may quarantine or release dispatch; Hermes owns agent execution approval. |
| Agent-origin attribution | Validated work receipt from Agent Dispatch companion CLI/skill. Unknown attribution fails conservative. |
| Record model | Observation, batch, decision, intent, attempt, acceptance receipt, and work receipt are separate. |
| Replay | Removed as an ambiguous operation. Use retry, reprocess, rerun, or reconcile. |
| Retention defaults | Observations/attempts 30 days, completed receipts 180 days, unresolved records until resolution. |
| Language and stack | Go, YAML, SQLite. macOS primary and Linux verified. |
| Roadmap | 7 epics and 32 linear tasks. |

## Intentionally Unresolved Until E0-T4

The approved SOT does not invent Hermes command names, flags, endpoints, response fields, or durability guarantees. `E0-T4` must inspect the real public interface and produce a capability mapping before the Hermes adapter is implemented.

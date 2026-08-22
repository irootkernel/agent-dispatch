# Architecture Decision Records

Accepted ADRs are normative below `required-spec.md` and above general architecture guidance.

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-event-ingress-activation-gateway.md) | Agent Dispatch is an event-ingress and activation gateway | Accepted |
| [0002](0002-hermes-authoritative-public-interface-only.md) | Hermes is authoritative; public interfaces only | Accepted |
| [0003](0003-watchman-one-shot-first.md) | Watchman one-shot trigger first; no v0.1 daemon | Accepted |
| [0004](0004-go-yaml-sqlite-stack.md) | Go, YAML, and SQLite stack | Accepted |
| [0005](0005-sqlite-before-side-effects.md) | SQLite intent commit before side effects | Accepted |
| [0006](0006-at-least-once-and-unknown-state.md) | At-least-once delivery and first-class unknown | Accepted |
| [0007](0007-separate-records-and-identities.md) | Separate records and identity concepts | Accepted |
| [0008](0008-latest-state-processing.md) | Latest-state processing for the vault | Accepted |
| [0009](0009-single-active-task-dirty-generation.md) | One active task and durable dirty generation | Accepted |
| [0010](0010-no-automatic-sink-failover.md) | No automatic Kanban/webhook failover | Accepted |
| [0011](0011-plugin-free-hermes-cooperation.md) | CLI and skill receipts without Hermes plugin | Accepted |
| [0012](0012-v01-markdown-single-vault-scope.md) | v0.1 certified scope is Markdown and one vault | Accepted |
| [0013](0013-structural-policy-and-quarantine.md) | Structural policy only, with quarantine | Accepted |
| [0014](0014-git-optional-evidence.md) | Git is optional supporting evidence | Accepted |
| [0015](0015-future-hermes-plugin-management-only.md) | Future Hermes plugin is management-only | Accepted |

## ADR Lifecycle

Statuses: Proposed, Accepted, Superseded, Rejected.

An accepted ADR may be changed only by a new ADR that explicitly supersedes it. Do not edit historical rationale to make a later decision appear original.

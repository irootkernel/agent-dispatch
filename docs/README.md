# JJUKKUMI Source of Truth

> **SOT version:** 1.0.3  
> **Implementation target:** JJUKKUMI v0.1.0  
> **Document status:** Approved baseline  
> **Tagline:** **Sense. Catch. Route.**

JJUKKUMI is a local-first **event-ingress and activation gateway**. Its first production use case is to observe meaningful Markdown changes in one Obsidian vault and create a durable, reviewable unit of work in Hermes so that a configured LLM Wiki maintainer can re-evaluate indexing, referencing, and grouping.

JJUKKUMI does not interpret the knowledge base, edit notes, execute agent workflows, or replace Hermes. Hermes is the authoritative agent runtime. JJUKKUMI observes, normalizes, governs, persists, dispatches, reconciles, and audits activation requests.

## SOT Authority Order

When documents conflict, use this order:

1. [`docs/00-sot/required-spec.md`](docs/00-sot/required-spec.md)
2. Accepted Architecture Decision Records under [`docs/adr/`](docs/adr/)
3. Contract documents under [`docs/02-contracts/`](docs/02-contracts/)
4. Architecture documents under [`docs/01-architecture/`](docs/01-architecture/)
5. The executable roadmap under [`docs/03-roadmap/roadmap.md`](docs/03-roadmap/roadmap.md)
6. Implementation and operations guidance
7. Examples
8. The original discussion draft under [`docs/99-source/`](docs/99-source/)

The original discussion draft is retained for provenance. It is not authoritative where this SOT resolves or changes a decision.

## Start Here

| Need | Document |
|---|---|
| Product purpose, scope, and success definition | [`project-charter.md`](docs/00-sot/project-charter.md) |
| Normative requirements | [`required-spec.md`](docs/00-sot/required-spec.md) |
| Definitions | [`terminology.md`](docs/00-sot/terminology.md) |
| Release acceptance gates | [`acceptance-criteria.md`](docs/00-sot/acceptance-criteria.md) |
| System structure | [`architecture-overview.md`](docs/01-architecture/architecture-overview.md) |
| Domain records and state | [`domain-model.md`](docs/01-architecture/domain-model.md) |
| Watchman behavior | [`watchman-integration.md`](docs/01-architecture/watchman-integration.md) |
| Hermes boundary | [`hermes-integration.md`](docs/01-architecture/hermes-integration.md) |
| Configuration and CLI contracts | [`docs/02-contracts/`](docs/02-contracts/) |
| Epic and task status | [`roadmap.md`](docs/03-roadmap/roadmap.md) |
| Coding guidance | [`implementation-guide.md`](docs/04-implementation/implementation-guide.md) |
| Runtime recovery | [`runbook.md`](docs/05-operations/runbook.md) |
| Design decisions | [`docs/adr/README.md`](docs/adr/README.md) |
| Post-baseline decisions and errata | [`decision-log.md`](docs/00-sot/decision-log.md) |
| Package validation | [`VALIDATION.md`](VALIDATION.md) |
| File checksums | [`MANIFEST.sha256`](MANIFEST.sha256) |

## Frozen v0.1 Product Shape

```text
Watchman trigger
    -> jjukkumi dispatch --route <route-id>
        -> deterministic validation and policy
        -> SQLite durable dispatch intent
        -> Hermes public Kanban interface
            -> Hermes agent runtime and LLM Wiki skill
                -> optional JJUKKUMI work receipt CLI
```

A Hermes webhook adapter is delivered after the Kanban path is production-capable. It is an explicit immediate-delivery option, never an automatic failover for an ambiguous Kanban submission.

## Explicit Hermes Boundary

The v0.1 implementation:

- does **not** modify Hermes core;
- does **not** access Hermes internal storage;
- does **not** require or create a Hermes plugin;
- integrates only through public Hermes machine interfaces;
- may ship a JJUKKUMI CLI and a Hermes-facing skill that an agent can use to report work provenance;
- defers an optional Hermes management plugin to future work.

## Roadmap State

The project is pre-implementation. Documentation baseline tasks `E0-T1` through `E0-T3` are complete. `E0-T4` is complete: the real Hermes 0.19.1 public interface was verified and the capability baseline frozen in [`integrations/hermes-public-interface-report.md`](integrations/hermes-public-interface-report.md) with a validated [`integrations/hermes-capability-report.json`](integrations/hermes-capability-report.json). `E0-T5` is complete: the real Watchman 2026.07.27.00 public interface was verified and the parser fixture baseline frozen in [`integrations/watchman-public-interface-report.md`](integrations/watchman-public-interface-report.md) with the sanitized real-payload corpus under [`integrations/fixtures/watchman/`](integrations/fixtures/watchman/). Epic `E0` and gate `G0` are closed. The next executable task is `E1-T1`, which bootstraps the Go repository, toolchain, and verification pipeline.

Only one roadmap task may be active globally. See [`task-execution-rules.md`](docs/03-roadmap/task-execution-rules.md).

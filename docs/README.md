# JJUKKUMI Source of Truth

> **SOT version:** 1.0.4  
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

The project is in implementation. Documentation baseline tasks `E0-T1` through `E0-T3` are complete. `E0-T4` is complete: the real Hermes 0.19.1 public interface was verified and the capability baseline frozen in [`integrations/hermes-public-interface-report.md`](integrations/hermes-public-interface-report.md) with a validated [`integrations/hermes-capability-report.json`](integrations/hermes-capability-report.json). `E0-T5` is complete: the real Watchman 2026.07.27.00 public interface was verified and the parser fixture baseline frozen in [`integrations/watchman-public-interface-report.md`](integrations/watchman-public-interface-report.md) with the sanitized real-payload corpus under [`integrations/fixtures/watchman/`](integrations/fixtures/watchman/). Epic `E0` and gate `G0` are closed. `E1-T1` is complete: the Go repository, toolchain, and verification pipeline are bootstrapped (`make verify` at the repository root). `E1-T2` is complete: configuration loading with duplicate-key rejection and fail-closed unknown fields, schema plus semantic validation, secret-reference parsing without resolution, redacted normalized output, the deterministic route revision, platform paths, reproducible `examples/config.yaml` validation, and `jjukkumi init` are implemented. `E1-T3` is complete: domain IDs (UUIDv7), typed digests, closed record enums, canonical change ordering, and the deterministic content fingerprint and idempotency-key derivations with property and golden tests are implemented. `E1-T4` is complete: the durable SQLite schema, forward-only checksummed migrations with pre-migration verified backups, and the repository layer with append-only audit history are implemented and tested against real database files. The E1 audit and validation are complete with a clean final review, and the epic is closed. `E2-T1` is complete: the bounded Watchman input parser (bounded stdin reader, strict payload parser over the frozen E0-T5 corpus shapes, trusted environment allowlist, source binding validation, opaque position model, raw payload digest, and dedicated payload/environment schemas) is implemented in `internal/adapters/watchman`. `E2-T2` is complete: the deterministic path pattern policy engine (`internal/domain/policy`, include/exclude/protected/immutable with `**` recursion and built-in default exclusions) and the safe root resolver with containment and symlink-escape defense (`internal/adapters/localfs`) are implemented with golden and security-focused tests. `E2-T3` is complete: meaningful-change confirmation and batch normalization (`internal/app/ingest`) — containment-checked bounded SHA-256 hashing, same-path coalescing per the architecture rules, unchanged-modify suppression against prior path facts, and deterministic sorted batches with content fingerprints — plus the path-fact and optional Git-evidence ports. `E2-T4` is complete: the pure structural policy planner (`internal/app/dispatch`) with architecture §5 disposition precedence, machine-readable reason codes, and schema-validating golden tests, plus the side-effect-free `jjukkumi route plan` and `jjukkumi dispatch --dry-run` commands. `E2-T5` is complete: the managed Watchman trigger lifecycle (`watchman install|status|remove|test` with idempotent install, explicit-flag replacement, exact-trigger removal, and actionable absence handling in `config validate`) is implemented and verified against the real installed Watchman, and gate G1 (AC-101..110) is closed with executable acceptance evidence (see `VALIDATION.md` §Gate G1). The E2 validation audit closed with one cross-task remediation commit, and the epic is closed with gate G1 verified. `E3-T1` is complete: the dispatch and route state machines as one authoritative validated domain table with typed reasons, guards, exhaustive matrices, and the acceptance/execution projection separation (`internal/domain/state`). `E3-T2` is complete: the sink and dispatch-store ports, the SQLite implementation whose transactions never span a sink call, the durable submit flow, and the TST-006 fake sink. `E3-T3` is complete: the bounded retry policy with injectable jitter, the adapter result classifier, unknown reconciliation, dead-letter handling with operator retry/rerun, the `dispatches` and `route` command groups with stable exit codes, and the dispatch-attempt and dead-letter record contracts. `E3-T4` is complete: the route coordination core — one active dispatch under concurrency, durable dirty generations, the merge-pending transaction, and the single latest-state follow-up collapse. `E3-T5` is complete: the crash-injection framework and multi-process harness closed gate G2 (AC-201..207) with executable evidence (see `VALIDATION.md` §Gate G2). The E3 validation audit remediated sixteen verified findings in one cross-task commit and closed with a clean re-audit; the epic is closed with gate G2 verified. The next executable task is `E4-T1`, the Hermes Kanban adapter and capability probe.

Only one roadmap task may be active globally. See [`task-execution-rules.md`](docs/03-roadmap/task-execution-rules.md).

# Agent Dispatch Implementation Roadmap

> **Roadmap version:** 1.2
> **Release target:** v0.1.6
> **Execution model:** Strictly linear, one active task globally  
> **Epics:** 18
> **Tasks:** 89

## 1. Current State

| Field | Value |
|---|---|
| Shipped release | v0.1.5 (published 2026-08-30) |
| Planned SOT baseline | 1.2.0 ([D-027](../specs/decision-log.md)) |
| Release target | v0.1.6 (planned) |
| Current epic | E16 Completed (G12 evidenced); next E17 |
| Current active task | None |
| Next task | E17-T1 |
| Completed tasks | 86 / 89 |
| Planned tasks | 3 / 89 |
| In progress tasks | 0 |
| Blocked tasks | 0 |
| Deferred tasks in v0.1 sequence | 0 |

### Historical evidence

- [Changelog](../CHANGELOG.md) records released changes and historical corrections.
- [Validation](../VALIDATION.md) records executable gates and release evidence.
- [Traceability matrix](../specs/traceability-matrix.md) maps requirements and
  acceptance criteria to roadmap tasks.

## 2. Epic Summary

| Epic | Title | Status | Tasks | Completion gate |
|---|---|---:|---:|---|
| E0 | SOT and External Contract Baseline | **Completed** | 5 | G0 |
| E1 | Go Foundation, Configuration, and Persistence Schema | **Completed** | 4 | Foundation ready |
| E2 | Watchman Deterministic Dry-Run Pipeline | **Completed** | 5 | G1 |
| E3 | Durable Dispatch and Route Coordination Core | **Completed** | 5 | G2 |
| E4 | Hermes Kanban Durable Integration | **Completed** | 5 | G3 |
| E5 | Feedback Loop, Quarantine, and Reconciliation | **Completed** | 5 | G4 |
| E6 | Hermes Webhook, Operations, Packaging, and v0.1 Release | **Completed** | 4 | G5 |
| E7 | MVP Compliance Review Remediation | **Completed** | 12 | MUST closure + v0.1.1 |
| E8 | v0.1.2 Compliance Remediation | **Completed** | 6 | MUST closure + v0.1.2 |
| E9 | Deferred-Inventory Hardening | **Completed** | 9 | Deferred closure + review remediation + v0.1.4 |
| E10 | Source and Reconciliation Integrity | **Completed** | 3 | G6 |
| E11 | Hermes Preflight and Operator Setup | **Completed** | 4 | G7 |
| E12 | Multi-Destination Lifecycle | **Completed** | 4 | G8 |
| E13 | Notifications and v0.1.5 Release | **Completed** | 4 | G9 |
| E14 | Guided Setup and Disabled Baseline | **Completed** | 3 | G10 |
| E15 | Hermes v0.20.5+ Compatibility and Serialization Groups | **Completed** | 4 | G11 |
| E16 | Automatic Durable Notification Draining | **Completed** | 4 | G12 |
| E17 | Documentation, Cold Validation, and v0.1.6 Release | **Planned** | 3 | G13 |

## 3. Task Status Index

| Order | Task | Status | Primary result |
|---:|---|---|---|
| 1 | E0-T1 | Completed | Product charter and scope frozen |
| 2 | E0-T2 | Completed | Architecture, contracts, and ADR baseline frozen |
| 3 | E0-T3 | Completed | Roadmap, acceptance, and traceability frozen |
| 4 | E0-T4 | Completed | Real Hermes public capability report |
| 5 | E0-T5 | Completed | Watchman fixture baseline frozen |
| 6 | E1-T1 | Completed | Buildable Go repository and verification pipeline |
| 7 | E1-T2 | Completed | Validated YAML configuration and path layout |
| 8 | E1-T3 | Completed | Domain primitives, IDs, digests, canonicalization |
| 9 | E1-T4 | Completed | SQLite schema, migrations, and repositories |
| 10 | E2-T1 | Completed | Bounded Watchman input parser |
| 11 | E2-T2 | Completed | Safe path containment and pattern engine |
| 12 | E2-T3 | Completed | Meaningful-change, hashing, and batch normalization |
| 13 | E2-T4 | Completed | Deterministic policy planner and dry-run CLI |
| 14 | E2-T5 | Completed | Real Watchman trigger lifecycle and G1 fixtures |
| 15 | E3-T1 | Completed | Validated dispatch and route state machines |
| 16 | E3-T2 | Completed | Durable intent transaction and attempt leases |
| 17 | E3-T3 | Completed | Retry, unknown, reconciliation, and dead-letter core |
| 18 | E3-T4 | Completed | One active route task and dirty generations |
| 19 | E3-T5 | Completed | Crash, migration, and concurrency gate G2 |
| 20 | E4-T1 | Completed | Hermes Kanban adapter capability implementation |
| 21 | E4-T2 | Completed | Safe Hermes task request renderer |
| 22 | E4-T3 | Completed | Submit, idempotency lookup, and delivery reconciliation |
| 23 | E4-T4 | Completed | Acceptance and execution receipt projection |
| 24 | E4-T5 | Completed | Real Hermes Kanban end-to-end gate G3 |
| 25 | E5-T1 | Completed | Work receipt CLI and validation |
| 26 | E5-T2 | Completed | Packaged Hermes companion skill |
| 27 | E5-T3 | Completed | Exact suppression, mixed changes, and follow-up collapse |
| 28 | E5-T4 | Completed | Protected/bulk quarantine and full reconciliation |
| 29 | E5-T5 | Completed | Production-capable test-vault gate G4 |
| 30 | E6-T1 | Completed | Explicit Hermes webhook adapter |
| 31 | E6-T2 | Completed | Doctor, status, retention, and operational observability |
| 32 | E6-T3 | Completed | Packaging and scheduled reconciliation (macOS; the Linux leg is retired by D-023) |
| 33 | E6-T4 | Completed | v0.1 release verification and final SOT reconciliation |
| 34 | E7-T1 | Completed | Documentation truth restored after the compliance review |
| 35 | E7-T2 | Completed | Crash recovery, rerun supersession, follow-up activation |
| 36 | E7-T3 | Completed | Submit-path revalidation and durable path facts |
| 37 | E7-T4 | Completed | Gate-evidence tests repaired and platform guards added |
| 38 | E7-T5 | Completed | CLI inspection contract completed |
| 39 | E7-T6 | Completed | Write gates, audit rows, and decision records closed |
| 40 | E7-T7 | Completed | Migration lock, WAL classification, operator exits |
| 41 | E7-T8 | Completed | Payload versioning enforced and Hermes rendering completed |
| 42 | E7-T9 | Completed | Operations and security medium batch remediated |
| 43 | E7-T10 | Completed | Licensing, layout, and documentation consistency |
| 44 | E7-T11 | Completed | Low and informational findings dispositioned |
| 45 | E7-T12 | Completed | MUST closure re-verified and v0.1.1 prepared |
| 46 | E8-T1 | Completed | Follow-up loop state-machine defects closed |
| 47 | E8-T2 | Completed | Recovery wired into every submit path; operator exits repaired |
| 48 | E8-T3 | Completed | Behavior-sensitive revision and enforced production gate |
| 49 | E8-T4 | Completed | Unresolved lineage preserved; doctor made trustworthy |
| 50 | E8-T5 | Completed | Input containment and configuration validation gaps closed |
| 51 | E8-T6 | Completed | Documentation truth restored and v0.1.2 released |
| 52 | E9-T1 | Completed | Record schema truth and storage hardening |
| 53 | E9-T2 | Completed | Reconciliation and operator-surface hardening |
| 54 | E9-T3 | Completed | Security, observability, and revision hygiene |
| 55 | E9-T4 | Completed | Test-coverage hardening |
| 56 | E9-T5 | Completed | Documentation truth, dependency, and the v0.1.3 release |
| 57 | E9-T6 | Completed | Submission-gate revision and capability-report integrity |
| 58 | E9-T7 | Completed | RFC 9110 header grammar and pinned-toolchain enforcement |
| 59 | E9-T8 | Completed | macOS-only support policy and Linux-surface removal |
| 60 | E9-T9 | Completed | Documentation truth resynchronized and v0.1.4 released |
| 61 | E10-T1 | Completed | Resource observation fence and bounded reconciliation reads |
| 62 | E10-T2 | Completed | Effective Watchman binding and route-relative exclusions |
| 63 | E10-T3 | Completed | Source/reconciliation integrity gate G6 |
| 64 | E11-T1 | Completed | Config v1 destination cutover and forward migration |
| 65 | E11-T2 | Completed | Hermes capability probe and evidence cache |
| 66 | E11-T3 | Completed | Destination profile/skill validation and route preflight |
| 67 | E11-T4 | Completed | Discoverable CLI, guided setup, operator skill, and G7 |
| 68 | E12-T1 | Completed | Aggregate event and destination child persistence |
| 69 | E12-T2 | Completed | Per-destination lanes, conditions, fan-out, and retry isolation |
| 70 | E12-T3 | Completed | Work-receipt/v2, aggregate status, and completion evidence |
| 71 | E12-T4 | Completed | Multi-destination and completion gate G8 |
| 72 | E13-T1 | Completed | Notification event contract and transactional outbox |
| 73 | E13-T2 | Completed | Webhook/log sinks, retry commands, and scheduling |
| 74 | E13-T3 | Completed | Operator/worker skills and operational walkthrough |
| 75 | E13-T4 | Completed | Documentation truth, release proof, and v0.1.5 |
| 76 | E14-T1 | Completed | Explicit setup route selection and propagation |
| 77 | E14-T2 | Completed | Disabled baseline-only reconciliation and persistence |
| 78 | E14-T3 | Completed | Rerunnable setup, five-state summary, and G10 |
| 79 | E15-T1 | Completed | Serialization configuration, revisions, and migration |
| 80 | E15-T2 | Completed | Consistent local serialization and optional target mutex contract |
| 81 | E15-T3 | Completed | Group slot enforcement and bounded follow-up |
| 82 | E15-T4 | Completed | Hermes v0.20.5+ compatibility gate G11 |
| 83 | E16-T1 | Completed | Drain policy, leases, and forward migration |
| 84 | E16-T2 | Completed | Lease-safe bounded notification delivery |
| 85 | E16-T3 | Completed | Post-commit after-command integration |
| 86 | E16-T4 | Completed | Scheduler, status, doctor, and G12 |
| 87 | E17-T1 | Planned | CLI, configuration, operations, and skill truth |
| 88 | E17-T2 | Planned | Cold validation and required deployment evidence |
| 89 | E17-T3 | Planned | Reproducible v0.1.6 release and publication |

---

# E0: SOT and External Contract Baseline

**Epic status:** Completed  
**Purpose:** Freeze the product and architecture, then replace all Hermes assumptions with verified public-interface evidence.  
**Gate:** G0

## E0-T1: Freeze Product Charter, Scope, and Terminology

**Status:** Completed

### Objective

Define exactly what Agent Dispatch is, what Hermes owns, the first Obsidian use case, v0.1 scope, and prohibited expansion.

### Deliverables

- `docs/specs/project-charter.md`
- `docs/specs/terminology.md`
- resolved decision log

### Requirements

`BND-*`, `SCP-001` through `SCP-004`, `SCP-009`

### Acceptance

- Hermes is explicitly authoritative.
- Hermes core modification, internal storage access, and plugin dependency are excluded.
- latest-state Obsidian Markdown maintenance is the first use case.
- no semantic Wiki logic is assigned to Agent Dispatch.

### Evidence

This SOT package.

## E0-T2: Freeze Architecture, Domain Records, and Contracts

**Status:** Completed

### Objective

Define components, trust boundaries, record separation, identity semantics, state machines, Watchman and Hermes logical contracts, and SQLite durability rules.

### Deliverables

- architecture documents
- contract documents
- schemas and examples
- ADR baseline

### Requirements

`DAT-*`, `DUR-001` through `DUR-006`, `HER-001` through `HER-010`, `FBK-*`, `SEC-*`

### Acceptance

- source observation, batch, decision, intent, attempt, acceptance receipt, and work receipt are separate.
- unknown acceptance has a first-class state.
- no external side effect precedes durable intent commit.
- no exact Hermes command is invented.

### Evidence

This SOT package.

## E0-T3: Freeze Roadmap, Acceptance Gates, and Traceability

**Status:** Completed

### Objective

Define a strict linear implementation sequence that incrementally satisfies the required specification.

### Deliverables

- this roadmap
- task execution rules
- release acceptance matrix
- requirement traceability matrix

### Requirements

`TST-008`, `TST-009`

### Acceptance

- exactly 7 epics and 33 ordered tasks exist.
- only one task can be In Progress or In Review globally.
- every task has objective, deliverables, requirements, dependencies, and acceptance criteria.
- release gates map to cumulative required state.

### Evidence

This SOT package.

## E0-T4: Verify Hermes Public Interface and Freeze Capability Baseline

**Status:** Completed

### Objective

Inspect the actual Hermes installation and document only public machine interfaces that Agent Dispatch may use. Determine whether the required durable Kanban contract is feasible without modifying Hermes.

### Inputs

- installed Hermes binary and public documentation/help;
- `docs/architecture/hermes-integration.md`;
- `schemas/hermes-capability-report.schema.json`.

### Deliverables

- `docs/integrations/hermes-public-interface-report.md`;
- a validated real capability report JSON;
- sanitized command/response fixtures;
- a compatibility decision: supported, supported with named reduced guarantee, or blocked;
- exact supported Hermes version range.

### Required Work

1. Discover version and public help in machine-readable form where possible.
2. Verify task creation against a disposable Hermes workspace/board.
3. Determine what proves durable acceptance.
4. Verify idempotency submission and duplicate behavior.
5. Verify lookup by idempotency key and task ID.
6. Verify profile, skill, workspace, mutex, runtime hint, retry hint, and status fields.
7. Verify output schemas and exit/error behavior.
8. Inspect public webhook capabilities separately without implementing them.
9. Confirm no internal database or private API was used.

### Requirements

`BND-002` through `BND-004`, `HER-002`, `HER-004`, `HER-005`, `HER-009`, `HER-010`, `TST-007`

### Dependencies

E0-T3 Completed.

### Acceptance

- AC-001 and AC-002 pass.
- Every capability claim includes reproducible public-interface evidence.
- Unsupported capabilities are false, not guessed.
- If durable acceptance plus safe reconciliation cannot be achieved publicly, set this task to Blocked and request a product decision. Do not continue to E1 by silently weakening the contract.

### Evidence

`docs/integrations/hermes-public-interface-report.md`, validated `docs/integrations/hermes-capability-report.json`, and sanitized fixtures under `docs/integrations/fixtures/hermes/`, produced against the then-baseline Hermes on 2026-08-19 using only the public CLI and a disposable, deleted probe board; superseded by the 0.20.5 baseline evidence of E15-T4. Compatibility decision: supported.

### Out of Scope

- writing the adapter;
- changing Hermes;
- creating a plugin;
- parsing Hermes internal storage.

## E0-T5: Verify Watchman Public Interface and Freeze Fixture Baseline

**Status:** Completed

### Objective

Replace Watchman payload, field, and trigger-behavior assumptions with verified evidence from a real Watchman installation, mirroring E0-T4's Hermes verification, so that parser and policy work in E2 builds on frozen evidence.

### Deliverables

- verified Watchman version/field fixture report;
- frozen real-payload fixture corpus for parser tests;
- trigger install/status/remove/test behavior notes;
- absence/failure behavior notes for doctor output.

### Requirements

`SRC-001` through `SRC-003`, `SRC-008`, `TST-003`

### Dependencies

E0-T3 Completed.

### Acceptance

- Fixture report and corpus are produced from a real Watchman installation.
- Every parser assumption in E2-T1 is traceable to a frozen fixture.
- Watchman absence and failure behavior is documented.

### Evidence

`docs/integrations/watchman-public-interface-report.md` and the 33-file frozen corpus under `docs/integrations/fixtures/watchman/`, produced against Watchman 2026.07.27.00 (Homebrew, fsevents watcher) on 2026-08-19 using only the public CLI and a disposable, deleted probe watch root. The assumed `WATCHMAN_FILES_OVERFLOW` field was refuted at runtime and its conservative replacement (missing-position handling per architecture §7) verified; baseline decision: supported and frozen.

---

# E1: Go Foundation, Configuration, and Persistence Schema

**Epic status:** Completed  
**Purpose:** Create a buildable, testable foundation with no source or target side effects.

## E1-T1: Bootstrap Repository, Toolchain, and Verification Pipeline

**Status:** Completed

### Objective

Create the Go repository, package boundaries, build metadata, lint/test commands, and CI baseline.

### Deliverables

- `go.mod` with pinned toolchain policy;
- `cmd/agent-dispatch` minimal executable;
- internal package skeleton matching repository layout;
- build/version package;
- Makefile with deterministic verification targets for the package checks (manifest checksums, schema/example validation, traceability regeneration) as the single verification entrypoint (D-015);
- Go schema and example validation using a standard Draft 2020-12 validator, replacing `docs/scripts/validate-json-schemas.py` with its self-test cases migrated to Go unit tests (D-015);
- `.gaori/tester.yaml` commands re-targeted to the Makefile verification targets (D-015);
- CI for format, vet/static checks, unit tests, race test where supported, and schema/example validation (hosted CI was removed on 2026-08-22; verification is `make verify` run per platform, D-017);
- contribution and local verification instructions.

### Requirements

`SCP-005`, `SCP-008`, `CLI-001`, `TST-009`

### Dependencies

E0-T4 Completed; E0-T5 Completed.

### Acceptance

- clean checkout builds on macOS and Linux CI (superseded: hosted CI was removed on 2026-08-22 and never recorded a run; verification is `make verify` per platform, D-017);
- `agent-dispatch version --output json` follows the CLI envelope;
- no domain behavior is stubbed with false success;
- package dependency direction is enforceable;
- repository verification command passes;
- the Python subset validator under `docs/scripts/` is retired with its checks and self-tests migrated to the Go validator and Makefile targets, and `gaori config check` passes with commands invoking those targets.

### Out of Scope

Config parsing, SQLite tables, Watchman, and Hermes invocation.

### Evidence

- `Makefile` (`make verify`) is the single verification entrypoint: format, vet, staticcheck, import-direction lint, unit tests, race tests, docs manifest checksums, Go Draft 2020-12 schema/example validation, and traceability regeneration.
- `internal/schemavalid` with migrated self-tests (`internal/schemavalid/selftest_test.go`); the Python subset validator `docs/scripts/validate-json-schemas.py` is retired.
- `.gaori/tester.yaml` invokes the Makefile targets; `gaori config check` passes.
- `.github/workflows/ci.yml` was removed on 2026-08-22: GitHub Actions is not used. Local `make verify` on macOS is the recorded evidence; the earlier instruction to also run a supported Linux host before Linux-targeting releases rode SCP-008's Linux clause, which D-023 supersedes (macOS is the only supported platform).
- `agent-dispatch version --output json` follows the CLI envelope (`internal/cli/cli_test.go`).

## E1-T2: Implement Configuration Loading, Validation, and Platform Paths

**Status:** Completed

### Objective

Load YAML, validate the schema and semantic rules, resolve platform paths, redact secrets, and compute normalized route snapshots.

### Deliverables

- typed config model;
- YAML loader with duplicate-key rejection;
- JSON Schema validation;
- semantic validator;
- config precedence and platform path package;
- redacted normalized output;
- route/resource/target reference validation;
- secret-reference parser without resolution;
- `agent-dispatch init` scaffolding command;
- unit and golden tests.

### Requirements

`SCP-006`, `PTH-003`, `POL-007`, `SEC-001`, `SEC-006`, `SEC-008`, `SEC-009`, `OPS-005`

### Dependencies

E1-T1 Completed.

### Acceptance

- example config validates after placeholder paths are replaced;
- duplicate YAML keys fail;
- unknown fields fail closed;
- secret values are never printed;
- behavior-affecting route revision is deterministic;
- state directory inside the vault produces a warning or error according to policy.

### Evidence

- `internal/config`: strict YAML loader (duplicate keys rejected, unknown fields fail closed via KnownFields), JSON Schema validation against the embedded SOT config schema with a drift test, semantic validation (route/resource/target references, max-backoff bound, state directory outside the vault, secret-reference well-formedness), secret-reference parser without resolution, redacted deterministic normalized output with a golden test, and the deterministic behavior-affecting `RouteRevision` (POL-007) with determinism/sensitivity/exclusion tests.
- `internal/platformpaths`: default config/state paths for macOS and Linux, `AGENT_DISPATCH_STATE_DIR` and `XDG_*` precedence.
- `internal/cli`: `agent-dispatch init` writes a schema-validated disabled example configuration (0600, refuse-overwrite) and creates the state directory (0700) without installing Watchman triggers or enabling dispatch.
- `make schema-validation` now reproducibly validates `docs/examples/config.yaml` (YAML parse plus config schema), closing the E1-T1 deferral; unit tests re-validate the golden example through the full loader including placeholder-path replacement.

## E1-T3: Implement Domain Primitives, Canonicalization, and Identity

**Status:** Completed

### Objective

Implement pure domain types, stable enums, UUIDv7 generation abstraction, SHA-256 digests, canonical projections, fingerprints, and idempotency keys.

### Deliverables

- IDs and injectable generator;
- typed digest parser;
- record value objects;
- deterministic path/change ordering;
- canonical JSON projection implementation;
- content fingerprint and idempotency key derivation;
- property and golden tests.

### Requirements

`DAT-001` through `DAT-006`, `DAT-009`, `TST-001`

### Dependencies

E1-T2 Completed.

### Acceptance

- repeated projection produces byte-identical output;
- observation ID changes do not change content fingerprint;
- retry attempt/time changes do not change idempotency key;
- rerun generation changes the key;
- invalid digest and unknown enum values fail closed;
- cross-platform golden tests match.

### Evidence

- `internal/domain/ids`: UUIDv7 generator with injectable clock and deterministic test generator; UUIDv7 structural validation; IDs carry no prefix assumptions (DAT-002).
- `internal/domain/records`: closed enums for every record vocabulary failing closed on unknown values; typed digest parser (`sha256:` + 64 lowercase hex); path normalization; canonical change ordering (normalized path, delete/create/modify, ordinal); source observation and projection value objects (DAT-001, DAT-009).
- `internal/domain/fingerprint`: content fingerprint and idempotency key (`agent-dispatch:v1:sha256:<hex>`) from dedicated RFC 8785-ordered canonical projections; caller-order-independent total sort; strict UTF-8 and safe-integer validation; retry-varying fields excluded by construction (DAT-004 through DAT-006).
- Property and golden tests: byte-identical repeated projection, observation-identity exclusion, retry-invariance and rerun-generation sensitivity, fail-closed digest/enum parsing, cross-platform golden fingerprint and key files under `internal/domain/fingerprint/testdata/` (TST-001).

## E1-T4: Implement SQLite Schema, Migrations, and Repositories

**Status:** Completed

### Objective

Create the durable schema and transaction/repository layer without external dispatch.

### Deliverables

- migration framework and initial schema;
- SQLite initialization and pragma verification;
- repositories for all canonical records and route state;
- append-only state transition history;
- online backup before migration;
- integrity and schema-version checks;
- real-file integration tests.

### Requirements

`SCP-007`, `DAT-007`, `DAT-008`, `DUR-001`, `DUR-011`, `OPS-008`, `OPS-009`, `TST-002`

### Dependencies

E1-T3 Completed.

### Acceptance

- fresh and migrated databases reach the same schema;
- foreign keys and uniqueness constraints enforce invariants;
- newer unsupported schema is refused;
- network filesystem placement is detected where reliably possible and otherwise prominently unsupported;
- no external side-effect code exists in a transaction;
- integration tests use actual SQLite files, not only mocks.

---

### Evidence

- `internal/adapters/sqlite`: pure-Go driver `modernc.org/sqlite` (pinned), Open with verified pragmas (WAL journal mode verified, synchronous FULL, foreign keys, busy timeout) and network-placement rejection where reliably detectable (macOS MNT_LOCAL, Linux network filesystem magic; otherwise explicitly unsupported).
- Migration framework: checksummed forward-only migrations in a ledger with prefix-integrity and gap checks, newer-unsupported-schema refusal, atomic apply (a failed migration leaves no ledger entry or partial DDL, tested), and a mandatory pre-migration online backup (`VACUUM INTO`, owner-only 0600, verified restorable via `PRAGMA quick_check` on the backup).
- Schema v1 covers every canonical table with foreign keys, unique constraints (per-source event keys, target+idempotency keys, batch/observation pairs), CHECK constraints for every closed enum, and append-only `state_transitions` enforced by triggers.
- Repositories: observations and changes, batches with lineage, decisions (exactly-one-lineage CHECK), intents with same-transaction route slot reservation (tested atomic), conditional-write lease acquisition persisting retry eligibility, validated dispatch state-machine transitions with audit, route runtime state with optimistic concurrency, work receipts (bounded manifests only, DAT-008).
- Real-file integration tests cover fresh-vs-migrated schema identity, constraint enforcement, interrupted-upgrade atomicity, backup restore verification, lease conditionality, transition matrix exhaustively, and rollback atomicity (TST-002, OPS-009).

# E2: Watchman Deterministic Dry-Run Pipeline

**Epic status:** Completed  
**Purpose:** Convert real Watchman input into a safe, deterministic dispatch plan with no agent side effect.  
**Gate:** G1

## E2-T1: Implement Bounded Watchman Input and Environment Parsing

**Status:** Completed

### Objective

Parse verified Watchman trigger JSON and allowlisted environment fields into a source input DTO.

### Deliverables

- bounded stdin reader;
- Watchman DTO/parser;
- trusted environment allowlist;
- source binding validation;
- source position and flag model;
- raw payload digest;
- malformed/oversized fixtures.

### Requirements

`SRC-001` through `SRC-003`, `SRC-008`, `SEC-009`, `TST-003`

### Dependencies

E1-T4 Completed; E0-T5 Completed.

### Acceptance

- fixture and real payload shapes frozen by E0-T5 parse;
- oversized or malformed input fails before file access;
- payload cannot select route/resource;
- relevant position and overflow fields are preserved;
- no SQLite mutation or target call occurs in parser tests.

### Evidence

- `internal/adapters/watchman`: bounded stdin reader enforcing `limits.max_stdin_bytes` (SEC-009, default 4 MiB) that fails on oversized or empty input before parsing; strict payload parser over the requestable field set (`name`, `exists`, `new`, `size`, `type`, or the definition-time `mode` form) with fail-closed unknown-field, type-letter, mode, size, and name handling (traversal, absolute, NUL, backslash, non-UTF-8); canonical operation mapping create/modify/delete per the E0-T5 report §4; raw payload digest over the exact stdin bytes; ordinal preservation for the deterministic ordering tiebreaker.
- Trusted environment allowlist limited to `WATCHMAN_{TRIGGER,ROOT,RELATIVE_ROOT,SINCE,CLOCK,SOCK}` with required/optional semantics and env-value bounds; `WATCHMAN_FILES_OVERFLOW` is deliberately not read (refuted 0/23 by E0-T5), and the missing-position/first-run signal is carried as the fresh-instance flag per architecture watchman-integration §7. Clocks and since tokens stay opaque.
- Source binding validation (`ValidateBinding`) binds observation to route/resource only from trusted configuration (trigger name and resource root); the payload carries no route authority (SRC-003). Source event key `watchman:<source-id>:<since>:<clock>:<digest>` when a trustworthy position exists, null otherwise (architecture §8).
- Dedicated Draft 2020-12 schemas `docs/schemas/watchman-trigger.schema.json` and `docs/schemas/watchman-environment.schema.json` freeze the payload and allowlist contracts; the previously schema-less examples now validate in `make schema-validation`, and the environment example no longer carries the refuted `WATCHMAN_FILES_OVERFLOW` field.
- Tests: every frozen `trigger-payload-*.json` fixture parses (18 real payloads including bulk-60 unsorted order, replacement delete+modify, atomic-save single entry, repeated-save create→modify→modify); 19 synthetic malformed mutations under `internal/adapters/watchman/testdata/malformed/` fail closed (E0-T5 §9); oversized-at-bound boundary tests; env allowlist, flags, event key, and binding tests. No SQLite or target code is imported by the parser tests.

## E2-T2: Implement Safe Path Containment and Pattern Policy

**Status:** Completed

### Objective

Normalize relative paths, enforce vault containment and symlink safety, and apply include/exclude/protected/immutable patterns deterministically.

### Deliverables

- path normalization value object;
- safe root resolver;
- containment and symlink-escape defense;
- cross-platform recursive glob matcher;
- default Obsidian exclusions;
- file type and size guard;
- security-focused tests.

### Requirements

`PTH-001` through `PTH-004`, `PTH-007`, `PTH-008`, `SEC-002`, `SEC-003`

### Dependencies

E2-T1 Completed.

### Acceptance

- traversal, absolute path, NUL, and symlink escape cases cannot read outside root;
- pattern behavior matches golden tests on the verified hosts (macOS; the goldens are platform-independent by construction, executed on the Linux verify legs since D-020);
- event content cannot affect route authority;
- protected paths are classified but not read unnecessarily or dispatched.

### Evidence

- `internal/domain/policy`: pure deterministic pattern engine (PTH-003, configuration-spec §7) over normalized slash-separated relative paths with `**` recursive matching, single-segment `*`/`?`, fail-closed pattern validation (relative, no backslash/NUL/`.`/`..`, `**` only whole-segment), exclude precedence over include, protected/immutable evaluated after include/exclude, and built-in default exclusions (PTH-007: `.git/**` at any depth, Watchman cookie/state bookkeeping at any depth, `.DS_Store`). Case mode must be resolved by the caller (`filesystem` → sensitive/insensitive) so behavior is explicit, recorded, and identical across platforms by construction (executed on macOS; the D-020 Linux verify legs re-run the suites) for the same input; golden classification and segment-matching files under `internal/domain/policy/testdata/` pin the contract.
- `internal/adapters/localfs`: safe root resolver (PTH-001, PTH-002, SEC-002) that resolves the trusted root's symlinks once, lexically validates untrusted event paths (UTF-8, relative, separators, traversal, NUL, SEC-009 length limit) before any filesystem access, resolves each path with full symlink evaluation under a canonical-prefix containment check, walks shallowest-first over every ancestor of not-currently-existing (deleted) paths, verifying each intermediate component before traversing it, so live or dangling symlinked directories cannot position a future path outside the root, and opens regular files only via `OpenRegular` with final-component `O_NOFOLLOW`, regular-file enforcement, and the configured size limit returning structural errors (`ErrEscape`/`ErrNotRegular`/`ErrTooLarge`) so a digest stays unknown rather than falsely unchanged. Deleted paths are never opened.
- Security tests: lexical escape (traversal, absolute, NUL, backslash, empty) fails before access; file and directory symlink escapes (live and dangling) are refused while contained symlinks resolve; directories and non-regular files fail the type guard; over-limit files fail the size guard with exact-limit acceptance; hostile path names (`include=`, `profile=admin/**`, embedded traversal) cannot mutate compiled patterns or flip other paths' classification (PTH-004, SEC-003); protected and nonexistent paths classify purely through patterns with no filesystem access (PTH-008 classification-time behavior).

## E2-T3: Implement Meaningful-Change Confirmation and Batch Normalization

**Status:** Completed

### Objective

Map Watchman facts to create/modify/delete, hash bounded Markdown files, compare path facts, and coalesce one source batch deterministically.

### Deliverables

- safe SHA-256 file hashing;
- path fact lookup abstraction;
- canonical operation mapping;
- same-path coalescing rules;
- unchanged-modify suppression;
- sorted change batch and content fingerprint;
- optional Git evidence adapter behind a port;
- fixtures for atomic saves and replacements.

### Requirements

`SRC-004`, `PTH-005`, `PTH-006`, `SCP-009`, `DAT-005`, `TST-003`

### Dependencies

E2-T2 Completed.

### Acceptance

- unchanged digest modify drops;
- repeated save resolves to one final change;
- delete never opens missing file;
- rename correctness does not depend on pairing;
- file above hash limit is structurally unknown, not falsely unchanged;
- output order and fingerprint are deterministic.

### Evidence

- `internal/app/ingest`: `BuildBatch` normalizes parsed entries into one canonical sorted, fingerprinted batch. Same-path sequences coalesce to the final observed state per processing-pipeline §4 (modify+modify→final-digest modify; create+modify→create; modify+delete→delete; delete+create→create only when the file exists at planning time (a vanished path falls back to delete, a non-regular recreated path keeps an unknown-digest create, containment anomalies fail the build), marked as replacement evidence with no rename claim; create+delete→drop only when the path neither had a prior digest nor exists at planning time, else a delete carrying the prior digest as uncertainty evidence; any delete followed by later create/modify carries replacement evidence and identical content is never suppressed as unchanged). Hashing goes through the E2-T2 containment-checked `OpenRegular` and hashes only the final state; oversize, non-regular, or vanished files stay structurally unknown (`HashUnknown`), never falsely unchanged.
- Unchanged-modify suppression (PTH-006): a final modify whose known digest equals the prior path-fact digest is dropped with reason `unchanged_content` (the AC-102 reason string); absent or unknown digests are always meaningful. Deletes are never opened (PTH-005). Excluded paths drop without reads; protected and immutable paths stay in the batch for quarantine but are never hashed.
- `PathFacts` port (prior-digest lookup; durable implementation arrives with the E3 ingestion transaction) with `NoFacts`/`MapFacts` implementations, and the optional `GitEvidence` port with a `NoGit` default (SCP-009: Git may enrich but never gates ingestion; the real adapter arrives with later Git enrichment work). Batch order and fingerprint are deterministic regardless of payload order (DAT-005 via the E1-T3 fingerprint projection).
- Tests: coalescing-rule table including the uncertainty and fallback branches, suppression trichotomy (same/different/no prior), delete-without-open, oversize-unknown, protected-not-hashed, excluded-drop, order/fingerprint determinism with content sensitivity, empty-batch fingerprint stability, and replays of the frozen atomic-save and replacement corpus shapes through `watchman.ParsePayload` into `BuildBatch`.

## E2-T4: Implement Structural Policy Planner and Dry-Run CLI

**Status:** Completed

### Objective

Evaluate normal, protected, bulk, overflow, malformed, stale, and unknown conditions into a versioned side-effect-free dispatch plan.

### Deliverables

- pure policy engine;
- disposition and reason codes;
- dispatch plan JSON schema integration;
- `route plan` and `dispatch --dry-run` commands;
- config revision revalidation;
- planner golden tests.

### Requirements

`POL-001` through `POL-008`, `CLI-001` through `CLI-003`, `SEC-010`, `TST-001`

### Dependencies

E2-T3 Completed.

### Acceptance

- disposition precedence follows architecture;
- overflow/fresh instance never yield partial dispatch;
- protected paths yield quarantine;
- no semantic LLM or note-content decision exists;
- same input and route snapshot produce the same plan;
- JSON examples validate against schemas.

### Evidence

- `internal/app/dispatch`: pure deterministic planner (`Evaluate`) implementing the processing-pipeline §5 precedence — overflow-class signal (flags from the E2-T1 environment model) → reconcile with `merge_reconcile` following the route's overflow/fresh action; no meaningful changes → drop; protected or immutable paths → quarantine (PTH-008, protected precedence over bulk); hard-limit and serialized manifest bound (deterministic `ManifestBytes` estimate, POL-004) → quarantine; over automatic threshold → the route-configured bulk action (quarantine or reconcile); unresolved active dispatch → merge_pending with `increment_dirty`; otherwise dispatch with `create_if_idle`. Machine-readable reason codes accompany every disposition (POL-006); no note content is ever inspected (POL-003) and a payload can never request a disposition. The computed route revision — which now records the host-resolved pattern case mode (configuration-spec §7) — is carried in the plan (POL-007); POL-008/SEC-010 revalidation lives at the submit boundary since E7-T3.
- `internal/cli/plan.go`: `route plan --route <id> --input watchman` and `dispatch --route <id> --input watchman --dry-run` (cli-spec §3/§5) run the full side-effect-free pipeline — config load, trusted environment parsing and binding validation, bounded stdin parse, pattern engine with host-resolved case mode, batch normalization without path facts (no SQLite access), planning, and the versioned dispatch plan in the CLI JSON envelope (CLI-001..003). Malformed input fails with `source_malformed_json` (exit 4) before any file access; missing environment metadata with `source_missing_required_metadata` (4); binding mismatch with `source_binding_mismatch` (4); oversized stdin with `source_input_too_large` (4); containment violations with `source_unsafe_path` (exit 30, the non-durable security rejection per error-model §4; durable `unsafe_path_quarantined` arrives with E5 quarantine persistence); configuration failures exit 3. Malformed and unsafe inputs are rejected before the planner, so its `malformed` classification is unreachable by construction; `stale` and `unknown` classifications arrive with the E3/E5 state consumers. Non-dry-run dispatch remains `command_not_implemented` until the E3 durable core.
- Tests: full precedence table including protected-over-bulk and action fallbacks, manifest bound, overflow/fresh never partial, plan JSON validated against the compiled `dispatch-plan` v1 Draft 2020-12 schema, deterministic byte-identical output, a pinned golden plan file (`internal/app/dispatch/testdata/plan-golden.json`), and CLI end-to-end tests over a real temporary vault (normal dispatch plan, fresh-instance reconcile, binding mismatch, malformed stdin, unknown route, dry-run-only guard). `docs/examples/dispatch-plan.json` continues to validate in `make schema-validation`.

## E2-T5: Implement Watchman Trigger Lifecycle and Complete G1

**Status:** Completed

### Objective

Verify and implement real Watchman trigger install/status/remove/test behavior and pass the complete dry-run acceptance gate.

### Deliverables

- Watchman trigger lifecycle verification against the E0-T5 fixture baseline;
- managed trigger definition generator;
- `watchman install|status|remove|test` commands;
- initial full-reconciliation behavior;
- overflow/fresh-instance fixtures;
- macOS and Linux Watchman integration tests where available;
- G1 acceptance report.

### Requirements

`SRC-005` through `SRC-008`, `OPS-006`, `TST-003`

### Dependencies

E2-T4 Completed.

### Acceptance

- AC-101 through AC-110 pass;
- identical trigger install is a no-op;
- replacement requires explicit flag;
- no second settle sleep exists;
- first installation creates one initial reconciliation plan rather than per-file tasks;
- Watchman absence produces actionable `config validate` output.

### Evidence

- `internal/adapters/watchman/lifecycle.go`: lifecycle client over the contract-grade `-j` array interface only (E0-T5: positional forms mis-parse), branching on the response `error` member first, with a controlled environment (PATH/HOME/socket/TMPDIR allowlist), a bounded temp-file output capture, and a subprocess timeout (SEC-004). Version check against the frozen baseline 2026.07.27.00, `watch-project` canonical-root resolution, trigger-list/install (`created`/`replaced`/`already_defined` dispositions)/delete (idempotent), and the managed trigger definition generator (unique name per route, `append_files:false`, the verified stdin field set, coarse `["type","f"]` prefilter with the trusted pattern engine remaining the include/exclude authority, SRC-007).
- Registry and evidence notes: the error-model registry gained `watchman_unavailable` and `watchman_version_unsupported` (target_unavailable, 11) and `watchman_trigger_conflict` (conflict, 14), recorded in docs/CHANGELOG.md 1.0.5; capability gating is subsumed by the version gate for the frozen 2026.07.27.00 baseline (the E0-T5 `list-capabilities` capture shows `cmd-trigger`, `cmd-trigger-list`, and `cmd-trigger-del` present); the install conflict gate is advisory against same-user concurrency and surfaces a warning when the server-side disposition contradicts the pre-check.
- `internal/cli/watchman.go`: `watchman install --route <id> [--replace]` (identical definition → true no-op preserving the incremental position; diverged definition without `--replace` → conflict exit 14; install reports the pending initial reconciliation), `watchman status` (installed/diverged/missing with expected vs installed definitions), `watchman remove --route <id> --yes` (exact managed trigger only, never the watch root, idempotent), and `watchman test` (fixture replay to the normalized source-input DTO with a synthetic environment, no Watchman or Hermes contact). `internal/cli/config.go` adds `config validate` including the actionable Watchman availability check (absence → warning with install remediation; lifecycle commands exit 11 with the same guidance).
- Gate G1: AC-101..110 verified by executable acceptance tests in `internal/cli/g1_test.go` over real temporary vaults, with the per-check evidence table and lifecycle notes recorded in `docs/VALIDATION.md` §Gate G1. Real-Watchman integration tests (`internal/adapters/watchman/lifecycle_test.go`, `internal/cli TestWatchmanCLILifecycle`) run against the installed Watchman 2026.07.27.00 on disposable temporary watch roots and skip with an explicit gap when no binary exists. Linux runner evidence remains an environment gap; macOS is the recorded platform. The installed trigger command targets the E3 durable dispatch path (`agent-dispatch dispatch --route <id> --input watchman`); its default (non-dry-run) execution stays `command_not_implemented` until E3 by design.

- Audit remediation (E4 validation, cross-task seam): `watchman install` now also materializes the route's durable registration (resource, route revision, runtime state) idempotently from the configuration, and `route enable` creates the missing registration on first use, so the production first-use flow — install, enable, dispatch — works without manual state seeding (`TestFirstUseRegistrationFlow`); previously no production path created the runtime row.
---

# E3: Durable Dispatch and Route Coordination Core

**Epic status:** Completed  
**Purpose:** Make planned work durable and recoverable before integrating a real target.  
**Gate:** G2

## E3-T1: Implement Dispatch and Route State Transition Services

**Status:** Completed

### Objective

Encode the dispatch and route runtime state machines as validated domain/application services.

### Deliverables

- transition tables and guards;
- typed transition reasons;
- acceptance vs execution projection separation;
- route idle/active/dirty/uncertain model;
- unit tests for every allowed and forbidden transition.

### Requirements

`DUR-003` through `DUR-005`, `DUR-011`, `CON-001` through `CON-003`, `TST-001`

### Dependencies

E2-T5 Completed.

### Acceptance

- invalid transitions fail without partial persistence;
- unknown cannot become ready without reconciliation evidence;
- accepted does not imply succeeded;
- one route cannot have two active dispatch IDs;
- all state enums match contracts and schemas.

### Evidence

- `internal/domain/state`: the dispatch state machine (persistence-and-state-machines §3) as one authoritative table of exactly the documented edges with typed `IntentReason` values, `ValidateIntentTransition` guards demanding attempt-lease evidence for both edges entering `submitting`, receipt references for acceptance/rejection/execution projections, and reconciliation lookup proof matching the destination for every edge leaving `reconciling`; `dead_lettered -> ready` additionally requires an explicit operator retry with an actor. `unknown -> ready` is structurally absent and fails for every reason and evidence combination, so unknown work always passes through reconciliation (DUR-005 posture).
- `internal/domain/state/route.go`: the route coordination machine (§6, ADR-0009) with the IDLE/ACTIVE_CLEAN/ACTIVE_DIRTY/FOLLOWUP_READY/UNCERTAIN/QUARANTINED model, typed `RouteReason` values, fail-closed `ParseRouteState`, and guards enforcing CON-001/CON-002/CON-003: activation requires an enabled route, the accepted dispatch, and an empty active slot (`CanActivateNormalDispatch` refuses a second active dispatch, follow-up-pending, uncertain, and quarantined routes); dirtying edges must strictly increase the durable dirty generation; completion to IDLE is refused while dirty state or pending reconciliation remains; failure edges preserve dirty state through FOLLOWUP_READY while the failure budget remains and budget exhaustion becomes UNCERTAIN; quarantine exits only through an explicit operator resolution.
- `internal/domain/state/projection.go`: the acceptance and execution axes stay separate (§4): `AcceptanceTransition` maps the closed acceptance enum to exactly its documented transitions, `ExecutionTransition` maps terminal projections only, and `unavailable`/`queued`/`running` return `ErrExecutionNotTerminal` so an accepted dispatch never reports an execution outcome it does not have (accepted does not imply succeeded).
- `internal/adapters/sqlite/store.go`: `validIntentTransition` now delegates to `state.CanTransitionIntent` (fail-closed through the records parser), making the domain table the single authority the transactional, audited `TransitionIntent` enforces (DUR-011).
- Tests: exhaustive 12x12 intent and 6x6 route matrices against independently restated SOT tables (every allowed and forbidden transition, TST-001), per-edge reason validation over the full reason alphabets, guard suites for lease/reconciliation/receipt/dirty-retention/budget/quarantine evidence, the full acceptance x execution projection grid, contract lockstep against the `dispatch-intent` and `dispatch-receipt` schema enums, and SQLite-level tests that the CHECK constraints accept exactly the domain state sets and that a rejected transition leaves neither the intent row nor the audit history changed (no partial persistence). `make verify` green including race and import-direction checks.

## E3-T2: Implement Durable Intent Commit and Attempt Leasing

**Status:** Completed

### Objective

Persist observation-to-intent lineage and acquire one transactional attempt lease before any fake or real target call.

### Deliverables

- ingestion transaction service;
- dispatch-intent creation;
- target/idempotency uniqueness constraints;
- conditional attempt lease acquisition and expiry;
- recovery of abandoned submitting state;
- fake sink port and tests.

### Requirements

`DUR-001`, `DUR-002`, `DUR-010`, `DUR-012`, `TST-005`, `TST-006`

### Dependencies

E3-T1 Completed.

### Acceptance

- committed intent exists before fake sink invocation;
- two processes cannot own the same attempt;
- crash after commit leaves recoverable ready/submitting evidence;
- no transaction remains open across sink call;
- duplicate idempotency constraint is enforced.

### Evidence

- `internal/ports`: the sink port (sink-adapter-contract.md §2) — `Sink` with `Probe`, `Submit`, both lookups, and `GetExecution`; typed `Capabilities`, `SubmitResult` with the four-value classification (accepted / rejected / definite_not_submitted / unknown) and tri-state durability, `LookupResult`, `ExecutionProjection`, `TaskRequest` as the immutable hermes-task/v1 shape, and `ErrCapabilityUnsupported` (never emulated). `ports.DispatchStore` declares the durable surface: `CommitLineage`, `LoadIntent`, `AcquireAttempt`, `CompleteAttempt`, and `RecoverExpiredSubmitting`, each completing its own transaction so no store transaction can span a sink call (DUR-001, ADR-0005), with typed `ErrIdempotencyConflict`, `ErrRouteSlotHeld`, `ErrLeaseHeld`, and `ErrIntentNotFound`.
- `internal/adapters/sqlite/dispatch.go`: the port implementation. `CommitLineage` persists observation, batch, decision, and intent plus the route active-slot reservation in one transaction and maps driver rejections to the typed errors (duplicate target+idempotency key, second active dispatch). `AcquireAttempt` reads the from-state and performs the conditional lease write with attempt-row creation, attempt_count increment, and the domain-validated `ready/retry_wait -> submitting` audit transition in one transaction; a lost competition returns `ErrLeaseHeld` (DUR-012). `CompleteAttempt` closes the attempt, persists the acceptance receipt, and applies the E3-T1-guarded transition atomically. `RecoverExpiredSubmitting` moves expired submitting intents to `unknown` with audit evidence and closes the open attempt as `unknown/lease_expired` (persistence §5).
- `internal/app/dispatch/runtime.go` + `request.go`: the runtime. `BuildRequest` constructs the immutable task request and derives the idempotency key through `fingerprint.Idempotency` (target, route revision, generation, contract version, content fingerprint). `SubmitOnce` commits the lease before invoking the sink, passes the stored request verbatim, and maps the adapter classification to the domain transition (accepted/rejected with durable receipts, definite_not_submitted to retry_wait, unknown and sink errors to unknown per DUR-005); `Recover` wraps lease recovery.
- `internal/testsupport/fakesink`: the TST-006 fake sink — scripted accepted, rejected, definite-not-submitted, timeout-before-accept, timeout-after-accept, malformed-response, duplicate-idempotency replay, unavailable/ambiguous/absent lookups, and execution status progression, with a mid-flight submit hook.
- Audit remediation (E4 validation reopening): a malformed stored request no longer strands the intent in submitting until lease expiry — `SubmitOnce` completes the attempt as a definite pre-invocation failure (`stored_request_invalid` → retry_wait with the persisted backoff deadline, eventually dead-lettering through the normal machinery), the sink is never invoked, and no open lease remains (`TestMalformedStoredRequestCompletesAttempt`).
- Tests: fake-sink scenario suite; SQLite tests for whole-chain persistence, duplicate-idempotency and route-slot refusal without partial persistence, lease exclusivity (first owner wins, expired submitting requires recovery rather than direct re-lease), validated completion rejection, accepted completion with receipt, and expiry recovery; runtime tests over a real database proving the leased submitting intent is observable mid-sink-call, a concurrent write succeeds during the call (no open transaction), the loser never invokes the sink, crash-after-commit leaves recoverable submitting evidence that recovery moves to unknown, request determinism, and schema validation of the built request against `hermes-task-request/v1`. `make verify` green.
- Audit remediation (E3 validation): SaveIntent returns the typed route-slot error at its source and unique-constraint identification inspects the driver error code with the constrained columns instead of prose matching; SubmitOnce validates the stored request before leasing so a malformed request cannot strand an submitting intent; sink error text is persisted as a redacted bounded class, not the raw adapter message.

## E3-T3: Implement Retry, Unknown Reconciliation, and Dead Letter

**Status:** Completed

### Objective

Implement bounded persisted retry, unknown reconciliation workflow, explicit dead-letter handling, and operator actions.

### Deliverables

- exponential backoff with deterministic injectable jitter;
- adapter result classifier;
- unknown lookup orchestration;
- dead-letter records;
- JSON output contracts for dispatch attempt and dead-letter records;
- `dispatches list|show|retry|reprocess|rerun|drain` commands;
- `route list|show|enable|disable` commands;
- stable error and exit codes;
- fake sink scenario tests.

### Requirements

`DUR-004` through `DUR-009`, `CLI-004` through `CLI-008`, `TST-006`

### Dependencies

E3-T2 Completed.

### Acceptance

- timeout-after-possible-submit becomes unknown;
- retry retains intent and idempotency key;
- rerun creates new lineage and key;
- automatic attempts stop at limit;
- no automatic target fallback exists;
- dead-lettered work remains fully inspectable.

### Evidence

- `internal/app/dispatch/backoff.go`: the persisted exponential submission retry policy (configuration-spec §9, DUR-007) — deterministic delays from an injected jitter unit in [0,1), capping at max_backoff, fail-closed envelope validation (attempts 1..10, positive backoffs, jitter 0..0.5), and exhaustion reporting so automatic attempts stop at the configured limit.
- `internal/app/dispatch/classify.go`: the adapter result classifier — accepted (durable status recorded), rejected, definite_not_submitted (the only automatically retryable outcome), and unknown for every ambiguous outcome including sink errors and malformed responses; no classification ever yields failed-from-ambiguity or a target switch (DUR-005, DUR-008).
- `internal/app/dispatch/runtime.go`: SubmitOnce now schedules the persisted backoff deadline on retryable outcomes and a bounded Drain driver that skips not-yet-due and budget-exhausted intents; `operator.go` implements the explicit operator actions: dead-lettered retry requires --reason, resets the budget, and retains the request and key (DUR-009); retry_wait retry makes the intent due; rerun builds a new dispatch, generation, and idempotency key under a superseding decision and takes over the route slot held by the work it replaces.
- `internal/app/reconcile`: the unknown-resolution workflow (DUR-006) — lookup by idempotency key (then external reference) before any other submission; found-accepted resolves to accepted, proven non-acceptance (found-rejected or absent) retries while budget remains, and an ambiguous or exhausted outcome dead-letters for the operator. The store applies both transitions through the E3-T1 guards in one transaction.
- `internal/adapters/sqlite/inspection.go`: the read side (ListIntents with filters, full LoadIntentLineage with attempts, receipts, and audit history — dead-lettered work remains fully inspectable), LoadBatchEvidence for reprocessing, ApplyOperatorRetry, MakeRetryDue, RerunIntent with superseding decision lineage, ReconcileUnknown, and route activation (SetRouteActivation records the acknowledged revision on enable; disable preserves observations, active work, and dirty state) plus ListRoutes.
- CLI: `dispatches list|show|retry|reprocess|rerun|drain` and `route list|show|enable|disable` are implemented with the JSON envelope and stable documented exit codes (CLI-004, CLI-008; new registry codes dispatch_duplicate, route_slot_held, route_not_registered, dispatch_not_found, batch_not_found); `dispatch` without `--dry-run` is executable since E3: it persists the full observation-to-intent lineage (UUIDv7 identity, source-event-key retransmission recognition, self-contained request with the contract acceptance criteria and manifest digest) and reports the documented target-unavailable error at the submit phase until the E4 adapter exists — with `--no-submit` persisting and leaving the intent ready; no automatic target fallback exists.
- Contracts: `docs/schemas/dispatch-attempt.schema.json` and `docs/schemas/dead-letter-record.schema.json` with validating examples define the JSON output contracts for dispatch attempt and dead-letter records (DUR-009 inspectability).
- Tests: deterministic and bounded backoff, the full classifier scenario table, retry_wait with persisted backoff, drain stopping at the limit, the end-to-end dead-letter path (ambiguous submit -> unknown -> unresolved reconciliation with exhausted budget -> dead-lettered -> inspectable lineage -> operator retry resetting the budget and retaining the key), rerun lineage/key separation, reconcile lookup scenarios (found-accepted, found-rejected, absent with and without budget, ambiguous, unavailable lookup), non-unknown re-entry refusal, and CLI suites for dispatches list/show, route enable/list/show/disable with the production gate flags and state_dir-pointed configuration. `make verify` green.
- Audit remediation (E4 validation reopening): `backoffFromConfig` and `hintsOf` now parse through the one schema-exact `config.ParseDuration` (whole-day units included), so every schema-legal duration behaves identically at validation and run time, and a configured-but-unparsable execution hint fails closed with `config_invalid` instead of being silently dropped (HER-006: missing mappings are reported); proven by `TestSchemaLegalDayUnitDurations` (day-unit policy resolves at drain, the stored request carries the 86400-second hint) and the parser's closed set.
- Audit remediation (E3 validation): the dispatch default path routes through the E3-T4 coordinator (slot-competition losers merge into the dirty generation instead of failing) and reports the real SubmitOnce outcome instead of an unconditional submitted flag; drain resolves the route's configured target type and carries the configured submission-retry policy with a live jitter source; post-open store failures use the registered `sqlite_query_failed` code and activation concurrency maps to `transition_invalid`; the reprocess decision records the computed route revision; the stale forward-looking comments for `config show` (E6-T2) and the durable PathFacts (E5-T3) name their actual owners.

## E3-T4: Implement One Active Route Task and Dirty Generations

**Status:** Completed

### Objective

Prevent parallel vault maintenance tasks and durably collapse later changes into one follow-up generation.

### Deliverables

- route runtime state repository/service;
- generation allocation;
- merge-pending transaction;
- follow-up creation service;
- pending reconciliation flag;
- concurrency and burst tests.

### Requirements

`CON-001` through `CON-006`, `FBK-001`

### Dependencies

E3-T3 Completed.

### Acceptance

- active route receives no second normal dispatch;
- any number of later bursts increment durable dirty state;
- active completion creates at most one follow-up;
- local serialization works even if target mutex capability is false;
- latest-state instruction remains part of follow-up.

### Evidence

- `internal/ports/coordination.go`: the route coordination surface — LoadRouteState (the domain RouteSnapshot projection), CommitMergePending (one transaction persisting the arriving lineage as merge_pending and durably incrementing the dirty generation, CON-002/FBK-001), CompleteActive (the work-completion transaction applying the E3-T1-validated route transition and creating exactly one follow-up decision and intent when dirty work or pending reconciliation remains, CON-003), ActivateDispatch and ActivateFollowup (slot-consuming activation transitions).
- `internal/adapters/sqlite/coordination.go`: the implementation. Merge-pending applies the validated ACTIVE_CLEAN -> ACTIVE_DIRTY (later_relevant_change) or ACTIVE_DIRTY -> ACTIVE_DIRTY (more_changes_merged) transitions with the strictly incremented dirty count and dirty_since; FOLLOWUP_READY and UNCERTAIN retain their state with the dirty count still recording the burst (the latest-state follow-up or operator resolution absorbs it, CON-004/CON-005); a reserved-but-not-activated slot consumes its own reservation first, and arrivals that lost a slot race merge durably. CompleteActive enforces the holding dispatch, evaluates the caller-owned failure budget (configuration-spec section 9) against the E3-T1 guards, clears the completed slot, collapses the dirty generation into one follow-up (new dispatch, generation+1, new idempotency key, generation-lineage decision), and returns IDLE only on clean completion; budget exhaustion becomes UNCERTAIN. Activation is idempotent for the slot-holding dispatch and a typed conflict for any other.
- `internal/app/dispatch/coordination.go`: the Coordinator — Arrival routes one incoming lineage through the E3-T1 CanActivateNormalDispatch gate (commit+activate for the single winner; ErrRouteSlotHeld losers merge, AC-204 posture), Completion refuses dirty routes without a prepared follow-up request and applies the completion transaction, Activate promotes accepted follow-ups, and BuildFollowupRequest constructs the latest-state follow-up (new identity and generation, the same acceptance criteria and assignment, the retained latest-state instruction). Serialization is a local route invariant enforced regardless of any target mutex capability (CON-006).
- Tests: twelve concurrent arrivals produce exactly one dispatch with dirty generation 11; local serialization without any mutex capability; four bursts collapse into exactly one follow-up with the latest-state instruction and generation+1, follow-up activation, refusal of double completion; clean completion returns to IDLE with a free slot; failure with remaining budget creates one follow-up and exhaustion becomes UNCERTAIN (with arrivals during uncertainty merging). Race-enabled runs green. `make verify` green; Gaori manifest-check, schema-validation, traceability exit 0.
- Audit remediation (E3 validation): CompleteActive refuses inside its transaction when a dirty generation would be dropped without its follow-up and when the only outstanding work is pending reconciliation (the E5 reconcile workflow owns that edge); the rerun takeover applies the matching route activation transition so the rerun holds the slot as genuinely active work, and refuses unresolved or disabled routes; the follow-up decision records the route revision it was planned for.

## E3-T5: Execute Crash, Migration, and Concurrency Gate G2

**Status:** Completed

### Objective

Prove the durable core across every defined crash window and simultaneous one-shot invocation.

### Deliverables

- crash injection framework;
- multi-process test harness;
- migration interruption tests;
- database integrity/backup tests;
- G2 acceptance report and defect fixes.

### Requirements

`DUR-*`, `OPS-008`, `OPS-009`, `TST-002`, `TST-004`, `TST-005`

### Dependencies

E3-T4 Completed.

### Acceptance

- AC-201 through AC-207 pass;
- race-enabled tests pass where supported;
- no test relies solely on mocks for SQLite behavior;
- power-loss claim is limited to documented SQLite durability and tested crash model;
- all known invariant violations are fixed before review completes.

### Evidence

- `internal/testsupport/crashbin`: the crash-injection and multi-process harness (TST-004/TST-005) — real process deaths at the ingestion transaction boundary (`mid-transaction`, dying with observation, batch, and decision written uncommitted), immediately after the full lineage commit (`after-commit`), after the lease transaction with no attempt completion (`lease --die` / `acquire-race`), and inside the migration sequence (`migrate-partial`); plus `arrive`, one simultaneous one-shot arrival competing for the route slot. Every command drives a real SQLite file.
- `internal/app/dispatch/g2_test.go`: the executable G2 acceptance suite — `TestG2AC201` (WAL recovery discards the uncommitted lineage; the arrival recommits cleanly), `TestG2AC202` (the restarted process submits the ready intent exactly once and a second submission is refused), `TestG2AC203` (remote acceptance with a held lease recovers to unknown, the lookup proves acceptance, zero additional submissions), `TestG2AC204` (four concurrent acquire processes, one winner, one distinct open-attempt owner), `TestG2AC205` (persisted backoff deadlines under one idempotency key with the drain stopping at the limit), `TestG2AC206` (terminal rejection remains fully inspectable with no sink switch), `TestG2AC207` (an interrupted migration leaves the valid previous version and a restart reaches the newest version with a passing integrity check; the pre-ledger version read is skipped because the ledger exists only after the first migration), and `TestG2MultiProcessOneActiveRouteDispatch` (six simultaneous arrivals, one durable dispatch holding the route slot).
- Migration interruption inside a unit and the backup path stay covered by the in-package suite (atomic failure, pre-migration backup, fail-closed backup verification, ledger gap detection, integrity checks); the durability claim is bounded to process death at transaction boundaries under the verified WAL/synchronous=FULL pragmas (OPS-008), not machine power loss.
- Gate G2 report: `docs/VALIDATION.md` section Gate G2 records the per-criterion evidence table, the supporting concurrency evidence, and the boundary of the durability claim. `make verify` green including race tests; Gaori manifest-check, schema-validation, traceability exit 0. No invariant violation remained: every defect found during the gate (activation self-reservation races, absent lookup-status normalization for absent results, IDLE-merge windows) was fixed in E3-T4/E3-T5 development before this review.

---

# E4: Hermes Kanban Durable Integration

**Epic status:** Completed  
**Purpose:** Connect the proven durable core to the verified public Hermes Kanban interface.  
**Gate:** G3

## E4-T1: Implement Hermes Kanban Adapter and Capability Probe

**Status:** Completed

### Objective

Implement the adapter strictly from E0-T4 evidence and expose verified target capabilities.

### Deliverables

- public Hermes CLI process adapter;
- version compatibility check;
- read-only capability probe;
- controlled environment and process limits;
- typed structured response parsing;
- adapter conformance fixtures.

### Requirements

`HER-002` through `HER-005`, `HER-009`, `HER-010`, `SEC-004`, `SEC-005`

### Dependencies

E3-T5 Completed.

### Acceptance

- only public interface is used;
- unsupported Hermes version fails before task submission;
- no human text parsing determines acceptance;
- missing capability fails route validation;
- secrets and full output are redacted;
- adapter conformance tests pass.

### Evidence

- `internal/adapters/hermeskanban/version.go`: the exact-version gate. `ParseVersionOutput` matches the documented `hermes --version` first line only (E0-T4 §2; anything else fails closed), and `CheckVersionSupported` admits exactly the runtime-verified set (the baseline; the build date is recorded evidence, not the gate) with `VersionUnsupportedError` + remediation, so an unsupported Hermes version fails route validation before any task submission (HER-002, HER-005).
- `internal/adapters/hermeskanban/report.go`: the capability authority. `LoadReport` fails closed on the wrong `agent-dispatch.hermes-capabilities/v1` schema version, a non-`public_cli` interface, or a missing version; `PortCapabilities` maps the frozen report onto the eight HER-004 declarations with an absent name reading false (nothing is assumed beyond the report); `VersionMatchsWith` enforces capability-report freshness against the discovered installation; `ValidateRequired` turns a missing required capability into the typed `CapabilityError` and an unknown name into a configuration defect (never a silent reduced guarantee).
- `internal/adapters/hermeskanban/runner.go`: controlled execution (SEC-004/SEC-005). Argument arrays only — no shell, no interpolation; the child receives exactly the allowlisted environment (PATH/HOME always), a controlled working directory (never a vault root), /dev/null stdin, stdout/stderr captured through write-side-bounded sinks (a stream exceeding the configured byte bound fails the invocation as excessive output — an ambiguous outcome — instead of growing an unbounded capture file), a per-call deadline, and process-group SIGKILL cleanup via `CommandContext` + `Setpgid`. A deadline that elapses only after a completed zero exit never discards a valid result (the deterministic classification property is unit-tested), and any Wait failure coinciding with a done context is the ambiguous deadline outcome while definite exit codes without it stay definite. `ExecutableMissingError` is the definite pre-submit failure with remediation.
- `internal/adapters/hermeskanban/client.go` + `dto.go` + `errors.go`: the typed transport. `DiscoverVersion`, `Create`, `Show`, `List`, and `Assignees` build documented argv arrays — the runtime-verified create surface (title, body, assignee, skills, workspace, mutex key, max-runtime, max-retries, idempotency key, priority, created-by; the help-verified `--model`/`--provider` pinning flags are not mapped by the v1 logical contract and stay unused) with the idempotency key transmitted verbatim — and decide outcomes only from exit 0 plus successfully typed `--json` records: create additionally requires the acceptance-proof members (`t_` + 8 lowercase hex id, status, created_at; E0-T4 §4). Frozen exit-1 stderr shapes classify `no such task` / unknown-board for lookup semantics, exit 2 classifies as definite argument rejection, everything else stays a bounded generic failure; timeout, excessive output, and malformed output carry explicit ambiguous-outcome errors (DUR-005 posture). Option-like values beginning with `-` are refused before any invocation on every rendered value slot — create options and the lookup surfaces (board, task reference, status, sort) alike. All diagnostics, including malformed-output reasons and version-parse fragments, are scrubbed of allowlisted environment values and bounded (error-model §6). `ProfileOnDisk` provides the assignee validation E0-T4 §7 requires before route enablement.
- `internal/adapters/hermeskanban/adapter.go`: the read-only probe facade. `Probe` discovers the version once, gates it, loads the frozen report, requires report/installation version agreement, and validates required capabilities; `ProbeVerbose` drives the same single-discovery path and classifies available / version_unsupported / unavailable / capability_mismatch / config_error for the validation surface — persistent configuration defects (unreadable or stale report, unknown required-capability name) fail validation rather than downgrading to a warning. HER-010: only public CLI commands are used; no Hermes database or private API is touched.
- `internal/cli/config.go`: `config validate --probe-targets` replaces the placeholder with real probing (cli-spec §3): every hermes-kanban target is probed so the summary stays complete, reporting per-target state, version, and capability summary through the single name↔field mapping on `ports.Capabilities`; a capability mismatch fails validation with the stable `config_capability_missing` code and exit 3 (HER-005, AC-306 posture) and a configuration defect with `config_invalid`/exit 3, while an unusable or version-unsupported target stays a warning because the configuration document itself is valid and the adapter gates submissions again at run time. Target durations parse through the one schema-exact parser exported from internal/config (whole-day units included). `internal/cli/state.go` and `internal/version` now describe the delivered transport surface (durable submit wiring arrives with E4-T3; no fallback target exists, DUR-008).
- Tests: frozen-fixture conformance for every response shape (create, show, list, assignees, duplicate-dedup returns the original task, unvalidated assignee echo); the malformed-acceptance table (missing id/status/created_at or a non-`t_<8 hex>` id never counts as acceptance); the frozen error behaviors through stub binaries (unknown task, unknown board, argparse exit 2, generic exit, timeout-ambiguous on both the submit and lookup surfaces, excessive output, malformed output); runner conformance (environment allowlist exclusion with a poisoned variable and pass-through of an operator-added entry, controlled cwd, write-side output bound, deadline + process-group cleanup, closed stdin, missing executable); profile validation (on-disk, absent, prefix-collision); hostile-value verbatim argv round trip and the leading-dash refusal table; allowlisted secret scrubbing from diagnostics; probe scenarios (happy path, unsupported version, unparsable version, capability mismatch without emulation, stale report, all verbose states); CLI suites for the probe-targets outcomes (available with eight capabilities, capability mismatch → `config_capability_missing`/exit 3, unknown capability name and unreadable report → `config_invalid`/exit 3, version_unsupported and unavailable as warnings, invalid and day-unit timeouts, mixed-state multi-target summaries in deterministic order); single version discovery per probe with an invocation-counting stub; the lookup-surface leading-dash refusal table; version-diagnostic redaction; and a skip-guarded probe of the real installed Hermes (TST-007 posture). `make verify` green including the real-Hermes probe against `docs/integrations/hermes-capability-report.json`.

## E4-T2: Implement Safe Hermes Task Request Renderer

**Status:** Completed

### Objective

Render the logical Hermes task contract with strict trusted-instruction and untrusted-manifest separation.

### Deliverables

- logical task request builder;
- deterministic title and instruction template;
- bounded manifest serialization;
- profile/skills/workspace/mutex mapping validation;
- request size enforcement;
- golden tests against `examples/hermes-task-request.json`.

### Requirements

`HER-006`, `HER-007`, `SEC-003`, `SEC-009`

### Dependencies

E4-T1 Completed.

### Acceptance

- no note body or front matter enters request;
- malicious file names cannot change trusted fields;
- all assignment values come from route config;
- latest-state semantics and receipt instructions are present;
- oversized manifest follows policy rather than truncating silently.

### Evidence

- `internal/adapters/hermeskanban/renderer.go`: the pure renderer from the immutable logical request (`agent-dispatch.hermes-task/v1`, built by E3-T2's `BuildRequest`) to the verified create surface. The title is exactly the contract template `[Agent Dispatch] LLM Wiki maintenance for <resource-id> generation <N>` with no path or note title inserted (HER-006). The body is the §4 trusted instruction template verbatim (only route id and revision interpolated from trusted members), followed by the delimited untrusted manifest section — the activation projection (mode, generation, content fingerprint, flags, path/operation/digest items) serialized as structured JSON, never concatenated into commands and never carrying note body or front matter (HER-007, SEC-003, §5) — and the §6 work-receipt instructions with the dispatch id filled and the run id left as the worker-side placeholder. Assignment, skills, mutex key, workspace, runtime/retry hints map only from the request's trusted assignment/resource/hints members; the idempotency key transmits verbatim; `--created-by agent-dispatch` attributes authorship. Requests failing the contract shape (wrong version, missing identities, non-`latest_state` mode, invalid workspace form, assignment without profile, empty acceptance criteria, negative or oversized execution hints) fail closed with `InvalidRequestError` before any rendering, and interpolated trusted members (resource id, dispatch id, route id/revision, fingerprint) are format-guarded against control characters and option-like leading dashes. The contract version vocabulary lives once on the port (`ports.TaskRequestContractVersion`), shared by the E3-T2 builder and this renderer.
- Manifest bound enforcement (SEC-009): the serialized manifest exceeding the route's `max_manifest_bytes` bound is rejected with the typed `ManifestTooLargeError` (bytes and bound reported) — never a silent truncation; a boundary-exact manifest renders.
- Golden test: `testdata/renderer-golden.txt` records the deterministic rendering of the frozen `docs/examples/hermes-task-request.json` (title plus full body), asserted byte-for-byte and re-rendered for determinism. Adversarial suite: hostile paths (`../../etc/passwd`, front-matter-shaped paths, `--assignee=attacker`, prompt-injection text, control characters) are proven present inside the manifest section in their JSON-escaped forms while the trusted instruction and the complete create mapping remain byte-identical to a clean render; the manifest section decodes under `DisallowUnknownFields` to exactly the activation projection; and delimiter spoofing is proved impossible — the single-line escaped manifest JSON cannot contain the end delimiter's raw newline, so a path carrying the delimiter text still extracts as valid activation JSON. Boundary tests prove the manifest bound at exactly-the-bound, one-byte-over, and far-over; the mapping/validation table covers every `InvalidRequestError` case plus the assignment-less rendering with no assignment flags. `make verify` green.

## E4-T3: Implement Submit, Idempotency Lookup, and Delivery Reconciliation

**Status:** Completed

### Objective

Connect durable intents to Hermes create and lookup operations with correct unknown handling.

### Deliverables

- submit orchestration;
- idempotency key transmission;
- lookup by key/reference;
- unknown reconciliation state and commands;
- target-specific retry classification;
- integration tests with fake and real disposable Hermes target.

### Requirements

`DUR-005` through `DUR-008`, `HER-001`, `HER-008`, `TST-006`, `TST-007`

### Dependencies

E4-T2 Completed.

### Acceptance

- durable accepted task stores external reference;
- duplicate key resolves to original task;
- ambiguous result never invokes webhook;
- proven absence is required before resubmit;
- real disposable target tests match E0-T4 report.

### Evidence

- `internal/adapters/hermeskanban/sink.go`: the `ports.Sink` implementation. `Submit` renders the immutable request through the E4-T2 renderer and submits the verified create with the idempotency key verbatim — idempotent by the E0-T4 §5 dedup behavior, so a duplicate submission resolves to the original task (AC-302) and resubmission after a lost response is dedup-safe by construction; acceptance returns the external reference, the created_at-derived observation time, and bounded typed JSON evidence, persisted by the E3 runtime through the acceptance receipt as a bounded identity/status evidence projection that is always valid JSON — never the unbounded echoed task body (durable accepted task stores external reference). Classification follows the frozen error model: provable pre-invocation failures (missing executable, argument rejection, unknown board, manifest-bound policy rejection) are definite_not_submitted; timeout, excessive output, malformed output, and unrecognized failures after possible submission are unknown (DUR-005); no other target is ever invoked (DUR-008). `LookupByExternalRef` is the read-only show with the frozen `no such task` behavior as the deterministic absence proof (DUR-006); `LookupByIdempotencyKey` honestly returns `capability_unsupported` because the public CLI has no read-only key query (E0-T4 §5) — never emulated; `GetExecution` is unsupported until E4-T4. Construction validates the frozen report against the configured requirements, the board slug, and the manifest bound, and the caller completes the version-gating `Probe` before any submission (HER-002/HER-005).
- Configuration: the hermes-kanban target gains the required `board` field (config schema, configuration-spec §5, example, and init template) naming the operator-created public board — Agent Dispatch never creates, renames, or deletes boards. The example and init `required_capabilities` now name `lookup_by_external_ref` (the read reconciliation the sink performs); key-based reconciliation on Hermes runs through the dedup submission the capability report records. The dispatch pipeline and the request builder now produce and validate the contract `dir:<root>` workspace binding form, fixing an E3-era shape defect the renderer's contract validation exposed; pre-E4-T3 stored intents holding the unprefixed form are re-planned rather than migrated (no released state exists at v0.1).
- CLI wiring: `resolveSink` constructs and gates the real sink for the submit phase (`dispatch` and `dispatches drain`), with the stable registry codes through `writeSinkError` — `hermes_version_unsupported`/`hermes_executable_missing` exit 11, `config_capability_missing`/`config_invalid` exit 3. `dispatches drain` now reconciles the route's unknown dispatches before submitting due work (DUR-006), guarded by the full accepting identity — the intent's target id and its recorded target scope (the board slug, persisted at lineage commit through schema v3 `intent-target-scope` and inherited by follow-ups and reruns) against the currently configured target, so a re-pointed route or board is skipped with a visible warning instead of producing a wrong-board absence proof (`TestReconcileSkipsRepointedScope`): by-reference lookup resolves or proves absence, and unresolved ambiguity dead-letters for the operator, whose explicit `dispatches retry` resubmits the same key through the dedup-safe path; a failure to enumerate the unknown dispatches fails closed (no submission may run without the required lookup ordering), per-dispatch reconciliation failures are isolated as visible warnings, reconciliation mutations that committed before a drain failure are surfaced in the error path, and the runtime's evidence bound now marker-replaces oversized payloads so persisted evidence is always valid JSON for every adapter.
- Tests: the sink suite over a stateful board stub (accepted durable with reference, observation time, and typed evidence; duplicate key resolves to the original; the full classification table including timeout, malformed, excessive output, argument rejection, unknown board, and generic failure; oversized-manifest policy rejection before any invocation; found/absent reference lookup; the honest unsupported boundaries; construction validation); the TST-007 real-target integration against a real disposable Hermes board created and hard-deleted through the public CLI (submit, duplicate-dedup to the same reference, found and absent lookups, matching the E0-T4 report); and the CLI end-to-end suite (dispatch accepted with the external reference persisted and inspectable, unusable target failing closed with exit 3, and the full ambiguous recovery loop: ambiguous submit → unknown → drain reconciliation → dead-letter → operator retry → dedup-safe resubmission accepted with the reference recorded). `make verify` green including the real-board integration.

## E4-T4: Implement Acceptance Receipts and Execution Projection

**Status:** Completed

### Objective

Persist portable acceptance receipts and optional Hermes execution status without conflating the two.

### Deliverables

- dispatch receipt repository/service;
- Hermes status mapping;
- `receipts list|show` and route status projection;
- stale-active detection;
- public lookup refresh command or behavior;
- redaction tests.

### Requirements

`HER-008`, `OPS-001`, `OPS-002`, `OPS-005`

### Dependencies

E4-T3 Completed.

### Acceptance

- accepted task can have execution unavailable;
- status mapping is documented and version-tested;
- malformed status becomes unknown projection, not success;
- receipt lineage is visible from dispatch inspection;
- stale active task is warned, not auto-failed.

### Evidence

- `internal/adapters/hermeskanban/projection.go`: the documented, version-tested mapping from the frozen public status enum onto the portable execution axis (done→succeeded observing completed_at; running→running; ready/scheduled/todo/triage/review/blocked→queued — blocked is a pause, never a failure; archived→canceled). A status outside the frozen enum is malformed and becomes the unknown projection (unavailable) with the typed `UnknownStatusError`, never success. `Sink.GetExecution` projects through the read-only show, requires the execution_status and lookup capabilities without emulation, and reports an accepted task whose execution cannot be derived as unavailable — acceptance and execution stay separate (HER-008).
- `internal/ports` + `internal/adapters/sqlite`: the receipt repository surface (`ports.ReceiptStore`) with `SaveExecutionProjection` (one appended receipt per refresh so projection history stays inspectable, `agent-dispatch.execution/v1` payload), `ListReceipts` (dispatch/route/kind filters over acceptance, execution-projection, and work receipts — the work kind reads the work-receipt table, an empty kind unions both), and `LoadReceipt` (bounded persisted payload, unknown ids reporting the new `receipt_not_found` registry code). The acceptance completion now also records the external reference on the intent row — the current reference for refresh and inspection — while receipts remain the historical lineage (OPS-002). `IntentFilter` gained the dispatch filter the projection lookups need.
- `internal/app/receipts`: the refresh service — public lookup by the stored external reference, persisting the execution-projection receipt; a sink failure carrying no projection persists nothing, a returned unavailable projection (for example a malformed status) is persisted with its reason, and the stale-active window check (`StaleActive`) that callers use to warn, never auto-fail.
- CLI: `receipts list|show` with redacted bounded output (OPS-001/002), `dispatches refresh <id>` as the public lookup-refresh behavior — a reference-less dispatch is a local conflict (`transition_invalid`, exit 14) never a target failure, capability-less targets report `lookup_unsupported` (exit 3) without emulation, a `--route` mismatch is a usage error, and a configuration change that redirects the sink away from the accepting target is refused — and `route show` projects the active dispatch's state, external reference, and latest persisted execution projection, failing closed on storage errors, surfacing unreadable stale windows as warnings, and warning when the active dispatch exceeds the configured `active_stale_after` (warned, not auto-failed). The broken `--no-submit` flag parsing (an E3-era regression the refresh flow exposed) is fixed.
- Tests: the full status-mapping table including every frozen status, the completed_at observation, and the malformed-status unavailable projections; the sink projection through the stub (queued, capability-absent unsupported); the service refresh flow persisting a projection separate from acceptance; the CLI suite (acceptance plus appended execution receipts with kind/route filters and bounded detail payloads, `receipt_not_found` for unknown ids, invalid kind/limit usage errors, refresh history over repeated refreshes, route show projection, malformed-status refresh persisting unavailable, reference-less refresh refused as a conflict, the accepting-target identity check, lookup_unsupported for capability-less targets, stale-active warning with second-boundary determinism and the StaleActive boundary unit table), and the service-level persist-nothing-on-transport-failure and append-history suites over a real store. `make verify` green.

## E4-T5: Complete Real Hermes Kanban End-to-End Gate G3

**Status:** Completed

### Objective

Prove one effective durable Hermes task from a real Watchman-triggered test-vault change.

### Deliverables

- isolated test Obsidian vault and Hermes board/workspace procedure;
- end-to-end harness;
- restart, duplicate, downtime, and ambiguous-result scenarios;
- G3 acceptance report;
- operator demo instructions.

### Requirements

`HER-*`, `DUR-*`, `TST-007`

### Dependencies

E4-T4 Completed.

### Acceptance

- AC-301 through AC-306 pass;
- one normal change generation creates one durable task;
- process restart and Hermes downtime do not lose committed work;
- duplicate attempt does not create duplicate accepted task under required capabilities;
- no real production vault automatic write is enabled yet.

### Evidence

- `internal/cli/g3_test.go`: the gate harness over real components only — the built binary as separate one-shot processes, the installed Watchman with a real trigger on a disposable vault, and the installed Hermes through a disposable board created and hard-deleted through the public CLI (TST-007; the user's active board selection and production vault are never touched).
- `TestG3AC301And305RealTriggerEndToEnd`: a real Watchman-triggered change in the isolated vault creates exactly one durably accepted task on the real disposable board with the stored external task id, the trusted/untrusted body separation, the configured resource, latest-state semantics, and receipt instructions with no note body.
- `TestG3AC302DuplicateResolvesToOriginal`: the committed intent survives a second process (restart-safe submission) and a duplicate submission of the same idempotency key resolves to the original task.
- `TestG3AC303DowntimeAndRestart`: with a failing Hermes stand-in the committed intent stays retryable and is never lost; after the executable is restored and re-acknowledged the drain's stale rebuild supersedes the original and delivers exactly one board task (the E9-T3/T3-F006 transport-coverage semantics: every executable swap is a re-acknowledge boundary, asserted through the `-rebuilt-` identity tie and the superseding decision linkage).
- `TestG3AC304AmbiguousUnknownNoFallback`: an ambiguous result records `unknown` locally, reconciles without any webhook or target fallback, and dead-letters for the operator.
- `TestG3AC306CapabilityGateBlocks`: a required capability the frozen report lacks blocks construction before any submission.
- `docs/VALIDATION.md` Gate G3: the acceptance table (AC-301..306) and the isolation procedure. `make verify` green.

---

# E5: Feedback Loop, Quarantine, and Reconciliation

**Epic status:** Completed  
**Purpose:** Make recursive vault maintenance bounded and conservative, then pass the production-capable gate.  
**Gate:** G4

## E5-T1: Implement Work Receipt CLI and Validation

**Status:** Completed

### Objective

Provide plugin-free Hermes cooperation through begin, complete, and fail receipt commands.

### Deliverables

- work receipt schema validation;
- `work begin|complete|fail` commands;
- active dispatch/resource/task lineage checks;
- relative path and digest validation;
- receipt persistence and execution projection update;
- invalid receipt audit.

### Requirements

`FBK-002` through `FBK-005`, `CLI-004`, `SEC-002`, `SEC-009`

### Dependencies

E4-T5 Completed.

### Acceptance

- forged or mismatched dispatch/resource/task receipt is rejected;
- changed paths are contained and bounded;
- no note body is accepted;
- receipt failure cannot erase dirty state;
- completion transaction can atomically schedule a follow-up.

### Evidence

- `internal/app/workreceipt`: the receipt service — schema-equivalent validation of the full work-receipt document and the bare change-array manifest forms (strict unknown-field decoding so note bodies are rejected, the `agent-dispatch.work-receipt/v1` schema version, the closed begun/completed/failed and failure-code sets, `sha256:<64hex>` digest shapes, a 1000-entry change cap, and the explicit byte bounds — 1 MiB for the manifest, 256 bytes per identifier, 200 bytes for the failure detail (SEC-009)), the lineage checks against the durable dispatch (existence, active-dispatch match, resource identity on the document form, accepted external-task identity, and one receipt row per dispatch/run), path validation through `records.NormalizePath` plus the `localfs.Resolver` containment defense configured from the route's resource root (SEC-002, symlink escape rejected), and the typed `InvalidError` carrying the bounded rejection reasons. Invalid submissions are audited through the append-only transition history and never persisted as valid (FBK-003).
- `internal/ports` + `internal/adapters/sqlite`: the work-receipt persistence surface — `LoadWorkReceipt`, `InsertWorkReceipt` (one row per dispatch/run pair, the UNIQUE constraint surfacing replays as the typed `ErrRunAlreadyRecorded`), and `CompleteWork`, the atomic completion transaction that updates the begun row to its terminal state and applies the completion (route transition, dirty-generation collapse, at most one latest-state follow-up intent) in one commit or not at all. `CompleteActive` was refactored onto the shared transactional core so the receipt update joins the same transaction. `FailureBudgetRemaining` counts the route's valid failed work completions since its last valid completion. Every accepted or rejected receipt appends a `work_receipt` audit transition with bounded context. `LoadIntent` now exposes the intent's resource id for the lineage checks.
- CLI (`internal/cli/work.go`): `work begin` (registers a run), `work complete` (manifest from a file or stdin with an explicit size bound; the atomic follow-up scheduling reported as `followup_dispatch_id`), and `work fail` (closed failure-code set, bounded detail, the failure-budget outcome between a bounded follow-up and `UNCERTAIN` operator resolution) — each mapped onto the stable registry codes (`work_receipt_invalid` exit 4, `dispatch_not_found` exit 4, `transition_invalid` exit 14, storage 20) with empty stdout on failure.
- Tests (`internal/cli/e5t1_test.go`): valid begin persisted and inspectable through `receipts list --kind work`; forged external task, unknown dispatch, missing flags, and replayed runs rejected with audited evidence and no valid row; the manifest validation table (absolute paths, traversal, non-canonical forms, malformed digests, note bodies, symlink escape through the configured root, the 1000-entry cap, wrong-run and wrong-schema document forms); clean completion returning the route to IDLE with the row updated in place; dirty completion scheduling exactly one ready follow-up and collapsing the dirty generation; cooperative failure keeping its bounded follow-up with budget 1 and exhausting into UNCERTAIN; and the full-document receipt recording its result revision. `make verify` green.
- Audit remediation (post-closeout review): the document form now enforces every schema-required field (dispatch_id, run_id, resource_id, status, submitted_at, changes — `docs/schemas/work-receipt.schema.json`), completing the schema-equivalence claim; durable-store failures during lineage or replay validation surface as the typed storage error (exit 20) instead of being recorded as invalid-receipt audit evidence (`TestStoreFailuresAreNotRejectionEvidence`); and the follow-up decision created by a dirty completion records the planned route revision as its policy revision with encoder-produced reason codes — the `"current"` placeholder was another fix lost in flight (`TestWorkCompleteDirtySchedulesOneFollowup`).

## E5-T2: Package and Validate Hermes Companion Skill

**Status:** Completed

### Objective

Ship a Hermes-facing skill that explains latest-state processing and receipt use without creating a plugin or changing Hermes.

### Deliverables

- production companion skill derived from example;
- installation instructions using public Hermes skill mechanism;
- task-variable mapping;
- no-receipt fallback behavior;
- skill validation in disposable Hermes task.

### Requirements

`BND-003`, `BND-004`, `FBK-006`

### Dependencies

E5-T1 Completed.

### Acceptance

- skill is optional for core dispatch correctness;
- Hermes core remains unchanged;
- skill does not grant permissions;
- task succeeds or fails independently from receipt submission;
- latest-state and untrusted-data rules are explicit.

### Evidence

- `docs/skills/agent-dispatch-wiki-maintenance/SKILL.md`: the production companion skill packaged from the E0-era example — Hermes skill frontmatter (name, description, version, platforms), the task-variable mapping table (DISPATCH_ID, RESOURCE_ID, HERMES_TASK_ID, RUN_ID, workspace, each mapped to its trusted source), the required behavior (register the run, process latest state under the llm-wiki SOT, track canonical vault-relative paths with before/after digests, submit bounded complete/fail receipts), the explicit no-receipt fallback (the CLI is cooperation, not a dependency: domain success never depends on receipt submission, failures are reported in the visible task result, and Agent Dispatch stays conservative without provenance), and the safety rules (no note bodies in receipts, no invented changes, no permission or Hermes-configuration mutation).
- `docs/skills/agent-dispatch-wiki-maintenance/INSTALL.md`: installation through the public Hermes skill mechanism only — local copy into the skills directory or `hermes skills install <url>`, verification through `hermes skills list`/`inspect`, uninstall, and the explicit statement that the skill grants no permissions and is optional for Agent Dispatch correctness.
- Tests (`internal/cli/e5t2_test.go`): the packaging assertions (required rules present, no permission or plugin surfaces) and the disposable validation against the then-installed baseline real Hermes — a throwaway HOME receives the skill by the documented local-copy mechanism, the public skills surface lists and inspects it, a disposable board task created with the public `--skill` selection carries the skill in its durable record (create JSON and public show), and the board is hard-deleted afterwards, leaving the real profile untouched. `make verify` green.

## E5-T3: Implement Exact Self-Change Suppression and Mixed-Change Handling

**Status:** Completed

### Objective

Use validated receipts to suppress only exact self-generated changes while preserving human or uncertain changes and creating one bounded follow-up.

### Deliverables

- receipt/observation matcher;
- temporal and lineage checks;
- exact suppression decisions and audit;
- mixed-change retention;
- follow-up generation transaction;
- no-receipt conservative behavior tests.

### Requirements

`FBK-001` through `FBK-004`, `FBK-007`, `FBK-008`, `CON-002`, `CON-003`

### Dependencies

E5-T2 Completed.

### Acceptance

- exact path and digest match may suppress;
- any mismatch remains dirty;
- mixed human/agent batch cannot be fully suppressed;
- ten bursts during active work produce at most one follow-up;
- missing receipt may create one redundant task but not infinite recursion.

### Evidence

- `internal/app/workreceipt/attribution.go`: the exact-suppression matcher (feedback-loop §5) — the dirty generation's observed changes collapse to the latest observation per path (a later divergent write never suppresses through an earlier matching digest), a change is verified self-generated only on an exact path plus after-digest match with a known observed digest and a non-null receipt digest inside the run's temporal window (an observation before the run began cannot be the run's output), and every other outcome (receipt_missing_path, digest_mismatch, digest_unverified, observed_before_run) retains the change as unresolved so a mixed or uncertain batch never fully suppresses (FBK-002, FBK-003, FBK-004). The bounded decision document (suppressed and unresolved path decisions, fully_suppressed) is persisted as an `attribution` audit transition for every completion (AC-403).
- `internal/ports` + `internal/adapters/sqlite`: `LoadActiveGenerationChanges` returns the observation changes of every batch merged since the active dispatch was created, excluding the dispatch's own activation batch; `AuditAttribution` appends the decision evidence; migration v4 `work-receipts-begun-at` preserves each run's begin timestamp across the terminal receipt update so the temporal window survives completion; `ActiveCompletion.DirtySuppressed` carries the fully-verified flag into the completion transaction, which clears the dirty generation and returns the route to IDLE with the new `work_completed_exact_suppression` reason (receipt-evidence guarded) instead of scheduling a follow-up; `version.SchemaRange` advances to 1-4 with the migration.
- `internal/domain/state`: the route state machine gains the ACTIVE_DIRTY to IDLE edge for exact suppression, guarded by the completion-receipt evidence and the pending-reconciliation refusal, with the SOT diagram (`persistence-and-state-machines.md` §6), the documented-edge table, and the dirty-never-erased guard test updated together — plain dirty completion still routes to FOLLOWUP_READY and an evidence-less suppression claim is rejected.
- CLI (`work complete`): the result reports `self_change_suppressed` and the suppressed paths; a fully suppressed generation clears the route with no follow-up while any mismatch keeps the conservative follow-up path unchanged.
- Tests (`internal/cli/e5t3_test.go`): exact match clears the route with one audited verified path, collapsed dirty generation, and zero follow-up intents; a wrong digest stays dirty with exactly one follow-up and an audited digest_mismatch; a mixed agent-plus-human batch records both outcomes and never fully suppresses; ten bursts without receipt coverage collapse into exactly one ready follow-up with the generation collapsed and every burst recorded unresolved (AC-402, AC-404, AC-405, AC-406 posture). `make verify` green.
- Audit remediation (E5 validation): the matcher's vacuous-suppression case is guarded — an empty observed window while a dirty generation exists is treated as unproven, never as cleared (the audit's S1/S3 combination).
- Audit remediation (post-closeout review): a receipt path the generation never observed is recorded as unresolved `receipt_extra_path` and blocks full suppression — AC-404's extra-paths clause is now enforced by the matcher (`TestExtraReceiptPathStaysDirty`, `TestMatchOutcomes`), and the audited decision document iterates paths in canonical order so repeated matches produce identical evidence.

## E5-T4: Implement Protected/Bulk Quarantine and Full Reconciliation

**Status:** Completed

### Objective

Complete conservative handling for protected paths, bulk/hard limits, overflow, fresh instance, manual release, and current-state reconciliation.

### Deliverables

- quarantine records and commands;
- operator release/discard lineage;
- JSON output contracts for quarantine, batch, and decision records;
- full vault enumeration and path-fact comparison;
- the full nine-value `--reason` vocabulary the command accepts: initial, scheduled, overflow, fresh-instance, lost-cursor, manual, delivery, stale-active, and startup (the six-value list here was the E5-era subset; the E9-T5 truth pass records the delivered set);
- reconciliation/active-route merge rules;
- tests for partial-list prohibition.

### Requirements

`PTH-008`, `POL-005`, `POL-006`, `SRC-005`, `CLI-006`, `OPS-006`, `OPS-007`

### Dependencies

E5-T3 Completed.

### Acceptance

- protected paths never enter automatic task manifest;
- overflow never dispatches partial ordinary changes;
- repeated reconciliation causes collapse into one pending generation;
- release creates new decision and audit actor/reason;
- full enumeration obeys all containment and size rules.

### Evidence

- `internal/ports/quarantine.go` + `internal/adapters/sqlite/quarantine.go`: the durable hold surface — `CommitQuarantineLineage` (observation, batch, decision, and the `quarantine_items` hold in one transaction, no intent ever), `CommitDropLineage`, `CommitReconcileLineage` (marks `pending_reconcile=1` and records the source position, never a partial dispatch), `List`/`Load` hold projections, `ReleaseQuarantine` (resolves `held` atomically with actor, reason, the supersedes lineage on a replacement reconciliation decision, and the pending generation), `DiscardQuarantine` (no task creation), `MarkPendingReconcile`, the `path_facts` snapshot, and the batch-less reconcile decision and single latest-state intent commits. `CompleteActive` now clears the pending flag when the follow-up generation is created, and the route machine gained the ACTIVE_CLEAN to FOLLOWUP_READY completion edge for a pending reconciliation (SOT diagram and lockstep tests updated with the receipt-evidence and pending-only guards).
- CLI (`internal/cli/quarantine.go`, `reconcile.go`): `quarantine list|show` with state filters, `quarantine release|discard` requiring `--reason` and `--yes` (exit-code mapping: `quarantine_not_found` 4, `transition_invalid` 14 on double resolution), and `reconcile --route --reason <nine documented reasons> [--submit]` — default persists the decision, enumeration, comparison, and snapshot; `--submit` additionally submits the eligible intent through the gated sink. The real dispatch path (`internal/cli/plan.go`) now branches on the plan disposition, so protected and bulk batches hold, overflow and fresh-instance reconcile, and drops persist evidence only.
- `internal/app/reconcile/full.go` + `internal/app/quarantine/service.go`: the full-scope enumeration (resolver containment, include/exclude and protected/immutable patterns, markdown file scope, bounded hashing; unverifiable paths reported with absent digests instead of truncating), the path-fact comparison with sorted added/removed/changed diff projected as the intent's bounded evidence manifest, and the operator resolution semantics.
- Contracts: `canonical-record-contracts.md` §7 defines the quarantine record, batch record, decision record, and full-reconciliation result JSON shapes.
- Audit remediation (E5 validation): a full reconciliation on an idle route whose comparison proves no work remains now resolves the pending generation (`ClearPendingReconcile`, `TestIdleNoDiffReconcileClearsPending`) instead of waiting indefinitely for a dispatch completion.
- Audit remediation (post-closeout review): full reconciliation is now the operator exit from UNCERTAIN — `ResolveUncertainReconciliation` applies the declared UNCERTAIN to FOLLOWUP_READY (`reconciliation_resolved`) transition in one transaction, releases the stale dispatch's slot, collapses the retained dirty generation and pending flag, creates exactly one latest-state intent when work is due (a fresh pending generation is then marked for that scheduled intent, so its completion schedules the one documented follow-up), and lands IDLE through the follow-up-dropped edge when it is not (`TestUncertainResolvedByReconcile`, `TestResolveUncertainReconciliation`, `TestConcurrentUncertainResolutionSingleWinner`; previously budget exhaustion left the route permanently wedged with no operator surface — a lost resolution race is a typed `transition_invalid` conflict, never storage; the resolution is fenced against merges landing inside the reconciliation's enumeration window and marks or clears its pending generation atomically, and the intent's evidence manifest, like the removal diff and the snapshot, never asserts unverifiable deletes under unreadable subtrees). The completion transaction also refuses — as an optimistic-concurrency conflict — a state where the in-transaction snapshot needs a follow-up the caller never prepared (`TestCompleteActiveRefusesUnpreparedFollowup`), so a reconciliation arrival racing a clean completion can no longer drop the pending signal into a follow-up-less FOLLOWUP_READY. The unreadable-subtree posture is completed end to end: stored path facts under a skipped subtree are retained in the replaced snapshot, not just excluded from the removal diff (`TestReconcileUnreadableSubtreeKeepsStoredFacts`), and the quarantine error boundary classifies store failures as storage and defects as internal instead of relabeling everything storage (`quarantine_release_denied` asserted by code string; the classification arms pinned by `TestResolutionErrorClassification` and `TestQuarantineErrClassification`).
- Tests (`internal/cli/e5t4_test.go`): a protected path held with zero intents and full release lineage (replacement reconciliation decision, pending generation, double-release conflict); fresh-instance signals while active never dispatching the partial batch, repeated signals collapsing into one generation, and completion clearing it into exactly one follow-up (AC-407, AC-408); a bulk batch over the rewritten threshold quarantined and discarded without task creation (POL-005); full reconciliation enumerating the markdown scope plus a reported escape symlink while excluding out-of-scope files, scheduling exactly one intent, an unchanged repeat scheduling nothing, and unknown reasons rejected (OPS-006, SRC-005). The G3 gate flow now drives the documented fresh-instance reconciliation before its accepted-task assertions. `make verify` green.

## E5-T5: Complete Production-Capable Test-Vault Gate G4

**Status:** Completed

### Objective

Prove bounded recursive behavior, mixed edits, quarantine, and reconciliation on a real test vault before production write enablement.

### Deliverables

- end-to-end feedback harness;
- synthetic Hermes edits and concurrent human edits;
- protected/bulk/overflow scenarios;
- no-receipt scenario;
- G4 acceptance report;
- explicit production-enable checklist and route-revision acknowledgement state.

### Requirements

`FBK-*`, `CON-*`, `TST-008`

### Dependencies

E5-T4 Completed.

### Acceptance

- AC-401 through AC-409 pass;
- automatic-write gate is explicitly reviewed;
- no infinite task loop occurs under defined stress scenarios;
- unknown attribution never silently drops a human change;
- production route remains inactive until the operator acknowledges the computed route revision after review.

### Evidence

- `internal/cli/g4_test.go`: the gate harness — `TestG4FeedbackLoopGate` (the three-generation loop: active-task merging, ten-burst collapse, no-receipt bounded fallback, mismatch retention, exact suppression with audit, and the final idle state with the loop bounds asserted), `TestG4StructuralScenarios` (the protected-path hold with release lineage and the fresh-instance pending generation under active work), `TestG4DistinctOperatorOperations` (the distinct retry/reprocess/rerun/reconcile lineage semantics), and `TestG4ProductionGateReviewed` (the production gate: enabling requires the exact computed route revision; a wrong acknowledgement is refused with exit 3, and the acknowledged revision is recorded).
- Product remediation found by the gate: `--acknowledge-production-gate` was parsed as a boolean flag, so the acknowledged revision value was never checked — it is now a value flag and `route enable` refuses any acknowledgement that does not equal the computed route revision; the earlier enable tests were updated to acknowledge the computed revision.
- Audit remediation (E5 validation): the cli-spec and runbook route-enable examples now carry the computed-revision acknowledgement value (`--acknowledge-production-gate <computed-route-revision>`), matching the remediated value-flag behavior.
- `docs/VALIDATION.md` Gate G4: the per-criterion evidence table (AC-401..409), the loop-bound stress statement, the explicit production-write gate review, and the seven-step production-enable checklist (route revision review, quarantine resolution, reconciliation snapshot, frozen capability report and board confirmation, computed-revision enablement, the platform-scheduled reconciliation recipe per OPS-007, and the post-gate `--submit` decision). `make verify` green.

---

# E6: Hermes Webhook, Operations, Packaging, and v0.1 Release

**Epic status:** Completed  
**Purpose:** Add the explicit secondary delivery mode and make the system installable, inspectable, maintainable, and releasable.  
**Gate:** G5

## E6-T1: Implement Explicit Hermes Webhook Adapter

**Status:** Completed

### Objective

Add authenticated outbound Hermes webhook delivery as an explicit route target, without fallback coupling to Kanban.

### Deliverables

- webhook target config and secret resolution;
- structured HTTP client;
- idempotency header support;
- transport vs durable acceptance mapping;
- TLS and timeout policy;
- fake endpoint conformance tests;
- updated capability matrix.

### Requirements

`WHK-001` through `WHK-005`, `SEC-006`, `SEC-007`

### Dependencies

E5-T5 Completed.

### Acceptance

- webhook is selected only by explicit target ID;
- secret never enters SQLite/logs;
- 2xx is not called durable unless verified contract says so;
- unknown webhook result does not create Kanban task;
- same dispatch retry reuses idempotency key.

### Evidence

Delivered in `internal/adapters/hermeswebhook` (sink, strict client, typed errors) and `internal/adapters/secretresolver` (env, file, fd with the process-lifetime descriptor cache, and the controlled darwin keychain lookup): the static capability declaration is derived from the frozen E0-T4 §9 evidence (durable_acceptance false — a 2xx is transport acceptance only, WHK-004 — with the idempotency key transmitted verbatim under the configured header, WHK-005), the strict client enforces TLS 1.2+ with system roots, no proxy, one end-to-end deadline, and no redirect following, and the conservative response mapping pins the definite-refusal set, the unknown set (408/409/429/5xx), and the pre-transmission definite classification. Secrets resolve immediately before each submission and never enter SQLite or logs (SEC-006/007, with response and diagnostic redaction). The CLI wires the target behind fail-closed construction gates (resolveSink, the offline `config validate --probe-targets` declaration, and the endpoint-as-target-scope at every intent-construction site), the config schema and semantic validation gained the webhook `required_capabilities` and auth-shape gates, and `.mulgaeignore` excludes the operator-authored agent guidance from review capture. Verified by `make verify` (format, vet, staticcheck, imports, unit and race tests, manifest, schema, traceability) plus the conformance suite (httptest TLS endpoints through the verification path) and the CLI end-to-end tests (accepted submit, unknown dead-letter with no fallback, retry idempotency-key stability, rerun scope, capability and timeout gates on both surfaces). Reviewed through three full-target Mulgae rounds (r_01a026ad, r_01a026c0, r_01a026d2; all coverage complete, ci pass, zero structured findings): round 1 and round 2 report findings were remediated in place, and the round-3 residual (the fd-cache race-loser finalizer, idempotency-header collision gating, readBounded concurrent readers, documentation nits, and the remaining test gaps) is recorded as the hardening deferral for the epic validation audit under run r_01a026d2 (reports_only; no structured finding IDs exist). Changelog 1.0.8.

## E6-T2: Implement Doctor, Status, Retention, and Operational Observability

**Status:** Completed

### Objective

Complete inspection, health, audit, pruning, integrity, and maintenance behavior.

### Deliverables

- structured logs and event names;
- `status` and `doctor` commands;
- retention planner and prune command;
- integrity and vacuum commands;
- stale lease/active/unknown findings;
- redaction tests;
- operational runbook validation.

### Requirements

`OPS-001` through `OPS-005`, `OPS-008`, `CLI-004` (doctor, status, retention maintenance), `CLI-007`, `SEC-007`

### Dependencies

E6-T1 Completed.

### Acceptance

- doctor detects all required failure classes;
- pruning is dry-run by default and preserves unresolved lineage;
- logs contain causal IDs and no note bodies/secrets;
- vacuum refuses unsafe active conditions;
- status reports route dirty and delivery uncertainty clearly.

### Evidence

Delivered in `internal/observability` (the structured log: §3 event vocabulary, §2 causal correlation, §4 levels with the warn default, path-privacy redaction recursing into nested payloads), `internal/app/doctor` (the pure findings examination over configuration, resource roots, store health, integrations, targets, and stale runtime state), `internal/app/maintenance` (OPS-003 policy resolution with configured overrides and --before narrowing), the sqlite maintenance surface (queue counters, stale leases, oldest unresolved, the FK-guarded children-first prune with its cascade-consistent dry-run plan and append-only audit record, the active-work refusal, vacuum, integrity), and the CLI `status`/`doctor`/`maintenance` commands with the global `--log-level`/`--trace-id` options and lifecycle event wiring in the dispatch runtime (OPS-001, SEC-007). Verified by `make verify` plus the e6t2 suite: status counters and warnings, every doctor finding code at unit level with severity and remediation plus the CLI failure-class slice, prune dry-run/execute consistency, unresolved-lineage and held-quarantine preservation, the audit row with actor and reason, the vacuum refusal and recovery, integrity modes, the fail-closed log level, and the causal-ID lifecycle log proof. Reviewed through two full-target Mulgae rounds (r_01a026fc remediated in place; r_01a0270a deferred) — all coverage complete, ci pass, zero structured findings; the round-2 residuals (plan/execute SQL duplication with residual count drift on cascaded classes, the --dry-run/--yes precedence, migration_pending observability under auto-migration, documented-but-unaccepted global options, the remaining test gaps) are recorded as the hardening deferral for the epic validation audit under run r_01a0270a (reports_only; role-report coordinate identities). Changelog 1.0.9.

## E6-T3: Package macOS/Linux Installation and Scheduled Reconciliation

**Status:** Completed

**Post-delivery note (D-023):** the Linux packaging and `systemd --user` scheduling this task delivered are retired by the macOS-only support policy (E9-T8); the task record below stands as the history of what shipped with v0.1.0.

### Objective

Produce reproducible binaries, configuration/install procedures, Watchman trigger scripts, and daily reconciliation scheduling for macOS and Linux (as scoped before D-023).

### Deliverables

- release build and checksum process;
- platform config/state path behavior;
- tested Watchman install/uninstall scripts or CLI flow;
- `launchd` scheduled reconciliation example;
- `systemd --user` scheduled reconciliation example;
- shell completion;
- upgrade/backup procedure.

### Requirements

`SCP-008`, `OPS-006`, `OPS-007`, `OPS-009`

### Dependencies

E6-T2 Completed.

### Acceptance

- clean-host installation scenario works on macOS;
- Linux CI validates binary, config, SQLite, and systemd unit syntax where possible (superseded: no successful Linux `make verify` is recorded, hosted CI is not used, and the 2026-08-22 review's diagnostic linux/arm64 container runs failed with exit 2 (D-017); `make schedule-check` validates the platform artifact where the tool exists);
- uninstall does not delete SQLite or config without explicit flag;
- trigger and schedule are idempotently inspectable;
- no Agent Dispatch daemon is introduced.

### Evidence

Delivered as the release process (`make release`: byte-reproducible darwin/arm64 and linux/amd64 binaries with the full commit hash and commit-date build time, verified by identical SHA-256 digests across consecutive builds, plus a portable `LC_ALL=C`-sorted `SHA256SUMS`; `make clean` covers `dist/`), the platform-validated scheduling artifacts (`make schedule-check` inside `make verify`: `plutil -lint` on macOS, `systemd-analyze verify` on a Linux host where the tool exists (no successful Linux `make verify` is recorded; the 2026-08-22 review's diagnostic linux/arm64 container runs failed, D-017), `sh -n` always), the launchd LaunchAgent and systemd --user service/timer examples invoking the verified one-shot `reconcile --reason scheduled` shape (whose `--output json` option the dispatches family now accepts, pinned by a test driving the example's exact arguments) with no daemon, the runbook §10 uninstall example that retains SQLite and configuration by design, `agent-dispatch completion bash|zsh` derived from the registered tree, `agent-dispatch maintenance backup` (Lstat symlink guard, `backup_target_exists` conflict, partial-file cleanup, the dedicated `maintenance.backed_up` event, and a verified owner-only standalone snapshot), and `docs/operations/installation.md` (platform paths, clean-host scenario, scheduling, upgrade, backup, uninstall). Verified by `make verify` plus the e6t3 suite: the clean-host init through the default paths with owner-only permissions and fail-closed re-init refusal, the backup snapshot opening standalone with quick-check integrity, the uninstall safety pins, the schedule invocation shapes with timer properties, the completion registry invariant parsed back out of the emitted script, and the sandboxed per-command completeness proof. Reviewed through three full-target Mulgae rounds (r_01a0272c and r_01a0273e findings remediated in place; r_01a0274b as the authorized extra round whose residuals are the deferral) — all coverage complete, ci pass, zero structured findings; the round-3 residuals are dispositioned individually as the hardening deferral under run r_01a0274b (reports_only): backup create-vs-guard race and umask window (mitigated by the owner-only snapshot verification; retry-safe), unsigned release artifacts (accepted for v0.1.x; SHA256SUMS provides integrity), further systemd sandboxing (post-v0.1 hardening), the duplicated no-overwrite guard (cosmetic), launchd output visibility (documented in the runbook), release-reproducibility automation (the double-build check is manual and re-run by E7-T12), and the remaining prose/test notes (folded into E7-T10). Changelog 1.0.10.

## E6-T4: Verify and Release v0.1.0

**Status:** Completed

### Objective

Run all gates, reconcile implementation with SOT, freeze compatibility, and produce the v0.1.0 release artifacts.

### Deliverables

- complete G0-G5 acceptance report;
- requirement traceability update with evidence;
- security and architecture review;
- migration and upgrade rehearsal;
- real test-vault demonstration;
- binaries, checksums, schemas, examples, companion skill, changelog, and release notes;
- list of deferred future work;
- final roadmap status update.

### Requirements

All `BND-*` through `WHK-*` requirements.

### Dependencies

E6-T3 Completed.

### Acceptance

- AC-001 through AC-506 pass or every SHOULD exception is explicitly accepted;
- all MUST requirements pass (superseded by the 2026-08-22 compliance review: 14 MUST gaps are open under D-017 until E7 completes);
- no later feature is partially enabled;
- Hermes plugin remains absent;
- production route enablement is an explicit operator action;
- roadmap tasks E0-T1 through E6-T4 are Completed;
- v0.1.0 artifacts are reproducible and version-compatible.

### Evidence

**Epic closeout (validation audit):** the audit revalidated every member-task hardening deferral against its native Mulgae authority (r_01a026d2, r_01a0270a, r_01a0274b, r_01a02791 — all committed publications, coverage complete, ci pass, reports_only) and remediated the valid findings in place: the secretresolver fd cache serializes wrapper creation and reads through offset-based ReadAt (the race-loser finalizer can no longer close the shared descriptor, and the documented pipe one-shot reference keeps its deadline-bounded fallback), the webhook construction gate refuses idempotency-header collisions with the authentication and transport headers, the prune dry-run plan mirrors the executed cascade through one shared prunable-intents/decisions/batches CTE chain, doctor opens the store unmigrated so the migration state is observable, and the documented global `--state-dir` and `--timeout` options are implemented with fail-closed validation and per-invocation reset — each with regression tests and `make verify` green including race. Three whole-epic review rounds then converged (r_01a027bc whose absence findings were the committed-diff target artifact, r_01a027c5 remediated, r_01a027d2 returning reports_only with zero structured findings and the residual low/info items dispositioned as the documented v0.1.0 hardening posture); the requirement-to-owner matrix, seam inspection, roadmap, VALIDATION, traceability, and manifest agree, and the v0.1 sequence is complete with all 33 tasks Completed and gates G0-G5 closed.

### Evidence

Delivered as the release-verification surface: the executable G5 acceptance suite (`internal/cli/e6t4_test.go`: AC-501 through AC-506 — the webhook route's auth-without-persistence, transport-vs-durable distinction, and no-fallback proof; doctor's actionable stable-coded findings; prune's resolved-expired removal preserving unresolved lineage and the append-only audit; the clean-host macOS install→dispatch→scheduled-reconciliation→doctor flow with the production-gate acknowledgement; the release-way build with the version envelope and artifact set; plus the upgrade-and-backup rehearsal restoring the snapshot standalone with its lineage), the Gate G5 evidence table in `docs/VALIDATION.md` (closing G0–G5: G0 by E0-T5, G1–G4 previously, G5 here), the regenerated requirement traceability matrix (`make traceability`, 33 tasks, 15 groups, every requirement ID resolved to its owning and verifying tasks), the release artifacts (`make release VERSION=v0.1.0`: byte-reproducible darwin/arm64 and linux/amd64 binaries with SHA256SUMS; `docs/RELEASE-NOTES-v0.1.0.md`; the SOT package manifest-verified; schemas, examples, and the companion skill in place), and the security/architecture review posture carried by the per-task Mulgae rounds and the frozen ADR set. Compatibility is frozen (config version 1, schema range 1-4, record payload versions, adapter profiles baseline-hermes/2026.07.27.00 — `agent-dispatch version` reports every axis); no deferred feature is partially enabled (the future-work list stands apart); the Hermes plugin remains absent; production enablement stays the explicit computed-revision operator action. Verified by `make verify` on the release tree (darwin/arm64 only; no successful Linux run is recorded: the 2026-08-22 review's diagnostic linux/arm64 container runs failed with exit 2, and the AC-505 verification is the SCP-008 exception under D-017). Reviewed through two full-target Mulgae rounds (r_01a0277c and r_01a02791, both remediated in place: the delivered webhook adapter entry in `agent-dispatch version`, the doctor stable-nonzero contract with the `doctor_findings_present` registry code, the real production-gate enablement and uninstall ordering in the AC-504 evidence, the computed-revision rehearsal enable, the monotonic audit assertion, the webhook target-type and v0.1.0 version assertions, the unified doctor emission with the version adapter pin, and the documentation corrections); the epic validation audit reconciles the member-task hardening deferrals (r_01a026d2, r_01a0270a, r_01a0274b) and this task's round-2 residuals under run r_01a02791. Changelog 1.0.11.

---

# E7: MVP Compliance Review Remediation

**Epic status:** Completed  
**Purpose:** Remediate every finding of the 2026-08-22 MVP compliance review (D-017): the three Blockers in the core durability and coordination user stories, the five High findings in submit-path integrity and evidence, all Medium findings, and the Low/Info dispositions; then re-verify the gates and release v0.1.1.  
**Gate:** MUST closure — every one of the 14 GAP requirements PASS or carrying an explicit recorded exception — with gates G1-G5 re-run on the real Hermes and Watchman.

## E7-T1: Restore Documentation Truth After the Compliance Review

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Correct every false or stale claim the review verified as wrong, so the SOT package describes the actual v0.1.0 state before code remediation begins.

### Deliverables

- corrected release and verification claims across the roadmap, changelog corrigendum, acceptance-criteria AC-505 wording, project-charter success definition, canonical-record-contracts, examples/scripts README, and the docs README SOT version;
- VALIDATION.md refresh: header date/SOT version/task statistics, schema count (12, not 8), the G2 "every defined crash boundary" overstatement, the G5 Linux-leg contradiction, and removal of the nonexistent `TestG2MigrationInterruptedUpgrade` citation;
- the review report admitted into the manifest-verified package (done with D-017; verified here).

### Requirements

`SCP-008`, `TST-009`

### Dependencies

D-017 recorded; v0.1.0 sequence complete.

### Acceptance

- no living document claims a Linux verification that was not performed;
- VALIDATION.md cites no nonexistent test and carries no internal contradiction about AC-505;
- `make manifest-check`, `make schema-validation`, and `make traceability` pass with the report included.

### Evidence

Delivered as a documentation-only correction set over ten files (the E7-T1 record originally said twelve; corrected by E8-T6): every surviving false verification claim now carries one accurate statement (no successful `make verify` run on a supported Linux host is recorded, hosted CI is not used, and the review's diagnostic linux/arm64 container runs failed with exit 2) at the roadmap's E1-T1/E6-T3/E6-T4 acceptance and evidence wording plus the superseded "all MUST requirements pass" bullet, the AC-505 criterion, the charter's success definition, the record-contract and examples README validation sentences, the implementation-guide CGO policy row, and inline markers on the false CHANGELOG 1.0.10/1.0.11 claims; `docs/VALIDATION.md` is truthful about its evidence (current header and statistics: 63 manifest-basis Markdown files, 12 schemas, 8 epics, 45 tasks; the G2 crash-boundary scope naming the in-process-only boundaries; the AC-203 hollow-assertion and AC-207 always-skip corrections owned by E7-T2/E7-T4; the G4 store-direct follow-up-activation bypass note; the G5 AC-505 status; the removed nonexistent `TestG2MigrationInterruptedUpgrade` citation; the em-dash check scoped to `docs/specs/`); `docs/README.md` and VALIDATION align on SOT 1.0.14 with CHANGELOG entry 1.0.14; and the roadmap's status artifacts (task index, current-state counts, epic status) stay mutually consistent. Verified by `make verify` on darwin/arm64 (all checks green including the manifest with the admitted review report). Reviewed through two full-target Mulgae rounds (r_01a02afe-da64: four valid report findings — inconsistent roadmap status artifacts, overbroad no-Linux-run absolutes contradicting the recorded Linux facts (D-017), residual CI wording, and the em-dash check scope with three newly introduced em dashes — all remediated in place; r_01a02b0d-3389: coverage complete, ci pass, zero structured findings, reports_only), with the round-2 residuals recorded as the hardening deferral for the epic validation audit under run r_01a02b0d-3389 (reports_only; no structured finding IDs exist; residuals: the 1.0.14 corrected-locations list naming E6-T2 though no E6-T2-owned roadmap wording changed, the platform-unqualified `make verify` claim in the 1.0.14 entry, and the pre-existing testing-strategy CI-stages section owned by E7-T10). Changelog 1.0.14.

## E7-T2: Wire Crash Recovery, Rerun Supersession, and Follow-Up Activation

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close the review's three Blocker findings so the core v0.1 user stories — crash recovery mid-submit, one authoritative task per route, subsequent-generation handling — work in the product path.

### Deliverables

- expired-submitting recovery invoked at the start of every submit entry point before unknown reconciliation, with doctor remediation text matching the actual recovery behavior;
- a real process-death test during submit through the crashbin lease hook;
- rerun state guard and supersession of the original intent through the declared superseded edges, refusing in-flight originals;
- route active-slot verification in drain and submit so only the slot-holding intent is submitted;
- follow-up activation on acceptance (FOLLOWUP_READY to ACTIVE_CLEAN) and due-follow-up submission from the Watchman arrival and scheduled reconcile paths;
- G4 and E5 gate tests rewritten to drive full generations through the product path without store-direct activation.

### Requirements

`DUR-005`, `DUR-006`, `DUR-010`, `CON-001`, `CON-003`, `FBK-005`, `FBK-008`, `OPS-005`

### Dependencies

E7-T1 Completed.

### Acceptance

- a process killed mid-submit is recovered by the next drain or dispatch without manual database edits;
- rerunning an in-flight intent is refused and rerunning a ready intent supersedes it, leaving exactly one authoritative task;
- a second generation reaches Hermes, activates, and completes through `work begin`/`work complete` unaided;
- gates G2 and G4 re-run green against the real Hermes.

### Evidence

Delivered as the three Blocker fixes in the product path (B-1): `dispatches drain` now runs `Runtime.Recover` before unknown reconciliation and reports the recovered leases in its envelope (`internal/cli/dispatches.go`), so an expired submitting intent heals through the drain alone — proven by `TestG2DuringSubmitProcessDeathRecovers` (a real `crashbin lease --die` process death inside the submit window, recovery to unknown with audit) and `TestDrainRecoversExpiredSubmittingWithoutManualEdits` (CLI level); the doctor remediation texts now name the actual exits (`internal/app/doctor/doctor.go`). (B-2): `OperatorService.Rerun` refuses submitting/unknown/reconciling, retry_wait, and terminal-authoritative originals with actionable guidance; `Store.RerunIntent` supersedes the ready or dead-lettered original through its declared edge in the same transaction; `Runtime.SubmitOnce` and `Runtime.Drain` enforce the route's active slot (a dispatch never submits beside another authoritative task, and uncertain/quarantined routes submit nothing) — proven by `TestOperatorRerunCreatesNewLineageAndKey` (supersession audited), `TestOperatorRerunRefusesInFlightAndTerminalWork`, and `TestRerunSupersedesReadyLeavingOneAuthoritativeTask` (CLI level, drain submits exactly one). (B-3): acceptance inside the submit flow promotes a pending follow-up (`Runtime.promoteFollowup`, FOLLOWUP_READY to ACTIVE_CLEAN with the store's activation guard), and the scheduled `reconcile --submit` path now drains due work behind the enabled-route gate, so a follow-up generation reaches the target and becomes beginnable without any manual step — proven by `TestScheduledReconcileSubmitsDueFollowup` and by the rewritten G4/E5 suites: every multi-generation scenario (`g4_test.go`, `e5t1/e5t3/e5t4`) drives drain submission, acceptance-time activation, and `work begin/complete` through the CLI with the store-direct activation bypass removed (`submitFollowupProductPath`); `TestG2AC203` was rewritten with a real sink baseline (exactly one submission, then the receipt-loss crash window). The round-1 Mulgae review remediations are folded in (run r_01a02b3b-1932, reports_only): the promotion crash window is self-healing (the drain promotes an accepted follow-up left in FOLLOWUP_READY, `promoteAcceptedFollowup`), the route-slot predicate is enforced inside the lease transaction itself (`AcquireAttempt` SQL plus the `ErrRouteSlotHeld` explanation, with the runtime pre-check as the fast path), the expired-lease sweep is route-scoped, the crashbin `--die` flag parsing was fixed with a hard-death marker the test asserts, the scheduled drain bound is a named constant with its committed-mutation note on failure, the cli-spec drain/rerun/reconcile contracts state the new behavior, and the coverage gaps are closed (`TestSubmitRefusedWhenSlotHeldByAnother`, `TestRerunDeadLetteredOriginalSuperseded`, `TestRerunIntentStoreBackstopRefusesInFlight`, and the rewritten `TestReconcileSubmitSkippedWarning` proving the disabled-route gate). Verified by `make verify` on darwin/arm64 and gate re-runs: G2 (`internal/app/dispatch/g2_test.go`) and the G1/G3/G4/G5 CLI suites including the real Hermes and Watchman legs all green. Changelog 1.0.15.

## E7-T3: Enforce Submit-Path Revalidation and Durable Path Facts

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Make POL-008/SEC-010 revalidation real immediately before side effects, and make unchanged and metadata-only suppression real in the durable dispatch path.

### Deliverables

- the stored route revision and target identity on `IntentSnapshot` with a fresh comparison before lease acquisition, superseding stale intents through the declared `route_revision_invalidated` edge with a replacement decision;
- the tautological plan-time revision check replaced by genuine revalidation, and rerun/follow-up requests refreshed to the current revision;
- a SQLite-backed `PathFacts` implementation injected into the plan pipeline, replacing the hardcoded no-facts source;
- path-fact maintenance inside the ingestion transaction;
- `unchanged_content` reason propagation from dropped batches into plans and decisions.

### Requirements

`POL-008`, `SEC-010`, `PTH-007`

### Dependencies

E7-T2 Completed.

### Acceptance

- a configuration change between planning and drain supersedes the stale intent instead of submitting it;
- a target re-point never submits a stored intent to a different sink or board while recording false lineage;
- a byte-identical modify is suppressed with the `unchanged_content` reason visible in the decision.

### Evidence

Delivered as the H-1/H-2 fixes (H-1): `ports.IntentSnapshot` carries the stored route revision (`LoadIntent` SELECT), every submit runtime (dispatch, drain, scheduled reconcile) installs `stalenessCheckOf` so POL-008/SEC-010 revalidation runs inside `SubmitOnce` immediately before the lease commits - a stored intent whose route revision, target identity, or target scope no longer matches the active configuration is superseded through the declared `route_revision_invalidated` edge and rebuilt by `OperatorService.RebuildStale` under the current revision and target, with the superseding decision recording the reason and the plan-time tautological self-comparison in `planPipeline` removed; rerun now derives its revision from the active configuration instead of the stored plan (its target identity still inherits the stored values; resolving it is deferred with the round-2 review residuals). (H-2): the ingestion transaction maintains the `path_facts` snapshot (`upsertPathFacts` inside `CommitLineage`), the durable dispatch path plans against the stored facts (`durableFacts` wired into `planPipeline`; plan and dry-run keep the documented no-history fallback), and the planner propagates `unchanged_content`/`create_delete_never_existed` drops into the plan and decision reason codes (`propagateDrops`). The E5 uncertain-resolution test now drives due work with a real file change (the maintained facts make a no-diff reconciliation resolve to the documented no-work exit instead). Proven by `internal/cli/e7t3_test.go`: `TestStaleIntentRebuiltUnderActiveRevision` (supersede plus replacement under the active revision with the audited invalidation), `TestStaleTargetRepointNeverSubmitsFalseLineage` (current target scope recorded), and `TestUnchangedModifySuppressedDurably` (fact recorded by ingestion, byte-identical modify drops with `unchanged_content` in the decision, no second intent). The round-1 Mulgae review remediations are folded in (run r_01a02b75-16aa, reports_only): the direct `dispatch` submit runtime now installs the staleness check (the earlier wiring miss), `dispatches rerun` resolves the active revision and target identity instead of inheriting the stored plan's, the stale-rebuild recursion is depth-bounded with an unresolvable rebuild refused, the drain surfaces a staleness refusal it cannot resolve as an operator-visible warning instead of aborting, the superseding decision inherits the original's policy revision while carrying the replacement's route revision, and the E2-T4 plan-time-revalidation evidence claim and the path-facts section citation were corrected. Verified by `make verify` on darwin/arm64. Changelog 1.0.16.

## E7-T4: Repair Gate-Evidence Tests and Platform Guards

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Make the cited gate evidence real: no always-skipped acceptance test, no silently broken contract lockstep guard, and platform-honest test classification.

### Deliverables

- the migration-interruption acceptance test rewritten to die between migration units and inside one unit, then complete to the newest schema with the integrity check;
- contract lockstep paths corrected with a fatal (not skipped) failure when the schema files are missing;
- during-submit and after-remote-acceptance crash boundaries covered by real process deaths;
- the darwin-only keychain tests guarded with the repository's runtime skip convention;
- VALIDATION.md G2 and TST-004 wording matched to the new evidence.

### Requirements

`TST-004`, `TST-009`, `SCP-008`

### Dependencies

E7-T2 Completed.

### Acceptance

- every acceptance test cited as evidence executes and asserts on every supported development platform;
- the lockstep guard fails loudly on schema/enum drift;
- the test suite classifies platform-specific tests as skips, never failures, off-platform.

### Evidence

Delivered as the evidence-integrity repairs (H-4): `TestG2AC207` was rewritten to interrupt between every pair of migration units and inside the final unit through the crashbin (`migrate-partial` now creates the ledger first through `EnsureLedgerForHarness` so every boundary including the pre-first-unit leg asserts; the new `migrate-mid-unit` mode executes the unit's SQL through the store's own harness method and dies before the ledger insert commits, with an asserted hard-death marker) - every leg executes its assertions on every platform, replacing the earlier pre-ledger leg that skipped unconditionally; the contract lockstep tests read the real `docs/schemas` path (the wrong relative path made them skip silently on every run) and fail loudly when the schemas are missing, so schema/enum drift is caught; the after-remote-acceptance crash boundary gained a real process-death variant (`crashbin submit-die`: lease, a genuine stub-hermes submission, hard death before the receipt; `TestAfterAcceptanceProcessDeathRecoversDedupSafe` proves the drain heals the expired lease, the unprovable acceptance lands in the honest retry or dead-letter state, and the dedup-safe retry resubmits the same idempotency key back to the original task with exactly one board task); and the two darwin-only keychain tests skip with a recorded reason off-platform instead of failing (the class of failure the review's Linux container runs exposed). The VALIDATION G2 header and the AC-203/AC-207/AC-505 rows now state the delivered evidence. Verified by `make verify` on darwin/arm64. Changelog 1.0.17.

## E7-T5: Complete the CLI Inspection Contract

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Deliver the inspection surface the CLI contract promises.

### Deliverables

- `config show` implemented over the normalized configuration with redaction;
- the full intent lineage in `dispatches show`: decision, batch, and observation chain plus work receipts;
- `dispatches list` age, external-reference, and causal-ID filters with pagination;
- envelope `trace_id` propagation;
- the retry `--reason` classification fix and the error-model category correction for `*_not_found` codes.

### Requirements

`CLI-004`, `CLI-008`, `OPS-002`

### Dependencies

E7-T3 Completed.

### Acceptance

- every command named by CLI-004 exits without `command_not_implemented`;
- a dispatch's complete causal lineage is inspectable from the CLI alone;
- list filters and pagination behave as the CLI contract specifies.

### Evidence

Delivered as the CLI inspection completion (H-5, M-13 through M-16): `config show` prints the normalized, redacted configuration through the envelope (no `command_not_implemented` remains in the tree); `dispatches show` returns the complete causal chain - the intent, attempts, receipts, and transitions it always carried plus the creating decision with its reason codes, the retained batch, that batch's source observations, and the cooperative work receipts (`ports.IntentLineage` extended with tagged JSON fields, `LoadIntentLineage` joining decision to batch to observations and work receipts); `dispatches list` gains the `--age` (positive Go duration), `--external-ref`, and `--causal` (dispatch or decision prefix) filters with `--offset` pagination echoed in the envelope; the parsed `--trace-id` reaches every success envelope (M-14); a dead-lettered `dispatches retry` without `--reason` is classified as a usage defect instead of an internal one (M-15); and the error-model category table corrects the exit-4 `*_not_found` codes to `input_rejected` (M-16; the dispatches, receipts, and quarantine sites emit the corrected category, while the work-command `dispatch_not_found` sites are deferred with the round-2 review residuals). Proven by `internal/cli/e7t5_test.go` (`TestConfigShowNormalized`, `TestDispatchesShowFullLineage`, `TestDispatchesListFiltersAndPagination`, `TestTraceIDReachesEnvelope`) plus the updated G1 not-implemented pin. The round-1 Mulgae remediations are folded in (run r_01a02bd2-6a77, reports_only): the four exit-4 `*_not_found` codes now emit `input_rejected` in the implementation as well as the table (with `config_route_not_found` kept at `configuration`/exit 3, restoring the category-to-exit 1:1 invariant), the causal-chain join surfaces every error instead of silently truncating, the retry pre-check reads the snapshot and surfaces load errors, the causal-prefix LIKE wildcards are escaped, and `config show` prints the computed route revisions the CLI contract promises, with the retry-classification and redaction/revisions tests added. Verified by `make verify` on darwin/arm64. Changelog 1.0.18.

## E7-T6: Close Write Gates, Audit Rows, and Decision Records

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Make the automatic write gates and the persisted audit and decision records match their specifications.

### Deliverables

- drain and rerun respect route activation state;
- `routes.<id>.enabled` read and enforced in the two-key gate;
- every route transition appends its `state_transitions` audit row with timestamp and context;
- `merge_pending` persisted on the decision record;
- `reprocess` evaluating current policy instead of hardcoding the disposition.

### Requirements

`CLI-007`, `DUR-011`, `POL-006`, `OPS-004`, `PTH-008`

### Dependencies

E7-T2 Completed.

### Acceptance

- a disabled route never receives automatic submissions through any path;
- route transitions are fully reconstructable from the audit table alone;
- merged bursts record `merge_pending` and reprocessed records carry evaluated dispositions.

### Evidence

Delivered as the write-gate and audit closures (M-1, M-2): the shared `slotAdmissible` admission rule now requires the store activation state to be enabled - drain never submits on a disabled route - and the YAML `routes.<id>.enabled` key participates on both ends of the two-key gate: `route enable` refuses while the configuration key is off, and both the drain and the dispatch submit phase refuse automatic submission while it is off (the AC-504 clean-host flow now flips the key as part of the reviewed enable). (M-3): `applyRouteTransition` appends the route-entity audit row inside the same transaction for every transition (deduplicated by transition id), `CommitLineage` records the intent's `:created` arrival row, and the previously manual duplicate resolution row was removed. (M-18): `CommitMergePending` persists `merge_pending` on the durable decision instead of the planner's optimistic `dispatch` disposition. (M-19): `dispatches reprocess` reclassifies every retained path through the active pattern engine and records the evaluated disposition, classification, and reason codes instead of the hardcoded `dispatch`/`normal`. Proven by `internal/cli/e7t6_test.go` (TestDisabledRouteNeverAutoSubmits, TestRouteTransitionsFullyAudited, TestMergePendingPersistedOnDecision) plus the updated audit determinism and runtime fixtures. The round-1 Mulgae remediations are folded in (run r_01a02c13-43e2, reports_only): reprocess delegates to the planner itself (the retained batch is reclassified and evaluated through `dispatch.Evaluate`, so the recorded disposition, classification, and reasons match the active policy for the retained evidence, with classification errors surfaced and unique per-invocation decision ids; the overflow and fresh-instance signals cannot fire on retained evidence, and the active-dispatch merge precedence belongs to the arrival path), `--no-submit` no longer emits the disabled-route warning, `route enable` distinguishes an undefined route from a disabled key, the transition auditer is a store method reusing AppendTransition, the cli-spec drain section states the gate, and the store-activation and reprocess-parity tests were added. Verified by `make verify` on darwin/arm64. Changelog 1.0.19.

## E7-T7: Migration Lock, WAL Classification, and Operator Exits

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Give concurrent first use and wedged route states safe, documented outcomes.

### Deliverables

- an application-level migration lock so concurrent first opens serialize instead of failing;
- WAL initialization classified as retryable busy rather than a fatal open failure;
- the four unwired stale-route edges with production writers, an operator exit for stale ACTIVE routes, and honest doctor remediation;
- a dead-letter closure path (discard or supersede generation) included in terminal-state cleanup.

### Requirements

`DUR-004`, `DUR-009`, `OPS-006`, `OPS-008`, `OPS-009`

### Dependencies

E7-T6 Completed.

### Acceptance

- six concurrent first invocations all succeed or queue without spurious failures;
- a stale ACTIVE route has a documented, working operator exit;
- dead-lettered work can be closed and pruned.

### Evidence

Delivered as the storage durability closures (M-4): the migration pass serializes through an exclusive lock file beside the database with a bounded wait window and stale-holder theft, and the WAL journal switch retries inside a bounded window under concurrent first-open congestion - proven by `TestConcurrentFirstOpensSerialize` (six concurrent first opens all observe the complete ledger, the lock file releases, and ten repeat runs stay green). (M-5): a WAL-switch busy that outlives the retry window surfaces as the retryable `sqlite_busy` (transient_local, exit 10) instead of a fatal `sqlite_open_failed`. (M-6): the declared `execution_evidence_stale` edge gained its production writer - `route stale --reason` moves an ACTIVE route to UNCERTAIN with an audited transition, and the uncertain route then resolves through the documented reconciliation exit (`TestRouteStaleOperatorExit`). (M-7): `dispatches discard --reason` closes a dead-lettered intent as superseded through the declared edge, releases the route slot, and keeps the audit history - the closed (superseded) form is what becomes retention-resolvable, while an open dead letter stays retained (`TestDeadLetterDiscardClosesLineage`, `TestOpenDeadLettersStayRetained`). The round-1 Mulgae remediations are folded in (run r_01a02c51-b6e7, reports_only): the lock steal is an atomic rename with an owned, pid-checked release and a wait window that covers the staleness bound, a lock wait that outlives the window surfaces as the retryable busy classification instead of a fatal open, the route stale operator reason is audited on the transition, the cli-spec documents `route stale` and `dispatches discard`, and the lock wait/steal and retention posture gained tests. Verified by `make verify` on darwin/arm64. Changelog 1.0.20.

## E7-T8: Enforce Payload Versioning and Complete Hermes Rendering

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Make stored payload versions load-bearing and the Hermes task complete per HER-006.

### Deliverables

- version validation on read with fail-closed unknown-major handling, non-null receipt payload versions, the webhook contract-version gate, and rerun/follow-up re-stamp prevention;
- acceptance criteria rendered into Kanban tasks with the golden file updated and the manifest-existence sentence added;
- schema-conformant persisted observation flags and `source.position` persistence.

### Requirements

`DAT-009`, `HER-006`, `SRC-002`

### Dependencies

E7-T7 Completed.

### Acceptance

- an unknown payload major version fails closed on read;
- a rendered Kanban task contains every HER-006 required element;
- persisted observations validate against their JSON schema.

### Evidence

Delivered as the data-contract closures (M-8): the acceptance receipt always carries the payload version of the contract actually submitted (`SubmitResult.PayloadVersion` with the task-request default; the insert never writes null - `TestReceiptPayloadVersionNeverNull`), the snapshot and lineage reads fail closed on any stored request version this build does not speak (`TestUnknownStoredRequestVersionFailsClosed`; the earlier family-only major check would have accepted a same-family newer major and now requires the exact contract version), and the webhook sink refuses a request naming any contract other than this build's exact task-request version before any transport work (rerun and follow-up rebuilds construct a fresh request under the current contract, so no stale payload restamping path exists by construction). (M-9): the Kanban renderer appends the HER-006 basis-four manifest-existence sentence and renders the acceptance criteria block, with the golden regenerated to pin both. (M-11): `SourceFlags` carries schema-conformant snake_case JSON tags and the observation persists its verbatim source position object (watchman since/clock) through migration v5 (`observation-position`, schema range now 1-5 with the version metadata and drift tests updated). The round-1 Mulgae remediations are folded in (run r_01a02c97-6580, reports_only): the observation converter persists the position it previously dropped (the column was always NULL), the webhook gate refuses any contract except this build's exact task-request version (the earlier family-only check passed a same-family `/v9`), and `has_relative` left the persisted flags (the published schema carries only overflow, fresh_instance, and relative_root), with the schema-conformance regression test added. Verified by `make verify` on darwin/arm64. Changelog 1.0.21.

## E7-T9: Remediate the Operations and Security Medium Batch

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close the remaining operational and defensive findings that gate safe unattended operation.

### Deliverables

- `file_scope: markdown` enforced at ingest regardless of include patterns;
- prune preservation of begun work receipts;
- error-class corrections for disabled-route rejections;
- required-capability validation at enable and offline validate;
- the doctor target probe constructed with real sink limits;
- unclassifiable filenames isolated instead of aborting the reconciliation;
- `file:` secret permission checks with doctor warnings;
- value-pattern log redaction;
- load-robust probe timeouts;
- `init --state-dir` persisted into the written configuration.

### Requirements

`SCP-003`, `SCP-004`, `PTH-002`, `SRC-005`, `SEC-003`, `SEC-006`, `SEC-007`, `HER-005`, `FBK-003`, `OPS-003`

### Dependencies

E7-T8 Completed.

### Acceptance

- binary and non-Markdown files are never dispatched under the default scope;
- an in-flight work receipt survives pruning;
- world-readable secret files and unredacted credential values are rejected or redacted;
- a fresh `init --state-dir` is honored by every later invocation.

### Evidence

Delivered as the operations and security batch (M-10): `BuildBatch` enforces the resource `file_scope` above the pattern engine - under `markdown`, a non-Markdown path an include pattern admitted drops before hashing whatever the include patterns say (`ingest.Options.FileScope` wired from the pipeline; `TestMarkdownScopeBeatsIncludePatterns`). (M-20): the prune's work-receipt delete excludes `begun` rows, so the in-flight attribution anchor survives any age (`TestPrunePreservesBegunReceipts`). (M-21): a route-guard refusal during reconciliation (the disabled activation state) classifies as a state conflict (`transition_invalid` posture) instead of storage. (M-22): `route enable` validates the required capabilities against the configured report when the local report is readable (an unreadable report path defers to the submit path's fail-closed gate). (M-23): the doctor target probe constructs the sink with the real manifest bound instead of zero. (M-24): an unclassifiable filename is isolated as an exists-but-unverifiable fact instead of aborting the full reconciliation (mirroring the adjacent unresolvable branch). (M-25): a `file:` secret reference with permissive group/other bits fails closed naming the mode. (M-26): redaction masks credential-shaped fragments (bearer tokens, query-string credential parameters) inside logged values. (M-28): the stub-probe test fixtures' fixed timeouts rose to 30s (load-flake posture; the configured probe limits are unchanged). (M-29): an explicitly chosen `init --state-dir` persists into the written configuration while an environment-derived default stays dynamic (`TestInitStateDirPersists`). The round-1 Mulgae remediations are folded in (run r_01a02cdc-e8dc, reports_only): the file-scope check moved after the protected/immutable classification (a protected non-Markdown path is reported, not silently dropped) and is case-insensitive, an environment-derived state directory stays dynamic instead of being frozen into the config, the credential redaction masks only the credential fragment (never the whole containing value) with the common credential parameter names included, the configuration spec documents the owner-only `file:` gate, and the M-21/M-22 tests landed (`TestDisabledReconcileRefusalClassified`, `TestRouteEnableValidatesCapabilities`); the round-2 review's bearer-body redaction gap was fixed with the credential-fragment and owner-only-file tests added (`TestCredentialFragmentRedaction`, `TestOwnerOnlySecretFileGate`) and the reconcile scope made case-insensitive to match the batch path. Verified by `make verify` on darwin/arm64. Changelog 1.0.22.

## E7-T10: Licensing, Layout, and Documentation Consistency

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Bring the package's licensing, layout claims, and process documents back in line with reality.

### Deliverables

- a LICENSE file and a dependency-license review record;
- the release checklist actually operated for v0.1.1;
- repository-layout deviations explained or cleaned (empty packages, placeholder doc.go files, the untracked migrations directory);
- testing-strategy refresh, skip-reason annotations, task-execution-rules record maintenance, E6-T3 hardening residual dispositions, the known-deferred ABA note in the changelog, and stray roadmap artifacts fixed.

### Requirements

`SCP-002`, `TST-009`

### Dependencies

E7-T9 Completed.

### Acceptance

- the repository carries a license and a recorded dependency-license review;
- every documented layout claim is true or explained;
- the release checklist for v0.1.1 is complete and checked off.

### Evidence

Delivered as the documentation-consistency restoration (M-27): the repository root carries an MIT `LICENSE` and `docs/implementation-tips/dependency-licenses.md` records the review of all 39 modules in the build graph (direct and indirect: MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0, and MPL-2.0 tool-chain only, all compatible). (M-31): the release checklist is retitled for v0.1.1 and operated - all 50 items checked with honest narrowing notes (the Linux leg references the recorded SCP-008 exception; the reproducibility double-build points at E7-T12); `repository-layout.md` gains the Layout Deviations section explaining the six named departures (the new LICENSE, the empty `migrations/`, the `test/e2e` and `test/helpers` placeholders, the per-domain ports files, the reserved empty packages, and the retained placeholder `doc.go` anchors); `task-execution-rules.md` §5 records that the owner/timestamp fields live in the execution environment rather than a per-task YAML file; the CHANGELOG 1.0.7 entry carries the `pending_reconcile` ABA known-deferred note; the six E6-T3 hardening residuals carry individual dispositions in the E6-T3 evidence; CONTRIBUTING and README state the full `make verify` composition; the two stale `docs schemas unavailable` skips became fatal broken-checkout guards and the stale E4-T4 `work begin` skip was removed; and the duplicate E6-T4 evidence heading was unified. Verified by `make verify` on darwin/arm64. Changelog 1.0.23.

## E7-T11: Disposition the Low and Informational Findings

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Leave no review finding undispositioned: fix the actionable Low items and record the accepted observations as documented reduced guarantees.

### Deliverables

- fixes for the actionable Low findings across identity encoding, JSON canonicalization edges, reason-code documentation, lease TTL documentation, capability naming, test assertions, and CLI flag surfaces;
- disposition records for accepted observations in the decision log or roadmap notes.

### Requirements

`DAT-002`, `DAT-005`, `DAT-007`, `OPS-001`, `CLI-006`, `SEC-004`, `WHK-003`

### Dependencies

E7-T10 Completed.

### Acceptance

- every Low/Info finding in the review's inventory maps to a fix or a recorded disposition;
- no new regression is introduced by the Low-batch fixes.

### Evidence

Delivered as D-018: one consolidated disposition record in the decision log mapping every Low and Info finding from the review's inventory, T1 through T8 and code hygiene to its disposition. The round-1 Mulgae review (run r_01a02d3c-620d, reports_only) caught three overstated FIXED claims and a materially incomplete inventory, all remediated: the three claims were made true in the tree during this task (the SKILL.md header now states the validated E5-T2 status, the AC-106 test additionally asserts the rejected escape path leaves no observation record, and the one-minute lease TTL is documented in D-018 itself), the rerun-key claim was restated to what the tests actually pin, and the inventory gained the nine missing T2-through-T8 items (the overflow generation_action label, the open-path placement classification, the ABA note, the webhook unreachable branches, the anti-accident revision print, the FileRegular enumeration report, the doctor envelope shape, the bounded-context assembly, and the prune policy-revision reconstruction); the round-2 review's further gaps (the vacuous AC-106 record assertion and seven still-unmapped clauses) were then closed in the same task: the assertion now always executes against the real store, and the inventory gained the final seven entries (the AC-106 content leg, the fixture-layout convention, the strategy goldens, the pragma tests, the json-only doc gap, the diagnostic bounds, and the trigger-drift detection). The remaining ACCEPTED rationales stand as documented reduced guarantees. Verified by `make verify` on darwin/arm64. Changelog 1.0.24.

## E7-T12: Re-Verify MUST Closure and Prepare v0.1.1

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Prove the remediated state end to end and produce the v0.1.1 patch release.

### Deliverables

- full macOS `make verify` including the race suite;
- gates G1-G5 re-run on the real Hermes and Watchman;
- the MUST-closure matrix recorded in VALIDATION.md (every one of the 14 GAP requirements PASS or explicitly excepted);
- a full VALIDATION.md refresh and changelog entry;
- `make release VERSION=v0.1.1` with byte-reproducibility verified across two consecutive builds;
- release notes disclosing the Linux verification exception.

### Requirements

All `BND-*` through `WHK-*` requirements.

### Dependencies

E7-T11 Completed.

### Acceptance

- every MUST requirement is PASS or carries an explicit recorded exception (SCP-008);
- AC-102, AC-203, AC-207, and AC-505 evidence is real and reproducible;
- v0.1.1 artifacts are byte-reproducible and the release notes state the unverified Linux platform;
- roadmap tasks E7-T1 through E7-T12 are Completed.

### Evidence

Delivered as the closeout verification: `make verify` passed on darwin/arm64 including the race suite (2026-08-23); the G1-G5 gate suites re-ran green on the then-baseline real Hermes and Watchman 2026.07.27.00; the MUST-closure matrix is recorded in `docs/VALIDATION.md` (thirteen of the fourteen GAP requirements PASS through the E7 remediation; SCP-008 carries the explicit D-017 exception); `make release VERSION=v0.1.1` ran twice with byte-identical `dist/SHA256SUMS` (darwin/arm64 `170b8984...`, linux-amd64 `c21ce0b5...`); and `docs/RELEASE-NOTES-v0.1.1.md` discloses the Linux verification exception beside the delivered remediation. Changelog 1.0.25.

**Epic closeout:** all twelve E7 tasks are Completed; every Blocker, High, Medium, and Low/Info finding of the 2026-08-22 review is fixed or dispositioned (D-018); the v0.1 sequence stands superseded by v0.1.1. The deferred hardening residuals recorded across the member tasks are reconciled by the epic validation audit.

---

# E8: v0.1.2 Compliance Remediation

**Epic status:** Completed  
**Purpose:** Remediate every finding of the 2026-08-23 MVP compliance review of v0.1.1 (D-020): the Blocker in the follow-up product loop, the two FAIL requirements (CON-003, POL-007), the three FAIL acceptance criteria (AC-502, AC-503, AC-506), the ten High findings, the mapped Medium findings, and the documentation-truth cluster; close the SCP-008 Linux-verification exception on the review's evidence; and release v0.1.2.  
**Gate:** MUST closure — CON-003 and POL-007 PASS, AC-502/AC-503/AC-506 pass with real evidence, and every PARTIAL clause named by the review is met or carries an explicit recorded exception — with gates G1-G5 re-run and v0.1.2 released from the tagged tree.

## E8-T1: Close the Follow-Up Loop State-Machine Defects

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close the review's Blocker and follow-up-chain High findings (B-1, H-1, M-10) so a vault change arriving between completion and follow-up submission cannot wedge the route and the follow-up chain is bounded by construction.

### Deliverables

- a new `FOLLOWUP_READY → ACTIVE_DIRTY` route edge (reason `followup_accepted_dirty`) with `ActivateFollowup` activating into ACTIVE_DIRTY when `dirty_generation > 0`, and the SOT persistence-and-state-machines §6 diagram updated to match;
- follow-up IDs derived from route and generation (no cumulative `-followup-N` suffix growth) with the parent lineage preserved explicitly, through a ledgered schema migration with pre-migration backups;
- the active-generation boundary keyed on collapsed batch identity or a monotonic generation watermark instead of a second-truncated timestamp, so a same-second completion batch is never re-imported into the next generation;
- receipt-manifest matching intersected with the route's effective scope (excluded or immaterial receipt paths no longer count as `receipt_extra_path`) and a per-route consecutive-follow-up budget that moves the route to UNCERTAIN exactly like the failure budget;
- the follow-up manifest reduced to the unresolved paths with the content fingerprint recomputed (M-10);
- the `work` command error mapping for `*state.TransitionError` (`transition_invalid`, exit 14) so route-guard rejections stop surfacing as `internal_unclassified`;
- regression tests: a vault edit between completion and follow-up submission, a chain of at least 25 dirty generations, and a same-second completion with a perfect receipt — replacing the generation-boundary sleeps among the seven `time.Sleep(1100ms)` dodges (three removed; the four that pin orthogonal second-precision boundaries — attribution begin windows, the failure-budget streak window, the stale-age crossing — are retained with documented reasons).

### Requirements

`CON-003`, `CON-004`, `CON-005`, `FBK-002`, `FBK-005`, `FBK-008`, `DAT-005`, `CLI-008`

### Dependencies

D-020 recorded; E7 complete.

### Acceptance

- the review's B-1 CLI reproduction (dispatch → drain → `work begin` → vault edit → `work complete` → one more vault edit → drain → `work begin` → `work complete`) exits 0 with the route progressing;
- 25 consecutive dirty completions keep the dispatch ID bounded and the route live;
- a same-second completion with a perfect receipt suppresses exactly, with no sleep in any test;
- feedback-loop-and-reconciliation §5, domain-model, and the SOT §6 diagram describe the delivered machine.

### Evidence

Delivered as the follow-up loop state-machine closure: the route table gains the FOLLOWUP_READY -> ACTIVE_DIRTY edge (reason `followup_accepted_dirty`, guarded on a dirty generation) so a vault edit arriving between completion and follow-up submission activates with its retained dirty generation instead of wedging `work complete` (B-1 — pinned end to end by `TestE8T1EditBetweenCompletionAndFollowupSubmission`); follow-up dispatch IDs are fresh UUIDv7 values with the parent recorded in the decision lineage and the creation audit row, so chained generations never grow cumulative `-followup-N` suffixes toward the 256-byte bound (H-1.2); migration v6 adds the `change_batches.batch_seq` watermark (SaveBatch assigns MAX+1 under the insert transaction, unique-indexed) and `dispatch_intents.base_batch_seq`, and `LoadActiveGenerationChanges` keys the generation window on the watermark so a batch merged in the same second as a completion is never re-imported into the next generation (H-1.3 — `TestE8T1SameSecondCompletionDoesNotReimport` without a single sleep, and `TestMigrationV6BackfillsBatchSequence` pins the backfill's deterministic ordering and both base resolutions; one recorded residual: the backfill's fallback arm keys on second-precision created_at for pre-migration decision-less intents, a one-time degraded first follow-up documented in the migration). The receipt matcher intersects the manifest with the route's effective scope (the single ingest encoding `ingest.OutsideScopePredicate`, classify errors staying material) and with the durable path facts, so out-of-scope paths and byte-identical rewrites record `receipt_immaterial_path` and never block exact suppression, while a consecutive-follow-up budget (`state.MaxConsecutiveFollowups` = 50) moves an over-budget completion — including the failure path — to UNCERTAIN exactly like failure-budget exhaustion, with the store as the single decision point (H-1.1 — `TestE8T1ReceiptScopeAndIdenticalRewriteSuppress`, `TestE8T1TwentyFiveGenerationChain` driving 25 generations with bounded IDs and a live route, and `TestCompleteActiveFollowupBudgetExhausted` plus its `OnFailure` variant). Follow-up manifests carry the unresolved paths of the dirty generation with the content fingerprint recomputed over them (M-10, asserted on the created intent in the B-1 test); `work fail` carries the generation fence like `work complete`, the work commands map `*state.TransitionError` to `transition_invalid`/14 (the H-9 leg, pinned in `TestWorkReceiptErrClassification`), and an IDLE empty-slot merge records a pending reconciliation instead of a wedge-prone dirty count (round-1 F001 — `TestCommitMergePendingIdleEmptySlotRecordsPendingReconcile`). The SOT updates: the persistence section 6 diagram carries both new edges, the domain-model dirty-counter invariant is corrected, and feedback-loop sections 5 and 7 carry the immaterial outcome vocabulary and the follow-up budget; the schema range and adapter label advance to 1-6. Three generation-boundary sleeps were removed (g4, e5t3, e5t4; the e5t4 intent selection made deterministic by identity with a rowid tiebreak); the four remaining 1100 ms sleeps pin orthogonal second-precision boundaries with documented reasons. Verified by `make verify` on darwin/arm64 (all checks green). Reviewed through two full-target Mulgae rounds (`r_01a02f4d-adee-73b8-aa11-3219d340ce67`, remediation-eligible: ten findings — the IDLE-merge wedge, the budget dual-encoding, the fail-path fence, the dual scope encoding with fail-open classify errors, the silent path-fact degradation, the documentation contradictions, and five test gaps — all verified valid and remediated in-tree; `r_01a02f5f-1bd6-754c-a1ca-59724e2b8ad2`, hardening-deferral-eligible: ci pass, coverage complete, publication committed, seven findings — six valid test-coverage observations deferred to epic hardening with the exact run and finding IDs recorded in the commit trailers, and one invalid premise: the review file at the repository root is untracked by design under D-019/D-020, never committed). Changelog 1.0.28.

## E8-T2: Wire Recovery Into Every Submit Path and Repair the Operator Exit Codes

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close H-6 (expired-lease recovery reachable only through a command nothing schedules), the H-9 exit-40 cluster, and the delivery Mediums M-1 through M-5, so committed work heals without a manual drain and operator refusals carry their documented exit codes.

### Deliverables

- route-scoped `Runtime.Recover` plus unknown reconciliation at the head of `dispatch` and `reconcile --submit` (or an equivalently scheduled drain unit), with the false E7-T2 recovery claims in this roadmap corrected;
- `LeaseTTL` derived from the configured `submit_timeout` so a live submitter's lease can never be stolen (M-1), documented;
- budget-exhausted `retry_wait` given a real operator exit — `dispatches retry` resets the backoff budget explicitly in an audited transaction or dead-letters, and `Drain` stops skipping it silently (M-2, L-1);
- the declared `rejected → dead_lettered` edge applied after a definite rejection so a rejected dispatch cannot hold the route slot forever, with a runbook entry (M-3);
- expected operator refusals wrapped as `ports.ErrStateNotEligible`, and CLI arms mapping `*state.TransitionError` to `transition_invalid`/14 and `ports.StoreError` to 20/10 across the dispatch and work command groups (H-9);
- migration-lock staleness handled by a heartbeat or lock refresh instead of a once-written mtime (M-4);
- `exec.Start` failures (including argv-length) classified as definite not-submitted instead of `unknown` (M-5);
- the test-only `AcquireLease`/`TransitionIntent` exports removed (L-2) and the `dispatches` usage string naming `discard` (L-22).

### Requirements

`DUR-005`, `DUR-007`, `DUR-009`, `DUR-010`, `DUR-012`, `OPS-005`, `CLI-008`

### Dependencies

E8-T1 Completed.

### Acceptance

- a process killed mid-submit heals on the next Watchman-triggered dispatch or scheduled reconcile without a manual `dispatches drain`;
- `dispatches retry` and the rerun refusals exit 14 with their registered codes, never 40;
- the attempt lease outlives any configured submit timeout;
- the budget-exhausted and rejected states each carry a documented operator exit in the runbook.

### Evidence

Delivered as the recovery and operator-exit repairs: the trigger path sweeps expired submitting leases at the head of the durable arrival (before the coordinator evaluates the burst, so a wedged submitter heals on the next Watchman trigger and the freed route can accept work) with the healed unknowns reconciled behind it and failures surfaced on stderr, the scheduled `reconcile --submit` path runs the same sweep before any submission with its ungated-maintenance posture documented at the site, and the drain keeps its E7 sweep (H-6 — `TestE8T2TriggerRecoversExpiredSubmitting` pins the lease-expired recovery transition, `TestE8T2ScheduledReconcileRecoversExpiredSubmitting` pins the scheduled site; the E7-T2 roadmap claims about recovery at every submit entry point are now true). `leaseTTLFor` derives the attempt lease from the configured `submit_timeout` plus a 30 s margin at the dispatch, drain, and reconcile runtime sites, keeping the shipped one-minute default, documented in configuration-spec section 5 (M-1, superseding the D-018 one-minute record). `MakeRetryDue` makes a retry_wait dispatch due and resets its attempt budget in one audited `explicit_retry_reset` transaction — the operator exit for a budget-exhausted wait that the drain previously skipped forever (M-2/L-1, pinned by `TestDrainStopsAtLimit` and the audit-row assertion). `DeadLetterRejected` applies the declared rejected-to-dead_lettered edge after a definite rejection so a refused dispatch cannot hold the route slot with no exit; the closure is the existing discard/rerun surface (M-3, guard branches pinned by `TestE8T2DeadLetterRejectedGuards`; `TestG2AC206` asserts the dead-letter shape with the record, attempts, and receipts inspectable). The operator refusals wrap `ErrStateNotEligible`/`ErrReasonRequired` and `intentErr` carries the TransitionError-to-14 and StoreError-to-20 arms so documented refusals and storage failures never exit 40 (H-9, pinned by `TestIntentErrClassification`); the migration lock gains a heartbeat that refreshes a live holder's mtime so a migration slower than the staleness bound is never stolen (M-4, `TestE8T2MigrationLockHeartbeatRefreshes`); fork/exec failures classify definite not-submitted, never an unknown dead-letter of provably unsubmitted work (M-5, `TestE8T2ExecStartFailureIsDefiniteNotSubmitted`); the test-only `AcquireLease`/`TransitionIntent` exports are deleted with their tests rewritten over the guarded flows (L-2); the `dispatches` usage string and the cli-spec section 2 tree name `discard` (L-22). Runbook section 11 documents the four delivery-failure operator exits (expired submitting, definite rejection, budget-exhausted retry_wait, over-budget follow-up chains). Verified by `make verify` on darwin/arm64 (all checks green). Reviewed through two full-target Mulgae rounds (`r_01a02f88-973d-75af-9f08-a0ee62d1dd55`, remediation-eligible: ten findings — the heartbeat testability, the intentErr arms, the scheduled-site regression, the lease documentation, the cli-spec tree, the dead-letter guards, the audit-row assertion, the trigger-test precision, the ungated-maintenance documentation, and the trigger-path reconciliation warnings — all remediated in-tree; `r_01a02f9c-4722-714f-9cb6-2976227f5c64`, hardening-deferral-eligible: ci pass, coverage complete, publication committed, five valid low coverage/doc findings deferred to epic hardening with the exact run and finding IDs recorded in the commit trailers). Changelog 1.0.29.

## E8-T3: Make the Route Revision and Production Gate Behavior-Sensitive

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close POL-007/H-2 and H-7 so behavior-affecting configuration changes cannot pass unnoticed and the production enable gate enforces the real adapter capabilities.

### Deliverables

- the revision projection extended with the referenced resource's canonicalized `root`, `file_scope`, and `git.mode`, the global `limits` block, and the target's `type`, `board`, and `endpoint`, with configuration-spec §13 amended to match;
- the staleness comparison extended with the resource root, and the acknowledged revision re-checked at submit (`slotAdmissible`, `Drain`, and `SubmitOnce` gate `acknowledged_revision` against the computed revision, `transition_invalid`/14 on mismatch) so a behavior-sensitive change pauses the route until re-acknowledged;
- `route enable` routed through `probeWithVersion` (report freshness enforced at enable; an unreadable report exits 3) with `durable_acceptance` and `submit_idempotency_key` as unconditional preconditions for a `hermes-kanban` production route;
- the capability report's `lookup_by_idempotency_key` corrected to `false` (the adapter never supports it), the D-018 contrary record corrected, and the dedup-recreate prose in hermes-integration §8 and the sink comments fixed;
- `resource_mutex` consulted before `--mutex-key` is sent (M-6) and `config validate` running the §12 checks that need no probe by default (M-18, probe-gated part);
- `TestRouteRevisionDeterministicAndSensitive` widened to the new fields.

### Requirements

`POL-007`, `POL-008`, `SEC-010`, `HER-004`, `HER-005`, `HER-009`, `CON-006`, `TST-008`

### Dependencies

E8-T2 Completed.

### Acceptance

- repointing `resources.<id>.root` or changing the target `board` changes the computed revision;
- enabling at revision A and then editing the include patterns leaves the route paused at the next submit until explicitly re-acknowledged;
- an unreadable or stale capability report fails `route enable`;
- two distinct vaults with identical relative paths can never share an idempotency key.

### Evidence

Delivered as the revision and production-gate closure: the route revision projection now covers the referenced resource's root, file scope, and git mode, the global limits block, and the target's type, board, and endpoint (configuration-spec section 13 amended) — repointing a vault or moving a board changes the revision and with it the idempotency key, so distinct vaults with identical relative paths can never collide (`TestE8T3RevisionCoversResourceAndTargetShape` pins every mutation; H-2/POL-007). The route snapshot carries the acknowledged revision and every submit path refuses with the typed stale-revision conflict when the intent's planned revision diverges — a behavior-sensitive change now genuinely pauses the route until `route enable` re-acknowledges, failing closed on empty acknowledgements and mapping to exit 14 (`TestE8T3BehaviorChangePausesUntilReacknowledged`; the e7t3 stale-rebuild and target-repoint tests updated to the pause-then-re-acknowledge semantics). `route enable` runs the live version-gated probe: an unreadable or stale report and an unsupported version refuse at exit 3, `durable_acceptance` and `submit_idempotency_key` are unconditional preconditions, target unavailability is a documented warning that still validates a present report (H-7 — `TestE8T3EnableGateRefusesStaleReportAndWeakGuarantees` reproduces both review defects as refusals). The capability report records `lookup_by_idempotency_key` false with the evidence entry re-graded and the adapter, example, configuration-spec, and hermes-integration prose aligned to the honest read-only semantics (H-7/HER-009; the D-018 record already stated the honest false — the file now matches it). `resource_mutex` gates `--mutex-key` in the renderer (M-6, `TestE8T3MutexKeyOnlyWhenSupported`), and `config validate` runs the probe-free section 12 target checks by default — a present-but-invalid report fails, a not-yet-placed report warns (M-18's probe-gated half). Fixtures acknowledge computed revisions and place reports before enabling (the corrected installation order). Verified by `make verify` on darwin/arm64 (all checks green). Reviewed through two full-target Mulgae rounds (`r_01a02fec-90ee-75b3-82bf-06e07c8c8a11`, remediation-eligible: the dispatch-path pause exit, the unavailable-branch report validation, the empty-acknowledgement hardening, and the missing-report warnings remediated in-tree, with the transport-field projection and silent mutex suppression declared for deferral; `r_01a03000-3488-7027-bb98-0f48b23f7807`, hardening-deferral-eligible: ci pass, coverage complete, publication committed, five valid documentation findings — the installation section 3 order, cli-spec section 3 enable wording, configuration-spec section 12 wording, the sink-contract example boolean, and the dispatch-plan example requirement — deferred to the E8-T6 documentation pass with the exact run and finding IDs recorded in the commit trailers). Changelog 1.0.30.

## E8-T4: Preserve Unresolved Lineage and Make Doctor Trustworthy

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close AC-503/H-3 and AC-502/H-4 plus the diagnostics Mediums, so retention never deletes live work's lineage and the doctor verdict is trustworthy for every AC-502 condition.

### Deliverables

- prune: `accepted` removed from the resolved set, and the active-slot/unresolved-lineage guard applied to the attempt, receipt, and work-receipt deletes; the AC-503 test extended to an accepted dispatch holding the route slot (H-3);
- doctor: the AC-502 conditions raised to SeverityError (or an explicit condition set forcing a nonzero exit), the offline kanban branch calling `ValidateRequired`, the resource-root probe using a real access check (`R_OK|X_OK` or open plus readdir), and no fabricated Watchman/target/store finding when the config fails to load (H-4);
- `state.db` (and its `-wal`/`-shm`) created 0600 with doctor checks for the DB and config file modes, correcting the D-019 M-12 record (M-19);
- log redaction widened — credential patterns for the missing header/query forms, map-value sanitization, the message path, and `config show` query-string masking (M-20);
- `reconcile` on a non-enabled route exiting 14 per cli-spec §9 in every route state (M-12); `route stale` enforcing the `active_stale_after` precondition (M-23); OPS-006 "startup uncertainty" mapped to a reason and runbook procedure or explicitly excepted (M-22); `prune --dry-run --yes` refusing the pair as `vacuum` does (L-9).

### Requirements

`OPS-003`, `OPS-004`, `OPS-005`, `OPS-006`, `SEC-007`, `SEC-008`, `CLI-006`

### Dependencies

E8-T3 Completed.

### Acceptance

- `maintenance prune --yes` against an active accepted dispatch holding the route slot deletes none of its attempts or receipts;
- `doctor` exits nonzero for each of AC-502's five conditions;
- a chmod-000 resource root is reported as an error, not passed;
- `make verify` is green including the extended tests.

### Evidence

Delivered as the retention and diagnostics closure: prune drops `accepted` from the resolved terminal set — an accepted dispatch is live, unreceipted work — and the attempt, receipt, and work-receipt deletes carry the active-slot predicate, mirrored exactly in the dry-run plan; `TestG5AC503` seeds an active accepted dispatch and asserts its lineage survives while a genuinely completed seed prunes (H-3/AC-503). Doctor raises `watchman_unavailable` and `target_gate_failed` to error severity so each AC-502 condition forces exit 3, runs the offline capability gate on readable reports, resolves PATH-named executables through `exec.LookPath`, and probes resource roots with a real open/readdir access check — a chmod-000 root reports `resource_root_not_readable` (`TestE8T4DoctorReportsUnreadableRoot`) — and a configuration that fails to load never fabricates an unexamined Watchman finding (`TestE8T4DoctorNeverFabricatesWatchman`; H-4). `state.db` is created 0600 at first open (M-19, correcting the D-019 M-12 record); the credential redactor covers basic/token authorization headers, `client_secret`/`apikey`/`password`/`key` query and fragment parameters, and bare JWTs, pinned adversarially (M-20); reconcile on a non-enabled route fails closed with `transition_invalid`/14 in every route state (M-12, `TestReconcileRefusalOnDisabledRoute`); `route stale` enforces the `active_stale_after` precondition with a nanosecond comparison (M-23, both refusal and exit paths covered); the `startup` reconcile reason is implemented, documented in the runbook, and enumerated in every published list (M-22); and `prune --dry-run --yes` refuses the pair as vacuum does (L-9). Verified by `make verify` on darwin/arm64 (all checks green). Reviewed through two full-target Mulgae rounds (`r_01a0302f-8cb9-76e4-af71-d85d8c3d8486`, remediation-eligible: request-changes high — the root access probe claimed but not implemented — plus four low findings, all remediated; `r_01a03047-b9ee-7b7e-a8ac-dc15698aa2d2`, hardening-deferral-eligible: request-changes high — a flag inversion the round-1 remediation itself introduced — fixed in-tree immediately after the review with regression tests rather than deferred, a deviation from the defer rule recorded here and in the Podway evidence for the epic validation audit to re-verify; F002 (two more reason enumerations) also fixed; five low/info findings F003-F007 deferred to epic hardening with the exact run and finding IDs in the commit trailers). Changelog 1.0.31.

## E8-T5: Close the Input-Containment and Configuration-Validation Gaps

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close H-8 and the input/configuration Mediums, so every recorded path is containment-checked before it reaches a manifest and `config validate` catches the §12 classes without a probe.

### Deliverables

- containment resolution for every recorded path — plain deletes and create+delete entries with a prior path included (the review's H-8, with its corrected reach), `ErrEscape` mapped to `ErrUnsafePath`, and the StatContained default branch wrapped (exit 30, never 40);
- the dry-run surface reading path facts read-only or reporting the fact gap explicitly, so `unchanged_content` and `merge_pending` stop being overstated (M-7);
- recrawl uncertainty detected or a recorded exception replacing the unimplemented promise in watchman-integration (M-8);
- the managed trigger command pinning `--config` (and the documented output flag) so a custom configuration survives fire time (M-9);
- SemanticValidate: resource-root overlap, absolute `state_dir` and resource roots, map-key patterns, include/exclude/protected pattern safety at validate time, and a `max_hash_file_bytes` floor (M-18, semantic part);
- quarantine release recomputing the route and policy revisions for the replacement decision, with the protected-release semantics documented (M-13), and the overflow-over-protected precedence either creating the hold or documented as an override (M-14);
- the reconcile content fingerprint built from vault-relative paths with a pinning test (M-11);
- DAT-009 version checks on read and the CLI record/schema alignment for the four published record schemas (M-15/M-16/M-17, scope as feasible in this task), plus the real-NUL path fixture restored (L-4).

### Requirements

`PTH-002`, `PTH-008`, `SEC-001`, `SEC-003`, `SRC-005`, `SRC-007`, `SCP-006`, `DAT-004`, `DAT-005`, `DAT-009`

### Dependencies

E8-T4 Completed.

### Acceptance

- a crafted stdin delete for a symlink-escaping path is rejected or quarantined with `source_unsafe_path`/30 and never recorded as dispatchable;
- `config validate` catches the §12 classes without `--probe-targets`;
- the same vault content produces the same reconcile idempotency key from any mount point;
- AC-102 and AC-106 evidence matches the claimed strength again.

### Evidence

Delivered as the input-containment and configuration-validation closure: every recorded path now resolves containment — a pure delete resolves before it is recorded (the plain delete previously bypassed every containment check), and the create,delete branch's checking error wraps as `ErrUnsafePath` — so a crafted stdin delete for a symlink-escaping path rejects with `source_unsafe_path`/30 and no intent exists (`TestE8T5SymlinkEscapeDeleteRejected`; H-8/PTH-002). The plan and dry-run envelope states its no-database fact gap instead of overstating unchanged-content suppression (M-7); recrawl detection is resolved as a recorded exception in watchman-integration section 7 — the production trigger path never observes the query-surface warning, and recrawl aftermath reaches the product through the detected overflow/fresh-instance signals plus durable path-fact suppression (M-8); the managed trigger command pins `--config` and the documented `--output json` so a custom configuration survives fire time (M-9). SemanticValidate gains resource-root overlap (with a form-consistent clean/resolved comparison — the naive mixed-form comparison silently missed nesting on macOS `/var` symlinks, caught by the new test), absolute `state_dir` and resource roots, the map-key identifier grammar, and the `max_hash_file_bytes` floor (`TestE8T5SemanticValidationWidened`; M-18's semantic half). Quarantine release recomputes the route's current revision into the replacement decision instead of copying the quarantined decision's stale one (M-13); the overflow/fresh-instance precedence records the protected reason so the hold stays visible while the protected path never enters an automatic task (M-14); and the reconcile fingerprint drops the absolute vault root so the idempotency key is mount-point independent (M-11). Verified by `make verify` on darwin/arm64 (all checks green). Reviewed through one full-target Mulgae round (`r_01a03074-a3ee-7104-892c-5653915257d8`: ci pass, coverage complete, publication committed, zero findings — the stop-immediately clean condition). Changelog 1.0.32.

## E8-T6: Restore Documentation Truth and Release v0.1.2

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close H-5, the H-10 documentation-truth cluster, and the D-020 Linux record; produce the v0.1.2 release from the tagged tree.

### Deliverables

- every documentation-truth item of the review's §4: the VALIDATION rows (CON-003, CLI-004, the AC-402/406/409/502/503/506 gate rows, the DAT-009 rationale, the dry-run evidence surface, the statistics), the contradicted D-018 records (per the D-020 correction list), the roadmap E7-T2 recovery claims, the E2-T2/E7-T1 Linux wording, the E4-T5/E5-T4/E7-T10/E1-T3 evidence hygiene, the docs/README narrative and version, repository-layout, the installation §3 order, the contract wording (cli-spec §2/§3/§9, configuration-spec §7-§13, hermes-task-contract §7, error-model §2), the AC-107 text, and the architecture prose (domain-model §14, hermes-integration §8, watchman-integration, feedback-loop §5, observability §3/§5/§7), with L-5 and L-24 folded in;
- the D-020 Linux closure executed: SCP-008/AC-505 exception closed across the charter, acceptance criteria, VALIDATION, README, and release notes; the two root-sensitive permission tests hardened with root self-skips;
- the exact toolchain pinned by the go.mod `go` directive itself (`go 1.26.6`; a separate `toolchain` line normalizes away as redundant — L-12 resolved honestly) and the `adapter_versions` label/schema-range drift guarded;
- `make release VERSION=v0.1.2` built twice at HEAD with byte-identical `dist/SHA256SUMS`, the `v0.1.2` tag created, the AC-506 test rewritten to read the version from one source and assert `RELEASE-NOTES-<version>.md` plus one SHA256SUMS line per artifact;
- release notes for v0.1.2 disclosing the remediation and the Linux closure; the TST-008 gate remains disabled in shipped defaults until the operator re-enables it through the gate;
- the refreshed MUST-closure matrix in VALIDATION.md recording the v0.1.2 disposition of every requirement the review judged FAIL or PARTIAL.

### Requirements

All `BND-*` through `WHK-*` requirements.

### Dependencies

E8-T5 Completed.

### Acceptance

- no living document states what the code does not do (the review's §4 list closed item by item);
- the v0.1.2 artifacts are byte-reproducible, tagged, and validated by the rewritten AC-506 test;
- every MUST requirement is PASS or explicitly excepted in the refreshed matrix;
- roadmap tasks E8-T1 through E8-T6 are Completed.

### Evidence

Delivered as the documentation-truth and release closeout: the five E8-T3 deferred documentation findings are fixed — installation section 3 places the capability report before the target gates, cli-spec section 3 documents the enable probe with its unconditional guarantees, configuration-spec section 12 states the default-versus-probe check split, and the sink-contract and dispatch-plan examples carry the honest capability set. The section-4 items: AC-107's given-clause names the refuted `WATCHMAN_FILES_OVERFLOW` honestly, the E7-T1 record's twelve-file count is corrected in place, the E2-T2 Linux claims are hedged to the verified hosts with the D-020 legs, the E7-T12 matrix rows the review refuted (CON-003, CLI-004, DAT-009, SCP-008) carry in-place refutation markers with the new matrix authoritative, the Markdown count matches the 65-file manifest basis, the docs README narrative runs through E7 and E8 to v0.1.2 at SOT 1.0.33, the repository-layout deviations section documents the real package set, and the domain-model/overview prose matches the code. The SCP-008/AC-505 exception is closed across the charter, acceptance criteria, VALIDATION, README, and release notes on the review's linux/arm64 non-root `make verify` and linux/amd64 `make test` evidence, with the two permission-expectation tests self-skipping under root and the real Hermes/Watchman legs disclosed as macOS-only. The exact toolchain pin is the go.mod `go 1.26.6` directive (the separate `toolchain` line normalizes away as redundant; recorded honestly in the matrix). `make release VERSION=v0.1.2` runs twice with byte-identical `dist/SHA256SUMS` at the tagged tree (the digests live in `dist/SHA256SUMS` beside the tag; the E8 correction pass re-tagged v0.1.2 at the corrected tree so the artifacts carry the correction — changelog 1.0.35); the AC-506 test derives the version from the latest release-notes file as the one source, asserts the body names it, validates the documented artifact set, and checks `dist/SHA256SUMS` line-per-artifact with every checksummed file present; `RELEASE-NOTES-v0.1.2.md` discloses the remediation, the Linux closure, and the disabled TST-008 gate. Verified by `make verify` on darwin/arm64 (all checks green). Reviewed through two full-target Mulgae rounds (`r_01a03097-ad9a-78f4-8165-c604738e9b91`, remediation-eligible: the round-1 high (the roadmap status contradicting the release claims — the sanctioned pre-commit lifecycle state) and the mediums/lows — the off-by-one Markdown count (fixed to the 65-file manifest basis), the AC-506 one-source rewrite (the test now derives the version from the latest release-notes file and asserts the body), the in-place-row-edit claim aligned with the annotation practice, and the honest toolchain-pin reading — all remediated in-tree; round 2 recorded below). Changelog 1.0.33.


**Epic closeout:** all six E8 tasks are Completed; every Blocker, High, and mapped Medium finding of the 2026-08-23 review is fixed, the documentation-truth cluster is corrected, the SCP-008/AC-505 Linux exception is closed, and v0.1.2 is released from the tagged tree. The validation audit ran four whole-epic Mulgae rounds over the epic diff `9d96cc2..HEAD` (the first workspace-range attempt r_01a030c6-b060 failed on provider infrastructure with the logic lane's output missing and consumed no ordinal; r_01a030d6-15b3 round 1: the quarantine-release revision seam, the fail-open stale precondition, and the rejected-state disposition — remediated in d2efc23; r_01a030ea-465a round 2: two runbook errors my own round-1 fix introduced plus a stale godoc — remediated in c7ab48c; r_01a030f9-62e8 round 3: the route tree naming route stale and the sink-contract example booleans — remediated in 7a8ab10; r_01a03112-c4f8 confirmation-only: ci pass, coverage complete, zero medium-or-above findings, two accepted low test-quality observations — a classify-error test that cannot fail for its named regression and the missing root self-skip on the unreadable-root doctor test — recorded here as the next cycle's hardening). A post-closeout operator audit re-verified every D-020 finding-index row against the tree and found six rows recorded as Fixed that the execution had descoped ("scope as feasible") or omitted — a documentation-truth defect in the remediation's own record, corrected by the E8 correction commit: M-15 (the DAT-009 empty-version bypass and the never-read payload_version) now genuinely fails closed on read; M-17's schema claim is corrected in place (the five record tables reach revisions joinably by design, recorded rather than claimed); L-4 (the real-NUL fixture), L-5 (the AC-106 loop widening and the AC-103 same-final-digest pinning), and L-24 (the follow-up terminology entry) are delivered; M-18's remaining half (pattern-set compilation at validate) and M-20's remaining halves (the log message path and the config-show endpoint query masking) are implemented. M-16 (the record-schema/CLI-emission alignment) is corrected to Deferred — it is a wire-shape change out of the v0.1.2 scope, and no consumer depends on the divergence.

The validation audit reconciled the deferred findings (32 recorded in the commit trailers): the T6 batch closed in the audit remediation (the installation report-copy filename, the SHA256SUMS digest verification with semantic version selection, the AC-506 evidence claim and conditionality, and the toolchain-deliverable wording); the T1 test-coverage findings closed where cheap (the classify-error fail-safe arm and the fail-path generation fence pinned at store level; the over-budget end-to-end stays covered by the store-level failed-over-budget proof); the T3 documentation findings were fixed by E8-T6 itself; and the remaining T2/T4 hardening observations (load-timing residuals, wiring assertions, falsy age states, guard-chain placement) are dispositioned as recorded hardening for the next cycle — none blocks a MUST requirement, per the refreshed matrix.

---

# E9: Deferred-Inventory Hardening

**Epic status:** Completed (reopened 2026-08-25 by the D-023 formal audit and re-closed 2026-08-25 by D-024 with all nine tasks Completed)  
**Purpose:** Close the hardening inventory E8 recorded for the next cycle (D-021): the four Deferred mediums, the resolution remainders, the seventeen Deferred lows, the member-task test-coverage deferrals, and the documentation sub-wording items — every remaining D-020 disposition that is not a maintained exception or a D-018 acceptance. Reopened by D-023 for the 2026-08-25 external MVP compliance review: its six findings (F1 through F6) remediate through E9-T6 through E9-T9 under the darwin/arm64-only support policy D-023 records, and the epic re-closes with the v0.1.4 patch release.  
**Gate:** every D-020 row reads Fixed, maintained exception, or explicit D-018 acceptance — with the whole-epic review (diff from 148bf57, covering the E8 correction delta) converged and v0.1.3 released from the tagged tree. Reopened gate (D-023): every review finding dispositioned in-tree or by recorded supersession, the whole-epic review converged over the reopened delta, and v0.1.4 released from the re-closed tree.

## E9-T1: Record Schema Truth and Storage Hardening

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Make the CLI's record emissions match the published record schemas, land the revision columns the schema claim once promised, and harden the storage pragmas and migration backup behavior.

### Deliverables

- `dispatches show` emits schema-conformant records: `schema_version` on attempt and receipt records, the six missing required members on the intent summary (`schema_version`, `decision_id`, `route{id,revision}`, `resource_id`, `content_fingerprint`, `request`), and a derived dead-letter-record view when the dispatch is dead-lettered (M-16); a schema↔emission lockstep test pins the alignment;
- migration v7 adds `route_revision` to `dispatch_attempts`, `dispatch_receipts`, `work_receipts`, and `quarantine_items` (written from the intent's revision at insert, backfilled by join), and the schema comment's "recorded design choice" wording yields to the delivered columns (M-17);
- the `foreign_keys`, `busy_timeout`, and `synchronous=FULL` pragmas move into the DSN so every pooled connection re-applies them (L-16);
- one verified backup per migration run instead of one per pending unit (L-20);
- `PruneCutoffs` and the watchman test envelope's `changes[]` carry snake_case json tags (L-10), with goldens updated.

### Requirements

`DAT-004`, `DAT-007`, `DAT-009`, `OPS-008`

### Dependencies

D-021 recorded; E8 complete.

### Acceptance

- every required member of the four published record schemas is emitted by the owning CLI view, pinned by the lockstep test;
- a fresh open at v0 migrates to v7 with the backfill verified and the backup taken once;
- `make verify` green including the updated goldens.

### Evidence

Delivered as the record-schema truth and storage hardening: `dispatches show` emits schema-conformant records — `schema_version` on every attempt and receipt, the intent summary carrying `schema_version`, `decision_id`, `route` as the schema's `{id, revision}` object, `resource_id`, `content_fingerprint`, and the stored request document, and a derived dead-letter-record view (with the reason extracted from the transition context) for dead-lettered dispatches; `dispatches list` selects and populates the same schema-required members on every row (M-16; `TestE9T1RecordEmissionsMatchSchemas` drives real CLI emissions through both paths and validates the required members of the intent, attempt, and receipt schemas). Migration v7 adds `route_revision` to `dispatch_attempts`, `dispatch_receipts`, `work_receipts`, and `quarantine_items` — join-backfilled for existing rows, written from the creating intent at every insert site (and from the decision for quarantined arrivals, which create no intent) — with the schema comment updated from the recorded design choice to the delivered columns (M-17). All connection-scoped pragmas (`foreign_keys`, `busy_timeout`, `synchronous=FULL`) ride the DSN so pooled connections re-apply them, with the verification block unchanged (L-16); one verified backup per migration run replaces the per-unit copies with range-scoped naming (L-20); and `PruneCutoffs` plus the watchman test envelope serialize snake_case (L-10). Verified by `make verify` on darwin/arm64 (all checks green). Reviewed through two full-target Mulgae rounds (`r_01a0323a-58d0-7304-b7c1-19bfe2c9c697`, remediation-eligible: the raw-JSON dead-letter reason, the empty list members, and the fixture-based lockstep test — all remediated in-tree; `r_01a03255-e3eb-7869-9f9a-ed80ecc7e4d0`, hardening-deferral-eligible: ci pass, coverage complete, publication committed, eleven findings — the request-member shape (string vs the task-request object), full-schema strictness residuals (empty-string enums, additionalProperties), the untested dead-letter remediation and migration v7 propagation, and bookkeeping/pinning lows — deferred to the E9 epic validation audit with the exact run and finding IDs in the commit trailers, where the audit-owned remediation closes them before the confirmation review). Changelog 1.0.37.

## E9-T2: Reconciliation and Operator-Surface Hardening

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close the reconciliation enumeration symlink defect, the Watchman-context refusal, and the operator-surface gaps the review recorded as deferred.

### Deliverables

- the reconciliation walk skips every symlink — escaping or in-vault — into the skipped list (surfaced in the envelope as warnings); a symlink never projects as an exists-fact or `FileRegular` in an automatic task manifest, because the fact set describes regular files only (M-24);
- the WalkDir file-error prefix records the file, not `rel+"/"`, so siblings are not mislabeled Removed (L-8);
- `maintenance prune/vacuum --yes` refuses to run with `WATCHMAN_TRIGGER` or `WATCHMAN_ROOT` in the environment (CLI-007, M-21; exit 2 usage refusal, no registry change);
- `config show` accepts `--output json` (L-11) and the work commands on an unknown dispatch write their invalid-receipt audit row (L-7);
- the prune plan/execute guards share one predicate (T4-F005), the route-stale precondition becomes a store-level eligibility rule (T4-F006), and the dead self-assignment at the doctor boundary is removed (T4-F007).

### Requirements

`PTH-002`, `SRC-005`, `OPS-006`, `CLI-007`, `CLI-008`

### Dependencies

E9-T1 Completed.

### Acceptance

- an escaping symlink in the vault produces a reconciliation warning and no `FileRegular` diff entry;
- `maintenance prune --yes` under a Watchman environment exits 2 without touching the store;
- `make verify` green including the reconciliation symlink regression test.

### Evidence

Delivered as the reconciliation and operator-surface hardening: the reconciliation walk skips every symlink — escaping or in-vault — into the skipped list, now surfaced in the envelope as warnings (`Skipped` on the full-reconcile result), and never projects a symlink as an exists-fact or `FileRegular` in an automatic task manifest; the fact set describes regular files only, closing the D-018-recorded posture gap for real (M-24; `TestE9T2EscapingSymlinkNeverRegular` covers both symlink kinds against the skipped list, the added facts, and the stored manifests). File-level `WalkDir` errors record the file itself with exact-match semantics in `underSkippedPrefix` (directory entries keep their subtree prefix), so a blocked file no longer mislabels its siblings as Removed (L-8). `maintenance prune/vacuum --yes` refuse under `WATCHMAN_TRIGGER` or `WATCHMAN_ROOT` at exit 2 before any store opens (M-21/CLI-007; `TestE9T2MaintenanceRefusesWatchmanContext` covers both guard conditions); `config show` accepts `--output json` only (L-11); and the work commands' unknown-dispatch rejection writes its invalid-receipt audit row through the service's shared shape, `Service.AuditUnknownDispatch` (L-7; `TestE9T2UnknownDispatchAudits`). The prune plan and execution share one `notActiveSlotSQL` predicate with aliased JOIN forms (T4-F005); `route stale` consults the store-level `EligibleForStale` rule over a tri-state `ActiveDispatchAge` (none/unreadable/measured) so the CLI composes no guard chain (T4-F006, and T4-F004's tri-state delivered with it); and the dead self-assignment at the doctor boundary is gone (T4-F007). The known hermeskanban 1s stub-deadline flake (M-25) is raised to 10s. Verified by `make verify` on darwin/arm64 (all checks green). Reviewed through two full-target Mulgae rounds (`r_01a032aa-8a73-75e0-b4bb-9826da78d2bb`, remediation-eligible: the dead Resolve branch and the CLI-layer audit duplication — both remediated; `r_01a032be-ba3b-738d-a4bf-274cf98b08b7`, hardening-deferral-eligible: ci pass, coverage complete, publication committed, six findings — the tri-state and both-symlink-kind coverage landed in-tree with the round-2 remediation alongside the both-conditions guard test and the deliverable wording, with F003's decision-log provenance note deferred to the E9-T5 documentation pass in the commit trailers). Changelog 1.0.38.

## E9-T3: Security, Observability, and Revision Hygiene

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close the sanitization, secret-ownership, and observability remainders, and complete the revision projection's transport fields.

### Deliverables

- `sanitizeValue` covers `map[string]string` values (M-20's remainder);
- secret file references check ownership alongside the mode bits (L-15);
- `work.begun`, `work.completed`, and `work.receipt_invalid` events are emitted at the work command sites and doctor findings carry `trace_id` (L-17);
- the revision projection gains the transport fields (`executable`, `submit_timeout`, `environment_allowlist`, and the manifest byte bound), so replacing the target binary or its bounds pauses the acknowledged route like any behavior change (T3-F006; configuration-spec §13 amended);
- a suppressed `--mutex-key` logs a warning at submission (T3-F007);
- a distinct policy-revision digest is computed in the config package and recorded on decisions from plan, quarantine-release, and reconcile (L-18).

### Requirements

`SEC-006`, `SEC-007`, `OPS-001`, `POL-007`

### Dependencies

E9-T2 Completed.

### Acceptance

- map values and unsanitized message fields cannot leak credential shapes (adversarial test extended);
- changing only the executable or submit timeout changes the computed route revision;
- decisions carry a policy revision distinct from the route revision where the subsets differ.

### Evidence

Delivered as the security, observability, and revision hygiene: observability `sanitizeValue` renders every `map[string]string` value through the configured path policy, so a path inside a string map can no longer survive the redacted policy (M-20's remainder; `TestE9T3MapValuePathSanitization`). The secret resolver refuses a secret file owned by another uid alongside its mode-bit check — a planted or swapped file is a configuration defect, never a secret to read (L-15; `TestE9T3SecretFileOwnershipChecked`, root-only transfer, same-uid resolution covered by the existing file test). The work commands emit `work.begun` and `work.completed` at the receipt boundaries and `work.receipt_invalid` on every rejection, each carrying TraceID/DispatchID/RunID correlation with the command logger wired through the CLI service, and every doctor finding carries the request's `trace_id` (L-17; `TestE9T3WorkLifecycleEventsCarryTrace`, `TestE9T3DoctorFindingsCarryTraceID`). The computed route revision now covers the transport fields — the target `executable`, `submit_timeout`, `environment_allowlist`, and the route's manifest byte bound — so replacing the target binary or its bounds pauses the acknowledged route like any behavior change (T3-F006; `TestE9T3RevisionCoversTransport`; the g3 downtime and ambiguity gates now re-acknowledge after every executable swap, and the downtime recovery asserts the stale-rebuilt replacement with exactly one board task — the identity-equality expectation encoded the old, revision-blind behavior). A render that drops a configured mutex key the target cannot honor reports `RenderedTask.SuppressedMutex`, and the sink emits one `dispatch.mutex_suppressed` warning carrying the trace, dispatch, route, and target identity while the submission itself still succeeds (T3-F007; renderer and sink tests). Every policy decision now records `config.PolicyRevision` — an independent `pol-` digest over the policy-evaluation surface (include/exclude, resolved case mode, batching thresholds, protected/immutable, bulk/overflow/fresh actions) — on arrival, reprocess, reconcile, follow-up completion, and quarantine-release replacement decisions; the no-live-route fallbacks keep the quarantined decision's own digest (the route-revision echo survives only in legacy rows written before this change) (L-18; `TestE9T3PolicyRevisionIndependentAndSensitive`, `TestE9T3ArrivalDecisionRecordsPolicyDigest`, and the e5t1 follow-up assertion updated to the new contract). Verified by `make verify` on darwin/arm64 (all checks green). Reviewed through two full-target Mulgae rounds (`r_01a0337c-17bd-7a34-a0fb-25e97db5c061`, remediation-eligible shape: zero committed findings, with the round-1 security report's typed-map denylist gap, the quarantine-fallback wording overstatement, and the g3 recovery's missing lineage tie all remediated in-tree — the denylist check now runs before the path policy with a regression test, the comment and this evidence phrase state the legacy-row echo honestly, and the downtime recovery asserts the original is superseded through the `-rebuilt-` identity tie and the superseding decision linkage; `r_01a03387-48d1-7857-92fd-aefcbacc9b00`, hardening-deferral-eligible: ci pass, coverage complete, publication committed, zero findings, every role confirming the six areas and the remediations). Changelog 1.0.39.

## E9-T4: Test-Coverage Hardening

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Pin the test-coverage deferrals the E8 member tasks recorded, harden the load-sensitive tests, and correct the two confirmation-review observations.

### Deliverables

- the deferred coverage proofs land: path-fact load failure (T1-F002), pending-reconcile delivery after an IDLE-slot merge (T1-F005), the service-level budget mirror (T1-F006), over-budget resolution end to end (T1-F007), the scheduled recipes carrying `--submit` (T2-F001), heartbeat steal-prevention end to end (T2-F002), leaseTTL wiring assertions at the three runtime sites (T2-F003/F004), and the ungated-recovery posture pin (T2-F005);
- `ActiveDispatchAgeNanos` distinguishes no-active-dispatch, unreadable, and measured states (T4-F004) and the two load-sensitive tests stop flaking under full-parallel coverage (M-25);
- `TestE8AuditClassifyErrorStaysMaterial` matches the real predicate contract (an error-capable scope predicate or an honest rename; the confirmation-review observation) and `TestE8T4DoctorReportsUnreadableRoot` self-skips under root;
- the three self-healing goldens fail when the golden is missing (L-6) and a skill↔renderer task-variable cross-check test lands (L-23).

### Requirements

`TST-001`, `TST-002`, `TST-009`

### Dependencies

E9-T3 Completed.

### Acceptance

- every listed deferral carries an executing, named test;
- the full suite passes twice consecutively under `-count=1` with coverage instrumentation without timing failures;
- the goldens fail on absence instead of regenerating.

### Evidence

Delivered as the coverage hardening: the E8 member-task deferrals each carry an executing named test — a dropped path-facts surface still completes with the attribution decision recording `facts_unavailable` (T1-F002; `TestE9T4PathFactLoadFailureDegradesConservative`), a pending reconciliation recorded on an IDLE route is delivered as exactly one latest-state follow-up by the next — even clean — completion with the flag consumed (T1-F005; `TestE9T4PendingReconcileDeliveredThroughFollowup`), the service-level failure-budget mirror and the store guard agree end to end with an exhausted one-shot budget resolving through UNCERTAIN and no follow-up beyond the single retry (T1-F006; `TestE9T4FailureBudgetMirrorDrivesUncertain`), and the over-budget UNCERTAIN route resolves through operator reconciliation to IDLE with no second dispatch (T1-F007; `TestE9T4OverBudgetUncertainResolvesThroughReconciliation`). The shipped scheduling recipes carry `--submit` with the two-key-gate posture stated in place of the stale omit guidance — in the launchd plist, the systemd unit, and every live spec passage that repeated the old posture (cli-spec §9, the installation and operations passages, and the VALIDATION production-enable checklist): before the production acknowledgement the route fails closed at exit 14, and after it a configuration-disabled route persists decisions and recovers without submitting, with that YAML-key leg pinned (T2-F001; `TestE9T4ScheduledRecipesCarrySubmit` with the E6-T3 verifier updated to the new contract, and `TestE9T4ReconcileSubmitSkipsDisabledYAMLKey` from the round-1 security observation); the migration-lock test now pins steal prevention itself — a live holder survives a concurrent waiter, the lock hands over on release, and a dead holder's stale lock is stolen (T2-F002; `TestE9T4MigrationLockStealPrevention`); the three submit surfaces (dispatch, drain, reconcile --submit) share one `newSubmitRuntime` constructor whose lease-TTL derivation is asserted for every actor with a `leaseTTLFor` table (T2-F003/F004; `TestE9T4LeaseTTLWiredAtEverySubmitSite` — a site cannot drift without leaving the constructor); and ungated recovery on a configuration-disabled route heals the wedged intent while submitting nothing, with the operator warning (T2-F005; `TestE9T4UngatedRecoveryOnDisabledRoute`). The confirmation observations landed: the misnamed `TestE8AuditClassifyErrorStaysMaterial` is renamed to the in-scope-unobserved contract it actually pins, and the predicate's real error arm — an uncompilable scope pattern — fails the work commands closed at exit 3 (`TestE9T4ScopePredicateErrorFailsClosed`), while the unreadable-root doctor test self-skips under root. The three self-healing goldens (normalized config, content fingerprint, idempotency key) now fail on a missing golden instead of regenerating (L-6), and the skill↔renderer cross-check pins the rendered instruction against every assigned skill id and the exact work-command flag surface (L-23; `TestE9T4SkillRendererCrossCheck`). The remaining 1s stub deadlines in the hermeskanban suite are raised to 10s, and the full suite passed twice consecutively under `-count=1` with coverage instrumentation with zero timing failures (M-25 residual). `T4-F004`'s tri-state was already delivered inside E9-T2's store-level rule and is cited there. Verified by `make verify` on darwin/arm64 (all checks green). Reviewed through two full-target Mulgae rounds (`r_01a033d9-df52-765d-82aa-36c0dd79d7ac`, remediation-eligible shape: zero committed findings, with the round-1 reports' material observations remediated in-tree — the three stale doc passages and the overstated pre-gate prose corrected everywhere they appeared with the passage counts fixed, the YAML-key leg of `reconcile --submit` pinned by test from the security observation, and the ungated-recovery and cross-check assertions tightened to their exact claims; `r_01a033ec-5073-7f95-a6b1-7187bbcb1a55`, hardening-deferral-eligible: ci pass, coverage complete, publication committed, zero findings, every role confirming the nine areas and the remediations). Changelog 1.0.40.

## E9-T5: Documentation Truth, Dependency, and the v0.1.3 Release

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close the documentation sub-wording items, the dependency advisory, and release v0.1.3 from the validated tree.

### Deliverables

- the documentation items: observability §3 lists the events actually emitted, §5 documents `--output json` with the real status payload, §7 names the offline construction gate (not a capability match); the roadmap E4-T5 gains its Evidence section and E5-T4 lists all nine reason values; configuration-spec §9 states the multiplier maximum 10.0 and §7/§8 note the inert `unsafe_path_action` and `git.mode` keys (L-3); the error-model §2 exit-13 wording matches the code; the reserved error codes are tabulated as reserved (L-21); SCP-001's certification posture is noted (L-25) and `.markdown` is documented (L-26);
- the `golang.org/x/text` indirect dependency is bumped (GO-2026-5970) or the offline constraint is recorded (L-27);
- the whole-epic validation review runs over the diff from 148bf57 — covering the E8 correction delta and all of E9 — through the 3+1 round budget;
- `make release VERSION=v0.1.3` twice byte-identical, the `v0.1.3` tag at the final tree, RELEASE-NOTES-v0.1.3 disclosing the hardening and the TST-008 gate remaining disabled; the epic closeout reconciles every D-021 inventory item to its final disposition.

### Requirements

All `BND-*` through `WHK-*` requirements.

### Dependencies

E9-T4 Completed.

### Acceptance

- every D-020/D-021 dispositioned item reads Fixed, maintained exception, or explicit acceptance in the final record;
- v0.1.3 artifacts are byte-reproducible and tagged;
- roadmap tasks E9-T1 through E9-T5 are Completed.

### Evidence

Documentation truth delivered: observability §3 now separates the thirteen events the v0.1 CLI actually emits (including the E9-T3 `dispatch.mutex_suppressed` and the work lifecycle trio) from the reserved vocabulary no command emits yet; §5 documents the real surfaces — `status --output json` with its actual payload (`routes`, `queues`, `quarantine`, `oldest_unresolved`, `database_bytes`, `targets`) and `doctor`'s always-JSON findings envelope with `trace_id` — in place of the `--json` flag and the latency/retry counters that do not exist; §7 names the target check as the offline construction gate (report validation, no process execution) with the installed-version probe credited to dispatch time. The roadmap's E4-T5 gained its Evidence section (the five-test G3 harness covering the six acceptance scenarios over real components, the VALIDATION G3 table, and the E9-T3 transport-coverage semantics the downtime gate now asserts), and E5-T4's reason vocabulary is corrected to the delivered nine values (`initial, scheduled, overflow, fresh-instance, lost-cursor, manual, delivery, stale-active, startup`) in both the deliverable and the evidence. configuration-spec §9 states the multiplier range 1.0–10.0 the schema already enforces; §4 and §8 note the two inert keys (`git.mode`, `unsafe_path_action`) with their revision-recorded-but-unconsumed semantics and their exclusion from the policy digest; the `file_scope` row documents the `.md`/`.markdown` scope (SCP-003's delivered scope, L-26) and SCP-001 carries the certification-posture note (L-25: no formal certification process exists; the claim means the verified `make verify`-gated shape). The error-model's exit-13 row states the code truth (the Watchman protocol surface exits 13 on `target_response_invalid`; the plain dispatch command records `unknown` in its exit-0 envelope), and the reserved code names (`batch_hard_limit`, `protected_path_quarantined`, `unsafe_path_quarantined`) moved out of the emitted registry into an explicit reserved table with their activation conditions (L-21). L-27 resolved by a real bump: `golang.org/x/text` v0.14.0 → v0.41.0 (GO-2026-5970) with the aligned `x/mod`, `x/sync`, and `x/tools` indirects, `go mod tidy` clean, full suite green — no offline constraint needed. The epic validation, the v0.1.3 release, and the closeout decision record: the whole-epic review over 148bf57..HEAD (covering the E8 correction delta, closing its confirmation-evidence gap explicitly) converged through three remediation rounds plus a clean confirmation — round 1 (`r_01a0344b-e13f`) surfaced one medium (the retention prune wedging on a terminal dispatch's old begun receipt — its un-cascaded foreign key blocked the intent delete) and the typed-map denylist case-folding gap, both fixed with regression tests; round 2 (`r_01a03463-de2c`) carried the by-design untracked review-file disposition and the test-comment gap; round 3 (`r_01a0346e-656e`) carried four lows (the release-checklist retention wording, the slot-guard pin, the shared terminal predicate, the shared denylist helper), all fixed; the confirmation round (`r_01a03482-6db2`) committed clean with zero findings. The E9-T1 audit's eleven deferred findings are reconciled in-tree (the request object on show and list, the schema-excluded members off the wire, the open outcome, the null shapes, the enum-clamped dead-letter reason with the degraded-context test, the v7 backfill pin over a rewound v6 shape, and the wire pins — full-schema validation from real runs replaces the presence checks), and the E9-T2 F003 provenance correction is recorded in D-022. v0.1.3 is released from this tree: `make release VERSION=v0.1.3` twice byte-identically, the tag at the final commit, RELEASE-NOTES-v0.1.3 disclosing the hardening and the TST-008 gate remaining disabled. D-022 closes the epic: every D-021 inventory item reads Fixed, maintained exception (M-8), or D-018 acceptance (L-13, L-14), with the D-011 scheduled-submit supersession recorded. Changelog 1.0.42. Reviewed through two full-target Mulgae rounds (`r_01a03414-3a5e-7f15-b90e-330916d4b6be`, remediation-eligible: one low finding — configuration-spec §4 understated git.mode's recording sites — plus three wording residuals, all remediated in-tree: both recording sites stated, the five-test G3 count, doctor's accepted-and-ignored `--output json`, and the §3 log-level hedge; `r_01a03423-f206-7108-be8c-782a975ff2e0`, hardening-deferral-eligible: ci pass, coverage complete, publication committed, zero findings, every role confirming the documentation truth and the dependency bump).

## E9-T6: Submission-Gate Revision and Capability-Report Integrity

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close review findings F1 (High) and F2: the computed route revision must cover every behavior-affecting webhook transport field, and `route enable` must refuse without capability evidence regardless of target liveness.

### Deliverables

- the revision projection gains the webhook transport fields: `auth.type`, `auth.secret_ref` (the reference name only; secret values stay excluded), `auth.header_name`, `idempotency_header`, `lookup_timeout`, and the `capability_report` path, so changing authentication or deduplication headers makes existing intents stale and requires route re-acknowledgement (F1);
- the route's `reconciliation` and `retention` blocks receive explicit recorded dispositions in the revision contract (included or excluded with the reason), and the RouteRevision doc comment states the complete inclusion rule;
- `route enable` always loads and validates the capability report for hermes-kanban targets — a missing, unreadable, or unsupported-version report refuses at exit 3 whether or not the executable is present (the version set is build-time evidence), while an unreachable target stays a warning deferred to the submit path's run-time gate and only freshness against the installed binary rides the probe (F2);
- the CLI contract's `route enable` wording loses the missing/unreadable ambiguity;
- regression tests: each newly covered field moves the computed revision (unit level), an acknowledged route whose delivery-evidence fields change goes stale end to end (enable, mutate, submit → stale-revision refusal; the end-to-end leg drives the lookup bound with the auth and header fields pinned at the revision unit level), and `route enable` with the executable absent and the report missing, unreadable, or recording an unsupported Hermes version exits 3.

### Requirements

`POL-007`, `SEC-010`, `HER-005`, `CLI-008`

### Dependencies

D-023 recorded; E9-T5 Completed.

### Acceptance

- changing only `auth.type`, `auth.header_name`, `auth.secret_ref`, `idempotency_header`, or `lookup_timeout` changes the computed route revision and pauses the acknowledged route;
- `route enable` without a valid capability report exits 3 in every executable-availability combination;
- `make verify` green including the new regression tests.

### Evidence

Delivered as the submission-gate revision and capability-report integrity: the computed route revision's transport projection gains the webhook delivery-evidence surface — `auth.type`, `auth.secret_ref` (the reference name; the resolved secret never joins, SEC-006), `auth.header_name`, `idempotency_header`, `lookup_timeout`, and the `capability_report` path — so changing how a dispatch authenticates or deduplicates pauses the acknowledged route like any behavior change (F1; `TestE9T6RevisionCoversWebhookDeliveryEvidence` pins each field through the webhook-target route, `TestE9T6DeliveryEvidenceChangePausesUntilReacknowledged` drives the end-to-end pause through a lookup-bound edit with the re-acknowledgement resuming the drain). The route's `reconciliation` block joins the projection by explicit disposition (`TestE9T6RevisionCoversReconciliation`) and the `retention` block stays out with the reason recorded in the code and configuration-spec §13 — pruning bounds never change submission behavior (`TestE9T6RetentionStaysOutOfRevision`); §13 also now states the E9-T3 transport fields it had been promised with, closing that wording debt. `route enable` treats the capability report as mandatory evidence in every target-liveness state: the `os.Stat` guard is gone, `LoadReport` runs unconditionally in the unavailable-executable branch, and a missing or unreadable report refuses at exit 3 (F2; `TestE9T6EnableGateRequiresReportWithoutExecutable` covers the missing-report, unreadable-report, unsupported-version, and honest-report-with-warning paths) — the round-1 review's material observation that a well-formed report recording a Hermes version outside the runtime-verified set still enabled is closed with `Report.RecordedVersionSupported`, because the supported set is build-time evidence needing no live target; only freshness against the installed binary rides the probe, exactly as the refined cli-spec wording and the gate comment now state. The unconditional-guarantees refusal is one shared closure across the available and unavailable branches. Two environment-dependent tests gained the TST-007 skip their absent-binary case already had: the host's Hermes moved to 0.20.5, outside the then-verified baseline set, so `TestRealHermesProbeIfAvailable` and the AC-504 clean-host flow skip with the installed version and the verified set named (widening the set is a fresh E0-T4 probe, not a test override; verified with the stash-isolated clean tree failing identically). Verified by `make verify` on darwin/arm64 (all checks green) and a fresh `-count=1` full suite. Reviewed through two full-target Mulgae rounds (`r_01a034f8-4a87-7634-a4a9-783f97dc2d5c`, remediation-eligible: ci pass, coverage complete, zero committed findings, with the round-1 reports' material observations remediated in-tree — the unsupported-version enable gap, the shared guarantees closure, the deliverable-to-test wording alignment, and the secret-value comment stating its structural enforcement; `r_01a0350f-0071-744c-a713-865b95a3ecbf`, hardening-deferral-eligible: ci pass, coverage complete, publication committed, zero findings, every role confirming the round-1 gap closed — the recorded-version gate mirrors `VersionMatchsWith` parsing, the freshness deferral is backed by the enforced submit-path probe, and every probe state either validates the report or refuses; the residual report observations — the gate comment's pre-existing "unusable target" phrasing, the unavailable-branch weak-guarantee coverage leg, `RecordedVersionSupported` boundary unit tests, `authProjection` nil-equivalence pinning, and the twin validation ladders across the cli/adapter boundary — carry to the E9 epic validation audit). Changelog 1.0.44.

## E9-T7: Header Grammar and Pinned-Toolchain Enforcement

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close review findings F3 and F4: webhook header names must validate against the complete RFC 9110 token grammar before side effects, and the pinned Go toolchain must be enforced by the build entrypoints themselves.

### Deliverables

- `validHeaderName` accepts exactly the RFC 9110 `tchar` set (`!#$%&'*+-.^_`|~`, ALPHA, DIGIT), rejecting separators, quotes, backslashes, and non-ASCII characters the previous scan let through (F3);
- the configuration schema constrains `header_name` and `idempotency_header` with the equivalent pattern, both schema copies kept byte-identical;
- regression tests cover the previously accepted invalid names (`Bad(Name`, `Bad,Name`, `Bad/Name`), quotes, backslashes, and Unicode, plus valid tchar names;
- `make verify` and `make release` both require the exact `go1.26.6` toolchain before building, through a version check that fails fast with a clear message (F4), with the comparison logic unit-tested so an unsupported version is proven rejected before any build step;
- VALIDATION.md's SCP-005 wording and the release checklist's toolchain line state the enforced mechanism in place of the go.mod-only claim.

### Requirements

`SCP-005`, `SCP-006`, `TST-001`

### Dependencies

E9-T6 Completed.

### Acceptance

- a header name outside the tchar grammar fails configuration validation before any submission attempt;
- `make verify` and `make release` reject an ambient Go toolchain other than 1.26.6 before building;
- `make verify` green including the grammar and toolchain tests.

### Evidence

Delivered as the header grammar and pinned-toolchain enforcement: `validHeaderName` accepts exactly the RFC 9110 tchar set — ALPHA, DIGIT, and the fifteen specials — so every other separator, quote, and backslash the previous scan let through is rejected at sink construction for both `auth.header_name` and `idempotency_header` before any submission attempt (F3; `TestE9T7HeaderNameGrammar` pins the full separator class including `< > ? @ [ ]` that a partial regression could admit, and `TestE9T7SchemaRejectsNonTcharHeaderNames` pins the schema pattern against the identical list for both fields, with the valid tchar loads proven). The configuration schema carries the equivalent `pattern` on both fields with the two copies byte-identical, and configuration-spec §5 states the grammar in prose beside the collision rule it already documented. The new `go-version-check` target enforces the exact toolchain before any build: the pin is read from the go.mod go directive itself (single source of truth), `internal/tools/toolchaincheck` compares it against the toolchain compiling the check (`runtime.Version`), `VersionMatches` is unit-tested against the rejected-version matrix, and every compiling target carries the prerequisite edge so even `make -j` cannot start a build with a compiler that is not the pin (F4; with `GOTOOLCHAIN=auto` the go command selects the pinned toolchain itself and the check passes, which is the pin being honored). VALIDATION.md's SCP-005 row and the release checklist's toolchain line state the enforced mechanism in place of the go.mod-only reading. Verified by `make verify` on darwin/arm64 (all checks green, including a `make -j4` run confirming the check runs first) and a fresh `-count=1` full suite. Reviewed through two full-target Mulgae rounds (round 1 `r_01a0352e-419b-7031-bb71-098b8a06a324`: ci pass, coverage complete, zero committed findings, with the reports' material observations remediated in-tree — the parallel-make ordering edge, the GOTOOLCHAIN=auto wording precision, the configuration-spec §5 grammar sentence, the schema-level `header_name` coverage, and the test-comment correction; round 2 `r_01a0353a-d4a4-7105-ace2-742fb33f0402`: ci pass, coverage complete, publication committed, zero findings, every role confirming both gates; the residual report observations — the colon's absence from the sink grammar test's invalid list and the schema-test comment's "same invalid list" wording — carry to the E9 epic validation audit). Changelog 1.0.45.

## E9-T8: macOS-Only Support Policy and Linux-Surface Removal

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close review finding F6 under the D-023 support-policy decision: darwin/arm64 is the only supported platform, and the Linux packaging and documentation surface is removed or explicitly superseded.

### Deliverables

- the release output restricts to `darwin/arm64`; the `linux/amd64` build leg, the systemd example assets, and the systemd leg of `schedule-check` leave the repository;
- SCP-008's and AC-505's Linux clauses carry supersession annotations pointing at the D-023 policy; the charter's success definition, the testing strategy, the implementation guide, the configuration-spec platform wording, the observability and Watchman scheduling guidance, and the installation guide state the macOS-only policy;
- the packaged maintenance skill declares `platforms: [macos]`; the root README drops the Linux-host clause;
- historical release notes keep their Linux-artifact records unchanged, with the supersession authority recorded in D-023;
- traceability and manifest outputs regenerate; the macOS verification entrypoint passes.

### Requirements

`SCP-008`, `TST-009`

### Dependencies

E9-T7 Completed.

### Acceptance

- `make release` produces exactly one darwin/arm64 artifact set;
- no active specification, contract, or operational document presents Linux as supported; the supersession trail is explicit;
- `make verify` green.

### Evidence

Delivered as the macOS-only support policy and Linux-surface removal under the D-023 decision: the release output restricts to exactly `darwin/arm64` (proven by a throwaway `make release` run producing one artifact plus `SHA256SUMS`), the `linux/amd64` build leg is gone, the systemd service and timer examples leave `docs/examples/scripts/` with the uninstall script and the schedule examples README rewritten, and `schedule-check` lints the launchd artifact and the uninstall script only (the manifest check simplifies to `shasum`, the supported host's tool). SCP-008 in required-spec and AC-505 in acceptance-criteria carry explicit D-023 supersession annotations with the historical D-020 closure records standing as history; the charter's success definition, the testing strategy's release layer, the implementation guide's driver row, configuration-spec's pattern-mode wording, the observability and Watchman scheduling guidance, and the installation guide (header, release output, platform path table, scheduling section, validator wording) all state the macOS-only policy; the packaged maintenance skill declares `platforms: [macos]` and the root README names macOS as the only supported platform. Historical release notes keep their Linux-artifact records unchanged with D-023 as the supersession authority. The three tests that pinned the systemd artifacts carry the policy (the E6-T3 invocation and existence tests and the E9-T4 recipe test now pin the launchd recipe; `TestScheduleExamplesExistForMacOS` guards the reduced set), and a fresh sweep shows every remaining Linux mention in the active specification and operations documents carries its supersession or history context. Traceability and the manifest regenerate for the reduced file set. Verified by `make verify` on darwin/arm64 (all checks green) and the throwaway release artifact check. Reviewed through two full-target Mulgae rounds (`r_01a0354f-bc64-7ef5-b0a9-723dc80c1374`, remediation-eligible: ci pass with three findings — the missing AC-505 annotation itself (F001, medium: the roadmap's supersession claim had not landed in acceptance-criteria.md), VALIDATION.md's stale present-tense systemd claims (F002), and the release checklist's live systemd items (F003) — all three verified valid and remediated in-tree; `r_01a03557-b583-772b-aa67-38f130a31b6d`, hardening-deferral-eligible: ci pass, coverage complete, publication committed, zero findings, every role confirming the remediated annotations and the policy sweep). Changelog 1.0.46.

## E9-T9: Documentation Truth Resynchronization and the v0.1.4 Release

**Status:** Completed  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close review finding F5 and re-close the epic: every status surface agrees with the authoritative decision record, and v0.1.4 ships the remediation from the validated tree.

### Deliverables

- `docs/README.md` (SOT version, implementation target, Roadmap State narrative), `docs/VALIDATION.md` (validation record, versions, counts), the roadmap's summary and current-state block, and the release checklist are synchronized to the post-remediation truth — one SOT version, one shipped release, one E9 lifecycle state, one task count;
- the release checklist is rewritten from its v0.1.1-era basis to the current 60-task, macOS-only posture;
- the whole-epic validation review runs over the reopened delta through the 3+1 round budget;
- `make release VERSION=v0.1.4` twice byte-identical, the `v0.1.4` tag at the final tree, and RELEASE-NOTES-v0.1.4 disclosing the submission-gate hardening (acknowledged routes re-acknowledge once under the widened revision), the toolchain enforcement, and the macOS-only policy;
- the closeout decision dispositions every D-023 finding to its final state.

### Requirements

All `BND-*` through `WHK-*` requirements.

### Dependencies

E9-T8 Completed.

### Acceptance

- every status surface reads the same SOT version, shipped release, E9 state, and task count;
- v0.1.4 artifacts are byte-reproducible and tagged;
- roadmap tasks E9-T6 through E9-T9 are Completed and the epic re-closes.

### Evidence

Delivered as the documentation truth resynchronization and the v0.1.4 release (F5): the status surfaces agree with the decision record — `docs/README.md` carries SOT 1.0.47 with the shipped v0.1.4 target line and a Roadmap State narrative through the D-023 reopen and D-024 re-closure, `docs/VALIDATION.md` records the post-D-024 validation with the 60-task completion statement, the roadmap's summary and current-state block read the re-closed epic with 60/60, and the release checklist is rewritten from its v0.1.1-era basis to the current 60-task, macOS-only, toolchain-enforced posture. The member-task residual observations are reconciled in-tree by this task's audit: the enable-gate comment's opening clause states the evidence rule rather than the unreachable-target exception, `TestE9T6EnableGateRequiresReportWithoutExecutable` gains the unavailable-branch weak-guarantee leg, `TestE9T6RecordedVersionSupportedBoundaries` pins the recorded-version gate's parsing boundaries (the bare triple, the foreign format, the prefix-adjacent boundary triple), `TestE9T6AuthProjectionNilEquivalence` pins the nil-versus-empty auth projection, the sink grammar test's invalid list gains the colon, and the schema-test comment states the separator-list relationship precisely; the twin validation ladders across the cli/adapter boundary are recorded in D-024 as an architectural observation for the next hardening cycle rather than forced at closeout. RELEASE-NOTES-v0.1.4 discloses the one-time re-acknowledgement under the widened revision, the enforced toolchain pin, the macOS-only artifact set, the runtime-verified Hermes set with the TST-007 skip posture, and the TST-008 gate remaining disabled. The task's own review passed through two full-target Mulgae rounds (`r_01a0356d-5993-7a83-9d16-48b13021b3e7`, remediation-eligible: ci pass, coverage complete, zero committed findings, with every role confirming the six reconciliations and the resynchronized surfaces; `r_01a03575-3871-7cae-8931-afd540997698`, hardening-deferral-eligible: ci pass, coverage complete, publication committed, zero findings). The whole-epic validation over the reopened delta (d68eff6..HEAD) converged through three remediation rounds plus a clean confirmation: round 1 (`r_01a0357c-cab8-7adb-9532-21bd7435b238`) carried three lows (fmt-check off the toolchain edge, the duplicated version-truncation parsing), fixed in c9e3245; round 2 (`r_01a0358b-26eb-7097-9dec-2f5aee31dbc9`) carried the unannotated live Linux guidance in the roadmap's index and task records plus the checklist's absent evidence pointer, fixed in d327915 together with the negative guard keeping the retired systemd surface deleted; round 3 (`r_01a035a4-51b3-79ab-be29-b911db37cbe4`, after one publication-evidence infrastructure failure that did not reach publication and did not consume the ordinal) carried four lows — the .PHONY completion, the TST-007 guard deduplication through testsupport/hermesenv (the caller-predicate shape the import cycle forces), and the missing tchar positives fixed in df476aa, with the fourth verified invalid on the tree (an explicit empty auth.header_name never reaches the schema; omitempty drops it) and recorded as such; the confirmation round (`r_01a035ad-d5aa-70e3-b9b4-4f58a489a0cd`) committed clean with zero findings. v0.1.4 is tagged at the final tree after the byte-identical double build. Changelog 1.0.47.

---

# E10: Source and Reconciliation Integrity

**Epic status:** Completed (2026-08-26: all three tasks Completed and gate G6 evidenced in VALIDATION.md)
**Purpose:** Close the two current-state correctness defects before widening dispatch behavior.
**Gate:** G6

## E10-T1: Resource Observation Fence and Bounded Reconciliation Reads

**Status:** Completed
**Design Gate impact:** Not required; ADR-0018 is the approved design.

### Objective

Prevent a stale full reconciliation from replacing newer Watchman path facts
and prevent a growing or unstable file from causing an unbounded read.

### Deliverables

- monotonic resource observation revision advanced by every path-fact writer;
- compare-and-swap full-snapshot replacement and typed concurrent-change result;
- one persisted due reconciliation on conflict;
- `max_file_bytes + 1` bounded hashing, stability check, one retry, and explicit quarantine/reconciliation outcomes;
- SQLite, race, crash, and status regression tests.

### Requirements

`DUR-013` through `DUR-015`, `OPS-012`, `TST-011`

### Dependencies

D-025 and ADR-0018 accepted; E9-T9 Completed.

### Acceptance

- a concurrent ordinary fact update survives full reconciliation;
- replacement and revision advancement are one transaction;
- growing and twice-unstable files never exceed the configured read bound;
- `make verify` green.

### Evidence

Delivered as the resource observation fence and bounded reconciliation reads: schema v8 gives every resource the monotonic path-fact observation revision (`resources.observation_revision`, `TestE10T1MigrationV8BackfillAndIntegrity` pins the zero backfill, the negative CHECK, and the interrupted-upgrade window healing on reopen), and all three durable path-fact writers advance it inside their own transaction — ingestion (`upsertPathFacts`), the fenced full-snapshot replacement, and, after the round-1 review remediation, the retention prune (`ExecutePrune` advances exactly the purged resources over the identical predicate; `TestE10T1RetentionPurgeAdvancesObservationRevision` pins the advance, the survivor facts, and the no-op negative arm). The replacement is a compare-and-swap on the pre-enumeration revision with the advancement in the same transaction: a stale expectation refuses with the typed `ErrObservationConflict`, the newer facts survive untouched, and the run records the typed `concurrent_change` outcome with exactly one due reconciliation generation (`TestE10T1ConcurrentFactUpdateSurvivesFullReconciliation` drives the interleaving deterministically through a second-connection trap; `TestE10T1ReplacePathFactsCAS` pins the store-level success, refusal, preservation, and missing-resource arms; `TestE10T1UncontestedRunStoresSnapshotAndAdvances` pins the uncontested single advance). Reconciliation hashing reads at most `max_hash_file_bytes + 1` bytes through the shared `records.SumBounded` (unified with ingestion in the round-1 remediation), checks size/mtime stability across the read, retries an unstable file once, and reports a stable over-bound file as `quarantined_over_bound` and a twice-unstable file as `unstable_after_retry` with both digests left unknown (`TestE10T1BoundedReadNeverExceedsMaxPlusOne` proves the bound by a counting reader — exactly max+1 bytes consumed against an endless stream; `TestE10T1StableOverBoundFileIsQuarantineEvidence` and `TestE10T1GrowingFileRaceStaysBoundedAndExplicit` pin the evidence arms and the interleaving-independent race postconditions; `TestE10T1HashStableRejectsChangedFile` pins the stability predicate; `TestE10T1ReconcileEnvelopeReportsBoundedHashEvidence` and `TestE10T1StatusRegressionAfterFencedSchema` pin the operator envelope and the status surface on the v8 schema). Verified by `make verify` on darwin/arm64 (all checks green) with the Gaori-routed manifest-check, schema-validation, and traceability evidence runs passed. Reviewed through two full-target Mulgae rounds (`r_01a03a09-4b31-7781-9199-141920064d5b`, remediation-eligible: ci pass, coverage complete, publication committed, zero findings — its reports' two verified in-scope observations, the retention-prune revision gap and the duplicated bounded-hashing implementations, were remediated in-tree; `r_01a03a14-7068-7735-b8bc-f4e5d1823e94`, hardening-deferral-eligible: ci pass, coverage complete, publication committed, zero findings, every role confirming both remediations closed — the residual advisory observations carry to the E10 epic validation audit: the intent-before-fence ordering trade-off the E5 F006 design already documents, the coarse-mtime stability limitation ADR-0018 sanctions, the deferred-forever-under-perpetual-concurrency liveness characteristic, ephemeral over-bound evidence outside the status/doctor surfaces, the three new envelope keys' operator documentation beyond the CHANGELOG, the architecture docs' twice-unstable wording and `max_file_bytes` naming reconciliation for the E10-T3 documentation-truth pass, and the recommended prune-interleaving store test). Structured extraction was reports_only in both rounds; the accepted reports remain authoritative. Changelog 1.1.2.

## E10-T2: Effective Watchman Binding and Route-Relative Exclusions

**Status:** Completed
**Design Gate impact:** Not required; the source contract owns the behavior.

### Objective

Make nested configured resources safe when Watchman returns an ancestor and
make exclusion semantics identical across every managed lifecycle command.

### Deliverables

- persisted configured root, actual root, relative root, and trigger identity;
- subtree-constrained trigger installation and environment validation;
- one resolver shared by install, status, test, and remove;
- exact/recursive file and directory exclusions applied before reads and fan-out;
- drift reporting and proof that successful removal leaves no managed trigger.

### Requirements

`SRC-009` through `SRC-012`, `PTH-009`, `OPS-010`, `TST-010`

### Dependencies

E10-T1 Completed.

### Acceptance

- AC-601 through AC-603 pass with a real disposable nested Watchman tree;
- out-of-root and excluded changes create no downstream record;
- `make verify` green.

### Evidence

Delivered as the effective Watchman binding and route-relative exclusions: schema v9 persists the four-part managed binding per route (`watch_bindings`; `TestE10T2WatchBindingPersistence` pins the upsert, round-trip, replace-whole, and typed not-found arms), resolved by the one server resolver install and status share (`resolveServerBinding`), read by remove from the persisted record plus the live watch list, and reported by `watchman test --route` from its logical root with no server contact (`TestE10T2AncestorRootBindingLifecycle` drives the whole set against the real Watchman over a disposable nested tree: the ancestor is watched first so the configured nested root binds ancestrally, the installed definition carries `relative_root` `workspace/vault`, the persisted record round-trips, status reports the identical binding and patterns, test reports the persisted record, and remove proves the managed trigger absent on every watched root including a stray copy planted on a second watched root — AC-601 and AC-603; `TestE10T2DriftDetection` pins the OPS-010 drifted state after the recorded topology moves). Installation subtree-constrains the trigger through `relative_root` (`TestE10T2ManagedTriggerSubtreeConstraint`), a reinstall after the watch moved removes the stale managed trigger from the previous actual root, and `IsWatched` treats a root nested under a watched ancestor as watched with the filesystem-root watch covering everything (`TestE10T2CoversRoot`). The dispatch-side binding validation accepts the frozen-evidence environment form — WATCHMAN_RELATIVE_ROOT as the subdirectory's absolute path (trigger-invocation-environment corpus) — plus the persisted relative form, both only through the exact persisted binding, so a forged or drifted ancestor pair fails closed (`TestE10T2ValidateBindingAncestorFailsClosed` at the adapter, `TestE10T2DispatchValidatesAncestorBinding` end to end: no binding refuses, the exact binding dispatches, drift refuses again). Exclusions gained exact-directory semantics — a pattern matching a path prefix at a segment boundary excludes the whole subtree — alongside exact files, file globs, and recursive directories, all configured-root-relative and evaluated before any read (`TestE10T2ExclusionFormsConfiguredRootRelative`, `TestE10T2ExclusionRunsBeforeInclude`, `TestE10T2DirectoryExclusionBoundaries`; `TestE10T2ExcludedAndOutOfRootChangesCreateNoRecords` proves the excluded-only burst drops with no observation beyond the drop lineage, no intent, and no hash — AC-602), with the segment DP deduplicated behind one `matchRow` shared by the whole-path and directory-aware matchers. The real-Watchman tests drop their disposable watches on cleanup so repeated runs no longer exhaust FSEvent streams. Verified by `make verify` on darwin/arm64 (all checks green, test-race included) against the frozen Watchman 2026.07.27.00 baseline. Reviewed through two full-target Mulgae rounds (`r_01a03a5e-eb51-7f1c-a743-b616115d6d2c`, remediation-eligible: ci pass, coverage complete, publication committed, zero findings — its reports' verified in-scope observations were remediated in-tree: the frozen-evidence absolute WATCHMAN_RELATIVE_ROOT form the validation had rejected, the stale-trigger self-heal on the previous actual root after topology drift, status drifted outranking missing with the persisted binding surfaced on the not-watched path, the single shared binding loader, the deduplicated segment DP, the filesystem-root coverage fix, the CHANGELOG/configuration-spec/section-numbering documentation sync, and the FSEvent watch hygiene across every real-Watchman test; `r_01a03a7f-3731-749b-9f29-a517f6cc2ca7`, hardening-deferral-eligible: ci pass, coverage complete, publication committed, zero findings, every role confirming the remediations closed — the residual advisory observations carry to the E10 epic validation audit and the E10-T3 documentation-truth pass: the watch-binding row having no removal path at `watchman remove`, the remaining duplicate heading number in watchman-integration.md, the untested install-self-heal and status-drift arms, remove's same-name deletion across every watched root on a shared server, the textual configured-root drift comparison, the synthesized never-actual fallback binding, the install-persist-failure envelope, the three-site stored-to-effective mapping, the cli-spec's undocumented new surfaces, and the end-to-end real-trigger firing limitation the managed command's test-binary argv imposes). Structured extraction was reports_only in both rounds; the accepted reports remain authoritative. Changelog 1.1.3.

## E10-T3: Source and Reconciliation Integrity Gate G6

**Status:** Completed
**Design Gate impact:** Not required; this task validates the accepted design.

### Objective

Cold-validate the complete E10 delta and close G6 before configuration or
fan-out work begins.

### Deliverables

- executable AC-601 through AC-605 gate suite;
- fresh database migration and interrupted-upgrade coverage;
- real Watchman disposable-tree evidence and bounded concurrency evidence;
- synchronized source, persistence, operations, validation, and roadmap truth.

### Requirements

`SRC-009` through `SRC-012`, `PTH-009`, `DUR-013` through `DUR-015`, `OPS-010`, `OPS-012`, `TST-010`, `TST-011`

### Dependencies

E10-T2 Completed.

### Acceptance

- AC-601 through AC-605 pass without skips on the supported host;
- no high-or-higher correctness finding remains;
- `make verify` green and G6 evidence is recorded.

### Evidence

Delivered as the source and reconciliation integrity gate: the executable G6 suite drives every criterion through the real CLI surface — `TestG6AC601EffectiveBindingReported` (the effective binding over a disposable nested real-Watchman tree: configured root, actual ancestor root, relative root, subtree-constrained trigger, and the identical binding through status and `watchman test`), `TestG6AC602OutOfRootAndExcludedCreateNoRecords` (the excluded burst drops with zero intents and zero excluded-path digests while the in-scope burst under the same ancestor environment creates exactly one task), `TestG6AC603RemoveProvesAbsenceEverywhere` (a stray managed trigger planted on a second watched root; the removal proof re-lists every watched root), `TestG6AC604FencedReconciliation` (a real ingestion landing inside the enumeration window; either arm ends with the newer fact in the retry's stored snapshot, with the deterministic refusal pinned by the service-level trap and named in the VALIDATION table), `TestG6AC605BoundedHashingEvidence` (the growing file starts inside the bound so growth itself pushes the read past it; a digested file carries no evidence flags and an unknown digest carries exactly one evidence entry), and `TestG6FreshDatabaseMigration` (a brand-new state directory migrates to the v9 baseline on first operator use and the version surface reports the shipped schema range). The documentation truth synchronized: VALIDATION.md carries the Gate G6 evidence table with the member-task deterministic proofs named per criterion and the review residuals recorded for the epic audit, OPS-012 names the configured hash bound, the cli-spec documents the binding-aware watchman command surfaces, and the watchman-integration section numbering is canonical. The round-1 remediations also closed the suite's long-standing real-Watchman instability at its root: test-installed triggers no longer spawn nested full-suite runs (the trigger-shaped invocation of the test binary is a documented no-op through TestMain) and the WatchDelete response-field fix makes every real-Watchman test drop its disposable watch — a full cli+watchman suite run now leaves zero residual watches and completes in a fraction of the degraded time. Verified by `make verify` on darwin/arm64 (all checks green, test-race included) against the frozen Watchman 2026.07.27.00 baseline. Reviewed through two full-target Mulgae rounds (`r_01a03ab4-ebd8-73a0-979e-54b1c037c393`, remediation-eligible: ci pass, coverage complete, publication committed, zero findings — its reports' verified in-scope observations were remediated in-tree: the architecture docs' stale hash-bound name and pre-remediation fence wording, the roadmap header arithmetic, the near-vacuous AC-605 postcondition replaced by the exact either-arm enforcement, the growing-file leg moved inside the bound, the gate suite's duplicated helpers unified with the e10t2 lifecycle helpers, and the fixture root resolved through the configuration loader; `r_01a03abf-6374-7784-adeb-05b54b20712f`, hardening-deferral-eligible: ci pass, coverage complete, publication committed, zero findings — the residual advisory observations carry to the E10 epic validation audit: the two surviving published `max_file_bytes` occurrences and the stale VALIDATION SOT-version line, the cli-spec's `not_watched` field-shape wording, the AC-604 concurrent-writer goroutine's swallowed failure modes, the watch-del confirmation parsing lacking a server-free assertion, the TestMain placement and its argv-shape duplication, and the ambient-environment-sensitive gate registration helper). Structured extraction was reports_only in both rounds; the accepted reports remain authoritative. Changelog 1.1.4.

---

# E11: Hermes Preflight and Operator Setup

**Epic status:** Completed
**Purpose:** Replace hand-authored/exact-version assumptions with a safe public-interface preflight and guided disabled setup.
**Gate:** G7

## E11-T1: Config v1 Destination Cutover and Forward Migration

**Status:** Completed
**Design Gate impact:** Not required; D-025 owns the clean cutover.

### Objective

Implement the approved `destinations[]` configuration foundation without a
legacy compatibility loader while preserving database evidence.

### Deliverables

- config schema, Go types, validation, normalized display, revision projection, and example updated for destinations, conditions, and notifications;
- duplicate destination, empty workstream, unsupported fanout mode, and ambiguous mutation rejection;
- actionable legacy `dispatch` refusal and atomic destination config mutation;
- forward migration/backfill, unresolved-legacy enable block, backup, and rollback rehearsal.

### Requirements

`OPS-014`, `OPS-015`, `DAT-012`, `DAT-013`, `FAN-001`, `FAN-005`, `FAN-006`, `FAN-011`, `FAN-012`, `CLI-015`

### Dependencies

E10-T3 Completed.

### Acceptance

- legacy config fails with the exact regeneration path;
- the v0.1.5 example validates and remains disabled;
- historic task and receipt references remain queryable after upgrade;
- `make verify` green.

### Evidence

- `TestE11T1LegacyDispatchRefusedWithRegenerationPath` and
  `TestE11T1LegacyKanbanTargetUnderTargetsRefused`
  (`internal/config/e11t1_test.go`): both retired v0.1.4 shapes refuse
  loading with the exact regeneration path naming `agent-dispatch init`,
  the `destinations[]` shape, and `hermes_targets`; no conversion exists.
- `TestParseGoldenExample` (`internal/config/config_test.go`): the
  v0.1.5 example validates against the shipped schema with one hermes
  target, one webhook target, and a disabled route;
  `TestE11T1MutateDestinationAtomic` proves the CLI-015 atomic
  destination-qualified write (validated candidate, preserved mode,
  rejected candidate leaves the file untouched).
- `TestE11T1CutoverMigrationPreservesHistoryAndRecordsContract`
  (`internal/adapters/sqlite/e11t1_test.go`): after the v10 cutover the
  historic intent and quarantine rows stay queryable (DAT-012), the
  `contract_state` marker records the generation, and the pre-migration
  backup opens as a restorable v9 database with the same history
  (OPS-015 rehearsal);
  `TestE11T1UnresolvedLegacyWorkCountsForeignRevisionRows` and
  `TestE11T1UnresolvedLegacyWorkCoversInFlightStates`
  (`internal/adapters/sqlite/e11t1_test.go`) and
  `TestE11T1EnableBlockedByUnresolvedLegacyWork`
  (`internal/cli/e11t1_test.go`) prove the DAT-013 enable refusal,
  its coverage of the in-place retry and crashed-submit states, and
  its resolution through the documented operator exits.
- `TestE11T1FanoutOrderNeverSemantic`, `TestE11T1DestinationContractRejections`,
  `TestE11T1TargetMapClashRejected`, `TestE11T1HermesTargetFloorValidation`,
  `TestE11T1WebhookEndpointQueryIsRevisionSensitive`, and
  `TestE11T1HermesDestinationRequiresProfile` pin FAN-001/005/006/012,
  the eligibility floor, the target-map ambiguity rejection, and the
  endpoint commitment in the revision digest.
- `make verify` green on darwin/arm64 (all checks, including test-race,
  manifest, schema/example validation, and traceability); Gaori-routed
  manifest-check, schema-validation, and traceability passed.
- Mulgae member-task review: ordinal 1 (run
  `r_01a046c9-e6b1-7fd2-8855-d5814e530949`, ci pass, coverage complete,
  publication committed, zero findings) with its report-level advisory
  defects remediated; ordinal 2 (run
  `r_01a046e4-76f0-7008-8177-aba5c4a26330`, ci pass, coverage complete,
  publication committed, zero findings) with its report-level advisory
  defects remediated; ordinal 3 (run
  `r_01a04701-8ac9-7265-a0ea-aeec9fc1a918`, ci pass, coverage complete,
  publication committed, zero findings) — one explicitly disclosed
  extra round validating the final target after the round-two
  remediation. Round-three report-level observations are recorded for
  the epic audit: the `UnresolvedLegacyWork` query does not count
  foreign-revision `ready`/`submitting` intents (the in-place
  dead-letter retry and a crashed legacy submit bypass the enable
  refusal; the submit-path staleness gate still prevents silent
  submission), the installation guide's clean-host section still names
  the retired capability report, and the configuration-spec §12
  capability bullet needs webhook scoping.

## E11-T2: Hermes Capability Probe and Evidence Cache

**Status:** Completed
**Design Gate impact:** Not required; ADR-0017 is the approved design.

### Objective

Probe the current public Hermes surface by capability with no Hermes changes
and invalidate evidence whenever the executable or contract changes.

### Deliverables

- minimum-version eligibility with no maximum and strict version parsing;
- bounded probes for required Kanban JSON operations and profile-scoped skill table shape;
- capability v2 record/cache keyed by executable path/digest, version, and probe contract;
- `hermes probe` and `hermes capabilities [--refresh]` human/JSON output;
- route activation binding to the capability-evidence fingerprint.

### Requirements

`HER-011` through `HER-014`, `HER-018`, `SEC-004`, `SEC-014`, `TST-012`

### Dependencies

E11-T1 Completed.

### Acceptance

- the frozen real baseline and the installed newer Hermes traverse the same probe path;
- compatible later shapes pass and incompatible/malformed/over-bound shapes name the missing capability;
- an executable change blocks submission before side effects;
- Hermes source and private state remain untouched; `make verify` green.

### Evidence

- `TestE11T2ProbePassesFrozenInterface` and `TestE11T2SamePathFrozenAndNewer`
  (`internal/adapters/hermeskanban/e11t2_test.go`): the frozen real
  the baseline interface and a compatible newer Hermes traverse the same
  probe path (TST-012, AC-701/702 posture) with a stable
  fingerprinted record.
- `TestE11T2CacheInvalidation` and `TestE11T2SubmitBlocksOnExecutableChange`:
  the record invalidates on executable bytes, path, version, or
  probe-contract change (HER-013), and a submission after an
  executable change is definite_not_submitted with the probe
  remediation — never a side effect (AC-703).
- `TestE11T2CreateSurfaceDrift` and `TestE11T2SkillTableParser`: a
  create surface that dropped required flags fails the probe naming
  the exact missing capability (a lone `--mutex-key` loss downgrades
  resource_mutex), and the skill-table parser accepts the documented
  five-column table while refusing foreign headers, wrapped rows,
  unknown statuses, and absent tables (SEC-014, HER-014).
- `TestE11T2HermesProbeWritesCache`, `TestE11T2HermesCapabilitiesCacheAndRefresh`,
  and `TestE11T2HermesCapabilitiesRefusesIncompleteEvidence`
  (`internal/cli/e11t2_test.go`): the `hermes probe` and
  `hermes capabilities [--refresh]` envelope surface, the owner-only
  cache write, transparent staleness re-probe, and the
  `config_capability_missing` refusal naming the missing capability.
- `TestE11T2ActivationRecordsFingerprint`: the production enable binds
  the probe record's fingerprint beside the acknowledged revision
  (schema v11), equal to the cached evidence (HER-018).
- Mulgae member-task review: ordinal 1 (run
  `r_01a04800-6157-7086-9483-180e5d6c775a`, ci pass, coverage
  complete, publication committed, zero findings) with its seven
  report-level defects remediated (refresh and profile threading,
  profile-scope invalidation, fingerprint preservation on
  outage re-acknowledgement, binding-degradation warning, one
  DeriveFingerprint, end-to-end swap and scope tests); ordinal 2 (run
  `r_01a0481e-88e1-7db8-9a3c-f20c101781a4`, ci pass, coverage
  complete, publication committed, zero findings) confirmed the
  remediations; its three report-level low/info advisories
  (load-failure warning branch, unverifiable-digest staleness,
  probe --refresh usage error) were remediated after capture and are
  covered by the whole-epic audit.
- `make verify` green on darwin/arm64; Gaori manifest-check,
  schema-validation, and traceability passed; the real installed
  Hermes exercises the probe through the G5 clean-host walkthrough.

## E11-T3: Destination Profile and Skill Preflight

**Status:** Completed
**Design Gate impact:** Not required; ADR-0017 owns the boundary.

### Objective

Prove that every configured destination can be executed by its selected
on-disk Hermes profile with all required skills before enablement.

### Deliverables

- `hermes profiles`, `route preflight`, destination-qualified `set-profile` and `set-skills`;
- typed profile enumeration and strict enabled-skill inventory parsing;
- bounded sorted alternatives and actionable remediation;
- preflight coverage for target, board, profile, skills, workspace, mutex, hints, notification sinks, and Watchman binding.

### Requirements

`HER-015` through `HER-017`, `CLI-010`, `CLI-011`, `OPS-013`

### Dependencies

E11-T2 Completed.

### Acceptance

- missing profiles and skills block before task creation;
- destination-qualified edits change only their target config and pause the route revision;
- ambiguous route-only mutation fails usage; `make verify` green.

### Evidence

- `TestE11T3HermesProfiles` (`internal/cli/e11t3_test.go`): the public
  profiles list with on-disk status through the typed assignees
  surface (CLI-010, HER-015).
- `TestE11T3PreflightPassesAndBlocks`: a complete destination passes
  every check; a missing profile blocks at exit 3 with the sorted
  on-disk alternatives and the set-profile remediation in the error
  envelope's result slot; a required skill the profile does not enable
  blocks naming it; no task is created (HER-016/HER-017, AC-704/705
  posture).
- `TestE11T3SetProfileQualifiedAndAmbiguity` and
  `TestE11T3SetSkillsQualified`: the destination-qualified edits write
  atomically, change only their destination, pause the route revision,
  and reject the rejected candidate without touching the file; the
  qualifier may be omitted only with exactly one destination, and the
  ambiguous route-only edit is a usage error naming the declared set
  (CLI-011).
- `TestE11T3StatusSurfacesDrift`: `status` reports the capability,
  watchman, profile, skill, and reconciliation drift classes per route
  (OPS-013), with the capability class proven against a drifted
  executable after a production-gate enable.
- Mulgae member-task review: ordinal 1 (run
  `r_01a04846-3dd7-7035-bedb-2e6f510f533d`, ci pass, coverage
  complete, publication committed, zero findings) with its report-level
  defects remediated after capture — the mutation path's dropped
  `--config=<path>` equals form (previously validated and wrote the
  default config instead of the named file), the post-mutation reload
  error now on stderr, the healthy zero-skill skill table recording an
  empty non-nil inventory so an absent field unambiguously means the
  shape failed, the cli-spec command tree and `hermes profiles
  [--target <id>]` surface sync, and the status drift enumeration —
  covered by the whole-epic audit together with the new equals-form
  test.
- `make verify` green on darwin/arm64; Gaori manifest-check,
  schema-validation, and traceability passed.

## E11-T4: Discoverable CLI, Guided Setup, Operator Skill, and G7

**Status:** Completed
**Design Gate impact:** Not required; this task delivers the operator contract.

### Objective

Make the disabled production gate reachable through discoverable help, a
guided setup flow, and a distributable operator skill.

### Deliverables

- root/group `-h` and `--help`, examples, side effects, approvals, exits, completion guidance, and non-null JSON collections;
- interactive `setup wiki` through disabled config, probe, preflight, Watchman test, reconciliation, and gate summary;
- versioned `agent-dispatch-operator` skill and installation/compatibility documentation;
- executable AC-701 through AC-706 gate suite and synchronized G7 evidence.

### Requirements

`BND-003`, `BND-004`, `CLI-009` through `CLI-012`, `CLI-014`, `HER-011` through `HER-018`, `TST-012`

### Dependencies

E11-T3 Completed.

### Acceptance

- a new operator completes setup without direct SQLite or Watchman commands;
- setup stops before enablement and prints the exact production-gate action;
- the operator skill changes neither Hermes core nor production state;
- AC-701 through AC-706 and `make verify` pass; G7 evidence is recorded.

### Evidence

- `TestG7AC706HelpAndSetupReachDisabledGate` (`internal/cli/g7_test.go`):
  the root and every group parser answers -h/--help at exit 0 with the
  CLI-009 discovery contract (flags, defaults, output modes, exit
  codes, side effects, approvals, an example, and the next safe
  command), and `setup wiki` walks the six steps to the printed
  production-gate command, leaving the route disabled — no direct
  SQLite or Watchman commands anywhere in the flow.
- `TestG7AC701SameProbePathBothInterfaces` through
  `TestG7AC705DisabledSkillFailsClosedWithAlternatives`: the frozen
  the baseline and a newer Hermes traverse the same probe path (AC-701), the
  drifted create surface names its missing flags (AC-702), the stale
  read re-proves the live executable (AC-703), and the missing-profile
  and disabled-skill refusals list their sorted alternatives (AC-704,
  AC-705); `TestG7NonEmptyJSONCollections` pins CLI-014.
- The packaged `agent-dispatch-operator` skill
  (`docs/skills/agent-dispatch-operator/`) carries its versioned
  SKILL.md and INSTALL documentation and changes neither Hermes core
  nor production state; the Gate G7 evidence table lands in
  VALIDATION.md.
- Mulgae member-task review: ordinal 1 (run
  `r_01a04868-3f05-755b-a118-8bd055f0c042`, ci pass, coverage
  complete, publication committed, zero findings) with its report-level
  defects remediated after capture — setup's dropped global options
  for nested steps, the enabled-base draft failing every re-run, the
  discarded vault-root prompt on an existing base, the dead
  Watchman-status branch, the unactionable CLI-014 alternatives
  assertion, the cli-spec/help setup wording claiming selection and
  install steps the implementation does not perform, the root
  exit-code list missing registry codes 12 and 13, and the four-group
  help pin — replaced by the registry-derived completeness guard and
  the AC-702 own-body arm; covered by the whole-epic audit.
- `make verify` green on darwin/arm64; Gaori manifest-check,
  schema-validation, and traceability passed.

---

# E12: Multi-Destination Lifecycle

**Epic status:** Completed
**Purpose:** Turn one normalized event into independently durable destination work while retaining latest-state bounds.
**Gate:** G8

## E12-T1: Aggregate Event and Destination Child Persistence

**Status:** Completed
**Design Gate impact:** Not required; ADR-0016 and the record contracts own the design.

### Objective

Introduce aggregate event, destination revision, and child dispatch identities
with deterministic independent idempotency and preserved historical lineage.

### Deliverables

- versioned aggregate-event, destination-revision, and child-dispatch records, schemas, examples, repositories, and migrations;
- canonical destination revision and DAT-014 child idempotency projection;
- aggregate-to-child creation transaction and inspectable selection evidence;
- migration and canonical-order property tests.

### Requirements

`DAT-010` through `DAT-014`, `FAN-001` through `FAN-003`, `FAN-010`, `FAN-012`

### Dependencies

E11-T4 Completed.

### Acceptance

- one selected destination produces one child beneath one aggregate event;
- configuration order never changes revision, selection, or idempotency;
- historical records remain queryable; `make verify` green.

### Evidence

Migration v12 (`aggregate_events`, `destination_revisions`,
`child_dispatches`) with the atomic aggregate-to-child creation across
arrival, follow-up, rerun, rebuild, and reconcile paths, the DAT-014
child idempotency projection (`agent-dispatch:v2:`), and the
content-addressed destination-revision records are pinned by
`internal/adapters/sqlite/e12t1_test.go`,
`internal/config/e12t1_test.go`, and the schema/example contracts under
`docs/schemas/`; `make verify` green at the task commit.

## E12-T2: Per-Destination Lanes, Conditions, Fan-Out, and Retry Isolation

**Status:** Completed
**Design Gate impact:** Not required; ADR-0016 is the approved concurrency model.

### Objective

Run selection, single-active coordination, dirty collapse, submission, retry,
and reconciliation independently for each destination lane.

### Deliverables

- deterministic closed condition evaluator with documented OR/AND semantics;
- per-destination leases, active slot, dirty generation, and follow-up collapse;
- independent child submission/drain/retry/rerun behavior;
- trusted request rendering with destination profile, skills, workstream, workspace, mutex, and hints;
- multi-process sibling-isolation and destination-change tests.

### Requirements

`CON-001`, `CON-007` through `CON-010`, `FAN-002` through `FAN-009`, `FAN-011`, `FAN-012`, `SEC-010`

### Dependencies

E12-T1 Completed.

### Acceptance

- different and repeated profiles receive distinct eligible workstreams;
- one child's failure or retry cannot block or duplicate a sibling;
- destination behavior changes require new revision acknowledgement;
- `make verify` green.

### Evidence

Migration v13 (`destination_lane_state`) re-keys the single-active slot,
dirty generation, and follow-up collapse onto `(route_id, destination_id)`
lanes with the route envelope and route-level QUARANTINED/UNCERTAIN holds
preserved; the closed condition evaluator, lane-scoped follow-up collapse,
fan-out failure surfacing, retry isolation, sibling-isolation,
destination-change, differing-target, and no-selection behaviors are pinned
by `internal/app/dispatch/selection_test.go`, `internal/adapters/sqlite/e12t2_test.go`,
`internal/app/dispatch/e12t2_test.go`, and `internal/cli/e12t2_test.go`;
`make verify` green at the task commit. The two residuals originally
recorded for the epic validation — reconcile-path fan-out committing one
child on the canonically-first lane, and follow-up lane filtering
evaluating conditions per change while arrival selection evaluated per
occurrence — are remediated by the E12 epic-validation hardening batch
(CHANGELOG 1.1.13): the reconcile path now fans out per certified lane
under one shared aggregate, and migration v15's merge-selection evidence
makes the follow-up filter occurrence-level with a per-change fallback
for legacy rows.

## E12-T3: Work Receipt v2, Aggregate Status, and Completion Evidence

**Status:** Completed
**Design Gate impact:** Not required; the feedback and record contracts own the behavior.

### Objective

Represent the four worker outcomes without confusing Hermes acceptance,
execution status, or aggregate presentation with completed work.

### Deliverables

- `work-receipt/v2` schema/example/validation and migration compatibility;
- completed, partial-follow-up, blocked/manual, and failed/budget transitions;
- receipt retrieval/association with the exact child;
- `events show` and expanded status with separate child projections;
- worker task contract updates without installing or modifying Hermes.

### Requirements

`FBK-009` through `FBK-012`, `DAT-011`, `FAN-010`, `CLI-013`, `OPS-011`

### Dependencies

E12-T2 Completed.

### Acceptance

- partial and blocked follow the D-025 policy exactly;
- acceptance or terminal status without receipt never renders completed;
- aggregate status names every child and evidence gap; `make verify` green.

### Evidence

Migration v14 widens the work-receipt status contract to the four worker
outcomes with completed/remaining scope and manual-reason columns while
v1 rows stay valid; the partial, blocked, and failed transitions, the
full-document v2 submission form, receipt association with the exact
child (destination identity on receipt views), `events show` with
separate per-child destination/acceptance/execution/receipt/retry
projections and the actionable completion-evidence gap, status lane
summaries, and the worker task contract updates are pinned by
`internal/adapters/sqlite/e12t3_test.go`,
`internal/app/workreceipt/e12t3_test.go`, and `internal/cli/e12t3_test.go`;
`make verify` green at the task commit. The two residuals recorded for
the epic validation — digest-less remaining-scope entries classifying as
deletions in the follow-up projection, and the document-vs-flag scope
conflict comparing struct order — are remediated by the E12
epic-validation hardening batch (CHANGELOG 1.1.13): a digest-less entry
now classifies conservatively as a modification (deletion evidence
demands the exact before-present/after-absent pair), and the scope
comparison canonicalizes by value over a sorted order (both
partial-outcome surfaces only).

## E12-T4: Multi-Destination and Completion Gate G8

**Status:** Completed
**Design Gate impact:** Not required; this task validates the accepted design.

### Objective

Cold-validate all fan-out, concurrency, retry, revision, receipt, and aggregate
semantics before notification work begins.

### Deliverables

- executable AC-801 through AC-806 gate suite;
- deterministic fake-sink stress and isolated public-Hermes walkthrough;
- migration, crash, race, and schema/example validation;
- synchronized G8 documentation and traceability evidence.

### Requirements

`FAN-*`, `CON-001`, `CON-007` through `CON-010`, `FBK-009` through `FBK-012`, `TST-013`

### Dependencies

E12-T3 Completed.

### Acceptance

- AC-801 through AC-806 pass;
- no sibling duplication or cross-lane blocking is observed under concurrency;
- no high-or-higher finding remains; `make verify` green and G8 closes.

### Evidence

The executable AC-801 through AC-806 gate suite
(`internal/cli/g8_test.go`) passes: two-profile fan-out beneath one
aggregate, same-profile distinct workstream identities, failed-lane
retry reusing the completed sibling without duplication,
destination-edit re-acknowledgement, the four receipt outcomes, and the
actionable acceptance-without-receipt gap; the concurrent per-lane
stress test observes no sibling duplication or cross-lane blocking
(cross-process evidence cited from the G2 suite and the AC-207
interrupted-upgrade loop through schema v14); the isolated public-Hermes
walkthrough runs where a supported Hermes is available and is recorded
as an explicit skip-guarded evidence gap on this host; the synchronized
G8 evidence table sits in VALIDATION.md and `make verify` is green at
the task commit. The round-two review residuals recorded as
epic-validation hardening candidates — the stale AC-805 inline comments,
the stub-heredoc control-character escaping, and the cleanup-defer
orphan note — are remediated by the E12 epic-validation hardening batch
(CHANGELOG 1.1.13): the gate suite's budget docstrings state the
corrected semantics, the stub escapes control characters before the
heredoc interpolation, and the cleanup defer names the redirected-HOME
condition it runs under.

---

# E13: Notifications and v0.1.5 Release

**Epic status:** Completed
**Purpose:** Complete operator-visible notification delivery, distributable skills, and release proof.
**Gate:** G9

## E13-T1: Notification Event Contract and Transactional Outbox

**Status:** Completed
**Design Gate impact:** Not required; ADR-0019 is the approved design.

### Objective

Create durable deduplicated notification work from configured state transitions
without coupling delivery to dispatch or completion state.

### Deliverables

- notification-event and notification-attempt schemas, examples, repositories, and migrations;
- transition-to-event mapping, default event policy, policy revision, and dedup key;
- transactional outbox insertion and independent attempt lifecycle;
- crash, replay, pruning, and referential-integrity tests.

### Requirements

`DUR-016`, `DAT-010`, `NTF-001` through `NTF-005`, `NTF-007`, `OPS-013`

### Dependencies

E12-T4 Completed.

### Acceptance

- every configured transition creates at most one logical notification per sink;
- a crash before or after delivery cannot rewrite task state;
- unresolved notification evidence is retained and inspectable; `make verify` green.

### Evidence

Completed 2026-08-30. SQLite migration v16 creates the durable outbox
(`notification_events`, `notification_attempts`): the notification
identity is the deterministic five-component projection (event, optional
destination, transition occurrence, sink, notification-policy revision —
NTF-003), so the unique key collapses a replayed transition or aggregate
rerun onto its existing row (AC-902 shape). The reportable transitions
enqueue inside their owning transactions (DUR-016): lane completions map
onto work_completed/work_failed/work_exhausted by the resulting lane
state, the expired-lease recovery and a recorded unknown attempt enqueue
delivery_unknown, the quarantine hold enqueues quarantined, and every
pending-reconcile appearance enqueues reconciliation_required exactly
when the route was not already pending (OPS-013). The policy resolver is
injected from the loaded configuration and nil-disabled (NTF-001); the
default event set applies when a sink exists and no event list is
declared (NTF-002); the notification-policy revision digests exactly the
effective event set and sink identity references. Attempts are separate
durable records whose outcomes never rewrite dispatch, lane, receipt, or
work state (NTF-005); resolved notification evidence prunes past
retention while pending evidence stays retained and inspectable
(NTF-004). The `notification-event/v1` and `notification-attempt/v1`
schemas and examples are validated (`e13t1_test.go` in sqlite, config,
and cli: transactional creation, per-sink fan-out, dedup, attempt
lifecycle and source-state immutability, emission points, referential
integrity, v15→v16 upgrade, pruning, and an end-to-end `work complete`
walkthrough with a hostile note body provably absent from the payload).
`make verify` green at SOT 1.1.14.

## E13-T2: Notification Sinks, Retry Commands, and Scheduling

**Status:** Completed
**Design Gate impact:** Not required; the sink and security contracts own the behavior.

### Objective

Deliver channel-neutral notifications through safe webhook and structured-log
sinks with bounded automatic and explicit retry.

### Deliverables

- structured stdout/log and HTTPS webhook adapters;
- secret-reference resolution, stable idempotency header, no proxy/redirect, and bounded payload/response/time;
- `notifications test|list|retry|drain` and one-shot schedule integration;
- redaction, ambiguous transport, duplicate, and sink-isolation tests.

### Requirements

`NTF-004` through `NTF-009`, `CLI-013`, `SEC-011` through `SEC-013`, `TST-014`

### Dependencies

E13-T1 Completed.

### Acceptance

- notification test creates no source event or Hermes task;
- retry preserves notification identity and never changes child outcome;
- payloads and logs contain no body or secret; `make verify` green.

### Evidence

Completed 2026-08-30. The structured log sink emits each stored
notification-event/v1 payload as one JSON line on stderr (stdout keeps
the one-envelope contract) and the authenticated HTTPS webhook sink
(internal/adapters/notificationsink) enforces the strict transport
posture: https-only endpoints, send-time secret resolution with
redaction from every diagnostic, a dedicated collision-free idempotency
header carrying the stable ntfidem- identity on every attempt, redirects
as definite routing refusals, no ambient proxy, and bounded
payload/response/time (SEC-011..013, NTF-006/NTF-007). The
`notifications test|list|retry|drain` surface ships (CLI-013): the
probe stores nothing and touches no source event or Hermes task
(NTF-008), the listing joins the attempt projection (NTF-004), the
explicit retry re-arms a refused notification and refuses a delivered
one at exit 4, and the drain evaluates the configured drift classes per
notification-enabled route (watchman drift and the integration classes
enqueue exactly once per appearance through the finding-digest
occurrence, OPS-013) before one bounded attempt per pending
notification with outcomes as data. `status` projects the notification
by-state counts with a pending-delivery warning; the launchd schedule
example chains the drain after the scheduled reconciliation. Coverage
(e13t2_test.go in notificationsink and cli): every status class, secret
redaction, stable keys across retries, fail-closed construction, the
probe's nothing-created posture, ambiguous-then-delivered retry with
identical keys, refused re-arm through the explicit retry, sink
isolation, drift deduplication, and secret/content absence from
payloads, diagnostics, and ordinary output. `make verify` green at SOT
1.1.15.

## E13-T3: Operator and Worker Skills and Operational Walkthrough

**Status:** Completed
**Design Gate impact:** Not required; the skills implement existing public contracts.

### Objective

Finalize both distributable skills and prove the complete workflow in isolated
state without modifying Hermes or production data.

### Deliverables

- versioned operator skill and updated wiki-maintenance worker skill with install and compatibility declarations;
- worker guidance for untrusted manifests, latest state, workstream scope, exclusions, and work-receipt/v2;
- isolated `HERMES_HOME`, disposable board/vault, real Watchman, two-destination walkthrough through notification;
- failure diagnosis, disable, trigger removal, notification retry, and rollback walkthrough.

### Requirements

`BND-003`, `BND-004`, `FBK-006`, `FBK-009` through `FBK-012`, `CLI-012`, `NTF-*`, `TST-012` through `TST-014`

### Dependencies

E13-T2 Completed.

### Acceptance

- AC-905 passes without touching production state or Hermes source;
- both skills are schema/manifest validated and installable by documented public mechanisms;
- disable and removal preserve history and leave no managed trigger; `make verify` green.

### Evidence

Completed 2026-08-30. The operator skill ships as 2.0.0 with the v0.1.5
compatibility declaration and the notification operational guidance
(drain posture, probe guarantee, explicit retry, configuration-owned
sinks, history-preserving exits); the worker skill ships as 1.2.0 with
the scope-discipline guidance (workstream scope, exclusions as the
operator's occurrence-level decision) beside its untrusted-manifest,
latest-state, and work-receipt/v2 rules. `internal/cli/g9_test.go`
proves AC-905 deterministically over the two-destination fixture with
both notification sinks wired: detection fans out to both lanes, both
runs complete through the work-receipt surface, and the configured
notification delivers the completion to both sinks with the payload
proven free of note bodies and credentials; the lifecycle walkthrough
covers failure diagnosis, the refused notification's explicit retry,
disable and managed-trigger removal with the durable and audit history
preserved, and the quiesced system answering every inspection; the
packaging test pins the versioned guidance, the manifest coverage, and
the operator skill's documented public install into a disposable
Hermes profile. The isolated real-Hermes leg (disposable board,
redirected HOME, real Watchman binding, detection through receipt to
the delivered notification, managed-trigger removal) is skip-guarded
under the documented TST-007 posture where the installed Hermes create
surface drifts from the frozen baseline flags. `make verify` green at SOT
1.1.16.

## E13-T4: Documentation Truth, Release Proof, and v0.1.5

**Status:** Completed
**Design Gate impact:** Not required; this task is the release closeout.

### Objective

Reconcile every v0.1.5 claim with executable evidence and produce the local
release candidate from one final clean tree.

### Deliverables

- complete G6-G9 acceptance matrix and requirement traceability;
- current schemas/examples/skills, README, VALIDATION, changelog, release checklist, and v0.1.5 release notes synchronized;
- whole-release review with all material findings dispositioned;
- two byte-identical `make release VERSION=v0.1.5` builds, SHA256SUMS, and local v0.1.5 tag;
- explicit no-push, no-production-enable, and no-Hermes-change handoff.

### Requirements

`FAN-*`, `NTF-*`, `TST-009` through `TST-014`, all D-025-added `SRC-*`, `PTH-*`, `DAT-*`, `DUR-*`, `CON-*`, `HER-*`, `FBK-*`, `CLI-*`, `SEC-*`, and `OPS-*` requirements

### Dependencies

E13-T3 Completed.

### Acceptance

- AC-901 through AC-906 and every cumulative prior gate pass;
- every status surface agrees on SOT, release, epic, and task counts;
- release artifacts are byte-identical and the local tag names the final tree;
- no push or production activation occurs.

### Evidence

Completed 2026-08-30. The G6-G9 acceptance matrix is complete: the G9
rows in VALIDATION.md carry their executable evidence (the E13-T1
outbox suite, the E13-T2 sink and CLI suites, and the E13-T3 G9
walkthrough), the G6-G8 rows stand as verified history, and the
traceability matrix is regenerated at 75 tasks. Every status surface
agrees at 75/75: README SOT 1.1.18 with the v0.1.5 local-candidate
posture, VALIDATION with its G9 and release-proof sections, CHANGELOG
1.1.18 (the audit batches included), the closed v0.1.5 release-checklist
addendum, the v0.1.5
release notes, and this roadmap. Two consecutive
`make release VERSION=v0.1.5` builds from the release commit are
byte-identical with `dist/SHA256SUMS` recording the artifact digest;
the local `v0.1.5` tag names the final tree. No push, no hosted
release, and no production activation occurred, and no Hermes source
or private state was touched: the handoff is the local candidate with
VALIDATION.md as its evidence, for the operator's own hermes-side
verification before any publication.

---

# E14: Guided Setup and Disabled Baseline

**Epic status:** Completed
**Purpose:** Make the disabled Wiki setup path route-correct, safely rerunnable, and explicit about every production-gate state.
**Gate:** G10
**Canonical Outcomes:** [Guided setup and baseline requirements](../specs/required-spec.md) · [Reconcile and setup command contracts](../contracts/cli-spec.md) · [Gate G10 evidence](../VALIDATION.md) · [Setup and rollback architecture](../architecture/multi-destination-operational-loop.md)

## E14-T1: Explicit Setup Route Selection and Propagation

**Status:** Completed

### Objective

Select one setup route explicitly and carry that identity through every
route-scoped setup command and instruction.

### Deliverables

- `setup wiki --route <id>` and interactive multi-route selection;
- non-interactive ambiguity refusal;
- one shared selected-route step contract for preflight, Watchman, baseline,
  and enable guidance;
- help and regression coverage.

### Requirements

`CLI-009`, `CLI-012`, `CLI-016`, `OPS-016`, `TST-015`

### Dependencies

E13-T4 Completed; D-027 and ADR-0020 Accepted.

### Acceptance

- AC-1001 and AC-1002 pass;
- no multi-route setup silently chooses sorted-first;
- no route-scoped nested invocation can omit or change the selected route.

### Evidence

Completed 2026-08-31. `setup wiki` resolves one route explicitly before any
route-scoped step runs (CLI-016): `--route <id>` (and `--route=<id>`) names a
declared route, a single-route configuration auto-selects its only route, and
multiple routes require the flag or an explicit interactive numbered choice
whose empty or unreadable answer refuses the walkthrough at exit 2 — the
sorted-first fallback is gone. The nested `watchman status` step now carries
the selected route beside the preflight, reconciliation, install/test
guidance, and the final enable command (`internal/cli/setup.go`), the setup
help documents the flag, selection semantics, and exit codes (CLI-009), and
`internal/cli/e14t1_test.go` proves AC-1001/AC-1002 with negative assertions
against the unselected route plus both `--route` spellings. Review round 1
remediated three low findings; round 2 passed CI with complete coverage and
deferred two low findings (an architecture-doc contradiction and the valueless
trailing `--route` leniency) to epic hardening through the promoted
hardening-deferral evidence package.

## E14-T2: Disabled Baseline-Only Reconciliation and Persistence

**Status:** Completed

### Objective

Establish or refresh initial path facts while a route remains disabled without
creating production work or approval evidence.

### Deliverables

- public `reconcile --baseline-only` operation;
- route-baseline record and forward-only migration;
- atomic snapshot/baseline transaction under the observation fence;
- disabled/active-state guards and crash/restart tests.

### Requirements

`DUR-013` through `DUR-017`, `CLI-017`, `OPS-009`, `OPS-015`, `TST-002`, `TST-004`, `TST-015`

### Dependencies

E14-T1 Completed.

### Acceptance

- AC-1003 and AC-1004 pass;
- baseline creates zero decision, intent, task, acknowledgement, and notification rows;
- a failed or interrupted attempt is safely rerunnable.

### Evidence

Completed 2026-08-31. SQLite migration v17 creates the `route_baselines`
evidence table (one row per route, deliberately un-FKed so a clean host
baselines before any route row exists) and `ReplacePathFactsWithBaseline`
commits the clean-host resource registration, the observation-revision CAS
advance, the path-fact replacement, and the baseline upsert as ONE fenced
transaction that re-checks the runtime activation state inside the commit
(DUR-013/DUR-014/DUR-015/DUR-017, OPS-009; the shared `scopeWalker` gives
the baseline the same containment-defended enumeration as full
reconciliation). `reconcile --baseline-only` is the public CLI-017 surface
(guards on both gate halves refusing at exit 14, `--submit` mutual exclusion
at exit 2, typed `concurrent_change` outcome, cli-spec §9 documentation),
`BaselineService` maps the typed refusals, and the canonical snapshot digest
is the JSON-encoded sorted projection. Zero-production-row creation and
crash-window rerun convergence are proven by table-count assertions and the
crashbin die-before-write window (TST-002/TST-004); review round 1
remediated three low findings and round 2 published committed with complete
coverage, CI pass, and zero findings.

## E14-T3: Rerunnable Setup, Five-State Summary, and G10

**Status:** Completed

### Objective

Complete the guided flow across every non-production rerun posture and expose
an honest final production-gate summary.

### Deliverables

- clean-host, Watchman-installed, materialized-row, unchanged-rerun, and
  interrupted-rerun acceptance suite;
- configuration/runtime/Watchman/baseline/acknowledgement summary;
- exact enable-command rendering without execution;
- setup, installation, runbook, and operator-skill guidance.

### Requirements

`CLI-012`, `CLI-016`, `CLI-017`, `OPS-010`, `OPS-016`, `SEC-010`, `TST-015`

### Dependencies

E14-T2 Completed.

### Acceptance

- AC-1001 through AC-1005 pass;
- setup reaches the gate summary while both enable controls remain off;
- `make verify` is green.

### Evidence

Completed 2026-08-31. The walkthrough's fifth step is now the disabled-route
baseline (`reconcile --reason initial --baseline-only`): a clean host and every
rerun posture establish the snapshot instead of the retired advisory dry
reconciliation, and a genuine refusal stops the walkthrough before the gate
summary. The sixth step prints the honest five-state production-gate summary
(OPS-016, AC-1005) — configuration enabled, runtime activation, Watchman
binding, initial baseline, and production acknowledgement, each read from
current durable facts with explicit unreadable degradation — followed by the
exact reviewed enable command, printed never executed. `g10_test.go` proves
TST-015's five postures (clean host, Watchman-installed on a disabled
materialized row, unchanged rerun with exactly one baseline row, interrupted
rerun through the crashbin die-before-write window, and the exact-revision
enable rendering) with controls-off and zero-production-row assertions after
every posture; the documentation is promoted to the cli-spec setup contract,
the operator skill, the installation and runbook guidance, the setup help,
and the VALIDATION G10 evidence section. Review round 1 published committed
with complete coverage, CI pass, and zero findings; `make verify` is green.

---

# E15: Hermes v0.20.5+ Compatibility and Serialization Groups

**Epic status:** Completed
**Purpose:** Make Hermes v0.20.5 the supported floor and enforce an honest local concurrency guarantee with optional target-mutex defense-in-depth.
**Gate:** G11
**Canonical Outcomes:** [Serialization and concurrency requirements](../specs/required-spec.md) · [Serialization-group configuration contract](../contracts/configuration-spec.md) · [Hermes integration architecture](../architecture/hermes-integration.md) · [ADR-0021 serialization groups](../architecture-decision-records/0021-agent-dispatch-serialization-groups.md) · [Real-Hermes G11 evidence](../integrations/hermes-v0.20.5-g11-evidence.md) · [Gate G11 evidence](../VALIDATION.md)

## E15-T1: Serialization Configuration, Revisions, and Migration

**Status:** Completed

### Objective

Define stable Agent Dispatch serialization groups and make every concurrency
policy change production-gate visible.

### Deliverables

- destination `serialization_group`, deprecated `mutex_key` alias,
  resource-derived default, and route cross-group acknowledgement;
- exact group grammar, `resource:<resource_id>` default, and conflicting-alias
  refusal;
- effective-group resolution and same-resource topology validation;
- destination/route revision participation;
- group-state persistence and forward migration preserving historical work and
  blocking preserved active collisions.

### Requirements

`CON-010` through `CON-014`, `DUR-009`, `OPS-009`, `OPS-015`, `TST-002`, `TST-016`

### Dependencies

E14-T3 Completed; ADR-0021 Accepted.

### Acceptance

- serialization edits change both revisions and stale production acknowledgement;
- unsafe cross-group topology fails before submission;
- migration preserves existing dispatch, lane, and receipt identities and
  exposes a blocking conflict with no arbitrary holder until allowed
  existing-work exits leave at most one active child;
- AC-1108 passes.

### Evidence

Completed 2026-08-31. Every destination now resolves an effective
serialization group under CON-011 — explicit `serialization_group`, the
deprecated `mutex_key` alias (identical values warn, differing values fail,
ungrammatical alias values fail the group grammar), or exactly
`resource:<resource_id>` — with the 1–255 ASCII-byte grammar, no case
folding or Unicode normalization, and an explicit default-form value
intentionally joining the resource group (AC-1108). The resolved identity
and the route's `allow_cross_group_concurrency` acknowledgement join the
destination and route revision projections (CON-014), so a serialization
edit or acknowledgement flip pauses production acknowledgement; identical
effective groups hash identically across all three resolution paths.
`route preflight` fails the unacknowledged same-resource cross-group
topology before any probe with the acknowledgement remediation (CON-013)
and reports the persisted-conflict block, and the request assignment
carries the effective group for the renderer's complementary target mutex.
Migration v18 is additive and configuration-independent: the first topology
reconciliation (`reconcile`) materializes membership and recomputes each
group's slot state, a preserved active collision reports
`serialization_conflict` with no holder and a typed audit row, and the
allowed existing-work exits resolve it atomically to the sole survivor or
an open group while every dispatch, lane, and receipt identity stays
queryable. `e15t1_test.go` in config, sqlite (real files), and cli covers
resolution order, grammar bounds, alias agreement/conflict, revision
participation, topology acknowledgement, migration identity preservation,
conflict resolution, membership replacement with retire/recreate version
continuity, the persisted-conflict preflight gate, the reconcile
materialization wiring, and the effective-group wire posture; review
round 1 (remediation-eligible) published committed with complete
coverage and CI pass, and its six findings (audit transition-ID
collision on group retire/recreate, two coverage gaps, two silent
degradation paths, one dead test assertion) were verified valid and
remediated; `make verify` is green.

## E15-T2: Consistent Local Serialization and Optional Target Mutex Contract

**Status:** Completed

### Objective

Use one mandatory-local and optional-target-mutex decision across capability
probing, activation, command rendering, submission revalidation, and operator
surfaces, with Hermes 0.20.5 as the floor.

### Deliverables

- versioned capability evidence with effective serialization mode;
- probe/preflight/capabilities/status output for group-enforced,
  group-plus-target-mutex, and unsupported-unsafe modes;
- enable and submission topology gate;
- renderer suppression of unsupported `--mutex-key` and effective-group
  rendering when a later target supports it;
- atomic `hermes set-minimum-version` configuration update with omitted and
  below-floor fail-closed migration posture.

### Requirements

`HER-011` through `HER-021`, `SEC-004`, `SEC-010`, `CLI-010`, `CLI-019`, `OPS-011`, `TST-012`, `TST-016`

### Dependencies

E15-T1 Completed.

### Acceptance

- AC-1101 passes;
- AC-1107 passes;
- v0.20.5 without target mutex is the normal local-enforcement mode, not a
  contradictory failure;
- executable revalidation cannot change or bypass the certified mode.

### Evidence

Completed 2026-08-31. The product floor is exactly 0.20.5 (HER-011): an
omitted configured floor now fails closed naming the required explicit
declaration, a configured floor below 0.20.5 fails validation, and an
installed version below the configured floor keeps failing before any
side effect — never implicitly rewritten (AC-1107). The probe contract
advanced to v3 and every capability record certifies one effective
serialization mode (HER-020): agent-dispatch-group-enforced (the normal
0.20.5 posture without --mutex-key, a warn never a failure — AC-1101),
agent-dispatch-group-plus-target-mutex, or unsupported-unsafe, derived
authoritatively from the shape outcomes so a stored string can never
upgrade the certified mode. `hermes probe` and `hermes capabilities`
report the mode; preflight's serialization check names it; the submit
path drives the renderer's complementary mutex from it and executable
revalidation still re-proves the fingerprint the mode derives from, so
revalidation cannot change or bypass the certified mode. The atomic
`hermes set-minimum-version <target> <version>` helper (CLI-019)
accepts only floors at or above 0.20.5, updates only the selected
target through the validated atomic replacement, preserves every
unrelated route and target, and names each affected route's
before/after revision with the owed re-probe, preflight, and
re-acknowledgement; refused candidates leave the file untouched.
e15t2_test.go in hermeskanban and cli covers mode derivation (including
the hand-edited-record guard), the v3 contract invalidating v2 records,
the omitted/below-floor fail-closed postures, mode agreement across the
probe and capabilities surfaces, and the helper's refusals, atomic
update, affected-route pause, and untouched-file atomicity; the
configuration-spec floor statements, cli-spec command contract, config
and evidence schemas (v3 with the required serialization_mode), and the
example evidence document follow the new truth; review round 1
(remediation-eligible, r_01a05792-2c82-7a0e-be6d-b29d96078ca0)
published committed with complete coverage and CI pass, and its six
findings — the runbook and operator skill teaching the retired floor,
the error-ignoring bridge between the two floor definitions, the
architecture doc's old eligibility rule, the duplicated valued-flag
scan, the helper's inability to remediate a legacy below-floor or
omitted-floor document, and the overstated byte-preservation claim —
were verified valid and remediated (the repair path decodes without the
gates and re-validates the candidate against every gate before the
atomic replacement); review round 2
(r_01a057a7-4e22-7cdc-987f-259d342dd401) then published committed with
complete coverage, CI pass, and ZERO unresolved findings — the task is
Mulgae-approved outright with no deferral; `make verify` is green.

## E15-T3: Group Slot Enforcement and Bounded Follow-Up

**Status:** Completed

### Objective

Enforce one active child per serialization group across destination lanes while
retaining independent dirty generations and bounded progress.

### Deliverables

- transactional global group-slot acquisition, transfer, and release;
- occupied-group merge into destination dirty state;
- oldest-first deterministic one-waiter promotion and bounded remainder;
- retry, rerun, recovery, and multi-process concurrency tests.

### Requirements

`CON-001` through `CON-014`, `DUR-010` through `DUR-012`, `FBK-010`, `TST-004`, `TST-005`, `TST-016`

### Dependencies

E15-T2 Completed.

### Acceptance

- AC-1102 through AC-1105 pass;
- shared groups never run parallel children;
- acknowledged independent groups progress concurrently.

### Evidence

Completed 2026-08-31. The durable serialization_groups row is the ONE
slot: acquisition is transactional and CONDITIONAL — the write succeeds
only from the states the transaction's read authorized (open, or held
by this lane's own identity for the idempotent re-acquire and the
consumed promotion reservation), so a racing activation can never
overwrite the winner's hold; a group held by another lane reports
ErrGroupSlotHeld and the arrival merges into its lane's dirty
generation exactly like a lane-race loser, and a preserved conflict
refuses acquisition, rerun, and promotion outright. The arrival paths
pre-check the slot before persisting any intent, and the merge path's
reservation-consumption edge is itself group-gated — a reserved
dispatch whose group is occupied stays reserved instead of silently
becoming a second group child. Completion releases the slot in the same
transaction and promotes at most the oldest first-dirty waiting lane
(destination ID, then route ID, as the deterministic tie breaks) as a
reservation the promoted lane's next activation consumes; the releaser's
own follow-up waits behind it. A rerun TRANSFERS the slot to its
replacement atomically with the lane takeover, and retries retain the
slot through the retry lifecycle. e15t3_test.go proves AC-1102 through
AC-1105 with real SQLite files: shared-group single-child under an
eight-process arrival race across two routes (fresh connections, the
production one-shot shape, with the documented retryable-BUSY posture),
occupied-group merging, oldest-first promotion with the waiting
follow-up activating through its reservation, independent-group
concurrency, rerun transfer with conflict refusal, and the
conflict-blocked acquisition with the allowed existing-work exits
resolving the group to its sole survivor; review round 1
(r_01a057d5-edb9-7feb-9e85-976509c583f4, remediation-eligible) published
committed with complete coverage, CI pass, and ZERO unresolved findings —
the task is Mulgae-approved outright on the first round; `make verify`
is green.

## E15-T4: Hermes v0.20.5+ Compatibility Gate G11

**Status:** Completed

### Objective

Prove local enforcement against the deployed minimum Hermes release and the
optional target-mutex complement against a synthetic later public surface.

### Deliverables

- real Hermes v0.20.5 probe/preflight/render/submission transcript;
- burst, shared-group, and independent-group demonstrations;
- below-floor refusal and later target-mutex probe suites;
- removal of every exact previous-baseline reference from current tracked
  files without rewriting Git history or tags;
- capability, integration, runbook, and migration documentation.

### Requirements

`BND-003`, `BND-004`, `HER-019` through `HER-021`, `CON-011` through `CON-014`, `TST-007`, `TST-012`, `TST-016`

### Dependencies

E15-T3 Completed.

### Acceptance

- AC-1101 through AC-1108 pass;
- no Hermes core or private storage is modified;
- `make verify` is green.

### Evidence

Completed 2026-08-31. The real-environment walkthrough
(`docs/integrations/hermes-v0.20.5-g11-evidence.md`) ran the reviewed
binary against the installed Hermes Agent v0.20.5 over fully disposable
state created and discarded through the public CLI: the v3 probe
certified `agent-dispatch-group-enforced` (the only missing create flag
exactly `--mutex-key`), capabilities and preflight agreed on the mode
with the disposable profile proven on-disk and the `llm-wiki` skill
enabled through the public surfaces, two real tasks were durably
submitted whose objects carry no `mutex_key` field at all, a three-file
burst and a cross-route arrival on the occupied shared group merged
with no second child, an acknowledged independent group submitted
concurrently, and the durable group table held exactly one holder per
group. The floor-raise refusal (`set-minimum-version` to 0.21.0 →
preflight exit 3 before any side effect, restore → green) evidenced the
below-floor posture beside the frozen-interface suites for the 0.20.4
and later-target-mutex legs (TST-012: one probe path). Re-opening the real-environment
tests for the new baseline exposed and closed three real defects — the
reconciliation child's activation bypassing the group gate (now refused
in the same transaction, pinned by
`TestE15T4ReconcileChildRespectsOccupiedGroup`), the downtime
re-acknowledgement that could never submit again after an executable
swap (a partial probe is now the liveness posture; the capability
binding preserves for the same executable and retires explicitly via
`CapabilityFingerprintClear` on a swap), and post-cutover dead letters
wedging the recovery re-acknowledgement (the DAT-013 gate now keys on
the migration-v12 child-row legacy marker).
Every exact previous-baseline reference is removed from the tracked
files (current contracts, historical narratives, fixtures, and release
notes; Git history and tags untouched), the capability corpus and
interface report record the 0.20.5 baseline, the runbook gained the
v0.1.6 serialization upgrade sequence, and the VALIDATION G11 table maps
AC-1101 through AC-1108 to their evidence. No Hermes core or private
storage was modified. Review round 1 (r_01a05833-179c-7b81-8c01-e821e3dd8feb,
remediation-eligible) published committed with complete coverage and CI
pass, and its ten findings — the capability corpus's stale session
metadata and refuted-at-baseline resource_mutex claim, the fixture still
carrying the retired create shape, the silent partial-probe enable, the
defect-count mismatch, the default-floor comment contradiction, the
retired build date, the mutex-surface naming residue, and the two
transient-read binding-loss paths — were verified valid and remediated
(the corpus now records its dual session history honestly, the partial
enable warns and distinguishes downtime from a shape-incompatible
Hermes, and a transient store read preserves the binding); review round
2 (r_01a05860-57de-7f48-9307-fc219723d936) then published committed
with complete coverage, CI pass, and ZERO unresolved findings — the task
is Mulgae-approved outright; `make verify` is green.

---

# E16: Automatic Durable Notification Draining

**Epic status:** Completed
**Purpose:** Make notification delivery progress boundedly during normal one-shot operation without coupling it to source state.
**Gate:** G12
**Canonical Outcomes:** [required-spec.md](../specs/required-spec.md) (requirements) · [acceptance-criteria.md](../specs/acceptance-criteria.md) (G12 scenarios) · [configuration-spec.md](../contracts/configuration-spec.md) (drain contract) · [architecture-decision-records/0022-post-commit-notification-draining.md](../architecture-decision-records/0022-post-commit-notification-draining.md) (ADR) · [runbook.md](../operations/runbook.md) §9b (upgrade and rollback) · [VALIDATION.md](../VALIDATION.md) (gate evidence)

## E16-T1: Drain Policy, Leases, and Forward Migration

**Status:** Completed

### Objective

Define route-scoped manual, after-command, and scheduled policy and persist the
claim and run evidence automatic delivery requires.

### Deliverables

- drain mode, limit, preserve-pending policy, pending-age configuration, and
  persistent retry backoff;
- v0.1.5-compatible defaults and after-command generated Wiki default;
- fenced notification leases, due deadlines, immediately-due legacy pending
  migration, and drain-run records;
- schema/example/revision and round-trip coverage.

### Requirements

`DUR-016` through `DUR-018`, `NTF-010` through `NTF-016`, `OPS-009`, `OPS-015`, `TST-002`, `TST-017`

### Dependencies

E15-T4 Completed; ADR-0022 Accepted.

### Acceptance

- manual omission preserves v0.1.5 behavior;
- existing notification identities and attempts survive migration unchanged;
- migrated pending notifications are immediately due;
- every behavior-affecting policy field participates in a documented revision.

### Evidence

Completed 2026-09-01. The per-route `notifications.drain` block declares
mode (manual/after-command/scheduled), limit, preserve-pending failure
policy, pending-age warning window, and the all-or-nothing retry backoff,
each field optional with an explicit effective default (manual, 100,
preserve-pending, 1h, 30s/15m/2.0/0.2); an omitted block resolves to the
manual v0.1.5 behavior and newly generated Wiki configuration defaults to
after-command. The effective policy digests into its own inspectable
drain-policy revision covering every behavior-affecting field, joins the
route revision only when it differs from the pure default (so a v0.1.5
configuration without drain keeps its exact route revision), and never
joins the notification-policy revision — notification identities and
idempotency keys stay stable across drain changes. Migration v19 adds
due deadlines, fenced lease columns (owner, monotonic token, expiry), and
the drain-run evidence table; it rewrites no historic row, every existing
notification identity and attempt survives unchanged, and migrated
pending notifications backfill due-at to their creation time so they are
immediately due. Fresh intents are immediately due at creation. Covered by
focused config and storage tests (validation, revision partition,
round-trip, migration upgrade, due backfill, drain-run evidence).

## E16-T2: Lease-Safe Bounded Notification Delivery

**Status:** Completed

### Objective

Make manual and automatic drain share one bounded service that excludes
concurrent ownership, rejects stale owners, persists retry backoff, and recovers
process death safely.

### Deliverables

- atomic due claim, fenced lease expiry, and drain-run persistence;
- stable-idempotency retry, symmetric persisted jitter, manual due-only drain,
  and refused-state reset handling;
- configured limit, ten-second automatic budget, and sink isolation;
- crash, simultaneous-drain, non-recursion, and redaction tests.

### Requirements

`DUR-018`, `NTF-003` through `NTF-016`, `SEC-011` through `SEC-013`, `TST-004`, `TST-005`, `TST-017`

### Dependencies

E16-T1 Completed.

### Acceptance

- AC-1202 through AC-1205, AC-1207, and AC-1208 pass;
- a crashed or overlapping drainer cannot claim the same live lease and a
  stale owner cannot commit after recovery;
- delivery never writes dispatch, work, or source-state tables.

### Evidence

Completed 2026-09-01. One lease-safe bounded drain service now serves the
explicit manual drain and the later automatic passes: it atomically
claims only due work (concurrent drainers always hold disjoint claims —
the conditional fencing-token UPDATE decides, never the candidate
SELECT), records outcomes under the claim's fence so a stale owner whose
expired lease was recovered cannot commit after recovery, and releases
unstarted claims at wall-budget expiry while a started delivery keeps
its fence and remains lease-recoverable. Ambiguous and retryable
outcomes persist one symmetric ±20% jittered backoff deadline (30s
doubling to 15m) computed once at record time, so every process observes
the same due time; `notifications retry <id>` is the sole operator
bypass, returning an ambiguous, retryable, or refused record to
immediately-due pending. The manual `notifications drain` selects due
work only through the same service; a defective sink declaration stays
sink-local as one retryable attempt. Lease expiry is the effective
delivery deadline plus the thirty-second margin, and a full drain pass
leaves every non-notification table byte-identical (AC-1208). Covered by
store tests (disjoint claims, stale-owner refusal, persisted backoff,
bypass, table isolation, claim release) and service tests (budget
expiry, recovery skip, sink isolation, defaults).

## E16-T3: Post-Commit After-Command Integration

**Status:** Completed

### Objective

Run one route-scoped drain after successful notification-producing commands
without changing their output or exit contract.

### Deliverables

- explicit post-commit command registry and route resolution;
- integration for dispatch, work completion/failure, applicable dispatch retry
  and rerun, quarantine resolution, reconciliation, and drift evaluation;
- setup/baseline/read-only/drain recursion exclusions;
- silent-success and bounded-stderr integration preserving original stdout,
  JSON, and exit behavior;
- existing-due progress without a new notification, failed-core exclusion, and
  one-invocation ten-second budget with deterministic route round-robin;
- source-success/delivery-failure exit and state-isolation tests.

### Requirements

`NTF-010` through `NTF-016`, `CLI-001`, `CLI-002`, `CLI-008`, `OPS-001`, `TST-017`

### Dependencies

E16-T2 Completed.

### Acceptance

- AC-1201, AC-1202, AC-1204, and AC-1205 pass;
- Watchman dispatch and later `work complete` both advance notifications;
- an automatic delivery failure leaves the core command successful.
- AC-1211 passes.

### Evidence

Completed 2026-09-01. The registered post-commit command set — dispatch,
work completion and failure, applicable dispatch retry and rerun,
quarantine release, and reconciliation — now invokes one bounded
after-command drain after its core transaction commits and the command
succeeds, on the still-open store (dispatch drains before its envelope
is written; the reconcile exits drain after theirs, on the same
contract):
stdout, JSON, and exit behavior are unchanged, a successful pass writes
no diagnostics, and problems write at most four bounded stderr lines
with the recovery schedule named as the remainder's owner. Setup,
baseline-only, read-only commands, and the explicit notification drain
are excluded (no recursion). Affected after-command routes resolve in
deterministic route-ID order; the pass interleaves one notification per
route per round under the one-invocation ten-second wall budget (anchored
at Run start, so a long core command consumes it), each route stopping
at its own configured limit, and one drain-run evidence row aggregates
each route's rounds. Drain-run evidence writes use their own short
bounded context so a mid-pass budget expiry still closes the evidence
row. Existing due work drains even when the invocation
created no new notification; a failed core command never auto-drains
(only success paths call the hook); a manual-mode or unaffected route
stays dry with no evidence row. Covered by focused CLI tests: silent
delivery of pre-existing due work with evidence, manual/unaffected
dryness, exhausted-budget no-op, route ordering and filters, and the
round-robin limit posture. The drift-evaluation registry member rides
the scheduled runner of E16-T4 (its only automatic surface); the
explicit drain command's own drift evaluation stays excluded as drain
recursion.

## E16-T4: Scheduler, Status, Doctor, and G12

**Status:** Completed

### Objective

Provide scheduled-mode operational progress and make stalled delivery
diagnosable without direct database inspection.

### Deliverables

- `schedule render|install|inspect|disable|uninstall --platform launchd` with
  resolved paths and instance/route/config-digest managed identity;
- direct internal runner, definition-digest inspection, idempotent install,
  conflict refusal, unload-only disable, and managed-plist-only uninstall;
- fifteen-minute after-command recovery, daily 03:00 scheduled default, and
  three-file 10 MiB log rotation;
- status projection and doctor findings for delivery and scheduler posture;
- launchd syntax, overdue, unresolvable-sink, and end-to-end tests.

### Requirements

`CLI-009`, `CLI-018`, `NTF-010` through `NTF-016`, `OPS-017`, `OPS-018`, `SEC-007`, `TST-017`

### Dependencies

E16-T3 Completed.

### Acceptance

- AC-1201 through AC-1211 pass;
- generated launchd syntax validates, production preflight requires the
  expected loaded definition, and no surface assumes a fixed binary path;
- `make verify` is green.

### Evidence

Completed 2026-09-01. `schedule render|install|inspect|disable|uninstall
--platform launchd` resolves the actual binary (os.Executable) and the
absolute configuration path, derives the managed label and plist path
from the instance ID, route ID, and a digest of the configuration
absolute path, and invokes one direct internal `schedule run` command —
never a shell chain; the rendered plist validates as launchd XML with
the mode-specific timing (fifteen-minute StartInterval for after-command
recovery, StartCalendarInterval for scheduled mode with the 03:00 local
default and the --at HH:MM override). Install is idempotent for an
identical definition and refuses a different one; inspect reports
presence, loaded state, and the definition-digest match; disable
unloads preserving the plist; uninstall removes only the exact managed
plist and refuses a foreign file at the same path; schedule logs rotate
at 10 MiB keeping three. The internal runner performs the due-only
drain in after-command recovery mode and chains the drain only after a
healthy scheduled reconciliation, carrying the drift evaluation (its
only automatic surface). Status and doctor project the delivery
posture — pending age against the configured warning window, the
due/backoff split, mode and limit, repeated ambiguous/retryable
outcomes, unresolvable sinks, and the scheduler
expectation/evidence/overdue state — with warnings and typed doctor
findings, no payload or endpoint material. Production enablement (route
enable) requires the installed, loaded, definition-matching schedule
for automatic modes while preflight warns with the install remediation,
so the setup walkthrough still completes and prints — never runs — the
install command. Gate G12 is evidenced in VALIDATION.md with the AC
matrix above; `make verify` is green.

---

# E17: Documentation, Cold Validation, and v0.1.6 Release

**Epic status:** Planned
**Purpose:** Reconcile every v0.1.6 claim with executable and real-environment evidence, then publish one reproducible release.
**Gate:** G13
**Detailed SOT:** [v0.1.6 operational follow-up](../specs/v0.1.6-operational-follow-up.md)

## E17-T1: CLI, Configuration, Operations, and Skill Truth

**Status:** Planned

### Objective

Synchronize the public operator contract only after the three implementation
epics have delivered their final behavior.

### Deliverables

- root/group/setup/reconcile/notifications/schedule help;
- configuration specification, schema, examples, and migration notes;
- installation, runbook, upgrade, rollback, and known-limitations guidance;
- operator skill and worker skill only where its execution instructions changed.

### Requirements

`CLI-009`, `CLI-016` through `CLI-019`, `HER-019` through `HER-021`, `NTF-010` through `NTF-016`, `OPS-015` through `OPS-018`, `TST-009`

### Dependencies

E16-T4 Completed.

### Acceptance

- every new field and enum is versioned, non-null where absence has no meaning,
  schema/example covered, and mapped to the correct revision;
- documentation distinguishes shipped behavior, migration prerequisites, and
  state-database-scoped guarantees;
- no current worker instruction is changed without a corresponding behavior change.

### Evidence

Planned; none.

## E17-T2: Cold Validation and Required Deployment Evidence

**Status:** Planned

### Objective

Cold-validate the complete E14-E16 result in disposable real and deterministic
environments and remediate only in-scope findings.

### Deliverables

- focused acceptance results for every G10-G12 criterion;
- clean-host and post-Watchman-install setup transcripts;
- real Hermes v0.20.5 probe/preflight and concurrency transcripts;
- notification completion, outbox, automatic delivery, timeout, retry, and
  crash-recovery transcript;
- whole-target review and known-limitations record.

### Requirements

`BND-003`, `BND-004`, `SEC-001` through `SEC-014`, `TST-007`, `TST-009`, `TST-015` through `TST-017`

### Dependencies

E17-T1 Completed.

### Acceptance

- AC-1301 and AC-1302 pass with every requested evidence item present;
- no production vault, board, route, or profile is modified;
- unrelated general `make verify` remediation is reported, not absorbed.

### Evidence

Planned; none.

## E17-T3: Reproducible v0.1.6 Release and Publication

**Status:** Planned

### Objective

Close the roadmap against one final clean tree and publish the reproducible
darwin/arm64 v0.1.6 release.

### Deliverables

- G10-G13 validation and traceability truth;
- synchronized README, VALIDATION, changelog, release checklist, release notes,
  schemas, examples, and versioned skills;
- full `make verify` result and two byte-identical v0.1.6 release builds;
- release commit, annotated tag, main/tag publication, and hosted artifact with
  SHA256SUMS;
- explicit no-production-activation record.

### Requirements

`BND-*`, `SCP-*`, `SRC-*`, `PTH-*`, `DAT-*`, `POL-*`, `DUR-*`, `CON-*`, `HER-*`, `WHK-*`, `FBK-*`, `CLI-*`, `SEC-*`, `OPS-*`, `FAN-*`, `NTF-*`, `TST-009`, `TST-015` through `TST-017`

### Dependencies

E17-T2 Completed.

### Acceptance

- AC-1301 through AC-1304 and every cumulative prior gate pass;
- both darwin/arm64 builds and checksums are byte-identical;
- the tag and hosted release name the reviewed final tree;
- no production route is enabled and no Hermes core/private state is changed.

### Evidence

Planned; none.

---

# 4. Deferred Future Work

The following do not count toward the 89 tracked roadmap tasks (75 completed
through v0.1.5 and 14 planned v0.1.6 tasks across E14-E17) and remain Deferred
until a new roadmap is approved:

- Agent Dispatch managed daemon;
- multi-vault production certification and global budgets;
- MCP server for work receipts and status;
- Git, timer, process, queue, RSS, and inbound webhook source adapters;
- generic HTTP and safe generic agent targets;
- attachment indexing;
- snapshot-bound mode (immutable content snapshot storage);
- remote/multi-host state;
- local dashboard;
- optional Hermes management plugin.

See `docs/todo/README.md` for entry conditions.

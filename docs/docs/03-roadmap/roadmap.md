# JJUKKUMI Implementation Roadmap

> **Roadmap version:** 1.0  
> **Release target:** v0.1.0  
> **Execution model:** Strictly linear, one active task globally  
> **Epics:** 7  
> **Tasks:** 33

## 1. Current State

| Field | Value |
|---|---|
| Current epic | E0, SOT and external contract baseline |
| Current active task | None |
| Next task | **E0-T4, Verify Hermes public interface and freeze capability baseline** |
| Completed tasks | 3 / 33 |
| Planned tasks | 30 / 33 |
| Blocked tasks | 0 |
| Deferred tasks in v0.1 sequence | 0 |

The SOT documents created in this package satisfy E0-T1 through E0-T3. Implementation begins with E0-T4.

## 2. Epic Summary

| Epic | Title | Status | Tasks | Completion gate |
|---|---|---:|---:|---|
| E0 | SOT and External Contract Baseline | **In Progress** | 5 | G0 |
| E1 | Go Foundation, Configuration, and Persistence Schema | **Planned** | 4 | Foundation ready |
| E2 | Watchman Deterministic Dry-Run Pipeline | **Planned** | 5 | G1 |
| E3 | Durable Dispatch and Route Coordination Core | **Planned** | 5 | G2 |
| E4 | Hermes Kanban Durable Integration | **Planned** | 5 | G3 |
| E5 | Feedback Loop, Quarantine, and Reconciliation | **Planned** | 5 | G4 |
| E6 | Hermes Webhook, Operations, Packaging, and v0.1 Release | **Planned** | 4 | G5 |

## 3. Task Status Index

| Order | Task | Status | Primary result |
|---:|---|---|---|
| 1 | E0-T1 | Completed | Product charter and scope frozen |
| 2 | E0-T2 | Completed | Architecture, contracts, and ADR baseline frozen |
| 3 | E0-T3 | Completed | Roadmap, acceptance, and traceability frozen |
| 4 | E0-T4 | Planned | Real Hermes public capability report |
| 5 | E0-T5 | Planned | Watchman interface verification and fixture baseline |
| 6 | E1-T1 | Planned | Buildable Go repository and verification pipeline |
| 7 | E1-T2 | Planned | Validated YAML configuration and path layout |
| 8 | E1-T3 | Planned | Domain primitives, IDs, digests, canonicalization |
| 9 | E1-T4 | Planned | SQLite schema, migrations, and repositories |
| 10 | E2-T1 | Planned | Bounded Watchman input parser |
| 11 | E2-T2 | Planned | Safe path containment and pattern engine |
| 12 | E2-T3 | Planned | Meaningful-change, hashing, and batch normalization |
| 13 | E2-T4 | Planned | Deterministic policy planner and dry-run CLI |
| 14 | E2-T5 | Planned | Real Watchman trigger lifecycle and G1 fixtures |
| 15 | E3-T1 | Planned | Validated dispatch and route state machines |
| 16 | E3-T2 | Planned | Durable intent transaction and attempt leases |
| 17 | E3-T3 | Planned | Retry, unknown, reconciliation, and dead-letter core |
| 18 | E3-T4 | Planned | One active route task and dirty generations |
| 19 | E3-T5 | Planned | Crash, migration, and concurrency gate G2 |
| 20 | E4-T1 | Planned | Hermes Kanban adapter capability implementation |
| 21 | E4-T2 | Planned | Safe Hermes task request renderer |
| 22 | E4-T3 | Planned | Submit, idempotency lookup, and delivery reconciliation |
| 23 | E4-T4 | Planned | Acceptance and execution receipt projection |
| 24 | E4-T5 | Planned | Real Hermes Kanban end-to-end gate G3 |
| 25 | E5-T1 | Planned | Work receipt CLI and validation |
| 26 | E5-T2 | Planned | Packaged Hermes companion skill |
| 27 | E5-T3 | Planned | Exact suppression, mixed changes, and follow-up collapse |
| 28 | E5-T4 | Planned | Protected/bulk quarantine and full reconciliation |
| 29 | E5-T5 | Planned | Production-capable test-vault gate G4 |
| 30 | E6-T1 | Planned | Explicit Hermes webhook adapter |
| 31 | E6-T2 | Planned | Doctor, status, retention, and operational observability |
| 32 | E6-T3 | Planned | macOS/Linux packaging and scheduled reconciliation |
| 33 | E6-T4 | Planned | v0.1 release verification and final SOT reconciliation |

---

# E0: SOT and External Contract Baseline

**Epic status:** In Progress  
**Purpose:** Freeze the product and architecture, then replace all Hermes assumptions with verified public-interface evidence.  
**Gate:** G0

## E0-T1: Freeze Product Charter, Scope, and Terminology

**Status:** Completed

### Objective

Define exactly what JJUKKUMI is, what Hermes owns, the first Obsidian use case, v0.1 scope, and prohibited expansion.

### Deliverables

- `docs/00-sot/project-charter.md`
- `docs/00-sot/terminology.md`
- resolved decision log

### Requirements

`BND-*`, `SCP-001` through `SCP-004`, `SCP-009`

### Acceptance

- Hermes is explicitly authoritative.
- Hermes core modification, internal storage access, and plugin dependency are excluded.
- latest-state Obsidian Markdown maintenance is the first use case.
- no semantic Wiki logic is assigned to JJUKKUMI.

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

**Status:** Planned

### Objective

Inspect the actual Hermes installation and document only public machine interfaces that JJUKKUMI may use. Determine whether the required durable Kanban contract is feasible without modifying Hermes.

### Inputs

- installed Hermes binary and public documentation/help;
- `docs/01-architecture/hermes-integration.md`;
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

### Out of Scope

- writing the adapter;
- changing Hermes;
- creating a plugin;
- parsing Hermes internal storage.

## E0-T5: Verify Watchman Public Interface and Freeze Fixture Baseline

**Status:** Planned

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

---

# E1: Go Foundation, Configuration, and Persistence Schema

**Epic status:** Planned  
**Purpose:** Create a buildable, testable foundation with no source or target side effects.

## E1-T1: Bootstrap Repository, Toolchain, and Verification Pipeline

**Status:** Planned

### Objective

Create the Go repository, package boundaries, build metadata, lint/test commands, and CI baseline.

### Deliverables

- `go.mod` with pinned toolchain policy;
- `cmd/jjukkumi` minimal executable;
- internal package skeleton matching repository layout;
- build/version package;
- Makefile or equivalent deterministic commands;
- CI for format, vet/static checks, unit tests, race test where supported, and schema/example validation;
- contribution and local verification instructions.

### Requirements

`SCP-005`, `SCP-008`, `CLI-001`, `TST-009`

### Dependencies

E0-T4 Completed; E0-T5 Completed.

### Acceptance

- clean checkout builds on macOS and Linux CI;
- `jjukkumi version --output json` follows the CLI envelope;
- no domain behavior is stubbed with false success;
- package dependency direction is enforceable;
- repository verification command passes.

### Out of Scope

Config parsing, SQLite tables, Watchman, and Hermes invocation.

## E1-T2: Implement Configuration Loading, Validation, and Platform Paths

**Status:** Planned

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
- `jjukkumi init` scaffolding command;
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

## E1-T3: Implement Domain Primitives, Canonicalization, and Identity

**Status:** Planned

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

## E1-T4: Implement SQLite Schema, Migrations, and Repositories

**Status:** Planned

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

# E2: Watchman Deterministic Dry-Run Pipeline

**Epic status:** Planned  
**Purpose:** Convert real Watchman input into a safe, deterministic dispatch plan with no agent side effect.  
**Gate:** G1

## E2-T1: Implement Bounded Watchman Input and Environment Parsing

**Status:** Planned

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

## E2-T2: Implement Safe Path Containment and Pattern Policy

**Status:** Planned

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
- pattern behavior matches golden tests on macOS and Linux;
- event content cannot affect route authority;
- protected paths are classified but not read unnecessarily or dispatched.

## E2-T3: Implement Meaningful-Change Confirmation and Batch Normalization

**Status:** Planned

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

## E2-T4: Implement Structural Policy Planner and Dry-Run CLI

**Status:** Planned

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

## E2-T5: Implement Watchman Trigger Lifecycle and Complete G1

**Status:** Planned

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

---

# E3: Durable Dispatch and Route Coordination Core

**Epic status:** Planned  
**Purpose:** Make planned work durable and recoverable before integrating a real target.  
**Gate:** G2

## E3-T1: Implement Dispatch and Route State Transition Services

**Status:** Planned

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

## E3-T2: Implement Durable Intent Commit and Attempt Leasing

**Status:** Planned

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

## E3-T3: Implement Retry, Unknown Reconciliation, and Dead Letter

**Status:** Planned

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

## E3-T4: Implement One Active Route Task and Dirty Generations

**Status:** Planned

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

## E3-T5: Execute Crash, Migration, and Concurrency Gate G2

**Status:** Planned

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

---

# E4: Hermes Kanban Durable Integration

**Epic status:** Planned  
**Purpose:** Connect the proven durable core to the verified public Hermes Kanban interface.  
**Gate:** G3

## E4-T1: Implement Hermes Kanban Adapter and Capability Probe

**Status:** Planned

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

## E4-T2: Implement Safe Hermes Task Request Renderer

**Status:** Planned

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

## E4-T3: Implement Submit, Idempotency Lookup, and Delivery Reconciliation

**Status:** Planned

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

## E4-T4: Implement Acceptance Receipts and Execution Projection

**Status:** Planned

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

## E4-T5: Complete Real Hermes Kanban End-to-End Gate G3

**Status:** Planned

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

---

# E5: Feedback Loop, Quarantine, and Reconciliation

**Epic status:** Planned  
**Purpose:** Make recursive vault maintenance bounded and conservative, then pass the production-capable gate.  
**Gate:** G4

## E5-T1: Implement Work Receipt CLI and Validation

**Status:** Planned

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

## E5-T2: Package and Validate Hermes Companion Skill

**Status:** Planned

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

## E5-T3: Implement Exact Self-Change Suppression and Mixed-Change Handling

**Status:** Planned

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

## E5-T4: Implement Protected/Bulk Quarantine and Full Reconciliation

**Status:** Planned

### Objective

Complete conservative handling for protected paths, bulk/hard limits, overflow, fresh instance, manual release, and current-state reconciliation.

### Deliverables

- quarantine records and commands;
- operator release/discard lineage;
- JSON output contracts for quarantine, batch, and decision records;
- full vault enumeration and path-fact comparison;
- initial, scheduled, overflow, fresh-instance, manual, and stale-active reasons;
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

## E5-T5: Complete Production-Capable Test-Vault Gate G4

**Status:** Planned

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

---

# E6: Hermes Webhook, Operations, Packaging, and v0.1 Release

**Epic status:** Planned  
**Purpose:** Add the explicit secondary delivery mode and make the system installable, inspectable, maintainable, and releasable.  
**Gate:** G5

## E6-T1: Implement Explicit Hermes Webhook Adapter

**Status:** Planned

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

## E6-T2: Implement Doctor, Status, Retention, and Operational Observability

**Status:** Planned

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

`OPS-001` through `OPS-005`, `OPS-008`, `CLI-007`, `SEC-007`

### Dependencies

E6-T1 Completed.

### Acceptance

- doctor detects all required failure classes;
- pruning is dry-run by default and preserves unresolved lineage;
- logs contain causal IDs and no note bodies/secrets;
- vacuum refuses unsafe active conditions;
- status reports route dirty and delivery uncertainty clearly.

## E6-T3: Package macOS/Linux Installation and Scheduled Reconciliation

**Status:** Planned

### Objective

Produce reproducible binaries, configuration/install procedures, Watchman trigger scripts, and daily reconciliation scheduling for macOS and Linux.

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
- Linux CI validates binary, config, SQLite, and systemd unit syntax where possible;
- uninstall does not delete SQLite or config without explicit flag;
- trigger and schedule are idempotently inspectable;
- no JJUKKUMI daemon is introduced.

## E6-T4: Verify and Release v0.1.0

**Status:** Planned

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
- all MUST requirements pass;
- no later feature is partially enabled;
- Hermes plugin remains absent;
- production route enablement is an explicit operator action;
- roadmap tasks E0-T1 through E6-T4 are Completed;
- v0.1.0 artifacts are reproducible and version-compatible.

---

# 4. Deferred Future Work

The following do not count toward the 33 v0.1 tasks and remain Deferred until a new roadmap is approved:

- JJUKKUMI managed daemon;
- multi-vault production certification and global budgets;
- MCP server for work receipts and status;
- Git, timer, process, queue, RSS, and inbound webhook source adapters;
- generic HTTP and safe generic agent targets;
- attachment indexing;
- snapshot-bound mode (immutable content snapshot storage);
- remote/multi-host state;
- local dashboard;
- optional Hermes management plugin.

See `docs/future-work.md` for entry conditions.

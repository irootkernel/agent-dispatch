# Agent Dispatch Implementation Roadmap

> **Roadmap version:** 1.0  
> **Release target:** v0.1.0  
> **Execution model:** Strictly linear, one active task globally  
> **Epics:** 10  
> **Tasks:** 56

## 1. Current State

| Field | Value |
|---|---|
| Current epic | E9 (hardening, D-021) |
| Current active task | None |
| Next task | E9-T1 |
| Completed tasks | 51 / 56 |
| Planned tasks | 5 / 56 |
| In progress tasks | 0 |
| Blocked tasks | 0 |
| Deferred tasks in v0.1 sequence | 0 |

The SOT documents created in this package satisfy E0-T1 through E0-T3. E0-T4 completed against the real installed Hermes 0.19.1 (see `docs/integrations/hermes-public-interface-report.md` and `docs/integrations/hermes-capability-report.json`). E0-T5 completed against the real installed Watchman 2026.07.27.00 (see `docs/integrations/watchman-public-interface-report.md` and the frozen corpus under `docs/integrations/fixtures/watchman/`), closing epic E0 and gate G0. E1-T1 bootstrapped the Go repository, toolchain, and verification pipeline. Epic E1 is complete: the Go foundation, configuration, domain primitives, and durable schema were delivered, audited, and validated (four task commits plus audit remediations). E2-T1 delivered the bounded Watchman input parser against the frozen E0-T5 fixture corpus. E2-T2 delivered the safe path containment resolver and the deterministic pattern policy engine. E2-T3 delivered meaningful-change confirmation and batch normalization. E2-T4 delivered the structural policy planner and the side-effect-free `route plan` / `dispatch --dry-run` CLI. E2-T5 delivered the managed Watchman trigger lifecycle and closed gate G1. Epic E2 is complete: the bounded parser, safe path containment, pattern engine, batch normalization, structural policy planner, dry-run CLI, and the real Watchman trigger lifecycle were delivered, audited (one cross-task remediation commit), and validated. E3-T1 delivered the validated dispatch and route state transition services as the authoritative domain table with typed reasons, guards, and the acceptance/execution projection separation. E3-T2 delivered the durable intent commit, the attempt lease, and the fake sink port: the ingestion transaction, conditional leasing, submitting recovery, and the submit flow that proves the committed intent exists before any target invocation. E3-T3 delivered the bounded retry core, unknown reconciliation, dead-letter handling with operator actions, the dispatches and route command groups with stable exit codes, the result classifier, and the dispatch-attempt and dead-letter record contracts. E3-T4 delivered the route coordination core: one active dispatch per route under concurrency, durable dirty generations for later bursts, the merge-pending transaction, and the single latest-state follow-up collapse. E3-T5 delivered the crash-injection framework, the multi-process harness, and gate G2: AC-201 through AC-207 verified with executable evidence in docs/VALIDATION.md section Gate G2. Epic E3 is complete: the validated state machines, the durable intent commit with attempt leasing, the bounded retry and reconciliation core with operator commands, the one-active-task route coordination, and the proven crash and concurrency guarantees were delivered, audited (16 verified findings remediated in one cross-task commit), and validated with a clean re-audit. E4-T1 delivered the public Hermes Kanban CLI process adapter strictly from the E0-T4 frozen evidence: the exact-version gate (0.19.1), the read-only capability probe over the frozen report with required-capability validation, the controlled process execution (allowlisted environment, controlled working directory, closed stdin, bounded output, deadline with process-group cleanup), the typed structured response parsing for create/show/list/assignees, the frozen error-behavior classification, and the `config validate --probe-targets` surface. E4-T2 delivered the safe Hermes task request renderer: the deterministic contract title and trusted instruction template, the strictly delimited untrusted manifest JSON section, assignment/hints/workspace/mutex mapping validated from trusted route-derived request members only, the verbatim idempotency key transmission, latest-state semantics and work-receipt instructions in every rendered task, and manifest byte-bound enforcement that rejects rather than truncates, with golden tests against the frozen example request. E4-T3 connected the durable core to the target: the gated hermes sink (render, dedup-safe submission, reference lookup, honest capability boundaries), the board-binding configuration, the submit-phase CLI wiring with stable error codes, drain-time reconciliation of unknown dispatches, and both stub-based and real disposable-board integration evidence. E4-T4 delivered the acceptance-receipt and execution-projection surface: the documented version-tested Hermes status mapping with malformed statuses becoming the unknown projection, the execution-projection receipt repository and refresh service, the receipts list|show commands, the route execution projection with stale-active warnings, and the redaction-bounded inspectable payloads. E4-T5 closed gate G3: the real-trigger end-to-end harness (real binary as separate processes, real Watchman trigger, disposable Hermes board) verified AC-301 through AC-306 with executable evidence in docs/VALIDATION.md, remediated the same-second attempts-uniqueness schema defect through migration v2, and recorded the operator demo procedure. The E4 validation audit remediated its confirmed findings under their owning tasks (E3-T3 day-unit durations, E3-T2 malformed-request attempt completion) and cross-task (production route registration through the operator entry points, reconciliation identity and board scope through migration v3, receipt uniqueness, typed error classes, adapter labels, runbook) across seven epic commits, closing with five full-epic review rounds converged to zero unresolved findings and a clean from-scratch matrix. Epic E4 is complete: the version-gated adapter and capability probe, the safe request renderer with strict trusted/untrusted separation, the dedup-safe durable sink with lookup and reconciliation, the acceptance-receipt and execution-projection surfaces, and the real end-to-end gate G3 were delivered, audited, and validated with a clean re-audit. E5-T1 delivered the cooperative work-receipt surface: the `work begin|complete|fail` commands with schema-equivalent receipt validation (identity fields, closed failure-code set, digest shapes, bounded change counts), the active dispatch/resource/task lineage checks, containment-validated relative change paths through the resource resolver, durable receipt persistence with the execution projection updated in one row per run, the atomic completion transaction that collapses a dirty generation into exactly one latest-state follow-up, the failure-budget decision between a bounded follow-up and operator-required UNCERTAIN resolution, and the append-only invalid-receipt audit that never deletes evidence. E5-T2 delivered the production Hermes companion skill: the packaged agent-dispatch-wiki-maintenance skill (frontmatter, task-variable mapping, explicit latest-state and untrusted-data rules, no-receipt fallback), public-mechanism installation instructions, and disposable-profile validation through the real Hermes 0.19.1 skills and kanban surfaces — optional for core correctness, permission-free, and leaving Hermes core unchanged (BND-003, BND-004, FBK-006). E5-T3 delivered exact self-change attribution: the receipt/observation matcher with temporal-window and lineage checks, audited suppression decisions, mixed-change retention, the exact-suppression route clearing (ACTIVE_DIRTY to IDLE under receipt evidence, added to the route state machine and its SOT), and the bounded follow-up collapse proven under ten bursts and no-receipt conditions (FBK-001 through FBK-004, FBK-008, CON-002, CON-003). E5-T4 delivered the conservative structural handling: the dispatch path now persists its true disposition (quarantine holds with operator release/discard lineage, reconcile generations that never dispatch partial ordinary work, drops), the quarantine list|show|release|discard commands with JSON record contracts, and full-scope reconciliation (containment-defended enumeration, path-fact comparison, one latest-state intent on an idle route, repeated reconciliation collapsing into the single pending generation cleared by completion) — with the partial-list prohibition and collapse proven by tests and the fresh-instance gate flow updated (PTH-008, POL-005, POL-006, SRC-005, CLI-006, OPS-006, OPS-007). E5-T5 closed gate G4: the executable feedback harness verified AC-401 through AC-409 on a disposable vault (concurrent human edits, exact suppression with audit, mixed retention, the no-receipt bounded fallback, protected and fresh-instance handling, and the distinct operator semantics), the loop bounds were proven under ten-burst stress, and the production gate was explicitly reviewed — route enable now demands the acknowledgement of the exact computed route revision (the previous boolean-parsed acknowledgement value was never checked and was remediated in this gate) with the production-enable checklist recorded in docs/VALIDATION.md. The E5 validation audit re-verified the requirement-to-owner matrix and integration seams and ran the full-epic review to convergence: its confirmed findings (three real defects: a budgeted clean failure creating no follow-up intent, the reconcile intent constant content fingerprint duplicating the target idempotency key, and unreadable subtrees reported as removals; plus registry, contract, error-boundary, and coverage gaps) were remediated under their owning tasks and the epic ID across the audit commits, closing with a final complete review round at zero critical/blocker/high/medium and two low findings that were initially accepted by explicit operator decision and subsequently resolved in the post-closeout correction commit (the sorted reconciliation reason codes with a regression test, and the error-model class-5 wording), and the production gate remediated to demand the computed route revision. A post-closeout maximum-effort review round then remediated its confirmed findings under the epic ID: the UNCERTAIN operator exit through full reconciliation (the declared resolution edges were unreachable, leaving budget-exhausted routes permanently wedged), the completion-transaction refusal for an unprepared follow-up (a reconciliation arrival racing a clean completion could drop the pending signal), the AC-404 extra-receipt-path suppression block with the `receipt_extra_path` outcome, the schema-required document-form fields, storage-classified lineage failures instead of false invalid-receipt audits, the unreadable-subtree snapshot retention completing the round-20 fix, and the follow-up decision's placeholder policy revision — each with regression tests, and recorded in the SOT changelog 1.0.7. The v0.1 sequence is complete: E6 delivered the explicit webhook adapter, the operations surface, the packaging and scheduling, and the v0.1.0 release verification; all 33 tasks are Completed and gates G0-G5 are closed. The 2026-08-22 MVP compliance review (D-017) then verified the shipped package against the SOT and found 14 MUST requirement gaps — concentrated in expired-submitting recovery wiring, rerun supersession, follow-up activation, submit-path revalidation, durable path facts, the unperformed Linux verification, gate-evidence integrity, and the CLI inspection contract — so remediation epic E7 is registered with twelve tasks to correct every Blocker, High, Medium, and Low/Info finding and release v0.1.1. The 2026-08-23 second MVP compliance review (D-020) then re-verified the shipped v0.1.1 claim that every MUST requirement is PASS or explicitly excepted and found it does not hold — one Blocker in the follow-up product loop (B-1), two FAIL requirements (CON-003, POL-007), three FAIL acceptance criteria (AC-502, AC-503, AC-506), and 22 PARTIAL clauses — so remediation epic E8 is registered with six tasks to close the Blocker, the ten High findings, the mapped Medium findings, and the documentation-truth cluster, to close the now-evidenced SCP-008 Linux exception, and to release v0.1.2; the TST-008 automatic-write gate stays disabled until E8-T1 through E8-T3 are Completed.

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
| E9 | Deferred-Inventory Hardening | Planned | 5 | Deferred closure + v0.1.3 |

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
| 32 | E6-T3 | Completed | macOS/Linux packaging and scheduled reconciliation |
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
| 52 | E9-T1 | Planned | Record schema truth and storage hardening |
| 53 | E9-T2 | Planned | Reconciliation and operator-surface hardening |
| 54 | E9-T3 | Planned | Security, observability, and revision hygiene |
| 55 | E9-T4 | Planned | Test-coverage hardening |
| 56 | E9-T5 | Planned | Documentation truth, dependency, and the v0.1.3 release |

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

- `docs/00-sot/project-charter.md`
- `docs/00-sot/terminology.md`
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

### Evidence

`docs/integrations/hermes-public-interface-report.md`, validated `docs/integrations/hermes-capability-report.json`, and sanitized fixtures under `docs/integrations/fixtures/hermes/`, produced against Hermes 0.19.1 on 2026-08-19 using only the public CLI and a disposable, deleted probe board. Compatibility decision: supported.

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
- `.github/workflows/ci.yml` was removed on 2026-08-22: GitHub Actions is not used. Local `make verify` on macOS is the recorded evidence; run it on a supported Linux host before Linux-targeting releases (SCP-008).
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

- `internal/adapters/hermeskanban/version.go`: the exact-version gate. `ParseVersionOutput` matches the documented `hermes --version` first line only (E0-T4 §2; anything else fails closed), and `CheckVersionSupported` admits exactly the runtime-verified set (0.19.1; the build date is recorded evidence, not the gate) with `VersionUnsupportedError` + remediation, so an unsupported Hermes version fails route validation before any task submission (HER-002, HER-005).
- `internal/adapters/hermeskanban/report.go`: the capability authority. `LoadReport` fails closed on the wrong `agent-dispatch.hermes-capabilities/v1` schema version, a non-`public_cli` interface, or a missing version; `PortCapabilities` maps the frozen report onto the eight HER-004 declarations with an absent name reading false (nothing is assumed beyond the report); `VersionMatchsWith` enforces capability-report freshness against the discovered installation; `ValidateRequired` turns a missing required capability into the typed `CapabilityError` and an unknown name into a configuration defect (never a silent reduced guarantee).
- `internal/adapters/hermeskanban/runner.go`: controlled execution (SEC-004/SEC-005). Argument arrays only — no shell, no interpolation; the child receives exactly the allowlisted environment (PATH/HOME always), a controlled working directory (never a vault root), /dev/null stdin, stdout/stderr captured through write-side-bounded sinks (a stream exceeding the configured byte bound fails the invocation as excessive output — an ambiguous outcome — instead of growing an unbounded capture file), a per-call deadline, and process-group SIGKILL cleanup via `CommandContext` + `Setpgid`. A deadline that elapses only after a completed zero exit never discards a valid result (the deterministic classification property is unit-tested), and any Wait failure coinciding with a done context is the ambiguous deadline outcome while definite exit codes without it stay definite. `ExecutableMissingError` is the definite pre-submit failure with remediation.
- `internal/adapters/hermeskanban/client.go` + `dto.go` + `errors.go`: the typed transport. `DiscoverVersion`, `Create`, `Show`, `List`, and `Assignees` build documented argv arrays — the runtime-verified create surface (title, body, assignee, skills, workspace, mutex key, max-runtime, max-retries, idempotency key, priority, created-by; the help-verified `--model`/`--provider` pinning flags are not mapped by the v1 logical contract and stay unused) with the idempotency key transmitted verbatim — and decide outcomes only from exit 0 plus successfully typed `--json` records: create additionally requires the acceptance-proof members (`t_` + 8 lowercase hex id, status, created_at; E0-T4 §4). Frozen exit-1 stderr shapes classify `no such task` / unknown-board for lookup semantics, exit 2 classifies as definite argument rejection, everything else stays a bounded generic failure; timeout, excessive output, and malformed output carry explicit ambiguous-outcome errors (DUR-005 posture). Option-like values beginning with `-` are refused before any invocation on every rendered value slot — create options and the lookup surfaces (board, task reference, status, sort) alike. All diagnostics, including malformed-output reasons and version-parse fragments, are scrubbed of allowlisted environment values and bounded (error-model §6). `ProfileOnDisk` provides the assignee validation E0-T4 §7 requires before route enablement.
- `internal/adapters/hermeskanban/adapter.go`: the read-only probe facade. `Probe` discovers the version once, gates it, loads the frozen report, requires report/installation version agreement, and validates required capabilities; `ProbeVerbose` drives the same single-discovery path and classifies available / version_unsupported / unavailable / capability_mismatch / config_error for the validation surface — persistent configuration defects (unreadable or stale report, unknown required-capability name) fail validation rather than downgrading to a warning. HER-010: only public CLI commands are used; no Hermes database or private API is touched.
- `internal/cli/config.go`: `config validate --probe-targets` replaces the placeholder with real probing (cli-spec §3): every hermes-kanban target is probed so the summary stays complete, reporting per-target state, version, and capability summary through the single name↔field mapping on `ports.Capabilities`; a capability mismatch fails validation with the stable `config_capability_missing` code and exit 3 (HER-005, AC-306 posture) and a configuration defect with `config_invalid`/exit 3, while an unusable or version-unsupported target stays a warning because the configuration document itself is valid and the adapter gates submissions again at run time. Target durations parse through the one schema-exact parser exported from internal/config (whole-day units included). `internal/cli/state.go` and `internal/version` now describe the delivered transport surface (durable submit wiring arrives with E4-T3; no fallback target exists, DUR-008).
- Tests: frozen-fixture conformance for every response shape (create, show, list, assignees, duplicate-dedup returns the original task, unvalidated assignee echo); the malformed-acceptance table (missing id/status/created_at or a non-`t_<8 hex>` id never counts as acceptance); the frozen error behaviors through stub binaries (unknown task, unknown board, argparse exit 2, generic exit, timeout-ambiguous on both the submit and lookup surfaces, excessive output, malformed output); runner conformance (environment allowlist exclusion with a poisoned variable and pass-through of an operator-added entry, controlled cwd, write-side output bound, deadline + process-group cleanup, closed stdin, missing executable); profile validation (on-disk, absent, prefix-collision); hostile-value verbatim argv round trip and the leading-dash refusal table; allowlisted secret scrubbing from diagnostics; probe scenarios (happy path, unsupported version, unparsable version, capability mismatch without emulation, stale report, all verbose states); CLI suites for the probe-targets outcomes (available with eight capabilities, capability mismatch → `config_capability_missing`/exit 3, unknown capability name and unreadable report → `config_invalid`/exit 3, version_unsupported and unavailable as warnings, invalid and day-unit timeouts, mixed-state multi-target summaries in deterministic order); single version discovery per probe with an invocation-counting stub; the lookup-surface leading-dash refusal table; version-diagnostic redaction; and a skip-guarded probe of the real installed Hermes (TST-007 posture). `make verify` green including the real Hermes 0.19.1 probe against `docs/integrations/hermes-capability-report.json`.

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
- Tests (`internal/cli/e5t2_test.go`): the packaging assertions (required rules present, no permission or plugin surfaces) and the disposable validation against the real installed Hermes 0.19.1 — a throwaway HOME receives the skill by the documented local-copy mechanism, the public skills surface lists and inspects it, a disposable board task created with the public `--skill` selection carries the skill in its durable record (create JSON and public show), and the board is hard-deleted afterwards, leaving the real profile untouched. `make verify` green.

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

### Evidence

- `internal/ports/quarantine.go` + `internal/adapters/sqlite/quarantine.go`: the durable hold surface — `CommitQuarantineLineage` (observation, batch, decision, and the `quarantine_items` hold in one transaction, no intent ever), `CommitDropLineage`, `CommitReconcileLineage` (marks `pending_reconcile=1` and records the source position, never a partial dispatch), `List`/`Load` hold projections, `ReleaseQuarantine` (resolves `held` atomically with actor, reason, the supersedes lineage on a replacement reconciliation decision, and the pending generation), `DiscardQuarantine` (no task creation), `MarkPendingReconcile`, the `path_facts` snapshot, and the batch-less reconcile decision and single latest-state intent commits. `CompleteActive` now clears the pending flag when the follow-up generation is created, and the route machine gained the ACTIVE_CLEAN to FOLLOWUP_READY completion edge for a pending reconciliation (SOT diagram and lockstep tests updated with the receipt-evidence and pending-only guards).
- CLI (`internal/cli/quarantine.go`, `reconcile.go`): `quarantine list|show` with state filters, `quarantine release|discard` requiring `--reason` and `--yes` (exit-code mapping: `quarantine_not_found` 4, `transition_invalid` 14 on double resolution), and `reconcile --route --reason <eight documented reasons> [--submit]` — default persists the decision, enumeration, comparison, and snapshot; `--submit` additionally submits the eligible intent through the gated sink. The real dispatch path (`internal/cli/plan.go`) now branches on the plan disposition, so protected and bulk batches hold, overflow and fresh-instance reconcile, and drops persist evidence only.
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
- Linux CI validates binary, config, SQLite, and systemd unit syntax where possible (superseded: no successful Linux `make verify` is recorded, hosted CI is not used, and the 2026-08-22 review's diagnostic linux/arm64 container runs failed with exit 2 (D-017); `make schedule-check` validates the platform artifact where the tool exists);
- uninstall does not delete SQLite or config without explicit flag;
- trigger and schedule are idempotently inspectable;
- no Agent Dispatch daemon is introduced.

### Evidence

Delivered as the release process (`make release`: byte-reproducible darwin/arm64 and linux/amd64 binaries with the full commit hash and commit-date build time, verified by identical SHA-256 digests across consecutive builds, plus a portable `LC_ALL=C`-sorted `SHA256SUMS`; `make clean` covers `dist/`), the platform-validated scheduling artifacts (`make schedule-check` inside `make verify`: `plutil -lint` on macOS, `systemd-analyze verify` on a Linux host where the tool exists (no successful Linux `make verify` is recorded; the 2026-08-22 review's diagnostic linux/arm64 container runs failed, D-017), `sh -n` always), the launchd LaunchAgent and systemd --user service/timer examples invoking the verified one-shot `reconcile --reason scheduled` shape (whose `--output json` option the dispatches family now accepts, pinned by a test driving the example's exact arguments) with no daemon, the runbook §10 uninstall example that retains SQLite and configuration by design, `agent-dispatch completion bash|zsh` derived from the registered tree, `agent-dispatch maintenance backup` (Lstat symlink guard, `backup_target_exists` conflict, partial-file cleanup, the dedicated `maintenance.backed_up` event, and a verified owner-only standalone snapshot), and `docs/docs/05-operations/installation.md` (platform paths, clean-host scenario, scheduling, upgrade, backup, uninstall). Verified by `make verify` plus the e6t3 suite: the clean-host init through the default paths with owner-only permissions and fail-closed re-init refusal, the backup snapshot opening standalone with quick-check integrity, the uninstall safety pins, the schedule invocation shapes with timer properties, the completion registry invariant parsed back out of the emitted script, and the sandboxed per-command completeness proof. Reviewed through three full-target Mulgae rounds (r_01a0272c and r_01a0273e findings remediated in place; r_01a0274b as the authorized extra round whose residuals are the deferral) — all coverage complete, ci pass, zero structured findings; the round-3 residuals are dispositioned individually as the hardening deferral under run r_01a0274b (reports_only): backup create-vs-guard race and umask window (mitigated by the owner-only snapshot verification; retry-safe), unsigned release artifacts (accepted for v0.1.x; SHA256SUMS provides integrity), further systemd sandboxing (post-v0.1 hardening), the duplicated no-overwrite guard (cosmetic), launchd output visibility (documented in the runbook), release-reproducibility automation (the double-build check is manual and re-run by E7-T12), and the remaining prose/test notes (folded into E7-T10). Changelog 1.0.10.

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

Delivered as the release-verification surface: the executable G5 acceptance suite (`internal/cli/e6t4_test.go`: AC-501 through AC-506 — the webhook route's auth-without-persistence, transport-vs-durable distinction, and no-fallback proof; doctor's actionable stable-coded findings; prune's resolved-expired removal preserving unresolved lineage and the append-only audit; the clean-host macOS install→dispatch→scheduled-reconciliation→doctor flow with the production-gate acknowledgement; the release-way build with the version envelope and artifact set; plus the upgrade-and-backup rehearsal restoring the snapshot standalone with its lineage), the Gate G5 evidence table in `docs/VALIDATION.md` (closing G0–G5: G0 by E0-T5, G1–G4 previously, G5 here), the regenerated requirement traceability matrix (`make traceability`, 33 tasks, 15 groups, every requirement ID resolved to its owning and verifying tasks), the release artifacts (`make release VERSION=v0.1.0`: byte-reproducible darwin/arm64 and linux/amd64 binaries with SHA256SUMS; `docs/RELEASE-NOTES-v0.1.0.md`; the SOT package manifest-verified; schemas, examples, and the companion skill in place), and the security/architecture review posture carried by the per-task Mulgae rounds and the frozen ADR set. Compatibility is frozen (config version 1, schema range 1-4, record payload versions, adapter profiles 0.19.1/2026.07.27.00 — `agent-dispatch version` reports every axis); no deferred feature is partially enabled (the future-work list stands apart); the Hermes plugin remains absent; production enablement stays the explicit computed-revision operator action. Verified by `make verify` on the release tree (darwin/arm64 only; no successful Linux run is recorded: the 2026-08-22 review's diagnostic linux/arm64 container runs failed with exit 2, and the AC-505 verification is the SCP-008 exception under D-017). Reviewed through two full-target Mulgae rounds (r_01a0277c and r_01a02791, both remediated in place: the delivered webhook adapter entry in `agent-dispatch version`, the doctor stable-nonzero contract with the `doctor_findings_present` registry code, the real production-gate enablement and uninstall ordering in the AC-504 evidence, the computed-revision rehearsal enable, the monotonic audit assertion, the webhook target-type and v0.1.0 version assertions, the unified doctor emission with the version adapter pin, and the documentation corrections); the epic validation audit reconciles the member-task hardening deferrals (r_01a026d2, r_01a0270a, r_01a0274b) and this task's round-2 residuals under run r_01a02791. Changelog 1.0.11.

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

Delivered as a documentation-only correction set over ten files (the E7-T1 record originally said twelve; corrected by E8-T6): every surviving false verification claim now carries one accurate statement (no successful `make verify` run on a supported Linux host is recorded, hosted CI is not used, and the review's diagnostic linux/arm64 container runs failed with exit 2) at the roadmap's E1-T1/E6-T3/E6-T4 acceptance and evidence wording plus the superseded "all MUST requirements pass" bullet, the AC-505 criterion, the charter's success definition, the record-contract and examples README validation sentences, the implementation-guide CGO policy row, and inline markers on the false CHANGELOG 1.0.10/1.0.11 claims; `docs/VALIDATION.md` is truthful about its evidence (current header and statistics: 63 manifest-basis Markdown files, 12 schemas, 8 epics, 45 tasks; the G2 crash-boundary scope naming the in-process-only boundaries; the AC-203 hollow-assertion and AC-207 always-skip corrections owned by E7-T2/E7-T4; the G4 store-direct follow-up-activation bypass note; the G5 AC-505 status; the removed nonexistent `TestG2MigrationInterruptedUpgrade` citation; the em-dash check scoped to `docs/docs/00-sot/`); `docs/README.md` and VALIDATION align on SOT 1.0.14 with CHANGELOG entry 1.0.14; and the roadmap's status artifacts (task index, current-state counts, epic status) stay mutually consistent. Verified by `make verify` on darwin/arm64 (all checks green including the manifest with the admitted review report). Reviewed through two full-target Mulgae rounds (r_01a02afe-da64: four valid report findings — inconsistent roadmap status artifacts, overbroad no-Linux-run absolutes contradicting the recorded Linux facts (D-017), residual CI wording, and the em-dash check scope with three newly introduced em dashes — all remediated in place; r_01a02b0d-3389: coverage complete, ci pass, zero structured findings, reports_only), with the round-2 residuals recorded as the hardening deferral for the epic validation audit under run r_01a02b0d-3389 (reports_only; no structured finding IDs exist; residuals: the 1.0.14 corrected-locations list naming E6-T2 though no E6-T2-owned roadmap wording changed, the platform-unqualified `make verify` claim in the 1.0.14 entry, and the pre-existing testing-strategy CI-stages section owned by E7-T10). Changelog 1.0.14.

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

Delivered as the documentation-consistency restoration (M-27): the repository root carries an MIT `LICENSE` and `docs/docs/04-implementation/dependency-licenses.md` records the review of all 39 modules in the build graph (direct and indirect: MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0, and MPL-2.0 tool-chain only, all compatible). (M-31): the release checklist is retitled for v0.1.1 and operated - all 50 items checked with honest narrowing notes (the Linux leg references the recorded SCP-008 exception; the reproducibility double-build points at E7-T12); `repository-layout.md` gains the Layout Deviations section explaining the six named departures (the new LICENSE, the empty `migrations/`, the `test/e2e` and `test/helpers` placeholders, the per-domain ports files, the reserved empty packages, and the retained placeholder `doc.go` anchors); `task-execution-rules.md` §5 records that the owner/timestamp fields live in the execution environment rather than a per-task YAML file; the CHANGELOG 1.0.7 entry carries the `pending_reconcile` ABA known-deferred note; the six E6-T3 hardening residuals carry individual dispositions in the E6-T3 evidence; CONTRIBUTING and README state the full `make verify` composition; the two stale `docs schemas unavailable` skips became fatal broken-checkout guards and the stale E4-T4 `work begin` skip was removed; and the duplicate E6-T4 evidence heading was unified. Verified by `make verify` on darwin/arm64. Changelog 1.0.23.

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

Delivered as the closeout verification: `make verify` passed on darwin/arm64 including the race suite (2026-08-23); the G1-G5 gate suites re-ran green on the real Hermes 0.19.1 and Watchman 2026.07.27.00; the MUST-closure matrix is recorded in `docs/VALIDATION.md` (thirteen of the fourteen GAP requirements PASS through the E7 remediation; SCP-008 carries the explicit D-017 exception); `make release VERSION=v0.1.1` ran twice with byte-identical `dist/SHA256SUMS` (darwin/arm64 `170b8984...`, linux-amd64 `c21ce0b5...`); and `docs/RELEASE-NOTES-v0.1.1.md` discloses the Linux verification exception beside the delivered remediation. Changelog 1.0.25.

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

**Epic status:** Planned  
**Purpose:** Close the hardening inventory E8 recorded for the next cycle (D-021): the four Deferred mediums, the resolution remainders, the seventeen Deferred lows, the member-task test-coverage deferrals, and the documentation sub-wording items — every remaining D-020 disposition that is not a maintained exception or a D-018 acceptance.  
**Gate:** every D-020 row reads Fixed, maintained exception, or explicit D-018 acceptance — with the whole-epic review (diff from 148bf57, covering the E8 correction delta) converged and v0.1.3 released from the tagged tree.

## E9-T1: Record Schema Truth and Storage Hardening

**Status:** Planned  
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

Pending (E9-T1 not started).

## E9-T2: Reconciliation and Operator-Surface Hardening

**Status:** Planned  
**Design Gate impact:** Not required (no design gate registry is enrolled in this repository; legacy rule recorded).

### Objective

Close the reconciliation enumeration symlink defect, the Watchman-context refusal, and the operator-surface gaps the review recorded as deferred.

### Deliverables

- the reconciliation walk resolves symlinks: an escaping symlink lands in the skipped list with a warning and never projects as an exists-fact or `FileRegular` in an automatic task manifest (M-24);
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

Pending (E9-T2 not started).

## E9-T3: Security, Observability, and Revision Hygiene

**Status:** Planned  
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

Pending (E9-T3 not started).

## E9-T4: Test-Coverage Hardening

**Status:** Planned  
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

Pending (E9-T4 not started).

## E9-T5: Documentation Truth, Dependency, and the v0.1.3 Release

**Status:** Planned  
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

Pending (E9-T5 not started).

---

# 4. Deferred Future Work

The following do not count toward the 56 tracked roadmap tasks (33 v0.1 feature tasks, 12 E7, 6 E8, and 5 E9 remediation tasks) and remain Deferred until a new roadmap is approved:

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

See `docs/future-work.md` for entry conditions.

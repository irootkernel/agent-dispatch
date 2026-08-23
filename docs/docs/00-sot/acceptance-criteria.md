# Acceptance Criteria

## 1. Release Gates

The release gate is cumulative. A later gate cannot pass while an earlier gate is incomplete.

| Gate | Meaning |
|---|---|
| G0 | SOT and real Hermes interface baseline are verified. |
| G1 | Deterministic Watchman dry-run planning is correct and safe. |
| G2 | Durable local dispatch survives crash and concurrency. |
| G3 | Hermes Kanban creates one effective durable task. |
| G4 | Feedback-loop and reconciliation behavior is production-capable. |
| G5 | Webhook, packaging, operations, and v0.1 release checks pass. |

## 2. Scenario Acceptance Matrix

### G0: Contract Baseline

| ID | Given / When / Then |
|---|---|
| AC-001 | Given the actual installed Hermes version, when `E0-T4` probes its public CLI and webhook, then a checked-in capability report records commands, JSON fields, authentication, idempotency, lookup, mutex, and status behavior without reading internal storage. |
| AC-002 | Given a missing required Hermes capability, when route validation runs, then validation fails or a named reduced guarantee is explicitly configured; no silent emulation occurs. |

### G1: Watchman Dry-Run

| ID | Given / When / Then |
|---|---|
| AC-101 | Given one new included Markdown file, when a valid Watchman batch is planned, then exactly one meaningful create appears in one dispatch plan. |
| AC-102 | Given a modified Markdown file whose digest is unchanged, when planned, then the change is dropped with reason `unchanged_content`. |
| AC-103 | Given repeated editor saves that resolve to the same final digest, when planned, then one effective modify remains. |
| AC-104 | Given an included Markdown deletion, when planned, then one delete is retained without attempting to read the deleted file. |
| AC-105 | Given `.git/**`, configured Obsidian UI state, or a non-Markdown attachment, when planned, then it is excluded with a deterministic reason. |
| AC-106 | Given an absolute path, parent traversal, NUL byte, or symlink escape, when planned, then no file outside the resource root is read and the input is rejected or quarantined. |
| AC-107 | Given a missing or unusable previous source position (overflow-class; `WATCHMAN_FILES_OVERFLOW` was refuted by E0-T5 and is never read) or fresh-instance semantics, when planned, then no partial normal task is produced and one reconciliation decision is persisted or printed. |
| AC-108 | Given more than the configured automatic threshold, when planned, then policy yields the configured bulk disposition and never silently truncates the manifest. |
| AC-109 | Given file names or front matter containing instructions, when planned, then target, profile, skills, workspace, and policy remain unchanged. |
| AC-110 | Given the same normalized input twice, when planned, then the canonical content fingerprint and plan are byte-for-byte stable apart from unique observation identity and timestamps. |

### G2: Durable Core

| ID | Given / When / Then |
|---|---|
| AC-201 | Given a crash before the intent transaction commits, when Agent Dispatch restarts, then no external submit is inferred and no committed intent is lost. |
| AC-202 | Given a crash after intent commit but before submit, when restarted, then the intent returns to eligible `ready` processing exactly once. |
| AC-203 | Given remote acceptance followed by a crash before local receipt commit, when restarted, then the dispatch enters or remains `unknown`, performs lookup, and does not blindly create a second task. |
| AC-204 | Given two simultaneous one-shot processes, when both attempt the same dispatch, then one obtains the attempt lease and one observes existing ownership. |
| AC-205 | Given bounded transient failure, when retries occur, then attempts use persisted backoff, retain one idempotency key, and stop at the configured limit. |
| AC-206 | Given terminal rejection, when processed, then the dispatch becomes inspectable `rejected` or `dead_lettered` with no automatic sink switch. |
| AC-207 | Given an interrupted database migration, when restarted, then the database is either valid at the previous version or valid at the new version, never partially assumed. |

### G3: Hermes Kanban MVP

| ID | Given / When / Then |
|---|---|
| AC-301 | Given one normal change generation and no active task, when dispatched, then one Hermes Kanban task is durably accepted and its external task ID is stored. |
| AC-302 | Given a duplicate local submission attempt, when Hermes supports idempotency, then lookup resolves to the original task and no second accepted task is created. |
| AC-303 | Given Hermes downtime, when dispatch occurs, then the committed intent remains retryable and no event is lost. |
| AC-304 | Given an ambiguous Hermes CLI result, when handled, then state becomes `unknown` and no webhook fallback occurs. |
| AC-305 | Given a generated Hermes task, when inspected, then it contains no note body, clearly separates trusted instructions from untrusted manifest data, references the configured resource, and instructs latest-state processing. |
| AC-306 | Given missing durable-acceptance or lookup capability, when the production route is enabled, then the operator receives a blocking validation error unless an approved reduced-guarantee ADR exists. |

### G4: Feedback Loop and Reconciliation

| ID | Given / When / Then |
|---|---|
| AC-401 | Given an unresolved active Hermes task, when additional relevant changes arrive, then no parallel maintenance task is created and the route dirty generation is durably incremented. |
| AC-402 | Given ten bursts during one active task, when the task completes, then at most one follow-up task is created for the latest vault state. |
| AC-403 | Given a valid work receipt whose changed path and digest set exactly matches observed changes, when attribution runs, then exact self-generated changes may be suppressed and the decision is audited. |
| AC-404 | Given a receipt with missing paths, extra paths, mismatched digest, wrong resource, or wrong dispatch, when attribution runs, then changes are not suppressed. |
| AC-405 | Given agent and human changes in the same interval, when attribution runs, then the route remains dirty and receives a bounded follow-up evaluation. |
| AC-406 | Given no work receipt, when agent changes are observed, then Agent Dispatch may produce an extra follow-up but never silently loses potential human work. |
| AC-407 | Given a protected path, when observed, then it is quarantined, excluded from the automatic task, and visible to the operator. |
| AC-408 | Given overflow or fresh instance while a task is active, when processed, then one dirty reconciliation generation remains pending after active completion. |
| AC-409 | Given explicit `retry`, `reprocess`, `rerun`, and `reconcile` commands, when each is used, then IDs and lineage follow their distinct documented semantics. |

### G5: Operations and Release

| ID | Given / When / Then |
|---|---|
| AC-501 | Given a configured Hermes webhook target, when an explicit webhook route dispatches, then authentication is resolved without persistence, transport and durable acceptance are distinguished, and Kanban is not used as fallback. |
| AC-502 | Given invalid config, missing root, non-local SQLite placement, unavailable Watchman, or target capability mismatch, when `doctor` runs, then it returns a stable nonzero code and actionable structured findings. |
| AC-503 | Given retention thresholds, when pruning runs, then resolved expired data is removed without breaking unresolved lineage or audit references. |
| AC-504 | Given a clean macOS host, when install instructions are followed, then Watchman trigger installation, one dispatch, scheduled reconciliation, and uninstall work without manual database edits. |
| AC-505 | Given a supported Linux host, when the test suite runs, then all unit, integration, race, migration, crash, and fixture tests pass. (Closed for v0.1.2 under D-020: `make verify` in full passed on linux/arm64 as a non-root user — 707 pass, 13 environment skips — and `make test` passed on linux/amd64; the two permission-expectation tests self-skip under root. The real Hermes/Watchman legs and `systemd-analyze` remain macOS-verified only.) |
| AC-506 | Given a v0.1 release candidate, when release verification runs, then binaries, checksums, schemas, example config, Hermes companion skill, SOT, and changelog are present and version-compatible. |

## 3. Automatic-Write Gate

Automatic Hermes writes to the real vault are prohibited until all scenarios in gates G0 through G4 pass in a test vault and the operator explicitly enables the production route. Dry-run, audit-only, or no-write Hermes profiles may be used earlier.

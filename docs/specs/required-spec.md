# Required Specification

## 1. Interpretation

This document is normative. Each requirement has a stable ID used by the roadmap and acceptance matrix. v0.1.5 is the shipped baseline (gates G6 through G9 closed 2026-08-30); requirements introduced by D-027 are the approved v0.1.6 target — their E14 share, the guided setup and disabled baseline, is implemented and evidenced at gate G10 (closed 2026-08-31) — and the release stays blocked until G11 through G13 close.

- **MUST** requirements block v0.1 release.
- **SHOULD** requirements require an explicit recorded exception if not met.
- **MAY** requirements are optional.

## 2. Product Boundary

| ID | Requirement |
|---|---|
| BND-001 | Agent Dispatch **MUST** operate as an event-ingress and activation gateway, not as an agent runtime or semantic knowledge engine. |
| BND-002 | Hermes **MUST** be treated as the authoritative runtime for task execution and semantic outcomes. |
| BND-003 | v0.1 **MUST NOT** modify Hermes core, access Hermes internal storage, or require a Hermes plugin. |
| BND-004 | Hermes integration **MUST** use public interfaces. Machine-readable forms are required where the public command provides them; D-025 permits a bounded fail-closed Agent Dispatch parser for the public profile-scoped skill list because Hermes exposes no JSON form. A Agent Dispatch-owned adapter, CLI, or skill is permitted. |
| BND-005 | Agent Dispatch **MUST NOT** directly edit governed vault content. |
| BND-006 | Agent Dispatch **MUST NOT** perform open-ended LLM classification inside the bridge. |
| BND-007 | A future Hermes management plugin **MAY** be documented but **MUST NOT** be a v0.1 dependency. |

## 3. v0.1 Scope and Platform

| ID | Requirement |
|---|---|
| SCP-001 | The certified v0.1 use case **MUST** support one Obsidian vault, one Watchman source, one route, and one primary Hermes Kanban target. (Certification posture, L-25: v0.1 carries no formal certification process — the claim means this shape is the verified, `make verify`-gated configuration; anything beyond it is untested by the gates.) |
| SCP-002 | Internal configuration structures **SHOULD** use named resources and routes so later multi-vault support does not require a format replacement. |
| SCP-003 | v0.1 **MUST** process Markdown files with the `.md` extension. (Delivered scope, L-26: the scope predicate also admits the `.markdown` extension — both are in scope wherever `file_scope: markdown` applies.) |
| SCP-004 | Attachments, PDFs, images, Canvas files, and arbitrary binary files **MUST** be out of scope for automatic dispatch in v0.1. |
| SCP-005 | The implementation **MUST** be written in Go and pin its toolchain and dependencies. |
| SCP-006 | Operator configuration **MUST** be YAML and validated before side effects. |
| SCP-007 | Durable local state **MUST** use SQLite on a local filesystem. Network filesystem state is unsupported. |
| SCP-008 | v0.1 **MUST** be verified on macOS and one supported Linux environment. Hosted CI is not used; run `make verify` on each target platform. *(Superseded by D-023, E9-T8: darwin/arm64 is the only supported platform for the current product line — the Linux verification clause is retired with the Linux packaging surface; the historical D-020 closure record stands as history.)* |
| SCP-009 | Git **MAY** enrich evidence but **MUST NOT** be required for basic ingestion and dispatch. |

## 4. Watchman Source

| ID | Requirement |
|---|---|
| SRC-001 | The first source adapter **MUST** accept Watchman trigger JSON on standard input. |
| SRC-002 | The adapter **MUST** read and persist relevant Watchman trigger environment fields, including root identity, trigger identity, since/clock position, relative root, and overflow indication when supplied. |
| SRC-003 | The adapter **MUST** validate that the invocation is bound to the configured route and resource. Payload data **MUST NOT** select another route or resource. |
| SRC-004 | Canonical operations **MUST** be `create`, `modify`, and `delete`. Rename **MAY** be represented only as derived evidence and **MUST NOT** be required for correctness. |
| SRC-005 | A fresh instance, lost cursor, recrawl uncertainty, or overflow **MUST NOT** dispatch a partial ordinary batch. It **MUST** create or merge one reconciliation request. |
| SRC-006 | Agent Dispatch **MUST NOT** add a second time-based settle delay in one-shot trigger mode. It **MUST** treat the Watchman trigger input as the source batch. |
| SRC-007 | Trigger registration **MUST** use an explicit unique trigger name and **MUST** avoid destructive unnecessary re-registration. |
| SRC-008 | The source adapter **MUST** support deterministic fixture input without a running Watchman daemon for tests. |
| SRC-009 | A managed Watchman binding **MUST** retain the configured resource root, actual Watchman root, effective relative root, and trigger name as distinct values. |
| SRC-010 | `watchman install`, `status`, `test`, and `remove` **MUST** resolve and report the same effective binding. |
| SRC-011 | Installation **MUST** constrain the trigger to the configured resource subtree with `relative_root` or an equivalent expression. |
| SRC-012 | A successful remove **MUST** prove that no trigger with the managed identity remains on any applicable Watchman root. |

## 5. Meaningful Change and Path Policy

| ID | Requirement |
|---|---|
| PTH-001 | Every event path **MUST** be normalized as a relative path under the configured resource root. |
| PTH-002 | Absolute paths, parent traversal, NUL bytes, invalid encodings, and paths escaping through symlinks **MUST** be rejected or quarantined before reading or hashing. |
| PTH-003 | Include, exclude, protected, and immutable patterns **MUST** be operator-owned route configuration. |
| PTH-004 | Changed file content, file names, front matter, and source payload fields **MUST NOT** modify profile, skill, workspace, target, permissions, or route policy. |
| PTH-005 | A create or delete of an included Markdown path **MUST** be meaningful unless excluded by a deterministic rule. |
| PTH-006 | A modify **MUST** be meaningful only when the effective content digest differs from the last known digest or when no prior digest is available. |
| PTH-007 | Metadata-only changes, Watchman bookkeeping, `.git/**`, and configured Obsidian UI state files **MUST** be excluded by default. |
| PTH-008 | Protected-path changes **MUST** be quarantined by default and **MUST NOT** be included in an automatic maintenance task. |
| PTH-009 | Exact files, file globs, exact directories, recursive directories, and multiple exclusions **MUST** be evaluated relative to the configured resource root and before reading, hashing, batching, fan-out, rendering, or notification. |

## 6. Canonical Records and Identity

| ID | Requirement |
|---|---|
| DAT-001 | Source observations, change batches, policy decisions, dispatch intents, dispatch attempts, dispatch receipts, and work receipts **MUST** be separate records. |
| DAT-002 | Observation, batch, decision, dispatch, attempt, and receipt IDs **MUST** be independently generated time-ordered unique IDs. |
| DAT-003 | Event identity **MUST NOT** be derived solely from content fingerprint. |
| DAT-004 | Content fingerprint, idempotency key, and attempt ID **MUST** have distinct documented derivations and purposes. |
| DAT-005 | Fingerprint inputs **MUST** use a deterministic canonical projection, sorted paths, normalized operations, and stable encoding. |
| DAT-006 | An idempotency key **MUST NOT** include attempt number, submission time, or other retry-varying fields. |
| DAT-007 | Records **MUST** retain route ID, route revision, policy revision, source ID, resource ID, timestamps, and causal parent IDs. |
| DAT-008 | Note bodies **MUST NOT** be persisted by default. Only path evidence, hashes, source metadata, decisions, states, and bounded receipts may be stored. |
| DAT-009 | Stored machine payloads **MUST** be versioned. Unknown future major versions **MUST** fail closed. |
| DAT-010 | An aggregate event, each selected destination child, each destination revision, and each notification intent or attempt **MUST** have separate durable identity and lineage. |
| DAT-011 | Aggregate status **MUST** remain a projection over child delivery, execution, and work-receipt records; it **MUST NOT** replace those authoritative records. |
| DAT-012 | Historical single-destination database evidence **MUST** remain queryable after the v0.1.5 forward migration. |
| DAT-013 | Unresolved legacy work **MUST NOT** be silently submitted under a new destination contract. |
| DAT-014 | The child idempotency projection **MUST** include route ID and revision, source generation and fingerprint, destination ID and revision, workstream, target scope, and contract version. |

## 7. Batching and Policy

| ID | Requirement |
|---|---|
| POL-001 | Policy evaluation **MUST** be deterministic and side-effect free. |
| POL-002 | The core **MUST** classify only structural conditions such as normal, protected, bulk, overflow, malformed, stale, or unknown. |
| POL-003 | The core **MUST NOT** inspect note semantics to decide indexing, referencing, or grouping. |
| POL-004 | A batch **MUST** have a configured maximum change count and serialized payload size. |
| POL-005 | A batch above the automatic threshold **MUST** be quarantined or converted to one explicit full-reconciliation request according to route policy. |
| POL-006 | Policy output **MUST** be one of `drop`, `dispatch`, `merge_pending`, `quarantine`, or `reconcile`, with machine-readable reason codes. |
| POL-007 | Changing behavior-affecting configuration **MUST** produce a different computed route or policy revision used by subsequent decisions. |
| POL-008 | A not-yet-submitted batch **MUST** be evaluated against the active policy revision before dispatch. An already accepted Hermes task retains its original lineage. |

## 8. Durability and Delivery

| ID | Requirement |
|---|---|
| DUR-001 | SQLite persistence **MUST** be active before the first external side effect. |
| DUR-002 | A dispatch intent **MUST** be committed before invoking Hermes. |
| DUR-003 | Delivery semantics **MUST** be documented as at-least-once, not exactly-once. |
| DUR-004 | The dispatch state machine **MUST** distinguish `ready`, `submitting`, `accepted`, `rejected`, `unknown`, `retry_wait`, `reconciling`, `dead_lettered`, and terminal superseded states where applicable. |
| DUR-005 | A timeout, broken pipe, process termination, invalid response after possible submission, or other ambiguous outcome **MUST** enter `unknown`, not `failed`. |
| DUR-006 | An unknown dispatch **MUST** attempt lookup by idempotency key or external reference before another submission. |
| DUR-007 | Automatic retries **MUST** be bounded and use persisted exponential backoff with jitter. |
| DUR-008 | Automatic sink failover **MUST NOT** occur when acceptance is ambiguous. |
| DUR-009 | A dead-lettered dispatch **MUST** remain inspectable and require an explicit retry, reprocess, rerun, or discard action. |
| DUR-010 | Process crash, machine reboot, and temporary Hermes unavailability **MUST NOT** silently lose committed work. |
| DUR-011 | State transitions **MUST** be transactional, validated, and appended to an audit history. |
| DUR-012 | Concurrent one-shot processes **MUST** use database constraints and leases so only one process owns a dispatch attempt. |
| DUR-013 | Each resource **MUST** carry a monotonic path-fact observation revision advanced by every durable path-fact mutation. |
| DUR-014 | Full reconciliation **MUST** replace a resource snapshot only when the pre-enumeration observation revision still matches, with replacement and revision advancement atomic. |
| DUR-015 | A reconciliation fence conflict **MUST** preserve newer facts, record a typed concurrent-change outcome, and leave one due reconciliation generation. |
| DUR-016 | State transitions that require notification **MUST** create their notification intent in the same transaction; delivery occurs after commit and cannot roll back or alter the underlying state. |
| DUR-017 | Baseline-only reconciliation **MUST** atomically store the complete bounded snapshot and its route baseline evidence, and **MUST NOT** create a policy decision, dispatch intent, production acknowledgement, Hermes task, or notification. |
| DUR-018 | Concurrent notification drainers **MUST** claim disjoint pending records through a durable lease or equivalent exclusion mechanism, and a crashed claim **MUST** become recoverable without changing notification identity. |

## 9. Route Concurrency and Latest-State Processing

| ID | Requirement |
|---|---|
| CON-001 | The Obsidian maintenance route **MUST** allow at most one unresolved authoritative Hermes maintenance task per `(route_id, destination_id)` lane. *(ADR-0016 supersedes the v0.1.4 route-wide scope while preserving single-active and latest-state collapse within each lane.)* |
| CON-002 | Relevant changes received while a task is unresolved **MUST** be durably retained as a dirty generation and **MUST NOT** be silently dropped. |
| CON-003 | Multiple dirty observations during one active task **MUST** collapse into at most one follow-up dispatch after completion or reconciliation. |
| CON-004 | Hermes **MUST** be instructed to evaluate the latest vault state at execution time. Event hashes are evidence, not a content snapshot contract. |
| CON-005 | A stale event **MUST NOT** force Hermes to recreate an obsolete historical state. |
| CON-006 | Route-level serialization **MUST** use a stable resource mutex when Hermes exposes that capability and local route-state enforcement regardless. |
| CON-007 | A destination lane's rejection, retry, unknown delivery, block, or completion **MUST NOT** prevent eligible sibling destinations from progressing. |
| CON-008 | Dirty observations **MUST** collapse independently within each destination lane. |
| CON-009 | Retrying one child **MUST** preserve its idempotency identity and **MUST NOT** duplicate accepted or completed siblings. |
| CON-010 | A behavior-affecting destination change **MUST** create a new destination revision and pause incompatible reuse until production acknowledgement. |
| CON-011 | Agent Dispatch **MUST** enforce at most one active child per effective serialization group in one shared state database, regardless of target-side mutex support. |
| CON-012 | Relevant work arriving for an occupied serialization group **MUST** merge into the selected destination lane's dirty generation; completion **MUST** promote at most one waiting lane and retries or reruns **MUST NOT** bypass the group slot. |
| CON-013 | Destinations governing the same resource under different serialization groups **MUST** fail preflight unless every involved route explicitly acknowledges cross-group concurrency. |
| CON-014 | Effective serialization-group identity and cross-group acknowledgement **MUST** participate in destination and route revision so a policy change makes existing production acknowledgement stale. |

## 10. Hermes Kanban Integration

| ID | Requirement |
|---|---|
| HER-001 | Hermes Kanban **MUST** be the primary durable target for v0.1. |
| HER-002 | The implementation **MUST NOT** assume exact Hermes commands until `E0-T4` records the real public interface and capability report. |
| HER-003 | The adapter **MUST** use argument arrays, structured input, bounded output, and no shell interpolation. |
| HER-004 | The adapter **MUST** declare whether it supports durable acceptance, idempotency key submission, lookup by idempotency key, external task lookup, mutex, execution status, cancellation, and result retrieval. |
| HER-005 | A route requiring a missing capability **MUST** fail validation or explicitly operate under a documented reduced guarantee. It **MUST NOT** silently emulate the capability. |
| HER-006 | A Hermes task **MUST** contain a Agent Dispatch dispatch ID, resource ID, latest-state instruction, bounded change manifest, route revision, configured profile, configured skill list, and acceptance criteria. |
| HER-007 | Note content **MUST NOT** be interpolated into operator instructions. Paths and source metadata **MUST** be encoded as structured untrusted data. |
| HER-008 | Hermes Kanban acceptance and Hermes execution status **MUST** be modeled separately. |
| HER-009 | If Hermes cannot provide machine-readable durable acceptance or lookup, the adapter **MUST** surface the limitation and the roadmap task **MAY** become blocked pending an explicit product decision. |
| HER-010 | The adapter **MUST NOT** access a Hermes internal database or private API. |
| HER-011 | Hermes versions below 0.19.1 **MUST** be rejected; versions at or above 0.19.1 **MUST** be treated as probe-eligible rather than automatically compatible, with no fixed maximum. |
| HER-012 | The product **MUST** provide a bounded public-interface capability probe and an inspectable cached report without requiring an operator-authored capability file. |
| HER-013 | Cached capability evidence **MUST** be invalidated when the executable path or digest, reported version, or probe-contract version changes. |
| HER-014 | Activation and submission **MUST** fail closed when required command or response shapes are absent, malformed, truncated, ambiguous, or over-bound. |
| HER-015 | Profiles **MUST** be enumerated through the public Hermes interface and a configured destination profile **MUST** exist on disk before enablement. |
| HER-016 | Every required skill **MUST** be proven enabled for the configured profile through the public profile-scoped Hermes interface before enablement. |
| HER-017 | Profile and skill failures **MUST** report bounded sorted alternatives and a concrete preflight remediation. |
| HER-018 | Route activation **MUST** bind the accepted capability-evidence fingerprint in addition to the computed route revision. |
| HER-019 | `--mutex-key` **MUST** be treated as an optional Hermes capability; a target missing only that flag remains compatible when Agent Dispatch can enforce a safe serialization topology. |
| HER-020 | Probe, preflight, enablement, rendering, submission-time revalidation, capabilities, and status **MUST** agree on whether serialization is target-enforced, Agent Dispatch group-enforced, or unsupported and unsafe. |
| HER-021 | The Hermes renderer **MUST NOT** send `--mutex-key` when current capability evidence says the executable does not support it. |

## 11. Hermes Webhook Integration

| ID | Requirement |
|---|---|
| WHK-001 | The Hermes webhook adapter **MUST** be implemented only after the Kanban path passes its production-capable gate. |
| WHK-002 | The webhook **MUST** be an explicit route target for immediate or stateless work, not an automatic Kanban fallback. |
| WHK-003 | Authentication secrets **MUST** be resolved outside stored event payloads and redacted from logs. |
| WHK-004 | The adapter **MUST** distinguish transport acceptance from durable task acceptance. |
| WHK-005 | A webhook retry **MUST** use the same dispatch idempotency key when the endpoint supports it. |

## 12. Feedback Loop and Provenance

| ID | Requirement |
|---|---|
| FBK-001 | Agent Dispatch **MUST** persist every relevant change observed while Hermes work is active. In-memory holding alone is prohibited. |
| FBK-002 | Agent Dispatch **MUST** suppress a self-generated result only when a validated work receipt and observed path/digest set match exactly. |
| FBK-003 | Missing, incomplete, invalid, or mismatched provenance **MUST** be treated as unknown and **MUST NOT** cause event deletion. |
| FBK-004 | Mixed human and agent changes **MUST** remain dirty and be re-evaluated. |
| FBK-005 | Agent Dispatch **MUST** provide public CLI commands for a cooperating Hermes task to begin, complete, or fail a work receipt without a Hermes plugin. |
| FBK-006 | A bundled Hermes companion skill **SHOULD** instruct the agent to use the receipt CLI and to process latest state. |
| FBK-007 | Git commits and commit messages **MAY** support attribution but **MUST NOT** be the sole trust anchor. |
| FBK-008 | Uncertain attribution **MUST** prefer an extra bounded follow-up over silent loss. |
| FBK-009 | Work receipts **MUST** distinguish `completed`, `partially_completed`, `blocked`, and `failed`. |
| FBK-010 | `partially_completed` **MUST** identify bounded completed and remaining scope; valid remaining scope creates at most one budgeted follow-up in the same destination lane. |
| FBK-011 | `blocked` **MUST** require manual intervention and **MUST NOT** trigger automatic work retry. |
| FBK-012 | Kanban acceptance or a terminal task status without an attributable bounded work receipt **MUST NOT** be reported as completed work. |

## 13. CLI and Operator Control

| ID | Requirement |
|---|---|
| CLI-001 | All machine-consumed commands **MUST** support structured JSON output. |
| CLI-002 | Human output and JSON output **MUST NOT** be mixed on standard output. Diagnostics belong on standard error. |
| CLI-003 | `dispatch --route <id>` **MUST** support Watchman input from standard input and a side-effect-free `--dry-run`. |
| CLI-004 | The CLI **MUST** provide config validation, route inspection and explicit enable/disable, Watchman installation/status, dispatch inspection, receipt inspection, quarantine inspection, retry, reprocess, rerun, reconcile, doctor, and retention maintenance commands as defined in the CLI contract. |
| CLI-005 | The CLI **MUST NOT** expose one ambiguous `replay` command. |
| CLI-006 | Manual release from quarantine **MUST** record actor, reason, previous decision, and new decision lineage. |
| CLI-007 | Destructive maintenance commands **MUST** require explicit flags and **MUST NOT** run from Watchman input. |
| CLI-008 | Exit codes **MUST** be stable and documented. |
| CLI-009 | The root command and every command group **MUST** support `-h` and `--help` with summaries, examples, defaults, side effects, exit codes, approval requirements, and the next safe command. |
| CLI-010 | The product **MUST** expose `hermes probe`, `hermes capabilities`, `hermes profiles`, and `route preflight`. |
| CLI-011 | The product **MUST** expose destination-qualified profile and skill updates and reject an ambiguous route-only update when multiple destinations exist. |
| CLI-012 | `setup wiki` **MUST** create disabled configuration, probe dependencies, test the Watchman binding state and print the explicit install and test commands (setup does not install the trigger), run initial reconciliation, and stop before enablement without explicit production approval. |
| CLI-013 | The product **MUST** expose aggregate event inspection and notification test, list, retry, and drain commands. |
| CLI-014 | Empty machine-readable collections **MUST** be `[]` or `{}`, never `null`. |
| CLI-015 | Configuration-mutating helpers **MUST** validate a candidate and replace the file atomically without modifying unrelated routes or destinations. |
| CLI-016 | `setup wiki` **MUST** accept an explicit route, require or clearly prompt for selection when multiple routes exist, and pass the selected route to every route-scoped command and instruction. |
| CLI-017 | `reconcile --baseline-only` **MUST** be a documented disabled-route operation with no submit path and **MUST** refuse active, uncertain, quarantined, or production-enabled state. |
| CLI-018 | The product **MUST** render and inspect a launchd schedule using resolved binary and configuration paths, bounded explicit logs, and history-preserving disable and uninstall instructions. |

## 14. Security and Privacy

| ID | Requirement |
|---|---|
| SEC-001 | Resource roots and route instructions **MUST** come from trusted operator configuration, not event payloads. |
| SEC-002 | Every path read **MUST** pass containment and symlink-escape checks. |
| SEC-003 | Source payloads, file names, front matter, URLs, and agent artifacts **MUST** be treated as hostile data. |
| SEC-004 | External processes **MUST** receive an allowlisted environment, controlled working directory, closed inherited descriptors, bounded stdout/stderr, and an execution timeout. |
| SEC-005 | Shell execution with interpolated event data **MUST NOT** be used. |
| SEC-006 | Secrets **MUST** be referenced by environment, protected file, OS credential mechanism, or file descriptor and **MUST NOT** enter SQLite or ordinary logs. |
| SEC-007 | Logs **MUST** redact credentials, authorization headers, query tokens, note bodies, and configured sensitive path components. |
| SEC-008 | Database and configuration permissions **SHOULD** be owner-only by default. |
| SEC-009 | Payload size, path length, file count, environment size, and subprocess output **MUST** have explicit limits. |
| SEC-010 | Dispatch **MUST** revalidate the active route revision and target capability requirements immediately before side effects. |
| SEC-011 | Notification payloads **MUST NOT** contain document contents, front matter, resolved secrets, authorization values, or unredacted sensitive paths. |
| SEC-012 | Notification endpoints and credentials **MUST** be trusted configuration using secret references; event and worker data **MUST NOT** select a sink. |
| SEC-013 | Webhook notifications **MUST** use HTTPS, reject redirects and ambient proxy routing, and bound request, response, and execution time. |
| SEC-014 | Hermes skill-list probing **MUST** use a fixed non-interactive rendering environment, bounded output, and fail-closed parsing without reading Hermes private storage. |

## 15. Observability, Retention, and Operations

| ID | Requirement |
|---|---|
| OPS-001 | Logs **MUST** be structured and include causal IDs without note bodies. |
| OPS-002 | State transitions, policy reasons, attempts, and receipts **MUST** be inspectable by CLI. |
| OPS-003 | The default retention policy **MUST** retain observations and ordinary attempts for 30 days, completed receipts for 180 days, and unresolved/quarantined/dead-lettered records until resolution. |
| OPS-004 | Retention **MUST** be configurable and pruning **MUST** preserve referential and audit integrity. |
| OPS-005 | Startup and `doctor` **MUST** detect database migration state, invalid configuration, inaccessible roots, unsupported filesystem placement, missing Watchman, and unavailable Hermes capabilities. |
| OPS-006 | Full reconciliation **MUST** be available on startup uncertainty, Watchman overflow/fresh instance, explicit operator request, and scheduled daily operation. |
| OPS-007 | Daily reconciliation **SHOULD** be installed through platform scheduling recipes rather than a Agent Dispatch daemon in v0.1. |
| OPS-008 | SQLite **MUST** enable foreign keys, a busy timeout, crash-safe journaling appropriate for concurrent one-shot processes, and documented synchronous durability. |
| OPS-009 | Database migrations **MUST** be forward-only, transactional where SQLite permits, and tested against interrupted upgrades. |
| OPS-010 | Status **MUST** expose configured and actual watch roots, relative root, effective patterns, trigger identity, and installed/missing/drifted state. |
| OPS-011 | Status **MUST** distinguish detection, planning, quarantine, pending delivery, acceptance, running, work outcomes, unknown delivery, reconciliation, and manual intervention. |
| OPS-012 | Reconciliation hashing **MUST** read at most `max_hash_file_bytes + 1` (the configured hash bound, `limits.max_hash_file_bytes`); a stable over-bound file is quarantined and a file unstable twice remains explicit reconciliation evidence. |
| OPS-013 | Capability, profile, skill, Watchman, and reconciliation drift **MUST** be visible in status and eligible for configured notifications. |
| OPS-014 | v0.1.5 configuration **MUST** retain `version: 1`, require `destinations[]`, and reject legacy `dispatch` with a concrete regeneration path. |
| OPS-015 | Rollback **MUST** preserve the upgraded database separately and restore the verified pre-migration backup with the previous readable binary and configuration; down migrations are not required. |
| OPS-016 | Setup output **MUST** distinguish configuration enabled state, runtime activation, Watchman binding, initial baseline, and production acknowledgement, and **MUST** print but never execute the reviewed enable command. |
| OPS-017 | Status and doctor **MUST** expose pending notification count and age, latest drain evidence, automatic mode and limit, expected scheduler state, overdue scheduled delivery, repeated ambiguous or retryable outcomes, and unresolvable sinks. |
| OPS-018 | A scheduled notification recipe **MUST** run drain only after healthy submitted reconciliation and **MUST NOT** assume `/usr/local/bin/agent-dispatch` or delete configuration or SQLite state on uninstall. |

## 16. Test and Release Quality

| ID | Requirement |
|---|---|
| TST-001 | Pure domain policy, canonicalization, and state transitions **MUST** have deterministic unit tests. |
| TST-002 | SQLite repositories and migrations **MUST** have integration tests using real SQLite files. |
| TST-003 | Watchman fixtures **MUST** cover create, modify, delete, atomic save, repeated save, overflow, fresh instance, bulk copy, ignored files, traversal, symlink escape, and malformed input. |
| TST-004 | Crash-injection tests **MUST** cover every boundary before intent commit, after intent commit, during submit, after remote acceptance before local receipt, and during migration. |
| TST-005 | Concurrency tests **MUST** run multiple one-shot processes against one database and prove one attempt owner and one active route dispatch. |
| TST-006 | Fake Hermes adapters **MUST** simulate accepted, rejected, timeout-before-accept, timeout-after-accept, malformed response, duplicate idempotency, unavailable lookup, and status progression. |
| TST-007 | A real Hermes compatibility test **MUST** run before release when a real public interface is available. |
| TST-008 | Automatic agent writes **MUST NOT** be enabled until all production-capable acceptance gates pass. |
| TST-009 | Every roadmap task **MUST** add or update tests, documentation, and traceability before completion. |
| TST-010 | Real or frozen-real Watchman evidence **MUST** cover ancestor roots, relative roots, exclusion forms, drift, test, and complete removal. |
| TST-011 | Reconciliation tests **MUST** race ordinary path-fact updates against full enumeration and prove that a growing file cannot exceed the read bound. |
| TST-012 | The same probe path **MUST** evaluate the frozen real Hermes 0.19.1 interface and the installed newer Hermes interface without modifying either Hermes installation. |
| TST-013 | Fan-out tests **MUST** cover different profiles, repeated profiles with distinct workstreams, sibling isolation, independent retry, destination revision changes, and aggregate reruns. |
| TST-014 | Notification tests **MUST** cover transactional intent creation, deduplication, ambiguous delivery, retry, sink isolation, and content/secret redaction. |
| TST-015 | Guided-setup tests **MUST** cover clean host, multiple-route selection, Watchman installation, interrupted baseline, idempotent rerun, zero task/acknowledgement/notification creation, and the five-state summary. |
| TST-016 | Hermes compatibility tests **MUST** cover the real or frozen-real v0.20.5 mutex-only downgrade, v0.19.1 regression, burst merging, shared-group exclusion, independent-group concurrency, and retry/rerun slot retention. |
| TST-017 | Notification progress tests **MUST** cover after-command completion delivery, timeout with unchanged work state, crash recovery, stable identity, concurrent drain leases, configured limits, manual mode, scheduled rendering, non-recursion, and successful core exit under delivery failure. |

## 17. Multi-Destination Fan-Out

| ID | Requirement |
|---|---|
| FAN-001 | A route **MUST** declare one or more destinations under `destinations[]`; every destination has a unique stable ID and non-empty workstream. |
| FAN-002 | Destinations **MAY** select different profiles or use the same profile for different workstreams. |
| FAN-003 | Each selected destination **MUST** create one independent durable child intent beneath the aggregate event. |
| FAN-004 | Destination selection **MUST** use only closed structural conditions over path, operation, classification, and policy outcome. |
| FAN-005 | Values within one condition class use OR; present condition classes use AND; absent conditions select the destination. |
| FAN-006 | The v0.1.5 `fanout_mode` vocabulary contains only `all`; any other value **MUST** fail configuration validation. |
| FAN-007 | Re-running an aggregate event **MUST** reuse accepted or completed child outcomes and create only missing or explicitly new-generation work. |
| FAN-008 | One child's delivery or work failure **MUST NOT** roll back or rewrite sibling outcomes. |
| FAN-009 | Every child task **MUST** preserve the trusted-instruction and untrusted-manifest boundary. |
| FAN-010 | Aggregate inspection **MUST** expose selection reason, destination revision, child identity, acceptance, execution, receipt, and retry state. |
| FAN-011 | A route may reference named targets per destination, while the certified v0.1.5 path remains one Hermes Kanban target with multiple destinations. |
| FAN-012 | Configuration ordering **MUST NOT** affect destination revision, selection, event identity, or child idempotency. |

## 18. Operator Notifications

| ID | Requirement |
|---|---|
| NTF-001 | Notifications **MUST** be configurable per route, event type, and sink and disabled when no sinks are configured. |
| NTF-002 | When sinks exist and events are omitted, defaults **MUST** cover completed work, exhausted failure, unknown delivery, quarantine, reconciliation required, integration drift, and Watchman drift. |
| NTF-003 | Notification identity **MUST** include event, optional destination, transition, sink, and notification-policy revision. |
| NTF-004 | Every notification attempt and outcome **MUST** be durable and inspectable. |
| NTF-005 | Notification failure or unknown delivery **MUST NOT** modify event, child dispatch, acceptance, execution, or work-receipt state. |
| NTF-006 | The release **MUST** ship a channel-neutral event contract plus structured log (stderr) and HTTPS webhook sinks. |
| NTF-007 | Retry **MUST** reuse a stable notification idempotency key and remain at-least-once under ambiguous transport outcomes. |
| NTF-008 | Operators **MUST** be able to test a sink without creating a source event or Hermes task. |
| NTF-009 | Adding a future channel adapter **MUST NOT** require changing dispatch or work-completion state semantics. |
| NTF-010 | Each route **MUST** support `manual`, `after-command`, and `scheduled` bounded drain modes; existing configurations that omit the policy retain manual behavior and newly generated Wiki configuration defaults to after-command. |
| NTF-011 | In after-command mode, one bounded drain pass **MUST** begin only after a successful notification-producing source transition commits. |
| NTF-012 | Notification delivery failure **MUST NOT** roll back source state, change dispatch acceptance or work completion, cause task resubmission, or change a successful core command exit status. |
| NTF-013 | Ambiguous and retryable delivery **MUST** remain pending under the same stable identity; refused delivery **MUST** remain inspectable and explicitly retryable. |
| NTF-014 | Automatic drain **MUST** enforce the configured limit and **MUST NOT** create a notification solely about successful notification draining. |
| NTF-015 | Existing v0.1.5 notification records **MUST** migrate forward without changing notification IDs, idempotency keys, or attempt history. |

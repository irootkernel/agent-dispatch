# Required Specification

## 1. Interpretation

This document is normative. Each requirement has a stable ID used by the roadmap and acceptance matrix.

- **MUST** requirements block v0.1 release.
- **SHOULD** requirements require an explicit recorded exception if not met.
- **MAY** requirements are optional.

## 2. Product Boundary

| ID | Requirement |
|---|---|
| BND-001 | JJUKKUMI **MUST** operate as an event-ingress and activation gateway, not as an agent runtime or semantic knowledge engine. |
| BND-002 | Hermes **MUST** be treated as the authoritative runtime for task execution and semantic outcomes. |
| BND-003 | v0.1 **MUST NOT** modify Hermes core, access Hermes internal storage, or require a Hermes plugin. |
| BND-004 | Hermes integration **MUST** use public machine interfaces. A JJUKKUMI-owned adapter, CLI, or skill is permitted. |
| BND-005 | JJUKKUMI **MUST NOT** directly edit governed vault content. |
| BND-006 | JJUKKUMI **MUST NOT** perform open-ended LLM classification inside the bridge. |
| BND-007 | A future Hermes management plugin **MAY** be documented but **MUST NOT** be a v0.1 dependency. |

## 3. v0.1 Scope and Platform

| ID | Requirement |
|---|---|
| SCP-001 | The certified v0.1 use case **MUST** support one Obsidian vault, one Watchman source, one route, and one primary Hermes Kanban target. |
| SCP-002 | Internal configuration structures **SHOULD** use named resources and routes so later multi-vault support does not require a format replacement. |
| SCP-003 | v0.1 **MUST** process Markdown files with the `.md` extension. |
| SCP-004 | Attachments, PDFs, images, Canvas files, and arbitrary binary files **MUST** be out of scope for automatic dispatch in v0.1. |
| SCP-005 | The implementation **MUST** be written in Go and pin its toolchain and dependencies. |
| SCP-006 | Operator configuration **MUST** be YAML and validated before side effects. |
| SCP-007 | Durable local state **MUST** use SQLite on a local filesystem. Network filesystem state is unsupported. |
| SCP-008 | v0.1 **MUST** be verified on macOS and one supported Linux CI environment. |
| SCP-009 | Git **MAY** enrich evidence but **MUST NOT** be required for basic ingestion and dispatch. |

## 4. Watchman Source

| ID | Requirement |
|---|---|
| SRC-001 | The first source adapter **MUST** accept Watchman trigger JSON on standard input. |
| SRC-002 | The adapter **MUST** read and persist relevant Watchman trigger environment fields, including root identity, trigger identity, since/clock position, relative root, and overflow indication when supplied. |
| SRC-003 | The adapter **MUST** validate that the invocation is bound to the configured route and resource. Payload data **MUST NOT** select another route or resource. |
| SRC-004 | Canonical operations **MUST** be `create`, `modify`, and `delete`. Rename **MAY** be represented only as derived evidence and **MUST NOT** be required for correctness. |
| SRC-005 | A fresh instance, lost cursor, recrawl uncertainty, or overflow **MUST NOT** dispatch a partial ordinary batch. It **MUST** create or merge one reconciliation request. |
| SRC-006 | JJUKKUMI **MUST NOT** add a second time-based settle delay in one-shot trigger mode. It **MUST** treat the Watchman trigger input as the source batch. |
| SRC-007 | Trigger registration **MUST** use an explicit unique trigger name and **MUST** avoid destructive unnecessary re-registration. |
| SRC-008 | The source adapter **MUST** support deterministic fixture input without a running Watchman daemon for tests. |

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

## 9. Route Concurrency and Latest-State Processing

| ID | Requirement |
|---|---|
| CON-001 | The Obsidian maintenance route **MUST** allow at most one unresolved authoritative Hermes maintenance task at a time. |
| CON-002 | Relevant changes received while a task is unresolved **MUST** be durably retained as a dirty generation and **MUST NOT** be silently dropped. |
| CON-003 | Multiple dirty observations during one active task **MUST** collapse into at most one follow-up dispatch after completion or reconciliation. |
| CON-004 | Hermes **MUST** be instructed to evaluate the latest vault state at execution time. Event hashes are evidence, not a content snapshot contract. |
| CON-005 | A stale event **MUST NOT** force Hermes to recreate an obsolete historical state. |
| CON-006 | Route-level serialization **MUST** use a stable resource mutex when Hermes exposes that capability and local route-state enforcement regardless. |

## 10. Hermes Kanban Integration

| ID | Requirement |
|---|---|
| HER-001 | Hermes Kanban **MUST** be the primary durable target for v0.1. |
| HER-002 | The implementation **MUST NOT** assume exact Hermes commands until `E0-T4` records the real public interface and capability report. |
| HER-003 | The adapter **MUST** use argument arrays, structured input, bounded output, and no shell interpolation. |
| HER-004 | The adapter **MUST** declare whether it supports durable acceptance, idempotency key submission, lookup by idempotency key, external task lookup, mutex, execution status, cancellation, and result retrieval. |
| HER-005 | A route requiring a missing capability **MUST** fail validation or explicitly operate under a documented reduced guarantee. It **MUST NOT** silently emulate the capability. |
| HER-006 | A Hermes task **MUST** contain a JJUKKUMI dispatch ID, resource ID, latest-state instruction, bounded change manifest, route revision, configured profile, configured skill list, and acceptance criteria. |
| HER-007 | Note content **MUST NOT** be interpolated into operator instructions. Paths and source metadata **MUST** be encoded as structured untrusted data. |
| HER-008 | Hermes Kanban acceptance and Hermes execution status **MUST** be modeled separately. |
| HER-009 | If Hermes cannot provide machine-readable durable acceptance or lookup, the adapter **MUST** surface the limitation and the roadmap task **MAY** become blocked pending an explicit product decision. |
| HER-010 | The adapter **MUST NOT** access a Hermes internal database or private API. |

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
| FBK-001 | JJUKKUMI **MUST** persist every relevant change observed while Hermes work is active. In-memory holding alone is prohibited. |
| FBK-002 | JJUKKUMI **MUST** suppress a self-generated result only when a validated work receipt and observed path/digest set match exactly. |
| FBK-003 | Missing, incomplete, invalid, or mismatched provenance **MUST** be treated as unknown and **MUST NOT** cause event deletion. |
| FBK-004 | Mixed human and agent changes **MUST** remain dirty and be re-evaluated. |
| FBK-005 | JJUKKUMI **MUST** provide public CLI commands for a cooperating Hermes task to begin, complete, or fail a work receipt without a Hermes plugin. |
| FBK-006 | A bundled Hermes companion skill **SHOULD** instruct the agent to use the receipt CLI and to process latest state. |
| FBK-007 | Git commits and commit messages **MAY** support attribution but **MUST NOT** be the sole trust anchor. |
| FBK-008 | Uncertain attribution **MUST** prefer an extra bounded follow-up over silent loss. |

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

## 15. Observability, Retention, and Operations

| ID | Requirement |
|---|---|
| OPS-001 | Logs **MUST** be structured and include causal IDs without note bodies. |
| OPS-002 | State transitions, policy reasons, attempts, and receipts **MUST** be inspectable by CLI. |
| OPS-003 | The default retention policy **MUST** retain observations and ordinary attempts for 30 days, completed receipts for 180 days, and unresolved/quarantined/dead-lettered records until resolution. |
| OPS-004 | Retention **MUST** be configurable and pruning **MUST** preserve referential and audit integrity. |
| OPS-005 | Startup and `doctor` **MUST** detect database migration state, invalid configuration, inaccessible roots, unsupported filesystem placement, missing Watchman, and unavailable Hermes capabilities. |
| OPS-006 | Full reconciliation **MUST** be available on startup uncertainty, Watchman overflow/fresh instance, explicit operator request, and scheduled daily operation. |
| OPS-007 | Daily reconciliation **SHOULD** be installed through platform scheduling recipes rather than a JJUKKUMI daemon in v0.1. |
| OPS-008 | SQLite **MUST** enable foreign keys, a busy timeout, crash-safe journaling appropriate for concurrent one-shot processes, and documented synchronous durability. |
| OPS-009 | Database migrations **MUST** be forward-only, transactional where SQLite permits, and tested against interrupted upgrades. |

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

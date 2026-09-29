# Required Specification

## 1. Interpretation

This document is normative. Each requirement has a stable ID used by the
roadmap and acceptance matrix. v0.1.8 is the shipped baseline. D-030 admits the
v0.2.0 two-node Wiki sync target under `SYN-*`; E20 through E22 completed that
work. Every unamended earlier requirement remains binding in v0.2.0 whether
sync is enabled or disabled.

Section 20 defines planned successor requirements for E23. `SMR-*` applies
only to the later management-provider release, whose version is not yet
selected. It neither reopens completed E20-E22 work nor adds a v0.2.0 release
condition. Current v0.2.0 configurations and capabilities remain unchanged.

- **MUST** requirements block their explicitly applicable release. `SMR-*`
  does not block the current v0.2.0 release.
- **SHOULD** requirements require an explicit recorded exception if not met.
- **MAY** requirements are optional.

## 2. Product Boundary

| ID | Requirement |
|---|---|
| BND-001 | Agent Dispatch **MUST** operate as an event-ingress and activation gateway, not as an agent runtime or semantic knowledge engine. |
| BND-002 | Hermes **MUST** be treated as the authoritative runtime for task execution and semantic outcomes. |
| BND-003 | v0.1 **MUST NOT** modify Hermes core, access Hermes internal storage, or require a Hermes plugin. |
| BND-004 | Hermes integration **MUST** use public interfaces. Machine-readable forms are required where the public command provides them; D-025 permits a bounded fail-closed Agent Dispatch parser for the public profile-scoped skill list because Hermes exposes no JSON form. A Agent Dispatch-owned adapter, CLI, or skill is permitted. |
| BND-005 | Agent Dispatch **MUST NOT** directly edit governed vault content except through the validated, journaled sync import boundary admitted by D-030 and `SYN-*`. |
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
| SCP-008 | Agent Dispatch **MUST** be verified on each supported platform: `darwin/arm64`, `linux/amd64`, and `linux/arm64`. Hosted CI is not used; run `make verify` on each target platform. *(Reactivated by D-029, E19-T2: supersedes the D-023/D-024 darwin/arm64-only exclusivity; the D-023 retirement note and the historical D-020 Linux closure stand as history. D-028 remains the Absolute Watch-Root Binding decision.)* |
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
| SRC-009 | A managed Watchman binding **MUST** retain the configured resource root, actual Watchman root, relative root, and trigger name as distinct recorded values. Since D-028 the actual Watchman root **MUST** be the configured resource root itself and the relative root **MUST** be the schema-vestigial constant `.`. |
| SRC-010 | `watchman install`, `status`, `test`, and `remove` **MUST** resolve and report the same effective binding. |
| SRC-011 | Installation **MUST** establish the configured absolute resource root itself as the Watchman watch root. It **MUST NOT** bind an ancestor watch root, **MUST NOT** send `relative_root` on the managed trigger definition, and **MUST** fail closed with actionable unwatch guidance when the server cannot watch that root as its own watch root (amended by D-028; the ancestor-plus-`relative_root` constraint is retired). |
| SRC-012 | A successful remove **MUST** prove that no trigger with the managed identity remains on any applicable Watchman root. |
| SRC-013 | A live trigger invocation **MUST** be accepted only when its canonicalized `WATCHMAN_ROOT` equals the configured resource root. `WATCHMAN_RELATIVE_ROOT` **MUST NOT** act as a second binding axis, and its presence **MUST** fail closed as a stale relative-root trigger (D-028). |

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
| DUR-018 | Concurrent notification drainers **MUST** claim disjoint due records through a durable lease with a fencing token, a crashed claim **MUST** become recoverable without changing notification identity, and a stale owner **MUST NOT** record an outcome after ownership changes. |

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
| CON-011 | Agent Dispatch **MUST** resolve every destination group from explicit `serialization_group`, deprecated `mutex_key`, then exact default `resource:<resource_id>` and enforce at most one active child per effective group in one shared state database, regardless of target-side mutex support; explicit groups **MUST** use the bounded ASCII grammar, and simultaneous alias fields **MUST** agree or fail validation. |
| CON-012 | Relevant work arriving for an occupied serialization group **MUST** merge into the selected destination lane's dirty generation; completion **MUST** promote at most the oldest first-dirty waiting lane with destination ID as the deterministic tie break, and retries or reruns **MUST NOT** bypass the group slot. |
| CON-013 | Destinations governing the same resource under different serialization groups **MUST** fail preflight unless every involved route sets `allow_cross_group_concurrency: true`; preserved migration-time conflicts **MUST** select no arbitrary holder, block all new group work without rewriting existing identities, retain safe completion and recovery exits, and resolve atomically to the sole remaining holder or oldest waiting lane. |
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
| HER-011 | The product and configured target floor **MUST** be at least 0.20.5; an omitted or lower configured floor and an installed version below the configured floor **MUST** fail before side effects without implicit rewriting. Versions meeting the configured floor **MUST** be treated as probe-eligible rather than automatically compatible, with no fixed maximum. |
| HER-012 | The product **MUST** provide a bounded public-interface capability probe and an inspectable cached report without requiring an operator-authored capability file. |
| HER-013 | Cached capability evidence **MUST** be invalidated when the executable path or digest, reported version, or probe-contract version changes. |
| HER-014 | Activation and submission **MUST** fail closed when required command or response shapes are absent, malformed, truncated, ambiguous, or over-bound. |
| HER-015 | Profiles **MUST** be enumerated through the public Hermes interface and a configured destination profile **MUST** exist on disk before enablement. |
| HER-016 | Every required skill **MUST** be proven enabled for the configured profile through the public profile-scoped Hermes interface before enablement. |
| HER-017 | Profile and skill failures **MUST** report bounded sorted alternatives and a concrete preflight remediation. |
| HER-018 | Route activation **MUST** bind the accepted capability-evidence fingerprint in addition to the computed route revision. |
| HER-019 | `--mutex-key` **MUST** be treated as optional defense-in-depth; a target missing only that flag remains compatible when Agent Dispatch can enforce a safe serialization topology, and target support never replaces the local slot. |
| HER-020 | Probe, preflight, enablement, rendering, submission-time revalidation, capabilities, and status **MUST** agree on `agent-dispatch-group-enforced`, `agent-dispatch-group-plus-target-mutex`, or `unsupported-unsafe`. |
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
| FBK-002 | Agent Dispatch **MUST** suppress a self-generated result only when either a validated work receipt or a separate validated sync-import record matches the observed path/digest-or-absence set exactly. An import record **MUST NOT** be represented as a Hermes work receipt. *(amended by D-030)* |
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
| CLI-018 | The product **MUST** render, install, inspect, disable, and uninstall one exact managed schedule per route for `--platform launchd|systemd` using resolved binary and configuration paths, bounded explicit logs, and history-preserving lifecycle operations. |
| CLI-019 | `hermes set-minimum-version <target> <version>` **MUST** accept only a floor at or above 0.20.5, atomically update only the selected target, preserve unrelated configuration, and stale every affected route for re-probe, preflight, and production re-acknowledgement. |

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
| OPS-007 | Daily reconciliation **SHOULD** be installed through platform scheduling recipes rather than an Agent Dispatch daemon in v0.1; this does not prohibit the bounded sync service admitted by D-030 under SYN-008 and SYN-014. *(amended by D-030)* |
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
| OPS-018 | Automatic notification modes **MUST** require an installed, loaded, definition-matching schedule before production enablement; managed identity **MUST** bind instance, route, and configuration-path digest; the platform unit (launchd plist or systemd user unit/timer) **MUST** invoke an internal runner without a shell; install **MUST** be idempotent and refuse a different definition; disable and uninstall **MUST** preserve configuration, SQLite state, logs, and notification history; after-command recovery runs every fifteen minutes, scheduled mode defaults to 03:00 local and drains only after healthy submitted reconciliation, and logs retain at most three 10 MiB files. |

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
| TST-010 | Real or frozen-real Watchman evidence **MUST** cover the exact configured watch root, the blocked-ancestor failure guidance, exclusion forms, drift, test, and complete removal (restated by D-028; ancestor-root and relative-root acceptance coverage is retired). |
| TST-011 | Reconciliation tests **MUST** race ordinary path-fact updates against full enumeration and prove that a growing file cannot exceed the read bound. |
| TST-012 | The same probe path **MUST** evaluate the real Hermes 0.20.5 interface and synthetic probe-compatible later interfaces without modifying the Hermes installation, while 0.20.4 and lower fail before side effects. |
| TST-013 | Fan-out tests **MUST** cover different profiles, repeated profiles with distinct workstreams, sibling isolation, independent retry, destination revision changes, and aggregate reruns. |
| TST-014 | Notification tests **MUST** cover transactional intent creation, deduplication, ambiguous delivery, retry, sink isolation, and content/secret redaction. |
| TST-015 | Guided-setup tests **MUST** cover clean host, multiple-route selection, Watchman installation, interrupted baseline, idempotent rerun, zero task/acknowledgement/notification creation, and the five-state summary. |
| TST-016 | Hermes compatibility tests **MUST** cover real or frozen-real v0.20.5 group enforcement without target mutex, a synthetic later target-mutex complement, configured and installed below-floor refusal, atomic floor updates, alias agreement/conflict, exact default identity, burst merging, shared-group exclusion, acknowledged independent-group concurrency, FIFO promotion, migration conflict and recovery, and retry/rerun slot retention. |
| TST-017 | Notification progress tests **MUST** cover after-command completion delivery and existing-due progress, one-invocation ten-second budgeting with multi-route fairness, fifteen-minute fallback recovery, migrated pending due state, persisted symmetric retry jitter, manual due-only drain and explicit retry reset, timeout with unchanged work state, crash recovery, stable identity, concurrent fenced leases, stale-owner refusal, configured limits, managed schedule identity/idempotency/drift/rotation/lifecycle, non-recursion, and successful core exit under delivery failure. |

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
| NTF-011 | In after-command mode, one route-scoped drain pass bounded by the configured item limit and ten seconds **MUST** begin only after a successful notification-producing source transition commits, and a managed fifteen-minute recovery schedule **MUST** be healthy before production enablement. |
| NTF-012 | Notification delivery failure **MUST NOT** roll back source state, change dispatch acceptance or work completion, cause task resubmission, or change a successful core command exit status. |
| NTF-013 | Ambiguous and retryable delivery **MUST** remain pending under the same stable identity with a persisted exponential-backoff deadline whose jitter is sampled once from plus or minus the configured fraction; automatic and manual drain **MUST** select due work only, while explicit retry **MUST** return ambiguous, retryable, or refused work to pending and immediately due. |
| NTF-014 | Automatic drain **MUST** enforce the configured item limit and one ten-second wall-clock budget per CLI invocation, visit multiple affected routes in deterministic one-item rounds, release unstarted claims when the budget expires, cancel started delivery through context while preserving fenced recovery, and **MUST NOT** create a notification solely about successful notification draining. |
| NTF-015 | Existing v0.1.5 notification records **MUST** migrate forward immediately due without changing notification IDs, idempotency keys, or attempt history. |
| NTF-016 | A successful registered command **MUST** drain due work for every affected after-command route after core commit even when it created no notification; a failed core command **MUST NOT** auto-drain, and automatic delivery **MUST** preserve the command's stdout, JSON, and exit contract. |

## 19. Two-Node Wiki Sync

| ID | Requirement |
|---|---|
| SYN-001 | The v0.2.0 sync capability **MUST** be opt-in, disabled by default, and limited to one group, one governed Git working copy, one configured content ref, one configured membership ref, and exactly two active nodes. Existing ingestion and dispatch **MUST** remain usable when sync is absent or disabled. |
| SYN-002 | Git **MUST** be the shared content-history authority for sync. SQLite databases, journals, credentials, locks, caches, local index state, and machine-specific Hermes or Obsidian configuration **MUST NOT** enter the synchronized scope or a network filesystem. |
| SYN-003 | Synchronized content **MUST** be limited to `.md` and `.markdown` files plus minimal controller publication manifests. Route-protected and immutable paths **MUST** remain outside the synchronized content scope, and a publication or import candidate that touches one **MUST** fail closed before content mutation. Attachments, submodules, symlinks, unsupported file modes, unsafe path aliases, arbitrary filters, and unexpected LFS behavior **MUST** fail closed. |
| SYN-004 | `sync publish` **MUST** be an explicit operator command. It **MUST** consume an eligible validated maintenance snapshot while holding the resource guard, persist intent before effects, create an SSH Ed25519-signed commit from a frozen snapshot, use a fast-forward push, reconcile ambiguous push results, and create a peer nudge obligation only after remote publication is confirmed. Publisher private-key resolution and signing **MUST** occur only in the explicit CLI process; unattended recovery may finish only already-signed push, confirmation, and nudge work. |
| SYN-005 | Membership **MUST** be a signed, versioned history on a separate configured Git ref with an out-of-band pinned administrator trust anchor and distinct administrator and per-node publisher SSH keys. The normal operating state **MUST** bind exactly two active `instance_id` entries, while retained retired/revoked entries and an emergency blocked state with fewer than two active entries remain representable. Bootstrap and every membership mutation **MUST** use `sync membership plan` and `sync membership apply`, a reviewed expected predecessor, an administrator signature, and a non-force update. A publication **MUST** name a verified ancestor membership revision in which its publisher key is active. Previously verified evidence remains historical after rotation, but previously unseen history from a removed key **MUST** require a current administrator checkpoint without using commit timestamps as authority. Signing keys **MUST** remain local secret references and unavailable to the peer service. |
| SYN-006 | Peer requests **MUST** use Tailscale HTTPS to a local-only Unix socket, loopback, or otherwise tailnet-only listener plus a separately provisioned credential for each direction, bound by the receiver to sender, receiver, and group. A local Unix socket **MUST** reside in an owner-only directory. The implementation **MUST** verify the configured peer endpoint and certificate, **MUST NOT** bind a public interface or enable Funnel, and **MUST NOT** modify Tailscale configuration. Tailnet reachability, Tailscale headers, or a claimed sender field alone **MUST NOT** authorize a request. |
| SYN-007 | A peer nudge **MUST** be a bounded wake-up hint and **MUST NOT** contain note bodies, arbitrary commands, repository locations, paths, executables, profiles, credentials, or request-selected refs. HTTP 202 **MUST** mean only that the durable inbox transaction committed. |
| SYN-008 | Startup and bounded periodic reconciliation **MUST** recover missed nudges, ambiguous already-signed publication and delivery outcomes, and pending imports from configured Git refs without an LLM call. A pre-signature publication interruption **MUST** remain pending for explicit `sync publish` re-entry under the same logical identity. Duplicate and reordered nudges **MUST** reuse stable logical identities. |
| SYN-009 | Automatic live-tree import **MUST** require a current cooperative-import acknowledgement bound to the resource, remote/refs, scope, local identity, pinned administrator trust anchor, and safety policy, meaning the SYN-010 guard set together with the configured import bounds. Without it, the system may fetch and validate but **MUST** defer application. Changes to those inputs **MUST** invalidate it; membership changes accepted under the same trust policy invalidate verification targets but not the acknowledgement. Publication and production activation are separate and **MUST NOT** depend on this acknowledgement. |
| SYN-010 | Import **MUST** persist a pre-apply plan and journal, hold the resource writer guard, reject Git operation/index/ref instability, overlapping staged or unstaged changes, and untracked overwrite collisions, preserve proven-disjoint local changes and out-of-scope or ignored files, preserve Watchman observations, and suppress only exact import effects proven by a separate durable import record with path and digest-or-absence evidence. If path disjointness cannot be proven, application **MUST** defer. |
| SYN-011 | Publication and import **MUST** be fast-forward-only. Import **MUST** start from an administrator-signed adoption checkpoint, and every commit to the target **MUST** be covered by a verified publication or later checkpoint. Divergence, uncovered or over-bound history, unexpected local state, remote history rewrite, trust failure, or unexplained partial effects **MUST** preserve data and produce an inspectable blocked or uncertain result. Conflict recovery **MUST** require ordinary Git resolution, `sync checkpoint plan`, `sync checkpoint apply`, and `sync reconcile`; reconciliation alone cannot adopt history. Agent Dispatch **MUST NOT** merge, rebase, stash, force-push, reset, or clean automatically. |
| SYN-012 | Publication preparation, remote publication, nudge acceptance, local import, historical delivery, and fresh pair convergence **MUST** be separate durable outcomes. A timeout, lease expiry, or missing process **MUST NOT** by itself prove completion or safe writer takeover. |
| SYN-013 | Fresh pair verification **MUST** pin and recheck group, membership revision, content ref, target commit, scope and contract digests, both node identities, and both incarnations. Equal commits **MUST NOT** produce success when either node has governed dirtiness, pending work, stale evidence, or unresolved uncertainty. |
| SYN-014 | The sync CLI **MUST** expose truthful `capabilities`, `status`, `publish`, `reconcile`, `verify`, `serve`, `pause`, and `resume` commands; two-phase membership `plan` and `apply` and checkpoint `plan` and `apply` administrator commands; and service `render`, `install`, `inspect`, `stop`, `disable`, and `uninstall` lifecycle commands through versioned machine contracts. Reserved but unimplemented capabilities **MUST** fail closed without Git, network, filesystem, or activation side effects. |
| SYN-015 | Sync queues, retries, payloads, history inspection, subprocess time and output, retained evidence, service concurrency, and shutdown **MUST** have documented tested bounds. Exhaustion **MUST** remain visible and **MUST NOT** silently discard a publication, import, peer, or verification obligation. |

## 20. Post-v0.2.0 Constrained Sync Management

These are planned E23 requirements, not implemented v0.2.0 behavior. The
[management contract](../contracts/sync-management-contract.md) specifies the
proposed boundaries; E23-T1 freezes its executable contract before runtime
work. Plugin EPIC-008 consumes the qualified provider later and is not a Core
correctness dependency.

| ID | Requirement |
|---|---|
| SMR-001 | The successor management provider **MUST** be independently opt-in, disabled by default, and restricted to the current local node and configured two-node group. Its absence or disablement **MUST** preserve ordinary sync and existing CLI behavior. |
| SMR-002 | Core **MUST** enforce an operator-owned action allowlist and relevant configuration, policy, group, and state-incarnation bindings at preparation, submission, and execution/recovery boundaries. Model text, claimed identities, request IDs, and caller-supplied confirmation **MUST NOT** confer authority. Revocation **MUST** prevent new effects without deleting accepted evidence. |
| SMR-003 | The admitted actions **MUST** be limited to `sync-now`, `verify-all`, `retry`, local `pause`, and local `resume`. Management **MUST NOT** create/sign publications, change membership/checkpoints, configure services or Tailscale, control a remote node, or accept arbitrary paths, executables, refs, remotes, profiles, credentials, force flags, or note bodies. |
| SMR-004 | A caller **MUST** obtain a durable prepared request handle before submission can execute an action. Preparation **MUST** bind immutable action inputs and preconditions and **MUST NOT** enqueue executable work or cause sync, control, network, or signing effects. Expiring unsubmitted drafts **MUST** have bounded storage. |
| SMR-005 | Submission **MUST** require an existing current handle, persist acceptance before effects, and return the same identity/result for duplicate or concurrent submissions. Missing, expired, retired, altered, wrong-group, or obsolete-incarnation handles **MUST NOT** create or restamp a request. |
| SMR-006 | Long-running requests **MUST** execute through the existing Core sync worker and shared application paths independently of the submitting process. Core **MUST** own durable request/job associations, claims, fencing, outcomes, and recovery without a second scheduler, general job framework, Plugin database, or alternative Git implementation. |
| SMR-007 | Exact-ID lookup **MUST** remain a bounded non-mutating read even when new management submissions are disabled. It **MUST** report that request's actual acceptance, execution and linked outcomes rather than infer them from latest status. Accepted, action-finished, publication, delivery, import, and fresh pair convergence **MUST** remain distinct. |
| SMR-008 | Response loss, caller termination, output rejection, timeout, or lease expiry **MUST NOT** establish no effect, success, or safe takeover. Recovery **MUST** inspect durable effect evidence under the same identities. A missing lookup after retention or restore **MUST NOT** authorize automatic creation of a replacement request. |
| SMR-009 | Local pause/resume request acceptance, the expected-revision control mutation, and its result **MUST** be atomic and use the operator CLI's safety rules. A bounded control reserve and exact lookup **MUST** remain usable during data-plane saturation and while the worker is paused/stopped. Pause acknowledgement **MUST** distinguish committed intent from observed quiescence; resume **MUST NOT** clear a safety or membership hold. |
| SMR-010 | Explicit retry **MUST** reference an earlier management request and reuse only its verified retry-eligible underlying obligations. It **MUST** preserve targets, logical identities, policy checks, and aggregate attempt limits. It **MUST NOT** recreate a fresh action, reset budgets, retry arbitrary jobs, or resolve pre-signature or safety-held work without existing operator authority. |
| SMR-011 | Management verification **MUST** preserve the existing pinned two-node target and freshness rules. Its own harmless request bookkeeping **MUST NOT** prevent convergence, while unrelated content-affecting pending work, stale evidence, dirtiness, or uncertainty **MUST** still prevent `complete`. No caller-controlled pending-work exemption is allowed. |
| SMR-012 | Preparation lifetime, queues, the control reserve, associated evidence, request execution, retry, shutdown, and result size **MUST** have frozen tested bounds. Fair service dispatch **MUST** preserve periodic and peer recovery. Saturation **MUST** refuse before acceptance or retain a visible existing obligation, never silently discard work or report success. |
| SMR-013 | An additive migration and retention rules **MUST** preserve existing sync state, unresolved request/job/retry lineage, and request-to-effect evidence. Old binaries **MUST** refuse unsupported upgraded state. Supported destructive restore **MUST** disable management and establish a fresh local state incarnation before new effects so old prepared handles cannot execute. |
| SMR-014 | The provider **MUST** expose a separately versioned management capability and closed command/input/result/error contract, consistently accepting trusted config selection. It **MUST** preserve identity fields safely without exposing secrets and **MUST NOT** advertise the new contract in the frozen v0.2.0 artifact set. |
| SMR-015 | E23 **MUST** pass G19 through the public Core CLI without a Plugin implementation, retain SCP-008 deterministic platform verification, and provide native Darwin arm64/Linux arm64 paired evidence plus exact artifact/schema/fixture identities to Plugin TASK-028. Core completion **MUST NOT** imply Plugin mutation admission, release, installation, or production activation. |

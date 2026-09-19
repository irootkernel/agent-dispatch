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
| G6 | Watchman binding and reconciliation integrity are concurrency-safe. |
| G7 | Hermes compatibility, destination preflight, and disabled setup are usable without modifying Hermes. |
| G8 | Aggregate events fan out to independent destination lanes and close only from bounded work evidence. |
| G9 | Notifications, operational walkthrough, documentation truth, and the v0.1.5 release checks pass. |
| G10 | Guided Wiki setup selects one route, establishes a disabled baseline, and reruns safely. |
| G11 | Hermes 0.20.5 and later probe-compatible releases are safe through mandatory local serialization groups and optional target mutex defense-in-depth. |
| G12 | Durable notifications make bounded automatic progress without coupling delivery to source state. |
| G13 | Documentation, cold validation, and reproducible v0.1.6 release evidence agree. |
| G14 | The absolute Watchman root binding is implemented and proven on the operator host. |
| G15 | Official darwin/arm64, linux/amd64, and linux/arm64 support evidence agrees with the shipped platform contract (scenario: AC-505; evidence in `VALIDATION.md` Gate G15). |
| G16 | The v0.2.0 two-node sync contracts, disabled configuration, and truthful capability surface are accepted. |
| G17 | Signed manual publication and guarded import preserve data through conflicts and crash recovery. |
| G18 | Authenticated nudges, periodic recovery, pair verification, service operations, and real two-node qualification pass. |

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
| AC-403 | Given either a valid work receipt or a separate valid sync-import record whose changed path and digest-or-absence set exactly matches observed changes, when attribution runs, then exact self-generated changes may be suppressed and the decision is audited without representing an import as Hermes work. *(amended by D-030)* |
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
| AC-505 | Given a supported Linux host (`linux/amd64` or `linux/arm64`), when the test suite runs, then all unit, integration, race, migration, crash, and fixture tests pass. (Reactivated by D-029, E19-T2 under the supported set `{darwin/arm64, linux/amd64, linux/arm64}`; G15 records platform×leg evidence. Historical: closed for v0.1.2 under D-020 — `make verify` in full passed on linux/arm64 as a non-root user and `make test` passed on linux/amd64 — then retired under D-023/E9-T8; those records stand as history. D-028/G14 remain the Absolute Watch-Root Binding records.) |
| AC-506 | Given a v0.1 release candidate, when release verification runs, then binaries, checksums, schemas, example config, Hermes companion skill, SOT, and changelog are present and version-compatible. |

### G6: Source and Reconciliation Integrity

| ID | Given / When / Then |
|---|---|
| AC-601 | Given a configured resource root, when the trigger is installed, then Watchman watches that absolute root itself and status reports the configured root, the same root as the actual root, the schema-vestigial relative root `.`, and a trigger definition without `relative_root` (amended by D-028; the ancestor-binding scenario is retired). |
| AC-602 | Given changes outside the configured root or inside any exact/recursive exclusion, when Watchman delivers them, then no event, child task, hash, or notification is created. |
| AC-603 | Given a stored binding and changed Watchman topology, when remove succeeds, then no trigger with the managed name remains on any applicable watch root. |
| AC-604 | Given ordinary ingestion advances path facts during full enumeration, when reconciliation commits, then the stale snapshot is refused, newer facts survive, and one retryable reconciliation remains. |
| AC-605 | Given a file grows beyond the hash limit or changes repeatedly while read, when reconciliation observes it, then the read stays bounded and explicit quarantine/reconciliation evidence is recorded. |

### G7: Hermes Preflight and Operator Setup

| ID | Given / When / Then |
|---|---|
| AC-701 | Given the frozen real Hermes baseline interface and the installed newer Hermes interface, when probed through the same product command, then each is accepted only if every required public capability shape is usable. |
| AC-702 | Given a future Hermes version above the minimum with compatible public shapes, when probed, then it works without a source allowlist edit; an incompatible shape fails with the exact missing capability. |
| AC-703 | Given the executable content, path, reported version, or probe contract changes, when cached evidence is read, then it is invalidated before submission. |
| AC-704 | Given a missing on-disk profile, when route preflight or enable runs, then it fails before task creation and lists available profiles. |
| AC-705 | Given a required skill absent or disabled for a selected profile, when preflight runs, then it fails closed with bounded available alternatives. |
| AC-706 | Given a new operator, when using only root/group help and `setup wiki`, then disabled configuration, the Watchman binding check with the printed install and test commands, the initial baseline, and a production-gate summary are reached without internal database or Watchman commands. |

### G8: Fan-Out and Completion Evidence

| ID | Given / When / Then |
|---|---|
| AC-801 | Given one event and two eligible destinations using different profiles, when dispatched, then two independent children and Hermes tasks are created beneath one aggregate event. |
| AC-802 | Given two destinations using the same profile but different workstreams, when dispatched, then their destination identities and idempotency keys remain distinct. |
| AC-803 | Given one child fails or is retried while a sibling completes, when the aggregate is reprocessed, then the completed sibling is reused and never duplicated. |
| AC-804 | Given a behavior-affecting destination edit, when new work is planned, then a new destination revision and child idempotency identity are used and production acknowledgement is required. |
| AC-805 | Given completed, partially completed, blocked, and failed worker receipts, when validated, then completion closes, partial creates bounded remaining work, blocked requires manual intervention, and failed follows its budget. |
| AC-806 | Given Hermes acceptance or a terminal task status without a valid attributable receipt, when status is rendered, then the child is not reported as completed and the missing evidence is actionable. |

### G9: Notifications and v0.1.5 Release

| ID | Given / When / Then |
|---|---|
| AC-901 | Given configured default notification events, when work completes, exhausts failure, becomes unknown, is quarantined, needs reconciliation, or integration/Watchman drift appears, then one notification intent per configured sink is committed. |
| AC-902 | Given a repeated transition or aggregate rerun, when notifications are evaluated, then the event/destination/transition/sink/policy key prevents a second logical notification. |
| AC-903 | Given webhook timeout or malformed response, when delivery is retried, then attempts remain visible, the stable idempotency key is reused, and dispatch/work state is unchanged. |
| AC-904 | Given hostile paths, note contents, and resolved credentials, when a notification is rendered, then none of those secret or content values enter the payload or ordinary logs. |
| AC-905 | Given a disposable vault, isolated Hermes home and board, and two destinations, when the full operational walkthrough runs, then detection through completion receipt and configured notification is demonstrated without touching production state or Hermes source. |
| AC-906 | Given the v0.1.5 candidate, when `make verify` and two release builds run, then all G0-G9 gates are reconciled, the darwin/arm64 artifacts are byte-identical, checksums and versioned skills are present, and the final tree is eligible for the local v0.1.5 tag. |

### G10: Guided Setup and Disabled Baseline

| ID | Given / When / Then |
|---|---|
| AC-1001 | Given a setup-selected route, when route-scoped setup steps and guidance run, then Watchman status, test, install guidance, preflight, baseline, and the enable command all name that exact route. |
| AC-1002 | Given multiple configured routes, when setup has no explicit selection, then it prompts clearly on an interactive terminal and fails without choosing lexicographic-first in non-interactive execution. |
| AC-1003 | Given clean, Watchman-installed, runtime-row-materialized, or interrupted non-production state, when setup reruns, then it reaches the production-gate summary idempotently while configuration and runtime remain disabled. |
| AC-1004 | Given baseline-only reconciliation, when it commits or crashes, then it records either the previous or complete new snapshot and creates no decision, dispatch, task, production acknowledgement, or notification. |
| AC-1005 | Given setup completes, when its summary is inspected, then configuration, runtime activation, Watchman binding, baseline, and acknowledgement states are distinct and the exact enable command was printed but not executed. |

### G11: Hermes Mutex Downgrade and Serialization Groups

| ID | Given / When / Then |
|---|---|
| AC-1101 | Given Hermes v0.20.5 missing `--mutex-key`, when probed and preflighted, then it is compatible as `agent-dispatch-group-enforced` and no rendered command contains the unsupported flag. |
| AC-1102 | Given an active serialization group and a burst of relevant occurrences, when they are processed, then no parallel group child is created and selected lanes retain bounded dirty work. |
| AC-1103 | Given two destinations sharing one group, when both are selected, then they cannot run concurrently and completion promotes only the oldest first-dirty waiting lane. |
| AC-1104 | Given independent groups over one resource with every involved route acknowledgement current, when selected, then they may run concurrently without being described as globally single-writer. |
| AC-1105 | Given a retry, rerun, or serialization-policy edit, when processed, then the group slot cannot be bypassed, changed policy changes destination and route revisions, and the old production acknowledgement is stale. |
| AC-1106 | Given Hermes 0.20.4, 0.20.5, and a synthetic later release exposing target mutex, when the same compatibility path runs, then the first fails before side effects, the second uses local enforcement only, and the later release uses local enforcement plus the effective target mutex. |
| AC-1107 | Given an omitted or below-floor target setting, when configuration is validated, then it fails without rewriting; when `hermes set-minimum-version` receives a value at or above 0.20.5, then only that target changes atomically and every affected route requires fresh probe, preflight, and production acknowledgement. |
| AC-1108 | Given omitted, identical dual-field, conflicting dual-field, and explicit default-form serialization settings, when validated and resolved, then they respectively produce `resource:<resource_id>`, one deprecated-alias warning, a configuration error, and intentional membership in the default group; a preserved active collision chooses no arbitrary holder and resolves atomically through allowed existing-work exits. |

### G12: Automatic Durable Notification Draining

| ID | Given / When / Then |
|---|---|
| AC-1201 | Given after-command mode, when work completion commits a completion notification, then one bounded automatic pass delivers it without waiting for another filesystem event. |
| AC-1202 | Given webhook timeout or process death after source commit and no later source command, when the fifteen-minute fallback runs, then completed work is unchanged and the due notification resumes under its stable identity. |
| AC-1203 | Given simultaneous automatic drains or an expired owner finishing late, when due work is claimed, then leases are disjoint, expiry recovers safely, and a stale fencing token cannot record an outcome. |
| AC-1204 | Given a configured limit, ten-second automatic budget, persisted backoff, manual mode, or scheduled mode, when draining runs, then both automatic bounds and due deadlines are enforced, manual behavior remains explicit, and scheduled drain runs only after healthy reconciliation. |
| AC-1205 | Given delivery refusal, ambiguity, retryability, or sink-resolution failure, when the source command has succeeded, then its exit remains successful and the notification outcome remains independently inspectable. |
| AC-1206 | Given pending or repeatedly failing delivery, when status and doctor run, then count, age, due/backoff counts, live/expired claims, latest outcome/run, mode, limit, scheduler expectation/evidence, and actionable findings are available without direct SQLite inspection. |
| AC-1207 | Given hostile document data, endpoint credentials, or a successful drain, when payloads and diagnostics are inspected, then no protected content leaks and no recursive drain-success notification exists. |
| AC-1208 | Given a v0.1.5 notification database, when it migrates, then every existing notification ID, idempotency key, and attempt record is unchanged. |
| AC-1209 | Given migrated pending, backoff-pending, or refused notifications, when manual drain and explicit retry run, then migrated work is initially due, drain selects due work only, retry alone makes the selected record pending and immediately due, and the persisted jittered deadline is shared by every process. |
| AC-1210 | Given an identical, drifted, disabled, or installed managed schedule, when lifecycle commands run, then install is idempotent, conflicting definitions are preserved and refused, inspect reports file/load/digest posture, disable preserves the platform unit (launchd plist or systemd user unit/timer), uninstall removes only that managed unit, and log rotation retains three 10 MiB files. |
| AC-1211 | Given a successful registered command affecting one or more after-command routes, when core commit completes, then existing due work advances under one global ten-second budget using deterministic one-item route rounds even if no new notification was created; a failed core command does not auto-drain, and stdout, JSON, and exit behavior remain unchanged. |

### G13: v0.1.6 Release

| ID | Given / When / Then |
|---|---|
| AC-1301 | Given the final v0.1.6 tree, when focused suites and `make verify` run, then G10-G12 and all unchanged earlier gates are reconciled without including the separate general verify remediation. |
| AC-1302 | Given disposable state, vaults, boards, and profiles, when the required clean-host, setup-rerun, real Hermes, concurrency, and notification walkthroughs run, then their sanitized transcripts cover every requested delivery-evidence item without production activation. |
| AC-1303 | Given two release builds from the same clean commit, when darwin/arm64 artifacts are compared, then binaries and checksums are byte-identical and versioned documentation and skills agree on v0.1.6. |
| AC-1304 | Given upgrade or rollback, when the documented procedure is followed, then forward-only migrations preserve identities, rollback restores the verified pre-upgrade database/config/binary set, and scheduler uninstall preserves state. |

### G14: Absolute Watch-Root Binding

| ID | Given / When / Then |
|---|---|
| AC-1401 | Given a parent of the configured resource root is already a Watchman watch root, when installation cannot establish the configured root as its own watch root, then the command fails closed with actionable unwatch guidance and binds no ancestor (D-028). |
| AC-1402 | Given a live trigger invocation, when `WATCHMAN_ROOT` canonicalizes to the configured resource root, then it is accepted; an ancestor root or a present `WATCHMAN_RELATIVE_ROOT` is rejected as a binding mismatch with no persisted-binding second axis (D-028). |
| AC-1403 | Given the operator host with a production route previously bound to an ancestor watch root, when the route is reinstalled and a Markdown file changes under the configured root, then a new source observation and its destination task exist and `agent-dispatch doctor` reports no binding-mismatch or staleness fallout caused by the re-binding. |

### G16: Two-Node Sync Contract and Disabled Baseline

| ID | Given / When / Then |
|---|---|
| AC-1601 | Given an existing v0.1.8 configuration with no sync block, when it is loaded and a side-effect-free ordinary inspection command runs, then its behavior is unchanged and no new sync-attributable Git, network, service, or live-tree side effect occurs. |
| AC-1602 | Given disabled sync configuration, when capabilities and status run, then they identify the reserved contract and disabled state without fetching, applying, publishing, or starting a listener. |
| AC-1603 | Given a third active member, overlapping resource, unapproved ref, non-Markdown scope, or inline secret, when configuration is validated, then it fails before a protected side effect. Given a cooperative-import acknowledgement whose locally bound inputs changed, when the contract-only currentness helper evaluates it, then the result is stale without claiming command-side enforcement or a live-tree effect. |
| AC-1604 | Given contract fixtures for a membership document signed by an unpinned key, a self-authorizing trust root, a stale predecessor, or an obsolete state incarnation, when the provisional provider bundle is validated, then the closed result rejects the document without claiming runtime signature verification. |
| AC-1605 | Given the provisional provider bundle, when its descriptors, schemas, fixtures, and checksums are validated, then every reserved command and unavailable result agrees with the documented v0.2.0 contract. |

### G17: Signed Publication and Guarded Import

| ID | Given / When / Then |
|---|---|
| AC-1701 | Given no eligible validated maintenance snapshot, when `sync publish` runs, then it refuses without creating a commit, push, or nudge. |
| AC-1702 | Given an eligible snapshot and a clean configured repository, when `sync publish` runs, then it freezes only governed Markdown, creates one SSH-signed commit and manifest, confirms a fast-forward push, and records one peer obligation. |
| AC-1703 | Given a push that succeeds remotely before timeout or local crash, when recovery runs, then it recognizes the published commit and creates no duplicate commit or push. |
| AC-1704 | Given two nodes publishing from one base, when the second push diverges, then both histories remain intact and the losing node blocks without merge, rebase, force push, reset, clean, or stash. |
| AC-1705 | Given no cooperative-import acknowledgement, changed locally bound acknowledgement inputs, remote membership drift, an overlapping staged or unstaged change, an untracked overwrite collision, an active Git operation, unstable index/ref state, or unprovable path disjointness, when an import is ready, then application is deferred and local content is unchanged while publication, status, and fetch-and-validate reconciliation remain available. |
| AC-1706 | Given an acknowledged resource with eligible path-disjoint local state and validated publication history, when import applies, then its journal and resource fence survive crash injection and only exact imported Watchman effects are suppressed. |
| AC-1707 | Given an imported change and an independent local edit, including a late edit to the same path, when attribution resolves, then the local edit remains dirty and is never cleared by older import evidence. |
| AC-1708 | Given out-of-band administrator trust, when membership is bootstrapped or updated through plan/apply, then separate administrator authority, expected predecessor, non-force publication, exactly two normal active entries, and blocked emergency revocation are enforced; runtime verification rejects self-authorizing, stale-parent, obsolete-incarnation, and unauthorized transitions. |
| AC-1709 | Given an existing vault, resolved divergence, previously unseen history from a removed publisher key, or history beyond the inspection bound, when trust is established, then an administrator-signed checkpoint binds the exact governed snapshot and uncovered history remains blocked until `sync reconcile` validates that checkpoint. |
| AC-1710 | Given a remote change to note Y and a local edit to disjoint note X or an out-of-scope/ignored file, when guarded import runs, then Y advances while X and the unrelated file remain byte-identical and dirty; an overlap or alias collision defers instead. |
| AC-1711 | Given publication snapshot capture races a participating writer or the process stops before signature, when recovery runs, then no mixed snapshot is attested and unattended service recovery cannot sign; given an already-signed candidate, recovery confirms or retries its push and nudge under the same identity. |

### G18: Peer Recovery, Verification, and Operations

| ID | Given / When / Then |
|---|---|
| AC-1801 | Given a wrong peer, group, receiver, credential, membership revision, or revoked identity, when a nudge or status request arrives, then it is refused and no Git or filesystem effect occurs. |
| AC-1802 | Given duplicate or reordered nudges, a lost 202 response, or a completely lost nudge, when delivery and periodic reconciliation run, then one logical import progresses and no semantic maintenance echo is created. |
| AC-1803 | Given one offline node, when the online node publishes, then publication completes with pending peer evidence; after reconnection, startup or periodic reconciliation catches the peer up from Git. |
| AC-1804 | Given equal target commits but governed local dirtiness, pending work, stale membership, or uncertain evidence on either node, when pair verification runs, then fresh convergence remains incomplete. |
| AC-1805 | Given the final v0.2.0 candidate on a MacBook and Oracle Cloud Linux node, when service lifecycle, publication, nudge, import, missed-nudge recovery, conflict stop, and pair verification are exercised, then the recorded results match one reviewed core revision without Plugin or production-vault mutation outside the authorized fixture. |
| AC-1806 | Given the peer service and its reviewed threat model, when it is rendered and started, then it binds only the admitted loopback or tailnet-only endpoint, verifies peer endpoint/certificate identity, leaves Tailscale configuration unchanged, and exposes neither a public listener nor Funnel. |
| AC-1807 | Given active, paused, blocked, and safely quiesced work, when `sync pause`, `sync resume`, `sync reconcile`, and managed service lifecycle commands run, then they respect control revisions and safe boundaries, preserve state on stop/disable/uninstall, and never clear a conflict or revocation without its required signed evidence. |
| AC-1808 | Given any configured queue, retry, payload, history, subprocess, evidence-retention, concurrency, or shutdown bound is exhausted, when work is admitted or advanced, then the exact obligation remains visible and incomplete or blocked without truncating the pair, dropping work, or reporting convergence. |

## 3. Automatic-Write Gate

Automatic Hermes writes to the real vault are prohibited until all scenarios in gates G0 through G14 pass in a test vault and the operator explicitly enables the production route. Dry-run, audit-only, baseline-only, or no-write Hermes profiles may be used earlier. Historical v0.1.5 evidence remains valid for its shipped scope but does not satisfy the new G10-G13 requirements; G14's live re-binding evidence (AC-1403) runs on the operator-enabled production route under D-028's bounded waiver.

Automatic sync application to a real vault is prohibited until G16 through
G18 pass on disposable repositories, the listener and real-vault security
reviews are accepted, and the operator acknowledges the exact group's
cooperative-import policy. Publication, production activation, and release
publication remain separate authorizations.

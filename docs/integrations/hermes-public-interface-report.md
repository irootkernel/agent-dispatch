# Hermes Public Interface Report

> **Task:** E0-T4, Verify Hermes Public Interface and Freeze Capability Baseline
> **Probed:** 2026-08-19 (KST) on macOS (Apple Silicon)
> **Hermes version:** `Hermes Agent v0.20.5 (2026.8.19)` (`hermes --version`; the durable-delivery
> shapes below were verified 2026-08-19 against the then-baseline Hermes and re-verified
> 2026-08-31 against 0.20.5 by the E15-T4 G11 probe — see
> `hermes-v0.20.5-g11-evidence.md`; the one baseline difference, the absent `--mutex-key`
> create flag, is recorded in §7 and §10)
> **Machine capability record:** [`hermes-capability-report.json`](hermes-capability-report.json)
> **Fixtures:** [`fixtures/hermes/`](fixtures/hermes/)
> **Compatibility decision:** **Supported** for Hermes `0.20.5` as the floor; see §10.

## 1. Method and Boundary

Every claim below was produced by invoking only public Hermes CLI commands
against a disposable kanban board `agent-dispatch-e0t4-probe`, created for this probe
and hard-deleted afterwards. The active board selection was never changed (all
commands used the `--board` flag). No Hermes source file was modified, no
Hermes internal database was opened, and no private API was assumed. Where a
fact could not be exercised at runtime without dispatching a real worker (and
therefore spending provider budget), it is graded `help-verified` and labeled;
everything else is `runtime-verified` with the exact command recorded.

Hermes kanban is a "Durable SQLite-backed task board shared across Hermes
profiles" per its public help. Boards isolate work streams: each board has its
own database, workspaces directory, and dispatcher loop.

## 2. Version Discovery

- `hermes --version` prints human text only; there is no JSON mode, and kanban
  JSON responses carry no version field. First line format:
  `Hermes Agent v<major>.<minor>.<patch> (<build date>)`
  (fixture: `fixtures/hermes/version-output.txt`, home path sanitized).
- Consequence for the adapter (E4-T1): version gating must invoke
  `hermes --version`, match the documented first-line pattern, and compare the
  parsed semantic version against the supported range; a nonmatching or
  unparsable response must fail route validation.

## 3. Task Creation (Verified)

```text
hermes kanban --board <slug> create <title> [options] --json
```

Runtime-verified options and their echo in the JSON response:

| Logical contract field | Public flag | Response field | Evidence |
|---|---|---|---|
| Title | positional `title` | `title` | runtime |
| Trusted body / opening post | `--body` | `body` | runtime |
| Profile (assignee) | `--assignee` | `assignee` | runtime |
| Skill selection | `--skill` (repeatable) | `skills[]` | runtime |
| Workspace binding | `--workspace scratch\|worktree\|worktree:<path>\|dir:<path>` | `workspace_kind`, `workspace_path` | runtime (`scratch`) |
| Resource mutex | `--mutex-key` | `mutex_key` | runtime (accept + echo) |
| Runtime hint | `--max-runtime` (`300`, `90s`, `30m`, `2h`, `1d`) | dispatcher-enforced cap | help |
| Retry hint | `--max-retries N` | `max_retries` | runtime |
| Idempotency key | `--idempotency-key` | dedup behavior, §5 | runtime |
| Priority tiebreaker | `--priority` | `priority` | runtime |
| Author attribution | `--created-by` | `created_by` | runtime |
| Model pinning | `--model` / `--provider` | `model_override`, `provider_override` | help |

The `--json` response is a single task object (fixture
`fixtures/hermes/create-response.json`). The id is the external reference
format `t_<8 hex>`.

## 4. Durable Acceptance (Verified)

What proves durable acceptance through the public interface:

1. `create --json` returns a stable task id, `status`, and `created_at` epoch.
2. Any later, separate CLI process can read the identical record back via
   `show <task-id> --json` (fixture `fixtures/hermes/show-response.json`). Every
   CLI invocation is a fresh process, so cross-process reads demonstrate a
   persisted write, not in-memory state.
3. The public help documents each board as a durable SQLite-backed queue, and
   `boards create` publicly discloses the per-board database path
   (`~/.hermes/kanban/boards/<slug>/kanban.db`). The probe never opened it.

Limits of the claim: durability is exactly SQLite's documented durability on
the local filesystem. No power-loss or Hermes-restart claim beyond that is
made, and none is needed for the v0.1 contract (`DUR-*`, HER-004).

## 5. Idempotency and Duplicate Behavior (Verified)

- `--idempotency-key` dedups creation: re-running `create` with the same key
  returns the **original** task (same id and title) and creates nothing. The
  probe board held exactly one task after the duplicate submission (fixture
  `fixtures/hermes/create-duplicate-dedup.json`).
- **Key lifetime is bounded by archival.** Dedup applies only while a
  **non-archived** task holds the key:
  - a `done` but not archived task still dedups (observed: `t_d191d897`
    returned for its key after completion);
  - after `archive`, the same key immediately creates a **new** task
    (observed: archived `t_6253023d`, then key reuse created `t_c3385bd7`).
- There is **no standalone query-by-key command**. The dedup create is the
  lookup-by-key mechanism.

Operational consequences recorded for E4-T3:

- Persist the accepted external task id locally before anything else; key-based
  reconciliation is only valid while the original task is non-archived.
- Reconciliation after a lost create response: re-run `create` with the same
  key; a returned existing id proves the original submission was accepted; a
  new id proves no non-archived task holds the key (safe resubmission).
- Never archive Agent Dispatch-created tasks from the Agent Dispatch side without
  recording that the idempotency key is freed by doing so.

## 6. Lookup (Verified)

- By external reference: `hermes kanban --board <slug> show <task-id> --json`
  returns the full task record wrapped as `{"task": {...}}`.
- Listing: `hermes kanban --board <slug> list [--status <s>] [--archived]
  [--json] [--sort <field>]` returns a task array (fixture
  `fixtures/hermes/list-response.json`).
- Public status enum: `archived`, `blocked`, `done`, `ready`, `review`,
  `running`, `scheduled`, `todo`, `triage`.
- Observed public transitions on probe tasks: `ready -> blocked`,
  `blocked -> ready` (unblock), `ready -> done` (complete sets `completed_at`),
  `ready -> archived`.

## 7. Profile, Mutex, Hints, and Status Fields (Mixed Grades)

- **Profile:** `--assignee` is accepted verbatim and is **not validated at
  create time**; an unknown profile name was accepted with exit 0 (fixture
  `fixtures/hermes/create-assignee-unvalidated.json`). Profiles are publicly
  enumerable beforehand: `hermes kanban --board <slug> assignees --json` lists
  every assignee with an `on_disk` existence flag (sanitized fixture
  `fixtures/hermes/assignees-default-board.json`), and `hermes profile list`
  exists. E4-T1 must validate the configured profile this way before enabling
  a route.
- **Mutex:** `--mutex-key` "serializes this task with other running tasks that
  use the same explicit board-local key" (public help). Key scope is the board;
  keys are trimmed only, schemes and case preserved. Runtime contention was not
  exercised (requires dispatching workers). Agent Dispatch's route-level single-task
  invariant (CON-*) must not depend solely on this; the local serialization of
  E3-T4 remains required.
- **Runtime hint:** `--max-runtime` is an enforced cap: on overrun the
  dispatcher SIGTERMs, then SIGKILLs, and re-queues the worker (public help).
  Treat as a guarantee of termination, not of completion.
- **Retry hint:** `--max-retries N` is a per-task consecutive-failure circuit
  breaker (trip on the Nth failure; dispatcher default 2). Distinct from
  Agent Dispatch's own persisted retry policy (DUR-*): this bounds only Hermes-side
  worker retries.
- **Execution status:** portable through the public status enum and
  `started_at` / `completed_at` / `result` fields; `result` and
  `completed_at` were observed set after the public `complete` operation. The
  worker-side `submit-result` flow exists in public help but requires an owned
  running worker and was not exercised.

## 8. Output Schemas and Error Model (Verified)

- Success with `--json`: machine-readable JSON on stdout, exit 0. Shapes are
  frozen in the fixtures: create returns a task object; show returns
  `{"task": {...}}`; list returns an array; `boards list --json` returns board
  objects; `assignees --json` returns assignee objects.
- Unknown task: message `no such task: <id>` on stderr, exit 1.
- Unknown board: message with a recovery hint (`Create it with ...`), exit 1.
- Unrecognized arguments: argparse error, exit 2.
- `hermes kanban log <task-id>` is human text only (no `--json`); before any
  worker run it prints a no-log-yet notice with exit 0. Never parse it for
  correctness decisions.
- No public request-size limit is documented for title or body. The adapter
  must enforce Agent Dispatch's own bounded-manifest policy (SEC-*, E4-T2) and must
  not rely on a Hermes-side limit.
- Fixture: `fixtures/hermes/error-cases.txt`.

## 9. Webhook Capabilities (Inspected Only, Not Implemented)

`hermes webhook subscribe|list|remove|test` manages **inbound**
event-driven agent activation subscriptions: a named route
(`/webhooks/<name>`), optional event-type filter, prompt template with payload
references, forced skills, an HMAC `--secret` (auto-generated when omitted), a
filter/transform script, and delivery targets including a zero-LLM
`--deliver-only` mode. In this installation the webhook receiving platform is
**not enabled**; `hermes webhook list` prints setup guidance (fixture
`fixtures/hermes/webhook-platform-disabled.txt`).

Consequences: E6-T1's outbound webhook adapter cannot assume a running inbound
platform on the target host. Authentication semantics observed: HMAC secret
per subscription. No implementation, subscription, or gateway change was made
during this probe.

## 10. Compatibility Decision

**Decision: supported.** All capabilities the durable Kanban contract requires
are present on the public CLI for the probed version, with no reduced
guarantee required: durable acceptance, idempotency submission, key-based
reconciliation via dedup create, external-ref lookup, board-local mutex,
portable execution status, cancellation for not-yet-running tasks
(block + archive), and a readable result/completed_at projection.

**Supported version range: `0.20.5` and later probe-eligible releases** (the runtime-verified
version, build date 2026.8.19). The adapter may widen to
later releases only after re-running this probe against each additional
version; any version outside the verified set must fail route validation
(HER-002, HER-005, AC-002, AC-306).

Known behavioral caveats the adapter must carry forward:

1. Idempotency keys are freed by archival (§5).
2. `--assignee` is unvalidated at create time (§7).
3. Version discovery is human text only (§2).
4. Mutex runtime behavior is help-verified, not runtime-verified (§7).
5. `maximum_request_bytes` is null: no public Hermes-side size limit (§8).

## 11. Boundary Confirmation

During this probe, Agent Dispatch work used only: `hermes --version`,
`hermes kanban boards {create,list,current,rm}`, `hermes kanban --board <slug>
{create,show,list,block,unblock,complete,archive,assignees}`,
`hermes webhook list`, and `hermes profile --help` (discovery only). The
per-board `kanban.db` path was disclosed by public CLI output but never
opened. No Hermes file, configuration, plugin, or internal state was modified.
The disposable board was hard-deleted after evidence capture
(`hermes kanban boards rm agent-dispatch-e0t4-probe --delete`), leaving the
installation's board inventory unchanged.

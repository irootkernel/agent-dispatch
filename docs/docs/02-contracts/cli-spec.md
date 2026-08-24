# CLI Specification

## 1. General Contract

Executable name: `agent-dispatch`

Global options:

```text
--config <path>
--state-dir <absolute-path>
--output human|json
--log-level error|warn|info|debug
--trace-id <id>
--timeout <duration>
```

Rules:

- machine data goes to stdout;
- logs and diagnostics go to stderr;
- `--output json` returns one JSON object unless the command explicitly documents JSON Lines;
- secrets and note bodies never appear;
- commands are non-interactive unless explicitly named `init` or given `--interactive`;
- stable exit codes are defined in `error-model.md`.

## 2. Command Tree

```text
agent-dispatch version
agent-dispatch init
agent-dispatch config validate|show
agent-dispatch route list|show|plan|enable|disable|stale
agent-dispatch watchman install|status|remove|test
agent-dispatch dispatch
agent-dispatch dispatches list|show|retry|reprocess|rerun|discard|refresh|drain
agent-dispatch receipts list|show
agent-dispatch work begin|complete|fail
agent-dispatch quarantine list|show|release|discard
agent-dispatch reconcile
agent-dispatch status
agent-dispatch doctor
agent-dispatch maintenance prune|vacuum|integrity|backup
agent-dispatch completion
```

There is no `replay` command.

## 3. Core Commands

### `version`

Prints binary version, commit, build time, supported config version, schema range, and adapter versions.

### `init`

Creates a disabled example configuration and state directory after checking for existing files. It does not install a Watchman trigger or enable dispatch unless explicit flags are supplied.

### `config validate`

```text
agent-dispatch config validate [--probe-targets] [--output json]
```

Performs schema and semantic validation. `--probe-targets` invokes read-only public capability probes; hermes-webhook targets report their static, evidence-tied capability declaration with no endpoint network I/O, because the receiving platform cannot be assumed running (E0-T4 §9).

### `config show`

Prints normalized redacted configuration and computed revisions.

### `route stale`

```text
agent-dispatch route stale --route <id> --reason <text>
```

Moves an active route whose dispatch is older than `active_stale_after` to UNCERTAIN through the declared execution-evidence-stale edge, auditing the operator reason. The uncertain route is then resolved through the documented reconciliation or lookup exits (DUR-004, E7-T7/M-6).

### `route enable|disable`

```text
agent-dispatch route enable --route <id> --acknowledge-production-gate <computed-route-revision> --yes
agent-dispatch route disable --route <id> [--reason <text>]
```

The config field `enabled: true` permits activation but does not by itself activate a production route. `route enable` stores an acknowledged route revision in SQLite. A behavior-sensitive revision change pauses the route until explicitly acknowledged again. `route disable` immediately prevents new submissions while preserving observations, active work, and dirty state. `route enable` probes the live target first (E8-T3): a missing, unreadable, or stale capability report, an unsupported Hermes version, or a missing required capability refuses at exit 3, and the report must itself carry `durable_acceptance` and `submit_idempotency_key`. The report is mandatory evidence in every target-liveness state (E9-T6): while the target is unreachable the refusal still covers a missing or unreadable report and a report recording a Hermes version outside the runtime-verified set (build-time evidence that needs no live target); only freshness against the installed binary rides the probe, so an unreachable target warns and that comparison defers to the submit path's run-time gate.

### `route plan`

```text
agent-dispatch route plan --route <id> --input watchman < fixture.json
```

Side-effect-free. Prints a versioned dispatch plan. It may read files under the resource root to hash them, but does not write SQLite unless `--with-state` is explicitly supported and documented. The default is no database mutation. This command accepts only `--output json`; it has no human rendering (CLI-001 machine-output discipline).

## 4. Watchman Commands

### `watchman install`

```text
agent-dispatch watchman install --route <id> [--replace]
```

Creates or verifies the route trigger. No replacement occurs without `--replace`.

### `watchman status`

Shows installed vs expected trigger definition and source health.

### `watchman remove`

```text
agent-dispatch watchman remove --route <id> --yes
```

Removes only the exact managed trigger. It never removes the Watchman watch root automatically.

### `watchman test`

Uses a temporary or supplied fixture and prints normalized source input without Hermes side effects.

## 5. Dispatch Command

```text
agent-dispatch dispatch \
  --route <id> \
  --input watchman \
  [--dry-run] \
  [--no-submit]
```

- `--dry-run`: no SQLite mutation and no target call. Dry-run output is the structured dispatch plan and accepts only `--output json`.
- `--no-submit`: persists observation, batch, decision, and eligible intent, but leaves it ready. This option is operator-only and is not used by the installed Watchman trigger.
- default: persist and attempt the one newly eligible intent plus at most one route-local recovery intent, as bounded by architecture.

A no-op meaningful-change result exits 0 with disposition `drop`.

## 6. Dispatch Inspection and Actions

### `dispatches list`

Filters by route, state, age, target, external reference, or causal ID. JSON mode supports pagination.

### `dispatches show <dispatch-id>`

Shows full redacted lineage: observation, batch, decision, attempts, receipts, route state, and work receipts.

### `dispatches retry <dispatch-id>`

Uses the same dispatch request and idempotency key. Allowed only when state and reconciliation evidence permit it. Requires `--reason` for dead-lettered or operator-resolved unknown work.

### `dispatches discard <dispatch-id>`

Closes one dead-lettered dispatch as superseded through the declared edge while keeping the record and its audit history inspectable; the closed lineage becomes retention-resolvable. The envelope reports the measured outcome (`slot_released`, `route_to_idle`, `dirty_generation_retained`): a clean active route whose only work was the closed letter moves to IDLE, and a dirty route retains its generation for reconciliation. Requires `--reason` (DUR-009, E7-T7/M-7).

### `dispatches refresh <id>`

Re-reads the target execution status for one accepted dispatch (public lookup by its stored external reference) and persists an execution-projection receipt. Acceptance is never reinterpreted (HER-008): a projection that cannot be derived is recorded as unavailable with its reason, never as success, and a target without the execution capability reports `lookup_unsupported` instead of an emulated projection. `route show` projects the active dispatch's latest persisted execution state and warns when the active dispatch is older than the configured `active_stale_after`; stale work is warned, never auto-failed.

### `dispatches reprocess <batch-id>`

Evaluates a retained batch against the current route policy and creates a new decision. It does not mutate the original decision.

### `dispatches rerun <dispatch-id>`

Creates an intentional new work request with new dispatch ID and idempotency key. Requires `--reason` and `--yes` in non-interactive mode. Eligibility (E7-T2): the original must be `ready` or `dead_lettered`; the rerun supersedes it through its declared state-machine edge in the same transaction, so exactly one authoritative request remains. In-flight work (`submitting`, `unknown`, `reconciling`) is refused with recovery guidance (`dispatches drain` resolves expired leases and unknown delivery), `retry_wait` work belongs to `dispatches retry`, and `accepted` or otherwise terminal work keeps its authoritative lineage.

### `dispatches drain`

The automatic-write gate applies: a route whose activation state or whose configuration `enabled` key is off submits nothing (the expired-lease recovery sweep still runs; the skip is visible in the envelope's skipped count, and a configuration-disabled route reports the skip as a warning).

```text
agent-dispatch dispatches drain --route <id> --max <N>
```

Operator command for bounded ready/retry work, in a fixed order: first the route's expired `submitting` leases are recovered to `unknown` (the `recovered` envelope field lists each), then the route's `unknown` dispatches are reconciled (lookup by idempotency key then external reference; on Hermes Kanban the by-key read does not exist, so unresolved ambiguity dead-letters for the operator, whose `dispatches retry` resubmits the same idempotency key through the dedup-safe path; the envelope reports each reconciliation under `reconciled` and per-dispatch failures as warnings without blocking the route's due work), then the due `ready`/`retry_wait` intents are submitted — only the dispatch holding the route's active slot, never beside another authoritative task, and never on an `uncertain` or `quarantined` route (CON-001). A route left in `FOLLOWUP_READY` with an already accepted follow-up (a crash between acceptance and promotion) is promoted by this command. Not installed as a Watchman trigger.

## 7. Receipt Commands

### `receipts list|show`

```text
agent-dispatch receipts list [--route <id>] [--dispatch <id>] [--kind acceptance|execution_projection|work] [--limit <N>]
agent-dispatch receipts show <receipt-id>
```

Inspect acceptance, execution projection, and work receipts. Output is redacted: detail carries the bounded persisted payload only, never note bodies or unrestricted target output (OPS-001/OPS-002).

### `work begin`

```text
agent-dispatch work begin \
  --dispatch-id <id> \
  --run-id <id> \
  [--external-task-id <id>] \
  [--base-revision <opaque>]
```

Registers an agent run. Returns the run ID that `work complete` and `work fail` require to record the outcome. Authentication is local-user and dispatch-lineage based in v0.1; future remote usage requires stronger authentication.

### `work complete`

```text
agent-dispatch work complete \
  --dispatch-id <id> \
  --run-id <id> \
  --manifest <json-file-or-stdin> \
  [--result-revision <opaque>]
```

Manifest contains only relative paths and before/after digests. It may trigger exact suppression and one follow-up decision transactionally.

### `work fail`

```text
agent-dispatch work fail \
  --dispatch-id <id> \
  --run-id <id> \
  --failure-code <code> \
  [--detail <string>]
```

Records a cooperative failed execution projection with a bounded reason code. It does not contain chain-of-thought. The v0.1 failure-code set is closed: `agent_error`, `canceled`, `timeout`, `environment_error`; the work-receipt schema enforces the set and requires a non-null code for failed receipts. Failure never erases dirty route state: while the route's consecutive-failure budget remains (the route's configured `failure_budget`), one follow-up intent for latest state is created; budget exhaustion requires operator resolution.

## 8. Quarantine Commands

### `quarantine list|show`

```text
agent-dispatch quarantine list [--route <id>] [--state held|released|discarded|superseded] [--limit <N>]
agent-dispatch quarantine show <quarantine-id>
```

Lists structural holds; the default covers every state, `--state held` narrows to open cases.

### `quarantine release <id>`

Requires `--reason` and `--yes` in non-interactive mode. Records the actor, reason, and previous-decision lineage and creates one replacement reconciliation decision marking the pending reconciliation generation; the reconciliation path (the `reconcile` command or the next completion) schedules the actual work — release itself never dispatches directly.

### `quarantine discard <id>`

Marks the hold resolved without task creation. Requires `--reason` and `--yes` and preserves audit lineage.

## 9. Reconciliation

```text
agent-dispatch reconcile \
  --route <id> \
  --reason initial|scheduled|overflow|fresh-instance|lost-cursor|manual|delivery|stale-active|startup \
  [--submit]
```

Default behavior persists the current-state reconciliation decision. `--submit` attempts an eligible intent and then drains the route's other due work (bounded), so a pending follow-up generation reaches the target on the scheduled path without a manual drain (E7-T2); an accepted follow-up is promoted to the route's active task at acceptance. A route whose activation state is not `enabled` fails closed with the state-conflict classification (`transition_invalid`, exit 14), never a storage failure. The installed scheduled recipes carry `--submit` unconditionally (E9-T4/T2-F001): the two-key gate is the safety boundary — before the production acknowledgement the route fails closed at exit 14 as above, and after it a route disabled in configuration (the YAML key) persists its reconciliation decisions and recovers without submitting.

## 10. Status and Doctor

### `status`

Returns route active/dirty state, queue counts, unresolved delivery, quarantine, last reconciliation, and target capability summary.

### `doctor`

```text
agent-dispatch doctor [--probe-targets] [--integrity full]
```

Returns findings with `code`, `severity`, `summary`, `details`, and `remediation`. Findings are the stdout result; when any finding has error severity the command also emits the stable `doctor_findings_present` code and exits nonzero (AC-502).

## 11. Maintenance

- `maintenance prune --before ... --dry-run|--yes`
- `maintenance vacuum --yes`
- `maintenance integrity [--full]`
- `maintenance backup --output <path>`

Prune is dry-run by default. Vacuum refuses while active attempts exist. Backup writes a verified owner-only snapshot of the durable store through the built-in `VACUUM INTO` path (runbook §8) and refuses to overwrite an existing file.

## 12. JSON Envelope

Successful commands use:

```json
{
  "api_version": "agent-dispatch.cli/v1",
  "command": "dispatch",
  "ok": true,
  "result": {},
  "warnings": [],
  "trace_id": "..."
}
```

Errors use the error contract. Command-specific schemas may be added without changing this outer envelope within v1.

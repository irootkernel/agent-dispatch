# CLI Specification

## 1. General Contract

Executable name: `jjukkumi`

Global options:

```text
--config <path>
--state-dir <path>
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
jjukkumi version
jjukkumi init
jjukkumi config validate|show
jjukkumi route list|show|plan|enable|disable
jjukkumi watchman install|status|remove|test
jjukkumi dispatch
jjukkumi dispatches list|show|retry|reprocess|rerun|drain
jjukkumi receipts list|show
jjukkumi work begin|complete|fail
jjukkumi quarantine list|show|release|discard
jjukkumi reconcile
jjukkumi status
jjukkumi doctor
jjukkumi maintenance prune|vacuum|integrity
jjukkumi completion
```

There is no `replay` command.

## 3. Core Commands

### `version`

Prints binary version, commit, build time, supported config version, schema range, and adapter versions.

### `init`

Creates a disabled example configuration and state directory after checking for existing files. It does not install a Watchman trigger or enable dispatch unless explicit flags are supplied.

### `config validate`

```text
jjukkumi config validate [--probe-targets] [--output json]
```

Performs schema and semantic validation. `--probe-targets` invokes read-only public capability probes.

### `config show`

Prints normalized redacted configuration and computed revisions.

### `route enable|disable`

```text
jjukkumi route enable --route <id> --acknowledge-production-gate --yes
jjukkumi route disable --route <id> [--reason <text>]
```

The config field `enabled: true` permits activation but does not by itself activate a production route. `route enable` stores an acknowledged route revision in SQLite. A behavior-sensitive revision change pauses the route until explicitly acknowledged again. `route disable` immediately prevents new submissions while preserving observations, active work, and dirty state.

### `route plan`

```text
jjukkumi route plan --route <id> --input watchman < fixture.json
```

Side-effect-free. Prints a versioned dispatch plan. It may read files under the resource root to hash them, but does not write SQLite unless `--with-state` is explicitly supported and documented. The default is no database mutation. This command accepts only `--output json`; it has no human rendering (CLI-001 machine-output discipline).

## 4. Watchman Commands

### `watchman install`

```text
jjukkumi watchman install --route <id> [--replace]
```

Creates or verifies the route trigger. No replacement occurs without `--replace`.

### `watchman status`

Shows installed vs expected trigger definition and source health.

### `watchman remove`

```text
jjukkumi watchman remove --route <id> --yes
```

Removes only the exact managed trigger. It never removes the Watchman watch root automatically.

### `watchman test`

Uses a temporary or supplied fixture and prints normalized source input without Hermes side effects.

## 5. Dispatch Command

```text
jjukkumi dispatch \
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

### `dispatches reprocess <batch-id>`

Evaluates a retained batch against the current route policy and creates a new decision. It does not mutate the original decision.

### `dispatches rerun <dispatch-id>`

Creates an intentional new work request with new dispatch ID and idempotency key. Requires `--reason` and `--yes` in non-interactive mode.

### `dispatches drain`

```text
jjukkumi dispatches drain --route <id> --max <N>
```

Operator command for bounded ready/retry work. Not installed as a Watchman trigger.

## 7. Receipt Commands

### `receipts list|show`

Inspect acceptance, execution projection, and work receipts. Output is redacted.

### `work begin`

```text
jjukkumi work begin \
  --dispatch-id <id> \
  --run-id <id> \
  [--external-task-id <id>] \
  [--base-revision <opaque>]
```

Registers an agent run. Returns the run ID that `work complete` and `work fail` require to record the outcome. Authentication is local-user and dispatch-lineage based in v0.1; future remote usage requires stronger authentication.

### `work complete`

```text
jjukkumi work complete \
  --dispatch-id <id> \
  --run-id <id> \
  --manifest <json-file-or-stdin> \
  [--result-revision <opaque>]
```

Manifest contains only relative paths and before/after digests. It may trigger exact suppression and one follow-up decision transactionally.

### `work fail`

```text
jjukkumi work fail \
  --dispatch-id <id> \
  --run-id <id> \
  --failure-code <code> \
  [--detail <string>]
```

Records a cooperative failed execution projection with a bounded reason code. It does not contain chain-of-thought. The v0.1 failure-code set is closed: `agent_error`, `canceled`, `timeout`, `environment_error`; the work-receipt schema enforces the set and requires a non-null code for failed receipts. Failure never erases dirty route state: while the route's consecutive-failure budget remains (the route's configured `failure_budget`), one follow-up intent for latest state is created; budget exhaustion requires operator resolution.

## 8. Quarantine Commands

### `quarantine list|show`

Lists held structural cases.

### `quarantine release <id>`

Creates a new operator decision. Requires `--reason` and `--yes`. It revalidates current policy and may create a new dispatch or reconciliation intent.

### `quarantine discard <id>`

Marks the hold resolved without task creation. Requires a reason and preserves audit lineage.

## 9. Reconciliation

```text
jjukkumi reconcile \
  --route <id> \
  --reason initial|scheduled|overflow|fresh-instance|lost-cursor|manual|delivery|stale-active \
  [--submit]
```

Default behavior persists the current-state reconciliation decision. `--submit` attempts an eligible intent. Installed scheduled recipes may include `--submit` only after the production gate.

## 10. Status and Doctor

### `status`

Returns route active/dirty state, queue counts, unresolved delivery, quarantine, last reconciliation, and target capability summary.

### `doctor`

```text
jjukkumi doctor [--probe-targets] [--integrity full]
```

Returns findings with `code`, `severity`, `summary`, `details`, and `remediation`.

## 11. Maintenance

- `maintenance prune --before ... --dry-run|--yes`
- `maintenance vacuum --yes`
- `maintenance integrity [--full]`

Prune is dry-run by default. Vacuum refuses while active attempts exist.

## 12. JSON Envelope

Successful commands use:

```json
{
  "api_version": "jjukkumi.cli/v1",
  "command": "dispatch",
  "ok": true,
  "result": {},
  "warnings": [],
  "trace_id": "..."
}
```

Errors use the error contract. Command-specific schemas may be added without changing this outer envelope within v1.

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
agent-dispatch setup wiki
agent-dispatch hermes probe|capabilities|profiles
agent-dispatch route list|show|plan|enable|disable|stale|preflight|set-profile|set-skills
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

Performs schema and semantic validation. `--probe-targets` invokes read-only public probes: hermes targets report minimum-version eligibility against the declared floor (a below-floor or unreachable target warns; the E11-T2 capability probe replaces this with per-executable shape evidence), and hermes-webhook targets report their static, evidence-tied capability declaration with no endpoint network I/O, because the receiving platform cannot be assumed running (E0-T4 §9).

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

The config field `enabled: true` permits activation but does not by itself activate a production route. `route enable` stores an acknowledged route revision in SQLite. A behavior-sensitive revision change pauses the route until explicitly acknowledged again. `route disable` immediately prevents new submissions while preserving observations, active work, and dirty state. `route enable` gates on the destinations contract (E11-T1, E12-T2): a route may declare several destinations but they must all bind the SAME target — the one Hermes Kanban submission surface (FAN-011); destinations referencing different targets refuse at exit 3 naming both targets. Every declared destination's profile is checked on the target's board (only a CONFIRMED missing profile refuses at exit 3, HER-015). Unresolved legacy work from a different route revision refuses at exit 14 with the resolution exits named (DAT-013), and a hermes destination's installed Hermes must meet the declared `minimum_version` eligibility floor — a below-floor version refuses at exit 3 (HER-011) while an unreachable executable warns and eligibility defers to the submit path's run-time gate, so re-acknowledging a paused route is never hostage to target liveness. The retired operator-authored capability report is gone from every surface; per-executable capability shape evidence and its activation fingerprint arrive with E11-T2.

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

Creates or verifies the route trigger. No replacement occurs without `--replace`. Install resolves and persists the managed binding (E10-T2, SRC-009): the configured resource root, the actual watch root Watchman canonicalized (which may be an ancestor of the configured root), the configured-root-relative path between them, and the trigger name. When the actual root is an ancestor, the trigger is installed with `relative_root` so only the configured subtree can invoke the managed command; a reinstall after the watch moved also removes the stale managed trigger from the previous actual root. The success envelope reports the binding alongside the watch root and disposition.

### `watchman status`

```text
agent-dispatch watchman status --route <id>
```

Reports the same effective binding, the effective include/exclude patterns, the expected and installed trigger definitions, and the state: `installed`, `missing`, `diverged` (definition mismatch), or `drifted` (the persisted binding no longer matches the live watch topology). An unwatched configured root sets the separate `watch_root_state` member to `not_watched` (the `state` member stays `missing`, or `drifted` when a persisted binding is stale) and reports the persisted binding when one exists.

### `watchman remove`

```text
agent-dispatch watchman remove --route <id> --yes
```

Removes only the exact managed trigger, searching the persisted binding's actual root and every root the server currently watches (SRC-012): it succeeds only after re-listing proves the managed trigger absent on every applicable root, and reports the per-root proof. It never removes the Watchman watch root automatically.

### `watchman test`

```text
agent-dispatch watchman test [--fixture <path>] [--route <id>]
```

Uses a temporary or supplied fixture and prints normalized source input without Hermes side effects. With `--route`, it also resolves and reports the same effective binding the other lifecycle commands use, from the persisted record (or the configured root with a trivial relative root when none is persisted) — with no server contact.

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

Closes one dead-lettered dispatch as superseded through the declared edge while keeping the record and its audit history inspectable; the closed lineage becomes retention-resolvable. The envelope reports the measured outcome (`slot_released`, `route_to_idle`, `dirty_generation_retained`), all measured on the dispatch's destination lane (E12-T2): the closed letter releases only its own lane's slot (a sibling lane's coordination is untouched, CON-007), a clean active lane whose only work was the closed letter moves to IDLE, and a dirty lane retains its generation for reconciliation. Route-level `uncertain`/`quarantined` holds are unaffected and still block every lane. Requires `--reason` (DUR-009, E7-T7/M-7).

### `dispatches refresh <id>`

Re-reads the target execution status for one accepted dispatch (public lookup by its stored external reference) and persists an execution-projection receipt. Acceptance is never reinterpreted (HER-008): a projection that cannot be derived is recorded as unavailable with its reason, never as success, and a target without the execution capability reports `lookup_unsupported` instead of an emulated projection. `route show` projects the active dispatch's latest persisted execution state and warns when the active dispatch is older than the configured `active_stale_after`; stale work is warned, never auto-failed.

### `dispatches reprocess <batch-id>`

Evaluates a retained batch against the current route policy and creates a new decision. It does not mutate the original decision.

### `dispatches rerun <dispatch-id>`

Creates an intentional new work request with new dispatch ID and idempotency key. Requires `--reason` and `--yes` in non-interactive mode. Eligibility (E7-T2): the original must be `ready` or `dead_lettered`; the rerun supersedes it through its declared state-machine edge in the same transaction, so exactly one authoritative request remains. In-flight work (`submitting`, `unknown`, `reconciling`) is refused with recovery guidance (`dispatches drain` resolves expired leases and unknown delivery), `retry_wait` work belongs to `dispatches retry`, and `accepted` or otherwise terminal work keeps its authoritative lineage. Destination lane (E12-T1): a stored request from before the destinations[] cutover resolves the live certified lane — persisting the destination-revision record it references — and refuses at `transition_invalid` with regeneration guidance when neither the stored request nor the live configuration names a lane (DAT-013). The rerun itself lands as one child beneath its own aggregate event; the stale rebuild the drain path performs shares this contract.

### `dispatches drain`

The automatic-write gate applies: a route whose activation state or whose configuration `enabled` key is off submits nothing (the expired-lease recovery sweep still runs; the skip is visible in the envelope's skipped count, and a configuration-disabled route reports the skip as a warning).

```text
agent-dispatch dispatches drain --route <id> --max <N>
```

Operator command for bounded ready/retry work, in a fixed order: first the route's expired `submitting` leases are recovered to `unknown` (the `recovered` envelope field lists each), then the route's `unknown` dispatches are reconciled (lookup by idempotency key then external reference; on Hermes Kanban the by-key read does not exist, so unresolved ambiguity dead-letters for the operator, whose `dispatches retry` resubmits the same idempotency key through the dedup-safe path; the envelope reports each reconciliation under `reconciled` and per-dispatch failures as warnings without blocking the route's due work), then the due `ready`/`retry_wait` intents are submitted — only the dispatch holding its destination lane's active slot, never beside another authoritative task on its lane (CON-001 per lane, CON-007), and never while a route-level `uncertain` or `quarantined` hold stands (it blocks every lane). A route left in `FOLLOWUP_READY` with an already accepted follow-up (a crash between acceptance and promotion) is promoted by this command. Not installed as a Watchman trigger.

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

Returns route active/dirty state, queue counts, unresolved delivery, quarantine, last reconciliation, the per-target summary (the static webhook capability declaration; the hermes probed-compatibility contract with its frozen-interface capability set), and the per-route OPS-013 drift projection (capability, profile, skill, watchman, reconciliation).

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

## 18. v0.1.5 Command Surface (shipped)

The commands below are the shipped v0.1.5 surface.

```text
agent-dispatch --help
agent-dispatch setup wiki
agent-dispatch hermes probe [--target <id>] [--profile <profile>]
agent-dispatch hermes capabilities [--refresh] [--target <id>] [--profile <profile>]
agent-dispatch hermes profiles [--target <id>]
agent-dispatch route preflight --route <id>
agent-dispatch route set-profile <route>:<destination> <profile>
agent-dispatch route set-skills <route>:<destination> <skill>...
```

`events show <event-id>` and `notifications test|list|retry|drain` are
E12/E13 work and are not part of the shipped surface; an invocation
today is `command_unknown` at exit 2.

Every root and group parser accepts `-h` and `--help`. Help states required
flags, defaults, output modes, exit codes, side effects, production approval,
examples, shell completion, and the next safe command. A destination qualifier
may be omitted by `set-profile` or `set-skills` only when the route has exactly
one destination; otherwise the command is a usage error.

`hermes probe` (E11-T2) runs the bounded public-interface probe set for
one hermes target — version and eligibility, the assignees and list
JSON shapes, the create-surface flag contract from the help text, and
the profile-scoped skill table under the fixed rendering environment —
and writes the owner-only capability-evidence cache
(`~/.config/agent-dispatch/hermes-capability-<target>.json`); every
probe is read-only and no Hermes state is touched. `hermes capabilities`
prints the cached evidence (fingerprint, version, per-shape outcomes,
and the derived capability set) through the standard envelope,
re-probing transparently when the cache is missing or stale and with
`--refresh` on request; incomplete evidence refuses with
`config_capability_missing` at exit 3 naming the missing capability.
Route activation binds the record's fingerprint beside the acknowledged
revision, and the submit path re-proves it against the live executable
before any side effect (HER-018, AC-703).

`setup wiki` is an interactive walkthrough (shipped with E11-T4): with
an explicitly named configuration it uses a disabled base in place (an
enabled base becomes a disabled draft beside it, re-runnable across
setup attempts); without one it generates a fresh disabled example
after prompting for the vault root. The six steps are: validate the
configuration, run the Hermes probes, preflight the destination, check
the Watchman binding state (printing the explicit install and test
commands — setup does not install the trigger), run the initial dry
reconciliation, and print the exact production-gate command. It stops
there: it never enables the route, never accepts production approval
implicitly, and selects nothing on the operator's behalf beyond the
documented defaults. JSON commands use the existing envelope and
represent empty collections as `[]` or `{}`.

Stable v0.1.5 usage/refusal codes join the error registry before implementation;
integration drift maps to target/capability refusal, ambiguous notification
delivery remains notification state rather than a dispatch exit, and config
mutation never partially writes a file.

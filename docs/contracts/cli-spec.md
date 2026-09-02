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
agent-dispatch hermes probe|capabilities|profiles|set-minimum-version
agent-dispatch route list|show|plan|enable|disable|stale|preflight|set-profile|set-skills
agent-dispatch watchman install|status|remove|test
agent-dispatch dispatch
agent-dispatch dispatches list|show|retry|reprocess|rerun|discard|refresh|drain
agent-dispatch events show
agent-dispatch notifications test|list|retry|drain
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

The fan-out dispatch envelope (E12-T2 and the E12 epic validation) carries
`fanout` — one `{destination_id, dispatch_id, dirty_generation}` row per
selected destination in destination order — plus `failed_lanes` (bounded,
redacted per-lane error text with the durable dispatch ID of a failed
activation) and `merged_lanes` (the lanes that merged into their dirty
generation while a sibling activated; also emitted as stderr notes). The
canonically-first lane's child is the envelope's `dispatch_id` and the one
the command submits; the sibling children stay ready and reach the target
through `dispatches drain`. An occurrence no destination's conditions
select refuses at exit 3 (configuration class) creating nothing — no
aggregate, no child, no intent. An invalid fan-out record is the other
exit-3 configuration failure (E12 epic whole-review round 2): a
destination revision that is not the content address of its projection
bytes, or a child referencing a destination revision with no durable
row (`ErrInvalidFanoutRecord`) — a producer/configuration defect that is
never lane-isolated and never retryable, so even when sibling lanes
succeeded the command fails at exit 3 naming the record. A
non-configuration lane failure beside a delivered sibling is exit 0 BY
CONTRACT (E12 cold validation round 1): the occurrence is partially
durable — the delivered lanes proceed and the failed lane's burst rides
`failed_lanes`, its stderr warning, and the lane's own
retry/dead-letter/reconcile resolution, never a replay of the occurrence
— whereas every selected lane failing is the command-level error the
coordinator returns (unlike `reconcile`, whose operator-driven repair
exits non-zero on any undelivered sibling because nothing else will
surface it).

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
  [--status completed|partially_completed|blocked] \
  --manifest <json-file-or-stdin> \
  [--remaining-manifest <json-file-or-stdin>] \
  [--manual-reason <text>] \
  [--result-revision <opaque>]
```

Manifest contains only relative paths and before/after digests. It may trigger exact suppression and one follow-up decision transactionally. Since work-receipt/v2 (E12-T3, FBK-009) the outcome is explicit: `--status completed` (the default) is the full-completion path; `--status partially_completed` requires `--remaining-manifest` (a non-empty bounded scope of the paths that remain) and records both scopes — the current child completes its lane and exactly one follow-up on the SAME destination lane carries the remaining scope unioned with the lane's unresolved dirty changes (FBK-010; an empty remaining scope is rejected with guidance to use completed); `--status blocked` requires `--manual-reason` (non-empty, bounded) and records the blocked receipt WITHOUT completing the lane or scheduling anything (FBK-011) — the child stays active on its lane, nothing auto-runs (the automatic retry machinery only touches retry_wait/dead-lettered states), and resolution is operator-only: run `work begin` with a fresh `--run-id` for that dispatch, then `work complete` or `work fail` under that run (or `dispatches rerun`). `--manifest` is the completed scope and is required except for `--status blocked` (a blocked run may have changed nothing). The full work-receipt/v2 document form routes through its own outcome: a document carrying `partially_completed` (with both scopes) or `blocked` (with its manual reason) submits through `--manifest` alone, an explicit `--status` that disagrees with the document rejects as a conflict, and a `--remaining-manifest` that differs from the document's remaining scope rejects likewise. `--remaining-manifest` outside the partial outcome is a usage error; `--manual-reason` outside the blocked outcome rejects naming exactly the blocked outcome — validated against the EFFECTIVE outcome (the v2 document's status first, the flag status otherwise, so a blocked document carrying its matching flag reason is the legal doubled form), and a v2 document's `manual_reason` beside a non-blocked outcome rejects the same way; a v2 document's `completed_scope` must be a subset by path of its own `changes` (or empty), because a scope naming unreported paths is unauditable completion evidence; and any change set beside a blocked outcome — bare array or a v2 blocked document's non-empty `changes` — rejects, because blocked takes its reason, not a change set (a v2 blocked document with empty changes remains the shipped form).

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
  [--submit] \
  [--baseline-only]
```

Default behavior persists the current-state reconciliation decision. `--submit` attempts an eligible intent and then drains the route's other due work (bounded), so a pending follow-up generation reaches the target on the scheduled path without a manual drain (E7-T2); an accepted follow-up is promoted to the route's active task at acceptance. A route whose activation state is not `enabled` fails closed with the state-conflict classification (`transition_invalid`, exit 14), never a storage failure. The installed scheduled recipes carry `--submit` unconditionally (E9-T4/T2-F001): the two-key gate is the safety boundary — before the production acknowledgement the route fails closed at exit 14 as above, and after it a route disabled in configuration (the YAML key) persists its reconciliation decisions and recovers without submitting.

`--baseline-only` is the documented disabled-route operation of E14-T2 (ADR-0020, CLI-017, DUR-017): it runs only while the route is disabled in both halves of the production gate — the configuration key off and the runtime activation `disabled`, with no `uncertain` or `quarantined` hold — and refuses every other state with the same `transition_invalid` exit 14 (checked before the enumeration and re-fenced inside the commit transaction). A clean host with no runtime row is the operation's home posture: the fenced transaction materializes the trusted resource row (never rewriting an existing registration), advances the observation revision, replaces the bounded path-fact snapshot, and upserts the one route baseline record — observation revision, fact count, canonical snapshot digest, route and policy revisions, reason, and timestamp — as ONE observation-fenced transaction, so a crash leaves either the previous or the complete new baseline and a rerun converges. It creates no policy decision, dispatch intent, Hermes task, production acknowledgement, or notification, has no submit path (combining it with `--submit` is the usage error `flag_invalid` at exit 2), and reports `baseline_only` in its envelope with `snapshot_stored` false plus `concurrent_change` true when the observation fence refuses a stale snapshot.

Since the E12 epic validation a reconciliation on a multi-destination route fans out PER LANE (FAN-002): one shared aggregate and decision, one child per certified destination lane with origin `reconcile`. The sibling children commit DURABLY FIRST — before the first lane's transaction — through the app-layer commit loop (`dispatch.CommitReconcileSiblings`), so a crash between the two leaves ready siblings the existing `dispatches drain` submits, never a first lane whose remaining selection left no durable marker (E12 epic whole-review round 1). Where `reconcile_lanes` rides depends on the envelope shape: the NO-SUBMIT result and the two `--submit`-skipped shapes render the enumeration result's own members at the top level with `reconcile_lanes` merged in BESIDE them, while the completed `--submit` envelope nests the result under `result` and carries `reconcile_lanes` at the top level beside `submitted`/`drained`; a first-lane failure carries it inside the error envelope's `result` slot. `reconcile_lanes` lists the SIBLING lanes only — the first lane's outcome is the result's own members (`reconcile_dispatch_id`, the enumeration counts). Each row is `{destination_id, dispatch_id, committed}` with a `note` (slot-held skip, or the duplicate idempotency key of an already-committed lane on a retry) or bounded `error`.

The sibling policy follows CON-007 isolation: a sibling whose lane already holds its own active work, or whose identical child already exists (a retry after partial failure — the content-derived idempotency key collides), skips with a warning (stderr and the envelope's `warnings` member; its lane keeps its coordination, exit stays 0, nothing new commits for that lane), and a sibling failing for any other reason never blocks the first lane but is never silent — the reconcile exits NON-ZERO with the mapped error on stderr (the result envelope still ships on stdout so the operator sees the delivered lanes, and a storage fault maps to the storage class, 20), because the occurrence is not fully durable.

## 10. Status and Doctor

### `status`

Returns route active/dirty state, queue counts, unresolved delivery, quarantine, last reconciliation, the per-target summary (the static webhook capability declaration; the hermes probed-compatibility contract with its frozen-interface capability set), the per-route OPS-013 drift projection (capability, profile, skill, watchman, reconciliation), and — since E12-T3 (OPS-011) — one bounded per-destination lane summary per route (destination id, lane state, active child, dirty generation) from the destination-lane coordination rows.

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
agent-dispatch hermes set-minimum-version <target> <version>
agent-dispatch route preflight --route <id>
agent-dispatch route set-profile <route>:<destination> <profile>
agent-dispatch route set-skills <route>:<destination> <skill>...
```

`events show <aggregate-id>` lands with E12-T3 (CLI-013): it renders one
occurrence's aggregate — the selection summary as a structured
`selections` array (one `{destination_id, destination_revision,
workstream, reason}` row per selected destination with its closed
reason, never an escaped JSON string inside the envelope), origin,
generation, and content fingerprint — every child beneath it with
separate destination, intent-state, acceptance, execution-projection,
work-receipt (status + validity), retry, and completion-evidence
projections, and the aggregate status as the worst child class
(evidence-gap > manual-intervention > failed > in-progress > completed;
FBK-012: accepted work without a valid attributable TERMINAL work receipt
— absent, invalid, or a still-begun run — renders `completion_evidence:
missing` with the actionable next step, never "completed"; a valid
terminal receipt (completed, partially_completed, blocked, failed)
renders `present`; a never-accepted child renders `not-applicable`. A
valid BEGUN receipt classifies the aggregate in-progress, not
evidence-gap — the run is known-busy work, and the gap class stays for
accepted work with no (or an invalid) receipt to trust, E12 epic
whole-review round 1). `notifications test|list|retry|drain` land with
E13-T2 (CLI-013): `test --route <id> --sink <id>` delivers one
transport-level probe of the declared sink — the payload is the
notification-event/v1 envelope with the dedicated `test` event value
(outside the stored vocabulary by design), its stable idempotency
identity derives from the (route, sink) pair so repeated probes
deduplicate at the endpoint, and nothing is stored: no notification
intent, no source event, no Hermes task (NTF-008); `list` renders the
durable intents with `--route`, `--state`, `--sink`, and `--limit`
filters plus each row's attempt count and last outcome (NTF-004);
`retry <notification-id>` re-arms one refused notification — the
operator's explicit decision — and performs one attempt under the
notification's stable idempotency identity, refusing a delivered
notification at exit 4 (NTF-007); `drain` performs one bounded attempt
per pending notification, oldest first, bounded by `--limit`, and never
evaluates drift — the OPS-013 drift evaluation rides the scheduled
runner as its only automatic surface (v0.1.6 §4), so the explicit drain
stays recursion-free. The envelope's `pending` and
`pending_remaining` report the store's post-pass pending truth — the
pass bound never hides a backlog. Delivery outcomes are data, never
exit codes: an ambiguous or
retryable outcome stays pending for the next pass, and no delivery
outcome ever mutates dispatch, receipt, or work state (NTF-005); the log
sink emits its structured payload lines on stderr so stdout keeps the
one-envelope contract. `status` projects the notification by-state
counts and warns on pending delivery work (observability-and-operations
§10).

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
before any side effect (HER-018, AC-703). Since E15-T2 the probe
contract is v3 and every record certifies one effective serialization
mode (HER-020) — `agent-dispatch-group-enforced` (the normal posture of
a target without `--mutex-key`, never a failure), 
`agent-dispatch-group-plus-target-mutex` (the renderer then sends the
effective serialization group as the complementary target mutex), or
`unsupported-unsafe` (fail closed before any side effect) — reported by
`hermes probe` and `hermes capabilities` and consumed identically by
preflight, enablement, rendering, and submission revalidation; the
product floor is 0.20.5 and an omitted configured floor fails closed
without rewriting (AC-1107).

`hermes set-minimum-version <target> <version>` (E15-T2, CLI-019) is
the atomic target-floor helper: it accepts only versions at or above
0.20.5, validates the candidate, and replaces the configuration file
atomically so that only the selected target's `minimum_version`
changes and every unrelated route and target keeps its bytes. The
floor joins the route revision through the target projection, so the
result names every affected route with its before/after revision and
the fresh probe, preflight, and production re-acknowledgement it now
owes; a below-floor version, an unknown target, or an unchanged floor
refuses with `config_invalid` at exit 3 leaving the file untouched.

`setup wiki` is an interactive walkthrough (shipped with E11-T4,
route-correct and rerunnable since E14): with an explicitly named
configuration it uses a disabled base in place (an enabled base becomes
a disabled draft beside it, re-runnable across setup attempts); without
one it generates a fresh disabled example after prompting for the vault
root, which must already exist — the walkthrough never creates the
operator's vault, and a missing root is a genuine finding that stops at
the baseline step (exit 3 with the re-run guidance) before the gate
summary, leaving the generated disabled configuration in place for a
converging rerun. The route is chosen explicitly (E14-T1): `--route <id>` names a
declared route, a single-route configuration selects its only route,
and multiple routes require the flag or an explicit interactive choice —
a non-interactive multi-route invocation fails rather than choosing one
silently. The six steps are: validate the configuration, run the Hermes
probes, preflight the destination, check the Watchman binding state
(printing the explicit install and test commands — setup does not
install the trigger), run the initial baseline
(`reconcile --reason initial --baseline-only`, the disabled-route
operation of §9 that converges across reruns and interrupted attempts),
and print the five-state production-gate summary: configuration enabled
state, runtime activation, Watchman binding, initial baseline, and
production acknowledgement as distinct states read from current durable
facts, followed by the exact reviewed enable command. It stops there:
it never enables the route, never accepts production approval
implicitly, and selects nothing on the operator's behalf beyond the
documented defaults. JSON commands use the existing envelope and
represent empty collections as `[]` or `{}`.

Stable v0.1.5 usage/refusal codes join the error registry before implementation;
integration drift maps to target/capability refusal, ambiguous notification
delivery remains notification state rather than a dispatch exit, and config
mutation never partially writes a file.

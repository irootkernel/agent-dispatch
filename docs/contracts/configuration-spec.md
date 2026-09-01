# Configuration Specification

## 1. Format and Authority

The configuration file is YAML. It is trusted operator policy and should be stored outside the watched vault. The JSON Schema at `schemas/config.schema.json` is the machine-readable baseline, while this document defines semantic validation that JSON Schema alone cannot express.

Configuration precedence:

1. `--config <path>`
2. `AGENT_DISPATCH_CONFIG`
3. platform default config path

No event payload may override configuration.

## 2. Top-Level Shape

```yaml
version: 1
instance:
  id: workstation-main
  state_dir: /absolute/local/path
  log_paths: relative

limits:
  max_stdin_bytes: 4194304
  max_path_bytes: 4096
  max_hash_file_bytes: 16777216
  max_subprocess_output_bytes: 1048576

# resources and routes each require at least one entry; the two target
# maps are optional and validated against the routes' references
# (sections 4-6).
resources:
  vault-main: {}
hermes_targets:
  hermes-main: {}
routes:
  wiki-maintenance: {}
retention: {}
```

## 3. Instance

| Field | Required | Meaning |
|---|---:|---|
| `instance.id` | yes | Stable operator-defined local instance ID. |
| `instance.state_dir` | no | Override for local state directory. Must be local filesystem. |
| `instance.log_paths` | no | `relative`, `redacted`, or `full`. Default `relative`. |

`state_dir` must not be inside the watched vault by default. Validation warns if it is. The global `--state-dir` option overrides the configured and platform-default state directory for one invocation (an absolute local path, fail-closed otherwise); the global `--timeout` option bounds the command's context-taking store operations through the schema-exact duration grammar and fails closed on an invalid value (context-free engine operations such as VACUUM and the backup snapshot are not interruptible and are not claimed).

## 4. Resources

```yaml
resources:
  vault-main:
    type: directory
    root: /Users/example/Obsidian/Vault
    file_scope: markdown
    git:
      mode: optional
```

| Field | Rule |
|---|---|
| resource key | `^[a-z][a-z0-9._-]{0,63}$` |
| `type` | `directory` in v0.1 |
| `root` | Existing absolute directory; canonicalizable and readable |
| `file_scope` | `markdown` in v0.1: the scope predicate admits the `.md` and `.markdown` extensions (SCP-003's delivered scope, L-26) |
| `git.mode` | `disabled` or `optional`; `required` is reserved for future routes. Inert in v0.1: no code path consumes the mode yet — it is recorded in the computed route revision and the durable resource registration, so changing it pauses the acknowledged route without changing behavior until a consumer lands |

A resource root is resolved to a canonical identity during validation. Symlinks inside the root remain subject to containment checks.

## 5. Targets

Two target maps exist since the v0.1.5 destinations cutover (E11-T1,
OPS-014): `hermes_targets` owns Hermes Kanban targets and `targets`
owns webhook targets. A legacy `hermes-kanban` entry under `targets` is
refused with the regeneration path; there is no load-time conversion.

### Hermes Kanban (`hermes_targets`)

```yaml
hermes_targets:
  hermes-main:
    board: agent-dispatch
    executable: hermes
    minimum_version: 0.20.5
    compatibility: capability_probe
    submit_timeout: 30s
    lookup_timeout: 15s
    environment_allowlist:
      - HOME
      - PATH
```

`board` names the Hermes kanban board the destinations submit to. `submit_timeout` defaults to 30s; the core's attempt lease TTL is derived from it (the configured timeout plus a 30 s margin, E8-T2/M-1), so a live submitter inside the operator-approved window can never have its lease stolen by a recovery sweep. The operator creates it once with the public `hermes kanban boards create <slug>` command; Agent Dispatch never creates, renames, or deletes boards. `minimum_version` is the explicit eligibility floor — at least 0.20.5, with no maximum (HER-011, ADR-0021); the value is required, an omitted floor fails closed and is never defaulted or implicitly rewritten (AC-1107), and a floor below 0.20.5 fails validation. `compatibility` is `capability_probe` in v0.1.5: compatibility is proven against the public interface, not an operator-authored file. The E0-T4 `capability_report` and `required_capabilities` fields are retired with the cutover: the frozen 0.20.5 runtime-verified interface (docs/integrations/hermes-capability-report.json) is the interim truth source for the unconditional delivery-evidence set — durable acceptance, idempotent submission, external-reference reconciliation — while the E11-T2 capability probe records the per-executable evidence and binds activation to its fingerprint, whose record certifies one effective serialization mode (HER-012, HER-018, HER-020). Exact command mapping is compiled into or versioned with the adapter after verification; it is not supplied by untrusted route data.

### Hermes Webhook

```yaml
targets:
  hermes-webhook-immediate:
    type: hermes-webhook
    endpoint: https://example.invalid/public/hermes-hook
    auth:
      type: bearer
      secret_ref: env:HERMES_WEBHOOK_TOKEN
    submit_timeout: 30s
    idempotency_header: Idempotency-Key
```

Webhook targets are explicit targets and never fallback targets. The
`idempotency_header` must not collide with `Authorization`,
`Content-Type`, `Host`, `Content-Length`, or the configured
`auth.header_name` — the target fails validation on a collision (the
later header write would silently drop the idempotency key). The endpoint must be an `https` URL; redirects are never followed (a redirecting endpoint is a definite routing rejection). `auth.type` is `bearer` (Authorization: Bearer) or `header` (a custom `header_name` carrying the secret); `auth.header_name` is required for `header` and must be empty for `bearer`. The secret reference resolves immediately before each submission and never enters SQLite or logs (SEC-006). `auth.header_name` and `idempotency_header` must consist solely of RFC 9110 token characters (ALPHA, DIGIT, and `!#$%&'*+-.^_`|~`): any other separator, quote, backslash, space, control character, or non-ASCII rune fails configuration validation before a submission attempt (E9-T7). `idempotency_header` defaults to `Idempotency-Key`; the core's idempotency key is transmitted verbatim so the same dispatch retry presents the same key (WHK-005). `submit_timeout` defaults to 30s. The optional `required_capabilities` gates against the adapter's static declaration (HER-005): the webhook declares `durable_acceptance` false — a 2xx is transport acceptance only — so requiring it fails validation.

## 6. Routes

```yaml
routes:
  wiki-maintenance:
    # This permits activation. Runtime activation still requires `route enable`.
    enabled: false
    source:
      type: watchman-trigger
      source_id: vault-main-watchman
      resource: vault-main
      trigger_name: agent-dispatch.wiki-maintenance.4f8c21
      include:
        - "**/*.md"
      exclude:
        - ".git/**"
        - ".obsidian/workspace*.json"
        - ".obsidian/cache/**"

    batching:
      automatic_threshold: 25
      hard_limit: 100
      max_manifest_bytes: 262144

    policy:
      protected:
        - "raw/**"
        - "canon/**"
      immutable: []
      bulk_action: quarantine
      overflow_action: reconcile
      fresh_instance_action: reconcile
      unsafe_path_action: quarantine

    fanout_mode: all
    destinations:
      - id: indexing
        target: hermes-main
        profile: wiki-maintainer
        skills:
          - llm-wiki
        workstream: indexing
        serialization_group: wiki-publish
        execution_hints:
          max_runtime: 30m
          max_attempts: 2
    submission_retry:
      max_attempts: 3
      initial_backoff: 2s
      max_backoff: 2m
      multiplier: 2.0
      jitter_fraction: 0.2
    latest_state: true
    failure_budget: 2
    active_stale_after: 2h

    reconciliation:
      initial: true
      daily_expected: true

    retention:
      observations: 30d
      completed_receipts: 180d
```

## 7. Include and Exclude Semantics

- Patterns apply to normalized slash-separated relative paths.
- `**` recursive matching is required.
- Exclude patterns are directory-aware (E10-T2, PTH-009): a pattern that matches a path prefix at a segment boundary excludes everything inside that directory, so an exact-directory exclusion (`Secrets`) covers its whole subtree exactly like a recursive one (`Secrets/**`), and a file or glob pattern also covers a same-named directory.
- Exclude takes precedence over include.
- Protected and immutable patterns are evaluated after include/exclude.
- Pattern behavior is explicit per host through the resolved case mode (the supported host is macOS/darwin-arm64, D-023; the resolver stays host-derived so a future platform carries its own explicit mode).
- Case sensitivity follows the configured policy, not an accidental host filesystem behavior. v0.1 default is `filesystem`, and the resolved behavior is recorded in the route revision.

## 8. Policy Actions

Allowed actions:

| Action | Meaning |
|---|---|
| `quarantine` | Persist and require explicit operator resolution. |
| `reconcile` | Merge one full current-state reconciliation generation. |
| `drop` | Only permitted for explicitly ignorable structural cases, never overflow. |

Protected and unsafe paths default to quarantine. Overflow and fresh instance default to reconcile.

Two keys are inert in v0.1 and stated as such (L-3): `unsafe_path_action` is validated and recorded in the computed route revision but no code path consumes it — the unsafe-path containment rejection (exit 30, `source_unsafe_path`) fires before any policy action today, so the key cannot change an outcome until a consuming policy lands; and `git.mode` (see §4) is likewise revision-recorded only. Both stay outside the independent policy digest (E9-T3/L-18) until they become behavior-affecting.

## 9. Retry and Failure Budget Configuration

Submission retry applies only to attempts to deliver the same dispatch intent. It does not control Hermes execution retries.

Validation rules:

- `max_attempts` from 1 to 10;
- positive initial and maximum backoff;
- maximum >= initial;
- multiplier from 1.0 through 10.0;
- jitter fraction from 0.0 through 0.5.

A remote ambiguity does not consume a normal retry until reconciliation proves non-acceptance.

The route-level `failure_budget` (integer, 1 through 10) bounds consecutive failed or canceled accepted tasks: while the count is within budget, each failure creates one bounded follow-up intent for latest state; a completed task resets the count; exhaustion moves the route to `UNCERTAIN` for operator resolution. The three budgets are distinct: `submission_retry.max_attempts` (delivery attempts for one intent), `execution_hints.max_attempts` (a Hermes execution hint), and `failure_budget` (consecutive accepted-work failures).

## 10. Retention Defaults

```yaml
retention:
  observations: 30d
  attempts: 30d
  completed_receipts: 180d
  resolved_quarantine: 180d
  unresolved: forever
```

`forever` is valid only for unresolved classes. Pruning never deletes a parent still referenced by an unresolved child. In v0.1 `maintenance prune` resolves the effective policy from the instance-level `retention` block; a route-level `retention` block is schema-valid and reserved for per-route pruning in a later release.

## 11. Secret References

Syntax:

```text
env:VARIABLE_NAME
file:/absolute/owner-readable/path
keychain:<provider-specific-reference>
fd:<positive-integer>
```

The config loader parses the reference but resolves its value only immediately before use. JSON display redacts the resolved value and may display the reference identifier.

A `file:` reference must be owner-only (mode 600): a file with group or other permission bits fails closed with the mode named, before any read (SEC-006, E7-T9).

## 12. Semantic Validation

Beyond schema validation, the validator must check:

- all route references exist;
- resource roots do not overlap (the default validation, E8-T5) and are absolute together with `instance.state_dir`;
- the map keys for resources, hermes_targets, targets, and routes follow the identifier grammar (E8-T5);
- `limits.max_hash_file_bytes` is positive when set (E8-T5);
- state directory is local and outside governed roots by default;
- each hermes target declares a non-empty board, an explicit canonical `minimum_version` at or above the 0.20.5 support floor (an omitted floor fails closed), and exactly the `capability_probe` compatibility mode (E11-T1, HER-011); the E11-T2 capability probe proves the shape evidence, `route enable` binds its fingerprint, and the submit path re-proves it live (the retired operator-authored report never returns);
- every destination resolves to a declared hermes or webhook target, and a webhook destination carries no profile, skills, workspace, or mutex (E11-T1);
- webhook-target required capabilities are available (the static declaration must carry every capability a webhook destination names; hermes capability truth is probed, not declared);
- profile, skills, mutex, workstream, and target are operator-owned fixed values;
- all durations and sizes are bounded;
- `enabled: true` only permits activation; SQLite must also contain an explicit operator acknowledgement for the computed route revision, and enablement under the destinations contract refuses while unresolved legacy work from a different route revision remains (an unreachable hermes executable warns and eligibility defers to the submit path's per-attempt gate);
- webhook target is not configured as an automatic fallback;
- configuration revision changes when behavior-affecting fields change.

## 13. Computed Configuration Revision

The operator does not manually enter a route revision. Agent Dispatch computes it from normalized behavior-affecting configuration. Canonical route revision input includes source binding, resource ID, normalized patterns, batch limits, policy actions, the fan-out mode, the sorted destination set with each destination revision — target, profile, sorted skills, workstream, workspace, mutex, execution hints, and sorted selection conditions (E11-T1) — the declared notification policy and sink references, the route-level runtime envelope (submission retry, latest-state flag, failure budget, active-stale bound), the referenced resource's root, file scope, and git mode, the global limits block, and every referenced target's shape and transport bounds: the hermes board, executable, timeouts, environment allowlist, eligibility floor, and compatibility mode, or the webhook endpoint, authentication shape, and idempotency header (E8-T3: repointing a vault or moving a board is behavior-affecting — it changes the idempotency key and pauses the acknowledged route; E9-T3: swapping the target binary or its bounds pauses the acknowledged route; E9-T6: changing how a dispatch authenticates, deduplicates, reconciles, or proves compatibility must pause the acknowledged route like any other behavior change), plus the route's reconciliation flags. Destination declaration order is non-semantic (FAN-012): destinations and their lists are sorted before hashing.

It excludes comments, display order, state directory, the command-line log level, and resolved secret values — a secret REFERENCE is behavior-affecting and joins the digest, the resolved secret never does — and, by explicit disposition, the retention block: pruning bounds (§10, OPS-003) never change what a dispatch submits or how a plan is classified, so a retention edit does not pause an acknowledged route.

## 14. Destinations Cutover Contract (E11-T1)

This section records the cutover E11-T1 delivered: the configuration
contract below is the shipped surface, and the executable schema and
example implement it. The legacy `routes.<id>.dispatch` shape is refused
with the exact regeneration path below and never converted (OPS-014,
D-025); historic database evidence stays queryable through the v10
forward migration, and route enablement under the destinations contract
refuses while unresolved legacy work from a different route revision
remains (DAT-013; since E15-T4 the legacy marker is the absence of a
destinations-contract child row — post-cutover residue resolves through
the retry/discard exits after the re-acknowledgement).

### Serialization groups (E15-T1, ADR-0021, CON-011 through CON-014)

Every destination resolves one effective serialization group — the
explicit `serialization_group`, the deprecated `mutex_key` alias, or
exactly `resource:<resource_id>`. Explicit values are 1 through 255
ASCII bytes matching `^[A-Za-z0-9][A-Za-z0-9._:/-]*$` with no case
folding or Unicode normalization; an explicit default-form value
intentionally joins the resource-derived group. `serialization_group`
and `mutex_key` may coexist only when identical (one deprecation
warning); different or ungrammatical values fail validation, and newly
generated configuration never emits `mutex_key`. A group is global
within one state database: at most one active child holds it, arrivals
for an occupied group merge into the selected lane's dirty generation,
completion promotes the oldest first-dirty waiting lane (destination ID
as the tie break), retries retain the slot, and reruns transfer it
atomically. Destinations governing one resource under different groups
fail `route preflight` unless every involved route sets
`allow_cross_group_concurrency: true`; the acknowledgement and each
effective group join the destination and route revisions, so every
topology change pauses production acknowledgement. The renderer sends
the effective group as the complementary target mutex exactly when the
certified serialization mode is `agent-dispatch-group-plus-target-mutex`
and suppresses the flag otherwise; the local group slot is never
replaced by it.

```yaml
version: 1

routes:
  wiki-maintenance:
    enabled: false
    source:
      type: watchman-trigger
      resource: main-wiki
      include: ["**/*.md"]
      exclude: ["**/B/**", "_exchange/**", "deliverables/**"]
    fanout_mode: all
    destinations:
      - id: indexing
        target: hermes-main
        profile: wolyeong
        skills: [llm-wiki, agent-dispatch-wiki-maintenance]
        workstream: indexing
        workspace: "dir:/srv/knowledge/A"
        serialization_group: wiki-publish
        conditions:
          path_include: ["**/*.md"]
          path_exclude: ["archive/**"]
          operations: [create, modify]
          classifications: [normal]
          policy_outcomes: [dispatch, merge_pending]
    notifications:
      events: [work_completed, work_failed, delivery_unknown]
      sinks:
        - id: operations-webhook
          type: webhook
          endpoint: https://notify.example.invalid/agent-dispatch
          auth:
            type: bearer
            secret_ref: env:AGENT_DISPATCH_NOTIFICATION_TOKEN

hermes_targets:
  hermes-main:
    board: agent-dispatch
    executable: hermes
    minimum_version: 0.20.5
    compatibility: capability_probe
```

Destination IDs are unique within a route; workstream is non-empty; skills are
a unique non-empty list; `fanout_mode` accepts only `all`. Condition keys are a
closed vocabulary. Values within a key use OR and present keys use AND.
Destination map order is non-semantic and canonicalization sorts by ID.

The notification policy (NTF-001/NTF-002, E13-T1) is disabled when the block
is absent or declares no sink. The `events` list is optional: when a sink
exists and no event list is declared — omitted or empty — the default event
set applies (work completed, exhausted failure, unknown delivery, quarantine,
reconciliation required, integration drift, and Watchman drift; `work_failed`
is configurable but not a default). Declared names must come from the closed
vocabulary. The notification-policy revision a notification's dedup identity
carries (NTF-003) digests exactly the effective event set and sink
identity/kind references — never endpoints or authentication references, so
repointing a sink does not re-identify notifications the previous policy
already created.

The optional `drain` block (E16-T1, NTF-010) declares how pending
notification work progresses. An omitted block means `manual` — exactly the
v0.1.5 behavior where only an explicit `notifications drain` delivers — and
newly generated Wiki configuration defaults to `after-command`. Every field
is optional with an explicit effective default:

| Field | Default | Contract |
| --- | --- | --- |
| `mode` | `manual` | `manual`, `after-command`, or `scheduled` (closed vocabulary). |
| `limit` | `100` | The bounded item budget of one drain pass (1 through 500). |
| `failure_policy` | `preserve-pending` | The only value in v0.1.6: a delivery failure leaves the record pending under its original identity. |
| `pending_warn_after` | `1h` | The pending-age window status and doctor warn after. |
| `retry.initial_backoff` | `30s` | The first retry delay; the block is all-or-nothing. |
| `retry.max_backoff` | `15m` | The doubling cap. |
| `retry.multiplier` | `2.0` | The per-retry delay multiplier (1.0 through 10.0). |
| `retry.jitter_fraction` | `0.2` | One symmetric ±20% jitter applied per retry (0.0 through 0.5). |

Both the explicit `notifications drain` and every automatic mode select
due work only: an ambiguous or retryable outcome persists a jittered
backoff deadline (30 seconds doubling to 15 minutes), and
`notifications retry <id>` is the sole operator bypass that returns an
ambiguous, retryable, or refused record to immediately-due pending.

The effective drain policy carries its own inspectable drain-policy
revision that digests every resolved field, and a declared block whose
effective policy differs from the pure default joins the route revision —
so switching a live route to automatic draining pauses production
acknowledgement — while a block equivalent to the default (including
omission) keeps the exact v0.1.5 route revision. Drain behavior never
joins the notification-policy revision, so drain changes never re-identify
notifications the previous policy already created.

The route revision includes the normalized source and pattern policy, sorted
destination set and each destination revision, fan-out conditions, runtime and
retry hints, notification policy and sink references, the effective drain
policy when it differs from the default, plus every previously
documented behavior-affecting field. Destination revision includes its target,
profile, skills, workstream, workspace, mutex, hints, and conditions. Resolved
secret values remain excluded.

Legacy `routes.<id>.dispatch` is an actionable validation error under D-025;
there is no load-time conversion or migration preview. `setup wiki` and the
configuration mutation commands write a validated temporary file, preserve
mode, fsync, and atomically replace the requested config while leaving it
disabled or revision-paused.

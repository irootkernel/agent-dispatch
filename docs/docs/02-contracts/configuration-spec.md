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

# resources, targets, and routes each require at least one entry;
# see sections 4-6 for entry fields.
resources:
  vault-main: {}
targets:
  hermes-kanban-main: {}
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

### Hermes Kanban

```yaml
targets:
  hermes-kanban-main:
    type: hermes-kanban
    board: agent-dispatch
    executable: hermes
    capability_report: /path/to/hermes-capabilities.json
    required_capabilities:
      - durable_acceptance
      - submit_idempotency_key
      - lookup_by_external_ref
    submit_timeout: 30s
    lookup_timeout: 15s
    environment_allowlist:
      - HOME
      - PATH
```

`board` names the Hermes kanban board the route submits to. `submit_timeout` defaults to 30s; the core's attempt lease TTL is derived from it (the configured timeout plus a 30 s margin, E8-T2/M-1), so a live submitter inside the operator-approved window can never have its lease stolen by a recovery sweep. The operator creates it once with the public `hermes kanban boards create <slug>` command; Agent Dispatch never creates, renames, or deletes boards. `capability_report` is generated and verified by the E0-T4 compatibility task. It contains no secrets. Exact command mapping is compiled into or versioned with the adapter after verification; it is not supplied by untrusted route data. `required_capabilities` names the sink capabilities the route depends on; key-based reconciliation on Hermes runs through the idempotent dedup submission (`submit_idempotency_key`), and `lookup_by_idempotency_key` — the port-level read-only query — is honestly false (the public CLI has no such query, E8-T3), so routes that reconcile by reference require `lookup_by_external_ref`.

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

    dispatch:
      target: hermes-kanban-main
      profile: wiki-maintainer
      skills:
        - llm-wiki
      mutex_key: wiki-publish
      latest_state: true
      submission_retry:
        max_attempts: 3
        initial_backoff: 2s
        max_backoff: 2m
        multiplier: 2.0
        jitter_fraction: 0.2
      execution_hints:
        max_runtime: 30m
        max_attempts: 2
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

The route dispatch field `failure_budget` (integer, 1 through 10) bounds consecutive failed or canceled accepted tasks: while the count is within budget, each failure creates one bounded follow-up intent for latest state; a completed task resets the count; exhaustion moves the route to `UNCERTAIN` for operator resolution. The three budgets are distinct: `submission_retry.max_attempts` (delivery attempts for one intent), `execution_hints.max_attempts` (a Hermes execution hint), and `failure_budget` (consecutive accepted-work failures).

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
- the map keys for resources, targets, and routes follow the identifier grammar (E8-T5);
- `limits.max_hash_file_bytes` is positive when set (E8-T5);
- state directory is local and outside governed roots by default;
- the target capability report file is readable and carries every required capability (the default validation; a not-yet-placed report warns, E8-T3), while the report's match against the installed target version requires the live probe and stays on `config validate --probe-targets` and `route enable`;
- route-required capabilities are available;
- profile, skills, mutex, and target are operator-owned fixed values;
- all durations and sizes are bounded;
- `enabled: true` only permits activation; SQLite must also contain an explicit operator acknowledgement for the computed route revision;
- webhook target is not configured as an automatic fallback;
- configuration revision changes when behavior-affecting fields change.

## 13. Computed Configuration Revision

The operator does not manually enter a route revision. Agent Dispatch computes it from normalized behavior-affecting configuration. Canonical route revision input includes source binding, resource ID, normalized patterns, batch limits, policy actions, target ID, profile, skills, mutex, latest-state flag, submission retry, execution hints, failure budget, capability requirements, the referenced resource's root, file scope, and git mode, the global limits block, the target's type, board, and endpoint (E8-T3: repointing a vault or moving a board is behavior-affecting — it changes the idempotency key and pauses the acknowledged route), the transport bounds — the executable, submit timeout, environment allowlist, and manifest byte bound (E9-T3: swapping the target binary or its bounds pauses the acknowledged route) — and the delivery-evidence surface: the authentication type, secret reference, auth header name, idempotency header, lookup timeout, and capability-report path, plus the route's reconciliation flags (E9-T6: changing how a dispatch authenticates, deduplicates, reconciles, or proves capability must pause the acknowledged route like any other behavior change).

It excludes comments, display order, state directory, the command-line log level, and resolved secret values — a secret REFERENCE is behavior-affecting and joins the digest, the resolved secret never does — and, by explicit disposition, the retention block: pruning bounds (§10, OPS-003) never change what a dispatch submits or how a plan is classified, so a retention edit does not pause an acknowledged route.

## 14. Planned v0.1.5 Configuration Contract

This section is the approved target contract. The executable schema and example
remain the shipped v0.1.4 contract until E11-T1 implements and validates the
cutover.

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
        mutex_key: wiki-publish
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
            secret_ref: AGENT_DISPATCH_NOTIFICATION_TOKEN

hermes_targets:
  hermes-main:
    executable: hermes
    minimum_version: 0.19.1
    compatibility: capability_probe
```

Destination IDs are unique within a route; workstream is non-empty; skills are
a unique non-empty list; `fanout_mode` accepts only `all`. Condition keys are a
closed vocabulary. Values within a key use OR and present keys use AND.
Destination map order is non-semantic and canonicalization sorts by ID.

The route revision includes the normalized source and pattern policy, sorted
destination set and each destination revision, fan-out conditions, runtime and
retry hints, notification policy and sink references, plus every previously
documented behavior-affecting field. Destination revision includes its target,
profile, skills, workstream, workspace, mutex, hints, and conditions. Resolved
secret values remain excluded.

Legacy `routes.<id>.dispatch` is an actionable validation error under D-025;
there is no load-time conversion or migration preview. `setup wiki` and the
configuration mutation commands write a validated temporary file, preserve
mode, fsync, and atomically replace the requested config while leaving it
disabled or revision-paused.

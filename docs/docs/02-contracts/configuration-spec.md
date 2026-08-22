# Configuration Specification

## 1. Format and Authority

The configuration file is YAML. It is trusted operator policy and should be stored outside the watched vault. The JSON Schema at `schemas/config.schema.json` is the machine-readable baseline, while this document defines semantic validation that JSON Schema alone cannot express.

Configuration precedence:

1. `--config <path>`
2. `JJUKKUMI_CONFIG`
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

`state_dir` must not be inside the watched vault by default. Validation warns if it is.

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
| `file_scope` | `markdown` in v0.1 |
| `git.mode` | `disabled` or `optional`; `required` is reserved for future routes |

A resource root is resolved to a canonical identity during validation. Symlinks inside the root remain subject to containment checks.

## 5. Targets

### Hermes Kanban

```yaml
targets:
  hermes-kanban-main:
    type: hermes-kanban
    board: jjukkumi
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

`board` names the Hermes kanban board the route submits to. The operator creates it once with the public `hermes kanban boards create <slug>` command; JJUKKUMI never creates, renames, or deletes boards. `capability_report` is generated and verified by the E0-T4 compatibility task. It contains no secrets. Exact command mapping is compiled into or versioned with the adapter after verification; it is not supplied by untrusted route data. `required_capabilities` names the sink capabilities the route depends on; key-based reconciliation on Hermes runs through the idempotent dedup submission (the capability report records `lookup_by_idempotency_key` as the dedup behavior), so routes that reconcile by reference require `lookup_by_external_ref`.

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

Webhook targets are explicit targets and never fallback targets. The endpoint must be an `https` URL; redirects are never followed (a redirecting endpoint is a definite routing rejection). `auth.type` is `bearer` (Authorization: Bearer) or `header` (a custom `header_name` carrying the secret); `auth.header_name` is required for `header` and must be empty for `bearer`. The secret reference resolves immediately before each submission and never enters SQLite or logs (SEC-006). `idempotency_header` defaults to `Idempotency-Key`; the core's idempotency key is transmitted verbatim so the same dispatch retry presents the same key (WHK-005). `submit_timeout` defaults to 30s. The optional `required_capabilities` gates against the adapter's static declaration (HER-005): the webhook declares `durable_acceptance` false — a 2xx is transport acceptance only — so requiring it fails validation.

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
      trigger_name: jjukkumi.wiki-maintenance.4f8c21
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
- Pattern behavior must be identical on macOS and Linux for the same normalized path.
- Case sensitivity follows the configured policy, not an accidental host filesystem behavior. v0.1 default is `filesystem`, and the resolved behavior is recorded in the route revision.

## 8. Policy Actions

Allowed actions:

| Action | Meaning |
|---|---|
| `quarantine` | Persist and require explicit operator resolution. |
| `reconcile` | Merge one full current-state reconciliation generation. |
| `drop` | Only permitted for explicitly ignorable structural cases, never overflow. |

Protected and unsafe paths default to quarantine. Overflow and fresh instance default to reconcile.

## 9. Retry and Failure Budget Configuration

Submission retry applies only to attempts to deliver the same dispatch intent. It does not control Hermes execution retries.

Validation rules:

- `max_attempts` from 1 to 10;
- positive initial and maximum backoff;
- maximum >= initial;
- multiplier >= 1.0;
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

`forever` is valid only for unresolved classes. Pruning never deletes a parent still referenced by an unresolved child.

## 11. Secret References

Syntax:

```text
env:VARIABLE_NAME
file:/absolute/owner-readable/path
keychain:<provider-specific-reference>
fd:<positive-integer>
```

The config loader parses the reference but resolves its value only immediately before use. JSON display redacts the resolved value and may display the reference identifier.

## 12. Semantic Validation

Beyond schema validation, the validator must check:

- all route references exist;
- roots do not overlap in unsupported ways;
- state directory is local and outside governed roots by default;
- target capability report matches the installed target version;
- route-required capabilities are available;
- profile, skills, mutex, and target are operator-owned fixed values;
- all durations and sizes are bounded;
- `enabled: true` only permits activation; SQLite must also contain an explicit operator acknowledgement for the computed route revision;
- webhook target is not configured as an automatic fallback;
- configuration revision changes when behavior-affecting fields change.

## 13. Computed Configuration Revision

The operator does not manually enter a route revision. JJUKKUMI computes it from normalized behavior-affecting configuration. Canonical route revision input includes source binding, resource ID, normalized patterns, batch limits, policy actions, target ID, profile, skills, mutex, latest-state flag, submission retry, execution hints, failure budget, and capability requirements.

It excludes comments, display order, state directory, the command-line log level, and resolved secret values.

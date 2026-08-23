# Error Model and Exit Codes

## 1. Error JSON

```json
{
  "api_version": "agent-dispatch.cli/v1",
  "command": "dispatch",
  "ok": false,
  "error": {
    "code": "target_acceptance_unknown",
    "category": "acceptance_unknown",
    "message": "Hermes may have accepted the task; reconciliation is required.",
    "retryable": false,
    "dispatch_id": "019c...",
    "remediation": "Run 'agent-dispatch dispatches show ...' and reconcile by idempotency key."
  },
  "trace_id": "..."
}
```

Messages are safe for humans. Machine behavior uses `code`, `category`, and persisted state.

## 2. Exit Codes

| Code | Class | Meaning |
|---:|---|---|
| 0 | success | Command completed, including deterministic no-op/drop. |
| 2 | usage | Invalid command or flags. |
| 3 | configuration | Config/schema/route/capability validation failed. |
| 4 | input rejected | Malformed or unsafe source input; no ambiguous side effect. |
| 5 | quarantined | Reserved for a future policy that durably stores structural evidence before holding it; the delivered structural holds are successful exit-0 outcomes with a disposition envelope. |
| 10 | transient local | Local lock, temporary filesystem, or definite pre-submit transient failure. |
| 11 | target unavailable | Target definitely unavailable before possible acceptance; retry scheduled or possible. |
| 12 | target rejected | Target definitely rejected the request. |
| 13 | acceptance unknown | Target may have accepted; local state records `unknown`. |
| 14 | conflict | Active route, lease, or state transition conflict prevented requested action. |
| 20 | storage | SQLite open, write, integrity, or local durability failure. |
| 21 | migration | Unsupported or failed schema migration. |
| 30 | security | Containment, secret, permissions, or trust-policy violation. |
| 40 | internal | Invariant violation or unclassified implementation defect. |

Exit code 1 is intentionally unassigned and must never be emitted; unclassified failures use exit 40. The CLI entry point must recover panics and exit 40, because an unrecovered Go runtime panic exits with status 2 and would be indistinguishable from the usage class.

A Watchman trigger invocation may receive nonzero status, but durable state and logs remain the source of truth. Exit 13 must never cause the caller to submit through another sink.

## 3. Error Categories

`category` enumerates exactly one value per nonzero exit-code class. Category and exit code determine each other; implementations derive one from the other rather than assigning them independently.

| Category | Exit code |
|---|---:|
| `usage` | 2 |
| `configuration` | 3 |
| `input_rejected` | 4 |
| `quarantined` | 5 |
| `transient_local` | 10 |
| `target_unavailable` | 11 |
| `target_rejected` | 12 |
| `acceptance_unknown` | 13 |
| `conflict` | 14 |
| `storage` | 20 |
| `migration` | 21 |
| `security` | 30 |
| `internal` | 40 |

The success class has no error category; a deterministic no-op, drop, or overflow-to-reconciliation conversion is `ok: true` with reason codes, not an error.

## 4. Stable Error Code Registry

The v0.1 registry is closed: implementations emit only the codes below. Adding or changing a code is a contract change recorded in the changelog and this table.

| Code | Category | Exit |
|---|---|---:|
| `command_unknown` | `usage` | 2 |
| `command_not_implemented` | `usage` | 2 |
| `flag_invalid` | `usage` | 2 |
| `internal_unclassified` | `internal` | 40 |
| `config_invalid` | `configuration` | 3 |
| `config_route_not_found` | `configuration` | 3 |
| `config_capability_missing` | `configuration` | 3 |
| `lookup_unsupported` | `configuration` | 3 |
| `capability_unsupported` | `configuration` | 3 |
| `state_directory_not_local` | `configuration` | 3 |
| `source_input_too_large` | `input_rejected` | 4 |
| `source_malformed_json` | `input_rejected` | 4 |
| `source_binding_mismatch` | `input_rejected` | 4 |
| `source_unsupported_version` | `input_rejected` | 4 |
| `source_missing_required_metadata` | `input_rejected` | 4 |
| `source_position_unusable` | `input_rejected` | 4 |
| `source_overflow_reconciliation` | `input_rejected` | 4 |
| `path_absolute_rejected` | `input_rejected` | 4 |
| `file_too_large` | `input_rejected` | 4 |
| `work_receipt_invalid` | `input_rejected` | 4 |
| `unsafe_path_quarantined` | `quarantined` | 5 |
| `sqlite_busy` | `transient_local` | 10 |
| `sqlite_query_failed` | `storage` | 20 |
| `hermes_executable_missing` | `target_unavailable` | 11 |
| `hermes_version_unsupported` | `target_unavailable` | 11 |
| `watchman_unavailable` | `target_unavailable` | 11 |
| `watchman_version_unsupported` | `target_unavailable` | 11 |
| `target_definite_unavailable` | `target_unavailable` | 11 |
| `target_rejected` | `target_rejected` | 12 |
| `target_acceptance_unknown` | `acceptance_unknown` | 13 |
| `target_response_invalid` | `acceptance_unknown` | 13 |
| `watchman_trigger_conflict` | `conflict` | 14 |
| `dispatch_duplicate` | `conflict` | 14 |
| `route_slot_held` | `conflict` | 14 |
| `route_not_registered` | `conflict` | 14 |
| `dispatch_not_found` |input_rejected| 4 |
| `batch_not_found` | `input_rejected` | 4 |
| `receipt_not_found` |input_rejected| 4 |
| `quarantine_not_found` | `input_rejected` | 4 |
| `transition_invalid` | `conflict` | 14 |
| `attempt_lease_conflict` | `conflict` | 14 |
| `dispatch_dead_lettered` | `conflict` | 14 |
| `quarantine_release_denied` | `conflict` | 14 |
| `retention_reference_conflict` | `conflict` | 14 |
| `maintenance_active_work` | `conflict` | 14 |
| `backup_target_exists` | `conflict` | 14 |
| `doctor_findings_present` | `configuration` | 3 |
| `sqlite_open_failed` | `storage` | 20 |
| `sqlite_integrity_failed` | `storage` | 20 |
| `migration_newer_schema` | `migration` | 21 |
| `path_traversal_rejected` | `security` | 30 |
| `path_symlink_escape` | `security` | 30 |
| `source_unsafe_path` | `security` | 30 |

Boundary notes:

- `path_absolute_rejected` is malformed input (class 4); an absolute path is structurally invalid. `path_traversal_rejected`, `path_symlink_escape`, and `source_unsafe_path` are containment violations (class 30) even though no side effect occurred.
- `source_unsafe_path` is the containment rejection used today: the invocation is refused before observation commit (exit 30) because no safe evidence can be stored. The class-5 code `unsafe_path_quarantined` (and the reserved names `batch_hard_limit`, `protected_path_quarantined`) apply only to a future policy that durably stores structural evidence before holding it; the delivered structural holds (protected, bulk) are successful trigger outcomes — exit 0 with an explicit `disposition: quarantine` envelope — so a Watchman trigger never retries a durably held case.
- `target_response_invalid` maps to `acceptance_unknown` because DUR-005 requires an invalid response after possible submission to enter `unknown`, never `failed`.
- `source_overflow_reconciliation` applies only when a command context cannot perform the overflow-to-reconciliation conversion; a successful conversion is exit 0 with reason codes.

## 5. Retryability

`retryable` in an immediate error response is advisory. Persisted dispatch state controls actual retry behavior.

- unknown acceptance is not immediately retryable;
- definite pre-submit transient failure may be retryable;
- invalid config is not retryable until config changes;
- quarantine is not automatic-retryable;
- SQLite busy may be retried within bounded command policy;
- invariant violations are never auto-retried.

## 6. Redaction

Errors may include relative path, resource ID, route ID, and target ID. They must not include note body, resolved secret, authorization header, or unrestricted subprocess output.

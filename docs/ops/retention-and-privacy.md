# Retention and Privacy

This guide is for the operator responsible for local macOS arm64 state and
backups. Inspect retention settings and unresolved lineage before removal;
pruning needs the state owner's authority and a verified backup. The normative
policy belongs to the specifications and configuration contract.

## 1. Data Minimization

Agent Dispatch stores operational evidence, not knowledge content.

Stored by default:

- relative paths;
- operations;
- SHA-256 digests;
- source positions and bounded flags;
- route/resource/target IDs and revisions;
- decisions and reason codes;
- dispatch attempts and states;
- external task references;
- work receipt path/digest manifests;
- audit actors and timestamps.

Not stored by default:

- note bodies;
- front matter content;
- link target text beyond path evidence;
- agent prompt/response bodies;
- chain-of-thought;
- resolved secrets;
- unrestricted Hermes stdout/stderr.

## 2. Default Retention

| Data | Default |
|---|---:|
| Resolved observations and change items | 30 days |
| Completed/rejected attempts | 30 days |
| Completed acceptance and work receipts | 180 days |
| Resolved quarantine | 180 days |
| Unknown, ready, retrying, active, quarantined, dead-lettered | Until resolved |
| State transition audit required by unresolved lineage | Until lineage resolves |

## 3. Pruning Rules

- dry-run by default;
- delete children before parents;
- never orphan a dispatch, receipt, quarantine, or state transition;
- never delete the only evidence needed to reconcile unknown acceptance;
- preserve summary audit rows if detailed resolved payload is compacted;
- record prune actor, policy revision, counts, and time;
- support a configured legal/operational hold in future without changing ordinary retention semantics.

## 4. Relative Path Privacy

Relative paths may reveal note titles. The instance may set `log_paths: redacted`, causing logs to replace path-shaped values with a stable digest while SQLite retains the relative paths needed for operation. The `log_paths` policy is instance-level in v0.1. A future encrypted-state feature requires a separate ADR.

## 5. Database Protection

- owner-only permissions by default;
- state outside shared cloud-sync folders and watched vault;
- local filesystem only;
- backups protected at least as strongly as the live database;
- support bundles redacted.

## 6. Secret Lifetime

Secret reference is stored; value is resolved immediately before call, held only in memory, and cleared by normal process exit. Go cannot guarantee immediate memory zeroization; therefore avoid long-lived processes and avoid copying secret strings unnecessarily.

## 7. User-Controlled Erasure

An explicit purge operation may be added after v0.1. Until then, operators may prune resolved data through supported commands and delete the entire retained state only after disabling triggers, backing up if needed, and accepting loss of dedup/reconciliation history.

## 8. Safe Retention Procedure

1. Inspect `agent-dispatch status` and `doctor` for active or unresolved work.
2. Stop competing maintenance and back up the database and matching configuration
   using [installation §6](installation.md#6-backup).
3. Run `agent-dispatch maintenance prune --before <duration> --dry-run` for the
   reviewed age (for example, `30d`), which narrows the retention horizons; inspect the selected counts and retained unresolved lineage.
4. Only after approving the result, repeat with `--yes` instead of `--dry-run`.
5. Run `agent-dispatch maintenance integrity --full` and inspect status/audit
   results. Confirm unresolved dispatches and receipts still have their lineage.

Pruning is irreversible without a backup. If it fails or deletes unexpected data,
stop further maintenance and submissions, preserve the failed state and evidence,
and restore only through the verified backup procedure. The installation owner
approves retention and restoration; maintainers handle unexpected selection,
referential-integrity failures, or suspected disclosure. Share redacted metadata,
not sensitive path lists or secret-bearing output.

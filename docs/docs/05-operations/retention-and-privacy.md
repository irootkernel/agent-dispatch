# Retention and Privacy

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

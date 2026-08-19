# Watchman Integration

## 1. Role

Watchman is the authoritative filesystem event source for v0.1. JJUKKUMI does not replace Watchman and does not run a duplicate long-lived file watcher.

```text
Watchman daemon -> settled trigger invocation -> short-lived JJUKKUMI process
```

## 2. Invocation Contract

The installed trigger invokes:

```text
jjukkumi dispatch --route wiki-maintenance --input watchman --output json
```

Watchman supplies JSON on standard input. JJUKKUMI treats selected environment variables as source metadata only after verifying the configured trigger binding.

Fields to capture when present:

- `WATCHMAN_TRIGGER`
- `WATCHMAN_ROOT`
- `WATCHMAN_RELATIVE_ROOT`
- `WATCHMAN_SINCE`
- `WATCHMAN_CLOCK`
- `WATCHMAN_FILES_OVERFLOW`

The adapter must tolerate absent optional fields in fixtures, but production validation may require the fields established by `E2-T5` against the installed Watchman version.

## 3. Trigger Installation

JJUKKUMI owns a stable trigger name, for example:

```text
jjukkumi.<route-id>.<short-config-digest>
```

`jjukkumi watchman install --route <id>` must:

1. verify Watchman availability;
2. resolve and validate the resource root;
3. inspect an existing trigger with the same name;
4. make no change if the normalized definition is identical;
5. require `--replace` if a different definition exists;
6. print the exact installed definition in JSON mode;
7. never reinstall on every ordinary dispatch.

Repeated destructive registration can change incremental behavior and is therefore prohibited.

## 4. Expression Scope

The Watchman expression should reduce noise, but JJUKKUMI remains responsible for final policy. A typical expression selects files under the vault and requests fields sufficient to determine path, existence, type, and change status.

The exact expression and field list must be verified during `E2-T5`. Examples are non-authoritative until that task completes.

## 5. No Second Settle Window

Watchman trigger mode is already settle-aware. JJUKKUMI must not sleep for a configurable `settle_seconds` in the one-shot path. The trigger payload is one source batch.

Later daemon-based subscriptions may introduce explicit coalescing windows under a separate ADR.

## 6. Operations

The canonical v0.1 change operations are:

- `create`: included path exists now and was reported as new;
- `modify`: included path exists now and is not new;
- `delete`: included path no longer exists.

Rename pairing is optional evidence. A note move may appear as delete plus create and still correctly causes Hermes latest-state reconciliation.

## 7. Overflow, Fresh Instance, and Recrawl

The adapter must conservatively identify incomplete incremental evidence.

| Condition | Action |
|---|---|
| `WATCHMAN_FILES_OVERFLOW=true` | Persist source observation; do not dispatch partial manifest; merge one reconciliation generation. |
| Fresh instance semantics | Same as overflow. |
| Missing or unusable previous position | Same as overflow unless first-install baseline policy explicitly says otherwise. |
| Recrawl warning or evidence | Mark source uncertainty and reconcile. |
| First installation | Default to one explicit initial full reconciliation, not thousands of ordinary tasks. |

## 8. Source Event Key

The source event key should be derived from stable trigger identity plus source position when available:

```text
watchman:<source-id>:<since>:<clock>:<payload-digest>
```

This key recognizes retransmission of the same source batch. It is not the observation ID and not the content fingerprint.

If a trustworthy source position is absent, leave `source_event_key` null and rely on content/idempotency controls without claiming source-level deduplication.

## 9. Safe Path Processing

1. Treat Watchman path strings as untrusted.
2. Require relative paths.
3. Normalize separators without resolving an attacker-selected absolute target.
4. join against the trusted root;
5. use descriptor-relative or equivalent checks to prevent symlink escape before reading;
6. apply include/exclude/protected rules to normalized relative paths;
7. hash only regular files within size limits;
8. never follow symlinked directories outside the vault.

## 10. Baseline and Reconciliation

`jjukkumi reconcile --route <id>` enumerates the current configured Markdown scope safely, compares it with persisted path facts, and creates one reconciliation batch. It does not synthesize one source observation per file and does not invoke Watchman re-registration.

Daily reconciliation is scheduled externally through `launchd`, `systemd --user`, cron, or another operator-owned scheduler. The schedule invokes the CLI and does not require a JJUKKUMI daemon.

## 11. Test Fixtures

Fixtures must cover:

- first trigger after installation;
- create, modify, and delete;
- Obsidian atomic-save patterns;
- repeated same-digest save;
- replacement file;
- overflow;
- fresh instance;
- bulk copy;
- empty file list;
- malformed JSON;
- path traversal;
- symlink escape;
- non-UTF-8 or invalid path representation supported by the platform abstraction;
- concurrent trigger processes.

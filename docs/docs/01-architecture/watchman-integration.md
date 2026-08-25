# Watchman Integration

## 1. Role

Watchman is the authoritative filesystem event source for v0.1. Agent Dispatch does not replace Watchman and does not run a duplicate long-lived file watcher.

```text
Watchman daemon -> settled trigger invocation -> short-lived Agent Dispatch process
```

## 2. Invocation Contract

The installed trigger invokes:

```text
agent-dispatch dispatch --route wiki-maintenance --input watchman --output json
```

Watchman supplies JSON on standard input. Agent Dispatch treats selected environment variables as source metadata only after verifying the configured trigger binding.

Fields to capture when present (the verified allowlist; `WATCHMAN_FILES_OVERFLOW` was refuted by the E0-T5 probe, 0/23 invocations — see `docs/integrations/watchman-public-interface-report.md` §3):

- `WATCHMAN_TRIGGER`
- `WATCHMAN_ROOT`
- `WATCHMAN_RELATIVE_ROOT`
- `WATCHMAN_SINCE`
- `WATCHMAN_CLOCK`
- `WATCHMAN_SOCK` (diagnostics only; never part of the source model)

The adapter must tolerate absent optional fields in fixtures, but production validation may require the fields established by `E2-T5` against the installed Watchman version.

## 3. Trigger Installation

Agent Dispatch owns a stable trigger name, for example:

```text
agent-dispatch.<route-id>.<short-config-digest>
```

`agent-dispatch watchman install --route <id>` must:

1. verify Watchman availability;
2. resolve and validate the resource root;
3. inspect an existing trigger with the same name;
4. make no change if the normalized definition is identical;
5. require `--replace` if a different definition exists;
6. print the exact installed definition in JSON mode;
7. never reinstall on every ordinary dispatch.

Repeated destructive registration can change incremental behavior and is therefore prohibited.

## 4. Expression Scope

The Watchman expression should reduce noise, but Agent Dispatch remains responsible for final policy. A typical expression selects files under the vault and requests fields sufficient to determine path, existence, type, and change status.

The exact expression and field list must be verified during `E2-T5`. Examples are non-authoritative until that task completes.

## 5. No Second Settle Window

Watchman trigger mode is already settle-aware. Agent Dispatch must not sleep for a configurable `settle_seconds` in the one-shot path. The trigger payload is one source batch.

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
| Overflow-class signal | Persist source observation; do not dispatch partial manifest; merge one reconciliation generation. The verified signal is the missing or unusable previous position itself (no `WATCHMAN_SINCE`) plus fresh-instance semantics — `WATCHMAN_FILES_OVERFLOW` was refuted by E0-T5 (0/23 invocations) and is never read. |
| Fresh instance semantics | Same as overflow-class signal. |
| Missing or unusable previous position | Same as overflow unless first-install baseline policy explicitly says otherwise. |
| Recrawl warning or evidence | Recorded exception (E8-T5, M-8): the production trigger path receives only the subscription payload and the trusted environment — the recrawl `warning` member lives on the query surface (`watchman status`/`query-recrawl-warning.json`) which no production flow runs, so a live recrawl warning is never observed. Recrawl aftermath reaches the product exactly through the detected signals: a recrawl resets the clock and the resulting missing/unusable position is overflow-class (marked source uncertainty and one reconciliation generation), and the spurious re-delivery of an unchanged path is suppressed by the durable path facts. The E0-T5 fixture remains the frozen evidence of both behaviors. |
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

`agent-dispatch reconcile --route <id>` enumerates the current configured Markdown scope safely, compares it with persisted path facts, and creates one reconciliation batch. It does not synthesize one source observation per file and does not invoke Watchman re-registration.

Daily reconciliation is scheduled externally through `launchd` or another operator-owned macOS scheduler (D-023: darwin/arm64 is the only supported platform). The schedule invokes the CLI and does not require a Agent Dispatch daemon.

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

## 10. v0.1.5 Effective Binding Target

E10 replaces exact-root assumptions with the four-part managed binding defined
in ADR-0018's companion design: configured root, actual Watchman root,
configured-root-relative `relative_root`, and stable trigger name. Installation
persists the binding. Status reports it and compares it with current Watchman
state; test uses its logical root; remove searches stored and current watch
roots and succeeds only after the exact managed trigger is absent everywhere.

The trigger is subtree-constrained before input reaches the adapter. The
adapter still validates `WATCHMAN_ROOT` plus `WATCHMAN_RELATIVE_ROOT` against
the configured root, so a forged or drifted environment fails closed. Route
patterns remain configured-root-relative and exclusions run before every read
or downstream record. See SRC-009 through SRC-012 and AC-601 through AC-603.

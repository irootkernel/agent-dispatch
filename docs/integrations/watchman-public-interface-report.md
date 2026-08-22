# Watchman Public Interface Report

> **Task:** E0-T5, Verify Watchman Public Interface and Freeze Fixture Baseline
> **Probed:** 2026-08-19 (KST) on macOS 26.5.2 (25F84), arm64
> **Watchman version:** `2026.07.27.00` (Homebrew `watchman 2026.07.27.00_1`)
> **Fixtures:** [`fixtures/watchman/`](fixtures/watchman/) (33 files)
> **Baseline decision:** **Supported and frozen.** The trigger contract required by `SRC-001` through `SRC-003` and `SRC-008` is implementable on the public interface of this version, with one assumed field refuted and its conservative replacement verified; see §10.

## 1. Method and Boundary

Every claim below was produced by invoking only public Watchman CLI commands
against a disposable watch root `/tmp/agent-dispatch-e0t5-probe/vault` (macOS
canonical form `/private/tmp/agent-dispatch-e0t5-probe/vault`), created for this
probe and deleted afterwards together with its watch and triggers. Trigger
executions were captured by a probe command that recorded its own
environment, argv, working directory, and standard-input bytes to files
stored outside the watched root; the stdin bodies are frozen verbatim as
`trigger-payload-*.json`. Watchman internal state files were never opened.
One assumption from the SOT (`WATCHMAN_FILES_OVERFLOW`) was disproved and is
reported as refuted rather than silently dropped. Everything in this report
is `runtime-verified`; the only `help-verified` content is the verbatim
`watchman -h` surface in `fixtures/watchman/help-output.txt`.

## 2. Version and Interface Discovery

- `watchman version` prints `{"version": "2026.07.27.00"}` and exits 0;
  `watchman get-sockname` discloses the per-user state directory layout
  (sock, state, log, pid under `~/.local/state/watchman/<user>-state/`)
  (fixture `fixtures/watchman/version-output.txt`).
- The client accepts a single JSON command on standard input via `-j`, but
  only in array form `["command", <args>...]`; a JSON object is rejected by
  client-side validation with exit 1 (fixture
  `fixtures/watchman/error-cases.txt`).
- `watchman list-capabilities` returns 109 capability strings, including the
  whole `field-*` namespace used by query and trigger stdin field lists
  (`field-name`, `field-exists`, `field-new`, `field-type`, `field-size`,
  `field-mode`, and more), the `term-*` expression namespace
  (`term-type`, `term-match`, `term-allof`, `term-anyof`, `term-suffix`,
  ...), `wildmatch`, `relative_root`, `dedup_results`, and `cmd-trigger`,
  `cmd-trigger-list`, `cmd-trigger-del`, `cmd-query`, `cmd-since`
  (fixture `fixtures/watchman/list-capabilities.json`).
- Consequence for E2-T5 and E6-T3: the managed trigger installer must
  define triggers through the `-j` array interface and gate on version
  `2026.07.27.00` plus the capability strings it actually uses.

## 3. Trigger Invocation Contract (SRC-001, SRC-002)

A trigger defined with `"stdin": [<fields>]`, `"append_files": false`, and an
expression invokes its command with the matching changed files as a JSON
array on standard input. Verified across 23 captured invocations covering first run,
incremental runs, concurrent runs, relative-root runs, and post-recrawl runs
(fixture `fixtures/watchman/trigger-invocation-environment.txt`):

| SOT-assumed field (architecture §2) | Verified reality | Evidence |
|---|---|---|
| `WATCHMAN_TRIGGER` | Always set | runtime, 23/23 invocations |
| `WATCHMAN_ROOT` | Always set, canonical absolute watch root | runtime |
| `WATCHMAN_RELATIVE_ROOT` | Set (absolute subdirectory path) only for triggers defined with `relative_root` | runtime |
| `WATCHMAN_SINCE` | Set on every invocation except the first after definition, definition replacement, or watch re-creation; equals the previous invocation's `WATCHMAN_CLOCK` | runtime |
| `WATCHMAN_CLOCK` | Always set, current batch position | runtime |
| `WATCHMAN_FILES_OVERFLOW` | **Never set**; no overflow indicator of any name appeared in any invocation, including immediately after a forced recrawl | runtime, 0/23 |

Additional verified facts the parser and installer need:

- `WATCHMAN_SOCK` is also set (not in the SOT list), pointing at the user's
  server socket.
- Working directory is the watch root (the `relative_root` subdirectory for
  a relative-root trigger); with `append_files: false` the command receives
  zero file arguments.
- The stdin payload is a bare JSON array of objects with exactly the fields
  requested in the definition's `stdin` list, in request order
  (`name`, `exists`, `new`, `size`, `type` verified; the definition-time
  default set is `name`, `exists`, `new`, `size`, `mode`).
- The trigger payload carries no route, resource, or target information of
  any kind, so route binding authority remains entirely with trusted
  configuration plus the trigger name and root environment (SRC-003 is
  satisfiable exactly as designed; the payload cannot select a route).

## 4. Change-Type Payload Semantics (TST-003)

All payloads below are verbatim trigger stdin bodies; watchman performs no
same-content suppression and no ordering guarantee.

| Change scenario | Delivered payload shape | Fixture |
|---|---|---|
| Create | `{name, exists: true, new: true, size, type: "f"}` | `trigger-payload-create.json` |
| Modify | same path, `new: false` | `trigger-payload-modify.json` |
| Delete | `exists: false`, `size` is the prior size, path still reported | `trigger-payload-delete.json` |
| Atomic save (write temp, rename onto target) | one entry for the final path only; the temp name never appears | `trigger-payload-atomic-save.json` |
| Repeated same-digest saves (3 writes, 0.5 s apart) | three separate invocations; identical content is still delivered every time | `trigger-payload-repeated-save-{1,2,3}.json` |
| Replacement (rename another file over an existing path) | source path `exists: false` plus target path `exists: true, new: false` in one batch | `trigger-payload-replacement.json` (source creation in `-source-create.json`) |
| Bulk copy (60 files at once) | one invocation, 60 entries, arbitrary non-sorted order | `trigger-payload-bulk-60.json` |
| First invocation after install, replacement, or re-watch | full matching file list, all entries `new: true`, no `WATCHMAN_SINCE` | `trigger-payload-first-full-list.json`, `trigger-payload-safe-expression-full-list.json` |
| Expression-filtered change | only matching entries; a non-matching change produces no invocation at all | `trigger-payload-expression-filtered.json` |
| Same change seen by two triggers | two concurrent processes (consecutive pids), one payload per trigger view | `trigger-payload-concurrent-main.json`, `trigger-payload-relative-root.json` |
| Recrawl aftermath | an unchanged existing symlink can be re-delivered as a spurious modify (`new: false`) | `trigger-payload-recrawl-spurious-modify.json` |
| Ordinary create after recrawl | clean incremental payload, environment indistinguishable from normal | `trigger-payload-post-recrawl-create.json` |

Structural consequences for E2-T1 and E2-T3, all evidenced above: batch entry
order is not sorted (the planner must sort deterministically), key order in
JSON objects is not stable across responses, `size` on a delete entry is the
pre-deletion size (never open the file), and same-content modifies are not
suppressed by Watchman (digest comparison is Agent Dispatch's responsibility).

## 5. Expression and Ignore Behavior

- **No default ignore for dotfiles:** `.git/config` and `.DS_Store` created
  inside the root were reported through both an unfiltered query and a
  `["type","f"]` filtered query (fixtures
  `fixtures/watchman/query-unfiltered-entries.json`,
  `fixtures/watchman/query-no-default-ignore.txt`). Agent Dispatch must apply its
  own exclusion rules (E2-T2 default Obsidian exclusions).
- **Symlinks** are reported with `type: "l"` and `size` equal to the length
  of the target path string. A `["type","f"]` expression excludes them; a
  name-only expression like `["match","*.md"]` does not. The safe trigger
  expression is `["allof", ["type","f"], ["match","*.md"]]`; its full-list
  output contains no symlink entries
  (`trigger-payload-safe-expression-full-list.json`).
- **Directories** appear with `type: "d"` in unfiltered views.
- **Cookie files:** each query creates and deletes a transient
  `.watchman-cookie-<host>-<pid>-<seq>` file inside the watched root (also
  in subdirectories, observed once inside `.git/`), disclosed in the
  response `debug.cookie_files` member. Exclusion rules must cover this
  name pattern.
- **Empty match suppression:** when a change set matches no entry of the
  trigger expression, the trigger command is not executed at all (a
  `.txt`-only change under a `*.md` trigger produced zero invocations).
  An empty stdin array is therefore not an expected runtime input for
  E2-T1; it remains a synthetic malformed-input class only.
- Query field control is exact: requesting only `name` returns an array of
  plain strings rather than objects
  (`fixtures/watchman/query-fresh-instance-after-rewatch.json`). The parser
  must handle both shapes for query responses; the trigger stdin field set
  is always the requested list.

## 6. Position, Fresh Instance, Recrawl, and Overflow (SRC-002, TST-003)

- **Position chain:** `WATCHMAN_SINCE` of invocation N equals
  `WATCHMAN_CLOCK` of invocation N-1, giving the source event key
  `watchman:<source-id>:<since>:<clock>:<digest>` a real, verified basis.
- **First-position semantics:** the first invocation after trigger
  definition, after a definition replacement (`disposition: "replaced"`),
  and after watch re-creation carries no `WATCHMAN_SINCE` and delivers the
  full matching list. An identical definition re-install
  (`disposition: "already_defined"`) preserves the position and the next
  invocation stays incremental
  (`trigger-payload-after-idempotent-reinstall.json`), so an idempotent
  installer is a true no-op (E2-T5).
- **Unknown or malformed since clocks do not fail:** both a well-formed
  unknown clock and a garbage string were accepted and answered with a full
  `is_fresh_instance: true` enumeration, exit 0
  (`fixtures/watchman/query-invalid-clock-fresh-instance.txt`). Clocks are
  opaque position tokens and must never be locally parsed or validated.
- **Forced recrawl (`debug-recrawl`):** the cursor chain survived, but the
  next query response carried a `warning` member naming the recrawl with
  remediation text (`fixtures/watchman/query-recrawl-warning.json`), and the
  trigger re-delivered one unchanged symlink as a spurious modify. Recrawl
  evidence therefore arrives as (a) the query `warning` member and (b)
  possibly-spurious entries, not as a fresh-instance flag.
- **Watch deletion and re-creation:** `watch-del` removes the watch together
  with all triggers; after re-watching, `trigger-list` is empty and any old
  since clock yields `is_fresh_instance: true` with the full current tree
  (`fixtures/watchman/query-fresh-instance-after-rewatch.json`,
  `fixtures/watchman/watch-lifecycle.txt`).
- **Overflow:** a genuine kernel event-queue overflow was not artificially
  induced in this probe (no public command triggers a real overflow, and
  inducing one would require saturating kernel state). What is verified is
  stronger for the parser: **no overflow indicator exists anywhere in the
  trigger interface** of this version (no env variable, no payload field,
  0/23 invocations including post-recrawl). The SOT's
  `WATCHMAN_FILES_OVERFLOW` assumption is therefore **refuted**, and the
  architecture §7 fallback row is the operative rule: a missing or unusable
  previous position (absent `WATCHMAN_SINCE`) must be treated with the same
  conservative handling as overflow, which is exactly what the first-position
  semantics above provide. E2-T1 and E2-T5 must implement overflow-class
  fixtures as the verified fresh-instance and missing-position shapes, not
  as an environment variable that this Watchman never sets.

## 7. Trigger Lifecycle: Install, Status, Remove, Test (SRC-005 through SRC-007 context)

- **Install:** trigger definitions must be created through the `-j` array
  interface with an object spec (`name`, `command`, `append_files`,
  `stdin` field list, `expression`, optionally `relative_root`). The
  positional CLI form mis-parses flags and names without any error (fixture
  `fixtures/watchman/trigger-cli-positional-misparse.txt`); it must not be
  used.
- **Status:** `trigger-list <root>` returns the normalized definition of
  every trigger (stdin field list, append_files, expression, command,
  relative_root, name). This normalized form is the comparison basis for
  the install no-op check (fixture
  `fixtures/watchman/trigger-lifecycle.txt`).
- **Replace:** re-adding a name with a different definition reports
  `replaced` and resets the incremental position (first-run semantics
  return); re-adding the identical definition reports `already_defined`
  and changes nothing.
- **Remove:** `trigger-del` reports `deleted: true`, or `deleted: false`
  with exit 0 for a name that does not exist, so uninstall is idempotent.
  `watch-del` on the root deletes the watch and every trigger in it.
- **Test:** there is no trigger-specific test command; verification is a
  settled file change observed through the captured invocation, which this
  probe performed for every scenario in §4.
- **Concurrent invocations:** multiple triggers matching the same change run
  as concurrent processes (observed consecutive pids in the same second),
  and repeated bursts each produce their own invocation. The single-active-
  route and dirty-generation design of E3-T4, not Watchman, is the
  coalescing authority.

## 8. Absence and Failure Behavior (doctor input, E6-T2)

All fixtures in `fixtures/watchman/error-cases.txt`.

- **Exit codes lie for server-side errors:** a failed server-side command
  (unwatched root, invalid definition) prints a JSON response containing an
  `error` member with exit code 0. Only client-side validation failures
  (unparseable command, unresolvable nonexistent path) exit nonzero. The
  adapter and doctor must branch on the response `error` member first, exit
  code second.
- **Distinguished absence classes, all runtime-verified:** path does not
  exist (client-side, exit 1, `Could not resolve ... No such file or
  directory`); directory exists but is not watched (server-side, exit 0,
  `RootResolveError ... is not watched`); watched root deleted under us
  (same RootResolveError class); binary missing from PATH (shell `command
  not found`, exit 127).
- **Daemon reachability:** `version` is answered by the client without a
  daemon (a version ping cannot prove server health); daemon-requiring
  commands with an unreachable socket fail with `command ... not available
  in this mode` under `--no-spawn`, exit 1. Passing `--unix-listener-path`
  without `--no-spawn` spawns a second server sharing the per-user state
  file; Agent Dispatch must never pass a custom listener path when targeting the
  user's server (a stray server spawned during this probe was shut down
  through its own socket; see §11).

## 9. Parser Assumption Traceability (E2-T1)

Every parser-relevant assumption in the SOT maps to frozen evidence:

| Assumption (source) | Verdict | Fixture |
|---|---|---|
| Trigger JSON arrives as an array on stdin (SRC-001, source-adapter-contract §5) | confirmed | all `trigger-payload-*.json` |
| File objects carry `name`, `exists`, `new`, `type`, `size` (examples/watchman-trigger.json) | confirmed as a requestable exact field set; default set uses `mode` instead of `type` | §4 fixtures, `trigger-cli-positional-misparse.txt` |
| `create` = exists and new; `modify` = exists and not new; `delete` = not exists (architecture §6) | confirmed | `query-change-types.json`, §4 fixtures |
| Environment allowlist `WATCHMAN_{TRIGGER,ROOT,RELATIVE_ROOT,SINCE,CLOCK,FILES_OVERFLOW}` (architecture §2) | five of six confirmed; `WATCHMAN_FILES_OVERFLOW` refuted (§6) | `trigger-invocation-environment.txt` |
| Since/clock position available for the source event key (architecture §8) | confirmed for invocations 2+; first invocation has no position and must degrade per §8's null rule | `trigger-invocation-environment.txt` |
| Payload cannot select route or resource (SRC-003) | confirmed: no route authority exists in the payload | all payloads |
| Paths are vault-relative (source-adapter-contract) | confirmed (relative to the relative root when one is defined) | `trigger-payload-relative-root.json` |
| Deterministic fixture input without a daemon (SRC-008) | enabled: the frozen stdin bodies replay without Watchman | corpus as a whole |

TST-003 scenario coverage: create, modify, delete, atomic save, repeated
save, bulk copy, ignored files (no-default-ignore evidence), symlink
reporting, overflow-class (via verified fresh-instance and spurious-modify
shapes, §6), and concurrent processes are covered by real-payload fixtures.
Malformed JSON, path traversal, and non-UTF-8 names are parser attack
inputs, not observable Watchman outputs; E2-T1 derives them as synthetic
mutations of the frozen real shapes (for example by editing a frozen payload
name field), which is the traceability this baseline exists to provide.

## 10. Fixture Baseline Decision

**Decision: supported and frozen.** The verified public interface of
Watchman `2026.07.27.00` satisfies the source contract with one explicit
replacement: overflow is detected by missing-position and recrawl evidence,
never by an environment variable. The frozen corpus under
`fixtures/watchman/` is the authoritative shape source for E2-T1 parsing
and E2 fixture generation.

**Verified version range: `2026.07.27.00` exactly** (the only
runtime-verified version, Homebrew build `_1`, fsevents watcher on macOS).
Widening the range requires re-running this probe against each additional
version; an unverified version must fail route validation.

Known behavioral caveats carried forward:

1. `WATCHMAN_FILES_OVERFLOW` does not exist; first-position and recrawl
   semantics are the overflow signal (§6).
2. No default ignore for `.git` contents or `.DS_Store`; own exclusions
   required, including `.watchman-cookie-*` (§5).
3. Server-side failures can exit 0; branch on the `error` member (§8).
4. Batch order and JSON key order are unstable; sort deterministically
   (§4).
5. The positional `trigger`/`query` CLI forms silently mis-parse; only the
   `-j` array form is contract-grade (§2, §7).
6. Same-content writes are always re-delivered; digest suppression is local
   (§4).
7. A definition replacement resets incremental state; only the identical
   definition is a no-op (§7).

## 11. Boundary Confirmation

During this probe, Agent Dispatch work used only: `watchman version`,
`watchman -h`, `watchman get-sockname`, `watchman list-capabilities`,
`watchman watch-project`, `watchman watch-list`, `watchman clock`,
`watchman query` (positional and `-j` array forms), `watchman since`,
`watchman -j` trigger add/replace, `watchman trigger-list`,
`watchman trigger-del`, `watchman debug-recrawl`,
`watchman debug-show-cursors`, `watchman watch-del`, and the documented
failure-path invocations of §8 (`--no-spawn`, `--unix-listener-path`,
PATH-absent shell). No Watchman state file, socket, or internal data
structure was opened directly. The disposable root, its watch, and its
triggers were deleted after evidence capture (`watch-del` plus directory
removal), leaving `watch-list` empty. One extra server process was
accidentally spawned by the `--unix-listener-path` failure-path probe and
was immediately shut down through that same socket path; the user's primary
server was never shut down, and `watch-del-all` and `shutdown-server`
against the primary were never issued.

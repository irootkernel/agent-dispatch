# v0.1.6 Cold Validation Evidence (E17-T2, AC-1302)

> **Status:** Verified 2026-09-02 (E17-T2)
> **Binaries under test:** the reviewed build of this task's tree
> (schema range 1-20) driven end to end through the public CLI.
> **Real dependencies:** the installed `Hermes Agent v0.20.5 (2026.8.19)`
> public CLI (unmodified, BND-003), the installed Watchman
> `2026.07.27.00`, and the host's real launchd session — all against
> fully disposable state: a throwaway `$HOME` (own `~/.hermes` and
> capability cache), a disposable board `agent-dispatch-e17t2` created
> and deleted only through the public CLI, a disposable
> `wiki-maintainer` profile with the bundled `llm-wiki` skill, a
> disposable vault, a disposable state directory, and a managed launchd
> schedule whose label derives from the disposable configuration's
> digest. No production route, board, profile, vault, or schedule was
> touched; no worker executed (task execution requires operator
> credentials and stays outside the gate); the webhook sink's failure
> legs used a non-routable address that never leaves the host.
>
> The deterministic legs of every G10-G12 criterion are the focused
> suites the gates already name (VALIDATION.md); this document records
> the REAL-environment legs AC-1302 adds, the three real defects the
> walkthrough exposed and closed, and the known limitations record.

## 1. Clean-host guided setup and post-install rerun (AC-1003/AC-1005)

`config validate --probe-targets` against the real installation: the
hermes target reports `0.20.5 / available`, Watchman reports
`2026.07.27.00 / available`. `setup wiki --route wiki` then walks the
six steps on the real surfaces: the probe certifies
`agent-dispatch-group-enforced` (the only missing create flag is
exactly `--mutex-key`; the two-assignee board answers the shape
probes), the destination preflight passes profile-on-disk, skills,
workspace, and the cross-group acknowledgement, the Watchman check
reports the clean host (`state: missing`, `watch_root_state:
not_watched`), the disabled baseline commits one observation-fenced
snapshot (1 fact), and the five-state production-gate summary prints
the exact enable command without executing it. `watchman install`
creates the real trigger (actual watch root `/private/tmp/...`,
relative root `.`, `disposition: created`) and `watchman test` drives
the real trigger surface. The setup RERUN converges: the summary now
reads `Watchman binding: installed (trigger agent-dispatch.wiki.e17t2)`
beside the same disabled gate states and the same printed enable
command, with the baseline refreshed idempotently (observation revision
advanced, snapshot digest unchanged).

## 2. Managed launchd schedule lifecycle (AC-1210, real launchd)

`schedule install` bootstraps the managed plist into the real user
session; `schedule inspect` reports `present/loaded/definition_matches/
healthy` all true and `plutil -lint` accepts the rendered definition;
`launchctl kickstart` fires the internal `schedule run` entrypoint for
real (the rotated log pair appears under the state directory's `logs/`,
empty — the healthy pass is silent). `schedule install --at 05:45`
followed by a FLAGLESS `schedule inspect` stays healthy — the durable
override (migration v20) reproduces the installed timing, where the
pre-fix behavior read the override as permanent drift and blocked
enablement. `route enable` passes the automatic-mode schedule gate on
the real session, and a flagless install over the override refuses at
exit 14 as a different definition (uninstall first). `schedule disable`
boots the loaded job out preserving the plist
(`present=true loaded=false`); `schedule uninstall` removes exactly the
managed plist.

## 3. Real submissions, completion, outbox, automatic delivery

`reconcile --submit` delivered a real task onto the disposable board
(`submitted_state: accepted`); the public `kanban list --json` read-back
shows five tasks created through the public CLI across the
walkthrough, each carrying the trusted instruction block, the untrusted
manifest boundary, and **no `mutex_key` field at all** (HER-021). `work
begin` / `work complete` recorded the cooperative receipts; the
completion notification entered the outbox and the AFTER-COMMAND pass
delivered it silently through the log sink (state `delivered`, one
attempt, resolved) while the core command's stdout, JSON, and exit
stayed unchanged. `status` projects the delivery posture
(`OPS-017`/AC-1206): due/backoff split, the oldest pending age, repeated
retry outcomes, the scheduler expectation — and the LATEST DRAIN
EVIDENCE read from the durable drain-run rows (trigger, mode, timing,
`claimed/delivered/retry-scheduled`, budget state), the surface the
E16 audit's F001 found missing.

## 4. Timeout, budget expiry, retry, and crash recovery (AC-1202/AC-1204)

With a second sink declared as an HTTPS webhook to an unreachable
endpoint, the failure legs ran against real transports:

- **Dual-sink fan-out**: a cooperative `work fail` created one
  notification per sink; the log sink delivered automatically while the
  webhook recorded a `retryable` outcome and stayed pending under its
  stable identity with a persisted backoff deadline (`backoff: 1`,
  `repeated_retry_outcomes: 1` in the posture).
- **Ten-second wall budget**: with the webhook repointed at a
  non-routable address, `work complete` exited successfully after
  exactly 10.0s — the invocation-wide budget expired mid-webhook, the
  unstarted claims were released, the new pair stayed pending with zero
  attempts, and the command's exit contract was preserved.
- **Transport timeout**: the manual drain's webhook attempt hit the
  sink's request bound (15.0s pass) and recorded `ambiguous` — the
  at-least-once posture: a timed-out request may have been delivered,
  so the record stays pending under backoff rather than assuming
  refusal.
- **The operator bypass**: `notifications retry <id>` re-armed the
  retryable record to immediately-due and performed its one attempt
  under the SAME idempotency identity (exit 0, outcome data), the
  attempt claiming its own lease fence.
- **Crash recovery**: a drain process was `kill -9`-ed mid-attempt
  inside the timeout window; the durable row shows the stranded lease
  (`lease_owner: drain-<pid>-…`, expiry set, zero attempts recorded).
  After expiry the next drain reclaimed the record through the fencing
  token — the dead owner can never record — attempted once
  (`ambiguous`), released the lease, and persisted the fresh backoff
  deadline. The drain report carries `claimed/delivered/ambiguous` and
  the post-pass pending truth.

## 5. Serialization groups under the real target (AC-1102 through AC-1104)

With the `wiki-publish` group held by the accepted burst child (the
durable group table names exactly one holder), a further arrival on the
holding route produced no second child — the work folded into the
pending generation (`disposition: reconcile`), the shared-group
exclusion the G11 walkthrough demonstrated at depth. A second route
acknowledging cross-group concurrency was enabled over the same real
target and its `side-publish` generation submitted immediately:
the durable table then shows BOTH groups `HELD` concurrently
(`side-publish|HELD|side|index` beside `wiki-publish|HELD|wiki|main`)
— acknowledged independent groups progress in parallel without any
global single-writer claim.

## 6. Defects the real-environment rerun exposed and closed

The G11 precedent predicted real defects; this walkthrough found three,
each fixed and pinned in this task:

1. **`launchctl print` target form (inspect/posture loaded
   false-positive).** The loaded check passed the domain and label as
   separate argv entries; real launchd then prints the whole DOMAIN
   dump (no "Could not find service" line, exit 0), so an absent
   schedule read `loaded: true`. The same wrong form made
   `schedule disable`'s bootout fail with error 5. Both now use the
   single joined service target `gui/<uid>/<label>` — the documented
   launchctl grammar — pinned by
   `TestE17T2LaunchctlPrintUsesJoinedTarget` and re-verified on the
   real session (absent → `loaded=false`; loaded → disable exits 0
   preserving the plist).
2. **Dispatch on a never-registered route surfaced as a raw storage
   fault.** A real Watchman trigger arrival on a configured route with
   no baseline or reconciliation exited 20 (`sqlite_query_failed`,
   FOREIGN KEY) — the arrival rows reference the trusted registration,
   and the mid-transaction failure violated the documented
   never-a-storage-failure posture. The dispatch path now refuses
   BEFORE any durable write at exit 14 (`transition_invalid`) with the
   setup guidance, pinned by `TestE17T2DispatchRefusesUnregisteredRoute`
   and re-verified against the real trigger environment.
3. **The real trigger environment cannot resolve a PATH-relative
   hermes executable.** The first real-trigger submission recorded
   `transport_failure / definite_not_submitted` and parked in
   `retry_wait` — Watchman invokes triggers with a minimal environment
   (the frozen `trigger-invocation-environment.txt` evidence), where
   `executable: hermes` does not resolve. The bounded, visible,
   retryable posture is correct; the operational guidance (below) now
   names the absolute-path production posture, and the recovery exit
   (`dispatches retry` from a PATH-complete shell, then the drain)
   submitted the parked burst child.

## 7. Known limitations (the release record)

- A configuration-declared webhook sink needs a reachable HTTPS
  endpoint; against unreachable endpoints notifications stay pending
  under their persisted backoff with `ambiguous`/`retryable` outcomes —
  at-least-once, never silently dropped, resolved by the explicit
  retry or a corrected endpoint (the endpoint joins the route revision,
  so repointing requires production re-acknowledgement).
- Under the real Watchman trigger's minimal environment, a
  PATH-relative `hermes` executable cannot resolve: production
  configurations should declare the absolute executable path; the
  arrival stays durable in `retry_wait` with the documented recovery
  exits either way.
- One invocation-wide ten-second budget bounds the after-command pass:
  a slow first sink can consume it before later routes are visited;
  the recovery schedule owns the remainder (visible as
  `budget_expired` in the drain evidence and the posture).
- The managed launchd schedule is per (instance, route,
  configuration-path digest): repointing the configuration creates a
  NEW schedule identity — uninstall the old one; the durable `--at`
  override travels with its label.

## 9. Final-artifact re-verification (pre-publication)

The whole-epic audit remediation changed delivery-envelope resolution,
the retry's lease guard, and the schedule lifecycle AFTER the walkthrough
above ran on its intermediate builds. Before publication, the exact
shipping artifact (`dist/agent-dispatch-v0.1.6-darwin-arm64`, stamped
`v0.1.6 / commit 24e07c4 / schema 1-20`) re-ran the affected surfaces on
a fresh disposable environment against the installed Hermes v0.20.5,
Watchman 2026.07.27.00, and real launchd:

- `hermes probe`: every shape probe passed, mode
  `agent-dispatch-group-enforced`, fingerprint `cap:35583a58b0753f09`;
  `route preflight` green (profile, skills, workspace, serialization);
- `schedule install --at 05:45` then a flagless `schedule inspect`:
  healthy with the definition matching (the audit-reworked
  persist-before-write ordering and joined launchctl targets on real
  launchd); the two-key enable passed the schedule gate;
- `reconcile --submit` delivered a real task on the disposable board
  (`t_886b752c`, public read-back: no `mutex_key` field, assignee and
  skills intact); `work begin`/`work complete` drove the completion into
  the outbox and the after-command pass delivered it automatically
  (attempts=1, delivered); `status` projected the latest drain evidence
  and the healthy scheduler posture;
- `schedule disable` preserved the plist while unloading on real
  launchd; `uninstall` removed exactly the managed plist and cleared the
  stored override; the disposable board was deleted through the public
  CLI and the environment discarded with no launchd residue.

The deterministic suites (make verify, race included) were green on the
same tree. The pre-existing boundary stands: no Hermes WORKER executed
a dispatched task (task execution requires operator credentials and
stays outside the gate, the documented TST-007 posture) — the
completion leg was driven through the same public work-receipt CLI a
worker uses.

## 8. Boundary

No Hermes core file, private storage, or plugin was read beyond the
public CLI surface or modified at all; no worker executed; no external
network egress occurred (the webhook failure legs used non-routable
addresses). The disposable board was deleted through the public CLI,
the disposable profile, home, vault, state directory, managed
schedules, and Watchman triggers were discarded, and the watch root was
removed. The full-machine `make verify` gate is green on the committed
tree.

# Installation, Scheduling, Upgrade, and Backup

This guide is for the operator of a local installation on a supported platform
(`darwin/arm64`, `linux/amd64`, or `linux/arm64` under [D-029](../specs/decision-log.md),
E19; superseding the D-023/E9-T8 darwin/arm64-only exclusivity). The operator
owns the configuration, vault/board selection, backups, and authority to install
binaries, triggers, or schedules. Agent Dispatch runs one-shot commands:
Watchman owns event sensing and the platform scheduler owns scheduling
(launchd on macOS; managed `--platform systemd` user units on Linux); there is no Agent Dispatch
daemon. Start with the [public quick start](../../README.md#quick-start) for a new instance.

Before changing an existing installation, record `agent-dispatch version --json`,
its configuration path, route status, Watchman binding, and managed schedule state.
Back up the database and configuration before upgrading. Defects or unexplained
integrity/capability findings go to repository maintainers with redacted evidence;
Hermes access and shared Watchman topology changes belong to their host owners.

## 1. Install the Binary

Build the current checkout or install a verified published binary using the
[README](../../README.md#install). Artifact names follow
`agent-dispatch-<version>-<os>-<arch>` plus `SHA256SUMS`. `make release` emits
darwin/arm64, linux/amd64, and linux/arm64 (E19-T4). The published v0.1.8 set
contains one binary for each supported platform and architecture. Verify downloaded bytes
before copying the binary onto
PATH (`shasum -a 256 -c SHA256SUMS`, or `sha256sum -c SHA256SUMS` on Linux).
Source builds and older releases must use documentation matching their
source/version.

Keep the installed executable at a stable absolute path because Watchman triggers
and launchd jobs refer to it. Release artifact generation and publication belong
to the [release engineering guide](../implementation-tips/release-guide.md).

## 2. Platform Configuration and State Paths

| Setting | macOS (`darwin/arm64`) | Linux (`linux/amd64`, `linux/arm64`) |
|---|---|---|
| Configuration | `~/.config/agent-dispatch/config.yaml` | `$XDG_CONFIG_HOME/agent-dispatch/config.yaml` when `XDG_CONFIG_HOME` is absolute; else `~/.config/agent-dispatch/config.yaml` |
| State directory | `~/Library/Application Support/Agent Dispatch` | `$XDG_STATE_HOME/agent-dispatch` when `XDG_STATE_HOME` is absolute; else `~/.local/state/agent-dispatch` |

Configuration precedence is `--config`, then `AGENT_DISPATCH_CONFIG`, then the
platform default. The global `--state-dir` override takes precedence when supplied;
otherwise the state directory comes from `instance.state_dir`, then
`AGENT_DISPATCH_STATE_DIR`, then the platform default. Use an absolute local state
path outside the watched vault and cloud-sync folders. See the
[configuration contract](../contracts/configuration-spec.md) for enforcement.
Relative `XDG_CONFIG_HOME` / `XDG_STATE_HOME` values are ignored per the
XDG base-directory specification (`internal/platformpaths`).

### 2a. Secret references (SEC-006)

Webhook and notification sinks carry secret *references*, never inline
secret values (configuration-spec §11). Supported forms:

| Form | macOS (`darwin/arm64`) | Linux (`linux/amd64`, `linux/arm64`) |
|---|---|---|
| `env:NAME` | yes | yes |
| `file:/absolute/path` (owner-only mode 600) | yes | yes |
| `fd:N` (inherited descriptor) | yes | yes |
| `keychain:<item>` | yes (controlled Keychain lookup) | no — typed unsupported (`UnresolvedError`) |

Resolution happens immediately before each submission; the value never
enters SQLite or logs. On Linux prefer `env:`, `file:`, or `fd:` —
a `keychain:` reference fails closed naming the unsupported kind
(D-029, E19-T6).

Watchman uses a minimal environment. Configure the Hermes target's `executable`
as an absolute path, such as `/Users/<user>/.local/bin/hermes`. A PATH-relative
executable that works in a terminal may leave trigger delivery in `retry_wait`
with `transport_failure / definite_not_submitted`.

## 3. First Use and Disabled Setup

Prepare an existing vault, the intended Hermes board, and a profile with the
route's requested skills. Hermes must meet the declared version floor (at least
0.20.5) and the public capability checks. The generated example requests board
`agent-dispatch`, profile `wiki-maintainer`, and skill `llm-wiki`.

Run `agent-dispatch setup wiki`. Correct the generated disabled configuration for
your environment, then rerun with its explicit path and selected route:

```sh
agent-dispatch setup wiki --config /absolute/path/config.yaml --route wiki-maintenance
```

Setup validates, probes Hermes, preflights the destination, inspects Watchman,
and runs `reconcile --reason initial --baseline-only` while the route is disabled.
The vault must already exist. An enabled base produces a disabled draft beside
it; subsequent steps must use the printed draft path. Multiple routes require
an explicit route choice. Missing prerequisites stop the walkthrough with a
finding; reruns converge on the same disabled baseline. On Linux the same
commands apply; configuration and state resolve through the XDG defaults
in section 2 (absolute `XDG_CONFIG_HOME` / `XDG_STATE_HOME` replace the
home-relative prefixes).

After the disabled baseline succeeds, install the exact-root Watchman trigger
and the appropriate schedule (§4), inspect both, and complete the disposable
acceptance trial required by the route's automatic-write policy. For production,
set the reviewed configuration's route `enabled: true`, run config validation,
preflight, and `route show`, then acknowledge that final computed revision with
`route enable`. The [README sequence](../../README.md#4-enable-the-reviewed-route)
and [runbook](runbook.md#1-initial-deployment-sequence) show the command order.

Verify with `status`, `doctor`, Watchman status, and schedule inspection. Fixture
normalization via `watchman test` is not live delivery proof. A real trial must
observe an eligible edit reaching the intended Hermes task and receipt flow.

## 4. Daily Reconciliation Scheduling (OPS-006, OPS-007)

Choose scheduling from the configured notification drain mode. Use one owner
for daily reconciliation; avoid installing two jobs for the same route's daily run.

| Drain mode | Schedule |
|---|---|
| `scheduled` | Managed launchd (macOS) or systemd (Linux, contracted) job runs reconciliation, then drains after a healthy pass; daily at 03:00 local by default |
| `after-command` (generated default) | Managed job recovers notification drain every 15 minutes; use the manual recipe below for daily source reconciliation |
| `manual` | No automatic drain job; use the manual recipe for daily source reconciliation and drain notifications explicitly |

### Managed Schedule

Review, install, and inspect against the same absolute configuration path.
`--platform` selects `launchd` (darwin, shipped) or `systemd` (linux,
managed systemd shipped in E19-T8):

```sh
# macOS (shipped)
agent-dispatch schedule render --route wiki-maintenance --platform launchd
agent-dispatch schedule install --route wiki-maintenance --platform launchd
agent-dispatch schedule inspect --route wiki-maintenance --platform launchd

# Linux (managed systemd; E19-T8)
agent-dispatch schedule render --route wiki-maintenance --platform systemd
agent-dispatch schedule install --route wiki-maintenance --platform systemd
agent-dispatch schedule inspect --route wiki-maintenance --platform systemd
```

Add `--config /absolute/path/config.yaml` when using a non-default configuration.
For `scheduled` mode, `--at HH:MM` selects the local daily time. The managed label
derives from the instance, route, and configuration-path digest. The platform
unit — a launchd plist under `~/Library/LaunchAgents/` or a systemd user
service+timer under `~/.config/systemd/user/` — invokes the internal
`schedule run` command directly. An identical install is
idempotent; a different installed definition is refused. Inspect and explicitly
remove/replace the old managed definition when changing it. Logs rotate at 10 MiB
with three files retained. Managed `--platform launchd` is the shipped scheduler
on macOS. Managed `--platform systemd` is the shipped scheduler on Linux
(E19-T8); reviewed example units ship as
`agent-dispatch-reconcile.systemd.*.example` (E19-T9).

Automatic drain modes require an installed, loaded, definition-matching schedule
before production activation. `route preflight` reports the install guidance and
`route enable` enforces the requirement. Setup prints the schedule command;
it does not install the job.

### Manual Daily Reconciliation Recipe

For `after-command` or `manual` mode, adapt the
[launchd example](../examples/scripts/agent-dispatch-reconcile.launchd.plist.example)
with the exact binary/configuration paths and route. Install it under
`~/Library/LaunchAgents/` using the [script guidance](../examples/scripts/README.md).
It runs a one-shot `reconcile --reason scheduled --submit` daily, followed by
an explicit notification drain after successful reconciliation. The shipped
drain command is not route-filtered; review its scope and pass the same custom
`--config` to both commands if needed. The managed `scheduled` mode already owns
a reconcile-and-drain chain, so it needs no additional daily plist.

On Linux, the managed path is `--platform systemd` (cli-spec §19b; managed CLI
shipped in E19-T8). Reviewed example units live at
`agent-dispatch-reconcile.systemd.service.example` and
`.systemd.timer.example` (E19-T9; managed identity / shell-less
`schedule run`). Prefer the managed lifecycle:
1. `agent-dispatch schedule render --route <id> --platform systemd`
   (review the oneshot service + timer under `~/.config/systemd/user/`);
2. `agent-dispatch schedule install --route <id> --platform systemd`;
3. `agent-dispatch schedule inspect --route <id> --platform systemd`
   (expect `healthy`).
Managed `--platform launchd` remains the shipped scheduler on macOS.

The submit leg requires an acknowledged production revision; before acknowledgement
it fails closed. After acknowledgement, disabling the YAML route retains
reconciliation decisions without submitting new work. Confirm `last_reconciled_at`
in `status`; `doctor` reports a never-run or overdue reconciliation (over 25 hours).
`make schedule-check` checks the shipped plist and shell examples, not a live job.

## 5. Upgrade (OPS-009)

1. Disable new route submissions and stop the relevant schedules and manual
   commands. Let short-lived commands finish and inspect outstanding attempts.
2. Back up the database and matching configuration (§6), retaining the old binary.
3. Verify the replacement binary identity and artifact integrity before installing it.
4. Opening the store with the new binary may apply forward-only migrations, one
   transaction per step with pre-migration backups. Even diagnostic commands may
   open the store; finish backups before this first open. A newer-than-supported
   database is refused as `migration_newer_schema` (exit 21).
5. Run `agent-dispatch maintenance integrity --full`, `hermes probe`, route
   preflight, and `doctor`; resolve findings. Review the version-specific migration
   instructions in [runbook §9](runbook.md#9-upgrade).
6. Inspect the Watchman binding and trigger's binary path. Current E18 source
   requires the configured absolute resource root itself; follow
   [binding recovery](failure-recovery.md#absolute-watch-root-binding) if an
   existing ancestor binding must change. Inspect and restore the selected schedules.
7. Re-acknowledge the final route revision when required, then perform a reviewed
   reconciliation with `--submit` to resume delivery. Confirm health and intended
   task/notification state before leaving the installation unattended.

If validation fails, leave submissions disabled. Preserve the upgraded database
and use the verified pre-upgrade database/configuration with its matching binary
for rollback. Never point an older binary at the upgraded database expecting a
reverse migration.

## 6. Backup

```sh
agent-dispatch maintenance backup --output /secure/path/state-backup.db
```

Use a fresh output path; backup refuses to overwrite an existing file. It writes
an owner-only SQLite `VACUUM INTO` snapshot with a post-write quick check.
Copying a live WAL database without its WAL/SHM or a checkpoint is not a valid
backup. Retain the matching configuration and version metadata with the backup,
protecting them as strongly as the live state.

Before risky maintenance, stop drain/reconciliation commands and confirm no
unexpired attempt lease through `doctor`. To restore, stop all Agent Dispatch
commands and schedules, preserve the failed database, restore the verified backup
and matching configuration with owner-only permissions, then run
`maintenance integrity --full` and `doctor` before resuming. A backup newer than
the running binary's schema range needs a matching binary. Escalate failed
integrity or uncertain outstanding delivery to maintainers before submission.

## 7. Uninstall

Disable the route, remove only its managed Watchman trigger, disable/uninstall its
managed schedule and any manually installed reconciliation job, then remove the
binary. Managed schedule `disable`/`uninstall` preserve configuration, state, and
history. Watchman removal never automatically removes a watch root.

The [uninstall example](../examples/scripts/agent-dispatch-uninstall.sh.example)
retains SQLite state and configuration. Inspect the managed schedule separately
when using automatic draining. Verify the exact trigger and jobs are absent;
retain state by default because it owns deduplication and reconciliation history.
If removal is interrupted, inspect what remains and continue only for this
installation's artifacts. Permanent state erasure is a separate operator decision
covered by [retention and privacy](retention-and-privacy.md).

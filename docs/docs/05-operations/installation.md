# Installation, Scheduling, Upgrade, and Backup

E6-T3 packaging surface (SCP-008, OPS-006, OPS-007, OPS-009): how the
binary, configuration, state, trigger, and daily reconciliation
schedule are installed on macOS and Linux, and how the installation is
upgraded, backed up, and removed. Agent Dispatch is a set of one-shot CLI
commands — **no daemon is installed or required** (OPS-007, ADR
posture): Watchman owns sensing, the platform scheduler owns the daily
reconciliation, and every agent-dispatch invocation opens SQLite, does its
bounded work, and exits.

## 1. Install the binary

Release artifacts are byte-reproducible binaries plus a portable
`SHA256SUMS` file, built from the repository root:

```sh
make release VERSION=v0.1.0
# dist/agent-dispatch-v0.1.0-darwin-arm64
# dist/agent-dispatch-v0.1.0-linux-amd64
# dist/SHA256SUMS
cd dist && shasum -a 256 -c SHA256SUMS   # or: sha256sum -c SHA256SUMS
```

The build pins `-trimpath` and stamps the version, the full release
commit hash, and the commit's committer date, so two builds of one
commit produce identical bytes (checksums are generated over the
binaries directly — archive layers would embed machine-specific
metadata). Copy the `agent-dispatch` binary for your platform onto the PATH
(for example `/usr/local/bin/agent-dispatch`).

## 2. Platform configuration and state paths

| Platform | Default config | Default state directory |
|---|---|---|
| macOS | `~/.config/agent-dispatch/config.yaml` | `~/Library/Application Support/Agent Dispatch` |
| Linux | `$XDG_CONFIG_HOME/agent-dispatch/config.yaml` (else `~/.config/agent-dispatch/config.yaml`) | `$XDG_STATE_HOME/agent-dispatch` (else `~/.local/state/agent-dispatch`) |

Precedence everywhere: an explicit `--config` path, then the
`AGENT_DISPATCH_CONFIG` environment variable, then the platform default; the
state directory comes from `instance.state_dir` in the configuration,
then `AGENT_DISPATCH_STATE_DIR`, then the platform default. The state
directory must be a local filesystem outside the watched vault
(configuration-spec §3; `agent-dispatch init` and every store open enforce
it, and `doctor` reports violations).

## 3. First-use installation on a clean host (macOS scenario)

```sh
agent-dispatch init                              # writes the disabled example config,
                                           # creates the owner-only state directory
agent-dispatch config validate --probe-targets   # configuration and target gates
agent-dispatch watchman install --route wiki-maintenance
agent-dispatch route enable --route wiki-maintenance \
    --acknowledge-production-gate <computed-route-revision> --yes
# install the daily schedule (section 4), then:
agent-dispatch doctor                            # clean findings expected
```

`init` never overwrites an existing configuration, the example route
stays disabled until `route enable` records the acknowledged revision,
and the Watchman trigger is created idempotently (re-running
`watchman install` reports `already_defined`).

## 4. Daily reconciliation scheduling (OPS-006, OPS-007)

The platform scheduler runs one-shot reconciliation; there is no
Agent Dispatch daemon. Verified examples live in
`docs/examples/scripts/`:

- **macOS (launchd)**: `agent-dispatch-reconcile.launchd.plist.example` —
  install into `~/Library/LaunchAgents/` and `launchctl load` it; it
  runs `agent-dispatch reconcile --route <id> --reason scheduled --output
  json` daily at the configured time.
- **Linux (systemd --user)**: `agent-dispatch-reconcile.service.example` and
  `agent-dispatch-reconcile.timer.example` — install into
  `~/.config/systemd/user/`, `systemctl --user daemon-reload`, and
  `systemctl --user enable --now agent-dispatch-reconcile.timer` (`daily`,
  persistent).

Before the production gate the recipes omit `--submit` (reconciliation
persists its decisions for audit without submitting work); after the
gate, add `--submit` to the installed invocation. The schedule is
idempotently inspectable: `agent-dispatch status` reports
`last_reconciled_at`, and `agent-dispatch doctor` flags
`reconciliation_never_run` and `reconciliation_overdue` (over 25
hours). `make verify` validates the artifacts with the platform tool
(`plutil -lint`, `systemd-analyze verify`) on each OS of the CI matrix
(SCP-008, where possible).

## 5. Upgrade (OPS-009)

1. Back up the database and configuration (section 6).
2. Verify the new binary's version and schema range:
   `agent-dispatch version --output json` (the schema range must cover the
   database's migration version).
3. Run `agent-dispatch doctor` with the new binary without submitting work.
4. Run any command once — forward-only migrations apply inside one
   transaction per step with a pre-migration backup; a database newer
   than the binary is refused (`migration_newer_schema`, exit 21).
5. Verify integrity: `agent-dispatch maintenance integrity --full`.
6. Re-inspect the trigger (`agent-dispatch watchman status --route <id>` —
   the managed command embeds the binary path) and the schedule units.
7. Resume the route and run one reconciliation:
   `agent-dispatch reconcile --route <id> --reason manual`.

## 6. Backup

```sh
agent-dispatch maintenance backup --output /secure/path/state-$(date -u +%Y%m%d).db
```

The command writes an owner-only snapshot through SQLite's `VACUUM
INTO` with a post-write quick check — a copy of a live WAL database
without its WAL/SHM or a checkpoint is not a valid backup (runbook
§8). Keep the configuration and capability report beside the backup.
Before upgrading or risky maintenance, stop drain/reconciliation
commands and confirm no unexpired attempt lease (`agent-dispatch doctor`).

To restore: stop all agent-dispatch commands, replace the state database
with the backup file (owner-only permissions), keep the configuration
that matches it, and run `agent-dispatch maintenance integrity --full` and
`agent-dispatch doctor` before resuming the route. Downgrades are
unsupported: restore only a backup made at or below the running
schema version, and treat a newer-schema backup as unreadable until
the matching binary is installed.

## 7. Uninstall

`docs/examples/scripts/agent-dispatch-uninstall.sh.example` follows runbook
§10: disable the route, remove the managed Watchman trigger (never the
watch root), remove the schedule units, remove the binary — and
**retain the SQLite state and configuration**; no flag deletes them
automatically, because that history is the dedup and reconciliation
evidence. Discarding the state directory is a manual, backed-up
operator decision.

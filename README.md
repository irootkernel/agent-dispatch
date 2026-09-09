# Agent Dispatch

Turn changes in a Markdown vault into durable Hermes Kanban tasks.
Agent Dispatch watches for meaningful file changes, groups the work, and
tracks delivery and completion so your agents can maintain a wiki without
processing every save as a separate job.

## What you can do

- Send changed Markdown files to a Hermes profile for indexing or wiki maintenance.
- Route work to multiple destinations on one Hermes board, with explicit
  serialization groups controlling which tasks may run together.
- Keep protected paths and large batches for review, and reconcile missed events.
- Inspect task delivery, work receipts, and optional completion notifications.

Watchman detects changes; Agent Dispatch records and coordinates work in a
local SQLite database; Hermes runs the agent tasks. Each Agent Dispatch
command does bounded work and exits. Watchman and the platform scheduler
(launchd on macOS; managed systemd user units on Linux)
provide the ongoing triggers and scheduling.

## Requirements

- **Supported platforms** are `darwin/arm64`, `linux/amd64`, and `linux/arm64`
  ([D-029](docs/specs/decision-log.md)). The v0.1.8 release provides binaries
  for all three platform and architecture pairs.
- Watchman, and Hermes **0.20.5 or newer** with the public Kanban interface.
  Version eligibility is checked separately from the capabilities of your
  installed executable.
- An existing local Markdown vault, a Hermes board, and a profile with the
  skills your route requests. The generated example uses board
  `agent-dispatch`, profile `wiki-maintainer`, and skill `llm-wiki`.
- A local state directory outside the watched vault and cloud-sync folders.
- **Go 1.26.6** if building from source. Contributor verification also uses Python 3.

The [companion worker skill](docs/skills/agent-dispatch-wiki-maintenance/INSTALL.md)
helps a Hermes agent report work receipts. The optional
[operator skill](docs/skills/agent-dispatch-operator/INSTALL.md) guides operational
commands. Install and select skills explicitly in Hermes; setup does not do this for you.

## Install

### Build this checkout

This README describes v0.1.8, including the v0.1.7 absolute watch-root fix and
official Linux support. See [v0.1.8](CHANGELOG.md#v018---2026-09-10) for the
release changes.

From the repository root:

```sh
make build
./bin/agent-dispatch version
mkdir -p "$HOME/.local/bin"
install -m 755 bin/agent-dispatch "$HOME/.local/bin/agent-dispatch"
export PATH="$HOME/.local/bin:$PATH"
```

The default build reports `agent-dispatch v0.1.8`. The install command replaces any
binary at the destination; retain the previous binary and back up an existing
installation before upgrading. Add the PATH entry to your shell configuration
if needed. Keep the installed path stable because managed triggers and schedules
refer to the executable.

### Use a published binary

Choose a version from the repository's
[GitHub Releases](https://github.com/irootkernel/agent-dispatch/releases), and read
that version's section in the [changelog](CHANGELOG.md). Download and install
the published v0.1.8 binary for the current supported host as follows; no Go
toolchain is needed. Watchman and Hermes remain separate requirements.

```sh
set -eu
release_version=v0.1.8
case "$(uname -s)/$(uname -m)" in
  Darwin/arm64) platform=darwin-arm64 ;;
  Linux/x86_64) platform=linux-amd64 ;;
  Linux/aarch64|Linux/arm64) platform=linux-arm64 ;;
  *) echo "unsupported platform" >&2; exit 1 ;;
esac
artifact="agent-dispatch-${release_version}-${platform}"
mkdir "agent-dispatch-${release_version}-download"
cd "agent-dispatch-${release_version}-download"
curl --fail --location --remote-name \
  "https://github.com/irootkernel/agent-dispatch/releases/download/${release_version}/${artifact}"
curl --fail --location --remote-name \
  "https://github.com/irootkernel/agent-dispatch/releases/download/${release_version}/SHA256SUMS"
grep -F "  ${artifact}" SHA256SUMS > "${artifact}.sha256"
if command -v shasum >/dev/null 2>&1; then
  shasum -a 256 -c "${artifact}.sha256"
else
  sha256sum -c "${artifact}.sha256"
fi
mkdir -p "$HOME/.local/bin"
install -m 755 "$artifact" "$HOME/.local/bin/agent-dispatch"
"$HOME/.local/bin/agent-dispatch" version
export PATH="$HOME/.local/bin:$PATH"
```

Installation replaces an existing binary at that path; retain the previous
binary and back up existing state before upgrading. Use documentation from
the matching tag when running an older release.

See the [installation and upgrade guide](docs/ops/installation.md) for state
paths, backups, migration behavior, and removal.

## Quick start

### 1. Prepare the destination

Create or select your Hermes board, profile, and skills through Hermes's public
interfaces. For a new board matching the generated example:

```sh
hermes kanban boards create agent-dispatch
```

Use an existing vault. For a first trial, use a disposable vault and Hermes board
with a profile configured for that trial before activating automated edits.

### 2. Run guided setup

```sh
agent-dispatch setup wiki
```

Setup prompts for the vault root, writes a disabled configuration, probes Hermes,
checks the destination and Watchman binding, and records the initial baseline.
It prints the configuration path and the remaining activation steps. If a
prerequisite is missing, correct it and rerun against that same configuration:

```sh
agent-dispatch setup wiki --config /absolute/path/config.yaml --route wiki-maintenance
```

Edit the generated configuration to match your board, profile, and skills.
Set `hermes_targets.<target-id>.executable` to the **absolute path** of Hermes:
Watchman runs with a minimal PATH. Keep `routes.<route-id>.enabled: false` until
setup and your trial are complete. An enabled input configuration produces a
disabled draft; use the configuration path printed by setup for subsequent commands.

Commands below use the default configuration and example route. If setup printed
a different path or you chose another route, pass the same `--config` and `--route`
throughout. See the [configuration contract](docs/contracts/configuration-spec.md)
for all fields and defaults.

### 3. Install the trigger and schedule

```sh
agent-dispatch watchman install --route wiki-maintenance
agent-dispatch watchman status --route wiki-maintenance
agent-dispatch schedule render --route wiki-maintenance --platform launchd
agent-dispatch schedule install --route wiki-maintenance --platform launchd
agent-dispatch schedule inspect --route wiki-maintenance --platform launchd
```

Review the rendered schedule before installing it. The generated configuration
uses `after-command` notification draining, which requires its managed recovery
schedule before activation. That schedule recovers notification delivery every
15 minutes; it does not replace daily source reconciliation. For daily
reconcile-and-drain behavior, configure `notifications.drain.mode: scheduled`
before rendering and installing. Routes with manual draining use the separate
[daily reconciliation recipe](docs/ops/installation.md#4-daily-reconciliation-scheduling-ops-006-ops-007).

The current source requires the configured absolute vault root itself to be a
Watchman watch root. If installation refuses an ancestor binding, follow the
reported diagnosis and the [recovery guide](docs/ops/failure-recovery.md);
removing another watch root can affect other tools.

### 4. Enable the reviewed route

For your first trial, activate the disposable vault and board prepared in step 1.
After disabled setup succeeds, set the selected route's configuration `enabled`
field to `true`, then inspect its final configuration:

```sh
agent-dispatch config validate --probe-targets
agent-dispatch route preflight --route wiki-maintenance
agent-dispatch route show --route wiki-maintenance
```

Use the computed route revision from this final configuration in the explicit
activation command:

```text
agent-dispatch route enable --route wiki-maintenance --acknowledge-production-gate <computed-route-revision> --yes
```

This enables automatic submissions to Hermes. Editing YAML alone does not
activate a route, and setup never executes this acknowledgement for you. Complete
the checks below on the disposable instance first; then repeat the setup and
reviewed activation for production with its own configuration and revision.

### 5. Check the result

```sh
agent-dispatch status
agent-dispatch doctor
agent-dispatch dispatches list
agent-dispatch receipts list --route wiki-maintenance
```

After an eligible Markdown edit, inspect the dispatch and its Hermes task.
Delivery acceptance and completed agent work are separate states. `watchman test`
checks fixture normalization; it does not prove that a live edit reached Hermes.

## Everyday commands

| Goal | Command |
|---|---|
| Inspect overall state | `agent-dispatch status` |
| Diagnose configuration or runtime findings | `agent-dispatch doctor` |
| Inspect one dispatch | `agent-dispatch dispatches show <dispatch-id>` |
| Pause new submissions | `agent-dispatch route disable --route wiki-maintenance` |
| Reconcile current files and submit eligible work | `agent-dispatch reconcile --route wiki-maintenance --reason manual --submit` |
| Inspect notifications | `agent-dispatch notifications list` |
| Back up state | `agent-dispatch maintenance backup --output /absolute/path/backup.db` |

Most commands accept `--output json`. Product identity uses the separate
`agent-dispatch version --json` form. The [CLI reference](docs/contracts/cli-spec.md)
describes arguments, output, and state-changing commands.

## Troubleshooting

- **Setup stops:** use its finding and configuration path, prepare the missing
  vault/profile/skill, then rerun. Setup keeps the route disabled.
- **No task after an edit:** check route activation, Watchman status, protected
  paths, and `doctor`. An unchanged digest may correctly produce no task.
- **Delivery is unknown:** inspect the dispatch before retrying. An interrupted
  request may already have created a Hermes task.
- **Notifications are pending:** inspect notification and schedule state;
  notification recovery does not require rerunning completed agent work.

The [operations runbook](docs/ops/runbook.md) covers diagnosis, reconciliation,
quarantine, backup, and recovery. Note bodies are not stored in the operational
database, but relative paths can reveal note titles; see
[retention and privacy](docs/ops/retention-and-privacy.md).

## Contribute

See the [changelog](CHANGELOG.md) for released and pending changes.

Start with [CONTRIBUTING.md](CONTRIBUTING.md) and the
[developer documentation](docs/README.md) for architecture, contracts, tests,
and the canonical roadmap. Agent Dispatch is [MIT licensed](LICENSE).

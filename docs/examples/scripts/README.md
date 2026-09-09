# Deployment Script Examples

The `launchd` scheduled-reconciliation example, the managed systemd
user unit/timer examples, and the uninstall procedure script are the
shipped scheduling deliverables under the D-029 three-platform support
matrix (E6-T3 launchd/uninstall; E19-T9 systemd). Examples track the
verified command shapes (OPS-006/007/018): the launchd recipe drives
one-shot `reconcile --reason scheduled --submit` plus the notification
drain; the systemd examples mirror the managed CLI identity and
shell-less `schedule run --route … --config …` ExecStart contract from
E19-T8 / cli-spec §19b (prefer `agent-dispatch schedule install
--platform systemd` over hand-editing). `make schedule-check` lints the
launchd artifact where `plutil` is present, the systemd pair where
`systemd-analyze` is present (skip+message when either tool is absent),
and the uninstall script with `sh -n`. Replace placeholder binary,
route, configuration, and managed-label values, and review the uninstall
script before use — see
[the installation guide](../../ops/installation.md) for the full
install, schedule, upgrade, and backup procedures.

- `agent-dispatch-reconcile.launchd.plist.example` — macOS LaunchAgent
- `agent-dispatch-reconcile.systemd.service.example` — Linux systemd --user oneshot service (managed identity)
- `agent-dispatch-reconcile.systemd.timer.example` — Linux systemd --user timer (03:00 OnCalendar default)
- `agent-dispatch-uninstall.sh.example` — runbook §10 uninstall order; retains SQLite and configuration

## Install a Manual Daily Reconciliation Job

Use this recipe only when no managed `scheduled` drain job already owns daily
reconciliation for the route. The installation operator owns this LaunchAgent;
review its binary path, configuration path, and route before loading it. The
shipped shell recipe explicitly drains notifications after a successful
reconciliation; its drain command is not route-filtered. For a custom
configuration, pass the same `--config` to both commands and quote shell paths
with spaces while preserving valid plist XML.

1. Copy the example to a local working file named `agent-dispatch-reconcile.plist`
   and replace its placeholders with the installation's absolute paths and route.
2. Run `plutil -lint agent-dispatch-reconcile.plist` on the edited file.
3. Place the reviewed file in `~/Library/LaunchAgents/` without overwriting an
   unrelated job, then load that file with
   `launchctl load "$HOME/Library/LaunchAgents/agent-dispatch-reconcile.plist"`.
4. Inspect the job under the Label declared in the plist. After its scheduled run,
   verify the route's `last_reconciled_at` through `agent-dispatch status` and
   investigate `doctor` findings.

If loading or execution fails, inspect the declared paths and job output before
retrying. Unload this exact file with `launchctl unload` before replacing or
removing it; preserve Agent Dispatch configuration and state. A job that uses
`--submit` can submit real Hermes work after route acknowledgement. The managed
notification schedule has its own `schedule inspect|disable|uninstall` lifecycle.

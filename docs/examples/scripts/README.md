# Deployment Script Examples

The `launchd` and `systemd --user` scheduled-reconciliation examples
and the uninstall procedure script are the E6-T3 deliverables,
generated from the verified `reconcile` and `watchman` command shapes
(OPS-006/007): the platform validators lint them on the platform where
each tool exists (`make schedule-check` per host; no hosted CI is used,
D-017), and the CLI test suite pins their
safety properties. Replace the placeholder binary path, route id, and
review the uninstall script before use — see
`docs/docs/05-operations/installation.md` for the full install,
schedule, upgrade, and backup procedures.

- `agent-dispatch-reconcile.launchd.plist.example` — macOS LaunchAgent
- `agent-dispatch-reconcile.service.example` + `agent-dispatch-reconcile.timer.example` — Linux systemd --user units
- `agent-dispatch-uninstall.sh.example` — runbook §10 uninstall order; retains SQLite and configuration

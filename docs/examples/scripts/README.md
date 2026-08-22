# Deployment Script Examples

The `launchd` and `systemd --user` scheduled-reconciliation examples
and the uninstall procedure script are the E6-T3 deliverables,
generated from the verified `reconcile` and `watchman` command shapes
(OPS-006/007): the platform validators lint them on each OS of the CI
matrix (`make schedule-check`), and the CLI test suite pins their
safety properties. Replace the placeholder binary path, route id, and
review the uninstall script before use — see
`docs/docs/05-operations/installation.md` for the full install,
schedule, upgrade, and backup procedures.

- `jjukkumi-reconcile.launchd.plist.example` — macOS LaunchAgent
- `jjukkumi-reconcile.service.example` + `jjukkumi-reconcile.timer.example` — Linux systemd --user units
- `jjukkumi-uninstall.sh.example` — runbook §10 uninstall order; retains SQLite and configuration

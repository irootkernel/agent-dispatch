# Deployment Script Examples

The `launchd` scheduled-reconciliation example and the uninstall
procedure script are the E6-T3 deliverables under the macOS-only
support policy (D-023/E9-T8; the retired `systemd --user` examples are
superseded history), generated from the verified `reconcile` and
`watchman` command shapes (OPS-006/007): `make schedule-check` lints
the launchd artifact on macOS and the CLI test suite pins the safety
properties. Replace the placeholder binary path, route id, and
review the uninstall script before use — see
`docs/docs/05-operations/installation.md` for the full install,
schedule, upgrade, and backup procedures.

- `agent-dispatch-reconcile.launchd.plist.example` — macOS LaunchAgent
- `agent-dispatch-uninstall.sh.example` — runbook §10 uninstall order; retains SQLite and configuration

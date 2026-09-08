# Operations

This directory owns operational guidance for the local macOS arm64 installation:
Agent Dispatch commands, SQLite state, managed Watchman triggers, and launchd
schedules. It is one of the eight canonical documentation roles. Guidance here
is non-normative and cannot override specifications, contracts, or accepted ADRs.

The installation's operator owns configuration, local access, backup, and approval
to change its vault/board integration. Repository maintainers own defect diagnosis
and documented recovery behavior. Hermes administrators own Hermes profiles,
skills, and board access; Watchman roots may also serve other tools.

| Need | Guide |
|---|---|
| First-time product use | [Public quick start](../../README.md#quick-start) |
| Install, schedule, upgrade, back up, or remove | [Installation](installation.md) |
| Inspect or operate an existing instance | [Runbook](runbook.md) |
| Resolve a failure | [Failure and recovery](failure-recovery.md) |
| Manage retention and sensitive operational evidence | [Retention and privacy](retention-and-privacy.md) |

Before intervention, identify the exact configuration, route, binary version,
and environment. Inspect status and preserve evidence. State-changing steps need
the installation owner's authority, and destructive changes need a restorable
backup. Use each guide's success and recovery checks before resuming submissions.
Never include secret values or unredacted support output in reports.

Release engineering belongs to the
[release guide](../implementation-tips/release-guide.md); production installation
and activation are separate from producing or publishing release artifacts.

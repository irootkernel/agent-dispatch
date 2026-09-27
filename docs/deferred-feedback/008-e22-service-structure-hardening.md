# DF-008: Clarify verification and managed-service boundaries

Recorded 2026-09-28 from the E22 whole-Epic correction review. The next owners
are the sync verification and managed-service follow-ups, respectively.

**Affected authority.** `internal/cli/sync_verify.go` resolves membership for
the operator command, `internal/cli/sync_serve.go` owns serving-process state,
and `internal/cli/sync_service.go` renders and installs managed service
definitions. `docs/ops/runbook.md` documents the managed log location.

**Bounded concern.** `sync verify` constructs a `peerService` containing only
configuration to call `currentMembers`; a one-shot command does not benefit
from the service's membership cache or monotonic-advance state. The current
call does not require other service fields and verification still checks the
remote membership ref. On macOS, render and install each derive the sync log
directory from the parent of `launchAgentsDir()`. Both expressions currently
agree with the documented `~/Library/Logs/agent-dispatch` path, but a future
change to the LaunchAgents location could separate rendered and created log
paths. The sync log cap also reuses the notification schedule's 10 MiB constant;
its value currently matches the runbook.

**Reconsideration condition.** When membership loading or verification setup
next changes, use the configuration-and-Git membership helper directly from
`sync verify` and test its remote binding. When service rendering, installation,
or log limits next change, give the sync log directory and cap explicit shared
owners in both render and install, and test the resulting definition and
created directory together.

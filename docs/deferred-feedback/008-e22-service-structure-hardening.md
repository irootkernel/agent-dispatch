# DF-008: Clarify verification and managed-service boundaries

Recorded 2026-09-28 from the E22 whole-Epic correction review. The next owners
are the sync verification and managed-service follow-ups, respectively.

**Affected authority.** `internal/cli/sync_verify.go` resolves membership for
the operator command, `internal/cli/sync_serve.go` owns serving-process state,
and `internal/cli/sync_service.go` renders and installs managed service
definitions. `docs/ops/runbook.md` documents the managed log location.
`internal/cli/sync_diagnostics.go` reads queue state, and
`internal/cli/sync_peer_inbox.go` parses the child `sync reconcile` result.

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

`sync service inspect` reports whether the configured group is enabled and
whether the manager has loaded the service. It does not report the manager's
automatic-start setting, so stop and disable have the same inspection result.
The current CLI contract promises the existing fields. Manager enablement is
an operator visibility improvement for a future contract change.

The queue count in `sync_diagnostics.go` uses a raw `sync_jobs` query in the
CLI package, contrary to the SQLite placement rule in
`docs/implementation-tips/repository-layout.md`. It currently returns the
correct count, but a later schema change could leave this query behind.

The peer inbox parses the `sync reconcile` child envelope. Tests use
hand-written reply bytes, so a future envelope change could break settlement
without failing those tests. The current producer and parser agree.

The peer-service tests do not directly exercise expired delivery-claim
recovery, open `fd:` signer refusal, a valid but obsolete inbox binding,
command-level startup refusal for unreadable membership or an unavailable
socket, or the inbox's local-Git-unavailable reason. Neighboring tests cover
replay, signer environment isolation, socket ownership, and recovery; the
uncovered branches currently fail closed or retain their obligations.

**Reconsideration condition.** When membership loading or verification setup
next changes, use the configuration-and-Git membership helper directly from
`sync verify` and test its remote binding. When service rendering, installation,
or log limits next change, give the sync log directory and cap explicit shared
owners in both render and install, and test the resulting definition and
created directory together. When service inspection gains manager enablement,
define and test the platform-specific state in the CLI contract. When queue
diagnostics or the `sync_jobs` schema next changes, move the count into a typed
SQLite method. When the reconcile envelope or peer inbox settlement next
changes, test the parser against bytes emitted by the real command.

When delivery claims, signer isolation, inbox settlement, or serve startup
next changes, add focused tests for the uncovered branches above and their
operator-visible results.

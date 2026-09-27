# DF-009: Guard sync contract usage against flag drift

Recorded 2026-09-28 from the E22 whole-Epic correction review. The next
owner is the sync provider-contract follow-up.

**Affected authority.** `docs/contracts/sync-provider-v1/commands.json` owns
the machine-readable flag contracts; `docs/contracts/sync-contract.md` and
`internal/cli/help.go` show operator-facing usage. The bundle checker in
`internal/synccontractcheck/check.go` pins the machine-readable flags.

**Bounded concern.** The pause and resume `--config` flag now agrees across
the CLI, provider bundle, and written usage. Existing checks validate the
bundle but do not compare its flag lists with the usage examples. A later flag
change could leave those examples stale while `make verify` passes. The
machine-readable contract remains enforced and current usage is correct.

**Reconsideration condition.** When a sync command's flag contract next
changes, add a focused check comparing the relevant usage examples with the
bundle, including optional and required flags. Keep the human-facing examples
accurate without treating prose as the authority for command behavior.

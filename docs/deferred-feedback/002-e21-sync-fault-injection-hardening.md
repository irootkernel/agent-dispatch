# DF-002: E21 sync fault-injection coverage can be strengthened

Recorded 2026-09-23 from the E21 cold validation.

**Affected authority.** The E21 qualification suites in
`internal/cli/e21t1_test.go`, `internal/cli/e21t3_test.go`, and
`internal/adapters/gitlocal/git_test.go` cover publication, guarded import,
mixed import recovery, stale acknowledgement rejection, content-binding
rejection, and local ref/index/worktree convergence. The recovery tests inject
durable intermediate states directly rather than terminating a real process at
every Git/SQLite boundary.

**Bounded concern.** Three Low hardening opportunities remain independent of
the accepted E21 behavior: run selected recovery cases through an actual
process-death harness, add a deterministic mutation seam between snapshot reads
to prove racy capture refusal, and exercise the remaining command-level
main-apply failure arms directly. Existing state-machine and integration tests
cover the corresponding behavior, so these additions are not required for E21
correctness or acceptance.

**Reconsideration condition.** E22-T5 owns the next sync qualification pass.
Add the cases when that task introduces the two-node service harness or changes
publication/import recovery boundaries; promote the work to a roadmap task if
the harness cannot remain a bounded test-only addition.

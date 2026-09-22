# DF-002: E21 sync fault-injection coverage can be strengthened

Recorded 2026-09-23 from the E21 cold validation.

**Affected authority.** The E21 qualification suites in
`internal/cli/e21t1_test.go`, `internal/cli/e21t3_test.go`, and
`internal/adapters/gitlocal/git_test.go` cover publication, guarded import,
mixed import recovery, content-binding rejection, and local
ref/index/worktree convergence. Stale-acknowledgement currentness and reporting
are covered by `internal/config/e20t4_test.go` and
`internal/cli/e20t4_test.go`. The recovery tests inject durable intermediate
states directly rather than terminating a real process at every Git/SQLite
boundary.

**Bounded concern.** Three Low hardening opportunities remain independent of
the accepted E21 behavior: run selected recovery cases through an actual
process-death harness, add deterministic mutation seams between snapshot reads
and the two publication-eligibility reads to prove racy refusal, and exercise
the remaining command-level main-apply and reconcile-fence result arms
directly. Existing state-machine and integration tests cover the corresponding
behavior, so these additions are not required for E21 correctness or
acceptance.

**Reconsideration condition.** E22-T5 owns the next sync qualification pass.
Add the cases when that task introduces the two-node service harness or changes
publication/import recovery boundaries; promote the work to a roadmap task if
the harness cannot remain a bounded test-only addition.

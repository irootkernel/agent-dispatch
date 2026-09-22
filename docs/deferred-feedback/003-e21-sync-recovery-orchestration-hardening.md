# DF-003: E21 recovery orchestration can be consolidated before the managed service

Recorded 2026-09-23 from the E21 cold-validation confirmation review.

**Affected authority.** `internal/cli/sync_publish.go`,
`internal/cli/sync_checkpoint.go`, and `internal/cli/sync_reconcile.go` own the
current one-shot claim, push, recovery, and operator-result orchestration.
Their behavior is covered by G17 and the E21 CLI suites, but phase-specific
claim lease arithmetic and the publication push-outcome switch remain local to
individual command paths.

**Bounded concern.** Claim leases use repeated phase multipliers, and fresh and
recovery publication paths repeat the four-way push-outcome mapping. In
addition, a publication obligation that crashes before its first signed
journal entry can remain visible but unresolved when later content produces a
different logical identity. These are Low maintainability and operability
concerns: current bounds, attempt caps, status visibility, and fail-closed
admission preserve correctness.

**Reconsideration condition.** E22-T2 owns periodic recovery, persisted
backoff, and the managed service loop. Name the lease policy, consolidate the
shared push disposition only where evidence/result differences remain explicit,
and define how the service retires or escalates a stranded pre-signature
obligation when that task extracts the one-shot orchestration.

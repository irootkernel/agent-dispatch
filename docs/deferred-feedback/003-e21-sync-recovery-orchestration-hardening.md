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

**E22-T2 disposition.** The peer worker now uses the guarded `sync reconcile`
command for startup, periodic, and nudge-driven work. It persists retry timing
in schema v27. `sync status` reports the count and oldest creation time of
pre-signature publication obligations, and the service warns while leaving
them for explicit `sync publish` re-entry. Push classification was already
shared by the fresh and recovery paths through `classifySyncPush`; their
evidence and result mappings remain distinct. Claim leases currently reserve
the configured subprocess limit times six for new publication, five for
checkpoint application, or four for signed-publication and import recovery,
plus 60 seconds in each case. This is a Low consolidation concern; the
durable claim fence and guarded recovery preserve correctness.

**Reconsideration condition.** E22-T4 owns the managed service lifecycle.
Consolidate phase-specific lease arithmetic if its lifecycle implementation
needs a shared deadline policy. Do not change the operation counts without
requalifying publication, checkpoint, and import recovery boundaries.

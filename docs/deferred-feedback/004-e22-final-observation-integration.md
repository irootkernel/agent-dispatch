# DF-004: Drive the final verification recheck through the two-node matrix

Recorded 2026-09-27 from the E22-T3 third work-unit review.

**Affected authority.** `internal/cli/sync_verify.go` re-observes local Git and
control state before completing a pair verification. The T3 tests exercise
target movement through the command and each final-disposition input through
the decision function. They do not change local state between the command's
first and final local observations.

**Bounded concern.** A future wiring regression could skip the final local
sample while the decision function's unit tests still pass. Current code makes
the second observation and fails closed on changed control, binding, or pair
state. The missing integration interleaving is a Low test gap.

**Reconsideration condition.** E22-T5 owns real two-node qualification. In its
verification matrix, change a governed file or pause local control after the
first local sample and before the final recheck. Require `incomplete` with
`local_changed_during_verification`, and retain the exact command and service
evidence with the T5 qualification package.

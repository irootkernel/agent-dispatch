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

**E22-T5 disposition (2026-09-28).** The disposable Mac/OCI matrix verified
clean pair convergence, lost-nudge recovery, and an incomplete conflict pair
on fixed `v0.2.0` binaries. It did not inject a local change in the short
interval between the first and final local samples. The command-level tests
exercise the final disposition inputs, and the implementation still performs
the second observation. The residual integration timing gap is Low.

**Reconsideration condition.** When a controlled two-node service harness or
verification timing hook is introduced, change a governed file or pause local
control after the first local sample and before the final recheck. Require
`incomplete` with `local_changed_during_verification` and retain the command
and service evidence with that harness's qualification package.

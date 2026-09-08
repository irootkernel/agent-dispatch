# DF-001: The g9 capability-cache write is not hermetic outside `make`

Recorded 2026-09-08 from the E18 delivery (twice in one day).

**Affected authority.** `internal/cli/hermes_cmds.go` derives the
capability cache path from the platform default (the user's real
`~/.config/agent-dispatch/`), and `TestG9AC905...` writes
`hermes-capability-hermes-main.json` through that path. `make test` and
`make verify` isolate HOME through `run_isolated_tests`, so the leak
appears only when the suite is invoked directly (`go test
./internal/cli/ ...`).

**Bounded concern.** A direct `go test` run overwrites the operator's
live capability record with a test-fixture record. Dispatch then keeps
the frozen `--mutex-key` rendering posture, Hermes v0.21.0+ rejects the
argument array, and live arrivals park in `retry_wait` until
`agent-dispatch hermes probe --target <id>` refreshes the record and
`dispatches drain` delivers them. This happened twice during E18-T2
(remediated both times by re-probing; disclosed in VALIDATION.md's G14
operational note).

**Reconsideration condition.** Make the CLI's capability cache path
overridable (for example a test-scoped `AGENT_DISPATCH_CONFIG_DIR` or a
`capabilityCachePath` seam injected in the tests) so the g9 suite can
never touch the real user configuration; pick it up with the next
roadmap task that touches the hermes command wiring or test hermeticity.
Until then, run this repository's suites only through `make`.

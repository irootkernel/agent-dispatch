# DF-001: The g9 capability-cache write is not hermetic outside `make`

Recorded 2026-09-08 from the E18 delivery (twice in one day).

**Status:** Resolved 2026-10-19.

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

**Resolution: 2026-10-19.** `capabilityCachePath` in
`internal/cli/hermes_cmds.go` now exposes a test-scoped
`testCapabilityCacheDir` seam, wired in `TestMain`
(`internal/cli/e18t1_test.go`) and per-test in `e4t3Fixture`
(`internal/cli/e4t3_test.go`), so `go test ./internal/cli/...` never
writes capability records into the operator's real
`~/.config/agent-dispatch/` even when invoked directly outside `make`.
In addition, `NewSink` in `internal/adapters/hermeskanban/sink.go`
defaults `ResourceMutexSupported` to `false`
(`agent-dispatch-group-enforced`, HER-019/HER-021), and `resolveSinkCtx`
in `internal/cli/state.go` rejects stale or foreign capability records
and re-probes activation-bound targets when the cache is missing or
stale. `TestE15T2SubmitIgnoresOverwrittenCapabilityCacheAndHeals` in
`internal/cli/e15t2_test.go` covers the overwritten-cache regression.

# DF-005: Harden endpoint diagnostics and regression coverage

Recorded 2026-09-28 from the E22-T5 corrected-target review.

**Affected authority.** `internal/cli/sync_peer_delivery.go` owns delivery
refusal, `internal/config/semantic.go` maps shared endpoint-parser errors, and
`internal/domain/syncrecords/endpoint.go` owns the origin grammar. The
associated tests live in `internal/config/e20t4_test.go`,
`internal/schemavalid/schemavalid_test.go`, and the CLI sync suites.

**Bounded concern.** Endpoint mismatch with signed membership persists as a
generic `refused` delivery result, so an operator must compare configuration
and membership to distinguish it from other refusals. The positive config-port
test uses a string replacement that could become a no-op if its example input
changes; the real Mac/OCI fixture and the nudge, verification, membership, and
schema tests currently exercise port acceptance. The domain parser's error
sentinels are tested indirectly, and config maps them by exact equality; a
future wrapped error would lose the specific diagnostic. The Linux Unix socket
path-length failure was visible during qualification and is documented, but
has no automated regression case. These are independent Low hardening risks;
current refusal, parsing, and listener failure remain fail-closed.

**Reconsideration condition.** When the peer delivery status reasons, endpoint
grammar, config example, or service listener diagnostics next change, add a
bounded `endpoint_drift` reason, assert that the positive port fixture changed,
pin parser sentinel classes directly, and add an over-long socket-path test.
Use `errors.Is` if parser errors gain wrapping. The next owner is the
corresponding E22 service or endpoint follow-up; this entry creates no new
roadmap lifecycle state.

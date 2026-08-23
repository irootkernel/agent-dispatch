# Agent Dispatch v0.1.2

Released 2026-08-23 from the tagged tree. v0.1.2 is the compliance
remediation release closing every Blocker, High, and mapped Medium
finding of the 2026-08-23 MVP compliance review (D-020), delivered as
roadmap epic E8 (six tasks, changelog 1.0.27–1.0.33).

## Fixed

- **Follow-up loop (the review's Blocker):** a vault edit arriving
  between completion and follow-up submission no longer wedges
  `work complete`; the route activates into `ACTIVE_DIRTY` with its
  retained dirty generation (E8-T1).
- **Bounded follow-up chains:** follow-up identities are fresh UUIDv7
  values (no cumulative suffix growth), the generation window is keyed
  on a monotonic batch-sequence watermark (same-second completions no
  longer re-import), receipt paths outside the route's effective scope
  and byte-identical rewrites never block exact suppression, and a
  consecutive-follow-up budget resolves runaway chains through
  `UNCERTAIN` (E8-T1).
- **Recovery on every submit path:** the Watchman-triggered dispatch
  and the scheduled `reconcile --submit` sweep expired submitting
  leases at their head — a process that died mid-submit heals on the
  next trigger without a manual drain (E8-T2).
- **Operator exits:** `dispatches retry` resets an exhausted attempt
  budget in one audited transaction; a definite target rejection
  dead-letters through the declared edge; expected refusals exit 14 and
  storage failures 20 instead of 40; the migration lock heartbeats
  under long units; argv-length `exec` failures classify as definite
  not-submitted (E8-T2).
- **Behavior-sensitive revision and production gate:** the computed
  route revision covers the resource root, file scope, git mode, the
  global limits, and the target type/board/endpoint; the acknowledged
  revision gates every submit so a behavior change pauses the route
  until re-acknowledged; `route enable` probes the live target and
  requires `durable_acceptance` and `submit_idempotency_key`
  unconditionally; the capability report records
  `lookup_by_idempotency_key` as false (the public CLI has no read-only
  key query) (E8-T3).
- **Retention and diagnostics:** prune never deletes the lineage of an
  active accepted dispatch; doctor exits nonzero for every AC-502
  condition, probes resource roots with a real access check, and never
  fabricates findings; `state.db` is created 0600; the credential
  redactor covers header, query/fragment, and bare-JWT forms; the
  `startup` reconcile reason lands with its runbook procedure (E8-T4).
- **Input containment and validation:** every recorded path resolves
  containment — pure deletes included — so a crafted escaping delete is
  rejected (`source_unsafe_path`, exit 30); `config validate` runs the
  probe-free section 12 checks by default, including resource-root
  overlap, absolute paths, key grammar, and the hash floor; the managed
  Watchman trigger pins `--config` (E8-T5).
- **Documentation truth:** the SOT documents describe the delivered
  behavior; the E7 closure rows the review refuted are superseded by
  the refreshed MUST-closure matrix in VALIDATION (E8-T6).

## Linux verification

The SCP-008 exception is closed under D-020: `make verify` passed in
full on linux/arm64 as a non-root user and `make test` passed on
linux/amd64. The two permission-expectation tests self-skip when run
as root. The real Hermes and Watchman integration legs and
`systemd-analyze verify` remain macOS-verified only.

## Production-write gate

The automatic production-write gate (TST-008) remains **disabled** in
the shipped defaults: re-enable it through `route enable` with the
acknowledged computed revision and the YAML `enabled` key after your
own gate review. The acknowledged revision is now enforced at submit
time, so behavior-affecting configuration changes pause the route until
re-acknowledged.

## Artifacts

`dist/agent-dispatch-v0.1.2-darwin-arm64`,
`dist/agent-dispatch-v0.1.2-linux-amd64`, and `dist/SHA256SUMS`
(byte-identical across two consecutive builds from the tagged tree).

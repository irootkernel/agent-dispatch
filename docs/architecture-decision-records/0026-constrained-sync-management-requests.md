# ADR-0026: Local constrained sync management requests

- Status: Proposed
- Date: 2026-09-30
- Delivery owner: E23-T1
- Scope: A successor release after v0.2.0, not the current release candidate

## Context

E20-E22 implement two-node synchronization without a Plugin. Plugin EPIC-007
adds inspection of that system. Its reserved EPIC-008 needs a safe management
provider, but direct wrapping of long-running operator commands cannot tell a
caller whether a timed-out invocation was accepted or which later result
belongs to it. Existing sync jobs provide useful durability primitives; they
do not yet form a public management request contract.

Master requested a follow-up Core epic and corresponding Plugin planning on
2026-09-30 after discussing release of the current pair. This records that
planning request, not approval of a new runtime capability or its exact wire
schema. E23-T1 must accept or supersede this proposal before implementation.

## Proposed decision

Add a local, independently disabled management provider over the existing sync
worker and application services. Use operator preauthorization for a closed
allowlist of `sync-now`, `verify-all`, `retry`, local `pause`, and local
`resume`. Keep new publication and all private-key resolution in the existing
explicit operator commands. Keep membership/checkpoint administration, peer
control, and service installation outside this provider.

A bounded `prepare` creates an immutable draft handle without executable work.
`submit` accepts that existing handle idempotently; `show` reads its exact
state. Obtaining the handle first lets a stateless Plugin recover an uncertain
submission without assuming that Hermes supplies a durable invocation ID.
Preparation is not human confirmation. The operator's configuration remains
the authority, and Core rechecks it at every effect boundary.

Long-running work survives the submitter through Core's existing executor.
Local pause/resume use the shared revision-fenced control transaction and a
bounded admission reserve, so a saturated or paused worker cannot prevent its
own control. A control receipt distinguishes intent from quiescence.

Preserve existing identities and attempt budgets when recovering an earlier
request. Do not make `retry` a fresh execution against whatever target exists
now. Exact request outcomes, actual sync job evidence, and fresh convergence
remain separate facts.

The [planned management contract](../contracts/sync-management-contract.md)
owns detailed semantics. `SMR-*` owns successor requirements, and the roadmap
owns delivery status. The v0.2.0 sync contract and Plugin artifact pins remain
unchanged until a separately qualified successor is admitted.

## Alternatives considered

Directly wrapping current CLI commands leaves post-timeout submission and
result correlation unresolved. Increasing the Plugin timeout cannot solve
response loss. A Plugin-owned database would duplicate Core's authority and
make recovery depend on the caller's installation.

A single admission call with an externally durable idempotency key could work
with a verified caller contract. The current Plugin ignores extra Hermes
handler context and has no qualified persistence guarantee for such a key.
Preparation avoids introducing that dependency. A later replacement would
need equivalent recovery evidence, not a model-generated confirmation field.

A separate management daemon or generic job framework is unnecessary for the
five scoped actions. Reuse the sync worker and factor existing CLI logic into
shared helpers only where the request path needs it.

## Consequences and acceptance

The caller uses more than one bounded tool call to prepare and submit work,
but needs no private store, signing key, remote endpoint, or arbitrary CLI
access. Unused drafts require expiry and storage limits. State restoration
requires the existing incarnation recovery procedure before management can
execute again.

G19 must cover response loss, exact lookup, duplicate submissions, control
under saturation, recovery, policy revocation, retention, restore, and real
paired execution. E23 completion provides a provider handoff to Plugin
TASK-028; it does not authorize Plugin mutations or change EPIC-007's
inspection-only contract. ADR-0023 through ADR-0025 remain unchanged.

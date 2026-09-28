# Architecture

This directory owns the current component model, boundaries, data flow, state
model, security posture, and operational design. Normative behavior remains in
`docs/specs/required-spec.md`; accepted rationale remains in
`docs/architecture-decision-records/`.

Start with [`architecture-overview.md`](architecture-overview.md). For the multi-destination flow, read
[`multi-destination-operational-loop.md`](multi-destination-operational-loop.md),
then the source, persistence, reconciliation, Hermes, observability, and
security documents for the owning subsystem.

For the v0.2.0 two-node Git sync loop, read [`wiki-sync.md`](wiki-sync.md).
E21 provides local control, membership, signed publication, checkpoints, and
guarded import. E22 adds authenticated peer admission, periodic configured-ref
recovery, fresh pair verification, and managed peer services on macOS and Linux.
E22-T5 qualified the disposable two-node system on the final committed code
revision; the release target remains unpublished.

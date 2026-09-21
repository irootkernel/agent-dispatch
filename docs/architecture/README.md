# Architecture

This directory owns the current component model, boundaries, data flow, state
model, security posture, and operational design. Normative behavior remains in
`docs/specs/required-spec.md`; accepted rationale remains in
`docs/architecture-decision-records/`.

Start with [`architecture-overview.md`](architecture-overview.md). For the multi-destination flow, read
[`multi-destination-operational-loop.md`](multi-destination-operational-loop.md),
then the source, persistence, reconciliation, Hermes, observability, and
security documents for the owning subsystem.

For the partially implemented v0.2.0 two-node Git sync loop—local control,
membership, signed publication, and checkpoints are present while import and
peer operations remain planned—read [`wiki-sync.md`](wiki-sync.md).

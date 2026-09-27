# Contracts

The [sync provider v1 bundle](sync-provider-v1/bundle.json) pins the versioned
v0.2.0 command tree, peer routes, machine results, canonical schemas, and their
checksums. Run `make sync-contract-check` after changing any included artifact.
Regenerate the ordered checksum file from `bundle.json` with
`make sync-contract-update`; do not edit it by hand.

This collection contains normative interfaces, records, configuration, CLI,
adapter boundaries, and the closed error model. It is subordinate to
`docs/specs/required-spec.md` and is part of the specifications role rather
than a separate lifecycle authority.

- [`canonical-record-contracts.md`](canonical-record-contracts.md)
- [`configuration-spec.md`](configuration-spec.md)
- [`cli-spec.md`](cli-spec.md)
- [`error-model.md`](error-model.md)
- [`hermes-task-contract.md`](hermes-task-contract.md)
- [`source-adapter-contract.md`](source-adapter-contract.md)
- [`sink-adapter-contract.md`](sink-adapter-contract.md)
- [`sync-contract.md`](sync-contract.md)

The sync contract defines the v0.2.0 command and protocol identities. The v1
command identities are implemented through E22-T4; the capability surface
continues to report future behavior as unavailable until its owning task
delivers it.

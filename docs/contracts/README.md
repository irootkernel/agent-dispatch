# Contracts

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

The sync contract reserves the v0.2.0 command and protocol identities. Its
capability surface must report unimplemented behavior honestly until the owning
roadmap tasks deliver it.

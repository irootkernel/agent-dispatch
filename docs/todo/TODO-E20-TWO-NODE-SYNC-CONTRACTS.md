# E20 Two-Node Sync Contracts and Admission

> **Lifecycle:** Active execution dossier for roadmap epic E20
> **Lifecycle authority:** [`docs/roadmap/roadmap.md`](../roadmap/roadmap.md)
> **Consumer epic:** E20
> **Closeout:** Remove this dossier after E20 is validated and its durable
> outcomes are linked from the roadmap.

## Goal and boundary

E20 freezes and qualifies the contract-only baseline for the v0.2.0 two-node
Markdown Wiki sync feature. It defines versioned records, closed command and
peer contracts, opt-in configuration, and truthful disabled capability output.
It does not implement Git publication or import, open a listener, install a
service, activate production sync, change the Hermes Plugin, or publish a
release.

The admitted product boundary is owned by [D-030](../specs/decision-log.md),
[`SYN-*`](../specs/required-spec.md),
[ADR-0023 through ADR-0025](../architecture-decision-records/README.md), the
[sync architecture](../architecture/wiki-sync.md), and the
[sync contract](../contracts/sync-contract.md). This dossier integrates those
authorities; it does not replace them.

## Ordered work and ownership

| Task | Current requirement owners | Required outcome |
|---|---|---|
| E20-T1 (completed, reference only) | `D-030`, `SYN-*`, G16-G18, ADR-0023 through ADR-0025, and the E20-E22 roadmap | Admitted two-node boundary, frozen decisions, and canonical authorities consumed by the remaining E20 tasks |
| E20-T2 | `SYN-005`, `SYN-008`-`SYN-013`, `SYN-015`; data and durability requirements linked by the roadmap | Versioned records, closed states and reasons, bounds, and positive/negative fixtures |
| E20-T3 | `SYN-005`-`SYN-008`, `SYN-013`-`SYN-015`; CLI and security requirements linked by the roadmap | Checksummed provider bundle with fixed CLI/peer descriptors, schemas, fixtures, errors, and unavailable results |
| E20-T4 | `SYN-001`-`SYN-006`, `SYN-009`, `SYN-014`, `SYN-015`; configuration and security requirements linked by the roadmap | Disabled-by-default configuration, revision and acknowledgement semantics, and side-effect-free capabilities/status |
| E20-T5 | `SYN-*`, G16 / AC-1601 through AC-1605 | Exact bundle qualification, full repository verification, review settlement, and E21-T1 handoff |

Tasks remain strictly ordered by the roadmap. Each task owns its canonical
documentation, tests, generated traceability and manifest changes, lifecycle
transition, review settlement, and isolated commit. E20-T5 hands off only to
E21-T1; E21 and E22 retain all runtime Git, persistence, listener, service, and
live-tree effects assigned to them.

## Cross-document acceptance

- Existing v0.1.8 configurations and ordinary routes remain unchanged when the
  sync block is absent or disabled.
- Contract validation rejects unknown fields and enums, invalid identities or
  refs, unpinned or self-authorizing trust, stale predecessors, obsolete
  incarnations, unsupported scope, and checksum drift.
- Configuration validation and read-only sync commands perform no Git,
  network, listener, service, activation, or live-tree side effect.
- Capability output distinguishes implemented contract-only reads from every
  reserved runtime action. Schema presence never advertises implementation.
- G16 closes only on the exact reviewed revision with repository verification,
  deterministic bundle validation, and every finding dispositioned without
  weakening a Must requirement.

## Verification and handoff

Focused schema, configuration, CLI, and contract tests run before the root
`make verify` gate. The configured Gaori commands `manifest-check`,
`schema-validation`, and `traceability` provide additional bounded evidence at
E20-T5. Static review covers the exact candidate against every applicable
criterion. Live two-node, Tailscale, Git push/import, service installation,
Plugin compatibility, and production activation remain explicit later-epic or
release evidence, not E20 completion evidence.

After whole-epic validation, promote durable information to its canonical
specification, contract, architecture, implementation, operations, validation,
and roadmap owners. Then remove this dossier and its TODO index entry while
preserving the roadmap's Canonical Outcomes links.

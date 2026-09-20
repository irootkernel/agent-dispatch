# E21 Signed Publication and Guarded Import

> **Lifecycle:** Active execution dossier for roadmap epic E21
> **Lifecycle authority:** [`docs/roadmap/roadmap.md`](../roadmap/roadmap.md)
> **Consumer epic:** E21
> **Closeout:** Remove this dossier after E21 is validated and its durable
> outcomes are linked from the roadmap.

## Goal and boundary

E21 implements and qualifies the durable manual publication and guarded
fast-forward import substrate for the v0.2.0 two-node Markdown Wiki sync
feature. It persists intent before effects, separates signed membership
administration from publication authority, freezes eligible Markdown snapshots,
confirms fast-forward publication, guards imports, attributes controller effects
exactly, and stops on conflicts or uncertain recovery.

E21 does not open a peer listener, install or operate a managed service, add
periodic pair verification, activate production sync, change the Hermes Plugin,
or publish a release. N-member groups, attachments and other non-Markdown
content, automatic merge, rebase, stash, force, reset, and clean remain outside
the admitted boundary. Qualification uses disposable repositories rather than
production vaults, credentials, remotes, tags, or releases.

The admitted product boundary is owned by [D-030](../specs/decision-log.md),
[`SYN-*`](../specs/required-spec.md),
[ADR-0023 through ADR-0025](../architecture-decision-records/README.md), the
[sync architecture](../architecture/wiki-sync.md), the
[sync contract](../contracts/sync-contract.md), and
[G17](../specs/acceptance-criteria.md). This dossier integrates those
authorities; it does not replace them.

## Ordered work and ownership

| Task | Current requirement owners | Required outcome |
|---|---|---|
| E21-T1 | `SYN-002`, `SYN-008`-`SYN-012`, `SYN-015`, `DUR-001`, `DUR-005`, `DUR-010`-`DUR-012` | Durable sync jobs, journals, idempotency, fenced ownership, pause/resume control, migration safety, and retention |
| E21-T2 | `SYN-002`-`SYN-005`, `SYN-011`, `SYN-015`, `SEC-001`-`SEC-007`, `AC-1708` | Bounded typed Git operations, signed two-member administration, separated roles, and hostile-environment controls |
| E21-T3 | `SYN-003`-`SYN-005`, `SYN-008`, `SYN-011`, `SYN-012`, `AC-1701`-`AC-1704`, `AC-1709`, `AC-1711`, `FBK-002`, `FBK-004` | Explicit eligible snapshots, signed append-only publication, fast-forward confirmation, recovery, and no-op or late-edit preservation |
| E21-T4 | `SYN-003`, `SYN-005`, `SYN-009`-`SYN-012`, `AC-1705`-`AC-1710`, `FBK-002`-`FBK-004` | Trusted guarded import, checkpoint-backed reconciliation, crash recovery, and exact Watchman attribution |
| E21-T5 | `SYN-002`-`SYN-005`, `SYN-009`-`SYN-012`, `SYN-014`, `SYN-015`, `AC-1701`-`AC-1711`, `TST-002`, `TST-007` | G17 qualification, cross-platform Git evidence, truthful capability reporting, review settlement, and E22-T1 handoff |

Tasks remain strictly ordered by the roadmap. Each task owns its canonical
documentation, tests, generated traceability and manifest changes, lifecycle
transition, review settlement, and isolated commit. E21-T5 hands off only to
E22-T1; listener, service, periodic verification, fresh-pair operations, and
live two-node qualification remain E22 work.

## Cross-document acceptance

- Sync intent, local state, and recovery evidence become durable before their
  external effects. Stable identities and fencing reject stale writers, and an
  ambiguous outcome remains explicit uncertainty until reconciled.
- The Git adapter exposes only fixed, bounded operations. Membership remains
  exactly two active members, updates require the expected predecessor and an
  administrator signature, publisher authority stays separate, and emergency
  revocation blocks protected effects.
- Publication accepts only an explicit eligible frozen snapshot, signs it, and
  confirms a fast-forward result. A no-op creates no commit or nudge, later
  local edits remain intact, and recovery reuses the signed publication
  identity.
- Import requires the current acknowledgement, actual remote digest, and
  current trust state. Unacknowledged, overlapping, divergent, or unsafe work
  is deferred without changing live files; proven-disjoint edits remain dirty
  and byte-identical.
- Import evidence suppresses only the exact attributed Watchman effects.
  Contradictory or late observations remain dirty, and every injected partial
  effect resolves to proven recovery or explicit uncertainty.
- G17 closes only on the exact reviewed revision using disposable repositories
  on every supported platform, with no production asset used as evidence.

## Verification and handoff

Focused real-SQLite migration, concurrency, fencing, retention, and recovery
tests precede hostile and disposable Git tests. Publication and import testing
covers crash boundaries, conflicts, unsafe paths, signatures, no-op behavior,
and late or disjoint edits. E21-T5 records cross-platform Git behavior, keeps
capability and status output truthful, runs the root `make verify` gate, and
uses the configured Gaori commands `manifest-check`, `schema-validation`, and
`traceability` as additional bounded evidence. Static review covers the exact
candidate against every applicable G17 criterion.

After whole-epic validation, promote durable information to its canonical
specification, contract, architecture, implementation, operations, validation,
and roadmap owners. Then remove this dossier and its TODO index entry while
preserving the roadmap's Canonical Outcomes links.

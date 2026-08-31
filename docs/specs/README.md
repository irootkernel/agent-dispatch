# Specifications

This directory is the canonical owner of required and implemented product
behavior. `required-spec.md` is the highest-precedence normative document;
accepted ADRs and the contract collection refine it without overriding it.

## Authority Index

- [`project-charter.md`](project-charter.md): product purpose, scope, and ownership boundary.
- [`required-spec.md`](required-spec.md): stable normative requirements.
- [`terminology.md`](terminology.md): canonical terms.
- [`acceptance-criteria.md`](acceptance-criteria.md): cumulative release gates.
- [`traceability-matrix.md`](traceability-matrix.md): generated requirement-to-task mapping.
- [`decision-log.md`](decision-log.md): append-only SOT admission, amendment, and errata decisions.
- [`v0.1.6-operational-follow-up.md`](v0.1.6-operational-follow-up.md): approved detailed contract for the planned v0.1.6 operational follow-up.

## v0.1.5 Feature-to-Authority Map

| Capability group | Normative groups | Acceptance gate | Roadmap owner |
|---|---|---|---|
| Watchman binding and reconciliation fencing | `SRC-*`, `PTH-*`, `DUR-*`, `OPS-*` | G6 | E10 |
| Hermes capability preflight and guided setup | `HER-*`, `CLI-*`, `SEC-*`, `OPS-*` | G7 | E11 |
| Aggregate fan-out and bounded completion | `FAN-*`, `CON-*`, `FBK-*`, `DAT-*` | G8 | E12 |
| Durable notifications and release proof | `NTF-*`, `DUR-*`, `SEC-*`, `OPS-*`, `TST-*` | G9 | E13 |

D-025 owns the approved product decision. Detailed design lives under
`docs/architecture/` and `docs/architecture-decision-records/`; interface and
record shapes live under `docs/contracts/`. `docs/roadmap/roadmap.md` alone
owns task identity, ordering, dependencies, and current status.

The v0.1.5 feature baseline is executable proof: the owning E10-E13 tasks
delivered the schemas, examples, packaged skills, code, and the local
v0.1.5 release, with the G6-G9 gate evidence recorded in
[`../VALIDATION.md`](../VALIDATION.md). v0.1.5 is the published latest
release (2026-08-30).

## v0.1.6 Planned Feature-to-Authority Map

| Capability group | Normative groups | Acceptance gate | Roadmap owner |
|---|---|---|---|
| Route-correct, rerunnable guided setup | `CLI-*`, `DUR-*`, `OPS-*`, `TST-*` | G10 | E14 |
| Optional Hermes mutex and local serialization groups | `HER-*`, `CON-*`, `DUR-*`, `TST-*` | G11 | E15 |
| Automatic durable notification draining | `NTF-*`, `DUR-*`, `CLI-*`, `OPS-*`, `TST-*` | G12 | E16 |
| Documentation, cold validation, and release proof | all applicable groups | G13 | E17 |

D-027 owns this approved plan, with the detailed contract in
[`v0.1.6-operational-follow-up.md`](v0.1.6-operational-follow-up.md) and
ADRs 0020 through 0022. E14's capabilities are implemented, validated,
and evidenced at gate G10 (closed 2026-08-31); E15 through E17 remain
Planned, and v0.1.5 remains the published latest release.

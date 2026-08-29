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
v0.1.5 release candidate, with the G6-G9 gate evidence recorded in
[`../VALIDATION.md`](../VALIDATION.md). v0.1.4 remains the latest pushed
release until the operator publishes the candidate.

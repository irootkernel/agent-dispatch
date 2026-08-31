# Agent Dispatch Documentation

> **Documentation profile:** Single delivery scope with an adopted legacy roadmap identity contract
> **SOT version:** 1.2.0
> **Shipped implementation:** Agent Dispatch v0.1.5 (published 2026-08-30)
> **Planned release target:** Agent Dispatch v0.1.6
> **Document status:** Approved planned baseline
> **Language:** English

This directory is the canonical specification package for Agent Dispatch. It
owns product requirements, architecture, decisions, delivery state, future
candidates, deferred findings, executable contracts, and package evidence.
Hermes remains the authoritative agent runtime; Agent Dispatch does not modify
Hermes core, private storage, versions, tags, or plugin surfaces.

## Canonical Role Owners

| Semantic role | Canonical owner | Responsibility |
|---|---|---|
| Specifications | [`specs/`](specs/README.md) | Required and implemented behavior, release scope, terminology, acceptance, and traceability |
| Architecture | [`architecture/`](architecture/README.md) | Current components, boundaries, data flow, and responsibilities |
| Architecture decision records | [`architecture-decision-records/`](architecture-decision-records/README.md) | Accepted, superseded, rejected, and proposed design decisions |
| Implementation tips | [`implementation-tips/`](implementation-tips/README.md) | Non-normative implementation, testing, migration, and release guidance |
| Roadmap | [`roadmap/`](roadmap/README.md) | Adopted epic/task identity, order, dependencies, lifecycle, and current status |
| Deferred feedback | [`deferred-feedback/`](deferred-feedback/README.md) | Small actionable findings intentionally postponed from current work |
| TODO | [`todo/`](todo/README.md) | Future epic-sized candidates not yet admitted to the roadmap |

There is one delivery scope. Its canonical roadmap namespace is
`docs/roadmap/roadmap.md`; no other document may own work-unit status.

## Supporting Collections

- [`contracts/`](contracts/README.md) contains normative interface and record
  contracts subordinate to `specs/required-spec.md`.
- [`operations/`](operations/README.md) contains non-normative installation,
  recovery, retention, and runbook guidance under the implementation-tips role.
- [`source/`](source/README.md) preserves non-authoritative source inputs and
  decision-resolution provenance.
- [`schemas/`](schemas/), [`examples/`](examples/), and [`skills/`](skills/)
  are executable or packaged contract artifacts owned by their roadmap tasks.
- [`integrations/`](integrations/) contains bounded external-interface evidence;
  [`VALIDATION.md`](VALIDATION.md), [`CHANGELOG.md`](CHANGELOG.md), and the
  release notes record package validation and release history.

## Source-of-Truth Precedence

When documents conflict, use this order:

1. [`specs/required-spec.md`](specs/required-spec.md)
2. Accepted ADRs under [`architecture-decision-records/`](architecture-decision-records/README.md)
3. Normative documents under [`contracts/`](contracts/README.md)
4. Current design under [`architecture/`](architecture/README.md)
5. [`roadmap/roadmap.md`](roadmap/roadmap.md) for adopted identity, order, dependencies, and status
6. Implementation and operations guidance
7. Schemas, examples, packaged skills, and integration evidence within their explicitly stated version scope
8. Original inputs under [`source/`](source/README.md)

The roadmap exclusively owns lifecycle truth even when a higher-ranked product
document describes intended behavior. Source inputs remain provenance and do
not override a resolved requirement or decision.

## Roadmap Identity Contract

- Established epic IDs use `E<n>` and task IDs use `E<n>-T<n>`.
- Epic numbers increase monotonically; task numbers increase within their epic.
- An allocated number is never reused, and task identity does not encode
  execution order beyond the explicit order and dependency fields in the roadmap.
- Lifecycle values remain `Planned`, `In Progress`, `In Review`, `Completed`,
  `Deferred`, and `Blocked`.
- This documentation migration does not rename any work unit or change any
  lifecycle state.

## v0.1.5 Baseline

D-025 approves v0.1.5 as planned work across source and reconciliation
integrity, Hermes capability-driven preflight and setup, multi-destination
lifecycle, bounded completion evidence, durable notifications, and release
proof. The feature-to-authority map is in [`specs/README.md`](specs/README.md),
and the only execution/status authority is [`roadmap/roadmap.md`](roadmap/roadmap.md).

Gates G6 through G9 carry their executable evidence: the G6 through G8
rows live in [`VALIDATION.md`](VALIDATION.md) with their E10-E12 suites, and
G9 (notifications and the release proof) closed with E13 on 2026-08-30 —
the notification outbox, sinks, retry and drain surface, skills, and the
two byte-identical `make release VERSION=v0.1.5` builds recorded there.
v0.1.5 is the published latest release (2026-08-30): the candidate was
re-cut from the post-validation final tree and published with its tag
and hosted Release.

## v0.1.6 Planned Follow-up

D-027 approves four sequential planned epics, E14 through E17, to close the
three operational gaps found during a real v0.1.5 deployment: route-correct
and rerunnable disabled setup, explicit Hermes v0.20.5 mutex downgrade through
Agent Dispatch serialization groups, and bounded post-commit notification
draining. The detailed contract is
[`specs/v0.1.6-operational-follow-up.md`](specs/v0.1.6-operational-follow-up.md),
with acceptance gates G10 through G13.

The adoption itself was documentation only and claimed no implementation,
migration, test, deployment-transcript, artifact, or release evidence at
the time. E14 has since delivered and evidenced its share — the explicit
setup route selection, the disabled baseline-only reconciliation, and
the rerunnable five-state walkthrough closed gate G10 on 2026-08-31 —
and E15 closed 2026-08-31: the Hermes v0.20.5 floor with certified
serialization modes, one active child per serialization group, and the
real-environment G11 walkthrough live in the roadmap's Canonical
Outcomes links. E16 and E17 remain Planned; v0.1.5 remains the shipped
release and E16-T1 is the next roadmap task. The separately requested
general `make verify` remediation is outside this release scope.

## Repository-Native Checks

Run `make verify` at the repository root. It is the single deterministic
entrypoint for format, vet, staticcheck, import direction, unit and race tests,
manifest checksums, schema/example validation, traceability regeneration, and
schedule validation. See [`VALIDATION.md`](VALIDATION.md) for bounded evidence
and reproduction details.

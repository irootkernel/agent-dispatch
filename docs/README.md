# Developer and Contributor Documentation

Agent Dispatch has one delivery scope: the CLI and its local Watchman, SQLite,
Hermes, and launchd integrations. This directory is its canonical specification
and maintainer documentation package. For product introduction, installation,
and everyday use, start with the [project README](../README.md).

**Profile:** `single-scope`, with preserved legacy roadmap identities.

**Language:** English.

**Documentation basis:** current source, including E18 / SOT 1.4.0, tracked under
[v0.1.7 - Unreleased](../CHANGELOG.md#v017---unreleased). Versioned release notes and
validation records describe their own snapshots, not proof for the current HEAD.

## Start Here

1. [Contributor setup](implementation-tips/getting-started.md): build, locate code,
   choose the relevant tests, and verify a change.
2. [Project charter](specs/project-charter.md) and
   [terminology](specs/terminology.md): purpose and domain language.
3. [Architecture overview](architecture/architecture-overview.md) and
   [repository layout](implementation-tips/repository-layout.md): components and code.
4. [Required specification](specs/required-spec.md),
   [contracts](contracts/README.md), and [accepted decisions](architecture-decision-records/README.md):
   the behavior a change must preserve.
5. [Canonical roadmap](roadmap/roadmap.md): task ownership, dependencies,
   status, and links to delivered outcomes.

## Find Documentation by Work

| Work | Start with |
|---|---|
| Change ingestion, dispatch, or reconciliation | [Architecture](architecture/README.md), [specifications](specs/README.md) |
| Change CLI, configuration, or persisted records | [Contracts](contracts/README.md), [schemas](schemas/README.md), [examples](examples/README.md) |
| Change Hermes or Watchman integration | [Integration evidence](integrations/README.md), [packaged skills](skills/README.md) |
| Add or run tests | [Testing strategy](implementation-tips/testing-strategy.md), [validation evidence](VALIDATION.md) |
| Operate, upgrade, or recover an installation | [Operations](ops/README.md) |
| Prepare a release | [Release guide](implementation-tips/release-guide.md) |
| Propose future work or record a postponed finding | [TODO](todo/README.md), [deferred feedback](deferred-feedback/README.md) |

## Canonical Role Owners

| Semantic role | Canonical owner | Responsibility |
|---|---|---|
| Specifications | [specs/](specs/README.md) | Required and implemented behavior, terminology, acceptance, traceability |
| Architecture | [architecture/](architecture/README.md) | Components, boundaries, data flow, responsibilities |
| Architecture decision records | [architecture-decision-records/](architecture-decision-records/README.md) | Decisions and their accepted, superseded, rejected, or proposed status |
| Implementation tips | [implementation-tips/](implementation-tips/README.md) | Development, testing, migration design, release engineering |
| Operations | [ops/](ops/README.md) | Host setup, scheduling, diagnosis, upgrades, backup, recovery |
| Roadmap | [roadmap/](roadmap/README.md) | Adopted identity, ordering, dependencies, lifecycle, current status |
| TODO | [todo/](todo/README.md) | Future epic-sized candidates and any explicitly adopted temporary dossiers |
| Deferred feedback | [deferred-feedback/](deferred-feedback/README.md) | Small actionable postponed findings |

Operations are owned by the operator of the local installation; maintainers own
its documented CLI behavior and recovery procedures. Hermes owns its runtime,
profiles, skills, and public Kanban surface. Agent Dispatch does not manage
Hermes core or private storage.

## Supporting Collections

- [contracts/](contracts/README.md), [schemas/](schemas/README.md), and
  [examples/](examples/README.md) refine or illustrate the specifications.
  Scheduling examples are maintained with the operations role.
- [skills/](skills/README.md) packages public integration guidance under the
  Hermes contract; it grants no runtime authority.
- [integrations/](integrations/README.md) contains bounded evidence supporting
  integration contracts. Captured fixtures keep their recorded version scope.
- [source/](source/README.md) preserves non-authoritative original inputs.
- [Product changelog](../CHANGELOG.md) is the sole release-note source. GitHub
  Release descriptions use its version sections; the [v0.1.6 section](../CHANGELOG.md#v016---2026-09-02)
  describes the release preceding E18.
- [SOT-CHANGELOG.md](SOT-CHANGELOG.md) records specification-package version
  history, under the specifications role. [VALIDATION.md](VALIDATION.md) owns
  dated validation evidence. Neither is another product release changelog.
- `scripts/generate-traceability.py` is developer tooling;
  `MANIFEST.sha256` and `specs/traceability-matrix.md` are generated package artifacts.
  Ignored runtime logs and workflow evidence are not documentation authorities.

## Source-of-Truth Precedence

1. [Required specification](specs/required-spec.md).
2. Accepted [ADRs](architecture-decision-records/README.md).
3. Normative [contracts](contracts/README.md).
4. Current [architecture](architecture/README.md).
5. [Roadmap](roadmap/roadmap.md) for adopted identity, order, dependencies, and status.
6. Implementation and operations guidance, including public usage summaries.
7. Schemas, examples, packaged skills, and evidence within their stated version scope.
8. Original [source inputs](source/README.md).

The roadmap exclusively owns lifecycle truth regardless of the product precedence
above. Historical evidence does not certify a newer implementation. When code
and a contract disagree, report and resolve the mismatch rather than silently
changing the contract to match the code.

## Roadmap Identity and Dossiers

The canonical namespace is `docs/roadmap/roadmap.md`. Established IDs remain
`E<n>` for epics and `E<n>-T<n>` for tasks. Epic numbers increase monotonically;
task numbers increase within their epic. Allocated numbers are never reused.
Explicit order and dependencies determine execution order.

Lifecycle values remain `Planned`, `In Progress`, `In Review`, `Completed`,
`Deferred`, and `Blocked`. TODO candidates and deferred findings do not create a
second status authority. Preserve existing Canonical Outcomes links and the
repository's dossier closeout convention: durable results belong to their
canonical role, and retired temporary dossiers are not recreated by docs setup.

## Path Migration

This reorganization moves the five files formerly in `docs/operations/` to
`docs/ops/`, keeping their filenames: `README.md`, `installation.md`,
`runbook.md`, `failure-recovery.md`, and `retention-and-privacy.md`.
Current references, including roadmap outcome links, use the new paths.
This paragraph records the old path for discovery; it is not a second owner.
Roadmap identities, lifecycle, and historical evidence are preserved apart from
necessary path references. Contracts, schemas, examples, and integration fixtures keep their established
locations. Product notes formerly in `docs/RELEASE-NOTES-v*.md` are consolidated
in the matching `CHANGELOG.md` version sections at the repository root.
The former `docs/CHANGELOG.md` is now `docs/SOT-CHANGELOG.md`; its SOT version
identities and history remain unchanged apart from necessary path references.

## Documentation Checks

Run `make verify` at the repository root. It covers build, format, vet,
staticcheck, import direction, unit and race tests, manifest verification,
schema/example validation, traceability regeneration, and scheduling artifacts.
The [contributor guide](../CONTRIBUTING.md) explains generated-file handling.
Review Markdown links, anchors, command examples, and version claims as well:
checksums and schema validation do not prove prose correctness.

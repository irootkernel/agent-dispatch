# Repository Layout

The current implementation is a Go module with one CLI. Start at
`cmd/agent-dispatch/main.go`, then follow the command into `internal/cli` and
its application service. The [architecture overview](../architecture/architecture-overview.md)
explains runtime responsibility and data flow.

| Location | Responsibility |
|---|---|
| `cmd/agent-dispatch/` | Executable entry point |
| `internal/cli/` | Command parsing, operator output, and public CLI gate tests |
| `internal/app/` | Ingest, dispatch, reconciliation, receipts, work receipts, quarantine, maintenance, doctor, notifications |
| `internal/domain/` | IDs, records, policy, state, and fingerprints |
| `internal/ports/` | Behavioral interfaces by domain family |
| `internal/adapters/` | Watchman, local files, SQLite, Hermes Kanban/webhook, notification sinks, secret resolution |
| `internal/config/` | Configuration loading, semantic checks, revisions, and disabled example generation |
| `internal/observability/`, `internal/platformpaths/`, `internal/version/` | Diagnostics, platform paths, and product/build identity |
| `internal/schemavalid/`, `internal/tools/`, `internal/importlint/` | Schema engine, check entrypoints, and package import enforcement |
| `internal/testsupport/` | Crash binaries, fake sinks, stub Hermes, and isolated Hermes environments |
| `docs/` | Canonical contracts, design, operations, roadmap, and evidence |
| `Makefile`, `go.mod`, `go.sum` | Build/verification entrypoints and pinned dependencies/tools |

SQL migrations live in `internal/adapters/sqlite`, not a top-level migration
package. Hand-maintained schemas and examples remain in `docs/schemas` and
`docs/examples`. Python traceability tooling lives in `docs/scripts`. Gate tests
live beside the packages they verify, particularly `internal/cli` and the relevant
app/adapter packages; test data lives beside its owning tests or in the recorded
`docs/integrations/fixtures` corpus.

`bin/` and `dist/` are generated build/release output. Ignored machine-local
configuration, workflow runtime history, and provider logs are not source or
documentation authorities. Some reserved packages and `test/e2e` / `test/helpers`
retain `doc.go` placeholders; their existence does not certify an implemented
feature or end-to-end suite.

## Package Rules

- Domain logic does not import application services, configuration, CLI, adapters,
  observability, or platform paths.
- Ports define behavior interfaces; source/target DTOs stay in their adapters,
  and SQL statements and rows stay in SQLite.
- Configuration becomes immutable domain snapshots before application use.
- Schemas, example records, and their Go producers/consumers change together
  under the owning contract, with observable compatibility tests.

`make check-imports` enforces the exact directions: `internal/domain` cannot
import `internal/{app,config,cli,adapters,observability,platformpaths}`;
`internal/ports` cannot import `internal/{app,adapters,config,cli,observability}`;
and `internal/config` cannot import `internal/{app,cli,adapters,observability}`,
including the family root packages. The check is part of `make verify`.

## Documentation Placement

Use the [documentation role map](../README.md#canonical-role-owners) to select
one owner. Requirements and contracts describe behavior, architecture describes
structure, ADRs explain decisions, implementation tips describe development and
release engineering, and ops contains installation and recovery procedures.

Keep user-facing setup in the root README. Only the canonical roadmap owns
adopted task state; temporary dossiers follow its existing closeout convention.
Preserve dated integration reports and fixtures when collecting new evidence,
with explicit snapshot/version scope for each new record. Generated traceability
and checksums must be reviewed after regeneration.

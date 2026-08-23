# Repository Layout

Recommended v0.1 repository structure:

```text
agent-dispatch/
├── cmd/
│   └── agent-dispatch/
│       └── main.go
├── internal/
│   ├── app/
│   │   ├── ingest/
│   │   ├── dispatch/
│   │   ├── reconcile/
│   │   ├── workreceipt/
│   │   ├── quarantine/
│   │   ├── maintenance/
│   │   └── doctor/
│   ├── domain/
│   │   ├── ids/
│   │   ├── records/
│   │   ├── policy/
│   │   ├── state/
│   │   ├── fingerprint/
│   │   └── errors/
│   ├── ports/
│   │   ├── source.go
│   │   ├── sink.go
│   │   ├── store.go
│   │   ├── filesystem.go
│   │   ├── process.go
│   │   ├── secrets.go
│   │   ├── clock.go
│   │   └── git.go
│   ├── adapters/
│   │   ├── watchman/
│   │   ├── localfs/
│   │   ├── sqlite/
│   │   ├── hermeskanban/
│   │   ├── hermeswebhook/
│   │   ├── processrunner/
│   │   ├── secretresolver/
│   │   └── gitlocal/
│   ├── config/
│   ├── cli/
│   ├── observability/
│   └── platformpaths/
├── migrations/
├── schemas/
├── examples/
├── docs/
├── test/
│   ├── e2e/
│   └── helpers/
├── testdata/
│   ├── watchman/
│   ├── hermes/
│   ├── filesystem/
│   └── sqlite/
├── scripts/
├── Makefile
├── go.mod
├── go.sum
├── LICENSE
└── README.md
```

## Package Rules

- `internal/domain` cannot import `internal/app`, `config`, `cli`, adapters, `observability`, or `platformpaths` (the full enforced set is listed below).
- `internal/ports` contains behavior interfaces, not shared dumping-ground DTOs.
- source-specific DTOs stay in `adapters/watchman`.
- target-specific DTOs stay in `adapters/hermeskanban` or `hermeswebhook`.
- SQL statements and row models stay in `adapters/sqlite`.
- config structs are converted to immutable domain snapshots before application use.
- generated JSON Schemas remain under top-level `schemas`; their source may be hand-maintained or generated, but drift tests are required.

The import-direction rules are enforced by `internal/importlint` through `make check-imports` (part of `make verify`): `internal/domain` may not import `internal/{app,config,cli,adapters,observability,platformpaths}`, `internal/ports` may not import `internal/{app,adapters,config,cli,observability}`, and `internal/config` may not import `internal/{app,cli,adapters,observability}`, including each family's root package.

Until generated schemas exist (E2 and later), the hand-maintained SOT schemas and examples remain under `docs/schemas` and `docs/examples` inside the checksummed docs package; the top-level `schemas/` directory appears with the first generated schema.

## Test Placement

- package unit tests beside code;
- cross-package integration tests under relevant adapter package or `internal/integrationtest` if necessary;
- end-to-end tests under `test/e2e` only after E4;
- immutable fixtures under `testdata` with provenance notes;
- crash helper binaries under `internal/testsupport` or `test/helpers`.

## Documentation Placement

This SOT directory layout should be preserved. Implementation-specific capability reports go under:

```text
docs/integrations/
  hermes-public-interface-report.md
  watchman-public-interface-report.md
```

Task evidence may go under:

```text
docs/reports/
  E2-T5-g1-acceptance.md
  E3-T5-g2-acceptance.md
  ...
```

Do not overwrite historical reports when rerunning a released gate; create a versioned report.


## Layout Deviations (E7-T10, M-31)

The v0.1 tree deviates from the layout above in six named places; each
deviation is intentional and explained here:

- `LICENSE` at the repository root was absent through v0.1.0 and added
  2026-08-23 (M-27), together with
  `docs/04-implementation/dependency-licenses.md`.
- `migrations/` is an empty untracked directory: SQL migrations live
  inside `internal/adapters/sqlite` (schema.go consts, migrate.go) by
  D-015's single-entrypoint design; the directory exists only for some
  tooling expectations and is not part of the package.
- `test/e2e` and `test/helpers` hold only `doc.go` placeholders: the
  gate suites (G1-G5) live beside the packages they verify
  (`internal/cli`, `internal/app/dispatch`), and the crash helpers live
  under `internal/testsupport` (crashbin, stubhermes, fakesink) so they
  build with the module's pinned toolchain.
- The ports are declared as `internal/ports/*.go` files by domain
  (dispatch, inspection, quarantine, coordination) rather than one file
  per interface; the per-domain split is the deliberate unit.
- `internal/adapters/gitlocal`, `internal/adapters/processrunner`, and
  `internal/domain/errors` are empty placeholder packages reserved by
  the repository layout for post-v0.1 sources (SCP-009 Git evidence,
  the reusable process runner, and the typed error domain); they carry
  only `doc.go`.
- `internal/adapters/watchman`, `internal/config`, and
  `internal/app/reconcile` retain their placeholder `doc.go` files
  alongside real implementations; the placeholders predate the code and
  are retained as package documentation anchors.

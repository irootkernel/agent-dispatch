# Repository Layout

Recommended v0.1 repository structure:

```text
jjukkumi/
├── cmd/
│   └── jjukkumi/
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

- `internal/domain` cannot import `internal/app`, `config`, `cli`, or adapters.
- `internal/ports` contains behavior interfaces, not shared dumping-ground DTOs.
- source-specific DTOs stay in `adapters/watchman`.
- target-specific DTOs stay in `adapters/hermeskanban` or `hermeswebhook`.
- SQL statements and row models stay in `adapters/sqlite`.
- config structs are converted to immutable domain snapshots before application use.
- generated JSON Schemas remain under top-level `schemas`; their source may be hand-maintained or generated, but drift tests are required.

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
  watchman-interface-report.md
```

Task evidence may go under:

```text
docs/reports/
  E2-T5-g1-acceptance.md
  E3-T5-g2-acceptance.md
  ...
```

Do not overwrite historical reports when rerunning a released gate; create a versioned report.

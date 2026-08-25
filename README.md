# Agent Dispatch

Agent Dispatch observes Watchman file-change events, plans safe deterministic
edits, and delivers them as durable Hermes Kanban tasks with verifiable
receipts. The source of truth for scope, contracts, and the roadmap lives
under [`docs/`](docs/README.md); start there.

## Repository layout

- `cmd/agent-dispatch/` CLI entry point.
- `internal/` application services, domain, ports, adapters, config, CLI,
  observability, and platform paths (see
  `docs/implementation-tips/repository-layout.md`).
- `internal/schemavalid/` Draft 2020-12 validator for the SOT schema and
  example documents.
- `internal/importlint/` package dependency-direction enforcement.
- `docs/` the SOT package: specification, ADRs, contracts, roadmap,
  schemas, examples, and integration evidence.
- `Makefile` the single verification entrypoint.
- GitHub Actions is not used; run `make verify` (format, vet, staticcheck, import lint, unit and race tests, manifest, schema, traceability, schedule-check) locally on the supported
  platform (macOS, darwin/arm64 — the only supported platform under the D-023 policy).

## Build and verify

```sh
make build        # build bin/agent-dispatch with version metadata
make verify       # every check: format, vet, staticcheck, import lint,
                  # unit tests, race tests, docs manifest checksums,
                  # schema/example validation, traceability regeneration
```

`agent-dispatch version --output json` reports build metadata in the CLI JSON
envelope.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). The roadmap under
`docs/roadmap/roadmap.md` is authoritative for task order and
status.

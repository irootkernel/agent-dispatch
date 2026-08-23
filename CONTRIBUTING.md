# Contributing

## Local verification

Every change must pass the single verification entrypoint (D-015):

```sh
make verify
```

This runs, in order: build, gofmt check, `go vet`, staticcheck (pinned
version), the package dependency-direction lint (`internal/importlint`),
unit tests, race tests, the docs package manifest checksum check, Draft
2020-12 schema/example validation (`internal/tools/schemavalid`),
traceability regeneration with a drift guard, and the scheduling-artifact
check (`make schedule-check`). Individual targets can be run directly,
for example `make schema-validation` or `make test-race`.

The docs package is checksummed: after editing anything under `docs/`,
regenerate `docs/MANIFEST.sha256` (see `docs/VALIDATION.md`) so
`make manifest-check` stays reproducible.

## Toolchain

Go and dependency versions are pinned in `go.mod` (SCP-005). The Python
traceability script requires python3; the retired Python schema validator
has been replaced by the Go Draft 2020-12 validator (D-015).

## Workflow rules

Task order, status vocabulary, transitions, and the single-active-task
rule are defined in `docs/docs/03-roadmap/task-execution-rules.md` and are
authoritative. Every task must add or update tests, documentation, and
traceability before completion (TST-009): update the roadmap status,
regenerate the traceability matrix (`make traceability`), and refresh the
docs manifest.

## Commit style

Use the observed `[<ID>] summary` convention, for example
`[E1-T1] Bootstrap Go repository and verification pipeline` for roadmap
task work or `[INT] ...` for internal tooling.

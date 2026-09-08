# Contributor Getting Started

This guide targets contributors changing Agent Dispatch on macOS arm64.
For installation and normal operation, use the [public README](../../README.md).

## Prepare and Build

Use Go 1.26.6 (the `go.mod` pin), Python 3, and macOS's `shasum` and `plutil`.
The Go command must have access to the pinned module dependencies; staticcheck
is already declared as a tool dependency. No global staticcheck install is needed.
Watchman and Hermes 0.20.5+ are prerequisites for real integration scenarios.

```sh
go version
python3 --version
make build VERSION=0.0.0-dev
./bin/agent-dispatch version --json
```

Building creates `bin/agent-dispatch`; it does not install or activate an instance.
Use explicit version metadata for a candidate build. The Makefile's default
version is a development placeholder, not a release selection.

## Understand One Change Path

Read the [architecture overview](../architecture/architecture-overview.md),
then locate the relevant layer in the [repository layout](repository-layout.md).
For a file-event change, follow the CLI into ingestion, domain policy, dispatch,
and the Watchman/SQLite/Hermes adapters. For command behavior, start in
`internal/cli` and the [CLI contract](../contracts/cli-spec.md).

Before implementing, identify the applicable requirement and acceptance scenario
in [specs](../specs/README.md), the accepted ADR, and the owning roadmap task.
Current task state is only in the [roadmap](../roadmap/roadmap.md).

## Develop and Verify

Keep unit and contract tests beside their implementation. Prefer the existing
fake ports and disposable integration helpers to introducing a new harness.
The [testing strategy](testing-strategy.md) explains the gate coverage.

`make test` and `make test-race` wrap tests in disposable HOME/Hermes roots while
preserving Go caches. Run these targets when testing integrations instead of
invoking a live operator instance. Test results must distinguish unavailable
external dependencies and skipped scenarios from completed real integration proof.

Run `make verify` for the complete check before handoff. Individual checks such as
`make check-imports` and `make schema-validation` are useful during iteration.
Use [CONTRIBUTING.md](../../CONTRIBUTING.md#refresh-the-documentation-package) to
refresh traceability and the manifest in the correct order.

## Handoff

Describe the changed behavior, verification commands and outcomes, and any
remaining limitations. Promote user-facing behavior to the root README,
contracts to their canonical owner, and operator recovery steps to
[ops](../ops/README.md). Keep historical gate evidence intact; a new test run
does not retroactively validate a previously published release.

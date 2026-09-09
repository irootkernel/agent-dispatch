# Contributing

Start with the [developer guide](docs/implementation-tips/getting-started.md)
for local setup and the [documentation index](docs/README.md) for design and
contract ownership. Repository-facing code, documentation, and review artifacts
use English.

## Development Environment

Use a supported platform (`darwin/arm64`, `linux/amd64`, or `linux/arm64`) and
the Go 1.26.6 toolchain pinned in `go.mod`. Python 3 runs the traceability
generator. Staticcheck is a pinned Go tool dependency. Watchman and Hermes are
needed for their real integration scenarios; use disposable fixtures and the
repository's test isolation, not production data.

```sh
make build
make verify
```

`make verify` is the single verification entrypoint (D-015): toolchain check,
build, format check, vet, staticcheck, import direction, unit and race tests,
docs manifest, schema/example validation, traceability, and scheduling artifacts.
It checks formatting without rewriting source. Traceability rewrites its generated
file and fails if the previous content was stale. Inspect the diff afterward.
GitHub Actions is not used; preserve the local verification result and its scope.

## Make a Change

1. Read the affected requirements, contracts, and accepted ADRs before editing.
2. Follow the [roadmap execution rules](docs/roadmap/task-execution-rules.md)
   for adopted task work. The [roadmap](docs/roadmap/roadmap.md) owns task order,
   lifecycle, and the single-active-task rule.
3. Test observable behavior and failure paths in the owning package. Keep
   platform and integration assumptions explicit.
4. Update durable documentation in its owning role. Update the public README
   when user-visible setup or usage changes. Record pending product changes in
   root [CHANGELOG.md](CHANGELOG.md) under `Unreleased` (or a selected version's
   `vX.Y.Z - Unreleased`). Keep `Added`, `Changed`, and `Fixed` entries concise;
   date released headings as `vX.Y.Z - YYYY-MM-DD` and use their content for GitHub
   Release descriptions. SOT versions have their separate history in
   [docs/SOT-CHANGELOG.md](docs/SOT-CHANGELOG.md). Keep captured evidence historical.
5. For roadmap work, update traceability and lifecycle only when the task's
   completion requirements are met. A standalone documentation reorganization
   does not create a task or change existing lifecycle state.
6. Refresh generated documentation, run verification, and inspect the final diff.

## Refresh the Documentation Package

After requirement or roadmap changes, run `make traceability` first. A drift
failure leaves the regenerated matrix for review; once accepted, rerun the target.
After all documentation edits, regenerate the checksums from the repository root:

```sh
python3 - <<'PYTHON'
from pathlib import Path
import hashlib
root = Path('docs')
paths = sorted(p for p in root.rglob('*') if p.is_file() and p.name != 'MANIFEST.sha256')
lines = [f'{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.relative_to(root).as_posix()}\n' for p in paths]
(root / 'MANIFEST.sha256').write_text(''.join(lines))
PYTHON
make verify
git diff --check
```

Review the document inventory before regenerating: only documentation package
files belong under `docs/`; keep local logs and temporary output elsewhere.
Check links and anchors for every moved file. The manifest proves byte integrity,
schema validation proves the covered contracts, and neither proves prose or live
integration behavior. See [VALIDATION.md](docs/VALIDATION.md) for dated evidence.

## Commit and Review

Follow [AGENTS.md](AGENTS.md). Commit subjects use `[E<n>] <summary>` for the
owning epic; bodies include `Confidence:`, `Scope-risk:`, `Reversibility:`, and
`Tested:` lines. Preserve unrelated changes and report any skipped verification.
Commit, push, release publication, and runtime installation are separate actions.
See the [release guide](docs/implementation-tips/release-guide.md) for release work.

# Release Engineering Guide

This is the repeatable process for maintainers preparing a release for
`darwin/arm64`, `linux/amd64`, and `linux/arm64`.
The [release checklist](release-checklist.md) is a dated v0.1.6 evidence record;
its checked boxes do not certify another candidate. Runtime upgrade and rollback
belong to [operations](../ops/installation.md#5-upgrade-ops-009).

## Current release and planned successor

E23, `SMR-*`, and G19 are planned work after the current v0.2.0 two-node
release. Their presence in the roadmap/specification does not add a v0.2.0
release blocker or make the planned management commands available. Freeze
and qualify the current Core/inspection-Plugin pair separately from E23
runtime changes.

Finalize the Core source commit, version, build time, and artifact SHA values
before the Plugin release owner updates its exact provider allowance and
qualifies that pair. The earlier EPIC-007 qualification used the reviewed
`6b1c78b` binaries; its evidence does not admit every build labelled v0.2.0.
A final commit/build-stamp change can change the binary even when Go source
is unchanged. Reconcile the allowance with the exact release artifacts and
qualify the final pair before publication.

Keep production on the released binaries and pinned Plugin source. E23
testing uses separate config, state, group, Git refs/remotes, and disposable
vaults. Do not run development migrations on the operating database. Real
vault activation remains separate from release and from E23 development.

## Select the Candidate

Record the explicitly selected version, full Git commit, and documentation scope.
Use the [roadmap](../roadmap/roadmap.md) for delivery status, the
[acceptance criteria](../specs/acceptance-criteria.md) for gates, and
[validation records](../VALIDATION.md) for evidence tied to an exact snapshot.
The selected release is [v0.2.0 - 2026-09-30](../../CHANGELOG.md#v020---2026-09-30),
adding the E20-E22 two-node sync scope to the v0.1.8 baseline. The dated
notes identify the candidate; publication requires the final gates below.

Reconcile public usage, contracts, compatibility, packaged skill versions, and
the root [changelog](../../CHANGELOG.md) with the candidate. Do not advance a versioned skill's compatibility
claim without its matching validation. Preserve historical version sections and evidence. Product changes use the root
changelog; specification-package revisions use [SOT-CHANGELOG.md](../SOT-CHANGELOG.md).

## Verify the Final Source

1. Review requirements, accepted ADRs, deferred findings, and current implementation.
2. Keep root `CHANGELOG.md` concise: use `Added`, `Changed`, and `Fixed` for
   user-visible outcomes and required upgrade actions. Until a version is selected,
   use `## Unreleased`; after selection, use `## vX.Y.Z - Unreleased`. Keep detailed
   validation and implementation evidence under `docs/`.
3. Refresh traceability and the documentation manifest using
   [CONTRIBUTING.md](../../CONTRIBUTING.md#refresh-the-documentation-package).
4. Run `make verify` on the final source. Report failures and skips separately.
5. Check the required real Watchman, Hermes, scheduling, and recovery gates for
   this change. Run authorized real tests only on disposable environments.
6. Finish the repository's applicable review and commit workflow. Record the
   accepted final commit; later edits require re-evaluating the affected evidence.

## Build Reproducible Artifacts

On the clean selected commit, set `release_version` to the approved tag string
before running these commands. `make release` replaces `dist/` on every run.

```sh
: "${release_version:?Set release_version to the approved tag first}"
make release VERSION="$release_version"
first_checksums=$(mktemp)
cp dist/SHA256SUMS "$first_checksums"
make release VERSION="$release_version"
cmp "$first_checksums" dist/SHA256SUMS
(cd dist &&
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 -c SHA256SUMS
  else
    sha256sum -c SHA256SUMS
  fi)
rm "$first_checksums"
```

Keep the first build's checksum evidence before the second invocation. Both
builds must use the same version, commit, commit date, and pinned toolchain.
The artifacts are bare binaries named `agent-dispatch-<version>-<os>-<arch>`
for all three supported pairs plus `SHA256SUMS`, not archives. `make release` does not run
`make verify`, create a Git tag, or publish a release.

## Publish and Record

Publication requires its own authorization. Verify the tag resolves to the
accepted commit and publish that commit's binaries and checksum list. Use the
approved changelog entry as the GitHub Release body, with no separately maintained
release-note file. Check the uploaded identity and artifact checksums before
recording publication as complete. Date the published entry as
`## vX.Y.Z - YYYY-MM-DD` using the actual release date. Open the next version's
`Unreleased` section only when that version is selected; otherwise keep an
unnumbered `Unreleased` section. Order versions newest first. The release artifact
test selects the greatest numeric dated version and ignores unreleased entries.

If builds or verification disagree, stop the release, retain the evidence, and
resolve the discrepancy. If a published artifact is wrong, maintainers decide
and document its withdrawal or replacement; do not silently rewrite a release
record or tag. Installing a release and enabling a production route are separate
operator actions, with backup and recovery covered by the operations owner.

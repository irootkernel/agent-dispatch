# DF-006: Exclude incidental files from manifest regeneration

Recorded 2026-09-28 from the E22-T5 corrected-target review.

**Affected authority.** `CONTRIBUTING.md` owns the documentation manifest
regeneration recipe; `docs/MANIFEST.sha256` is the checked package inventory.

**Bounded concern.** The recipe walks every regular file under `docs/`, so an
untracked editor backup or local log could enter the manifest if the required
inventory review is missed. The current E22-T5 manifest was regenerated from
its reviewed path list, contains the new qualification record, and passes
`make manifest-check`; no incidental file is present in this candidate.

**Reconsideration condition.** When the contributor recipe or documentation
package tooling next changes, derive the list from tracked paths plus
explicitly selected new documents, or reject incidental paths before writing
the manifest. The documentation tooling owner should preserve portable
checksum generation on all three supported platforms.

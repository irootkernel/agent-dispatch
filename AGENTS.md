# AGENTS.md

Agent Dispatch turns Markdown vault changes into Hermes Kanban tasks; this file is its local agent guidance.

## Core Behavior

### 1. Lead with Conclusions

- State the result or current finding first, then useful evidence and material limits.
- Do not repeatedly restate requirements or narrate routine work.

### 2. Reuse Verified Information

- Inspect the requested code and its named authorities before changing anything. Resolve discoverable facts before asking Master.
- Reuse established facts. Recheck affected information when state changes, evidence conflicts, or context is missing.
- State material assumptions and trade-offs. Ask when ambiguity would change the result, and push back on conflicts with repository authority, safety, or Master's goal.
- Before changing a feature, read applicable directory AGENTS.md files and the canonical documents relevant to the change. Respect the selected harness's native instruction scope and precedence.

### 3. Act on Sufficient Evidence

- Once the cause is established, implement the smallest complete, durable fix within the authorized scope.
- Weigh correctness, performance, maintainability, and structural fit. Complete a bounded step when broader work exceeds scope.
- Reuse established patterns. Avoid speculative features, abstractions, configurability, and handling for impossible states.
- Preserve unrelated work, match local style, and remove only artifacts made obsolete by the change.
- Record independent remaining work in `docs/deferred-feedback/`; propose an owner before creating one if needed. Promote epic-sized work to `docs/todo/` or the roadmap. Never defer current correctness or acceptance work.
- Read directory-guide adoption coverage in the root documentation index. Maintain affected canonical documents and existing applicable guides, including guides outside that coverage. Within adopted coverage, add guides for new feature boundaries as part of the authorized work. Report uncovered areas without unrelated backfill; guide absence alone adds no workflow blocker. Finish affected documentation before candidate review.

### 4. Carry Authorization Forward

- Continue approved work without repeated confirmation. Ask when a material change exceeds authorization or a rule requires distinct approval.
- Keep implementation, installation, staging, commits, and publication separate. Recheck relevant state before an approved action.

### 5. Verify in Proportion to Risk

- Define success checks before implementation. Verify affected behavior and relevant failure paths in proportion to risk.
- Run focused checks first and honor required repository gates. Broaden or repeat checks when changes, failures, or unresolved concerns justify it.
- Do not add tests merely to appear rigorous or substitute prose matching for behavior verification.

### 6. Finish When Complete

- Continue until deliverables and required verification are complete or a concrete blocker prevents progress.
- Report the result, evidence, skipped checks and reasons, and remaining uncertainty; then stop without opening unrelated work.

### 7. Delegate Selectively

- Use a sub-agent only for independent work whose benefit outweighs coordination cost.
- Honor required independent reviews and delegation restrictions. Keep tightly coupled work local.

## Master Preferences

- Respond to Master in Korean using polite speech. When directly addressing the user, use exactly `Master`.
- Keep repository artifacts in their established language and style; use English when no convention exists unless Master requests otherwise.
- Report concise conclusions and useful evidence without exposing private chain-of-thought.

## Aquarium Development Guide

- Use `$aquarium:task-handler` for one named roadmap task, `$aquarium:epic-handler` for one epic's sequential task goals, and `$aquarium:epic-validator` for cold validation of one completed epic.
- Use `$aquarium:new-project`, `$aquarium:new-feature`, or `$aquarium:refactor` for an explicitly requested Ouroboros-assisted design workflow. Use `$aquarium:war-room` for difficult-bug diagnosis ending in a task, epic, or incomplete-investigation proposal.
- Use `$aquarium:dev-setup` for repository-local tooling and guidance, `$aquarium:dev-setup-global` for supported user-global tools, `$aquarium:docs-setup` for documentation structure, and `$aquarium:test-setup` for the common test contract.
- Use `$use-sanho` at an authorized commit or push boundary in a Sanho-managed repository, or for an explicitly requested Sanho operation.
- Use `$use-mulgae` for authorized asynchronous reviews, run inspection, finding follow-up, configuration diagnosis, cleanup planning, and recovery. Use `$use-gaori` when selected checks run through Gaori or its evidence is inspected; use `$use-gaori-status` for historical timing and outcomes.
- Let Aquarium workflows use Podway by default for Git-backed work unless Master opts out before the first managed-session mutation. Each workflow retains its stricter roadmap, ownership, and approval rules, and an opt-out applies only to that workflow.
- Use `$use-podway` directly for explicitly requested Procedure v2 lifecycle, diagnosis, recovery, cancellation, or discard. Use `$create-podway-procedure` for Procedure authoring.
- Use `$lore-commits` for non-trivial commit messages and `$lore-query` for recorded decision context. Use `$deslop` for task-owned cleanup when an Aquarium workflow requests it.
- Use `$humanizer` once as the final prose pass for English human-authored documentation, preserving facts, identifiers, commands, URLs, citations, and generated content. If it is unavailable or validation fails, retain the unchanged draft.
- Treat `.mulgae/**`, `.gaori/runs/**`, `.podway/runtime/**`, and other local runtime evidence as transient. When tracked documentation needs evidence, use only a reviewed promoted evidence package rather than raw logs or runtime identities.
- Repository-specific rules in Project Configuration override these defaults.
- Use `$use-sorage` only when Master explicitly requests a broker operation. Check only the requested inbox or outbox; Project registration does not authorize discovery. Resolve Handoff, review, revision, retention, deletion, and Vault operations through that skill; never edit the managed Vault or derived `.sorage/INBOX.md` directly.

## Project Configuration

### Repository Index and Authorities

- The [README](README.md) introduces Agent Dispatch and its Watchman, SQLite, Hermes, launchd, and systemd integrations. `docs/README.md` indexes the canonical specification package and its precedence; `docs/roadmap/roadmap.md` alone owns roadmap identity, order, dependencies, and lifecycle status.
- `docs/specs/required-spec.md`, accepted ADRs, normative contracts, and current architecture define product behavior in the order recorded by `docs/README.md`. Use `docs/deferred-feedback/` for small postponed findings and `docs/todo/` for epic-sized candidates.
- `make build` builds the CLI. `make verify` (D-015) is the deterministic verification entrypoint for format, vet, staticcheck, import direction, unit and race tests, documentation manifest checksums, schema and example validation, traceability, and schedule artifacts. `make release` builds the supported release artifacts.

### Commit Messages

- Every developer-authored subject uses one header and a concise English imperative summary: `[FEAT]`, `[FIX]`, `[DEV]`, `[TEST]`, `[DOC]`, `[CI]`, `[REL]`, or `[INT]`.
- Use `[FEAT]` for new user-facing capabilities, `[FIX]` for defects, and `[DEV]` for development tools, build configuration, dependencies, or internal integration. Use `[TEST]`, `[DOC]`, or `[CI]` for changes limited to that concern, and `[INT]` for internal contracts or refactoring.
- Release metadata commits use exactly `[REL] Release v<version>`. Tool-generated commits retain their native subjects and provenance; never hand-author a `[SANHO]` synchronization commit.
- End every developer-authored message with one trailer block containing exactly one `Aquarium-Workflow:`, `Aquarium-Epic:`, and `Aquarium-Task:`, in that order. Separate the block from the subject or optional body with one blank line; do not put blank lines between trailers.
- `Aquarium-Workflow` records the responsible `aquarium:<canonical-skill-name>`. Preserve a verified original skill through later commits, including changes from documentation, development, or test setup. Child phases and reviews inherit an established parent's owner; independently approved workflows own their candidate. Use `aquarium:task-commit` only for a direct commit without an original Aquarium workflow, and `none` for work outside Aquarium.
- Resolve a Task and its parent Epic from the accepted relationship and canonical roadmap, including a standing `EPIC-000` where applicable. Preserve native IDs, suffixes, and scope qualification. Epic-owned work uses `Aquarium-Task: none`; work without either owner uses `none` for both. Resolve unknown relationships instead of treating them as absent.
- Put primary work IDs in these attributes rather than the subject or legacy `Task:` and `Task-Relationship:` aliases. Use `$lore-commits` for useful decision context on non-trivial changes; optional Lore trailers may repeat. Preserve required evidence, release-note, and tool provenance trailers.
- Parse the prepared message and actual post-hook commit with `git interpret-trailers --parse`; verify all three attributes against the accepted candidate. Attribution never grants staging, commit, amend, push, or publication authority.

- Commit messages also carry `Confidence:`, `Scope-risk:`, `Reversibility:`, and `Tested:` trailers.

### Project-Specific Operating Rules

- Root `CHANGELOG.md` is the sole product release-note source; use its version sections for GitHub Release descriptions. Pending changes use `Unreleased` or `vX.Y.Z - Unreleased` once selected. Released headings use `vX.Y.Z - YYYY-MM-DD` with concise `Added`, `Changed`, and `Fixed` outcomes. `docs/SOT-CHANGELOG.md` records specification-package versions.
- Gaori-routed long checks are exactly `manifest-check`, `schema-validation`, and `traceability` in `.gaori/tester.yaml`, matching Makefile targets.
- Mulgae active roles, provider routing, required roles, and findings policy follow `.mulgae/config.yaml`.
- The five tracked `.podway/procedures/aquarium-*-v2.yaml` files are managed Procedures. `.podway/runtime/` contains ignored session history and is never edited by hand.
- `docs/MANIFEST.sha256` is generated and checked by `make manifest-check`. Keep `.mulgae/local.yaml`, `.gaori/runs/`, and `.zcode/` untracked.
- Go is pinned to 1.27.1. Staticcheck is a `go.mod` tool dependency (SCP-005), so verification needs no network fetch.

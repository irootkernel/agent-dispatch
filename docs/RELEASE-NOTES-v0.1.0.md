# JJUKKUMI v0.1.0 Release Notes

Release target: v0.1.0 · SOT package version 1.0.x · schema range 1-4 ·
config version 1 · Hermes adapter 0.19.1 · Watchman verified
2026.07.27.00.

## What this release is

JJUKKUMI watches an Obsidian-style markdown vault through Watchman and
delivers bounded, latest-state maintenance tasks to a Hermes agent
runtime through the explicit Kanban or webhook target, with durable
SQLite-backed dispatch, receipt-cooperative feedback suppression,
quarantine, and reconciliation. It is a set of one-shot CLI commands:
no daemon is installed or required.

## Highlights

- Deterministic Watchman sensing over the frozen E0-T5 corpus with
  safe path containment and structural policy (E2).
- Durable dispatch with transactional intents, attempt leases, bounded
  retry, unknown reconciliation, and dead-lettering (E3).
- Version-gated Hermes Kanban integration with the verbatim
  idempotency key and execution projections (E4), plus the explicit
  webhook target whose 2xx is transport acceptance only (E6-T1).
- Exact self-change suppression with audited attribution, quarantine
  with operator lineage, and full-scope reconciliation (E5).
- The operations surface: structured causal logs, status, doctor,
  retention prune, integrity, vacuum, and backup (E6-T2).
- Reproducible release binaries with checksums, platform-validated
  launchd/systemd scheduling examples, and the installation guide
  (E6-T3).

## Artifacts

- `jjukkumi-v0.1.0-darwin-arm64`, `jjukkumi-v0.1.0-linux-amd64`, and
  `SHA256SUMS` from `make release VERSION=v0.1.0`.
- The SOT documentation package under `docs/` (manifest-verified).
- JSON schemas and examples for every durable record.
- The Hermes companion skill `jjukkumi-wiki-maintenance`.

## Verification

`make verify` (format, vet, staticcheck, import direction, unit and
race tests, docs manifest, schema and example validation,
traceability, scheduling-artifact lint) plus the acceptance gates
G0–G5 recorded in `docs/VALIDATION.md`. Downgrades are unsupported;
restore the pre-upgrade backup per the installation guide.

## Known deferred work

See `docs/future-work.md`: managed daemon, multi-vault certification,
MCP server, additional source adapters, and the optional Hermes
management plugin remain future work. The post-closeout hardening
deferrals recorded in the E6 member-task commits are reconciled by the
E6 validation audit.

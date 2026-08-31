# Agent Dispatch v0.1.1

Released 2026-08-23. This is the MVP compliance remediation release
(D-017): every finding of the 2026-08-22 review is fixed or carries
a recorded disposition (the finding index is recorded in D-019).

## What changed

- **Blockers fixed.** A process killed mid-submit heals through
  `dispatches drain` alone; rerun refuses in-flight work and supersedes
  the original through the declared edge (one authoritative task per
  route, enforced inside the lease transaction); a follow-up generation
  is promoted at acceptance and submitted by the scheduled path, so the
  second generation runs end to end without manual steps.
- **High findings fixed.** Submit-path revalidation supersedes and
  rebuilds stale plans under the active configuration (revision and
  target identity); durable path facts make unchanged-modify suppression
  real with the reason visible in decisions; the gate evidence executes
  (migration interruption before/between/inside units, a real
  after-remote-acceptance process death with dedup-safe recovery, live
  lockstep guards); the CLI inspection surface is complete (`config
  show`, the full causal lineage, list filters with pagination,
  `trace_id`).
- **All 32 Medium findings and the Low/Info inventory** are fixed or
  dispositioned in D-018; the release checklist is operated (50/50 with
  honest notes); the repository is MIT-licensed with a 39-module
  dependency-license review.
- **Verification.** `make verify` passes on darwin/arm64 including the
  race suite; gates G1-G5 re-ran green on the then-baseline real Hermes and
  Watchman 2026.07.27.00; the two consecutive `make release` builds are
  byte-identical.

## Known limitations

- **Linux is not verified at runtime.** The linux-amd64 artifact ships
  and builds reproducibly, but no successful `make verify` run on Linux
  is recorded: this is the explicit SCP-008 exception under D-017. The
  2026-08-22 review's diagnostic linux/arm64 container runs failed (two
  darwin-only keychain tests, since platform-guarded, plus root-path
  artifacts); a supported-host verification run remains outstanding.
- Platform-specific tests skip (never fail) off-platform; the two
  closed hardening items (the migration-lock heartbeat and the
  release/steal TOCTOU windows) are recorded for the post-release
  hardening pass.

# Deferred Feedback

This directory is the sole owner of small actionable findings intentionally
postponed from current work.

| ID | Finding | Recorded |
|---|---|---|
| [DF-001](001-g9-capability-cache-write-is-not-hermetic.md) | The g9 capability-cache write is not hermetic outside `make` | 2026-09-08 |
| [DF-002](002-e21-sync-fault-injection-hardening.md) | E21 sync fault-injection coverage can be strengthened | 2026-09-23 |
| [DF-003](003-e21-sync-recovery-orchestration-hardening.md) | E21 recovery orchestration can be consolidated before the managed service | 2026-09-23 |
| [DF-004](004-e22-final-observation-integration.md) | Final verification recheck needs a timed two-node integration case | 2026-09-27 |
| [DF-005](005-e22-endpoint-operations-hardening.md) | Endpoint diagnostics and regression coverage can be strengthened | 2026-09-28 |
| [DF-006](006-doc-manifest-inventory-hardening.md) | Manifest regeneration should exclude incidental files | 2026-09-28 |

An entry here must name the affected authority, the bounded concern, and
the condition for reconsideration. Oversized work is promoted to one TODO candidate
or an adopted roadmap unit; this directory never owns lifecycle status.

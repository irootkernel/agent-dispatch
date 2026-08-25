# Agent Dispatch MVP Feature and Change Request — Accepted Source Record

> **Source status:** Non-authoritative input retained for provenance
> **Received:** 2026-08-25
> **Original:** `/Users/draccoon/Workspace/Hermes/vault/Hermes/deliverables/agent-dispatch-mvp-feature-change-request.md`
> **Resolution authority:** D-025 and the v0.1.5 SOT 1.1.0 baseline

Hermes operations requested that Agent Dispatch complete the operational loop
from a correctly scoped Watchman event through one or more durable Hermes
tasks, bounded completion evidence, aggregate status, and deduplicated operator
notifications. The request defines eight inseparable functional areas:

1. correct nested Watchman root binding and route-relative exclusions;
2. Hermes 0.19.1+ eligibility followed by public-interface capability probing;
3. per-destination Hermes profile, skill, workspace, mutex, and hint validation;
4. separate operator and worker companion skills;
5. discoverable help and a guided `setup wiki` flow;
6. one event fanning out to independent destination/workstream tasks;
7. completion visibility and durable webhook or structured-log notifications;
8. reconciliation fencing and bounded reads while files change.

The requested product outcome is:

```text
Document change
  -> correctly scoped detection
  -> deterministic routing
  -> one or more durable Hermes tasks
  -> worker execution and bounded receipt
  -> aggregate status and operator notification
```

The complete original request remains at the path above. This source record is
intentionally concise: normative behavior is restated under stable IDs in
`docs/specs/required-spec.md`, design decisions live in ADR-0016 through
ADR-0019, and implementation ownership lives in roadmap epics E10 through E13.

## Accepted Clarifications

D-025 records the owner-approved clarifications that supersede conflicting
parts of the source request:

- Hermes itself is not modified. Agent Dispatch uses only the currently
  available public Hermes CLI and fails closed when its shape is unusable.
- Configuration remains `version: 1`, but legacy route `dispatch` blocks are
  rejected instead of automatically migrated. There are no deployed users to
  preserve through a compatibility layer.
- Existing durable database evidence is retained through forward migration;
  rollback uses the pre-migration backup and previous binary/config rather than
  a down migration.
- `partially_completed` schedules bounded remaining work, while `blocked`
  requires manual intervention and is never automatically retried.

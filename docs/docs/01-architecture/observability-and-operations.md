# Observability and Operations Architecture

## 1. Observability Goals

An operator must be able to answer:

- What source event caused this work?
- Why was it dropped, merged, quarantined, reconciled, or dispatched?
- Was Hermes definitely asked?
- Which Hermes task is related?
- Is another follow-up pending?
- Did a retry preserve idempotency?
- Which changes were considered self-generated?
- What requires manual action?

## 2. Correlation Fields

Every structured log and audit event should include available fields from this set:

```text
trace_id
observation_id
batch_id
decision_id
dispatch_id
attempt_id
receipt_id
route_id
route_revision
resource_id
target_id
external_ref
run_id
```

No correlation field replaces a foreign-key relationship in SQLite.

## 3. Structured Log Event Names

Recommended stable event names:

```text
source.received
source.rejected
observation.persisted
batch.planned
policy.decided
dispatch.intent_created
dispatch.attempt_started
dispatch.accepted
dispatch.rejected
dispatch.unknown
dispatch.retry_scheduled
dispatch.dead_lettered
delivery.reconciled
route.dirty_marked
route.followup_created
work.begun
work.completed
work.receipt_invalid
feedback.suppressed_exact
feedback.unresolved
quarantine.created
quarantine.released
reconciliation.requested
maintenance.pruned, maintenance.vacuumed, maintenance.backed_up
doctor.finding
```

## 4. Log Levels

- `DEBUG`: deterministic planner details with sensitive values removed.
- `INFO`: normal state transitions and operator actions.
- `WARN`: recoverable uncertainty, stale active work, reduced capability.
- `ERROR`: terminal command failure, dead letter, migration or integrity failure.

A normal excluded path is not an error.

## 5. Metrics

v0.1 does not require a metrics server. `jjukkumi status --json` and `doctor --json` should expose counters computed from SQLite:

- observations by disposition;
- active routes;
- dirty routes;
- ready/retry/unknown/dead-letter counts;
- dispatch acceptance latency;
- retry counts;
- quarantine count;
- last successful reconciliation;
- database size and oldest retained unresolved record.

A later daemon may export OpenTelemetry metrics under a separate ADR.

## 6. Audit History

Each state transition records:

```text
transition_id
entity_type
entity_id
from_state
to_state
actor
reason_code
causal_id
occurred_at
bounded_details_json
```

Audit rows are append-only through application code. Retention may compact resolved historical detail only under the documented retention policy.

## 7. Health and Doctor

`jjukkumi doctor` checks:

- config syntax and schema;
- route/resource references;
- resource root existence and permissions;
- SQLite open, journal mode, integrity, migration version, and local filesystem placement;
- Watchman presence and trigger definition;
- Hermes executable/endpoint presence;
- target capability match;
- secret reference resolvability without printing the value;
- stale leases;
- unknown or dead-lettered dispatches;
- stale active route state;
- overdue reconciliation;
- retention/database size warnings.

Findings have stable severity and code.

## 8. Operational Scheduling

Daily full reconciliation uses an external scheduler invoking:

```text
jjukkumi reconcile --route wiki-maintenance --reason scheduled --output json
```

Before the production gate, scheduled invocations omit `--submit` and persist reconciliation decisions for audit only. After the production gate, installed scheduled recipes add `--submit` (see the CLI contract) so that due reconciliation intents are actually submitted; without it, reconcile output alone never reaches Hermes when no new source events arrive.

Recommended schedules and installation examples are included for `launchd` and `systemd --user`. A scheduler failure is visible through `last_reconciled_at` and `doctor`.

## 9. Privacy-Preserving Diagnostics

A support bundle command may be added only if it:

- excludes note bodies and secrets;
- redacts configured path components;
- includes config schema and digests, not secret values;
- includes database schema/version and selected state counts;
- requires explicit operator destination.

It is not required for v0.1.

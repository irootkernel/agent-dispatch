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

Events actually emitted by the v0.1 CLI (each appears on the operational
log — stderr JSON lines — with the correlation fields of §2; the
info-level events are visible under `--log-level info`, the default warn
level carries the warn/error emissions):

```text
dispatch.attempt_started    dispatch.accepted      dispatch.rejected
dispatch.unknown            dispatch.retry_scheduled
dispatch.mutex_suppressed   (warn; E9-T3/T3-F007)
work.begun                  work.completed         work.receipt_invalid
maintenance.pruned          maintenance.vacuumed   maintenance.backed_up
doctor.finding
```

The wider vocabulary below is reserved for later surfaces; no v0.1
command emits these yet, and a name from it never appears in a log line:

```text
source.received  source.rejected  observation.persisted  batch.planned
policy.decided  dispatch.intent_created  dispatch.dead_lettered
delivery.reconciled  route.dirty_marked  route.followup_created
feedback.suppressed_exact  feedback.unresolved  quarantine.created
quarantine.released  reconciliation.requested
```

## 4. Log Levels

- `DEBUG`: deterministic planner details with sensitive values removed.
- `INFO`: normal state transitions and operator actions.
- `WARN`: recoverable uncertainty, stale active work, reduced capability.
- `ERROR`: terminal command failure, dead letter, migration or integrity failure.

A normal excluded path is not an error.

## 5. Metrics

v0.1 does not require a metrics server. The inspectable counters are the
JSON envelopes of `status` (taken with `--output json`; the flag's
only value is `json`) and `doctor` (always JSON):

- `status --output json` exposes `routes` (per route: activation and
  route state, dirty generation, pending-reconcile flag, active dispatch
  id, `last_reconciled_at`, and — since E12-T3 (OPS-011) — `lanes`, one
  bounded row per destination lane: destination id, lane state, active
  child, dirty generation), `queues` (dispatch intents by state),
  `quarantine` (items by state), `oldest_unresolved`, `database_bytes`,
  and `targets` (the offline capability summary per target), with the
  dirty/pending/unknown/dead-letter/held conditions repeated as envelope
  warnings;
- `events show <aggregate-id> --output json` (E12-T3, CLI-013) exposes one
  occurrence's aggregate — the selection summary with its closed reasons,
  origin, generation, content fingerprint — and every child beneath it
  with separate destination, intent-state, acceptance, execution,
  work-receipt (status + validity), retry, and completion-evidence
  projections; the `aggregate_status` member is the worst child class
  (evidence-gap > manual-intervention > failed > in-progress >
  completed, FBK-012: accepted work without a valid attributable work
  receipt — absent or invalid — renders `completion_evidence: missing`
  with the actionable next step, never "completed"; a never-accepted
  child renders `not-applicable`);
- `doctor` always emits its findings envelope on stdout (no `--output`
  flag required; `--output json` is accepted and ignored): `findings` (stable code, severity, summary, details,
  remediation, and the request's `trace_id`) and `findings_count`.

Latency and retry counters are not computed in v0.1; the durable attempt
history in SQLite carries the raw evidence for any later derivation. A
later daemon may export OpenTelemetry metrics under a separate ADR.

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

`agent-dispatch doctor` checks:

- config syntax and schema;
- route/resource references;
- resource root existence and permissions;
- SQLite open, journal mode, integrity, migration version, and local filesystem placement;
- Watchman presence and trigger definition;
- Hermes executable/endpoint presence;
- executable and endpoint presence plus the declared eligibility floor; compatibility truth is the per-executable capability probe (E11-T2, ADR-0017) whose fingerprint binds at enablement and is re-proved at dispatch-time;
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
agent-dispatch reconcile --route wiki-maintenance --reason scheduled --output json
```

Scheduled invocations carry `--submit` (E9-T4/T2-F001); the two-key gate is the safety boundary. Before the production acknowledgement the reconciliation fails closed at exit 14 (`transition_invalid`, nothing persisted) until `route enable --acknowledge-production-gate` records the route; after it, a route disabled in configuration (the YAML key) persists its reconciliation decisions and recovers without submitting anything, and the same scheduled leg actually delivers the due reconciliation intents — without the submit leg, reconcile output alone never reaches Hermes when no new source events arrive.

A recommended schedule and installation example is included for `launchd` (macOS, the only supported platform, D-023; the retired `systemd --user` example is superseded history). A scheduler failure is visible through `last_reconciled_at` and `doctor`.

## 9. Privacy-Preserving Diagnostics

A support bundle command may be added only if it:

- excludes note bodies and secrets;
- redacts configured path components;
- includes config schema and digests, not secret values;
- includes database schema/version and selected state counts;
- requires explicit operator destination.

It is not required for v0.1.

## 10. v0.1.5 Status and Notifications

Status adds aggregate event counts, child states per destination, configured
and actual Watchman roots, effective relative root and patterns, capability
evidence fingerprint, profile/skill preflight, reconciliation conflicts, and
notification retries. Human output groups the projections; JSON preserves
their separate records and emits empty collections rather than null.

Configured reportable transitions create a durable channel-neutral
notification intent. The default event set, when at least one sink is present
and no event list is supplied, is work completed, exhausted failure, delivery
unknown, quarantine, reconciliation required, integration drift, and Watchman
drift. Structured stdout/log and HTTPS webhook are the shipped sinks. Delivery
failure is visible and retryable but never mutates task outcome.

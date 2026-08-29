# Canonical Record Contracts

## 1. General Rules

- JSON field names use `snake_case`.
- Timestamps use RFC 3339 with timezone and subsecond precision when available.
- IDs are strings with documented prefixes optional; storage does not depend on prefixes.
- Relative paths use `/` separators.
- Digests use `sha256:<lowercase-hex>`.
- Unknown fields in a compatible minor version may be ignored only when schema permits.
- Unknown major versions fail closed.
- Arrays that affect fingerprints are sorted before canonical encoding.

## 2. Source Observation

See `schemas/source-observation.schema.json`.

Important separation:

- `observation_id` is unique identity;
- `source_event_key` recognizes source retransmission;
- `raw_payload_digest` identifies received bytes;
- no `risk`, `origin`, or `route_hint` field appears in the observation.

Structural classification and attribution belong to later records.

## 3. Dispatch Plan

A dry-run plan contains:

```json
{
  "schema_version": "agent-dispatch.dispatch-plan/v1",
  "route": {"id": "wiki-maintenance", "revision": "..."},
  "resource_id": "vault-main",
  "changes": [],
  "content_fingerprint": "sha256:...",
  "classification": ["normal"],
  "disposition": "dispatch",
  "reason_codes": ["meaningful_markdown_change"],
  "required_capabilities": ["durable_acceptance"],
  "generation_action": "create_if_idle"
}
```

Dry-run output omits persistent IDs that would imply a committed record.

## 4. Dispatch Intent

See `schemas/dispatch-intent.schema.json`.

The `request` object is immutable and stores the logical Hermes task request defined in `docs/contracts/hermes-task-contract.md` §2 verbatim (`schemas/hermes-task-request.schema.json`). Envelope fields such as `dispatch_id`, `route`, `generation`, `idempotency_key`, and `content_fingerprint` are deliberately duplicated inside `request` so the record is a self-contained audit artifact. Resolved secret material is never included.

## 5. Dispatch Receipt

See `schemas/dispatch-receipt.schema.json`.

Acceptance states:

- `accepted`
- `rejected`
- `unknown`

Execution projection states:

- `unavailable`
- `queued`
- `running`
- `succeeded`
- `failed`
- `canceled`

A receipt may contain one axis or both, but must not infer execution success from acceptance.

## 6. Work Receipt

See `schemas/work-receipt.schema.json`.

The change manifest has relative paths and digests only. An absent digest means the item cannot support exact suppression.

## 7. Quarantine, Batch, and Decision Records (E5-T4)

The operator surfaces expose three record shapes with the general rules
of §1 (snake_case, RFC 3339 timestamps, relative slash paths,
`sha256:<hex>` digests).

**Quarantine record** (`quarantine list|show`, `quarantine release|discard` result):

```json
{
  "quarantine_id": "q-<opaque>",
  "batch_id": "<batch id>",
  "decision_id": "<originating policy decision>",
  "reason_codes": ["protected_path_present"],
  "state": "held",
  "created_at": "<RFC3339>"
}
```

`state` is `held`, `released`, or `discarded` (`superseded` is reserved
for a future supersession flow and is not emitted in v0.1). While held,
the resolution fields are absent from the JSON; a release resolves with
`resolved_by` (actor), `resolution_reason`, and a
`replacement_decision_id` referencing the new reconciliation decision
that supersedes the originating decision (CLI-006). A discard resolves
without a replacement.

**Batch record** (the canonical policy unit behind `dispatches reprocess`): the
retained observation-to-batch lineage — `batch_id`, `route_id`,
`route_revision`, `resource_id`, `created_at`, `content_fingerprint`,
observation ids, and — since migration v15 (E12 epic validation) — the
`selected_destinations` list, the occurrence's FULL destination selection
recorded as merge evidence in canonical destination order (empty when
unrecorded; the pre-v15 legacy shape). The batch keeps its normalized
change rows (`path`, `operation`, `exists`, `file_type`, `before_digest`,
`after_digest`, `digest_status`).

**Decision record** (every policy outcome): `decision_id`, `route_id`,
`route_revision`, `policy_revision`, exactly one of `batch_id` or
`generation_lineage`, `disposition` (`drop|dispatch|merge_pending|quarantine|reconcile`,
POL-006), `classification` (`normal|protected|bulk|overflow|malformed|stale|unknown`),
`reason_codes` (machine-readable, sorted), `created_at`, `actor`, and
`supersedes_decision_id` lineage. Reconciliation decisions carry
`generation_lineage` (`{"route_id":...,"reason":...,"origin":...}`) instead of a
batch: a reconciliation generation may span many batches or none.

**Full-reconciliation result** (`reconcile` command): `route_id`,
`reason` (one of `initial|scheduled|overflow|fresh-instance|lost-cursor|manual|delivery|stale-active|startup`),
`enumerated`, `compared`, sorted `added`/`removed`/`changed` path lists,
`pending_reconcile`, `decision_id`, optional `reconcile_dispatch_id`
(exactly one latest-state intent when the route was idle with due work,
or when an uncertain route was resolved with due work),
and `snapshot_stored`. The enumeration obeys the containment and size
rules of the resource resolver; an unverifiable path is reported with an
absent digest, never silently truncated (SRC-005).

## 8. Canonicalization

Fingerprint and idempotency projection must:

1. construct a dedicated value object containing only documented fields;
2. normalize strings and paths before projection;
3. sort maps by key and arrays by documented ordering;
4. encode using RFC 8785-compatible canonical JSON or an implementation proven byte-equivalent for the supported field types;
5. hash UTF-8 bytes with SHA-256.

Do not fingerprint arbitrary marshaled domain structs because adding a field could silently change identity.

## 9. Schema Compatibility

- `v1` producers may add optional fields only when consumers ignore them safely.
- Required semantic changes create a new major contract version.
- SQLite migration version and JSON contract version are independent.
- Examples are validated against schemas by `make schema-validation` inside `make verify` (hosted CI is not used; D-017).

## 10. Planned v0.1.5 Record Families

| Contract | Required purpose |
|---|---|
| `agent-dispatch.aggregate-event/v1` | One normalized source/policy occurrence and destination-selection summary |
| `agent-dispatch.destination-revision/v1` | Canonical behavior projection for one destination ID |
| `agent-dispatch.child-dispatch/v1` | Aggregate/destination lineage and independent intent identity |
| `agent-dispatch.work-receipt/v2` | Completed, partial, blocked, or failed bounded worker evidence |
| `agent-dispatch.hermes-capabilities/v2` | Executable identity, version, command/shape evidence, profiles, and required skills |
| `agent-dispatch.notification-event/v1` | Channel-neutral safe transition payload |
| `agent-dispatch.notification-attempt/v1` | Sink attempt and outcome under the stable idempotency identity (retry scheduling arrives with E13-T2 delivery) |

Aggregate event identity is independently generated and never substitutes for
content fingerprint. Child identity is independently generated; its
idempotency key is the canonical digest defined by DAT-014. Receipt v2 partial
results require completed and remaining scope, and all receipt paths remain
bounded, relative, containment-validated data. Notification contracts prohibit
document bodies and resolved secrets and carry only safe identities, states,
reason codes, timestamps, configured-safe paths, and digests.

These record contracts become executable schemas and examples only in their
owning E11-E13 tasks. The `aggregate-event/v1`, `destination-revision/v1`,
and `child-dispatch/v1` families landed with E12-T1 (migration v12:
`aggregate_events`, `destination_revisions`, `child_dispatches`; the child
idempotency key is the DAT-014 projection above). The
`notification-event/v1` and `notification-attempt/v1` families landed with
E13-T1 (migration v16: `notification_events`, `notification_attempts`;
ADR-0019): the notification identity derives deterministically from the
event, optional destination, transition occurrence, sink, and
notification-policy revision (NTF-003), so a replayed or rerun transition
collapses onto its existing record (AC-902); attempts are separate durable
records whose outcomes never rewrite the intent's source state (NTF-005),
and resolved notification evidence prunes past retention while pending
evidence stays retained and inspectable (NTF-004). The remaining families
are still planned and the checked-in JSON schemas for them describe the
shipped v0.1.4 wire surface until their owning tasks implement them.

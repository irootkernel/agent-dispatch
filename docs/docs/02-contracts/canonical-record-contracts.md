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
  "schema_version": "jjukkumi.dispatch-plan/v1",
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

The `request` object is immutable and stores the logical Hermes task request defined in `docs/02-contracts/hermes-task-contract.md` §2 verbatim (`schemas/hermes-task-request.schema.json`). Envelope fields such as `dispatch_id`, `route`, `generation`, `idempotency_key`, and `content_fingerprint` are deliberately duplicated inside `request` so the record is a self-contained audit artifact. Resolved secret material is never included.

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

## 7. Canonicalization

Fingerprint and idempotency projection must:

1. construct a dedicated value object containing only documented fields;
2. normalize strings and paths before projection;
3. sort maps by key and arrays by documented ordering;
4. encode using RFC 8785-compatible canonical JSON or an implementation proven byte-equivalent for the supported field types;
5. hash UTF-8 bytes with SHA-256.

Do not fingerprint arbitrary marshaled domain structs because adding a field could silently change identity.

## 8. Schema Compatibility

- `v1` producers may add optional fields only when consumers ignore them safely.
- Required semantic changes create a new major contract version.
- SQLite migration version and JSON contract version are independent.
- Examples are validated in CI against schemas.

# Source Adapter Contract

## 1. Purpose

A source adapter converts source-specific input into a validated `SourceInput`. It does not select policy, target, profile, or workspace.

## 2. Port

Conceptual Go interface:

```go
type SourceAdapter interface {
    Type() SourceType
    Parse(ctx context.Context, req ParseRequest) (SourceInput, error)
    ValidateBinding(ctx context.Context, input SourceInput, binding SourceBinding) error
    SourceEventKey(input SourceInput) (*string, error)
}
```

The exact package names may differ, but responsibility must not.

## 3. ParseRequest

```text
ParseRequest {
  bounded stdin reader
  allowlisted environment map
  invocation time
  input size limits
}
```

Adapters must not receive the full process environment.

## 4. SourceInput

```text
SourceInput {
  schema_version
  source_type
  source_id_candidate
  trigger_identity
  root_identity_candidate
  relative_root
  source_position
  flags
  file_facts[]
  raw_payload_digest
}
```

All fields remain non-authoritative until route binding validation.

## 5. Watchman Requirements

The Watchman adapter must:

- stream or bounded-read stdin;
- reject trailing ambiguous JSON unless the verified Watchman contract permits it;
- cap array length before materializing unbounded input;
- normalize booleans and source fields according to verified fixtures;
- preserve unknown non-critical fields only in a bounded extension object if useful;
- never use a payload field as a shell argument without safe mapping;
- recognize overflow/fresh-instance evidence conservatively.

## 6. Error Classes

- `source_input_too_large`
- `source_malformed_json`
- `source_unsupported_version`
- `source_binding_mismatch`
- `source_missing_required_metadata`
- `source_position_unusable`
- `source_unsafe_path`

The application layer maps errors to rejection, quarantine, or reconciliation according to the SOT. A source adapter does not directly persist or dispatch.

## 7. Future Sources

Future adapters for Git, webhook ingress, timers, processes, or queues must implement the same observation boundary. They may have different source-position structures, but they cannot add authority-bearing route hints to the canonical observation.

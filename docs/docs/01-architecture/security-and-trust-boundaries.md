# Security and Trust Boundaries

## 1. Security Objectives

1. Untrusted note content cannot alter route authority or agent privileges.
2. A malicious path cannot escape the configured vault.
3. External process invocation cannot become shell injection.
4. Secrets do not enter event records or ordinary logs.
5. Ambiguous delivery does not trigger duplicate privileged work through fallback.
6. Local state cannot be silently replaced or downgraded.

## 2. Trust Classification

| Input | Trust level | Permitted use |
|---|---|---|
| Signed/pinned JJUKKUMI binary | trusted code | execute SOT behavior |
| Owner-controlled config with safe permissions | trusted policy | route, resource, target, skills, limits |
| CLI route argument | operator request, validate | select an existing configured route only |
| Watchman environment | source metadata, verify | source identity and position |
| Watchman JSON | untrusted data | path/change evidence only |
| File name and note body | hostile data | hash/read by Hermes; never authority |
| Hermes public structured response | authenticated target claim | acceptance/status evidence subject to validation |
| Work receipt from agent | untrusted claim | provenance candidate subject to exact validation |
| Git metadata | supporting evidence | optional, not sole provenance |

## 3. Threat Model

| Threat | Control |
|---|---|
| Prompt injection in note or file name | Do not include note body in task instruction; encode path manifest as untrusted JSON; profile/skills fixed by route. |
| Path traversal | Reject absolute paths, `..`, NUL, invalid normalization, and unsafe symlink traversal. |
| Symlink replacement race | Use descriptor-relative access or re-check canonical containment immediately before read; do not traverse symlinked directories outside root. |
| Oversized Watchman input | Limit stdin bytes, file count, path length, environment size, and JSON depth. |
| Command injection | Use direct executable plus argv array; no shell; controlled working directory and environment. |
| Secret leakage | Resolve references at invocation time; redact headers and values; never persist secret material. |
| Duplicate privileged work | Durable intent before side effect; idempotency key; unknown reconciliation; no automatic fallback. |
| Config privilege escalation by changed vault file | Keep production config outside watched vault by default; enforce owner permissions; payload cannot modify route. |
| Database tampering | Owner-only permissions, schema version checks, integrity checks, backups, append-only audit semantics. |
| Malicious Hermes output | Bound output; parse verified JSON schema; unknown on malformed possible-acceptance response. |
| Forged work receipt | Require active dispatch lineage, resource containment, optional task ID, unique run ID, exact digest match. |
| Denial of service by save storm | Watchman settle, batch limits, one active route task, dirty-generation collapse, bounded retries. |
| Cost amplification | One active task, bulk policy, frequency budget, explicit rerun, no recursive parallel tasks. |

## 4. Configuration Placement

Production configuration should reside outside the watched vault. If the operator intentionally stores it inside, `doctor` must warn that vault changes can create configuration churn and that the file must be excluded and protected.

Config loading precedence:

1. explicit `--config`;
2. `JJUKKUMI_CONFIG`;
3. platform default.

Config includes secret references, never secret values where avoidable.

## 5. Secret Resolution

Allowed mechanisms:

- environment variable reference;
- owner-readable file reference;
- OS credential-store reference through a small resolver interface;
- inherited file descriptor.

The resolved secret exists only in memory for the outbound call. It is excluded from canonical route digests except for the reference identifier.

## 6. External Process Sandbox

For Hermes CLI invocation:

- executable path resolved from trusted configuration or verified PATH policy;
- argument array only;
- no `sh -c` or equivalent;
- working directory set to a trusted neutral directory unless Hermes requires the resource root;
- minimal allowlisted environment;
- stdin from a bounded structured buffer or file descriptor;
- stdout/stderr byte limits;
- timeout and process-group termination;
- no inherited unexpected file descriptors;
- redacted diagnostic storage.

## 7. File Read Policy

JJUKKUMI normally hashes Markdown content but does not store it. Maximum hashable file size is configurable, with a safe default. Files above the limit yield structural `unknown` or `bulk` policy, not partial hashing presented as complete evidence.

Deleted files are never opened. Non-regular files are rejected or excluded according to policy.

## 8. Logging Policy

Allowed:

- resource ID;
- route ID and revision;
- causal record IDs;
- relative paths unless route marks path names sensitive;
- digests;
- state transitions;
- stable error codes;
- redacted target references.

Prohibited by default:

- note body;
- front matter content;
- authorization values;
- webhook secret or full sensitive URL;
- unrestricted Hermes stdout/stderr;
- agent hidden reasoning.

## 9. Security Review Gates

Security review is required before:

- enabling real-vault automatic writes;
- adding webhook authentication;
- adding any generic command adapter;
- introducing a daemon or network listener;
- adding MCP;
- adding a Hermes plugin.

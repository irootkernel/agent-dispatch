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
| Signed/pinned Agent Dispatch binary | trusted code | execute SOT behavior |
| Owner-controlled config with safe permissions | trusted policy | route, resource, target, skills, limits |
| CLI route argument | operator request, validate | select an existing configured route only |
| Watchman environment | source metadata, verify | source identity and position |
| Watchman JSON | untrusted data | path/change evidence only |
| File name and note body | hostile data | hash/read by Hermes; never authority |
| Hermes public structured response | authenticated target claim | acceptance/status evidence subject to validation |
| Work receipt from agent | untrusted claim | provenance candidate subject to exact validation |
| Ordinary Git metadata | supporting evidence | optional, not sole provenance |
| Verified sync membership/publication/checkpoint signatures | scoped authority | membership and content-history trust only under D-030 and ADR-0024/0025 |
| Peer nudge or status payload | untrusted data | wake-up and correlation evidence only; never selects paths, executables, remotes, refs, profiles, credentials, or force behavior |

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
| Forged, replayed, malformed, ambiguous, oversized, or wrong-direction peer request | Tailnet-only endpoint and certificate verification; direction-specific credential binding to sender, receiver, and group; schema, size, and duplicate-field rejection; durable inbox and idempotency; no content or arbitrary commands. |

## 4. Configuration Placement

Production configuration should reside outside the watched vault. If the operator intentionally stores it inside, `doctor` must warn that vault changes can create configuration churn and that the file must be excluded and protected.

Config loading precedence:

1. explicit `--config`;
2. `AGENT_DISPATCH_CONFIG`;
3. platform default.

Platform defaults (`internal/platformpaths`): macOS uses
`~/.config/agent-dispatch/config.yaml` and
`~/Library/Application Support/Agent Dispatch` for state; Linux follows
the XDG base-directory rules — absolute `XDG_CONFIG_HOME` /
`XDG_STATE_HOME` replace the home-relative `~/.config` /
`~/.local/state` prefixes (relative `XDG_*` values are ignored). See
`docs/ops/installation.md` §2.

Config includes secret references, never secret values where avoidable.

## 5. Secret Resolution

Allowed mechanisms (configuration-spec §11, `secretresolver`):

- `env:` environment variable reference (all supported platforms);
- `file:` owner-readable, owner-only file reference (all supported platforms);
- `fd:` inherited file descriptor (all supported platforms);
- `keychain:` macOS Keychain reference through the controlled `security`
  lookup (darwin-only).

On Linux, only `env:`, `file:`, and `fd:` resolve. A `keychain:`
reference fails closed as a typed unresolved secret (`UnresolvedError`)
with cause `keychain references are not supported on this platform` —
never a panic and never a credential value (D-029, E19-T6).

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

Agent Dispatch normally hashes Markdown content but does not store it. Maximum hashable file size is configurable, with a safe default. Files above the limit yield structural `unknown` or `bulk` policy, not partial hashing presented as complete evidence.

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

For v0.2.0, E22-T1 must complete the listener/authentication threat-model
review before introducing `sync serve`, and E22-T5 must carry the reviewed
real-vault automatic-write disposition into the release handoff. The peer
listener is a local owner-only socket or tailnet-only; public interface binding, Funnel, and
Tailscale configuration changes are outside the admitted design.

### E22-T1 peer listener and authentication threat model

The operator controls the Tailscale HTTPS route and its certificate. Agent
Dispatch uses an owner-only Unix socket beneath the state directory behind
that route. It does not create a Serve or Funnel route. A Tailscale identity or
forwarded header alone is not application authorization: a tailnet member,
local process, or compromised reverse proxy can still send a request. Public
interface binding, an unexpected proxy hop, and a changed route are deployment
failures. The peer client uses only the configured `https://*.ts.net` endpoint,
validates its certificate and exact destination, ignores ambient proxy settings,
and refuses redirects. The service does not log credentials or full sensitive
URLs.

The configured group, two member identities and incarnations, membership
revision, endpoints, content ref, and direction-specific secret references are
the authority boundary. The receiving node resolves only its inbound direction
secret; the sender resolves only the configured outbound direction secret.
Each request must authenticate to the configured group, sender, and receiver,
then match the current locally signed membership. A stale revision, retired
identity, wrong receiver, or invalid credential fails before durable admission.
The peer request carries no incarnation field; replacing an incarnation also
requires rotating its directional credential so the old process cannot
authenticate as the new one. Secret values remain in memory and are compared
without exposing their length, value, or reference. A nudge grants no
publication, import, signing, or membership authority.

The service resolves peer credentials only. It has no code path for resolving
publisher or administrator signing keys. Managed launchd and systemd services
run under the same user as the explicit CLI, so a same-user `file:` or
`keychain:` signing reference cannot isolate the signer from the service.
For a service-enabled group, signing references must use `env:` or `fd:`;
the named variables and descriptors must be absent from the service process.
Startup checks this condition without resolving a signing value and refuses
`file:` or `keychain:` signing references. The operator supplies the signing
environment or descriptor only to an explicit CLI invocation. Service render
and install must not propagate either into the managed unit.

The fixed socket path is inside an owner-only real directory, so another UID
cannot bind the final proxy hop while the service is down. An active socket
cannot be replaced at startup; a stale socket is removed only after a refused
local connection. A compromised process with the same UID remains inside the
operator's OS credential boundary and requires credential rotation. The
operator must route Tailscale Serve to `unix:<state_dir>/peer-service/http.sock`
and verify that no Funnel or public route exists.

The HTTP parser admits only the two fixed POST routes. It limits request and
response bytes, header bytes, concurrency, and time, rejects unsupported
content types and transfer ambiguity, and requires exactly one unambiguous
authorization value. JSON parsing rejects duplicate and unknown fields,
trailing values, malformed types, and schema mismatches. The nudge body is at
most 256 KiB; the status request body is at most 16 KiB. Peer fields are
correlation data, never executable, path, remote, ref, profile, credential, or
force selectors. The content ref must equal the configured ref and the target
must be a full object ID. The nudge cannot prove that the target is trusted;
the separate worker checks the fetched commit and publication evidence before
import. The sender's rate budget is charged before secret resolution, including
failed credentials, to bound credential-store work. Rate limits and bounded
shutdown protect existing local work.

An accepted nudge is keyed by its stable publication identity and exact
sender, receiver, membership revision, and target. Duplicate delivery of the
same logical request is idempotent; reuse of an identity with different bytes
is refused. HTTP 202 follows the inbox transaction commit, even if the sender
disconnects after commit. A database error or queue limit returns failure
without a success claim. The handler may inspect local signed membership and
refs through the restricted Git adapter, but does not fetch, import, write
the vault, or mutate Git. A separate fenced worker handles admitted obligations.
Failed or offline peers leave the local signed publication intact, with a durable
delivery obligation and bounded retry. Process loss or timeout preserves the
logical identity and does not turn an uncertain attempt into success.

Fresh status is read-only. Its nonce and requested revision, commit, scope,
and contract digests are correlation and equality checks. E22-T1 reports a
freshly read local ref with `unknown` state and `uncertain: true`; E22-T3 will
measure governed dirtiness, pending work, membership currentness, and evidence
age for pair verification. Echoing a new nonce cannot refresh cached evidence.
Pair convergence requires observations from both nodes within the 300-second
age bound. Shutdown stops admission, lets committed requests finish within the
bounded grace period, and preserves queued work for restart.

The security review accepts this model only if implementation and tests cover
wrong-direction, revoked and stale membership, replay and conflicting
duplicates, parser ambiguity, payload and resource bounds, redirects, commit
before 202, crash recovery, and shutdown. An unresolved local bind, identity,
or credential ambiguity blocks listener activation. The external HTTPS route
remains an operator deployment check.

## 10. v0.1.5 Boundary Additions

- Destination profile, skill, workstream, workspace, target, mutex, conditions,
  and notification sinks come only from trusted configuration.
- Hermes profile/skill probing uses public commands with fixed non-interactive
  rendering, bounded output, and no private-storage fallback.
- Exclusions apply before file access and before any child or notification is
  rendered.
- Notification events never carry note bodies, front matter, resolved secrets,
  authorization values, or event-selected endpoints.
- Webhook notification transport is HTTPS-only, ignores ambient proxy settings,
  does not follow redirects, and bounds payload, response, and duration.

# Two-Node Wiki Sync Contract

This contract reserves the v0.2.0 Agent Dispatch sync surface. E20 freezes the
schemas and implements truthful capability reporting. E21 and E22 implement
the behavior. Until the relevant capability is implemented, commands must
return the closed unavailable result without a Git, network, filesystem, or
activation side effect.

## Scope

One sync group binds exactly two active node identities, one resource root, one
approved Git remote, one content ref, one membership ref, and the Markdown file
scope. Route-protected and immutable paths remain outside that scope; a
publication or import candidate touching one fails closed before content
mutation. Configuration and local state choose every path, executable,
endpoint, ref, credential reference, and trust anchor. Event or peer payloads
cannot override them.

## Command identities

```text
agent-dispatch sync capabilities --output json
agent-dispatch sync status --group GROUP --output json
agent-dispatch sync publish --group GROUP --expected-config-revision REV --output json
agent-dispatch sync reconcile --group GROUP --output json
agent-dispatch sync verify --group GROUP --output json
agent-dispatch sync serve --group GROUP
agent-dispatch sync pause --group GROUP --expected-control-revision REV --output json
agent-dispatch sync resume --group GROUP --expected-control-revision REV --output json
agent-dispatch sync membership plan|apply --group GROUP --output json
agent-dispatch sync checkpoint plan|apply --group GROUP --output json
agent-dispatch sync service render|install|inspect|stop|disable|uninstall --group GROUP --output json
```

Reserved command identities remain registered in the CLI tree and return the
closed unavailable result until their owning task implements them.

`sync publish` is the only ordinary publication entrypoint. It requires an
eligible maintenance snapshot but not cooperative-import acknowledgement. The
command holds the resource guard through snapshot capture, persists publication
intent before effects, and resolves the node publisher key only in the explicit
CLI process. A pre-signature interruption requires an explicit re-entry under
the same logical identity. The service may recover an already-signed commit's
push, confirmation, and nudge work but cannot create or sign a new commit. A
confirmed remote publication and accepted nudge are separate outcomes.

`sync reconcile` fetches configured refs, resolves ambiguous prior effects,
and schedules eligible import or delivery work. It cannot select an arbitrary
remote, ref, path, executable, profile, or force option. After an operator
resolves a conflict, reconciliation clears only a block covered by the exact
administrator-signed checkpoint; it never adopts uncovered history by itself.

`sync verify` pins the group, membership revision, content ref, target commit,
scope digest, contract digest, both node identities, and their incarnations.
It reports historical delivery separately from fresh pair convergence.

`sync serve` hosts the authenticated nudge and fresh-status endpoints and runs
bounded periodic reconciliation. HTTP handlers validate and persist requests;
application services perform Git and filesystem effects after admission.

`pause` prevents new protected effects and lets in-flight work reach a safe
boundary. `resume` revalidates configuration, membership, activation, and local
safety. Neither command clears a conflict or revocation.

`sync membership plan|apply` is the two-phase administrator surface for initial
bootstrap, endpoint/key update, replacement, retirement, revocation, and
incarnation re-registration. The plan uses a closed change-kind enum. Apply
requires the same plan identity, expected membership predecessor, separate
administrator key role, and non-force ref update.

`sync checkpoint plan|apply` creates the administrator-signed content adoption
evidence used for an initial baseline, reviewed conflict resolution, or visible
bounded-history exhaustion. It binds the exact target commit, governed snapshot,
scope and contract digests, and membership revision.

`sync service` owns the managed launchd/systemd user-service definition. Install
starts the exact rendered definition, stop preserves it, disable stops and
disables it, and uninstall removes only the matching managed definition while
preserving local state and evidence.

## Peer protocol

The v0.2.0 protocol exposes authenticated nudge reception and bounded fresh
status observation. A nudge identifies the schema, group, publication, sender,
receiver, membership revision, content ref, and full target object ID. HTTP
202 means the inbox transaction committed. It does not mean that Git fetch or
content application completed.

Fresh status uses a request-correlated nonce and identifies the responder and
state incarnation. Cached evidence retains its original age and generation;
echoing a new nonce does not make cached evidence fresh.

Requests reject unknown fields, duplicate-field ambiguity, oversized input,
wrong group or receiver, revoked identities, unsupported schemas, invalid
credentials, and unapproved redirects. Peer messages never contain note bodies
or arbitrary commands. The listener is loopback or tailnet-only, never enables
Funnel or public binding, never changes Tailscale configuration, and
verifies the configured peer endpoint and certificate.

## Outcomes and recovery

Publication preparation, remote publication, nudge acceptance, local import,
historical delivery, and fresh pair convergence are separate durable milestones. Every external effect
has a stable logical identity, bounded attempts, claim ownership, a fencing
generation, and an explicit ambiguous outcome.

The service recovers already-signed publication, delivery, and import work at
startup and during periodic reconciliation. Duplicate or reordered nudges are
normal. Retries reuse the original logical identity. Conflict, trust failure,
pre-signature publication work, and unsafe local state require operator action
and are not retryable transport failures.

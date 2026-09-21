# ADR-0024: Signed membership and peer authentication

- **Status:** Accepted
- **Date:** 2026-09-17
- **Decision:** D-030

## Context

Git transport, tailnet reachability, and a claimed sender field do not prove
that a node belongs to a sync group or may publish maintained content. The two
nodes also need stable identity across ordinary service restarts and a way to
reject evidence from a restored or reset local state.

## Decision

Membership is a signed, versioned history on a separately configured Git ref.
It contains the group identity, parent revision, content binding, contract
digest, and entries keyed by `instance_id`. The normal operating state has
exactly two active entries; retired and revoked entries remain as historical
records. An emergency revocation may temporarily leave fewer than two active
entries, but protected publication, import, and verification-success effects
remain blocked until an authorized replacement restores the pair. Each entry
records its endpoint, SSH Ed25519 publication identity, lifecycle state, and
current `state_incarnation_id`.

The administrator and publisher signing modes use distinct SSH Ed25519 keys. Initial
administrator trust is pinned out of band. A membership revision cannot make
its own previously untrusted key authoritative. Administrator and publisher are
separate key roles; a node publisher key cannot authorize membership.
Membership changes are explicit two-phase `sync membership plan|apply`
operations with a closed change kind, reviewed expected predecessor,
administrator signature, and non-force ref update. This path owns initial
bootstrap, endpoint and key updates, replacement, retirement, revocation, and
re-registration after an incarnation change.
The membership content binding is the configured canonical remote-repository
digest, with the configured content ref carried separately. Each signed
revision uses a fixed two-file tree containing the membership document and the
reviewed plan. Non-bootstrap plans name the affected instance explicitly;
blocked-pair recovery names the configured replacement being added.

Historical publication evidence names the verified membership revision that
authorized its publisher when the publication was prepared. That revision must
belong to the verified membership ancestry and the publisher key must be active
in it. Evidence verified before a later rotation or
revocation remains historical evidence. After accepting the superseding
membership revision, previously unseen history signed by the removed key is
untrusted regardless of commit timestamps and requires a current
administrator-signed adoption checkpoint. Commit timestamps are never rotation
or revocation authority.

Administrator and publisher private keys are local `secret_ref` values. The
publisher key is resolved only by explicit `sync publish`; the administrator
key is resolved only by explicit membership/checkpoint apply. The long-running
peer service receives neither signing key and cannot create signed history.

Peer HTTP uses Tailscale HTTPS plus one credential for each communication
direction. Credentials are local `secret_ref` values and never appear in Git,
SQLite, logs, status output, or peer payloads. The receiver binds each
credential to the configured sender, receiver, and group. Tailscale identity
headers alone do not authorize an application request.

A destructive state reset or restore rotates `state_incarnation_id`. Ordinary
process or machine restart retains it. Evidence from an obsolete incarnation
cannot satisfy current application or verification.

## Consequences

- Key and credential rotation are expected-predecessor updates with explicit
  operator action. They do not silently rewrite historical evidence.
- Wrong group, receiver, signature, credential, membership revision, or
  incarnation fails before Git or filesystem mutation.
- Every membership change invalidates the current pair-verification target. A
  change accepted under the already acknowledged trust policy does not by
  itself invalidate cooperative-import acknowledgement; changing the trust
  anchor or trust policy does.

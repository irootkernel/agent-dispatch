# E22-T5 Disposable Two-Node Qualification

**Date:** 2026-09-27
**Candidate:** E22-T4 commit `d10cc2f1895246f1b92f3b80ccdc604e8291d382` plus the E22-T5 explicit-port correction.
**Hosts:** macOS arm64 MacBook and Oracle Cloud Linux arm64 (`ssh doksuri`), with isolated fixture repositories and SQLite state. No production vault was mounted or changed.

The configuration, signed membership, schema, and peer clients now accept an explicit HTTPS port on a `.ts.net` origin. The fixture used private Tailscale Serve port `8448` on both nodes. Neither `8448` route permitted Funnel. The Mac already had an unrelated Funnel route on port `443`; it was not changed. The first replay used the initial patched candidate; the fixed `v0.2.0` corrected-source replay is recorded below.

| Leg | Observed result |
|---|---|
| Source and deployment | A `darwin/arm64` binary and a `linux/arm64` binary were built from the same candidate. The Mac binary SHA-256 was `b850b7fc68e55821f45fd8c9f4c97b9fab2684987668a4451a614e9d463cee95`; the Linux binary SHA-256 was `1795226c20e6b5ad5ffd760f66f20366a82a923e899cf100d8b39842babee800`, and the installed OCI bytes matched. Both configurations validated. |
| Transport | Both directions completed TLS certificate verification over the Tailscale address and port `8448`. A nonce-bound `sync verify` from each node returned `complete` at checkpoint `e36a327ddb6cbced00436b251f86e8d5ed9ff86b`. |
| Membership | Two reviewed, administrator-signed `endpoint_update` plans changed one endpoint at a time. The final signed membership revision was `84395a3806a1aeffa3c793f464b74aeea13ab262`. |
| Publication and nudge | The Mac published signed commit `991f621c93124e2dc97b804f6ebdd46982c7c5d2`. Its delivery settled as `accepted`; OCI admitted the nudge and imported `note.md`. The first inbox attempt deferred because the isolated OCI store lacked a resource observation. After that observation was recorded, guarded reconcile applied the import and a service restart settled the retained inbox row. Pair verification then returned `complete`. |
| Lost nudge | OCI's `8448` route and service were stopped. The Mac published `c30b541c587293a918e6d3e29005a3926a95ee73`; delivery remained unresolved. OCI's service restarted while its route was still absent and caught up from the configured Git ref without a nudge. After route restoration the retry settled as `accepted`; pair verification returned `complete` on the second commit. |
| Conflict stop | OCI made independent local commit `803e6a421c21cf1eef3d2752b788353083adfb4b`. The Mac then published signed commit `0da03f9b6382e44264ced492ae641129f6b1fdbd`. OCI reconcile returned `blocked` with `history_diverged`, leaving its local commit and file content intact. Pair verification stayed incomplete. No force update or automatic conflict resolution occurred. |
| Service managers | The patched candidate installed, inspected as matching/loaded/healthy, stopped, disabled, and uninstalled the exact temporary launchd and systemd user service definitions. State was preserved on uninstall. |

**Listener disposition.** The OCI Tailscale Serve route could proxy directly to the owner-only Unix socket. The macOS Tailscale network extension returned HTTP 502 when configured to proxy directly to its owner-only Unix socket. A temporary proxy bound only to `127.0.0.1:18089` forwarded to that socket; Tailscale Serve then proxied the private `8448` route to the loopback listener. The proxy was a disposable qualification fixture, not a product component. The peer client still verified the `.ts.net` certificate and refused redirects and ambient proxies. The product did not edit Tailscale configuration.

**Evidence boundary.** The signed commits, remote Git exchange, TLS routing, nudge, import, recovery, and conflict observations used the real two hosts and product binaries. To exercise the sync publication gate without a Hermes maintenance worker, the disposable SQLite stores received synthetic completed maintenance receipts and matching path observations through a fixture helper. This qualifies the sync path after the maintenance evidence boundary; it does not certify a real Hermes-generated receipt or a production vault. The Mac's initial clone preceded the administrator checkpoint and correctly entered a trust hold; the successful pair used a fresh clone at the signed checkpoint. The conflict fixture remains blocked by design.

**Bound exhaustion (AC-1808).** The two-host fixture exercised ordinary operation and an offline delivery obligation. Exhaustion is covered by deterministic checks on the same candidate, with retained obligations inspected after refusal:

| Bound | Executable evidence and disposition |
|---|---|
| Queue and retry attempts | `TestE21T1QueueAndAttemptBoundsFailClosed` keeps the first job when the queue is full and refuses a claim after the attempt ceiling. `TestE22T1NudgeAdmissionIsDurableIdempotentAndBounded` keeps the first inbox row and returns 503 for a distinct nudge at capacity. `TestE22T1DeliveryBackoffIsBounded` retains unresolved delivery through retries. |
| Payload, rate, and retained peer identities | `TestE22T1NudgeRejectsWrongAuthorityAndParserAmbiguity` rejects oversized bodies; `TestE22T1AuthenticatedRateLimitKeepsInboxBounded` returns 429 while its committed row remains. `TestPeerNudgeRetainedLedgerBoundKeepsExactReplay` refuses a new identity at the retained-ledger limit while accepting an exact replay. |
| Git history and subprocess | `TestE22T1OlderNudgeCoverageWalksConfiguredLocalHistory` keeps an over-bound history inconclusive. `TestRestrictedRunnerBoundsOutputAndKillsProcessGroup` returns explicit output-bound and timeout errors and terminates the process group. |
| Evidence retention, concurrency, and shutdown | `TestE21T1ResolvedRetentionIsChildrenFirstAndUnresolvedIsPreserved` retains unresolved work across pruning. `TestPeerNudgeConcurrentAdmissionKeepsBound` admits only within the cap. `TestE22T1ShutdownPreservesCommittedInbox` keeps admitted work through shutdown. |
| Verification deadline | `TestInterruptedVerificationExpiresOnlyAfterBound` preserves fresh planned work at the boundary and expires only older work; `TestE22T3LiveDeadlinePrecedesInterruptedExpiry` pins the live deadline before expiry. One-node or dirty outcomes remain incomplete in the G18 verification tests. |

The bound tests use disposable stores and transports. They establish fail-closed behavior at exhaustion; they do not claim a separate real-network overload run.

**Source attestation (2026-09-28).** The corrected source-code digest is `sha256:81cb1a5526bb75f44d2ef972dc73a11e5d85c143d2f2e093c0ef4f085d510da9`, computed by sorting every `*.go` path under `cmd/` and `internal/` and hashing each relative path, a NUL byte, its bytes, and another NUL byte. The Mac, OCI, and emulated amd64 source trees matched this digest. `GOFLAGS=-timeout=30m make verify` passed on native `darwin/arm64`, OCI-native `linux/arm64`, and emulated `linux/amd64`. The macOS run included real Watchman and launchd plist lint; the OCI host separately passed `systemd-analyze verify` for the example user units. The emulated Linux container did not provide `systemd-analyze`; the OCI host supplied that separate manager check.

The fixed `v0.2.0` corrected-source `darwin/arm64` flow binary SHA-256 is `1c2a9061b5e6a8b650210c664e1b7039f3c42d48ed9f3ed625da48990685662d`. The fixed `v0.2.0` `linux/arm64` flow binary SHA-256 is `682e7c1bb70b8861832040b1b4b7a5471e9a3d3dc00d22507dc9c50842098ef2`; its installed bytes on OCI matched. Both binaries used the same corrected Go source digest and accepted the `:8448` fixture configuration. These qualification builds carry the pre-closeout commit stamp; release artifacts are built separately. Status on the Mac's qualified fixture was `active`; the OCI fixture retained its deliberate `blocked/conflict` state under the new binary.

## Fixed v0.2.0 Replay

The first two-node flow above preceded consolidation of the endpoint parser. A corrected-source development build then repeated the matrix, but its Mac binary carried the Makefile's default `v0.1.8` development stamp. The final replay used fixed `v0.2.0` binaries on both hosts. It used fresh OCI state and a clone from the administrator-signed checkpoint `e36a327ddb6cbced00436b251f86e8d5ed9ff86b`; OCI first imported prior signed history.

The Mac published `ccc4edb516ca0b22bbd652a0e61bc763bc520da4`. Its nudge was accepted, OCI applied `note.md`, and both nodes returned `complete` for that commit. With OCI's service and `8448` route stopped, the Mac published `90fa8fd083f6be92096f51601848ff1c7467b588`. OCI restarted while its route was still absent and imported that commit from Git without a nudge. After route restoration, delivery settled as `accepted`, and both nodes returned `complete` for the same commit.

OCI then made independent local commit `5232fd9ec77667ed076944ecdf32903333649272`. After the Mac published `c737089aef3c7c467082dd1f0bde621b39f57ed3`, OCI reconcile returned exit 14, `blocked/history_diverged`, with no protected side effect. Its local commit and `note.md` SHA-256 `f2fc5badf31edb7bc65c562e5abc6cf5c5d69dfcd5f52f9d9d7cc8d285c071b7` were retained, and pair verification was incomplete. The fixed binaries also completed install, matching loaded/healthy inspection, stop, disable, and state-preserving uninstall on launchd and systemd user services. The replay reused signed membership created earlier; publication and subsequent operations used the fixed binaries. The disposable maintenance receipts remained synthetic, as described above.

The first final-replay OCI state path was too long for its Unix listener socket and `sync serve` failed with `bind: invalid argument`. The disposable state directory was moved to a shorter path before rerunning service and transport checks; no product or production state was changed. The runbook now calls out this deployment constraint.

The installed Hermes v0.21.0 passed the real adapter version probe. Its `kanban create` help lacks the frozen `--mutex-key` option, so `TestG8RealHermesTwoDestinationWalkthrough` skipped at its compatibility guard. This result does not qualify that installed Hermes for a production write route.

**Real-vault disposition.** No production vault was mounted in this qualification. Automatic import to a real vault remains subject to its exact group acknowledgement, a reviewed listener route, and separate activation authority. The macOS loopback bridge permits other local processes to reach the HTTP hop while it runs; peer authentication still applies, but the Unix socket's filesystem permissions do not guard that hop.

**Cleanup.** After both flow runs, both temporary `8448` Serve routes, the Mac loopback proxy, peer processes, exact managed service definitions, temporary SSH authorization, host-key additions, agents, and OCI private keys were removed. The remote user's original `authorized_keys` and `known_hosts` hashes were restored after the corrected-source replay as well. Isolated fixture repositories and state were retained outside the repository for evidence; the production vault and the unrelated Mac port `443` route were unchanged.

An isolated corrected-source rehearsal on OCI arm64 built all three `v0.2.0` release binaries twice with the same explicit pre-closeout commit `d10cc2f1895246f1b92f3b80ccdc604e8291d382` and build-time stamp `2026-09-27T15:53:19Z`. The two checksum files matched byte-for-byte, and `sha256sum -c` passed for all three binaries:

| Artifact | SHA-256 |
|---|---|
| `agent-dispatch-v0.2.0-linux-amd64` | `6f0cb46513478a7ea88958718ea38c67232937ce040258e8e575d4db1dfaef79` |
| `agent-dispatch-v0.2.0-linux-arm64` | `df84bffe07b88313290dbeac529c28e1ab7f507db6ce4f6833dc9f86b035cd80` |
| `agent-dispatch-v0.2.0-darwin-arm64` | `e4c5fb114e44465ce5f6385b10fce54ce136842d6c39c9df839db42193b249f9` |

The remaining independent hardening risks are tracked in [DF-004](../deferred-feedback/004-e22-final-observation-integration.md), [DF-005](../deferred-feedback/005-e22-endpoint-operations-hardening.md), and [DF-006](../deferred-feedback/006-doc-manifest-inventory-hardening.md). Final commit-stamped artifacts and whole-epic validation remain open. This record alone does not close G18 or authorize a tag, publication, or production activation.

The current inspection-only Plugin admits Agent Dispatch `>=0.1.6,<0.2.0` under ADR-0023. It therefore excludes v0.2.0 and must remain fail-closed. This handoff grants no Plugin restart authority; a later explicit admission is required.

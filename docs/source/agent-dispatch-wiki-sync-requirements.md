# Wiki Sync Requirements - Accepted Source Record

> **Source status:** Non-authoritative input retained for provenance
> **Received:** 2026-09-12
> **Document ID:** `REQ-DISPATCH-WIKI-SYNC/v1`
> **Program baseline:** `AD-WIKI-SYNC-PROGRAM/v1`
> **Original:** `/Users/draccoon/Workspace/RootKernel/hermes/agent-dispatch.md`
> **Original SHA-256:** `2ab8692f99eb31a0002bff74d8c93b02cc480ba89a5b6f23c9cd5f2e7c93581d`
> **Inspected Dispatch baseline:** `fcd75f1b231e4403c39f5c873bce25b10d95754a` (`v0.1.8`)
> **Inspected Plugin baseline:** `94d863e7a34ea682b3e347dc9d9ab629446e55e7`
> **Admission checkout:** `9f70a1200396d7abd3755184ca88242e07efae99`
> **Plugin prerequisite handoff:** `d029c956df76cfeb30680e1a8482fff2187e7f94`
> **Resolution authority:** D-030 and SOT 1.6.0

The request proposed Git-backed Wiki synchronization across a MacBook, an
Oracle Cloud Linux node, and later enrolled machines. It covered publication,
import, peer nudges, signed membership, all-member verification, plugin tools,
and service operations as one eleven-epic cross-repository program.

D-030 admits a smaller first release. The v0.2.0 target synchronizes one
Markdown vault between exactly two active nodes. It retains the request's
data-preservation, provenance, signing, authentication, crash-recovery, and
no-automatic-merge requirements. It does not admit N-member qualification,
attachment synchronization, automatic conflict resolution, or new Plugin
tools into the active roadmap.

The complete original request remains at the path and digest above. Normative
behavior is restated under `SYN-*` IDs in `docs/specs/required-spec.md`.
Architecture decisions live in ADR-0023 through ADR-0025. Implementation
ownership lives in roadmap epics E20 through E22.

The original document's epic/task, `D-*`, `X-*`, `EPIC-*`, `TASK-*`, and
`G0x` identifiers are foreign source labels that may collide with repository
identities. Only roadmap E20 through E22 define the admitted scope and order.

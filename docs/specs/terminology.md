# Terminology

Normative words **MUST**, **MUST NOT**, **SHOULD**, **SHOULD NOT**, and **MAY** have their conventional requirements meaning.

| Term | Definition |
|---|---|
| **Authoritative runtime** | The system that owns agent task execution and semantic outcome. For v0.1 this is Hermes. |
| **One-shot** | The v0.1 execution mode: a short-lived CLI process per trigger invocation. SQLite, not a daemon, coordinates concurrent processes. |
| **Resource** | A trusted operator-configured workspace alias, such as `vault-main`, resolved to an absolute local root only inside Agent Dispatch. |
| **Workspace** | The approved target-side working location a task is bound to, such as `dir:` plus a resolved root. It is fixed by configuration and cannot be changed by event payloads. |
| **Route** | A versioned operator policy binding one source, one resource, filtering rules, structural dispositions, and one or more named destinations. |
| **Destination** | A stable route-owned ID binding a target, profile, skills, workstream, workspace, serialization group, hints, and optional structural conditions. |
| **Destination revision** | Canonical digest of one destination's behavior-affecting configuration. Incompatible changes cannot reuse older child work. |
| **Destination lane** | The `(route_id, destination_id)` coordination unit with at most one unresolved authoritative task and one collapsed pending generation. |
| **Serialization group** | The effective per-destination group (`serialization_group`, the deprecated `mutex_key` alias, or `resource:<resource_id>`) that admits at most one active child per state database; an optional target-side mutex complements it and never replaces it (ADR-0021). |
| **Aggregate event** | Durable parent record for one normalized source/policy occurrence and the destination-selection results. Its status is derived from child records. |
| **Child dispatch** | One destination-specific durable dispatch intent and lifecycle beneath an aggregate event. |
| **Effective Watchman binding** | Configured logical resource root, actual Watchman root (the configured root itself since D-028), the schema-vestigial relative root `.`, and managed trigger identity treated as one lifecycle binding. |
| **Resource observation revision** | Monotonic fence advanced by path-fact mutation and compared before a full snapshot may replace resource facts. |
| **Route revision** | A canonical digest Agent Dispatch computes from behavior-affecting normalized route configuration. Decisions and records carry it; a change requires explicit operator acknowledgement before the route activates. |
| **Policy revision** | A canonical digest of the policy subset of route configuration. A not-yet-submitted batch is re-evaluated against the active policy revision before dispatch. |
| **Source observation** | An immutable record of what a source reported at one source position. It is evidence, not authority. |
| **Source event key** | Stable source-provided identity or position used to recognize retransmission. It is distinct from a Agent Dispatch ID. |
| **Settled** | Watchman trigger mode invokes Agent Dispatch only after observed changes stop for a quiet period. The payload is already one settled batch, so Agent Dispatch adds no second settle delay. |
| **Change item** | One normalized relative-path create, modify, or delete observation. |
| **Follow-up** | The single latest-state dispatch scheduled when a completed generation leaves unresolved work (a dirty generation or a pending reconciliation). A route holds at most one pending follow-up; consecutive follow-up chains are bounded (`MaxConsecutiveFollowups`) and resolve through UNCERTAIN when exceeded (E8-T1). |
| **Change batch** | A bounded, deterministically ordered set of relevant change items evaluated together. |
| **Manifest** | A bounded list of normalized relative paths and digests attached to a task or work receipt. It is untrusted activation evidence, not an instruction or a state snapshot. |
| **Route generation** | The logical maintenance generation for a route. While one Hermes task is unresolved, later batches increment its durable dirty generation instead of creating parallel work. |
| **Dirty generation** | Evidence that relevant changes occurred after the active dispatch snapshot. It guarantees a later re-evaluation, not a second immediate task. |
| **Policy decision** | Immutable output of deterministic route policy evaluation. It records a disposition and machine-readable reasons. |
| **Disposition** | One of `drop`, `dispatch`, `merge_pending`, `quarantine`, or `reconcile`. |
| **Sink** | A target adapter that maps a dispatch intent to one explicit public target interface and returns acceptance evidence. It never decides whether a dispatch should exist. |
| **Capability report** | A versioned, evidence-backed record of a target executable identity, public capabilities, response shapes, profiles, skills, and limits. Route activation binds its fingerprint. |
| **Reduced guarantee** | An explicitly documented operating mode for a route whose target lacks a required capability. The limitation is surfaced, never silently emulated. |
| **Dispatch intent** | An immutable, durable request Agent Dispatch intends to submit to one target. |
| **Dispatch attempt** | One concrete external submission attempt. Attempts may be retried while the dispatch intent remains the same. |
| **Lease** | An expiring database-backed ownership claim ensuring only one process owns a dispatch attempt. It is a recovery tool, not proof that a remote side effect did not happen. |
| **Dispatch receipt** | Evidence about target acceptance. Portable states are `accepted`, `rejected`, and `unknown`. Earlier drafts called this record an acceptance receipt; it is the same record. |
| **Dead letter** | Terminal dispatch state where automatic progress has stopped. A dead-lettered dispatch remains inspectable and requires an explicit retry, reprocess, rerun, or discard action. |
| **Execution projection** | Optional target status such as queued, running, succeeded, failed, or canceled. It is not required from every sink. |
| **Work receipt** | A record submitted by a cooperating Hermes task or agent describing run identity and changed paths/digests. It supports feedback attribution but is not trusted until validated. |
| **Notification intent** | Durable channel-neutral work created transactionally from a configured state transition and delivered independently of task outcome. |
| **Notification attempt** | One bounded sink delivery attempt using the notification's stable idempotency identity. |
| **Content fingerprint** | SHA-256 of a canonical, sorted projection of normalized change evidence. It compares effective content evidence and is not an event ID. |
| **Idempotency key** | Stable key supplied to a target so repeated submission of the same dispatch intent can resolve to one accepted task. |
| **Observation ID** | Time-ordered unique Agent Dispatch ID for a source observation. |
| **Batch ID** | Unique Agent Dispatch ID for a change batch. |
| **Decision ID** | Unique Agent Dispatch ID for a policy decision. |
| **Dispatch ID** | Unique Agent Dispatch ID for a dispatch intent. |
| **Attempt ID** | Unique Agent Dispatch ID for one dispatch attempt. |
| **External reference** | Target-provided task, run, or request identifier. |
| **Durable acceptance** | Target acknowledgement that work has been persisted and can be queried after target restart. A transport-level 2xx response is not automatically durable acceptance. |
| **Meaningful change** | A configured Markdown create or delete, or a modify whose effective digest changed. Metadata-only changes and excluded paths are not meaningful. |
| **Latest-state processing** | Hermes receives change evidence but performs maintenance against the current vault state at execution time. |
| **Reconciliation** | A new assessment of current authoritative state after overflow, lost position, uncertainty, or explicit request. It creates new observation lineage. |
| **Retry** | Another submission attempt for the same dispatch intent and idempotency key. |
| **Reprocess** | Apply the current route policy to a retained observation or batch, creating new decision lineage. |
| **Rerun** | Intentionally request new agent work from a previously accepted dispatch. It creates a new dispatch ID and idempotency key. |
| **Replay** | Ambiguous legacy term. It is not used as a v0.1 CLI operation; use retry, reprocess, rerun, or reconcile. |
| **Quarantine** | Durable hold for an event that must not be automatically submitted. Release requires an explicit operator action and creates new decision lineage. |
| **Protected path** | A route-configured path that cannot enter automatic maintenance work. Agent Dispatch does not edit or rewrite it. |
| **Fresh instance** | Source condition indicating the previous incremental source position is no longer usable. It triggers reconciliation rather than ordinary dispatch. |
| **Overflow** | Source condition indicating the reported change list may be incomplete. Partial dispatch is forbidden; one reconciliation is scheduled. |
| **Hermes companion skill** | A skill shipped by Agent Dispatch that instructs a Hermes agent how to consume a Agent Dispatch task and optionally submit work receipts. It is not a Hermes plugin. |

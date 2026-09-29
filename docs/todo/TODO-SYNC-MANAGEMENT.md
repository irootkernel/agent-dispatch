# E23: Constrained sync management request provider

## Delivery boundary

Master requested this Core follow-up and related Plugin planning on 2026-09-30.
The [roadmap](../roadmap/roadmap.md#e23-constrained-sync-management-request-provider)
is the only owner of E23 status, task identities, and order. This dossier is
the cross-task map; the task sections in that roadmap own their checklists.

The intended sequence is current Core/inspection-Plugin v0.2.0 release,
Core E23, then Plugin EPIC-008. Planning may occur before the release; E23
runtime changes must remain outside the current release candidate. Deployment
of a real vault is a separate operator action, not a prerequisite to doing
isolated development. The successor product version is selected in E23-T1.

## Authorities

| Concern | Owner |
|---|---|
| Successor requirements | [SMR-001 through SMR-015](../specs/required-spec.md#20-post-v020-constrained-sync-management) |
| Proposed command and recovery contract | [Sync management contract](../contracts/sync-management-contract.md) |
| Architectural choice | [Proposed ADR-0026](../architecture-decision-records/0026-constrained-sync-management-requests.md) |
| Acceptance | [G19](../specs/acceptance-criteria.md#g19-constrained-sync-management-provider-post-v020) |
| Current sync semantics, preserved | [Two-node sync contract](../contracts/sync-contract.md) |
| Consumer | `agent-dispatch-plugin`, `docs/todo/TODO-SYNC-MANAGEMENT.md`, EPIC-008/TASK-028 through TASK-032 |

Repository-relative names identify the consumer; an absolute checkout path is
not part of the delivered contract.

## Scope choices used by this plan

The initial actions are `sync-now`, `verify-all`, `retry`, local `pause`, and
local `resume`, subject to an operator-owned allowlist disabled by default.
New publication/signing, remote-node administration, membership/checkpoints,
service installation, automatic conflict resolution, multiple groups, and
additional nodes stay out of scope. Management requires neither a Plugin nor
an LLM to execute correctly.

Use an expiring prepared handle before submission so response-loss recovery
does not require a Plugin database or unverified Hermes invocation context.
Core owns exact lookup and request-to-effect evidence. Local control uses a
bounded separate admission reserve and the shared control transaction; long
work uses the existing worker. The detailed contract is still subject to
E23-T1's schema and security review.

## Sequential work

| Order | Task | Deliverable |
|---|---|---|
| 1 | E23-T1 | Freeze the successor scope, authorization model, command/schema/error contract, limits, and compatibility boundary |
| 2 | E23-T2 | Add the durable request ledger, immutable bindings, additive migration, and retention primitives |
| 3 | E23-T3 | Implement bounded preparation, idempotent admission, exact lookup, and capability gating |
| 4 | E23-T4 | Execute reconciliation/verification and atomic local controls through shared Core paths |
| 5 | E23-T5 | Complete identity-preserving retry, process-loss recovery, expiry, pruning, and restore behavior |
| 6 | E23-T6 | Prove the security, fault, saturation, fairness, and legacy-regression acceptance matrix |
| 7 | E23-T7 | Qualify the exact provider on the paired hosts and deliver the immutable Plugin handoff |

Each task is one bounded reviewed commit-sized work unit. Do not mark a task
complete merely because later tasks are expected to finish its own acceptance
criteria. Intermediate runtime code must not advertise an enabled management
capability until its required execution and recovery paths exist. Preserve the
current one-active-task rule and do not start a Plugin runtime task in parallel
as a substitute for a missing provider result.

## Contract-freeze checklist for E23-T1

- [ ] Select the post-v0.2.0 version and record the frozen current release pair.
- [ ] Accept or replace ADR-0026; keep operator preauthorization distinct from human confirmation.
- [ ] Freeze prepare/submit/show and capability argv, handle grammar, immutable fingerprints, exact action inputs, and trusted config handling.
- [ ] Freeze lifecycle states, receipt meanings, caller-unknown outcomes, result-to-exit mappings, and exact request/job associations.
- [ ] Freeze policy/config/incarnation/control bindings and revocation behavior, including lookup after management is disabled.
- [ ] Define data-plane and reserved control capacities, preparation expiry, terminal retention, lookup/evidence limits, execution deadlines, and cumulative retry limits.
- [ ] Specify paused/stopped-service admission, fair worker dispatch, atomic pause/resume results, and no self-blocking verification.
- [ ] Specify supported migration/rollback/restore and old-handle refusal without retrospective request creation.
- [ ] Commit closed schemas, positive/negative fixtures, compatibility rules, and an independent management digest/checksum checker outside the frozen v0.2.0 bundle.

These are implementation-contract decisions owned by the task, not unanswered
product questions that require Master to design the storage schema. Return to
Master before expanding signing authority, remote control, action scope,
per-request human approval requirements, or the current release boundary.

## Existing feedback brought into scope

DF-007's verification admission/deadline concern is relevant: inject a stalled
transport and short deadline, test saturation, and preserve planned/expired
verification evidence without a false complete result. DF-008's membership
helper and SQLite ownership concerns apply where execution is extracted;
result parsing must be tested against real command-produced bytes if reused.
Use DF-002's crash-injection and DF-003's recovery-consolidation observations
for touched paths. These are test/design inputs, not claims that the deferred
items are already closed. Unrelated endpoint, log-path, and manifest work does
not become a release blocker merely because it shares that inventory.

## Final handoff checklist

- [ ] Every SMR requirement has a task and G19 scenario with inspected evidence.
- [ ] Public Core CLI tests work without a Plugin implementation or direct test mutation of private production state.
- [ ] Deterministic gates retain Core's three-platform scope; native Darwin arm64/Linux arm64 paired behavior and service lifecycles use one exact provider candidate.
- [ ] The handoff includes version, full commit, artifact SHA values, both contract identities, schemas/fixtures, failure outcomes, limits, and migration/rollback guidance.
- [ ] Ordinary manual publication and the existing peer service keep their signing-key separation.
- [ ] Plugin TASK-028 can admit the provider without guessing paths, interpreting latest status as a request receipt, or silently broadening its binary allowlist.
- [ ] Final review has no unresolved acceptance blocker; publication, installation, and production activation remain separately authorized.

The paired handoff initially covers Darwin arm64 and Linux arm64. It does not
change Core's SCP-008 requirement or grant native Linux amd64 Plugin support.
Plugin EPIC-009/TASK-033 remains independent of E23 and EPIC-008.

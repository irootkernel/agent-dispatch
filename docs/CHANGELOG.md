# SOT Changelog

## 1.1.17 - 2026-08-30

E13-T4: documentation truth, release proof, and the local v0.1.5
candidate close the roadmap at 75/75:

- VALIDATION.md gains its Gate G9 section — every AC-901 through
  AC-906 criterion mapped to its executing test (the outbox
  transactional/dedup/retention suite, the sink transport and CLI
  suites, the G9 walkthrough and lifecycle coverage, and the skills
  packaging proof) — plus the release-proof record: make verify green
  on darwin/arm64, two byte-identical `make release VERSION=v0.1.5`
  builds with SHA256SUMS, and the explicit no-push, no-production, no-
  Hermes-change handoff;
- the docs README baseline section records the now-evidenced G6-G9
  gates and the v0.1.5 local-candidate posture; the release
  checklist's v0.1.5 addendum closes all eight items against their
  owning gates; RELEASE-NOTES-v0.1.5.md ships with the feature
  summary, artifact record (the real darwin-arm64 artifact name), and
  known state; and the roadmap records E13 Completed on the summary
  row and the epic body with task 75/75, the final Current State
  (75/75, no next task), and the local-tag handoff;
- review round 1 hardening (nine findings, all verified valid and
  fixed): VALIDATION's header, package-target line, and deferred list
  stop declaring G9 Planned and SOT 1.1.13 beside the new evidence;
  the release notes name the artifact the release target actually
  produces and claim the candidate posture instead of post-publication
  supersession; the architecture overview, the installation guide, and
  the specs README drop their G9-pending qualifiers; and the AC-904
  walkthrough leg asserts no absolute environment path enters a
  notification payload beside the note-body and credential scans.

## 1.1.16 - 2026-08-30

E13-T3: the operator and worker skills and the G9 operational
walkthrough land (BND-003/BND-004, FBK-006, FBK-009 through FBK-012,
CLI-012, NTF-*, TST-012 through TST-014):

- the operator skill ships as 2.0.0 with the v0.1.5 compatibility
  declaration and the notification operational guidance: the drain's
  bounded one-attempt posture, the probe's nothing-created guarantee,
  the explicit refused-notification retry under the stable idempotency
  identity, the configuration-owned sink/credential posture, and the
  history-preserving disable/removal exits;
- the worker skill ships as 1.2.0 with the scope-discipline guidance
  beside its existing untrusted-manifest, latest-state, and
  work-receipt/v2 rules: stay inside the task instruction's workstream,
  never expand into a sibling lane's workstream, and treat route
  exclusions as the operator's scope decision — the occurrence's
  selection defines the follow-up, never a per-file re-evaluation;
- `internal/cli/g9_test.go` proves AC-905 deterministically: one vault
  change on the two-destination fixture fans out to both lanes, both
  runs complete through the work-receipt surface, and the configured
  notification carries the completion to both sinks (the webhook
  capture proves the payload bytes carry the event and no note body or
  credential; the log lines land on stderr); the lifecycle walkthrough
  covers failure diagnosis through events show, the refused
  notification's explicit retry, disable with the durable history
  preserved, managed-trigger removal leaving no binding with the audit
  history intact, and the quiesced system still answering every
  inspection; the packaging test pins both skills' versioned guidance
  and manifest coverage and installs the operator skill through the
  documented public mechanism into a disposable Hermes profile; and the
  isolated real-Hermes leg (disposable board under a redirected HOME,
  a real Watchman binding on the disposable vault, detection through
  receipt to the delivered notification, managed-trigger removal
  afterwards) is skip-guarded as the documented TST-007
  environment-dependent evidence gap where the installed Hermes
  create surface drifts from the frozen 0.19.1 flags — the
  deterministic legs own the gate;
- review round 1 hardening: the G9 suite shares the E13-T2 capture
  fixture (one parameterized loopback-TLS scaffolding instead of two),
  the real-Hermes leg reuses the shared testsupport hermes guard and
  bounds every cleanup subprocess with the G8 30-second deadline
  instead of an unbounded copy.

## 1.1.15 - 2026-08-30

E13-T2: notification sinks, retry commands, and scheduling land
(NTF-004 through NTF-009, CLI-013, SEC-011 through SEC-013, TST-014,
OPS-013):

- the structured log sink emits each stored notification-event/v1
  payload as one JSON line on the operator's stderr — stdout keeps the
  one-envelope contract (CLI-001/002) and the structured log stream
  owns the delivery lines (OPS-001 posture); the authenticated HTTPS
  webhook sink (internal/adapters/notificationsink) reuses the strict
  transport posture of the hermes-webhook target: https-only endpoints
  without embedded userinfo, the secret resolved only at send time and
  redacted from every captured diagnostic, a dedicated idempotency
  header no authentication or transport header can collide with,
  redirects surfaced as definite routing refusals (never followed, no
  ambient proxy), bounded payload, response capture, and execution
  time (SEC-011 through SEC-013); the four-way outcome contract maps
  2xx delivered, the definite-refusal statuses refused, throttling and
  server failures retryable, and non-dial transport failures ambiguous
  — at-least-once under the stable ntfidem- identity (NTF-006/NTF-007);
- `notifications test|list|retry|drain` ship (CLI-013): the probe
  delivers the notification-event/v1 envelope with the dedicated
  `test` event value and a stable per-(route, sink) idempotency
  identity, storing nothing and touching no source event or Hermes
  task (NTF-008); the listing joins each intent's attempt count and
  last outcome (NTF-004); the explicit retry re-arms one refused
  notification and refuses a delivered one at exit 4; and the drain
  first evaluates the configured drift classes per
  notification-enabled route — the missing or changed Watchman binding
  enqueues watchman_drift and the capability, profile, and skill
  findings enqueue integration_drift, each exactly once per drift
  appearance through the finding-digest occurrence (OPS-013, AC-901) —
  then performs one bounded attempt per pending notification, oldest
  first, with delivery outcomes as data and never exit codes (NTF-005
  posture);
- `status` projects the notification by-state counts and warns on
  pending delivery work with the drain command (observability-and-
  operations §10); the launchd schedule example chains
  `notifications drain` after the scheduled reconciliation inside its
  one shell recipe (the E13-T2 one-shot schedule integration), and the
  schedule test pins both legs;
- coverage (TST-014): the sink adapters classify every status class,
  redact an echoed credential from every diagnostic, keep the stable
  idempotency header identical across retries, and fail closed on
  plain-http endpoints, embedded userinfo, missing secret references,
  and colliding headers; the CLI suite proves end to end that the
  probe creates nothing, the drain delivers the log sink while the
  stalling endpoint classifies ambiguous and stays pending, the
  recovered retry presents the same key per notification, the refused
  notification re-arms and delivers through the explicit retry, the
  two sinks resolve independently, a persisting drift never
  re-notifies, and no payload, attempt diagnostic, or ordinary output
  retains the resolved secret or the vault's note body.

## 1.1.14 - 2026-08-30

E13-T1: the notification event contract and transactional outbox land
(ADR-0019, DUR-016, DAT-010, NTF-001 through NTF-005, NTF-007, OPS-013):

- SQLite migration v16 (`notification-events-attempts`) creates the
  durable outbox: `notification_events` holds one channel-neutral intent
  per logical (event, optional destination, transition occurrence, sink,
  notification-policy revision) identity — the deterministic `ntf-` id
  IS that five-component projection, so the unique key collapses a
  replayed transition, an aggregate rerun, or a crash-recovery
  re-evaluation onto its existing row instead of notifying twice
  (NTF-003, AC-902) — and `notification_attempts` holds the independent
  delivery lifecycle whose ambiguous/retryable outcomes leave the
  notification pending under its stable `ntfidem-` idempotency key while
  definite outcomes resolve it (NTF-004/NTF-007); nothing in the
  notification tables can rewrite dispatch, lane, receipt, or work state
  (NTF-005);
- the reportable transitions enqueue inside their owning transactions
  (DUR-016): a lane completion maps onto work_completed / work_failed /
  work_exhausted by its resulting lane state; the expired-lease recovery
  and a recorded unknown attempt enqueue delivery_unknown; the quarantine
  hold enqueues quarantined; and every pending-reconcile appearance —
  reconcile classification, merge-into-idle, quarantine release, and the
  uncertain resolution that found work — enqueues reconciliation_required
  exactly when the route was not already pending (OPS-013), with the
  occurrence discriminator keeping replays deduplicated while a genuinely
  new generation notifies again;
- the policy resolver is injectable and nil-disabled (NTF-001): the CLI
  installs it from the loaded configuration so a transition commits its
  intents under exactly the policy the operator sees; doctor, read-only,
  and test stores stay inert; a store-level event filter (the effective
  event set) is enforced per sink before any insert;
- the default event policy ships (NTF-002): with a sink present, an
  omitted or empty `events` list selects the seven defaults (work
  completed, exhausted failure, unknown delivery, quarantine,
  reconciliation required, integration drift, Watchman drift;
  work_failed stays configurable but non-default); the config schema and
  semantic validation accept the omission, and the notification-policy
  revision digests exactly the effective event set plus sink
  identity/kind references — endpoints and authentication references are
  outside the projection, so repointing a sink never re-identifies its
  notifications (SEC-012);
- the `agent-dispatch.notification-event/v1` and
  `agent-dispatch.notification-attempt/v1` record families become
  executable schemas and examples (canonical-record-contracts §10) with
  content-derived example identities; resolved notification evidence
  prunes past retention with its attempts while pending evidence stays
  retained and inspectable (NTF-004), carried as a new maintenance-prune
  class on the resolved-quarantine horizon;
- coverage: store-level transactional creation, per-sink fan-out,
  five-component identity, replay/rerun dedup, attempt lifecycle and
  source-state immutability, delivery-unknown/quarantine/reconciliation
  emission points with once-per-appearance semantics, referential
  integrity (the enforced FK refuses orphan attempts), v15→v16 upgrade,
  disabled-store silence, and pruning — plus the config-level effective
  policy/revision suite and one end-to-end CLI walkthrough where a real
  `work complete` commits its intents through the full stack with a
  hostile note body provably absent from the payload (SEC-011);
- review round 1 hardening: the payload projection's safety is enforced
  at the enqueue boundary (at most 12 bounded, control-character-free
  source values; a violation fails the owning transaction loudly instead
  of leaking), a route-lookup error during the unknown transition now
  fails the transaction instead of silently dropping the notification,
  and a late terminal attempt after resolution records as evidence
  without rewriting the first resolution; the persistence architecture
  table inventory and the attempt contract's purpose row are
  synchronized with the shipped v16 surface.

## 1.1.13 - 2026-08-29

E12 epic validation: the reconciled member-task review residuals are
remediated as one hardening batch (FAN-005, FBK-010/FBK-011, DAT-010,
CON-008, CLI-013), folded together with the epic whole-review round-1
through round-3 residuals (FAN-002/FAN-005, CON-007/CON-008, FBK-012,
DAT-010):

- SQLite migration v15 (`merge-selection-evidence`) records each merged
  batch's destination-selection summary at merge time — and the arrival
  path stamps the same occurrence evidence on the batches it commits —
  and the follow-up lane filter reads OCCURRENCE-level FAN-005 semantics
  from it: a change whose path alone fails a path_include stays in the
  selected lane's follow-up (the occurrence selected the lane), a
  sibling lane still excludes it, and legacy rows without selection
  evidence keep the per-change fallback — no more silent work loss on
  conditioned multi-lane routes; the evidence is the occurrence's FULL
  selection, written once and never narrowed: the merge path UNIONs the
  occurrence's selection into the batch (a first committing lane that
  merges no longer overwrites it with its own lane) and the later
  lanes' merges union onto the same batch, so a conditioned burst with
  every lane busy keeps every selected lane's follow-up whole (round 2);
- a digest-less remaining-scope entry classifies as modify (the
  conservative default): deletion evidence now requires the exact
  before-present/after-absent pair, never a fabricated removal;
- the reconcile path fans the reconciliation intent out per certified
  destination — one shared aggregate and decision, one child per lane
  with origin reconcile — through the existing single-intent store
  surface plus the app-layer sibling commit
  (`dispatch.CommitReconcileSiblings`); the siblings commit DURABLY
  FIRST, so the crash window leaves ready siblings the drain submits
  instead of a first lane with no durable marker of the remaining
  selection, and a sibling's non-slot-held commit failure is never
  silent — it rides the `reconcile_lanes` envelope member, stderr, and a
  non-zero exit while the first lane still delivers (slot-held skips
  stay warnings); the sibling outcomes stay visible on the FIRST-lane
  failure path too (the error envelope's result slot carries the lane
  listing and stderr names each committed sibling); the retry shape is
  truthful — a retry after partial failure is a fresh occurrence whose
  already-committed lane skips by constraint (held slot or the
  content-derived duplicate idempotency key), never a duplicate child;
  each sibling's creation audit names origin reconcile and its own
  destination; the sibling outcomes are OWNED at the app seam (the
  `ReconcileSiblingJournal` the CLI only renders from — success, skip,
  and failure paths alike); and a storage fault in a sibling commit
  classifies as storage (20), never internal;
- store-boundary hardening in `saveFanoutTx`: every persisted
  destination-revision record must be the content address of its
  projection bytes, and a child's destination reference must have a
  durable revision row (inserted in the transaction or previously) —
  dangling references fail closed as NON-RETRYABLE validation failures
  of the invalid-configuration family (never replayed conflicts), and
  the failure is never lane-isolated: a lane outcome of that class
  keeps its live typed error and surfaces as the command-level
  configuration failure (exit 3) even when sibling lanes succeeded; the
  dst- derivation itself is ONE canonical function
  (`records.RevisionOfProjection`) shared by the config-level
  computation and the store verification, and the selection evidence's
  canonical form is ONE shared derivation too
  (`records.CanonicalDestinations`: sort + dedupe) used by both the
  union writes and the batch-column encoding;
- work-receipt contract truth: `--manual-reason` outside the blocked
  outcome rejects naming exactly the blocked outcome — validated against the
  EFFECTIVE outcome (the v2 document's status first), so a blocked
  document carrying its matching flag reason accepts, and a document
  manual_reason beside a non-blocked outcome rejects the same way
  instead of being silently discarded; a change set beside a blocked
  outcome rejects (blocked takes its reason); a whitespace-only
  document manual_reason rejects like the flag form; the
  document-vs-flag scope conflict compares canonicalized values, not
  struct order; a v2 document's `completed_scope` persists on the
  receipt row and must be a subset by path of the document's own
  changes (unauditable scopes reject); and `completion_evidence:
  present` requires a valid TERMINAL receipt — a valid begun receipt
  renders the running state, never completed, and classifies the
  aggregate in-progress rather than evidence-gap (FBK-012);
- the mixed activate+merge dispatch envelope reports `merged_lanes`
  beside the fan-out (with stderr notes); `BoundLaneError`, the
  resolver diagnostics, and the package's other bounded texts truncate
  through ONE shared rune-bound helper at ONE shared operator-text
  bound; the operator-text audit contexts (discard, retry, rerun)
  build through the JSON encoder; and the live-lane resolver surfaces
  the bounded underlying configuration cause when resolution fails;
- the published contracts follow the code: the fan-out dispatch
  envelope lives under `dispatch` in cli-spec (not `work begin`),
  §9 Reconciliation documents the per-lane reconcile fan-out, where
  `reconcile_lanes` rides in each envelope shape, the sibling policy
  with its retry and exit-code behavior, that `reconcile_lanes` lists
  the SIBLING lanes only, and the reconcile/dispatch help lines name the
  invalid-fanout-record exit-3 cause; cli-spec §5 names the same cause
  beside no-destination-selected; `events show`'s aggregate-status
  precedence documents the begun in-progress class in cli-spec AND the
  observability guide (the shipped terminal-receipt rule); the batch
  record's canonical contract carries the v15 `selected_destinations`
  evidence; `SaveBatch` takes the explicit `BatchRecord` form instead of
  a widened positional list; and `CommitMergePending` never writes
  through the caller's lineage;
- test gaps closed: rebuild-path child persistence, the filter's
  fail-closed arms, lane-aggregation precedence (ACTIVE over
  FOLLOWUP_READY, first-holder-in-destination-order, ListRoutes agreeing
  with LoadRouteState), the begun-receipt events-show case, the sibling
  commit policy (including the duplicate-key retry arm) and its
  non-zero exit, the busy-lane production-shaped merge-evidence path
  through ArrivalFanout, the mixed merge+invalid-record arm keeping the
  live typed error for the command-level exit-3 class, and
  content-addressed revision pairs in every test fixture the store now
  verifies.

## 1.1.12 - 2026-08-29

E12-T4: the multi-destination and completion gate G8 closes with its
executable AC-801 through AC-806 evidence suite (FAN-*, CON-001,
CON-007 through CON-010, FBK-009 through FBK-012, TST-005/TST-007/
TST-013):

- `internal/cli/g8_test.go` drives every criterion through the real CLI
  surface over the deterministic stub Hermes: one aggregate with two
  profile-distinct Hermes tasks (AC-801), same-profile lanes with
  distinct identities and DAT-014 keys (AC-802), the failed lane's retry
  reusing the completed sibling with no duplicate task and the
  re-observed same generation refusing on the idempotency constraint
  (AC-803), the destination-edit pause until production
  re-acknowledgement (AC-804), the four receipt outcomes end to end
  including the blocked lane's drain-idle posture and the budget
  exhaustion into the UNCERT operator hold (AC-805), and the
  completion-evidence gap rendered actionable with the aggregate naming
  it (AC-806);
- the stress leg races ten concurrent fan-out arrivals over a
  two-destination route: one active child per lane, no cross-lane
  blocking, per-lane dirty generations, no duplicated (occurrence,
  destination) child, and exactly one external submission per lane
  winner (the cross-process leg stays the cited g2/crashbin coverage —
  `TestG2AC207` interrupts every migration unit through v14);
- the isolated real-Hermes walkthrough is skip-guarded
  (TST-007): two tasks beneath one aggregate on a disposable
  hard-deleted board, receipts recorded, `events show` complete — and
  the gate never depends on it;
- the stub Hermes create surface records the assignee beside each task
  so the two-profile criterion is observable on the deterministic
  fixture (test-support only; the frozen interface is unchanged);
- `docs/VALIDATION.md` gains the G8 gate evidence section with the
  per-criterion table, the stress/concurrency evidence, the cited
  migration/crash/race/schema coverage, and the Hermes-walkthrough
  posture.

## 1.1.11 - 2026-08-29

E12-T3: work-receipt/v2, aggregate inspection, and completion evidence
(FBK-009 through FBK-012, DAT-011, FAN-010, CLI-013, OPS-011):

- `docs/schemas/work-receipt.schema.json` moves to v2: the status
  vocabulary gains `partially_completed` (both scopes required, the
  remaining scope non-empty) and `blocked` (a non-empty bounded
  `manual_reason` required); v1 documents remain valid (the stored
  history stays readable, DAT-009 posture), and three validating
  examples ship (partial, completed, blocked);
- SQLite migration v14 rebuilds `work_receipts` with the widened status
  CHECK plus the scope and manual-reason columns — no historic row is
  rewritten (the v2 columns default empty on v1 rows);
- `work complete --status completed|partially_completed|blocked`
  (completed stays the default): partially_completed requires
  `--remaining-manifest` (FBK-010) — the current child closes its lane
  and exactly one follow-up on the SAME destination lane carries the
  remaining scope unioned with the lane's unresolved dirty changes
  (an empty remaining scope rejects with guidance to use completed);
  blocked requires `--manual-reason` (FBK-011) — the receipt records the
  reason and NOTHING else happens: the lane stays active with its child,
  nothing auto-runs (the automatic retry machinery only touches
  retry_wait/dead-lettered work), and resolution is operator-only
  (`work complete`/`work fail` later, or a rerun);
- the full-document form routes through the document's own outcome
  (review round 1): a work-receipt/v2 document carrying
  `partially_completed` or `blocked` submits through `--manifest` alone —
  its scopes or manual reason are the outcome's members — while an
  explicit `--status` that disagrees, or a `--remaining-manifest` that
  differs from the document's remaining scope, rejects as a conflict;
  `--remaining-manifest` outside the partial outcome is a usage error
  and a bare change-array manifest beside `blocked` rejects with
  guidance (blocked takes its manual reason);
- the receipt views name the child lane explicitly (DAT-011): the
  work-receipt read model, `receipts list` work rows, and the intent
  lineage carry `destination_id` (empty for pre-cutover legacy
  dispatches), and the lineage surfaces the blocked reason;
- `events show <aggregate-id>` (CLI-013, FAN-010): one occurrence's
  aggregate with its selection summary and closed reasons, and every
  child's separate destination, intent-state, acceptance, execution,
  work-receipt, retry, and completion-evidence projections; the
  `aggregate_status` projection is the worst child class — evidence-gap
  > manual-intervention > failed > in-progress > completed — and
  accepted work without a valid attributable work receipt (absent or
  invalid) renders `completion_evidence: missing` with the actionable
  next step, never "completed", while a never-accepted child renders
  `not-applicable` — its pending state is the story, not a false
  "present" (FBK-012, AC-806 posture);
- `status` grows one bounded lane summary row per destination (OPS-011);
- the rendered task receipt block, the worker skill, and the task
  contract document the four outcomes and their exact CLI invocations
  (no Hermes installation or modification is involved).

## 1.1.10 - 2026-08-29

E12-T2: per-destination lane coordination, the structural selection
evaluator, and multi-destination fan-out (FAN-002 through FAN-005, FAN-009,
FAN-011; CON-003, CON-007, CON-008, CON-010):

- SQLite migration v13 adds `destination_lane_state` (primary key route +
  destination) carrying the lane's single-active slot, dirty generation,
  and follow-up chain; the backfill copies each route's in-flight
  coordination onto the lane its active dispatch belongs to (the child
  row's destination, else the synthetic `__legacy__` lane), and the
  `route_runtime_state` coordination columns become the frozen v12-era
  history plus the route-level QUARANTINED/UNCERTAIN hold — a hold blocks
  every lane (CON-007);
- the closed destination-selection evaluator ships with FAN-005 semantics
  (values within one present condition class OR, present classes AND,
  absent classes select unconditionally) over the FAN-004 structural
  classes only — path include/exclude, operations, classification, and
  policy outcome — failing closed on matcher errors and reporting the
  closed machine reasons;
- `dispatch` fans one occurrence out per selected destination under ONE
  aggregate event and ONE shared decision: one child intent per lane with
  its own request, DAT-014 key, and lane slot, the full selection summary
  and every referenced destination revision on the aggregate, and
  per-lane arrival (activate-or-merge) where one sibling's held slot
  never blocks the others (CON-008); the envelope lists each lane's
  dispatch and reports any lane that failed beside its successful
  siblings with bounded, redacted error text (a failed activation keeps
  its durable dispatch ID for the operator exits); an occurrence no
  destination's conditions select fails closed at the configuration
  class creating nothing;
- the follow-up collapse is lane-scoped (CON-008): a completing lane's
  follow-up manifest and attribution decision see only the dirty changes
  its own destination's conditions select, so a sibling lane's
  conditioned-out work can never ride along — a child-linked dispatch
  whose lane conditions cannot be resolved fails the completion closed;
- enablement lifts the single-destination bound: a route enables with
  several destinations under ONE shared target (fail-closed on differing
  targets, FAN-011) with every lane's profile checked on the board;
- the rendered task body names the destination lane and its workstream
  inside the trusted instruction block (FAN-009), from trusted
  configuration data only.

## 1.1.9 - 2026-08-29

E12-T1: the aggregate-event, destination-revision, and child-dispatch
record families land (DAT-010 through DAT-014, FAN-001 through FAN-003,
FAN-010, FAN-012):

- SQLite migration v12 adds `aggregate_events`,
  `destination_revisions`, and `child_dispatches` beside the untouched
  historic tables: no pre-cutover row is rewritten, a legacy intent
  keeps its exact lineage and reads with empty child linkage
  (DAT-012), and `UNIQUE(aggregate_id, destination_id)` enforces one
  child per selected destination per occurrence (FAN-003);
- the aggregate-to-child creation is one transaction with the intent
  across every creation path — arrival, follow-up, rerun, rebuild,
  quarantine-release replacement, and reconciliation — each recording
  its origin, the canonically ordered selection summary, and one
  append-only audit row;
- the child idempotency key is the DAT-014 destination-scoped
  projection (`agent-dispatch:v2:` over route ID and revision, source
  generation and fingerprint, destination ID and revision, workstream,
  target scope, and contract version): sibling destinations and
  destination revisions can never collide, retries keep the key
  (CON-009), and declaration order changes neither the revision, the
  selection order, nor the key (FAN-012, pinned by the
  content-addressed projection test);
- the destination revision persists with the exact projection bytes it
  digests, so a stored record verifies itself;
- `dispatches list`, `dispatches show`, and the intent snapshot surface
  the child linkage; the stored request carries an optional destination
  block that reruns, rebuilds, and follow-ups derive their lane from
  through one shared DAT-013 precedence (stored block, snapshot linkage,
  live certified lane), with legacy requests resolving the live lane —
  persisting the referenced destination-revision record — or failing
  closed.

## 1.1.8 - 2026-08-28

E11-T4: the discoverable operator surface and gate G7 land (BND-003,
BND-004, CLI-009 through CLI-012, CLI-014, HER-011 through HER-018
re-verification, TST-012):

- every root and group parser accepts -h and --help at exit 0 with the
  full discovery contract — subcommands, flags, defaults, output
  modes, exit codes, side effects, approvals, an example, and the next
  safe command;
- the interactive `setup wiki` walkthrough drives a new operator from
  the vault root through disabled configuration, validation, the
  Hermes probes, the destination preflight, the Watchman guidance, and
  the initial dry reconciliation, then stops before enablement
  printing the exact production-gate command — it never accepts
  production approval implicitly, uses an explicitly named disabled
  configuration in place, and drafts a disabled copy when the named
  base carries an enabled route;
- the versioned agent-dispatch-operator skill ships with installation
  and compatibility documentation and changes neither Hermes core nor
  production state;
- the executable AC-701 through AC-706 gate suite passes with the
  synchronized Gate G7 evidence table in VALIDATION.md (the AC-702
  compatible-newer arm asserts in its own body; AC-703's behavioral
  submit-time block is cross-referenced to the E11-T2 suite), empty
  JSON collections serialize as [] or {} across the new surfaces, the
  root exit-code list covers the full registry, and the help
  completeness guard derives from the CLI registry so a future group
  cannot ship without its contract (round-one review remediations:
  setup forwards the operator's global options to every nested step,
  the enabled-base draft is re-runnable, the vault-root prompt is
  skipped when a base configuration exists, the Watchman step shows
  the real status output and prints the explicit install and test
  commands, and the setup wording in cli-spec and help matches the
  implemented walkthrough).

## 1.1.7 - 2026-08-28

E11-T3: the destination preflight surface lands (HER-015 through
HER-017, CLI-010, CLI-011, OPS-013):

- `hermes profiles` lists the public profiles with on-disk status;
  `route preflight` proves every destination executable before
  enablement — target, board, profile, skills, workspace, mutex,
  hints, notification sinks, and the persisted Watchman binding —
  blocking at exit 3 with bounded sorted alternatives and the concrete
  set-profile/set-skills remediation, and creating no task;
- the destination-qualified `route set-profile`/`set-skills` edit
  exactly one destination through the validated atomic mutation and
  pause the route revision; the qualifier may be omitted only with
  exactly one destination and an ambiguous route-only edit is a usage
  error naming the declared set;
- the probe record now carries the parsed enabled-skill inventory for
  its profile scope so preflight never re-parses table text;
- `status` reports the five OPS-013 drift classes per route —
  capability, profile, skill, watchman, and reconciliation — as an
  observational projection, and the mutation commands accept the
  `--config=<path>` equals form exactly like every shared parser
  (round-one review remediation: the dropped equals form previously
  mutated the default config instead of the named one); a passing
  skill table always records its inventory (an absent field now
  unambiguously means the shape failed, including the healthy
  zero-skill table).

## 1.1.6 - 2026-08-28

E11-T2: the capability probe and evidence cache land (HER-011 through
HER-014, HER-018, SEC-004, SEC-014, TST-012):

- `hermes probe` runs the five read-only shape probes — version and
  eligibility, assignees JSON, list JSON, the create-surface flag
  contract from the help text, and the profile-scoped skill table under
  the fixed rendering environment — and writes the owner-only
  capability-evidence cache; `hermes capabilities [--refresh]` prints
  the inspectable record through the standard envelope, re-probing
  stale evidence transparently;
- the cached record is keyed by executable path and digest, reported
  version, and probe contract; any change invalidates it, incomplete
  evidence refuses with the missing capability named, and a create
  surface that dropped `--mutex-key` (the observed 0.20.5 drift)
  downgrades the resource_mutex capability instead of failing the
  target;
- route activation binds the capability-evidence fingerprint beside
  the acknowledged revision (schema v11), and every submission
  re-proves the live executable identity against it before any side
  effect — an executable change blocks the submission with the probe
  remediation, never an ambiguous outcome;
- the skill-table probe records its profile scope in the evidence
  record: a cache probed unscoped or under another profile is stale for
  a profile-scoped read, activation probes with the destination's
  profile, and a lone `--mutex-key` loss downgrades the resource_mutex
  capability instead of failing the target;
- the frozen 0.19.1 interface fixture and a compatible newer Hermes
  traverse the same probe path with no source allowlist edit
  (TST-012), the stub Hermes carries every probe surface, and the
  schema/example pair for the v2 evidence record joins the SOT package.

## 1.1.5 - 2026-08-28

E11-T1: the v0.1.5 configuration cutover to `destinations[]` lands
(OPS-014, OPS-015, DAT-012, DAT-013, FAN-001, FAN-005, FAN-006,
FAN-011, FAN-012, CLI-015):

- every route declares its delivery under `destinations[]` — unique
  stable IDs, non-empty workstreams, per-destination execution hints,
  and the closed structural condition vocabulary — with
  `fanout_mode: all` the only v0.1.5 mode and declaration order
  non-semantic in revision, selection, and display;
- Hermes Kanban targets move to `hermes_targets` with the eligibility
  contract (`minimum_version` at or above 0.19.1, no maximum,
  `compatibility: capability_probe`); the operator-authored capability
  report and per-route required-capability lists retire, the frozen
  0.19.1 interface remains the interim truth source, submission gates on
  live minimum-version eligibility on every attempt, and enablement
  refuses a below-floor target while warning and deferring on an
  unreachable one, until the E11-T2 capability probe restores
  per-executable shape evidence;
- the legacy `routes.<id>.dispatch` shape and a `hermes-kanban` entry
  under `targets` are refused with the exact regeneration path
  (`agent-dispatch init` now; the interactive `setup wiki` flow arrives
  with E11-T4) — no load-time conversion exists, and an ID declared in
  both target maps is rejected as ambiguous;
- the route revision covers the sorted destination set, each
  destination revision, the declared notification policy and sink
  references, and the eligibility surface of every referenced target;
- SQLite migration v10 records the cutover: historic task and receipt
  evidence stays queryable, route enablement under the new contract
  refuses while unresolved legacy work from a different route revision
  remains, and the per-run pre-migration backup plus the documented
  restore rehearsal cover the reverse direction;
- the configuration mutation commands share one atomic
  destination-qualified write path: validated candidate, private
  temporary file, preserved mode, fsync, atomic rename.

## 1.1.4 - 2026-08-26

E10-T3: the source and reconciliation integrity gate G6 closes (SRC-009
through SRC-012, PTH-009, DUR-013 through DUR-015, OPS-010, OPS-012,
TST-010, TST-011):

- the executable gate suite drives AC-601 through AC-605 through the
  real CLI surface: the effective binding over a disposable nested
  real-Watchman tree, the out-of-root and exclusion evidence, the
  changed-topology removal proof, the fenced reconciliation with its
  retry, and the bounded hashing evidence, plus the fresh-database
  migration leg reporting the shipped schema range;
- the per-criterion evidence table lands in VALIDATION.md under Gate
  G6 with the member-task deterministic proofs named per criterion and
  the review residuals recorded for the epic audit;
- documentation truth synchronizes: OPS-012 names the configured hash
  bound (`limits.max_hash_file_bytes`), the cli-spec documents the
  binding-aware watchman install/status/remove/test surfaces, and the
  watchman-integration section numbering is canonical;
- the real-trigger test hygiene closes the suite's long-standing
  watchman instability: test-installed triggers no longer spawn nested
  full-suite runs (the trigger-shaped invocation of the test binary is
  a documented no-op) and every real-Watchman test drops its disposable
  watch, so repeated runs leave zero residual watches.

## 1.1.3 - 2026-08-26

E10-T2: the effective Watchman binding and route-relative exclusions
close the second v0.1.5 correctness defect (SRC-009 through SRC-012,
PTH-009, OPS-010):

- schema v9 persists the four-part managed binding per route — the
  configured resource root, the actual Watchman root (which may be an
  ancestor), the configured-root-relative path, and the stable trigger
  name — resolved by the one server resolver install and status share,
  read by remove from the persisted record plus the live watch list,
  and reported by watchman test from its logical root with no server
  contact; a reinstall after the watch moved also removes the stale
  managed trigger from the previous actual root;
- installation subtree-constrains the trigger through relative_root so
  an ancestral watch root cannot fire the managed command outside the
  configured subtree, and the dispatch-side binding validation accepts
  an ancestor environment only through the exact persisted record — a
  forged or drifted ancestor-plus-relative pair fails closed;
- exclusions gain exact-directory semantics (an exclusion naming a
  directory covers its whole subtree) alongside exact files, file
  globs, and recursive directories, all evaluated configured-root-
  relative before any read, hash, batch, or downstream record;
- status exposes the configured and actual roots, the relative root,
  the effective include/exclude patterns, the trigger identity, and the
  installed/missing/diverged/drifted states, where drifted means the
  persisted binding no longer matches the live watch topology;
- remove searches every watched root plus the stored actual root and
  succeeds only after re-listing proves the managed trigger absent
  everywhere;
- the dispatch-side ancestor validation accepts the frozen-evidence
  environment form (WATCHMAN_RELATIVE_ROOT as the subdirectory's
  absolute path) plus the persisted relative form, both only through
  the exact persisted binding;
- real disposable nested Watchman tree evidence covers the ancestor
  root, relative root, exclusion forms, drift, test, and complete
  removal including a stray managed trigger planted on a second watched
  root (TST-010).

## 1.1.2 - 2026-08-26

E10-T1: the resource observation fence and bounded reconciliation reads
close the first v0.1.5 correctness defect (ADR-0018, DUR-013 through
DUR-015, OPS-012):

- schema v8 gives every resource a monotonic path-fact observation
  revision that each durable path-fact mutation advances inside its own
  transaction;
- the full-snapshot replacement becomes a compare-and-swap fenced on the
  pre-enumeration revision, with replacement and advancement one
  transaction: a concurrent ingestion fact inside the enumeration window
  refuses as a typed concurrent-change outcome, the newer facts survive
  untouched, and exactly one due reconciliation generation remains;
- reconciliation hashing reads at most `max_hash_file_bytes + 1` bytes,
  checks file stability (size and mtime) across the read, retries an
  unstable file once, and reports a stable over-bound file as explicit
  quarantine evidence and a twice-unstable file as explicit
  reconciliation evidence, with both digests left unknown;
- SQLite (revision advance, fenced replacement, v8 backfill and the
  interrupted-upgrade window), race (deterministic fence trap and a
  growing-file interleaving), and status regression tests pin the
  behavior; the reconcile envelope gains `concurrent_change`,
  `quarantined_over_bound`, and `unstable_after_retry`.

## 1.1.1 - 2026-08-25

D-026 migrates the documentation package to explicit canonical role owners
without changing product behavior or roadmap lifecycle:

- specifications, architecture, ADRs, implementation tips, roadmap, deferred feedback, and TODO each have one top-level owner and README index;
- contracts, operations, and source provenance remain distinct top-level supporting collections with their owning role and precedence recorded in `docs/README.md`;
- the v0.1.5 feature map connects the four approved capability groups to G6-G9 and E10-E13 while keeping detailed requirements, design, and lifecycle authority separate;
- every tracked live path reference, traceability input/output, validation instruction, and manifest entry moves atomically to the new tree;
- the traceability drift guard compares the generated matrix with its pre-run content, preserving the stale-file failure without depending on staging or Git-index state;
- all established E/T identities, 14 epics, 75 tasks, statuses, completed history, the v0.1.4 shipped boundary, and the v0.1.5 planned boundary remain unchanged.

## 1.1.0 - 2026-08-25

D-025 approves the v0.1.5 planned baseline without implementing or releasing it:

- the accepted Hermes operations request is retained under `docs/source/`, with the clean config-v1 cutover, no-Hermes-change boundary, and partial/blocked work policy recorded as owner clarifications;
- ADR-0016 through ADR-0019 freeze aggregate destination lanes, capability-probed Hermes compatibility, resource observation fencing, and the durable notification outbox;
- required-spec and acceptance criteria gain the Watch binding, reconciliation, Hermes preflight, fan-out, work receipt, setup/help, and notification contracts plus planned gates G6-G9;
- roadmap epics E10-E13 add 15 Planned tasks, bringing the package to 14 epics and 75 tasks (60 Completed, E10-T1 next); v0.1.4 remains the shipped release;
- executable schemas, examples, packaged skills, release notes, code, artifacts, and tags remain at v0.1.4 until their owning roadmap tasks deliver tested changes.

## 1.0.47 - 2026-08-25

E9-T9 and the D-024 closeout: documentation truth and the v0.1.4 release:

- the status surfaces resynchronize (README SOT and narrative, VALIDATION post-D-024 record, the roadmap's re-closed summary and current state, and the release checklist rewritten to the 60-task macOS-only toolchain-enforced basis) closing D-023 F5;
- the member-task residual observations reconcile in-tree: the unavailable-branch weak-guarantee coverage leg, the RecordedVersionSupported boundary unit tests, the authProjection nil-equivalence pin, the gate-comment phrasing, the grammar-test colon, and the schema-test comment wording (the twin validation ladders are recorded in D-024 as an architectural observation);
- RELEASE-NOTES-v0.1.4 discloses the one-time re-acknowledgement under the widened revision projection, the enforced toolchain pin, the macOS-only artifact set, and the known Hermes-set posture;
- D-024 re-closes epic E9 9/9 with every D-023 finding dispositioned and v0.1.4 tagged at the final tree after the byte-identical double build.
## 1.0.46 - 2026-08-25

E9-T8: the macOS-only support policy lands (D-023 finding F6):

- the release output restricts to darwin/arm64 (one artifact plus SHA256SUMS); the linux/amd64 build leg, the systemd example assets, and the systemd leg of schedule-check are gone, and the uninstall script loses its systemd loop;
- SCP-008 and AC-505 carry explicit D-023 supersession annotations with the D-020 closure records standing as history; the charter, testing strategy, implementation guide, configuration-spec, observability and Watchman guidance, and the installation guide state the macOS-only policy; the maintenance skill declares platforms macos and the root README names the only supported platform;
- the schedule-example tests pin the reduced launchd-only set; historical release notes keep their Linux records with D-023 as the supersession authority.
## 1.0.45 - 2026-08-25

E9-T7: header grammar and pinned-toolchain enforcement:

- webhook header names validate against the complete RFC 9110 tchar allowlist at sink construction and through the configuration schema pattern on both `header_name` and `idempotency_header` (both copies byte-identical), with configuration-spec §5 stating the grammar and the regression tests pinning the identical separator class at both levels;
- the new `go-version-check` target enforces the exact go.mod-pinned toolchain (1.26.6) before any build step of `make verify` and `make release` — the pin is read from the go directive, compared against the compiling toolchain, unit-tested, and prerequisite-ordered so `make -j` cannot bypass it; with GOTOOLCHAIN=auto the pinned toolchain is selected and the check passes;
- VALIDATION.md's SCP-005 row and the release checklist's toolchain line state the enforced mechanism.
## 1.0.44 - 2026-08-25

E9-T6: submission-gate revision and capability-report integrity:

- the computed route revision covers the webhook delivery-evidence surface (auth type, secret reference, auth header name, idempotency header, lookup timeout, capability-report path) and, by explicit disposition, the reconciliation block in and the retention block out (pruning bounds never change submission behavior); configuration-spec §13 states the complete rule including the E9-T3 transport fields;
- `route enable` requires the capability report in every target-liveness state: the os.Stat guard is gone, a missing, unreadable, or unsupported-version report refuses at exit 3 while an unreachable executable stays a warning, and a report recording a Hermes outside the runtime-verified set refuses without a live target (Report.RecordedVersionSupported — the supported set is build-time evidence); only freshness against the installed binary rides the probe;
- the unconditional durable/idempotency refusal is one shared closure across the probe branches; the cli-spec `route enable` contract states the precise liveness semantics;
- the real-Hermes environment tests skip under TST-007 when the installed Hermes is outside the verified set (the host moved to 0.20.5 against the verified 0.19.1; widening is a fresh E0-T4 probe, not a test override).
## 1.0.43 - 2026-08-25

D-023 registration (E9 reopened for the external compliance review):

- the 2026-08-25 external MVP compliance review of the shipped v0.1.3 tree (one High, five Medium) is accepted in full and epic E9 reopens under the formal-audit rule with four remediation tasks: the route revision's webhook-transport blind spot and the `route enable` capability-report gap (E9-T6), the RFC 9110 header grammar and the unenforced Go 1.26.6 toolchain pin (E9-T7), the Linux surface under the new darwin/arm64-only support policy (E9-T8), and the status-surface contradictions with the v0.1.4 release (E9-T9);
- D-023 records the macOS-only support policy that supersedes SCP-008's and AC-505's Linux verification clauses; historical release notes keep their records unchanged with the decision as the supersession authority;
- the roadmap re-counts to 10 epics and 60 tasks; E9-T1 through E9-T5 keep their Completed status and D-022 evidence; the epic summary's stale Planned row is corrected to In Progress; v0.1.4 supersedes v0.1.3 at the re-closure.
## 1.0.42 - 2026-08-24

E9 closeout and v0.1.3:

- the E9-T1 audit's eleven deferred findings reconcile in-tree: the intent emission carries the request document as the schema's object (show and list), the schema-excluded operator members leave the wire, an in-flight attempt reports the open outcome, nullable members render null, the derived dead-letter reason clamps to the enum, migration v7's backfill is pinned over a rewound v6 shape, and the record emissions validate against the full published schemas from real runs;
- the whole-epic validation (148bf57..HEAD, including the E8 correction delta) converges through three remediation rounds plus a clean confirmation: the retention prune no longer wedges on a terminal dispatch's begun receipt (in-flight anchors stay protected through non-terminality and the active slot), the typed-map denylist case-folds, the terminal-lineage and denylist guards are single shared predicates, and the release-checklist retention wording states the exact semantics;
- D-022 closes the epic (56/56 tasks, every D-021 inventory item dispositioned) and records the D-011 scheduled-submit supersession plus the E9-T2 provenance correction;
- v0.1.3 is tagged at the final tree, built twice byte-identically, with the release notes disclosing the hardening and the TST-008 gate remaining disabled.

## 1.0.41 - 2026-08-24

E9-T5: documentation truth and the dependency advisory:

- observability §3 lists the events actually emitted (thirteen, including dispatch.mutex_suppressed and the work lifecycle trio) and marks the rest of the vocabulary reserved; §5 documents status --output json's real payload and doctor's always-JSON findings envelope; §7 names the offline construction gate;
- the roadmap E4-T5 evidence section lands and E5-T4's reason vocabulary is corrected to the delivered nine values;
- configuration-spec states the 1.0-10.0 multiplier range, notes the inert git.mode and unsafe_path_action keys with their digest exclusion, and documents the .md/.markdown scope (SCP-003); SCP-001 carries the certification-posture note;
- the error-model exit-13 row states the code truth and the reserved code names are tabulated with their activation conditions (L-21);
- golang.org/x/text is bumped v0.14.0 -> v0.41.0 (GO-2026-5970) with the aligned indirects; full suite green.

## 1.0.40 - 2026-08-24

E9-T4: test-coverage hardening:

- the member-task coverage deferrals land as executing tests: path-fact load failure degrades the attribution conservatively with the degradation recorded (T1-F002), a pending reconciliation on an IDLE route is delivered as one follow-up through the next completion (T1-F005), the service-level failure-budget mirror resolves an exhausted budget through UNCERTAIN end to end (T1-F006), and the over-budget UNCERTAIN route resolves through operator reconciliation to IDLE (T1-F007);
- the scheduled recipes carry --submit (T2-F001: the runbook's automatic-recovery leg); the two-key gate is the safety boundary — before the production acknowledgement the route fails closed at exit 14, and after it a configuration-disabled route persists decisions and recovers without submitting (cli-spec §9, the installation and observability passages, and the VALIDATION checklist all state the posture; the YAML-key leg is pinned by test);
- the migration-lock test pins steal prevention rather than the mtime proxy (T2-F002), the three submit surfaces share one runtime constructor whose lease-TTL derivation is asserted (T2-F003/F004), and ungated recovery on a disabled route is pinned (T2-F005);
- the confirmation observations land (the misnamed classify-error test renamed to what it pins, the real error arm — uncompilable scope patterns — fails the work commands closed; doctor's unreadable-root test self-skips under root);
- the three self-healing goldens now fail on absence (L-6) and the skill-renderer cross-check pins the rendered instruction against the assigned skills and the work-command flag surface (L-23);
- the load-sensitive 1s stub deadlines are raised to 10s and the suite passes twice consecutively under -count=1 with coverage (M-25 residual).

## 1.0.39 - 2026-08-24

E9-T3: security, observability, and revision hygiene:

- log sanitization covers string-map values (the M-20 remainder), so a path inside any map value rides the configured path policy;
- a secret file owned by another uid is refused alongside the mode-bit check (L-15);
- work.begun, work.completed, and work.receipt_invalid emit at the work command boundaries with trace/dispatch/run correlation, and doctor findings carry trace_id (L-17);
- the route revision covers the transport fields — executable, submit_timeout, environment_allowlist, and the manifest byte bound — so swapping the target binary or its bounds pauses the acknowledged route (T3-F006);
- a suppressed --mutex-key warns as dispatch.mutex_suppressed at submission instead of dropping silently (T3-F007);
- every policy decision records config.PolicyRevision: an independent digest of the policy-evaluation surface, on arrival, reprocess, reconcile, follow-up, and quarantine-release decisions (L-18).

## 1.0.38 - 2026-08-24

E9-T2: reconciliation and operator-surface hardening:

- every symlink - escaping or in-vault - is skipped from the reconciliation fact set with the skipped list surfaced as envelope warnings, never projected into task manifests as regular files (M-24); file-level walk errors skip exactly the file (L-8);
- maintenance prune/vacuum refuse under the Watchman trigger environment at exit 2 (M-21/CLI-007); config show accepts --output json (L-11); unknown-dispatch work commands audit (L-7);
- the prune guards share one predicate across plan and execution, route stale consults a store-level eligibility rule over a tri-state age, and the flaky stub deadline (M-25 residual) is raised;
- one decision-log provenance note defers to the E9-T5 documentation pass.

## 1.0.37 - 2026-08-24

E9-T1: record schema truth and storage hardening:

- dispatches show and list emit the schema-required members of the intent, attempt, and receipt records (schema_version, decision and route linkage, resource, fingerprint, request document) with a real-emission lockstep test, and a derived dead-letter-record view appears for dead-lettered dispatches;
- migration v7 adds route_revision to the four record tables (backfilled by join, written from the creating intent or decision at insert);
- the connection pragmas ride the DSN, one verified backup covers a whole migration run, and the prune cutoffs and watchman envelope serialize snake_case;
- eleven round-2 findings (request-member object shape, strict-schema residuals, covering tests) defer to the E9 validation audit.

## 1.0.36 - 2026-08-24

D-021: the hardening inventory is accepted and epic E9 is registered:

- the four Deferred mediums, the resolution remainders, the seventeen Deferred lows, the member-task test-coverage deferrals, and the documentation sub-wording items become E9's five tasks (10 epics, 56 tasks) closing with the v0.1.3 patch release;
- the M-8 recrawl exception stands, the D-018 Accepted dispositions (L-13, L-14) are not reopened, and the E8 round-2 T6-F003 premise is dispositioned as invalid (the root-level review file is untracked by design);
- E9's validation review covers the diff from 148bf57, so the E8 correction delta rides into reviewed evidence with it.

## 1.0.35 - 2026-08-24

E8 correction pass (operator audit of the D-020 finding index against the tree):

- six rows recorded as Fixed but descoped or omitted are now true: M-15 (empty stored request versions fail closed; receipt payload_version is checked on read), M-17 (the schema DAT-007 claim corrected in place to the join-reachable design), L-4 (the real-NUL malformed fixture), L-5 (the AC-106 loop widened with a NUL name; the AC-103 repeated-save test pins the same final digest), L-24 (the follow-up terminology entry), M-18's pattern half (route pattern sets compile at validate), and M-20's remaining halves (log messages are sanitized; config show masks endpoint query strings);
- M-16 (record schemas vs CLI emissions) is corrected to Deferred — a wire-shape change out of the v0.1.2 scope, recorded in the epic closeout;
- v0.1.2 is re-tagged at the corrected tree and rebuilt twice byte-identically: the correction pass changed source after the first tag, which would have repeated the review's H-5 defect (binaries not built from the released tree).

## 1.0.34 - 2026-08-23

E8 epic validation: the four-round whole-epic review converged (ci pass, zero medium-or-above findings; two low test-quality observations recorded for the next cycle), the audit remediations landed (the quarantine-release revision seam, the fail-closed stale precondition, the honest runbook exits, the route tree and example alignments), and the deferred findings are reconciled - E8 closes 6/6 with v0.1.2 tagged.

## 1.0.33 - 2026-08-23

E8-T6: documentation truth restored and v0.1.2 released:

- the five E8-T3 deferred documentation findings fixed (installation section 3 places the capability report before the gates; cli-spec section 3 documents the enable probe; configuration-spec section 12 states the default/probe split; the sink-contract example and the dispatch-plan example carry the honest capability set);
- the section-4 truth items: AC-107's refuted given-clause, the E7-T1 file count, the E2-T2 Linux wording, the E7 matrix rows the review refuted (CON-003, CLI-004, DAT-009) superseded rather than rewritten, the Markdown count, the README narrative through v0.1.2, the repository-layout deviations, and the domain-model/overview prose;
- SCP-008/AC-505 closed across the charter, acceptance criteria, VALIDATION, README, and release notes, with the two permission-expectation tests self-skipping under root;
- the refreshed MUST-closure matrix records the v0.1.2 disposition of every FAIL/PARTIAL requirement; `make release VERSION=v0.1.2` is byte-reproducible with the tag; the AC-506 test reads one version source and validates the artifacts; RELEASE-NOTES-v0.1.2 ships with the TST-008 disclosure.

## 1.0.32 - 2026-08-23

E8-T5: input containment and configuration validation are closed:

- H-8: every recorded path resolves containment - pure deletes included - and escaping anomalies wrap as source_unsafe_path/30, so a crafted escaping delete is never recorded as dispatchable;
- M-7: the plan/dry-run envelope states its no-database fact gap; M-8: recrawl detection resolved as a recorded exception with the product-path reasoning; M-9: the managed trigger pins --config and --output json;
- M-18 (semantic half): SemanticValidate covers resource-root overlap, absolute state_dir and roots, map-key grammar, and the max_hash_file_bytes floor; M-13: quarantine release recomputes the current revision; M-14: the protected hold stays visible under overflow precedence; M-11: the reconcile fingerprint is mount-point independent;
- one Mulgae round converged clean (ci pass, zero findings).

## 1.0.31 - 2026-08-23

E8-T4: unresolved lineage is preserved and doctor is trustworthy:

- H-3/AC-503: prune drops accepted from the resolved set and guards every lineage delete with the active-slot predicate (mirrored in the dry-run plan) - an active accepted dispatch keeps its attempts, receipts, and work receipts;
- H-4/AC-502: watchman_unavailable and target_gate_failed are error severities, the offline capability gate runs on readable reports, PATH-named executables resolve, resource roots get a real open/readdir access probe, and a broken configuration never fabricates an unexamined Watchman finding;
- M-19: state.db is created 0600; M-20: the credential redactor covers basic/token headers, client_secret/apikey/password/key query and fragment forms, and bare JWTs; M-12: reconcile on a non-enabled route exits 14 in every state; M-23: route stale enforces active_stale_after; M-22: the startup reconcile reason with runbook procedure; L-9: prune refuses --dry-run with --yes;
- five round-2 low/info findings deferred to epic hardening; the round-2 high (a flag inversion introduced by the round-1 remediation) was fixed in-tree with regression tests - the deviation is recorded for the epic validation audit.

## 1.0.30 - 2026-08-23

E8-T3: the route revision is behavior-sensitive and the production gate is enforced:

- H-2/POL-007: the revision projection covers the resource root, file scope, git mode, the global limits, and the target type/board/endpoint (configuration-spec section 13 amended); the acknowledged revision is re-checked on every submit path, so a behavior-sensitive change pauses the route until re-acknowledged (exit 14);
- H-7: route enable runs the live version-gated probe — unreadable or stale reports and unsupported versions refuse at exit 3, durable_acceptance and submit_idempotency_key are unconditional, and the capability report records lookup_by_idempotency_key false (the honest read-only semantics; the D-018 record now matches the file);
- M-6: resource_mutex gates --mutex-key in the renderer; M-18: config validate runs the probe-free section 12 target checks by default (a missing report warns, an invalid one fails);
- five round-2 documentation findings deferred to the E8-T6 pass via epic hardening.

## 1.0.29 - 2026-08-23

E8-T2: recovery is wired into every submit path and the operator exits are repaired:

- H-6: the trigger path and the scheduled `reconcile --submit` sweep expired submitting leases at their head (before the arrival is evaluated and before any submission), so a process that died mid-submit heals on the next trigger without a manual drain;
- M-1: the attempt lease TTL derives from the configured `submit_timeout` plus a 30 s margin (documented in configuration-spec section 5, superseding the D-018 one-minute record); M-2/L-1: `dispatches retry` resets the attempt budget in one audited transaction — the operator exit for a budget-exhausted wait; M-3: a definite rejection dead-letters through the declared edge so a refused dispatch cannot hold the route slot;
- H-9: operator refusals map to exit 14 with registered codes and storage failures to 20; M-4: the migration lock refreshes its mtime under long units; M-5: fork/exec failures classify definite not-submitted; L-2: the test-only store exports are deleted; L-22: the usage string and cli-spec tree name `discard`;
- runbook section 11 documents the four delivery-failure operator exits; five round-2 low findings deferred to epic hardening.

## 1.0.28 - 2026-08-23

E8-T1: the follow-up loop state machine is closed:

- B-1: the FOLLOWUP_READY -> ACTIVE_DIRTY edge (reason `followup_accepted_dirty`) lets a burst arriving between completion and follow-up submission activate with its dirty generation; the review's CLI reproduction now completes through the product path;
- H-1: follow-up IDs are UUIDv7 (no cumulative suffix growth), migration v6 keys the generation window on a batch-sequence watermark instead of second-truncated timestamps, the receipt matcher intersects the route's effective scope and the durable path facts (immaterial paths never block suppression), and a consecutive-follow-up budget resolves over-budget chains through UNCERTAIN;
- M-10: follow-up manifests carry the unresolved paths with the fingerprint recomputed; the work commands map route-guard rejections to exit 14;
- round-1 Mulgae remediations folded in (the IDLE empty-slot merge wedge, the budget single decision point, the fail-path fence, the single scope encoding, the path-fact degradation record); six round-2 test-coverage findings deferred to epic hardening.

## 1.0.27 - 2026-08-23

D-020: the second MVP compliance review is accepted; remediation epic E8 is registered:

- the 2026-08-23 review of v0.1.1 (commit `f00ed30`) found one Blocker in the follow-up product loop, two FAIL MUST requirements (CON-003, POL-007), three FAIL acceptance criteria (AC-502, AC-503, AC-506), and 22 PARTIAL clauses; it is accepted in full and stays outside the package per the D-019 precedent;
- the roadmap gains six E8 remediation tasks (9 epics, 51 tasks) covering the Blocker, the ten High findings, the mapped Medium findings, and the documentation-truth cluster, with every remaining finding dispositioned Deferred/Accepted in the D-020 index;
- the SCP-008/AC-505 Linux-verification exception is closable on the review's linux/arm64 non-root `make verify` evidence; the closure executes with E8-T6 and the v0.1.2 release;
- the TST-008 automatic-write gate stays disabled until E8-T1 through E8-T3 are Completed.

## 1.0.26 - 2026-08-23

D-019: the compliance review report is retired from the SOT package:

- `docs/reports/mvp-compliance-review-2026-08-22.md` is removed and the manifest regenerates without it;
- the review's severity-classified finding index (B/H/M, 40 entries with owners and dispositions) is recorded in the decision log (D-019), and the Low/Info inventory remains in D-018;
- the living documents now cite D-017/D-018 where they cited the report path; the package statistics drop to 62 Markdown files.

## 1.0.25 - 2026-08-23

E7-T12: the MUST-closure re-verification and the v0.1.1 release:

- `make verify` including the race suite passes on darwin/arm64 and the G1-G5 gate suites re-ran green on the real Hermes and Watchman;
- the MUST-closure matrix is recorded in VALIDATION (13 of the 14 GAP requirements PASS; SCP-008 carries the explicit D-017 exception);
- `make release VERSION=v0.1.1` is byte-reproducible across two consecutive builds (darwin/arm64 and linux-amd64 artifacts with SHA256SUMS);
- the release notes disclose the Linux verification exception; E7 is complete (12/12 tasks).

## 1.0.24 - 2026-08-23

E7-T11: the consolidated low and informational finding dispositions (D-018):

- every Low/Info finding from the compliance review's section 5 maps to a fix (with the owning E7 task named) or a documented reduced guarantee with its rationale, in one decision-log record the epic audit reconciles;
- round-1 review remediations: the three overstated FIXED claims were made true in the tree (the validated SKILL.md header, the AC-106 record assertion, the documented lease TTL), and the inventory gained the ten missing T2-T8 items.

## 1.0.23 - 2026-08-23

E7-T10: licensing, layout, and documentation consistency (M-27, M-31):

- the repository carries an MIT LICENSE and a recorded dependency-license review (all 39 modules in the build graph: MIT, BSD, Apache, and MPL-2.0 tool-chain only, all compatible);
- the release checklist is retitled for v0.1.1 and operated: all 50 items checked with honest narrowing notes;
- repository-layout documents the six named layout deviations; task-execution-rules §5 records the owner/timestamp deviation; the CHANGELOG 1.0.7 entry carries the pending_reconcile known-deferred note; the six E6-T3 hardening residuals carry individual dispositions; CONTRIBUTING and README state the full make verify composition;
- the two stale docs-schemas skips became fatal broken-checkout guards, the stale E4-T4 skip was removed, and the duplicate E6-T4 heading was unified.

## 1.0.22 - 2026-08-23

E7-T9: the operations and security medium batch (SCP-004, FBK-002/003, HER-005, OPS-003, SEC-006, SEC-007):

- the resource file_scope is enforced above the pattern engine (under markdown, a non-Markdown path an include pattern admitted drops before hashing);
- prune never deletes a begun (in-flight) work receipt; the disabled-route reconciliation refusal classifies as a state conflict;
- `route enable` validates the required capabilities against the configured report when readable; the doctor target probe constructs with the real manifest bound;
- an unclassifiable filename is isolated as unverifiable instead of aborting the reconciliation; a permissive `file:` secret reference fails closed naming the mode; credential fragments (bearer token bodies; token, access_token, api_key, and secret query parameters) are masked inside logged values;
- the stub-probe test fixtures' fixed timeouts rose to 30 seconds (load-flake posture; the configured probe limits are unchanged) and an explicitly chosen `init --state-dir` persists into the written configuration.

## 1.0.21 - 2026-08-23

E7-T8: payload versioning enforced and the Hermes rendering completed (DAT-009, HER-006, SRC-002):

- acceptance receipts always carry the submitted contract's payload version (never null), the snapshot and lineage reads fail closed on any stored request version this build does not speak, and the webhook sink refuses a foreign task-request contract before transport;
- the Kanban renderer carries the acceptance criteria block and the HER-006 manifest-existence sentence, pinned by the regenerated golden;
- persisted observations use schema-conformant snake_case flags and store their verbatim source position (migration v5, schema range 1-5);
- round-1 remediations: the observation converter actually persists the position (the column was written NULL), the webhook gate refuses every contract except this build's exact version, and the unpublished has_relative key left the persisted flags (rerun and follow-up rebuilds construct fresh requests under the current contract, so no stale restamping path exists).

## 1.0.20 - 2026-08-23

E7-T7: storage durability and operator exits (OPS-008, OPS-009, DUR-004, DUR-009):

- the migration pass serializes through an exclusive lock file (bounded wait, stale-holder theft) and the WAL switch retries under concurrent first-open congestion, so concurrent first invocations no longer fail spuriously;
- a WAL-switch busy that outlives the retry window surfaces as the retryable `sqlite_busy` (exit 10) instead of a fatal open failure;
- `route stale --reason` moves a stale ACTIVE route to UNCERTAIN through the declared execution-evidence-stale edge (the audited operator exit; reconciliation then resolves it);
- `dispatches discard --reason` closes a dead-lettered dispatch as superseded through the declared edge, releasing the route slot; the closed form is retention-resolvable while an open dead letter stays retained;
- round-1 remediations: the migration-lock steal is an atomic rename with an owned pid-checked release and a wait window covering the staleness bound, a lock wait past the window surfaces as retryable busy, and the route stale operator reason is audited.

## 1.0.19 - 2026-08-23

E7-T6: write gates, audit rows, and decision records (TST-008, DUR-011, POL-006, OPS-004, PTH-008):

- the automatic-write gate is closed: drain requires the store activation to be enabled, and the YAML `routes.<id>.enabled` key participates in the two-key gate on both ends (`route enable` refuses while the key is off; drain and dispatch refuse automatic submission while it is off);
- every route transition appends its audit row inside the transition transaction (the route timeline is reconstructable from state_transitions alone) and intent creation records its arrival row;
- merged bursts persist `merge_pending` on the durable decision;
- `dispatches reprocess` evaluates the retained batch against the active pattern engine and records the evaluated disposition, classification, and reasons.

## 1.0.18 - 2026-08-23

E7-T5: the CLI inspection contract completed (CLI-004, CLI-008, OPS-002):

- `config show` prints the normalized, redacted configuration; no command in the tree answers `command_not_implemented`;
- `dispatches show` returns the complete causal lineage: decision with reason codes, retained batch, source observations, and work receipts beside the intent, attempts, receipts, and transitions;
- `dispatches list` gains the age, external-reference, and causal-ID filters with offset pagination;
- the parsed `--trace-id` reaches every success envelope; a dead-lettered retry without `--reason` is a usage defect; the exit-4 `*_not_found` codes on the dispatches, receipts, and quarantine surfaces emit `input_rejected` in the implementation and the table (`config_route_not_found` stays `configuration`/exit 3), restoring the category-to-exit 1:1 rule (the work-command `dispatch_not_found` sites are deferred with the round-2 residuals);
- round-1 remediations: the lineage join surfaces errors instead of truncating, the causal-prefix LIKE wildcards are escaped, and `config show` prints the computed route revisions the CLI contract promises.

## 1.0.17 - 2026-08-23

E7-T4: gate-evidence integrity and platform honesty (TST-004, TST-009, AC-207, AC-203):

- `TestG2AC207` now interrupts before the first unit, between every pair of migration units, and inside the final unit (the new in-unit crashbin mode carries an asserted hard-death marker); every assertion executes on every platform, replacing the pre-ledger leg that skipped unconditionally;
- the contract lockstep tests read the real docs/schemas directory and fail loudly on missing schemas, so schema/enum drift is caught;
- the after-remote-acceptance crash boundary has a real process-death variant: the crashbin leases, genuinely submits through the stub hermes, and dies before the receipt; the drain heals the lease and the dedup-safe retry resubmits the same idempotency key back to the original task;
- the darwin-only keychain tests skip with a recorded reason off-platform instead of failing;
- the VALIDATION G2 evidence rows state the delivered evidence.

## 1.0.16 - 2026-08-23

E7-T3: submit-path revalidation and durable path facts (POL-008, SEC-010, PTH-006, PTH-007):

- every submit path revalidates the stored plan inside `SubmitOnce` immediately before the lease commits: a stale route revision or moved target identity supersedes the intent through the declared `route_revision_invalidated` edge and rebuilds it under the active configuration, with the superseding decision recording the reason and the tautological plan-time self-comparison removed;
- rerun derives its route revision from the active configuration instead of the stored plan (target-identity resolution on rerun is deferred with the round-2 residuals);
- the ingestion transaction maintains the durable `path_facts` snapshot, the durable dispatch path plans against it, and `unchanged_content`/`create_delete_never_existed` suppressions are visible in the plan and decision reason codes (AC-102);
- the regression suite `internal/cli/e7t3_test.go` proves the revision and target halves and the durable unchanged-modify suppression end to end;
- round-1 review remediations: the direct dispatch submit runtime installs the staleness check, rerun resolves the active revision and target, the rebuild recursion is depth-bounded with unresolvable rebuilds refused, the drain warns instead of aborting on a staleness it cannot resolve, the superseding decision inherits the original policy revision, and two stale documentation claims (the E2-T4 plan-time revalidation, the path-facts section cite) were corrected.

## 1.0.15 - 2026-08-23

E7-T2: the compliance review's three Blockers closed in the product path (DUR-010, CON-001, CON-003, FBK-005):

- B-1: `dispatches drain` invokes the expired-submitting recovery sweep before unknown reconciliation and reports the recovered leases (`recovered` in the envelope); the during-submit crash boundary now has real process-death coverage (`TestG2DuringSubmitProcessDeathRecovers` through the crashbin lease hook), `TestG2AC203` was rewritten with a real sink baseline, and the doctor remediation texts name the actual exits;
- B-2: rerun requires ready or dead-lettered work (in-flight, retry_wait, and terminal-authoritative originals are refused with guidance), the store supersedes the original through its declared edge in the rerun transaction, and both the submit and drain paths enforce the route's active slot (one authoritative task per route; uncertain and quarantined routes submit nothing);
- B-3: an accepted follow-up is promoted to the route's active task at acceptance (`Runtime.promoteFollowup`), the scheduled `reconcile --submit` path drains due follow-up work behind the enabled-route gate, and every G4/E5 multi-generation scenario now drives the full product path (drain, acceptance-time activation, work begin/complete) with the store-direct activation bypass removed;
- round-1 review remediations: the promotion crash window heals on the next drain (accepted follow-ups left in FOLLOWUP_READY are promoted), the route-slot predicate is enforced inside the lease transaction (`ErrRouteSlotHeld`), the expired-lease sweep is route-scoped, the crashbin `--die` parsing bug is fixed with an asserted hard-death marker, the cli-spec drain/rerun/reconcile contracts state the new behavior, and the disabled-route `--submit` gate has its proof;
- gates G2 and G4 (plus G1/G3/G5 with the real Hermes and Watchman) re-run green on darwin/arm64 with `make verify`.

## 1.0.14 - 2026-08-23

E7-T1: documentation truth restored after the 2026-08-22 MVP compliance review:

- every surviving false verification claim is corrected at its source with one accurate statement: no successful `make verify` run on a supported Linux host is recorded, hosted CI is not used, and the review's own diagnostic linux/arm64 container runs failed (exit 2). Corrected locations: the roadmap's CI-based acceptance and evidence wording (E1-T1, E6-T2, E6-T3, E6-T4, including the "all MUST requirements pass" acceptance bullet, now marked superseded by the open 14 MUST gaps under D-017), the AC-505 criterion and the charter's success definition (a supported Linux host, with the v0.1.1 exception recorded), the record-contract and examples README validation sentences, the implementation-guide CGO policy row, and the 1.0.10/1.0.11 entries below (inline markers);
- `docs/README.md` and `docs/VALIDATION.md` align on SOT 1.0.14;
- `docs/VALIDATION.md` is truthful about its evidence: the header and package statistics are current (12 schemas, 8 epics, 45 tasks, the archived review report included), the em-dash check is scoped to what is true (`docs/specs/`), the G2 crash-boundary scope states which boundaries are in-process only, AC-203 and AC-207 rows carry the review's corrections (hollow fake-sink assertion; always-skipping migration test) with their E7-T2/E7-T4 restoration owners, the G4 header records the store-direct follow-up-activation bypass, the G5 section states AC-505's status and the failed diagnostic runs, and the nonexistent `TestG2MigrationInterruptedUpgrade` citation is removed;
- the roadmap's status artifacts agree (task index, current-state counts, and epic status carry E7-T1 In Progress);
- no source code changed; `make verify` passes with the corrected package.

## 1.0.13 - 2026-08-23

D-017: the 2026-08-22 MVP compliance review is accepted in full and remediation epic E7 is registered:

- the review report (`docs/reports/mvp-compliance-review-2026-08-22.md`) is admitted into the manifest-verified docs package;
- the roadmap gains E7 with twelve Planned tasks (8 epics, 45 tasks total) owning the Blocker, High, Medium, and Low/Info remediation and the v0.1.1 patch release; the traceability matrix is regenerated for the new membership;
- SCP-008 is recorded as an explicit exception for v0.1.1: verification runs on macOS only, the linux-amd64 artifact ships unverified, and the surviving "Linux CI leg" claims are queued for correction in E7-T1;
- affected Completed tasks are not reopened (task-execution-rules §4); E7 is the formal audit remediation cycle and appends `Audit remediation (compliance review 2026-08-22):` evidence notes to the affected tasks instead.

## 1.0.12 - 2026-08-22

E6 epic validation audit and closeout:

- every member-task hardening deferral was revalidated against its native Mulgae authority and the valid findings were remediated: the secretresolver fd cache serializes wrapper creation and reads seek-state-free through ReadAt with the deadline-bounded pipe one-shot fallback (the race-loser finalizer can no longer close the shared descriptor; concurrent-first-resolution and post-GC tests pin it), the webhook construction gate refuses idempotency-header collisions with the authentication and transport headers (four-case refusal test), the prune dry-run plan mirrors the executed cascade through one shared CTE chain (freed-chain dry-run test), doctor opens the store unmigrated so migration_pending is observable (downgrade regression test), and the documented global --state-dir and --timeout options are implemented with fail-closed validation, per-invocation reset, and store-context bounding (behavior tests);
- three whole-epic review rounds converged to r_01a027d2 returning reports_only with zero structured findings; the residual low/info items are the documented v0.1.0 hardening posture (release notes) or were fixed in place (the lock-name comment, the unreachable init branch, the timeout claim wording);
- the roadmap records the E6 epic Completed with the closeout narrative; the v0.1 sequence is complete (33/33 tasks, gates G0-G5 closed).

## 1.0.11 - 2026-08-22

E6-T4: v0.1.0 verification and release:

- the executable G5 acceptance suite closes the gate: AC-501 (webhook auth without persistence, transport-vs-durable distinction, no Kanban fallback), AC-502 (doctor's stable actionable findings), AC-503 (prune removes resolved expired data while unresolved lineage and the audit survive), AC-504 (the clean-host macOS install→validate→dry-run dispatch→gate-acknowledged enable→scheduled reconciliation→doctor flow without manual database edits), AC-505 (the Linux CI leg of make verify; corrected in 1.0.14: no successful Linux run is recorded, and the review's diagnostic arm64 container runs failed), and AC-506 (the release-way build with its version envelope and the full artifact set);
- the upgrade-and-backup rehearsal is executable: built-in backup with verification, doctor, full integrity, one reconciliation, and a standalone restore that carries the lineage;
- docs/VALIDATION.md gains the Gate G5 evidence table (G0–G5 now closed) and docs/RELEASE-NOTES-v0.1.0.md ships as the release notes artifact;
- the requirement traceability matrix regenerates with every requirement resolved to its owning and verifying tasks (33 tasks, 15 groups);
- compatibility is frozen and reported: config version 1, schema range 1-4, record payload versions, adapter profiles hermes 0.19.1 and watchman 2026.07.27.00;
- the roadmap records all 33 tasks Completed with the v0.1 sequence complete; the deferred future work stays apart (no partially enabled feature), the Hermes plugin remains absent, and production enablement stays the explicit computed-revision operator action.

Review round 1 remediations (all roles, reports_only):

- `agent-dispatch version` reports the delivered webhook adapter (its static-declaration HTTPS sink posture) instead of the stale "not-implemented" entry;
- doctor now satisfies AC-502's stable-nonzero contract: error-severity findings keep the structured stdout result while the command emits the new stable `doctor_findings_present` code (configuration, exit 3) — registry and cli-spec updated, and the affected tests pinned to the new shape;
- the AC-504 evidence runs the real production gate (`route enable --acknowledge-production-gate <computed> --yes`) instead of a direct store write, adds the documented uninstall ordering (route disable, trigger removal under Watchman, configuration and state retention), and the upgrade rehearsal enables the route through the same command with the computed revision;
- the AC-503 audit assertion proves monotonicity (exactly the prune's own audit row is added, nothing deleted), AC-501 asserts the durable intent's webhook target type, and AC-506 asserts the built binary reports v0.1.0 and lists the release-notes artifact;
- the Gate G5 header names all four evidence files, and the installation guide documents the restore procedure the release notes reference.

Round 2 remediations (all roles, reports_only):

- the doctor emission tail is unified (one writer computes the error-finding count in both branches), the config-load failure produces exactly the configuration finding set with nothing fabricated, and the doc comment states the nonzero contract;
- the version command's adapter map is regression-pinned (no not-implemented markers; the hermeswebhook entry names its delivered HTTPS-sink posture);
- the completeness test sandbox resets the XDG variables too.

## 1.0.10 - 2026-08-22

E6-T3: packaging and scheduled reconciliation (SCP-008, OPS-006, OPS-007, OPS-009):

- `make release VERSION=v0.1.0` builds byte-reproducible cross-platform binaries (darwin/arm64, linux/amd64) with `-trimpath`, the full release commit hash, and the commit's committer date as the build time, and emits a portable `LC_ALL=C`-sorted `SHA256SUMS` over the binaries under `dist/`;
- the scheduling examples exist and are validated: the launchd LaunchAgent plist (`plutil -lint` on macOS), the systemd --user service and timer (`systemd-analyze verify` on Linux), and the uninstall script (`sh -n`), wired as `make schedule-check` inside `make verify` so each host lints its own artifact where the tool exists (SCP-008, where possible; corrected in 1.0.14: hosted CI is not used); both schedules invoke the verified `reconcile --reason scheduled` one-shot shape with no daemon, omitting `--submit` before the production gate;
- `agent-dispatch completion bash|zsh` emits the static v0.1 command-tree completion, completing the registered CLI tree;
- `agent-dispatch maintenance backup --output <path>` writes the runbook §8 built-in backup: an owner-only `VACUUM INTO` snapshot with a post-write quick check that refuses to overwrite (cli-spec §11 updated);
- `docs/operations/installation.md` documents the install, platform config/state paths (macOS and XDG Linux), first-use clean-host scenario, daily scheduling, upgrade, backup, and uninstall procedures;
- the uninstall example follows runbook §10 and retains SQLite and configuration by design — `--purge-state` only prints the manual backup guidance; the acceptance lines are pinned by tests (clean-host init through the default paths with owner-only perms and idempotent refusal, backup standalone-open and integrity, schedule shapes, no recursive deletion);
- the Linux CI leg of `make verify` (existing ubuntu-latest matrix) validates the binary, configuration, SQLite, and — with this change — the systemd unit syntax. (Corrected in 1.0.14: no CI run was ever recorded and no successful Linux `make verify` exists; this claim was false as written.)

Review round 1 remediations (all roles, reports_only):

- the release ships byte-reproducible binaries with checksums over the binaries themselves (Go `-trimpath` plus the commit-pinned version, commit, and build time; verified by two consecutive `make release` runs producing identical SHA-256 digests) — the earlier tar archives embedded machine-specific metadata and a stale-checksum edge, so the archive layer is gone, `make clean` removes `dist/`, and the checksum list sorts under `LC_ALL=C`;
- `maintenance backup` refuses an existing target with the new stable `backup_target_exists` code (conflict, exit 14) before opening the store, logs the dedicated `maintenance.backed_up` event instead of the vacuum event, and its failure shapes are test-pinned alongside the missing-flag usage;
- the completion vocabulary derives from the registered command registry at run time (the duplicated list is gone), the no-op zsh transform is removed, and the registry-equals-vocabulary invariant is pinned by a test;
- the cli-spec §2 command tree lists `maintenance backup`; the launchd example drops its fixed `/tmp` error path; the bare-command completeness test regained its teeth (each registered command must reach its own handler with its own error class, never `command_not_implemented` or `command_unknown`).

Review round 2 remediations (all roles, reports_only):

- the release documentation matches the remediated shape everywhere: the installation guide and the changelog headline describe the byte-reproducible binaries with binary-level checksums (no tar layer), the full commit hash is stamped for builder-independent bytes, and the checksum verification line runs from `dist/` as written;
- the backup target guard uses `Lstat`, so a symlink at the target — dangling or not — is itself the `backup_target_exists` conflict and the snapshot can never land through a link the backup path did not create; a failed snapshot removes its partial file so the operator's retry is not misclassified as a conflict; the storage-failure branch and the no-artifact-on-failure property are test-pinned;
- the completion invariant test parses the emitted bash word list back out and compares it set-for-set with the registry (the earlier derivation-based test was tautological); the completeness test sandboxes `HOME` so a bare `init` cannot touch the developer's configuration, allows exactly the three legitimately-succeeding bare commands, and excludes `command_unknown` fallthrough;
- the launchd example's fixed `/tmp` path removal is pinned by a regression assertion; `make schedule-check` lost its dead success flag; the stale dispatcher comments now describe the fully implemented tree.

Round 3 remediations (authorized extra round; all roles, reports_only):

- the scheduling examples' invocation actually runs: the documented global `--output json` option is accepted across the dispatches command family (reconcile included — the high-severity finding that would have failed every scheduled run), pinned by a test driving the example's exact arguments;
- the uninstall example no longer passes the undocumented `--yes` to `route disable`;
- the Lstat symlink guard, the `maintenance.backed_up` event, the zsh completion header, and the launchd `/tmp` exclusion all gained real assertions;
- `make release` warns when git metadata is absent instead of silently stamping `unknown`.

## 1.0.9 - 2026-08-22

E6-T2: doctor, status, retention, and operational observability (OPS-001..005, OPS-008, CLI-004, CLI-007, SEC-007):

- the structured operational log exists (`internal/observability`): one JSON object per line on stderr, the stable §3 event-name vocabulary, the §2 causal correlation fields, the §4 level semantics, and the path-privacy policy (relative, redacted with stable digests, full); note bodies, credentials, and authorization material never survive redaction, and the default warn level keeps successful one-shot commands quiet (CLI-002). The global `--log-level` and `--trace-id` options shape it, and the dispatch runtime emits the attempt-started and classified-outcome lifecycle events with causal IDs;
- `agent-dispatch status` reports route active/dirty state with queue counts, unresolved delivery, quarantine, the oldest unresolved record, the database size, and the offline target capability summary (the webhook's static declaration and the kanban frozen report), warning on dirty routes, pending reconciliation, held quarantine, and delivery uncertainty;
- `agent-dispatch doctor` examines configuration validity, resource roots (existence, readability, owner-only posture), the durable store (open failure, WAL journal mode, quick/integrity check, schema currency), Watchman presence and version, per-target construction gates, secret-reference resolvability without printing values, stale attempt leases, unknown and dead-lettered dispatches, stale active routes, and never-run daily reconciliation — findings carry stable code, severity, summary, details, and remediation, are data on stdout (exit 0), and emit doctor.finding log events at their severities;
- the retention planner and prune are implemented (`internal/app/maintenance` policy resolution with the OPS-003 defaults and configured overrides; `maintenance prune` computes per-class cutoffs children-first): dry-run by default, `--yes` executes in one transaction with foreign keys enforced, `--before` only narrows horizons, unresolved lineages (unknown, retry_wait, dead_lettered, submitting, reconciling) survive intact with their ancestry, state-transition audit rows are never pruned, and the actor, policy cutoffs, and counts are recorded in the append-only audit;
- `maintenance vacuum` requires `--yes` (CLI-007) and refuses with the new stable `maintenance_active_work` code (conflict, exit 14) while any intent is submitting or holds an unexpired lease; `maintenance integrity` reports the check mode and schema currency;
- stale findings are first-class: expired attempt leases, stale active routes against active_stale_after, unknown/dead-lettered counts, and overdue reconciliation surface in both doctor and status;
- the runbook's routine inspection commands (`status`, `doctor`, `doctor --probe-targets`) now exist and are exercised end to end by the CLI suite.

Review round 1 remediations (all roles, reports_only):

- `doctor --integrity full` is reachable (the flag parses as a value option), `--output=json` and boolean equals-forms parse for every ops command, `vacuum --dry-run` is rejected as the contradiction it is, and an invalid `--log-level` fails closed instead of being silently ignored;
- the prune execution guards every foreign-key referencer the plan can meet: held or unresolved quarantine blocks its decision and batch, work receipts outside retention block their intent, and the route's active slot never loses its dispatch; the dry-run plan now mirrors the execution's cascade (decisions freed by pruned intents free batches, which free observations), so the counts cannot diverge;
- `maintenance prune --reason` is recorded in the append-only audit beside the actor, cutoffs, and counts;
- the kanban capability-report path is passed through verbatim like every other consumer (the divergent config-directory resolution rule is gone);
- `doctor --probe-targets` no longer duplicates a target's offline gate failure, a configuration failure no longer fabricates `sqlite_open_failed`, the stale-active age comes from the last completed attempt (lease expiry as fallback), and overdue daily reconciliation (over 25 hours) joins the never-run finding;
- the unknown-delivery lifecycle event logs at WARN per observability §4 (recoverable uncertainty), and log redaction recurses into nested map and list payloads;
- tests pin the retention policy resolution with `--before` narrowing, every doctor finding code with severity and remediation, the prune audit row with actor and reason, plan-versus-execution consistency, held-quarantine lineage preservation, the doctor option surface, and the fail-closed log level;
- docs: configuration-spec §10 states that v0.1 prune resolves the instance-level policy (route-level retention is reserved), and retention-and-privacy §4 names the instance-level `log_paths` policy.

## 1.0.8 - 2026-08-22

E6-T1: the explicit Hermes webhook adapter (contract addition, WHK-001..005, SEC-006, SEC-007):

- the `hermes-webhook` target is wired end to end: `resolveSink` constructs the adapter after its fail-closed gates (https endpoint, bearer/header authentication shape, valid header names, HER-005 required-capability validation against the static declaration), and the unwired-adapter error is gone;
- the capability declaration is static and offline, derived from the frozen E0-T4 §9 evidence (inbound-only receiving platform, not enabled): `durable_acceptance` false — a 2xx is transport acceptance only, never durable (WHK-004) — `submit_idempotency_key` true (the core's key transmitted verbatim under the configured header, default `Idempotency-Key`, WHK-005), every lookup and projection unsupported and never emulated, `maximum_request_bytes` 262144 enforced before transmission;
- the structured HTTP client is the repository's first: one end-to-end submit deadline (default 30s), TLS 1.2+ with system roots and no bypass, no proxy, redirects disabled (an unfollowed 3xx is a definite routing rejection), and a conservative response mapping — definite refusal statuses (400/401/403/404/405/406/410/413/414/415/422 and 3xx) reject; 408/409/429/5xx stay unknown; transport failures provably before transmission (resolution, dial, handshake) are definite non-submission, everything after possible transmission unknown;
- authentication secrets resolve immediately before each submission through the new `secretresolver` adapter (env, file, fd, and the controlled macOS keychain lookup; argv-only, no environment, bounded) and never enter SQLite or logs (SEC-006); every captured response byte is redacted against the resolved secret (SEC-007);
- webhook intents record the endpoint URL as their durable target scope (the analog of the kanban board) at every intent-construction site, so drain reconciliation re-verifies the accepting identity;
- `config validate --probe-targets` reports webhook targets with their declared capabilities and no endpoint network I/O; unknown webhook dispatches dead-letter through drain (no lookup exists) and never fall over to another sink (WHK-002, DUR-008);
- config schema: the `hermesWebhook` target accepts optional `required_capabilities` (mirroring hermes-kanban), and semantic validation enforces the authentication shape (`header` requires `header_name`, `bearer` rejects it);
- docs: hermes-integration §10 carries the declaration table and response mapping; configuration-spec §5 documents the webhook fields and defaults; the frozen capability report gains the append-only E6-T1 derivation note.

Review round 1 remediations (all roles, reports_only):

- `fd:` secret references now survive repeated resolution in one process (the WHK-005 retry posture): the read rewinds a seekable descriptor and loops to EOF so a chunked writer cannot silently truncate the credential, while the descriptor stays open because the launching process owns it;
- every secret kind enforces the 64 KiB bound, and the keychain subprocess output is bounded to it (the comment's "bounded output" claim is now true) with the controlled invocation pinned by a stub test (argv, no inherited environment, bounded failure detail);
- a webhook `submit_timeout` that is schema-pattern-valid but unparseable (int64 overflow) is a configuration failure (exit 3) at dispatch time, matching `config validate --probe-targets`;
- endpoints embedding URL userinfo are rejected at construction (the userinfo would otherwise persist verbatim as the durable target scope and could surface as a Basic authorization header — SEC-006);
- a resolved secret containing characters invalid in a header value is refused before transmission, and transport-failure diagnostics are redacted against the resolved secret (SEC-007);
- a response body that dies mid-stream marks its captured evidence truncated — partial evidence is never presented as complete;
- tests pin the semantic auth-shape gates (the only bearer-shape enforcement), the redirect single-delivery guarantee, the mid-body truncation marker, the userinfo and negative-timeout construction gates, and the probe surface's config_error and capability_mismatch exits;
- docs: the secretresolver package comment no longer claims to be a skeleton, README's project status records E6-T1 complete, and the capability-report note names the public interface report instead of "this report".

Review round 2 remediations (all roles, reports_only):

- `fd:` references cache one `*os.File` wrapper per descriptor for the process lifetime — a transient `os.NewFile` wrapper would be finalized closed by the runtime after a GC cycle, nondeterministically closing the launching process's descriptor (and a later fd-number reuse could resolve an unrelated stream as the credential); the survival is pinned by a GC-forcing test;
- the keychain lookup captures stderr separately from stdout: keychain notices no longer concatenate into the resolved credential (a corrupted token would have surfaced as an undiagnosable permanent 401), and the separation is pinned by a stub that writes to both streams;
- the response-body capture never fabricates adapter text as endpoint evidence: a read error (zero-byte or mid-stream) leaves the body empty or partial, marks the capture truncated, and records the redacted read error explicitly; the oversize capture takes the same shape;
- `file:` and `fd:` reads run under the caller's context with a 10-second default deadline and the 64 KiB bound applied during the read, so a wedged or oversized source can neither hang nor over-allocate a submission; a consumed pipe-backed descriptor reports its one-shot cause distinctly;
- the 64 KiB bound, the dispatch-surface exit-3 timeout classification, transport-diagnostic redaction, the zero-byte capture shape, and the rerun path's endpoint target scope all gained pinning tests (the round-1 test gaps);
- the webhook probe surfaces a Probe failure as config_error instead of reporting empty capabilities.

## 1.0.7 - 2026-08-21

(Known deferred work from this entry: the `pending_reconcile` ABA window is accepted with one-time self-healing semantics; recorded here per the release notes' known-deferred disclosure, E7-T10.)

E5 post-closeout review remediations (no contract surface changes; behavior corrections under the existing contracts):

- attribution: a receipt path the generation never observed is recorded as unresolved `receipt_extra_path` and blocks full suppression — AC-404's extra-paths clause is enforced (`feedback-loop-and-reconciliation.md` §5 vocabulary updated);
- route machine: the declared UNCERTAIN exits are now reachable — full reconciliation is the operator resolution (`UNCERTAIN -> FOLLOWUP_READY` under `reconciliation_resolved`, landing IDLE through `followup_dropped_after_reconciliation` when no work is due; budget exhaustion previously left the route permanently wedged);
- completion transaction: an in-transaction follow-up need the caller never prepared refuses as an optimistic-concurrency conflict instead of dropping the pending reconciliation signal into a follow-up-less FOLLOWUP_READY;
- receipt validation: the document form enforces every schema-required field (`docs/schemas/work-receipt.schema.json`), and durable-store failures during lineage or replay validation classify as storage (exit 20) instead of being written as invalid-receipt audit evidence;
- reconciliation: stored path facts under an unreadable subtree are retained in the replaced snapshot (not merely excluded from the removal diff), completing the round-20 phantom-removal fix;
- records: the follow-up decision records the planned route revision as its policy revision with encoder-produced reason codes (the `"current"` placeholder is gone); the quarantine error boundary classifies store failures as storage and defects as internal instead of relabeling everything storage;
- docs: `processing-pipeline.md` §5 rule 1 no longer carries the refuted class-5 exit wording (error-model §4 boundary notes are authoritative).

Second review round (same date, advisory findings adopted):

- the lost UNCERTAIN-resolution race reports `transition_invalid` (exit 14) instead of a storage failure (`classifyResolutionError`, `TestClassifyResolutionError`), and `quarantine show` wraps durable-read failures as storage instead of internal defects;
- the intent-builder contract is uniform (due work without a builder fails loudly on every eligible route state, never silently drops);
- a no-work UNCERTAIN resolution no longer resurrects its pending generation between two transactions;
- the quarantine domain-outcome classification is shared through `ports.IsQuarantineDomainOutcome`;
- tests pin the quarantine/receipt error-boundary arms (`TestResolutionErrorClassification`, `TestQuarantineErrClassification`, `TestWorkReceiptErrClassification`), the attribution decision document's input-order determinism (`TestMatchDecisionDocumentDeterministic`), the follow-up decision's recorded revisions and reason content, and the single-winner concurrent resolution (`TestConcurrentUncertainResolutionSingleWinner`);
- feedback-loop §10 names `reconcile` as the operator exit from UNCERTAIN, and the full-reconciliation result contract's `reconcile_dispatch_id` condition covers the uncertain-resolution case.

Third review round (same date, convergence):

- the UNCERTAIN resolution is fenced like the completion path: the dirty generation and pending flag observed before the enumeration window must still hold inside the resolution transaction, so a merge or release landing mid-reconciliation refuses as `transition_invalid` instead of being silently absorbed;
- the resolution marks (work due) or clears (no work) its pending generation atomically in the same transaction, removing the second-transaction window; every lost-race arm of the reconcile surface — eligibility, the fence, a held slot — reports `transition_invalid` (exit 14), and `MarkPendingReconcile`/`CommitReconcileDecision` failures classify as storage;
- the reconciliation intent's evidence manifest no longer asserts deletes under unreadable subtrees (matching the removal-diff and snapshot posture);
- `quarantine show` reads share `ports.IsQuarantineDomainOutcome`, the unused resolution method is dropped from the CLI `storeOp` interface, and `Coordinator.Completion`'s unprepared-followup refusal is a typed conflict;
- new coverage: the fence (`TestResolveUncertainReconciliation`), the no-work CLI resolution (`TestUncertainNoWorkResolutionLandsIdle`), the manifest exclusion (`TestReconcileUnreadableSubtreeKeepsStoredFacts`), the builder contract (`TestIntentBuilderRequired`), and the reconcile/read error arms (`TestReconcileErrClassification`, `TestWrapQuarantineReadError`).

Fourth review round (convergence; the logic and security roles returned zero findings):

- the generation-conflict sentinel is shared (`ports.ErrGenerationConflict`, aliased by the adapter) and adopted at every boundary, the idempotency conflict joins the reconcile conflict arms, and the conditional `ClearPendingReconcile` never wipes a pending signal another actor marked inside the window (`TestClearPendingReconcileConditional`) while the idle no-work reconciliation resolves its own observed flag in one transaction;
- the coordinator's typed unprepared-followup refusal is pinned (`TestCompletionRefusesUnpreparedFollowupTyped`) together with the work-command conflict arms, and the document form rejects unknown top-level keys exactly as the published schema does (`doc unknown top field`).

Fifth review round (closure; security clean, logic clean except the residual below):

- receipt validation is exact against the published schema: exact key spellings at the document and change levels (Go's case-insensitive tag matching can no longer absorb a `Dispatch_ID`), and exactly one JSON value — trailing content is malformed input, never silently ignored (`doc case-variant key`, `item case-variant key`, `trailing json value`); the resolution fence's pending-only arm is pinned;
- accepted residual, recorded honestly: `pending_reconcile` is a bare boolean, so a mark folded into an already-true flag inside one enumeration window (an ABA interleaving) can be cleared with the stale signal — bounded to one delayed follow-up, self-healing on the next reconciliation; fully closing it needs a versioned pending signal (a schema change deferred with the finding).

## 1.0.6 - 2026-08-21

E5 contract changes:

- route state machine: the new edges `ACTIVE_DIRTY -> IDLE` (exact suppression, receipt-evidence guarded) and `ACTIVE_CLEAN -> FOLLOWUP_READY` (completion with a pending reconciliation generation) with their documented reasons (`persistence-and-state-machines.md` §6);
- canonical records: the quarantine record, batch record, decision record, and full-reconciliation result JSON shapes are defined (`canonical-record-contracts.md` §7);
- error registry: `work_receipt_invalid` is emitted by the receipt CLI, `quarantine_not_found` joins the usage class (exit 4), and `quarantine_release_denied` documents the denied re-release; the structural holds are successful trigger outcomes (exit 0 with an explicit disposition envelope) and `unsafe_path_quarantined` (class 5) is reserved for a future durable-evidence policy;
- cli-spec: `route enable` requires the computed route revision acknowledgement value; the quarantine command surface (state filter, non-interactive requirements, release semantics) and the reconcile command match the shipped behavior;
- work-receipt schema: the persisted run keeps its begin timestamp (migration v4) so attribution windows survive terminal updates.

## 1.0.5 - 2026-08-20

E2 errata:

- extended the closed error-code registry with the Watchman lifecycle surface: `watchman_unavailable` and `watchman_version_unsupported` (`target_unavailable`, exit 11) and `watchman_trigger_conflict` (`conflict`, exit 14);
- recorded in architecture `watchman-integration.md` §2/§7 that `WATCHMAN_FILES_OVERFLOW` was refuted by the E0-T5 probe and the allowlist includes `WATCHMAN_SOCK`;
- the AC-102 drop reason is emitted as `unchanged_content` (matching the acceptance text) and the E2-T3 same-path coalescing rules are qualified for persisted-prior paths (a create observed over a prior digest is conservatively a modify).

## 1.0.4 - 2026-08-20

Implementation bootstrap errata (E1-T1):

- extended the closed error-code registry with the usage class (exit 2) required by the first real CLI surface: `command_unknown`, `command_not_implemented`, and `flag_invalid`, plus `internal_unclassified` (exit 40) for the panic-recovery path;
- repository-layout now records the importlint-enforced dependency rules and that SOT schemas/examples remain under `docs/schemas` and `docs/examples` until generated schemas exist.

## 1.0.3 - 2026-08-19

Design-review errata on 1.0.2 (see `docs/specs/decision-log.md`):

- the route failure budget is an explicit `failure_budget` field (1 through 10, required, revision-affecting), ending the `execution_hints.max_attempts` overload introduced in D-009; the three budgets are documented together in configuration-spec §9 (D-013);
- completed the partially applied 1.0.2 fixes: no remaining "trusted environment" label in the architecture overview, `SinkCapabilities` no longer embeds `maximum_request_bytes`, and `work-receipt.schema.json` enforces the closed failure-code set with a required non-null code on failed receipts (D-014).

## 1.0.2 - 2026-08-19

Multi-agent design-review errata (see `docs/specs/decision-log.md`):

- made policy-driven unsafe-path quarantine expressible in the closed error registry via `unsafe_path_quarantined` (exit class 5) (D-007);
- clarified decision lineage: a policy decision references either an immutable batch or a durable generation lineage, resolving the follow-up and reconciliation conflict with invariant 1 (D-008);
- specified route behavior when accepted work fails or is canceled: one bounded follow-up while the retry budget remains, operator resolution on exhaustion; added the `work fail` synopsis with a closed failure-code set and a quarantine-release exit edge in the route state machine (D-009);
- added E0-T5 (Watchman public-interface verification and fixture baseline) so E2-T1 builds on frozen evidence, assigned `init` to E1-T2 and route-management commands to E3-T3; the roadmap now has 33 tasks (D-010);
- documented that scheduled reconciliation adds `--submit` only after the production gate, and declared trigger-driven retry liveness intentional (D-011);
- bundled consistency errata across terminology, contracts, examples, and operations docs (D-012).

## 1.0.1 - 2026-08-19

Design-review errata and post-baseline decisions (see `docs/specs/decision-log.md`):

- made the Hermes logical task request single-sourced in the task contract, added `hermes-task-request.schema.json`, and constrained `dispatch-intent.request` with a `$ref` to it (D-001);
- enumerated error categories 1:1 with exit-code classes, closed the error code registry with per-code category/exit mapping, assigned the adapter error codes, and defined exit code 1 as never emitted with panic recovery to exit 40 (D-002);
- replaced the manually curated traceability matrix with output generated by `scripts/generate-traceability.py` (D-003);
- kept E3-T3 as a single task, to re-evaluate at E3 entry (D-004);
- fixed verified cross-document inconsistencies: `lost-cursor` reconcile reason, initial reconciliation type, `test/` in the repository tree, `SetRouteActivation` service placement, conditional `go test` in DoD, trigger-name convention in the config example, and JSON-output-contract deliverables for E3-T3/E5-T4 (D-005);
- kept the E5/E6 detail freeze with an explicit amendment path (D-006);
- added `dispatch-intent` and `dispatch-receipt` examples so every schema has a validating example.

## 1.0.0 - 2026-08-19

Initial approved SOT baseline.

Major resolutions from the discussion draft:

- narrowed the primary product identity from a broad control plane to an event-ingress and activation gateway;
- made Hermes the authoritative runtime;
- prohibited Hermes core modification, internal database access, and a Hermes plugin in v0.1;
- selected Watchman one-shot trigger mode as the initial execution model;
- selected Go, YAML, and SQLite as the implementation stack;
- made SQLite mandatory before the first external side effect;
- separated observations, batches, policy decisions, dispatch intents, attempts, and receipts;
- separated event identity, content fingerprint, idempotency key, and attempt identity;
- selected latest-state processing for the Obsidian vault use case;
- limited the certified v0.1 scope to Markdown changes in one vault and one primary Hermes Kanban target;
- added a route-level single-active-work rule with durable dirty-generation tracking;
- defined retry, reconciliation, reprocessing, rerun, and quarantine as distinct operations;
- scheduled the Hermes webhook adapter after the durable Kanban MVP;
- deferred daemon mode, MCP, multi-vault certification, generic adapters, and a Hermes plugin.

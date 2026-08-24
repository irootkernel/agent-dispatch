# Agent Dispatch v0.1.3

Released 2026-08-24 from the tagged tree. v0.1.3 is the hardening
release closing the deferred inventory the 2026-08-23 compliance
remediation (v0.1.2, D-020) recorded for the next cycle, delivered as
roadmap epic E9 (five tasks, changelog 1.0.36–1.0.42, D-021/D-022).

## Fixed

- **Record-schema truth (E9-T1):** `dispatches show` and `list` emit
  the published record schemas exactly — the request document as the
  schema's object, the schema-required members on every row, migration
  v7's route-revision columns on the four record tables (backfilled by
  join), the connection pragmas riding the DSN, one verified backup per
  migration run, and snake_case wire shapes; the validation remediation
  completes the set with the open outcome for in-flight attempts, null
  shapes for unset members, an enum-clamped dead-letter reason, and
  full-schema validation of the real emissions.
- **Reconciliation and operator surface (E9-T2):** every symlink —
  escaping or in-vault — is skipped from the reconciliation fact set
  and reported, never projected into task manifests; file-level walk
  errors skip exactly the file; `maintenance prune/vacuum` refuse under
  the Watchman trigger environment; `config show` accepts
  `--output json`; the unknown-dispatch work rejection audits through
  the shared service shape; the prune guards share one predicate and
  `route stale` consults a store-level tri-state eligibility rule.
- **Security, observability, and revision hygiene (E9-T3):** log
  sanitization covers string-map values with the same key denylist and
  case folding as the untyped map; a secret file owned by another uid
  is refused; the work commands emit `work.begun`, `work.completed`,
  and `work.receipt_invalid` with trace correlation and doctor findings
  carry `trace_id`; the computed route revision covers the transport
  fields (executable, submit timeout, environment allowlist, manifest
  byte bound) so swapping the target binary pauses the acknowledged
  route; a suppressed mutex key warns as `dispatch.mutex_suppressed`;
  and every policy decision records an independent `PolicyRevision`
  digest of the policy-evaluation surface.
- **Coverage and stability (E9-T4):** the member-task coverage
  deferrals carry executing named tests, the scheduled recipes carry
  `--submit` with the two-key-gate safety stated everywhere, the three
  submit surfaces share one runtime constructor with the lease-TTL
  derivation asserted, the self-healing goldens fail on absence, the
  skill-renderer cross-check pins the rendered instruction, and the
  suite passes twice consecutively under coverage without timing
  failures.
- **Documentation truth and dependency (E9-T5):** the observability,
  configuration, error-model, and required-spec statements match the
  code (the emitted event set, the real status payload, the inert keys,
  the reserved codes, the exit-13 split, the certification posture, the
  `.markdown` scope); `golang.org/x/text` is bumped to v0.41.0
  (GO-2026-5970).
- **Epic validation (D-022):** the whole-epic review converged through
  three remediation rounds plus a clean confirmation; the retention
  prune no longer wedges on a terminal dispatch's begun receipt, and
  the in-flight anchor protection (unresolved state or active slot)
  is pinned from both sides.

## Operational notes

- Routes acknowledged under v0.1.2 re-acknowledge once after upgrade:
  the revision projection gained the transport fields, so every route's
  computed revision changes by design (the same published migration
  semantics as v0.1.2's resource-shape coverage).
- The TST-008 gate (coverage-instrumented verification in `make
  verify`) remains disabled in shipped defaults; the suite's
  twice-consecutive coverage runs are recorded in the E9-T4 evidence.
- The M-8 recrawl exception and the D-018 accepted residuals (L-13
  TOCTOU window, L-14 Watchman lifecycle subprocess) stand as
  maintained exceptions, recorded in D-021/D-022.

## Artifacts

Two statically linked binaries (`darwin/arm64`, `linux/amd64`) with
`SHA256SUMS`, built twice byte-identically from the tagged tree by
`make release VERSION=v0.1.3`.

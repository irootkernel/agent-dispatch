# Testing Strategy

## 1. Test Pyramid

| Layer | Purpose | External dependencies |
|---|---|---|
| Unit | Pure policy, identity, canonicalization, state transitions | none |
| Component | Config, Watchman parser, path safety, Hermes mapper | filesystem fixtures as needed |
| SQLite integration | Constraints, transactions, leases, migrations | real SQLite file |
| Adapter contract | Target outcome classification | fake process/HTTP target and verified fixtures |
| Multi-process | Race and lease correctness | helper processes + real SQLite |
| Crash injection | Every side-effect boundary | helper process termination |
| End-to-end | Real Watchman + disposable Hermes + test vault | installed tools |
| Release | Clean-host install and upgrade | macOS (darwin/arm64, the only supported platform, D-023) |

## 2. Determinism

Tests inject time, IDs, jitter, process runner, and target. No unit test sleeps to wait for backoff or leases. Golden outputs normalize platform-specific temporary paths.

## 3. Unit Test Suites

### Identity and canonicalization

- same semantic changes in different input order;
- path order and operation precedence;
- observation/time variance;
- retry invariance;
- route revision sensitivity;
- rerun sensitivity;
- Unicode normalization policy;
- invalid digest and version rejection.

### Policy

Table-driven tests for every classification and precedence combination. Include protected plus overflow, active plus bulk, and empty plus fresh instance.

### State machines

Generate or enumerate all state pairs and prove only documented transitions succeed. Verify transition reasons and audit events.

## 4. Filesystem Security Tests

Use temporary trees with:

- ordinary files;
- nested directories;
- symlinks inside root;
- symlinks escaping root;
- symlink target replacement race where testable;
- `..`, absolute path, NUL/invalid representation;
- oversized file;
- deleted file;
- permission denied;
- case collision on supported platforms.

The test must assert no outside file was opened, not merely that an error occurred.

## 5. Watchman Fixtures

Each fixture directory contains:

```text
input.json
environment.json
expected-source-input.json
expected-plan.json
README.md
```

Fixture names use scenario IDs, for example `AC-107-overflow`.

Real Watchman fixtures captured in E2-T5 must be sanitized and tagged with Watchman version and platform.

## 6. SQLite Integration

Test:

- pragma verification;
- migration from every supported prior schema;
- unique source event key;
- unique target/idempotency pair;
- foreign-key rejection;
- attempt lease race;
- one active route constraint;
- transaction rollback;
- busy timeout behavior;
- online backup and integrity check;
- retention without orphaning.

## 7. Crash Matrix

| Crash point | Expected recovery |
|---|---|
| before observation transaction | no committed work |
| during observation transaction | rollback |
| after decision, before intent commit | no external call |
| after intent commit, before lease | ready |
| after lease, before process start | definite not submitted if proven; otherwise recover conservatively |
| after process start, before request transmission proof | adapter-classified |
| after possible transmission | unknown |
| after remote acceptance, before local receipt | unknown then lookup |
| during work completion transaction | old active/dirty state remains valid |
| during migration | previous or new valid schema |

Crash tests terminate a helper process, not merely return an injected error, for key cases.

## 8. Fake Hermes Matrix

The fake target supports programmable scenarios:

```text
accept_durable
accept_non_durable
reject_structured
fail_before_submit
timeout_before_accept
timeout_after_accept
malformed_after_accept
duplicate_returns_original
lookup_found
lookup_absent
lookup_unknown
lookup_unsupported
status_progression
oversized_output
```

It records received idempotency keys and request digests for assertions.

## 9. Real End-to-End Tests

Use a disposable vault and disposable Hermes task space. Never run release E2E tests against the production vault.

The harness must capture:

- Watchman trigger definition;
- Agent Dispatch version/config digest;
- Hermes version/capability report;
- source batch;
- database lineage IDs;
- target task ID;
- acceptance result;
- final route state;
- redacted logs.

## 10. CI Stages

Recommended order:

1. formatting and generated-file drift;
2. static analysis;
3. unit tests;
4. schema and example validation;
5. SQLite integration;
6. race tests;
7. multi-process/crash tests;
8. platform matrix;
9. optional real Watchman/Hermes integration on trusted runners.

A test required by the current task cannot be skipped merely because the external tool is inconvenient. Use a trusted runner or keep the task In Progress/Blocked.

## 11. Coverage

Coverage percentage is secondary to state and failure-path completeness. Require explicit tests for every transition, error class, and acceptance scenario. Critical packages should have high branch coverage, but no release claim relies on a single aggregate percentage.

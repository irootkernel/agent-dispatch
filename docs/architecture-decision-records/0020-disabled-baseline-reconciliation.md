# ADR-0020: Disabled Baseline-Only Reconciliation

**Status:** Accepted
**Decision:** D-027
**Target:** v0.1.6

## Context

Guided setup must establish a resource baseline while leaving the route
disabled. The production reconciliation command currently refuses disabled
runtime state, so a prior Watchman installation can make setup reruns fail.
Temporarily enabling the route would cross the production gate.

## Decision

Add an explicit public `reconcile --baseline-only` mode. It is allowed only
when both configuration and runtime activation are disabled. It atomically
stores the bounded snapshot and baseline evidence without creating a decision,
dispatch intent, task, acknowledgement, or notification. It refuses active or
production state and has no submit path.

## Consequences

Setup can be rerun after any non-production step and interrupted baselines are
recoverable. Baseline evidence is distinguishable from ordinary
reconciliation and cannot be presented as production validation.

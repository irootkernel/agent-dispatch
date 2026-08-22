# ADR-0001: Agent Dispatch Is an Event-Ingress and Activation Gateway

> **Status:** Accepted  
> **Date:** 2026-08-19

## Context

The discussion draft described Agent Dispatch as a control-plane layer and a general event-to-agent bridge. That wording risked overlap with Hermes and future agent orchestration systems.

## Decision

Agent Dispatch is an **event-ingress and activation gateway**. It owns observation, deterministic policy, durable handoff, delivery reconciliation, and audit. It does not own agent reasoning, workflow execution, semantic results, or note mutation.

## Consequences

- The core remains useful with different public targets.
- LLM Wiki semantics stay in Hermes skills.
- Execution state is only a projection in Agent Dispatch.
- Features resembling a workflow engine require separate justification.

## Rejected Alternatives

- A general agent control plane: rejected because it duplicates Hermes authority.
- A simple watcher shell hook: rejected because it cannot meet durability and reconciliation goals.

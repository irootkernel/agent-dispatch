# Roadmap

This directory is the sole owner of adopted work-unit identity, execution
order, dependencies, lifecycle vocabulary, and current status.

- [`roadmap.md`](roadmap.md) is the canonical roadmap and status authority.
- [`task-execution-rules.md`](task-execution-rules.md) defines lifecycle
  transitions, the one-active-task rule, and evidence expectations.

The completed v0.1.5 sequence delivered source/reconciliation integrity,
Hermes preflight/setup, multi-destination lifecycle, and durable notification
outbox support. The approved v0.1.6 follow-up proceeds through rerunnable
guided setup, mutex-capability downgrade and serialization groups, automatic
notification draining, and final validation/release. Consult `roadmap.md` for
the current task and status; this index intentionally does not duplicate
mutable lifecycle values.

The repository retains its established `E<n>` and `E<n>-T<n>` identity scheme.
This path migration neither renames work units nor rewrites completed history.

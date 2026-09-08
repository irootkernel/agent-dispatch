# Examples

These fixtures illustrate the [specifications](../specs/README.md) and
[contracts](../contracts/README.md); they do not establish additional behavior.
Schema-covered records are checked by `make schema-validation`.

- [config.yaml](config.yaml): disabled example configuration. Replace example
  paths and destination settings; the [public setup guide](../../README.md#quick-start)
  explains local prerequisites and activation.
- [dispatch-plan.json](dispatch-plan.json) and
  [dispatch-intent.json](dispatch-intent.json): planning and durable delivery records.
- [work-receipt-completed.json](work-receipt-completed.json) and
  [work-receipt-blocked.json](work-receipt-blocked.json): agent completion evidence.
- [scripts](scripts/README.md): launchd and uninstall examples owned by
  [operations](../ops/README.md); `make schedule-check` validates these artifacts.
- [Hermes skill example](hermes-skill/SKILL.md): integration guidance. The
  [packaged skills](../skills/README.md) own the distributable copies.

Examples contain illustrative data. Keep production paths, captured note bodies,
and secrets out of this collection. Preserve record identity and schema references
when adding a new case, and refresh the documentation manifest after changes.

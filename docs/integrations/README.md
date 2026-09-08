# Integration Evidence

This collection preserves bounded public-interface evidence supporting the
specifications and [integration architecture](../architecture/README.md).
It is evidence, not the source of current roadmap status or a guarantee about a
newly installed Hermes or Watchman version.

| Record | Coverage |
|---|---|
| [Hermes public interface](hermes-public-interface-report.md) | Original Kanban capability baseline and fixture provenance |
| [Watchman public interface](watchman-public-interface-report.md) | Event, query, trigger, and watch behavior, including dated amendments |
| [Hermes G11 evidence](hermes-v0.20.5-g11-evidence.md) | Capability and serialization certification |
| [E17 cold validation](e17t2-cold-validation-evidence.md) | v0.1.6 real-environment validation |
| [Capability report](hermes-capability-report.json) | Schema-validated historical capability fixture |

Captured fixtures remain under `fixtures/hermes/` and `fixtures/watchman/`.
Read each report's version, method, environment, and limitations before reusing a
claim. New runs must retain their own snapshot and environment identity instead
of overwriting old proof. The [validation record](../VALIDATION.md) includes gate
G14 for E18; the [roadmap](../roadmap/roadmap.md) owns its delivered outcomes.

`make schema-validation` covers the declared schema fixtures. It does not rerun
external-interface certification. Use the current `agent-dispatch hermes probe`
and authorized disposable integration tests when fresh runtime evidence is needed.

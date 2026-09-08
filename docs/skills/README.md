# Packaged Hermes Skills

These skills provide public Hermes integration guidance under the
[Hermes task contract](../contracts/hermes-task-contract.md), owned by the
specifications role. They are versioned artifacts, not agent permissions or a
second runtime implementation.

| Skill | Purpose | Installation |
|---|---|---|
| [agent-dispatch-operator](agent-dispatch-operator/SKILL.md) | Guide setup, inspection, and bounded recovery | [Instructions](agent-dispatch-operator/INSTALL.md) |
| [agent-dispatch-wiki-maintenance](agent-dispatch-wiki-maintenance/SKILL.md) | Guide worker provenance and work receipts | [Instructions](agent-dispatch-wiki-maintenance/INSTALL.md) |

Install through the documented public Hermes skill mechanism and select the skill
in the intended profile or route. Setup does not install skills automatically.
The generated route requests `llm-wiki`; it is an external prerequisite, not a
skill distributed in this directory.

When changing a skill, reconcile its instructions with the current CLI and receipt
contracts, its declared version scope, and the owning integration tests. Preserve
previous validation as historical evidence; do not relabel it for a new release.

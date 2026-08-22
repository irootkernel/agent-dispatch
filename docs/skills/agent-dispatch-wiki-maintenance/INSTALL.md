# Installing the Agent Dispatch Companion Skill

The companion skill is an ordinary Hermes skill: one directory containing a
`SKILL.md` with YAML frontmatter. Installation uses only the public Hermes
skill mechanism — no plugin, no Hermes core change, no permission grant
(BND-003, BND-004).

## Option A: local copy (recommended)

1. Copy this directory into your Hermes skills directory under a category
   folder of your choice (for example `productivity`):

   ```bash
   mkdir -p ~/.hermes/skills/productivity
   cp -R <agent-dispatch-repo>/docs/skills/agent-dispatch-wiki-maintenance \
     ~/.hermes/skills/productivity/
   ```

2. Verify Hermes recognizes it through the public surface:

   ```bash
   hermes skills list          # shows agent-dispatch-wiki-maintenance, source local, enabled
   hermes skills inspect agent-dispatch-wiki-maintenance
   ```

3. Agent Dispatch routes request the skill by name (`skills: [llm-wiki,
   agent-dispatch-wiki-maintenance]` in the route configuration). Hermes preloads
   it for the task through the same public `--skill` selection the kanban
   create command exposes.

## Option B: install from a URL

If you publish the `SKILL.md` at an HTTPS URL:

```bash
hermes skills install https://<host>/agent-dispatch-wiki-maintenance/SKILL.md \
  --category productivity --name agent-dispatch-wiki-maintenance
```

Pin integrity: publish the SKILL.md alongside its SHA-256 digest and
verify the downloaded file against it before installing (`shasum -a
256` and the `docs/MANIFEST.sha256` entry for the packaged copy) — a
skill is executable guidance, so an unpinned URL must not be trusted
blindly.

## Uninstall

```bash
hermes skills uninstall agent-dispatch-wiki-maintenance
# or, for a local copy: rm -rf ~/.hermes/skills/<category>/agent-dispatch-wiki-maintenance
```

## What the skill does not do

- It does not modify Hermes core, internal storage, configuration, or other
  skills.
- It grants no permissions and requests no tool elevation.
- It is optional: Agent Dispatch dispatch, deduplication, reconciliation, and the
  receipt CLI work identically without it. Without the skill, Agent Dispatch
  simply lacks agent-side provenance and stays conservative (at most one
  redundant latest-state follow-up, never a silently dropped change).

## Validation

The disposable validation harness is `TestHermesCompanionSkillValidated` in
`internal/cli/e5t2_test.go`: it installs the packaged skill into a
throwaway `HOME`, confirms the public skill surface lists and inspects it,
creates a disposable board task that selects it through the public
`--skill` flag, verifies the durable task record carries the skill, and
deletes the board — leaving the real Hermes profile untouched.

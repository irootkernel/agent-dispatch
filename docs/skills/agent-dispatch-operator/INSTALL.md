# Installing the Agent Dispatch Operator Skill

Copy the skill directory into the public Hermes skills location:

```sh
mkdir -p ~/.hermes/skills
cp -R docs/skills/agent-dispatch-operator ~/.hermes/skills/
```

Verify from any Hermes profile:

```sh
hermes -p <profile> skills list --enabled-only
```

The skill changes neither Hermes core nor production state. Remove it
by deleting the copied directory.

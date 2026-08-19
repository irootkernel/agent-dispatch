# Migration and Versioning

## 1. Independent Version Axes

| Axis | Example | Compatibility rule |
|---|---|---|
| Product release | `v0.1.0` | semantic versioning after first release |
| SOT | `1.0.0` | document change control |
| Config | `version: 1` | unknown major rejected |
| SQLite schema | integer migration version | binary supports an explicit range |
| JSON records | `jjukkumi.* /v1` | major contract version |
| CLI envelope | `jjukkumi.cli/v1` | stable machine interface |
| Hermes adapter | target/version compatibility table | exact verified public behavior |
| Watchman adapter | captured fixture/version range | exact verified source behavior |

Do not tie all axes to the product version.

## 2. Config Evolution

- New optional fields may be added within config version 1 when defaults preserve behavior.
- New required semantics require config version 2 or an explicit migration command.
- Unknown fields fail closed to prevent misspelled policy from being ignored.
- `config migrate` may be added in a later release; v0.1 may require manual reviewed migration.

## 3. Database Evolution

- forward-only migrations;
- immutable migration checksums;
- pre-migration backup;
- application-level migration lock;
- no external side effects during migration;
- refuse newer schema;
- document oldest supported upgrade source.

Each release notes whether downgrade is unsupported and how to restore the backup.

## 4. Record Contract Evolution

Persist the record schema version with every bounded JSON payload. Database columns that carry essential query state should not require parsing arbitrary JSON.

Minor additive payload fields must not alter fingerprints unless included in the documented canonical projection. Fingerprint projection version is explicit.

## 5. Adapter Compatibility

Hermes and Watchman versions can change independently. The binary contains or loads verified compatibility profiles. A version outside the known range fails probe/doctor before production submission unless an explicit `--allow-unsupported` development flag exists. Such a flag must never be accepted by the installed production Watchman command.

## 6. SOT Change Control

A change to a MUST requirement, authority boundary, delivery guarantee, state machine, identity derivation, or roadmap order requires:

1. an ADR or superseding ADR;
2. SOT version update;
3. required-spec and traceability update;
4. migration/compatibility assessment;
5. acceptance test update.

Editorial clarification that changes no behavior may update the SOT patch version.

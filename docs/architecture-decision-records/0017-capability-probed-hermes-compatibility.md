# ADR-0017: Capability-Probed Hermes Compatibility

- **Status:** Accepted for v0.1.5
- **Date:** 2026-08-25
- **Extends:** ADR-0002 and ADR-0011

## Context

Exact-version allowlisting blocks known-compatible Hermes upgrades, while an
open-ended semantic-version range assumes unstable public behavior. Hermes must
not be modified to satisfy Agent Dispatch.

## Decision

Hermes 0.19.1 is the minimum eligible version and there is no fixed maximum.
Eligibility is followed by a strict public-CLI capability probe. Profile
enumeration uses bounded JSON. Skill availability uses the existing public
profile-scoped human command under a fixed rendering environment and a
fail-closed parser. Evidence is cached by executable digest, version, and probe
contract; activation acknowledges its exact fingerprint.

## Consequences

Compatible future versions can work without a source allowlist edit. Any
missing command, malformed response, table drift, executable change, or missing
profile/skill blocks enablement and submission with a concrete remediation.
Hermes source, private state, and plugin surfaces remain out of scope.

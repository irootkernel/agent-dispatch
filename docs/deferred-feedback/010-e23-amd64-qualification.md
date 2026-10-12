# DF-010: Qualify the management successor on Linux amd64

Recorded 2026-10-12 under Master's approved D-031 ARM-scoped E23 completion.
The owner is the Core verification maintainer.

**Affected authority.** SCP-008, SMR-015 and G19 retain three-platform release
verification. D-031 permits E23 completion and Plugin TASK-028's qualification
handoff on native Darwin arm64/Linux arm64 only. Plugin EPIC-009's native amd64
inspection qualification remains independent.

**Bounded concern.** Product source
`6786088bc7b6540b34f25c9a398e644dae6a4547` passed both native ARM full gates
and the native paired public CLI matrix. Its reproducible Linux amd64 artifact
is built but unverified. Emulation passed the unit suite, but the CLI race
package reached the unchanged 30-minute limit and `make verify` exited 2.
This establishes a missing full-platform gate, not an individual assertion
failure or race violation. It cannot be recorded as amd64 qualification.

**Reconsideration condition.** Before a future three-platform successor
release or amd64 management admission, use a qualified Linux amd64 environment
and run the entire repository-owned `make verify` with the pinned toolchain.
Bind successful terminal evidence to the exact selected source and artifact
SHA, clearly label emulation if used, and update validation and the consumer
handoff. If the source changes, refresh affected platform and paired evidence;
an old source pass cannot qualify a new binary. Do not silently change the
timeout, skip checks, reuse a unit-only pass, broaden Plugin pins or publish.
Any canonical-gate correction, installation or publication requires its own
authority. This follow-up is unfinished and is not part of E23's ARM closeout.

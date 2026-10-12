# DF-010: Qualify the management successor on Linux amd64

Recorded 2026-10-12 under Master's approved D-031 ARM-scoped E23 completion.
The owner is the Core verification maintainer.

**Status:** Resolved 2026-10-13 for the source and artifact below.

**Affected authority.** SCP-008, SMR-015 and G19 retain three-platform release
verification. D-031 permits E23 completion and Plugin TASK-028's qualification
handoff on native Darwin arm64/Linux arm64 only. Plugin EPIC-009's native amd64
inspection qualification remains independent.

**Original bounded concern.** The selected product source is
`f263e096c5b01fe2a83c7f95c66410035030a752`. Its code, tests, build and contract
inputs passed both native ARM full gates on the fixed source projection;
the later roadmap completion note and generated manifest passed focused checks.
Both exact ARM binaries passed the refreshed native paired public CLI matrix,
including paused-dispatch recovery on the original handles. At the ARM closeout,
the reproducible Linux amd64 artifact was built only, with no new execution result.

Earlier emulation on source `6786088bc7b6540b34f25c9a398e644dae6a4547`
passed the unit suite, but the CLI race package reached the unchanged 30-minute
limit and `make verify` exited 2. That historical result cannot qualify the
current binary. It left a missing full-platform gate at that time and did not establish an
individual assertion failure or race violation.

**Original reconsideration condition.** Before a future three-platform successor
release or amd64 management admission, use a qualified Linux amd64 environment
and run the entire repository-owned `make verify` with the pinned toolchain.
Bind successful terminal evidence to the exact selected source and artifact
SHA, clearly label emulation if used, and update validation and the consumer
handoff. If the source changes, refresh affected platform and paired evidence;
an old source pass cannot qualify a new binary. Do not silently change the
timeout, skip checks, reuse a unit-only pass, broaden Plugin pins or publish.
Any canonical-gate correction, installation or publication requires its own
authority. This follow-up was separate from E23's ARM closeout.

**Resolution: 2026-10-13.** The selected source's native Linux amd64 full gate
is qualified. The cloud verification maintainer supplied terminal evidence
from a clean checkout of `f263e096c5b01fe2a83c7f95c66410035030a752` on a native
x86_64 KVM virtual machine with Go `1.27.1`. Default `make verify` exited 0,
including the unchanged 30-minute unit and race targets and Linux systemd
artifact lint. Real Watchman was available through the isolated test runner.
The successful unit stage reused 30 package results from the same source's
prior attempt; individual test skips were not observable in the non-verbose
output. Optional real-Hermes integration is not established by this result.
macOS launchd lint was skipped on Linux and remains covered by native Darwin.

A native `make release` from the same source reproduced the Linux amd64 binary
`fc6c676063357a70058f1b1227d44e0e0dde6f4ba1fef2989317a950844f11bd`;
its ELF architecture and Go build metadata identify Linux amd64, and native
`version` and `version --json` exited 0 with `v0.3.0`. The documentation-only
HEAD `15092eb47760940fd008c2938ebc26f6d2abb163` passed focused documentation
and contract checks. The local coordinator checked the supplied bindings and
repository inputs; it did not independently read the cloud filesystem.

DF-010 is resolved for this source and artifact. New source or release inputs
require fresh applicable qualification. Native paired management and service
lifecycle evidence remains ARM-only, as does Plugin TASK-028's admission scope.
This resolution grants no Plugin pin change, installation, activation or release.

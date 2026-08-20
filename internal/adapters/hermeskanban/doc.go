// Package hermeskanban implements the Hermes Kanban sink adapter against
// the verified public Hermes CLI only (E4-T1): the exact-version gate, the
// read-only capability probe over the frozen E0-T4 capability report, and
// the controlled CLI transport with typed structured response parsing.
//
// Every command, flag, response shape, and error behavior used here is
// frozen evidence from docs/integrations/hermes-public-interface-report.md
// (probed against Hermes 0.19.1); nothing may be assumed beyond that
// report (HER-002), no Hermes internal database or private API is touched
// (HER-010), and no human text decides acceptance — only exit 0 plus a
// successfully typed --json response does (HER-003, sink-adapter-contract
// §6).
package hermeskanban

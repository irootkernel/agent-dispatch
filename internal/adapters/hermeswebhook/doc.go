// Package hermeswebhook implements the explicit Hermes webhook sink
// adapter (E6-T1, WHK-001..005, SEC-006, SEC-007): authenticated
// outbound delivery of the logical task contract over HTTPS as one
// explicit route target, with no fallback coupling to the Kanban path
// (WHK-002, DUR-008, ADR-0010).
//
// The capability declaration is static and conservative, derived from
// the frozen E0-T4 evidence in
// docs/integrations/hermes-public-interface-report.md §9: the inspected
// Hermes installation's webhook receiving platform is inbound-only and
// not enabled, so no response contract proves durable acceptance. A 2xx
// therefore proves transport acceptance only (WHK-004); lookups and
// execution projection are unsupported and never emulated.
package hermeswebhook

package hermeskanban

import (
	"context"
	"errors"
	"fmt"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// Adapter is the E4-T1 Hermes Kanban target facade: it owns the
// exact-version gate, the read-only capability probe, and the typed CLI
// transport. Durable submission and reconciliation wiring arrive with
// E4-T3 on top of this transport; until then no submit path may use this
// package to claim acceptance.
type Adapter struct {
	id         string
	client     *Client
	reportPath string
	required   []string
}

// New builds the adapter for one configured target. executable is the
// verified public CLI, reportPath the frozen E0-T4 capability report, and
// required the route-facing required capability names (HER-005).
func New(targetID, executable, reportPath string, required []string, limits ProcessLimits) *Adapter {
	return &Adapter{
		id:         targetID,
		client:     NewClient(executable, limits),
		reportPath: reportPath,
		required:   append([]string(nil), required...),
	}
}

// ID is the configured target ID.
func (a *Adapter) ID() string { return a.id }

// Type is the explicit sink type; no fallback to another type exists
// (DUR-008).
func (a *Adapter) Type() ports.SinkType { return ports.SinkHermesKanban }

// Client exposes the typed transport for the submit orchestration.
func (a *Adapter) Client() *Client { return a.client }

// Required lists the configured required capabilities.
func (a *Adapter) Required() []string { return append([]string(nil), a.required...) }

// Probe is the read-only capability probe. It discovers the installed
// version through the public CLI only, gates it against the
// runtime-verified set, loads the frozen capability report, requires the
// report to have been probed against this exact version, and validates
// the required capabilities (HER-002/HER-004/HER-005). It never mutates
// any Hermes state and never touches an internal database (HER-010).
func (a *Adapter) Probe(ctx context.Context) (ports.Capabilities, error) {
	version, err := a.client.DiscoverVersion(ctx)
	if err != nil {
		return ports.Capabilities{}, fmt.Errorf("target %s: %w", a.id, err)
	}
	return a.probeWithVersion(ctx, version)
}

// probeWithVersion performs every post-discovery probe step against one
// already-discovered version, so Probe and ProbeVerbose gate exactly the
// version they report.
func (a *Adapter) probeWithVersion(ctx context.Context, version Version) (ports.Capabilities, error) {
	if err := CheckVersionSupported(version); err != nil {
		return ports.Capabilities{}, fmt.Errorf("target %s: %w", a.id, err)
	}
	report, err := LoadReport(a.reportPath)
	if err != nil {
		return ports.Capabilities{}, fmt.Errorf("target %s: %w", a.id, err)
	}
	if !report.VersionMatchsWith(version) {
		return ports.Capabilities{}, fmt.Errorf("target %s: %w", a.id, &ReportError{
			Detail: fmt.Sprintf("report records hermes %s but %s is installed; re-run the E0-T4 probe for this version", report.HermesVersion, version),
		})
	}
	caps := report.PortCapabilities()
	if err := ValidateRequired(a.id, caps, a.required); err != nil {
		return caps, fmt.Errorf("target %s: %w", a.id, err)
	}
	return caps, nil
}

// ProbeSummary is the operator-facing result of one probe, used by
// `config validate --probe-targets` (cli-spec §3).
type ProbeSummary struct {
	TargetID string
	// State is one of available, version_unsupported, unavailable,
	// capability_mismatch, or config_error.
	State   string
	Version string
	Detail  string // bounded, redacted operator detail
}

// ProbeVerbose discovers the version once and classifies every failure
// mode for the validation surface: a missing required capability
// (capability_mismatch) and a persistent configuration defect
// (config_error: unreadable/stale report, unknown capability name) are
// validation failures returned as errors so the caller fails closed
// (HER-005); an unusable or version-unsupported target is a summary
// state the caller reports without failing config validation.
func (a *Adapter) ProbeVerbose(ctx context.Context) (ProbeSummary, ports.Capabilities, error) {
	summary := ProbeSummary{TargetID: a.id, State: "available"}
	version, err := a.client.DiscoverVersion(ctx)
	if err != nil {
		summary.State = "unavailable"
		summary.Detail = truncate(err.Error(), diagnosticBound)
		return summary, ports.Capabilities{}, nil
	}
	summary.Version = version.String()
	if unsupported := CheckVersionSupported(version); unsupported != nil {
		summary.State = "version_unsupported"
		summary.Detail = truncate(unsupported.Error(), diagnosticBound)
		return summary, ports.Capabilities{}, nil
	}
	caps, perr := a.probeWithVersion(ctx, version)
	if perr == nil {
		return summary, caps, nil
	}
	summary.Detail = truncate(perr.Error(), diagnosticBound)
	var capability *CapabilityError
	var report *ReportError
	var requirement *InvalidRequirementError
	switch {
	case errors.As(perr, &capability):
		summary.State = "capability_mismatch"
	case errors.As(perr, &report), errors.As(perr, &requirement):
		// Persistent configuration defects (unreadable or stale report,
		// unknown required-capability name) fail validation, distinct
		// from a transiently unreachable target (HER-005).
		summary.State = "config_error"
	default:
		summary.State = "unavailable"
		return summary, caps, nil
	}
	return summary, caps, perr
}

package hermeskanban

import (
	"context"
	"fmt"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// UnconditionalCapabilities is the delivery-evidence set every Hermes
// Kanban destination depends on (durable acceptance, idempotent
// submission, external-reference reconciliation). Since the v0.1.5
// cutover it is a contract constant rather than a per-route
// configuration list: the frozen 0.20.5 runtime-verified interface is
// its interim truth source until the E11-T2 probe proves the shapes per
// executable.
var UnconditionalCapabilities = []string{"durable_acceptance", "submit_idempotency_key", "lookup_by_external_ref"}

// Adapter is the Hermes Kanban target facade: it owns the
// minimum-version eligibility gate (HER-011, ADR-0017) and the typed CLI
// transport. Since the v0.1.5 destinations cutover there is no
// operator-authored capability report: eligibility is followed by the
// public-interface capability probe (E11-T2), which binds activation to
// the capability-evidence fingerprint; until it lands, this gate proves
// version eligibility only and the submit path keeps its typed
// fail-closed response classification.
type Adapter struct {
	id      string
	client  *Client
	minimum Version
}

// New builds the adapter for one configured hermes target. executable is
// the verified public CLI and minimumVersion the declared eligibility
// floor ("" is accepted only from pre-validation callers; the loader rejects an omitted
// below it).
func New(targetID, executable, minimumVersion string, limits ProcessLimits) (*Adapter, error) {
	minimum, err := ParseMinimumVersion(minimumVersion)
	if err != nil {
		return nil, fmt.Errorf("target %s: %w", targetID, err)
	}
	return &Adapter{
		id:      targetID,
		client:  NewClient(executable, limits),
		minimum: minimum,
	}, nil
}

// ID is the configured target ID.
func (a *Adapter) ID() string { return a.id }

// Type is the explicit sink type; no fallback to another type exists
// (DUR-008).
func (a *Adapter) Type() ports.SinkType { return ports.SinkHermesKanban }

// Client exposes the typed transport for the submit orchestration.
func (a *Adapter) Client() *Client { return a.client }

// Minimum is the parsed eligibility floor.
func (a *Adapter) Minimum() Version { return a.minimum }

// Probe is the read-only eligibility probe. It discovers the installed
// version through the public CLI only and gates it against the declared
// floor (HER-011). It never mutates any Hermes state and never touches
// an internal database (HER-010). Capability-shape probing against the
// required Kanban JSON operations and the profile-scoped skill table
// arrives with E11-T2 (HER-012); until then the submit path's typed
// response classification remains the fail-closed shape check.
func (a *Adapter) Probe(ctx context.Context) (ports.Capabilities, error) {
	version, err := a.client.DiscoverVersion(ctx)
	if err != nil {
		return ports.Capabilities{}, fmt.Errorf("target %s: %w", a.id, err)
	}
	if err := CheckVersionEligible(version, a.minimum); err != nil {
		return ports.Capabilities{}, fmt.Errorf("target %s: %w", a.id, err)
	}
	return ports.Capabilities{}, nil
}

// ProbeSummary is the operator-facing result of one probe, used by
// `config validate --probe-targets` (cli-spec §3).
type ProbeSummary struct {
	TargetID string
	// State is one of available, version_unsupported, or unavailable.
	// The capability_mismatch and config_error classes return with the
	// E11-T2 capability probe.
	State   string
	Version string
	Detail  string // bounded, redacted operator detail
}

// ProbeVerbose discovers the version once and classifies every failure
// mode for the validation surface: an unusable target is a summary state
// the caller reports without failing configuration validation, while a
// version below the eligibility floor fails the enable gate (HER-011).
func (a *Adapter) ProbeVerbose(ctx context.Context) (ProbeSummary, ports.Capabilities, error) {
	summary := ProbeSummary{TargetID: a.id, State: "available"}
	version, err := a.client.DiscoverVersion(ctx)
	if err != nil {
		summary.State = "unavailable"
		summary.Detail = truncate(err.Error(), diagnosticBound)
		return summary, ports.Capabilities{}, nil
	}
	summary.Version = version.String()
	if unsupported := CheckVersionEligible(version, a.minimum); unsupported != nil {
		summary.State = "version_unsupported"
		summary.Detail = truncate(unsupported.Error(), diagnosticBound)
		return summary, ports.Capabilities{}, nil
	}
	return summary, ports.Capabilities{}, nil
}

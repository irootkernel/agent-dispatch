// Package config implements operator configuration loading and validation
// (SCP-006): YAML with duplicate-key rejection and fail-closed unknown
// fields, JSON Schema validation against the SOT config schema, semantic
// validation beyond the schema, a secret-reference parser that never
// resolves values (SEC-006), redacted normalized output, and the computed
// deterministic route and destination revisions (POL-007, FAN-012).
package config

// Config is the typed operator configuration. Since the v0.1.5 cutover
// (E11-T1, OPS-014) each route declares `destinations[]` and Hermes
// targets live under `hermes_targets`; the legacy `routes.<id>.dispatch`
// shape is refused with a regeneration path and never converted.
type Config struct {
	Version       int                     `yaml:"version"        json:"version"`
	Instance      Instance                `yaml:"instance"       json:"instance"`
	Limits        Limits                  `yaml:"limits"         json:"limits,omitempty"`
	Resources     map[string]Resource     `yaml:"resources"      json:"resources"`
	Targets       map[string]Target       `yaml:"targets"        json:"targets"`
	HermesTargets map[string]HermesTarget `yaml:"hermes_targets" json:"hermes_targets"`
	Routes        map[string]Route        `yaml:"routes"         json:"routes"`
	Retention     *Retention              `yaml:"retention"      json:"retention,omitempty"`

	// Warnings carries non-fatal semantic validation warnings; it is
	// produced by the loader and never part of the configuration document.
	Warnings []string `yaml:"-" json:"-"`
}

// Instance identifies this agent-dispatch installation (§3).
type Instance struct {
	ID       string `yaml:"id"         json:"id"`
	StateDir string `yaml:"state_dir"  json:"state_dir,omitempty"`
	LogPaths string `yaml:"log_paths"  json:"log_paths,omitempty"`
}

// Limits are the defensive ingress and subprocess bounds (§2, SEC-009).
type Limits struct {
	MaxStdinBytes         *int64 `yaml:"max_stdin_bytes"          json:"max_stdin_bytes,omitempty"`
	MaxPathBytes          *int64 `yaml:"max_path_bytes"           json:"max_path_bytes,omitempty"`
	MaxHashFileBytes      *int64 `yaml:"max_hash_file_bytes"      json:"max_hash_file_bytes,omitempty"`
	MaxSubprocessOutBytes *int64 `yaml:"max_subprocess_output_bytes" json:"max_subprocess_output_bytes,omitempty"`
}

// Resource is a watched directory resource (§4).
type Resource struct {
	Type      string `yaml:"type"        json:"type"`
	Root      string `yaml:"root"        json:"root"`
	FileScope string `yaml:"file_scope"  json:"file_scope"`
	Git       *Git   `yaml:"git"         json:"git,omitempty"`
}

// Git is the optional resource git enrichment policy.
type Git struct {
	Mode string `yaml:"mode" json:"mode"`
}

// Target is a webhook delivery target; exactly one variant applies (§5).
// Hermes Kanban targets moved to HermesTarget under `hermes_targets` with
// the v0.1.5 destinations cutover; a `hermes-kanban` entry here is a
// legacy shape refused with a regeneration path.
type Target struct {
	Type string `yaml:"type" json:"type"`
	// Endpoint is the webhook endpoint; it must be an https URL.
	Endpoint          string `yaml:"endpoint,omitempty"             json:"endpoint,omitempty"`
	SubmitTimeout     string `yaml:"submit_timeout,omitempty"        json:"submit_timeout,omitempty"`
	Auth              *Auth  `yaml:"auth,omitempty"                 json:"auth,omitempty"`
	IdempotencyHeader string `yaml:"idempotency_header,omitempty"    json:"idempotency_header,omitempty"`
	// RequiredCapabilities names the capabilities a webhook destination
	// requires; the static webhook declaration fails closed on any it
	// does not provide (HER-005).
	RequiredCapabilities []string `yaml:"required_capabilities,omitempty" json:"required_capabilities,omitempty"`
}

// HermesTarget is a Hermes Kanban delivery target under
// `hermes_targets` (E11-T1, ADR-0017). Compatibility is probed against
// the public interface; there is no operator-authored capability report
// and no maximum version (HER-011 through HER-013 land with E11-T2's
// probe; until then activation gates on minimum-version eligibility).
type HermesTarget struct {
	// Board is the Hermes kanban board slug; the operator creates it
	// with the public boards create command.
	Board          string `yaml:"board"                   json:"board"`
	Executable     string `yaml:"executable,omitempty"    json:"executable,omitempty"`
	MinimumVersion string `yaml:"minimum_version,omitempty" json:"minimum_version,omitempty"`
	// Compatibility is the probed-compatibility mode; v0.1.5 accepts
	// exactly capability_probe.
	Compatibility        string   `yaml:"compatibility"             json:"compatibility"`
	SubmitTimeout        string   `yaml:"submit_timeout,omitempty"  json:"submit_timeout,omitempty"`
	LookupTimeout        string   `yaml:"lookup_timeout,omitempty"  json:"lookup_timeout,omitempty"`
	EnvironmentAllowlist []string `yaml:"environment_allowlist,omitempty" json:"environment_allowlist,omitempty"`
}

// Route binds a source to one or more destinations under a policy (§6,
// §14). Delivery envelope fields that predate the cutover stay
// route-level: submission retry, the latest-state flag, the failure
// budget, and the stale-active window govern the route's single dispatch
// pipeline until E12 runs per-destination lanes.
type Route struct {
	Enabled          bool           `yaml:"enabled"           json:"enabled"`
	Source           Source         `yaml:"source"            json:"source"`
	Batching         Batching       `yaml:"batching"          json:"batching"`
	Policy           Policy         `yaml:"policy"            json:"policy"`
	FanoutMode       string         `yaml:"fanout_mode"       json:"fanout_mode"`
	Destinations     []Destination  `yaml:"destinations"      json:"destinations"`
	Notifications    *Notifications `yaml:"notifications,omitempty" json:"notifications,omitempty"`
	SubmissionRetry  Retry          `yaml:"submission_retry"  json:"submission_retry"`
	LatestState      bool           `yaml:"latest_state"      json:"latest_state"`
	FailureBudget    int            `yaml:"failure_budget"    json:"failure_budget"`
	ActiveStaleAfter string         `yaml:"active_stale_after" json:"active_stale_after"`
	Reconciliation   Reconciliation `yaml:"reconciliation"    json:"reconciliation"`
	Retention        *Retention     `yaml:"retention,omitempty" json:"retention,omitempty"`
	// AllowCrossGroupConcurrency acknowledges that destinations governing
	// this route's resource run under different serialization groups
	// (CON-013, ADR-0021): the acknowledgement joins the route revision,
	// so flipping it pauses production acknowledgement.
	AllowCrossGroupConcurrency bool `yaml:"allow_cross_group_concurrency,omitempty" json:"allow_cross_group_concurrency,omitempty"`
}

// Destination is one durable delivery lane under `destinations[]`
// (FAN-001): a unique stable ID, a non-empty workstream, and the target
// binding with its execution envelope. Selection conditions are closed
// structural classes evaluated with OR-within-key and AND-across-keys
// semantics (FAN-004, FAN-005); the evaluator arrives with E12-T2.
type Destination struct {
	ID                 string   `yaml:"id"               json:"id"`
	Target             string   `yaml:"target"           json:"target"`
	Profile            string   `yaml:"profile,omitempty"   json:"profile,omitempty"`
	Skills             []string `yaml:"skills"         json:"skills"`
	Workstream         string   `yaml:"workstream"      json:"workstream"`
	Workspace          string   `yaml:"workspace,omitempty" json:"workspace,omitempty"`
	SerializationGroup string   `yaml:"serialization_group,omitempty" json:"serialization_group,omitempty"`
	// MutexKey is the deprecated compatibility alias of
	// SerializationGroup (CON-011, ADR-0021): it may coexist with the
	// explicit field only when the values are identical (a deprecation
	// warning); differing values fail validation. New configuration never
	// emits it.
	MutexKey       string         `yaml:"mutex_key,omitempty" json:"mutex_key,omitempty"`
	ExecutionHints ExecutionHints `yaml:"execution_hints"  json:"execution_hints"`
	Conditions     *Conditions    `yaml:"conditions,omitempty" json:"conditions,omitempty"`
}

// Conditions is the closed structural selection vocabulary over path,
// operation, classification, and policy outcome (FAN-004). Values within
// one key OR; present keys AND; absent keys select unconditionally
// (FAN-005).
type Conditions struct {
	PathInclude     []string `yaml:"path_include,omitempty"     json:"path_include,omitempty"`
	PathExclude     []string `yaml:"path_exclude,omitempty"     json:"path_exclude,omitempty"`
	Operations      []string `yaml:"operations,omitempty"       json:"operations,omitempty"`
	Classifications []string `yaml:"classifications,omitempty"  json:"classifications,omitempty"`
	PolicyOutcomes  []string `yaml:"policy_outcomes,omitempty"  json:"policy_outcomes,omitempty"`
}

// Notifications is the per-route notification policy (NTF-001): disabled
// when no sinks are configured. The durable outbox (ADR-0019) enqueues
// inside the owning transition and delivers through the configured
// sinks after commit; the policy revision digests the effective event
// set and sink identities (NTF-003).
type Notifications struct {
	Events []string           `yaml:"events" json:"events"`
	Sinks  []NotificationSink `yaml:"sinks"  json:"sinks"`
}

// NotificationSink is one declared notification sink. A webhook sink
// carries an https endpoint and an authentication reference; a log sink
// emits structured records without external effects.
type NotificationSink struct {
	ID       string `yaml:"id"                  json:"id"`
	Type     string `yaml:"type"                json:"type"`
	Endpoint string `yaml:"endpoint,omitempty"  json:"endpoint,omitempty"`
	Auth     *Auth  `yaml:"auth,omitempty"      json:"auth,omitempty"`
}

// Auth is the webhook authentication reference (§5). The secret is a
// reference only; it is never resolved or printed.
type Auth struct {
	Type       string `yaml:"type"        json:"type"`
	SecretRef  string `yaml:"secret_ref"  json:"secret_ref"`
	HeaderName string `yaml:"header_name,omitempty" json:"header_name,omitempty"`
}

// Source is the watchman-trigger binding (§6).
type Source struct {
	Type        string   `yaml:"type"         json:"type"`
	SourceID    string   `yaml:"source_id"    json:"source_id"`
	Resource    string   `yaml:"resource"     json:"resource"`
	TriggerName string   `yaml:"trigger_name" json:"trigger_name"`
	Include     []string `yaml:"include"      json:"include"`
	Exclude     []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
}

// Batching bounds a route batch (§6).
type Batching struct {
	AutomaticThreshold int `yaml:"automatic_threshold" json:"automatic_threshold"`
	HardLimit          int `yaml:"hard_limit"          json:"hard_limit"`
	MaxManifestBytes   int `yaml:"max_manifest_bytes"  json:"max_manifest_bytes"`
}

// Policy selects structural actions (§8).
type Policy struct {
	Protected           []string `yaml:"protected"          json:"protected"`
	Immutable           []string `yaml:"immutable"          json:"immutable"`
	BulkAction          string   `yaml:"bulk_action"        json:"bulk_action"`
	OverflowAction      string   `yaml:"overflow_action"    json:"overflow_action"`
	FreshInstanceAction string   `yaml:"fresh_instance_action" json:"fresh_instance_action"`
	UnsafePathAction    string   `yaml:"unsafe_path_action" json:"unsafe_path_action"`
}

// Retry is the delivery-only submission retry policy (§9).
type Retry struct {
	MaxAttempts    int     `yaml:"max_attempts"     json:"max_attempts"`
	InitialBackoff string  `yaml:"initial_backoff"  json:"initial_backoff"`
	MaxBackoff     string  `yaml:"max_backoff"      json:"max_backoff"`
	Multiplier     float64 `yaml:"multiplier"       json:"multiplier"`
	JitterFraction float64 `yaml:"jitter_fraction" json:"jitter_fraction"`
}

// ExecutionHints advise the target about execution bounds (§9). They are
// destination-scoped: each destination's lane submits its own bounds.
type ExecutionHints struct {
	MaxRuntime  string `yaml:"max_runtime"   json:"max_runtime"`
	MaxAttempts int    `yaml:"max_attempts" json:"max_attempts"`
}

// Reconciliation declares expected reconciliation generations (§6).
type Reconciliation struct {
	Initial       bool `yaml:"initial"        json:"initial"`
	DailyExpected bool `yaml:"daily_expected" json:"daily_expected"`
}

// Retention is the retention policy (§10).
type Retention struct {
	Observations       *string `yaml:"observations,omitempty"       json:"observations,omitempty"`
	Attempts           *string `yaml:"attempts,omitempty"           json:"attempts,omitempty"`
	CompletedReceipts  *string `yaml:"completed_receipts,omitempty" json:"completed_receipts,omitempty"`
	ResolvedQuarantine *string `yaml:"resolved_quarantine,omitempty" json:"resolved_quarantine,omitempty"`
	Unresolved         *string `yaml:"unresolved,omitempty"         json:"unresolved,omitempty"`
}

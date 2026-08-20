// Package config implements operator configuration loading and validation
// (SCP-006): YAML with duplicate-key rejection and fail-closed unknown
// fields, JSON Schema validation against the SOT config schema, semantic
// validation beyond the schema, a secret-reference parser that never
// resolves values (SEC-006), redacted normalized output, and the computed
// deterministic route revision (POL-007).
package config

// Config is the typed operator configuration (configuration-spec §2).
type Config struct {
	Version   int                 `yaml:"version"     json:"version"`
	Instance  Instance            `yaml:"instance"    json:"instance"`
	Limits    Limits              `yaml:"limits"      json:"limits,omitempty"`
	Resources map[string]Resource `yaml:"resources"   json:"resources"`
	Targets   map[string]Target   `yaml:"targets"     json:"targets"`
	Routes    map[string]Route    `yaml:"routes"      json:"routes"`
	Retention *Retention          `yaml:"retention"   json:"retention,omitempty"`

	// Warnings carries non-fatal semantic validation warnings; it is
	// produced by the loader and never part of the configuration document.
	Warnings []string `yaml:"-" json:"-"`
}

// Instance identifies this jjukkumi installation (§3).
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

// Target is a delivery target; exactly one variant applies (§5).
type Target struct {
	Type string `yaml:"type" json:"type"`
	// Board is the Hermes kanban board slug for hermes-kanban targets;
	// the operator creates it with the public boards create command.
	Board                string   `yaml:"board,omitempty"       json:"board,omitempty"`
	Executable           string   `yaml:"executable,omitempty"   json:"executable,omitempty"`
	CapabilityReport     string   `yaml:"capability_report,omitempty" json:"capability_report,omitempty"`
	RequiredCapabilities []string `yaml:"required_capabilities,omitempty" json:"required_capabilities,omitempty"`
	SubmitTimeout        string   `yaml:"submit_timeout,omitempty" json:"submit_timeout,omitempty"`
	LookupTimeout        string   `yaml:"lookup_timeout,omitempty" json:"lookup_timeout,omitempty"`
	EnvironmentAllowlist []string `yaml:"environment_allowlist,omitempty" json:"environment_allowlist,omitempty"`

	Endpoint          string `yaml:"endpoint,omitempty"             json:"endpoint,omitempty"`
	Auth              *Auth  `yaml:"auth,omitempty"                 json:"auth,omitempty"`
	IdempotencyHeader string `yaml:"idempotency_header,omitempty"   json:"idempotency_header,omitempty"`
}

// Auth is the webhook authentication reference (§5). The secret is a
// reference only; it is never resolved or printed.
type Auth struct {
	Type       string `yaml:"type"        json:"type"`
	SecretRef  string `yaml:"secret_ref"  json:"secret_ref"`
	HeaderName string `yaml:"header_name,omitempty" json:"header_name,omitempty"`
}

// Route binds a source to a dispatch target under a policy (§6).
type Route struct {
	Enabled        bool           `yaml:"enabled"        json:"enabled"`
	Source         Source         `yaml:"source"         json:"source"`
	Batching       Batching       `yaml:"batching"       json:"batching"`
	Policy         Policy         `yaml:"policy"         json:"policy"`
	Dispatch       Dispatch       `yaml:"dispatch"       json:"dispatch"`
	Reconciliation Reconciliation `yaml:"reconciliation" json:"reconciliation"`
	Retention      *Retention     `yaml:"retention,omitempty" json:"retention,omitempty"`
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

// Dispatch names the target and execution envelope (§6, §9).
type Dispatch struct {
	Target           string         `yaml:"target"           json:"target"`
	Profile          string         `yaml:"profile"          json:"profile"`
	Skills           []string       `yaml:"skills"           json:"skills"`
	MutexKey         string         `yaml:"mutex_key"        json:"mutex_key"`
	LatestState      bool           `yaml:"latest_state"     json:"latest_state"`
	SubmissionRetry  Retry          `yaml:"submission_retry" json:"submission_retry"`
	ExecutionHints   ExecutionHints `yaml:"execution_hints"  json:"execution_hints"`
	FailureBudget    int            `yaml:"failure_budget"   json:"failure_budget"`
	ActiveStaleAfter string         `yaml:"active_stale_after" json:"active_stale_after"`
}

// Retry is the delivery-only submission retry policy (§9).
type Retry struct {
	MaxAttempts    int     `yaml:"max_attempts"     json:"max_attempts"`
	InitialBackoff string  `yaml:"initial_backoff"  json:"initial_backoff"`
	MaxBackoff     string  `yaml:"max_backoff"      json:"max_backoff"`
	Multiplier     float64 `yaml:"multiplier"       json:"multiplier"`
	JitterFraction float64 `yaml:"jitter_fraction"  json:"jitter_fraction"`
}

// ExecutionHints advise the target about execution bounds (§9).
type ExecutionHints struct {
	MaxRuntime  string `yaml:"max_runtime"   json:"max_runtime"`
	MaxAttempts int    `yaml:"max_attempts"  json:"max_attempts"`
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

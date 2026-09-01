package config

import (
	"strings"
	"testing"
	"time"
)

// e16t1BaseConfig returns a minimal valid v0.1.5-shaped configuration
// whose wiki route carries one log sink; drain variants mutate the
// notifications block.
func e16t1BaseConfig() *Config {
	return &Config{
		Version:  1,
		Instance: Instance{ID: "test"},
		Resources: map[string]Resource{
			"vault-main": {Type: "directory", Root: "/srv/vault", FileScope: "markdown"},
		},
		HermesTargets: map[string]HermesTarget{
			"hermes-main": {Board: "agent-dispatch", Executable: "hermes", MinimumVersion: MinimumEligibleHermesVersion, Compatibility: "capability_probe"},
		},
		Routes: map[string]Route{
			"wiki": {
				Enabled:  false,
				Source:   Source{Type: "watchman-trigger", SourceID: "vault-watch", Resource: "vault-main", TriggerName: "t", Include: []string{"**/*.md"}, Exclude: []string{".git/**"}},
				Batching: Batching{AutomaticThreshold: 25, HardLimit: 100, MaxManifestBytes: 262144},
				Policy: Policy{Protected: []string{"raw/**"}, Immutable: []string{}, BulkAction: "quarantine", OverflowAction: "reconcile",
					FreshInstanceAction: "reconcile", UnsafePathAction: "quarantine"},
				FanoutMode:       "all",
				Destinations:     []Destination{{ID: "indexing", Target: "hermes-main", Profile: "wiki-maintainer", Skills: []string{"llm-wiki"}, Workstream: "indexing", ExecutionHints: ExecutionHints{MaxRuntime: "30m", MaxAttempts: 2}}},
				Notifications:    &Notifications{Sinks: []NotificationSink{{ID: "ops-log", Type: "log"}}},
				SubmissionRetry:  Retry{MaxAttempts: 3, InitialBackoff: "2s", MaxBackoff: "2m", Multiplier: 2, JitterFraction: 0.2},
				FailureBudget:    2,
				LatestState:      true,
				ActiveStaleAfter: "2h",
			},
		},
	}
}

// TestE16T1OmittedDrainResolvesManualDefaults pins the v0.1.5
// compatibility acceptance: an omitted block means manual with the
// documented effective defaults.
func TestE16T1OmittedDrainResolvesManualDefaults(t *testing.T) {
	cfg := e16t1BaseConfig()
	if errs, _ := SemanticValidate(cfg); len(errs) != 0 {
		t.Fatalf("v0.1.5-shaped config must stay valid: %v", errs)
	}
	policy, err := EffectiveNotificationDrain(cfg.Routes["wiki"].Notifications)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Mode != DrainModeManual || policy.Limit != 100 || policy.FailurePolicy != "preserve-pending" ||
		policy.PendingWarnAfter != time.Hour || policy.InitialBackoff != 30*time.Second ||
		policy.MaxBackoff != 15*time.Minute || policy.Multiplier != 2.0 || policy.JitterFraction != 0.2 {
		t.Fatalf("omitted drain must resolve to the documented defaults: %+v", policy)
	}
	// A nil notifications block resolves identically.
	policy, err = EffectiveNotificationDrain(nil)
	if err != nil || policy.Mode != DrainModeManual {
		t.Fatalf("nil block: %+v %v", policy, err)
	}
}

// TestE16T1DrainRevisionCoverage pins the acceptance that every
// behavior-affecting policy field participates in a documented revision:
// flipping each declared field changes the drain-policy revision.
func TestE16T1DrainRevisionCoverage(t *testing.T) {
	base := &NotificationDrain{
		Mode:             DrainModeAfterCommand,
		Limit:            100,
		FailurePolicy:    "preserve-pending",
		PendingWarnAfter: "1h",
		Retry:            &DrainRetry{InitialBackoff: "30s", MaxBackoff: "15m", Multiplier: 2.0, JitterFraction: 0.2},
	}
	baseRevision := NotificationDrainRevision(&Notifications{Drain: base})
	if baseRevision == "" || baseRevision == NotificationDrainRevision(nil) {
		t.Fatalf("a declared block must digest distinctly from an omitted one")
	}
	variants := []*NotificationDrain{
		{Mode: DrainModeScheduled, Limit: base.Limit, FailurePolicy: base.FailurePolicy, PendingWarnAfter: base.PendingWarnAfter, Retry: base.Retry},
		{Mode: base.Mode, Limit: 50, FailurePolicy: base.FailurePolicy, PendingWarnAfter: base.PendingWarnAfter, Retry: base.Retry},
		{Mode: base.Mode, Limit: base.Limit, FailurePolicy: base.FailurePolicy, PendingWarnAfter: "2h", Retry: base.Retry},
		{Mode: base.Mode, Limit: base.Limit, FailurePolicy: base.FailurePolicy, PendingWarnAfter: base.PendingWarnAfter,
			Retry: &DrainRetry{InitialBackoff: "45s", MaxBackoff: "15m", Multiplier: 2.0, JitterFraction: 0.2}},
		{Mode: base.Mode, Limit: base.Limit, FailurePolicy: base.FailurePolicy, PendingWarnAfter: base.PendingWarnAfter,
			Retry: &DrainRetry{InitialBackoff: "30s", MaxBackoff: "20m", Multiplier: 2.0, JitterFraction: 0.2}},
		{Mode: base.Mode, Limit: base.Limit, FailurePolicy: base.FailurePolicy, PendingWarnAfter: base.PendingWarnAfter,
			Retry: &DrainRetry{InitialBackoff: "30s", MaxBackoff: "15m", Multiplier: 2.5, JitterFraction: 0.2}},
		{Mode: base.Mode, Limit: base.Limit, FailurePolicy: base.FailurePolicy, PendingWarnAfter: base.PendingWarnAfter,
			Retry: &DrainRetry{InitialBackoff: "30s", MaxBackoff: "15m", Multiplier: 2.0, JitterFraction: 0.1}},
	}
	for i, v := range variants {
		if got := NotificationDrainRevision(&Notifications{Drain: v}); got == baseRevision {
			t.Fatalf("variant %d must change the drain-policy revision", i)
		}
	}
	// Equivalent durations still digest distinctly from the defaults and
	// identical declarations are stable.
	if NotificationDrainRevision(&Notifications{Drain: base}) != baseRevision {
		t.Fatalf("the drain revision must be deterministic")
	}
}

// TestE16T1DrainRevisionPartitions pins the v0.1.6 §5 partition: drain
// behavior joins the route revision but never the notification-policy
// revision, and an omitted block keeps the exact v0.1.5 route revision.
func TestE16T1DrainRevisionPartitions(t *testing.T) {
	cfg := e16t1BaseConfig()
	routeRevisionV015, ok := RouteRevision(cfg, "wiki")
	if !ok {
		t.Fatal("route revision")
	}
	policyRevisionV015 := NotificationPolicyRevision(cfg.Routes["wiki"])

	// Omission keeps the v0.1.5 route revision byte-for-byte.
	cfg.Routes["wiki"].Notifications.Drain = &NotificationDrain{}
	if got, _ := RouteRevision(cfg, "wiki"); got != routeRevisionV015 {
		t.Fatalf("an explicitly-empty drain block projects the manual default and must not change the route revision")
	}

	// A declared block changes the route revision...
	cfg.Routes["wiki"].Notifications.Drain = &NotificationDrain{Mode: DrainModeAfterCommand}
	if got, _ := RouteRevision(cfg, "wiki"); got == routeRevisionV015 {
		t.Fatalf("a declared drain block must join the route revision")
	}
	// ...but never the notification-policy revision.
	if got := NotificationPolicyRevision(cfg.Routes["wiki"]); got != policyRevisionV015 {
		t.Fatalf("drain must not join the notification-policy revision")
	}
}

// TestE16T1DrainValidation rejects every present-but-invalid field.
func TestE16T1DrainValidation(t *testing.T) {
	cases := map[string]*NotificationDrain{
		"mode":             {Mode: "always"},
		"limit-high":       {Limit: 501},
		"failure_policy":   {FailurePolicy: "drop"},
		"warn_after":       {PendingWarnAfter: "0s"},
		"initial_backoff":  {Retry: &DrainRetry{InitialBackoff: "soon", MaxBackoff: "15m", Multiplier: 2, JitterFraction: 0.2}},
		"backoff_inverted": {Retry: &DrainRetry{InitialBackoff: "20m", MaxBackoff: "15m", Multiplier: 2, JitterFraction: 0.2}},
		"multiplier":       {Retry: &DrainRetry{InitialBackoff: "30s", MaxBackoff: "15m", Multiplier: 0.5, JitterFraction: 0.2}},
		"jitter":           {Retry: &DrainRetry{InitialBackoff: "30s", MaxBackoff: "15m", Multiplier: 2, JitterFraction: 0.6}},
	}
	for name, drain := range cases {
		cfg := e16t1BaseConfig()
		cfg.Routes["wiki"].Notifications.Drain = drain
		errs, _ := SemanticValidate(cfg)
		if len(errs) == 0 {
			t.Fatalf("%s must fail semantic validation", name)
		}
	}
}

// TestE16T1DrainRoundTrip covers the schema/example/round-trip
// deliverable: a full drain block parses, validates, marshals, and
// reloads identically.
func TestE16T1DrainRoundTrip(t *testing.T) {
	cfg := e16t1BaseConfig()
	cfg.Routes["wiki"].Notifications.Drain = &NotificationDrain{
		Mode:             DrainModeAfterCommand,
		Limit:            100,
		FailurePolicy:    "preserve-pending",
		PendingWarnAfter: "1h",
		Retry:            &DrainRetry{InitialBackoff: "30s", MaxBackoff: "15m", Multiplier: 2.0, JitterFraction: 0.2},
	}
	if err := SchemaValidate(cfg); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if errs, _ := SemanticValidate(cfg); len(errs) != 0 {
		t.Fatalf("semantic: %v", errs)
	}
	data, err := MarshalYAML(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "after-command") {
		t.Fatalf("the drain block must serialize: %s", data)
	}
	reloaded, err := Parse(data)
	if err != nil {
		t.Fatalf("round-trip parse: %v", err)
	}
	got := reloaded.Routes["wiki"].Notifications.Drain
	if got == nil || got.Mode != DrainModeAfterCommand || got.Limit != 100 || got.FailurePolicy != "preserve-pending" ||
		got.PendingWarnAfter != "1h" || got.Retry == nil || got.Retry.InitialBackoff != "30s" ||
		got.Retry.MaxBackoff != "15m" || got.Retry.Multiplier != 2.0 || got.Retry.JitterFraction != 0.2 {
		t.Fatalf("round-trip drift: %+v", got)
	}
}

// TestE16T1ExampleConfigDrainsAfterCommand pins the generated Wiki
// default: newly generated configuration uses after-command.
func TestE16T1ExampleConfigDrainsAfterCommand(t *testing.T) {
	cfg := Example("test-instance", "/srv/vault")
	drain := cfg.Routes["wiki-maintenance"].Notifications.Drain
	if drain == nil || drain.Mode != DrainModeAfterCommand {
		t.Fatalf("generated configuration must default to after-command: %+v", drain)
	}
	policy, err := EffectiveNotificationDrain(cfg.Routes["wiki-maintenance"].Notifications)
	if err != nil || policy.Mode != DrainModeAfterCommand || policy.Limit != 100 {
		t.Fatalf("effective generated policy: %+v %v", policy, err)
	}
}

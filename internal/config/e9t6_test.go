package config

import (
	"strings"
	"testing"
)

// e9t6WebhookYAML extends the minimal fixture with a second route bound
// to the webhook target, so the auth-only target surface participates in
// revision assertions.
func e9t6WebhookYAML(t *testing.T) []byte {
	t.Helper()
	return []byte(strings.Replace(string(minimalYAML(t)),
		"    reconciliation:\n      initial: true\n      daily_expected: true\n",
		"    reconciliation:\n      initial: true\n      daily_expected: true\n"+
			"  r2:\n    enabled: false\n    source:\n      type: watchman-trigger\n      source_id: vault-main-watchman\n      resource: vault-main\n      trigger_name: agent-dispatch.r2.def456\n      include:\n        - \"**/*.md\"\n      exclude:\n        - \".git/**\"\n    batching:\n      automatic_threshold: 25\n      hard_limit: 100\n      max_manifest_bytes: 262144\n    policy:\n      protected: []\n      immutable: []\n      bulk_action: quarantine\n      overflow_action: reconcile\n      fresh_instance_action: reconcile\n      unsafe_path_action: quarantine\n    dispatch:\n      target: hermes-webhook-immediate\n      profile: wiki-maintainer\n      skills:\n        - llm-wiki\n      mutex_key: wiki-publish\n      latest_state: true\n      submission_retry:\n        max_attempts: 3\n        initial_backoff: 2s\n        max_backoff: 2m\n        multiplier: 2.0\n        jitter_fraction: 0.2\n      execution_hints:\n        max_runtime: 30m\n        max_attempts: 2\n      failure_budget: 2\n      active_stale_after: 2h\n    reconciliation:\n      initial: false\n      daily_expected: false\n", 1))
}

// TestE9T6RevisionCoversWebhookDeliveryEvidence proves D-023 F1: the
// authentication type, the secret reference, the auth header name, the
// idempotency header, the lookup bound, and the capability-report path
// are behavior-affecting delivery evidence — changing any of them must
// move the computed route revision, so an acknowledged route pauses
// until the new behavior is re-acknowledged.
func TestE9T6RevisionCoversWebhookDeliveryEvidence(t *testing.T) {
	cfg, err := Parse(e9t6WebhookYAML(t))
	if err != nil {
		t.Fatal(err)
	}
	firstWebhook, ok := RouteRevision(cfg, "r2")
	if !ok {
		t.Fatal("webhook route missing")
	}
	firstKanban, ok := RouteRevision(cfg, "r1")
	if !ok {
		t.Fatal("kanban route missing")
	}
	for name, changed := range map[string]string{
		"auth shape to header": strings.Replace(string(e9t6WebhookYAML(t)), "type: bearer\n      secret_ref: env:TEST_TOKEN", "type: header\n      secret_ref: env:TEST_TOKEN\n      header_name: X-Token", 1),
		"secret reference":     strings.Replace(string(e9t6WebhookYAML(t)), "secret_ref: env:TEST_TOKEN", "secret_ref: env:OTHER_TOKEN", 1),
		"idempotency header":   strings.Replace(string(e9t6WebhookYAML(t)), "endpoint: https://example.invalid/hook", "endpoint: https://example.invalid/hook\n    idempotency_header: X-Dedup-Key", 1),
		"lookup timeout":       strings.Replace(string(minimalYAML(t)), "capability_report: /etc/agent-dispatch/caps.json", "capability_report: /etc/agent-dispatch/caps.json\n    lookup_timeout: 45s", 1),
		"capability report":    strings.Replace(string(minimalYAML(t)), "capability_report: /etc/agent-dispatch/caps.json", "capability_report: /etc/agent-dispatch/other-caps.json", 1),
	} {
		cfgC, err := Parse([]byte(changed))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		routeID := "r2"
		if strings.Contains(name, "timeout") || strings.Contains(name, "capability") {
			routeID = "r1"
		}
		first := firstWebhook
		if routeID == "r1" {
			first = firstKanban
		}
		if rev, _ := RouteRevision(cfgC, routeID); rev == first {
			t.Errorf("changing the %s must change the route revision", name)
		}
	}

	// The secret VALUE exclusion is enforced structurally: Config
	// carries only the reference (types.go Auth), so no resolved secret
	// can reach the projection (SEC-006); the reference identity is
	// pinned above.
}

// TestE9T6RevisionCoversReconciliation proves the explicit disposition:
// the reconciliation flags change which arrivals produce work (the
// initial sweep and the daily-expected window), so they are
// behavior-affecting and must move the revision.
func TestE9T6RevisionCoversReconciliation(t *testing.T) {
	cfg, err := Parse(minimalYAML(t))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := RouteRevision(cfg, "r1")
	for name, changed := range map[string]string{
		"initial flag":        strings.Replace(string(minimalYAML(t)), "initial: true", "initial: false", 1),
		"daily expected flag": strings.Replace(string(minimalYAML(t)), "daily_expected: true", "daily_expected: false", 1),
	} {
		cfgC, err := Parse([]byte(changed))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if rev, _ := RouteRevision(cfgC, "r1"); rev == first {
			t.Errorf("changing the %s must change the route revision", name)
		}
	}
}

// TestE9T6AuthProjectionNilEquivalence pins the projection's nil
// handling: an absent auth block and an explicitly empty one must hash
// identically, so only a real authentication change moves the revision.
func TestE9T6AuthProjectionNilEquivalence(t *testing.T) {
	nilAuth := authProjection(nil)
	emptyAuth := authProjection(&Auth{})
	if nilAuth["type"] != emptyAuth["type"] || nilAuth["secret_ref"] != emptyAuth["secret_ref"] || nilAuth["header_name"] != emptyAuth["header_name"] {
		t.Fatalf("nil and empty auth must project identically: %v vs %v", nilAuth, emptyAuth)
	}
}

// TestE9T6RetentionStaysOutOfRevision pins the exclusion half of the
// disposition: the retention block bounds record pruning (OPS-003) and
// never changes what a dispatch submits or how a plan is classified, so
// a retention edit must NOT pause an acknowledged route.
func TestE9T6RetentionStaysOutOfRevision(t *testing.T) {
	cfg, err := Parse(minimalYAML(t))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := RouteRevision(cfg, "r1")
	changed := strings.Replace(string(minimalYAML(t)),
		"    reconciliation:\n      initial: true\n      daily_expected: true\n",
		"    reconciliation:\n      initial: true\n      daily_expected: true\n    retention:\n      observations: 7d\n      unresolved: forever\n", 1)
	cfgC, err := Parse([]byte(changed))
	if err != nil {
		t.Fatal(err)
	}
	if rev, _ := RouteRevision(cfgC, "r1"); rev != first {
		t.Error("a retention edit must not change the route revision (pruning bounds are not submission behavior)")
	}
}

package config

import (
	"strings"
	"testing"
)

// TestE9T3PolicyRevisionIndependentAndSensitive proves L-18's digest
// shape: deterministic, prefixed, stable under display reordering,
// moved by policy content and batching thresholds, and unchanged by
// non-policy route fields.
func TestE9T3PolicyRevisionIndependentAndSensitive(t *testing.T) {
	cfg, err := Parse(minimalYAML(t))
	if err != nil {
		t.Fatal(err)
	}
	first := PolicyRevision(cfg.Routes["r1"])
	if again := PolicyRevision(cfg.Routes["r1"]); first != again {
		t.Fatalf("policy revision must be deterministic: %q vs %q", first, again)
	}
	if !strings.HasPrefix(first, "pol-") || len(first) != len("pol-")+24 {
		t.Fatalf("policy revision must be a prefixed 12-byte digest, got %q", first)
	}

	// Pattern display order is neutralized like the route revision.
	reordered := strings.Replace(string(minimalYAML(t)), "include:\n      - \"docs/*.md\"\n      - \"**/*.md\"\n", "include:\n      - \"**/*.md\"\n      - \"docs/*.md\"\n", 1)
	cfg2, err := Parse([]byte(reordered))
	if err != nil {
		t.Fatal(err)
	}
	if PolicyRevision(cfg2.Routes["r1"]) != first {
		t.Error("pattern display order must not change the policy digest")
	}

	// Policy content and planner thresholds move it. (overflow_action is
	// schema-pinned to reconcile in v0.1, so it has no second value to
	// test; the shared projection still covers it.)
	for name, changed := range map[string]string{
		"protected pattern":  strings.Replace(string(minimalYAML(t)), "- \"raw/**\"", "- \"raw/**\"\n        - \"tmp/**\"", 1),
		"bulk action":        strings.Replace(string(minimalYAML(t)), "bulk_action: quarantine", "bulk_action: reconcile", 1),
		"batching threshold": strings.Replace(string(minimalYAML(t)), "hard_limit: 100", "hard_limit: 101", 1),
	} {
		cfgC, err := Parse([]byte(changed))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if PolicyRevision(cfgC.Routes["r1"]) == first {
			t.Errorf("changing %s must change the policy digest", name)
		}
	}

	// Non-policy route fields stay out: the dispatch envelope cannot
	// change a decision, so it must not move the digest.
	profile := strings.Replace(string(minimalYAML(t)), "profile: wiki-maintainer", "profile: other-maintainer", 1)
	profile = strings.Replace(profile, "mutex_key: wiki-publish", "mutex_key: other-publish", 1)
	cfg3, err := Parse([]byte(profile))
	if err != nil {
		t.Fatal(err)
	}
	if PolicyRevision(cfg3.Routes["r1"]) != first {
		t.Error("dispatch-envelope fields must not change the policy digest")
	}
}

// TestE9T3RevisionCoversTransport proves T3-F006: swapping the target
// binary, its submit bound, its environment allowlist, or the route's
// manifest byte bound is behavior-affecting and must move the route's
// behavior digest — a route acknowledged under one transport never
// submits under another without re-acknowledgment.
func TestE9T3RevisionCoversTransport(t *testing.T) {
	cfg, err := Parse(minimalYAML(t))
	if err != nil {
		t.Fatal(err)
	}
	first, ok := RouteRevision(cfg, "r1")
	if !ok {
		t.Fatal("route missing")
	}
	for name, changed := range map[string]string{
		"executable":            strings.Replace(string(minimalYAML(t)), "executable: hermes\n", "executable: hermes2\n", 1),
		"submit timeout":        strings.Replace(string(minimalYAML(t)), "executable: hermes\n", "executable: hermes\n    submit_timeout: 45s\n", 1),
		"environment allowlist": strings.Replace(string(minimalYAML(t)), "executable: hermes\n", "executable: hermes\n    environment_allowlist:\n      - LANG\n", 1),
		"manifest byte bound":   strings.Replace(string(minimalYAML(t)), "max_manifest_bytes: 262144", "max_manifest_bytes: 262145", 1),
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

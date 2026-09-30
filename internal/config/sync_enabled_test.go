package config

import (
	"strings"
	"testing"
)

func TestSyncEnabledRawInput(t *testing.T) {
	example := string(goldenExample(t))
	const header = "sync:\n  enabled: false\n"
	if !strings.Contains(example, header) {
		t.Fatal("example is missing the sync.enabled fixture")
	}
	withHeader := func(headerValue string) string {
		return strings.Replace(example, header, headerValue, 1)
	}
	type testCase struct {
		name    string
		input   string
		valid   bool
		enabled bool
	}
	cases := []testCase{
		{name: "missing", input: withHeader("sync:\n")},
		{name: "empty object", input: string(minimalYAML(t)) + "sync: {}\n"},
		{name: "null", input: withHeader("sync:\n  enabled: null\n")},
		{name: "empty", input: withHeader("sync:\n  enabled:\n")},
		{name: "tilde", input: withHeader("sync:\n  enabled: ~\n")},
		{name: "string false", input: withHeader("sync:\n  enabled: \"false\"\n")},
		{name: "string true", input: withHeader("sync:\n  enabled: \"true\"\n")},
		{name: "legacy no", input: withHeader("sync:\n  enabled: no\n")},
		{name: "legacy yes", input: withHeader("sync:\n  enabled: yes\n")},
		{name: "integer", input: withHeader("sync:\n  enabled: 0\n")},
		{name: "false", input: example, valid: true},
		{name: "true", input: withHeader("sync:\n  enabled: true\n"), valid: true, enabled: true},
		{name: "sync absent", input: string(minimalYAML(t)), valid: true},
		{name: "sync null", input: string(minimalYAML(t)) + "sync: null\n", valid: true},
		{name: "merge false", input: withHeader("sync:\n  <<: {enabled: false}\n"), valid: true},
		{name: "merge true", input: withHeader("sync:\n  <<: {enabled: true}\n"), valid: true, enabled: true},
		{name: "merge missing", input: withHeader("sync:\n  <<: {group_id: wiki-pair}\n")},
		{name: "merge null", input: withHeader("sync:\n  <<: {enabled: null}\n")},
		{name: "merge string", input: withHeader("sync:\n  <<: {enabled: \"false\"}\n")},
		{name: "null overrides merge", input: withHeader("sync:\n  <<: {enabled: true}\n  enabled: null\n")},
		{name: "false overrides merge", input: withHeader("sync:\n  <<: {enabled: true}\n  enabled: false\n"), valid: true},
		{name: "false overrides null merge", input: withHeader("sync:\n  <<: {enabled: null}\n  enabled: false\n"), valid: true},
		{name: "merge sequence first wins", input: withHeader("sync:\n  <<: [{enabled: false}, {enabled: true}]\n"), valid: true},
		{name: "merge sequence null first", input: withHeader("sync:\n  <<: [{enabled: null}, {enabled: true}]\n")},
		{name: "boolean alias", input: withHeader("sync:\n  <<: {enabled: &flag true}\n  enabled: *flag\n"), valid: true, enabled: true},
		{name: "false alias", input: withHeader("sync:\n  <<: {enabled: &flag false}\n  enabled: *flag\n"), valid: true},
		{name: "null alias", input: withHeader("sync:\n  <<: {enabled: &flag null}\n  enabled: *flag\n")},
		{name: "string alias", input: withHeader("sync:\n  <<: {enabled: &flag \"false\"}\n  enabled: *flag\n")},
	}
	// Inherited sync blocks and aliases must obey the same gate as direct blocks.
	for _, enabled := range []struct {
		name  string
		value string
		valid bool
	}{{"false", "false", true}, {"true", "true", true}, {"missing", "", false}, {"null", "null", false}} {
		headerValue := "sync: &sync_config\n"
		if enabled.value != "" {
			headerValue += "  enabled: " + enabled.value + "\n"
		}
		merged := "<<: &defaults\n" + strings.TrimRight("  "+strings.ReplaceAll(withHeader(headerValue), "\n", "\n  "), " ")
		cases = append(cases,
			testCase{name: "root merge " + enabled.name, input: merged, valid: enabled.valid, enabled: enabled.value == "true"},
			testCase{name: "sync alias " + enabled.name, input: merged + "sync: *sync_config\n", valid: enabled.valid, enabled: enabled.value == "true"},
		)
	}
	for _, parser := range []struct {
		name  string
		parse func([]byte) (*Config, error)
	}{{"Parse", Parse}, {"ParseDecoded", ParseDecoded}} {
		t.Run(parser.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					cfg, err := parser.parse([]byte(tc.input))
					if !tc.valid {
						if err == nil || !strings.Contains(err.Error(), "enabled") {
							t.Fatalf("invalid sync.enabled must fail with a field error: %v", err)
						}
						return
					}
					if err != nil {
						t.Fatalf("valid input must load: %v", err)
					}
					if tc.name == "sync absent" || tc.name == "sync null" {
						if cfg.Sync != nil {
							t.Fatal("absent sync must remain absent")
						}
					} else if cfg.Sync == nil || cfg.Sync.Enabled != tc.enabled {
						t.Fatalf("sync.enabled changed: got %+v, want %t", cfg.Sync, tc.enabled)
					}
				})
			}
		})
	}
}

func TestParseDecodedSyncEnabledRepairFloor(t *testing.T) {
	example := strings.Replace(string(goldenExample(t)), "minimum_version: 0.20.5", "minimum_version: 0.20.4", 1)
	if _, err := Parse([]byte(example)); err == nil || !strings.Contains(err.Error(), "support floor") {
		t.Fatalf("old floor must still require repair: %v", err)
	}
	if _, err := ParseDecoded([]byte(example)); err != nil {
		t.Fatalf("valid sync.enabled must allow floor repair: %v", err)
	}
	for _, enabled := range []string{"", "  enabled: null\n", "  enabled: \"false\"\n"} {
		input := strings.Replace(example, "sync:\n  enabled: false\n", "sync:\n"+enabled, 1)
		if _, err := ParseDecoded([]byte(input)); err == nil || !strings.Contains(err.Error(), "enabled") {
			t.Fatalf("floor repair must reject invalid sync.enabled %q: %v", enabled, err)
		}
	}
}

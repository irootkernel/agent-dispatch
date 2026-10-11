package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestE23T3ManagementRawAuthority(t *testing.T) {
	example := string(goldenExample(t))
	cases := []struct {
		name, block string
		valid       bool
	}{
		{"absent", "", true}, {"disabled", "{enabled: false, actions: []}", true}, {"enabled", "{enabled: true, actions: [sync-now, pause]}", true},
		{"null", "null", false}, {"missing enabled", "{actions: []}", false}, {"null enabled", "{enabled: null, actions: []}", false}, {"string enabled", "{enabled: 'false', actions: []}", false},
		{"missing actions", "{enabled: false}", false}, {"null actions", "{enabled: false, actions: null}", false}, {"empty enabled", "{enabled: true, actions: []}", false},
		{"duplicate actions", "{enabled: false, actions: [pause, pause]}", false}, {"unknown action", "{enabled: false, actions: [publish]}", false},
		{"merged valid", "{<<: {enabled: false, actions: []}}", true}, {"merged null override", "{<<: {enabled: true, actions: [pause]}, enabled: null}", false},
	}
	for _, parser := range []struct {
		name  string
		parse func([]byte) (*Config, error)
	}{{"Parse", Parse}, {"ParseDecoded", ParseDecoded}} {
		t.Run(parser.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					input := example
					if tc.block != "" {
						input = strings.Replace(input, "sync:\n", "sync:\n  management: "+tc.block+"\n", 1)
					}
					_, err := parser.parse([]byte(input))
					if (err == nil) != tc.valid {
						t.Fatalf("valid=%t err=%v", tc.valid, err)
					}
				})
			}
		})
	}
	for _, block := range []string{"{enabled: false, actions: [], extra: 1}", "{enabled: false, enabled: true, actions: [pause]}"} {
		_, err := ParseDecoded([]byte(strings.Replace(example, "sync:\n", "sync:\n  management: "+block+"\n", 1)))
		if err == nil {
			t.Fatalf("strict typed decoding accepted %s", block)
		}
	}
}

func TestE23T3ManagementRevisions(t *testing.T) {
	cfg, err := Parse(goldenExample(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Sync.Management = &SyncManagement{Enabled: true, Actions: []string{"sync-now", "pause"}}
	policy, revision, err := ManagementRevisions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ack, _ := SyncRevision(cfg)
	slices.Reverse(cfg.Sync.Management.Actions)
	slices.Reverse(cfg.Sync.Nodes)
	p, r, err := ManagementRevisions(cfg)
	if err != nil || p != policy || r != revision {
		t.Fatalf("order changed bindings: %s %s %v", p, r, err)
	}
	cfg.Sync.PublisherSigningKeyRef = "env:CHANGED_PUBLISHER_KEY"
	p, r, err = ManagementRevisions(cfg)
	if err != nil || p != policy || r != revision {
		t.Fatal("private signing reference affected management projection")
	}
	cfg.Sync.Nodes[0].Endpoint = "https://rotated.example.ts.net"
	_, r, err = ManagementRevisions(cfg)
	nextAck, _ := SyncRevision(cfg)
	if err != nil || r == revision || nextAck != ack {
		t.Fatal("endpoint must bind management while preserving acknowledgement projection")
	}
	cfg.Sync.Management.Actions = []string{"pause"}
	p, _, err = ManagementRevisions(cfg)
	if err != nil || p == policy {
		t.Fatal("allowlist change did not change policy")
	}
	cfg.Sync.Management = nil
	p, _, err = ManagementRevisions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Sync.Management = &SyncManagement{Enabled: false, Actions: []string{}}
	next, _, err := ManagementRevisions(cfg)
	if err != nil || p != next {
		t.Fatal("absent and explicit disabled-empty policies differ")
	}
}

func TestE23T3EmbeddedManagementSchema(t *testing.T) {
	load := func(path string) map[string]any {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var d map[string]any
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	frozen := load("../../docs/contracts/sync-management-v1/config.schema.json")
	delete(frozen, "$schema")
	delete(frozen, "$id")
	for _, path := range []string{"../../docs/schemas/config.schema.json", "schema/config.schema.json"} {
		d := load(path)
		m := d["properties"].(map[string]any)["sync"].(map[string]any)["properties"].(map[string]any)["management"]
		if !reflect.DeepEqual(m, frozen) {
			t.Fatalf("schema drift: %s", path)
		}
	}
}

func TestE23T3ConfigInspectionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LoadContext(ctx, "does-not-exist"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled config load: %v", err)
	}
	if _, err := LoadContext(context.Background(), t.TempDir()); err == nil {
		t.Fatal("non-regular configuration accepted")
	}
}

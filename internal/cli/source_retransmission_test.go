package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDispatchSourceRetransmissionPreservesLineage(t *testing.T) {
	for _, disposition := range []string{"dispatch", "drop", "quarantine"} {
		t.Run(disposition, func(t *testing.T) {
			configPath, vault := e4t3Fixture(t)
			if disposition == "quarantine" {
				raw, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				raw = bytes.Replace(raw, []byte("protected: []"), []byte("protected: [\"Inbox/**\"]"), 1)
				if err := os.WriteFile(configPath, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			setPlanEnv(t, vault, false)
			e4t3RegisterRoute(t, configPath)
			payload := `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`
			if disposition == "drop" {
				payload = `[{"name":"Inbox/ignored.txt","exists":true,"new":true,"size":5,"type":"f"}]`
			}
			run := func() (int, string, string) {
				var out, errb bytes.Buffer
				var code int
				withStdin(t, payload, func() {
					code = Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
				})
				return code, out.String(), errb.String()
			}
			if code, out, err := run(); code != 0 {
				t.Fatalf("first occurrence: %d out=%s err=%s", code, out, err)
			}
			store := e5t1Store(t, configPath)
			snapshot := func() string {
				counts := map[string]int{}
				for _, table := range []string{"source_observations", "observation_changes", "change_batches", "batch_observations", "policy_decisions", "dispatch_intents", "dispatch_attempts", "dispatch_receipts", "state_transitions", "path_facts", "quarantine_items"} {
					var n int
					if err := store.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
						t.Fatal(err)
					}
					counts[table] = n
				}
				var observation, dirty int64
				var active string
				if err := store.QueryRow(`SELECT observation_revision FROM resources WHERE resource_id='vault-main'`).Scan(&observation); err != nil {
					t.Fatal(err)
				}
				if err := store.QueryRow(`SELECT dirty_generation,COALESCE(active_dispatch_id,'') FROM route_runtime_state WHERE route_id='wiki'`).Scan(&dirty, &active); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal([]any{counts, observation, dirty, active})
				return string(raw)
			}
			before := snapshot()
			for attempt := 0; attempt < 2; attempt++ {
				code, out, err := run()
				if code != 14 || out != "" || !strings.Contains(err, "dispatch_duplicate") || strings.Contains(err, "sqlite_query_failed") {
					t.Fatalf("retransmission: %d out=%s err=%s", code, out, err)
				}
				if after := snapshot(); after != before {
					t.Fatalf("retransmission changed durable lineage: %s -> %s", before, after)
				}
			}
		})
	}
}

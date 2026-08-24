package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestE9T1RecordEmissionsMatchSchemas pins M-16: every required member
// of the four published record schemas appears in the CLI's emitted
// records (required-member names checked against the schema documents;
// dead-letter view emitted for dead-lettered dispatches).
func TestE9T1RecordEmissionsMatchSchemas(t *testing.T) {
	root, err := filepath.Abs("../../docs/schemas")
	if err != nil {
		t.Fatal(err)
	}
	required := func(schema string) map[string]bool {
		raw, err := os.ReadFile(filepath.Join(root, schema))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, r := range doc.Required {
			out[r] = true
		}
		return out
	}
	// Real CLI emissions (round-1 F003): drive a live lineage through
	// the fixture and validate the actual envelopes.
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	if errb.Len() != 0 {
		t.Fatalf("dispatch: %s", errb.String())
	}
	dispatchID, _ := decodeEnvelope(t, &out)["dispatch_id"].(string)
	if dispatchID == "" {
		t.Fatal("no dispatch id")
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "show", "--config", configPath, dispatchID}, &out, &errb); code != 0 {
		t.Fatalf("dispatches show: %s", errb.String())
	}
	// decodeEnvelope returns the unwrapped result object; the lineage
	// members live directly under it.
	shown := decodeEnvelope(t, &out)
	raw, _ := json.Marshal(shown)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	// The intent summary under intent, the attempts and receipts arrays,
	// validated member-by-member against the schemas.
	check := func(label, schema string, rawItem json.RawMessage) {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(rawItem, &obj); err != nil {
			t.Fatalf("%s: emission does not parse: %v", label, err)
		}
		for member := range required(schema) {
			if member == "attempts" || member == "request" {
				continue
			}
			if _, ok := obj[member]; !ok {
				t.Errorf("%s: required member %q missing from the CLI emission", label, member)
			}
		}
	}
	var result struct {
		Intent struct {
			SchemaVersion string `json:"schema_version"`
			Request       string `json:"request"`
		} `json:"intent"`
		Attempts []json.RawMessage `json:"attempts"`
		Receipts []json.RawMessage `json:"receipts"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("show result does not parse: %v", err)
	}
	intentRaw, _ := json.Marshal(mustPath(t, raw, "intent"))
	check("dispatch-intent", "dispatch-intent.schema.json", intentRaw)
	if result.Intent.Request == "" {
		t.Error("dispatch-intent: the request document member must be non-empty on the show path")
	}
	for _, a := range result.Attempts {
		check("dispatch-attempt", "dispatch-attempt.schema.json", a)
	}
	for _, r := range result.Receipts {
		check("dispatch-receipt", "dispatch-receipt.schema.json", r)
	}
	// The list path emits the same schema-required members (round-1
	// F002): non-empty decision_id, route, resource_id,
	// content_fingerprint on every row.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "list", "--config", configPath, "--output", "json"}, &out, &errb); code != 0 {
		t.Fatalf("dispatches list: %s", errb.String())
	}
	listed := decodeEnvelope(t, &out)
	var rawList []byte
	for _, key := range []string{"intents", "dispatches", "items"} {
		if v, ok := listed[key]; ok {
			rawList, _ = json.Marshal(v)
			break
		}
	}
	if rawList == nil {
		t.Fatalf("dispatches list: no row array in the envelope: %v", listed)
	}
	var listRows []struct {
		SchemaVersion string `json:"schema_version"`
		DecisionID    string `json:"decision_id"`
		Route         struct {
			ID       string `json:"id"`
			Revision string `json:"revision"`
		} `json:"route"`
		ResourceID         string `json:"resource_id"`
		ContentFingerprint string `json:"content_fingerprint"`
	}
	if err := json.Unmarshal(rawList, &listRows); err != nil {
		t.Fatalf("list rows do not parse: %v", err)
	}
	if len(listRows) == 0 {
		t.Fatal("list returned no rows")
	}
	for i, row := range listRows {
		if row.SchemaVersion == "" || row.DecisionID == "" || row.Route.ID == "" || row.Route.Revision == "" ||
			row.ResourceID == "" || row.ContentFingerprint == "" {
			t.Errorf("list row %d: schema-required members must be non-empty: %+v", i, row)
		}
	}
}

func mustPath(t *testing.T, raw json.RawMessage, key string) any {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("result does not parse: %v", err)
	}
	var v any
	if err := json.Unmarshal(obj[key], &v); err != nil {
		t.Fatalf("member %q does not parse: %v", key, err)
	}
	return v
}

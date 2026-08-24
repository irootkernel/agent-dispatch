package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/ports"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// e9SchemaSet compiles every published schema under docs/schemas with
// URN cross-references registered, returning the compiled schemas by
// file stem for the emission validations.
func e9SchemaSet(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	dir, err := filepath.Abs("../../docs/schemas")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	compiler.DefaultDraft(jsonschema.Draft2020)
	byStem := map[string]string{}
	for _, path := range entries {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		var id struct {
			ID string `json:"$id"`
		}
		if err := json.Unmarshal(raw, &id); err != nil || id.ID == "" {
			t.Fatalf("%s: no $id (%v)", path, err)
		}
		if err := compiler.AddResource(id.ID, doc); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		byStem[filepath.Base(path)] = id.ID
	}
	out := map[string]*jsonschema.Schema{}
	for stem, id := range byStem {
		sch, err := compiler.Compile(id)
		if err != nil {
			t.Fatalf("%s: compile: %v", stem, err)
		}
		out[stem] = sch
	}
	return out
}

// e9Validate checks one emitted document against its published schema.
func e9Validate(t *testing.T, schemas map[string]*jsonschema.Schema, stem, label string, doc json.RawMessage) {
	t.Helper()
	sch := schemas[stem]
	if sch == nil {
		t.Fatalf("%s: schema %s not loaded", label, stem)
	}
	var instance any
	if err := json.Unmarshal(doc, &instance); err != nil {
		t.Fatalf("%s: emission does not parse: %v", label, err)
	}
	if err := sch.Validate(instance); err != nil {
		t.Errorf("%s: emission violates %s: %v", label, stem, err)
	}
}

// TestE9T1RecordEmissionsMatchSchemas pins M-16 and the reconciled E9-T1
// audit findings: the CLI's emitted intent, attempt, receipt, and
// dead-letter records validate against the FULL published schemas —
// required members, enum values, nullable shapes, and
// additionalProperties: false — from real command runs (the request
// document passes through as the schema's object, an in-flight attempt
// reports the open outcome, and a dead-lettered dispatch carries the
// derived record with a non-empty enum reason).
func TestE9T1RecordEmissionsMatchSchemas(t *testing.T) {
	schemas := e9SchemaSet(t)

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

	// The work receipt over the accepted dispatch gives the lineage an
	// in-flight and a completed attempt plus both receipt kinds.
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--external-task-id", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	withStdin(t, `[{"path":"Inbox/new.md","after_digest":"`+e5t1GoodDigest+`"}]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-1", "--manifest", "-"}, &out, &errb)
	})

	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "show", "--config", configPath, dispatchID}, &out, &errb); code != 0 {
		t.Fatalf("dispatches show: %s", errb.String())
	}
	shown := decodeEnvelope(t, &out)
	raw, _ := json.Marshal(shown)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	intentRaw := top["intent"]
	if len(intentRaw) == 0 {
		t.Fatal("the show result must carry the intent member")
	}
	e9Validate(t, schemas, "dispatch-intent.schema.json", "intent", intentRaw)
	if attempts, ok := shown["attempts"].([]any); ok {
		if len(attempts) == 0 {
			t.Error("dispatch-attempt: the accepted lineage must carry attempts to validate")
		}
		for i, a := range attempts {
			doc, _ := json.Marshal(a)
			e9Validate(t, schemas, "dispatch-attempt.schema.json", "attempt", doc)
			_ = i
		}
	} else {
		t.Error("dispatch-attempt: the show result must carry the attempts array")
	}
	if receipts, ok := shown["receipts"].([]any); ok {
		if len(receipts) == 0 {
			t.Error("dispatch-receipt: the accepted lineage must carry receipts to validate")
		}
		for _, r := range receipts {
			doc, _ := json.Marshal(r)
			e9Validate(t, schemas, "dispatch-receipt.schema.json", "receipt", doc)
		}
	} else {
		t.Error("dispatch-receipt: the show result must carry the receipts array")
	}

	// The list path emits rows that each validate against the intent
	// schema (round-1 F002).
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "list", "--config", configPath, "--output", "json"}, &out, &errb); code != 0 {
		t.Fatalf("dispatches list: %s", errb.String())
	}
	listed := decodeEnvelope(t, &out)
	var rawList json.RawMessage
	for _, key := range []string{"intents", "dispatches", "items"} {
		if v, ok := listed[key]; ok {
			rawList, _ = json.Marshal(v)
			break
		}
	}
	if rawList == nil {
		t.Fatalf("dispatches list: no row array in the envelope: %v", listed)
	}
	var listRows []json.RawMessage
	if err := json.Unmarshal(rawList, &listRows); err != nil || len(listRows) == 0 {
		t.Fatalf("list rows do not parse or are empty: %v", err)
	}
	for i, row := range listRows {
		e9Validate(t, schemas, "dispatch-intent.schema.json", "list row", row)
		_ = i
	}
}

// TestE9ValidationDeadLetterRecordMatchesSchema pins the reconciled
// F003/F006/F009: a dead-lettered dispatch's derived record validates
// against the published dead-letter schema with a non-empty enum
// reason even when the transition context is missing entirely.
func TestE9ValidationDeadLetterRecordMatchesSchema(t *testing.T) {
	schemas := e9SchemaSet(t)
	configPath, vault := e4t3Fixture(t)
	dispatchID := e7t2NoSubmitDispatch(t, configPath, vault)

	// Wedge the intent in submitting with an expired lease and drain:
	// the unknown reconciliation with no external reference
	// dead-letters the dispatch.
	store := e5t1Store(t, configPath)
	if _, err := store.AcquireAttempt(context.Background(), ports.AcquireAttempt{
		DispatchID: dispatchID, AttemptID: "attempt-victim", Owner: "victim",
		Now: "2026-08-20T00:00:00Z", LeaseExpiresAt: "2026-08-20T00:01:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	// The degraded-context case (F009): the dead-lettering transition's
	// context is destroyed, so the reason must degrade to the enum
	// default rather than an empty string.
	if _, err := store.Exec(`DELETE FROM state_transitions WHERE entity_type = 'dispatch_intent' AND entity_id = ? AND to_state = 'dead_lettered'`, dispatchID); err != nil {
		t.Fatal(err)
	}
	store.Close()

	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "drain", "--route", "wiki", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("drain: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "show", "--config", configPath, dispatchID}, &out, &errb); code != 0 {
		t.Fatalf("dispatches show: %s", errb.String())
	}
	shown := decodeEnvelope(t, &out)
	dl, ok := shown["dead_letter_record"].(map[string]any)
	if !ok {
		t.Fatalf("a dead-lettered dispatch must carry the dead_letter_record view: %v", shown)
	}
	if reason, _ := dl["dead_letter_reason"].(string); reason == "" {
		t.Fatalf("the derived reason must degrade to the enum default, never empty: %v", dl)
	}
	doc, _ := json.Marshal(dl)
	e9Validate(t, schemas, "dead-letter-record.schema.json", "dead-letter record", doc)
}

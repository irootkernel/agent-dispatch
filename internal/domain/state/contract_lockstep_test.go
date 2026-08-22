package state

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// TestIntentStatesMatchContractSchema reads the dispatch-intent contract
// schema and fails when its state enum drifts from the domain state set or
// from the transition table alphabet (acceptance: all state enums match
// contracts and schemas).
func TestIntentStatesMatchContractSchema(t *testing.T) {
	// The schemas live at the repository root's docs/schemas; a missing
	// file is a broken checkout, not a skippable condition (E7-T4: the
	// wrong relative path made this guard skip silently on every run).
	raw, err := os.ReadFile("../../../docs/schemas/dispatch-intent.schema.json")
	if err != nil {
		t.Fatalf("docs schemas unavailable: %v", err)
	}
	var schema struct {
		Properties struct {
			State struct {
				Enum []string `json:"enum"`
			} `json:"state"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Properties.State.Enum) == 0 {
		t.Fatal("dispatch-intent schema has no state enum")
	}
	declared := map[string]bool{}
	for _, s := range AllIntentStates() {
		declared[string(s)] = true
		if _, err := records.ParseIntentState(string(s)); err != nil {
			t.Errorf("declared state %s does not parse through records", s)
		}
	}
	for _, s := range schema.Properties.State.Enum {
		if !declared[s] {
			t.Errorf("schema state %q is missing from the domain state set", s)
		}
	}
	if len(schema.Properties.State.Enum) != len(declared) {
		t.Errorf("schema enum has %d states, domain declares %d", len(schema.Properties.State.Enum), len(declared))
	}
	// Every state participates in the table (initial ready included).
	inTable := map[records.IntentState]bool{records.IntentReady: true}
	for edge := range intentTable {
		inTable[edge.from] = true
		inTable[edge.to] = true
	}
	for _, s := range AllIntentStates() {
		if !inTable[s] {
			t.Errorf("state %s does not participate in the transition table", s)
		}
	}
}

// TestReceiptAxesMatchContractSchema reads the dispatch-receipt contract
// schema and fails when the acceptance or execution enums drift from the
// projection axes.
func TestReceiptAxesMatchContractSchema(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/schemas/dispatch-receipt.schema.json")
	if err != nil {
		t.Fatalf("docs schemas unavailable: %v", err)
	}
	var schema struct {
		Properties struct {
			AcceptanceState struct {
				Enum []string `json:"enum"`
			} `json:"acceptance_state"`
			ExecutionState struct {
				Enum []string `json:"enum"`
			} `json:"execution_state"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	wantAcceptance := map[string]bool{
		string(records.AcceptanceAccepted): true,
		string(records.AcceptanceRejected): true,
		string(records.AcceptanceUnknown):  true,
	}
	for _, s := range schema.Properties.AcceptanceState.Enum {
		if !wantAcceptance[s] {
			t.Errorf("schema acceptance state %q is not a projection axis value", s)
		}
	}
	if len(schema.Properties.AcceptanceState.Enum) != len(wantAcceptance) {
		t.Errorf("acceptance enum drift: schema %d values, domain %d", len(schema.Properties.AcceptanceState.Enum), len(wantAcceptance))
	}
	wantExecution := map[string]bool{
		string(records.ExecUnavailable): true,
		string(records.ExecQueued):      true,
		string(records.ExecRunning):     true,
		string(records.ExecSucceeded):   true,
		string(records.ExecFailed):      true,
		string(records.ExecCanceled):    true,
	}
	for _, s := range schema.Properties.ExecutionState.Enum {
		if !wantExecution[s] {
			t.Errorf("schema execution state %q is not a projection axis value", s)
		}
	}
	if len(schema.Properties.ExecutionState.Enum) != len(wantExecution) {
		t.Errorf("execution enum drift: schema %d values, domain %d", len(schema.Properties.ExecutionState.Enum), len(wantExecution))
	}
}

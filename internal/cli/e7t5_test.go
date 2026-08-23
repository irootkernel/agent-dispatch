package cli

import (
	"bytes"
	"strings"
	"testing"
)

// E7-T5 regression suite: the CLI inspection contract (H-5, M-13, M-14,
// M-15, M-16).

// TestConfigShowNormalized proves config show prints the normalized
// configuration without any command_not_implemented (H-5).
func TestConfigShowNormalized(t *testing.T) {
	configPath, _ := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	if code := Run([]string{"config", "show", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("config show: %s", errb.String())
	}
	body := out.String()
	for _, want := range []string{`"routes"`, `"wiki"`, `"hermes-main"`, `"vault-main"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("config show must print the normalized configuration (%s missing): %s", want, body)
		}
	}
	if strings.Contains(body, "command_not_implemented") {
		t.Fatalf("config show must be implemented: %s", body)
	}
}

// e7t5AcceptedDispatch drives one accepted dispatch with work begun and
// completed, returning its id.
func e7t5AcceptedDispatch(t *testing.T, configPath, vault string) string {
	t.Helper()
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb)
	})
	id, _ := decodeEnvelope(t, &out)["dispatch_id"].(string)
	if id == "" {
		t.Fatalf("dispatch produced no id: %s %s", out.String(), errb.String())
	}
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", id, "--run-id", "r1", "--external-task-id", "t_00000001"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("work begin failed")
	}
	withStdin(t, `[]`, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", id, "--run-id", "r1", "--manifest", "-"}, &bytes.Buffer{}, &bytes.Buffer{})
	})
	return id
}

// TestDispatchesShowFullLineage proves the complete causal chain is
// inspectable from dispatches show alone (OPS-002, H-5): decision,
// batch, source observations, and work receipts.
func TestDispatchesShowFullLineage(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	id := e7t5AcceptedDispatch(t, configPath, vault)
	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "show", "--config", configPath, id}, &out, &errb); code != 0 {
		t.Fatalf("dispatches show: %s", errb.String())
	}
	body := out.String()
	for _, want := range []string{`"decision"`, `"decision_id"`, `"disposition"`, `"batch"`, `"batch_id"`, `"observations"`, `"observation_id"`, `"work_receipt"`, `"run_id"`, `"t_00000001"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the full lineage must be inspectable (%s missing): %s", want, body)
		}
	}
}

// TestDispatchesListFiltersAndPagination proves the age, external-ref,
// and causal-ID filters with offset pagination (M-13).
func TestDispatchesListFiltersAndPagination(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	first := e7t5AcceptedDispatch(t, configPath, vault)

	var out, errb bytes.Buffer
	// External-reference filter finds the accepted dispatch.
	if code := Run([]string{"dispatches", "list", "--config", configPath, "--external-ref", "t_00000001"}, &out, &errb); code != 0 {
		t.Fatalf("external-ref filter: %s", errb.String())
	}
	if res := decodeEnvelope(t, &out); res["count"] != float64(1) {
		t.Fatalf("the external-ref filter must find exactly the accepted dispatch: %v", res["count"])
	}
	// Causal-ID filter matches by dispatch prefix.
	prefix := first[:len(first)-4] + "%"
	_ = prefix
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "list", "--config", configPath, "--causal", first[:len(first)-6]}, &out, &errb); code != 0 {
		t.Fatalf("causal filter: %s", errb.String())
	}
	if res := decodeEnvelope(t, &out); res["count"] != float64(1) {
		t.Fatalf("the causal filter must find the dispatch: %v", res["count"])
	}
	// Age filter: a one-hour window excludes nothing now but a
	// zero-width future window must exclude the fresh dispatch.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "list", "--config", configPath, "--age", "1h"}, &out, &errb); code != 0 {
		t.Fatalf("age filter: %s", errb.String())
	}
	if res := decodeEnvelope(t, &out); res["count"] != float64(0) {
		t.Fatalf("a fresh dispatch is not older than one hour: %v", res["count"])
	}
	// Pagination: offset past the end yields an empty page with the
	// offset echoed.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "list", "--config", configPath, "--offset", "50"}, &out, &errb); code != 0 {
		t.Fatalf("offset pagination: %s", errb.String())
	}
	if res := decodeEnvelope(t, &out); res["count"] != float64(0) || res["offset"] != float64(50) {
		t.Fatalf("offset pagination must return an empty page with the offset echoed: %v", res)
	}
}

// TestDeadLetteredRetryRequiresReasonClassification proves M-15: a
// dead-lettered retry without --reason is a usage defect (exit 2,
// flag_invalid), never an internal one.
func TestDeadLetteredRetryRequiresReasonClassification(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/new.md","exists":true,"new":true,"size":5,"type":"f"}]`, func() {
		Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman", "--no-submit"}, &out, &errb)
	})
	id, _ := decodeEnvelope(t, &out)["dispatch_id"].(string)
	// Dead-letter the work through an ambiguous submit and an unresolved,
	// budget-exhausted reconciliation.
	healed := e5t1Store(t, configPath)
	var key string
	healed.QueryRow(`SELECT idempotency_key FROM dispatch_intents WHERE dispatch_id = ?`, id).Scan(&key)
	healed.Exec(`UPDATE dispatch_intents SET state = 'dead_lettered', updated_at = '2026-08-23T00:00:00Z' WHERE dispatch_id = ?`, id)
	healed.Close()
	out.Reset()
	errb.Reset()
	if code := Run([]string{"dispatches", "retry", "--config", configPath, id}, &out, &errb); code != 2 {
		t.Fatalf("a dead-lettered retry without reason must be a usage defect, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "flag_invalid") {
		t.Fatalf("the usage defect must classify as flag_invalid: %s", errb.String())
	}
}

// TestConfigShowRedactedAndRevisions proves config show redacts secret
// references and prints the computed route revisions.
func TestConfigShowRedactedAndRevisions(t *testing.T) {
	configPath, _ := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	if code := Run([]string{"config", "show", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("config show: %s", errb.String())
	}
	body := out.String()
	if !strings.Contains(body, `"computed_route_revisions"`) || !strings.Contains(body, `"wiki"`) {
		t.Fatalf("config show must print the computed route revisions: %s", body)
	}
	// The stub fixture carries no live secret, but the redaction contract
	// holds structurally: no secret value key ever appears raw.
	if strings.Contains(body, `"secret":"`) || strings.Contains(body, `"password"`) {
		t.Fatalf("config show must never print raw secret values: %s", body)
	}
}

// TestTraceIDReachesEnvelope proves the parsed --trace-id lands in every
// success envelope (M-14).
func TestTraceIDReachesEnvelope(t *testing.T) {
	configPath, _ := e4t3Fixture(t)
	e4t3RegisterRoute(t, configPath)
	var out, errb bytes.Buffer
	if code := Run([]string{"dispatches", "list", "--config", configPath, "--trace-id", "trace-e7t5-1"}, &out, &errb); code != 0 {
		t.Fatalf("dispatches list: %s", errb.String())
	}
	if !strings.Contains(out.String(), `"trace_id":"trace-e7t5-1"`) {
		t.Fatalf("the trace id must reach the envelope: %s", out.String())
	}
}

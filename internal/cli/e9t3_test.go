package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/config"
)

// e9t3LogEvents decodes the JSON operational log lines from one buffer,
// ignoring anything that is not a log line.
func e9t3LogEvents(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if json.Unmarshal([]byte(raw), &line) != nil {
			continue
		}
		if _, ok := line["event"]; ok {
			events = append(events, line)
		}
	}
	return events
}

// TestE9T3WorkLifecycleEventsCarryTrace proves L-17: work begin and
// work complete emit the declarative work.begun / work.completed events
// on the operational log with the request's trace id and the causal
// dispatch and run identity.
func TestE9T3WorkLifecycleEventsCarryTrace(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	if dispatchID == "" {
		t.Fatalf("no dispatch id: %v", res)
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-e9t3", "--external-task-id", "t_00000001", "--log-level", "info", "--trace-id", "trace-e9t3"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	var begun bool
	for _, ev := range e9t3LogEvents(t, &errb) {
		if ev["event"] != "work.begun" {
			continue
		}
		begun = true
		if ev["trace_id"] != "trace-e9t3" || ev["dispatch_id"] != dispatchID || ev["run_id"] != "run-e9t3" {
			t.Fatalf("work.begun must carry the trace and causal identity: %v", ev)
		}
	}
	if !begun {
		t.Fatalf("work begin must emit work.begun on the operational log: %s", errb.String())
	}

	out.Reset()
	errb.Reset()
	manifest := `[{"path":"Inbox/new.md","after_digest":"` + e5t1GoodDigest + `"}]`
	withStdin(t, manifest, func() {
		Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", dispatchID, "--run-id", "run-e9t3", "--manifest", "-", "--log-level", "info", "--trace-id", "trace-e9t3"}, &out, &errb)
	})
	var completed bool
	for _, ev := range e9t3LogEvents(t, &errb) {
		if ev["event"] != "work.completed" {
			continue
		}
		completed = true
		if ev["trace_id"] != "trace-e9t3" || ev["dispatch_id"] != dispatchID || ev["run_id"] != "run-e9t3" {
			t.Fatalf("work.completed must carry the trace and causal identity: %v", ev)
		}
	}
	if !completed {
		t.Fatalf("work complete must emit work.completed on the operational log: %s", errb.String())
	}
}

// TestE9T3DoctorFindingsCarryTraceID proves L-17's doctor half: every
// finding in the doctor result carries the request's trace id so an
// operator can correlate a failing check with the invocation that
// produced it.
func TestE9T3DoctorFindingsCarryTraceID(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	if err := os.Chmod(vault, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(vault, 0o755) })
	var out, errb bytes.Buffer
	if code := Run([]string{"doctor", "--config", configPath, "--trace-id", "trace-e9t3"}, &out, &errb); code != 3 {
		t.Fatalf("an unreadable root must fail doctor at exit 3, got %d: %s", code, errb.String())
	}
	envelope := decodeEnvelope(t, &out)
	findings, _ := envelope["findings"].([]any)
	if len(findings) == 0 {
		t.Fatalf("the unreadable root must produce findings: %v", envelope)
	}
	for _, raw := range findings {
		f, _ := raw.(map[string]any)
		if f["trace_id"] != "trace-e9t3" {
			t.Fatalf("every finding must carry the request's trace id: %v", f)
		}
	}
}

// TestE9T3ArrivalDecisionRecordsPolicyDigest proves L-18 at the arrival
// path: the planner's decision records the independent policy digest of
// the live route, not a route revision echo.
func TestE9T3ArrivalDecisionRecordsPolicyDigest(t *testing.T) {
	configPath, vault := e4t3Fixture(t)
	res, _ := e4t3Dispatch(t, configPath, vault)
	dispatchID, _ := res["dispatch_id"].(string)
	if dispatchID == "" {
		t.Fatalf("no dispatch id: %v", res)
	}
	store := e5t1Store(t, configPath)
	var policyRevision, routeRevision string
	if err := store.QueryRow(`SELECT d.policy_revision, d.route_revision FROM policy_decisions d
		JOIN dispatch_intents i ON i.decision_id = d.decision_id WHERE i.dispatch_id = ?`, dispatchID).Scan(&policyRevision, &routeRevision); err != nil {
		t.Fatalf("the arrival decision must exist: %v", err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	expected := config.PolicyRevision(cfg.Routes["wiki"])
	if policyRevision != expected || !strings.HasPrefix(policyRevision, "pol-") {
		t.Fatalf("the arrival decision must record the live route's policy digest %q, got %q", expected, policyRevision)
	}
	if policyRevision == routeRevision {
		t.Fatalf("the policy digest must be independent of the route revision, got both %q", policyRevision)
	}
}

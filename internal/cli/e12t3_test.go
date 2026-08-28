package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/ports"
)

// E12-T3 CLI coverage: `events show` projects a two-destination aggregate
// with per-child acceptance/execution/receipt/retry projections and the
// FBK-012 completion-evidence gap (AC-806 posture), the blocked outcome
// surfaces manual intervention without auto-running anything (FBK-011),
// the partial outcome schedules one same-lane follow-up for the remaining
// scope (FBK-010), and `status` carries the per-destination lane
// summaries (OPS-011).

// e12t3AggregateOf reads the aggregate id of the latest children.
func e12t3AggregateOf(t *testing.T, configPath string) string {
	t.Helper()
	store := e5t1Store(t, configPath)
	defer store.Close()
	var aggregateID string
	if err := store.QueryRow(`SELECT aggregate_id FROM child_dispatches ORDER BY created_at DESC, destination_id DESC LIMIT 1`).Scan(&aggregateID); err != nil {
		t.Fatal(err)
	}
	return aggregateID
}

// e12t3DispatchBothLanes enables the two-destination fixture and submits
// one occurrence: both lanes' children persist, the canonically-first
// child is submitted (accepted), the sibling stays ready.
func e12t3DispatchBothLanes(t *testing.T) (configPath, vault, mainChild, reviewChild string) {
	t.Helper()
	configPath, vault = e12t2TwoDestinationFixture(t)
	setPlanEnv(t, vault, false)
	e4t3RegisterRoute(t, configPath)
	e12t2Enable(t, configPath)
	os.WriteFile(filepath.Join(vault, "Inbox", "e12t3.md"), []byte("two lanes"), 0o644)
	var out, errb bytes.Buffer
	withStdin(t, `[{"name":"Inbox/e12t3.md","exists":true,"new":true,"size":8,"type":"f"}]`, func() {
		if code := Run([]string{"dispatch", "--route", "wiki", "--config", configPath, "--input", "watchman"}, &out, &errb); code != 0 {
			t.Fatalf("dispatch: %s", errb.String())
		}
	})
	store := e5t1Store(t, configPath)
	defer store.Close()
	for lane, ptr := range map[string]*string{"main": &mainChild, "review": &reviewChild} {
		if err := store.QueryRow(`SELECT dispatch_id FROM child_dispatches WHERE destination_id = ?`, lane).Scan(ptr); err != nil {
			t.Fatalf("lane %s child: %v", lane, err)
		}
	}
	return configPath, vault, mainChild, reviewChild
}

// TestE12T3EventsShowProjectsChildrenAndEvidenceGap pins CLI-013/FAN-010
// and the FBK-012 gap: the accepted child without a work receipt renders
// completion_evidence missing with the actionable next step, the aggregate
// status names the evidence gap, and both children carry their separate
// destination/retry/receipt projections.
func TestE12T3EventsShowProjectsChildrenAndEvidenceGap(t *testing.T) {
	configPath, _, mainChild, _ := e12t3DispatchBothLanes(t)
	aggregateID := e12t3AggregateOf(t, configPath)
	var out, errb bytes.Buffer
	if code := Run([]string{"events", "show", "--config", configPath, aggregateID}, &out, &errb); code != 0 {
		t.Fatalf("events show: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["aggregate_status"] != "evidence-gap" {
		t.Fatalf("an accepted child without a work receipt must drive the aggregate status: %v", res["aggregate_status"])
	}
	children, _ := res["children"].([]any)
	if len(children) != 2 {
		t.Fatalf("both lanes' children must render: %d", len(children))
	}
	var gapChild, sibling map[string]any
	for _, raw := range children {
		child, _ := raw.(map[string]any)
		dest, _ := child["destination"].(map[string]any)
		if child["dispatch_id"] == mainChild {
			gapChild = child
		} else {
			sibling = child
		}
		if dest["id"] == "" || dest["workstream"] == "" {
			t.Fatalf("every child names its destination lane: %v", child)
		}
	}
	if gapChild == nil || gapChild["completion_evidence"] != "missing" {
		t.Fatalf("the accepted child without a receipt must render the gap: %v", gapChild)
	}
	nextStep, _ := gapChild["completion_evidence_next_step"].(string)
	if !strings.Contains(nextStep, mainChild) || !strings.Contains(nextStep, "work") {
		t.Fatalf("the gap must carry the actionable next step: %q", nextStep)
	}
	if gapChild["acceptance"] != "accepted" {
		t.Fatalf("the submitted child's acceptance must project: %v", gapChild["acceptance"])
	}
	// A never-accepted child renders not-applicable — its pending state
	// is the story, never a false "present" (review round 1).
	if sibling == nil || sibling["completion_evidence"] != "not-applicable" {
		t.Fatalf("the not-yet-accepted sibling is not an evidence gap: %v", sibling)
	}
	// The selection summary with its closed reasons rides along.
	selections := jsonToText(t, res["selections"])
	if !strings.Contains(selections, "main") || !strings.Contains(selections, "review") {
		t.Fatalf("the selection summary must name both lanes: %v", selections)
	}
}

// TestE12T3EventsShowBlockedNamesManualIntervention pins FBK-011 at the
// inspection surface: a blocked child renders the manual-intervention
// class and the aggregate names it, while the sibling lane is untouched.
func TestE12T3EventsShowBlockedNamesManualIntervention(t *testing.T) {
	configPath, _, mainChild, _ := e12t3DispatchBothLanes(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-block"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-block",
		"--status", "blocked", "--manual-reason", "protected-path policy forbids the rewrite"}, &out, &errb); code != 0 {
		t.Fatalf("work complete --status blocked: %s", errb.String())
	}
	blocked := decodeEnvelope(t, &out)
	if blocked["status"] != "blocked" || blocked["manual_intervention"] != true {
		t.Fatalf("the blocked outcome must surface manual intervention: %v", blocked)
	}
	aggregateID := e12t3AggregateOf(t, configPath)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"events", "show", "--config", configPath, aggregateID}, &out, &errb); code != 0 {
		t.Fatalf("events show: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["aggregate_status"] != "manual-intervention" {
		t.Fatalf("the blocked lane must drive the aggregate status: %v", res["aggregate_status"])
	}
	// Nothing auto-ran: no follow-up intent exists beside the two children.
	store := e5t1Store(t, configPath)
	defer store.Close()
	var intents int
	if err := store.QueryRow(`SELECT COUNT(*) FROM dispatch_intents`).Scan(&intents); err != nil || intents != 2 {
		t.Fatalf("a blocked lane schedules nothing: %d %v", intents, err)
	}
	var laneState string
	if err := store.QueryRow(`SELECT lane_state FROM destination_lane_state WHERE route_id = 'wiki' AND destination_id = 'main'`).Scan(&laneState); err != nil || laneState != "ACTIVE_CLEAN" {
		t.Fatalf("the blocked lane stays active with its child: %q %v", laneState, err)
	}
}

// TestE12T3PartialOutcomeCarriesRemainingScope pins FBK-010 at the CLI:
// `work complete --status partially_completed` schedules exactly one
// same-lane follow-up whose manifest is the remaining scope, and the
// receipt view rows name the child lane (DAT-011).
func TestE12T3PartialOutcomeCarriesRemainingScope(t *testing.T) {
	configPath, _, mainChild, reviewChild := e12t3DispatchBothLanes(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-partial"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	remainingPath := filepath.Join(t.TempDir(), "remaining.json")
	os.WriteFile(remainingPath, []byte(`[{"path":"Indexes/remaining.md"}]`), 0o644)
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	os.WriteFile(manifestPath, []byte(`[{"path":"Indexes/done.md"}]`), 0o644)
	if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-partial",
		"--status", "partially_completed", "--manifest", manifestPath, "--remaining-manifest", remainingPath}, &out, &errb); code != 0 {
		t.Fatalf("work complete --status partially_completed: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	followup, _ := res["followup_dispatch_id"].(string)
	if res["status"] != "partially_completed" || followup == "" {
		t.Fatalf("the partial outcome must schedule its follow-up: %v", res)
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	snap, err := store.LoadIntent(context.Background(), followup)
	if err != nil {
		t.Fatal(err)
	}
	if snap.DestinationID != "main" || snap.Generation != 2 {
		t.Fatalf("the follow-up must be the same lane's next generation: %+v", snap)
	}
	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(snap.RequestJSON), &req); err != nil {
		t.Fatal(err)
	}
	// The follow-up manifest is exactly the remaining scope when the
	// lane has no unresolved dirty work: the parent's own arrival batch
	// sits below the generation window, and the parent-manifest fallback
	// never rides along beside a partial scope (FBK-010, review round 1).
	if len(req.Activation.Manifest) != 1 || req.Activation.Manifest[0].Path != "Indexes/remaining.md" {
		t.Fatalf("the follow-up manifest must be exactly the remaining scope: %+v", req.Activation.Manifest)
	}
	// The receipts list names the child lane on work rows (DAT-011).
	rows, err := store.ListReceipts(context.Background(), ports.ReceiptFilter{DispatchID: mainChild, Kind: "work", Limit: 10})
	if err != nil || len(rows) == 0 || rows[0].DestinationID != "main" {
		t.Fatalf("the work rows must carry the destination lane: %+v %v", rows, err)
	}
	// The empty remaining scope fails closed with guidance (the CLI test
	// asserts the exit class; the service-level guidance text is pinned by
	// the workreceipt suite).
	emptyManifest := filepath.Join(t.TempDir(), "empty-manifest.json")
	emptyRemaining := filepath.Join(t.TempDir(), "empty-remaining.json")
	os.WriteFile(emptyManifest, []byte(`[{"path":"Indexes/more.md"}]`), 0o644)
	os.WriteFile(emptyRemaining, []byte(`[]`), 0o644)
	out.Reset()
	errb.Reset()
	// The sibling lane's ready child is lane-active (work begin admits
	// it) and carries no begun receipt yet.
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", reviewChild, "--run-id", "run-empty"}, &out, &errb); code != 0 {
		t.Fatalf("work begin (sibling): %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", reviewChild, "--run-id", "run-empty",
		"--status", "partially_completed", "--manifest", emptyManifest, "--remaining-manifest", emptyRemaining}, &out, &errb); code != 4 {
		t.Fatalf("an empty remaining scope must reject at 4, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "completed") {
		t.Fatalf("the rejection must point at the completed outcome: %s", errb.String())
	}
}

// TestE12T3StatusCarriesLaneSummaries pins OPS-011: the status surface
// reports one bounded row per destination lane.
func TestE12T3StatusCarriesLaneSummaries(t *testing.T) {
	configPath, _, mainChild, reviewChild := e12t3DispatchBothLanes(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"status", "--config", configPath}, &out, &errb); code != 0 {
		t.Fatalf("status: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	routes, _ := res["routes"].([]any)
	if len(routes) != 1 {
		t.Fatalf("one route row: %v", routes)
	}
	route, _ := routes[0].(map[string]any)
	lanes, _ := route["lanes"].([]any)
	if len(lanes) != 2 {
		t.Fatalf("one lane row per destination: %v", lanes)
	}
	byDestination := map[string]map[string]any{}
	for _, raw := range lanes {
		lane, _ := raw.(map[string]any)
		byDestination[lane["destination_id"].(string)] = lane
	}
	if byDestination["main"]["active_dispatch_id"] != mainChild || byDestination["review"]["active_dispatch_id"] != reviewChild {
		t.Fatalf("each lane names its active child: %v", lanes)
	}
	if byDestination["main"]["lane_state"] != "ACTIVE_CLEAN" || byDestination["review"]["lane_state"] != "ACTIVE_CLEAN" {
		t.Fatalf("both lanes are active after the fan-out: %v", lanes)
	}
}

// jsonToText renders one envelope member deterministically for substring
// assertions.
func jsonToText(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// e12t3WriteDoc writes one JSON document and returns its path.
func e12t3WriteDoc(t *testing.T, doc map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestE12T3FullDocumentPartialRoutesAndSchedules pins the document form
// (review round 1, product finding): a work-receipt/v2 document carrying
// status partially_completed with its scopes routes through the partial
// branch with NO --status/--remaining-manifest flags — one same-lane
// follow-up whose manifest leads with the document's remaining scope.
func TestE12T3FullDocumentPartialRoutesAndSchedules(t *testing.T) {
	configPath, _, mainChild, _ := e12t3DispatchBothLanes(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-doc"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	doc := map[string]any{
		"schema_version":  "agent-dispatch.work-receipt/v2",
		"dispatch_id":     mainChild,
		"run_id":          "run-doc",
		"resource_id":     "vault-main",
		"submitted_at":    "2026-08-29T05:00:00Z",
		"status":          "partially_completed",
		"changes":         []map[string]any{{"path": "Indexes/done.md"}},
		"completed_scope": []map[string]any{{"path": "Indexes/done.md"}},
		"remaining_scope": []map[string]any{{"path": "Indexes/doc-remaining.md"}},
		"failure_code":    nil,
	}
	manifest := e12t3WriteDoc(t, doc)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-doc", "--manifest", manifest}, &out, &errb); code != 0 {
		t.Fatalf("full-document partial submission: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	followup, _ := res["followup_dispatch_id"].(string)
	if res["status"] != "partially_completed" || followup == "" {
		t.Fatalf("the document's partial outcome must route: %v", res)
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	snap, err := store.LoadIntent(context.Background(), followup)
	if err != nil || snap.DestinationID != "main" {
		t.Fatalf("the follow-up keeps the document's lane: %+v %v", snap, err)
	}
	var req ports.TaskRequest
	if err := json.Unmarshal([]byte(snap.RequestJSON), &req); err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, m := range req.Activation.Manifest {
		paths[m.Path] = true
	}
	if !paths["Indexes/doc-remaining.md"] {
		t.Fatalf("the follow-up manifest must lead with the document's remaining scope: %+v", req.Activation.Manifest)
	}
	var status, remaining string
	if err := store.QueryRow(`SELECT status, remaining_scope_json FROM work_receipts WHERE dispatch_id = ? AND run_id = 'run-doc'`, mainChild).Scan(&status, &remaining); err != nil || status != "partially_completed" || !strings.Contains(remaining, "doc-remaining") {
		t.Fatalf("the document's outcome and scope must persist: %q %s %v", status, remaining, err)
	}
}

// TestE12T3FullDocumentBlockedRoutes pins the document form of the
// blocked outcome: a v2 blocked document records its manual reason with
// no flags and leaves the lane untouched.
func TestE12T3FullDocumentBlockedRoutes(t *testing.T) {
	configPath, _, mainChild, _ := e12t3DispatchBothLanes(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-doc-block"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	doc := map[string]any{
		"schema_version": "agent-dispatch.work-receipt/v2",
		"dispatch_id":    mainChild,
		"run_id":         "run-doc-block",
		"resource_id":    "vault-main",
		"submitted_at":   "2026-08-29T05:10:00Z",
		"status":         "blocked",
		"changes":        []map[string]any{},
		"manual_reason":  "documented operator blocker",
		"failure_code":   nil,
	}
	manifest := e12t3WriteDoc(t, doc)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-doc-block", "--manifest", manifest}, &out, &errb); code != 0 {
		t.Fatalf("full-document blocked submission: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["status"] != "blocked" || res["manual_intervention"] != true {
		t.Fatalf("the document's blocked outcome must route: %v", res)
	}
	store := e5t1Store(t, configPath)
	defer store.Close()
	var reason string
	if err := store.QueryRow(`SELECT COALESCE(manual_reason, '') FROM work_receipts WHERE dispatch_id = ? AND run_id = 'run-doc-block'`, mainChild).Scan(&reason); err != nil || reason != "documented operator blocker" {
		t.Fatalf("the document's manual reason must persist: %q %v", reason, err)
	}
}

// TestE12T3DocumentStatusConflictRejected pins the conflict rule: an
// explicit --status that disagrees with the v2 document's status is a
// validation error naming both outcomes.
func TestE12T3DocumentStatusConflictRejected(t *testing.T) {
	configPath, _, mainChild, _ := e12t3DispatchBothLanes(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-conflict"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	doc := map[string]any{
		"schema_version":  "agent-dispatch.work-receipt/v2",
		"dispatch_id":     mainChild,
		"run_id":          "run-conflict",
		"resource_id":     "vault-main",
		"submitted_at":    "2026-08-29T05:20:00Z",
		"status":          "partially_completed",
		"changes":         []map[string]any{},
		"completed_scope": []map[string]any{},
		"remaining_scope": []map[string]any{{"path": "Indexes/still.md"}},
		"failure_code":    nil,
	}
	manifest := e12t3WriteDoc(t, doc)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-conflict",
		"--status", "completed", "--manifest", manifest}, &out, &errb); code != 4 {
		t.Fatalf("a conflicting --status must reject at 4, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "conflicts with --status") {
		t.Fatalf("the rejection must name the conflict: %s", errb.String())
	}
}

// TestE12T3UnknownDocumentVersionRejected pins the version boundary
// (review round 1, testing finding): a document whose schema_version is
// a genuinely unknown future version rejects on the version alone — the
// identity fields all match the invoked run.
func TestE12T3UnknownDocumentVersionRejected(t *testing.T) {
	configPath, _, mainChild, _ := e12t3DispatchBothLanes(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-v3"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	doc := map[string]any{
		"schema_version": "agent-dispatch.work-receipt/v3",
		"dispatch_id":    mainChild,
		"run_id":         "run-v3",
		"resource_id":    "vault-main",
		"submitted_at":   "2026-08-29T05:30:00Z",
		"status":         "completed",
		"changes":        []map[string]any{},
		"failure_code":   nil,
	}
	manifest := e12t3WriteDoc(t, doc)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-v3", "--manifest", manifest}, &out, &errb); code != 4 {
		t.Fatalf("an unknown document version must reject at 4, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "schema_version must be") {
		t.Fatalf("the rejection must be the version rule: %s", errb.String())
	}
	if strings.Contains(errb.String(), "does not match") {
		t.Fatalf("a matching identity must not add rejection noise: %s", errb.String())
	}
}

// TestE12T3InvalidLatestReceiptKeepsEvidenceGap pins the FBK-012
// invalid-receipt half: an accepted child whose LATEST work receipt is
// invalid renders completion_evidence missing and the aggregate stays
// evidence-gap (review round 1, testing finding).
func TestE12T3InvalidLatestReceiptKeepsEvidenceGap(t *testing.T) {
	configPath, _, mainChild, _ := e12t3DispatchBothLanes(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-bad"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	// The invalid-receipt shape: the run's begun row is marked invalid
	// evidence (the audited-invalid path records the same
	// validation_state; the direct seed pins the read model without
	// coupling to one rejection class).
	store := e5t1Store(t, configPath)
	if _, err := store.Exec(`UPDATE work_receipts SET validation_state = 'invalid', validation_reasons_json = '["seeded invalid evidence"]'
		WHERE dispatch_id = ? AND run_id = 'run-bad'`, mainChild); err != nil {
		t.Fatal(err)
	}
	store.Close()
	aggregateID := e12t3AggregateOf(t, configPath)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"events", "show", "--config", configPath, aggregateID}, &out, &errb); code != 0 {
		t.Fatalf("events show: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	if res["aggregate_status"] != "evidence-gap" {
		t.Fatalf("an invalid latest receipt keeps the aggregate at evidence-gap: %v", res["aggregate_status"])
	}
	children, _ := res["children"].([]any)
	for _, raw := range children {
		child, _ := raw.(map[string]any)
		if child["dispatch_id"] == mainChild && child["completion_evidence"] != "missing" {
			t.Fatalf("the accepted child with an invalid latest receipt must render the gap: %v", child)
		}
	}
}

// TestE12T3BlockedResolutionCompletesLane pins the blocked resolution
// leg (review round 1, testing finding): after the blocked receipt, a
// fresh run completes the lane and events show flips the child to
// completed — the LATEST receipt drives the projection.
func TestE12T3BlockedResolutionCompletesLane(t *testing.T) {
	configPath, _, mainChild, _ := e12t3DispatchBothLanes(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-block-1"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-block-1",
		"--status", "blocked", "--manual-reason", "wait for policy"}, &out, &errb); code != 0 {
		t.Fatalf("blocked: %s", errb.String())
	}
	// Resolution: a FRESH run id begins and completes the same dispatch.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-block-2"}, &out, &errb); code != 0 {
		t.Fatalf("work begin (resolution): %s", errb.String())
	}
	manifest := filepath.Join(t.TempDir(), "resolved.json")
	os.WriteFile(manifest, []byte(`[]`), 0o600)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-block-2", "--manifest", manifest}, &out, &errb); code != 0 {
		t.Fatalf("work complete (resolution): %s", errb.String())
	}
	aggregateID := e12t3AggregateOf(t, configPath)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"events", "show", "--config", configPath, aggregateID}, &out, &errb); code != 0 {
		t.Fatalf("events show: %s", errb.String())
	}
	res := decodeEnvelope(t, &out)
	children, _ := res["children"].([]any)
	for _, raw := range children {
		child, _ := raw.(map[string]any)
		if child["dispatch_id"] != mainChild {
			continue
		}
		if child["completion_evidence"] != "present" {
			t.Fatalf("the resolved child must render present after its completed receipt: %v", child)
		}
		receipt, _ := child["work_receipt"].(map[string]any)
		if receipt["status"] != "completed" {
			t.Fatalf("the LATEST receipt must drive the projection: %v", receipt)
		}
	}
}

// TestE12T3RemainingManifestDocumentConflictRejected pins the
// both-given rule (review round 1): a --remaining-manifest whose content
// differs from the v2 document's remaining_scope is an explicit conflict.
func TestE12T3RemainingManifestDocumentConflictRejected(t *testing.T) {
	configPath, _, mainChild, _ := e12t3DispatchBothLanes(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-scope-conflict"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	doc := map[string]any{
		"schema_version":  "agent-dispatch.work-receipt/v2",
		"dispatch_id":     mainChild,
		"run_id":          "run-scope-conflict",
		"resource_id":     "vault-main",
		"submitted_at":    "2026-08-29T05:40:00Z",
		"status":          "partially_completed",
		"changes":         []map[string]any{{"path": "Indexes/done.md"}},
		"completed_scope": []map[string]any{{"path": "Indexes/done.md"}},
		"remaining_scope": []map[string]any{{"path": "Indexes/from-document.md"}},
		"failure_code":    nil,
	}
	manifest := e12t3WriteDoc(t, doc)
	flagScope := filepath.Join(t.TempDir(), "remaining.json")
	os.WriteFile(flagScope, []byte(`[{"path":"Indexes/from-flag.md"}]`), 0o600)
	out.Reset()
	errb.Reset()
	// The explicit --status agrees with the document, so only the two
	// differing remaining scopes conflict.
	if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-scope-conflict",
		"--status", "partially_completed", "--manifest", manifest, "--remaining-manifest", flagScope}, &out, &errb); code != 4 {
		t.Fatalf("a differing flag scope must reject at 4, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "conflicts with --remaining-manifest") {
		t.Fatalf("the rejection must name the scope conflict: %s", errb.String())
	}
}

// TestE12T3BlockedManifestRejected pins the blocked flag grammar (review
// round 1, security finding): a bare change-array --manifest beside a
// blocked outcome is a shape error — blocked takes its manual reason (or
// a v2 blocked document).
func TestE12T3BlockedManifestRejected(t *testing.T) {
	configPath, _, mainChild, _ := e12t3DispatchBothLanes(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"work", "begin", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-blocked-manifest"}, &out, &errb); code != 0 {
		t.Fatalf("work begin: %s", errb.String())
	}
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	os.WriteFile(manifest, []byte(`[{"path":"Indexes/done.md"}]`), 0o600)
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-blocked-manifest",
		"--status", "blocked", "--manual-reason", "policy", "--manifest", manifest}, &out, &errb); code != 4 {
		t.Fatalf("a bare-array manifest beside blocked must reject at 4, got %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "takes --manual-reason only") {
		t.Fatalf("the rejection must tell the operator blocked takes the manual reason: %s", errb.String())
	}
	// The lenient discard is gone too: --remaining-manifest on a completed
	// outcome is a usage error, never a silent drop.
	out.Reset()
	errb.Reset()
	if code := Run([]string{"work", "complete", "--config", configPath, "--dispatch-id", mainChild, "--run-id", "run-blocked-manifest",
		"--manifest", manifest, "--remaining-manifest", manifest}, &out, &errb); code != 2 {
		t.Fatalf("--remaining-manifest beside completed must be a usage error at 2, got %d: %s", code, errb.String())
	}
}

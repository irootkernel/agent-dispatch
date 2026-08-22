package dispatch

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/app/ingest"
	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"github.com/irootkernel/agent-dispatch/internal/schemavalid"
)

func basePolicy() RoutePolicy {
	return RoutePolicy{
		RouteID:              "wiki",
		RouteRevision:        "rev-1",
		ResourceID:           "vault-main",
		AutomaticThreshold:   25,
		HardLimit:            100,
		MaxManifestBytes:     262144,
		BulkAction:           "quarantine",
		OverflowAction:       "reconcile",
		FreshInstanceAction:  "reconcile",
		RequiredCapabilities: []string{"kanban.create", "kanban.lookup"},
	}
}

func batch(n int) *ingest.Result {
	res := &ingest.Result{Replacements: map[string]bool{}}
	for i := 0; i < n; i++ {
		res.Changes = append(res.Changes, records.ChangeItem{
			Path:         "Notes/n.md",
			Operation:    records.OpCreate,
			ExistsAfter:  true,
			FileType:     records.FileRegular,
			DigestStatus: records.DigestUnavailable,
		})
	}
	res.Fingerprint = records.SumDigest([]byte("fp"))
	return res
}

func mustPlan(t *testing.T, p RoutePolicy, in Input) *Plan {
	t.Helper()
	plan, err := Evaluate(p, in)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func hasReason(plan *Plan, reason string) bool {
	for _, r := range plan.ReasonCodes {
		if r == reason {
			return true
		}
	}
	return false
}

func TestDispositionPrecedence(t *testing.T) {
	p := basePolicy()

	if plan := mustPlan(t, p, Input{Batch: batch(3), Flags: records.SourceFlags{Overflow: true}}); plan.Disposition != "reconcile" || plan.GenerationAction != "merge_reconcile" || !hasReason(plan, ReasonOverflowSignal) {
		t.Fatalf("overflow must reconcile without partial dispatch: %+v", plan)
	}
	if plan := mustPlan(t, p, Input{Batch: batch(3), Flags: records.SourceFlags{FreshInstance: true}}); plan.Disposition != "reconcile" || !hasReason(plan, ReasonFreshInstance) {
		t.Fatalf("fresh instance must reconcile: %+v", plan)
	}
	if plan := mustPlan(t, p, Input{Batch: batch(0)}); plan.Disposition != "drop" || plan.GenerationAction != "none" || !hasReason(plan, ReasonNoMeaningfulChanges) {
		t.Fatalf("empty meaningful batch must drop: %+v", plan)
	}
	protected := batch(3)
	protected.Protected = []string{"raw/x.md"}
	if plan := mustPlan(t, p, Input{Batch: protected}); plan.Disposition != "quarantine" || !hasReason(plan, ReasonProtectedPath) {
		t.Fatalf("protected must quarantine: %+v", plan)
	}
	// Protected wins over bulk (precedence 4 before 5/6).
	protectedBulk := batch(200)
	protectedBulk.Protected = []string{"raw/x.md"}
	if plan := mustPlan(t, p, Input{Batch: protectedBulk}); plan.Disposition != "quarantine" || !hasReason(plan, ReasonProtectedPath) {
		t.Fatalf("protected precedence over bulk: %+v", plan)
	}
	immutable := batch(3)
	immutable.Immutable = []string{"SUMMARY.md"}
	if plan := mustPlan(t, p, Input{Batch: immutable}); plan.Disposition != "quarantine" || !hasReason(plan, ReasonImmutablePath) {
		t.Fatalf("immutable must quarantine: %+v", plan)
	}
	if plan := mustPlan(t, p, Input{Batch: batch(101)}); plan.Disposition != "quarantine" || !hasReason(plan, ReasonBatchOverHardLimit) {
		t.Fatalf("hard limit must quarantine: %+v", plan)
	}
	if plan := mustPlan(t, p, Input{Batch: batch(26)}); plan.Disposition != "quarantine" || !hasReason(plan, ReasonOverThreshold) {
		t.Fatalf("over threshold follows bulk action: %+v", plan)
	}
	p.BulkAction = "reconcile"
	if plan := mustPlan(t, p, Input{Batch: batch(26)}); plan.Disposition != "reconcile" || plan.GenerationAction != "merge_reconcile" {
		t.Fatalf("reconcile bulk action: %+v", plan)
	}
	p = basePolicy()
	if plan := mustPlan(t, p, Input{Batch: batch(3), ActiveDispatchExists: true}); plan.Disposition != "merge_pending" || plan.GenerationAction != "increment_dirty" || !hasReason(plan, ReasonActiveDispatch) {
		t.Fatalf("active dispatch must merge pending: %+v", plan)
	}
	if plan := mustPlan(t, p, Input{Batch: batch(3)}); plan.Disposition != "dispatch" || plan.GenerationAction != "create_if_idle" || !hasReason(plan, ReasonNormalBatch) {
		t.Fatalf("normal bounded batch must dispatch: %+v", plan)
	}
}

func TestOverflowAndFreshActionsAreIndependent(t *testing.T) {
	// Overflow action only.
	p := basePolicy()
	p.OverflowAction = "quarantine"
	p.FreshInstanceAction = "reconcile"
	if plan := mustPlan(t, p, Input{Batch: batch(3), Flags: records.SourceFlags{Overflow: true}}); plan.Disposition != "quarantine" {
		t.Fatalf("overflow action quarantine: %+v", plan)
	}
	if plan := mustPlan(t, p, Input{Batch: batch(3), Flags: records.SourceFlags{FreshInstance: true}}); plan.Disposition != "reconcile" {
		t.Fatalf("fresh action must stay reconcile when only overflow quarantines: %+v", plan)
	}
	// Fresh action only.
	p = basePolicy()
	p.OverflowAction = "reconcile"
	p.FreshInstanceAction = "quarantine"
	if plan := mustPlan(t, p, Input{Batch: batch(3), Flags: records.SourceFlags{FreshInstance: true}}); plan.Disposition != "quarantine" {
		t.Fatalf("fresh action quarantine: %+v", plan)
	}
	if plan := mustPlan(t, p, Input{Batch: batch(3), Flags: records.SourceFlags{Overflow: true}}); plan.Disposition != "reconcile" {
		t.Fatalf("overflow action must stay reconcile when only fresh quarantines: %+v", plan)
	}
}

func TestProtectedDeleteQuarantines(t *testing.T) {
	p := basePolicy()
	b := batch(3)
	b.Protected = []string{"raw/secret.md"}
	// A delete of a protected path: precedence 4 still applies.
	for i := range b.Changes {
		b.Changes[i].Operation = records.OpDelete
		b.Changes[i].ExistsAfter = false
	}
	plan := mustPlan(t, p, Input{Batch: b})
	if plan.Disposition != "quarantine" || !hasReason(plan, ReasonProtectedPath) {
		t.Fatalf("protected delete must quarantine: %+v", plan)
	}
}

func TestInvalidPolicyActionsFallBack(t *testing.T) {
	p := basePolicy()
	p.BulkAction = "drop"
	p.OverflowAction = "dispatch"
	plan := mustPlan(t, p, Input{Batch: batch(26)})
	if plan.Disposition != "quarantine" {
		t.Fatalf("invalid bulk action falls back to quarantine: %+v", plan)
	}
	plan = mustPlan(t, p, Input{Batch: batch(3), Flags: records.SourceFlags{Overflow: true}})
	if plan.Disposition != "reconcile" {
		t.Fatalf("invalid overflow action falls back to reconcile: %+v", plan)
	}
}

func TestManifestBoundQuarantines(t *testing.T) {
	p := basePolicy()
	p.MaxManifestBytes = ManifestBytes(nil) // zero only when empty
	b := batch(3)
	p.MaxManifestBytes = ManifestBytes(b.Changes) - 1
	plan := mustPlan(t, p, Input{Batch: b})
	if plan.Disposition != "quarantine" || !hasReason(plan, ReasonManifestOverLimit) {
		t.Fatalf("manifest over limit must quarantine: %+v", plan)
	}
}

func TestPlanJSONValidatesAgainstSchema(t *testing.T) {
	if _, err := os.Stat("../../../docs/schemas"); err != nil {
		t.Skip("docs schemas unavailable")
	}
	compiled, err := schemavalid.CompileSchemas("../../../docs/schemas")
	if err != nil {
		t.Fatal(err)
	}
	schema, ok := compiled["urn:agent-dispatch:schema:dispatch-plan:v1"]
	if !ok {
		t.Fatal("dispatch-plan schema missing")
	}
	for _, in := range []Input{
		{Batch: batch(3)},
		{Batch: batch(0)},
		{Batch: batch(3), Flags: records.SourceFlags{Overflow: true}},
		{Batch: func() *ingest.Result { b := batch(3); b.Protected = []string{"raw/x"}; return b }()},
		{Batch: batch(3), ActiveDispatchExists: true},
	} {
		plan := mustPlan(t, basePolicy(), in)
		raw, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(doc); err != nil {
			t.Fatalf("plan does not validate: %v\n%s", err, raw)
		}
	}
}

func TestDeterministicOutput(t *testing.T) {
	p := basePolicy()
	in := Input{Batch: batch(5), Flags: records.SourceFlags{}}
	a := mustPlan(t, p, in)
	b := mustPlan(t, p, in)
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	if string(ra) != string(rb) {
		t.Fatalf("same input and route snapshot must produce the same plan:\n%s\n%s", ra, rb)
	}
	// A behavior-affecting change produces a different plan identity.
	p2 := basePolicy()
	p2.RouteRevision = "rev-2"
	c := mustPlan(t, p2, in)
	if c.Route.Revision == a.Route.Revision {
		t.Fatal("revision must be carried into the plan")
	}
}

func TestEvaluateRejectsInvalidInputs(t *testing.T) {
	p := basePolicy()
	if _, err := Evaluate(p, Input{}); err == nil {
		t.Fatal("nil batch must fail")
	}
	p.HardLimit = 0
	if _, err := Evaluate(p, Input{Batch: batch(1)}); err == nil {
		t.Fatal("non-positive limits must fail")
	}
}

// TestPlanGolden pins the full JSON shape of a canonical normal plan;
// any change to the plan contract is a deliberate golden update.
func TestPlanGolden(t *testing.T) {
	p := basePolicy()
	b := &ingest.Result{Replacements: map[string]bool{}}
	b.Changes = []records.ChangeItem{{
		Path: "Inbox/new-note.md", Operation: records.OpCreate, ExistsAfter: true,
		FileType: records.FileRegular, DigestStatus: records.DigestKnown,
		AfterDigest: records.SumDigest([]byte("note")),
	}}
	b.Fingerprint = records.SumDigest([]byte("golden"))
	plan, err := Evaluate(p, Input{Batch: b})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/plan-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw)+"\n" != string(want) {
		t.Fatalf("plan golden drifted:\n%s", raw)
	}
}

package records

import (
	"strings"
	"testing"
)

// E12 cold-validation unit coverage (round 1 of the second whole-epic
// validation): the exported fanout helpers the store's union writes and
// batch-column encoding share — the canonical destination derivation,
// the selection ordering, the closed origin vocabulary, and the
// selection-row validation — pinned at their own seam.

func TestCanonicalDestinationsSortsDedupesAndDropsEmpty(t *testing.T) {
	in := []string{"wiki-secondary", "", "wiki-primary", "wiki-secondary", "wiki-primary", ""}
	got := CanonicalDestinations(in)
	if len(got) != 2 || got[0] != "wiki-primary" || got[1] != "wiki-secondary" {
		t.Fatalf("the canonical form is sorted, deduped, and empty-free: %v", got)
	}
	// The derivation returns a fresh slice: mutating it never writes back
	// through the caller's input.
	got[0] = "mutated"
	if in[0] != "wiki-secondary" {
		t.Fatalf("the canonical derivation must not alias its input: %v", in)
	}
	if out := CanonicalDestinations(nil); len(out) != 0 {
		t.Fatalf("no destinations canonically render no list: %v", out)
	}
}

func TestSortSelectionsOrdersByIDThenWorkstream(t *testing.T) {
	selections := []DestinationSelection{
		{DestinationID: "zeta", DestinationRevision: "r", Workstream: "b", Reason: "n"},
		{DestinationID: "alpha", DestinationRevision: "r", Workstream: "z", Reason: "n"},
		{DestinationID: "alpha", DestinationRevision: "r", Workstream: "a", Reason: "n"},
	}
	SortSelections(selections)
	if selections[0].Workstream != "a" || selections[1].Workstream != "z" || selections[2].DestinationID != "zeta" {
		t.Fatalf("selections order by destination ID with the workstream tiebreak: %+v", selections)
	}
}

func TestParseAggregateOriginClosedVocabulary(t *testing.T) {
	for _, valid := range []string{"arrival", "followup", "rerun", "rebuild", "reconcile"} {
		if _, err := ParseAggregateOrigin(valid); err != nil {
			t.Fatalf("the closed origin set accepts %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "created", "arrival ", "Arrival"} {
		if _, err := ParseAggregateOrigin(invalid); err == nil {
			t.Fatalf("an unknown origin must fail closed: %q", invalid)
		}
	}
}

func TestDestinationSelectionValidateFailsClosed(t *testing.T) {
	valid := DestinationSelection{DestinationID: "wiki-primary", DestinationRevision: "dst-1", Workstream: "maintenance", Reason: "fanout_mode:all"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a complete selection validates: %v", err)
	}
	for name, mutate := range map[string]func(*DestinationSelection){
		"missing id":         func(s *DestinationSelection) { s.DestinationID = "" },
		"missing revision":   func(s *DestinationSelection) { s.DestinationRevision = "" },
		"missing workstream": func(s *DestinationSelection) { s.Workstream = "" },
		"missing reason":     func(s *DestinationSelection) { s.Reason = "" },
		"invalid utf8":       func(s *DestinationSelection) { s.Reason = "\xff\xfe" },
	} {
		broken := valid
		mutate(&broken)
		if err := broken.Validate(); err == nil {
			t.Fatalf("an incomplete or non-UTF-8 selection must fail closed (%s)", name)
		} else if !strings.Contains(err.Error(), "destination selection") {
			t.Fatalf("unexpected rejection shape (%s): %v", name, err)
		}
	}
}

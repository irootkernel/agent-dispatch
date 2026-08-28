package dispatch

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// E12-T2 acceptance coverage for the destination-selection evaluator
// (FAN-004/FAN-005): OR within one condition class, AND across present
// classes, absent classes select unconditionally, exclude semantics, and
// fail-closed matcher errors.

// globMatcher is the test matcher: filepath.Match semantics, the same
// delegate the pattern-engine wiring uses for one pattern.
func globMatcher(pattern, path string) (bool, error) {
	return filepath.Match(pattern, path)
}

func TestE12T2SelectDestinationAbsentConditionsSelectUnconditionally(t *testing.T) {
	ctx := SelectionContext{Paths: []string{"Inbox/a.md"}, Classification: "normal", Disposition: "dispatch"}
	for _, conds := range []*DestinationConditionSet{
		nil,
		{},
		{PathInclude: []string{}, PathExclude: nil},
	} {
		selected, reason, err := SelectDestination(ctx, conds, globMatcher)
		if err != nil || !selected || reason != SelectionReasonUnconditional {
			t.Fatalf("absent conditions must select unconditionally: conds=%+v selected=%v reason=%q err=%v", conds, selected, reason, err)
		}
	}
}

func TestE12T2SelectDestinationORWithinOneClass(t *testing.T) {
	ctx := SelectionContext{
		Paths:       []string{"Inbox/a.md"},
		Operations:  []records.Operation{records.OpCreate},
		Disposition: "dispatch",
	}
	// Either include pattern alone suffices (OR within path_include).
	selected, reason, err := SelectDestination(ctx, &DestinationConditionSet{PathInclude: []string{"Logs/**", "Inbox/*.md"}}, globMatcher)
	if err != nil || !selected || reason != SelectionReasonMatched {
		t.Fatalf("one matching value within the class must select: %v %q %v", selected, reason, err)
	}
	// Either operation alone suffices (OR within operations).
	selected, _, err = SelectDestination(ctx, &DestinationConditionSet{Operations: []string{"modify", "create"}}, globMatcher)
	if err != nil || !selected {
		t.Fatalf("one matching operation must select: %v %v", selected, err)
	}
	// No value matches: the first failing class is named.
	selected, reason, err = SelectDestination(ctx, &DestinationConditionSet{Operations: []string{"delete"}}, globMatcher)
	if err != nil || selected || reason != "conditions-not-matched:operations" {
		t.Fatalf("non-matching operations must refuse naming the class: %v %q %v", selected, reason, err)
	}
}

func TestE12T2SelectDestinationANDAcrossClasses(t *testing.T) {
	ctx := SelectionContext{
		Paths:          []string{"Inbox/a.md"},
		Operations:     []records.Operation{records.OpCreate},
		Classification: "normal",
		Disposition:    "dispatch",
	}
	conds := &DestinationConditionSet{
		PathInclude:    []string{"Inbox/**"},
		Operations:     []string{"create"},
		PolicyOutcomes: []string{"dispatch"},
	}
	if selected, _, err := SelectDestination(ctx, conds, globMatcher); err != nil || !selected {
		t.Fatalf("every present class satisfied must select: %v %v", selected, err)
	}
	// One failing class refuses even though the others match (AND across
	// classes); the first failing class in evaluation order is named.
	conds.PolicyOutcomes = []string{"merge_pending"}
	selected, reason, err := SelectDestination(ctx, conds, globMatcher)
	if err != nil || selected || reason != "conditions-not-matched:policy_outcomes" {
		t.Fatalf("a failing class must refuse naming itself: %v %q %v", selected, reason, err)
	}
	// The path class failing is named before later classes.
	conds.PathInclude = []string{"Logs/**"}
	selected, reason, err = SelectDestination(ctx, conds, globMatcher)
	if err != nil || selected || reason != "conditions-not-matched:path_include" {
		t.Fatalf("the first failing class must be named: %v %q %v", selected, reason, err)
	}
}

func TestE12T2SelectDestinationExcludeSemantics(t *testing.T) {
	ctx := SelectionContext{Paths: []string{"Inbox/a.md", "Logs/b.log"}}
	// The class is satisfied when NO path matches ANY exclude pattern.
	selected, _, err := SelectDestination(ctx, &DestinationConditionSet{PathExclude: []string{"Tmp/**"}}, globMatcher)
	if err != nil || !selected {
		t.Fatalf("no excluded path must select: %v %v", selected, err)
	}
	// One matching path excludes the destination.
	selected, reason, err := SelectDestination(ctx, &DestinationConditionSet{PathExclude: []string{"Logs/**"}}, globMatcher)
	if err != nil || selected || reason != "conditions-not-matched:path_exclude" {
		t.Fatalf("an excluded path must refuse: %v %q %v", selected, reason, err)
	}
	// Include and exclude combine with AND: both must hold.
	selected, reason, err = SelectDestination(ctx, &DestinationConditionSet{
		PathInclude: []string{"Inbox/**"}, PathExclude: []string{"Logs/**"},
	}, globMatcher)
	if err != nil || selected || reason != "conditions-not-matched:path_exclude" {
		t.Fatalf("include pass plus exclude fail must refuse on exclude: %v %q %v", selected, reason, err)
	}
}

func TestE12T2SelectDestinationClassificationClass(t *testing.T) {
	ctx := SelectionContext{Paths: []string{"Inbox/a.md"}, Classification: "bulk", Disposition: "dispatch"}
	selected, _, err := SelectDestination(ctx, &DestinationConditionSet{Classifications: []string{"bulk", "protected"}}, globMatcher)
	if err != nil || !selected {
		t.Fatalf("a matching classification must select: %v %v", selected, err)
	}
	selected, reason, err := SelectDestination(ctx, &DestinationConditionSet{Classifications: []string{"normal"}}, globMatcher)
	if err != nil || selected || reason != "conditions-not-matched:classifications" {
		t.Fatalf("a non-matching classification must refuse: %v %q %v", selected, reason, err)
	}
}

func TestE12T2SelectDestinationFailsClosedOnMatcherError(t *testing.T) {
	ctx := SelectionContext{Paths: []string{"Inbox/a.md"}}
	boom := errors.New("matcher unavailable")
	selected, _, err := SelectDestination(ctx, &DestinationConditionSet{PathInclude: []string{"Inbox/**"}}, func(pattern, path string) (bool, error) {
		return false, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("a matcher error must fail closed: %v", err)
	}
	if selected {
		t.Fatal("a matcher error must never select")
	}
	// A missing matcher with present path conditions is a fail-closed
	// defect, never a silent pass.
	if selected, _, err := SelectDestination(ctx, &DestinationConditionSet{PathInclude: []string{"Inbox/**"}}, nil); err == nil || selected {
		t.Fatalf("a nil matcher with path conditions must fail closed: %v %v", selected, err)
	}
}

func TestE12T2SelectDestinationOrderIndependence(t *testing.T) {
	ctx := SelectionContext{Paths: []string{"Inbox/a.md"}, Operations: []records.Operation{records.OpModify}, Classification: "normal", Disposition: "dispatch"}
	conds := &DestinationConditionSet{
		PathInclude:     []string{"Inbox/**"},
		Operations:      []string{"modify", "create"},
		Classifications: []string{"normal"},
		PolicyOutcomes:  []string{"dispatch", "merge_pending"},
	}
	selectedA, reasonA, err := SelectDestination(ctx, conds, globMatcher)
	if err != nil || !selectedA || reasonA != SelectionReasonMatched {
		t.Fatalf("baseline selection: %v %q %v", selectedA, reasonA, err)
	}
	// Permuting the values inside every class keeps the outcome identical
	// (FAN-012: list order is never semantic).
	permuted := &DestinationConditionSet{
		PathInclude:     []string{"Inbox/**"},
		Operations:      []string{"create", "modify"},
		Classifications: []string{"normal"},
		PolicyOutcomes:  []string{"merge_pending", "dispatch"},
	}
	selectedB, reasonB, err := SelectDestination(ctx, permuted, globMatcher)
	if err != nil || selectedB != selectedA || reasonB != reasonA {
		t.Fatalf("class-value order must not change selection: %v %q %v", selectedB, reasonB, err)
	}
	// A present include class with no occurrence paths selects nothing
	// (fail closed on emptiness).
	empty := SelectionContext{Classification: "normal", Disposition: "dispatch"}
	selected, reason, err := SelectDestination(empty, &DestinationConditionSet{PathInclude: []string{"Inbox/**"}}, globMatcher)
	if err != nil || selected || reason != "conditions-not-matched:path_include" {
		t.Fatalf("no paths against a present include must refuse: %v %q %v", selected, reason, err)
	}
}

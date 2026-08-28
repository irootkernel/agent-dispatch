package dispatch

import (
	"fmt"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// Destination selection (E12-T2, FAN-004/FAN-005): the pure evaluator that
// decides which of a route's destinations one structural occurrence
// selects. The evaluator is closed over the four structural classes —
// path, operation, classification, and policy outcome — never content
// semantics, and it fails closed on matcher errors.

// SelectionContext is the closed structural occurrence context destination
// conditions evaluate over (FAN-004: path, operation, classification, and
// policy outcome only — never content semantics).
type SelectionContext struct {
	// Paths are the normalized relative change paths of the occurrence.
	Paths []string
	// Operations are the normalized change operations of the occurrence.
	Operations []records.Operation
	// Classification is the primary classification ("normal", ...).
	Classification string
	// Disposition is the policy outcome ("dispatch", ...).
	Disposition string
}

// DestinationConditionSet mirrors config.Conditions but lives in the app
// layer so the evaluator stays independent of configuration loading.
type DestinationConditionSet struct {
	PathInclude, PathExclude, Operations, Classifications, PolicyOutcomes []string
}

// The closed machine reason strings SelectDestination reports (FAN-010:
// selection evidence is a closed vocabulary, never prose).
const (
	SelectionReasonUnconditional  = "unconditional"
	SelectionReasonMatched        = "conditions-matched"
	selectionReasonNotMatchedSep  = ":"
	selectionClassPathInclude     = "path_include"
	selectionClassPathExclude     = "path_exclude"
	selectionClassOperations      = "operations"
	selectionClassClassifications = "classifications"
	selectionClassPolicyOutcomes  = "policy_outcomes"
)

// SelectDestination evaluates FAN-005 semantics: values within one present
// condition class use OR; present condition classes combine with AND;
// absent condition classes select the destination unconditionally. The
// returned reason is one of the closed strings "unconditional",
// "conditions-matched" (when selected), or
// "conditions-not-matched:<class>" naming the first failing class
// (path_include|path_exclude|operations|classifications|policy_outcomes).
// Path matching delegates to the caller-supplied matcher (the CLI wires the
// existing pattern engine); a matcher error fails closed and selects
// nothing.
func SelectDestination(ctx SelectionContext, conds *DestinationConditionSet, matchPath func(pattern, path string) (bool, error)) (bool, string, error) {
	if conds == nil || (len(conds.PathInclude) == 0 && len(conds.PathExclude) == 0 && len(conds.Operations) == 0 &&
		len(conds.Classifications) == 0 && len(conds.PolicyOutcomes) == 0) {
		return true, SelectionReasonUnconditional, nil
	}
	// path_include: the class is satisfied when ANY occurrence path matches
	// ANY include pattern; with no occurrence paths a present include class
	// selects nothing (fail closed on emptiness, never a silent pass).
	if len(conds.PathInclude) > 0 {
		included, err := anyPathMatches(ctx.Paths, conds.PathInclude, matchPath)
		if err != nil {
			return false, "", err
		}
		if !included {
			return false, selectionNotMatched(selectionClassPathInclude), nil
		}
	}
	// path_exclude: the class is satisfied when NO occurrence path matches
	// ANY exclude pattern.
	if len(conds.PathExclude) > 0 {
		excluded, err := anyPathMatches(ctx.Paths, conds.PathExclude, matchPath)
		if err != nil {
			return false, "", err
		}
		if excluded {
			return false, selectionNotMatched(selectionClassPathExclude), nil
		}
	}
	// operations: OR over the class values against the occurrence's
	// normalized operations.
	if len(conds.Operations) > 0 && !anyStringIn(operationsAsStrings(ctx.Operations), conds.Operations) {
		return false, selectionNotMatched(selectionClassOperations), nil
	}
	if len(conds.Classifications) > 0 && !contains(conds.Classifications, ctx.Classification) {
		return false, selectionNotMatched(selectionClassClassifications), nil
	}
	if len(conds.PolicyOutcomes) > 0 && !contains(conds.PolicyOutcomes, ctx.Disposition) {
		return false, selectionNotMatched(selectionClassPolicyOutcomes), nil
	}
	return true, SelectionReasonMatched, nil
}

// anyPathMatches reports whether any occurrence path matches any of the
// patterns through the caller's matcher; a matcher error fails closed.
func anyPathMatches(paths, patterns []string, matchPath func(pattern, path string) (bool, error)) (bool, error) {
	if matchPath == nil {
		return false, fmt.Errorf("destination path conditions require a path matcher")
	}
	for _, pattern := range patterns {
		for _, path := range paths {
			matched, err := matchPath(pattern, path)
			if err != nil {
				return false, fmt.Errorf("path condition %q: %w", pattern, err)
			}
			if matched {
				return true, nil
			}
		}
	}
	return false, nil
}

// anyStringIn reports whether any candidate equals one of the allowed
// values (OR-within-class over scalar membership).
func anyStringIn(candidates, allowed []string) bool {
	for _, candidate := range candidates {
		if contains(allowed, candidate) {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func operationsAsStrings(ops []records.Operation) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, string(op))
	}
	return out
}

func selectionNotMatched(class string) string {
	return "conditions-not-matched:" + class
}

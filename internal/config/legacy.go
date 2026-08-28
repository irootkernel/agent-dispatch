package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// LegacyShapeError is the actionable refusal for a retired v0.1.4
// configuration shape (OPS-014, D-025): loading never converts or
// previews a migration; the error names the exact regeneration path.
type LegacyShapeError struct {
	// Routes lists route IDs that still declare a `dispatch` block.
	Routes []string
	// KanbanTargets lists target IDs declared as `hermes-kanban` under
	// `targets` instead of `hermes_targets`.
	KanbanTargets []string
}

func (e *LegacyShapeError) Error() string {
	var parts []string
	if len(e.Routes) > 0 {
		parts = append(parts, fmt.Sprintf(
			"routes %s still use the retired v0.1.4 `dispatch` block", strings.Join(e.Routes, ", ")))
	}
	if len(e.KanbanTargets) > 0 {
		parts = append(parts, fmt.Sprintf(
			"targets %s still declare type hermes-kanban under `targets`", strings.Join(e.KanbanTargets, ", ")))
	}
	return fmt.Sprintf("legacy configuration refused: %s. %s", strings.Join(parts, "; "), e.RegenerationPath())
}

// RegenerationPath is the exact operator action that produces a valid
// v0.1.5 configuration. It is the actionable refusal OPS-014 requires:
// no load-time conversion exists.
func (e *LegacyShapeError) RegenerationPath() string {
	return "regenerate the configuration for the v0.1.5 destinations contract: " +
		"either run `agent-dispatch setup wiki` (the interactive walkthrough writes a " +
		"disabled v0.1.5 configuration and validates, probes, and preflights it) " +
		"or `agent-dispatch init` (writes a disabled example you adapt without interaction) " +
		"or edit the file by hand — declare each route's delivery under `destinations[]` " +
		"(unique id, non-empty workstream, and target are required; a hermes destination " +
		"also requires profile and a non-empty skills list, while workspace, mutex_key, " +
		"and execution_hints are optional, as are closed conditions), move the Hermes Kanban target to " +
		"`hermes_targets` with board, executable, minimum_version (default 0.19.1), and " +
		"compatibility: capability_probe, keep webhook targets under `targets`, and move " +
		"submission_retry, latest_state, failure_budget, and active_stale_after to the route. " +
		"See docs/contracts/configuration-spec.md and docs/examples/config.yaml."
}

// detectLegacyShape inspects the decoded YAML node tree for retired
// shapes before strict decoding so the operator receives the actionable
// refusal instead of a bare unknown-field failure. It reports at most
// one route ID and one target ID per class, bounded and sorted.
func detectLegacyShape(root *yaml.Node) *LegacyShapeError {
	if root == nil || root.Kind != yaml.DocumentNode || len(root.Content) != 1 {
		return nil
	}
	top := root.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil
	}
	var routes []string
	var kanban []string
	for i := 0; i+1 < len(top.Content); i += 2 {
		key, value := top.Content[i], top.Content[i+1]
		switch key.Value {
		case "routes":
			for _, entry := range mappingEntries(value) {
				if nodeHasKey(entry.node, "dispatch") {
					routes = append(routes, entry.key)
				}
			}
		case "targets":
			for _, entry := range mappingEntries(value) {
				if typeNode := mappingValue(entry.node, "type"); typeNode != nil && typeNode.Value == "hermes-kanban" {
					kanban = append(kanban, entry.key)
				}
			}
		}
	}
	if len(routes) == 0 && len(kanban) == 0 {
		return nil
	}
	return &LegacyShapeError{Routes: boundIDs(routes), KanbanTargets: boundIDs(kanban)}
}

// boundIDs sorts the IDs and keeps at most the first plus a bounded
// "(n more)" marker so one refusal names a concrete example without
// echoing an unbounded operator document.
func boundIDs(ids []string) []string {
	sortStrings(ids)
	if len(ids) <= 1 {
		return ids
	}
	return []string{ids[0], fmt.Sprintf("(%d more)", len(ids)-1)}
}

// mappingEntries returns the key/value pairs of a mapping node as
// (scalar-key, value-node) pairs; a non-mapping yields nothing.
func mappingEntries(node *yaml.Node) []struct {
	key  string
	node *yaml.Node
} {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	var out []struct {
		key  string
		node *yaml.Node
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		out = append(out, struct {
			key  string
			node *yaml.Node
		}{node.Content[i].Value, node.Content[i+1]})
	}
	return out
}

// nodeHasKey reports whether a mapping node carries the scalar key.
func nodeHasKey(node *yaml.Node, key string) bool {
	for _, entry := range mappingEntries(node) {
		if entry.key == key {
			return true
		}
	}
	return false
}

// mappingValue returns the value node for a scalar key, or nil.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	for _, entry := range mappingEntries(node) {
		if entry.key == key {
			return entry.node
		}
	}
	return nil
}

func sortStrings(in []string) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j] < in[j-1]; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

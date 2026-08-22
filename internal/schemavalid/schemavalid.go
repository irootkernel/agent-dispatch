// Package schemavalid validates the SOT JSON Schemas and example documents
// using a standard Draft 2020-12 validator (D-015). It replaces the former
// standard-library Python subset validator: every schema document is
// compiled (malformed schemas fail closed at compilation), format
// assertions are enabled, and URN-based cross-document $ref resolution is
// provided by registering each schema under its $id.
package schemavalid

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// schemaLessExamples are illustrative examples without a dedicated schema.
// The Watchman trigger payload and environment examples gained dedicated
// schemas in E2-T1 and are now validated like every other example.
var schemaLessExamples = map[string]bool{}

// reportTargets are integration documents validated against one dedicated
// schema $id rather than "matches at least one schema".
var reportTargets = []struct {
	Path   string
	Schema string
}{
	{"integrations/hermes-capability-report.json", "urn:agent-dispatch:schema:hermes-capabilities:v1"},
}

// Failure describes one invalid document or schema.
type Failure struct {
	Path   string
	Detail string
}

func (f Failure) String() string { return f.Path + ": " + f.Detail }

// loadSchemas reads every schema document under dir, keyed by $id. A
// missing or duplicate $id is an error, mirroring the retired Python
// loader.
func loadSchemas(dir string) (map[string]json.RawMessage, []string, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(entries)
	schemas := make(map[string]json.RawMessage, len(entries))
	var ids []string
	for _, path := range entries {
		raw, err := readFile(path)
		if err != nil {
			return nil, nil, err
		}
		var id struct {
			ID string `json:"$id"`
		}
		if err := json.Unmarshal(raw, &id); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		if id.ID == "" {
			return nil, nil, fmt.Errorf("%s: schema document has no $id", path)
		}
		if _, dup := schemas[id.ID]; dup {
			return nil, nil, fmt.Errorf("%s: duplicate schema $id %s", path, id.ID)
		}
		schemas[id.ID] = raw
		ids = append(ids, id.ID)
	}
	return schemas, ids, nil
}

func readFile(path string) (json.RawMessage, error) { return os.ReadFile(path) }

// compileAll registers every schema under its $id and compiles each one.
// Compilation is the fail-closed admission step: invalid patterns,
// malformed subschemas, and unresolvable structure fail here regardless of
// instance reachability.
func compileAll(schemas map[string]json.RawMessage) (map[string]*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	compiler.DefaultDraft(jsonschema.Draft2020)
	for id, raw := range schemas {
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("schema %s: %w", id, err)
		}
		if err := compiler.AddResource(id, doc); err != nil {
			return nil, fmt.Errorf("schema %s: %w", id, err)
		}
	}
	compiled := make(map[string]*jsonschema.Schema, len(schemas))
	for id := range schemas {
		sch, err := compiler.Compile(id)
		if err != nil {
			return nil, fmt.Errorf("schema %s: %w", id, err)
		}
		compiled[id] = sch
	}
	return compiled, nil
}

// Validate validates the SOT package rooted at root (the docs/ directory):
// every schemas/*.json is compiled, every examples/*.json outside the
// schema-less set must match at least one schema, and each integration
// report target is validated against its dedicated schema. It returns the
// per-document outcome lines and any failures.
func Validate(root string) ([]string, []Failure, error) {
	schemas, ids, err := loadSchemas(filepath.Join(root, "schemas"))
	if err != nil {
		return nil, nil, err
	}
	if len(schemas) == 0 {
		return nil, nil, fmt.Errorf("%s: no schema documents found", filepath.Join(root, "schemas"))
	}
	compiled, err := compileAll(schemas)
	if err != nil {
		return nil, nil, err
	}
	var lines []string
	var failures []Failure
	lines = append(lines, fmt.Sprintf("compiled %d schema documents", len(compiled)))

	examples, err := filepath.Glob(filepath.Join(root, "examples", "*.json"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(examples)
	covered := 0
	for _, path := range examples {
		rel, _ := filepath.Rel(root, path)
		if schemaLessExamples[filepath.Base(path)] {
			lines = append(lines, fmt.Sprintf("skip %s (no dedicated schema until E2-T1)", rel))
			continue
		}
		covered++
		var doc any
		raw, err := readFile(path)
		if err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			failures = append(failures, Failure{rel, err.Error()})
			continue
		}
		matched := 0
		for _, id := range ids {
			if err := compiled[id].Validate(doc); err == nil {
				matched++
			}
		}
		switch {
		case matched == 0:
			failures = append(failures, Failure{rel, "matches no schema"})
		default:
			lines = append(lines, fmt.Sprintf("ok   %s (%d schema match(es))", rel, matched))
		}
	}
	if covered == 0 {
		// A missing or emptied examples directory must fail the check, not
		// pass vacuously.
		return nil, nil, fmt.Errorf("%s: no schema-covered example documents found", filepath.Join(root, "examples"))
	}

	for _, target := range reportTargets {
		path := filepath.Join(root, filepath.FromSlash(target.Path))
		raw, err := readFile(path)
		if err != nil {
			// A configured report target that disappears must fail the
			// check, not silently drop out of coverage.
			return nil, nil, fmt.Errorf("report target %s: %w", target.Path, err)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			failures = append(failures, Failure{target.Path, err.Error()})
			continue
		}
		sch := compiled[target.Schema]
		if sch == nil {
			return nil, nil, fmt.Errorf("report target %s references unknown schema %s", target.Path, target.Schema)
		}
		if err := sch.Validate(doc); err != nil {
			failures = append(failures, Failure{target.Path, err.Error()})
			continue
		}
		lines = append(lines, fmt.Sprintf("ok   %s (against %s)", target.Path, target.Schema))
	}

	// The YAML operator configuration example is parsed with duplicate-key
	// detection (yaml.v3 rejects duplicate mapping keys) and validated
	// against the config schema (SCP-006; the reproducible check deferred
	// from E1-T1 to E1-T2).
	configExample := filepath.Join(root, "examples", "config.yaml")
	raw, err := readFile(configExample)
	if err != nil {
		return nil, nil, fmt.Errorf("examples/config.yaml: %w", err)
	}
	var configDoc any
	if err := yaml.Unmarshal(raw, &configDoc); err != nil {
		failures = append(failures, Failure{"examples/config.yaml", err.Error()})
	} else {
		sch := compiled["urn:agent-dispatch:schema:config:v1"]
		if sch == nil {
			return nil, nil, fmt.Errorf("config schema urn:agent-dispatch:schema:config:v1 not found")
		}
		if err := sch.Validate(configDoc); err != nil {
			failures = append(failures, Failure{"examples/config.yaml", err.Error()})
		} else {
			lines = append(lines, "ok   examples/config.yaml (against urn:agent-dispatch:schema:config:v1)")
		}
	}
	return lines, failures, nil
}

// CompileSchemas exposes compilation for tests.
func CompileSchemas(dir string) (map[string]*jsonschema.Schema, error) {
	schemas, _, err := loadSchemas(dir)
	if err != nil {
		return nil, err
	}
	return compileAll(schemas)
}

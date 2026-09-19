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
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// negativeTargets binds fail-closed examples to the one schema they must
// violate. The filename prefix is part of the documentation contract: adding
// an unrecognised negative fixture fails validation instead of silently
// dropping it from coverage.
var negativeTargets = []struct {
	Prefix string
	Schema string
}{
	{"sync-membership-plan-", "urn:agent-dispatch:schema:sync-membership-plan:v1"},
	{"sync-checkpoint-plan-", "urn:agent-dispatch:schema:sync-checkpoint-plan:v1"},
	{"sync-import-acknowledgement-", "urn:agent-dispatch:schema:sync-import-acknowledgement:v1"},
	{"sync-membership-", "urn:agent-dispatch:schema:sync-membership:v1"},
	{"sync-checkpoint-", "urn:agent-dispatch:schema:sync-checkpoint:v1"},
	{"sync-publication-", "urn:agent-dispatch:schema:sync-publication:v1"},
	{"sync-delivery-", "urn:agent-dispatch:schema:sync-delivery:v1"},
	{"sync-import-", "urn:agent-dispatch:schema:sync-import:v1"},
	{"sync-control-", "urn:agent-dispatch:schema:sync-control:v1"},
	{"sync-verification-", "urn:agent-dispatch:schema:sync-verification:v1"},
	{"sync-nudge-", "urn:agent-dispatch:schema:sync-nudge:v1"},
}

var expectedSemanticRejections = map[string]string{
	"sync-import-duplicate-alias.json":            "unique under the resolved case mode",
	"sync-verification-duplicate-node.json":       "pair instance identities must be distinct",
	"sync-verification-obsolete-incarnation.json": "incarnation is obsolete or unexpected",
	"sync-verification-false-freshness.json":      "freshness must be derived",
}

var expectedSchemaRejections = map[string]bool{
	"sync-verification-false-complete.json": true,
}

var syncPositiveTargets = map[string]string{
	"sync-membership.json":             "urn:agent-dispatch:schema:sync-membership:v1",
	"sync-membership-plan.json":        "urn:agent-dispatch:schema:sync-membership-plan:v1",
	"sync-checkpoint.json":             "urn:agent-dispatch:schema:sync-checkpoint:v1",
	"sync-checkpoint-plan.json":        "urn:agent-dispatch:schema:sync-checkpoint-plan:v1",
	"sync-import-acknowledgement.json": "urn:agent-dispatch:schema:sync-import-acknowledgement:v1",
	"sync-publication.json":            "urn:agent-dispatch:schema:sync-publication:v1",
	"sync-delivery.json":               "urn:agent-dispatch:schema:sync-delivery:v1",
	"sync-import.json":                 "urn:agent-dispatch:schema:sync-import:v1",
	"sync-control.json":                "urn:agent-dispatch:schema:sync-control:v1",
	"sync-verification.json":           "urn:agent-dispatch:schema:sync-verification:v1",
	"sync-nudge.json":                  "urn:agent-dispatch:schema:sync-nudge:v1",
	"sync-status-request.json":         "urn:agent-dispatch:schema:sync-status-request:v1",
	"sync-status-response.json":        "urn:agent-dispatch:schema:sync-status-response:v1",
}

var requiredSyncNegativeExamples = []string{
	"sync-membership-third-active.json", "sync-membership-duplicate-instance.json",
	"sync-membership-administrator-key-reused.json", "sync-membership-duplicate-publisher-key.json",
	"sync-membership-public-endpoint.json",
	"sync-membership-plan-stale-predecessor.json",
	"sync-checkpoint-malformed-oid.json", "sync-publication-missing-proof.json",
	"sync-publication-commit-mismatch.json", "sync-publication-unresolved-prunable.json",
	"sync-delivery-unknown-without-retention.json", "sync-membership-invalid-ref.json",
	"sync-import-empty-target.json", "sync-import-unsafe-path.json", "sync-import-duplicate-alias.json", "sync-control-invalid-state.json",
	"sync-nudge-invalid-ref.json", "sync-delivery-contradictory-reason.json", "sync-publication-contradictory-reason.json",
	"sync-verification-obsolete-incarnation.json", "sync-verification-empty-pair.json",
	"sync-verification-false-complete.json", "sync-verification-duplicate-node.json", "sync-verification-false-freshness.json",
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
	if compiled["urn:agent-dispatch:schema:sync-membership:v1"] != nil {
		if err := requireBasenames(filepath.Join(root, "examples"), examples, sortedKeys(syncPositiveTargets)); err != nil {
			return nil, nil, err
		}
	}
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
		base := filepath.Base(path)
		if target := syncPositiveTargets[base]; target != "" {
			sch := compiled[target]
			if sch == nil {
				return nil, nil, fmt.Errorf("positive fixture schema %s not found", target)
			}
			if err := sch.Validate(doc); err == nil {
				matched = 1
			}
		} else if strings.HasPrefix(base, "sync-") {
			return nil, nil, fmt.Errorf("%s: sync positive fixture has no exact schema mapping", rel)
		} else {
			for _, id := range ids {
				if err := compiled[id].Validate(doc); err == nil {
					matched++
				}
			}
		}
		switch {
		case matched == 0:
			failures = append(failures, Failure{rel, "matches no schema"})
		default:
			if err := validateSyncSemantics(doc); err != nil {
				failures = append(failures, Failure{rel, err.Error()})
				continue
			}
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

	negativeDir := filepath.Join(root, "examples", "invalid")
	negative, err := filepath.Glob(filepath.Join(negativeDir, "*.json"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(negative)
	if len(negative) == 0 {
		return nil, nil, fmt.Errorf("%s: no negative example documents found", negativeDir)
	}
	if compiled["urn:agent-dispatch:schema:sync-membership:v1"] != nil {
		if err := requireBasenames(negativeDir, negative, requiredSyncNegativeExamples); err != nil {
			return nil, nil, err
		}
	}
	for _, path := range negative {
		rel, _ := filepath.Rel(root, path)
		base := filepath.Base(path)
		var schemaID string
		for _, target := range negativeTargets {
			if strings.HasPrefix(base, target.Prefix) {
				schemaID = target.Schema
				break
			}
		}
		if schemaID == "" {
			return nil, nil, fmt.Errorf("%s: no negative fixture schema mapping", rel)
		}
		sch := compiled[schemaID]
		if sch == nil {
			return nil, nil, fmt.Errorf("%s: negative fixture references unknown schema %s", rel, schemaID)
		}
		raw, err := readFile(path)
		if err != nil {
			return nil, nil, err
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			failures = append(failures, Failure{rel, err.Error()})
			continue
		}
		schemaErr := sch.Validate(doc)
		semanticErr := validateSyncSemantics(doc)
		if want := expectedSemanticRejections[base]; want != "" {
			if schemaErr != nil || semanticErr == nil || !strings.Contains(semanticErr.Error(), want) {
				failures = append(failures, Failure{rel, fmt.Sprintf("expected semantic rejection %q; schema=%v semantic=%v", want, schemaErr, semanticErr)})
				continue
			}
		}
		if expectedSchemaRejections[base] && schemaErr == nil {
			failures = append(failures, Failure{rel, "expected schema rejection"})
			continue
		}
		if schemaErr == nil && semanticErr == nil {
			failures = append(failures, Failure{rel, "negative fixture unexpectedly validates"})
			continue
		}
		lines = append(lines, fmt.Sprintf("ok   %s (rejected by %s)", rel, schemaID))
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

func validateSyncSemantics(doc any) error {
	m, ok := doc.(map[string]any)
	if !ok {
		return nil
	}
	version, _ := m["schema_version"].(string)
	switch version {
	case "agent-dispatch.sync-membership/v1":
		if err := validateSyncRefs(m); err != nil {
			return err
		}
		return validateSyncMembership(m)
	case "agent-dispatch.sync-membership-plan/v1":
		proposed, _ := m["proposed_membership"].(map[string]any)
		if proposed == nil || !sameJSONScalar(m["group_id"], proposed["group_id"]) || !sameJSONScalar(m["expected_predecessor"], proposed["predecessor"]) || !sameJSONScalar(m["administrator_key"], proposed["administrator_key"]) {
			return fmt.Errorf("membership plan binding mismatch")
		}
		return validateSyncSemantics(proposed)
	case "agent-dispatch.sync-checkpoint-plan/v1":
		proposed, _ := m["proposed_checkpoint"].(map[string]any)
		if proposed == nil || !sameJSONScalar(m["group_id"], proposed["group_id"]) || !sameJSONScalar(m["expected_membership_revision"], proposed["membership_revision"]) || !sameJSONScalar(m["administrator_key"], proposed["administrator_key"]) {
			return fmt.Errorf("checkpoint plan binding mismatch")
		}
	case "agent-dispatch.sync-verification/v1":
		if err := validateSyncRefs(m); err != nil {
			return err
		}
		nodes, _ := m["nodes"].([]any)
		expected, _ := m["expected_nodes"].([]any)
		expectedBindings := map[string]string{}
		for _, raw := range expected {
			node, _ := raw.(map[string]any)
			instance, _ := node["instance_id"].(string)
			incarnation, _ := node["state_incarnation_id"].(string)
			if instance == "" || expectedBindings[instance] != "" {
				return fmt.Errorf("verification expected pair identities must be distinct")
			}
			expectedBindings[instance] = incarnation
		}
		seen := map[string]bool{}
		for _, raw := range nodes {
			node, _ := raw.(map[string]any)
			key, _ := node["instance_id"].(string)
			if key == "" || seen[key] {
				return fmt.Errorf("verification pair instance identities must be distinct")
			}
			incarnation, _ := node["state_incarnation_id"].(string)
			if expectedBindings[key] == "" || expectedBindings[key] != incarnation {
				return fmt.Errorf("verification node incarnation is obsolete or unexpected")
			}
			for _, field := range []string{"membership_revision", "content_ref", "target_commit", "scope_digest", "contract_digest"} {
				if !sameJSONScalar(node[field], m[field]) {
					return fmt.Errorf("verification node %s does not bind the verified target", field)
				}
			}
			if ageRaw, present := node["evidence_age_seconds"]; present {
				age, _ := ageRaw.(float64)
				fresh, _ := node["evidence_fresh"].(bool)
				if fresh != (age <= 300) {
					return fmt.Errorf("verification evidence freshness must be derived from the 300-second age bound")
				}
			}
			seen[key] = true
		}
	case "agent-dispatch.sync-import/v1":
		paths, _ := m["paths"].([]any)
		seenPaths := map[string]bool{}
		for _, raw := range paths {
			entry, _ := raw.(map[string]any)
			path := fmt.Sprint(entry["path"])
			if !safeSyncMarkdownPath(path) {
				return fmt.Errorf("import path is not a safe relative Markdown path")
			}
			canonical := path
			if mode, _ := m["case_mode"].(string); mode == "insensitive" {
				canonical = strings.ToLower(canonical)
			}
			if seenPaths[canonical] {
				return fmt.Errorf("import paths must be unique under the resolved case mode")
			}
			seenPaths[canonical] = true
		}
	case "agent-dispatch.sync-publication/v1":
		if err := validateSyncRefs(m); err != nil {
			return err
		}
		if m["state"] == "published" && !sameJSONScalar(m["candidate_commit"], m["remote_commit"]) {
			return fmt.Errorf("published candidate and remote commits must match")
		}
	case "agent-dispatch.sync-import-acknowledgement/v1":
		return validateSyncRefs(m)
	case "agent-dispatch.sync-nudge/v1", "agent-dispatch.sync-status-request/v1", "agent-dispatch.sync-status-response/v1":
		return validateSyncRefs(m)
	}
	return nil
}

func validateSyncMembership(m map[string]any) error {
	if err := uniqueSyncIdentities(m, "active_members", "historical_members"); err != nil {
		return err
	}
	administrator, _ := m["administrator_key"].(string)
	keys := map[string]bool{administrator: true}
	for _, field := range []string{"active_members", "historical_members"} {
		entries, _ := m[field].([]any)
		for _, raw := range entries {
			entry, _ := raw.(map[string]any)
			key, _ := entry["publisher_key"].(string)
			if key == "" || keys[key] {
				return fmt.Errorf("membership administrator and publisher keys must be distinct")
			}
			keys[key] = true
			if field == "active_members" {
				if err := validateSyncMemberEndpoint(fmt.Sprint(entry["endpoint"])); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateSyncMemberEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" || u.Port() != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || !strings.HasSuffix(strings.ToLower(u.Hostname()), ".ts.net") {
		return fmt.Errorf("membership endpoint must be a credential-free Tailscale HTTPS origin")
	}
	return nil
}

func safeSyncMarkdownPath(raw string) bool {
	if raw == "" || strings.HasPrefix(raw, "/") || strings.Contains(raw, "\\") || strings.Contains(raw, "//") {
		return false
	}
	parts := strings.Split(raw, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, r := range part {
			if r < 0x20 || r == 0x7f {
				return false
			}
		}
	}
	lower := strings.ToLower(raw)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown")
}

func uniqueSyncIdentities(m map[string]any, fields ...string) error {
	seenInstances := map[string]bool{}
	seenIncarnations := map[string]bool{}
	for _, field := range fields {
		entries, _ := m[field].([]any)
		for _, raw := range entries {
			entry, _ := raw.(map[string]any)
			instance, _ := entry["instance_id"].(string)
			incarnation, _ := entry["state_incarnation_id"].(string)
			if instance == "" || seenInstances[instance] || incarnation == "" || seenIncarnations[incarnation] {
				return fmt.Errorf("membership instance and incarnation identities must be unique")
			}
			seenInstances[instance] = true
			seenIncarnations[incarnation] = true
		}
	}
	return nil
}

func sameJSONScalar(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

func validateSyncRefs(m map[string]any) error {
	for _, field := range []string{"content_ref", "membership_ref"} {
		if ref, ok := m[field].(string); ok && !validGitRef(ref) {
			return fmt.Errorf("%s is not a valid configured Git ref", field)
		}
	}
	return nil
}

func validGitRef(ref string) bool {
	if !strings.HasPrefix(ref, "refs/") || strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") || strings.Contains(ref, "//") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.HasSuffix(ref, ".lock") {
		return false
	}
	for _, r := range ref {
		if r <= ' ' || r == 0x7f || strings.ContainsRune("~^:?*[\\", r) {
			return false
		}
	}
	for _, component := range strings.Split(ref, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func requireBasenames(dir string, paths, required []string) error {
	present := make(map[string]bool, len(paths))
	for _, path := range paths {
		present[filepath.Base(path)] = true
	}
	for _, name := range required {
		if !present[name] {
			return fmt.Errorf("%s: required sync fixture %s is missing", dir, name)
		}
	}
	return nil
}

// CompileSchemas exposes compilation for tests.
func CompileSchemas(dir string) (map[string]*jsonschema.Schema, error) {
	schemas, _, err := loadSchemas(dir)
	if err != nil {
		return nil, err
	}
	return compileAll(schemas)
}

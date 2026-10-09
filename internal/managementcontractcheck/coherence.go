package managementcontractcheck

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// checkCoherence compares the declarative authorities with their executable
// schema/parser counterparts, including when a successor rotates the digest.
func checkCoherence(raw map[string][]byte, schemas map[string]*jsonschema.Schema) error {
	docs := map[string]map[string]any{}
	for _, name := range []string{"commands.json", "policy.json", "lifecycle.json", "errors.json", "input.schema.json", "result.schema.json", "envelope.schema.json"} {
		value, err := strictJSON(raw[name])
		if err != nil {
			return err
		}
		doc, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s is not an object", name)
		}
		docs[name] = doc
	}
	resultBranches, ok := docs["result.schema.json"]["oneOf"].([]any)
	if !ok || len(resultBranches) != 2 {
		return fmt.Errorf("missing request/capabilities schema branches")
	}
	request := objectAt(resultBranches[0], "properties")
	capabilities := objectAt(resultBranches[1], "properties")
	limits := objectAt(docs["policy.json"], "limits")
	constLimits := objectAt(capabilities, "limits", "properties")
	if len(limits) == 0 || len(limits) != len(constLimits) {
		return fmt.Errorf("policy/schema limit set differs")
	}
	for key, value := range limits {
		if !reflect.DeepEqual(value, objectAt(constLimits, key)["const"]) {
			return fmt.Errorf("policy/schema limit differs: %s", key)
		}
	}
	if !reflect.DeepEqual(docs["lifecycle.json"]["states"], objectAt(request, "state")["enum"]) {
		return fmt.Errorf("lifecycle/schema state set differs")
	}
	if !reflect.DeepEqual(docs["policy.json"]["actions"], objectAt(docs["input.schema.json"], "properties", "action")["enum"]) {
		return fmt.Errorf("policy/schema action set differs")
	}
	errors, ok := docs["errors.json"]["errors"].([]any)
	if !ok || len(errors) == 0 {
		return fmt.Errorf("missing error registry")
	}
	pairs := map[string]any{}
	for _, entry := range errors {
		e := objectAt(entry)
		code, ok := e["code"].(string)
		if !ok || pairs[code] != nil {
			return fmt.Errorf("invalid error registry pair")
		}
		pairs[code] = e["category"]
		if code == "internal_unclassified" && !reflect.DeepEqual(e["exit_code"], docs["errors.json"]["output_failure_exit"]) {
			return fmt.Errorf("output failure exit differs from internal error")
		}
	}
	branches, ok := objectAt(docs["envelope.schema.json"], "properties", "error")["oneOf"].([]any)
	if !ok || len(branches) != len(pairs) {
		return fmt.Errorf("registry/schema error set differs")
	}
	for _, branch := range branches {
		properties := objectAt(branch, "properties")
		code, ok := objectAt(properties, "code")["const"].(string)
		if !ok || !reflect.DeepEqual(pairs[code], objectAt(properties, "category")["const"]) {
			return fmt.Errorf("registry/schema error pair differs")
		}
		delete(pairs, code)
	}
	commands, ok := docs["commands.json"]["commands"].([]any)
	if !ok || len(commands) != len(phaseSet) {
		return fmt.Errorf("command/parser phase set differs")
	}
	seen := map[string]bool{}
	inputSchema := schemas["urn:agent-dispatch:schema:sync-management-input:v1"]
	for _, rawCommand := range commands {
		command := objectAt(rawCommand)
		path, _ := command["path"].(string)
		phase := strings.TrimPrefix(path, "sync request ")
		if !phaseSet[phase] || seen[phase] {
			return fmt.Errorf("command/parser phase differs: %s", path)
		}
		seen[phase] = true
		flags, err := stringSet(command["flags"])
		if err != nil {
			return err
		}
		base := []string{phase, "--group", "wiki"}
		if phase == "prepare" {
			base = append(base, "--action", "sync-now")
		}
		if phase == "submit" || phase == "show" {
			base = append(base, "--request-id", "mgr_"+strings.Repeat("1", 32))
		}
		validate := func(argv []string) bool {
			input, err := parseArgs(argv)
			return err == nil && inputSchema != nil && inputSchema.Validate(input) == nil
		}
		if !validate(base) {
			return fmt.Errorf("declared command base rejected: %s", path)
		}
		for flag, value := range map[string]string{"--group": "wiki", "--config": "/operator/config.yaml", "--output": "json", "--action": "sync-now", "--request-id": "mgr_" + strings.Repeat("1", 32), "--target-request-id": "mgr_" + strings.Repeat("2", 32)} {
			argv := append([]string{}, base...)
			present := false
			for i := 1; i < len(argv); i += 2 {
				if argv[i] == flag {
					present = true
				}
				if flag == "--target-request-id" && argv[i] == "--action" {
					argv[i+1] = "retry"
				}
			}
			if !present {
				argv = append(argv, flag, value)
			}
			if validate(argv) != flags[flag] {
				return fmt.Errorf("command/parser flag differs: %s %s", phase, flag)
			}
			delete(flags, flag)
		}
		if len(flags) != 0 {
			return fmt.Errorf("declared unknown command flag")
		}
		required, err := stringSet(command["required_flags"])
		if err != nil {
			return err
		}
		for i := 1; i < len(base); i += 2 {
			without := append(append([]string{}, base[:i]...), base[i+2:]...)
			if !validate(without) != required[base[i]] {
				return fmt.Errorf("command/schema required flag differs: %s %s", phase, base[i])
			}
			delete(required, base[i])
		}
		if len(required) != 0 {
			return fmt.Errorf("declared unknown required flag")
		}
	}
	return nil
}

func objectAt(value any, path ...string) map[string]any {
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	object, _ := value.(map[string]any)
	return object
}

func stringSet(value any) (map[string]bool, error) {
	values, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("missing declared string set")
	}
	set := map[string]bool{}
	for _, value := range values {
		s, ok := value.(string)
		if !ok || set[s] {
			return nil, fmt.Errorf("invalid or duplicate declared string")
		}
		set[s] = true
	}
	return set, nil
}

func sortProjection(domain string, value any) error {
	object := objectAt(value)
	var values []any
	switch domain {
	case "agent-dispatch.sync-management.policy/v1":
		values, _ = object["actions"].([]any)
		for _, value := range values {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("invalid action projection")
			}
		}
		sort.Slice(values, func(i, j int) bool { return values[i].(string) < values[j].(string) })
	case "agent-dispatch.sync-management.config/v1":
		values, _ = object["nodes"].([]any)
		for _, value := range values {
			if _, ok := objectAt(value)["instance_id"].(string); !ok {
				return fmt.Errorf("invalid node projection")
			}
		}
		sort.Slice(values, func(i, j int) bool {
			return objectAt(values[i])["instance_id"].(string) < objectAt(values[j])["instance_id"].(string)
		})
	default:
		return fmt.Errorf("unknown sortable projection domain")
	}
	if values == nil {
		return fmt.Errorf("missing projection array")
	}
	return nil
}

package schemavalid

import (
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// checkCompiled compiles schema with optional extra registered resources
// and validates instance against it. Compilation failures are returned so
// callers can assert fail-closed admission.
func checkCompiled(t *testing.T, schema string, resources map[string]string, instance string) error {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	c.DefaultDraft(jsonschema.Draft2020)
	for id, doc := range resources {
		var d any
		if err := json.Unmarshal([]byte(doc), &d); err != nil {
			t.Fatalf("bad test resource %s: %v", id, err)
		}
		if err := c.AddResource(id, d); err != nil {
			t.Fatalf("register resource %s: %v", id, err)
		}
	}
	var s any
	if err := json.Unmarshal([]byte(schema), &s); err != nil {
		t.Fatalf("bad test schema: %v", err)
	}
	if err := c.AddResource("urn:agent-dispatch:selftest:case", s); err != nil {
		t.Fatalf("register case schema: %v", err)
	}
	compiled, err := c.Compile("urn:agent-dispatch:selftest:case")
	if err != nil {
		return err
	}
	var inst any
	if err := json.Unmarshal([]byte(instance), &inst); err != nil {
		t.Fatalf("bad test instance %q: %v", instance, err)
	}
	return compiled.Validate(inst)
}

// TestSelfTestKeywordCases migrates the retired Python validator's keyword
// self-test: every case runs through the standard Draft 2020-12 compiler
// with format assertions enabled, on every invocation of go test.
func TestSelfTestKeywordCases(t *testing.T) {
	cases := []struct {
		name      string
		schema    string
		instance  string
		expectOK  bool
		resources map[string]string
	}{
		{"type-ok", `{"type":"integer"}`, `3`, true, nil},
		{"type-bad", `{"type":"integer"}`, `"3"`, false, nil},
		{"const-ok", `{"const":[1,2]}`, `[1,2]`, true, nil},
		{"const-bad", `{"const":[1,2]}`, `[2,1]`, false, nil},
		{"enum-ok", `{"enum":["a","b"]}`, `"b"`, true, nil},
		{"enum-bad", `{"enum":["a","b"]}`, `"c"`, false, nil},
		{"bool-not-int", `{"type":"integer"}`, `true`, false, nil},
		{"pattern-ok", `{"pattern":"^sha256:[0-9a-f]{8}$"}`, `"sha256:deadbeef"`, true, nil},
		{"pattern-bad", `{"pattern":"^sha256:[0-9a-f]{8}$"}`, `"sha256:DEADBEEF"`, false, nil},
		{"minLength-bad", `{"minLength":3}`, `"ab"`, false, nil},
		{"maxLength-bad", `{"maxLength":3}`, `"abcd"`, false, nil},
		{"minimum-ok", `{"minimum":1}`, `1`, true, nil},
		{"minimum-bad", `{"minimum":1}`, `0`, false, nil},
		{"maximum-bad", `{"maximum":10}`, `11`, false, nil},
		{"exclusiveMinimum-ok", `{"exclusiveMinimum":1}`, `2`, true, nil},
		{"exclusiveMinimum-bad", `{"exclusiveMinimum":1}`, `1`, false, nil},
		{"exclusiveMaximum-ok", `{"exclusiveMaximum":10}`, `9`, true, nil},
		{"exclusiveMaximum-bad", `{"exclusiveMaximum":10}`, `10`, false, nil},
		{"minItems-bad", `{"minItems":2}`, `[1]`, false, nil},
		{"maxItems-bad", `{"maxItems":1}`, `[1,2]`, false, nil},
		{"uniqueItems-ok", `{"uniqueItems":true}`, `[1,2,"1"]`, true, nil},
		{"uniqueItems-bad", `{"uniqueItems":true}`, `[1,1]`, false, nil},
		{"uniqueItems-bool-int", `{"uniqueItems":true}`, `[1,true]`, true, nil},
		{"minProperties-bad", `{"minProperties":2}`, `{"a":1}`, false, nil},
		{"maxProperties-bad", `{"maxProperties":1}`, `{"a":1,"b":2}`, false, nil},
		{"required-bad", `{"required":["a"]}`, `{}`, false, nil},
		{"additional-bad", `{"properties":{"a":{}},"additionalProperties":false}`, `{"a":1,"b":2}`, false, nil},
		{"items-bad", `{"items":{"type":"string"}}`, `["a",1]`, false, nil},
		{"allOf-bad", `{"allOf":[{"minimum":0},{"maximum":5}]}`, `7`, false, nil},
		{"anyOf-ok", `{"anyOf":[{"type":"string"},{"type":"integer"}]}`, `7`, true, nil},
		{"anyOf-bad", `{"anyOf":[{"type":"string"},{"type":"integer"}]}`, `1.5`, false, nil},
		{"oneOf-ok", `{"oneOf":[{"type":"string"},{"type":"integer"}]}`, `"a"`, true, nil},
		{"oneOf-two-bad", `{"oneOf":[{"type":"string"},{"minLength":1}]}`, `"ab"`, false, nil},
		{"not-bad", `{"not":{"type":"null"}}`, `null`, false, nil},
		{"if-then-bad", `{"if":{"properties":{"a":{"const":1}}},"then":{"required":["b"]}}`, `{"a":1}`, false, nil},
		{"if-else-ok", `{"if":{"properties":{"a":{"const":1}}},"else":{"required":["c"]}}`, `{"a":2,"c":3}`, true, nil},
		{"if-else-bad", `{"if":{"properties":{"a":{"const":1}}},"else":{"required":["c"]}}`, `{"a":2}`, false, nil},
		{"format-ok", `{"format":"date-time"}`, `"2026-08-19T21:25:24+09:00"`, true, nil},
		{"format-bad", `{"format":"date-time"}`, `"2026-08-19"`, false, nil},
		{"uri-ok", `{"format":"uri"}`, `"https://example.test/hook"`, true, nil},
		{"uri-no-scheme-bad", `{"format":"uri"}`, `"//example.test/hook"`, false, nil},
		// "https:" is a valid RFC 3986 URI (empty path); the retired subset
		// validator rejected it, the standard one is spec-correct.
		{"uri-empty-path-ok", `{"format":"uri"}`, `"https:"`, true, nil},
		{"uri-space-bad", `{"format":"uri"}`, `"https://exa mple.test"`, false, nil},
		{"const-numeric-equal-ok", `{"const":1}`, `1.0`, true, nil},
		{"const-numeric-equal-bad", `{"const":1}`, `2`, false, nil},
		{"const-bool-not-one-bad", `{"const":1}`, `true`, false, nil},
		{"uniqueItems-numeric-bad", `{"uniqueItems":true}`, `[1,1.0]`, false, nil},
		{"items-false-ok", `{"items":false}`, `[]`, true, nil},
		{"items-false-bad", `{"items":false}`, `[1]`, false, nil},
		{"items-true-ok", `{"items":true}`, `[1,"a",null]`, true, nil},
		{"ref-cross-doc-ok", `{"$ref":"urn:agent-dispatch:selftest:v1#/$defs/pos"}`, `5`, true, refResources},
		{"ref-cross-doc-bad", `{"$ref":"urn:agent-dispatch:selftest:v1#/$defs/pos"}`, `-1`, false, refResources},
		{"ref-sibling-applies-ok", `{"$ref":"urn:agent-dispatch:selftest:v1#/$defs/pos","maximum":10}`, `5`, true, refResources},
		{"ref-sibling-applies-bad", `{"$ref":"urn:agent-dispatch:selftest:v1#/$defs/pos","maximum":10}`, `99`, false, refResources},
		{"deep-numeric-equal-ok", `{"const":[[1,{"x":2}]]}`, `[[1.0,{"x":2.0}]]`, true, nil},
		{"deep-numeric-unequal-bad", `{"const":[[1]]}`, `[[2]]`, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkCompiled(t, tc.schema, tc.resources, tc.instance)
			if tc.expectOK && err != nil {
				t.Errorf("expected valid, got error: %v", err)
			}
			if !tc.expectOK && err == nil {
				t.Errorf("expected invalid, instance was accepted")
			}
		})
	}
}

var refResources = map[string]string{
	"urn:agent-dispatch:selftest:v1": `{
		"$id": "urn:agent-dispatch:selftest:v1",
		"$defs": {"pos": {"minimum": 0}, "small": {"maximum": 10}},
		"type": "object"
	}`,
}

// TestSelfTestStaticAdmission migrates the fail-closed static-admission
// self-test: schemas the standard compiler rejects must fail compilation.
// Boolean subschemas, which the retired subset validator could not enforce,
// are now legal Draft 2020-12 and are asserted as accepted.
func TestSelfTestStaticAdmission(t *testing.T) {
	rejected := []struct {
		name   string
		schema string
	}{
		{"static-invalid-pattern", `{"properties":{"a":{"pattern":"([unclosed"}}}`},
		{"static-items-not-schema", `{"items":[{"type":"string"}]}`},
		{"static-unknown-type-name", `{"type":["string","gizmo"]}`},
		{"static-unresolvable-ref", `{"$ref":"urn:agent-dispatch:selftest:missing:v1"}`},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			err := checkCompiled(t, tc.schema, nil, `null`)
			if err == nil {
				t.Errorf("compilation accepted an invalid schema")
			} else {
				t.Logf("rejected with: %v", err)
			}
		})
	}
	accepted := []struct {
		name     string
		schema   string
		instance string
	}{
		{"static-bool-property-schema", `{"properties":{"a":false}}`, `{}`},
		{"static-bool-combinator-schema", `{"allOf":[true]}`, `{"a":1}`},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkCompiled(t, tc.schema, nil, tc.instance); err != nil {
				t.Errorf("compilation rejected a legal Draft 2020-12 schema: %v", err)
			}
		})
	}
}

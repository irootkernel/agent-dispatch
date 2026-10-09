package managementcontractcheck

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryBundle(t *testing.T) {
	r, err := Check("../../docs/contracts/sync-management-v1")
	if err != nil {
		t.Fatal(err)
	}
	if r.JSONFixtures < 200 || r.ArgvFixtures < 50 || r.DigestVectors < 3 {
		t.Fatalf("missing contract coverage: %+v", r)
	}
}

func TestSchemaCompilerRefusesNonObjects(t *testing.T) {
	for _, raw := range []string{"null", "[]", "42", `"schema"`, "true"} {
		t.Run(raw, func(t *testing.T) {
			_, err := compileSchemaBytes(map[string][]byte{"config.schema.json": []byte(raw)})
			if err == nil || !strings.Contains(err.Error(), "schema root must be an object") {
				t.Fatalf("non-object schema must return a diagnostic: %v", err)
			}
		})
	}
}

func TestToolPropagatesBundleFailure(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "managementcontractcheck")
	if output, err := exec.Command("go", "build", "-o", binary, "../tools/managementcontractcheck").CombinedOutput(); err != nil {
		t.Fatalf("build checker tool: %v\n%s", err, output)
	}
	if output, err := exec.Command(binary, "-dir", "../../docs/contracts/sync-management-v1").CombinedOutput(); err != nil || !bytes.Contains(output, []byte(ContractDigest)) {
		t.Fatalf("valid bundle tool result: %v\n%s", err, output)
	}
	output, err := exec.Command(binary, "-dir", t.TempDir()).CombinedOutput()
	status, ok := err.(*exec.ExitError)
	if !ok || status.ExitCode() != 1 || !bytes.Contains(output, []byte("unexpected or missing artifacts")) {
		t.Fatalf("invalid bundle must fail the executable gate with exit 1: %v\n%s", err, output)
	}
}

func TestBundleRejectsMutation(t *testing.T) {
	for _, mutation := range []string{"bytes", "checksum", "extra", "missing", "symlink", "resigned-bytes", "bundle-id", "bundle-artifacts", "self-reported-digest"} {
		t.Run(mutation, func(t *testing.T) {
			dir := t.TempDir()
			entries, err := os.ReadDir("../../docs/contracts/sync-management-v1")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				raw, err := os.ReadFile(filepath.Join("../../docs/contracts/sync-management-v1", entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, entry.Name()), raw, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(dir, "commands.json")
			switch mutation {
			case "bytes":
				err = os.WriteFile(path, []byte("{}\n"), 0o644)
			case "checksum":
				err = os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte("wrong\n"), 0o644)
			case "extra":
				err = os.WriteFile(filepath.Join(dir, "extra.json"), []byte("{}"), 0o644)
			case "missing":
				err = os.Remove(path)
			case "symlink":
				if err = os.Remove(path); err == nil {
					err = os.Symlink("policy.json", path)
				}
			case "resigned-bytes":
				err = os.WriteFile(path, []byte("{}\n"), 0o644)
			case "bundle-id", "bundle-artifacts", "self-reported-digest":
				raw, readErr := os.ReadFile(filepath.Join(dir, "bundle.json"))
				if readErr != nil {
					t.Fatal(readErr)
				}
				var b bundle
				if err := json.Unmarshal(raw, &b); err != nil {
					t.Fatal(err)
				}
				if mutation == "bundle-id" {
					b.BundleID = "unreviewed-bundle"
				} else if mutation == "bundle-artifacts" {
					b.Artifacts[0], b.Artifacts[1] = b.Artifacts[1], b.Artifacts[0]
				} else {
					b.ContractDigest = "sha256:" + strings.Repeat("b", 64)
				}
				raw, err = json.Marshal(b)
				if err == nil {
					err = os.WriteFile(filepath.Join(dir, "bundle.json"), raw, 0o644)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if mutation == "resigned-bytes" || strings.HasPrefix(mutation, "bundle-") || mutation == "self-reported-digest" {
				var sums strings.Builder
				for _, name := range append([]string{"bundle.json"}, artifacts...) {
					raw, err := os.ReadFile(filepath.Join(dir, name))
					if err != nil {
						t.Fatal(err)
					}
					fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(raw), name)
				}
				if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, checkErr := Check(dir)
			if checkErr == nil {
				t.Fatalf("accepted %s mutation", mutation)
			}
			if mutation == "resigned-bytes" && !strings.Contains(checkErr.Error(), "contract digest mismatch") {
				t.Fatalf("coordinated artifact tamper reached wrong guard: %v", checkErr)
			}
			if (strings.HasPrefix(mutation, "bundle-") || mutation == "self-reported-digest") && !strings.Contains(checkErr.Error(), "bundle identity") {
				t.Fatalf("coordinated identity tamper reached wrong guard: %v", checkErr)
			}
		})
	}
}

func TestRawJSONRejectsAmbiguousInputs(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"action":"pause","action":"resume"}`),
		[]byte(`{"a":[{"id":1,"id":2}]}`),
		[]byte(`{"action":"pause","\u0061ction":"resume"}`),
		[]byte(`{} {}`), []byte(`{"broken":`), []byte{'"', 0xff, '"'},
		[]byte(strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65)),
	} {
		if _, err := strictJSON(raw); err == nil {
			t.Fatalf("accepted ambiguous JSON: %q", raw)
		}
	}
	if _, err := strictJSON([]byte(`{"a":[{"id":1},{"id":2}]}`)); err != nil {
		t.Fatalf("rejected keys in distinct objects: %v", err)
	}
	if _, err := strictJSON([]byte(strings.Repeat("[", 64) + "0" + strings.Repeat("]", 64))); err != nil {
		t.Fatalf("rejected exact container-depth boundary: %v", err)
	}
}

func TestResultByteCeiling(t *testing.T) {
	// An otherwise valid JSON string distinguishes the exact byte ceiling from
	// malformed input, without storing a megabyte-scale fixture in Git.
	raw := append([]byte{'"'}, bytes.Repeat([]byte{'a'}, 1048574)...)
	raw = append(raw, '"')
	if _, err := strictJSON(raw); err != nil {
		t.Fatal(err)
	}
	raw = append(raw[:len(raw)-1], 'a', '"')
	if _, err := strictJSON(raw); err == nil {
		t.Fatal("accepted a result over the byte ceiling")
	}
}

func TestAssociationCeiling(t *testing.T) {
	dir := "../../docs/contracts/sync-management-v1"
	schemas, err := compileSchemas(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Fixtures []struct {
			Name string `json:"name"`
			JSON string `json:"json"`
		} `json:"fixtures"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	for _, f := range doc.Fixtures {
		if f.Name == "lookup-state-running" {
			value, err := strictJSON([]byte(f.JSON))
			if err != nil {
				t.Fatal(err)
			}
			result = value.(map[string]any)
		}
	}
	if result == nil {
		t.Fatal("missing running fixture")
	}
	jobs := make([]any, 1001)
	for i := range jobs {
		jobs[i] = map[string]any{
			"job_id": fmt.Sprintf("job-%d", i), "kind": "import",
			"association_reason": "observed_reconciliation", "target_commit": strings.Repeat("f", 40),
			"attempts": 20, "state": "imported",
		}
	}
	result["jobs"] = jobs[:1000]
	schema := schemas["urn:agent-dispatch:schema:sync-management-result:v1"]
	if err := schema.Validate(result); err != nil {
		t.Fatalf("rejected exact association ceiling: %v", err)
	}
	result["jobs"] = jobs
	if err := schema.Validate(result); err == nil {
		t.Fatal("accepted more than 1000 associations")
	}
}

func TestArgvRefusesAuthorityAndGrammarChanges(t *testing.T) {
	for _, argv := range [][]string{
		{"submit", "--group", "wiki", "--confirmed", "true"},
		{"show", "--group", "wiki", "--group=other"},
		{"capabilities", "--config", ""},
		{"capabilities", "--group", "wiki", "--output", "human"},
		{"capabilities", "--group", "wiki", "--state-dir", "/other"},
		{"capabilities", "--group", "wiki", "--"},
		{}, {"serve"}, {"prepare", "--group", "--action", "pause"},
	} {
		if _, err := parseArgs(argv); err == nil {
			t.Fatalf("accepted argv %q", argv)
		}
	}
	for _, control := range append([]rune{127, 128, 133, 159}, []rune("\x00\x01\x02\x03\x04\x05\x06\x07\x08\t\n\v\f\r\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f")...) {
		if _, err := parseArgs([]string{"capabilities", "--group", "wiki", "--config", "/operator/" + string(control)}); err == nil {
			t.Fatalf("accepted config control character U+%04X", control)
		}
	}
}

func TestTypedDocumentAndTraceabilityRefusals(t *testing.T) {
	raw, err := os.ReadFile("../../docs/contracts/sync-management-v1/traceability.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"missing", "order", "unknown-field"} {
		t.Run(mutation, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			requirements := doc["requirements"].([]any)
			switch mutation {
			case "missing":
				doc["requirements"] = requirements[:14]
			case "order":
				requirements[0], requirements[1] = requirements[1], requirements[0]
			case "unknown-field":
				doc["unreviewed"] = true
			}
			changed, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkTraceabilityBytes(changed); err == nil {
				t.Fatalf("accepted malformed traceability: %s", mutation)
			}
		})
	}
}

func TestCoherenceRejectsAuthorityDrift(t *testing.T) {
	for _, mutation := range []string{"limit", "error-category", "state", "action", "flag", "required-flag"} {
		t.Run(mutation, func(t *testing.T) {
			captured := map[string][]byte{}
			for _, name := range artifacts {
				raw, err := os.ReadFile(filepath.Join("../../docs/contracts/sync-management-v1", name))
				if err != nil {
					t.Fatal(err)
				}
				captured[name] = raw
			}
			name := "policy.json"
			if mutation == "error-category" {
				name = "errors.json"
			} else if mutation == "state" {
				name = "lifecycle.json"
			} else if mutation == "flag" || mutation == "required-flag" {
				name = "commands.json"
			}
			value, err := strictJSON(captured[name])
			if err != nil {
				t.Fatal(err)
			}
			doc := objectAt(value)
			switch mutation {
			case "limit":
				objectAt(doc, "limits")["call_deadline_seconds"] = 6
			case "error-category":
				objectAt(doc["errors"].([]any)[0])["category"] = "internal"
			case "state":
				doc["states"].([]any)[0] = "unreviewed"
			case "action":
				doc["actions"].([]any)[0] = "publish"
			case "flag":
				command := objectAt(doc["commands"].([]any)[0])
				command["flags"] = append(command["flags"].([]any), "--force")
			case "required-flag":
				objectAt(doc["commands"].([]any)[0])["required_flags"] = []any{}
			}
			captured[name], err = json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			schemas, err := compileSchemaBytes(captured)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkCoherence(captured, schemas); err == nil {
				t.Fatalf("accepted authority drift: %s", mutation)
			}
		})
	}
}

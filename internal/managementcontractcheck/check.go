// Package managementcontractcheck validates the E23 management contract without
// registering runtime commands or opening operator state.
package managementcontractcheck

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const bundleDomain = "agent-dispatch.sync-management.bundle/v1"

// ContractDigest freezes the reviewed v1 artifact bytes independently of the
// base sync provider. A successor contract needs its own reviewed identity.
const ContractDigest = "sha256:77dba7fac82cf1e627ad29ab89057c18d4d5c2c380251112579e551d1122cf39"

var artifacts = []string{
	"commands.json", "policy.json", "lifecycle.json", "errors.json",
	"config.schema.json", "input.schema.json", "result.schema.json", "envelope.schema.json",
	"fixtures.json", "argv-fixtures.json", "traceability.json", "digest-vectors.json",
}

type bundle struct {
	SchemaVersion  string   `json:"schema_version"`
	BundleID       string   `json:"bundle_id"`
	ContractDigest string   `json:"contract_digest"`
	Artifacts      []string `json:"artifacts"`
}

// Report counts the fixtures actually checked, including expected refusals.
type Report struct {
	ContractDigest string
	JSONFixtures   int
	ArgvFixtures   int
	DigestVectors  int
}

// Check verifies the closed artifact inventory, checksums, independently framed
// digest, compiled schemas, raw JSON fixtures, argv, and requirement coverage.
func Check(dir string) (Report, error) {
	var report Report
	var b bundle
	paths := append([]string{"bundle.json"}, artifacts...)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return report, err
	}
	if len(entries) != len(paths)+1 {
		return report, fmt.Errorf("bundle has unexpected or missing artifacts")
	}
	var sums strings.Builder
	captured := make(map[string][]byte, len(paths))
	h := sha256.New()
	_, _ = h.Write([]byte(bundleDomain + "\x00"))
	for i, path := range paths {
		info, err := os.Lstat(filepath.Join(dir, path))
		if err != nil || !info.Mode().IsRegular() {
			return report, fmt.Errorf("%s must be a regular artifact", path)
		}
		raw, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			return report, err
		}
		sum := sha256.Sum256(raw)
		captured[path] = raw
		fmt.Fprintf(&sums, "%x  %s\n", sum, path)
		if i > 0 {
			_ = binary.Write(h, binary.BigEndian, uint64(len(path)))
			_, _ = h.Write([]byte(path))
			_ = binary.Write(h, binary.BigEndian, uint64(len(raw)))
			_, _ = h.Write(raw)
		}
	}
	readCaptured := func(name string, target any) error {
		return decodeDocument(captured[name], name, target)
	}
	if err := readCaptured("bundle.json", &b); err != nil {
		return report, err
	}
	if b.SchemaVersion != "agent-dispatch.sync-management-bundle/v1" ||
		b.BundleID != "agent-dispatch-sync-management-v1" || b.ContractDigest != ContractDigest || !reflect.DeepEqual(b.Artifacts, artifacts) {
		return report, fmt.Errorf("bundle identity or ordered artifact set differs from management v1")
	}
	actualDigest := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if actualDigest != b.ContractDigest {
		return report, fmt.Errorf("management contract digest mismatch: got %s", actualDigest)
	}
	checksumInfo, err := os.Lstat(filepath.Join(dir, "SHA256SUMS"))
	if err != nil || !checksumInfo.Mode().IsRegular() {
		return report, fmt.Errorf("SHA256SUMS must be a regular artifact")
	}
	checksumBytes, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil || string(checksumBytes) != sums.String() {
		return report, fmt.Errorf("management SHA256SUMS mismatch")
	}
	schemas, err := compileSchemaBytes(captured)
	if err != nil {
		return report, err
	}
	var errorDoc struct {
		SchemaVersion string `json:"schema_version"`
		Errors        []struct {
			Code     string `json:"code"`
			Category string `json:"category"`
			ExitCode int    `json:"exit_code"`
		} `json:"errors"`
		LookupExit int    `json:"lookup_and_accepted_submit_exit"`
		Distinct   bool   `json:"lookup_success_does_not_prove_action_success"`
		Stdout     string `json:"stdout"`
		Stderr     string `json:"stderr"`
		OutputExit int    `json:"output_failure_exit"`
		Loss       string `json:"response_loss"`
	}
	if err := readCaptured("errors.json", &errorDoc); err != nil {
		return report, err
	}
	exits := make(map[string]int, len(errorDoc.Errors))
	for _, e := range errorDoc.Errors {
		if _, duplicate := exits[e.Code]; duplicate || e.Code == "" || e.ExitCode == 0 {
			return report, fmt.Errorf("invalid or duplicate error registration")
		}
		exits[e.Code] = e.ExitCode
	}
	var fixtureDoc struct {
		SchemaVersion string `json:"schema_version"`
		Fixtures      []struct {
			Name     string `json:"name"`
			Schema   string `json:"schema"`
			Valid    bool   `json:"valid"`
			ExitCode *int   `json:"exit_code,omitempty"`
			JSON     string `json:"json"`
		} `json:"fixtures"`
	}
	if err := readCaptured("fixtures.json", &fixtureDoc); err != nil {
		return report, err
	}
	names := map[string]bool{}
	for _, f := range fixtureDoc.Fixtures {
		if f.Name == "" || names[f.Name] || schemas[f.Schema] == nil {
			return report, fmt.Errorf("invalid fixture identity: %s", f.Name)
		}
		names[f.Name] = true
		isEnvelope := strings.HasSuffix(f.Schema, "envelope:v1")
		if isEnvelope != (f.ExitCode != nil) {
			return report, fmt.Errorf("fixture %s: exit_code belongs only to envelopes", f.Name)
		}
		value, validationErr := strictJSON([]byte(f.JSON))
		if validationErr == nil {
			validationErr = schemas[f.Schema].Validate(value)
		}
		if validationErr == nil {
			validationErr = checkByteLimits(value)
		}
		if validationErr == nil && isEnvelope {
			validationErr = checkExit(value, *f.ExitCode, exits)
		}
		if (validationErr == nil) != f.Valid {
			return report, fmt.Errorf("fixture %s: expected valid=%v; validation=%v", f.Name, f.Valid, validationErr)
		}
		report.JSONFixtures++
	}
	var argvDoc struct {
		SchemaVersion string `json:"schema_version"`
		Fixtures      []struct {
			Name  string   `json:"name"`
			Argv  []string `json:"argv"`
			Valid bool     `json:"valid"`
		} `json:"fixtures"`
	}
	if err := readCaptured("argv-fixtures.json", &argvDoc); err != nil {
		return report, err
	}
	for _, f := range argvDoc.Fixtures {
		if f.Name == "" || names[f.Name] {
			return report, fmt.Errorf("invalid argv fixture identity: %s", f.Name)
		}
		names[f.Name] = true
		input, validationErr := parseArgs(f.Argv)
		if validationErr == nil {
			validationErr = schemas["urn:agent-dispatch:schema:sync-management-input:v1"].Validate(input)
		}
		if (validationErr == nil) != f.Valid {
			return report, fmt.Errorf("argv fixture %s: expected valid=%v; validation=%v", f.Name, f.Valid, validationErr)
		}
		report.ArgvFixtures++
	}
	if err := checkTraceabilityBytes(captured["traceability.json"]); err != nil {
		return report, err
	}
	if err := checkCoherence(captured, schemas); err != nil {
		return report, err
	}
	var vectorDoc struct {
		SchemaVersion string `json:"schema_version"`
		Vectors       []struct {
			Domain    string          `json:"domain"`
			Input     json.RawMessage `json:"input,omitempty"`
			Value     json.RawMessage `json:"value"`
			Canonical string          `json:"canonical"`
			Digest    string          `json:"digest"`
		} `json:"vectors"`
	}
	if err := readCaptured("digest-vectors.json", &vectorDoc); err != nil {
		return report, err
	}
	for _, v := range vectorDoc.Vectors {
		value, err := strictJSON(v.Value)
		if err != nil {
			return report, err
		}
		if len(v.Input) != 0 {
			input, err := strictJSON(v.Input)
			if err != nil {
				return report, err
			}
			if err := sortProjection(v.Domain, input); err != nil {
				return report, err
			}
			if !reflect.DeepEqual(input, value) {
				return report, fmt.Errorf("digest projection mismatch for %s", v.Domain)
			}
		}
		var canonical bytes.Buffer
		encoder := json.NewEncoder(&canonical)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(value); err != nil {
			return report, err
		}
		encoded := bytes.TrimSuffix(canonical.Bytes(), []byte{'\n'})
		sum := sha256.Sum256(append([]byte(v.Domain+"\x00"), encoded...))
		if string(encoded) != v.Canonical || "sha256:"+hex.EncodeToString(sum[:]) != v.Digest {
			return report, fmt.Errorf("digest vector mismatch for %s", v.Domain)
		}
		report.DigestVectors++
	}
	if report.JSONFixtures == 0 || report.ArgvFixtures == 0 || report.DigestVectors == 0 {
		return report, fmt.Errorf("empty fixture or digest vector set")
	}
	report.ContractDigest = actualDigest
	return report, nil
}

func decodeDocument(raw []byte, name string, target any) error {
	if _, err := strictJSON(raw); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func compileSchemas(dir string) (map[string]*jsonschema.Schema, error) {
	captured := map[string][]byte{}
	for _, path := range artifacts {
		if strings.HasSuffix(path, ".schema.json") {
			raw, err := os.ReadFile(filepath.Join(dir, path))
			if err != nil {
				return nil, err
			}
			captured[path] = raw
		}
	}
	return compileSchemaBytes(captured)
}

func compileSchemaBytes(captured map[string][]byte) (map[string]*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	c.DefaultDraft(jsonschema.Draft2020)
	var ids []string
	for _, path := range artifacts {
		if !strings.HasSuffix(path, ".schema.json") {
			continue
		}
		doc, err := strictJSON(captured[path])
		if err != nil {
			return nil, err
		}
		object, ok := doc.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: schema root must be an object", path)
		}
		id, ok := object["$id"].(string)
		if !ok || id == "" {
			return nil, fmt.Errorf("%s: missing schema identity", path)
		}
		if err := c.AddResource(id, doc); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	compiled := map[string]*jsonschema.Schema{}
	for _, id := range ids {
		s, err := c.Compile(id)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		compiled[id] = s
	}
	return compiled, nil
}

func strictJSON(raw []byte) (any, error) {
	if len(raw) > 1048576 || !utf8.Valid(raw) {
		return nil, fmt.Errorf("JSON exceeds byte bound or is not UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := scanValue(d, 0); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("JSON has trailing data")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func scanValue(d *json.Decoder, depth int) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if depth >= 64 {
		return fmt.Errorf("JSON nesting exceeds 64 containers")
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return fmt.Errorf("duplicate or invalid JSON key %v", key)
			}
			keys[name] = true
			if err := scanValue(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := scanValue(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	end, err := d.Token()
	if err != nil || (delim == '{' && end != json.Delim('}')) || (delim == '[' && end != json.Delim(']')) {
		return fmt.Errorf("invalid JSON closing delimiter")
	}
	return nil
}

func checkByteLimits(value any) error {
	switch v := value.(type) {
	case map[string]any:
		if jobs, ok := v["jobs"].([]any); ok {
			ids := map[string]bool{}
			for _, raw := range jobs {
				job, ok := raw.(map[string]any)
				if !ok {
					return fmt.Errorf("invalid job association")
				}
				id, ok := job["job_id"].(string)
				if !ok || ids[id] {
					return fmt.Errorf("invalid or duplicate job association ID")
				}
				ids[id] = true
			}
		}
		for k, child := range v {
			if s, ok := child.(string); ok && (k == "reason" || k == "message" || k == "remediation") && len(s) > 256 {
				return fmt.Errorf("%s exceeds 256 UTF-8 bytes", k)
			}
			if err := checkByteLimits(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := checkByteLimits(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkExit(value any, exit int, exits map[string]int) error {
	e := value.(map[string]any)
	if e["ok"] == true {
		if exit != 0 {
			return fmt.Errorf("successful envelope requires exit 0")
		}
		return nil
	}
	body := e["error"].(map[string]any)
	code := body["code"].(string)
	if expected, ok := exits[code]; !ok || exit != expected {
		return fmt.Errorf("exit %d does not match error %s", exit, code)
	}
	return nil
}

var phaseSet = map[string]bool{"capabilities": true, "prepare": true, "submit": true, "show": true}

func parseArgs(argv []string) (map[string]any, error) {
	if len(argv) == 0 || !phaseSet[argv[0]] {
		return nil, fmt.Errorf("unknown command phase")
	}
	input := map[string]any{"phase": argv[0]}
	seen := map[string]bool{}
	for i := 1; i < len(argv); i++ {
		flag, value, equals := strings.Cut(argv[i], "=")
		if !equals {
			if i+1 >= len(argv) {
				return nil, fmt.Errorf("missing value for %s", flag)
			}
			i++
			value = argv[i]
		}
		if seen[flag] {
			return nil, fmt.Errorf("missing value or duplicate flag %s", flag)
		}
		seen[flag] = true
		if value == "" || strings.HasPrefix(value, "--") || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("invalid value for %s", flag)
		}
		switch flag {
		case "--group":
			input["group_id"] = value
		case "--action":
			input["action"] = value
		case "--target-request-id":
			input["target_request_id"] = value
		case "--request-id":
			input["request_id"] = value
		case "--config":
		case "--output":
			if value != "json" {
				return nil, fmt.Errorf("only JSON output is admitted")
			}
		default:
			return nil, fmt.Errorf("unknown flag %s", flag)
		}
	}
	return input, nil
}

func checkTraceabilityBytes(raw []byte) error {
	var doc struct {
		SchemaVersion string `json:"schema_version"`
		Requirements  []struct {
			ID    string   `json:"id"`
			Tasks []string `json:"tasks"`
			G19   []string `json:"g19"`
		} `json:"requirements"`
	}
	if err := decodeDocument(raw, "traceability.json", &doc); err != nil {
		return err
	}
	if len(doc.Requirements) != 15 {
		return fmt.Errorf("traceability must cover all 15 SMR requirements")
	}
	task := regexp.MustCompile(`^E23-T[1-7]$`)
	gate := regexp.MustCompile(`^AC-19(?:0[1-9]|1[0-2])$`)
	for i, r := range doc.Requirements {
		if r.ID != fmt.Sprintf("SMR-%03d", i+1) || len(r.Tasks) == 0 || len(r.G19) == 0 {
			return fmt.Errorf("missing or unordered SMR coverage")
		}
		for _, t := range r.Tasks {
			if !task.MatchString(t) {
				return fmt.Errorf("invalid task owner %s", t)
			}
		}
		for _, g := range r.G19 {
			if !gate.MatchString(g) {
				return fmt.Errorf("invalid G19 scenario %s", g)
			}
		}
	}
	return nil
}

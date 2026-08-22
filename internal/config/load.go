package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// Load reads, parses, schema-validates, and semantically validates the
// configuration at path. YAML duplicate keys are rejected by the decoder
// and unknown fields fail closed via KnownFields.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes configuration bytes. It applies, in order: strict YAML
// decoding with duplicate-key rejection and fail-closed unknown fields,
// JSON Schema validation against the SOT config schema, and semantic
// validation.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	if err := SchemaValidate(&cfg); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	errs, warnings := SemanticValidate(&cfg)
	if len(errs) > 0 {
		return nil, fmt.Errorf("semantic: %s", joinErrors(errs))
	}
	cfg.Warnings = warnings
	return &cfg, nil
}

func joinErrors(errs []error) string {
	var b bytes.Buffer
	for i, e := range errs {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(e.Error())
	}
	return b.String()
}

// schemaCompiler compiles the embedded SOT config schema. The embedded
// copy is guarded against drift from docs/schemas by TestSchemaDrift.
func schemaCompiler() (*jsonschema.Schema, error) {
	var doc any
	if err := json.Unmarshal(schemaJSON, &doc); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(configSchemaID, doc); err != nil {
		return nil, err
	}
	return c.Compile(configSchemaID)
}

const configSchemaID = "urn:agent-dispatch:schema:config:v1"

// SchemaValidate validates the typed configuration against the SOT config
// JSON Schema by round-tripping through canonical JSON.
func SchemaValidate(cfg *Config) error {
	raw, err := toRaw(cfg)
	if err != nil {
		return err
	}
	sch, err := schemaCompiler()
	if err != nil {
		return err
	}
	if err := sch.Validate(raw); err != nil {
		return fmt.Errorf("%w", err)
	}
	return nil
}

// toRaw converts the typed config into generic JSON-compatible values so
// the JSON Schema validator sees the same document a YAML-to-JSON
// conversion would produce.
func toRaw(cfg *Config) (any, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ResolvePath applies the configuration precedence (configuration-spec
// §1): an explicit path wins, then AGENT_DISPATCH_CONFIG, then the platform
// default. It returns the resolved path and whether it exists.
func ResolvePath(explicit string, defaultPath func() string) (string, bool) {
	if explicit != "" {
		return explicit, fileExists(explicit)
	}
	if env := os.Getenv("AGENT_DISPATCH_CONFIG"); env != "" {
		return env, fileExists(env)
	}
	p := defaultPath()
	return p, fileExists(p)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

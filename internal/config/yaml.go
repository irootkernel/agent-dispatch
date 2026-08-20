package config

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// MarshalYAML renders a configuration as YAML with sorted map keys so the
// output is deterministic. Secret references appear as references only.
func MarshalYAML(cfg *Config) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
